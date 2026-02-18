package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/robfig/cron/v3"
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
	// PrisnCronJobFinalizer is the finalizer for PrisnCronJob resources
	PrisnCronJobFinalizer = "prisn.io/cronjob-finalizer"
)

// PrisnCronJobReconciler reconciles a PrisnCronJob object
type PrisnCronJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time // For testing
}

// +kubebuilder:rbac:groups=prisn.io,resources=prisncronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=prisn.io,resources=prisncronjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=prisn.io,resources=prisncronjobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=prisn.io,resources=prisnjobs,verbs=get;list;watch;create;delete

// Reconcile is the main reconciliation loop for PrisnCronJob
func (r *PrisnCronJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling PrisnCronJob", "namespace", req.Namespace, "name", req.Name)

	// Fetch the PrisnCronJob instance
	cronJob := &prisnv1alpha1.PrisnCronJob{}
	if err := r.Get(ctx, req.NamespacedName, cronJob); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !cronJob.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cronJob)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(cronJob, PrisnCronJobFinalizer) {
		controllerutil.AddFinalizer(cronJob, PrisnCronJobFinalizer)
		if err := r.Update(ctx, cronJob); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip if suspended
	if cronJob.Spec.Suspend {
		logger.V(1).Info("CronJob is suspended", "name", cronJob.Name)
		return ctrl.Result{}, nil
	}

	// Get all child jobs
	childJobs, err := r.listChildJobs(ctx, cronJob)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update active jobs list in status
	r.updateActiveJobs(cronJob, childJobs)

	// Clean up old jobs
	if err := r.cleanupOldJobs(ctx, cronJob, childJobs); err != nil {
		logger.Error(err, "Failed to cleanup old jobs")
	}

	// Check concurrency policy and create new job if needed
	now := r.now()
	schedule, err := r.parseSchedule(cronJob.Spec.Schedule)
	if err != nil {
		r.Recorder.Event(cronJob, corev1.EventTypeWarning, "InvalidSchedule", fmt.Sprintf("Invalid schedule: %v", err))
		return ctrl.Result{}, nil
	}

	// Calculate next schedule time
	nextSchedule := r.getNextSchedule(cronJob, schedule, now)
	if nextSchedule != nil {
		cronJob.Status.NextScheduleTime = &metav1.Time{Time: *nextSchedule}
	}

	// Check if we should run now
	missedRun, nextRun, err := r.getMissedRun(cronJob, schedule, now)
	if err != nil {
		logger.Error(err, "Unable to determine missed run")
		return ctrl.Result{}, nil
	}

	// Calculate requeue time
	requeueAfter := nextRun.Sub(now)
	if requeueAfter < 0 {
		requeueAfter = 0
	}

	// No missed run, wait for next schedule
	if missedRun == nil {
		return ctrl.Result{RequeueAfter: requeueAfter}, r.Status().Update(ctx, cronJob)
	}

	// Check starting deadline
	if cronJob.Spec.StartingDeadlineSeconds != nil {
		deadline := missedRun.Add(time.Duration(*cronJob.Spec.StartingDeadlineSeconds) * time.Second)
		if now.After(deadline) {
			logger.V(1).Info("Missed starting deadline", "deadline", deadline)
			r.Recorder.Event(cronJob, corev1.EventTypeWarning, "MissedDeadline", "Missed starting deadline for scheduled run")
			return ctrl.Result{RequeueAfter: requeueAfter}, r.Status().Update(ctx, cronJob)
		}
	}

	// Check concurrency policy
	if cronJob.Spec.ConcurrencyPolicy == "Forbid" && len(cronJob.Status.Active) > 0 {
		logger.V(1).Info("Concurrent execution forbidden, skipping", "active", len(cronJob.Status.Active))
		return ctrl.Result{RequeueAfter: requeueAfter}, r.Status().Update(ctx, cronJob)
	}

	if cronJob.Spec.ConcurrencyPolicy == "Replace" && len(cronJob.Status.Active) > 0 {
		// Delete active jobs
		for _, ref := range cronJob.Status.Active {
			job := &prisnv1alpha1.PrisnJob{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, job); err != nil {
				if !apierrors.IsNotFound(err) {
					logger.Error(err, "Failed to get job to replace")
				}
				continue
			}
			if err := r.Delete(ctx, job); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete job for replacement")
			}
		}
		cronJob.Status.Active = nil
	}

	// Create new job
	job, err := r.createJobFromTemplate(ctx, cronJob, *missedRun)
	if err != nil {
		r.Recorder.Event(cronJob, corev1.EventTypeWarning, "FailedCreate", fmt.Sprintf("Failed to create job: %v", err))
		return ctrl.Result{}, err
	}

	r.Recorder.Event(cronJob, corev1.EventTypeNormal, "SuccessfulCreate", fmt.Sprintf("Created job %s", job.Name))
	cronJob.Status.LastScheduleTime = &metav1.Time{Time: *missedRun}

	// Add to active list
	cronJob.Status.Active = append(cronJob.Status.Active, corev1.ObjectReference{
		APIVersion: prisnv1alpha1.GroupVersion.String(),
		Kind:       "PrisnJob",
		Name:       job.Name,
		Namespace:  job.Namespace,
		UID:        job.UID,
	})

	return ctrl.Result{RequeueAfter: requeueAfter}, r.Status().Update(ctx, cronJob)
}

// handleDeletion handles the deletion of a PrisnCronJob
func (r *PrisnCronJobReconciler) handleDeletion(ctx context.Context, cronJob *prisnv1alpha1.PrisnCronJob) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of PrisnCronJob", "name", cronJob.Name)

	r.Recorder.Event(cronJob, corev1.EventTypeNormal, "Deleting", "Cleaning up child jobs")

	controllerutil.RemoveFinalizer(cronJob, PrisnCronJobFinalizer)
	if err := r.Update(ctx, cronJob); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// listChildJobs lists all PrisnJobs owned by the CronJob
func (r *PrisnCronJobReconciler) listChildJobs(ctx context.Context, cronJob *prisnv1alpha1.PrisnCronJob) ([]prisnv1alpha1.PrisnJob, error) {
	var childJobs prisnv1alpha1.PrisnJobList
	if err := r.List(ctx, &childJobs, client.InNamespace(cronJob.Namespace), client.MatchingLabels{
		"prisn.io/cronjob": cronJob.Name,
	}); err != nil {
		return nil, err
	}
	return childJobs.Items, nil
}

// updateActiveJobs updates the active jobs list in status
func (r *PrisnCronJobReconciler) updateActiveJobs(cronJob *prisnv1alpha1.PrisnCronJob, childJobs []prisnv1alpha1.PrisnJob) {
	var active []corev1.ObjectReference
	var lastSuccessful *time.Time

	for _, job := range childJobs {
		if job.Status.Phase == "Running" || job.Status.Phase == "Pending" {
			active = append(active, corev1.ObjectReference{
				APIVersion: prisnv1alpha1.GroupVersion.String(),
				Kind:       "PrisnJob",
				Name:       job.Name,
				Namespace:  job.Namespace,
				UID:        job.UID,
			})
		}
		if job.Status.Phase == "Succeeded" && job.Status.CompletionTime != nil {
			if lastSuccessful == nil || job.Status.CompletionTime.Time.After(*lastSuccessful) {
				t := job.Status.CompletionTime.Time
				lastSuccessful = &t
			}
		}
	}

	cronJob.Status.Active = active
	if lastSuccessful != nil {
		cronJob.Status.LastSuccessfulTime = &metav1.Time{Time: *lastSuccessful}
	}
}

// cleanupOldJobs removes old completed jobs based on history limits
func (r *PrisnCronJobReconciler) cleanupOldJobs(ctx context.Context, cronJob *prisnv1alpha1.PrisnCronJob, childJobs []prisnv1alpha1.PrisnJob) error {
	logger := log.FromContext(ctx)

	var successful, failed []prisnv1alpha1.PrisnJob
	for _, job := range childJobs {
		if job.Status.Phase == "Succeeded" {
			successful = append(successful, job)
		} else if job.Status.Phase == "Failed" {
			failed = append(failed, job)
		}
	}

	// Sort by completion time (oldest first)
	sortByCompletionTime := func(jobs []prisnv1alpha1.PrisnJob) {
		sort.Slice(jobs, func(i, j int) bool {
			if jobs[i].Status.CompletionTime == nil {
				return true
			}
			if jobs[j].Status.CompletionTime == nil {
				return false
			}
			return jobs[i].Status.CompletionTime.Before(jobs[j].Status.CompletionTime)
		})
	}

	sortByCompletionTime(successful)
	sortByCompletionTime(failed)

	// Delete excess successful jobs
	successLimit := cronJob.Spec.SuccessfulJobsHistoryLimit
	if successLimit == 0 {
		successLimit = 3
	}
	for i := 0; i < len(successful)-int(successLimit); i++ {
		logger.V(1).Info("Deleting old successful job", "job", successful[i].Name)
		if err := r.Delete(ctx, &successful[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	// Delete excess failed jobs
	failLimit := cronJob.Spec.FailedJobsHistoryLimit
	if failLimit == 0 {
		failLimit = 1
	}
	for i := 0; i < len(failed)-int(failLimit); i++ {
		logger.V(1).Info("Deleting old failed job", "job", failed[i].Name)
		if err := r.Delete(ctx, &failed[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// parseSchedule parses a cron schedule string
func (r *PrisnCronJobReconciler) parseSchedule(schedule string) (cron.Schedule, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return parser.Parse(schedule)
}

// getNextSchedule returns the next scheduled time
func (r *PrisnCronJobReconciler) getNextSchedule(cronJob *prisnv1alpha1.PrisnCronJob, schedule cron.Schedule, now time.Time) *time.Time {
	next := schedule.Next(now)
	return &next
}

// getMissedRun calculates if there's a missed run that should be executed
func (r *PrisnCronJobReconciler) getMissedRun(cronJob *prisnv1alpha1.PrisnCronJob, schedule cron.Schedule, now time.Time) (*time.Time, time.Time, error) {
	var earliestTime time.Time

	if cronJob.Status.LastScheduleTime != nil {
		earliestTime = cronJob.Status.LastScheduleTime.Time
	} else {
		earliestTime = cronJob.ObjectMeta.CreationTimestamp.Time
	}

	if cronJob.Spec.StartingDeadlineSeconds != nil {
		deadline := now.Add(-time.Duration(*cronJob.Spec.StartingDeadlineSeconds) * time.Second)
		if deadline.After(earliestTime) {
			earliestTime = deadline
		}
	}

	if earliestTime.After(now) {
		return nil, schedule.Next(now), nil
	}

	// Find most recent missed run
	var missedRun *time.Time
	for t := schedule.Next(earliestTime); !t.After(now); t = schedule.Next(t) {
		missedRun = &t
		// Prevent infinite loop - if we've gone more than 100 iterations, something is wrong
		if t.Sub(earliestTime) > 100*24*time.Hour {
			return nil, schedule.Next(now), fmt.Errorf("too many missed runs")
		}
	}

	return missedRun, schedule.Next(now), nil
}

// createJobFromTemplate creates a new PrisnJob from the CronJob template
func (r *PrisnCronJobReconciler) createJobFromTemplate(ctx context.Context, cronJob *prisnv1alpha1.PrisnCronJob, scheduledTime time.Time) (*prisnv1alpha1.PrisnJob, error) {
	name := fmt.Sprintf("%s-%d", cronJob.Name, scheduledTime.Unix())

	job := &prisnv1alpha1.PrisnJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cronJob.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       cronJob.Name,
				"app.kubernetes.io/managed-by": "prisn-operator",
				"prisn.io/cronjob":             cronJob.Name,
			},
			Annotations: map[string]string{
				"prisn.io/scheduled-at": scheduledTime.Format(time.RFC3339),
			},
		},
		Spec: cronJob.Spec.JobTemplate,
	}

	if err := controllerutil.SetControllerReference(cronJob, job, r.Scheme); err != nil {
		return nil, err
	}

	if err := r.Create(ctx, job); err != nil {
		return nil, err
	}

	return job, nil
}

// now returns the current time (mockable for tests)
func (r *PrisnCronJobReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager sets up the controller with the Manager
func (r *PrisnCronJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&prisnv1alpha1.PrisnCronJob{}).
		Owns(&prisnv1alpha1.PrisnJob{}).
		Complete(r)
}
