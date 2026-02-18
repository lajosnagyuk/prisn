// Package v1alpha1 contains API Schema definitions for prisn v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=prisn.io
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// PrisnApp - Long-running services (HTTP APIs, workers, etc.)
// ============================================================================

// PrisnAppSpec defines the desired state of PrisnApp
type PrisnAppSpec struct {
	// Source is the script or directory to run
	// +kubebuilder:validation:Required
	Source SourceSpec `json:"source"`

	// Runtime specifies the execution runtime (python, node, bash)
	// +kubebuilder:validation:Enum=python;python3;node;bash
	// +kubebuilder:default=python3
	Runtime string `json:"runtime,omitempty"`

	// Replicas is the desired number of instances
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`

	// Port is the port the app listens on (for services)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// Args are command-line arguments to pass to the script
	Args []string `json:"args,omitempty"`

	// Env are environment variables
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom are environment variable sources (ConfigMaps, Secrets)
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Resources are compute resource requirements
	Resources ResourceSpec `json:"resources,omitempty"`

	// HealthCheck configures the health check probe
	HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`

	// ServiceAccountName is the Kubernetes service account to use
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// ImagePullSecrets for pulling the base image
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// NodeSelector for pod scheduling
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for pod scheduling
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Suspend pauses the app (scales to 0 without changing replicas)
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`

	// Storage configures persistent storage for the app
	Storage []StorageSpec `json:"storage,omitempty"`

	// Ingress configures external access to the app
	Ingress *IngressSpec `json:"ingress,omitempty"`
}

// IngressSpec defines external access configuration
type IngressSpec struct {
	// Enabled controls whether an Ingress is created
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname for the Ingress (required if enabled)
	Host string `json:"host,omitempty"`

	// Path is the URL path prefix (default: /)
	// +kubebuilder:default=/
	Path string `json:"path,omitempty"`

	// PathType specifies how the path should be matched
	// +kubebuilder:validation:Enum=Prefix;Exact;ImplementationSpecific
	// +kubebuilder:default=Prefix
	PathType string `json:"pathType,omitempty"`

	// TLS enables HTTPS with automatic certificate (requires cert-manager)
	TLS bool `json:"tls,omitempty"`

	// IngressClassName specifies which Ingress controller to use
	IngressClassName string `json:"ingressClassName,omitempty"`

	// Annotations are added to the Ingress resource
	Annotations map[string]string `json:"annotations,omitempty"`
}

// StorageSpec defines persistent storage configuration
type StorageSpec struct {
	// Name is the identifier for this storage mount
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// MountPath is where to mount the storage in the container
	// +kubebuilder:validation:Required
	MountPath string `json:"mountPath"`

	// Size is the requested storage size (e.g., "1Gi", "10Gi")
	// Required if not using existingClaim
	Size string `json:"size,omitempty"`

	// StorageClassName is the storage class to use
	// Must support RWX for multi-replica apps (e.g., nfs, azurefile, efs)
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessMode is the access mode for the PVC
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany;ReadOnlyMany
	// +kubebuilder:default=ReadWriteMany
	AccessMode string `json:"accessMode,omitempty"`

	// ExistingClaim is the name of an existing PVC to use
	// If set, size and storageClassName are ignored
	ExistingClaim string `json:"existingClaim,omitempty"`

	// SubPath is a subdirectory within the volume to mount
	SubPath string `json:"subPath,omitempty"`

	// ReadOnly mounts the volume as read-only
	// +kubebuilder:default=false
	ReadOnly bool `json:"readOnly,omitempty"`
}

// SourceSpec defines where the code comes from
type SourceSpec struct {
	// Inline code (for simple scripts)
	Inline string `json:"inline,omitempty"`

	// ConfigMapRef references a ConfigMap containing the code
	ConfigMapRef *corev1.LocalObjectReference `json:"configMapRef,omitempty"`

	// SecretRef references a Secret containing the code
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// Git repository to clone
	Git *GitSource `json:"git,omitempty"`

	// Entrypoint is the script to execute (default: auto-detect)
	Entrypoint string `json:"entrypoint,omitempty"`
}

// GitSource defines a git repository source
type GitSource struct {
	// URL is the git repository URL
	// +kubebuilder:validation:Required
	URL string `json:"url"`

	// Ref is the git reference (branch, tag, or commit)
	// +kubebuilder:default=main
	Ref string `json:"ref,omitempty"`

	// Path is the subdirectory within the repo
	Path string `json:"path,omitempty"`

	// SecretRef for authentication
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// ResourceSpec defines compute resources
type ResourceSpec struct {
	// CPU limit (e.g., "500m", "1")
	CPU string `json:"cpu,omitempty"`

	// Memory limit (e.g., "256Mi", "1Gi")
	Memory string `json:"memory,omitempty"`

	// Requests are the guaranteed resources
	Requests *ResourceRequests `json:"requests,omitempty"`
}

// ResourceRequests defines requested resources
type ResourceRequests struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// HealthCheckSpec defines health check configuration
type HealthCheckSpec struct {
	// Path is the HTTP path for health checks (default: /health)
	// +kubebuilder:default=/health
	Path string `json:"path,omitempty"`

	// InitialDelaySeconds before starting checks
	// +kubebuilder:default=5
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`

	// PeriodSeconds between checks
	// +kubebuilder:default=10
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`

	// FailureThreshold before marking unhealthy
	// +kubebuilder:default=3
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// PrisnAppStatus defines the observed state of PrisnApp
type PrisnAppStatus struct {
	// Phase is the current lifecycle phase
	// +kubebuilder:validation:Enum=Pending;Building;Running;Failed;Suspended
	Phase string `json:"phase,omitempty"`

	// Replicas is the current number of running replicas
	Replicas int32 `json:"replicas,omitempty"`

	// ReadyReplicas is the number of ready replicas
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Conditions represent the latest observations
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastTransitionTime is when the status last changed
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// Message is a human-readable status message
	Message string `json:"message,omitempty"`

	// ServiceName is the name of the created Service
	ServiceName string `json:"serviceName,omitempty"`

	// DeploymentName is the name of the created Deployment
	DeploymentName string `json:"deploymentName,omitempty"`

	// EnvHash is the hash of the current environment setup
	EnvHash string `json:"envHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PrisnApp is the Schema for the prisnapps API
type PrisnApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PrisnAppSpec   `json:"spec,omitempty"`
	Status PrisnAppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PrisnAppList contains a list of PrisnApp
type PrisnAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrisnApp `json:"items"`
}

// ============================================================================
// PrisnJob - One-time job execution
// ============================================================================

// PrisnJobSpec defines the desired state of PrisnJob
type PrisnJobSpec struct {
	// Source is the script to run
	// +kubebuilder:validation:Required
	Source SourceSpec `json:"source"`

	// Runtime specifies the execution runtime
	// +kubebuilder:validation:Enum=python;python3;node;bash
	// +kubebuilder:default=python3
	Runtime string `json:"runtime,omitempty"`

	// Args are command-line arguments
	Args []string `json:"args,omitempty"`

	// Env are environment variables
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom are environment variable sources
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Resources are compute resource requirements
	Resources ResourceSpec `json:"resources,omitempty"`

	// Timeout is the maximum execution time
	// +kubebuilder:default="1h"
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// BackoffLimit is the number of retries before marking failed
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3
	BackoffLimit int32 `json:"backoffLimit,omitempty"`

	// TTLSecondsAfterFinished cleans up completed jobs
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// ServiceAccountName is the Kubernetes service account to use
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// NodeSelector for pod scheduling
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for pod scheduling
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Storage configures persistent storage for the job
	Storage []StorageSpec `json:"storage,omitempty"`
}

// PrisnJobStatus defines the observed state of PrisnJob
type PrisnJobStatus struct {
	// Phase is the current lifecycle phase
	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
	Phase string `json:"phase,omitempty"`

	// StartTime is when the job started
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the job completed
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Duration is how long the job ran
	Duration string `json:"duration,omitempty"`

	// ExitCode is the script's exit code
	ExitCode *int32 `json:"exitCode,omitempty"`

	// Retries is the number of retry attempts
	Retries int32 `json:"retries,omitempty"`

	// Conditions represent the latest observations
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Message is a human-readable status message
	Message string `json:"message,omitempty"`

	// JobName is the name of the created Job
	JobName string `json:"jobName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Duration",type=string,JSONPath=`.status.duration`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PrisnJob is the Schema for the prisnjobs API
type PrisnJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PrisnJobSpec   `json:"spec,omitempty"`
	Status PrisnJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PrisnJobList contains a list of PrisnJob
type PrisnJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrisnJob `json:"items"`
}

// ============================================================================
// PrisnCronJob - Scheduled job execution
// ============================================================================

// PrisnCronJobSpec defines the desired state of PrisnCronJob
type PrisnCronJobSpec struct {
	// Schedule in Cron format (or @hourly, @daily, etc.)
	// +kubebuilder:validation:Required
	Schedule string `json:"schedule"`

	// JobTemplate defines the job to create
	// +kubebuilder:validation:Required
	JobTemplate PrisnJobSpec `json:"jobTemplate"`

	// ConcurrencyPolicy specifies how to treat concurrent executions
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default=Forbid
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// Suspend prevents new executions
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`

	// SuccessfulJobsHistoryLimit is how many successful jobs to keep
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3
	SuccessfulJobsHistoryLimit int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// FailedJobsHistoryLimit is how many failed jobs to keep
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	FailedJobsHistoryLimit int32 `json:"failedJobsHistoryLimit,omitempty"`

	// StartingDeadlineSeconds is the deadline for starting a missed job
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`
}

// PrisnCronJobStatus defines the observed state of PrisnCronJob
type PrisnCronJobStatus struct {
	// Active is a list of currently running jobs
	Active []corev1.ObjectReference `json:"active,omitempty"`

	// LastScheduleTime is when the job was last scheduled
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`

	// LastSuccessfulTime is when the job last succeeded
	LastSuccessfulTime *metav1.Time `json:"lastSuccessfulTime,omitempty"`

	// NextScheduleTime is when the next job will run
	NextScheduleTime *metav1.Time `json:"nextScheduleTime,omitempty"`

	// Conditions represent the latest observations
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Suspend",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="Last Schedule",type=date,JSONPath=`.status.lastScheduleTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PrisnCronJob is the Schema for the prisncronjobs API
type PrisnCronJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PrisnCronJobSpec   `json:"spec,omitempty"`
	Status PrisnCronJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PrisnCronJobList contains a list of PrisnCronJob
type PrisnCronJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrisnCronJob `json:"items"`
}

// ============================================================================
// Condition Types
// ============================================================================

const (
	// ConditionTypeReady indicates the resource is ready
	ConditionTypeReady = "Ready"

	// ConditionTypeProgressing indicates the resource is progressing
	ConditionTypeProgressing = "Progressing"

	// ConditionTypeDegraded indicates the resource is degraded
	ConditionTypeDegraded = "Degraded"

	// ConditionTypeEnvReady indicates the environment is ready
	ConditionTypeEnvReady = "EnvironmentReady"

	// ConditionTypeSourceReady indicates the source is ready
	ConditionTypeSourceReady = "SourceReady"
)

// ============================================================================
// Helper methods
// ============================================================================

// GetResourceRequirements converts ResourceSpec to corev1.ResourceRequirements
func (r *ResourceSpec) GetResourceRequirements() corev1.ResourceRequirements {
	req := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}

	if r.CPU != "" {
		req.Limits[corev1.ResourceCPU] = resource.MustParse(r.CPU)
	}
	if r.Memory != "" {
		req.Limits[corev1.ResourceMemory] = resource.MustParse(r.Memory)
	}

	if r.Requests != nil {
		if r.Requests.CPU != "" {
			req.Requests[corev1.ResourceCPU] = resource.MustParse(r.Requests.CPU)
		}
		if r.Requests.Memory != "" {
			req.Requests[corev1.ResourceMemory] = resource.MustParse(r.Requests.Memory)
		}
	}

	return req
}

// IsInlineSource returns true if the source is inline code
func (s *SourceSpec) IsInlineSource() bool {
	return s.Inline != ""
}

// IsGitSource returns true if the source is a git repository
func (s *SourceSpec) IsGitSource() bool {
	return s.Git != nil
}

// IsConfigMapSource returns true if the source is a ConfigMap
func (s *SourceSpec) IsConfigMapSource() bool {
	return s.ConfigMapRef != nil
}
