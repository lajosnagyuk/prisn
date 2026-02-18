package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/k8s"
	"github.com/lajosnagyuk/prisn/pkg/log"
)

// KubeDeployment holds parameters for a Kubernetes deployment.
type KubeDeployment struct {
	Name      string
	Namespace string
	Source    string
	Runtime   string
	Port      int32
	Replicas  int32
	Env       map[string]string
	Schedule  string // If set, creates a CronJob instead of an App
}

// DeployToKubernetes deploys an application to Kubernetes by creating CRDs.
func DeployToKubernetes(ctx context.Context, resolved *prisnctx.Resolved, deploy KubeDeployment) error {
	// Create K8s client
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	// Use resolved namespace if deploy doesn't specify one
	if deploy.Namespace == "" {
		deploy.Namespace = resolved.Namespace
	}
	client = client.WithNamespace(deploy.Namespace)

	// Read the source file/directory
	sourceInfo, err := os.Stat(deploy.Source)
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	// Get the code content
	var code string
	var entrypoint string

	if sourceInfo.IsDir() {
		// For directories, find the entrypoint and read it
		entrypoint, code, err = readDirectory(deploy.Source)
		if err != nil {
			return fmt.Errorf("failed to read directory: %w", err)
		}
	} else {
		// For single files, read directly
		content, err := os.ReadFile(deploy.Source)
		if err != nil {
			return fmt.Errorf("failed to read source: %w", err)
		}
		code = string(content)
		entrypoint = filepath.Base(deploy.Source)
	}

	// Detect runtime if not specified
	if deploy.Runtime == "" {
		deploy.Runtime = detectRuntime(entrypoint)
	}

	// Create ConfigMap with the code
	configMapName := deploy.Name + "-code"
	if err := createOrUpdateConfigMap(ctx, resolved, deploy.Namespace, configMapName, entrypoint, code); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}
	log.Done("created ConfigMap %s", configMapName)

	// Build and create the CRD
	if deploy.Schedule != "" {
		// Create CronJob
		cronJob := k8s.NewCronJobBuilder(deploy.Name).
			Namespace(deploy.Namespace).
			Schedule(deploy.Schedule).
			ConfigMapSource(configMapName, entrypoint).
			Runtime(deploy.Runtime).
			Env(deploy.Env).
			Build()

		if _, err := client.CreateCronJob(ctx, cronJob); err != nil {
			// Try update if it exists
			if existing, getErr := client.GetCronJob(ctx, deploy.Name); getErr == nil {
				// Update spec
				existing.Object["spec"] = cronJob.Object["spec"]
				if _, err := client.UpdateCronJob(ctx, existing); err != nil {
					return fmt.Errorf("failed to update PrisnCronJob: %w", err)
				}
				log.Done("updated PrisnCronJob %s", deploy.Name)
				return nil
			}
			return fmt.Errorf("failed to create PrisnCronJob: %w", err)
		}
		log.Done("created PrisnCronJob %s (schedule: %s)", deploy.Name, deploy.Schedule)
	} else {
		// Create App
		builder := k8s.NewAppBuilder(deploy.Name).
			Namespace(deploy.Namespace).
			ConfigMapSource(configMapName, entrypoint).
			Runtime(deploy.Runtime).
			Replicas(deploy.Replicas).
			Env(deploy.Env)

		if deploy.Port > 0 {
			builder.Port(deploy.Port)
		}

		app := builder.Build()

		if _, err := client.CreateApp(ctx, app); err != nil {
			// Try update if it exists
			if existing, getErr := client.GetApp(ctx, deploy.Name); getErr == nil {
				// Update spec
				existing.Object["spec"] = app.Object["spec"]
				if _, err := client.UpdateApp(ctx, existing); err != nil {
					return fmt.Errorf("failed to update PrisnApp: %w", err)
				}
				log.Done("updated PrisnApp %s", deploy.Name)
				return nil
			}
			return fmt.Errorf("failed to create PrisnApp: %w", err)
		}
		log.Done("created PrisnApp %s", deploy.Name)
	}

	return nil
}

// ScaleInKubernetes scales a PrisnApp in Kubernetes.
func ScaleInKubernetes(ctx context.Context, resolved *prisnctx.Resolved, name string, replicas int32) error {
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	if err := client.ScaleApp(ctx, name, replicas); err != nil {
		return fmt.Errorf("failed to scale %s to %d: %w", name, replicas, err)
	}

	log.Done("scaled %s to %d replicas", name, replicas)
	return nil
}

// ListAppsInKubernetes lists all PrisnApps in the namespace.
func ListAppsInKubernetes(ctx context.Context, resolved *prisnctx.Resolved) ([]k8s.AppStatus, error) {
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s client: %w", err)
	}

	list, err := client.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list PrisnApps: %w", err)
	}

	result := make([]k8s.AppStatus, 0, len(list.Items))
	for _, item := range list.Items {
		result = append(result, k8s.ExtractAppStatus(&item))
	}
	return result, nil
}

// GetAppInKubernetes gets a single PrisnApp.
func GetAppInKubernetes(ctx context.Context, resolved *prisnctx.Resolved, name string) (*unstructured.Unstructured, error) {
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s client: %w", err)
	}

	return client.GetApp(ctx, name)
}

// DeleteInKubernetes deletes a PrisnApp or PrisnCronJob.
func DeleteInKubernetes(ctx context.Context, resolved *prisnctx.Resolved, name string) error {
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	// Try to delete as App first
	if err := client.DeleteApp(ctx, name); err == nil {
		log.Done("deleted PrisnApp %s", name)
		return nil
	}

	// Try CronJob
	if err := client.DeleteCronJob(ctx, name); err == nil {
		log.Done("deleted PrisnCronJob %s", name)
		return nil
	}

	// Try Job
	if err := client.DeleteJob(ctx, name); err == nil {
		log.Done("deleted PrisnJob %s", name)
		return nil
	}

	return fmt.Errorf("resource %s not found", name)
}

// createOrUpdateConfigMap creates or updates a ConfigMap with the code.
func createOrUpdateConfigMap(ctx context.Context, resolved *prisnctx.Resolved, namespace, name, entrypoint, code string) error {
	// Use default loading rules which properly handle:
	// - KUBECONFIG env var (including multiple paths separated by :)
	// - Default ~/.kube/config fallback
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	if resolved.KubeContext != "" {
		configOverrides.CurrentContext = resolved.KubeContext
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		configOverrides,
	).ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "prisn",
			},
		},
		Data: map[string]string{
			entrypoint: code,
		},
	}

	// Try to create, update if exists
	_, err = clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		// Try update
		_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create/update ConfigMap: %w", err)
		}
	}

	return nil
}

// readDirectory reads a directory and returns the entrypoint file and its content.
func readDirectory(dir string) (entrypoint string, code string, err error) {
	// Common entrypoint names
	candidates := []string{
		"main.py", "main.sh", "index.js", "app.py", "server.py",
		"run.py", "run.sh", "start.py", "start.sh",
	}

	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		if content, err := os.ReadFile(path); err == nil {
			return candidate, string(content), nil
		}
	}

	// Fall back to first script file
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".py") || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".sh") {
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			return name, string(content), nil
		}
	}

	return "", "", fmt.Errorf("no entrypoint found in %s", dir)
}

// detectRuntime infers the runtime from the entrypoint filename.
func detectRuntime(entrypoint string) string {
	switch {
	case strings.HasSuffix(entrypoint, ".py"):
		return "python3"
	case strings.HasSuffix(entrypoint, ".js"):
		return "node"
	case strings.HasSuffix(entrypoint, ".sh"):
		return "bash"
	default:
		return "python3"
	}
}
