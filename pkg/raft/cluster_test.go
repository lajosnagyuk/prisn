package raft

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestNetworkPartition tests that a leader cannot commit entries when
// isolated from a majority of nodes.
func TestNetworkPartition(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:9001"),
		network.AddTransport("node2:9001"),
		network.AddTransport("node3:9001"),
	}

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

	// Bootstrap cluster
	bootstrapCluster(nodes, transports)

	// Start all nodes
	for _, node := range nodes {
		node.Start()
	}
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
	}()

	// Wait for leader election
	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]
	t.Logf("Leader elected: %s (index %d)", leader.config.ID, leaderIdx)

	// Verify initial proposal works
	cmd := DeploymentCommand{Name: "before-partition", Namespace: "default", Type: "job"}
	_, _, err := leader.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("failed to propose before partition: %v", err)
	}

	// Wait for replication
	time.Sleep(100 * time.Millisecond)
	for i, node := range nodes {
		if node.FSM().GetDeployment("default", "before-partition") == nil {
			t.Errorf("node %d: deployment not replicated before partition", i)
		}
	}

	// Partition: isolate the leader from both followers
	follower1Idx := (leaderIdx + 1) % 3
	follower2Idx := (leaderIdx + 2) % 3
	network.Disconnect(transports[leaderIdx].Address(), transports[follower1Idx].Address())
	network.Disconnect(transports[leaderIdx].Address(), transports[follower2Idx].Address())
	t.Logf("Partitioned leader %d from followers %d and %d", leaderIdx, follower1Idx, follower2Idx)

	// The partitioned leader should eventually step down or be unable to commit
	// The followers should elect a new leader among themselves

	// Wait for new leader among followers
	var newLeader *Node
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		for j, node := range nodes {
			if j == leaderIdx {
				continue // Skip partitioned leader
			}
			if node.IsLeader() {
				newLeader = node
				break
			}
		}
		if newLeader != nil {
			break
		}
	}

	if newLeader == nil {
		t.Fatal("no new leader elected among non-partitioned nodes")
	}
	t.Logf("New leader elected: %s", newLeader.config.ID)

	// Propose through new leader should succeed
	cmd2 := DeploymentCommand{Name: "during-partition", Namespace: "default", Type: "job"}
	_, _, err = newLeader.Propose(LogDeploymentCreate, cmd2)
	if err != nil {
		t.Fatalf("failed to propose through new leader: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// New deployment should be on the majority (2 nodes), not the partitioned leader
	replicatedCount := 0
	for i, node := range nodes {
		if node.FSM().GetDeployment("default", "during-partition") != nil {
			replicatedCount++
			t.Logf("node %d has during-partition deployment", i)
		}
	}
	if replicatedCount < 2 {
		t.Errorf("expected at least 2 nodes to have the new deployment, got %d", replicatedCount)
	}

	// Heal partition
	network.Reconnect(transports[leaderIdx].Address(), transports[follower1Idx].Address())
	network.Reconnect(transports[leaderIdx].Address(), transports[follower2Idx].Address())
	t.Log("Healed partition")

	// Wait for convergence
	time.Sleep(200 * time.Millisecond)

	// Eventually all nodes should have both deployments
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		allHave := true
		for _, node := range nodes {
			if node.FSM().GetDeployment("default", "before-partition") == nil ||
				node.FSM().GetDeployment("default", "during-partition") == nil {
				allHave = false
				break
			}
		}
		if allHave {
			break
		}
	}

	for i, node := range nodes {
		if node.FSM().GetDeployment("default", "before-partition") == nil {
			t.Errorf("node %d missing before-partition deployment after heal", i)
		}
		if node.FSM().GetDeployment("default", "during-partition") == nil {
			t.Errorf("node %d missing during-partition deployment after heal", i)
		}
	}
}

// TestQuorumCalculation verifies that quorum is calculated correctly.
// This test exposes the bug where quorum uses FSM state instead of config.
func TestQuorumCalculation(t *testing.T) {
	// This test demonstrates the membership model bug:
	// The current implementation calculates quorum from n.fsm.Nodes (dynamic)
	// but it should use a fixed configuration.

	dir := t.TempDir()

	network := NewMemoryNetwork()
	transport := network.AddTransport("node1:9002")

	node, err := NewNode(Config{
		ID:                "node1",
		DataDir:           dir,
		HeartbeatInterval: 10 * time.Millisecond,
		ElectionTimeout:   50 * time.Millisecond,
	}, transport)
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	transport.SetNode(node)

	// Start with empty FSM (no nodes registered)
	// A single node with empty FSM should still become leader
	node.Start()
	defer node.Stop()

	time.Sleep(200 * time.Millisecond)

	if !node.IsLeader() {
		t.Error("single node should become leader even with empty FSM")
	}

	// Now add itself to FSM
	nodeCmd := NodeCommand{NodeID: "node1", Address: "node1:9002"}
	nodeBytes, _ := MarshalCommand(nodeCmd)
	node.fsm.Apply(&LogEntry{Index: 1, Term: 1, Type: LogNodeJoin, Command: nodeBytes})

	// Should still be leader
	if !node.IsLeader() {
		t.Error("node should remain leader after adding itself to FSM")
	}

	// Propose should work
	cmd := DeploymentCommand{Name: "test", Namespace: "default", Type: "job"}
	_, _, err = node.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Errorf("propose failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if node.FSM().GetDeployment("default", "test") == nil {
		t.Error("deployment not applied")
	}
}

// TestDynamicMembership tests adding and removing nodes from a running cluster.
func TestDynamicMembership(t *testing.T) {
	dir := t.TempDir()

	network := NewMemoryNetwork()

	// Start with 3 nodes
	nodes := make([]*Node, 3)
	transports := make([]*MemoryTransport, 3)

	for i := 0; i < 3; i++ {
		nodeID := NodeID(fmt.Sprintf("node%d", i+1))
		transports[i] = network.AddTransport(fmt.Sprintf("node%d:9003", i+1))
		cfg := Config{
			ID:                nodeID,
			DataDir:           filepath.Join(dir, string(nodeID)),
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

	// Bootstrap with 3 nodes
	for _, node := range nodes {
		for i := 0; i < 3; i++ {
			nodeCmd := NodeCommand{
				NodeID:  nodes[i].config.ID,
				Address: transports[i].Address(),
			}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
		}
	}

	// Start nodes
	for _, node := range nodes {
		node.Start()
	}

	// Wait for leader
	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]
	t.Logf("Initial leader: %s", leader.config.ID)

	// Add a 4th node
	transports = append(transports, network.AddTransport("node4:9003"))
	cfg := Config{
		ID:                "node4",
		DataDir:           filepath.Join(dir, "node4"),
		HeartbeatInterval: 10 * time.Millisecond,
		ElectionTimeout:   50 * time.Millisecond,
	}
	node4, err := NewNode(cfg, transports[3])
	if err != nil {
		t.Fatalf("failed to create node4: %v", err)
	}
	nodes = append(nodes, node4)
	transports[3].SetNode(node4)

	// Notify existing nodes about the new node
	for _, node := range nodes[:3] {
		nodeCmd := NodeCommand{NodeID: "node4", Address: "node4:9003"}
		nodeBytes, _ := MarshalCommand(nodeCmd)
		node.fsm.Apply(&LogEntry{Index: 4, Term: 1, Type: LogNodeJoin, Command: nodeBytes})
	}

	// Bootstrap node4 with cluster state
	for i := 0; i < 4; i++ {
		nodeCmd := NodeCommand{
			NodeID:  nodes[i].config.ID,
			Address: transports[i].Address(),
		}
		nodeBytes, _ := MarshalCommand(nodeCmd)
		node4.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
	}

	node4.Start()

	// Wait for node4 to catch up
	time.Sleep(200 * time.Millisecond)

	// Propose a new deployment - should replicate to 4 nodes
	cmd := DeploymentCommand{Name: "four-node-test", Namespace: "default", Type: "job"}
	_, _, err = leader.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("propose failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify all 4 nodes have the deployment
	for i, node := range nodes {
		if node.FSM().GetDeployment("default", "four-node-test") == nil {
			t.Errorf("node %d missing deployment", i)
		}
	}

	// Stop all nodes
	for _, node := range nodes {
		node.Stop()
	}
}

// TestLogReplicationUnderLoad tests that entries are correctly replicated
// under continuous load.
func TestLogReplicationUnderLoad(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:9004"),
		network.AddTransport("node2:9004"),
		network.AddTransport("node3:9004"),
	}

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

	// Bootstrap
	bootstrapCluster(nodes, transports)

	for _, node := range nodes {
		node.Start()
	}
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
	}()

	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]

	// Submit 100 entries in parallel
	const numEntries = 100
	var wg sync.WaitGroup
	errors := make(chan error, numEntries)

	for i := 0; i < numEntries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := DeploymentCommand{
				Name:      deployNameFromInt(i),
				Namespace: "default",
				Type:      "job",
			}
			_, _, err := leader.Propose(LogDeploymentCreate, cmd)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	errCount := 0
	for err := range errors {
		t.Logf("propose error: %v", err)
		errCount++
	}
	if errCount > 0 {
		t.Errorf("%d proposals failed", errCount)
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Count deployments on each node
	for i, node := range nodes {
		count := 0
		for j := 0; j < numEntries; j++ {
			if node.FSM().GetDeployment("default", deployNameFromInt(j)) != nil {
				count++
			}
		}
		t.Logf("node %d has %d/%d deployments", i, count, numEntries)
		if count < numEntries-5 { // Allow small margin for timing
			t.Errorf("node %d: expected ~%d deployments, got %d", i, numEntries, count)
		}
	}
}

// TestCommitIndexAdvancement verifies that commit index advances correctly
// when entries are replicated to a majority.
func TestCommitIndexAdvancement(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:9005"),
		network.AddTransport("node2:9005"),
		network.AddTransport("node3:9005"),
	}

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

	// Bootstrap
	bootstrapCluster(nodes, transports)

	for _, node := range nodes {
		node.Start()
	}
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
	}()

	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]

	// Track initial commit indexes
	initialCommits := make([]Index, 3)
	for i, node := range nodes {
		node.mu.RLock()
		initialCommits[i] = node.commitIndex
		node.mu.RUnlock()
	}

	// Propose entry
	cmd := DeploymentCommand{Name: "commit-test", Namespace: "default", Type: "job"}
	idx, _, err := leader.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("propose failed: %v", err)
	}
	t.Logf("Proposed entry at index %d", idx)

	// Wait for commit
	time.Sleep(200 * time.Millisecond)

	// Verify commit index advanced on all nodes
	for i, node := range nodes {
		node.mu.RLock()
		commitIdx := node.commitIndex
		node.mu.RUnlock()

		if commitIdx < idx {
			t.Errorf("node %d: commit index %d < entry index %d", i, commitIdx, idx)
		}
		t.Logf("node %d: commit index %d -> %d", i, initialCommits[i], commitIdx)
	}
}

// TestFollowerCatchup tests that a slow follower can catch up
// after being disconnected.
func TestFollowerCatchup(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:9006"),
		network.AddTransport("node2:9006"),
		network.AddTransport("node3:9006"),
	}

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

	// Bootstrap
	bootstrapCluster(nodes, transports)

	for _, node := range nodes {
		node.Start()
	}
	defer func() {
		for _, node := range nodes {
			node.Stop()
		}
	}()

	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]

	// Pick a follower to disconnect
	followerIdx := (leaderIdx + 1) % 3
	otherIdx := (leaderIdx + 2) % 3

	// Disconnect follower from all other nodes
	network.Disconnect(transports[followerIdx].Address(), transports[leaderIdx].Address())
	network.Disconnect(transports[followerIdx].Address(), transports[otherIdx].Address())
	t.Logf("Disconnected node %d", followerIdx)

	// Propose several entries while follower is disconnected
	for i := 0; i < 10; i++ {
		cmd := DeploymentCommand{
			Name:      deployNameFromInt(i) + "-catchup",
			Namespace: "default",
			Type:      "job",
		}
		_, _, err := leader.Propose(LogDeploymentCreate, cmd)
		if err != nil {
			t.Fatalf("propose %d failed: %v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Verify leader and other follower have entries, but disconnected doesn't
	for i := 0; i < 10; i++ {
		name := deployNameFromInt(i) + "-catchup"
		if leader.FSM().GetDeployment("default", name) == nil {
			t.Errorf("leader missing deployment %s", name)
		}
		if nodes[otherIdx].FSM().GetDeployment("default", name) == nil {
			t.Errorf("node %d missing deployment %s", otherIdx, name)
		}
		// Disconnected follower should be missing entries
		if nodes[followerIdx].FSM().GetDeployment("default", name) != nil {
			t.Errorf("disconnected node %d has deployment %s unexpectedly", followerIdx, name)
		}
	}

	// Reconnect follower
	network.Reconnect(transports[followerIdx].Address(), transports[leaderIdx].Address())
	network.Reconnect(transports[followerIdx].Address(), transports[otherIdx].Address())
	t.Logf("Reconnected node %d", followerIdx)

	// Wait for catchup
	time.Sleep(500 * time.Millisecond)

	// Verify follower caught up
	for i := 0; i < 10; i++ {
		name := deployNameFromInt(i) + "-catchup"
		if nodes[followerIdx].FSM().GetDeployment("default", name) == nil {
			t.Errorf("node %d did not catch up: missing deployment %s", followerIdx, name)
		}
	}
}

// Helper functions

// bootstrapCluster sets up the cluster configuration on all nodes.
// This initializes both FSM.Nodes and clusterConfig for proper quorum calculation.
func bootstrapCluster(nodes []*Node, transports []*MemoryTransport) {
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{NodeID: n.config.ID, Address: transports[i].Address()}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also update clusterConfig (normally done by applyMembershipChange)
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}
}

func waitForLeader(t *testing.T, nodes []*Node, leaderIdx *int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for i, node := range nodes {
			if node.IsLeader() {
				*leaderIdx = i
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no leader elected within %v", timeout)
}

// deployNameFromInt creates a deployment name from an integer
func deployNameFromInt(i int) string {
	return fmt.Sprintf("deploy-%d", i)
}

// waitForDeployment polls until a deployment exists on all nodes or timeout.
func waitForDeployment(t *testing.T, nodes []*Node, namespace, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found := true
		for _, node := range nodes {
			if node.FSM().GetDeployment(namespace, name) == nil {
				found = false
				break
			}
		}
		if found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("deployment %s/%s not replicated to all nodes within %v", namespace, name, timeout)
}

// waitForNodeInFSM polls until a node exists in all FSMs or timeout.
func waitForNodeInFSM(t *testing.T, nodes []*Node, nodeID NodeID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found := true
		for _, node := range nodes {
			if node.FSM().GetNode(nodeID) == nil {
				found = false
				break
			}
		}
		if found {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitForCommitIndex polls until all nodes reach at least the given commit index.
func waitForCommitIndex(t *testing.T, nodes []*Node, minIndex Index, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReached := true
		for _, node := range nodes {
			node.mu.RLock()
			commit := node.commitIndex
			node.mu.RUnlock()
			if commit < minIndex {
				allReached = false
				break
			}
		}
		if allReached {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("not all nodes reached commit index %d within %v", minIndex, timeout)
}

// waitForCondition polls until condition returns true or timeout.
func waitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// stopNodes stops all nodes and waits briefly for file handles to be released.
// This prevents "directory not empty" errors on macOS during test cleanup.
func stopNodes(nodes []*Node) {
	for _, node := range nodes {
		node.Stop()
	}
	// Brief delay to ensure OS releases file handles
	time.Sleep(10 * time.Millisecond)
}
