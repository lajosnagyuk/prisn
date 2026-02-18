package controllers

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	prisnv1alpha1 "github.com/lajosnagyuk/prisn/operator/api/v1alpha1"
)

const (
	// PrisnJobFinalizer is the finalizer for PrisnJob resources
	PrisnJobFinalizer = "prisn.io/job-finalizer"
)

// PrisnJobReconciler reconciles a PrisnJob object
type PrisnJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=prisn.io,resources=prisnjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=prisn.io,resources=prisnjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=prisn.io,resources=prisnjobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is the main reconciliation loop for PrisnJob
func (r *PrisnJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling PrisnJob", "namespace", req.Namespace, "name", req.Name)

	// Fetch the PrisnJob instance
	job := &prisnv1alpha1.PrisnJob{}
	if err := r.Get(ctx, req.NamespacedName, job); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !job.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, job)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(job, PrisnJobFinalizer) {
		controllerutil.AddFinalizer(job, PrisnJobFinalizer)
		if err := r.Update(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip if already completed or failed
	if job.Status.Phase == "Succeeded" || job.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	// Reconcile the source ConfigMap if inline
	if job.Spec.Source.IsInlineSource() {
		if err := r.reconcileInlineSource(ctx, job); err != nil {
			r.setPhase(ctx, job, "Failed", fmt.Sprintf("Source error: %v", err))
			return ctrl.Result{}, err
		}
	}

	// Reconcile the Kubernetes Job
	if err := r.reconcileJob(ctx, job); err != nil {
		r.setPhase(ctx, job, "Failed", fmt.Sprintf("Job error: %v", err))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Update status from Job
	if err := r.updateStatusFromJob(ctx, job); err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// Requeue if still running
	if job.Status.Phase == "Running" || job.Status.Phase == "Pending" {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a PrisnJob
func (r *PrisnJobReconciler) handleDeletion(ctx context.Context, job *prisnv1alpha1.PrisnJob) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of PrisnJob", "name", job.Name)

	r.Recorder.Event(job, corev1.EventTypeNormal, "Deleting", "Cleaning up resources")

	controllerutil.RemoveFinalizer(job, PrisnJobFinalizer)
	if err := r.Update(ctx, job); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileInlineSource creates a ConfigMap for inline code
func (r *PrisnJobReconciler) reconcileInlineSource(ctx context.Context, job *prisnv1alpha1.PrisnJob) error {
	cmName := r.sourceConfigMapName(job)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: job.Namespace,
			Labels:    r.labels(job),
		},
		Data: map[string]string{
			"script": job.Spec.Source.Inline,
		},
	}

	if err := controllerutil.SetControllerReference(job, cm, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: job.Namespace, Name: cmName}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, cm)
		}
		return err
	}

	return nil
}

// reconcileJob creates the Kubernetes Job
func (r *PrisnJobReconciler) reconcileJob(ctx context.Context, prisnJob *prisnv1alpha1.PrisnJob) error {
	k8sJob := r.buildJob(prisnJob)

	if err := controllerutil.SetControllerReference(prisnJob, k8sJob, r.Scheme); err != nil {
		return err
	}

	existing := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: k8sJob.Namespace, Name: k8sJob.Name}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			r.Recorder.Event(prisnJob, corev1.EventTypeNormal, "CreatingJob", "Creating Kubernetes Job")
			r.setPhase(ctx, prisnJob, "Pending", "Creating job")
			return r.Create(ctx, k8sJob)
		}
		return err
	}

	// Job exists, don't update (jobs are immutable)
	return nil
}

// buildJob constructs a Kubernetes Job for the PrisnJob
func (r *PrisnJobReconciler) buildJob(prisnJob *prisnv1alpha1.PrisnJob) *batchv1.Job {
	labels := r.labels(prisnJob)

	// Determine image and command
	image, command := r.runtimeConfig(prisnJob.Spec.Runtime)

	// Build entrypoint
	entrypoint := prisnJob.Spec.Source.Entrypoint
	if entrypoint == "" {
		entrypoint = "script"
	}

	args := []string{"/scripts/" + entrypoint}
	args = append(args, prisnJob.Spec.Args...)

	// Build volumes and mounts
	volumes, mounts := r.buildVolumes(prisnJob)

	// Build init containers (for git source)
	initContainers := r.buildInitContainers(prisnJob)

	// Determine backoff limit
	backoffLimit := prisnJob.Spec.BackoffLimit
	if backoffLimit == 0 {
		backoffLimit = 3
	}

	// Active deadline from timeout
	var activeDeadlineSeconds *int64
	if prisnJob.Spec.Timeout.Duration > 0 {
		seconds := int64(prisnJob.Spec.Timeout.Duration.Seconds())
		activeDeadlineSeconds = &seconds
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.jobName(prisnJob),
			Namespace: prisnJob.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: activeDeadlineSeconds,
			TTLSecondsAfterFinished: prisnJob.Spec.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					InitContainers:     initContainers,
					ServiceAccountName: prisnJob.Spec.ServiceAccountName,
					NodeSelector:       prisnJob.Spec.NodeSelector,
					Tolerations:        prisnJob.Spec.Tolerations,
					Containers: []corev1.Container{
						{
							Name:            "job",
							Image:           image,
							Command:         command,
							Args:            args,
							Env:             prisnJob.Spec.Env,
							EnvFrom:         prisnJob.Spec.EnvFrom,
							Resources:       prisnJob.Spec.Resources.GetResourceRequirements(),
							VolumeMounts:    mounts,
							ImagePullPolicy: corev1.PullIfNotPresent,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return job
}

// buildInitContainers creates init containers for the job
func (r *PrisnJobReconciler) buildInitContainers(prisnJob *prisnv1alpha1.PrisnJob) []corev1.Container {
	var initContainers []corev1.Container

	if prisnJob.Spec.Source.IsGitSource() {
		git := prisnJob.Spec.Source.Git
		ref := git.Ref
		if ref == "" {
			ref = "main"
		}

		// Build git clone command
		cloneCmd := fmt.Sprintf(
			"git clone --depth 1 --branch %s %s /tmp/repo && cp -r /tmp/repo/* /scripts/",
			ref, git.URL,
		)

		// If a subdirectory is specified, only copy that
		if git.Path != "" {
			cloneCmd = fmt.Sprintf(
				"git clone --depth 1 --branch %s %s /tmp/repo && cp -r /tmp/repo/%s/* /scripts/",
				ref, git.URL, git.Path,
			)
		}

		initContainer := corev1.Container{
			Name:  "git-clone",
			Image: "alpine/git:latest",
			Command: []string{"sh", "-c", cloneCmd},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "scripts",
					MountPath: "/scripts",
				},
			},
		}

		// Add git credentials if secret is provided
		if git.SecretRef != nil {
			initContainer.Env = []corev1.EnvVar{
				{
					Name: "GIT_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: *git.SecretRef,
							Key:                  "username",
							Optional:             ptr(true),
						},
					},
				},
				{
					Name: "GIT_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: *git.SecretRef,
							Key:                  "password",
							Optional:             ptr(true),
						},
					},
				},
			}
			cloneCmd = fmt.Sprintf(
				"git config --global credential.helper '!f() { echo username=$GIT_USERNAME; echo password=$GIT_PASSWORD; }; f' && %s",
				cloneCmd,
			)
			initContainer.Command = []string{"sh", "-c", cloneCmd}
		}

		initContainers = append(initContainers, initContainer)
	}

	return initContainers
}

// buildVolumes constructs volumes for the job
func (r *PrisnJobReconciler) buildVolumes(job *prisnv1alpha1.PrisnJob) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount

	source := job.Spec.Source

	if source.IsInlineSource() {
		volumes = append(volumes, corev1.Volume{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: r.sourceConfigMapName(job),
					},
					DefaultMode: ptr(int32(0755)),
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "scripts",
			MountPath: "/scripts",
			ReadOnly:  true,
		})
	} else if source.IsConfigMapSource() {
		volumes = append(volumes, corev1.Volume{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: *source.ConfigMapRef,
					DefaultMode:          ptr(int32(0755)),
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "scripts",
			MountPath: "/scripts",
			ReadOnly:  true,
		})
	} else if source.SecretRef != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  source.SecretRef.Name,
					DefaultMode: ptr(int32(0755)),
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "scripts",
			MountPath: "/scripts",
			ReadOnly:  true,
		})
	} else if source.IsGitSource() {
		// Git source uses init container to clone repo into emptyDir
		volumes = append(volumes, corev1.Volume{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "scripts",
			MountPath: "/scripts",
		})
	}

	return volumes, mounts
}

// updateStatusFromJob updates the PrisnJob status from the Kubernetes Job
func (r *PrisnJobReconciler) updateStatusFromJob(ctx context.Context, prisnJob *prisnv1alpha1.PrisnJob) error {
	k8sJob := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: prisnJob.Namespace, Name: r.jobName(prisnJob)}, k8sJob); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	prisnJob.Status.JobName = k8sJob.Name
	prisnJob.Status.ObservedGeneration = prisnJob.Generation
	prisnJob.Status.Retries = k8sJob.Status.Failed

	// Update times
	if k8sJob.Status.StartTime != nil && prisnJob.Status.StartTime == nil {
		prisnJob.Status.StartTime = k8sJob.Status.StartTime
		r.Recorder.Event(prisnJob, corev1.EventTypeNormal, "Started", "Job started")
	}

	if k8sJob.Status.CompletionTime != nil && prisnJob.Status.CompletionTime == nil {
		prisnJob.Status.CompletionTime = k8sJob.Status.CompletionTime
		if prisnJob.Status.StartTime != nil {
			duration := k8sJob.Status.CompletionTime.Sub(prisnJob.Status.StartTime.Time)
			prisnJob.Status.Duration = duration.Round(time.Second).String()
		}
	}

	// Determine phase
	if k8sJob.Status.Succeeded > 0 {
		r.setPhase(ctx, prisnJob, "Succeeded", "Job completed successfully")
		r.Recorder.Event(prisnJob, corev1.EventTypeNormal, "Succeeded", fmt.Sprintf("Job completed in %s", prisnJob.Status.Duration))
	} else if k8sJob.Status.Failed > 0 {
		// Check if exceeded backoff limit
		backoffLimit := int32(3)
		if prisnJob.Spec.BackoffLimit > 0 {
			backoffLimit = prisnJob.Spec.BackoffLimit
		}
		if k8sJob.Status.Failed >= backoffLimit {
			r.setPhase(ctx, prisnJob, "Failed", fmt.Sprintf("Job failed after %d attempts", k8sJob.Status.Failed))
			r.Recorder.Event(prisnJob, corev1.EventTypeWarning, "Failed", "Job exceeded backoff limit")
		} else {
			r.setPhase(ctx, prisnJob, "Running", fmt.Sprintf("Retry %d/%d", k8sJob.Status.Failed, backoffLimit))
		}
	} else if k8sJob.Status.Active > 0 {
		r.setPhase(ctx, prisnJob, "Running", "Job is running")
	} else {
		r.setPhase(ctx, prisnJob, "Pending", "Waiting for pod")
	}

	// Check for deadline exceeded
	for _, cond := range k8sJob.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Reason == "DeadlineExceeded" {
			r.setPhase(ctx, prisnJob, "Failed", "Job exceeded timeout")
			r.Recorder.Event(prisnJob, corev1.EventTypeWarning, "Timeout", "Job exceeded deadline")
		}
	}

	// Update conditions
	r.updateConditions(prisnJob, k8sJob)

	return r.Status().Update(ctx, prisnJob)
}

// updateConditions updates conditions based on job state
func (r *PrisnJobReconciler) updateConditions(prisnJob *prisnv1alpha1.PrisnJob, k8sJob *batchv1.Job) {
	now := metav1.Now()

	readyCondition := metav1.Condition{
		Type:               prisnv1alpha1.ConditionTypeReady,
		LastTransitionTime: now,
	}

	if k8sJob.Status.Succeeded > 0 {
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = "JobSucceeded"
		readyCondition.Message = "Job completed successfully"
	} else if k8sJob.Status.Failed > 0 {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "JobFailed"
		readyCondition.Message = fmt.Sprintf("Job failed %d times", k8sJob.Status.Failed)
	} else {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "JobRunning"
		readyCondition.Message = "Job is still running"
	}

	setCondition(&prisnJob.Status.Conditions, readyCondition)
}

// setPhase updates the phase and message
func (r *PrisnJobReconciler) setPhase(ctx context.Context, job *prisnv1alpha1.PrisnJob, phase, message string) {
	job.Status.Phase = phase
	job.Status.Message = message
}

// Helper functions
func (r *PrisnJobReconciler) labels(job *prisnv1alpha1.PrisnJob) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       job.Name,
		"app.kubernetes.io/managed-by": "prisn-operator",
		"prisn.io/job":                 job.Name,
	}
}

func (r *PrisnJobReconciler) jobName(job *prisnv1alpha1.PrisnJob) string {
	return job.Name + "-job"
}

func (r *PrisnJobReconciler) sourceConfigMapName(job *prisnv1alpha1.PrisnJob) string {
	return job.Name + "-source"
}

func (r *PrisnJobReconciler) runtimeConfig(runtime string) (image string, command []string) {
	switch runtime {
	case "python", "python3":
		return "python:3.11-slim", []string{"python3"}
	case "node":
		return "node:20-slim", []string{"node"}
	case "bash":
		return "bash:5", []string{"bash"}
	default:
		return "python:3.11-slim", []string{"python3"}
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *PrisnJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&prisnv1alpha1.PrisnJob{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
