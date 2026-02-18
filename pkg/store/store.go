// Package store provides persistent state storage for prisn.
//
// Uses SQLite for single-node deployments. Simple, reliable, no external deps.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// isUniqueConstraintError checks if an error is a SQLite unique constraint violation.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite returns "UNIQUE constraint failed" for duplicate key errors
	return strings.Contains(err.Error(), "UNIQUE constraint")
}

// Store is the main storage interface.
type Store struct {
	db   *sql.DB
	path string
}

// DeploymentSpec defines the desired state of a deployment.
// This is the input type used when creating/updating deployments.
type DeploymentSpec struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Type        string            `json:"type,omitempty"` // service, job, cronjob
	Runtime     string            `json:"runtime,omitempty"`
	Source      string            `json:"source"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Port        int               `json:"port,omitempty"`
	Replicas    int               `json:"replicas,omitempty"`
	Schedule    string            `json:"schedule,omitempty"` // Cron expression
	Timeout     time.Duration     `json:"timeout,omitempty"`
	Resources   ResourceLimits    `json:"resources,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	HealthCheck *HealthCheck      `json:"health_check,omitempty"`
}

// HealthCheck configures health checking for a deployment.
type HealthCheck struct {
	Type     string        `json:"type"`               // tcp, http, exec
	Target   string        `json:"target"`             // Address, path, or command
	Interval time.Duration `json:"interval,omitempty"` // How often to check (default: 10s)
	Timeout  time.Duration `json:"timeout,omitempty"`  // Timeout per check (default: 1s)
	Retries  int           `json:"retries,omitempty"`  // Failures before unhealthy (default: 3)
}

// Deployment represents a deployed service or job template.
// It embeds DeploymentSpec and adds runtime state.
type Deployment struct {
	ID string `json:"id"`
	DeploymentSpec
	Status    DeploymentStatus `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// ResourceLimits specifies resource constraints.
type ResourceLimits struct {
	CPUMillicores int   `json:"cpu_millicores,omitempty"`
	MemoryBytes   int64 `json:"memory_bytes,omitempty"`
	MaxProcesses  int   `json:"max_processes,omitempty"`
}

// DeploymentStatus tracks the current state of a deployment.
type DeploymentStatus struct {
	Phase           string    `json:"phase"` // Pending, Running, Paused, Failed
	Health          string    `json:"health,omitempty"` // healthy, unhealthy, unknown
	ReadyReplicas   int       `json:"ready_replicas"`
	DesiredReplicas int       `json:"desired_replicas"`
	Restarts        int       `json:"restarts"`
	Message         string    `json:"message,omitempty"`
	LastUpdated     time.Time `json:"last_updated"`
}

// Execution represents a single run of a script.
type Execution struct {
	ID           string        `json:"id"`
	DeploymentID string        `json:"deployment_id,omitempty"`
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"` // For ad-hoc jobs
	Status       string        `json:"status"` // Pending, Running, Completed, Failed
	ExitCode     int           `json:"exit_code"`
	StartedAt    time.Time     `json:"started_at"`
	CompletedAt  time.Time     `json:"completed_at,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
	Stdout       string        `json:"stdout,omitempty"`
	Stderr       string        `json:"stderr,omitempty"`
	Error        string        `json:"error,omitempty"`
	NodeID       string        `json:"node_id,omitempty"` // Which node ran this
}

// CronSchedule tracks scheduled jobs.
type CronSchedule struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	Schedule     string    `json:"schedule"`
	NextRun      time.Time `json:"next_run"`
	LastRun      time.Time `json:"last_run,omitempty"`
	Enabled      bool      `json:"enabled"`
}

// Open opens or creates a store at the given path.
func Open(path string) (*Store, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	return s, nil
}

// OpenDefault opens the store at the default location (~/.prisn/state.db).
func OpenDefault() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find home directory: %w", err)
	}
	return Open(filepath.Join(home, ".prisn", "state.db"))
}

// Close closes the store.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate runs database migrations.
func (s *Store) migrate() error {
	// Check if we have the old schema (with 'config' and 'status' JSON columns)
	// and migrate to the new flattened schema
	var colCount int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('deployments')
		WHERE name = 'config' OR name = 'status'
	`).Scan(&colCount)

	if err == nil && colCount > 0 {
		// Old schema detected - drop and recreate
		// In production, you'd want to migrate data, but for dev this is fine
		_, _ = s.db.Exec(`DROP TABLE IF EXISTS deployments`)
		_, _ = s.db.Exec(`DROP TABLE IF EXISTS executions`)
		_, _ = s.db.Exec(`DROP TABLE IF EXISTS cron_schedules`)
	}

	// Schema version 2: Flattened deployments table (no more JSON blobs for config/status)
	schema := `
	CREATE TABLE IF NOT EXISTS deployments (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL DEFAULT 'default',
		type TEXT NOT NULL,
		-- Core fields (previously in config JSON)
		runtime TEXT,
		source TEXT,
		port INTEGER DEFAULT 0,
		replicas INTEGER DEFAULT 1,
		schedule TEXT,
		timeout_ns INTEGER DEFAULT 0,
		-- Collections (still JSON - rarely queried)
		args_json TEXT DEFAULT '[]',
		env_json TEXT DEFAULT '{}',
		labels_json TEXT DEFAULT '{}',
		resources_json TEXT DEFAULT '{}',
		-- Status fields (previously in status JSON)
		status_phase TEXT DEFAULT 'Pending',
		status_ready_replicas INTEGER DEFAULT 0,
		status_desired_replicas INTEGER DEFAULT 0,
		status_restarts INTEGER DEFAULT 0,
		status_message TEXT DEFAULT '',
		status_last_updated DATETIME,
		-- Timestamps
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(namespace, name)
	);

	CREATE INDEX IF NOT EXISTS idx_deployments_namespace ON deployments(namespace);
	CREATE INDEX IF NOT EXISTS idx_deployments_type ON deployments(type);
	CREATE INDEX IF NOT EXISTS idx_deployments_status_phase ON deployments(status_phase);

	CREATE TABLE IF NOT EXISTS executions (
		id TEXT PRIMARY KEY,
		deployment_id TEXT,
		namespace TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		exit_code INTEGER,
		started_at DATETIME,
		completed_at DATETIME,
		duration_ms INTEGER,
		stdout TEXT,
		stderr TEXT,
		error TEXT,
		node_id TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE SET NULL
	);

	CREATE INDEX IF NOT EXISTS idx_executions_deployment ON executions(deployment_id);
	CREATE INDEX IF NOT EXISTS idx_executions_namespace ON executions(namespace);
	CREATE INDEX IF NOT EXISTS idx_executions_status ON executions(status);
	CREATE INDEX IF NOT EXISTS idx_executions_created ON executions(created_at);

	CREATE TABLE IF NOT EXISTS cron_schedules (
		id TEXT PRIMARY KEY,
		deployment_id TEXT NOT NULL,
		schedule TEXT NOT NULL,
		next_run DATETIME NOT NULL,
		last_run DATETIME,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (deployment_id) REFERENCES deployments(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_cron_next_run ON cron_schedules(next_run) WHERE enabled = 1;

	CREATE TABLE IF NOT EXISTS secrets (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL DEFAULT 'default',
		data TEXT NOT NULL, -- JSON blob of key-value pairs
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(namespace, name)
	);

	CREATE TABLE IF NOT EXISTS tokens (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		secret_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		namespaces TEXT NOT NULL, -- JSON array of namespace names
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME,
		revoked_at DATETIME,
		last_used_at DATETIME,
		created_by TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_tokens_role ON tokens(role);

	CREATE TABLE IF NOT EXISTS runtimes (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL DEFAULT 'default',
		definition TEXT NOT NULL, -- JSON blob of runtime definition
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(namespace, id)
	);

	CREATE INDEX IF NOT EXISTS idx_runtimes_namespace ON runtimes(namespace);
	`
	_, err = s.db.Exec(schema)
	return err
}

// CreateDeployment creates a new deployment.
func (s *Store) CreateDeployment(d *Deployment) error {
	// Marshal collection fields to JSON
	argsJSON, _ := json.Marshal(d.Args)
	envJSON, _ := json.Marshal(d.Env)
	labelsJSON, _ := json.Marshal(d.Labels)
	resourcesJSON, _ := json.Marshal(d.Resources)

	_, err := s.db.Exec(`
		INSERT INTO deployments (
			id, name, namespace, type,
			runtime, source, port, replicas, schedule, timeout_ns,
			args_json, env_json, labels_json, resources_json,
			status_phase, status_ready_replicas, status_desired_replicas, status_restarts, status_message, status_last_updated,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		d.ID, d.Name, d.Namespace, d.Type,
		d.Runtime, d.Source, d.Port, d.Replicas, d.Schedule, int64(d.Timeout),
		string(argsJSON), string(envJSON), string(labelsJSON), string(resourcesJSON),
		d.Status.Phase, d.Status.ReadyReplicas, d.Status.DesiredReplicas, d.Status.Restarts, d.Status.Message, d.Status.LastUpdated,
		d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return &AlreadyExistsError{Resource: "deployment", ID: d.Namespace + "/" + d.Name}
		}
		return fmt.Errorf("failed to insert deployment: %w", err)
	}

	return nil
}

// UpdateDeployment updates an existing deployment.
func (s *Store) UpdateDeployment(d *Deployment) error {
	// Marshal collection fields to JSON
	argsJSON, _ := json.Marshal(d.Args)
	envJSON, _ := json.Marshal(d.Env)
	labelsJSON, _ := json.Marshal(d.Labels)
	resourcesJSON, _ := json.Marshal(d.Resources)

	d.UpdatedAt = time.Now()
	result, err := s.db.Exec(`
		UPDATE deployments SET
			runtime = ?, source = ?, port = ?, replicas = ?, schedule = ?, timeout_ns = ?,
			args_json = ?, env_json = ?, labels_json = ?, resources_json = ?,
			status_phase = ?, status_ready_replicas = ?, status_desired_replicas = ?, status_restarts = ?,
			status_message = ?, status_last_updated = ?,
			updated_at = ?
		WHERE id = ?
	`,
		d.Runtime, d.Source, d.Port, d.Replicas, d.Schedule, int64(d.Timeout),
		string(argsJSON), string(envJSON), string(labelsJSON), string(resourcesJSON),
		d.Status.Phase, d.Status.ReadyReplicas, d.Status.DesiredReplicas, d.Status.Restarts,
		d.Status.Message, d.Status.LastUpdated,
		d.UpdatedAt, d.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "deployment", ID: d.ID}
	}

	return nil
}

// deploymentColumns is the list of columns to select for deployments.
const deploymentColumns = `
	id, name, namespace, type,
	runtime, source, port, replicas, schedule, timeout_ns,
	args_json, env_json, labels_json, resources_json,
	status_phase, status_ready_replicas, status_desired_replicas, status_restarts, status_message, status_last_updated,
	created_at, updated_at
`

// scanDeployment scans a deployment from a database row.
func scanDeployment(scanner interface{ Scan(...any) error }) (*Deployment, error) {
	var d Deployment
	var argsJSON, envJSON, labelsJSON, resourcesJSON string
	var timeoutNS int64
	var statusLastUpdated sql.NullTime

	err := scanner.Scan(
		&d.ID, &d.Name, &d.Namespace, &d.Type,
		&d.Runtime, &d.Source, &d.Port, &d.Replicas, &d.Schedule, &timeoutNS,
		&argsJSON, &envJSON, &labelsJSON, &resourcesJSON,
		&d.Status.Phase, &d.Status.ReadyReplicas, &d.Status.DesiredReplicas, &d.Status.Restarts, &d.Status.Message, &statusLastUpdated,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	d.Timeout = time.Duration(timeoutNS)
	if statusLastUpdated.Valid {
		d.Status.LastUpdated = statusLastUpdated.Time
	}

	// Unmarshal JSON fields
	json.Unmarshal([]byte(argsJSON), &d.Args)
	json.Unmarshal([]byte(envJSON), &d.Env)
	json.Unmarshal([]byte(labelsJSON), &d.Labels)
	json.Unmarshal([]byte(resourcesJSON), &d.Resources)

	return &d, nil
}

// GetDeployment retrieves a deployment by ID.
func (s *Store) GetDeployment(id string) (*Deployment, error) {
	row := s.db.QueryRow(`SELECT `+deploymentColumns+` FROM deployments WHERE id = ?`, id)
	d, err := scanDeployment(row)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "deployment", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	return d, nil
}

// GetDeploymentByName retrieves a deployment by namespace and name.
func (s *Store) GetDeploymentByName(namespace, name string) (*Deployment, error) {
	row := s.db.QueryRow(`SELECT `+deploymentColumns+` FROM deployments WHERE namespace = ? AND name = ?`, namespace, name)
	d, err := scanDeployment(row)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "deployment", ID: namespace + "/" + name}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	return d, nil
}

// ListDeployments lists deployments with optional filtering.
func (s *Store) ListDeployments(namespace, deployType string) ([]*Deployment, error) {
	query := `SELECT ` + deploymentColumns + ` FROM deployments WHERE 1=1`
	args := []any{}

	if namespace != "" {
		query += " AND namespace = ?"
		args = append(args, namespace)
	}
	if deployType != "" {
		query += " AND type = ?"
		args = append(args, deployType)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}
	defer rows.Close()

	deployments := make([]*Deployment, 0) // Use empty slice, not nil, for JSON encoding
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deployment: %w", err)
		}
		deployments = append(deployments, d)
	}

	return deployments, rows.Err()
}

// DeleteDeployment deletes a deployment.
func (s *Store) DeleteDeployment(id string) error {
	result, err := s.db.Exec("DELETE FROM deployments WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "deployment", ID: id}
	}
	return nil
}

// CreateExecution creates a new execution record.
func (s *Store) CreateExecution(e *Execution) error {
	// Use NULL for empty deployment_id (foreign key constraint)
	var deploymentID interface{}
	if e.DeploymentID != "" {
		deploymentID = e.DeploymentID
	}

	_, err := s.db.Exec(`
		INSERT INTO executions (id, deployment_id, namespace, name, status, started_at, node_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, e.ID, deploymentID, e.Namespace, e.Name, e.Status, e.StartedAt, e.NodeID)
	if err != nil {
		return fmt.Errorf("failed to insert execution: %w", err)
	}
	return nil
}

// UpdateExecution updates an execution record.
func (s *Store) UpdateExecution(e *Execution) error {
	durationMs := e.Duration.Milliseconds()
	_, err := s.db.Exec(`
		UPDATE executions SET status = ?, exit_code = ?, completed_at = ?, duration_ms = ?,
		stdout = ?, stderr = ?, error = ?
		WHERE id = ?
	`, e.Status, e.ExitCode, e.CompletedAt, durationMs, e.Stdout, e.Stderr, e.Error, e.ID)
	if err != nil {
		return fmt.Errorf("failed to update execution: %w", err)
	}
	return nil
}

// GetExecution retrieves an execution by ID.
func (s *Store) GetExecution(id string) (*Execution, error) {
	var e Execution
	var durationMs sql.NullInt64
	var completedAt sql.NullTime
	var stdout, stderr, errMsg sql.NullString
	var deploymentID sql.NullString
	var exitCode sql.NullInt64

	err := s.db.QueryRow(`
		SELECT id, deployment_id, namespace, name, status, exit_code, started_at, completed_at,
		duration_ms, stdout, stderr, error, node_id
		FROM executions WHERE id = ?
	`, id).Scan(&e.ID, &deploymentID, &e.Namespace, &e.Name, &e.Status, &exitCode,
		&e.StartedAt, &completedAt, &durationMs, &stdout, &stderr, &errMsg, &e.NodeID)

	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "execution", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}

	if deploymentID.Valid {
		e.DeploymentID = deploymentID.String
	}
	if exitCode.Valid {
		e.ExitCode = int(exitCode.Int64)
	}
	if completedAt.Valid {
		e.CompletedAt = completedAt.Time
	}
	if durationMs.Valid {
		e.Duration = time.Duration(durationMs.Int64) * time.Millisecond
	}
	if stdout.Valid {
		e.Stdout = stdout.String
	}
	if stderr.Valid {
		e.Stderr = stderr.String
	}
	if errMsg.Valid {
		e.Error = errMsg.String
	}

	return &e, nil
}

// ListExecutions lists executions with optional filtering.
func (s *Store) ListExecutions(namespace, deploymentID, status string, limit int) ([]*Execution, error) {
	query := "SELECT id, deployment_id, namespace, name, status, exit_code, started_at, completed_at, duration_ms, node_id FROM executions WHERE 1=1"
	args := []interface{}{}

	if namespace != "" {
		query += " AND namespace = ?"
		args = append(args, namespace)
	}
	if deploymentID != "" {
		query += " AND deployment_id = ?"
		args = append(args, deploymentID)
	}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list executions: %w", err)
	}
	defer rows.Close()

	executions := make([]*Execution, 0) // Use empty slice, not nil, for JSON encoding
	for rows.Next() {
		var e Execution
		var durationMs sql.NullInt64
		var completedAt sql.NullTime
		var deployID sql.NullString

		if err := rows.Scan(&e.ID, &deployID, &e.Namespace, &e.Name, &e.Status,
			&e.ExitCode, &e.StartedAt, &completedAt, &durationMs, &e.NodeID); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		if deployID.Valid {
			e.DeploymentID = deployID.String
		}
		if completedAt.Valid {
			e.CompletedAt = completedAt.Time
		}
		if durationMs.Valid {
			e.Duration = time.Duration(durationMs.Int64) * time.Millisecond
		}
		executions = append(executions, &e)
	}

	return executions, rows.Err()
}

// CleanupOldExecutions deletes execution records older than the given duration.
// Returns the number of deleted records.
func (s *Store) CleanupOldExecutions(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)

	result, err := s.db.Exec(`
		DELETE FROM executions WHERE completed_at < ? AND completed_at IS NOT NULL
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup executions: %w", err)
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}

// CleanupRunningExecutionsOlderThan marks old running executions as failed.
// This handles cases where the server crashed mid-execution.
func (s *Store) CleanupRunningExecutionsOlderThan(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)

	result, err := s.db.Exec(`
		UPDATE executions
		SET status = 'Failed', error = 'Server restart - execution status unknown', completed_at = ?
		WHERE status = 'Running' AND started_at < ?
	`, time.Now(), cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup running executions: %w", err)
	}

	affected, _ := result.RowsAffected()
	return affected, nil
}

// GetExecutionStats returns statistics about executions.
type ExecutionStats struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
}

func (s *Store) GetExecutionStats() (*ExecutionStats, error) {
	var stats ExecutionStats

	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			SUM(CASE WHEN status = 'Completed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'Failed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'Running' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = 'Pending' THEN 1 ELSE 0 END)
		FROM executions
	`).Scan(&stats.Total, &stats.Completed, &stats.Failed, &stats.Running, &stats.Pending)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution stats: %w", err)
	}

	return &stats, nil
}

// Secret represents a stored secret.
type Secret struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Data      map[string]string `json:"data"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// CreateSecret creates a new secret.
func (s *Store) CreateSecret(sec *Secret) error {
	data, err := json.Marshal(sec.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal secret data: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO secrets (id, name, namespace, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sec.ID, sec.Name, sec.Namespace, string(data), sec.CreatedAt, sec.UpdatedAt)
	if err != nil {
		if isUniqueConstraintError(err) {
			return &AlreadyExistsError{Resource: "secret", ID: sec.Namespace + "/" + sec.Name}
		}
		return fmt.Errorf("failed to insert secret: %w", err)
	}
	return nil
}

// GetSecret retrieves a secret by namespace and name.
func (s *Store) GetSecret(namespace, name string) (*Secret, error) {
	var sec Secret
	var data string

	err := s.db.QueryRow(`
		SELECT id, name, namespace, data, created_at, updated_at FROM secrets
		WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&sec.ID, &sec.Name, &sec.Namespace, &data, &sec.CreatedAt, &sec.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "secret", ID: namespace + "/" + name}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	if err := json.Unmarshal([]byte(data), &sec.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal secret data: %w", err)
	}

	return &sec, nil
}

// GetSecretValue retrieves a specific key from a secret.
func (s *Store) GetSecretValue(namespace, name, key string) (string, error) {
	sec, err := s.GetSecret(namespace, name)
	if err != nil {
		return "", err
	}
	val, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s/%s", key, namespace, name)
	}
	return val, nil
}

// DeleteSecret deletes a secret.
func (s *Store) DeleteSecret(namespace, name string) error {
	result, err := s.db.Exec("DELETE FROM secrets WHERE namespace = ? AND name = ?", namespace, name)
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "secret", ID: namespace + "/" + name}
	}
	return nil
}

// SecretMeta contains secret metadata without the actual data.
type SecretMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Keys      []string  `json:"keys"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListSecrets lists all secrets in a namespace (metadata only, no values).
func (s *Store) ListSecrets(namespace string) ([]*SecretMeta, error) {
	query := `SELECT id, name, namespace, data, created_at, updated_at FROM secrets`
	args := []any{}

	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY name"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}
	defer rows.Close()

	secrets := make([]*SecretMeta, 0) // Use empty slice, not nil, for JSON encoding
	for rows.Next() {
		var id, name, ns, data string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &name, &ns, &data, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan secret: %w", err)
		}

		// Extract keys from data JSON
		var dataMap map[string]string
		if err := json.Unmarshal([]byte(data), &dataMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal secret data: %w", err)
		}

		keys := make([]string, 0, len(dataMap))
		for k := range dataMap {
			keys = append(keys, k)
		}

		secrets = append(secrets, &SecretMeta{
			ID:        id,
			Name:      name,
			Namespace: ns,
			Keys:      keys,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating secrets: %w", err)
	}

	return secrets, nil
}

// =============================================================================
// Token Operations (RBAC)
// =============================================================================

// TokenRecord represents a token stored in the database.
type TokenRecord struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	SecretHash  string     `json:"secret_hash"`
	Role        string     `json:"role"`
	Namespaces  []string   `json:"namespaces"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
}

// CreateToken creates a new token in the database.
func (s *Store) CreateToken(t *TokenRecord) error {
	namespacesJSON, err := json.Marshal(t.Namespaces)
	if err != nil {
		return fmt.Errorf("failed to marshal namespaces: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO tokens (id, name, secret_hash, role, namespaces, created_at, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.Name, t.SecretHash, t.Role, string(namespacesJSON), t.CreatedAt, t.ExpiresAt, t.CreatedBy)
	if err != nil {
		return fmt.Errorf("failed to insert token: %w", err)
	}
	return nil
}

// GetToken retrieves a token by ID.
func (s *Store) GetToken(id string) (*TokenRecord, error) {
	var t TokenRecord
	var namespacesJSON string
	var expiresAt, revokedAt, lastUsedAt sql.NullTime
	var createdBy sql.NullString

	err := s.db.QueryRow(`
		SELECT id, name, secret_hash, role, namespaces, created_at, expires_at, revoked_at, last_used_at, created_by
		FROM tokens WHERE id = ?
	`, id).Scan(&t.ID, &t.Name, &t.SecretHash, &t.Role, &namespacesJSON, &t.CreatedAt,
		&expiresAt, &revokedAt, &lastUsedAt, &createdBy)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "token", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	if err := json.Unmarshal([]byte(namespacesJSON), &t.Namespaces); err != nil {
		return nil, fmt.Errorf("failed to unmarshal namespaces: %w", err)
	}

	if expiresAt.Valid {
		t.ExpiresAt = &expiresAt.Time
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	if lastUsedAt.Valid {
		t.LastUsedAt = &lastUsedAt.Time
	}
	if createdBy.Valid {
		t.CreatedBy = createdBy.String
	}

	return &t, nil
}

// GetTokenBySecret retrieves a token by validating its secret.
// This is used during authentication - iterates through all tokens and checks bcrypt hash.
// Returns nil if no matching token is found.
func (s *Store) GetTokenBySecret(secret string) (*TokenRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, name, secret_hash, role, namespaces, created_at, expires_at, revoked_at, last_used_at, created_by
		FROM tokens WHERE revoked_at IS NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query tokens: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var t TokenRecord
		var namespacesJSON string
		var expiresAt, revokedAt, lastUsedAt sql.NullTime
		var createdBy sql.NullString

		if err := rows.Scan(&t.ID, &t.Name, &t.SecretHash, &t.Role, &namespacesJSON, &t.CreatedAt,
			&expiresAt, &revokedAt, &lastUsedAt, &createdBy); err != nil {
			continue
		}

		// Check if secret matches using bcrypt
		if err := bcrypt.CompareHashAndPassword([]byte(t.SecretHash), []byte(secret)); err == nil {
			if err := json.Unmarshal([]byte(namespacesJSON), &t.Namespaces); err != nil {
				continue
			}
			if expiresAt.Valid {
				t.ExpiresAt = &expiresAt.Time
			}
			if revokedAt.Valid {
				t.RevokedAt = &revokedAt.Time
			}
			if lastUsedAt.Valid {
				t.LastUsedAt = &lastUsedAt.Time
			}
			if createdBy.Valid {
				t.CreatedBy = createdBy.String
			}
			return &t, nil
		}
	}

	return nil, nil // No matching token found
}

// ListTokens lists all tokens, optionally filtered by role.
func (s *Store) ListTokens(role string) ([]*TokenRecord, error) {
	query := `
		SELECT id, name, secret_hash, role, namespaces, created_at, expires_at, revoked_at, last_used_at, created_by
		FROM tokens
	`
	args := []interface{}{}

	if role != "" {
		query += " WHERE role = ?"
		args = append(args, role)
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tokens: %w", err)
	}
	defer rows.Close()

	tokens := make([]*TokenRecord, 0) // Use empty slice, not nil, for JSON encoding
	for rows.Next() {
		var t TokenRecord
		var namespacesJSON string
		var expiresAt, revokedAt, lastUsedAt sql.NullTime
		var createdBy sql.NullString

		if err := rows.Scan(&t.ID, &t.Name, &t.SecretHash, &t.Role, &namespacesJSON, &t.CreatedAt,
			&expiresAt, &revokedAt, &lastUsedAt, &createdBy); err != nil {
			return nil, fmt.Errorf("failed to scan token: %w", err)
		}

		if err := json.Unmarshal([]byte(namespacesJSON), &t.Namespaces); err != nil {
			return nil, fmt.Errorf("failed to unmarshal namespaces: %w", err)
		}

		if expiresAt.Valid {
			t.ExpiresAt = &expiresAt.Time
		}
		if revokedAt.Valid {
			t.RevokedAt = &revokedAt.Time
		}
		if lastUsedAt.Valid {
			t.LastUsedAt = &lastUsedAt.Time
		}
		if createdBy.Valid {
			t.CreatedBy = createdBy.String
		}

		tokens = append(tokens, &t)
	}

	return tokens, nil
}

// RevokeToken marks a token as revoked.
func (s *Store) RevokeToken(id string) error {
	result, err := s.db.Exec(`
		UPDATE tokens SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Check if token exists but is already revoked
		var count int
		s.db.QueryRow("SELECT COUNT(*) FROM tokens WHERE id = ?", id).Scan(&count)
		if count > 0 {
			return ErrAlreadyRevoked
		}
		return &NotFoundError{Resource: "token", ID: id}
	}
	return nil
}

// DeleteToken permanently deletes a token.
func (s *Store) DeleteToken(id string) error {
	result, err := s.db.Exec("DELETE FROM tokens WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "token", ID: id}
	}
	return nil
}

// UpdateTokenLastUsed updates the last_used_at timestamp for a token.
func (s *Store) UpdateTokenLastUsed(id string) error {
	_, err := s.db.Exec("UPDATE tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to update token last used: %w", err)
	}
	return nil
}

// TokenCount returns the number of tokens, optionally filtered by role.
func (s *Store) TokenCount(role string) (int, error) {
	query := "SELECT COUNT(*) FROM tokens"
	args := []interface{}{}
	if role != "" {
		query += " WHERE role = ?"
		args = append(args, role)
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count tokens: %w", err)
	}
	return count, nil
}

// =============================================================================
// Runtime Operations (Custom Runtime Definitions)
// =============================================================================

// RuntimeDefinition represents a custom runtime stored in the cluster.
type RuntimeDefinition struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Extensions []string          `json:"extensions"`
	Env        map[string]string `json:"env,omitempty"`
	Detect     RuntimeDetect     `json:"detect,omitempty"`
	Install    RuntimeInstall    `json:"install,omitempty"`
	Deps       RuntimeDeps       `json:"deps,omitempty"`
	Shebang    []string          `json:"shebang_patterns,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// RuntimeDetect configures runtime detection.
type RuntimeDetect struct {
	Check        string `json:"check,omitempty"`
	VersionRegex string `json:"version_regex,omitempty"`
	MinVersion   string `json:"min_version,omitempty"`
}

// RuntimeInstall configures installation hints.
type RuntimeInstall struct {
	MacOS   string `json:"macos,omitempty"`
	Linux   string `json:"linux,omitempty"`
	Windows string `json:"windows,omitempty"`
	Script  string `json:"script,omitempty"`
	DocURL  string `json:"doc_url,omitempty"`
	Note    string `json:"note,omitempty"`
}

// RuntimeDeps configures dependency management.
type RuntimeDeps struct {
	Enabled        bool     `json:"enabled"`
	ManifestFiles  []string `json:"manifest_files,omitempty"`
	Parser         string   `json:"parser,omitempty"`
	InstallCommand string   `json:"install_command,omitempty"`
	SelfManaged    bool     `json:"self_managed,omitempty"`
	EnvType        string   `json:"env_type,omitempty"`
}

// CreateRuntime creates a new runtime definition.
func (s *Store) CreateRuntime(rt *RuntimeDefinition) error {
	definitionJSON, err := json.Marshal(rt)
	if err != nil {
		return fmt.Errorf("failed to marshal runtime definition: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO runtimes (id, name, namespace, definition, created_at, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, rt.ID, rt.Name, rt.Namespace, string(definitionJSON))
	if err != nil {
		if isUniqueConstraintError(err) {
			return &AlreadyExistsError{Resource: "runtime", ID: rt.ID}
		}
		return fmt.Errorf("failed to insert runtime: %w", err)
	}
	return nil
}

// GetRuntime retrieves a runtime by ID and namespace.
func (s *Store) GetRuntime(namespace, id string) (*RuntimeDefinition, error) {
	var definitionJSON string
	err := s.db.QueryRow(`
		SELECT definition FROM runtimes WHERE namespace = ? AND id = ?
	`, namespace, id).Scan(&definitionJSON)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Resource: "runtime", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime: %w", err)
	}

	var rt RuntimeDefinition
	if err := json.Unmarshal([]byte(definitionJSON), &rt); err != nil {
		return nil, fmt.Errorf("failed to unmarshal runtime: %w", err)
	}

	return &rt, nil
}

// ListRuntimes lists all runtime definitions in a namespace.
func (s *Store) ListRuntimes(namespace string) ([]*RuntimeDefinition, error) {
	query := `SELECT definition FROM runtimes`
	args := []any{}

	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}

	query += " ORDER BY name ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list runtimes: %w", err)
	}
	defer rows.Close()

	runtimes := make([]*RuntimeDefinition, 0)
	for rows.Next() {
		var definitionJSON string
		if err := rows.Scan(&definitionJSON); err != nil {
			return nil, fmt.Errorf("failed to scan runtime: %w", err)
		}

		var rt RuntimeDefinition
		if err := json.Unmarshal([]byte(definitionJSON), &rt); err != nil {
			return nil, fmt.Errorf("failed to unmarshal runtime: %w", err)
		}
		runtimes = append(runtimes, &rt)
	}

	return runtimes, rows.Err()
}

// UpdateRuntime updates an existing runtime definition.
func (s *Store) UpdateRuntime(rt *RuntimeDefinition) error {
	definitionJSON, err := json.Marshal(rt)
	if err != nil {
		return fmt.Errorf("failed to marshal runtime definition: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE runtimes SET name = ?, definition = ?, updated_at = CURRENT_TIMESTAMP
		WHERE namespace = ? AND id = ?
	`, rt.Name, string(definitionJSON), rt.Namespace, rt.ID)
	if err != nil {
		return fmt.Errorf("failed to update runtime: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "runtime", ID: rt.ID}
	}
	return nil
}

// DeleteRuntime deletes a runtime definition.
func (s *Store) DeleteRuntime(namespace, id string) error {
	result, err := s.db.Exec("DELETE FROM runtimes WHERE namespace = ? AND id = ?", namespace, id)
	if err != nil {
		return fmt.Errorf("failed to delete runtime: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return &NotFoundError{Resource: "runtime", ID: id}
	}
	return nil
}

// =============================================================================
// Cron Operations
// =============================================================================

// CreateCronSchedule creates a new cron schedule.
func (s *Store) CreateCronSchedule(cs *CronSchedule) error {
	_, err := s.db.Exec(`
		INSERT INTO cron_schedules (id, deployment_id, schedule, next_run, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, cs.ID, cs.DeploymentID, cs.Schedule, cs.NextRun, cs.Enabled)
	if err != nil {
		return fmt.Errorf("failed to insert cron schedule: %w", err)
	}
	return nil
}

// UpdateCronSchedule updates a cron schedule's next run time.
func (s *Store) UpdateCronSchedule(id string, nextRun, lastRun time.Time) error {
	_, err := s.db.Exec(`
		UPDATE cron_schedules SET next_run = ?, last_run = ? WHERE id = ?
	`, nextRun, lastRun, id)
	if err != nil {
		return fmt.Errorf("failed to update cron schedule: %w", err)
	}
	return nil
}

// DisableCronSchedule disables a cron schedule.
func (s *Store) DisableCronSchedule(id string) error {
	_, err := s.db.Exec(`UPDATE cron_schedules SET enabled = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to disable cron schedule: %w", err)
	}
	return nil
}

// GetDueCronSchedules returns cron schedules that are due to run.
func (s *Store) GetDueCronSchedules(before time.Time) ([]*CronSchedule, error) {
	rows, err := s.db.Query(`
		SELECT id, deployment_id, schedule, next_run, last_run, enabled
		FROM cron_schedules
		WHERE enabled = 1 AND next_run <= ?
		ORDER BY next_run ASC
	`, before)
	if err != nil {
		return nil, fmt.Errorf("failed to get due cron schedules: %w", err)
	}
	defer rows.Close()

	schedules := make([]*CronSchedule, 0) // Use empty slice, not nil, for JSON encoding
	for rows.Next() {
		var cs CronSchedule
		var lastRun sql.NullTime

		if err := rows.Scan(&cs.ID, &cs.DeploymentID, &cs.Schedule, &cs.NextRun, &lastRun, &cs.Enabled); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		if lastRun.Valid {
			cs.LastRun = lastRun.Time
		}
		schedules = append(schedules, &cs)
	}

	return schedules, rows.Err()
}

// Stats returns basic statistics about the store.
type Stats struct {
	Deployments int
	Executions  int
	Secrets     int
	CronJobs    int
}

// GetStats returns storage statistics.
func (s *Store) GetStats() (*Stats, error) {
	var stats Stats

	if err := s.db.QueryRow("SELECT COUNT(*) FROM deployments").Scan(&stats.Deployments); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM executions").Scan(&stats.Executions); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM secrets").Scan(&stats.Secrets); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM cron_schedules").Scan(&stats.CronJobs); err != nil {
		return nil, err
	}

	return &stats, nil
}
