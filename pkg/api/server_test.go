package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/runner"
	"github.com/lajosnagyuk/prisn/pkg/store"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	// Create temp database
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	r, err := runner.New()
	if err != nil {
		st.Close()
		t.Fatalf("failed to create runner: %v", err)
	}

	s, err := NewServer(Config{
		Addr:   ":0",
		Store:  st,
		Runner: r,
		NodeID: "test-node",
	})
	if err != nil {
		st.Close()
		t.Fatalf("failed to create server: %v", err)
	}

	cleanup := func() {
		st.Close()
	}

	return s, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	s.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp healthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", resp.Status)
	}
	if resp.NodeID != "test-node" {
		t.Errorf("expected node_id 'test-node', got %q", resp.NodeID)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	s.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "prisn_deployments_total") {
		t.Error("expected prisn_deployments_total metric")
	}
	if !strings.Contains(body, "prisn_uptime_seconds") {
		t.Error("expected prisn_uptime_seconds metric")
	}
}

func TestDeploymentsAPI(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	t.Run("create deployment", func(t *testing.T) {
		body := `{
			"name": "test-app",
			"namespace": "default",
			"type": "service",
			"source": "/path/to/script.py",
			"port": 8080,
			"replicas": 2
		}`
		req := httptest.NewRequest("POST", "/api/v1/deployments", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateDeployment(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}

		var d store.Deployment
		if err := json.NewDecoder(rr.Body).Decode(&d); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if d.Name != "test-app" {
			t.Errorf("expected name 'test-app', got %q", d.Name)
		}
		if d.Replicas != 2 {
			t.Errorf("expected 2 replicas, got %d", d.Replicas)
		}
	})

	t.Run("list deployments", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
		rr := httptest.NewRecorder()

		s.handleListDeployments(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		var resp struct {
			Items []store.Deployment `json:"items"`
			Count int                `json:"count"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Count != 1 {
			t.Errorf("expected 1 deployment, got %d", resp.Count)
		}
	})

	t.Run("create duplicate deployment", func(t *testing.T) {
		body := `{
			"name": "test-app",
			"namespace": "default",
			"source": "/path/to/script.py"
		}`
		req := httptest.NewRequest("POST", "/api/v1/deployments", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateDeployment(rr, req)

		if rr.Code != http.StatusConflict {
			t.Errorf("expected 409 conflict, got %d", rr.Code)
		}
	})

	t.Run("create deployment without name", func(t *testing.T) {
		body := `{"source": "/path/to/script.py"}`
		req := httptest.NewRequest("POST", "/api/v1/deployments", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateDeployment(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("create deployment without source", func(t *testing.T) {
		body := `{"name": "no-source"}`
		req := httptest.NewRequest("POST", "/api/v1/deployments", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateDeployment(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})
}

func TestGetDeployment(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a deployment first
	d := &store.Deployment{
		ID: "dep-123",
		DeploymentSpec: store.DeploymentSpec{
			Name:      "test",
			Namespace: "default",
			Source:    "/path/to/script.py",
			Replicas:  1,
		},
		Status: store.DeploymentStatus{
			Phase: "Running",
		},
		CreatedAt: time.Now(),
	}
	s.store.CreateDeployment(d)

	t.Run("get existing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments/dep-123", nil)
		req.SetPathValue("id", "dep-123")
		rr := httptest.NewRecorder()

		s.handleGetDeployment(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("get non-existent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments/non-existent", nil)
		req.SetPathValue("id", "non-existent")
		rr := httptest.NewRecorder()

		s.handleGetDeployment(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})
}

func TestScaleDeployment(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	d := &store.Deployment{
		ID: "dep-scale",
		DeploymentSpec: store.DeploymentSpec{
			Name:      "scale-test",
			Namespace: "default",
			Source:    "/path/to/script.py",
			Replicas:  1,
		},
		Status:    store.DeploymentStatus{Phase: "Running"},
		CreatedAt: time.Now(),
	}
	s.store.CreateDeployment(d)

	t.Run("scale up", func(t *testing.T) {
		body := `{"replicas": 5}`
		req := httptest.NewRequest("POST", "/api/v1/deployments/dep-scale/scale", strings.NewReader(body))
		req.SetPathValue("id", "dep-scale")
		rr := httptest.NewRecorder()

		s.handleScaleDeployment(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		// Verify scale
		d, _ := s.store.GetDeployment("dep-scale")
		if d.Replicas != 5 {
			t.Errorf("expected 5 replicas, got %d", d.Replicas)
		}
	})

	t.Run("scale to zero", func(t *testing.T) {
		body := `{"replicas": 0}`
		req := httptest.NewRequest("POST", "/api/v1/deployments/dep-scale/scale", strings.NewReader(body))
		req.SetPathValue("id", "dep-scale")
		rr := httptest.NewRecorder()

		s.handleScaleDeployment(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		d, _ := s.store.GetDeployment("dep-scale")
		if d.Replicas != 0 {
			t.Errorf("expected 0 replicas, got %d", d.Replicas)
		}
	})

	t.Run("negative replicas", func(t *testing.T) {
		body := `{"replicas": -1}`
		req := httptest.NewRequest("POST", "/api/v1/deployments/dep-scale/scale", strings.NewReader(body))
		req.SetPathValue("id", "dep-scale")
		rr := httptest.NewRecorder()

		s.handleScaleDeployment(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})
}

func TestDeleteDeployment(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	d := &store.Deployment{
		ID: "dep-delete",
		DeploymentSpec: store.DeploymentSpec{
			Name:      "delete-test",
			Namespace: "default",
			Source:    "/path/to/script.py",
		},
		CreatedAt: time.Now(),
	}
	s.store.CreateDeployment(d)

	t.Run("delete existing", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/deployments/dep-delete", nil)
		req.SetPathValue("id", "dep-delete")
		rr := httptest.NewRecorder()

		s.handleDeleteDeployment(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", rr.Code)
		}

		// Verify deletion
		_, err := s.store.GetDeployment("dep-delete")
		if err == nil {
			t.Error("deployment should be deleted")
		}
	})

	t.Run("delete non-existent", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/deployments/non-existent", nil)
		req.SetPathValue("id", "non-existent")
		rr := httptest.NewRecorder()

		s.handleDeleteDeployment(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})
}

func TestExecutionsAPI(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a simple test script
	dir := t.TempDir()
	script := filepath.Join(dir, "test.py")
	os.WriteFile(script, []byte("print('hello')"), 0644)

	t.Run("create execution sync", func(t *testing.T) {
		body, _ := json.Marshal(CreateExecutionRequest{
			Source:    script,
			Namespace: "default",
		})
		req := httptest.NewRequest("POST", "/api/v1/executions", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateExecution(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var exec store.Execution
		if err := json.NewDecoder(rr.Body).Decode(&exec); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if exec.Status != "Completed" {
			t.Errorf("expected status 'Completed', got %q", exec.Status)
		}
	})

	t.Run("create execution async", func(t *testing.T) {
		body, _ := json.Marshal(CreateExecutionRequest{
			Source:    script,
			Namespace: "default",
			Async:     true,
		})
		req := httptest.NewRequest("POST", "/api/v1/executions", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateExecution(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Errorf("expected 202 accepted, got %d", rr.Code)
		}

		var exec store.Execution
		json.NewDecoder(rr.Body).Decode(&exec)
		if exec.Status != "Pending" {
			t.Errorf("async execution should be pending, got %q", exec.Status)
		}

		// Wait for async completion
		time.Sleep(500 * time.Millisecond)
	})

	t.Run("create without source or deployment", func(t *testing.T) {
		body := `{"namespace": "default"}`
		req := httptest.NewRequest("POST", "/api/v1/executions", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateExecution(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("list executions", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/executions", nil)
		rr := httptest.NewRecorder()

		s.handleListExecutions(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestSecretsAPI(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	t.Run("create secret", func(t *testing.T) {
		body := `{
			"name": "db-creds",
			"namespace": "default",
			"data": {
				"username": "admin",
				"password": "secret123"
			}
		}`
		req := httptest.NewRequest("POST", "/api/v1/secrets", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateSecret(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("create duplicate secret", func(t *testing.T) {
		body := `{
			"name": "db-creds",
			"namespace": "default",
			"data": {"key": "value"}
		}`
		req := httptest.NewRequest("POST", "/api/v1/secrets", strings.NewReader(body))
		rr := httptest.NewRecorder()

		s.handleCreateSecret(rr, req)

		if rr.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rr.Code)
		}
	})

	t.Run("get secret", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/secrets/db-creds?namespace=default", nil)
		req.SetPathValue("name", "db-creds")
		rr := httptest.NewRecorder()

		s.handleGetSecret(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("get non-existent secret", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/secrets/non-existent", nil)
		req.SetPathValue("name", "non-existent")
		rr := httptest.NewRecorder()

		s.handleGetSecret(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("delete secret", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/v1/secrets/db-creds?namespace=default", nil)
		req.SetPathValue("name", "db-creds")
		rr := httptest.NewRecorder()

		s.handleDeleteSecret(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", rr.Code)
		}
	})
}

func TestMiddleware(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	handler := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": true}`))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Type") != "application/json" {
		t.Error("expected Content-Type application/json")
	}
	if rr.Header().Get("X-Prisn-Node") != "test-node" {
		t.Error("expected X-Prisn-Node header")
	}
}

func TestErrorResponse(t *testing.T) {
	s, cleanup := setupTestServer(t)
	defer cleanup()

	rr := httptest.NewRecorder()
	s.writeError(rr, http.StatusBadRequest, fmt.Errorf("test error"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var resp errorResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Message != "test error" {
		t.Errorf("expected message 'test error', got %q", resp.Message)
	}
}

func TestResponseWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rr, status: 200}

	rw.WriteHeader(http.StatusCreated)

	if rw.status != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rw.status)
	}
}

func TestAPIAuthentication(t *testing.T) {
	// Create server with API key
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, _ := store.Open(dbPath)
	defer st.Close()
	r, _ := runner.New()

	s, err := NewServer(Config{
		Addr:   ":0",
		Store:  st,
		Runner: r,
		NodeID: "test-node",
		APIKey: "test-secret-key",
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	t.Run("health endpoint without auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("health should not require auth, got %d", rr.Code)
		}
	})

	t.Run("api endpoint without auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
		rr := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("api endpoint with wrong key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
		req.Header.Set("Authorization", "Bearer wrong-key")
		rr := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("api endpoint with correct bearer key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
		req.Header.Set("Authorization", "Bearer test-secret-key")
		rr := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("api endpoint with raw key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
		req.Header.Set("Authorization", "test-secret-key")
		rr := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestIsPublicEndpoint(t *testing.T) {
	tests := []struct {
		path   string
		public bool
	}{
		{"/health", true},
		{"/metrics", true},
		{"/api/v1/deployments", false},
		{"/api/v1/executions", false},
		{"/api/v1/secrets", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if isPublicEndpoint(tt.path) != tt.public {
				t.Errorf("%s: expected public=%v", tt.path, tt.public)
			}
		})
	}
}
