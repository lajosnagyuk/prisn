package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreOpenAndClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Errorf("failed to close store: %v", err)
	}
}

func TestDeploymentCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create
	d := &Deployment{
		ID: "dep-test-1",
		DeploymentSpec: DeploymentSpec{
			Name:      "api",
			Namespace: "default",
			Type:      "service",
			Runtime:   "python3",
			Source:    "/tmp/api/main.py",
			Port:      8080,
			Replicas:  3,
		},
		Status: DeploymentStatus{
			Phase:           "Pending",
			DesiredReplicas: 3,
			LastUpdated:     time.Now(),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := st.CreateDeployment(d); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Read by ID
	got, err := st.GetDeployment("dep-test-1")
	if err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}
	if got.Name != "api" {
		t.Errorf("expected name 'api', got %q", got.Name)
	}
	if got.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", got.Replicas)
	}

	// Read by name
	got2, err := st.GetDeploymentByName("default", "api")
	if err != nil {
		t.Fatalf("failed to get deployment by name: %v", err)
	}
	if got2.ID != d.ID {
		t.Errorf("expected ID %q, got %q", d.ID, got2.ID)
	}

	// Update
	d.Replicas = 5
	d.Status.Phase = "Running"
	d.Status.ReadyReplicas = 5
	if err := st.UpdateDeployment(d); err != nil {
		t.Fatalf("failed to update deployment: %v", err)
	}

	got3, err := st.GetDeployment("dep-test-1")
	if err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}
	if got3.Replicas != 5 {
		t.Errorf("expected 5 replicas after update, got %d", got3.Replicas)
	}
	if got3.Status.Phase != "Running" {
		t.Errorf("expected phase Running, got %q", got3.Status.Phase)
	}

	// Delete
	if err := st.DeleteDeployment("dep-test-1"); err != nil {
		t.Fatalf("failed to delete deployment: %v", err)
	}

	_, err = st.GetDeployment("dep-test-1")
	if err == nil {
		t.Error("expected error when getting deleted deployment")
	}
}

func TestListDeployments(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create multiple deployments
	deployments := []*Deployment{
		{ID: "dep-1", DeploymentSpec: DeploymentSpec{Name: "api", Namespace: "default", Type: "service"}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "dep-2", DeploymentSpec: DeploymentSpec{Name: "worker", Namespace: "default", Type: "job"}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "dep-3", DeploymentSpec: DeploymentSpec{Name: "cron", Namespace: "prod", Type: "cronjob"}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	for _, d := range deployments {
		if err := st.CreateDeployment(d); err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}
	}

	// List all
	all, err := st.ListDeployments("", "")
	if err != nil {
		t.Fatalf("failed to list all deployments: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 deployments, got %d", len(all))
	}

	// List by namespace
	defaultNS, err := st.ListDeployments("default", "")
	if err != nil {
		t.Fatalf("failed to list default namespace: %v", err)
	}
	if len(defaultNS) != 2 {
		t.Errorf("expected 2 deployments in default namespace, got %d", len(defaultNS))
	}

	// List by type
	services, err := st.ListDeployments("", "service")
	if err != nil {
		t.Fatalf("failed to list services: %v", err)
	}
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}

	// List by namespace and type
	defaultJobs, err := st.ListDeployments("default", "job")
	if err != nil {
		t.Fatalf("failed to list default jobs: %v", err)
	}
	if len(defaultJobs) != 1 {
		t.Errorf("expected 1 job in default namespace, got %d", len(defaultJobs))
	}
}

func TestExecutionCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create an execution
	e := &Execution{
		ID:        "exec-test-1",
		Namespace: "default",
		Name:      "test-job",
		Status:    "Pending",
		StartedAt: time.Now(),
	}

	if err := st.CreateExecution(e); err != nil {
		t.Fatalf("failed to create execution: %v", err)
	}

	// Get
	got, err := st.GetExecution("exec-test-1")
	if err != nil {
		t.Fatalf("failed to get execution: %v", err)
	}
	if got.Name != "test-job" {
		t.Errorf("expected name 'test-job', got %q", got.Name)
	}
	if got.Status != "Pending" {
		t.Errorf("expected status Pending, got %q", got.Status)
	}

	// Update
	e.Status = "Completed"
	e.ExitCode = 0
	e.Stdout = "Hello world"
	e.CompletedAt = time.Now()
	e.Duration = 100 * time.Millisecond

	if err := st.UpdateExecution(e); err != nil {
		t.Fatalf("failed to update execution: %v", err)
	}

	got2, err := st.GetExecution("exec-test-1")
	if err != nil {
		t.Fatalf("failed to get updated execution: %v", err)
	}
	if got2.Status != "Completed" {
		t.Errorf("expected status Completed, got %q", got2.Status)
	}
	if got2.Stdout != "Hello world" {
		t.Errorf("expected stdout 'Hello world', got %q", got2.Stdout)
	}

	// List
	list, err := st.ListExecutions("default", "", "", 10)
	if err != nil {
		t.Fatalf("failed to list executions: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 execution, got %d", len(list))
	}
}

func TestSecretCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create
	secret := &Secret{
		ID:        "secret-1",
		Name:      "db-creds",
		Namespace: "default",
		Data: map[string]string{
			"username": "admin",
			"password": "secret123",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := st.CreateSecret(secret); err != nil {
		t.Fatalf("failed to create secret: %v", err)
	}

	// Get
	got, err := st.GetSecret("default", "db-creds")
	if err != nil {
		t.Fatalf("failed to get secret: %v", err)
	}
	if got.Data["username"] != "admin" {
		t.Errorf("expected username 'admin', got %q", got.Data["username"])
	}
	if got.Data["password"] != "secret123" {
		t.Errorf("expected password 'secret123', got %q", got.Data["password"])
	}

	// Get specific value
	val, err := st.GetSecretValue("default", "db-creds", "username")
	if err != nil {
		t.Fatalf("failed to get secret value: %v", err)
	}
	if val != "admin" {
		t.Errorf("expected 'admin', got %q", val)
	}

	// Delete
	if err := st.DeleteSecret("default", "db-creds"); err != nil {
		t.Fatalf("failed to delete secret: %v", err)
	}

	_, err = st.GetSecret("default", "db-creds")
	if err == nil {
		t.Error("expected error when getting deleted secret")
	}
}

func TestCronSchedule(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create deployment first (foreign key)
	d := &Deployment{
		ID: "dep-cron",
		DeploymentSpec: DeploymentSpec{
			Name:      "backup",
			Namespace: "default",
			Type:      "cronjob",
			Schedule:  "0 0 * * *",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := st.CreateDeployment(d); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Create schedule
	nextRun := time.Now().Add(time.Hour)
	sched := &CronSchedule{
		ID:           "sched-1",
		DeploymentID: "dep-cron",
		Schedule:     "0 0 * * *",
		NextRun:      nextRun,
		Enabled:      true,
	}

	if err := st.CreateCronSchedule(sched); err != nil {
		t.Fatalf("failed to create schedule: %v", err)
	}

	// Get due schedules (none should be due yet)
	due, err := st.GetDueCronSchedules(time.Now())
	if err != nil {
		t.Fatalf("failed to get due schedules: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("expected 0 due schedules, got %d", len(due))
	}

	// Update next run to past
	pastTime := time.Now().Add(-time.Hour)
	if err := st.UpdateCronSchedule(sched.ID, pastTime, time.Time{}); err != nil {
		t.Fatalf("failed to update schedule: %v", err)
	}

	// Now it should be due
	due2, err := st.GetDueCronSchedules(time.Now())
	if err != nil {
		t.Fatalf("failed to get due schedules: %v", err)
	}
	if len(due2) != 1 {
		t.Errorf("expected 1 due schedule, got %d", len(due2))
	}
}

func TestUniqueConstraints(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a deployment
	d1 := &Deployment{
		ID:             "dep-1",
		DeploymentSpec: DeploymentSpec{Name: "api", Namespace: "default", Type: "service"},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := st.CreateDeployment(d1); err != nil {
		t.Fatalf("failed to create first deployment: %v", err)
	}

	// Try to create another with same namespace/name
	d2 := &Deployment{
		ID:             "dep-2",
		DeploymentSpec: DeploymentSpec{Name: "api", Namespace: "default", Type: "service"},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = st.CreateDeployment(d2)
	if err == nil {
		t.Error("expected error when creating duplicate deployment name")
	}

	// Different namespace should work
	d3 := &Deployment{
		ID:             "dep-3",
		DeploymentSpec: DeploymentSpec{Name: "api", Namespace: "prod", Type: "service"},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := st.CreateDeployment(d3); err != nil {
		t.Errorf("should allow same name in different namespace: %v", err)
	}
}

func TestCleanupOldExecutions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	now := time.Now()

	// Create old execution (31 days ago)
	oldExec := &Execution{
		ID:          "exec-old",
		Namespace:   "default",
		Name:        "old-job",
		Status:      "Running",
		StartedAt:   now.Add(-31 * 24 * time.Hour),
	}
	if err := st.CreateExecution(oldExec); err != nil {
		t.Fatalf("failed to create old execution: %v", err)
	}
	// Update to set completed_at
	oldExec.Status = "Completed"
	oldExec.CompletedAt = now.Add(-31 * 24 * time.Hour)
	if err := st.UpdateExecution(oldExec); err != nil {
		t.Fatalf("failed to update old execution: %v", err)
	}

	// Create recent execution (1 day ago)
	recentExec := &Execution{
		ID:          "exec-recent",
		Namespace:   "default",
		Name:        "recent-job",
		Status:      "Running",
		StartedAt:   now.Add(-24 * time.Hour),
	}
	if err := st.CreateExecution(recentExec); err != nil {
		t.Fatalf("failed to create recent execution: %v", err)
	}
	// Update to set completed_at
	recentExec.Status = "Completed"
	recentExec.CompletedAt = now.Add(-24 * time.Hour)
	if err := st.UpdateExecution(recentExec); err != nil {
		t.Fatalf("failed to update recent execution: %v", err)
	}

	// Cleanup executions older than 30 days
	deleted, err := st.CleanupOldExecutions(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Verify old is gone
	_, err = st.GetExecution("exec-old")
	if err == nil {
		t.Error("old execution should be deleted")
	}

	// Verify recent is still there
	_, err = st.GetExecution("exec-recent")
	if err != nil {
		t.Error("recent execution should still exist")
	}
}

func TestCleanupRunningExecutions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	now := time.Now()

	// Create stale running execution (2 hours ago)
	staleExec := &Execution{
		ID:        "exec-stale",
		Namespace: "default",
		Name:      "stale-job",
		Status:    "Running",
		StartedAt: now.Add(-2 * time.Hour),
	}
	if err := st.CreateExecution(staleExec); err != nil {
		t.Fatalf("failed to create stale execution: %v", err)
	}

	// Create recent running execution (5 minutes ago)
	recentExec := &Execution{
		ID:        "exec-recent-running",
		Namespace: "default",
		Name:      "recent-job",
		Status:    "Running",
		StartedAt: now.Add(-5 * time.Minute),
	}
	if err := st.CreateExecution(recentExec); err != nil {
		t.Fatalf("failed to create recent execution: %v", err)
	}

	// Cleanup running executions older than 1 hour
	affected, err := st.CleanupRunningExecutionsOlderThan(1 * time.Hour)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}

	// Verify stale is marked as failed
	stale, err := st.GetExecution("exec-stale")
	if err != nil {
		t.Fatalf("failed to get stale execution: %v", err)
	}
	if stale.Status != "Failed" {
		t.Errorf("stale should be Failed, got %s", stale.Status)
	}

	// Verify recent is still running
	recent, _ := st.GetExecution("exec-recent-running")
	if recent.Status != "Running" {
		t.Errorf("recent should still be Running, got %s", recent.Status)
	}
}

func TestGetExecutionStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	now := time.Now()

	// Create executions with different statuses
	statuses := []string{"Completed", "Completed", "Failed", "Running", "Pending"}
	for i, status := range statuses {
		exec := &Execution{
			ID:        NewExecutionID(),
			Namespace: "default",
			Name:      "test-job",
			Status:    status,
			StartedAt: now,
		}
		if status == "Completed" || status == "Failed" {
			exec.CompletedAt = now
		}
		if err := st.CreateExecution(exec); err != nil {
			t.Fatalf("failed to create execution %d: %v", i, err)
		}
	}

	stats, err := st.GetExecutionStats()
	if err != nil {
		t.Fatalf("GetExecutionStats failed: %v", err)
	}

	if stats.Total != 5 {
		t.Errorf("expected total 5, got %d", stats.Total)
	}
	if stats.Completed != 2 {
		t.Errorf("expected completed 2, got %d", stats.Completed)
	}
	if stats.Failed != 1 {
		t.Errorf("expected failed 1, got %d", stats.Failed)
	}
	if stats.Running != 1 {
		t.Errorf("expected running 1, got %d", stats.Running)
	}
	if stats.Pending != 1 {
		t.Errorf("expected pending 1, got %d", stats.Pending)
	}
}
