package main

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// serveConfig is the fully parsed configuration of a `kubeswap serve`
// invocation. The render functions produce deterministic objects from it.
type serveConfig struct {
	Model        string // original model ID
	Sanitized    string // DNS-safe model ID
	DepName      string // Deployment name (sanitized ID + original-ID hash)
	SvcName      string // Service name (deployment name + -svc)
	Namespace    string
	Image        string
	Command      []string // container command (overrides the image entrypoint)
	Args         []string // container args (everything after --)
	Port         int32    // container port the backend listens on
	HealthPath   string   // backend endpoint driving the pod readiness probe
	CheckPath    string   // path the wrapper answers itself (pod readiness state)
	LivenessPath string
	ProbeTimeout int64 // seconds; readiness/liveness probe request timeout
	// StartupTimeout bounds model loading: the startup probe tolerates this
	// much time before the pod is restarted, and gates readiness/liveness
	// until it succeeds.
	StartupTimeout int64 // seconds
	ServicePorts   []servicePortSpec
	Requests       map[string]string // resource name -> quantity
	Limits         map[string]string // resource name -> quantity
	Env            []envVar
	GPUs           map[string]string // resource name -> quantity (requests AND limits)
	NodeSel        map[string]string
	Tolerations    []toleration
	Volumes        []volumeSpec
	ExtraLabels    map[string]string

	// PVC defaults, used when a --volume pvc:<name> does not exist yet.
	PVCSize       string
	PVCClass      string
	PVCAccessMode string

	GraceSeconds int64
	Strict       bool
}

// pvcNames returns the names of PVCs referenced by the config.
func (c *serveConfig) pvcNames() []string {
	var out []string
	for _, v := range c.Volumes {
		if v.Kind == volPVC {
			out = append(out, v.Name)
		}
	}
	return out
}

// renderDeployment builds the model's Deployment.
func (c *serveConfig) renderDeployment() (*appsv1.Deployment, error) {
	depLabels := map[string]string{labelAppName: appNameValue}
	for k, v := range managedLabels(c.Sanitized) {
		depLabels[k] = v
	}
	for k, v := range c.ExtraLabels {
		depLabels[k] = v
	}

	podTemplateLabels := podLabels(c.Sanitized, c.DepName)
	for k, v := range c.ExtraLabels {
		podTemplateLabels[k] = v
	}

	resources, err := c.renderResources()
	if err != nil {
		return nil, err
	}

	container := corev1.Container{
		Name:      containerName,
		Image:     c.Image,
		Command:   c.Command,
		Args:      c.Args,
		Ports:     c.renderPorts(),
		Env:       c.renderEnv(),
		Resources: resources,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        httpProbe(c.HealthPath, c.Port),
			InitialDelaySeconds: 2,
			PeriodSeconds:       5,
			TimeoutSeconds:      c.probeTimeout(),
			FailureThreshold:    3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        httpProbe(c.livenessPath(), c.Port),
			InitialDelaySeconds: 30,
			PeriodSeconds:       15,
			TimeoutSeconds:      c.probeTimeout(),
			FailureThreshold:    3,
		},
		// Gates readiness and liveness until the backend answers once —
		// model loading (often several minutes from a network PVC) must not
		// trip the liveness probe and restart the pod mid-load.
		StartupProbe: &corev1.Probe{
			ProbeHandler:     httpProbe(c.livenessPath(), c.Port),
			PeriodSeconds:    5,
			TimeoutSeconds:   c.probeTimeout(),
			FailureThreshold: c.startupFailureThreshold(),
		},
	}

	volumes, mounts, err := c.renderVolumes()
	if err != nil {
		return nil, err
	}
	container.VolumeMounts = mounts

	// Copies, not references: rendered objects must stay independent of the
	// config (a later mutation of c.NodeSel must not rewrite a rendered
	// Deployment, and two renders of one config must not share maps). An
	// empty selector stays nil, as before.
	var nodeSel map[string]string
	if len(c.NodeSel) > 0 {
		nodeSel = make(map[string]string, len(c.NodeSel))
		for k, v := range c.NodeSel {
			nodeSel[k] = v
		}
	}
	grace := c.GraceSeconds

	annotations := map[string]string{annotationModelID: c.Model}
	recreate := appsv1.RecreateDeploymentStrategyType
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        c.DepName,
			Namespace:   c.Namespace,
			Labels:      depLabels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: recreate},
			Selector: &metav1.LabelSelector{MatchLabels: podSelectorLabels(c.Sanitized, c.DepName)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podTemplateLabels,
					Annotations: map[string]string{annotationModelID: c.Model},
				},
				Spec: corev1.PodSpec{
					Containers:                    []corev1.Container{container},
					Volumes:                       volumes,
					NodeSelector:                  nodeSel,
					Tolerations:                   c.renderTolerations(),
					TerminationGracePeriodSeconds: &grace,
					RestartPolicy:                 corev1.RestartPolicyAlways,
				},
			},
		},
	}, nil
}

// renderPorts renders the container ports: the primary http port plus any
// extra --service-port entries.
func (c *serveConfig) renderPorts() []corev1.ContainerPort {
	ports := []corev1.ContainerPort{{Name: "http", ContainerPort: c.Port}}
	for _, sp := range c.ServicePorts {
		ports = append(ports, corev1.ContainerPort{Name: sp.Name, ContainerPort: sp.Port})
	}
	return ports
}

// livenessPath returns the liveness probe path (defaults to the readiness
// probe path).
func (c *serveConfig) livenessPath() string {
	if c.LivenessPath != "" {
		return c.LivenessPath
	}
	return c.HealthPath
}

// probeTimeout returns the probe request timeout in seconds (minimum 1).
func (c *serveConfig) probeTimeout() int32 {
	if c.ProbeTimeout > 0 {
		return int32(c.ProbeTimeout)
	}
	return 5
}

// startupFailureThreshold converts --startup-timeout into the number of
// failed 5s-interval startup probe attempts the pod may accumulate before
// being restarted.
func (c *serveConfig) startupFailureThreshold() int32 {
	secs := c.StartupTimeout
	if secs <= 0 {
		secs = defaultStartupTimeout
	}
	ft := int32((secs + 4) / 5)
	if ft < 1 {
		ft = 1
	}
	return ft
}

// renderEnv Renders the parsed env vars as corev1.EnvVar values.
func (c *serveConfig) renderEnv() []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(c.Env))
	for _, e := range c.Env {
		out = append(out, corev1.EnvVar{Name: e.Key, Value: e.Value})
	}
	return out
}

// renderResources merges --gpu with explicit --request/--limit entries.
// GPUs are exclusive (non-shareable) resources, so --gpu sets BOTH requests
// and limits; explicit --request/--limit entries override, GPU keys included.
// Quantities are validated at parse time (parseGPUs, parseResources); a
// parse failure here means a serveConfig was built without that validation,
// and rendering returns it instead of zeroing or dropping the entry.
func (c *serveConfig) renderResources() (corev1.ResourceRequirements, error) {
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}
	for _, k := range sortedKeys(c.GPUs) {
		qty, err := resource.ParseQuantity(c.GPUs[k])
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid --gpu quantity %s=%q: %v", k, c.GPUs[k], err)
		}
		requests[corev1.ResourceName(k)] = qty
		limits[corev1.ResourceName(k)] = qty
	}
	for _, k := range sortedKeys(c.Requests) {
		qty, err := resource.ParseQuantity(c.Requests[k])
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid --request quantity %s=%q: %v", k, c.Requests[k], err)
		}
		requests[corev1.ResourceName(k)] = qty
	}
	for _, k := range sortedKeys(c.Limits) {
		qty, err := resource.ParseQuantity(c.Limits[k])
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid --limit quantity %s=%q: %v", k, c.Limits[k], err)
		}
		limits[corev1.ResourceName(k)] = qty
	}
	if len(requests) == 0 && len(limits) == 0 {
		return corev1.ResourceRequirements{}, nil
	}
	return corev1.ResourceRequirements{Requests: requests, Limits: limits}, nil
}

// renderTolerations Renders the parsed tolerations as corev1.Toleration values.
func (c *serveConfig) renderTolerations() []corev1.Toleration {
	out := make([]corev1.Toleration, 0, len(c.Tolerations))
	for _, t := range c.Tolerations {
		out = append(out, corev1.Toleration{
			Key:      t.Key,
			Operator: corev1.TolerationOperator(t.Operator),
			Value:    t.Value,
			Effect:   corev1.TaintEffect(t.Effect),
		})
	}
	return out
}

// renderVolumes converts --volume entries into pod volumes + mounts.
func (c *serveConfig) renderVolumes() (volumes []corev1.Volume, mounts []corev1.VolumeMount, err error) {
	for i, v := range c.Volumes {
		name := fmt.Sprintf("vol%d", i)
		switch v.Kind {
		case volPVC:
			volumes = append(volumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: v.Name,
						ReadOnly:  v.ReadOnly,
					},
				},
			})
		case volEmptyDir:
			volumes = append(volumes, corev1.Volume{
				Name:         name,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
		case volHostPath:
			volumes = append(volumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: v.Name},
				},
			})
		default:
			return nil, nil, fmt.Errorf("unknown volume kind %q", v.Kind)
		}
		mounts = append(mounts, corev1.VolumeMount{
			Name:      name,
			MountPath: v.Path,
			ReadOnly:  v.ReadOnly,
		})
	}
	return volumes, mounts, nil
}

// httpProbe Builds an HTTP probe handler for path on port.
func httpProbe(path string, port int32) corev1.ProbeHandler {
	return corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port)},
	}
}

// renderService builds the model's ClusterIP Service: the primary http port
// plus any extra --service-port entries (for backend metrics etc.).
func (c *serveConfig) renderService() *corev1.Service {
	ports := []corev1.ServicePort{{
		Name:       "http",
		Port:       c.Port,
		TargetPort: intstr.FromInt32(c.Port),
	}}
	for _, sp := range c.ServicePorts {
		ports = append(ports, corev1.ServicePort{
			Name:       sp.Name,
			Port:       sp.Port,
			TargetPort: intstr.FromInt32(sp.Port),
		})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        c.SvcName,
			Namespace:   c.Namespace,
			Labels:      managedLabels(c.Sanitized),
			Annotations: map[string]string{annotationModelID: c.Model},
		},
		Spec: corev1.ServiceSpec{
			Selector: podSelectorLabels(c.Sanitized, c.DepName),
			Ports:    ports,
		},
	}
}

// renderPVC builds a PVC for a missing --volume pvc:<name>.
func (c *serveConfig) renderPVC(name string) (*corev1.PersistentVolumeClaim, error) {
	size, err := resource.ParseQuantity(c.PVCSize)
	if err != nil {
		return nil, fmt.Errorf("invalid --pvc-size %q: %v", c.PVCSize, err)
	}
	mode := corev1.ReadWriteOnce
	if strings.EqualFold(c.PVCAccessMode, "rwx") {
		mode = corev1.ReadWriteMany
	}
	accessModes := []corev1.PersistentVolumeAccessMode{mode}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   c.Namespace,
			Labels:      managedLabels(c.Sanitized),
			Annotations: map[string]string{annotationModelID: c.Model},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
			StorageClassName: strPtr(c.PVCClass),
		},
	}
	return pvc, nil
}

// strPtr Returns a pointer to s, or nil when s is empty.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deploymentSpecMatches reports whether an existing deployment's pod
// template matches the desired one (used by --strict adoption). Every
// configuration-owned field is compared — image, command, args, env, ports,
// volumes, node selector, tolerations, grace period, probes and resources —
// so strict mode replaces the Deployment whenever any of them drifted.
func deploymentSpecMatches(have *appsv1.Deployment, want *appsv1.Deployment) bool {
	if have == nil || want == nil {
		return false
	}
	hs, ws := have.Spec.Template.Spec, want.Spec.Template.Spec
	if !stringMapEqual(hs.NodeSelector, ws.NodeSelector) {
		return false
	}
	if !tolerationsEqual(hs.Tolerations, ws.Tolerations) {
		return false
	}
	if !volumesEqual(hs.Volumes, ws.Volumes) {
		return false
	}
	if derefInt64(hs.TerminationGracePeriodSeconds) != derefInt64(ws.TerminationGracePeriodSeconds) {
		return false
	}
	hc, wc := deploymentContainer(have), deploymentContainer(want)
	if hc == nil || wc == nil {
		return hc == wc
	}
	if hc.Image != wc.Image {
		return false
	}
	if !stringSlicesEqual(hc.Command, wc.Command) {
		return false
	}
	if !stringSlicesEqual(hc.Args, wc.Args) {
		return false
	}
	if !envsEqual(hc.Env, wc.Env) {
		return false
	}
	if !mountsEqual(hc.VolumeMounts, wc.VolumeMounts) {
		return false
	}
	if !portsEqual(hc.Ports, wc.Ports) {
		return false
	}
	if !probeEqual(hc.ReadinessProbe, wc.ReadinessProbe) ||
		!probeEqual(hc.LivenessProbe, wc.LivenessProbe) ||
		!probeEqual(hc.StartupProbe, wc.StartupProbe) {
		return false
	}
	return resourcesEqual(hc.Resources, wc.Resources)
}

// probePath extracts the HTTP path from a probe ("" when absent).
func probePath(p *corev1.Probe) string {
	if p == nil || p.HTTPGet == nil {
		return ""
	}
	return p.HTTPGet.Path
}

// deploymentContainer Returns the kubeswap-managed container of a Deployment (the named one, else the first).
func deploymentContainer(dep *appsv1.Deployment) *corev1.Container {
	containers := dep.Spec.Template.Spec.Containers
	for i := range containers {
		if containers[i].Name == containerName {
			return &containers[i]
		}
	}
	if len(containers) > 0 {
		return &containers[0]
	}
	return nil
}

// stringSlicesEqual Reports whether two string slices are element-for-element equal.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// envsEqual Reports whether two env var lists define the same name-to-value mapping, ignoring order.
func envsEqual(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	byKey := func(vars []corev1.EnvVar) map[string]string {
		m := make(map[string]string, len(vars))
		for _, v := range vars {
			m[v.Name] = v.Value
		}
		return m
	}
	ma, mb := byKey(a), byKey(b)
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// resourcesEqual Reports whether two resource requirement sets are equal (limits and requests).
func resourcesEqual(a, b corev1.ResourceRequirements) bool {
	return resourceListEqual(a.Limits, b.Limits) && resourceListEqual(a.Requests, b.Requests)
}

// resourceListEqual Reports whether two resource lists hold the same quantities.
func resourceListEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !b[k].Equal(v) {
			return false
		}
	}
	return true
}

// derefInt64 dereferences a *int64 (nil counts as 0).
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// stringMapEqual compares two string maps.
func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// probeEqual compares the configuration-owned probe fields: the endpoint
// (path + port, the port from --port), the request timeout
// (--probe-timeout) and the timing (initial delay, period, failure
// threshold) that kubeswap explicitly configures.
func probeEqual(a, b *corev1.Probe) bool {
	if a == nil || b == nil {
		return a == b
	}
	aHas, bHas := a.HTTPGet != nil, b.HTTPGet != nil
	if aHas != bHas || probePath(a) != probePath(b) {
		return false
	}
	if aHas && a.HTTPGet.Port != b.HTTPGet.Port {
		return false
	}
	return a.InitialDelaySeconds == b.InitialDelaySeconds &&
		a.PeriodSeconds == b.PeriodSeconds &&
		a.TimeoutSeconds == b.TimeoutSeconds &&
		a.FailureThreshold == b.FailureThreshold
}

// portsEqual compares container ports by name (order-insensitive); the
// primary http port and every --service-port entry are configuration-owned.
func portsEqual(a, b []corev1.ContainerPort) bool {
	byName := func(ports []corev1.ContainerPort) map[string]int32 {
		m := make(map[string]int32, len(ports))
		for _, p := range ports {
			m[p.Name] = p.ContainerPort
		}
		return m
	}
	ma, mb := byName(a), byName(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// mountsEqual compares volume mounts by name (order-insensitive).
func mountsEqual(a, b []corev1.VolumeMount) bool {
	type mountKey struct {
		path     string
		readOnly bool
	}
	byName := func(mounts []corev1.VolumeMount) map[string]mountKey {
		m := make(map[string]mountKey, len(mounts))
		for _, v := range mounts {
			m[v.Name] = mountKey{v.MountPath, v.ReadOnly}
		}
		return m
	}
	ma, mb := byName(a), byName(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if mb[k] != v {
			return false
		}
	}
	return true
}

// volumesEqual compares pod volumes by name and source (order-insensitive).
func volumesEqual(a, b []corev1.Volume) bool {
	source := func(v corev1.Volume) string {
		switch {
		case v.PersistentVolumeClaim != nil:
			return "pvc:" + v.PersistentVolumeClaim.ClaimName
		case v.HostPath != nil:
			return "host:" + v.HostPath.Path
		case v.EmptyDir != nil:
			return "emptydir"
		default:
			return v.Name + ":unknown-source"
		}
	}
	ma := make(map[string]string, len(a))
	for _, v := range a {
		ma[v.Name] = source(v)
	}
	if len(ma) != len(b) {
		return false
	}
	for _, v := range b {
		if ma[v.Name] != source(v) {
			return false
		}
	}
	return true
}

// tolerationsEqual compares tolerations as multisets (order-insensitive).
func tolerationsEqual(a, b []corev1.Toleration) bool {
	type tolKey struct {
		key, op, value, effect string
		seconds                int64
	}
	byKey := func(list []corev1.Toleration) map[tolKey]int {
		m := make(map[tolKey]int, len(list))
		for _, t := range list {
			k := tolKey{t.Key, string(t.Operator), t.Value, string(t.Effect), derefInt64(t.TolerationSeconds)}
			m[k]++
		}
		return m
	}
	ma, mb := byKey(a), byKey(b)
	if len(ma) != len(mb) {
		return false
	}
	for k, n := range ma {
		if mb[k] != n {
			return false
		}
	}
	return true
}
