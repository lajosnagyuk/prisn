package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FSM is the finite state machine that processes committed log entries.
type FSM interface {
	// Apply applies a committed log entry to the state machine.
	Apply(entry *LogEntry) error

	// Snapshot returns a snapshot of the current state for compaction.
	Snapshot() ([]byte, error)

	// Restore restores state from a snapshot.
	Restore(data []byte) error
}

// ClusterState is the in-memory representation of cluster state.
// This is what gets replicated via Raft consensus.
type ClusterState struct {
	mu sync.RWMutex

	// Deployment metadata (not the files, just the metadata)
	Deployments map[string]*DeploymentMeta `json:"deployments"` // key: namespace/name

	// Node membership
	Nodes map[NodeID]*NodeInfo `json:"nodes"`

	// Deployment -> Node assignments
	Assignments map[string][]NodeID `json:"assignments"` // key: namespace/name

	// Active version for each deployment
	ActiveVersions map[string]string `json:"active_versions"` // key: namespace/name -> version

	// Last applied index
	LastApplied Index `json:"last_applied"`
}

// DeploymentMeta is the metadata for a deployment stored in Raft.
type DeploymentMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Type        string            `json:"type"` // service, cronjob, job
	Runtime     string            `json:"runtime"`
	Replicas    int               `json:"replicas"`
	Port        int               `json:"port,omitempty"`
	Schedule    string            `json:"schedule,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Versions    []VersionMeta     `json:"versions"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// VersionMeta is metadata for a specific version.
type VersionMeta struct {
	ID          string    `json:"id"`          // e.g., v-a1b2c3d4
	Fingerprint string    `json:"fingerprint"` // xxhash64
	Size        int64     `json:"size"`
	FileCount   int       `json:"file_count"`
	CreatedAt   time.Time `json:"created_at"`
	Active      bool      `json:"active"`
}

// NewClusterState creates a new cluster state.
func NewClusterState() *ClusterState {
	return &ClusterState{
		Deployments:    make(map[string]*DeploymentMeta),
		Nodes:          make(map[NodeID]*NodeInfo),
		Assignments:    make(map[string][]NodeID),
		ActiveVersions: make(map[string]string),
	}
}

// Key returns the map key for a deployment.
func deploymentKey(namespace, name string) string {
	return namespace + "/" + name
}

// Apply implements FSM.
func (s *ClusterState) Apply(entry *LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch entry.Type {
	case LogNoop:
		// No-op, just updates last applied

	case LogDeploymentCreate, LogDeploymentUpdate:
		var cmd DeploymentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal deployment command: %w", err)
		}

		key := deploymentKey(cmd.Namespace, cmd.Name)
		existing := s.Deployments[key]

		if existing == nil {
			// Create new deployment
			s.Deployments[key] = &DeploymentMeta{
				Name:      cmd.Name,
				Namespace: cmd.Namespace,
				Type:      cmd.Type,
				Replicas:  cmd.Replicas,
				Port:      cmd.Port,
				Schedule:  cmd.Schedule,
				Env:       cmd.Env,
				Versions: []VersionMeta{{
					ID:          cmd.Version,
					Fingerprint: cmd.Fingerprint,
					Size:        cmd.Size,
					FileCount:   cmd.FileCount,
					CreatedAt:   time.Now(),
					Active:      true,
				}},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			s.ActiveVersions[key] = cmd.Version
		} else {
			// Update existing
			existing.Replicas = cmd.Replicas
			existing.Port = cmd.Port
			existing.Schedule = cmd.Schedule
			existing.Env = cmd.Env
			existing.UpdatedAt = time.Now()

			// Add new version if different
			if cmd.Version != "" && cmd.Version != s.ActiveVersions[key] {
				// Deactivate previous
				for i := range existing.Versions {
					existing.Versions[i].Active = false
				}
				existing.Versions = append(existing.Versions, VersionMeta{
					ID:          cmd.Version,
					Fingerprint: cmd.Fingerprint,
					Size:        cmd.Size,
					FileCount:   cmd.FileCount,
					CreatedAt:   time.Now(),
					Active:      true,
				})
				s.ActiveVersions[key] = cmd.Version
			}
		}

	case LogDeploymentDelete:
		var cmd DeploymentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal deployment command: %w", err)
		}
		key := deploymentKey(cmd.Namespace, cmd.Name)
		delete(s.Deployments, key)
		delete(s.Assignments, key)
		delete(s.ActiveVersions, key)

	case LogDeploymentActivate:
		var cmd DeploymentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal deployment command: %w", err)
		}
		key := deploymentKey(cmd.Namespace, cmd.Name)
		if d := s.Deployments[key]; d != nil {
			for i := range d.Versions {
				d.Versions[i].Active = (d.Versions[i].ID == cmd.Version)
			}
			s.ActiveVersions[key] = cmd.Version
		}

	case LogSchedulerAssign:
		var cmd AssignmentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal assignment command: %w", err)
		}
		key := deploymentKey(cmd.Namespace, cmd.DeploymentName)
		s.Assignments[key] = cmd.Nodes

	case LogSchedulerUnassign:
		var cmd AssignmentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal assignment command: %w", err)
		}
		key := deploymentKey(cmd.Namespace, cmd.DeploymentName)
		delete(s.Assignments, key)

	case LogNodeJoin:
		var cmd NodeCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal node command: %w", err)
		}
		s.Nodes[cmd.NodeID] = &NodeInfo{
			ID:       cmd.NodeID,
			Address:  cmd.Address,
			JoinedAt: time.Now(),
			LastSeen: time.Now(),
			State:    Follower,
		}

	case LogNodeLeave:
		var cmd NodeCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal node command: %w", err)
		}
		delete(s.Nodes, cmd.NodeID)

	case LogScaleChange:
		var cmd DeploymentCommand
		if err := UnmarshalCommand(entry.Command, &cmd); err != nil {
			return fmt.Errorf("unmarshal deployment command: %w", err)
		}
		key := deploymentKey(cmd.Namespace, cmd.Name)
		if d := s.Deployments[key]; d != nil {
			d.Replicas = cmd.Replicas
			d.UpdatedAt = time.Now()
		}
	}

	s.LastApplied = entry.Index
	return nil
}

// Snapshot implements FSM.
func (s *ClusterState) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(s)
}

// Restore implements FSM.
func (s *ClusterState) Restore(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var restored ClusterState
	if err := json.Unmarshal(data, &restored); err != nil {
		return err
	}

	s.Deployments = restored.Deployments
	s.Nodes = restored.Nodes
	s.Assignments = restored.Assignments
	s.ActiveVersions = restored.ActiveVersions
	s.LastApplied = restored.LastApplied
	return nil
}

// GetDeployment returns deployment metadata.
func (s *ClusterState) GetDeployment(namespace, name string) *DeploymentMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Deployments[deploymentKey(namespace, name)]
}

// ListDeployments returns all deployments in a namespace.
func (s *ClusterState) ListDeployments(namespace string) []*DeploymentMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*DeploymentMeta
	for key, d := range s.Deployments {
		if namespace == "" || d.Namespace == namespace {
			_ = key
			result = append(result, d)
		}
	}
	return result
}

// GetAssignment returns the nodes a deployment is assigned to.
func (s *ClusterState) GetAssignment(namespace, name string) []NodeID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Assignments[deploymentKey(namespace, name)]
}

// GetActiveVersion returns the active version for a deployment.
func (s *ClusterState) GetActiveVersion(namespace, name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveVersions[deploymentKey(namespace, name)]
}

// GetNode returns node info.
func (s *ClusterState) GetNode(id NodeID) *NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Nodes[id]
}

// ListNodes returns all nodes.
func (s *ClusterState) ListNodes() []*NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*NodeInfo, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		result = append(result, n)
	}
	return result
}

// SaveSnapshot saves a snapshot to disk.
func (s *ClusterState) SaveSnapshot(dir string) error {
	data, err := s.Snapshot()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	path := filepath.Join(dir, "snapshot.json")
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// LoadSnapshot loads a snapshot from disk.
func (s *ClusterState) LoadSnapshot(dir string) error {
	path := filepath.Join(dir, "snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No snapshot yet
		}
		return err
	}
	return s.Restore(data)
}
