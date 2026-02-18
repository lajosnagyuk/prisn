package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lajosnagyuk/prisn/pkg/context"
)

func TestClientNew(t *testing.T) {
	ctx := &context.Resolved{
		Server:    "localhost:7331",
		Namespace: "default",
	}

	client, err := New(ctx)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if client.BaseURL() != "http://localhost:7331" {
		t.Errorf("expected http://localhost:7331, got %s", client.BaseURL())
	}

	if client.Context() != ctx {
		t.Error("Context() should return the same context")
	}
}

func TestClientWithTLS(t *testing.T) {
	ctx := &context.Resolved{
		Server:    "secure.example.com:7331",
		Namespace: "default",
		TLS: context.TLSConfig{
			Enabled: true,
		},
	}

	client, err := New(ctx)
	if err != nil {
		t.Fatalf("New with TLS failed: %v", err)
	}

	if client.BaseURL() != "https://secure.example.com:7331" {
		t.Errorf("expected https scheme, got %s", client.BaseURL())
	}
}

func TestClientGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/test" {
			t.Errorf("expected /test, got %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") != "prisn-cli/1.0" {
			t.Errorf("expected User-Agent header")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "test-ns",
	}

	client, _ := New(ctx)
	resp, err := client.Get("/test")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestClientAuthHeader(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
		Token:     "test-token-123",
	}

	client, _ := New(ctx)
	resp, _ := client.Get("/test")
	resp.Body.Close()

	expected := "Bearer test-token-123"
	if receivedAuth != expected {
		t.Errorf("expected auth %q, got %q", expected, receivedAuth)
	}
}

func TestClientNamespaceHeader(t *testing.T) {
	var receivedNS string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedNS = r.Header.Get("X-Prisn-Namespace")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "my-namespace",
	}

	client, _ := New(ctx)
	resp, _ := client.Get("/test")
	resp.Body.Close()

	if receivedNS != "my-namespace" {
		t.Errorf("expected namespace 'my-namespace', got %q", receivedNS)
	}
}

func TestClientPost(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json")
		}
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	resp, err := client.Post("/test", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	resp.Body.Close()

	if receivedBody["key"] != "value" {
		t.Errorf("expected body with key=value, got %v", receivedBody)
	}
}

func TestClientPut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	resp, err := client.Put("/test", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	resp.Body.Close()
}

func TestClientDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	resp, err := client.Delete("/test")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestClientGetJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	var result map[string]string
	err := client.GetJSON("/health", &result)
	if err != nil {
		t.Fatalf("GetJSON failed: %v", err)
	}

	if result["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", result)
	}
}

func TestClientPostJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "123", "name": req["name"]})
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	var result map[string]string
	err := client.PostJSON("/create", map[string]string{"name": "test"}, &result)
	if err != nil {
		t.Fatalf("PostJSON failed: %v", err)
	}

	if result["id"] != "123" {
		t.Errorf("expected id=123, got %v", result)
	}
}

func TestClientAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
	}))
	defer server.Close()

	ctx := &context.Resolved{
		Server:    server.Listener.Addr().String(),
		Namespace: "default",
	}

	client, _ := New(ctx)
	var result map[string]string
	err := client.GetJSON("/test", &result)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid request" {
		t.Errorf("expected message 'invalid request', got %q", apiErr.Message)
	}
}

func TestConnectError(t *testing.T) {
	ctx := &context.Resolved{
		Server:    "invalid-server:99999",
		Namespace: "default",
	}

	client, _ := New(ctx)
	_, err := client.Get("/test")
	if err == nil {
		t.Fatal("expected connection error")
	}

	if !IsOffline(err) {
		t.Error("expected IsOffline to return true")
	}

	connErr, ok := err.(*ConnectError)
	if !ok {
		t.Fatalf("expected ConnectError, got %T", err)
	}
	if connErr.Server != "invalid-server:99999" {
		t.Errorf("expected server 'invalid-server:99999', got %q", connErr.Server)
	}
}

func TestIsNotFound(t *testing.T) {
	err := &APIError{StatusCode: 404, Message: "not found"}
	if !IsNotFound(err) {
		t.Error("expected IsNotFound to return true for 404")
	}

	err = &APIError{StatusCode: 500, Message: "server error"}
	if IsNotFound(err) {
		t.Error("expected IsNotFound to return false for 500")
	}
}

func TestIsUnauthorized(t *testing.T) {
	err := &APIError{StatusCode: 401, Message: "unauthorized"}
	if !IsUnauthorized(err) {
		t.Error("expected IsUnauthorized to return true for 401")
	}

	err = &APIError{StatusCode: 403, Message: "forbidden"}
	if !IsUnauthorized(err) {
		t.Error("expected IsUnauthorized to return true for 403")
	}

	err = &APIError{StatusCode: 200, Message: "ok"}
	if IsUnauthorized(err) {
		t.Error("expected IsUnauthorized to return false for 200")
	}
}

func TestWithOfflineFallback(t *testing.T) {
	t.Run("online success", func(t *testing.T) {
		result, err := WithOfflineFallback(
			func() (string, error) { return "online data", nil },
			nil,
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Offline {
			t.Error("expected Offline=false")
		}
		if result.Data != "online data" {
			t.Errorf("expected 'online data', got %q", result.Data)
		}
	})

	t.Run("offline with fallback", func(t *testing.T) {
		fallback := "cached data"
		result, err := WithOfflineFallback(
			func() (string, error) { return "", &ConnectError{Server: "test", Err: nil} },
			&fallback,
			"Using cached data",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Offline {
			t.Error("expected Offline=true")
		}
		if result.Data != "cached data" {
			t.Errorf("expected 'cached data', got %q", result.Data)
		}
	})

	t.Run("offline without fallback", func(t *testing.T) {
		_, err := WithOfflineFallback(
			func() (string, error) { return "", &ConnectError{Server: "test", Err: nil} },
			nil,
			"",
		)
		if err == nil {
			t.Fatal("expected error when offline without fallback")
		}
	})

	t.Run("non-connection error", func(t *testing.T) {
		_, err := WithOfflineFallback(
			func() (string, error) { return "", &APIError{StatusCode: 500, Message: "server error"} },
			nil,
			"",
		)
		if err == nil {
			t.Fatal("expected error to propagate")
		}
		if IsOffline(err) {
			t.Error("expected non-offline error")
		}
	})
}
