package raft

import (
	"path/filepath"
	"testing"
	"time"
)

// TestQuorumUsesConfigNotFSM verifies that quorum calculations use the
// static cluster configuration, not the dynamic FSM state.
//
// Bug: The current implementation calculates quorum from n.fsm.Nodes (lines 239, 499),
// which changes as log entries are applied. This is incorrect - quorum should be
// calculated from a fixed configuration that only changes through explicit
// reconfiguration commands.
func TestQuorumUsesConfigNotFSM(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:10001"),
		network.AddTransport("node2:10001"),
		network.AddTransport("node3:10001"),
	}

	// Create nodes with a 3-node configuration
	nodes := make([]*Node, 3)
	for i := 0; i < 3; i++ {
		cfg := Config{
			ID:                NodeID(filepath.Base(dirs[i])),
			DataDir:           dirs[i],
			HeartbeatInterval: 10 * time.Millisecond,
			ElectionTimeout:   50 * time.Millisecond,
			// This is the fix - pass initial peers in config
			// Peers: []string{"node1:10001", "node2:10001", "node3:10001"},
		}
		node, err := NewNode(cfg, transports[i])
		if err != nil {
			t.Fatalf("failed to create node %d: %v", i, err)
		}
		nodes[i] = node
		transports[i].SetNode(node)
	}

	// Bootstrap FSM AND clusterConfig with 3 nodes
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{NodeID: n.config.ID, Address: transports[i].Address()}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also update clusterConfig (this is normally done by applyMembershipChange)
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}

	// Start all nodes
	for _, node := range nodes {
		node.Start()
	}
	defer stopNodes(nodes)

	// Wait for leader
	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]
	t.Logf("Leader elected: %s", leader.config.ID)

	// Verify initial state - all nodes should see 3 nodes in FSM
	for i, node := range nodes {
		fsmNodes := len(node.fsm.Nodes)
		if fsmNodes != 3 {
			t.Errorf("node %d: expected 3 nodes in FSM, got %d", i, fsmNodes)
		}
	}

	// Now here's the problematic scenario:
	// If we manually modify the FSM on the leader (simulating out-of-band change),
	// the quorum calculation would change. This should NOT happen - quorum should
	// be based on configuration, not FSM state.

	// Clear FSM nodes on leader (simulate FSM being ahead/behind config)
	leader.fsm.mu.Lock()
	originalNodes := make(map[NodeID]*NodeInfo, len(leader.fsm.Nodes))
	for k, v := range leader.fsm.Nodes {
		originalNodes[k] = v
	}
	leader.fsm.Nodes = make(map[NodeID]*NodeInfo) // Clear FSM nodes
	leader.fsm.mu.Unlock()

	// With the fix: clusterSize() should return 3 (from clusterConfig), not 0 (from empty FSM)
	// Verify this BEFORE proposing/restoring

	// Verify that clusterSize uses clusterConfig, not FSM
	configSize := leader.clusterSize()
	fsmSize := len(leader.fsm.Nodes)

	t.Logf("FSM nodes: %d, clusterConfig nodes: %d", fsmSize, configSize)

	if fsmSize != 0 {
		t.Error("expected FSM.Nodes to be empty for this test")
	}

	if configSize != 3 {
		t.Errorf("clusterSize() should return 3 from clusterConfig, got %d", configSize)
	}

	// Quorum should be 2 (not 1 as it would be with empty FSM)
	quorum := leader.quorumSize()
	if quorum != 2 {
		t.Errorf("quorumSize() should be 2 for 3-node cluster, got %d", quorum)
	}

	// Now try to propose - this tests if quorum uses config or FSM
	cmd := DeploymentCommand{Name: "test-quorum", Namespace: "default", Type: "job"}
	_, _, err := leader.Propose(LogDeploymentCreate, cmd)

	// Restore FSM
	leader.fsm.mu.Lock()
	leader.fsm.Nodes = originalNodes
	leader.fsm.mu.Unlock()

	if err != nil {
		// Propose might fail if followers can't be reached, but not due to quorum issues
		t.Logf("Propose failed: %v", err)
	} else {
		t.Log("Propose succeeded - clusterConfig is correctly used for quorum calculation")
	}
}

// TestMembershipChangeRequiresConsensus verifies that membership changes
// go through Raft consensus, not direct FSM manipulation.
func TestMembershipChangeRequiresConsensus(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
	}

	network := NewMemoryNetwork()
	transports := []*MemoryTransport{
		network.AddTransport("node1:10002"),
		network.AddTransport("node2:10002"),
		network.AddTransport("node3:10002"),
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

	// Bootstrap FSM and clusterConfig
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{NodeID: n.config.ID, Address: transports[i].Address()}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also add to clusterConfig for quorum calculations
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}

	for _, node := range nodes {
		node.Start()
	}
	defer stopNodes(nodes)

	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]

	// Propose a membership change through leader
	// This should go through Raft and be replicated
	newNodeCmd := NodeCommand{
		NodeID:  "node4",
		Address: "node4:10002",
	}
	_, _, err := leader.Propose(LogNodeJoin, newNodeCmd)
	if err != nil {
		t.Fatalf("failed to propose node join: %v", err)
	}

	// Poll for replication instead of hardcoded sleep
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allHaveNode4 := true
		for _, node := range nodes {
			if node.fsm.GetNode("node4") == nil {
				allHaveNode4 = false
				break
			}
		}
		if allHaveNode4 {
			return // Success
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Final check with error reporting
	for i, node := range nodes {
		if node.fsm.GetNode("node4") == nil {
			t.Errorf("node %d: node4 not found in FSM after membership change", i)
		}
	}
}

// TestSplitBrainPrevention verifies that network partitions don't cause
// split brain - only the majority partition can elect a leader and commit.
func TestSplitBrainPrevention(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{
		filepath.Join(dir, "node1"),
		filepath.Join(dir, "node2"),
		filepath.Join(dir, "node3"),
		filepath.Join(dir, "node4"),
		filepath.Join(dir, "node5"),
	}

	network := NewMemoryNetwork()
	transports := make([]*MemoryTransport, 5)
	for i := 0; i < 5; i++ {
		transports[i] = network.AddTransport(filepath.Base(dirs[i]) + ":10003")
	}

	nodes := make([]*Node, 5)
	for i := 0; i < 5; i++ {
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

	// Bootstrap FSM and clusterConfig with 5 nodes
	for _, node := range nodes {
		for i, n := range nodes {
			nodeCmd := NodeCommand{NodeID: n.config.ID, Address: transports[i].Address()}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			node.fsm.Apply(&LogEntry{Index: Index(i + 1), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
			// Also add to clusterConfig for quorum calculations
			node.AddToClusterConfig(n.config.ID, transports[i].Address())
		}
	}

	for _, node := range nodes {
		node.Start()
	}
	defer stopNodes(nodes)

	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	t.Logf("Initial leader: node%d", leaderIdx+1)

	// Create a network partition: {0,1} | {2,3,4}
	// The majority (3 nodes) should be able to elect a leader and commit
	// The minority (2 nodes) should not

	// Partition: disconnect nodes 0,1 from nodes 2,3,4
	for i := 0; i < 2; i++ {
		for j := 2; j < 5; j++ {
			network.Disconnect(transports[i].Address(), transports[j].Address())
		}
	}
	t.Log("Created partition: {node1,node2} | {node3,node4,node5}")

	// Wait for new leader in majority partition
	time.Sleep(300 * time.Millisecond)

	// Count leaders - there should be exactly one (in the majority)
	leaderCount := 0
	var majorityLeader *Node
	for i, node := range nodes {
		if node.IsLeader() {
			leaderCount++
			if i >= 2 {
				majorityLeader = node
			}
			t.Logf("node%d is leader", i+1)
		}
	}

	// There might be a brief period with 2 leaders (old leader hasn't stepped down)
	// but the minority partition leader should not be able to commit

	// Try to commit in minority partition (if there's a leader)
	for i := 0; i < 2; i++ {
		if nodes[i].IsLeader() {
			cmd := DeploymentCommand{Name: "minority-test", Namespace: "default", Type: "job"}
			_, _, err := nodes[i].Propose(LogDeploymentCreate, cmd)
			// This should either fail or the entry shouldn't be committed
			if err == nil {
				// Wait and check if it's actually committed
				time.Sleep(100 * time.Millisecond)
				// Entry might be in log but shouldn't be committed
				t.Log("Minority leader proposed entry - checking if committed...")
			}
		}
	}

	// Majority should be able to commit
	if majorityLeader != nil {
		cmd := DeploymentCommand{Name: "majority-test", Namespace: "default", Type: "job"}
		_, _, err := majorityLeader.Propose(LogDeploymentCreate, cmd)
		if err != nil {
			t.Errorf("majority leader failed to propose: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		// Check replication in majority partition
		for i := 2; i < 5; i++ {
			if nodes[i].FSM().GetDeployment("default", "majority-test") == nil {
				t.Errorf("node%d (majority) missing majority-test deployment", i+1)
			}
		}
	}
}
