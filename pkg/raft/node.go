package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config holds node configuration.
type Config struct {
	ID      NodeID
	DataDir string
	Peers   []string // Initial peer addresses

	// Timing (with sensible defaults)
	HeartbeatInterval time.Duration
	ElectionTimeout   time.Duration
	ApplyInterval     time.Duration

	// Batching (for throughput)
	BatchInterval  time.Duration // How often to flush batched proposals (default 5ms)
	BatchThreshold int           // Max proposals before immediate flush (default 100)
}

func (c *Config) defaults() {
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 50 * time.Millisecond
	}
	if c.ElectionTimeout == 0 {
		c.ElectionTimeout = 150 * time.Millisecond
	}
	if c.ApplyInterval == 0 {
		c.ApplyInterval = 10 * time.Millisecond
	}
	if c.BatchInterval == 0 {
		c.BatchInterval = 5 * time.Millisecond
	}
	if c.BatchThreshold == 0 {
		c.BatchThreshold = 100
	}
}

// peerTracker tracks replication state for a single peer.
type peerTracker struct {
	lastContact time.Time // Last successful RPC
	failedRPCs  int       // Consecutive failed RPCs
}

// Node is a Raft cluster node.
type Node struct {
	mu sync.RWMutex

	config Config
	state  NodeState

	// Persistent state
	currentTerm Term
	votedFor    NodeID
	log         *Log

	// Cluster membership configuration (used for quorum calculations)
	// This is separate from FSM.Nodes which tracks node metadata.
	// clusterConfig only changes through committed membership changes.
	clusterConfig *ClusterConfig

	// Volatile state
	commitIndex Index
	lastApplied Index

	// Leader state
	leaderState *LeaderState
	leaderID    NodeID
	peerState   map[NodeID]*peerTracker // Per-peer replication tracking

	// State machine
	fsm *ClusterState

	// Transport
	transport Transport

	// Channels
	applyCh   chan struct{}
	shutdownC chan struct{}
	wg        sync.WaitGroup

	// Timers
	electionTimer  *time.Timer
	heartbeatTimer *time.Ticker

	// Leadership transfer state
	transferTarget NodeID        // Target node for leadership transfer (empty if not transferring)
	transferDone   chan struct{} // Closed when transfer completes

	// Entry batching
	proposalQueue  chan *proposalRequest // Buffered channel for batching proposals
	batchTicker    *time.Ticker          // Flushes batch periodically
	batchThreshold int                   // Max entries before immediate flush

	// Callbacks
	onLeaderChange func(NodeID)
}

// proposalRequest represents a pending proposal
type proposalRequest struct {
	entryType LogEntryType
	command   any
	result    chan proposalResult
}

type proposalResult struct {
	index Index
	term  Term
	err   error
}

// Transport is the interface for node-to-node communication.
type Transport interface {
	// PreVote sends a PreVote RPC (pre-election check).
	PreVote(ctx context.Context, addr string, args *PreVoteArgs) (*PreVoteReply, error)

	// RequestVote sends a RequestVote RPC.
	RequestVote(ctx context.Context, addr string, args *RequestVoteArgs) (*RequestVoteReply, error)

	// AppendEntries sends an AppendEntries RPC.
	AppendEntries(ctx context.Context, addr string, args *AppendEntriesArgs) (*AppendEntriesReply, error)

	// InstallSnapshot sends an InstallSnapshot RPC for follower catch-up.
	InstallSnapshot(ctx context.Context, addr string, args *InstallSnapshotArgs) (*InstallSnapshotReply, error)

	// TimeoutNow sends a TimeoutNow RPC for leadership transfer.
	TimeoutNow(ctx context.Context, addr string, args *TimeoutNowArgs) (*TimeoutNowReply, error)

	// Address returns this node's address.
	Address() string
}

// NewNode creates a new Raft node.
func NewNode(cfg Config, transport Transport) (*Node, error) {
	cfg.defaults()

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	log, err := NewLog(filepath.Join(cfg.DataDir, "log"))
	if err != nil {
		return nil, fmt.Errorf("create log: %w", err)
	}

	n := &Node{
		config:         cfg,
		state:          Follower,
		log:            log,
		fsm:            NewClusterState(),
		transport:      transport,
		applyCh:        make(chan struct{}, 1),
		shutdownC:      make(chan struct{}),
		proposalQueue:  make(chan *proposalRequest, 1000),
		batchThreshold: cfg.BatchThreshold,
		clusterConfig: &ClusterConfig{
			Nodes: make(map[NodeID]*NodeInfo),
		},
	}

	// Load persistent state
	if err := n.loadPersistentState(); err != nil {
		return nil, fmt.Errorf("load persistent state: %w", err)
	}

	// Load FSM snapshot
	if err := n.fsm.LoadSnapshot(filepath.Join(cfg.DataDir, "snapshots")); err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	return n, nil
}

// Start begins the Raft node's main loop.
func (n *Node) Start() {
	n.batchTicker = time.NewTicker(n.config.BatchInterval)
	n.wg.Add(3)
	go n.runLoop()
	go n.applyLoop()
	go n.batchLoop()
}

// Stop gracefully shuts down the node.
func (n *Node) Stop() error {
	close(n.shutdownC)
	n.wg.Wait()
	return n.log.Close()
}

// GracefulStop performs a graceful shutdown with optional leadership transfer.
// If transferLeader is true and this node is the leader, it will attempt to
// transfer leadership to another node before shutting down.
// drainTimeout is the time to wait for in-flight operations to complete.
func (n *Node) GracefulStop(transferLeader bool, drainTimeout time.Duration) error {
	// Step 1: Transfer leadership if we're the leader
	if transferLeader && n.IsLeader() {
		// Find the best target (the one most caught up)
		target := n.findBestTransferTarget()
		if target != "" {
			// Best-effort transfer, don't fail if it doesn't work
			if err := n.TransferLeadership(target, drainTimeout/2); err != nil {
				fmt.Printf("raft: leadership transfer failed (continuing shutdown): %v\n", err)
			}
		}
	}

	// Step 2: Step down if we're still the leader
	n.mu.Lock()
	wasLeader := n.state == Leader
	if wasLeader {
		n.state = Follower
		n.leaderState = nil
		if n.heartbeatTimer != nil {
			n.heartbeatTimer.Stop()
		}
	}
	n.mu.Unlock()

	// Step 3: Signal shutdown and wait for goroutines
	close(n.shutdownC)

	// Step 4: Wait with drain timeout
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown
	case <-time.After(drainTimeout):
		fmt.Printf("raft: drain timeout exceeded, forcing shutdown\n")
	}

	// Step 5: Close log
	return n.log.Close()
}

// findBestTransferTarget finds the node most caught up with our log.
func (n *Node) findBestTransferTarget() NodeID {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if n.leaderState == nil {
		return ""
	}

	var bestTarget NodeID
	var bestMatch Index

	for id, matchIdx := range n.leaderState.MatchIndex {
		if matchIdx > bestMatch {
			bestMatch = matchIdx
			bestTarget = id
		}
	}

	return bestTarget
}

// runLoop is the main Raft loop handling elections and heartbeats.
func (n *Node) runLoop() {
	defer n.wg.Done()

	n.resetElectionTimer()

	for {
		select {
		case <-n.shutdownC:
			if n.electionTimer != nil {
				n.electionTimer.Stop()
			}
			if n.heartbeatTimer != nil {
				n.heartbeatTimer.Stop()
			}
			return

		case <-n.electionTimer.C:
			n.startElection()
		}
	}
}

// applyLoop applies committed entries to the FSM.
func (n *Node) applyLoop() {
	defer n.wg.Done()

	ticker := time.NewTicker(n.config.ApplyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.shutdownC:
			return
		case <-ticker.C:
			n.applyCommitted()
		case <-n.applyCh:
			n.applyCommitted()
		}
	}
}

// batchLoop processes batched proposals for improved throughput.
func (n *Node) batchLoop() {
	defer n.wg.Done()
	defer n.batchTicker.Stop()

	batch := make([]*proposalRequest, 0, n.batchThreshold)

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}

		n.processBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-n.shutdownC:
			// Flush remaining and reject with shutdown error
			for _, req := range batch {
				req.result <- proposalResult{err: fmt.Errorf("node shutting down")}
			}
			return

		case <-n.batchTicker.C:
			flushBatch()

		case req := <-n.proposalQueue:
			batch = append(batch, req)
			// Immediate flush if threshold reached
			if len(batch) >= n.batchThreshold {
				flushBatch()
			}
		}
	}
}

// processBatch processes a batch of proposals together.
func (n *Node) processBatch(batch []*proposalRequest) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if we're leader
	if n.state != Leader {
		for _, req := range batch {
			req.result <- proposalResult{err: fmt.Errorf("not leader (leader is %s)", n.leaderID)}
		}
		return
	}

	// Append all entries to log
	entries := make([]LogEntry, 0, len(batch))
	for _, req := range batch {
		var cmdBytes []byte
		var err error
		if req.command != nil {
			cmdBytes, err = MarshalCommand(req.command)
			if err != nil {
				req.result <- proposalResult{err: fmt.Errorf("marshal command: %w", err)}
				continue
			}
		}

		entry := LogEntry{
			Index:   n.log.LastIndex() + 1,
			Term:    n.currentTerm,
			Type:    req.entryType,
			Command: cmdBytes,
		}

		if err := n.log.Append(entry); err != nil {
			req.result <- proposalResult{err: fmt.Errorf("append to log: %w", err)}
			continue
		}

		entries = append(entries, entry)
		req.result <- proposalResult{index: entry.Index, term: entry.Term}
	}

	if len(entries) == 0 {
		return
	}

	// For single-node cluster, commit immediately
	if n.clusterSize() <= 1 {
		n.commitIndex = entries[len(entries)-1].Index
		select {
		case n.applyCh <- struct{}{}:
		default:
		}
	} else {
		// Trigger replication for multi-node cluster
		go n.sendHeartbeats()
	}
}

// DefaultCompactionThreshold is the number of log entries after which compaction triggers.
const DefaultCompactionThreshold = 1000

// applyCommitted applies all committed but unapplied entries.
func (n *Node) applyCommitted() {
	n.mu.Lock()
	commitIdx := n.commitIndex
	lastApplied := n.lastApplied
	n.mu.Unlock()

	if lastApplied >= commitIdx {
		return
	}

	entries := n.log.GetRange(lastApplied+1, commitIdx)
	for _, entry := range entries {
		if err := n.fsm.Apply(&entry); err != nil {
			// Log error but continue
			fmt.Printf("raft: failed to apply entry %d: %v\n", entry.Index, err)
		}

		// Update cluster config for membership changes
		n.applyMembershipChange(&entry)
	}

	n.mu.Lock()
	n.lastApplied = commitIdx
	n.mu.Unlock()

	// Check if log compaction is needed
	n.maybeCompactLog()
}

// maybeCompactLog triggers log compaction if the log is too large.
func (n *Node) maybeCompactLog() {
	if !n.log.ShouldCompact(DefaultCompactionThreshold) {
		return
	}

	n.mu.RLock()
	lastApplied := n.lastApplied
	n.mu.RUnlock()

	if lastApplied == 0 {
		return
	}

	// Get the term at lastApplied for the snapshot
	lastAppliedTerm := n.log.TermAt(lastApplied)

	// Save snapshot before compacting
	snapshotDir := filepath.Join(n.config.DataDir, "snapshots")
	if err := n.fsm.SaveSnapshot(snapshotDir); err != nil {
		fmt.Printf("raft: failed to save snapshot: %v\n", err)
		return
	}

	// Compact the log
	if err := n.log.Compact(lastApplied, lastAppliedTerm); err != nil {
		fmt.Printf("raft: failed to compact log: %v\n", err)
		return
	}

	fmt.Printf("raft: compacted log up to index %d\n", lastApplied)
}

// applyMembershipChange updates cluster config when membership entries are committed.
func (n *Node) applyMembershipChange(entry *LogEntry) {
	switch entry.Type {
	case LogNodeJoin:
		var cmd NodeCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return
		}
		n.AddToClusterConfig(cmd.NodeID, cmd.Address)
	case LogNodeLeave:
		var cmd NodeCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return
		}
		n.RemoveFromClusterConfig(cmd.NodeID)
	}
}

// resetElectionTimer resets the election timeout with randomization.
func (n *Node) resetElectionTimer() {
	timeout := n.config.ElectionTimeout + time.Duration(rand.Int63n(int64(n.config.ElectionTimeout)))

	if n.electionTimer == nil {
		n.electionTimer = time.NewTimer(timeout)
	} else {
		if !n.electionTimer.Stop() {
			select {
			case <-n.electionTimer.C:
			default:
			}
		}
		n.electionTimer.Reset(timeout)
	}
}

// startElection transitions to candidate and starts an election.
// Uses pre-vote to prevent partitioned nodes from disrupting stable clusters.
func (n *Node) startElection() {
	n.mu.RLock()
	currentTerm := n.currentTerm
	lastLogIndex := n.log.LastIndex()
	lastLogTerm := n.log.LastTerm()
	n.mu.RUnlock()

	// If we're the only node, become leader immediately
	if n.clusterSize() <= 1 {
		n.mu.Lock()
		n.state = Candidate
		n.currentTerm++
		n.votedFor = n.config.ID
		n.mu.Unlock()
		n.savePersistentState()
		n.becomeLeader()
		return
	}

	// Phase 1: Pre-vote (doesn't increment term or change state)
	// This prevents partitioned nodes from disrupting stable clusters
	if !n.runPreVote(currentTerm+1, lastLogIndex, lastLogTerm) {
		// Pre-vote failed - would not win election
		n.resetElectionTimer()
		return
	}

	// Phase 2: Real election (we got pre-vote quorum)
	n.mu.Lock()
	n.state = Candidate
	n.currentTerm++
	n.votedFor = n.config.ID
	currentTerm = n.currentTerm
	n.mu.Unlock()

	if err := n.savePersistentState(); err != nil {
		fmt.Printf("raft: failed to save state: %v\n", err)
	}

	// Vote for self
	votes := 1
	needed := n.quorumSize()

	// Request votes from peers
	args := &RequestVoteArgs{
		Term:         currentTerm,
		CandidateID:  n.config.ID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	var voteMu sync.Mutex
	var wg sync.WaitGroup

	// Use cluster config for peer list (with FSM fallback for backward compat)
	peers := n.ClusterNodes()
	if len(peers) == 0 {
		peers = n.fsm.ListNodes()
	}
	for _, peer := range peers {
		if peer.ID == n.config.ID {
			continue
		}

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			reply, err := n.transport.RequestVote(ctx, addr, args)
			if err != nil {
				return
			}

			n.mu.Lock()
			if reply.Term > n.currentTerm {
				n.currentTerm = reply.Term
				n.state = Follower
				n.votedFor = ""
				n.mu.Unlock()
				n.savePersistentState()
				return
			}
			n.mu.Unlock()

			if reply.VoteGranted {
				voteMu.Lock()
				votes++
				voteMu.Unlock()
			}
		}(peer.Address)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}

	voteMu.Lock()
	gotVotes := votes
	voteMu.Unlock()

	n.mu.Lock()
	// Check if still candidate and won
	if n.state == Candidate && n.currentTerm == currentTerm && gotVotes >= needed {
		n.mu.Unlock()
		n.becomeLeader()
		return
	}
	n.mu.Unlock()

	// Lost or timed out, reset election timer
	n.resetElectionTimer()
}

// runPreVote sends PreVote RPCs to all peers and returns true if we got quorum.
// PreVote doesn't change any state - it just checks if we would win an election.
func (n *Node) runPreVote(proposedTerm Term, lastLogIndex Index, lastLogTerm Term) bool {
	args := &PreVoteArgs{
		Term:         proposedTerm,
		CandidateID:  n.config.ID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	votes := 1 // Count ourselves
	needed := n.quorumSize()

	var voteMu sync.Mutex
	var wg sync.WaitGroup

	peers := n.ClusterNodes()
	if len(peers) == 0 {
		peers = n.fsm.ListNodes()
	}

	for _, peer := range peers {
		if peer.ID == n.config.ID {
			continue
		}

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			reply, err := n.transport.PreVote(ctx, addr, args)
			if err != nil {
				return
			}

			// PreVote doesn't update our term (that's the point)
			// We only count votes
			if reply.VoteGranted {
				voteMu.Lock()
				votes++
				voteMu.Unlock()
			}
		}(peer.Address)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}

	voteMu.Lock()
	gotVotes := votes
	voteMu.Unlock()

	return gotVotes >= needed
}

// becomeLeader transitions to leader state.
func (n *Node) becomeLeader() {
	n.mu.Lock()
	n.state = Leader
	n.leaderID = n.config.ID
	lastIdx := n.log.LastIndex()

	// Initialize leader state
	n.leaderState = &LeaderState{
		NextIndex:  make(map[NodeID]Index),
		MatchIndex: make(map[NodeID]Index),
	}
	n.peerState = make(map[NodeID]*peerTracker)
	// Use cluster config for peer list. If clusterConfig is empty,
	// this is a single-node cluster and we don't need to track peers.
	if n.clusterConfig != nil {
		for id := range n.clusterConfig.Nodes {
			// Don't track ourselves
			if id != n.config.ID {
				n.leaderState.NextIndex[id] = lastIdx + 1
				n.leaderState.MatchIndex[id] = 0
				n.peerState[id] = &peerTracker{
					lastContact: time.Now(),
					failedRPCs:  0,
				}
			}
		}
	}
	n.mu.Unlock()

	// Notify callback
	if n.onLeaderChange != nil {
		n.onLeaderChange(n.config.ID)
	}

	// Start heartbeat
	n.startHeartbeat()

	// Append a no-op to establish leadership
	n.Propose(LogNoop, nil)
}

// startHeartbeat starts sending periodic heartbeats.
func (n *Node) startHeartbeat() {
	if n.heartbeatTimer != nil {
		n.heartbeatTimer.Stop()
	}
	n.heartbeatTimer = time.NewTicker(n.config.HeartbeatInterval)

	go func() {
		for {
			select {
			case <-n.shutdownC:
				return
			case <-n.heartbeatTimer.C:
				n.mu.RLock()
				if n.state != Leader {
					n.mu.RUnlock()
					return
				}
				n.mu.RUnlock()
				n.sendHeartbeats()
			}
		}
	}()
}

// sendHeartbeats sends AppendEntries to all peers.
func (n *Node) sendHeartbeats() {
	n.mu.RLock()
	if n.state != Leader {
		n.mu.RUnlock()
		return
	}
	currentTerm := n.currentTerm
	commitIndex := n.commitIndex
	n.mu.RUnlock()

	// Use cluster config for peer list (with FSM fallback for backward compat)
	peers := n.ClusterNodes()
	if len(peers) == 0 {
		peers = n.fsm.ListNodes()
	}
	for _, peer := range peers {
		if peer.ID == n.config.ID {
			continue
		}

		go n.replicateTo(peer.ID, peer.Address, currentTerm, commitIndex)
	}
}

// replicateTo sends log entries to a single peer.
func (n *Node) replicateTo(peerID NodeID, addr string, term Term, leaderCommit Index) {
	n.mu.RLock()
	if n.leaderState == nil {
		n.mu.RUnlock()
		return
	}
	nextIdx := n.leaderState.NextIndex[peerID]
	n.mu.RUnlock()

	// If the next needed entry has been compacted, send snapshot instead
	if nextIdx < n.log.FirstIndex() {
		n.sendSnapshot(peerID, addr, term)
		return
	}

	prevLogIndex := nextIdx - 1
	prevLogTerm := n.log.TermAt(prevLogIndex)

	// Get entries to send
	entries := n.log.GetRange(nextIdx, n.log.LastIndex())

	args := &AppendEntriesArgs{
		Term:         term,
		LeaderID:     n.config.ID,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: leaderCommit,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	reply, err := n.transport.AppendEntries(ctx, addr, args)
	if err != nil {
		// Track RPC failure
		n.mu.Lock()
		if n.peerState != nil && n.peerState[peerID] != nil {
			n.peerState[peerID].failedRPCs++
		}
		n.mu.Unlock()
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Track RPC success
	if n.peerState != nil && n.peerState[peerID] != nil {
		n.peerState[peerID].lastContact = time.Now()
		n.peerState[peerID].failedRPCs = 0
	}

	if reply.Term > n.currentTerm {
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = ""
		n.leaderState = nil
		n.peerState = nil
		n.savePersistentState()
		n.resetElectionTimer()
		return
	}

	if n.state != Leader || n.leaderState == nil {
		return
	}

	if reply.Success {
		if len(entries) > 0 {
			n.leaderState.NextIndex[peerID] = entries[len(entries)-1].Index + 1
			n.leaderState.MatchIndex[peerID] = entries[len(entries)-1].Index
		}
		n.updateCommitIndex()
	} else {
		// Log conflict, back up
		if reply.ConflictTerm > 0 {
			// Find last entry with conflict term
			found := false
			for i := n.log.LastIndex(); i >= 1; i-- {
				if n.log.TermAt(i) == reply.ConflictTerm {
					n.leaderState.NextIndex[peerID] = i + 1
					found = true
					break
				}
			}
			if !found {
				n.leaderState.NextIndex[peerID] = reply.ConflictIndex
			}
		} else {
			n.leaderState.NextIndex[peerID] = reply.ConflictIndex
		}
	}
}

// sendSnapshot sends a snapshot to a follower that's too far behind.
func (n *Node) sendSnapshot(peerID NodeID, addr string, term Term) {
	// Get current snapshot
	snapshot, err := n.fsm.Snapshot()
	if err != nil {
		fmt.Printf("raft: failed to create snapshot: %v\n", err)
		return
	}

	// Get last applied index from FSM
	n.mu.RLock()
	lastApplied := n.lastApplied
	lastTerm := n.currentTerm // Approximation - in production would store term with snapshot
	clusterCfg := n.clusterConfig
	n.mu.RUnlock()

	args := &InstallSnapshotArgs{
		Term:              term,
		LeaderID:          n.config.ID,
		LastIncludedIndex: lastApplied,
		LastIncludedTerm:  lastTerm,
		Data:              snapshot,
		ClusterConfig:     clusterCfg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	reply, err := n.transport.InstallSnapshot(ctx, addr, args)
	if err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if reply.Term > n.currentTerm {
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = ""
		n.leaderState = nil
		n.savePersistentState()
		n.resetElectionTimer()
		return
	}

	if n.state != Leader || n.leaderState == nil {
		return
	}

	// Update nextIndex to continue from after the snapshot
	n.leaderState.NextIndex[peerID] = lastApplied + 1
	n.leaderState.MatchIndex[peerID] = lastApplied

	fmt.Printf("raft: sent snapshot to %s, next index now %d\n", peerID, lastApplied+1)
}

// updateCommitIndex updates the commit index based on matchIndex.
func (n *Node) updateCommitIndex() {
	// Must be called with lock held

	if n.leaderState == nil {
		return
	}

	// Find the highest index replicated to a majority
	for idx := n.log.LastIndex(); idx > n.commitIndex; idx-- {
		if n.log.TermAt(idx) != n.currentTerm {
			continue // Only commit entries from current term
		}

		count := 1 // Self
		for _, matchIdx := range n.leaderState.MatchIndex {
			if matchIdx >= idx {
				count++
			}
		}

		if count >= n.quorumSize() {
			n.commitIndex = idx
			// Signal apply goroutine
			select {
			case n.applyCh <- struct{}{}:
			default:
			}
			break
		}
	}
}

// HandlePreVote processes a PreVote RPC.
// PreVote doesn't change our state or term - it just tells the candidate
// whether we would vote for them if they started a real election.
func (n *Node) HandlePreVote(args *PreVoteArgs) *PreVoteReply {
	n.mu.RLock()
	defer n.mu.RUnlock()

	reply := &PreVoteReply{
		Term:        n.currentTerm,
		VoteGranted: false,
	}

	// If the candidate's proposed term is less than ours, reject
	if args.Term < n.currentTerm {
		return reply
	}

	// Only grant pre-vote if:
	// 1. We don't have a leader (or haven't heard from them recently)
	// 2. The candidate's log is at least as up-to-date as ours
	//
	// Note: We don't check votedFor because pre-vote doesn't consume our vote.
	// We also don't reset election timer - pre-vote is just an inquiry.
	if n.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		reply.VoteGranted = true
	}

	return reply
}

// HandleRequestVote processes a RequestVote RPC.
func (n *Node) HandleRequestVote(args *RequestVoteArgs) *RequestVoteReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := &RequestVoteReply{
		Term:        n.currentTerm,
		VoteGranted: false,
	}

	if args.Term < n.currentTerm {
		return reply
	}

	if args.Term > n.currentTerm {
		n.currentTerm = args.Term
		n.state = Follower
		n.votedFor = ""
	}

	reply.Term = n.currentTerm

	// Check if we can vote for this candidate
	if (n.votedFor == "" || n.votedFor == args.CandidateID) &&
		n.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		n.votedFor = args.CandidateID
		reply.VoteGranted = true
		n.resetElectionTimer()
	}

	n.savePersistentState()
	return reply
}

// isLogUpToDate checks if candidate's log is at least as up-to-date as ours.
func (n *Node) isLogUpToDate(lastIndex Index, lastTerm Term) bool {
	ourLastTerm := n.log.LastTerm()
	ourLastIndex := n.log.LastIndex()

	if lastTerm != ourLastTerm {
		return lastTerm > ourLastTerm
	}
	return lastIndex >= ourLastIndex
}

// HandleAppendEntries processes an AppendEntries RPC.
func (n *Node) HandleAppendEntries(args *AppendEntriesArgs) *AppendEntriesReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := &AppendEntriesReply{
		Term:    n.currentTerm,
		Success: false,
	}

	if args.Term < n.currentTerm {
		return reply
	}

	if args.Term > n.currentTerm {
		n.currentTerm = args.Term
		n.votedFor = ""
	}

	n.state = Follower
	n.leaderID = args.LeaderID
	n.resetElectionTimer()

	reply.Term = n.currentTerm

	// Check log consistency
	if args.PrevLogIndex > 0 {
		if args.PrevLogIndex > n.log.LastIndex() {
			reply.ConflictIndex = n.log.LastIndex() + 1
			return reply
		}

		if n.log.TermAt(args.PrevLogIndex) != args.PrevLogTerm {
			reply.ConflictTerm = n.log.TermAt(args.PrevLogIndex)
			// Find first index with this term
			for i := args.PrevLogIndex - 1; i >= 1; i-- {
				if n.log.TermAt(i) != reply.ConflictTerm {
					reply.ConflictIndex = i + 1
					break
				}
			}
			return reply
		}
	}

	// Append new entries
	if len(args.Entries) > 0 {
		// Check for conflicts and truncate
		for i, entry := range args.Entries {
			if entry.Index <= n.log.LastIndex() {
				if n.log.TermAt(entry.Index) != entry.Term {
					n.log.TruncateAfter(entry.Index - 1)
					n.log.Append(args.Entries[i:]...)
					break
				}
			} else {
				n.log.Append(args.Entries[i:]...)
				break
			}
		}
	}

	// Update commit index
	if args.LeaderCommit > n.commitIndex {
		lastNew := n.log.LastIndex()
		if args.LeaderCommit < lastNew {
			n.commitIndex = args.LeaderCommit
		} else {
			n.commitIndex = lastNew
		}
		// Signal apply goroutine
		select {
		case n.applyCh <- struct{}{}:
		default:
		}
	}

	reply.Success = true
	n.savePersistentState()
	return reply
}

// HandleInstallSnapshot processes an InstallSnapshot RPC from the leader.
// Used when this follower is too far behind to catch up via log replication.
func (n *Node) HandleInstallSnapshot(args *InstallSnapshotArgs) *InstallSnapshotReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := &InstallSnapshotReply{
		Term: n.currentTerm,
	}

	if args.Term < n.currentTerm {
		return reply
	}

	if args.Term > n.currentTerm {
		n.currentTerm = args.Term
		n.votedFor = ""
	}

	n.state = Follower
	n.leaderID = args.LeaderID
	n.resetElectionTimer()

	reply.Term = n.currentTerm

	// Restore FSM from snapshot
	if err := n.fsm.Restore(args.Data); err != nil {
		fmt.Printf("raft: failed to restore snapshot: %v\n", err)
		return reply
	}

	// Update cluster config from snapshot
	if args.ClusterConfig != nil {
		n.clusterConfig = args.ClusterConfig
	}

	// Truncate log and set indices
	n.log.TruncateAll()
	n.commitIndex = args.LastIncludedIndex
	n.lastApplied = args.LastIncludedIndex

	n.savePersistentState()

	fmt.Printf("raft: installed snapshot up to index %d term %d\n", args.LastIncludedIndex, args.LastIncludedTerm)
	return reply
}

// HandleTimeoutNow processes a TimeoutNow RPC for leadership transfer.
// When received, the node should immediately start an election (bypassing pre-vote).
func (n *Node) HandleTimeoutNow(args *TimeoutNowArgs) *TimeoutNowReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := &TimeoutNowReply{
		Term:    n.currentTerm,
		Success: false,
	}

	// Only accept from current leader
	if args.Term < n.currentTerm {
		return reply
	}

	if args.LeaderID != n.leaderID {
		return reply
	}

	// Start immediate election (bypass pre-vote since leader initiated this)
	reply.Success = true
	reply.Term = n.currentTerm

	// Trigger election in background (after releasing lock)
	go n.startImmediateElection()

	return reply
}

// startImmediateElection starts an election immediately, bypassing pre-vote.
// Used for leadership transfer where the leader explicitly asked us to take over.
func (n *Node) startImmediateElection() {
	n.mu.Lock()
	n.state = Candidate
	n.currentTerm++
	n.votedFor = n.config.ID
	currentTerm := n.currentTerm
	lastLogIndex := n.log.LastIndex()
	lastLogTerm := n.log.LastTerm()
	n.mu.Unlock()

	if err := n.savePersistentState(); err != nil {
		fmt.Printf("raft: failed to save state: %v\n", err)
	}

	votes := 1
	needed := n.quorumSize()

	args := &RequestVoteArgs{
		Term:         currentTerm,
		CandidateID:  n.config.ID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	var voteMu sync.Mutex
	var wg sync.WaitGroup

	peers := n.ClusterNodes()
	if len(peers) == 0 {
		peers = n.fsm.ListNodes()
	}
	for _, peer := range peers {
		if peer.ID == n.config.ID {
			continue
		}

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			reply, err := n.transport.RequestVote(ctx, addr, args)
			if err != nil {
				return
			}

			n.mu.Lock()
			if reply.Term > n.currentTerm {
				n.currentTerm = reply.Term
				n.state = Follower
				n.votedFor = ""
				n.mu.Unlock()
				n.savePersistentState()
				return
			}
			n.mu.Unlock()

			if reply.VoteGranted {
				voteMu.Lock()
				votes++
				voteMu.Unlock()
			}
		}(peer.Address)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}

	voteMu.Lock()
	gotVotes := votes
	voteMu.Unlock()

	n.mu.Lock()
	if n.state == Candidate && n.currentTerm == currentTerm && gotVotes >= needed {
		n.mu.Unlock()
		n.becomeLeader()
		return
	}
	n.mu.Unlock()

	n.resetElectionTimer()
}

// TransferLeadership transfers leadership to the specified node.
// Returns nil if transfer succeeds, error otherwise.
func (n *Node) TransferLeadership(targetID NodeID, timeout time.Duration) error {
	n.mu.Lock()
	if n.state != Leader {
		n.mu.Unlock()
		return fmt.Errorf("not leader")
	}
	if n.transferTarget != "" {
		n.mu.Unlock()
		return fmt.Errorf("transfer already in progress")
	}

	// Find target address
	var targetAddr string
	if n.clusterConfig != nil {
		if info, ok := n.clusterConfig.Nodes[targetID]; ok {
			targetAddr = info.Address
		}
	}
	if targetAddr == "" {
		n.mu.Unlock()
		return fmt.Errorf("target node %s not found in cluster", targetID)
	}

	n.transferTarget = targetID
	n.transferDone = make(chan struct{})
	currentTerm := n.currentTerm
	n.mu.Unlock()

	defer func() {
		n.mu.Lock()
		n.transferTarget = ""
		n.transferDone = nil
		n.mu.Unlock()
	}()

	// Wait for target to catch up
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		n.mu.RLock()
		if n.state != Leader {
			n.mu.RUnlock()
			return fmt.Errorf("no longer leader")
		}
		if n.leaderState == nil {
			n.mu.RUnlock()
			return fmt.Errorf("leader state not available")
		}
		matchIdx := n.leaderState.MatchIndex[targetID]
		lastIdx := n.log.LastIndex()
		n.mu.RUnlock()

		if matchIdx >= lastIdx {
			// Target is caught up, send TimeoutNow
			break
		}

		// Force replication to target
		go n.replicateTo(targetID, targetAddr, currentTerm, n.commitIndex)

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for target to catch up")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Send TimeoutNow to trigger immediate election
	args := &TimeoutNowArgs{
		Term:     currentTerm,
		LeaderID: n.config.ID,
	}

	reply, err := n.transport.TimeoutNow(ctx, targetAddr, args)
	if err != nil {
		return fmt.Errorf("TimeoutNow RPC failed: %w", err)
	}

	if !reply.Success {
		return fmt.Errorf("target rejected TimeoutNow")
	}

	// Wait for new leader to emerge
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for new leader")
		case <-time.After(10 * time.Millisecond):
			n.mu.RLock()
			if n.state != Leader {
				// We stepped down, transfer succeeded
				n.mu.RUnlock()
				return nil
			}
			n.mu.RUnlock()
		}
	}
}

// Propose proposes a new command to be replicated (synchronous).
// Returns the index and term if leader, or error if not.
// For high-throughput scenarios, use ProposeAsync instead.
func (n *Node) Propose(entryType LogEntryType, command any) (Index, Term, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return 0, 0, fmt.Errorf("not leader (leader is %s)", n.leaderID)
	}

	var cmdBytes []byte
	var err error
	if command != nil {
		cmdBytes, err = MarshalCommand(command)
		if err != nil {
			return 0, 0, fmt.Errorf("marshal command: %w", err)
		}
	}

	entry := LogEntry{
		Index:   n.log.LastIndex() + 1,
		Term:    n.currentTerm,
		Type:    entryType,
		Command: cmdBytes,
	}

	if err := n.log.Append(entry); err != nil {
		return 0, 0, fmt.Errorf("append to log: %w", err)
	}

	// For single-node cluster, commit immediately
	if n.clusterSize() <= 1 {
		n.commitIndex = entry.Index
		// Signal apply goroutine
		select {
		case n.applyCh <- struct{}{}:
		default:
		}
	} else {
		// Immediately trigger replication for multi-node cluster
		go n.sendHeartbeats()
	}

	return entry.Index, entry.Term, nil
}

// ProposeAsync proposes a command using the batch queue for higher throughput.
// Multiple proposals are batched together and replicated in fewer RPCs.
// Returns the index and term if leader, or error if not.
func (n *Node) ProposeAsync(entryType LogEntryType, command any) (Index, Term, error) {
	// Quick check if we're leader (avoid queueing if we're not)
	n.mu.RLock()
	if n.state != Leader {
		leaderID := n.leaderID
		n.mu.RUnlock()
		return 0, 0, fmt.Errorf("not leader (leader is %s)", leaderID)
	}
	n.mu.RUnlock()

	req := &proposalRequest{
		entryType: entryType,
		command:   command,
		result:    make(chan proposalResult, 1),
	}

	// Send to batch queue
	select {
	case n.proposalQueue <- req:
		// Queued successfully
	case <-n.shutdownC:
		return 0, 0, fmt.Errorf("node shutting down")
	}

	// Wait for result
	result := <-req.result
	return result.index, result.term, result.err
}

// State returns the current node state.
func (n *Node) State() NodeState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state
}

// Leader returns the current leader ID.
func (n *Node) Leader() NodeID {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.leaderID
}

// IsLeader returns true if this node is the leader.
func (n *Node) IsLeader() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state == Leader
}

// Term returns the current term.
func (n *Node) Term() Term {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.currentTerm
}

// FSM returns the cluster state (read-only access).
func (n *Node) FSM() *ClusterState {
	return n.fsm
}

// Metrics returns observability metrics for this node.
func (n *Node) Metrics() NodeMetrics {
	n.mu.RLock()
	state := n.state
	leader := n.leaderID
	term := n.currentTerm
	commitIdx := n.commitIndex
	lastApplied := n.lastApplied
	clusterSize := n.clusterSize()
	quorumSize := n.quorumSize()

	// Collect peer metrics if we're leader
	var peers []PeerMetrics
	if state == Leader && n.leaderState != nil && n.peerState != nil {
		logLast := n.log.LastIndex()
		for peerID, tracker := range n.peerState {
			nextIdx := n.leaderState.NextIndex[peerID]
			matchIdx := n.leaderState.MatchIndex[peerID]

			// Determine peer state
			var peerState PeerState
			lag := Index(0)
			if logLast > matchIdx {
				lag = logLast - matchIdx
			}
			if tracker.failedRPCs >= 3 {
				peerState = PeerUnreachable
			} else if lag > 10 {
				peerState = PeerBehind
			} else {
				peerState = PeerSynced
			}

			// Get address from cluster config
			addr := ""
			if n.clusterConfig != nil && n.clusterConfig.Nodes[peerID] != nil {
				addr = n.clusterConfig.Nodes[peerID].Address
			}

			lastContactMs := time.Since(tracker.lastContact).Milliseconds()

			peers = append(peers, PeerMetrics{
				NodeID:          peerID,
				Address:         addr,
				State:           peerState,
				NextIndex:       nextIdx,
				MatchIndex:      matchIdx,
				LagEntries:      lag,
				LastContactMs:   lastContactMs,
				FailedRPCs:      tracker.failedRPCs,
				InflightEntries: int(nextIdx - matchIdx - 1),
			})
		}
	}
	n.mu.RUnlock()

	// Get log metrics
	logFirst := n.log.FirstIndex()
	logLast := n.log.LastIndex()
	logLen := n.log.Len()
	snapshotIdx := n.log.SnapshotIndex()

	// Get FSM metrics
	n.fsm.mu.RLock()
	deployments := len(n.fsm.Deployments)
	nodes := len(n.fsm.Nodes)
	n.fsm.mu.RUnlock()

	entriesBehind := Index(0)
	if commitIdx > lastApplied {
		entriesBehind = commitIdx - lastApplied
	}

	return NodeMetrics{
		NodeID:           n.config.ID,
		State:            state,
		Leader:           leader,
		Term:             term,
		LogFirstIndex:    logFirst,
		LogLastIndex:     logLast,
		LogLength:        logLen,
		CommitIndex:      commitIdx,
		LastApplied:      lastApplied,
		SnapshotIndex:    snapshotIdx,
		EntriesBehind:    entriesBehind,
		ClusterSize:      clusterSize,
		QuorumSize:       quorumSize,
		Deployments:      deployments,
		Nodes:            nodes,
		Peers:            peers,
	}
}

// OnLeaderChange sets a callback for leader changes.
func (n *Node) OnLeaderChange(fn func(NodeID)) {
	n.mu.Lock()
	n.onLeaderChange = fn
	n.mu.Unlock()
}

// clusterSize returns the number of nodes in the cluster configuration.
// This is used for quorum calculations and should not change dynamically
// based on FSM state - only through committed membership changes.
func (n *Node) clusterSize() int {
	if n.clusterConfig == nil || len(n.clusterConfig.Nodes) == 0 {
		// Single-node cluster bootstrap: if we have no cluster config,
		// we're either bootstrapping or haven't joined a cluster yet.
		// In this case, treat ourselves as a single-node cluster.
		// Note: We explicitly do NOT fall back to FSM.Nodes as that can
		// cause split-brain during membership changes.
		return 1
	}
	return len(n.clusterConfig.Nodes)
}

// quorumSize returns the number of votes needed for a majority.
func (n *Node) quorumSize() int {
	size := n.clusterSize()
	if size <= 1 {
		return 1 // Single node cluster
	}
	return (size / 2) + 1
}

// AddToClusterConfig adds a node to the cluster configuration.
// This should be called when a membership change is committed.
func (n *Node) AddToClusterConfig(nodeID NodeID, addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.clusterConfig == nil {
		n.clusterConfig = &ClusterConfig{Nodes: make(map[NodeID]*NodeInfo)}
	}
	n.clusterConfig.Nodes[nodeID] = &NodeInfo{
		ID:      nodeID,
		Address: addr,
	}
	// Persist the membership change immediately
	n.savePersistentState()
}

// RemoveFromClusterConfig removes a node from the cluster configuration.
func (n *Node) RemoveFromClusterConfig(nodeID NodeID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.clusterConfig != nil {
		delete(n.clusterConfig.Nodes, nodeID)
		// Persist the membership change immediately
		n.savePersistentState()
	}
}

// ClusterNodes returns the list of nodes in the cluster configuration.
func (n *Node) ClusterNodes() []*NodeInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.clusterConfig == nil {
		return nil
	}
	nodes := make([]*NodeInfo, 0, len(n.clusterConfig.Nodes))
	for _, node := range n.clusterConfig.Nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// savePersistentState saves currentTerm, votedFor, and clusterConfig to disk.
func (n *Node) savePersistentState() error {
	state := PersistentState{
		CurrentTerm:   n.currentTerm,
		VotedFor:      n.votedFor,
		ClusterConfig: n.clusterConfig,
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	path := filepath.Join(n.config.DataDir, "state.json")
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// loadPersistentState loads currentTerm, votedFor, and clusterConfig from disk.
func (n *Node) loadPersistentState() error {
	path := filepath.Join(n.config.DataDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Fresh start
		}
		return err
	}

	var state PersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	n.currentTerm = state.CurrentTerm
	n.votedFor = state.VotedFor
	if state.ClusterConfig != nil {
		n.clusterConfig = state.ClusterConfig
	}
	return nil
}
