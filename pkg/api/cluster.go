package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lajosnagyuk/prisn/pkg/raft"
)

// ClusterServer extends Server with cluster operations.
type ClusterServer struct {
	*Server
	cluster *raft.Cluster
}

// NewClusterServer creates a server with cluster support.
func NewClusterServer(cfg Config, cluster *raft.Cluster) (*ClusterServer, error) {
	srv, err := NewServer(cfg)
	if err != nil {
		return nil, err
	}

	cs := &ClusterServer{
		Server:  srv,
		cluster: cluster,
	}

	// Register Raft transport handlers
	mux := http.NewServeMux()

	// Health and metrics
	mux.HandleFunc("GET /health", cs.handleClusterHealth)
	mux.HandleFunc("GET /metrics", cs.handleClusterMetrics)

	// Cluster API
	mux.HandleFunc("GET /api/v1/cluster/status", cs.handleClusterStatus)
	mux.HandleFunc("GET /api/v1/cluster/nodes", cs.handleListNodes)
	mux.HandleFunc("POST /api/v1/cluster/join", cs.handleJoinCluster)
	mux.HandleFunc("POST /api/v1/cluster/leave", cs.handleLeaveCluster)

	// Deployments API (through Raft for writes, local for reads)
	mux.HandleFunc("GET /api/v1/deployments", cs.handleListDeploymentsCluster)
	mux.HandleFunc("POST /api/v1/deployments", cs.handleCreateDeploymentCluster)
	mux.HandleFunc("GET /api/v1/deployments/{id}", cs.handleGetDeploymentCluster)
	mux.HandleFunc("DELETE /api/v1/deployments/{id}", cs.handleDeleteDeploymentCluster)
	mux.HandleFunc("POST /api/v1/deployments/{id}/scale", cs.handleScaleDeploymentCluster)
	mux.HandleFunc("POST /api/v1/deployments/{id}/rollback", cs.handleRollbackDeployment)

	// Executions API (local - hot path)
	mux.HandleFunc("GET /api/v1/executions", srv.handleListExecutions)
	mux.HandleFunc("POST /api/v1/executions", srv.handleCreateExecution)
	mux.HandleFunc("GET /api/v1/executions/{id}", srv.handleGetExecution)
	mux.HandleFunc("GET /api/v1/executions/{id}/logs", srv.handleExecutionLogs)

	// Secrets API (local for now)
	mux.HandleFunc("GET /api/v1/secrets", srv.handleListSecrets)
	mux.HandleFunc("POST /api/v1/secrets", srv.handleCreateSecret)
	mux.HandleFunc("GET /api/v1/secrets/{name}", srv.handleGetSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{name}", srv.handleDeleteSecret)

	// Register Raft RPC handlers
	cluster.Transport().RegisterHandlers(mux)

	cs.Server.server.Handler = cs.middleware(mux)

	return cs, nil
}

// handleClusterHealth returns cluster health status.
func (cs *ClusterServer) handleClusterHealth(w http.ResponseWriter, r *http.Request) {
	status := cs.cluster.Status()

	healthy := status.State == raft.Leader || status.Leader != ""

	json.NewEncoder(w).Encode(map[string]any{
		"status":      boolToStatus(healthy),
		"node_id":     status.NodeID,
		"state":       status.State.String(),
		"leader":      status.Leader,
		"term":        status.Term,
		"nodes":       status.Nodes,
		"deployments": status.Deployments,
	})
}

func boolToStatus(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "unhealthy"
}

// handleClusterStatus returns detailed cluster status.
func (cs *ClusterServer) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	status := cs.cluster.Status()
	json.NewEncoder(w).Encode(status)
}

// handleListNodes returns all cluster nodes.
func (cs *ClusterServer) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes := cs.cluster.ListNodes()
	json.NewEncoder(w).Encode(map[string]any{
		"items": nodes,
		"count": len(nodes),
	})
}

// JoinRequest is the request body for joining a cluster.
type JoinRequest struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
}

func (cs *ClusterServer) handleJoinCluster(w http.ResponseWriter, r *http.Request) {
	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.NodeID == "" || req.Address == "" {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("node_id and address are required"))
		return
	}

	if err := cs.cluster.JoinCluster(raft.NodeID(req.NodeID), req.Address); err != nil {
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"message": "node joined cluster",
		"node_id": req.NodeID,
	})
}

// LeaveRequest is the request body for leaving a cluster.
type LeaveRequest struct {
	NodeID string `json:"node_id"`
}

func (cs *ClusterServer) handleLeaveCluster(w http.ResponseWriter, r *http.Request) {
	var req LeaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	nodeID := raft.NodeID(req.NodeID)
	if nodeID == "" {
		nodeID = cs.cluster.NodeID() // Leave self
	}

	if err := cs.cluster.LeaveCluster(nodeID); err != nil {
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"message": "node left cluster",
		"node_id": nodeID,
	})
}

// --- Deployment operations through Raft ---

func (cs *ClusterServer) handleListDeploymentsCluster(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")

	// Read from local FSM (fast, no Raft)
	deployments := cs.cluster.ListDeployments(namespace)

	json.NewEncoder(w).Encode(map[string]any{
		"items": deployments,
		"count": len(deployments),
	})
}

// CreateDeploymentClusterRequest extends CreateDeploymentRequest with versioning.
type CreateDeploymentClusterRequest struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Type        string            `json:"type"`
	Version     string            `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	Source      string            `json:"source"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Port        int               `json:"port,omitempty"`
	Replicas    int               `json:"replicas"`
	Schedule    string            `json:"schedule,omitempty"`
	Size        int64             `json:"size"`
	FileCount   int               `json:"file_count"`
}

func (cs *ClusterServer) handleCreateDeploymentCluster(w http.ResponseWriter, r *http.Request) {
	var req CreateDeploymentClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.Name == "" {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}

	// Set defaults
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Replicas == 0 {
		req.Replicas = 1
	}

	// Create through Raft consensus
	cmd := raft.DeploymentCommand{
		Name:        req.Name,
		Namespace:   req.Namespace,
		Version:     req.Version,
		Fingerprint: req.Fingerprint,
		SourcePath:  req.Source,
		Type:        req.Type,
		Replicas:    req.Replicas,
		Port:        req.Port,
		Schedule:    req.Schedule,
		Env:         req.Env,
		Size:        req.Size,
		FileCount:   req.FileCount,
	}

	if err := cs.cluster.CreateDeployment(cmd); err != nil {
		// Check if not leader
		if !cs.cluster.IsLeader() {
			cs.writeError(w, http.StatusServiceUnavailable, fmt.Errorf("not leader, redirect to %s", cs.cluster.Leader()))
			return
		}
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"name":        req.Name,
		"namespace":   req.Namespace,
		"version":     req.Version,
		"message":     "deployment created",
		"assigned_to": cs.cluster.GetAssignment(req.Namespace, req.Name),
	})
}

func (cs *ClusterServer) handleGetDeploymentCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Parse namespace/name from id (namespace/name format supported in future)
	namespace := "default"
	name := id

	d := cs.cluster.GetDeployment(namespace, name)
	if d == nil {
		cs.writeError(w, http.StatusNotFound, fmt.Errorf("deployment not found: %s", id))
		return
	}

	json.NewEncoder(w).Encode(d)
}

func (cs *ClusterServer) handleDeleteDeploymentCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	if err := cs.cluster.DeleteDeployment(namespace, id); err != nil {
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (cs *ClusterServer) handleScaleDeploymentCluster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var req ScaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if err := cs.cluster.ScaleDeployment(namespace, id, req.Replicas); err != nil {
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"name":     id,
		"replicas": req.Replicas,
		"message":  fmt.Sprintf("scaled to %d replicas", req.Replicas),
	})
}

// RollbackRequest is the request body for rollback.
type RollbackRequest struct {
	Version string `json:"version"` // Target version (empty = previous)
}

func (cs *ClusterServer) handleRollbackDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var req RollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Get deployment to find target version
	d := cs.cluster.GetDeployment(namespace, id)
	if d == nil {
		cs.writeError(w, http.StatusNotFound, fmt.Errorf("deployment not found: %s", id))
		return
	}

	targetVersion := req.Version
	if targetVersion == "" {
		// Find previous version
		if len(d.Versions) < 2 {
			cs.writeError(w, http.StatusBadRequest, fmt.Errorf("no previous version to rollback to"))
			return
		}
		// Find the non-active version
		for _, v := range d.Versions {
			if !v.Active {
				targetVersion = v.ID
				break
			}
		}
	}

	if err := cs.cluster.ActivateVersion(namespace, id, targetVersion); err != nil {
		cs.writeError(w, http.StatusInternalServerError, err)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"name":            id,
		"previous_version": cs.cluster.GetActiveVersion(namespace, id),
		"new_version":     targetVersion,
		"message":         "rolled back successfully",
	})
}

// handleClusterMetrics returns Prometheus metrics including Raft cluster metrics.
func (cs *ClusterServer) handleClusterMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := cs.cluster.Metrics()

	// Prometheus text format
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Raft node info
	fmt.Fprintf(w, "# HELP prisn_raft_info Raft node information\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_info gauge\n")
	fmt.Fprintf(w, "prisn_raft_info{node_id=\"%s\",state=\"%s\",leader=\"%s\"} 1\n",
		metrics.NodeID, metrics.State.String(), metrics.Leader)
	fmt.Fprintf(w, "\n")

	// Raft term
	fmt.Fprintf(w, "# HELP prisn_raft_term Current Raft term\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_term gauge\n")
	fmt.Fprintf(w, "prisn_raft_term %d\n", metrics.Term)
	fmt.Fprintf(w, "\n")

	// Is leader
	fmt.Fprintf(w, "# HELP prisn_raft_is_leader Whether this node is the leader (1=leader, 0=not leader)\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_is_leader gauge\n")
	isLeader := 0
	if metrics.State == raft.Leader {
		isLeader = 1
	}
	fmt.Fprintf(w, "prisn_raft_is_leader %d\n", isLeader)
	fmt.Fprintf(w, "\n")

	// Log indices
	fmt.Fprintf(w, "# HELP prisn_raft_log_first_index First index in the Raft log\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_log_first_index gauge\n")
	fmt.Fprintf(w, "prisn_raft_log_first_index %d\n", metrics.LogFirstIndex)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_log_last_index Last index in the Raft log\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_log_last_index gauge\n")
	fmt.Fprintf(w, "prisn_raft_log_last_index %d\n", metrics.LogLastIndex)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_log_entries Number of entries in the Raft log\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_log_entries gauge\n")
	fmt.Fprintf(w, "prisn_raft_log_entries %d\n", metrics.LogLength)
	fmt.Fprintf(w, "\n")

	// Commit and apply indices
	fmt.Fprintf(w, "# HELP prisn_raft_commit_index Current commit index\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_commit_index gauge\n")
	fmt.Fprintf(w, "prisn_raft_commit_index %d\n", metrics.CommitIndex)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_last_applied_index Last applied index\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_last_applied_index gauge\n")
	fmt.Fprintf(w, "prisn_raft_last_applied_index %d\n", metrics.LastApplied)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_entries_behind Number of entries waiting to be applied\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_entries_behind gauge\n")
	fmt.Fprintf(w, "prisn_raft_entries_behind %d\n", metrics.EntriesBehind)
	fmt.Fprintf(w, "\n")

	// Snapshot
	fmt.Fprintf(w, "# HELP prisn_raft_snapshot_index Index of last snapshot\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_snapshot_index gauge\n")
	fmt.Fprintf(w, "prisn_raft_snapshot_index %d\n", metrics.SnapshotIndex)
	fmt.Fprintf(w, "\n")

	// Cluster size
	fmt.Fprintf(w, "# HELP prisn_raft_cluster_size Number of nodes in the cluster\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_cluster_size gauge\n")
	fmt.Fprintf(w, "prisn_raft_cluster_size %d\n", metrics.ClusterSize)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_quorum_size Number of nodes needed for quorum\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_quorum_size gauge\n")
	fmt.Fprintf(w, "prisn_raft_quorum_size %d\n", metrics.QuorumSize)
	fmt.Fprintf(w, "\n")

	// FSM state
	fmt.Fprintf(w, "# HELP prisn_raft_deployments Number of deployments in the state machine\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_deployments gauge\n")
	fmt.Fprintf(w, "prisn_raft_deployments %d\n", metrics.Deployments)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_raft_nodes Number of nodes tracked in the state machine\n")
	fmt.Fprintf(w, "# TYPE prisn_raft_nodes gauge\n")
	fmt.Fprintf(w, "prisn_raft_nodes %d\n", metrics.Nodes)
	fmt.Fprintf(w, "\n")

	// Per-peer replication metrics (only on leader)
	if len(metrics.Peers) > 0 {
		fmt.Fprintf(w, "# HELP prisn_raft_peer_match_index Match index for each peer\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_match_index gauge\n")
		for _, peer := range metrics.Peers {
			fmt.Fprintf(w, "prisn_raft_peer_match_index{peer=\"%s\"} %d\n", peer.NodeID, peer.MatchIndex)
		}
		fmt.Fprintf(w, "\n")

		fmt.Fprintf(w, "# HELP prisn_raft_peer_next_index Next index for each peer\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_next_index gauge\n")
		for _, peer := range metrics.Peers {
			fmt.Fprintf(w, "prisn_raft_peer_next_index{peer=\"%s\"} %d\n", peer.NodeID, peer.NextIndex)
		}
		fmt.Fprintf(w, "\n")

		fmt.Fprintf(w, "# HELP prisn_raft_peer_lag_entries Number of entries peer is behind\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_lag_entries gauge\n")
		for _, peer := range metrics.Peers {
			fmt.Fprintf(w, "prisn_raft_peer_lag_entries{peer=\"%s\"} %d\n", peer.NodeID, peer.LagEntries)
		}
		fmt.Fprintf(w, "\n")

		fmt.Fprintf(w, "# HELP prisn_raft_peer_last_contact_ms Milliseconds since last successful contact\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_last_contact_ms gauge\n")
		for _, peer := range metrics.Peers {
			fmt.Fprintf(w, "prisn_raft_peer_last_contact_ms{peer=\"%s\"} %d\n", peer.NodeID, peer.LastContactMs)
		}
		fmt.Fprintf(w, "\n")

		fmt.Fprintf(w, "# HELP prisn_raft_peer_failed_rpcs Consecutive failed RPCs to peer\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_failed_rpcs gauge\n")
		for _, peer := range metrics.Peers {
			fmt.Fprintf(w, "prisn_raft_peer_failed_rpcs{peer=\"%s\"} %d\n", peer.NodeID, peer.FailedRPCs)
		}
		fmt.Fprintf(w, "\n")

		fmt.Fprintf(w, "# HELP prisn_raft_peer_state Peer replication state (1=synced, 0=behind, -1=unreachable)\n")
		fmt.Fprintf(w, "# TYPE prisn_raft_peer_state gauge\n")
		for _, peer := range metrics.Peers {
			stateVal := 0
			switch peer.State {
			case raft.PeerSynced:
				stateVal = 1
			case raft.PeerBehind:
				stateVal = 0
			case raft.PeerUnreachable:
				stateVal = -1
			}
			fmt.Fprintf(w, "prisn_raft_peer_state{peer=\"%s\",state=\"%s\"} %d\n", peer.NodeID, peer.State, stateVal)
		}
	}
}
