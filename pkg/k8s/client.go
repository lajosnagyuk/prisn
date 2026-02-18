// Package k8s provides a Kubernetes client for managing prisn CRDs.
package k8s

import (
	"context"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// APIGroup is the prisn API group
	APIGroup = "prisn.io"
	// APIVersion is the current API version
	APIVersion = "v1alpha1"
)

// GroupVersionResource definitions for prisn CRDs
var (
	PrisnAppGVR = schema.GroupVersionResource{
		Group:    APIGroup,
		Version:  APIVersion,
		Resource: "prisnapps",
	}

	PrisnJobGVR = schema.GroupVersionResource{
		Group:    APIGroup,
		Version:  APIVersion,
		Resource: "prisnjobs",
	}

	PrisnCronJobGVR = schema.GroupVersionResource{
		Group:    APIGroup,
		Version:  APIVersion,
		Resource: "prisncronjobs",
	}
)

// Client provides methods to interact with prisn CRDs in Kubernetes.
type Client struct {
	dynamic   dynamic.Interface
	namespace string
}

// NewClient creates a new Kubernetes client for prisn CRDs.
// kubeContext is the kubeconfig context to use (empty = current context).
// namespace is the default namespace for operations.
func NewClient(kubeContext, namespace string) (*Client, error) {
	config, err := buildConfig(kubeContext)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if namespace == "" {
		namespace = "default"
	}

	return &Client{
		dynamic:   dynamicClient,
		namespace: namespace,
	}, nil
}

// buildConfig creates a rest.Config from kubeconfig.
func buildConfig(kubeContext string) (*rest.Config, error) {
	// Try in-cluster config first
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		config, err := rest.InClusterConfig()
		if err == nil {
			return config, nil
		}
	}

	// Use default loading rules which properly handle:
	// - KUBECONFIG env var (including multiple paths separated by :)
	// - Default ~/.kube/config fallback
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	if kubeContext != "" {
		configOverrides.CurrentContext = kubeContext
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		configOverrides,
	).ClientConfig()
}

// Namespace returns the client's default namespace.
func (c *Client) Namespace() string {
	return c.namespace
}

// WithNamespace returns a new client with a different namespace.
func (c *Client) WithNamespace(ns string) *Client {
	return &Client{
		dynamic:   c.dynamic,
		namespace: ns,
	}
}

// =============================================================================
// PrisnApp Operations
// =============================================================================

// CreateApp creates a PrisnApp resource.
func (c *Client) CreateApp(ctx context.Context, app *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnAppGVR).Namespace(c.namespace).Create(ctx, app, metav1.CreateOptions{})
}

// GetApp retrieves a PrisnApp by name.
func (c *Client) GetApp(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnAppGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
}

// UpdateApp updates an existing PrisnApp.
func (c *Client) UpdateApp(ctx context.Context, app *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnAppGVR).Namespace(c.namespace).Update(ctx, app, metav1.UpdateOptions{})
}

// DeleteApp deletes a PrisnApp by name.
func (c *Client) DeleteApp(ctx context.Context, name string) error {
	return c.dynamic.Resource(PrisnAppGVR).Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ListApps lists all PrisnApps in the namespace.
func (c *Client) ListApps(ctx context.Context) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(PrisnAppGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
}

// ScaleApp updates the replicas for a PrisnApp.
func (c *Client) ScaleApp(ctx context.Context, name string, replicas int32) error {
	app, err := c.GetApp(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get app %q: %w", name, err)
	}

	if err := unstructured.SetNestedField(app.Object, int64(replicas), "spec", "replicas"); err != nil {
		return fmt.Errorf("failed to set replicas: %w", err)
	}

	_, err = c.UpdateApp(ctx, app)
	return err
}

// =============================================================================
// PrisnJob Operations
// =============================================================================

// CreateJob creates a PrisnJob resource.
func (c *Client) CreateJob(ctx context.Context, job *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnJobGVR).Namespace(c.namespace).Create(ctx, job, metav1.CreateOptions{})
}

// GetJob retrieves a PrisnJob by name.
func (c *Client) GetJob(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnJobGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
}

// DeleteJob deletes a PrisnJob by name.
func (c *Client) DeleteJob(ctx context.Context, name string) error {
	return c.dynamic.Resource(PrisnJobGVR).Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ListJobs lists all PrisnJobs in the namespace.
func (c *Client) ListJobs(ctx context.Context) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(PrisnJobGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
}

// =============================================================================
// PrisnCronJob Operations
// =============================================================================

// CreateCronJob creates a PrisnCronJob resource.
func (c *Client) CreateCronJob(ctx context.Context, cronJob *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnCronJobGVR).Namespace(c.namespace).Create(ctx, cronJob, metav1.CreateOptions{})
}

// GetCronJob retrieves a PrisnCronJob by name.
func (c *Client) GetCronJob(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnCronJobGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
}

// UpdateCronJob updates an existing PrisnCronJob.
func (c *Client) UpdateCronJob(ctx context.Context, cronJob *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return c.dynamic.Resource(PrisnCronJobGVR).Namespace(c.namespace).Update(ctx, cronJob, metav1.UpdateOptions{})
}

// DeleteCronJob deletes a PrisnCronJob by name.
func (c *Client) DeleteCronJob(ctx context.Context, name string) error {
	return c.dynamic.Resource(PrisnCronJobGVR).Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ListCronJobs lists all PrisnCronJobs in the namespace.
func (c *Client) ListCronJobs(ctx context.Context) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(PrisnCronJobGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
}

// SuspendCronJob suspends or resumes a PrisnCronJob.
func (c *Client) SuspendCronJob(ctx context.Context, name string, suspend bool) error {
	cronJob, err := c.GetCronJob(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get cronjob %q: %w", name, err)
	}

	if err := unstructured.SetNestedField(cronJob.Object, suspend, "spec", "suspend"); err != nil {
		return fmt.Errorf("failed to set suspend: %w", err)
	}

	_, err = c.UpdateCronJob(ctx, cronJob)
	return err
}
