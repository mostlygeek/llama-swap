package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func newFakeClient() *fake.Clientset { return fake.NewClientset() }

func makeDep(cfg *serveConfig, client *fake.Clientset) (*serveConfig, error) {
	_, err := ensureResources(client, cfg)
	return cfg, err
}

func TestKubeswap_EnsureResourcesCreates(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	res, err := ensureResources(client, cfg)
	if err != nil {
		t.Fatalf("ensureResources: %v", err)
	}
	if res.Adopted {
		t.Error("fresh namespace should not be adopted")
	}
	created := map[string]bool{}
	for _, c := range res.Created {
		created[c] = true
	}
	for _, want := range []string{"pvc/llama-swap-models", "deployment/" + cfg.DepName, "service/" + cfg.SvcName} {
		if !created[want] {
			t.Errorf("expected %q to be created, got %v", want, res.Created)
		}
	}

	// Objects exist with the right labels.
	dep, err := client.AppsV1().Deployments("llama-swap").Get(context.Background(), cfg.DepName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment: %v", err)
	}
	if dep.Labels[labelModel] != "author-model-tag" {
		t.Errorf("labels: %v", dep.Labels)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(context.Background(), cfg.SvcName, metav1.GetOptions{}); err != nil {
		t.Errorf("service: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Get(context.Background(), "llama-swap-models", metav1.GetOptions{}); err != nil {
		t.Errorf("pvc: %v", err)
	}
}

func TestKubeswap_EnsureResourcesAdopts(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}
	// Second invocation adopts instead of failing on AlreadyExists.
	res, err := ensureResources(client, cfg)
	if err != nil {
		t.Fatalf("second ensureResources: %v", err)
	}
	if !res.Adopted {
		t.Error("second invocation should adopt")
	}
	if len(res.Created) != 0 {
		t.Errorf("second invocation should create nothing, got %v", res.Created)
	}
}

func TestKubeswap_EnsureResourcesStrictReplace(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}

	// Simulate drift: someone changed the image.
	ctx := context.Background()
	dep, _ := client.AppsV1().Deployments("llama-swap").Get(ctx, cfg.DepName, metav1.GetOptions{})
	dep.Spec.Template.Spec.Containers[0].Image = "someone-else:latest"
	if _, err := client.AppsV1().Deployments("llama-swap").Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Non-strict: adopt as-is.
	res, err := ensureResources(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Adopted {
		t.Error("non-strict should adopt drifted deployment")
	}

	// Strict: replace.
	cfg.Strict = true
	res, err = ensureResources(client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted {
		t.Error("strict should replace drifted deployment")
	}
	got, err := client.AppsV1().Deployments("llama-swap").Get(ctx, cfg.DepName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("image after replace: %s", got.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestKubeswap_EnsureResourcesAdoptsExistingPVC(t *testing.T) {
	client := newFakeClient()
	// Pre-create the PVC without kubeswap labels (user-managed shared cache).
	userPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-swap-models", Namespace: "llama-swap"},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Create(context.Background(), userPVC, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatalf("ensureResources: %v", err)
	}
	// The user's PVC must not be relabeled or replaced.
	pvc, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Get(context.Background(), "llama-swap-models", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pvc.Labels[labelModel]; ok {
		t.Errorf("user PVC must not be relabeled: %v", pvc.Labels)
	}
}

func TestKubeswap_DeleteModel(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}

	if err := deleteModel(client, "llama-swap", "author/model:tag", true, 0); err != nil {
		t.Fatalf("deleteModel: %v", err)
	}
	ctx := context.Background()
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, cfg.DepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("deployment should be gone, err=%v", err)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(ctx, cfg.SvcName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("service should be gone, err=%v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Get(ctx, "llama-swap-models", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("kubeswap-created PVC should be gone, err=%v", err)
	}

	// Idempotent: deleting again is not an error.
	if err := deleteModel(client, "llama-swap", "author/model:tag", true, 0); err != nil {
		t.Errorf("second deleteModel: %v", err)
	}
}

func TestKubeswap_DeleteModelKeepsUserPVC(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	// A user-managed PVC with a name NOT referenced by the config survives;
	// also one referenced but pre-existing (unlabeled) must survive.
	preExisting := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-swap-models", Namespace: "llama-swap"},
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Create(context.Background(), preExisting, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}
	if err := deleteModel(client, "llama-swap", "author/model:tag", true, 0); err != nil {
		t.Fatalf("deleteModel: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("llama-swap").Get(context.Background(), "llama-swap-models", metav1.GetOptions{}); err != nil {
		t.Errorf("pre-existing PVC must survive deletion: %v", err)
	}
}

func TestKubeswap_EnsureResourcesRejectsForeignModel(t *testing.T) {
	client := newFakeClient()
	ctx := context.Background()
	cfg := testConfig()

	// A deployment exists under this model's name but belongs to a
	// different model (a name collision).
	foreign := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cfg.DepName,
			Namespace:   cfg.Namespace,
			Labels:      managedLabels(cfg.Sanitized),
			Annotations: map[string]string{annotationModelID: "other-model"},
		},
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Create(ctx, foreign, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureResources(client, cfg); err == nil {
		t.Fatal("expected adoption to be refused for a foreign model")
	} else if !strings.Contains(err.Error(), "belongs to model \"other-model\"") {
		t.Errorf("unexpected error: %v", err)
	}

	// The foreign deployment is untouched.
	dep, err := client.AppsV1().Deployments(cfg.Namespace).Get(ctx, cfg.DepName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("foreign deployment should still exist: %v", err)
	}
	if dep.Annotations[annotationModelID] != "other-model" {
		t.Errorf("foreign deployment was modified: %v", dep.Annotations)
	}
}

func TestKubeswap_DeleteModelRefusesForeignModel(t *testing.T) {
	client := newFakeClient()
	ctx := context.Background()
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}

	// A differently-cased ID sanitizes to the same model label; deleting
	// it must not touch this model's backend.
	if err := deleteModel(client, cfg.Namespace, "AUTHOR/MODEL:TAG", true, 0); err == nil {
		t.Fatal("expected deletion to be refused for a different model ID")
	} else if !strings.Contains(err.Error(), "belongs to model") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Get(ctx, cfg.DepName, metav1.GetOptions{}); err != nil {
		t.Errorf("deployment should survive a foreign delete: %v", err)
	}
	if _, err := client.CoreV1().Services(cfg.Namespace).Get(ctx, cfg.SvcName, metav1.GetOptions{}); err != nil {
		t.Errorf("service should survive a foreign delete: %v", err)
	}

	// Deleting with the exact model ID works.
	if err := deleteModel(client, cfg.Namespace, cfg.Model, true, 0); err != nil {
		t.Fatalf("delete with the correct model ID: %v", err)
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Get(ctx, cfg.DepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("deployment should be gone: %v", err)
	}
}

// collidingConfigs returns two configs whose model IDs sanitize to the
// same label (the reviewer's Model_A / model-a case).
func collidingConfigs() (*serveConfig, *serveConfig) {
	a := testConfig()
	a.Model = "Model_A"
	a.Sanitized = "model-a"
	a.DepName, _ = deploymentName(a.Model)
	a.SvcName, _ = serviceName(a.Model)
	b := testConfig()
	b.Model = "model-a"
	b.Sanitized = "model-a"
	b.DepName, _ = deploymentName(b.Model)
	b.SvcName, _ = serviceName(b.Model)
	return a, b
}

func TestKubeswap_PodSelectionIsModelUnique(t *testing.T) {
	client := newFakeClient()
	ctx := context.Background()
	cfgA, cfgB := collidingConfigs()
	if _, err := ensureResources(client, cfgA); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureResources(client, cfgB); err != nil {
		t.Fatal(err)
	}

	// Only model B has a pod, and it is ready; model A's pod is not
	// (up) yet. Model A's wrapper must not see B's pod.
	podB := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfgB.DepName + "-pod",
			Namespace: cfgB.Namespace,
			Labels:    podLabels(cfgB.Sanitized, cfgB.DepName),
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.9",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
	}
	if _, err := client.CoreV1().Pods(cfgB.Namespace).Create(ctx, podB, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	depA, err := client.AppsV1().Deployments(cfgA.Namespace).Get(ctx, cfgA.DepName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srvA := newServer(cfgA, client, "", false, time.Second)
	if st := srvA.findPod(ctx, depA); st.pod != nil {
		t.Fatalf("findPod for %q must not return sibling pod %q", cfgA.Model, st.pod.Name)
	}

	// Model A's Service selector must not match model B's pod.
	selA := labels.SelectorFromSet(podSelectorLabels(cfgA.Sanitized, cfgA.DepName))
	if selA.Matches(labels.Set(podB.Labels)) {
		t.Errorf("service selector for %q matches sibling pod labels %v", cfgA.Model, podB.Labels)
	}
	selB := labels.SelectorFromSet(podSelectorLabels(cfgB.Sanitized, cfgB.DepName))
	if !selB.Matches(labels.Set(podB.Labels)) {
		t.Errorf("service selector for %q must match its own pod", cfgB.Model)
	}

	// Deleting model A (with wait) must succeed immediately despite the
	// sibling pod still running, and must not touch model B.
	if err := deleteModel(client, cfgA.Namespace, cfgA.Model, false, 5*time.Second); err != nil {
		t.Fatalf("deleteModel: %v", err)
	}
	if _, err := client.CoreV1().Pods(cfgB.Namespace).Get(ctx, podB.Name, metav1.GetOptions{}); err != nil {
		t.Errorf("sibling pod must survive deleting model A: %v", err)
	}
}

func TestKubeswap_GCAllowedModels(t *testing.T) {
	allowed, err := gcAllowedModels([]string{"a", "b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed["a"] || !allowed["b"] || allowed["c"] {
		t.Errorf("allowed: %v", allowed)
	}
	if _, err := gcAllowedModels(nil, ""); err == nil {
		t.Error("expected error with no models and no config")
	}
}

func TestKubeswap_GCAllowedModelsFromConfig(t *testing.T) {
	cfgFile := t.TempDir() + "/config.yaml"
	data := []byte("models:\n  model-a:\n    cmd: sleep 1\n  model-b:\n    cmd: sleep 2\n")
	if err := os.WriteFile(cfgFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	allowed, err := gcAllowedModels(nil, cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed["model-a"] || !allowed["model-b"] {
		t.Errorf("allowed: %v", allowed)
	}
}

func TestKubeswap_GCCollectsStaleWorkloads(t *testing.T) {
	client := newFakeClient()
	cfgA := testConfig()
	cfgA.Model = "model-a"
	cfgA.Sanitized = "model-a"
	cfgA.DepName, _ = deploymentName(cfgA.Model)
	cfgA.SvcName, _ = serviceName(cfgA.Model)
	cfgB := testConfig()
	cfgB.Model = "model-b"
	cfgB.Sanitized = "model-b"
	cfgB.DepName, _ = deploymentName(cfgB.Model)
	cfgB.SvcName, _ = serviceName(cfgB.Model)
	if _, err := ensureResources(client, cfgA); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureResources(client, cfgB); err != nil {
		t.Fatal(err)
	}

	// model-b was removed from config: only model-a survives.
	allowed := map[string]bool{"model-a": true}
	ctx := context.Background()
	deleted, err := gcCollect(client, "llama-swap", allowed, false)
	if err != nil {
		t.Fatalf("gcCollect: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "model-b" {
		t.Errorf("deleted: %v", deleted)
	}
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, cfgA.DepName, metav1.GetOptions{}); err != nil {
		t.Errorf("model-a should survive: %v", err)
	}
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, cfgB.DepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("model-b should be collected, err=%v", err)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(ctx, cfgB.SvcName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("model-b service should be collected, err=%v", err)
	}
}

func TestKubeswap_FindPod(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	s := newServer(cfg, client, "", true, time.Millisecond)
	ctx := context.Background()

	// No pods at all.
	dep, _ := cfg.renderDeployment()
	st := s.findPod(ctx, dep)
	if st.ready || st.pod != nil {
		t.Errorf("expected not ready with no pod, got %+v", st)
	}

	// Pending pod.
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1", Namespace: "llama-swap",
			Labels: podLabels(cfg.Sanitized, cfg.DepName),
			UID:    types.UID("u1"),
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	if _, err := client.CoreV1().Pods("llama-swap").Create(ctx, pending, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	st = s.findPod(ctx, dep)
	if st.ready {
		t.Error("pending pod should not be ready")
	}

	// Ready pod with an IP.
	ready := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1", Namespace: "llama-swap",
			Labels: podLabels(cfg.Sanitized, cfg.DepName),
			UID:    types.UID("u1"),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.5",
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
	if _, err := client.CoreV1().Pods("llama-swap").Update(ctx, ready, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	st = s.findPod(ctx, dep)
	if !st.ready || st.pod == nil || st.pod.Status.PodIP != "10.0.0.5" {
		t.Errorf("expected ready pod with IP, got %+v", st)
	}
}

func TestKubeswap_PollOnceRequestsStopOnDeletion(t *testing.T) {
	client := newFakeClient()
	cfg := testConfig()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatal(err)
	}
	s := newServer(cfg, client, "", true, time.Millisecond)
	ctx := context.Background()

	// Deployment exists: no stop requested, not-ready (no pod) reported.
	s.pollOnce(ctx)
	select {
	case <-s.stopCh:
		t.Fatal("stop requested while deployment exists")
	default:
	}
	if s.ready.Load() {
		t.Error("should not be ready without a pod")
	}
	if reason := s.readyReason.Load().(string); reason == "" {
		t.Error("expected a not-ready reason")
	}

	// Delete the deployment: serve must request a stop.
	if err := client.AppsV1().Deployments("llama-swap").Delete(ctx, cfg.DepName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	s.pollOnce(ctx)
	select {
	case <-s.stopCh:
	default:
		t.Fatal("stop not requested after deployment deletion")
	}
}
