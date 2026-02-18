// Package controllers contains the Kubernetes controllers for prisn resources
package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	prisnv1alpha1 "github.com/lajosnagyuk/prisn/operator/api/v1alpha1"
)

const (
	// PrisnAppFinalizer is the finalizer for PrisnApp resources
	PrisnAppFinalizer = "prisn.io/app-finalizer"

	// DefaultBaseImage is the default container image for running scripts
	DefaultBaseImage = "python:3.11-slim"

	// RequeueDelay is the default requeue delay for reconciliation
	RequeueDelay = 30 * time.Second
)

// PrisnAppReconciler reconciles a PrisnApp object
type PrisnAppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=prisn.io,resources=prisnapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=prisn.io,resources=prisnapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=prisn.io,resources=prisnapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the main reconciliation loop for PrisnApp
func (r *PrisnAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling PrisnApp", "namespace", req.Namespace, "name", req.Name)

	// Fetch the PrisnApp instance
	app := &prisnv1alpha1.PrisnApp{}
	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("PrisnApp not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get PrisnApp")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !app.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, app)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(app, PrisnAppFinalizer) {
		controllerutil.AddFinalizer(app, PrisnAppFinalizer)
		if err := r.Update(ctx, app); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if suspended
	if app.Spec.Suspend {
		return r.reconcileSuspended(ctx, app)
	}

	// Reconcile the source (ConfigMap for inline code)
	if err := r.reconcileSource(ctx, app); err != nil {
		r.setPhase(ctx, app, "Failed", fmt.Sprintf("Source error: %v", err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	// Reconcile persistent storage (PVCs)
	if err := r.reconcileStorage(ctx, app); err != nil {
		r.setPhase(ctx, app, "Failed", fmt.Sprintf("Storage error: %v", err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	// Reconcile the Deployment
	if err := r.reconcileDeployment(ctx, app); err != nil {
		r.setPhase(ctx, app, "Failed", fmt.Sprintf("Deployment error: %v", err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	// Reconcile the Service (if port is specified)
	if app.Spec.Port > 0 {
		if err := r.reconcileService(ctx, app); err != nil {
			r.setPhase(ctx, app, "Failed", fmt.Sprintf("Service error: %v", err))
			return ctrl.Result{RequeueAfter: RequeueDelay}, err
		}
	}

	// Reconcile the Ingress (if configured)
	if err := r.reconcileIngress(ctx, app); err != nil {
		r.setPhase(ctx, app, "Failed", fmt.Sprintf("Ingress error: %v", err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	// Update status from Deployment
	if err := r.updateStatusFromDeployment(ctx, app); err != nil {
		logger.Error(err, "Failed to update status")
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	return ctrl.Result{RequeueAfter: RequeueDelay}, nil
}

// handleDeletion handles the deletion of a PrisnApp
func (r *PrisnAppReconciler) handleDeletion(ctx context.Context, app *prisnv1alpha1.PrisnApp) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of PrisnApp", "name", app.Name)

	// Clean up owned resources (handled by ownerReferences, but emit event)
	r.Recorder.Event(app, corev1.EventTypeNormal, "Deleting", "Cleaning up resources")

	// Remove finalizer
	controllerutil.RemoveFinalizer(app, PrisnAppFinalizer)
	if err := r.Update(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileSuspended handles suspended apps by scaling to 0
func (r *PrisnAppReconciler) reconcileSuspended(ctx context.Context, app *prisnv1alpha1.PrisnApp) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("App is suspended, scaling to 0", "name", app.Name)

	// Get the deployment
	deploy := &appsv1.Deployment{}
	deployName := r.deploymentName(app)
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: deployName}, deploy); err != nil {
		if apierrors.IsNotFound(err) {
			// No deployment to scale down
			r.setPhase(ctx, app, "Suspended", "App is suspended")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Scale to 0
	zero := int32(0)
	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != zero {
		deploy.Spec.Replicas = &zero
		if err := r.Update(ctx, deploy); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(app, corev1.EventTypeNormal, "Suspended", "Scaled to 0 replicas")
	}

	r.setPhase(ctx, app, "Suspended", "App is suspended")
	return ctrl.Result{}, nil
}

// reconcileSource ensures the source code is available
func (r *PrisnAppReconciler) reconcileSource(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	if app.Spec.Source.IsInlineSource() {
		return r.reconcileInlineSource(ctx, app)
	}
	// Git and ConfigMap sources are mounted directly in the pod
	return nil
}

// reconcileInlineSource creates a ConfigMap for inline code
func (r *PrisnAppReconciler) reconcileInlineSource(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	cmName := r.sourceConfigMapName(app)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: app.Namespace,
			Labels:    r.labels(app),
		},
		Data: map[string]string{
			"script": app.Spec.Source.Inline,
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(app, cm, r.Scheme); err != nil {
		return err
	}

	// Create or update
	existing := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: cmName}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			r.Recorder.Event(app, corev1.EventTypeNormal, "CreatingConfigMap", "Creating source ConfigMap")
			return r.Create(ctx, cm)
		}
		return err
	}

	// Update if changed
	if existing.Data["script"] != cm.Data["script"] {
		existing.Data = cm.Data
		r.Recorder.Event(app, corev1.EventTypeNormal, "UpdatingConfigMap", "Updating source ConfigMap")
		return r.Update(ctx, existing)
	}

	return nil
}

// reconcileStorage creates PVCs for persistent storage
func (r *PrisnAppReconciler) reconcileStorage(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	for _, storage := range app.Spec.Storage {
		// Skip if using existing claim
		if storage.ExistingClaim != "" {
			continue
		}

		pvcName := r.storagePVCName(app, storage.Name)
		pvc := r.buildPVC(app, storage, pvcName)

		// Set owner reference
		if err := controllerutil.SetControllerReference(app, pvc, r.Scheme); err != nil {
			return err
		}

		// Check if exists
		existing := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: pvcName}, existing); err != nil {
			if apierrors.IsNotFound(err) {
				r.Recorder.Event(app, corev1.EventTypeNormal, "CreatingPVC",
					fmt.Sprintf("Creating PVC %s for storage %s", pvcName, storage.Name))
				if err := r.Create(ctx, pvc); err != nil {
					return err
				}
				continue
			}
			return err
		}
		// PVC exists, no update needed (PVCs are immutable)
	}
	return nil
}

// buildPVC constructs a PersistentVolumeClaim for storage
func (r *PrisnAppReconciler) buildPVC(app *prisnv1alpha1.PrisnApp, storage prisnv1alpha1.StorageSpec, name string) *corev1.PersistentVolumeClaim {
	// Determine access mode - default to RWX for multi-replica support
	accessMode := corev1.ReadWriteMany
	switch storage.AccessMode {
	case "ReadWriteOnce":
		accessMode = corev1.ReadWriteOnce
	case "ReadOnlyMany":
		accessMode = corev1.ReadOnlyMany
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: app.Namespace,
			Labels:    r.labels(app),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(storage.Size),
				},
			},
		},
	}

	// Set storage class if specified
	if storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = &storage.StorageClassName
	}

	return pvc
}

// storagePVCName returns the PVC name for a storage volume
func (r *PrisnAppReconciler) storagePVCName(app *prisnv1alpha1.PrisnApp, storageName string) string {
	return fmt.Sprintf("%s-storage-%s", app.Name, storageName)
}

// reconcileDeployment creates or updates the Deployment
func (r *PrisnAppReconciler) reconcileDeployment(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	deploy := r.buildDeployment(app)

	// Set owner reference
	if err := controllerutil.SetControllerReference(app, deploy, r.Scheme); err != nil {
		return err
	}

	// Check if exists
	existing := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: deploy.Namespace, Name: deploy.Name}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			r.Recorder.Event(app, corev1.EventTypeNormal, "CreatingDeployment", "Creating Deployment")
			r.setPhase(ctx, app, "Building", "Creating deployment")
			return r.Create(ctx, deploy)
		}
		return err
	}

	// Update deployment spec (preserve replicas if changed externally)
	existing.Spec.Template = deploy.Spec.Template
	existing.Spec.Selector = deploy.Spec.Selector
	if !app.Spec.Suspend {
		existing.Spec.Replicas = deploy.Spec.Replicas
	}

	return r.Update(ctx, existing)
}

// buildDeployment constructs a Deployment for the PrisnApp
func (r *PrisnAppReconciler) buildDeployment(app *prisnv1alpha1.PrisnApp) *appsv1.Deployment {
	labels := r.labels(app)
	replicas := app.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	// Determine the base image and command based on runtime
	image, command := r.runtimeConfig(app.Spec.Runtime)

	// Build the entrypoint
	entrypoint := app.Spec.Source.Entrypoint
	if entrypoint == "" {
		entrypoint = "script"
	}

	// Build args
	args := []string{"/scripts/" + entrypoint}
	args = append(args, app.Spec.Args...)

	// Build container
	container := corev1.Container{
		Name:            "app",
		Image:           image,
		Command:         command,
		Args:            args,
		Env:             app.Spec.Env,
		EnvFrom:         app.Spec.EnvFrom,
		Resources:       app.Spec.Resources.GetResourceRequirements(),
		ImagePullPolicy: corev1.PullIfNotPresent,
	}

	// Add port if specified
	if app.Spec.Port > 0 {
		container.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: app.Spec.Port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	// Add health check if configured
	if app.Spec.HealthCheck != nil && app.Spec.Port > 0 {
		path := app.Spec.HealthCheck.Path
		if path == "" {
			path = "/health"
		}

		// Startup probe: runs during startup, allows container to start up
		// Once it succeeds, liveness/readiness probes take over
		// This enables fast detection without killing slow-starting containers
		container.StartupProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: path,
					Port: intstr.FromInt32(app.Spec.Port),
				},
			},
			PeriodSeconds:    1, // Check every second during startup
			FailureThreshold: 30, // Allow up to 30s for startup
			SuccessThreshold: 1,
		}

		// Liveness probe: restarts container if unhealthy after startup
		container.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: path,
					Port: intstr.FromInt32(app.Spec.Port),
				},
			},
			InitialDelaySeconds: 0, // No delay - startup probe handles initial wait
			PeriodSeconds:       app.Spec.HealthCheck.PeriodSeconds,
			FailureThreshold:    app.Spec.HealthCheck.FailureThreshold,
		}

		// Readiness probe: controls traffic routing
		container.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: path,
					Port: intstr.FromInt32(app.Spec.Port),
				},
			},
			InitialDelaySeconds: 0, // No delay - startup probe handles initial wait
			PeriodSeconds:       app.Spec.HealthCheck.PeriodSeconds,
			FailureThreshold:    1, // Remove from service immediately if unhealthy
			SuccessThreshold:    1,
		}
	}

	// Build volumes and mounts
	volumes, mounts := r.buildVolumes(app)
	container.VolumeMounts = mounts

	// Build init containers (for git source)
	initContainers := r.buildInitContainers(app)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.deploymentName(app),
			Namespace: app.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"prisn.io/source-hash": r.sourceHash(app),
					},
				},
				Spec: corev1.PodSpec{
					InitContainers:     initContainers,
					Containers:         []corev1.Container{container},
					Volumes:            volumes,
					ServiceAccountName: app.Spec.ServiceAccountName,
					ImagePullSecrets:   app.Spec.ImagePullSecrets,
					NodeSelector:       app.Spec.NodeSelector,
					Tolerations:        app.Spec.Tolerations,
				},
			},
		},
	}

	return deploy
}

// buildInitContainers creates init containers for the deployment
func (r *PrisnAppReconciler) buildInitContainers(app *prisnv1alpha1.PrisnApp) []corev1.Container {
	var initContainers []corev1.Container

	if app.Spec.Source.IsGitSource() {
		git := app.Spec.Source.Git
		ref := git.Ref
		if ref == "" {
			ref = "main"
		}

		// Build git clone command
		// Clone to temp, then move to scripts dir (handles non-empty dir issues)
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
			// Update clone command to use credentials
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

// buildVolumes constructs volumes and mounts for the source code
func (r *PrisnAppReconciler) buildVolumes(app *prisnv1alpha1.PrisnApp) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount

	source := app.Spec.Source

	if source.IsInlineSource() {
		volumes = append(volumes, corev1.Volume{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: r.sourceConfigMapName(app),
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
		// Git source uses init container (handled separately)
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

	// Add persistent storage volumes
	for _, storage := range app.Spec.Storage {
		volName := "storage-" + storage.Name

		// Determine PVC name (existing or generated)
		pvcName := storage.ExistingClaim
		if pvcName == "" {
			pvcName = r.storagePVCName(app, storage.Name)
		}

		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
					ReadOnly:  storage.ReadOnly,
				},
			},
		})

		mount := corev1.VolumeMount{
			Name:      volName,
			MountPath: storage.MountPath,
			ReadOnly:  storage.ReadOnly,
		}
		if storage.SubPath != "" {
			mount.SubPath = storage.SubPath
		}
		mounts = append(mounts, mount)
	}

	return volumes, mounts
}

// reconcileService creates or updates the Service
func (r *PrisnAppReconciler) reconcileService(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	svc := r.buildService(app)

	// Set owner reference
	if err := controllerutil.SetControllerReference(app, svc, r.Scheme); err != nil {
		return err
	}

	// Check if exists
	existing := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			r.Recorder.Event(app, corev1.EventTypeNormal, "CreatingService", "Creating Service")
			return r.Create(ctx, svc)
		}
		return err
	}

	// Update service spec (preserve ClusterIP)
	svc.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec = svc.Spec
	return r.Update(ctx, existing)
}

// buildService constructs a Service for the PrisnApp
func (r *PrisnAppReconciler) buildService(app *prisnv1alpha1.PrisnApp) *corev1.Service {
	labels := r.labels(app)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.serviceName(app),
			Namespace: app.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       app.Spec.Port,
					TargetPort: intstr.FromInt32(app.Spec.Port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// reconcileIngress creates, updates, or deletes the Ingress based on spec
func (r *PrisnAppReconciler) reconcileIngress(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	ingressName := r.ingressName(app)

	// If ingress is not configured or disabled, ensure it doesn't exist
	if app.Spec.Ingress == nil || !app.Spec.Ingress.Enabled {
		existing := &networkingv1.Ingress{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: ingressName}, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Doesn't exist, nothing to do
			}
			return err
		}
		// Delete existing ingress
		r.Recorder.Event(app, corev1.EventTypeNormal, "DeletingIngress", "Ingress disabled, removing")
		return r.Delete(ctx, existing)
	}

	// Build the desired Ingress
	ingress := r.buildIngress(app)

	// Set owner reference
	if err := controllerutil.SetControllerReference(app, ingress, r.Scheme); err != nil {
		return err
	}

	// Check if exists
	existing := &networkingv1.Ingress{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}, existing); err != nil {
		if apierrors.IsNotFound(err) {
			r.Recorder.Event(app, corev1.EventTypeNormal, "CreatingIngress", fmt.Sprintf("Creating Ingress for host %s", app.Spec.Ingress.Host))
			return r.Create(ctx, ingress)
		}
		return err
	}

	// Update existing
	existing.Spec = ingress.Spec
	existing.Annotations = ingress.Annotations
	return r.Update(ctx, existing)
}

// buildIngress constructs an Ingress for the PrisnApp
func (r *PrisnAppReconciler) buildIngress(app *prisnv1alpha1.PrisnApp) *networkingv1.Ingress {
	labels := r.labels(app)
	ingressSpec := app.Spec.Ingress

	// Determine path and pathType
	path := ingressSpec.Path
	if path == "" {
		path = "/"
	}
	pathType := networkingv1.PathTypePrefix
	if ingressSpec.PathType == "Exact" {
		pathType = networkingv1.PathTypeExact
	} else if ingressSpec.PathType == "ImplementationSpecific" {
		pathType = networkingv1.PathTypeImplementationSpecific
	}

	// Build annotations
	annotations := make(map[string]string)
	for k, v := range ingressSpec.Annotations {
		annotations[k] = v
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.ingressName(app),
			Namespace:   app.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: ingressSpec.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: r.serviceName(app),
											Port: networkingv1.ServiceBackendPort{
												Number: app.Spec.Port,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set ingress class if specified
	if ingressSpec.IngressClassName != "" {
		ingress.Spec.IngressClassName = &ingressSpec.IngressClassName
	}

	// Add TLS if enabled
	if ingressSpec.TLS && ingressSpec.Host != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{ingressSpec.Host},
				SecretName: app.Name + "-tls",
			},
		}
	}

	return ingress
}

func (r *PrisnAppReconciler) ingressName(app *prisnv1alpha1.PrisnApp) string {
	return app.Name + "-ingress"
}

// updateStatusFromDeployment updates the PrisnApp status based on the Deployment
func (r *PrisnAppReconciler) updateStatusFromDeployment(ctx context.Context, app *prisnv1alpha1.PrisnApp) error {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: r.deploymentName(app)}, deploy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update replica counts
	app.Status.Replicas = deploy.Status.Replicas
	app.Status.ReadyReplicas = deploy.Status.ReadyReplicas
	app.Status.DeploymentName = deploy.Name
	app.Status.ObservedGeneration = app.Generation

	if app.Spec.Port > 0 {
		app.Status.ServiceName = r.serviceName(app)
	}

	// Determine phase
	if deploy.Status.ReadyReplicas == deploy.Status.Replicas && deploy.Status.Replicas > 0 {
		r.setPhase(ctx, app, "Running", "All replicas ready")
	} else if deploy.Status.Replicas > 0 {
		r.setPhase(ctx, app, "Running", fmt.Sprintf("%d/%d replicas ready", deploy.Status.ReadyReplicas, deploy.Status.Replicas))
	} else {
		r.setPhase(ctx, app, "Pending", "Waiting for replicas")
	}

	// Update conditions
	r.updateConditions(app, deploy)

	// Update status
	return r.Status().Update(ctx, app)
}

// updateConditions updates the conditions based on deployment state
func (r *PrisnAppReconciler) updateConditions(app *prisnv1alpha1.PrisnApp, deploy *appsv1.Deployment) {
	now := metav1.Now()

	// Ready condition
	readyCondition := metav1.Condition{
		Type:               prisnv1alpha1.ConditionTypeReady,
		LastTransitionTime: now,
	}
	if deploy.Status.ReadyReplicas == deploy.Status.Replicas && deploy.Status.Replicas > 0 {
		readyCondition.Status = metav1.ConditionTrue
		readyCondition.Reason = "AllReplicasReady"
		readyCondition.Message = "All replicas are ready"
	} else {
		readyCondition.Status = metav1.ConditionFalse
		readyCondition.Reason = "ReplicasNotReady"
		readyCondition.Message = fmt.Sprintf("%d/%d replicas ready", deploy.Status.ReadyReplicas, deploy.Status.Replicas)
	}
	setCondition(&app.Status.Conditions, readyCondition)

	// Progressing condition (from deployment)
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			progressingCondition := metav1.Condition{
				Type:               prisnv1alpha1.ConditionTypeProgressing,
				Status:             metav1.ConditionStatus(cond.Status),
				LastTransitionTime: now,
				Reason:             cond.Reason,
				Message:            cond.Message,
			}
			setCondition(&app.Status.Conditions, progressingCondition)
		}
	}
}

// setPhase updates the phase and message
func (r *PrisnAppReconciler) setPhase(ctx context.Context, app *prisnv1alpha1.PrisnApp, phase, message string) {
	if app.Status.Phase != phase {
		app.Status.LastTransitionTime = metav1.Now()
		r.Recorder.Event(app, corev1.EventTypeNormal, "PhaseChange", fmt.Sprintf("Phase: %s -> %s", app.Status.Phase, phase))
	}
	app.Status.Phase = phase
	app.Status.Message = message
}

// Helper functions
func (r *PrisnAppReconciler) labels(app *prisnv1alpha1.PrisnApp) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       app.Name,
		"app.kubernetes.io/managed-by": "prisn-operator",
		"app.kubernetes.io/instance":   app.Name,
		"prisn.io/app":                 app.Name,
	}
}

func (r *PrisnAppReconciler) deploymentName(app *prisnv1alpha1.PrisnApp) string {
	return app.Name + "-app"
}

func (r *PrisnAppReconciler) serviceName(app *prisnv1alpha1.PrisnApp) string {
	return app.Name + "-svc"
}

func (r *PrisnAppReconciler) sourceConfigMapName(app *prisnv1alpha1.PrisnApp) string {
	return app.Name + "-source"
}

func (r *PrisnAppReconciler) sourceHash(app *prisnv1alpha1.PrisnApp) string {
	h := sha256.New()
	if app.Spec.Source.IsInlineSource() {
		h.Write([]byte(app.Spec.Source.Inline))
	} else if app.Spec.Source.IsGitSource() {
		h.Write([]byte(app.Spec.Source.Git.URL + app.Spec.Source.Git.Ref))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (r *PrisnAppReconciler) runtimeConfig(runtime string) (image string, command []string) {
	switch runtime {
	case "python", "python3":
		// -u: unbuffered stdout/stderr for faster log output
		// -B: don't write .pyc files (faster startup, cleaner container)
		return "python:3.11-slim", []string{"python3", "-u", "-B"}
	case "node":
		return "node:20-slim", []string{"node"}
	case "bash":
		return "bash:5", []string{"bash"}
	default:
		return "python:3.11-slim", []string{"python3", "-u", "-B"}
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *PrisnAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&prisnv1alpha1.PrisnApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

// Helper to set a condition
func setCondition(conditions *[]metav1.Condition, newCondition metav1.Condition) {
	for i, existing := range *conditions {
		if existing.Type == newCondition.Type {
			if existing.Status != newCondition.Status {
				(*conditions)[i] = newCondition
			} else {
				// Keep existing transition time if status unchanged
				newCondition.LastTransitionTime = existing.LastTransitionTime
				(*conditions)[i] = newCondition
			}
			return
		}
	}
	*conditions = append(*conditions, newCondition)
}

// ptr returns a pointer to the given value
func ptr[T any](v T) *T {
	return &v
}

// mustParseQuantity parses a quantity string, returning a zero quantity on error
func mustParseQuantity(s string) resource.Quantity {
	if s == "" {
		return resource.MustParse("1Gi") // Default size
	}
	return resource.MustParse(s)
}
