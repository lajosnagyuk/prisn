//go:build !windows

package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerNew(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if r.VenvManager == nil {
		t.Error("VenvManager should be initialized")
	}
	if r.DepDetector == nil {
		t.Error("DepDetector should be initialized")
	}
	if r.WorkDir == "" {
		t.Error("WorkDir should be set")
	}
	if r.DefaultTimeout != 1*time.Hour {
		t.Errorf("expected 1 hour default timeout, got %v", r.DefaultTimeout)
	}
}

func TestRunnerSimpleScript(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Create a simple script
	dir := t.TempDir()
	script := filepath.Join(dir, "test.py")
	if err := os.WriteFile(script, []byte("print('hello from test')"), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout bytes.Buffer
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "hello from test") {
		t.Errorf("expected output to contain 'hello from test', got %q", stdout.String())
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestRunnerWithArgs(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "args.py")
	code := `import sys
for arg in sys.argv[1:]:
    print(f"arg: {arg}")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout bytes.Buffer
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Args:    []string{"one", "two", "three"},
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	output := stdout.String()
	if !strings.Contains(output, "arg: one") || !strings.Contains(output, "arg: two") || !strings.Contains(output, "arg: three") {
		t.Errorf("expected all args in output, got %q", output)
	}
}

func TestRunnerWithEnv(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "env.py")
	code := `import os
print(f"MY_VAR={os.environ.get('MY_VAR', 'not set')}")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout bytes.Buffer
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Env:     map[string]string{"MY_VAR": "test_value"},
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "MY_VAR=test_value") {
		t.Errorf("expected env var in output, got %q", stdout.String())
	}
}

func TestRunnerTimeout(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "sleep.py")
	code := `import time
time.sleep(60)
print("done")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Timeout: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !result.Killed {
		t.Error("expected script to be killed")
	}
	if result.KillReason != "timeout" {
		t.Errorf("expected kill reason 'timeout', got %q", result.KillReason)
	}
	if result.ExitCode != -1 {
		t.Errorf("expected exit code -1 for killed process, got %d", result.ExitCode)
	}
}

func TestRunnerNonZeroExit(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "fail.py")
	code := `import sys
sys.exit(42)
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
	if result.Killed {
		t.Error("script should not be marked as killed for normal exit")
	}
}

func TestRunnerStdin(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "stdin.py")
	code := `import sys
for line in sys.stdin:
    print(f"received: {line.strip()}")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout bytes.Buffer
	stdin := strings.NewReader("line1\nline2\n")
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Stdin:   stdin,
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	output := stdout.String()
	if !strings.Contains(output, "received: line1") || !strings.Contains(output, "received: line2") {
		t.Errorf("expected stdin lines in output, got %q", output)
	}
}

func TestRunnerStderr(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "stderr.py")
	code := `import sys
print("stdout", file=sys.stdout)
print("stderr", file=sys.stderr)
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "stdout") {
		t.Errorf("expected 'stdout' in stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr") {
		t.Errorf("expected 'stderr' in stderr, got %q", stderr.String())
	}
}

func TestQuickRun(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "quick.py")
	code := `import sys
print(f"arg: {sys.argv[1]}")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	result, err := r.QuickRun(ctx, script, "test_arg")
	if err != nil {
		t.Fatalf("QuickRun failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestRunInline(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()
	result, err := r.RunInline(ctx, "print('inline code')", "python3")
	if err != nil {
		t.Fatalf("RunInline failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestRunInlineUnsupportedRuntime(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx := context.Background()
	_, err = r.RunInline(ctx, "code", "unsupported")
	if err == nil {
		t.Error("expected error for unsupported runtime")
	}
	if !strings.Contains(err.Error(), "unknown runtime") && !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("expected unknown/unsupported runtime error, got %v", err)
	}
}

func TestRunnerWithDependencies(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dir := t.TempDir()

	// Create requirements.txt
	reqFile := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(reqFile, []byte("requests\n"), 0644); err != nil {
		t.Fatalf("failed to write requirements.txt: %v", err)
	}

	// Create script that imports requests
	script := filepath.Join(dir, "with_deps.py")
	code := `import requests
print(f"requests version: {requests.__version__}")
`
	if err := os.WriteFile(script, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	ctx := context.Background()
	var stdout bytes.Buffer
	result, err := r.Run(ctx, RunConfig{
		Script:  script,
		Stdout:  &stdout,
		Stderr:  os.Stderr,
		Timeout: 2 * time.Minute, // Allow time for pip install
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "requests version:") {
		t.Errorf("expected requests version in output, got %q", stdout.String())
	}
}

func TestRunConfig(t *testing.T) {
	cfg := RunConfig{
		Script:  "/path/to/script.py",
		Args:    []string{"arg1"},
		Env:     map[string]string{"KEY": "value"},
		Timeout: 5 * time.Second,
	}

	if cfg.Script != "/path/to/script.py" {
		t.Error("Script not set correctly")
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "arg1" {
		t.Error("Args not set correctly")
	}
	if cfg.Env["KEY"] != "value" {
		t.Error("Env not set correctly")
	}
	if cfg.Timeout != 5*time.Second {
		t.Error("Timeout not set correctly")
	}
}

func TestRunResult(t *testing.T) {
	result := RunResult{
		ExitCode:         0,
		Stdout:           "output",
		Stderr:           "error",
		Duration:         1 * time.Second,
		StartTime:        time.Now().Add(-1 * time.Second),
		EndTime:          time.Now(),
		Killed:           false,
		EnvCached:        true,
		EnvSetupDuration: 100 * time.Millisecond,
	}

	if result.ExitCode != 0 {
		t.Error("ExitCode not set correctly")
	}
	if result.Stdout != "output" {
		t.Error("Stdout not set correctly")
	}
	if result.EnvCached != true {
		t.Error("EnvCached not set correctly")
	}
}

func TestResourceConfig(t *testing.T) {
	res := ResourceConfig{
		MaxMemoryMB:   512,
		MaxCPUPercent: 50,
		MaxProcesses:  10,
	}

	if res.MaxMemoryMB != 512 {
		t.Error("MaxMemoryMB not set correctly")
	}
	if res.MaxCPUPercent != 50 {
		t.Error("MaxCPUPercent not set correctly")
	}
	if res.MaxProcesses != 10 {
		t.Error("MaxProcesses not set correctly")
	}
}

func TestValidateScriptPath(t *testing.T) {
	// Create a temp file for valid tests
	dir := t.TempDir()
	validScript := filepath.Join(dir, "valid.py")
	os.WriteFile(validScript, []byte("print('ok')"), 0644)

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{"valid script", validScript, false, ""},
		{"empty path", "", true, "script path is empty"},
		{"non-existent", "/nonexistent/script.py", true, "script not found"},
		{"directory", dir, true, "is a directory"},
		{"path traversal", filepath.Join(dir, "..", "..", "etc", "passwd"), true, ""},
		{"etc/passwd", "/etc/passwd", true, "access denied"},
		{"proc self", "/proc/self/environ", true, ""}, // access denied on Linux, not found on macOS
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScriptPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
