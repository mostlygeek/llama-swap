package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTestServer Builds a proxy server over a fake client for the handler tests.
func newTestServer(cfg *serveConfig, upstreamOverride string) *server {
	return newServer(cfg, newFakeClient(), upstreamOverride, true, time.Millisecond, 300*time.Second)
}

// TestKubeswap_StartModel verifies the start path: creates the backend
// resources and exits, and a second run adopts instead of failing.
func TestKubeswap_StartModel(t *testing.T) {
	client := newFakeClient()
	f := &serveFlags{
		model: "m", namespace: "llama-swap", image: "img", port: 8080,
		healthPath: "/health", probeTimeout: 5 * time.Second,
		startupTimeout: 10 * time.Minute, grace: 30 * time.Second,
		pvcSize: "1Gi", pvcMode: "rwo",
	}
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	if err := f.validateServeFlags(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := startModel(client, f, fs); err != nil {
		t.Fatalf("startModel: %v", err)
	}
	depName, _ := deploymentName("m")
	svcName, _ := serviceName("m")
	ctx := context.Background()
	if _, err := client.AppsV1().Deployments("llama-swap").Get(ctx, depName, metav1.GetOptions{}); err != nil {
		t.Errorf("deployment should exist: %v", err)
	}
	if _, err := client.CoreV1().Services("llama-swap").Get(ctx, svcName, metav1.GetOptions{}); err != nil {
		t.Errorf("service should exist: %v", err)
	}
	if err := startModel(client, f, fs); err != nil {
		t.Errorf("second startModel should adopt, got %v", err)
	}
}

// TestKubeswap_ServeFlagsValidation verifies required fields and invalid
// values are rejected before they reach the API server.
func TestKubeswap_ServeFlagsValidation(t *testing.T) {
	base := &serveFlags{model: "m", image: "img", port: 8080, healthPath: "/health", checkPath: "/health"}
	if err := (&serveFlags{image: "img", port: 8080}).validateServeFlags(); err == nil || !strings.Contains(err.Error(), "--model") {
		t.Errorf("missing model: %v", err)
	}
	if err := (&serveFlags{model: "m", port: 8080}).validateServeFlags(); err == nil || !strings.Contains(err.Error(), "--image") {
		t.Errorf("missing image: %v", err)
	}
	if err := base.validateServeFlags(); err != nil {
		t.Errorf("valid flags: %v", err)
	}
	badPort := *base
	badPort.port = 70000
	if err := badPort.validateServeFlags(); err == nil || !strings.Contains(err.Error(), "invalid --port") {
		t.Errorf("bad port: %v", err)
	}
	badPath := *base
	badPath.livenessPath = "health"
	if err := badPath.validateServeFlags(); err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Errorf("bad path: %v", err)
	}
}

// TestKubeswap_ToConfigRejectsReservedLabels Verifies managed label keys are rejected as extra labels.
func TestKubeswap_ToConfigRejectsReservedLabels(t *testing.T) {
	for _, key := range []string{labelManagedBy, labelModel, labelDeployment, labelAppName} {
		f := &serveFlags{model: "m", extraLbls: stringList{key + "=x"}}
		if _, err := f.toConfig(); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("label %s: got %v, want reserved-key error", key, err)
		}
	}
	f := &serveFlags{model: "m", extraLbls: stringList{"team=ml", "note=dev"}}
	cfg, err := f.toConfig()
	if err != nil {
		t.Fatalf("non-reserved labels should pass: %v", err)
	}
	if cfg.ExtraLabels["team"] != "ml" || cfg.ExtraLabels["note"] != "dev" {
		t.Errorf("got %v", cfg.ExtraLabels)
	}
}

// TestKubeswap_ServeRejectsNegativeGrace Verifies a negative --grace is rejected at parse time.
func TestKubeswap_ServeRejectsNegativeGrace(t *testing.T) {
	err := serveCmd([]string{
		"--listen", "127.0.0.1:0", "--model", "m", "--image", "img",
		"--grace", "-5s",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --grace") {
		t.Errorf("expected invalid --grace error, got %v", err)
	}
}

// TestKubeswap_ServeRejectsNegativeProxyResponseTimeout Verifies a negative
// --proxy-response-timeout is rejected at parse time (zero is valid: no
// bound, for slow CPU image generation).
func TestKubeswap_ServeRejectsNegativeProxyResponseTimeout(t *testing.T) {
	err := serveCmd([]string{
		"--listen", "127.0.0.1:0", "--model", "m", "--image", "img",
		"--proxy-response-timeout", "-5s",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid --proxy-response-timeout") {
		t.Errorf("expected invalid --proxy-response-timeout error, got %v", err)
	}
}

// TestKubeswap_ServeRejectsOutOfRangePort Verifies out-of-range --port values are rejected.
func TestKubeswap_ServeRejectsOutOfRangePort(t *testing.T) {
	for _, port := range []string{"0", "-1", "65536"} {
		err := serveCmd([]string{
			"--listen", "127.0.0.1:0", "--model", "m", "--image", "img",
			"--port", port,
		})
		if err == nil || !strings.Contains(err.Error(), "invalid --port") {
			t.Errorf("--port %s: got %v, want invalid --port error", port, err)
		}
	}
}

// TestKubeswap_HandlerNotReady Verifies the check path reports 503 with a reason before the backend is ready.
func TestKubeswap_HandlerNotReady(t *testing.T) {
	s := newTestServer(testConfig(), "")
	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not-ready") {
		t.Errorf("body: %s", rr.Body.String())
	}

	// Non-check paths get the same answer.
	rr2 := httptest.NewRecorder()
	s.handler(rr2, httptest.NewRequest(http.MethodPost, "/completion", nil))
	if rr2.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /completion, got %d", rr2.Code)
	}
}

// TestKubeswap_HandlerReadyButNoUpstream Verifies 502 is returned once ready but before an upstream pod exists.
func TestKubeswap_HandlerReadyButNoUpstream(t *testing.T) {
	s := newTestServer(testConfig(), "")
	s.ready.Store(true)
	s.readyReason.Store("pod ready")
	// upstream left empty; non-check paths need it
	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/v1/completions", nil))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 with no upstream, got %d", rr.Code)
	}
}

// TestKubeswap_HandlerProxies Verifies ready traffic is proxied to the upstream pod.
func TestKubeswap_HandlerProxies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	s := newTestServer(testConfig(), "")
	s.upstream.Store(upstream.URL)
	s.ready.Store(true)
	s.readyReason.Store("pod ready")

	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/v1/completions", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestKubeswap_HandlerCheckPath Verifies the self-answered check path mirrors pod readiness state.
func TestKubeswap_HandlerCheckPath(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		io.WriteString(w, "from-backend")
	}))
	defer upstream.Close()

	cfg := testConfig()
	s := newTestServer(cfg, "")
	s.upstream.Store(upstream.URL)

	// Not ready: check path answers 503 + reason without touching upstream.
	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "not-ready") {
		t.Errorf("not-ready check: %d %s", rr.Code, rr.Body.String())
	}

	// Ready: check path answers 200 from pod state, upstream untouched.
	s.ready.Store(true)
	s.readyReason.Store("pod ready")
	rr = httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ready"`) {
		t.Errorf("ready check: %d %s", rr.Code, rr.Body.String())
	}
	if upstreamCalled {
		t.Error("check path must not be proxied upstream")
	}

	// Non-check paths are still proxied.
	rr = httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/v1/completions", nil))
	if rr.Body.String() != "from-backend" {
		t.Errorf("proxied body: %s", rr.Body.String())
	}

	// A custom check path moves the self-answered endpoint; /health
	// becomes an ordinary proxied path again.
	cfg2 := testConfig()
	cfg2.CheckPath = "/custom"
	s2 := newTestServer(cfg2, "")
	s2.upstream.Store(upstream.URL)
	s2.ready.Store(true)
	s2.readyReason.Store("pod ready")

	rr = httptest.NewRecorder()
	s2.handler(rr, httptest.NewRequest(http.MethodGet, "/custom", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ready"`) {
		t.Errorf("custom check: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s2.handler(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Body.String() != "from-backend" {
		t.Errorf("/health should proxy with custom check path: %s", rr.Body.String())
	}
}

// TestKubeswap_HandlerProxiesSSE Verifies streaming (SSE) responses are flushed through the proxy.
func TestKubeswap_HandlerProxiesSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: hello\n\n")
	}))
	defer upstream.Close()

	s := newTestServer(testConfig(), "")
	s.upstream.Store(upstream.URL)
	s.ready.Store(true)
	s.readyReason.Store("pod ready")

	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/completion", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if !strings.Contains(rr.Body.String(), "data: hello") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestKubeswap_HandlerOverrideUpstream Verifies the --upstream override redirects the proxy target.
func TestKubeswap_HandlerOverrideUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "from-override")
	}))
	defer upstream.Close()

	s := newTestServer(testConfig(), upstream.URL)
	if s.upstream.Load().(string) != upstream.URL {
		t.Fatalf("override not applied: %v", s.upstream.Load())
	}
	s.ready.Store(true)
	rr := httptest.NewRecorder()
	s.handler(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Body.String() != "from-override" {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestKubeswap_ProxyTransportBypassesEnvironmentProxy verifies the proxy
// does not route in-cluster traffic through the environment proxy.
func TestKubeswap_ProxyTransportBypassesEnvironmentProxy(t *testing.T) {
	s := newTestServer(testConfig(), "")
	tr, ok := s.proxy.Transport.(*http.Transport)
	if !ok {
		t.Fatal("proxy transport is not an *http.Transport")
	}
	if tr.Proxy != nil {
		t.Error("proxy transport must not use the environment proxy for pod traffic")
	}
}

// TestKubeswap_PodCrashState Verifies crash detection over container states:
// a running or creating container is not a failure; a terminated container
// (crash or clean exit) and a CrashLoopBackOff container are.
func TestKubeswap_PodCrashState(t *testing.T) {
	term := func(code int32, reason string) corev1.ContainerState {
		return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason}}
	}
	waiting := func(reason string) corev1.ContainerState {
		return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
	}
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}

	cases := []struct {
		name     string
		statuses []corev1.ContainerStatus
		code     int
		failed   bool
	}{
		{"nil pod", nil, 0, false},
		{"running", []corev1.ContainerStatus{{Name: "server", State: running}}, 0, false},
		{"creating", []corev1.ContainerStatus{{Name: "server", State: waiting("ContainerCreating")}}, 0, false},
		{"image pull backoff", []corev1.ContainerStatus{{Name: "server", State: waiting("ImagePullBackOff")}}, 0, false},
		{"oom killed", []corev1.ContainerStatus{{Name: "server", State: term(137, "OOMKilled")}}, 137, true},
		{"clean exit", []corev1.ContainerStatus{{Name: "server", State: term(0, "")}}, 0, true},
		{
			"crash loop with last state",
			[]corev1.ContainerStatus{{
				Name: "server", State: waiting("CrashLoopBackOff"),
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
			}},
			1, true,
		},
		{
			"crash loop without last state",
			[]corev1.ContainerStatus{{Name: "server", State: waiting("CrashLoopBackOff")}},
			1, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p *corev1.Pod
			if tc.statuses != nil {
				p = &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: tc.statuses}}
			}
			code, desc, failed := podCrashState(p)
			if failed != tc.failed {
				t.Fatalf("failed=%v (%s) want %v", failed, desc, tc.failed)
			}
			if tc.failed && code != tc.code {
				t.Errorf("code=%d want %d", code, tc.code)
			}
		})
	}
}

// crashedPod Builds the model's pod (matching the deployment selector) with
// one container in the given state.
func crashedPod(cfg *serveConfig, state corev1.ContainerState) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: cfg.Namespace,
			Labels:    podSelectorLabels(cfg.Sanitized, cfg.DepName),
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: containerName, State: state}},
		},
	}
}

// TestKubeswap_BackendCrashFailsWrapper Verifies the crash path end to end:
// the poller detects the failed container, the wrapper's own objects are
// deleted (ownership-verified), and the backend's exit code is signalled.
func TestKubeswap_BackendCrashFailsWrapper(t *testing.T) {
	cfg := testConfig()
	client := newFakeClient()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatalf("ensureResources: %v", err)
	}
	state := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}
	if _, err := client.CoreV1().Pods(cfg.Namespace).Create(context.Background(), crashedPod(cfg, state), metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating pod: %v", err)
	}

	s := newServer(cfg, client, "", true, time.Millisecond, 300*time.Second)
	s.pollOnce(context.Background())

	select {
	case code := <-s.crashCh:
		if code != 137 {
			t.Fatalf("crashCh code=%d want 137 (the backend's exit code)", code)
		}
	default:
		t.Fatal("crashCh is empty: the failed backend was not signalled")
	}
	if s.ready.Load() {
		t.Error("wrapper must not be ready after a backend crash")
	}
	reason := s.readyReason.Load().(string)
	if !strings.Contains(reason, "backend failing") {
		t.Errorf("ready reason %q should record the backend failure", reason)
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Get(context.Background(), cfg.DepName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("deployment should be deleted after a backend crash (err=%v)", err)
	}
	if _, err := client.CoreV1().Services(cfg.Namespace).Get(context.Background(), cfg.SvcName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("service should be deleted after a backend crash (err=%v)", err)
	}
}

// TestKubeswap_BackendCrashCrashLoop Verifies the backoff window between
// restarts is caught too, with the exit code read from the last termination.
func TestKubeswap_BackendCrashCrashLoop(t *testing.T) {
	cfg := testConfig()
	client := newFakeClient()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatalf("ensureResources: %v", err)
	}
	state := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	pod := crashedPod(cfg, state)
	pod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{ExitCode: 3, Reason: "Error"},
	}
	if _, err := client.CoreV1().Pods(cfg.Namespace).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating pod: %v", err)
	}

	s := newServer(cfg, client, "", true, time.Millisecond, 300*time.Second)
	s.pollOnce(context.Background())

	select {
	case code := <-s.crashCh:
		if code != 3 {
			t.Fatalf("crashCh code=%d want 3 (from the last termination state)", code)
		}
	default:
		t.Fatal("crashCh is empty: a CrashLoopBackOff backend was not signalled")
	}
}

// TestKubeswap_BackendCrashKeepsForeignObjects Verifies the cleanup never
// touches objects that do not verify as kubeswap-managed for the model.
func TestKubeswap_BackendCrashKeepsForeignObjects(t *testing.T) {
	cfg := testConfig()
	client := newFakeClient()
	// A deployment holding our name but carrying no kubeswap labels.
	foreign := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: cfg.DepName, Namespace: cfg.Namespace}}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Create(context.Background(), foreign, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating foreign deployment: %v", err)
	}
	state := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}
	if _, err := client.CoreV1().Pods(cfg.Namespace).Create(context.Background(), crashedPod(cfg, state), metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating pod: %v", err)
	}

	s := newServer(cfg, client, "", true, time.Millisecond, 300*time.Second)
	s.pollOnce(context.Background())

	select {
	case <-s.crashCh:
	default:
		t.Fatal("crashCh is empty: the failed backend was not signalled")
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Get(context.Background(), cfg.DepName, metav1.GetOptions{}); err != nil {
		t.Fatalf("foreign deployment must be kept (err=%v)", err)
	}
}

// TestKubeswap_BackendCrashLosesToShutdown Verifies a graceful shutdown in
// flight wins over the crash path: no signal, no cleanup, deployment kept.
func TestKubeswap_BackendCrashLosesToShutdown(t *testing.T) {
	cfg := testConfig()
	client := newFakeClient()
	if _, err := ensureResources(client, cfg); err != nil {
		t.Fatalf("ensureResources: %v", err)
	}
	state := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}}
	if _, err := client.CoreV1().Pods(cfg.Namespace).Create(context.Background(), crashedPod(cfg, state), metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating pod: %v", err)
	}

	s := newServer(cfg, client, "", true, time.Millisecond, 300*time.Second)
	s.requestStop() // a graceful shutdown is already in progress
	s.pollOnce(context.Background())

	select {
	case code := <-s.crashCh:
		t.Fatalf("crashCh signalled with %d despite an in-flight shutdown", code)
	default:
	}
	if _, err := client.AppsV1().Deployments(cfg.Namespace).Get(context.Background(), cfg.DepName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the deployment must be kept for adoption on a graceful shutdown (err=%v)", err)
	}
}

// TestKubeswap_ExitErrorCarriesCode Verifies the error type main unwraps for
// a non-blanket exit code.
func TestKubeswap_ExitErrorCarriesCode(t *testing.T) {
	e := &exitError{code: 137, msg: "backend failed; exiting with code 137"}
	var ee *exitError
	if !errors.As(e, &ee) {
		t.Fatal("errors.As did not match *exitError")
	}
	if ee.code != 137 || ee.Error() != "backend failed; exiting with code 137" {
		t.Errorf("unwrapped %v / %q", ee.code, ee.Error())
	}
}
