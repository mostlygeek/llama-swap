package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ensureResult describes what ensureResources did.
type ensureResult struct {
	Adopted bool
	Created []string
}

// strictReplaceTimeout bounds how long serve waits for a drifted
// deployment's deletion to finish before creating the replacement
// (the cascade includes pod termination).
const strictReplaceTimeout = 5 * time.Minute

// createRetryInterval is the pause between create retries while a name
// is still reserved; a variable so tests can shrink it.
var createRetryInterval = 500 * time.Millisecond

const maxCreateAttempts = 20

// waitForDeploymentGone polls until the deployment no longer exists. A
// successful Delete can return while the object is still terminating
// (finalizers, pod teardown); creating the replacement before it is
// gone can fail with AlreadyExists and then lose the name entirely
// when the old object finally disappears.
func waitForDeploymentGone(ctx context.Context, client kubernetes.Interface, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("checking deployment %s: %w", name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for deployment %s to finish deleting", timeout, name)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// verifyOwnership checks that obj was created by kubeswap for modelID.
// Every object kubeswap creates carries the ORIGINAL model ID in the
// llama-swap.io/model-id annotation, so two distinct IDs that sanitize to
// the same string can never be confused. Objects without the annotation
// (an older kubeswap) fall back to the sanitized model label.
func verifyOwnership(obj metav1.Object, modelID string) error {
	if v := obj.GetLabels()[labelManagedBy]; v != managedByValue {
		return fmt.Errorf("%s/%s is not managed by kubeswap", obj.GetNamespace(), obj.GetName())
	}
	if v := obj.GetAnnotations()[annotationModelID]; v != "" {
		if v != modelID {
			return fmt.Errorf("%s/%s belongs to model %q, not %q", obj.GetNamespace(), obj.GetName(), v, modelID)
		}
		return nil
	}
	sanitized, err := sanitizeModelID(modelID)
	if err != nil || obj.GetLabels()[labelModel] != sanitized {
		return fmt.Errorf("%s/%s carries no verifiable model ID for %q", obj.GetNamespace(), obj.GetName(), modelID)
	}
	return nil
}

// ensureResources creates (or adopts) the model's PVCs, Deployment and
// Service. Adopting an existing deployment keeps a backend alive across
// head-end restarts; --strict replaces it when the container spec drifted.
// Adoption and deletion only ever touch objects verified to belong to the
// model (verifyOwnership), so colliding model IDs cannot adopt or tear
// down each other's backends.

// ensureResources Creates the model's Deployment and Service, adopting or strictly replacing an existing Deployment and creating any missing PVCs.
func ensureResources(client kubernetes.Interface, cfg *serveConfig) (*ensureResult, error) {
	ctx := context.Background()
	res := &ensureResult{}

	depName := cfg.DepName
	dep, err := client.AppsV1().Deployments(cfg.Namespace).Get(ctx, depName, metav1.GetOptions{})
	switch {
	case err == nil:
		if err := verifyOwnership(dep, cfg.Model); err != nil {
			return nil, fmt.Errorf("adopting deployment %s/%s: %w", cfg.Namespace, depName, err)
		}
		res.Adopted = true
		if cfg.Strict {
			want, err := cfg.renderDeployment()
			if err != nil {
				return nil, err
			}
			if !deploymentSpecMatches(dep, want) {
				log.Printf("deployment %s/%s exists but spec drifted; replacing (--strict)", cfg.Namespace, depName)
				if err := client.AppsV1().Deployments(cfg.Namespace).Delete(ctx, depName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					return nil, fmt.Errorf("deleting drifted deployment %s: %w", depName, err)
				}
				// Wait for the deletion to actually complete before creating
				// the replacement: an immediate Create can return
				// AlreadyExists while the old object is still terminating,
				// and the name then vanishes with it (no replacement, serve
				// shuts down on the next poll, the model never starts).
				if err := waitForDeploymentGone(ctx, client, cfg.Namespace, depName, strictReplaceTimeout); err != nil {
					return nil, fmt.Errorf("replacing drifted deployment %s: %w", depName, err)
				}
				res.Adopted = false
			}
		}
	case apierrors.IsNotFound(err):
		// fall through: create everything
	default:
		return nil, fmt.Errorf("getting deployment %s/%s: %w", cfg.Namespace, depName, err)
	}

	if !res.Adopted {
		created, err := createMissing(client, cfg)
		if err != nil {
			return nil, err
		}
		res.Created = created
	}

	// A deployment can exist while its Service was deleted; recreate the
	// Service so in-cluster consumers keep working.
	svcName := cfg.SvcName
	svc, err := client.CoreV1().Services(cfg.Namespace).Get(ctx, svcName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if _, err := client.CoreV1().Services(cfg.Namespace).Create(ctx, cfg.renderService(), metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("creating service %s: %w", svcName, err)
		}
		if !res.Adopted {
			res.Created = append(res.Created, "service/"+svcName)
		}
	case err == nil:
		if err := verifyOwnership(svc, cfg.Model); err != nil {
			return nil, fmt.Errorf("adopting service %s/%s: %w", cfg.Namespace, svcName, err)
		}
	default:
		return nil, fmt.Errorf("getting service %s: %w", svcName, err)
	}

	return res, nil
}

// createMissing creates the PVCs, Deployment and Service for cfg.
func createMissing(client kubernetes.Interface, cfg *serveConfig) ([]string, error) {
	ctx := context.Background()
	var created []string

	for _, name := range cfg.pvcNames() {
		_, err := client.CoreV1().PersistentVolumeClaims(cfg.Namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			// Adopt the existing PVC as-is (it may be user-managed, e.g. a
			// shared model cache); do not relabel it.
		case apierrors.IsNotFound(err):
			pvc, err := cfg.renderPVC(name)
			if err != nil {
				return nil, err
			}
			if _, err := client.CoreV1().PersistentVolumeClaims(cfg.Namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
				return nil, fmt.Errorf("creating PVC %s: %w", name, err)
			}
			created = append(created, "pvc/"+name)
		default:
			return nil, fmt.Errorf("getting PVC %s: %w", name, err)
		}
	}

	dep, err := cfg.renderDeployment()
	if err != nil {
		return nil, err
	}
	// Retry while the name is still reserved by a deletion in progress;
	// a persistent AlreadyExists means something else holds the name -
	// fail loudly instead of assuming adoption.
	for attempt := 1; ; attempt++ {
		_, err = client.AppsV1().Deployments(cfg.Namespace).Create(ctx, dep, metav1.CreateOptions{})
		if err == nil {
			created = append(created, "deployment/"+dep.Name)
			break
		}
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("creating deployment %s: %w", dep.Name, err)
		}
		if attempt >= maxCreateAttempts {
			return nil, fmt.Errorf("creating deployment %s: name still reserved after %d attempts (a deletion may still be in progress)", dep.Name, attempt)
		}
		time.Sleep(createRetryInterval)
	}

	svc := cfg.renderService()
	if _, err := client.CoreV1().Services(cfg.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("creating service %s: %w", svc.Name, err)
		}
	} else {
		created = append(created, "service/"+svc.Name)
	}

	return created, nil
}

// server is the state of a running `kubeswap serve` process.
type server struct {
	cfg              *serveConfig
	client           kubernetes.Interface
	proxy            *httputil.ReverseProxy
	upstreamOverride string
	noLogs           bool
	poll             time.Duration

	ready       atomic.Bool
	readyReason atomic.Value // string
	upstream    atomic.Value // string

	mu         sync.Mutex
	logUID     string
	logCancel  context.CancelFunc
	logsFailed bool

	srv    *http.Server
	stopCh chan struct{}
	stopMu sync.Once
}

// serveFlags holds the parsed `serve` flags.
type serveFlags struct {
	listen         string
	model          string
	namespace      string
	kubeconfig     string
	image          string
	port           int
	healthPath     string
	checkPath      string
	livenessPath   string
	probeTimeout   time.Duration
	startupTimeout time.Duration
	command        stringList
	requests       stringList
	limits         stringList
	servicePorts   stringList
	envs           stringList
	gpus           stringList
	nodeSel        stringList
	tols           stringList
	volumes        stringList
	extraLbls      stringList
	pvcSize        string
	pvcClass       string
	pvcMode        string
	grace          time.Duration
	strict         bool
	upstream       string
	poll           time.Duration
	noLogs         bool
}

// toConfig Builds the fully parsed serveConfig from the serve flags, validating every field.
func (f *serveFlags) toConfig() (*serveConfig, error) {
	sanitized, err := sanitizeModelID(f.model)
	if err != nil {
		return nil, err
	}
	depName, err := deploymentName(f.model)
	if err != nil {
		return nil, err
	}
	svcName, err := serviceName(f.model)
	if err != nil {
		return nil, err
	}
	envs, err := parseEnvVars(f.envs)
	if err != nil {
		return nil, err
	}
	gpus, err := parseGPUs(f.gpus)
	if err != nil {
		return nil, err
	}
	nodeSel, err := parseKeyValues([]string(f.nodeSel), "node-selector")
	if err != nil {
		return nil, err
	}
	tols, err := parseTolerations(f.tols)
	if err != nil {
		return nil, err
	}
	volumes, err := parseVolumes(f.volumes)
	if err != nil {
		return nil, err
	}
	extra, err := parseKeyValues([]string(f.extraLbls), "label")
	if err != nil {
		return nil, err
	}
	// The managed labels (ownership, model ID, deployment name, app name)
	// are set by kubeswap on every object and drive ownership checks and
	// pod selection, so an extra label must not be able to override them
	// in renderDeployment. Reject them here, before they are stored.
	for k := range extra {
		if k == labelManagedBy || k == labelModel || k == labelDeployment || k == labelAppName {
			return nil, fmt.Errorf("--label %q is reserved (managed by kubeswap); choose another key", k)
		}
	}
	requests, err := parseResources(f.requests, "request")
	if err != nil {
		return nil, err
	}
	limits, err := parseResources(f.limits, "limit")
	if err != nil {
		return nil, err
	}
	servicePorts, err := parseServicePorts(f.servicePorts)
	if err != nil {
		return nil, err
	}
	for _, sp := range servicePorts {
		if sp.Name == "http" || sp.Port == int32(f.port) {
			return nil, fmt.Errorf("--service-port %s:%d collides with the primary http port (%d)", sp.Name, sp.Port, f.port)
		}
	}
	livenessPath := f.livenessPath
	if livenessPath == "" {
		livenessPath = f.healthPath
	}
	checkPath := f.checkPath
	if checkPath == "" {
		checkPath = "/health"
	}
	probeTimeout := int64(f.probeTimeout / time.Second)
	if probeTimeout < 1 {
		probeTimeout = 1
	}
	return &serveConfig{
		Model:          f.model,
		Sanitized:      sanitized,
		DepName:        depName,
		SvcName:        svcName,
		Namespace:      f.namespace,
		Image:          f.image,
		Args:           nil, // set by the caller from the post "--" args
		Port:           int32(f.port),
		HealthPath:     f.healthPath,
		CheckPath:      checkPath,
		LivenessPath:   livenessPath,
		ProbeTimeout:   probeTimeout,
		StartupTimeout: int64(f.startupTimeout / time.Second),
		Command:        []string(f.command),
		Requests:       requests,
		Limits:         limits,
		ServicePorts:   servicePorts,
		Env:            envs,
		GPUs:           gpus,
		NodeSel:        nodeSel,
		Tolerations:    tols,
		Volumes:        volumes,
		ExtraLabels:    extra,
		PVCSize:        f.pvcSize,
		PVCClass:       f.pvcClass,
		PVCAccessMode:  f.pvcMode,
		GraceSeconds:   int64(f.grace / time.Second),
		Strict:         f.strict,
	}, nil
}

// serveCmd Runs the serve subcommand: ensures the backend resources exist, then proxies the listen address to the backend pod until the model is unloaded.
func serveCmd(args []string) error {
	var f serveFlags
	fs := newFlagSet("serve")
	fs.StringVar(&f.listen, "listen", "", "address to listen on (required, e.g. 127.0.0.1:${PORT})")
	fs.StringVar(&f.model, "model", "", "llama-swap model ID (required)")
	addKubeFlags(fs, &f.namespace, &f.kubeconfig)
	fs.StringVar(&f.image, "image", "", "container image (required)")
	fs.IntVar(&f.port, "port", defaultPort, "port the backend container listens on (must match the backend's --port)")
	fs.StringVar(&f.healthPath, "health-path", "/health", "backend health endpoint (drives the pod readiness probe)")
	fs.StringVar(&f.checkPath, "check-path", "/health", "path the wrapper answers itself from pod readiness (200 when ready, 503+reason until then); point consumers' health checks here")
	fs.StringVar(&f.livenessPath, "liveness-path", "", "backend liveness probe endpoint (default: same as --health-path)")
	fs.DurationVar(&f.probeTimeout, "probe-timeout", 5*time.Second, "timeout for the readiness/liveness probe requests")
	fs.DurationVar(&f.startupTimeout, "startup-timeout", 10*time.Minute, "model loading time the startup probe tolerates before the pod restarts; llama-swap's global healthCheckTimeout should be at least this long")
	fs.Var(&f.command, "command", "container command token (repeatable; overrides the image entrypoint)")
	fs.Var(&f.requests, "request", "resource request name=quantity, e.g. cpu=2 or memory=4Gi (repeatable)")
	fs.Var(&f.limits, "limit", "resource limit name=quantity, e.g. cpu=4 (repeatable)")
	fs.Var(&f.servicePorts, "service-port", "additional service port name:port (repeatable)")
	fs.Var(&f.envs, "env", "container env var K=V (repeatable)")
	fs.Var(&f.gpus, "gpu", "GPU resource to request, resource=count (e.g. amd.com/gpu=1, nvidia.com/gpu=1)")
	fs.Var(&f.nodeSel, "node-selector", "node selector K=V (repeatable)")
	fs.Var(&f.tols, "toleration", "toleration key:operator:value:effect (repeatable)")
	fs.Var(&f.volumes, "volume", "volume pvc|emptydir|hostpath:name:path[:ro] (repeatable)")
	fs.Var(&f.extraLbls, "label", "extra pod label K=V (repeatable)")
	fs.StringVar(&f.pvcSize, "pvc-size", "1Gi", "size used when kubeswap must create a missing PVC")
	fs.StringVar(&f.pvcClass, "pvc-class", "", "storage class for created PVCs (default: cluster default)")
	fs.StringVar(&f.pvcMode, "pvc-access-mode", "rwo", "access mode for created PVCs: rwo or rwx")
	fs.DurationVar(&f.grace, "grace", 30*time.Second, "terminationGracePeriodSeconds for the pod")
	fs.BoolVar(&f.strict, "strict", false, "replace the backend when its spec drifts from the config (default: adopt as-is)")
	fs.StringVar(&f.upstream, "upstream", "", "override the proxy upstream URL (default: discovered pod IP)")
	fs.DurationVar(&f.poll, "poll", time.Second, "interval for cluster state polling")
	fs.BoolVar(&f.noLogs, "no-logs", false, "disable pod log forwarding to stderr")
	fs.Parse(args)

	if f.listen == "" {
		return errors.New("--listen is required")
	}
	if f.model == "" {
		return errors.New("--model is required")
	}
	if f.image == "" {
		return errors.New("--image is required")
	}
	for name, p := range map[string]string{"health-path": f.healthPath, "check-path": f.checkPath, "liveness-path": f.livenessPath} {
		if p != "" && !strings.HasPrefix(p, "/") {
			return fmt.Errorf("--%s %q must start with /", name, p)
		}
	}
	if f.port < 1 || f.port > 65535 {
		return fmt.Errorf("invalid --port %d (want 1-65535)", f.port)
	}

	cfg, err := f.toConfig()
	if err != nil {
		return err
	}
	cfg.Args = fs.Args()

	client, err := buildClient(f.kubeconfig)
	if err != nil {
		return err
	}

	res, err := ensureResources(client, cfg)
	if err != nil {
		return err
	}
	if res.Adopted {
		log.Printf("adopting existing deployment %s/%s", cfg.Namespace, cfg.DepName)
	} else {
		log.Printf("created %v", res.Created)
	}

	s := newServer(cfg, client, f.upstream, f.noLogs, f.poll)

	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", f.listen, err)
	}
	log.Printf("kubeswap serve: model=%s namespace=%s upstream=%s listening on %s",
		cfg.Model, cfg.Namespace, s.upstreamDescription(), f.listen)

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.pollLoop(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	var shutdownReason string
	select {
	case sig := <-sigCh:
		shutdownReason = fmt.Sprintf("received %s; shutting down (deployment kept for adoption)", sig)
	case <-s.stopCh:
		shutdownReason = "deployment deleted; shutting down"
	case err := <-errCh:
		shutdownReason = err.Error()
	}
	log.Println(shutdownReason)

	s.stopLogs()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := s.srv.Shutdown(shutCtx); err != nil {
		log.Printf("http server shutdown: %v", err)
	}
	return nil
}

// newServer wires up the reverse proxy and HTTP server. The upstream is
// dynamic (discovered pod IP) unless an override was given.
func newServer(cfg *serveConfig, client kubernetes.Interface, upstreamOverride string, noLogs bool, poll time.Duration) *server {
	s := &server{
		cfg:              cfg,
		client:           client,
		upstreamOverride: strings.TrimRight(upstreamOverride, "/"),
		noLogs:           noLogs,
		poll:             poll,
		stopCh:           make(chan struct{}),
	}
	s.readyReason.Store("starting")
	s.upstream.Store("")

	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			u, err := url.Parse(s.upstream.Load().(string))
			if err != nil {
				// Should not happen; the Director cannot return errors, so
				// point at an invalid host and let the ErrorHandler report.
				r.URL.Scheme, r.URL.Host = "http", "invalid.upstream"
				return
			}
			r.URL.Scheme = u.Scheme
			r.URL.Host = u.Host
			r.Host = u.Host
		},
		// No Proxy: the upstream is a pod IP in this cluster and must
		// never be routed through the environment proxy (the head-end
		// container may carry HTTP_PROXY for other purposes, and
		// ProxyFromEnvironment would send in-cluster traffic through
		// it).
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			// Bounds a wedged backend, not model loading: llama-swap
			// only forwards once the check path reports the pod ready
			// (startup probe passed), so this caps how long a ready
			// backend may stall before producing response headers.
			ResponseHeaderTimeout: 300 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		},
		FlushInterval: -1, // stream responses (SSE) without buffering
		ModifyResponse: func(resp *http.Response) error {
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // client went away; nothing to report
			}
			log.Printf("proxy error: %v", err)
			rec, _ := w.(*statusRecorder)
			if rec == nil || !rec.wrote {
				http.Error(w, fmt.Sprintf("kubeswap proxy error: %v", err), http.StatusBadGateway)
			}
		},
	}
	s.proxy = proxy

	if s.upstreamOverride != "" {
		s.upstream.Store(s.upstreamOverride)
	}

	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.handler),
		ReadHeaderTimeout: 10 * time.Second,
		// No Read/Write timeouts: inference responses can be long-running.
		IdleTimeout: 120 * time.Second,
	}
	return s
}

// pollInterval Returns the cluster polling interval (--poll, floored at one second).
func (s *server) pollInterval() time.Duration {
	if s.poll > 0 {
		return s.poll
	}
	return time.Second
}

// upstreamDescription Describes the current proxy upstream for log messages.
func (s *server) upstreamDescription() string {
	if s.upstreamOverride != "" {
		return s.upstreamOverride + " (override)"
	}
	return "<discovered pod IP>"
}

// handler gates requests on backend readiness, then proxies. The check
// path (--check-path, default /health) is answered by the wrapper itself
// from pod readiness state, so consumers (llama-swap's health check) never
// depend on the backend's own health endpoint existing or behaving.
func (s *server) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == s.cfg.CheckPath {
		s.writeCheck(w)
		return
	}
	if !s.ready.Load() {
		s.writeNotReady(w)
		return
	}
	up := s.upstream.Load().(string)
	if up == "" {
		http.Error(w, "no upstream backend pod available", http.StatusBadGateway)
		return
	}
	recorder := &statusRecorder{ResponseWriter: w}
	s.proxy.ServeHTTP(recorder, r)
}

// writeCheck answers the wrapper's own check path from pod readiness state.
func (s *server) writeCheck(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if s.ready.Load() {
		io.WriteString(w, `{"status":"ready"}`)
		return
	}
	s.writeNotReady(w)
}

// writeNotReady answers 503 with the current readiness reason. Consumers
// polling while the backend loads see this until the pod is Ready.
func (s *server) writeNotReady(w http.ResponseWriter) {
	reason := s.readyReason.Load().(string)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `{"status":"not-ready","reason":%q}`, reason)
}

// podState is the result of one readiness inspection.
type podState struct {
	pod    *corev1.Pod
	ready  bool
	reason string
}

// pollLoop watches cluster state until ctx is done.
func (s *server) pollLoop(ctx context.Context) {
	interval := s.pollInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollOnce(ctx)
		}
	}
}

// requestStop asks serveCmd to shut down (used when the deployment is gone).
func (s *server) requestStop() {
	s.stopMu.Do(func() { close(s.stopCh) })
}

// setReady Updates the proxy readiness state (and its reason), logging transitions.
func (s *server) setReady(ready bool, reason string) {
	if ready != s.ready.Load() || reason != s.readyReason.Load().(string) {
		if ready {
			log.Printf("backend ready (%s)", reason)
		} else if !s.ready.Load() {
			log.Printf("backend not ready: %s", reason)
		}
		s.ready.Store(ready)
		s.readyReason.Store(reason)
	}
}

// pollOnce Polls the cluster once: updates readiness and the proxy upstream, and stops the wrapper when its Deployment has been deleted.
func (s *server) pollOnce(ctx context.Context) {
	cfg := s.cfg
	depName := cfg.DepName
	dep, err := s.client.AppsV1().Deployments(cfg.Namespace).Get(ctx, depName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Printf("deployment %s/%s deleted, shutting down", cfg.Namespace, depName)
			s.setReady(false, "deployment deleted")
			s.requestStop()
		} else {
			s.setReady(false, "error getting deployment: "+err.Error())
		}
		return
	}

	st := s.findPod(ctx, dep)
	if st.ready {
		s.setReady(true, "pod ready")
	} else {
		s.setReady(false, st.reason)
	}
	if s.upstreamOverride == "" {
		if st.pod != nil && st.pod.Status.PodIP != "" {
			s.upstream.Store(fmt.Sprintf("http://%s:%d", st.pod.Status.PodIP, cfg.Port))
		} else {
			s.upstream.Store("")
		}
	}
	if !s.noLogs {
		s.followPodLogs(ctx, st.pod)
	}
}

// findPod picks the model's active pod (not terminating) and reports its
// readiness with a human-readable reason.
func (s *server) findPod(ctx context.Context, dep *appsv1.Deployment) podState {
	cfg := s.cfg
	selector := labels.Set(podSelectorLabels(cfg.Sanitized, cfg.DepName)).String()
	pods, err := s.client.CoreV1().Pods(cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return podState{reason: "error listing pods: " + err.Error()}
	}

	var active *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		if active == nil {
			active = p
			continue
		}
		// Prefer a Ready pod when several match (should be rare with the
		// Recreate strategy, but be deterministic anyway).
		if podIsReady(p) && !podIsReady(active) {
			active = p
		}
	}
	if active == nil {
		return podState{reason: "no running pod (" + deploymentSummary(dep) + ")"}
	}
	if podIsReady(active) && active.Status.PodIP != "" {
		return podState{pod: active, ready: true, reason: "pod " + active.Name + " ready"}
	}
	return podState{pod: active, ready: false, reason: podNotReadyReason(active)}
}

// podIsReady Reports whether the pod carries the Ready condition.
func podIsReady(p *corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// deploymentSummary Formats a one-line replica/ready summary of the Deployment status.
func deploymentSummary(dep *appsv1.Deployment) string {
	st := dep.Status
	return fmt.Sprintf("deployment replicas=%d ready=%d available=%d unavailable=%d",
		st.Replicas, st.ReadyReplicas, st.AvailableReplicas, st.UnavailableReplicas)
}

// podNotReadyReason Describes why a pod is not ready (phase plus waiting/terminating container states).
func podNotReadyReason(p *corev1.Pod) string {
	reason := "pod " + p.Name + " " + string(p.Status.Phase)
	for _, cs := range p.Status.ContainerStatuses {
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
			reason += fmt.Sprintf(", container %s waiting: %s", cs.Name, cs.State.Waiting.Reason)
		case cs.State.Terminated != nil:
			reason += fmt.Sprintf(", container %s terminated (exit %d)", cs.Name, cs.State.Terminated.ExitCode)
		}
		if cs.RestartCount > 0 {
			reason += fmt.Sprintf(", restarts=%d", cs.RestartCount)
		}
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	return reason
}

// followPodLogs keeps one GetLogs(follow) stream open for the current pod
// and reopens it when the pod changes (reschedule).
func (s *server) followPodLogs(ctx context.Context, pod *corev1.Pod) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pod == nil || pod.DeletionTimestamp != nil {
		s.stopLogsLocked()
		return
	}
	uid := string(pod.UID)
	if uid == s.logUID {
		return
	}
	s.stopLogsLocked()
	logCtx, cancel := context.WithCancel(ctx)
	s.logUID = uid
	s.logCancel = cancel
	go s.streamLogs(logCtx, pod)
}

// stopLogs Stops the background pod log forwarding.
func (s *server) stopLogs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLogsLocked()
}

// stopLogsLocked Stops the pod log forwarding, with the server lock already held.
func (s *server) stopLogsLocked() {
	if s.logCancel != nil {
		s.logCancel()
		s.logCancel = nil
	}
	s.logUID = ""
}

// streamLogs Follows the backend pod logs and forwards them to stderr until the pod or the wrapper goes away.
func (s *server) streamLogs(ctx context.Context, pod *corev1.Pod) {
	stream, err := s.client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	}).Stream(ctx)
	if err != nil {
		perm := strings.Contains(strings.ToLower(err.Error()), "forbidden")
		s.mu.Lock()
		perm = perm || s.logsFailed
		s.logsFailed = perm
		s.mu.Unlock()
		if perm {
			log.Printf("log forwarding disabled: %v", err)
			return
		}
		// Transient failure (container still creating/restarting): retry
		// after a short delay until the stream context is canceled (pod
		// change or shutdown).
		select {
		case <-time.After(15 * time.Second):
		case <-ctx.Done():
		}
		if ctx.Err() == nil {
			s.streamLogs(ctx, pod)
		}
		return
	}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "[pod/%s] %s\n", pod.Name, scanner.Text())
	}
	// The scanner has consumed the stream to EOF; close releases the
	// transport connection.
	_ = stream.Close()
}
