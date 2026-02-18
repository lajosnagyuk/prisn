package k8s

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// =============================================================================
// Builder Tests
// =============================================================================

func TestAppBuilder(t *testing.T) {
	app := NewAppBuilder("my-app").
		Namespace("test-ns").
		InlineSource("print('hello')").
		Runtime("python3").
		Replicas(3).
		Port(8080).
		Args([]string{"--verbose", "--debug"}).
		Env(map[string]string{"ENV": "prod", "DEBUG": "true"}).
		Resources("500m", "256Mi").
		Build()

	// Check metadata
	if app.GetName() != "my-app" {
		t.Errorf("expected name 'my-app', got %q", app.GetName())
	}
	if app.GetNamespace() != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", app.GetNamespace())
	}

	// Check apiVersion and kind
	if app.GetAPIVersion() != "prisn.io/v1alpha1" {
		t.Errorf("expected apiVersion 'prisn.io/v1alpha1', got %q", app.GetAPIVersion())
	}
	if app.GetKind() != "PrisnApp" {
		t.Errorf("expected kind 'PrisnApp', got %q", app.GetKind())
	}

	// Check spec fields
	spec, found, _ := unstructured.NestedMap(app.Object, "spec")
	if !found {
		t.Fatal("spec not found")
	}

	if runtime, _, _ := unstructured.NestedString(spec, "runtime"); runtime != "python3" {
		t.Errorf("expected runtime 'python3', got %q", runtime)
	}

	if replicas, _, _ := unstructured.NestedInt64(spec, "replicas"); replicas != 3 {
		t.Errorf("expected replicas 3, got %d", replicas)
	}

	if port, _, _ := unstructured.NestedInt64(spec, "port"); port != 8080 {
		t.Errorf("expected port 8080, got %d", port)
	}

	// Check source
	source, found, _ := unstructured.NestedMap(spec, "source")
	if !found {
		t.Fatal("source not found")
	}
	if inline, _, _ := unstructured.NestedString(source, "inline"); inline != "print('hello')" {
		t.Errorf("expected inline source, got %q", inline)
	}

	// Check args
	args, found, _ := unstructured.NestedStringSlice(spec, "args")
	if !found || len(args) != 2 {
		t.Errorf("expected 2 args, got %v", args)
	}

	// Check env
	envList, found, _ := unstructured.NestedSlice(spec, "env")
	if !found || len(envList) != 2 {
		t.Errorf("expected 2 env vars, got %d", len(envList))
	}

	// Check resources
	resources, found, _ := unstructured.NestedMap(spec, "resources")
	if !found {
		t.Fatal("resources not found")
	}
	if cpu, _, _ := unstructured.NestedString(resources, "cpu"); cpu != "500m" {
		t.Errorf("expected cpu '500m', got %q", cpu)
	}
}

func TestAppBuilderConfigMapSource(t *testing.T) {
	app := NewAppBuilder("api").
		ConfigMapSource("api-code", "main.py").
		Build()

	source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	cmRef, found, _ := unstructured.NestedMap(source, "configMapRef")
	if !found {
		t.Fatal("configMapRef not found")
	}

	if name, _, _ := unstructured.NestedString(cmRef, "name"); name != "api-code" {
		t.Errorf("expected configMap name 'api-code', got %q", name)
	}

	if entrypoint, _, _ := unstructured.NestedString(source, "entrypoint"); entrypoint != "main.py" {
		t.Errorf("expected entrypoint 'main.py', got %q", entrypoint)
	}
}

func TestAppBuilderGitSource(t *testing.T) {
	app := NewAppBuilder("api").
		GitSource("https://github.com/example/repo", "main", "src/app", "git-creds").
		Build()

	source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	git, found, _ := unstructured.NestedMap(source, "git")
	if !found {
		t.Fatal("git source not found")
	}

	if url, _, _ := unstructured.NestedString(git, "url"); url != "https://github.com/example/repo" {
		t.Errorf("expected git url, got %q", url)
	}
	if ref, _, _ := unstructured.NestedString(git, "ref"); ref != "main" {
		t.Errorf("expected ref 'main', got %q", ref)
	}
	if path, _, _ := unstructured.NestedString(git, "path"); path != "src/app" {
		t.Errorf("expected path 'src/app', got %q", path)
	}
}

func TestAppBuilderStorage(t *testing.T) {
	app := NewAppBuilder("api").
		InlineSource("print('hi')").
		Storage("data", "/data", "10Gi", "standard").
		Storage("cache", "/cache", "1Gi", "fast-ssd").
		Build()

	storageList, found, _ := unstructured.NestedSlice(app.Object, "spec", "storage")
	if !found {
		t.Fatal("storage not found")
	}
	if len(storageList) != 2 {
		t.Errorf("expected 2 storage mounts, got %d", len(storageList))
	}

	// Check first storage
	storage0 := storageList[0].(map[string]interface{})
	if storage0["name"] != "data" {
		t.Errorf("expected storage name 'data', got %v", storage0["name"])
	}
	if storage0["mountPath"] != "/data" {
		t.Errorf("expected mountPath '/data', got %v", storage0["mountPath"])
	}
}

func TestAppBuilderIngress(t *testing.T) {
	app := NewAppBuilder("api").
		InlineSource("print('hi')").
		Port(8080).
		Ingress("api.example.com", "/v1", "nginx", true).
		Build()

	ingress, found, _ := unstructured.NestedMap(app.Object, "spec", "ingress")
	if !found {
		t.Fatal("ingress not found")
	}

	if enabled, _, _ := unstructured.NestedBool(ingress, "enabled"); !enabled {
		t.Error("expected ingress enabled")
	}
	if host, _, _ := unstructured.NestedString(ingress, "host"); host != "api.example.com" {
		t.Errorf("expected host 'api.example.com', got %q", host)
	}
	if tls, _, _ := unstructured.NestedBool(ingress, "tls"); !tls {
		t.Error("expected tls enabled")
	}
}

func TestJobBuilder(t *testing.T) {
	job := NewJobBuilder("backup-job").
		Namespace("jobs").
		InlineSource("echo 'backing up...'").
		Runtime("bash").
		Args([]string{"-c", "backup.sh"}).
		Timeout("30m").
		BackoffLimit(5).
		Build()

	if job.GetName() != "backup-job" {
		t.Errorf("expected name 'backup-job', got %q", job.GetName())
	}
	if job.GetKind() != "PrisnJob" {
		t.Errorf("expected kind 'PrisnJob', got %q", job.GetKind())
	}

	spec, _, _ := unstructured.NestedMap(job.Object, "spec")
	if timeout, _, _ := unstructured.NestedString(spec, "timeout"); timeout != "30m" {
		t.Errorf("expected timeout '30m', got %q", timeout)
	}
	if backoff, _, _ := unstructured.NestedInt64(spec, "backoffLimit"); backoff != 5 {
		t.Errorf("expected backoffLimit 5, got %d", backoff)
	}
}

func TestCronJobBuilder(t *testing.T) {
	cronJob := NewCronJobBuilder("nightly-backup").
		Namespace("scheduled").
		Schedule("0 2 * * *").
		InlineSource("echo 'nightly backup'").
		Runtime("bash").
		ConcurrencyPolicy("Forbid").
		Suspend(false).
		Build()

	if cronJob.GetName() != "nightly-backup" {
		t.Errorf("expected name 'nightly-backup', got %q", cronJob.GetName())
	}
	if cronJob.GetKind() != "PrisnCronJob" {
		t.Errorf("expected kind 'PrisnCronJob', got %q", cronJob.GetKind())
	}

	spec, _, _ := unstructured.NestedMap(cronJob.Object, "spec")
	if schedule, _, _ := unstructured.NestedString(spec, "schedule"); schedule != "0 2 * * *" {
		t.Errorf("expected schedule '0 2 * * *', got %q", schedule)
	}
	if policy, _, _ := unstructured.NestedString(spec, "concurrencyPolicy"); policy != "Forbid" {
		t.Errorf("expected concurrencyPolicy 'Forbid', got %q", policy)
	}

	// Check jobTemplate
	jobTemplate, found, _ := unstructured.NestedMap(spec, "jobTemplate")
	if !found {
		t.Fatal("jobTemplate not found")
	}
	if runtime, _, _ := unstructured.NestedString(jobTemplate, "runtime"); runtime != "bash" {
		t.Errorf("expected runtime 'bash', got %q", runtime)
	}
}

// =============================================================================
// Status Extraction Tests
// =============================================================================

func TestExtractAppStatus(t *testing.T) {
	// Build a mock PrisnApp
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "prisn.io/v1alpha1",
			"kind":       "PrisnApp",
			"metadata": map[string]interface{}{
				"name":              "test-app",
				"namespace":         "test-ns",
				"creationTimestamp": "2024-01-15T10:00:00Z",
			},
			"spec": map[string]interface{}{
				"replicas": int64(5),
				"port":     int64(8080),
				"runtime":  "python3",
			},
			"status": map[string]interface{}{
				"phase":         "Running",
				"replicas":      int64(3),
				"readyReplicas": int64(2),
				"serviceName":   "test-app-svc",
				"message":       "All good",
			},
		},
	}

	// Set creation timestamp properly
	app.SetCreationTimestamp(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})

	status := ExtractAppStatus(app)

	if status.Name != "test-app" {
		t.Errorf("expected name 'test-app', got %q", status.Name)
	}
	if status.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", status.Namespace)
	}
	if status.Phase != "Running" {
		t.Errorf("expected phase 'Running', got %q", status.Phase)
	}
	// DesiredReplicas comes from spec.replicas
	if status.DesiredReplicas != 5 {
		t.Errorf("expected desiredReplicas 5, got %d", status.DesiredReplicas)
	}
	// Replicas comes from status.replicas (current)
	if status.Replicas != 3 {
		t.Errorf("expected replicas 3, got %d", status.Replicas)
	}
	if status.ReadyReplicas != 2 {
		t.Errorf("expected readyReplicas 2, got %d", status.ReadyReplicas)
	}
	if status.Port != 8080 {
		t.Errorf("expected port 8080, got %d", status.Port)
	}
	if status.Runtime != "python3" {
		t.Errorf("expected runtime 'python3', got %q", status.Runtime)
	}
	if status.ServiceName != "test-app-svc" {
		t.Errorf("expected serviceName 'test-app-svc', got %q", status.ServiceName)
	}
	if status.Message != "All good" {
		t.Errorf("expected message 'All good', got %q", status.Message)
	}
	if status.Age < 59*time.Minute || status.Age > 61*time.Minute {
		t.Errorf("expected age around 1 hour, got %v", status.Age)
	}
}

func TestExtractAppSpec(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": int64(5),
				"port":     int64(9090),
				"runtime":  "node",
			},
		},
	}

	replicas, port, runtime := ExtractAppSpec(app)

	if replicas != 5 {
		t.Errorf("expected replicas 5, got %d", replicas)
	}
	if port != 9090 {
		t.Errorf("expected port 9090, got %d", port)
	}
	if runtime != "node" {
		t.Errorf("expected runtime 'node', got %q", runtime)
	}
}

func TestExtractJobStatus(t *testing.T) {
	job := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-job",
				"namespace": "jobs",
			},
			"status": map[string]interface{}{
				"phase":    "Succeeded",
				"duration": "2m30s",
				"exitCode": int64(0),
				"retries":  int64(1),
				"message":  "Completed successfully",
			},
		},
	}
	job.SetCreationTimestamp(metav1.Time{Time: time.Now().Add(-30 * time.Minute)})

	status := ExtractJobStatus(job)

	if status.Name != "test-job" {
		t.Errorf("expected name 'test-job', got %q", status.Name)
	}
	if status.Phase != "Succeeded" {
		t.Errorf("expected phase 'Succeeded', got %q", status.Phase)
	}
	if status.Duration != "2m30s" {
		t.Errorf("expected duration '2m30s', got %q", status.Duration)
	}
	if status.ExitCode == nil || *status.ExitCode != 0 {
		t.Errorf("expected exitCode 0, got %v", status.ExitCode)
	}
	if status.Retries != 1 {
		t.Errorf("expected retries 1, got %d", status.Retries)
	}
}

func TestExtractCronJobStatus(t *testing.T) {
	cronJob := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "nightly-job",
				"namespace": "scheduled",
			},
			"spec": map[string]interface{}{
				"schedule": "0 2 * * *",
				"suspend":  true,
			},
			"status": map[string]interface{}{
				"active": []interface{}{
					map[string]interface{}{"name": "job-1"},
					map[string]interface{}{"name": "job-2"},
				},
			},
		},
	}
	cronJob.SetCreationTimestamp(metav1.Time{Time: time.Now().Add(-24 * time.Hour)})

	status := ExtractCronJobStatus(cronJob)

	if status.Name != "nightly-job" {
		t.Errorf("expected name 'nightly-job', got %q", status.Name)
	}
	if status.Schedule != "0 2 * * *" {
		t.Errorf("expected schedule '0 2 * * *', got %q", status.Schedule)
	}
	if !status.Suspend {
		t.Error("expected suspend to be true")
	}
	if status.ActiveJobs != 2 {
		t.Errorf("expected 2 active jobs, got %d", status.ActiveJobs)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestFormatAge(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "<1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{2 * time.Hour, "2h"},
		{23 * time.Hour, "23h"},
		{48 * time.Hour, "2d"},
		{72 * time.Hour, "3d"},
	}

	for _, tt := range tests {
		result := FormatAge(tt.duration)
		if result != tt.expected {
			t.Errorf("FormatAge(%v) = %q, want %q", tt.duration, result, tt.expected)
		}
	}
}

func TestGVRDefinitions(t *testing.T) {
	// Verify GVR definitions are correct
	if PrisnAppGVR.Group != "prisn.io" {
		t.Errorf("PrisnAppGVR.Group = %q, want 'prisn.io'", PrisnAppGVR.Group)
	}
	if PrisnAppGVR.Version != "v1alpha1" {
		t.Errorf("PrisnAppGVR.Version = %q, want 'v1alpha1'", PrisnAppGVR.Version)
	}
	if PrisnAppGVR.Resource != "prisnapps" {
		t.Errorf("PrisnAppGVR.Resource = %q, want 'prisnapps'", PrisnAppGVR.Resource)
	}

	if PrisnJobGVR.Resource != "prisnjobs" {
		t.Errorf("PrisnJobGVR.Resource = %q, want 'prisnjobs'", PrisnJobGVR.Resource)
	}

	if PrisnCronJobGVR.Resource != "prisncronjobs" {
		t.Errorf("PrisnCronJobGVR.Resource = %q, want 'prisncronjobs'", PrisnCronJobGVR.Resource)
	}
}
