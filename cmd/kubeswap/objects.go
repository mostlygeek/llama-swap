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

	podTemplateLabels := podLabels(c.Sanitized)
	for k, v := range c.ExtraLabels {
		podTemplateLabels[k] = v
	}

	container := corev1.Container{
		Name:      containerName,
		Image:     c.Image,
		Command:   c.Command,
		Args:      c.Args,
		Ports:     c.renderPorts(),
		Env:       c.renderEnv(),
		Resources: c.renderResources(),
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

	annotations := map[string]string{annotationModelID: c.Model}
	recreate := appsv1.RecreateDeploymentStrategyType
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        deploymentName(c.Sanitized),
			Namespace:   c.Namespace,
			Labels:      depLabels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{Type: recreate},
			Selector: &metav1.LabelSelector{MatchLabels: managedLabels(c.Sanitized)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podTemplateLabels,
					Annotations: map[string]string{annotationModelID: c.Model},
				},
				Spec: corev1.PodSpec{
					Containers:                    []corev1.Container{container},
					Volumes:                       volumes,
					NodeSelector:                  c.NodeSel,
					Tolerations:                   c.renderTolerations(),
					TerminationGracePeriodSeconds: &c.GraceSeconds,
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
func (c *serveConfig) renderResources() corev1.ResourceRequirements {
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}
	for _, k := range sortedKeys(c.GPUs) {
		qty, err := resource.ParseQuantity(c.GPUs[k])
		if err != nil {
			// An unparseable quantity is surfaced at parse time already;
			// fall back to a zero quantity so rendering cannot fail.
			qty = resource.MustParse("0")
		}
		requests[corev1.ResourceName(k)] = qty
		limits[corev1.ResourceName(k)] = qty
	}
	for _, k := range sortedKeys(c.Requests) {
		if qty, err := resource.ParseQuantity(c.Requests[k]); err == nil {
			requests[corev1.ResourceName(k)] = qty
		}
	}
	for _, k := range sortedKeys(c.Limits) {
		if qty, err := resource.ParseQuantity(c.Limits[k]); err == nil {
			limits[corev1.ResourceName(k)] = qty
		}
	}
	if len(requests) == 0 && len(limits) == 0 {
		return corev1.ResourceRequirements{}
	}
	return corev1.ResourceRequirements{Requests: requests, Limits: limits}
}

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
			Name:        serviceName(c.Sanitized),
			Namespace:   c.Namespace,
			Labels:      managedLabels(c.Sanitized),
			Annotations: map[string]string{annotationModelID: c.Model},
		},
		Spec: corev1.ServiceSpec{
			Selector: managedLabels(c.Sanitized),
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

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deploymentSpecMatches reports whether an existing deployment's container
// spec matches the desired one (used by --strict adoption).
func deploymentSpecMatches(have *appsv1.Deployment, want *appsv1.Deployment) bool {
	if have == nil || want == nil {
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
	if probePath(hc.ReadinessProbe) != probePath(wc.ReadinessProbe) ||
		probePath(hc.LivenessProbe) != probePath(wc.LivenessProbe) {
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

func resourcesEqual(a, b corev1.ResourceRequirements) bool {
	return resourceListEqual(a.Limits, b.Limits) && resourceListEqual(a.Requests, b.Requests)
}

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
