// Package api provides the HTTP API for prisn server.
//
// Endpoints:
//   GET    /api/v1/deployments          - List deployments
//   POST   /api/v1/deployments          - Create deployment
//   GET    /api/v1/deployments/:id      - Get deployment
//   PUT    /api/v1/deployments/:id      - Update deployment
//   DELETE /api/v1/deployments/:id      - Delete deployment
//   POST   /api/v1/deployments/:id/scale - Scale deployment
//
//   GET    /api/v1/executions           - List executions
//   POST   /api/v1/executions           - Create (run) execution
//   GET    /api/v1/executions/:id       - Get execution
//   GET    /api/v1/executions/:id/logs  - Get execution logs
//
//   GET    /api/v1/secrets              - List secrets
//   POST   /api/v1/secrets              - Create secret
//   GET    /api/v1/secrets/:name        - Get secret
//   DELETE /api/v1/secrets/:name        - Delete secret
//
//   GET    /health                      - Health check
//   GET    /metrics                     - Prometheus metrics
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/raft"
	"github.com/lajosnagyuk/prisn/pkg/runner"
	"github.com/lajosnagyuk/prisn/pkg/scheduler"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/lajosnagyuk/prisn/pkg/supervisor"
	"github.com/lajosnagyuk/prisn/pkg/validate"
	"golang.org/x/time/rate"
)

// Secret sanitization patterns - matches common secret formats
var secretPatterns = []*regexp.Regexp{
	// AWS keys
	regexp.MustCompile(`(?i)(aws_secret_access_key|aws_access_key_id)\s*[=:]\s*['"]?([A-Za-z0-9/+=]{20,})['"]?`),
	// API keys and tokens
	regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret[_-]?key|auth[_-]?token|bearer)\s*[=:]\s*['"]?([A-Za-z0-9_\-]{16,})['"]?`),
	// Database URLs with passwords
	regexp.MustCompile(`(?i)(postgres|mysql|mongodb|redis)://[^:]+:([^@]+)@`),
	// Generic password patterns
	regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[=:]\s*['"]?([^\s'"]{8,})['"]?`),
	// Private keys
	regexp.MustCompile(`(?i)-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
	// JWT tokens
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	// GitHub/GitLab tokens
	regexp.MustCompile(`(?i)(ghp_[A-Za-z0-9]{36}|gho_[A-Za-z0-9]{36}|glpat-[A-Za-z0-9\-]{20,})`),
	// Prisn tokens
	regexp.MustCompile(`prisn_[A-Za-z0-9]{32,}`),
}

// sanitizeOutput removes potential secrets from execution output.
func sanitizeOutput(output string) string {
	result := output
	for _, pattern := range secretPatterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			// For patterns with capture groups, try to preserve the key name
			if submatch := pattern.FindStringSubmatch(match); len(submatch) > 1 {
				return submatch[1] + "=[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return result
}

// Server is the HTTP API server.
type Server struct {
	store      *store.Store
	runner     *runner.Runner
	supervisor *supervisor.Supervisor
	scheduler  *scheduler.Scheduler // Cron job scheduler
	addr       string
	server     *http.Server
	mux        *http.ServeMux
	mu         sync.RWMutex
	nodeID     string
	startedAt  time.Time
	apiKey     string          // Legacy API key for backward compatibility
	authMW     *AuthMiddleware // RBAC authentication middleware
	stopCh     chan struct{}
	cluster    *raft.Cluster   // Optional cluster for HA mode

	// Rate limiting
	rateLimiters   map[string]*rate.Limiter
	rateLimitersMu sync.RWMutex
}

// Config holds server configuration.
type Config struct {
	Addr      string
	Store     *store.Store
	Runner    *runner.Runner
	Scheduler *scheduler.Scheduler // Cron job scheduler (optional)
	NodeID    string
	APIKey    string        // Optional API key for authentication (if empty, no auth required)
	Cluster   *raft.Cluster // Optional cluster for HA mode (if nil, single-node mode)
}

// NewServer creates a new API server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Addr == "" {
		cfg.Addr = ":7331"
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "node-1"
	}

	s := &Server{
		store:        cfg.Store,
		runner:       cfg.Runner,
		supervisor:   supervisor.New(),
		scheduler:    cfg.Scheduler,
		addr:         cfg.Addr,
		nodeID:       cfg.NodeID,
		startedAt:    time.Now(),
		apiKey:       cfg.APIKey,
		authMW:       NewAuthMiddleware(cfg.Store, cfg.APIKey),
		stopCh:       make(chan struct{}),
		cluster:      cfg.Cluster,
		rateLimiters: make(map[string]*rate.Limiter),
	}

	mux := http.NewServeMux()

	// Register Raft handlers if cluster mode is enabled
	if cfg.Cluster != nil {
		cfg.Cluster.Transport().RegisterHandlers(mux)
	}

	// Health and metrics
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Deployments API
	mux.HandleFunc("GET /api/v1/deployments", s.handleListDeployments)
	mux.HandleFunc("POST /api/v1/deployments", s.handleCreateDeployment)
	mux.HandleFunc("GET /api/v1/deployments/{id}", s.handleGetDeployment)
	mux.HandleFunc("PUT /api/v1/deployments/{id}", s.handleUpdateDeployment)
	mux.HandleFunc("DELETE /api/v1/deployments/{id}", s.handleDeleteDeployment)
	mux.HandleFunc("POST /api/v1/deployments/{id}/scale", s.handleScaleDeployment)

	// Executions API
	mux.HandleFunc("GET /api/v1/executions", s.handleListExecutions)
	mux.HandleFunc("POST /api/v1/executions", s.handleCreateExecution)
	mux.HandleFunc("GET /api/v1/executions/{id}", s.handleGetExecution)
	mux.HandleFunc("GET /api/v1/executions/{id}/logs", s.handleExecutionLogs)

	// Secrets API
	mux.HandleFunc("GET /api/v1/secrets", s.handleListSecrets)
	mux.HandleFunc("POST /api/v1/secrets", s.handleCreateSecret)
	mux.HandleFunc("GET /api/v1/secrets/{name}", s.handleGetSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{name}", s.handleDeleteSecret)

	// Tokens API (RBAC)
	mux.HandleFunc("GET /api/v1/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/v1/tokens", s.handleCreateToken)
	mux.HandleFunc("GET /api/v1/tokens/{id}", s.handleGetToken)
	mux.HandleFunc("POST /api/v1/tokens/{id}/revoke", s.handleRevokeToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleDeleteToken)

	// Runtimes API
	mux.HandleFunc("GET /api/v1/runtimes", s.handleListRuntimes)
	mux.HandleFunc("POST /api/v1/runtimes", s.handleCreateRuntime)
	mux.HandleFunc("GET /api/v1/runtimes/{id}", s.handleGetRuntime)
	mux.HandleFunc("PUT /api/v1/runtimes/{id}", s.handleUpdateRuntime)
	mux.HandleFunc("DELETE /api/v1/runtimes/{id}", s.handleDeleteRuntime)

	// Cluster API (single-node mode stubs)
	mux.HandleFunc("GET /api/v1/cluster/status", s.handleClusterStatus)
	mux.HandleFunc("GET /api/v1/cluster/nodes", s.handleClusterNodes)
	mux.HandleFunc("POST /api/v1/cluster/join", s.handleClusterJoin)
	mux.HandleFunc("POST /api/v1/cluster/leave", s.handleClusterLeave)

	s.mux = mux
	s.server = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.middleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// Handler returns the HTTP handler for the server.
// This is useful for testing with httptest.Server.
func (s *Server) Handler() http.Handler {
	return s.middleware(s.mux)
}

// Start starts the API server.
func (s *Server) Start() error {
	// Load existing service deployments and start them
	if err := s.loadExistingDeployments(); err != nil {
		log.Warn("failed to load existing deployments: %v", err)
	}

	// Subscribe to supervisor state changes (event-driven, no polling)
	s.supervisor.OnStateChange(s.handleSupervisorStateChange)

	log.Start("API server on %s", s.addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Start("shutting down server")

	// Signal background goroutines to stop
	select {
	case <-s.stopCh:
		// Already closed
	default:
		close(s.stopCh)
	}

	// Drain and stop services (2s drain, 10s stop timeout)
	s.supervisor.DrainAndStop(2*time.Second, 10*time.Second)

	// Shutdown HTTP server
	log.Info("closing HTTP connections")
	if err := s.server.Shutdown(ctx); err != nil {
		log.Warn("HTTP shutdown error: %v", err)
		return err
	}

	log.Done("server shutdown complete")
	return nil
}

// maxRequestBodySize is the maximum allowed request body size (10MB).
const maxRequestBodySize = 10 * 1024 * 1024

// Rate limiting configuration
const (
	rateLimitRequestsPerSecond = 100 // Allow 100 requests per second per IP
	rateLimitBurst             = 200 // Allow bursts up to 200 requests
)

// getRateLimiter returns or creates a rate limiter for the given IP.
func (s *Server) getRateLimiter(ip string) *rate.Limiter {
	s.rateLimitersMu.RLock()
	limiter, exists := s.rateLimiters[ip]
	s.rateLimitersMu.RUnlock()

	if exists {
		return limiter
	}

	s.rateLimitersMu.Lock()
	defer s.rateLimitersMu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = s.rateLimiters[ip]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(rateLimitRequestsPerSecond, rateLimitBurst)
	s.rateLimiters[ip] = limiter
	return limiter
}

// Pagination defaults and limits.
const (
	defaultPageLimit = 100
	maxPageLimit     = 1000
)

// parsePagination extracts limit and offset from query parameters.
func parsePagination(r *http.Request) (limit, offset int) {
	limit = defaultPageLimit
	offset = 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
			if limit > maxPageLimit {
				limit = maxPageLimit
			}
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	return limit, offset
}

// getClientIP extracts the client IP from a request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain (format: "client, proxy1, proxy2")
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// middleware wraps handlers with logging, auth, and common headers.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Rate limiting
		clientIP := getClientIP(r)
		limiter := s.getRateLimiter(clientIP)
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Too Many Requests","message":"Rate limit exceeded. Try again later."}`))
			return
		}

		// Limit request body size to prevent OOM attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

		// Set common headers
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Prisn-Node", s.nodeID)

		// Wrap response writer to capture status
		rw := &responseWriter{ResponseWriter: w, status: 200}

		// Skip auth for public endpoints
		if isPublicEndpoint(r.URL.Path) {
			next.ServeHTTP(rw, r)
			s.logRequest(r, rw.status, start)
			return
		}

		// Authenticate using RBAC middleware
		token, err := s.authMW.Authenticate(r)
		if err != nil {
			rw.status = http.StatusInternalServerError
			w.WriteHeader(http.StatusInternalServerError)
			s.writeJSON(w, errorResponse{
				Error:   "Internal Server Error",
				Message: "Authentication error",
			})
			s.logRequest(r, rw.status, start)
			return
		}

		// Check if authentication is required
		authHeader := r.Header.Get("Authorization")
		if token == nil && authHeader != "" {
			// Token provided but invalid
			rw.status = http.StatusUnauthorized
			w.WriteHeader(http.StatusUnauthorized)
			s.writeJSON(w, errorResponse{
				Error:   "Unauthorized",
				Message: "Invalid API token. Use Authorization: Bearer <token> header.",
			})
			s.logRequest(r, rw.status, start)
			return
		}

		// If API key is configured, require authentication
		if s.apiKey != "" && token == nil {
			rw.status = http.StatusUnauthorized
			w.WriteHeader(http.StatusUnauthorized)
			s.writeJSON(w, errorResponse{
				Error:   "Unauthorized",
				Message: "API token required. Use Authorization: Bearer <token> header.",
			})
			s.logRequest(r, rw.status, start)
			return
		}

		// Store token in context if authenticated
		ctx := r.Context()
		if token != nil {
			ctx = context.WithValue(ctx, tokenContextKey, token)
		}

		next.ServeHTTP(rw, r.WithContext(ctx))
		s.logRequest(r, rw.status, start)
	})
}

// logRequest logs an HTTP request.
func (s *Server) logRequest(r *http.Request, status int, start time.Time) {
	// Skip noisy health checks in normal mode
	if r.URL.Path != "/health" || log.V() {
		log.LogHTTP(log.HTTPEvent{
			Method:   r.Method,
			Path:     r.URL.Path,
			Status:   status,
			Duration: time.Since(start),
		})
	}
}

// isPublicEndpoint returns true for endpoints that don't require authentication.
func isPublicEndpoint(path string) bool {
	return path == "/health" || path == "/metrics"
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Health check response
type healthResponse struct {
	Status    string    `json:"status"`
	NodeID    string    `json:"node_id"`
	Uptime    string    `json:"uptime"`
	StartedAt time.Time `json:"started_at"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:    "healthy",
		NodeID:    s.nodeID,
		Uptime:    time.Since(s.startedAt).Round(time.Second).String(),
		StartedAt: s.startedAt,
	}
	s.writeJSON(w, resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Get deployment breakdown by status
	deployments, err := s.store.ListDeployments("", "")
	if err != nil {
		log.Warn("metrics: failed to list deployments: %v", err)
		deployments = nil // Continue with empty data
	}
	statusCounts := make(map[string]int)
	totalReplicas := 0
	readyReplicas := 0
	totalRestarts := 0
	for _, d := range deployments {
		status := d.Status.Phase
		if status == "" {
			status = "unknown"
		}
		statusCounts[status]++
		totalReplicas += d.Replicas
		readyReplicas += d.Status.ReadyReplicas
		totalRestarts += d.Status.Restarts
	}

	// Get execution breakdown by status
	executions, err := s.store.ListExecutions("", "", "", 1000)
	if err != nil {
		log.Warn("metrics: failed to list executions: %v", err)
		executions = nil // Continue with empty data
	}
	execStatusCounts := make(map[string]int)
	for _, e := range executions {
		execStatusCounts[e.Status]++
	}

	// Prometheus text format
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Server info
	fmt.Fprintf(w, "# HELP prisn_info Server information\n")
	fmt.Fprintf(w, "# TYPE prisn_info gauge\n")
	fmt.Fprintf(w, "prisn_info{node_id=\"%s\"} 1\n", s.nodeID)
	fmt.Fprintf(w, "\n")

	// Uptime
	fmt.Fprintf(w, "# HELP prisn_uptime_seconds Server uptime in seconds\n")
	fmt.Fprintf(w, "# TYPE prisn_uptime_seconds counter\n")
	fmt.Fprintf(w, "prisn_uptime_seconds %d\n", int(time.Since(s.startedAt).Seconds()))
	fmt.Fprintf(w, "\n")

	// Deployments
	fmt.Fprintf(w, "# HELP prisn_deployments_total Total number of deployments\n")
	fmt.Fprintf(w, "# TYPE prisn_deployments_total gauge\n")
	fmt.Fprintf(w, "prisn_deployments_total %d\n", stats.Deployments)
	fmt.Fprintf(w, "\n")

	// Deployments by status
	fmt.Fprintf(w, "# HELP prisn_deployments_by_status Deployments by status\n")
	fmt.Fprintf(w, "# TYPE prisn_deployments_by_status gauge\n")
	for status, count := range statusCounts {
		fmt.Fprintf(w, "prisn_deployments_by_status{status=\"%s\"} %d\n", status, count)
	}
	fmt.Fprintf(w, "\n")

	// Replicas
	fmt.Fprintf(w, "# HELP prisn_replicas_total Total desired replicas across all deployments\n")
	fmt.Fprintf(w, "# TYPE prisn_replicas_total gauge\n")
	fmt.Fprintf(w, "prisn_replicas_total %d\n", totalReplicas)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP prisn_replicas_ready Total ready replicas across all deployments\n")
	fmt.Fprintf(w, "# TYPE prisn_replicas_ready gauge\n")
	fmt.Fprintf(w, "prisn_replicas_ready %d\n", readyReplicas)
	fmt.Fprintf(w, "\n")

	// Restarts
	fmt.Fprintf(w, "# HELP prisn_restarts_total Total restart count across all deployments\n")
	fmt.Fprintf(w, "# TYPE prisn_restarts_total counter\n")
	fmt.Fprintf(w, "prisn_restarts_total %d\n", totalRestarts)
	fmt.Fprintf(w, "\n")

	// Executions
	fmt.Fprintf(w, "# HELP prisn_executions_total Total number of executions\n")
	fmt.Fprintf(w, "# TYPE prisn_executions_total gauge\n")
	fmt.Fprintf(w, "prisn_executions_total %d\n", stats.Executions)
	fmt.Fprintf(w, "\n")

	// Executions by status
	fmt.Fprintf(w, "# HELP prisn_executions_by_status Executions by status\n")
	fmt.Fprintf(w, "# TYPE prisn_executions_by_status gauge\n")
	for status, count := range execStatusCounts {
		fmt.Fprintf(w, "prisn_executions_by_status{status=\"%s\"} %d\n", status, count)
	}
	fmt.Fprintf(w, "\n")

	// Secrets
	fmt.Fprintf(w, "# HELP prisn_secrets_total Total number of secrets\n")
	fmt.Fprintf(w, "# TYPE prisn_secrets_total gauge\n")
	fmt.Fprintf(w, "prisn_secrets_total %d\n", stats.Secrets)
	fmt.Fprintf(w, "\n")

	// Cron jobs
	fmt.Fprintf(w, "# HELP prisn_cronjobs_total Total number of cron jobs\n")
	fmt.Fprintf(w, "# TYPE prisn_cronjobs_total gauge\n")
	fmt.Fprintf(w, "prisn_cronjobs_total %d\n", stats.CronJobs)
}

// Deployments handlers

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	deployType := r.URL.Query().Get("type")
	limit, offset := parsePagination(r)

	deployments, err := s.store.ListDeployments(namespace, deployType)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply pagination
	total := len(deployments)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := deployments[offset:end]

	s.writeJSON(w, map[string]any{
		"items":  page,
		"count":  len(page),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// CreateDeploymentRequest is the request body for creating a deployment.
type CreateDeploymentRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Type      string            `json:"type"`
	Runtime   string            `json:"runtime"`
	Source    string            `json:"source"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Port      int               `json:"port,omitempty"`
	Replicas  int               `json:"replicas"`
	Schedule  string            `json:"schedule,omitempty"`
	Timeout   string            `json:"timeout,omitempty"`
	Resources struct {
		CPU       string `json:"cpu,omitempty"`
		Memory    string `json:"memory,omitempty"`
	} `json:"resources,omitempty"`
}

func (s *Server) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req CreateDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Validate required fields
	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if req.Source == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("source is required"))
		return
	}

	// Validate name format
	if err := validate.Name(req.Name); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	// Validate port if specified
	if req.Port != 0 {
		if err := validate.Port(req.Port); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	// Validate replicas
	if req.Replicas != 0 {
		if err := validate.Replicas(req.Replicas); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	// Validate schedule if specified
	if req.Schedule != "" {
		if err := validate.CronSchedule(req.Schedule); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	// Validate env vars if specified
	for key := range req.Env {
		if _, _, err := validate.EnvVar(key + "=" + req.Env[key]); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	// Set defaults
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Type == "" {
		if req.Schedule != "" {
			req.Type = "cronjob"
		} else if req.Port > 0 {
			req.Type = "service"
		} else {
			req.Type = "job"
		}
	}
	if req.Replicas == 0 {
		req.Replicas = 1
	}

	// Create deployment
	d := &store.Deployment{
		ID: store.NewDeploymentID(),
		DeploymentSpec: store.DeploymentSpec{
			Name:      req.Name,
			Namespace: req.Namespace,
			Type:      req.Type,
			Runtime:   req.Runtime,
			Source:    req.Source,
			Args:      req.Args,
			Env:       req.Env,
			Port:      req.Port,
			Replicas:  req.Replicas,
			Schedule:  req.Schedule,
		},
		Status: store.DeploymentStatus{
			Phase:           "Pending",
			DesiredReplicas: req.Replicas,
			LastUpdated:     time.Now(),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.store.CreateDeployment(d); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			s.writeError(w, http.StatusConflict, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Start service in supervisor if it's a service type
	if d.Type == "service" && d.Replicas > 0 {
		if err := s.startService(d); err != nil {
			log.Warn("failed to start service %s: %v", d.Name, err)
			d.Status.Phase = "Failed"
			d.Status.Message = err.Error()
			s.store.UpdateDeployment(d)
		}
	}

	// Register cron job with scheduler if it's a cronjob type
	if d.Type == "cronjob" && d.Schedule != "" && s.scheduler != nil {
		if err := s.scheduler.RegisterDeployment(d); err != nil {
			log.Warn("failed to register cronjob %s: %v", d.Name, err)
			d.Status.Phase = "Failed"
			d.Status.Message = err.Error()
			s.store.UpdateDeployment(d)
		} else {
			d.Status.Phase = "Scheduled"
			s.store.UpdateDeployment(d)
		}
	}

	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, d)
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("deployment ID required"))
		return
	}

	d, err := s.getDeploymentByIDOrName(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, d)
}

// getDeploymentByIDOrName tries to get deployment by ID first, then by name.
func (s *Server) getDeploymentByIDOrName(idOrName string) (*store.Deployment, error) {
	// Try by ID first
	d, err := s.store.GetDeployment(idOrName)
	if err == nil {
		return d, nil
	}

	// Try by name in default namespace
	d, err = s.store.GetDeploymentByName("default", idOrName)
	if err == nil {
		return d, nil
	}

	// Return a proper NotFoundError that errors.Is() can match
	return nil, &store.NotFoundError{Resource: "deployment", ID: idOrName}
}

func (s *Server) handleUpdateDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	d, err := s.getDeploymentByIDOrName(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	var req CreateDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Update fields if provided (with validation)
	if req.Replicas > 0 {
		if err := validate.Replicas(req.Replicas); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		d.Replicas = req.Replicas
		d.Status.DesiredReplicas = req.Replicas
	}
	if req.Source != "" {
		if err := validate.Path(req.Source); err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid source: %w", err))
			return
		}
		d.Source = req.Source
	}
	if req.Port != 0 {
		if err := validate.Port(req.Port); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		d.Port = req.Port
	}
	if req.Env != nil {
		d.Env = req.Env
	}

	if err := s.store.UpdateDeployment(d); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, d)
}

func (s *Server) handleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	idOrName := r.PathValue("id")

	// Get deployment first to check if it's a service
	d, err := s.getDeploymentByIDOrName(idOrName)
	if err == nil && d.Type == "service" {
		// Stop the service in supervisor
		s.stopService(d)
	}

	// Delete by actual ID
	id := idOrName
	if d != nil {
		id = d.ID
	}

	if err := s.store.DeleteDeployment(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ScaleRequest is the request body for scaling a deployment.
type ScaleRequest struct {
	Replicas int `json:"replicas"`
}

func (s *Server) handleScaleDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	d, err := s.getDeploymentByIDOrName(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	var req ScaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if err := validate.Replicas(req.Replicas); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	oldReplicas := d.Replicas
	d.Replicas = req.Replicas
	d.Status.DesiredReplicas = req.Replicas
	d.Status.LastUpdated = time.Now()

	if err := s.store.UpdateDeployment(d); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Handle service scaling
	if d.Type == "service" {
		if oldReplicas == 0 && req.Replicas > 0 {
			// Scale up from 0 - start service
			if err := s.startService(d); err != nil {
				log.Warn("failed to start service %s: %v", d.Name, err)
			}
		} else if oldReplicas > 0 && req.Replicas == 0 {
			// Scale down to 0 - stop service
			s.stopService(d)
			d.Status.Phase = "Stopped"
			d.Status.ReadyReplicas = 0
			s.store.UpdateDeployment(d)
		}
	}

	s.writeJSON(w, map[string]any{
		"deployment": d.Name,
		"replicas":   d.Replicas,
		"message":    fmt.Sprintf("Scaled to %d replicas", d.Replicas),
	})
}

// Executions handlers

func (s *Server) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	deploymentID := r.URL.Query().Get("deployment_id")
	status := r.URL.Query().Get("status")
	limit, offset := parsePagination(r)

	// Fetch more than limit to support pagination
	// Store limit is just for efficiency, we apply real pagination here
	executions, err := s.store.ListExecutions(namespace, deploymentID, status, offset+limit)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Apply pagination
	total := len(executions)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := executions[offset:end]

	s.writeJSON(w, map[string]any{
		"items":  page,
		"count":  len(page),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// CreateExecutionRequest is the request body for creating an execution.
type CreateExecutionRequest struct {
	DeploymentID string            `json:"deployment_id,omitempty"`
	Name         string            `json:"name,omitempty"`
	Namespace    string            `json:"namespace"`
	Source       string            `json:"source,omitempty"`
	Runtime      string            `json:"runtime,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Timeout      string            `json:"timeout,omitempty"`
	Async        bool              `json:"async"` // If true, return immediately
}

func (s *Server) handleCreateExecution(w http.ResponseWriter, r *http.Request) {
	var req CreateExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.Namespace == "" {
		req.Namespace = "default"
	}

	// If deployment_id provided, get the deployment config
	var d *store.Deployment
	if req.DeploymentID != "" {
		var err error
		// Try by ID first, then by name
		d, err = s.getDeploymentByIDOrName(req.DeploymentID)
		if err != nil {
			s.writeError(w, http.StatusNotFound, fmt.Errorf("deployment not found: %s", req.DeploymentID))
			return
		}
	} else if req.Source == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("either deployment_id or source is required"))
		return
	}

	// Create execution record
	exec := &store.Execution{
		ID:           store.NewExecutionID(),
		DeploymentID: req.DeploymentID,
		Namespace:    req.Namespace,
		Name:         req.Name,
		Status:       "Pending",
		StartedAt:    time.Now(),
		NodeID:       s.nodeID,
	}
	if d != nil {
		exec.Name = d.Name
	}

	if err := s.store.CreateExecution(exec); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// For async, return immediately
	if req.Async {
		w.WriteHeader(http.StatusAccepted)
		s.writeJSON(w, exec)

		// Run in background with panic recovery and max timeout
		go func() {
			// Detached jobs still get a timeout to prevent runaway executions
			// Default 1 hour max for detached jobs
			maxTimeout := 1 * time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), maxTimeout)
			defer cancel()

			defer func() {
				if r := recover(); r != nil {
					log.Fail("%s panicked: %v", exec.Name, r)
					exec.Status = "Failed"
					exec.Error = fmt.Sprintf("panic: %v", r)
					exec.CompletedAt = time.Now()
					exec.Duration = exec.CompletedAt.Sub(exec.StartedAt)
					s.store.UpdateExecution(exec)
				}
			}()
			s.runExecution(ctx, exec, d, req)
		}()
		return
	}

	// Synchronous execution - use request context so client disconnect stops job
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	s.runExecution(ctx, exec, d, req)

	// Reload execution to get final status
	exec, _ = s.store.GetExecution(exec.ID)
	s.writeJSON(w, exec)
}

func (s *Server) runExecution(ctx context.Context, exec *store.Execution, d *store.Deployment, req CreateExecutionRequest) {
	exec.Status = "Running"
	s.store.UpdateExecution(exec)

	// Build run config
	source := req.Source
	args := req.Args
	env := req.Env
	if d != nil {
		source = d.Source
		args = d.Args
		if env == nil {
			env = d.Env
		}
	}

	cfg := runner.RunConfig{
		Script: source,
		Args:   args,
		Env:    env,
	}

	// Parse timeout if provided
	if req.Timeout != "" {
		dur, err := time.ParseDuration(req.Timeout)
		if err != nil {
			exec.Status = "Failed"
			exec.Error = fmt.Sprintf("invalid timeout %q: %v", req.Timeout, err)
			exec.CompletedAt = time.Now()
			exec.Duration = exec.CompletedAt.Sub(exec.StartedAt)
			s.store.UpdateExecution(exec)
			return
		}
		cfg.Timeout = dur
	}

	result, err := s.runner.Run(ctx, cfg)

	exec.CompletedAt = time.Now()
	exec.Duration = exec.CompletedAt.Sub(exec.StartedAt)

	if err != nil {
		exec.Status = "Failed"
		exec.Error = sanitizeOutput(err.Error())
	} else {
		exec.ExitCode = result.ExitCode
		exec.Stdout = sanitizeOutput(result.Stdout)
		exec.Stderr = sanitizeOutput(result.Stderr)
		if result.ExitCode == 0 {
			exec.Status = "Completed"
		} else {
			exec.Status = "Failed"
		}
	}

	s.store.UpdateExecution(exec)
}

func (s *Server) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	exec, err := s.store.GetExecution(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, exec)
}

func (s *Server) handleExecutionLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	exec, err := s.store.GetExecution(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, map[string]any{
		"stdout": exec.Stdout,
		"stderr": exec.Stderr,
	})
}

// Secrets handlers

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	limit, offset := parsePagination(r)

	// ListSecrets returns SecretMeta (with Keys, not values)
	secrets, err := s.store.ListSecrets(namespace)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to list secrets: %w", err))
		return
	}

	// Apply pagination
	total := len(secrets)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := secrets[offset:end]

	s.writeJSON(w, map[string]any{
		"items":  page,
		"count":  len(page),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// CreateSecretRequest is the request body for creating a secret.
type CreateSecretRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Data      map[string]string `json:"data"`
}

func (s *Server) handleCreateSecret(w http.ResponseWriter, r *http.Request) {
	var req CreateSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if err := validate.Name(req.Name); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid secret name: %w", err))
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if err := validate.Namespace(req.Namespace); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid namespace: %w", err))
		return
	}

	sec := &store.Secret{
		ID:        store.NewSecretID(),
		Name:      req.Name,
		Namespace: req.Namespace,
		Data:      req.Data,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.store.CreateSecret(sec); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			s.writeError(w, http.StatusConflict, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, map[string]any{
		"id":        sec.ID,
		"name":      sec.Name,
		"namespace": sec.Namespace,
		"keys":      len(sec.Data),
	})
}

func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	sec, err := s.store.GetSecret(namespace, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, sec)
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	if err := s.store.DeleteSecret(namespace, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Error response helper
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (s *Server) writeError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	s.writeJSON(w, errorResponse{
		Error:   http.StatusText(status),
		Message: err.Error(),
	})
}

// writeJSON safely encodes JSON, logging errors instead of ignoring them.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Fail("json encode: %v", err)
	}
}

// =============================================================================
// Cluster API (single-node mode)
// =============================================================================

// handleClusterStatus returns cluster status for single-node mode.
func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	deployments, err := s.store.ListDeployments("", "")
	deploymentCount := 0
	if err != nil {
		log.Warn("cluster status: failed to list deployments: %v", err)
	} else {
		deploymentCount = len(deployments)
	}

	s.writeJSON(w, map[string]any{
		"mode":        "single-node",
		"node_id":     s.nodeID,
		"state":       "leader",
		"leader":      s.nodeID,
		"term":        1,
		"nodes":       1,
		"deployments": deploymentCount,
		"uptime":      time.Since(s.startedAt).String(),
	})
}

// handleClusterNodes returns node list for single-node mode.
func (s *Server) handleClusterNodes(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"items": []map[string]any{
			{
				"id":        s.nodeID,
				"address":   s.addr,
				"state":     "leader",
				"joined_at": s.startedAt,
				"last_seen": time.Now(),
			},
		},
		"count": 1,
	})
}

// handleClusterJoin returns an error - joining is not supported in single-node mode.
func (s *Server) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, http.StatusNotImplemented, fmt.Errorf("clustering not enabled - server running in single-node mode"))
}

// handleClusterLeave returns an error - leaving is not supported in single-node mode.
func (s *Server) handleClusterLeave(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, http.StatusNotImplemented, fmt.Errorf("clustering not enabled - server running in single-node mode"))
}

// loadExistingDeployments loads and starts existing service deployments.
func (s *Server) loadExistingDeployments() error {
	deployments, err := s.store.ListDeployments("", "service")
	if err != nil {
		return err
	}

	for _, d := range deployments {
		if d.Replicas > 0 {
			if err := s.startService(d); err != nil {
				log.Warn("failed to start %s: %v", d.Name, err)
			}
		}
	}

	return nil
}

// startService adds a deployment to the supervisor and starts it.
func (s *Server) startService(d *store.Deployment) error {
	// Determine command and args
	runtime := d.Runtime
	if runtime == "" {
		runtime = detectRuntimeFromSource(d.Source)
	}

	cmd, args := buildCommand(runtime, d.Source, d.Args)

	// Build environment
	env := []string{
		fmt.Sprintf("PORT=%d", d.Port),
		fmt.Sprintf("PRISN_DEPLOYMENT=%s", d.Name),
		fmt.Sprintf("PRISN_NAMESPACE=%s", d.Namespace),
	}
	for k, v := range d.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	svc := &supervisor.Service{
		ID:            d.ID,
		Name:          d.Name,
		Command:       cmd,
		Args:          args,
		Dir:           filepath.Dir(d.Source),
		Env:           env,
		Port:          d.Port,
		RestartPolicy: supervisor.RestartAlways,
		RestartDelay:  time.Second,
	}

	s.supervisor.Add(svc)
	if err := s.supervisor.Start(d.ID); err != nil {
		return err
	}

	log.OK("started service %s (port %d)", d.Name, d.Port)
	return nil
}

// stopService stops and removes a deployment from the supervisor.
func (s *Server) stopService(d *store.Deployment) error {
	if err := s.supervisor.Stop(d.ID); err != nil {
		return err
	}
	log.Done("stopped service %s", d.Name)
	return nil
}

// handleSupervisorStateChange is called when a supervisor service state changes.
// This replaces polling with event-driven state sync.
func (s *Server) handleSupervisorStateChange(event supervisor.StateChangeEvent) {
	d, err := s.store.GetDeployment(event.ServiceID)
	if err != nil {
		return // Deployment not found, ignore
	}

	// Map supervisor state to deployment phase
	var phase string
	var ready int
	switch event.NewState {
	case supervisor.StateRunning:
		phase = "Running"
		ready = 1
	case supervisor.StateStarting:
		phase = "Starting"
		ready = 0
	case supervisor.StateFailed:
		phase = "Failed"
		ready = 0
	case supervisor.StateBackoff:
		phase = "CrashLoopBackOff"
		ready = 0
	case supervisor.StateStopped:
		phase = "Stopped"
		ready = 0
	default:
		phase = "Pending"
		ready = 0
	}

	// Only update if changed
	if d.Status.Phase != phase || d.Status.ReadyReplicas != ready || d.Status.Restarts != event.Restarts {
		d.Status.Phase = phase
		d.Status.ReadyReplicas = ready
		d.Status.Restarts = event.Restarts
		d.Status.Message = ""
		if event.NewState == supervisor.StateFailed && event.ExitCode != 0 {
			d.Status.Message = fmt.Sprintf("exited with code %d", event.ExitCode)
		}
		d.Status.LastUpdated = event.Timestamp
		s.store.UpdateDeployment(d)
	}
}

// detectRuntimeFromSource infers runtime from source file extension.
func detectRuntimeFromSource(source string) string {
	ext := filepath.Ext(source)
	switch ext {
	case ".py":
		return "python3"
	case ".js":
		return "node"
	case ".sh":
		return "bash"
	default:
		return "python3"
	}
}

// buildCommand builds the command and args for a runtime.
func buildCommand(runtime, source string, extraArgs []string) (string, []string) {
	var cmd string
	var args []string

	switch runtime {
	case "python3", "python":
		cmd = "python3"
		args = append([]string{source}, extraArgs...)
	case "node", "nodejs":
		cmd = "node"
		args = append([]string{source}, extraArgs...)
	case "bash", "sh":
		cmd = "bash"
		args = append([]string{source}, extraArgs...)
	default:
		// Assume runtime is the command itself
		cmd = runtime
		args = append([]string{source}, extraArgs...)
	}

	return cmd, args
}
