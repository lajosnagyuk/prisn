package raft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cluster provides high-level cluster operations on top of Raft.
// Metadata operations go through Raft consensus.
// Execution reads from local state (hot path).
type Cluster struct {
	mu sync.RWMutex

	node      *Node
	transport *HTTPTransport
	config    ClusterConfig

	// Callbacks
	onDeploymentChange func(namespace, name string)
}

// ClusterOptions configures the cluster.
type ClusterOptions struct {
	NodeID   NodeID
	DataDir  string
	BindAddr string // Address to listen on for Raft RPCs
	Peers    []string // Initial peer addresses
}

// NewCluster creates a new cluster node.
func NewCluster(opts ClusterOptions) (*Cluster, error) {
	if opts.DataDir == "" {
		home, _ := os.UserHomeDir()
		opts.DataDir = filepath.Join(home, ".prisn", "raft")
	}

	if opts.BindAddr == "" {
		opts.BindAddr = ":7332" // Raft port
	}

	transport := NewHTTPTransport(opts.BindAddr)

	node, err := NewNode(Config{
		ID:      opts.NodeID,
		DataDir: opts.DataDir,
		Peers:   opts.Peers,
	}, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft node: %w", err)
	}

	transport.SetNode(node)

	c := &Cluster{
		node:      node,
		transport: transport,
		config: ClusterConfig{
			Nodes: make(map[NodeID]*NodeInfo),
		},
	}

	return c, nil
}

// Start starts the cluster node.
func (c *Cluster) Start() error {
	c.node.Start()
	return nil
}

// Stop stops the cluster node.
func (c *Cluster) Stop() error {
	return c.node.Stop()
}

// GracefulStop performs a graceful shutdown with optional leadership transfer.
// If this node is the leader, it will attempt to transfer leadership before
// shutting down to minimize cluster disruption.
func (c *Cluster) GracefulStop(drainTimeout time.Duration) error {
	return c.node.GracefulStop(true, drainTimeout)
}

// IsLeader returns true if this node is the cluster leader.
func (c *Cluster) IsLeader() bool {
	return c.node.IsLeader()
}

// Leader returns the ID of the current leader.
func (c *Cluster) Leader() NodeID {
	return c.node.Leader()
}

// NodeID returns this node's ID.
func (c *Cluster) NodeID() NodeID {
	return c.node.config.ID
}

// Transport returns the HTTP transport for registering handlers.
func (c *Cluster) Transport() *HTTPTransport {
	return c.transport
}

// OnDeploymentChange sets a callback for deployment changes.
func (c *Cluster) OnDeploymentChange(fn func(namespace, name string)) {
	c.mu.Lock()
	c.onDeploymentChange = fn
	c.mu.Unlock()
}

// --- Metadata Operations (through Raft) ---

// CreateDeployment creates a deployment through Raft consensus.
func (c *Cluster) CreateDeployment(cmd DeploymentCommand) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	_, _, err := c.node.Propose(LogDeploymentCreate, cmd)
	if err != nil {
		return fmt.Errorf("propose deployment: %w", err)
	}

	// Wait for commit (with timeout)
	return c.waitForApply(cmd.Namespace, cmd.Name, 5*time.Second)
}

// UpdateDeployment updates a deployment through Raft consensus.
func (c *Cluster) UpdateDeployment(cmd DeploymentCommand) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	_, _, err := c.node.Propose(LogDeploymentUpdate, cmd)
	return err
}

// DeleteDeployment deletes a deployment through Raft consensus.
func (c *Cluster) DeleteDeployment(namespace, name string) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := DeploymentCommand{
		Name:      name,
		Namespace: namespace,
	}

	_, _, err := c.node.Propose(LogDeploymentDelete, cmd)
	return err
}

// ActivateVersion activates a specific version through Raft consensus.
func (c *Cluster) ActivateVersion(namespace, name, version string) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := DeploymentCommand{
		Name:      name,
		Namespace: namespace,
		Version:   version,
	}

	_, _, err := c.node.Propose(LogDeploymentActivate, cmd)
	return err
}

// ScaleDeployment changes replica count through Raft consensus.
func (c *Cluster) ScaleDeployment(namespace, name string, replicas int) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := DeploymentCommand{
		Name:      name,
		Namespace: namespace,
		Replicas:  replicas,
	}

	_, _, err := c.node.Propose(LogScaleChange, cmd)
	return err
}

// AssignDeployment assigns a deployment to nodes through Raft consensus.
func (c *Cluster) AssignDeployment(namespace, name string, nodes []NodeID) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := AssignmentCommand{
		DeploymentName: name,
		Namespace:      namespace,
		Nodes:          nodes,
	}

	_, _, err := c.node.Propose(LogSchedulerAssign, cmd)
	return err
}

// JoinCluster requests to join the cluster.
func (c *Cluster) JoinCluster(nodeID NodeID, addr string) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := NodeCommand{
		NodeID:  nodeID,
		Address: addr,
	}

	_, _, err := c.node.Propose(LogNodeJoin, cmd)
	return err
}

// LeaveCluster removes a node from the cluster.
func (c *Cluster) LeaveCluster(nodeID NodeID) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}

	cmd := NodeCommand{
		NodeID: nodeID,
	}

	_, _, err := c.node.Propose(LogNodeLeave, cmd)
	return err
}

// TransferLeadership transfers leadership to the specified node.
// This enables zero-downtime upgrades by gracefully handing off leadership.
func (c *Cluster) TransferLeadership(targetID NodeID, timeout time.Duration) error {
	if !c.node.IsLeader() {
		return fmt.Errorf("not leader (leader is %s)", c.node.Leader())
	}
	return c.node.TransferLeadership(targetID, timeout)
}

// --- Read Operations (local, no Raft) ---

// GetDeployment returns deployment metadata (from local FSM state).
func (c *Cluster) GetDeployment(namespace, name string) *DeploymentMeta {
	return c.node.FSM().GetDeployment(namespace, name)
}

// ListDeployments returns all deployments (from local FSM state).
func (c *Cluster) ListDeployments(namespace string) []*DeploymentMeta {
	return c.node.FSM().ListDeployments(namespace)
}

// GetActiveVersion returns the active version for a deployment.
func (c *Cluster) GetActiveVersion(namespace, name string) string {
	return c.node.FSM().GetActiveVersion(namespace, name)
}

// GetAssignment returns which nodes a deployment is assigned to.
func (c *Cluster) GetAssignment(namespace, name string) []NodeID {
	return c.node.FSM().GetAssignment(namespace, name)
}

// ListNodes returns all cluster nodes.
func (c *Cluster) ListNodes() []*NodeInfo {
	return c.node.FSM().ListNodes()
}

// GetNode returns info for a specific node.
func (c *Cluster) GetNode(id NodeID) *NodeInfo {
	return c.node.FSM().GetNode(id)
}

// --- Internal ---

// waitForApply waits for a deployment to appear in the FSM.
func (c *Cluster) waitForApply(namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deployment to be applied")
		case <-ticker.C:
			if d := c.node.FSM().GetDeployment(namespace, name); d != nil {
				return nil
			}
		}
	}
}

// ClusterStatus returns the current cluster status.
type ClusterStatus struct {
	NodeID       NodeID     `json:"node_id"`
	State        NodeState  `json:"state"`
	Leader       NodeID     `json:"leader"`
	Term         Term       `json:"term"`
	Nodes        int        `json:"nodes"`
	Deployments  int        `json:"deployments"`
	CommitIndex  Index      `json:"commit_index"`
	LastApplied  Index      `json:"last_applied"`
}

func (c *Cluster) Status() ClusterStatus {
	fsm := c.node.FSM()
	fsm.mu.RLock()
	defer fsm.mu.RUnlock()

	return ClusterStatus{
		NodeID:      c.node.config.ID,
		State:       c.node.State(),
		Leader:      c.node.Leader(),
		Term:        c.node.Term(),
		Nodes:       len(fsm.Nodes),
		Deployments: len(fsm.Deployments),
		LastApplied: fsm.LastApplied,
	}
}

// Metrics returns detailed observability metrics for the cluster.
func (c *Cluster) Metrics() NodeMetrics {
	return c.node.Metrics()
}
