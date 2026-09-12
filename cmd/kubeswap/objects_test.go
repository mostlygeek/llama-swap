package main

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func testConfig() *serveConfig {
	cfg := &serveConfig{
		Model:        "author/model:tag",
		Sanitized:    "author-model-tag",
		Namespace:    "llama-swap",
		Image:        "ghcr.io/ggml-org/llama.cpp:server-vulkan",
		Args:         []string{"--model", "/models/m.gguf", "--port", "8080"},
		Port:         8080,
		HealthPath:   "/health",
		CheckPath:    "/health",
		LivenessPath: "",
		ProbeTimeout: 5,
		Env:          []envVar{{Key: "HSA_FORCE_FINE_GRAIN_PCIE", Value: "1"}},
		GPUs:         map[string]string{"amd.com/gpu": "1"},
		NodeSel:      map[string]string{"feature.node.kubernetes.io/amd-gpu": "true"},
		Tolerations:  []toleration{{Key: "dedicated", Operator: "Exists", Effect: "NoSchedule"}},
		Volumes: []volumeSpec{
			{Kind: volPVC, Name: "llama-swap-models", Path: "/models", ReadOnly: true},
			{Kind: volEmptyDir, Name: "slots", Path: "/slots"},
		},
		ExtraLabels:  map[string]string{"team": "inference"},
		PVCSize:      "5Gi",
		PVCClass:     "local-path",
		GraceSeconds: 30,
	}
	cfg.DepName, _ = deploymentName(cfg.Model)
	cfg.SvcName, _ = serviceName(cfg.Model)
	return cfg
}

func TestKubeswap_RenderDeployment(t *testing.T) {
	cfg := testConfig()
	dep, err := cfg.renderDeployment()
	if err != nil {
		t.Fatalf("renderDeployment: %v", err)
	}

	if dep.Name != cfg.DepName || dep.Namespace != "llama-swap" {
		t.Errorf("name/namespace: %s/%s", dep.Namespace, dep.Name)
	}
	if dep.Labels[labelModel] != "author-model-tag" || dep.Labels[labelManagedBy] != managedByValue {
		t.Errorf("labels: %v", dep.Labels)
	}
	if dep.Spec.Selector.MatchLabels[labelDeployment] != cfg.DepName {
		t.Errorf("deployment selector must carry the deployment label: %v", dep.Spec.Selector.MatchLabels)
	}
	tpl := dep.Spec.Template
	if tpl.Labels[labelDeployment] != cfg.DepName {
		t.Errorf("pod template deployment label: %v", tpl.Labels)
	}
	if dep.Annotations[annotationModelID] != "author/model:tag" {
		t.Errorf("annotation model-id: %v", dep.Annotations)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Error("expected replicas=1")
	}
	if dep.Spec.Strategy.Type != "Recreate" {
		t.Errorf("expected Recreate strategy, got %s", dep.Spec.Strategy.Type)
	}

	pod := &dep.Spec.Template
	if pod.Labels[labelModel] != "author-model-tag" || pod.Labels[labelAppName] != appNameValue {
		t.Errorf("pod template labels: %v", pod.Labels)
	}
	if pod.Labels["team"] != "inference" {
		t.Errorf("extra pod labels missing: %v", pod.Labels)
	}

	c := pod.Spec.Containers[0]
	if c.Name != containerName || c.Image != cfg.Image {
		t.Errorf("container name/image: %s %s", c.Name, c.Image)
	}
	if strings.Join(c.Args, " ") != strings.Join(cfg.Args, " ") {
		t.Errorf("args: %v", c.Args)
	}
	if len(c.Env) != 1 || c.Env[0].Name != "HSA_FORCE_FINE_GRAIN_PCIE" || c.Env[0].Value != "1" {
		t.Errorf("env: %v", c.Env)
	}
	if got := c.Resources.Limits[corev1.ResourceName("amd.com/gpu")]; got.String() != "1" {
		t.Errorf("gpu limit: %v", c.Resources.Limits)
	}
	if got := c.Resources.Requests[corev1.ResourceName("amd.com/gpu")]; got.String() != "1" {
		t.Errorf("gpu request: %v", c.Resources.Requests)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil ||
		c.ReadinessProbe.HTTPGet.Path != "/health" {
		t.Errorf("readiness probe: %v", c.ReadinessProbe)
	}
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil ||
		c.LivenessProbe.HTTPGet.Path != "/health" {
		t.Errorf("liveness probe: %v", c.LivenessProbe)
	}
	if c.StartupProbe == nil || c.StartupProbe.HTTPGet == nil ||
		c.StartupProbe.HTTPGet.Path != "/health" ||
		c.StartupProbe.FailureThreshold != int32(defaultStartupTimeout/5) {
		t.Errorf("startup probe: %v", c.StartupProbe)
	}
	if len(c.Ports) != 1 || c.Ports[0].Name != "http" || c.Ports[0].ContainerPort != 8080 {
		t.Errorf("container ports: %v", c.Ports)
	}
	if len(c.VolumeMounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(c.VolumeMounts))
	}
	if c.VolumeMounts[0].MountPath != "/models" || !c.VolumeMounts[0].ReadOnly {
		t.Errorf("models mount: %v", c.VolumeMounts[0])
	}
	if c.VolumeMounts[1].MountPath != "/slots" || c.VolumeMounts[1].ReadOnly {
		t.Errorf("slots mount: %v", c.VolumeMounts[1])
	}
	if len(pod.Spec.Volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(pod.Spec.Volumes))
	}
	if pod.Spec.Volumes[0].PersistentVolumeClaim == nil ||
		pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "llama-swap-models" {
		t.Errorf("pvc volume: %v", pod.Spec.Volumes[0])
	}
	if pod.Spec.Volumes[1].EmptyDir == nil {
		t.Errorf("emptydir volume: %v", pod.Spec.Volumes[1])
	}
	if pod.Spec.NodeSelector["feature.node.kubernetes.io/amd-gpu"] != "true" {
		t.Errorf("nodeSelector: %v", pod.Spec.NodeSelector)
	}
	if len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "dedicated" {
		t.Errorf("tolerations: %v", pod.Spec.Tolerations)
	}
	if pod.Spec.TerminationGracePeriodSeconds == nil || *pod.Spec.TerminationGracePeriodSeconds != 30 {
		t.Error("grace period not set to 30")
	}
}

func TestKubeswap_RenderDeploymentCPU(t *testing.T) {
	cfg := testConfig()
	cfg.GPUs = nil
	cfg.NodeSel = nil
	dep, err := cfg.renderDeployment()
	if err != nil {
		t.Fatalf("renderDeployment: %v", err)
	}
	pod := &dep.Spec.Template
	if len(pod.Spec.Containers[0].Resources.Limits) != 0 {
		t.Errorf("expected no resource limits, got %v", pod.Spec.Containers[0].Resources.Limits)
	}
	if pod.Spec.NodeSelector != nil {
		t.Errorf("expected no nodeSelector, got %v", pod.Spec.NodeSelector)
	}
}

func TestKubeswap_RenderService(t *testing.T) {
	cfg := testConfig()
	svc := cfg.renderService()
	if svc.Name != cfg.SvcName || svc.Namespace != "llama-swap" {
		t.Errorf("service name/namespace: %s/%s", svc.Namespace, svc.Name)
	}
	if svc.Spec.Selector[labelModel] != "author-model-tag" {
		t.Errorf("selector: %v", svc.Spec.Selector)
	}
	if svc.Spec.Selector[labelDeployment] != cfg.DepName {
		t.Errorf("service selector must carry the deployment label: %v", svc.Spec.Selector)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Errorf("ports: %v", svc.Spec.Ports)
	}
	if svc.Annotations[annotationModelID] != "author/model:tag" {
		t.Errorf("annotation: %v", svc.Annotations)
	}
}

func TestKubeswap_RenderDeploymentCommandPortsProbes(t *testing.T) {
	cfg := testConfig()
	cfg.GPUs = nil
	cfg.Command = []string{"sd-server"}
	cfg.ServicePorts = []servicePortSpec{{Name: "metrics", Port: 9090}}
	cfg.LivenessPath = "/live"
	cfg.ProbeTimeout = 10
	cfg.Requests = map[string]string{"cpu": "2"}
	cfg.Limits = map[string]string{"memory": "4Gi"}

	dep, err := cfg.renderDeployment()
	if err != nil {
		t.Fatalf("renderDeployment: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers[0]

	if strings.Join(c.Command, " ") != "sd-server" {
		t.Errorf("command: %v", c.Command)
	}
	if len(c.Ports) != 2 || c.Ports[1].Name != "metrics" || c.Ports[1].ContainerPort != 9090 {
		t.Errorf("container ports: %v", c.Ports)
	}
	if c.ReadinessProbe.HTTPGet.Path != "/health" || c.ReadinessProbe.TimeoutSeconds != 10 {
		t.Errorf("readiness probe: %v", c.ReadinessProbe)
	}
	if c.LivenessProbe.HTTPGet.Path != "/live" || c.LivenessProbe.TimeoutSeconds != 10 {
		t.Errorf("liveness probe: %v", c.LivenessProbe)
	}
	if got := c.Resources.Requests[corev1.ResourceCPU]; got.String() != "2" {
		t.Errorf("cpu request: %v", c.Resources.Requests)
	}
	if got := c.Resources.Limits[corev1.ResourceMemory]; got.String() != "4Gi" {
		t.Errorf("memory limit: %v", c.Resources.Limits)
	}

	svc := cfg.renderService()
	if len(svc.Spec.Ports) != 2 || svc.Spec.Ports[1].Name != "metrics" ||
		svc.Spec.Ports[1].Port != 9090 {
		t.Errorf("service ports: %v", svc.Spec.Ports)
	}
}

func TestKubeswap_RenderResourcesGPUPriority(t *testing.T) {
	cfg := testConfig()                                  // gpu: amd.com/gpu=1
	cfg.Requests = map[string]string{"amd.com/gpu": "2"} // explicit wins
	cfg.Limits = map[string]string{"cpu": "8"}
	dep, err := cfg.renderDeployment()
	if err != nil {
		t.Fatalf("renderDeployment: %v", err)
	}
	r := dep.Spec.Template.Spec.Containers[0].Resources
	if got := r.Requests[corev1.ResourceName("amd.com/gpu")]; got.String() != "2" {
		t.Errorf("gpu request (explicit override): %v", r.Requests)
	}
	if got := r.Limits[corev1.ResourceName("amd.com/gpu")]; got.String() != "1" {
		t.Errorf("gpu limit (from --gpu): %v", r.Limits)
	}
	if got := r.Limits[corev1.ResourceCPU]; got.String() != "8" {
		t.Errorf("cpu limit: %v", r.Limits)
	}
}

func TestKubeswap_StartupFailureThreshold(t *testing.T) {
	cases := []struct {
		secs int64
		want int32
	}{
		{0, int32(defaultStartupTimeout / 5)},  // unset -> default
		{600, 120},                             // default value
		{30, 6},                                // short load
		{31, 7},                                // rounds up
		{3, 1},                                 // clamped to at least 1
		{-5, int32(defaultStartupTimeout / 5)}, // negative -> default
	}
	for _, tc := range cases {
		c := &serveConfig{StartupTimeout: tc.secs}
		if got := c.startupFailureThreshold(); got != tc.want {
			t.Errorf("startupFailureThreshold(%d) = %d, want %d", tc.secs, got, tc.want)
		}
	}
}

func TestKubeswap_LivenessPathDefault(t *testing.T) {
	cfg := testConfig()
	if cfg.livenessPath() != "/health" {
		t.Errorf("livenessPath default: %q", cfg.livenessPath())
	}
	cfg.LivenessPath = "/live"
	if cfg.livenessPath() != "/live" {
		t.Errorf("livenessPath: %q", cfg.livenessPath())
	}
	cfg.ProbeTimeout = 0
	if cfg.probeTimeout() != 5 {
		t.Errorf("probeTimeout default: %d", cfg.probeTimeout())
	}
}

func TestKubeswap_RenderPVC(t *testing.T) {
	cfg := testConfig()
	pvc, err := cfg.renderPVC("llama-swap-models")
	if err != nil {
		t.Fatalf("renderPVC: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "local-path" {
		t.Errorf("storage class: %v", pvc.Spec.StorageClassName)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "5Gi" {
		t.Errorf("size: %v", got)
	}
	if pvc.Labels[labelModel] != "author-model-tag" {
		t.Errorf("labels: %v", pvc.Labels)
	}
}

func TestKubeswap_DeploymentSpecMatches(t *testing.T) {
	cfg := testConfig()
	dep, err := cfg.renderDeployment()
	if err != nil {
		t.Fatal(err)
	}

	// Identical spec matches.
	dep2, _ := cfg.renderDeployment()
	if !deploymentSpecMatches(dep, dep2) {
		t.Error("identical specs should match")
	}

	// Image drift.
	dep2.Spec.Template.Spec.Containers[0].Image = "other:tag"
	if deploymentSpecMatches(dep, dep2) {
		t.Error("image drift should not match")
	}

	// Args drift.
	dep2, _ = cfg.renderDeployment()
	dep2.Spec.Template.Spec.Containers[0].Args = []string{"--model", "/other.gguf"}
	if deploymentSpecMatches(dep, dep2) {
		t.Error("args drift should not match")
	}

	// GPU drift.
	dep2, _ = cfg.renderDeployment()
	dep2.Spec.Template.Spec.Containers[0].Resources.Limits = nil
	if deploymentSpecMatches(dep, dep2) {
		t.Error("resource drift should not match")
	}

	// Command drift.
	dep2, _ = cfg.renderDeployment()
	dep2.Spec.Template.Spec.Containers[0].Command = []string{"other-server"}
	if deploymentSpecMatches(dep, dep2) {
		t.Error("command drift should not match")
	}

	// Liveness probe path drift.
	dep2, _ = cfg.renderDeployment()
	dep2.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet.Path = "/other"
	if deploymentSpecMatches(dep, dep2) {
		t.Error("probe path drift should not match")
	}
}
