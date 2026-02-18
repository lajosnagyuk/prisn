package k8s

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AppBuilder helps construct PrisnApp resources.
type AppBuilder struct {
	name      string
	namespace string
	spec      map[string]interface{}
}

// NewAppBuilder creates a new AppBuilder with the given name.
func NewAppBuilder(name string) *AppBuilder {
	return &AppBuilder{
		name: name,
		spec: make(map[string]interface{}),
	}
}

// Namespace sets the namespace.
func (b *AppBuilder) Namespace(ns string) *AppBuilder {
	b.namespace = ns
	return b
}

// InlineSource sets inline code as the source.
func (b *AppBuilder) InlineSource(code string) *AppBuilder {
	b.spec["source"] = map[string]interface{}{
		"inline": code,
	}
	return b
}

// ConfigMapSource sets a ConfigMap as the source.
func (b *AppBuilder) ConfigMapSource(name, entrypoint string) *AppBuilder {
	source := map[string]interface{}{
		"configMapRef": map[string]interface{}{
			"name": name,
		},
	}
	if entrypoint != "" {
		source["entrypoint"] = entrypoint
	}
	b.spec["source"] = source
	return b
}

// GitSource sets a git repository as the source.
func (b *AppBuilder) GitSource(url, ref, path, secretName string) *AppBuilder {
	git := map[string]interface{}{
		"url": url,
	}
	if ref != "" {
		git["ref"] = ref
	}
	if path != "" {
		git["path"] = path
	}
	if secretName != "" {
		git["secretRef"] = map[string]interface{}{
			"name": secretName,
		}
	}
	b.spec["source"] = map[string]interface{}{
		"git": git,
	}
	return b
}

// Runtime sets the execution runtime.
func (b *AppBuilder) Runtime(runtime string) *AppBuilder {
	if runtime != "" {
		b.spec["runtime"] = runtime
	}
	return b
}

// Replicas sets the number of replicas.
func (b *AppBuilder) Replicas(replicas int32) *AppBuilder {
	b.spec["replicas"] = int64(replicas)
	return b
}

// Port sets the port the app listens on.
func (b *AppBuilder) Port(port int32) *AppBuilder {
	if port > 0 {
		b.spec["port"] = int64(port)
	}
	return b
}

// Args sets command-line arguments.
func (b *AppBuilder) Args(args []string) *AppBuilder {
	if len(args) > 0 {
		b.spec["args"] = toInterfaceSlice(args)
	}
	return b
}

// Env sets environment variables.
func (b *AppBuilder) Env(env map[string]string) *AppBuilder {
	if len(env) > 0 {
		envList := make([]interface{}, 0, len(env))
		for k, v := range env {
			envList = append(envList, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
		b.spec["env"] = envList
	}
	return b
}

// Resources sets resource limits.
func (b *AppBuilder) Resources(cpu, memory string) *AppBuilder {
	if cpu != "" || memory != "" {
		resources := make(map[string]interface{})
		if cpu != "" {
			resources["cpu"] = cpu
		}
		if memory != "" {
			resources["memory"] = memory
		}
		b.spec["resources"] = resources
	}
	return b
}

// Storage adds persistent storage configuration.
func (b *AppBuilder) Storage(name, mountPath, size, storageClass string) *AppBuilder {
	storage := map[string]interface{}{
		"name":      name,
		"mountPath": mountPath,
	}
	if size != "" {
		storage["size"] = size
	}
	if storageClass != "" {
		storage["storageClassName"] = storageClass
	}

	existing, ok := b.spec["storage"].([]interface{})
	if !ok {
		existing = []interface{}{}
	}
	b.spec["storage"] = append(existing, storage)
	return b
}

// Ingress configures external access.
func (b *AppBuilder) Ingress(host, path, ingressClass string, tls bool) *AppBuilder {
	if host != "" {
		ingress := map[string]interface{}{
			"enabled": true,
			"host":    host,
		}
		if path != "" {
			ingress["path"] = path
		}
		if ingressClass != "" {
			ingress["ingressClassName"] = ingressClass
		}
		if tls {
			ingress["tls"] = true
		}
		b.spec["ingress"] = ingress
	}
	return b
}

// Build creates the unstructured PrisnApp object.
func (b *AppBuilder) Build() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": APIGroup + "/" + APIVersion,
			"kind":       "PrisnApp",
			"metadata": map[string]interface{}{
				"name": b.name,
			},
			"spec": b.spec,
		},
	}
	if b.namespace != "" {
		obj.SetNamespace(b.namespace)
	}
	return obj
}

// JobBuilder helps construct PrisnJob resources.
type JobBuilder struct {
	name      string
	namespace string
	spec      map[string]interface{}
}

// NewJobBuilder creates a new JobBuilder with the given name.
func NewJobBuilder(name string) *JobBuilder {
	return &JobBuilder{
		name: name,
		spec: make(map[string]interface{}),
	}
}

// Namespace sets the namespace.
func (b *JobBuilder) Namespace(ns string) *JobBuilder {
	b.namespace = ns
	return b
}

// InlineSource sets inline code as the source.
func (b *JobBuilder) InlineSource(code string) *JobBuilder {
	b.spec["source"] = map[string]interface{}{
		"inline": code,
	}
	return b
}

// ConfigMapSource sets a ConfigMap as the source.
func (b *JobBuilder) ConfigMapSource(name, entrypoint string) *JobBuilder {
	source := map[string]interface{}{
		"configMapRef": map[string]interface{}{
			"name": name,
		},
	}
	if entrypoint != "" {
		source["entrypoint"] = entrypoint
	}
	b.spec["source"] = source
	return b
}

// Runtime sets the execution runtime.
func (b *JobBuilder) Runtime(runtime string) *JobBuilder {
	if runtime != "" {
		b.spec["runtime"] = runtime
	}
	return b
}

// Args sets command-line arguments.
func (b *JobBuilder) Args(args []string) *JobBuilder {
	if len(args) > 0 {
		b.spec["args"] = toInterfaceSlice(args)
	}
	return b
}

// Env sets environment variables.
func (b *JobBuilder) Env(env map[string]string) *JobBuilder {
	if len(env) > 0 {
		envList := make([]interface{}, 0, len(env))
		for k, v := range env {
			envList = append(envList, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
		b.spec["env"] = envList
	}
	return b
}

// Resources sets resource limits.
func (b *JobBuilder) Resources(cpu, memory string) *JobBuilder {
	if cpu != "" || memory != "" {
		resources := make(map[string]interface{})
		if cpu != "" {
			resources["cpu"] = cpu
		}
		if memory != "" {
			resources["memory"] = memory
		}
		b.spec["resources"] = resources
	}
	return b
}

// Timeout sets the maximum execution time.
func (b *JobBuilder) Timeout(timeout string) *JobBuilder {
	if timeout != "" {
		b.spec["timeout"] = timeout
	}
	return b
}

// BackoffLimit sets the number of retries.
func (b *JobBuilder) BackoffLimit(limit int32) *JobBuilder {
	b.spec["backoffLimit"] = int64(limit)
	return b
}

// Build creates the unstructured PrisnJob object.
func (b *JobBuilder) Build() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": APIGroup + "/" + APIVersion,
			"kind":       "PrisnJob",
			"metadata": map[string]interface{}{
				"name": b.name,
			},
			"spec": b.spec,
		},
	}
	if b.namespace != "" {
		obj.SetNamespace(b.namespace)
	}
	return obj
}

// CronJobBuilder helps construct PrisnCronJob resources.
type CronJobBuilder struct {
	name        string
	namespace   string
	spec        map[string]interface{}
	jobTemplate map[string]interface{}
}

// NewCronJobBuilder creates a new CronJobBuilder with the given name.
func NewCronJobBuilder(name string) *CronJobBuilder {
	return &CronJobBuilder{
		name:        name,
		spec:        make(map[string]interface{}),
		jobTemplate: make(map[string]interface{}),
	}
}

// Namespace sets the namespace.
func (b *CronJobBuilder) Namespace(ns string) *CronJobBuilder {
	b.namespace = ns
	return b
}

// Schedule sets the cron schedule.
func (b *CronJobBuilder) Schedule(schedule string) *CronJobBuilder {
	b.spec["schedule"] = schedule
	return b
}

// InlineSource sets inline code as the source.
func (b *CronJobBuilder) InlineSource(code string) *CronJobBuilder {
	b.jobTemplate["source"] = map[string]interface{}{
		"inline": code,
	}
	return b
}

// ConfigMapSource sets a ConfigMap as the source.
func (b *CronJobBuilder) ConfigMapSource(name, entrypoint string) *CronJobBuilder {
	source := map[string]interface{}{
		"configMapRef": map[string]interface{}{
			"name": name,
		},
	}
	if entrypoint != "" {
		source["entrypoint"] = entrypoint
	}
	b.jobTemplate["source"] = source
	return b
}

// Runtime sets the execution runtime.
func (b *CronJobBuilder) Runtime(runtime string) *CronJobBuilder {
	if runtime != "" {
		b.jobTemplate["runtime"] = runtime
	}
	return b
}

// Args sets command-line arguments.
func (b *CronJobBuilder) Args(args []string) *CronJobBuilder {
	if len(args) > 0 {
		b.jobTemplate["args"] = toInterfaceSlice(args)
	}
	return b
}

// Env sets environment variables.
func (b *CronJobBuilder) Env(env map[string]string) *CronJobBuilder {
	if len(env) > 0 {
		envList := make([]interface{}, 0, len(env))
		for k, v := range env {
			envList = append(envList, map[string]interface{}{
				"name":  k,
				"value": v,
			})
		}
		b.jobTemplate["env"] = envList
	}
	return b
}

// ConcurrencyPolicy sets how to handle concurrent executions.
func (b *CronJobBuilder) ConcurrencyPolicy(policy string) *CronJobBuilder {
	if policy != "" {
		b.spec["concurrencyPolicy"] = policy
	}
	return b
}

// Suspend sets whether the cron job is suspended.
func (b *CronJobBuilder) Suspend(suspend bool) *CronJobBuilder {
	b.spec["suspend"] = suspend
	return b
}

// Build creates the unstructured PrisnCronJob object.
func (b *CronJobBuilder) Build() *unstructured.Unstructured {
	b.spec["jobTemplate"] = b.jobTemplate

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": APIGroup + "/" + APIVersion,
			"kind":       "PrisnCronJob",
			"metadata": map[string]interface{}{
				"name": b.name,
			},
			"spec": b.spec,
		},
	}
	if b.namespace != "" {
		obj.SetNamespace(b.namespace)
	}
	return obj
}

// Helper function
func toInterfaceSlice(strs []string) []interface{} {
	result := make([]interface{}, len(strs))
	for i, s := range strs {
		result[i] = s
	}
	return result
}
