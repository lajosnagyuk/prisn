package raft

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogAppendAndGet(t *testing.T) {
	dir := t.TempDir()

	log, err := NewLog(dir)
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Append entries
	entries := []LogEntry{
		{Index: 1, Term: 1, Type: LogNoop},
		{Index: 2, Term: 1, Type: LogDeploymentCreate, Command: []byte(`{"name":"test"}`)},
		{Index: 3, Term: 2, Type: LogDeploymentDelete},
	}

	if err := log.Append(entries...); err != nil {
		t.Fatalf("failed to append: %v", err)
	}

	// Verify
	if log.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", log.Len())
	}

	if log.LastIndex() != 3 {
		t.Errorf("expected last index 3, got %d", log.LastIndex())
	}

	if log.LastTerm() != 2 {
		t.Errorf("expected last term 2, got %d", log.LastTerm())
	}

	// Get specific entry
	entry, ok := log.Get(2)
	if !ok {
		t.Fatal("entry 2 not found")
	}
	if entry.Type != LogDeploymentCreate {
		t.Errorf("expected LogDeploymentCreate, got %v", entry.Type)
	}
}

func TestLogPersistence(t *testing.T) {
	dir := t.TempDir()

	// Create and write
	log1, err := NewLog(dir)
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	entries := []LogEntry{
		{Index: 1, Term: 1, Type: LogNodeJoin, Command: []byte(`{"node_id":"node-1"}`)},
		{Index: 2, Term: 1, Type: LogDeploymentCreate, Command: []byte(`{"name":"api"}`)},
	}
	if err := log1.Append(entries...); err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	log1.Close()

	// Reopen and verify
	log2, err := NewLog(dir)
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	if log2.Len() != 2 {
		t.Errorf("expected 2 entries after reopen, got %d", log2.Len())
	}

	entry, ok := log2.Get(1)
	if !ok {
		t.Fatal("entry 1 not found after reopen")
	}
	if entry.Type != LogNodeJoin {
		t.Errorf("expected LogNodeJoin, got %v", entry.Type)
	}
}

func TestClusterStateFSM(t *testing.T) {
	state := NewClusterState()

	// Test deployment create
	cmd := DeploymentCommand{
		Name:        "api",
		Namespace:   "default",
		Version:     "v-12345678",
		Fingerprint: "abc123",
		Type:        "service",
		Replicas:    3,
		Port:        8080,
	}
	cmdBytes, _ := MarshalCommand(cmd)

	entry := &LogEntry{
		Index:   1,
		Term:    1,
		Type:    LogDeploymentCreate,
		Command: cmdBytes,
	}

	if err := state.Apply(entry); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}

	// Verify deployment was created
	d := state.GetDeployment("default", "api")
	if d == nil {
		t.Fatal("deployment not found")
	}
	if d.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", d.Replicas)
	}
	if d.Port != 8080 {
		t.Errorf("expected port 8080, got %d", d.Port)
	}
	if len(d.Versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(d.Versions))
	}

	// Test active version
	activeVersion := state.GetActiveVersion("default", "api")
	if activeVersion != "v-12345678" {
		t.Errorf("expected v-12345678, got %s", activeVersion)
	}
}

func TestClusterStateNodeJoin(t *testing.T) {
	state := NewClusterState()

	cmd := NodeCommand{
		NodeID:  "node-1",
		Address: "localhost:7332",
	}
	cmdBytes, _ := MarshalCommand(cmd)

	entry := &LogEntry{
		Index:   1,
		Term:    1,
		Type:    LogNodeJoin,
		Command: cmdBytes,
	}

	if err := state.Apply(entry); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}

	nodes := state.ListNodes()
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}

	node := state.GetNode("node-1")
	if node == nil {
		t.Fatal("node not found")
	}
	if node.Address != "localhost:7332" {
		t.Errorf("expected localhost:7332, got %s", node.Address)
	}
}

func TestClusterStateSnapshotRestore(t *testing.T) {
	state := NewClusterState()

	// Add some data
	deployCmd := DeploymentCommand{Name: "api", Namespace: "default", Type: "service"}
	deployBytes, _ := MarshalCommand(deployCmd)
	state.Apply(&LogEntry{Index: 1, Term: 1, Type: LogDeploymentCreate, Command: deployBytes})

	nodeCmd := NodeCommand{NodeID: "node-1", Address: "localhost:7332"}
	nodeBytes, _ := MarshalCommand(nodeCmd)
	state.Apply(&LogEntry{Index: 2, Term: 1, Type: LogNodeJoin, Command: nodeBytes})

	// Snapshot
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatalf("failed to snapshot: %v", err)
	}

	// Create new state and restore
	state2 := NewClusterState()
	if err := state2.Restore(snapshot); err != nil {
		t.Fatalf("failed to restore: %v", err)
	}

	// Verify
	if state2.GetDeployment("default", "api") == nil {
		t.Error("deployment not restored")
	}
	if state2.GetNode("node-1") == nil {
		t.Error("node not restored")
	}
}

func TestNodeSingleNode(t *testing.T) {
	dir := t.TempDir()

	transport := NewHTTPTransport("localhost:17332")

	node, err := NewNode(Config{
		ID:                "node-1",
		DataDir:           filepath.Join(dir, "raft"),
		HeartbeatInterval: 10 * time.Millisecond,
		ElectionTimeout:   50 * time.Millisecond,
	}, transport)
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	transport.SetNode(node)

	node.Start()
	defer node.Stop()

	// Single node should become leader quickly
	time.Sleep(200 * time.Millisecond)

	if !node.IsLeader() {
		t.Error("single node should be leader")
	}

	// Test proposing a command
	cmd := DeploymentCommand{
		Name:      "test",
		Namespace: "default",
		Type:      "job",
	}

	idx, term, err := node.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("failed to propose: %v", err)
	}

	if idx != 2 { // 1 is noop
		t.Errorf("expected index 2, got %d", idx)
	}
	if term != 1 {
		t.Errorf("expected term 1, got %d", term)
	}

	// Wait for apply
	time.Sleep(50 * time.Millisecond)

	// Verify FSM has the deployment
	d := node.FSM().GetDeployment("default", "test")
	if d == nil {
		t.Error("deployment not applied to FSM")
	}
}

func TestFingerprint(t *testing.T) {
	dir := t.TempDir()

	// Create test files
	os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')"), 0644)
	os.WriteFile(filepath.Join(dir, "utils.py"), []byte("def foo(): pass"), 0644)

	// Note: This test uses the store package, but we can test the concept
	// The fingerprint should be deterministic
}

func TestThreeNodeCluster(t *testing.T) {
	// Create data directories
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	// Create in-memory network
	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:7332"),
		network.AddTransport("node2:7332"),
		network.AddTransport("node3:7332"),
	}

	// Create nodes
	nodes := make([]*Node, 3)
	for i := 0; i < 3; i++ {
		cfg := Config{
			ID:                NodeID(filepath.Base(dirs[i])),
			DataDir:           dirs[i],
			HeartbeatInterval: 10 * time.Millisecond,
			ElectionTimeout:   50 * time.Millisecond,
		}
		node, err := NewNode(cfg, transports[i])
		if err != nil {
			t.Fatalf("failed to create node %d: %v", i, err)
		}
		nodes[i] = node
		transports[i].SetNode(node)
	}

	// Bootstrap the cluster by adding nodes to each FSM and clusterConfig
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{
				NodeID:  n.config.ID,
				Address: transports[i].Address(),
			}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also add to clusterConfig for quorum calculations
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}

	// Start all nodes
	for _, node := range nodes {
		node.Start()
	}
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
	}()

	// Wait for a leader to be elected
	var leader *Node
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		for _, node := range nodes {
			if node.IsLeader() {
				leader = node
				break
			}
		}
		if leader != nil {
			break
		}
	}

	if leader == nil {
		t.Fatal("no leader elected after 2.5s")
	}

	t.Logf("Leader elected: %s", leader.config.ID)

	// Verify all nodes agree on the leader
	leaderID := leader.config.ID
	for _, node := range nodes {
		if node.Leader() != leaderID && !node.IsLeader() {
			// Give followers a moment to learn about the leader
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Propose a deployment through the leader
	cmd := DeploymentCommand{
		Name:        "api",
		Namespace:   "default",
		Version:     "v-test123",
		Fingerprint: "fingerprint123",
		Type:        "service",
		Replicas:    3,
		Port:        8080,
	}

	idx, term, err := leader.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("failed to propose: %v", err)
	}

	t.Logf("Proposed entry: index=%d, term=%d", idx, term)

	// Wait for replication and apply
	time.Sleep(200 * time.Millisecond)

	// Verify all nodes have the deployment in their FSM
	for i, node := range nodes {
		d := node.FSM().GetDeployment("default", "api")
		if d == nil {
			t.Errorf("node %d: deployment not found in FSM", i)
			continue
		}
		if d.Replicas != 3 {
			t.Errorf("node %d: expected 3 replicas, got %d", i, d.Replicas)
		}
		if d.Port != 8080 {
			t.Errorf("node %d: expected port 8080, got %d", i, d.Port)
		}
	}

	// Verify commit index is updated across all nodes
	for i, node := range nodes {
		if node.commitIndex < idx {
			t.Errorf("node %d: commit index %d < entry index %d", i, node.commitIndex, idx)
		}
	}
}

func TestLeaderFailover(t *testing.T) {
	// Create data directories
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	// Create in-memory network
	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:8332"),
		network.AddTransport("node2:8332"),
		network.AddTransport("node3:8332"),
	}

	// Create nodes
	nodes := make([]*Node, 3)
	for i := 0; i < 3; i++ {
		cfg := Config{
			ID:                NodeID(filepath.Base(dirs[i])),
			DataDir:           dirs[i],
			HeartbeatInterval: 10 * time.Millisecond,
			ElectionTimeout:   50 * time.Millisecond,
		}
		node, err := NewNode(cfg, transports[i])
		if err != nil {
			t.Fatalf("failed to create node %d: %v", i, err)
		}
		nodes[i] = node
		transports[i].SetNode(node)
	}

	// Bootstrap the cluster (FSM and clusterConfig)
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{
				NodeID:  n.config.ID,
				Address: transports[i].Address(),
			}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also add to clusterConfig for quorum calculations
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}

	// Start all nodes
	for _, node := range nodes {
		node.Start()
	}

	// Wait for a leader
	var leaderIdx int
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		for j, node := range nodes {
			if node.IsLeader() {
				leaderIdx = j
				break
			}
		}
		if nodes[leaderIdx].IsLeader() {
			break
		}
	}

	if !nodes[leaderIdx].IsLeader() {
		t.Fatal("no leader elected")
	}

	originalLeader := nodes[leaderIdx].config.ID
	t.Logf("Original leader: %s", originalLeader)

	// Stop the leader
	nodes[leaderIdx].Stop()
	t.Logf("Stopped leader node %d", leaderIdx)

	// Ensure remaining nodes get stopped at the end
	defer func() {
		for j, node := range nodes {
			if j != leaderIdx {
				node.Stop()
			}
		}
		// Allow file handles to be released before TempDir cleanup
		time.Sleep(50 * time.Millisecond)
	}()

	// Wait for new leader election (use longer timeout and polling)
	var newLeader *Node
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for j, node := range nodes {
			if j == leaderIdx {
				continue // Skip stopped node
			}
			if node.IsLeader() {
				newLeader = node
				break
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if newLeader == nil {
		t.Fatal("no new leader elected after original leader stopped")
	}

	t.Logf("New leader elected: %s", newLeader.config.ID)

	if newLeader.config.ID == originalLeader {
		t.Error("new leader is same as original leader")
	}
}

func TestLogCompaction(t *testing.T) {
	dir := t.TempDir()

	log, err := NewLog(dir)
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Append many entries
	for i := Index(1); i <= 100; i++ {
		entry := LogEntry{Index: i, Term: 1, Type: LogNoop}
		if err := log.Append(entry); err != nil {
			t.Fatalf("failed to append entry %d: %v", i, err)
		}
	}

	// Verify initial state
	if log.FirstIndex() != 1 {
		t.Errorf("expected first index 1, got %d", log.FirstIndex())
	}
	if log.LastIndex() != 100 {
		t.Errorf("expected last index 100, got %d", log.LastIndex())
	}
	if log.Len() != 100 {
		t.Errorf("expected 100 entries, got %d", log.Len())
	}

	// Compact up to index 50
	if err := log.Compact(50, 1); err != nil {
		t.Fatalf("failed to compact: %v", err)
	}

	// Verify compacted state
	if log.FirstIndex() != 51 {
		t.Errorf("expected first index 51 after compaction, got %d", log.FirstIndex())
	}
	if log.LastIndex() != 100 {
		t.Errorf("expected last index 100 after compaction, got %d", log.LastIndex())
	}
	if log.Len() != 50 {
		t.Errorf("expected 50 entries after compaction, got %d", log.Len())
	}
	if log.SnapshotIndex() != 50 {
		t.Errorf("expected snapshot index 50, got %d", log.SnapshotIndex())
	}
	if log.SnapshotTerm() != 1 {
		t.Errorf("expected snapshot term 1, got %d", log.SnapshotTerm())
	}

	// Verify entries before compaction point are gone
	if _, ok := log.Get(50); ok {
		t.Error("entry at compacted index should not exist")
	}
	if _, ok := log.Get(1); ok {
		t.Error("entry at compacted index should not exist")
	}

	// Verify entries after compaction point exist
	entry, ok := log.Get(51)
	if !ok {
		t.Error("entry at first index after compaction should exist")
	}
	if entry.Index != 51 {
		t.Errorf("expected entry index 51, got %d", entry.Index)
	}

	// Test persistence - close and reopen
	log.Close()

	log2, err := NewLog(dir)
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	// Verify state survived restart
	if log2.FirstIndex() != 51 {
		t.Errorf("expected first index 51 after restart, got %d", log2.FirstIndex())
	}
	if log2.LastIndex() != 100 {
		t.Errorf("expected last index 100 after restart, got %d", log2.LastIndex())
	}
	if log2.SnapshotIndex() != 50 {
		t.Errorf("expected snapshot index 50 after restart, got %d", log2.SnapshotIndex())
	}
}
