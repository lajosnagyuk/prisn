package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/prisn/pkg/api"
	"github.com/lajosnagyuk/prisn/pkg/runner"
	"github.com/lajosnagyuk/prisn/pkg/store"
)

// Integration tests for CLI-to-server communication.
// These tests verify the full flow: HTTP -> Server -> Response.

// testServer holds a running test server instance.
type testServer struct {
	httpServer *httptest.Server
	server     *api.Server
	store      *store.Store
}

// startTestServer starts a prisn API server for testing using httptest.Server.
func startTestServer(t *testing.T) *testServer {
	t.Helper()

	// Create temp directory for store
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create store
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create runner
	r, err := runner.New()
	if err != nil {
		st.Close()
		t.Fatalf("failed to create runner: %v", err)
	}

	// Create server (but don't call Start - we'll use httptest.Server instead)
	server, err := api.NewServer(api.Config{
		Addr:   ":0", // Not used since we use httptest
		Store:  st,
		Runner: r,
		NodeID: "test-node",
	})
	if err != nil {
		st.Close()
		t.Fatalf("failed to create server: %v", err)
	}

	// Create httptest server with the server's handler
	httpServer := httptest.NewServer(server.Handler())

	ts := &testServer{
		httpServer: httpServer,
		server:     server,
		store:      st,
	}

	t.Cleanup(func() {
		httpServer.Close()
		st.Close()
	})

	return ts
}

// URL returns the test server URL.
func (ts *testServer) URL() string {
	return ts.httpServer.URL
}

// apiCall makes an HTTP request to the test server.
func (ts *testServer) apiCall(method, path string, body interface{}) (*http.Response, []byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, ts.URL()+path, reqBody)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

// createDeployment creates a deployment via the API.
func (ts *testServer) createDeployment(t *testing.T, name, deployType, source string, port, replicas int) string {
	t.Helper()

	req := api.CreateDeploymentRequest{
		Name:     name,
		Type:     deployType,
		Runtime:  "bash",
		Source:   source,
		Port:     port,
		Replicas: replicas,
	}

	resp, body, err := ts.apiCall("POST", "/api/v1/deployments", req)
	if err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	return d.ID
}

// ============================================================================
// Test 1: Server Health Check
// Verifies: Server responds to health endpoint with correct status
// ============================================================================

func TestIntegration_ServerHealthCheck(t *testing.T) {
	ts := startTestServer(t)

	resp, body, err := ts.apiCall("GET", "/health", nil)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var health struct {
		Status string `json:"status"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("failed to unmarshal health: %v", err)
	}

	if health.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", health.Status)
	}
	if health.NodeID == "" {
		t.Error("expected non-empty node_id")
	}
}

// ============================================================================
// Test 2: Create Service Deployment
// Verifies: POST /api/v1/deployments creates a service and returns it
// ============================================================================

func TestIntegration_CreateServiceDeployment(t *testing.T) {
	ts := startTestServer(t)

	// Create a test script
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "service.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho running"), 0755); err != nil {
		t.Fatal(err)
	}

	req := api.CreateDeploymentRequest{
		Name:     "test-service",
		Type:     "service",
		Runtime:  "bash",
		Source:   scriptPath,
		Port:     9000,
		Replicas: 2,
	}

	resp, body, err := ts.apiCall("POST", "/api/v1/deployments", req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Verify returned deployment
	if d.Name != "test-service" {
		t.Errorf("expected name 'test-service', got %q", d.Name)
	}
	if d.Type != "service" {
		t.Errorf("expected type 'service', got %q", d.Type)
	}
	if d.Port != 9000 {
		t.Errorf("expected port 9000, got %d", d.Port)
	}
	if d.Replicas != 2 {
		t.Errorf("expected replicas 2, got %d", d.Replicas)
	}
	if d.ID == "" {
		t.Error("expected non-empty ID")
	}
}

// ============================================================================
// Test 3: List Deployments
// Verifies: GET /api/v1/deployments returns list with created deployments
// ============================================================================

func TestIntegration_ListDeployments(t *testing.T) {
	ts := startTestServer(t)

	// Create test script
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "list-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create two deployments
	ts.createDeployment(t, "app-one", "service", scriptPath, 8001, 1)
	ts.createDeployment(t, "app-two", "service", scriptPath, 8002, 2)

	// List deployments
	resp, body, err := ts.apiCall("GET", "/api/v1/deployments", nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Count int                `json:"count"`
		Items []store.Deployment `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if result.Count != 2 {
		t.Errorf("expected count 2, got %d", result.Count)
	}

	// Verify both apps are in the list
	names := make(map[string]bool)
	for _, d := range result.Items {
		names[d.Name] = true
	}
	if !names["app-one"] {
		t.Error("app-one not in list")
	}
	if !names["app-two"] {
		t.Error("app-two not in list")
	}
}

// ============================================================================
// Test 4: Get Deployment by ID
// Verifies: GET /api/v1/deployments/{id} returns specific deployment
// ============================================================================

func TestIntegration_GetDeploymentByID(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "get-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create deployment
	id := ts.createDeployment(t, "get-by-id-test", "service", scriptPath, 8003, 1)

	// Get by ID
	resp, body, err := ts.apiCall("GET", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.ID != id {
		t.Errorf("expected ID %q, got %q", id, d.ID)
	}
	if d.Name != "get-by-id-test" {
		t.Errorf("expected name 'get-by-id-test', got %q", d.Name)
	}
}

// ============================================================================
// Test 5: Get Deployment by Name
// Verifies: GET /api/v1/deployments/{name} returns specific deployment
// ============================================================================

func TestIntegration_GetDeploymentByName(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "name-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create deployment
	id := ts.createDeployment(t, "get-by-name-test", "service", scriptPath, 8004, 1)

	// Get by name
	resp, body, err := ts.apiCall("GET", "/api/v1/deployments/get-by-name-test", nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.ID != id {
		t.Errorf("expected ID %q, got %q", id, d.ID)
	}
	if d.Name != "get-by-name-test" {
		t.Errorf("expected name 'get-by-name-test', got %q", d.Name)
	}
}

// ============================================================================
// Test 6: Scale Deployment Up
// Verifies: POST /api/v1/deployments/{id}/scale increases replicas
// ============================================================================

func TestIntegration_ScaleDeploymentUp(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "scale-up.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create with 1 replica
	id := ts.createDeployment(t, "scale-up-test", "service", scriptPath, 8005, 1)

	// Scale to 5
	scaleReq := map[string]int{"replicas": 5}
	resp, body, err := ts.apiCall("POST", "/api/v1/deployments/"+id+"/scale", scaleReq)
	if err != nil {
		t.Fatalf("scale failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify scaled
	resp, body, err = ts.apiCall("GET", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.Replicas != 5 {
		t.Errorf("expected replicas 5, got %d", d.Replicas)
	}
}

// ============================================================================
// Test 7: Scale Deployment to Zero (Pause)
// Verifies: Scaling to 0 pauses the deployment
// ============================================================================

func TestIntegration_ScaleToZero(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "scale-zero.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create with 2 replicas
	id := ts.createDeployment(t, "scale-zero-test", "service", scriptPath, 8006, 2)

	// Scale to 0
	scaleReq := map[string]int{"replicas": 0}
	resp, body, err := ts.apiCall("POST", "/api/v1/deployments/"+id+"/scale", scaleReq)
	if err != nil {
		t.Fatalf("scale failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify scaled to 0
	resp, body, err = ts.apiCall("GET", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.Replicas != 0 {
		t.Errorf("expected replicas 0, got %d", d.Replicas)
	}
}

// ============================================================================
// Test 8: Scale Back Up from Zero
// Verifies: Scaling from 0 to N restarts the deployment
// ============================================================================

func TestIntegration_ScaleBackFromZero(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "scale-back.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nsleep 60"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create with 1 replica
	id := ts.createDeployment(t, "scale-back-test", "service", scriptPath, 8007, 1)

	// Scale to 0
	scaleReq := map[string]int{"replicas": 0}
	_, _, err := ts.apiCall("POST", "/api/v1/deployments/"+id+"/scale", scaleReq)
	if err != nil {
		t.Fatalf("scale to 0 failed: %v", err)
	}

	// Scale back to 3
	scaleReq = map[string]int{"replicas": 3}
	resp, body, err := ts.apiCall("POST", "/api/v1/deployments/"+id+"/scale", scaleReq)
	if err != nil {
		t.Fatalf("scale back failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify scaled
	resp, body, err = ts.apiCall("GET", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.Replicas != 3 {
		t.Errorf("expected replicas 3, got %d", d.Replicas)
	}
}

// ============================================================================
// Test 9: Delete Deployment
// Verifies: DELETE /api/v1/deployments/{id} removes the deployment
// ============================================================================

func TestIntegration_DeleteDeployment(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "delete-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create deployment
	id := ts.createDeployment(t, "delete-test", "service", scriptPath, 8008, 1)

	// Delete it
	resp, _, err := ts.apiCall("DELETE", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Verify it's gone
	resp, _, err = ts.apiCall("GET", "/api/v1/deployments/"+id, nil)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

// ============================================================================
// Test 10: Create Job (Not Service)
// Verifies: Jobs can be created and have type "job"
// ============================================================================

func TestIntegration_CreateJob(t *testing.T) {
	ts := startTestServer(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "job.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho job done"), 0755); err != nil {
		t.Fatal(err)
	}

	req := api.CreateDeploymentRequest{
		Name:    "test-job",
		Type:    "job",
		Runtime: "bash",
		Source:  scriptPath,
	}

	resp, body, err := ts.apiCall("POST", "/api/v1/deployments", req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var d store.Deployment
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if d.Type != "job" {
		t.Errorf("expected type 'job', got %q", d.Type)
	}
	if d.Name != "test-job" {
		t.Errorf("expected name 'test-job', got %q", d.Name)
	}
}

