package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

type manageFlags struct {
	namespace     string
	kubeconfig    string
	deleteVolumes bool
	wait          time.Duration
}

// deleteModel removes a model's managed objects. It is idempotent: missing
// objects are not an error. A running `kubeswap serve` observes the
// deployment deletion and exits on its own.
func deleteModel(client kubernetes.Interface, namespace, model string, deleteVolumes bool, wait time.Duration) error {
	ctx := context.Background()
	sanitized, err := sanitizeModelID(model)
	if err != nil {
		return err
	}

	depName, err := deploymentName(model)
	if err != nil {
		return err
	}
	svcName, err := serviceName(model)
	if err != nil {
		return err
	}

	// Lookups are by exact name, never by the coarse model label, so
	// sibling models that sanitize to the same string are out of reach.
	deleted := false
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, depName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// not loaded
	case err != nil:
		return fmt.Errorf("getting deployment %s: %w", depName, err)
	default:
		if err := verifyOwnership(dep, model); err != nil {
			return err
		}
		if err := client.AppsV1().Deployments(namespace).Delete(ctx, depName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting deployment %s: %w", depName, err)
		}
		log.Printf("deleted deployment %s/%s", namespace, depName)
		deleted = true
	}

	svc, err := client.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// no service to delete
	case err != nil:
		return fmt.Errorf("getting service %s: %w", svcName, err)
	default:
		if err := verifyOwnership(svc, model); err != nil {
			return err
		}
		if err := client.CoreV1().Services(namespace).Delete(ctx, svcName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting service %s: %w", svcName, err)
		}
		log.Printf("deleted service %s/%s", namespace, svcName)
		deleted = true
	}

	if deleteVolumes {
		// Only PVCs kubeswap itself created carry the model label;
		// user-managed claims (e.g. a shared model cache) are untouched.
		selector := labels.Set(managedLabels(sanitized)).String()
		pvcs, err := client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return fmt.Errorf("listing PVCs: %w", err)
		}
		for i := range pvcs.Items {
			if err := verifyOwnership(&pvcs.Items[i], model); err != nil {
				return err
			}
			if err := client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcs.Items[i].Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting PVC %s: %w", pvcs.Items[i].Name, err)
			}
			log.Printf("deleted PVC %s/%s", namespace, pvcs.Items[i].Name)
			deleted = true
		}
	}

	if !deleted {
		log.Printf("nothing to delete for model %q in %s", model, namespace)
		return nil
	}

	if wait > 0 {
		if err := waitForPodsGone(ctx, client, namespace, sanitized, depName, wait); err != nil {
			return err
		}
	}
	return nil
}

// waitForPodsGone polls until no pod objects for the model remain (the
// deployment deletion cascades, but we wait so cmdStop returns after the
// GPU is free). A pod that merely has a deletion timestamp is still
// terminating and may still hold the GPU, so we wait for the objects
// themselves to disappear. The selector includes the deployment name so
// sibling models that sanitize to the same label are not counted.
func waitForPodsGone(ctx context.Context, client kubernetes.Interface, namespace, sanitized, depName string, timeout time.Duration) error {
	selector := labels.Set(podSelectorLabels(sanitized, depName)).String()
	deadline := time.Now().Add(timeout)
	for {
		pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return fmt.Errorf("listing pods: %w", err)
		}
		count := len(pods.Items)
		if count == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %d pods to terminate", timeout, count)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// deleteCmd Implements the delete subcommand: tears down a model's backend Deployment and Service (and, with --delete-volumes, the PVCs kubeswap created).
func deleteCmd(args []string) error {
	var (
		model string
		f     manageFlags
	)
	fs := newFlagSet("delete")
	fs.StringVar(&model, "model", "", "llama-swap model ID (required)")
	addKubeFlags(fs, &f.namespace, &f.kubeconfig)
	fs.BoolVar(&f.deleteVolumes, "delete-volumes", false, "also delete PVCs that kubeswap created for this model")
	fs.DurationVar(&f.wait, "wait", 30*time.Second, "how long to wait for pods to terminate (0 = do not wait)")
	fs.Parse(args)

	if model == "" {
		return errors.New("--model is required")
	}
	client, err := buildClient(f.kubeconfig)
	if err != nil {
		return err
	}
	return deleteModel(client, f.namespace, model, f.deleteVolumes, f.wait)
}

type gcFlags struct {
	namespace     string
	kubeconfig    string
	models        stringList
	config        string
	deleteVolumes bool
}

// gcAllowedModels builds the set of model IDs from --models entries and,
// optionally, the keys of the models map in a llama-swap config.yaml.
// --models is repeatable and each value may be comma-separated
// (model-a,model-b); the entries are split so both forms work.
func gcAllowedModels(models []string, configPath string) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, m := range models {
		for _, id := range strings.Split(m, ",") {
			if id = strings.TrimSpace(id); id != "" {
				allowed[id] = true
			}
		}
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading config %s: %w", configPath, err)
		}
		var doc struct {
			Models map[string]interface{} `yaml:"models"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", configPath, err)
		}
		for id := range doc.Models {
			allowed[id] = true
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("no models to keep: use --models or --config")
	}
	return allowed, nil
}

// gcCmd Implements the gc subcommand: deletes backend workloads whose models are no longer allowed by the config.
func gcCmd(args []string) error {
	var f gcFlags
	fs := newFlagSet("gc")
	addKubeFlags(fs, &f.namespace, &f.kubeconfig)
	fs.Var(&f.models, "models", "comma-separated model IDs that should survive GC (repeatable)")
	fs.StringVar(&f.config, "config", "", "path to a llama-swap config.yaml; its models keep surviving GC")
	fs.BoolVar(&f.deleteVolumes, "delete-volumes", false, "also delete PVCs that kubeswap created for collected models")
	fs.Parse(args)

	client, err := buildClient(f.kubeconfig)
	if err != nil {
		return err
	}
	allowed, err := gcAllowedModels(f.models, f.config)
	if err != nil {
		return err
	}

	deleted, err := gcCollect(client, f.namespace, allowed, f.deleteVolumes)
	if err != nil {
		return err
	}
	log.Printf("gc complete: collected %v", deleted)
	return nil
}

// gcCollect deletes managed workloads whose model ID is not in allowed and
// returns the collected model IDs.
func gcCollect(client kubernetes.Interface, namespace string, allowed map[string]bool, deleteVolumes bool) ([]string, error) {
	ctx := context.Background()
	managed := labels.Set(map[string]string{labelManagedBy: managedByValue}).String()
	deps, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: managed})
	if err != nil {
		return nil, fmt.Errorf("listing managed deployments: %w", err)
	}

	var deleted []string
	for i := range deps.Items {
		dep := &deps.Items[i]
		modelID := dep.Annotations[annotationModelID]
		if modelID == "" {
			modelID = dep.Labels[labelModel]
		}
		if allowed[modelID] {
			log.Printf("keeping %s/%s (model %q)", namespace, dep.Name, modelID)
			continue
		}
		log.Printf("collecting %s/%s (model %q not in config)", namespace, dep.Name, modelID)
		if err := deleteModel(client, namespace, modelID, deleteVolumes, 0); err != nil {
			return deleted, fmt.Errorf("collecting model %q: %w", modelID, err)
		}
		deleted = append(deleted, modelID)
	}
	return deleted, nil
}

// statusCmd Implements the status subcommand: reports on the namespace's managed workloads.
func statusCmd(args []string) error {
	var (
		namespace  string
		kubeconfig string
	)
	fs := newFlagSet("status")
	addKubeFlags(fs, &namespace, &kubeconfig)
	fs.Parse(args)
	return runStatus(namespace, kubeconfig)
}

// runStatus Lists the managed Deployments, their pods and the models they serve.
func runStatus(namespace, kubeconfig string) error {
	client, err := buildClient(kubeconfig)
	if err != nil {
		return err
	}
	ctx := context.Background()
	managed := labels.Set(map[string]string{labelManagedBy: managedByValue}).String()
	deps, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: managed})
	if err != nil {
		return fmt.Errorf("listing managed deployments: %w", err)
	}
	if len(deps.Items) == 0 {
		fmt.Printf("no managed workloads in namespace %s\n", namespace)
		return nil
	}

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: managed})
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}
	podByModel := map[string]*corev1.Pod{}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		// Key by the deployment label (unique per model); fall back to the
		// coarse model label for pods rendered before the deployment label
		// existed (there the deployment name IS the model label value).
		key := p.Labels[labelDeployment]
		if key == "" {
			key = p.Labels[labelModel]
		}
		if _, ok := podByModel[key]; !ok {
			podByModel[key] = p
		}
	}

	fmt.Printf("%-32s %-28s %-24s %-8s %s\n", "MODEL", "DEPLOYMENT", "POD", "READY", "AGE")
	for i := range deps.Items {
		dep := &deps.Items[i]
		model := dep.Annotations[annotationModelID]
		if model == "" {
			model = dep.Labels[labelModel]
		}
		age := "new"
		if !dep.CreationTimestamp.IsZero() {
			age = time.Since(dep.CreationTimestamp.Time).Round(time.Second).String()
		}
		pod, ok := podByModel[dep.Name]
		podName, ready := "-", "-"
		if ok {
			podName = pod.Name
			ready = "no"
			if podIsReady(pod) {
				ready = "yes"
			}
		}
		fmt.Printf("%-32s %-28s %-24s %-8s %s\n",
			truncate(model, 32), truncate(dep.Name, 28), truncate(podName, 24), ready, age)
	}
	return nil
}

// truncate Shortens s to at most n characters, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
