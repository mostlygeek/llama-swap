package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
func deleteModel(ctx context.Context, client kubernetes.Interface, namespace, model string, deleteVolumes bool, wait time.Duration) error {
	// The user's --wait plus headroom for the API calls, derived from the
	// caller's context: a hung control plane must not hang the CLI
	// (rest.Config has no request timeout), and a gc pass stays inside
	// its own overall budget rather than 2 minutes per model.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+wait)
	defer cancel()
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
		// Per-iteration bound: a hung List must not stall the loop past
		// its deadline.
		callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
		pods, err := client.CoreV1().Pods(namespace).List(callCtx, metav1.ListOptions{LabelSelector: selector})
		cancel()
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
	return deleteModel(context.Background(), client, f.namespace, model, f.deleteVolumes, f.wait)
}

type gcFlags struct {
	namespace     string
	kubeconfig    string
	models        stringList
	config        string
	deleteVolumes bool
	dryRun        bool
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
	fs.BoolVar(&f.dryRun, "dry-run", false, "report what would be collected without deleting anything")
	fs.Parse(args)

	client, err := buildClient(f.kubeconfig)
	if err != nil {
		return err
	}
	allowed, err := gcAllowedModels(f.models, f.config)
	if err != nil {
		return err
	}

	deleted, err := gcCollect(client, f.namespace, allowed, f.deleteVolumes, f.dryRun)
	if err != nil {
		return err
	}
	if f.dryRun {
		log.Printf("gc complete (dry run): would collect %v", deleted)
	} else {
		log.Printf("gc complete: collected %v", deleted)
	}
	return nil
}

// gcCollect deletes managed workloads whose model ID is not in allowed and
// returns the collected model IDs. With dryRun the workloads are only
// reported, so the operator can see the plan before letting it run.
func gcCollect(client kubernetes.Interface, namespace string, allowed map[string]bool, deleteVolumes, dryRun bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
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
			// No annotation: the allowed set holds ORIGINAL model IDs
			// (the label carries only the sanitized form, which several
			// distinct IDs share) and the deployment name hashes the
			// original ID, so such an object can neither be matched
			// against the config nor deleted by name. Skip it with a
			// loud log instead of guessing: every Deployment kubeswap
			// creates carries the annotation.
			log.Printf("skipping %s/%s: no %s annotation (not a kubeswap object?)", namespace, dep.Name, annotationModelID)
			continue
		}
		if allowed[modelID] {
			log.Printf("keeping %s/%s (model %q)", namespace, dep.Name, modelID)
			continue
		}
		if dryRun {
			log.Printf("would collect %s/%s (model %q not in config)", namespace, dep.Name, modelID)
			deleted = append(deleted, modelID)
			continue
		}
		log.Printf("collecting %s/%s (model %q not in config)", namespace, dep.Name, modelID)
		if err := deleteModel(ctx, client, namespace, modelID, deleteVolumes, 0); err != nil {
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
		watch      bool
		interval   time.Duration
	)
	fs := newFlagSet("status")
	addKubeFlags(fs, &namespace, &kubeconfig)
	fs.BoolVar(&watch, "watch", false, "keep refreshing the table (for interactive use; Ctrl-C to stop)")
	fs.DurationVar(&interval, "interval", 5*time.Second, "refresh interval for --watch")
	fs.Parse(args)
	// time.NewTicker panics on a non-positive duration; reject at parse
	// time like the other flag validation instead of crashing after the
	// initial status print.
	if watch && interval <= 0 {
		return fmt.Errorf("invalid --interval %s (must be positive with --watch)", interval)
	}
	return runStatus(namespace, kubeconfig, watch, interval)
}

// runStatus Lists the managed Deployments, their pods and the models they serve.
func runStatus(namespace, kubeconfig string, watch bool, interval time.Duration) error {
	client, err := buildClient(kubeconfig)
	if err != nil {
		return err
	}
	return runStatusWithClient(context.Background(), client, namespace, watch, interval)
}

func runStatusWithClient(ctx context.Context, client kubernetes.Interface, namespace string, watch bool, interval time.Duration) error {
	bounded, cancel := context.WithTimeout(ctx, apiCallTimeout)
	err := statusOnce(bounded, client, namespace)
	cancel()
	if err != nil {
		return err
	}
	if !watch {
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			bounded, cancel := context.WithTimeout(ctx, apiCallTimeout)
			err := statusOnce(bounded, client, namespace)
			cancel()
			if err != nil {
				fmt.Println("status:", err)
			}
		}
	}
}

// runStatusWithClient prints the status table and, with watch, refreshes it
// on a ticker until ctx is done. A transient API error in the loop is
// printed but does not kill the watch; the initial print failing is an
// error (there is nothing to watch yet).
func statusOnce(ctx context.Context, client kubernetes.Interface, namespace string) error {
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

	fmt.Printf("%-32s %-28s %-24s %-8s %-10s %s\n", "MODEL", "DEPLOYMENT", "POD", "READY", "AGE", "REASON")
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
		podName, ready, reason := "-", "-", ""
		if ok {
			podName = pod.Name
			ready = "no"
			if podIsReady(pod) {
				ready = "yes"
			} else {
				reason = podReason(pod)
			}
		}
		fmt.Printf("%-32s %-28s %-24s %-8s %-10s %s\n",
			truncate(model, 32), truncate(dep.Name, 28), truncate(podName, 24), ready, age, reason)
	}
	return nil
}

// podReason summarizes why a pod is not ready (empty when it is): the
// container waiting state (CrashLoopBackOff, ImagePullBackOff, ...) when
// there is one, the scheduler's verdict when the pod is unscheduled, or
// "starting" when the container is up but the probes have not passed.
func podReason(p *corev1.Pod) string {
	if podIsReady(p) {
		return ""
	}
	// A container that just crashed is Terminated (reason + exit code)
	// until the kubelet's backoff starts and flips it to Waiting —
	// report it rather than the "starting" fallback.
	for _, cs := range p.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil {
			return fmt.Sprintf("%s (exit code %d)", t.Reason, t.ExitCode)
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			if w.Message != "" {
				return truncate(w.Reason+": "+w.Message, 64)
			}
			return w.Reason
		}
	}
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Message != "" {
			return truncate("unscheduled: "+cond.Message, 64)
		}
	}
	return "starting"
}

// logsCmd Implements the logs subcommand: prints (and, with --follow, streams) the backend container logs of a model's pod.
func logsCmd(args []string) error {
	var (
		model  string
		f      manageFlags
		tail   int
		follow bool
	)
	fs := newFlagSet("logs")
	fs.StringVar(&model, "model", "", "llama-swap model ID (required)")
	addKubeFlags(fs, &f.namespace, &f.kubeconfig)
	fs.IntVar(&tail, "tail", 100, "number of lines to start from (0 = all lines)")
	fs.BoolVar(&follow, "follow", false, "keep streaming until the pod or container stops")
	fs.Parse(args)

	if model == "" {
		return errors.New("--model is required")
	}
	client, err := buildClient(f.kubeconfig)
	if err != nil {
		return err
	}
	return runLogs(client, f.namespace, model, tail, follow)
}

// runLogs finds the model's pod and copies the backend container's logs to
// stdout. The pod lookup is bounded (a hung control plane must not hang the
// CLI); the log stream itself runs until it ends or the process is killed.
func runLogs(client kubernetes.Interface, namespace, model string, tail int, follow bool) error {
	sanitized, err := sanitizeModelID(model)
	if err != nil {
		return err
	}
	depName, err := deploymentName(model)
	if err != nil {
		return err
	}
	pod, err := findModelPod(context.Background(), client, namespace, sanitized, depName)
	if err != nil {
		return err
	}
	if pod == nil {
		return fmt.Errorf("no pod found for model %q in namespace %s (is it loaded?)", model, namespace)
	}
	opts := &corev1.PodLogOptions{Container: containerName, Follow: follow}
	if tail > 0 {
		n := int64(tail)
		opts.TailLines = &n
	}
	stream, err := client.CoreV1().Pods(namespace).GetLogs(pod.Name, opts).Stream(context.Background())
	if err != nil {
		return fmt.Errorf("opening logs for pod %s: %w", pod.Name, err)
	}
	defer stream.Close()
	if _, err := io.Copy(os.Stdout, stream); err != nil {
		return fmt.Errorf("reading logs for pod %s: %w", pod.Name, err)
	}
	return nil
}

// findModelPod returns the model's current pod (preferring a ready one, then
// the newest), or nil if the model has no live pod. The deployment name in
// the selector keeps sibling models that sanitize to the same label out of
// reach.
func findModelPod(ctx context.Context, client kubernetes.Interface, namespace, sanitized, depName string) (*corev1.Pod, error) {
	selector := labels.Set(podSelectorLabels(sanitized, depName)).String()
	callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()
	pods, err := client.CoreV1().Pods(namespace).List(callCtx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}
	var best *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		if best == nil ||
			(podIsReady(p) && !podIsReady(best)) ||
			(podIsReady(p) == podIsReady(best) && p.CreationTimestamp.After(best.CreationTimestamp.Time)) {
			best = p
		}
	}
	return best, nil
}

// truncate Shortens s to at most n characters, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
