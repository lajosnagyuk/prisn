// Package client provides an HTTP client for communicating with prisn servers.
//
// The client handles authentication, TLS configuration, and provides helpers
// for common API operations. It integrates with the context package for
// server configuration.
package client

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/context"
)

// Client wraps HTTP operations with context-aware auth and TLS.
type Client struct {
	ctx        *context.Resolved
	httpClient *http.Client
	baseURL    string
}

// New creates a new client from the resolved context.
func New(ctx *context.Resolved) (*Client, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// Configure TLS if enabled
	if ctx.TLS.Enabled || ctx.TLS.CAFile != "" || ctx.TLS.CertFile != "" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: ctx.TLS.Insecure,
			MinVersion:         tls.VersionTLS12,
		}

		// Custom CA certificate
		if ctx.TLS.CAFile != "" {
			caCert, err := os.ReadFile(ctx.TLS.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA file %s: %w", ctx.TLS.CAFile, err)
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate from %s", ctx.TLS.CAFile)
			}
			tlsConfig.RootCAs = caPool
		}

		// Client certificate (mTLS)
		if ctx.TLS.CertFile != "" && ctx.TLS.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(ctx.TLS.CertFile, ctx.TLS.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client certificate: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		transport.TLSClientConfig = tlsConfig
	}

	return &Client{
		ctx: ctx,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		baseURL: ctx.BaseURL(),
	}, nil
}

// Do performs an HTTP request with auth headers.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	// Add auth header if token is set
	if c.ctx.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.ctx.Token)
	}

	// Add common headers
	req.Header.Set("User-Agent", "prisn-cli/1.0")
	req.Header.Set("X-Prisn-Namespace", c.ctx.Namespace)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ConnectError{Server: c.ctx.Server, Err: err}
	}

	return resp, nil
}

// Get performs a GET request to the given path.
func (c *Client) Get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	return c.Do(req)
}

// Post performs a POST request with JSON body.
func (c *Client) Post(path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return c.Do(req)
}

// Put performs a PUT request with JSON body.
func (c *Client) Put(path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("PUT", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return c.Do(req)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	return c.Do(req)
}

// GetJSON performs a GET request and decodes the JSON response.
func (c *Client) GetJSON(path string, result any) error {
	resp, err := c.Get(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.errorFromResponse(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// PostJSON performs a POST request and decodes the JSON response.
func (c *Client) PostJSON(path string, body, result any) error {
	resp, err := c.Post(path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return c.errorFromResponse(resp)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// PutJSON performs a PUT request and decodes the JSON response.
func (c *Client) PutJSON(path string, body, result any) error {
	resp, err := c.Put(path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.errorFromResponse(resp)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// errorFromResponse creates an error from an HTTP response.
func (c *Client) errorFromResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	// Try to parse as JSON error
	var apiErr struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &apiErr) == nil {
		msg := apiErr.Error
		if msg == "" {
			msg = apiErr.Message
		}
		if msg != "" {
			return &APIError{
				StatusCode: resp.StatusCode,
				Message:    msg,
			}
		}
	}

	// Fall back to raw body
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
	}
}

// Context returns the resolved context this client is using.
func (c *Client) Context() *context.Resolved {
	return c.ctx
}

// BaseURL returns the base URL for this client.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// ConnectError indicates a connection failure.
type ConnectError struct {
	Server string
	Err    error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("failed to connect to %s: %v", e.Server, e.Err)
}

func (e *ConnectError) Unwrap() error {
	return e.Err
}

// APIError indicates an error response from the server.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server error (%d): %s", e.StatusCode, e.Message)
}

// IsOffline checks if the error indicates the server is unreachable.
func IsOffline(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*ConnectError)
	return ok
}

// IsNotFound checks if the error indicates a 404 response.
func IsNotFound(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsUnauthorized checks if the error indicates a 401/403 response.
func IsUnauthorized(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}
	return false
}
