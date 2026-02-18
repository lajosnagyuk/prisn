package k8s

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AppStatus represents the status of a PrisnApp.
type AppStatus struct {
	Name            string
	Namespace       string
	Phase           string
	DesiredReplicas int32 // From spec.replicas
	Replicas        int32 // From status.replicas (current)
	ReadyReplicas   int32 // From status.readyReplicas
	ServiceName     string
	Port            int32
	Runtime         string
	Message         string
	Age             time.Duration
}

// JobStatus represents the status of a PrisnJob.
type JobStatus struct {
	Name           string
	Namespace      string
	Phase          string
	Duration       string
	ExitCode       *int32
	Retries        int32
	Message        string
	StartTime      *time.Time
	CompletionTime *time.Time
	Age            time.Duration
}

// CronJobStatus represents the status of a PrisnCronJob.
type CronJobStatus struct {
	Name             string
	Namespace        string
	Schedule         string
	Suspend          bool
	ActiveJobs       int
	LastScheduleTime *time.Time
	NextScheduleTime *time.Time
	Age              time.Duration
}

// ExtractAppStatus extracts status and spec from an unstructured PrisnApp.
func ExtractAppStatus(obj *unstructured.Unstructured) AppStatus {
	status := AppStatus{
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		DesiredReplicas: 1, // default
	}

	// Extract spec fields
	if r, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); found {
		status.DesiredReplicas = int32(r)
	}
	if p, found, _ := unstructured.NestedInt64(obj.Object, "spec", "port"); found {
		status.Port = int32(p)
	}
	if rt, found, _ := unstructured.NestedString(obj.Object, "spec", "runtime"); found {
		status.Runtime = rt
	}

	// Extract status fields
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "phase"); found {
		status.Phase = s
	}
	if r, found, _ := unstructured.NestedInt64(obj.Object, "status", "replicas"); found {
		status.Replicas = int32(r)
	}
	if r, found, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas"); found {
		status.ReadyReplicas = int32(r)
	}
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "serviceName"); found {
		status.ServiceName = s
	}
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "message"); found {
		status.Message = s
	}

	ct := obj.GetCreationTimestamp()
	if !ct.IsZero() {
		status.Age = time.Since(ct.Time)
	}

	return status
}

// ExtractAppSpec extracts spec fields from an unstructured PrisnApp.
func ExtractAppSpec(obj *unstructured.Unstructured) (replicas int32, port int32, runtime string) {
	if r, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); found {
		replicas = int32(r)
	}
	if p, found, _ := unstructured.NestedInt64(obj.Object, "spec", "port"); found {
		port = int32(p)
	}
	if rt, found, _ := unstructured.NestedString(obj.Object, "spec", "runtime"); found {
		runtime = rt
	}
	return
}

// ExtractJobStatus extracts status from an unstructured PrisnJob.
func ExtractJobStatus(obj *unstructured.Unstructured) JobStatus {
	status := JobStatus{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	if s, found, _ := unstructured.NestedString(obj.Object, "status", "phase"); found {
		status.Phase = s
	}
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "duration"); found {
		status.Duration = s
	}
	if r, found, _ := unstructured.NestedInt64(obj.Object, "status", "retries"); found {
		status.Retries = int32(r)
	}
	if s, found, _ := unstructured.NestedString(obj.Object, "status", "message"); found {
		status.Message = s
	}
	if ec, found, _ := unstructured.NestedInt64(obj.Object, "status", "exitCode"); found {
		exitCode := int32(ec)
		status.ExitCode = &exitCode
	}

	ct := obj.GetCreationTimestamp()
	if !ct.IsZero() {
		status.Age = time.Since(ct.Time)
	}

	return status
}

// ExtractCronJobStatus extracts status from an unstructured PrisnCronJob.
func ExtractCronJobStatus(obj *unstructured.Unstructured) CronJobStatus {
	status := CronJobStatus{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	if s, found, _ := unstructured.NestedString(obj.Object, "spec", "schedule"); found {
		status.Schedule = s
	}
	if s, found, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); found {
		status.Suspend = s
	}

	if active, found, _ := unstructured.NestedSlice(obj.Object, "status", "active"); found {
		status.ActiveJobs = len(active)
	}

	ct := obj.GetCreationTimestamp()
	if !ct.IsZero() {
		status.Age = time.Since(ct.Time)
	}

	return status
}

// FormatAge formats a duration as a human-readable age string.
func FormatAge(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
