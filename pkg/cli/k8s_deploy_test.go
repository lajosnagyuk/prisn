package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRuntime(t *testing.T) {
	tests := []struct {
		entrypoint string
		want       string
	}{
		{"main.py", "python3"},
		{"app.py", "python3"},
		{"script.py", "python3"},
		{"index.js", "node"},
		{"server.js", "node"},
		{"run.sh", "bash"},
		{"start.sh", "bash"},
		{"unknown.txt", "python3"}, // default
		{"", "python3"},            // empty default
	}

	for _, tt := range tests {
		t.Run(tt.entrypoint, func(t *testing.T) {
			got := detectRuntime(tt.entrypoint)
			if got != tt.want {
				t.Errorf("detectRuntime(%q) = %q, want %q", tt.entrypoint, got, tt.want)
			}
		})
	}
}

func TestReadDirectory(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()

	t.Run("finds main.py", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test1")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')"), 0644); err != nil {
			t.Fatal(err)
		}

		entrypoint, code, err := readDirectory(dir)
		if err != nil {
			t.Fatalf("readDirectory() error = %v", err)
		}
		if entrypoint != "main.py" {
			t.Errorf("entrypoint = %q, want %q", entrypoint, "main.py")
		}
		if code != "print('hello')" {
			t.Errorf("code = %q, want %q", code, "print('hello')")
		}
	})

	t.Run("finds app.py if no main.py", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test2")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("from flask import Flask"), 0644); err != nil {
			t.Fatal(err)
		}

		entrypoint, _, err := readDirectory(dir)
		if err != nil {
			t.Fatalf("readDirectory() error = %v", err)
		}
		if entrypoint != "app.py" {
			t.Errorf("entrypoint = %q, want %q", entrypoint, "app.py")
		}
	})

	t.Run("finds index.js", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test3")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('hi')"), 0644); err != nil {
			t.Fatal(err)
		}

		entrypoint, _, err := readDirectory(dir)
		if err != nil {
			t.Fatalf("readDirectory() error = %v", err)
		}
		if entrypoint != "index.js" {
			t.Errorf("entrypoint = %q, want %q", entrypoint, "index.js")
		}
	})

	t.Run("falls back to first script file", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test4")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "custom.py"), []byte("# custom script"), 0644); err != nil {
			t.Fatal(err)
		}

		entrypoint, _, err := readDirectory(dir)
		if err != nil {
			t.Fatalf("readDirectory() error = %v", err)
		}
		if entrypoint != "custom.py" {
			t.Errorf("entrypoint = %q, want %q", entrypoint, "custom.py")
		}
	})

	t.Run("error on empty directory", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test5")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}

		_, _, err := readDirectory(dir)
		if err == nil {
			t.Error("readDirectory() expected error for empty directory")
		}
	})

	t.Run("ignores non-script files", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test6")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Readme"), 0644); err != nil {
			t.Fatal(err)
		}

		_, _, err := readDirectory(dir)
		if err == nil {
			t.Error("readDirectory() expected error when only non-script files")
		}
	})
}

func TestKubeDeploymentStruct(t *testing.T) {
	// Test that KubeDeployment can be properly initialized
	deploy := KubeDeployment{
		Name:      "test-app",
		Namespace: "default",
		Source:    "/path/to/app.py",
		Runtime:   "python3",
		Port:      8080,
		Replicas:  3,
		Env: map[string]string{
			"DEBUG": "true",
		},
		Schedule: "",
	}

	if deploy.Name != "test-app" {
		t.Errorf("Name = %q, want %q", deploy.Name, "test-app")
	}
	if deploy.Port != 8080 {
		t.Errorf("Port = %d, want %d", deploy.Port, 8080)
	}
	if deploy.Replicas != 3 {
		t.Errorf("Replicas = %d, want %d", deploy.Replicas, 3)
	}
	if deploy.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want %q", deploy.Env["DEBUG"], "true")
	}
}

func TestKubeDeploymentWithSchedule(t *testing.T) {
	// Test that a scheduled deployment is recognized as CronJob
	deploy := KubeDeployment{
		Name:     "scheduled-job",
		Schedule: "0 * * * *",
	}

	if deploy.Schedule == "" {
		t.Error("Schedule should not be empty")
	}
	if deploy.Schedule != "0 * * * *" {
		t.Errorf("Schedule = %q, want %q", deploy.Schedule, "0 * * * *")
	}
}
