// Package raft implements a lightweight Raft consensus protocol for cluster coordination.
// This is optimized for prisn's use case: small metadata operations, not bulk data.
package raft

import (
	"encoding/json"
	"time"
)

// NodeID is a unique identifier for a cluster node.
type NodeID string

// Term is a Raft term number (increases on each election).
type Term uint64

// Index is a log entry index (1-based, 0 means no entry).
type Index uint64

// NodeState represents the current role of a node.
type NodeState uint8

const (
	Follower NodeState = iota
	Candidate
	Leader
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "unknown"
	}
}

// LogEntryType identifies the type of state machine command.
type LogEntryType uint8

const (
	LogNoop LogEntryType = iota
	LogDeploymentCreate
	LogDeploymentUpdate
	LogDeploymentDelete
	LogDeploymentActivate
	LogSchedulerAssign
	LogSchedulerUnassign
	LogNodeJoin
	LogNodeLeave
	LogScaleChange
	LogSecretCreate
	LogSecretDelete
)

// LogEntry is a single entry in the Raft log.
type LogEntry struct {
	Index   Index        `json:"index"`
	Term    Term         `json:"term"`
	Type    LogEntryType `json:"type"`
	Command []byte       `json:"command"` // JSON-encoded command data
}

// PersistentState is the state that must survive restarts.
type PersistentState struct {
	CurrentTerm   Term           `json:"current_term"`
	VotedFor      NodeID         `json:"voted_for"`       // Empty if not voted this term
	ClusterConfig *ClusterConfig `json:"cluster_config"`  // Membership configuration
}

// VolatileState is state that can be reconstructed.
type VolatileState struct {
	CommitIndex Index // Highest log entry known to be committed
	LastApplied Index // Highest log entry applied to state machine
}

// LeaderState is state only maintained by leaders.
type LeaderState struct {
	NextIndex  map[NodeID]Index // For each server, index of next log entry to send
	MatchIndex map[NodeID]Index // For each server, index of highest log entry known to be replicated
}

// NodeInfo represents a cluster member.
type NodeInfo struct {
	ID       NodeID    `json:"id"`
	Address  string    `json:"address"`   // host:port for RPC
	JoinedAt time.Time `json:"joined_at"`
	LastSeen time.Time `json:"last_seen"`
	State    NodeState `json:"state"`
}

// ClusterConfig holds cluster-wide configuration.
type ClusterConfig struct {
	Nodes map[NodeID]*NodeInfo `json:"nodes"`
}

// RequestVoteArgs is the RPC argument for RequestVote.
type RequestVoteArgs struct {
	Term         Term   `json:"term"`
	CandidateID  NodeID `json:"candidate_id"`
	LastLogIndex Index  `json:"last_log_index"`
	LastLogTerm  Term   `json:"last_log_term"`
}

// RequestVoteReply is the RPC reply for RequestVote.
type RequestVoteReply struct {
	Term        Term `json:"term"`
	VoteGranted bool `json:"vote_granted"`
}

// AppendEntriesArgs is the RPC argument for AppendEntries.
type AppendEntriesArgs struct {
	Term         Term       `json:"term"`
	LeaderID     NodeID     `json:"leader_id"`
	PrevLogIndex Index      `json:"prev_log_index"`
	PrevLogTerm  Term       `json:"prev_log_term"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit Index      `json:"leader_commit"`
}

// AppendEntriesReply is the RPC reply for AppendEntries.
type AppendEntriesReply struct {
	Term          Term  `json:"term"`
	Success       bool  `json:"success"`
	ConflictIndex Index `json:"conflict_index"` // For fast log backtracking
	ConflictTerm  Term  `json:"conflict_term"`
}

// InstallSnapshotArgs is the RPC argument for InstallSnapshot.
// Used when a follower is too far behind to catch up via log replication.
type InstallSnapshotArgs struct {
	Term              Term   `json:"term"`
	LeaderID          NodeID `json:"leader_id"`
	LastIncludedIndex Index  `json:"last_included_index"` // Last log index in snapshot
	LastIncludedTerm  Term   `json:"last_included_term"`  // Term of last log index
	Data              []byte `json:"data"`                // Snapshot data (FSM state)
	// ClusterConfig is included so follower can update its membership config
	ClusterConfig *ClusterConfig `json:"cluster_config,omitempty"`
}

// InstallSnapshotReply is the RPC reply for InstallSnapshot.
type InstallSnapshotReply struct {
	Term Term `json:"term"`
}

// PreVoteArgs is the RPC argument for PreVote.
// PreVote is used before starting a real election to check if we would win.
// This prevents partitioned nodes from disrupting stable clusters.
type PreVoteArgs struct {
	Term         Term   `json:"term"`          // The term the candidate would use (current + 1)
	CandidateID  NodeID `json:"candidate_id"`
	LastLogIndex Index  `json:"last_log_index"`
	LastLogTerm  Term   `json:"last_log_term"`
}

// PreVoteReply is the RPC reply for PreVote.
type PreVoteReply struct {
	Term        Term `json:"term"`
	VoteGranted bool `json:"vote_granted"`
}

// TimeoutNowArgs is the RPC argument for TimeoutNow.
// Sent by leader to trigger immediate election on target for leadership transfer.
type TimeoutNowArgs struct {
	Term     Term   `json:"term"`
	LeaderID NodeID `json:"leader_id"`
}

// TimeoutNowReply is the RPC reply for TimeoutNow.
type TimeoutNowReply struct {
	Term    Term `json:"term"`
	Success bool `json:"success"`
}

// Command types for the state machine

// DeploymentCommand is for deployment-related log entries.
type DeploymentCommand struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Version     string            `json:"version"`
	Fingerprint string            `json:"fingerprint"` // xxhash64 of contents
	SourcePath  string            `json:"source_path"`
	Type        string            `json:"type"`        // service, cronjob, job
	Replicas    int               `json:"replicas"`
	Port        int               `json:"port,omitempty"`
	Schedule    string            `json:"schedule,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Size        int64             `json:"size"`
	FileCount   int               `json:"file_count"`
}

// AssignmentCommand assigns a deployment to nodes.
type AssignmentCommand struct {
	DeploymentName string   `json:"deployment_name"`
	Namespace      string   `json:"namespace"`
	Nodes          []NodeID `json:"nodes"`
	ReplicaCount   int      `json:"replica_count"`
}

// NodeCommand is for node join/leave operations.
type NodeCommand struct {
	NodeID  NodeID `json:"node_id"`
	Address string `json:"address"`
}

// MarshalCommand encodes a command for the log.
func MarshalCommand(cmd any) ([]byte, error) {
	return json.Marshal(cmd)
}

// UnmarshalCommand decodes a command from the log.
func UnmarshalCommand(data []byte, cmd any) error {
	return json.Unmarshal(data, cmd)
}

// PeerState indicates whether a peer is synced, behind, or unreachable.
type PeerState string

const (
	PeerSynced      PeerState = "synced"
	PeerBehind      PeerState = "behind"
	PeerUnreachable PeerState = "unreachable"
)

// PeerMetrics contains replication metrics for a single peer.
type PeerMetrics struct {
	NodeID          NodeID    `json:"node_id"`
	Address         string    `json:"address"`
	State           PeerState `json:"state"`
	NextIndex       Index     `json:"next_index"`
	MatchIndex      Index     `json:"match_index"`
	LagEntries      Index     `json:"lag_entries"`       // How many entries behind
	LastContactMs   int64     `json:"last_contact_ms"`   // Time since last successful RPC
	FailedRPCs      int       `json:"failed_rpcs"`       // Consecutive failed RPCs
	InflightEntries int       `json:"inflight_entries"`  // Entries sent but not acked
}

// NodeMetrics contains observability metrics for a Raft node.
type NodeMetrics struct {
	// Identity
	NodeID  NodeID    `json:"node_id"`
	State   NodeState `json:"state"`
	Leader  NodeID    `json:"leader"`
	Term    Term      `json:"term"`

	// Log metrics
	LogFirstIndex    Index `json:"log_first_index"`
	LogLastIndex     Index `json:"log_last_index"`
	LogLength        int   `json:"log_length"`
	CommitIndex      Index `json:"commit_index"`
	LastApplied      Index `json:"last_applied"`
	SnapshotIndex    Index `json:"snapshot_index"`
	EntriesBehind    Index `json:"entries_behind"` // CommitIndex - LastApplied

	// Cluster metrics
	ClusterSize int `json:"cluster_size"`
	QuorumSize  int `json:"quorum_size"`

	// FSM metrics
	Deployments int `json:"deployments"`
	Nodes       int `json:"nodes"`

	// Timing (in milliseconds for Prometheus)
	LastHeartbeatMs int64 `json:"last_heartbeat_ms,omitempty"` // Leader only

	// Per-peer metrics (only on leader)
	Peers []PeerMetrics `json:"peers,omitempty"`
}
