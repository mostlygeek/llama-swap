package main

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	for _, want := range []string{"pvc/llama-swap-models", "deployment/author-model-tag", "service/author-model-tag-svc"} {
		if !created[want] {
			t.Errorf("expected %q to be created, got %v", want, res.Created)
		}
	}

	// Objects exist with the right labels.
	dep, err := client.AppsV1().Deployments("llama-swap").Get(context.Background(), "author-model-tag", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment: %v", err)
	}
	if dep.Labels[labelModel] != "author-model-tag" {
		t.Errorf("labels: %v", dep.Labels)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(context.Background(), "author-model-tag-svc", metav1.GetOptions{}); err != nil {
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
	dep, _ := client.AppsV1().Deployments("llama-swap").Get(ctx, "author-model-tag", metav1.GetOptions{})
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
	got, err := client.AppsV1().Deployments("llama-swap").Get(ctx, "author-model-tag", metav1.GetOptions{})
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
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, "author-model-tag", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("deployment should be gone, err=%v", err)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(ctx, "author-model-tag-svc", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
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
	cfgB := testConfig()
	cfgB.Model = "model-b"
	cfgB.Sanitized = "model-b"
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
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, "model-a", metav1.GetOptions{}); err != nil {
		t.Errorf("model-a should survive: %v", err)
	}
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, "model-b", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("model-b should be collected, err=%v", err)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(ctx, "model-b-svc", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
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
			Labels: managedLabels(cfg.Sanitized),
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
			Labels: managedLabels(cfg.Sanitized),
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
	if err := client.AppsV1().Deployments("llama-swap").Delete(ctx, "author-model-tag", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	s.pollOnce(ctx)
	select {
	case <-s.stopCh:
	default:
		t.Fatal("stop not requested after deployment deletion")
	}
}
