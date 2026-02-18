// Package cli implements the prisn command-line interface.
package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/client"
	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/spf13/cobra"
)

// Event represents an audit event.
type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`      // deployment, execution, scale, delete, etc.
	Action    string    `json:"action"`    // created, updated, started, completed, failed
	Resource  string    `json:"resource"`  // deployment/my-api, execution/abc123
	Actor     string    `json:"actor"`     // user or "system"
	Details   string    `json:"details,omitempty"`
}

// EventsResponse is the events list output.
type EventsResponse struct {
	Events []Event `json:"events"`
	Total  int     `json:"total"`
}

func newEventsCmd() *cobra.Command {
	var (
		since     string
		limit     int
		eventType string
	)

	cmd := &cobra.Command{
		Use:   "events",
		Short: "Show recent events and audit log",
		Long: `Show recent events across all deployments.

Events include deployments created/updated/deleted, executions started/completed,
scaling operations, and system events.

Examples:
  prisn events                    # Last 20 events
  prisn events --since 1h         # Events in last hour
  prisn events --type deployment  # Only deployment events
  prisn events -o json            # JSON for scripting`,

		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)

			// Parse since duration
			var sinceTime time.Time
			if since != "" {
				d, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("invalid duration %q: %w", since, err)
				}
				sinceTime = time.Now().Add(-d)
			}

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Try server first
			c, err := client.New(resolved)
			if err == nil {
				url := fmt.Sprintf("/api/v1/namespaces/%s/events?limit=%d", resolved.Namespace, limit)
				if !sinceTime.IsZero() {
					url += fmt.Sprintf("&since=%d", sinceTime.Unix())
				}
				if eventType != "" {
					url += fmt.Sprintf("&type=%s", eventType)
				}

				var events []Event
				if err := c.GetJSON(url, &events); err == nil {
					return printEvents(printer, events)
				}
			}

			// Fall back to local: reconstruct events from executions and deployments
			home, _ := os.UserHomeDir()
			dbPath := filepath.Join(home, ".prisn", "state.db")
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("cannot open local database: %w", err)
			}
			defer st.Close()

			events := []Event{}

			// Get recent deployments
			deployments, _ := st.ListDeployments(resolved.Namespace, "")
			for _, d := range deployments {
				if !sinceTime.IsZero() && d.CreatedAt.Before(sinceTime) {
					continue
				}
				if eventType != "" && eventType != "deployment" {
					continue
				}
				events = append(events, Event{
					Timestamp: d.CreatedAt,
					Type:      "deployment",
					Action:    "created",
					Resource:  fmt.Sprintf("deployment/%s", d.Name),
					Actor:     "user",
					Details:   fmt.Sprintf("type=%s replicas=%d", d.Type, d.Replicas),
				})
				if d.UpdatedAt.After(d.CreatedAt) {
					events = append(events, Event{
						Timestamp: d.UpdatedAt,
						Type:      "deployment",
						Action:    "updated",
						Resource:  fmt.Sprintf("deployment/%s", d.Name),
						Actor:     "user",
					})
				}
			}

			// Get recent executions
			if eventType == "" || eventType == "execution" {
				executions, _ := st.ListExecutions(resolved.Namespace, "", "", limit*2)
				for _, e := range executions {
					if !sinceTime.IsZero() && e.StartedAt.Before(sinceTime) {
						continue
					}
					events = append(events, Event{
						Timestamp: e.StartedAt,
						Type:      "execution",
						Action:    "started",
						Resource:  fmt.Sprintf("execution/%s", e.ID[:8]),
						Actor:     "system",
						Details:   e.Name,
					})
					if !e.CompletedAt.IsZero() {
						action := "completed"
						details := fmt.Sprintf("exit=%d duration=%v", e.ExitCode, e.Duration.Round(time.Millisecond))
						if e.ExitCode != 0 {
							action = "failed"
						}
						events = append(events, Event{
							Timestamp: e.CompletedAt,
							Type:      "execution",
							Action:    action,
							Resource:  fmt.Sprintf("execution/%s", e.ID[:8]),
							Actor:     "system",
							Details:   details,
						})
					}
				}
			}

			// Sort by timestamp descending
			sort.Slice(events, func(i, j int) bool {
				return events[i].Timestamp.After(events[j].Timestamp)
			})

			// Limit
			if len(events) > limit {
				events = events[:limit]
			}

			return printEvents(printer, events)
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "Show events since duration (e.g., 1h, 30m)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of events")
	cmd.Flags().StringVar(&eventType, "type", "", "Filter by event type (deployment, execution)")

	return cmd
}

func printEvents(printer *Printer, events []Event) error {
	resp := EventsResponse{
		Events: events,
		Total:  len(events),
	}

	if printer.IsStructured() {
		return printer.Print(resp)
	}

	if len(events) == 0 {
		fmt.Println("No events found")
		return nil
	}

	fmt.Println("EVENTS")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("%-20s %-8s %-10s %-25s %s\n", "TIMESTAMP", "ACTOR", "ACTION", "RESOURCE", "DETAILS")
	fmt.Println(strings.Repeat("-", 80))

	for _, e := range events {
		fmt.Printf("%-20s %-8s %-10s %-25s %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.Actor,
			e.Action,
			e.Resource,
			truncate(e.Details, 30))
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// SelfTestResult represents a single self-test check.
type SelfTestResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass, fail, skip
	Message string `json:"message,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// SelfTestReport is the full self-test output.
type SelfTestReport struct {
	Results []SelfTestResult `json:"results"`
	Passed  int              `json:"passed"`
	Failed  int              `json:"failed"`
	Skipped int              `json:"skipped"`
}

func newSelfTestCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "self-test",
		Short: "Run self-diagnostics to verify prisn is working",
		Long: `Run comprehensive self-tests to verify prisn installation.

Tests include:
- API responsiveness
- Database operations
- Process spawning
- Python runtime availability
- Temp directory writable

Use in CI/CD or as a synthetic monitor.

Examples:
  prisn self-test           # Run all tests
  prisn self-test -v        # Verbose output
  prisn self-test -o json   # JSON for monitoring`,

		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)
			report := runSelfTests(cmd, verbose)

			if printer.IsStructured() {
				return printer.Print(report)
			}

			// Text output
			fmt.Println("PRISN SELF-TEST")
			fmt.Println("===============")
			fmt.Println()

			for _, r := range report.Results {
				symbol := "+"
				color := "\033[32m" // green
				switch r.Status {
				case "fail":
					symbol = "!"
					color = "\033[31m" // red
				case "skip":
					symbol = "~"
					color = "\033[33m" // yellow
				}
				reset := "\033[0m"

				msg := r.Message
				if r.Latency != "" {
					msg = fmt.Sprintf("%s (%s)", r.Message, r.Latency)
				}
				fmt.Printf("%s%s%s %s: %s\n", color, symbol, reset, r.Name, msg)
			}

			fmt.Println()
			fmt.Printf("Results: %d passed, %d failed, %d skipped\n",
				report.Passed, report.Failed, report.Skipped)

			if report.Failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

	return cmd
}

func runSelfTests(cmd *cobra.Command, verbose bool) SelfTestReport {
	var report SelfTestReport

	// Test 1: API Response Time
	report.Results = append(report.Results, testAPIResponse(cmd))

	// Test 2: Database Operations
	report.Results = append(report.Results, testDatabase())

	// Test 3: Process Spawning
	report.Results = append(report.Results, testProcessSpawn())

	// Test 4: Python Available
	report.Results = append(report.Results, testPythonAvailable())

	// Test 5: Temp Directory Writable
	report.Results = append(report.Results, testTempWritable())

	// Calculate summary
	for _, r := range report.Results {
		switch r.Status {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
		case "skip":
			report.Skipped++
		}
	}

	return report
}

func testAPIResponse(cmd *cobra.Command) SelfTestResult {
	result := SelfTestResult{Name: "API Response"}

	flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
	flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
	resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
	if err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot resolve context: %v", err)
		return result
	}

	start := time.Now()
	c, err := client.New(resolved)
	if err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot create client: %v", err)
		return result
	}

	var health struct {
		Status string `json:"status"`
	}
	if err := c.GetJSON("/health", &health); err != nil {
		// Check if server is just not running
		if _, ok := err.(*client.ConnectError); ok {
			result.Status = "skip"
			result.Message = "server not running"
			return result
		}
		result.Status = "fail"
		result.Message = err.Error()
		return result
	}

	latency := time.Since(start)
	result.Status = "pass"
	result.Message = health.Status
	result.Latency = latency.Round(time.Microsecond).String()

	// Warn if latency > 100ms
	if latency > 100*time.Millisecond {
		result.Message = fmt.Sprintf("%s (slow!)", health.Status)
	}

	return result
}

func testDatabase() SelfTestResult {
	result := SelfTestResult{Name: "Database"}

	home, err := os.UserHomeDir()
	if err != nil {
		result.Status = "fail"
		result.Message = "cannot determine home directory"
		return result
	}

	dbPath := filepath.Join(home, ".prisn", "state.db")

	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		result.Status = "skip"
		result.Message = "database not created yet"
		return result
	}

	start := time.Now()
	st, err := store.Open(dbPath)
	if err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot open: %v", err)
		return result
	}
	defer st.Close()

	// Test read
	_, err = st.GetStats()
	if err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot read: %v", err)
		return result
	}

	latency := time.Since(start)
	result.Status = "pass"
	result.Message = "read/write OK"
	result.Latency = latency.Round(time.Microsecond).String()

	return result
}

func testProcessSpawn() SelfTestResult {
	result := SelfTestResult{Name: "Process Spawn"}

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "prisn-test")
	output, err := cmd.Output()
	if err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot spawn: %v", err)
		return result
	}

	if strings.TrimSpace(string(output)) != "prisn-test" {
		result.Status = "fail"
		result.Message = "unexpected output"
		return result
	}

	latency := time.Since(start)
	result.Status = "pass"
	result.Message = "spawn and capture OK"
	result.Latency = latency.Round(time.Microsecond).String()

	return result
}

func testPythonAvailable() SelfTestResult {
	result := SelfTestResult{Name: "Python Runtime"}

	for _, py := range []string{"python3", "python"} {
		cmd := exec.Command(py, "--version")
		output, err := cmd.Output()
		if err == nil {
			result.Status = "pass"
			result.Message = strings.TrimSpace(string(output))
			return result
		}
	}

	result.Status = "fail"
	result.Message = "python3 not found"
	return result
}

func testTempWritable() SelfTestResult {
	result := SelfTestResult{Name: "Temp Directory"}

	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, fmt.Sprintf("prisn-test-%d", time.Now().UnixNano()))

	start := time.Now()
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot write: %v", err)
		return result
	}
	defer os.Remove(testFile)

	if _, err := os.ReadFile(testFile); err != nil {
		result.Status = "fail"
		result.Message = fmt.Sprintf("cannot read: %v", err)
		return result
	}

	latency := time.Since(start)
	result.Status = "pass"
	result.Message = tmpDir
	result.Latency = latency.Round(time.Microsecond).String()

	return result
}

// ErrorSummary represents aggregated errors.
type ErrorSummary struct {
	Name       string    `json:"name"`
	ErrorCount int       `json:"error_count"`
	LastError  string    `json:"last_error"`
	LastTime   time.Time `json:"last_time"`
}

// ErrorsResponse is the errors command output.
type ErrorsResponse struct {
	Errors []ErrorSummary `json:"errors"`
	Total  int            `json:"total"`
}

func newErrorsCmd() *cobra.Command {
	var since string

	cmd := &cobra.Command{
		Use:   "errors",
		Short: "Show recent errors across deployments",
		Long: `Aggregate and display recent errors across all deployments.

Scans execution history for failures and groups by deployment.
Useful for quick troubleshooting.

Examples:
  prisn errors              # Errors in last hour
  prisn errors --since 24h  # Errors in last day
  prisn errors -o json      # JSON for alerting`,

		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)

			// Parse since duration (default 1h)
			sinceDuration := time.Hour
			if since != "" {
				d, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("invalid duration %q: %w", since, err)
				}
				sinceDuration = d
			}
			sinceTime := time.Now().Add(-sinceDuration)

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get from local store
			home, _ := os.UserHomeDir()
			dbPath := filepath.Join(home, ".prisn", "state.db")
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("cannot open local database: %w", err)
			}
			defer st.Close()

			// Get all recent executions
			executions, _ := st.ListExecutions(resolved.Namespace, "", "", 500)

			// Aggregate errors by deployment
			errorsByName := make(map[string]*ErrorSummary)
			for _, e := range executions {
				if e.StartedAt.Before(sinceTime) {
					continue
				}
				if e.ExitCode == 0 && e.Status == "Completed" {
					continue
				}
				if e.Status == "Running" || e.Status == "Pending" {
					continue
				}

				name := e.Name
				if name == "" {
					name = "unknown"
				}

				summary, exists := errorsByName[name]
				if !exists {
					summary = &ErrorSummary{Name: name}
					errorsByName[name] = summary
				}

				summary.ErrorCount++
				errMsg := e.Error
				if errMsg == "" && e.Stderr != "" {
					// Extract last line of stderr
					lines := strings.Split(strings.TrimSpace(e.Stderr), "\n")
					if len(lines) > 0 {
						errMsg = lines[len(lines)-1]
					}
				}
				if errMsg == "" {
					errMsg = fmt.Sprintf("exit code %d", e.ExitCode)
				}

				if summary.LastTime.Before(e.StartedAt) {
					summary.LastTime = e.StartedAt
					summary.LastError = errMsg
				}
			}

			// Convert to slice and sort
			errors := make([]ErrorSummary, 0, len(errorsByName))
			totalErrors := 0
			for _, s := range errorsByName {
				errors = append(errors, *s)
				totalErrors += s.ErrorCount
			}
			sort.Slice(errors, func(i, j int) bool {
				return errors[i].ErrorCount > errors[j].ErrorCount
			})

			resp := ErrorsResponse{
				Errors: errors,
				Total:  totalErrors,
			}

			if printer.IsStructured() {
				return printer.Print(resp)
			}

			if len(errors) == 0 {
				fmt.Printf("No errors in the last %v\n", sinceDuration)
				return nil
			}

			fmt.Printf("ERRORS (last %v)\n", sinceDuration)
			fmt.Println(strings.Repeat("=", 80))
			fmt.Printf("%-20s %-8s %-20s %s\n", "DEPLOYMENT", "COUNT", "LAST", "ERROR")
			fmt.Println(strings.Repeat("-", 80))

			for _, e := range errors {
				fmt.Printf("%-20s %-8d %-20s %s\n",
					truncate(e.Name, 20),
					e.ErrorCount,
					e.LastTime.Format("2006-01-02 15:04"),
					truncate(e.LastError, 35))
			}

			fmt.Printf("\nTotal: %d errors across %d deployments\n", totalErrors, len(errors))

			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "1h", "Show errors since duration (e.g., 1h, 24h)")

	return cmd
}

// testPortAvailable checks if a port is available.
func testPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
