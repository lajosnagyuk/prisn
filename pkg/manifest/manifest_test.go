package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrInfer(t *testing.T) {
	// Create temp directory with a Python script
	dir := t.TempDir()

	// Write a simple script
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hello')"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test inference
	pf, err := LoadOrInfer(dir)
	if err != nil {
		t.Fatalf("LoadOrInfer failed: %v", err)
	}

	if pf.Entrypoint != "main.py" {
		t.Errorf("Entrypoint = %q, want main.py", pf.Entrypoint)
	}
	if pf.Runtime != "python3" {
		t.Errorf("Runtime = %q, want python3", pf.Runtime)
	}
	if pf.Type != "job" {
		t.Errorf("Type = %q, want job", pf.Type)
	}
}

func TestLoadPrisnfile(t *testing.T) {
	dir := t.TempDir()

	// Write a Prisnfile
	prismfile := `
name = "my-api"
entrypoint = "server.py"
runtime = "python3"
port = 8080
replicas = 3

[env]
DEBUG = "true"
`
	if err := os.WriteFile(filepath.Join(dir, "Prisnfile"), []byte(prismfile), 0644); err != nil {
		t.Fatal(err)
	}

	pf, err := LoadOrInfer(dir)
	if err != nil {
		t.Fatalf("LoadOrInfer failed: %v", err)
	}

	if pf.Name != "my-api" {
		t.Errorf("Name = %q, want my-api", pf.Name)
	}
	if pf.Entrypoint != "server.py" {
		t.Errorf("Entrypoint = %q, want server.py", pf.Entrypoint)
	}
	if pf.Port != 8080 {
		t.Errorf("Port = %d, want 8080", pf.Port)
	}
	if pf.Replicas != 3 {
		t.Errorf("Replicas = %d, want 3", pf.Replicas)
	}
	if pf.Type != "service" {
		t.Errorf("Type = %q, want service", pf.Type)
	}
	if pf.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want true", pf.Env["DEBUG"])
	}
}

func TestInferType(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		schedule string
		wantType string
	}{
		{"no port, no schedule", 0, "", "job"},
		{"with port", 8080, "", "service"},
		{"with schedule", 0, "0 * * * *", "cronjob"},
		{"port and schedule", 8080, "0 * * * *", "cronjob"}, // schedule wins
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf := &Prisnfile{
				Port:     tt.port,
				Schedule: tt.schedule,
			}
			pf.inferType()
			if pf.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", pf.Type, tt.wantType)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		pf      Prisnfile
		wantErr bool
	}{
		{
			name:    "valid",
			pf:      Prisnfile{Name: "api", Entrypoint: "main.py"},
			wantErr: false,
		},
		{
			name:    "missing name",
			pf:      Prisnfile{Entrypoint: "main.py"},
			wantErr: true,
		},
		{
			name:    "missing entrypoint",
			pf:      Prisnfile{Name: "api"},
			wantErr: true,
		},
		{
			name:    "invalid port",
			pf:      Prisnfile{Name: "api", Entrypoint: "main.py", Port: 99999},
			wantErr: true,
		},
		{
			name:    "negative replicas",
			pf:      Prisnfile{Name: "api", Entrypoint: "main.py", Replicas: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.pf.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDetectEntrypoint(t *testing.T) {
	tests := []struct {
		files       []string
		wantEntry   string
		wantRuntime string
	}{
		{[]string{"main.py"}, "main.py", "python3"},
		{[]string{"app.py"}, "app.py", "python3"},
		{[]string{"index.js"}, "index.js", "node"},
		{[]string{"main.sh"}, "main.sh", "bash"},
		{[]string{"random.py"}, "random.py", "python3"},
		{[]string{"main.py", "other.py"}, "main.py", "python3"}, // priority
		{[]string{}, "", ""},                                     // no files
	}

	for _, tt := range tests {
		t.Run(tt.wantEntry, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
					t.Fatal(err)
				}
			}

			entry, runtime := detectEntrypoint(dir)
			if entry != tt.wantEntry {
				t.Errorf("entrypoint = %q, want %q", entry, tt.wantEntry)
			}
			if runtime != tt.wantRuntime {
				t.Errorf("runtime = %q, want %q", runtime, tt.wantRuntime)
			}
		})
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()

	envContent := `# Comment
DEBUG=true
DATABASE_URL=postgres://localhost/db
EMPTY=
QUOTED="value with spaces"
`
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0644); err != nil {
		t.Fatal(err)
	}

	pf := &Prisnfile{baseDir: dir}
	pf.loadEnvFile()

	tests := []struct {
		key   string
		value string
	}{
		{"DEBUG", "true"},
		{"DATABASE_URL", "postgres://localhost/db"},
		{"EMPTY", ""},
		{"QUOTED", "value with spaces"},
	}

	for _, tt := range tests {
		if got := pf.Env[tt.key]; got != tt.value {
			t.Errorf("Env[%s] = %q, want %q", tt.key, got, tt.value)
		}
	}
}
