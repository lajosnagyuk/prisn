package raft

import (
	"path/filepath"
	"testing"
	"time"
)

// TestEndToEndClusterFlow tests the complete cluster lifecycle:
// 1. Bootstrap first node
// 2. Join additional nodes
// 3. Leader election
// 4. Data replication
// 5. Leader failover
// 6. Data persistence
func TestEndToEndClusterFlow(t *testing.T) {
	dir := t.TempDir()

	// Create 3-node cluster with memory transport
	network := NewMemoryNetwork()
	transports := make([]*MemoryTransport, 3)
	nodes := make([]*Node, 3)

	for i := 0; i < 3; i++ {
		nodeID := NodeID("node" + string(rune('1'+i)))
		addr := string(nodeID) + ":7332"
		transports[i] = network.AddTransport(addr)

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

	// Step 1: Bootstrap first node
	t.Log("Step 1: Bootstrapping first node")
	bootstrapCluster(nodes[:1], transports[:1])
	nodes[0].Start()

	// Wait for node1 to become leader (single-node cluster)
	time.Sleep(200 * time.Millisecond)
	if !nodes[0].IsLeader() {
		t.Fatal("node1 should be leader after bootstrap")
	}
	t.Log("node1 is leader")

	// Step 2: Join additional nodes
	t.Log("Step 2: Joining additional nodes")

	// Add nodes 2 and 3 to the cluster via leader
	for i := 1; i < 3; i++ {
		cmd := NodeCommand{
			NodeID:  nodes[i].config.ID,
			Address: transports[i].Address(),
		}
		_, _, err := nodes[0].Propose(LogNodeJoin, cmd)
		if err != nil {
			t.Fatalf("failed to propose node join for node%d: %v", i+1, err)
		}
	}

	// Wait for membership changes to replicate
	time.Sleep(100 * time.Millisecond)

	// Now bootstrap the new nodes with cluster info and start them
	for i := 1; i < 3; i++ {
		// Copy cluster config from leader
		for id, info := range nodes[0].clusterConfig.Nodes {
			nodes[i].AddToClusterConfig(id, info.Address)
		}
		// Also copy FSM nodes
		for id, info := range nodes[0].fsm.Nodes {
			nodeCmd := NodeCommand{NodeID: id, Address: info.Address}
			nodeBytes, _ := MarshalCommand(nodeCmd)
			nodes[i].fsm.Apply(&LogEntry{Index: Index(id[4] - '0'), Term: 1, Type: LogNodeJoin, Command: nodeBytes})
		}
		nodes[i].Start()
	}

	// Wait for all nodes to be running
	time.Sleep(200 * time.Millisecond)

	// Verify leader
	var leaderIdx int
	waitForLeader(t, nodes, &leaderIdx, 2*time.Second)
	leader := nodes[leaderIdx]
	t.Logf("Leader: %s", leader.config.ID)

	// Step 3: Verify data replication
	t.Log("Step 3: Verifying data replication")

	// Create some deployments through the leader
	for i := 0; i < 5; i++ {
		cmd := DeploymentCommand{
			Name:      deployNameFromInt(i),
			Namespace: "default",
			Type:      "job",
			Replicas:  i + 1,
		}
		_, _, err := leader.Propose(LogDeploymentCreate, cmd)
		if err != nil {
			t.Fatalf("failed to propose deployment %d: %v", i, err)
		}
	}

	// Wait for replication
	time.Sleep(300 * time.Millisecond)

	// Verify all nodes have the deployments
	for i, node := range nodes {
		for j := 0; j < 5; j++ {
			d := node.FSM().GetDeployment("default", deployNameFromInt(j))
			if d == nil {
				t.Errorf("node%d: missing deployment %s", i+1, deployNameFromInt(j))
			} else if d.Replicas != j+1 {
				t.Errorf("node%d: deployment %s has wrong replicas: got %d, want %d",
					i+1, deployNameFromInt(j), d.Replicas, j+1)
			}
		}
	}
	t.Log("All 5 deployments replicated to all 3 nodes")

	// Step 4: Test leader failover
	t.Log("Step 4: Testing leader failover")

	// Stop the current leader
	leader.Stop()
	t.Logf("Stopped leader %s", leader.config.ID)

	// Wait for new leader election
	time.Sleep(300 * time.Millisecond)

	// Find new leader among remaining nodes
	var newLeader *Node
	for i, node := range nodes {
		if i == leaderIdx {
			continue
		}
		if node.IsLeader() {
			newLeader = node
			t.Logf("New leader elected: %s", node.config.ID)
			break
		}
	}

	if newLeader == nil {
		t.Fatal("no new leader elected after failover")
	}

	// Step 5: Verify data still accessible through new leader
	t.Log("Step 5: Verifying data accessible through new leader")

	for j := 0; j < 5; j++ {
		d := newLeader.FSM().GetDeployment("default", deployNameFromInt(j))
		if d == nil {
			t.Errorf("new leader: missing deployment %s", deployNameFromInt(j))
		}
	}

	// Create a new deployment through new leader
	cmd := DeploymentCommand{
		Name:      "post-failover",
		Namespace: "default",
		Type:      "service",
		Replicas:  3,
	}
	_, _, err := newLeader.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		t.Fatalf("failed to propose through new leader: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify new deployment replicated to surviving node
	for i, node := range nodes {
		if i == leaderIdx {
			continue
		}
		d := node.FSM().GetDeployment("default", "post-failover")
		if d == nil {
			t.Errorf("node%d: missing post-failover deployment", i+1)
		}
	}

	// Cleanup
	for i, node := range nodes {
		if i != leaderIdx {
			node.Stop()
		}
	}

	t.Log("End-to-end cluster test completed successfully")
}

// TestClusterPersistence tests that cluster state survives restarts.
func TestClusterPersistence(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: Create cluster and add data
	t.Log("Phase 1: Creating cluster and adding data")

	network := NewMemoryNetwork()
	transport := network.AddTransport("node1:7332")

	cfg := Config{
		ID:                "node1",
		DataDir:           filepath.Join(dir, "node1"),
		HeartbeatInterval: 10 * time.Millisecond,
		ElectionTimeout:   50 * time.Millisecond,
	}

	node, err := NewNode(cfg, transport)
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	transport.SetNode(node)

	// Bootstrap
	node.AddToClusterConfig("node1", "node1:7332")
	node.fsm.Apply(&LogEntry{Index: 1, Term: 1, Type: LogNodeJoin,
		Command: []byte(`{"node_id":"node1","address":"node1:7332"}`)})

	node.Start()
	time.Sleep(200 * time.Millisecond)

	if !node.IsLeader() {
		t.Fatal("node should be leader")
	}

	// Create some data
	for i := 0; i < 3; i++ {
		cmd := DeploymentCommand{
			Name:      deployNameFromInt(i) + "-persist",
			Namespace: "default",
			Type:      "job",
		}
		node.Propose(LogDeploymentCreate, cmd)
	}

	time.Sleep(100 * time.Millisecond)

	// Save snapshot
	node.fsm.SaveSnapshot(filepath.Join(cfg.DataDir, "snapshots"))

	// Get term before stopping
	originalTerm := node.Term()
	t.Logf("Original term: %d", originalTerm)

	node.Stop()
	t.Log("Node stopped")

	// Phase 2: Restart and verify data persisted
	t.Log("Phase 2: Restarting and verifying data")

	// Create new network/transport (simulating restart)
	network2 := NewMemoryNetwork()
	transport2 := network2.AddTransport("node1:7332")

	node2, err := NewNode(cfg, transport2)
	if err != nil {
		t.Fatalf("failed to recreate node: %v", err)
	}
	transport2.SetNode(node2)

	// Re-bootstrap cluster config
	node2.AddToClusterConfig("node1", "node1:7332")

	node2.Start()
	defer node2.Stop()

	time.Sleep(200 * time.Millisecond)

	// Verify term preserved (or incremented)
	if node2.Term() < originalTerm {
		t.Errorf("term should be preserved: got %d, want >= %d", node2.Term(), originalTerm)
	}

	// Verify data persisted
	for i := 0; i < 3; i++ {
		name := deployNameFromInt(i) + "-persist"
		d := node2.FSM().GetDeployment("default", name)
		if d == nil {
			t.Errorf("deployment %s not persisted", name)
		}
	}

	t.Log("Persistence test completed successfully")
}

// TestClusterScaleDeployment tests scaling a deployment through Raft consensus.
func TestClusterScaleDeployment(t *testing.T) {
	dir := t.TempDir()

	network := NewMemoryNetwork()
	transports := make([]*MemoryTransport, 3)
	nodes := make([]*Node, 3)

	for i := 0; i < 3; i++ {
		nodeID := NodeID("node" + string(rune('1'+i)))
		addr := string(nodeID) + ":7332"
		transports[i] = network.AddTransport(addr)

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

	// Create a deployment
	createCmd := DeploymentCommand{
		Name:      "scalable-app",
		Namespace: "default",
		Type:      "service",
		Replicas:  1,
	}
	_, _, err := leader.Propose(LogDeploymentCreate, createCmd)
	if err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Poll for deployment to be created
	var d *DeploymentMeta
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d = leader.FSM().GetDeployment("default", "scalable-app")
		if d != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Verify initial replicas
	if d == nil {
		t.Fatal("deployment not found")
	}
	if d.Replicas != 1 {
		t.Errorf("initial replicas: got %d, want 1", d.Replicas)
	}

	// Scale up
	scaleCmd := DeploymentCommand{
		Name:      "scalable-app",
		Namespace: "default",
		Replicas:  5,
	}
	_, _, err = leader.Propose(LogScaleChange, scaleCmd)
	if err != nil {
		t.Fatalf("failed to scale deployment: %v", err)
	}

	// Poll for scale replication to all nodes
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allScaled := true
		for _, node := range nodes {
			d := node.FSM().GetDeployment("default", "scalable-app")
			if d == nil || d.Replicas != 5 {
				allScaled = false
				break
			}
		}
		if allScaled {
			t.Log("Scale test completed successfully")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Final check with error reporting
	for i, node := range nodes {
		d := node.FSM().GetDeployment("default", "scalable-app")
		if d == nil {
			t.Errorf("node%d: deployment not found", i+1)
			continue
		}
		if d.Replicas != 5 {
			t.Errorf("node%d: replicas after scale: got %d, want 5", i+1, d.Replicas)
		}
	}

	t.Log("Scale test completed successfully")
}
