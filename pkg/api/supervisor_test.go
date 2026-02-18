package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/lajosnagyuk/prisn/pkg/supervisor"
)

// TestSupervisorServiceLifecycle tests that services are started/stopped with supervisor.
func TestSupervisorServiceLifecycle(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a test script that runs briefly
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "test-service.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nwhile true; do sleep 1; done"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a service deployment via API
	reqBody := CreateDeploymentRequest{
		Name:     "test-svc",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9999,
		Replicas: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Give supervisor time to start the process
	time.Sleep(500 * time.Millisecond)

	// Check supervisor has the service
	id := getDeploymentID(t, rr.Body.Bytes())
	status := s.supervisor.Status(id)
	if status == nil {
		t.Fatal("expected service to be in supervisor")
	}
	if status.State != supervisor.StateRunning {
		t.Errorf("expected state Running, got %s", status.State)
	}

	// Stop via scaling to 0 - use mux for proper path value routing
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/deployments/{id}/scale", s.handleScaleDeployment)

	scaleReq := httptest.NewRequest("POST", "/api/v1/deployments/"+id+"/scale",
		bytes.NewReader([]byte(`{"replicas": 0}`)))
	scaleRR := httptest.NewRecorder()
	mux.ServeHTTP(scaleRR, scaleReq)

	if scaleRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for scale, got %d: %s", scaleRR.Code, scaleRR.Body.String())
	}

	// Give supervisor time to stop
	time.Sleep(500 * time.Millisecond)

	// Verify process stopped (supervisor should have stopped it)
	status = s.supervisor.Status(id)
	if status != nil && status.State == supervisor.StateRunning {
		t.Error("expected service to be stopped after scale to 0")
	}
}

// TestSupervisorServiceStartsOnCreate verifies service starts immediately on create.
func TestSupervisorServiceStartsOnCreate(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a test script
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "service.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	reqBody := CreateDeploymentRequest{
		Name:     "start-test",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9998,
		Replicas: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	// Wait for supervisor to start
	time.Sleep(200 * time.Millisecond)

	// Verify it's in the supervisor
	id := getDeploymentID(t, rr.Body.Bytes())
	status := s.supervisor.Status(id)
	if status == nil {
		t.Fatal("expected service to be in supervisor after create")
	}

	// Clean up - stop the service
	s.supervisor.Stop(id)
}

// TestSupervisorServiceStopsOnDelete verifies service stops when deleted.
func TestSupervisorServiceStopsOnDelete(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a test script
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "delete-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create deployment
	reqBody := CreateDeploymentRequest{
		Name:     "delete-test",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9997,
		Replicas: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	id := getDeploymentID(t, rr.Body.Bytes())
	time.Sleep(200 * time.Millisecond)

	// Verify running
	status := s.supervisor.Status(id)
	if status == nil || status.State != supervisor.StateRunning {
		t.Fatal("expected service to be running")
	}

	// Delete deployment - use mux for proper path value routing
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/deployments/{id}", s.handleDeleteDeployment)

	delReq := httptest.NewRequest("DELETE", "/api/v1/deployments/"+id, nil)
	delRR := httptest.NewRecorder()
	mux.ServeHTTP(delRR, delReq)

	if delRR.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for delete, got %d", delRR.Code)
	}

	// Give time for process to stop
	time.Sleep(200 * time.Millisecond)

	// Supervisor stop was called (service removed)
	status = s.supervisor.Status(id)
	if status != nil && status.State == supervisor.StateRunning {
		t.Error("expected service to be stopped after delete")
	}
}

// TestSupervisorJobNotStarted verifies jobs don't start in supervisor.
func TestSupervisorJobNotStarted(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a job (not service) deployment
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "job.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho done"), 0755); err != nil {
		t.Fatal(err)
	}

	reqBody := CreateDeploymentRequest{
		Name:     "test-job",
		Type:     "job", // Not a service
		Runtime:  "bash",
		Source:   scriptPath,
		Replicas: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	// Jobs should NOT be in supervisor
	id := getDeploymentID(t, rr.Body.Bytes())
	status := s.supervisor.Status(id)
	if status != nil {
		t.Error("jobs should not be added to supervisor")
	}
}

// TestSupervisorZeroReplicasDefaultsToOne verifies that 0 replicas defaults to 1 and starts.
func TestSupervisorZeroReplicasDefaultsToOne(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "zero.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	reqBody := CreateDeploymentRequest{
		Name:     "zero-replicas",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9996,
		Replicas: 0, // Will be defaulted to 1
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	// Verify that replicas was defaulted to 1
	var resp store.Deployment
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Replicas != 1 {
		t.Errorf("expected replicas to default to 1, got %d", resp.Replicas)
	}

	// Service should be started since replicas defaults to 1
	time.Sleep(200 * time.Millisecond)
	status := s.supervisor.Status(resp.ID)
	if status == nil {
		t.Error("expected service to be started (0 replicas defaults to 1)")
	}

	// Clean up
	s.supervisor.Stop(resp.ID)
}

// TestSupervisorScaleUpFromZero verifies scaling up from 0 starts the service.
func TestSupervisorScaleUpFromZero(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "scale-up.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create service with 1 replica (can't create with 0 - it defaults to 1)
	reqBody := CreateDeploymentRequest{
		Name:     "scale-up-test",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9995,
		Replicas: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	id := getDeploymentID(t, rr.Body.Bytes())
	time.Sleep(200 * time.Millisecond)

	// Verify it's running
	status := s.supervisor.Status(id)
	if status == nil || status.State != supervisor.StateRunning {
		t.Fatal("expected service to be running after create")
	}

	// Scale to 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/deployments/{id}/scale", s.handleScaleDeployment)

	scaleReq := httptest.NewRequest("POST", "/api/v1/deployments/"+id+"/scale",
		bytes.NewReader([]byte(`{"replicas": 0}`)))
	scaleRR := httptest.NewRecorder()
	mux.ServeHTTP(scaleRR, scaleReq)

	if scaleRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for scale to 0, got %d", scaleRR.Code)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify stopped
	status = s.supervisor.Status(id)
	if status != nil && status.State == supervisor.StateRunning {
		t.Error("expected service to be stopped after scale to 0")
	}

	// Scale back up to 1
	scaleReq2 := httptest.NewRequest("POST", "/api/v1/deployments/"+id+"/scale",
		bytes.NewReader([]byte(`{"replicas": 1}`)))
	scaleRR2 := httptest.NewRecorder()
	mux.ServeHTTP(scaleRR2, scaleReq2)

	if scaleRR2.Code != http.StatusOK {
		t.Fatalf("expected 200 for scale to 1, got %d", scaleRR2.Code)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify running again
	status = s.supervisor.Status(id)
	if status == nil {
		t.Error("expected service to be in supervisor after scale up")
	} else if status.State != supervisor.StateRunning {
		t.Errorf("expected state Running after scale up, got %s", status.State)
	}

	// Clean up
	s.supervisor.Stop(id)
}

// TestGetDeploymentByIDOrName verifies lookup works by both ID and name.
func TestGetDeploymentByIDOrName(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a deployment
	reqBody := CreateDeploymentRequest{
		Name:    "lookup-test",
		Type:    "job",
		Runtime: "bash",
		Source:  "/tmp/test.sh",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/deployments", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateDeployment(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	id := getDeploymentID(t, rr.Body.Bytes())

	t.Run("lookup by ID", func(t *testing.T) {
		d, err := s.getDeploymentByIDOrName(id)
		if err != nil {
			t.Fatalf("lookup by ID failed: %v", err)
		}
		if d.Name != "lookup-test" {
			t.Errorf("expected name 'lookup-test', got %q", d.Name)
		}
	})

	t.Run("lookup by name", func(t *testing.T) {
		d, err := s.getDeploymentByIDOrName("lookup-test")
		if err != nil {
			t.Fatalf("lookup by name failed: %v", err)
		}
		if d.ID != id {
			t.Errorf("expected ID %q, got %q", id, d.ID)
		}
	})

	t.Run("lookup non-existent", func(t *testing.T) {
		_, err := s.getDeploymentByIDOrName("non-existent")
		if err == nil {
			t.Error("expected error for non-existent deployment")
		}
	})
}

// TestBuildCommand verifies command building for different runtimes.
func TestBuildCommand(t *testing.T) {
	tests := []struct {
		runtime   string
		source    string
		extraArgs []string
		wantCmd   string
		wantArgs  []string
	}{
		{"python3", "/app/main.py", nil, "python3", []string{"/app/main.py"}},
		{"python", "/app/script.py", []string{"--debug"}, "python3", []string{"/app/script.py", "--debug"}},
		{"node", "/app/index.js", nil, "node", []string{"/app/index.js"}},
		{"bash", "/app/run.sh", []string{"-v"}, "bash", []string{"/app/run.sh", "-v"}},
		{"custom-runtime", "/app/main", nil, "custom-runtime", []string{"/app/main"}},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			cmd, args := buildCommand(tt.runtime, tt.source, tt.extraArgs)
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if len(args) != len(tt.wantArgs) {
				t.Errorf("args = %v, want %v", args, tt.wantArgs)
			}
		})
	}
}

// TestDetectRuntimeFromSource verifies runtime detection from file extension.
func TestDetectRuntimeFromSource(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{"/app/main.py", "python3"},
		{"/app/index.js", "node"},
		{"/app/script.sh", "bash"},
		{"/app/binary", "python3"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := detectRuntimeFromSource(tt.source)
			if got != tt.want {
				t.Errorf("detectRuntimeFromSource(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

// Helper to extract deployment ID from response body.
func getDeploymentID(t *testing.T, body []byte) string {
	t.Helper()
	var resp store.Deployment
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal deployment: %v", err)
	}
	return resp.ID
}
