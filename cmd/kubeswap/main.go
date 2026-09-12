// kubeswap is a cmd/cmdStop wrapper that lets llama-swap manage inference
// backends in a Kubernetes namespace the same way it manages docker backends:
// the model's cmd launches `kubeswap serve`, which creates (or adopts) a
// Deployment + Service for the model and proxies a local port to it; the
// model's cmdStop runs `kubeswap delete` to tear the objects down.
//
// Subcommands:
//
//	serve   long-running: lifecycle + local proxy (used as a model's cmd)
//	delete  one-shot: delete a model's managed objects (used as cmdStop)
//	gc      one-shot: delete managed objects for models not in the config
//	status  one-shot: print the managed workloads
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultNamespace      = "llama-swap"
	containerName         = "server"
	defaultPort           = 8080
	defaultStartupTimeout = int64(600) // seconds of model loading the startup probe tolerates
)

// Set at build time via -ldflags (see the Makefile kubeswap target and
// docker/unified/install-kubeswap.sh).
var (
	version   = "dev"
	buildTime = ""
)

func printVersion() {
	suffix := ""
	if buildTime != "" {
		suffix = ", built " + buildTime
	}
	fmt.Printf("kubeswap %s (go %s%s)\n", version, runtime.Version(), suffix)
}

// managed-object labels/annotations. Every object kubeswap creates carries
// these so `gc` and adoption can find them later.
const (
	labelManagedBy    = "llama-swap.io/managed-by"
	labelModel        = "llama-swap.io/model"
	labelDeployment   = "llama-swap.io/deployment"
	labelAppName      = "app.kubernetes.io/name"
	managedByValue    = "llama-swap"
	appNameValue      = "llama-swap-backend"
	annotationModelID = "llama-swap.io/model-id"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serveCmd(os.Args[2:])
	case "delete":
		err = deleteCmd(os.Args[2:])
	case "gc":
		err = gcCmd(os.Args[2:])
	case "status":
		err = statusCmd(os.Args[2:])
	case "version":
		printVersion()
		return
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "kubeswap: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubeswap %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `kubeswap - manage llama-swap inference backends in a Kubernetes namespace

Usage:
  kubeswap serve [flags] -- <container args...>
  kubeswap delete --model <id> [flags]
  kubeswap gc [flags]
  kubeswap status [flags]
  kubeswap version

Commands:
  serve    Start as a forward proxy for a model's backend (a model's cmd).
           Creates or adopts the model's Deployment + Service and proxies
           --listen to the backend pod until stopped. SIGTERM exits without
           deleting anything (the backend is kept for adoption on restart).
  delete   Delete a model's Deployment/Service (and PVCs with
           --delete-volumes). Used as a model's cmdStop; a running serve
           process exits on its own when it observes the deletion.
  gc       Delete managed workloads whose model is not in the given list
           (--models or --config pointing at a llama-swap config.yaml).
  status   Print the managed workloads in the namespace.
  version  Print version and build information.

Run "kubeswap <command> -h" for command flags.
`)
}

// stringList is a repeatable flag value: --env K=V --env K2=V2.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// newFlagSet builds a per-command flag set with the shared kube flags.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: kubeswap %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	return fs
}

func addKubeFlags(fs *flag.FlagSet, namespace, kubeconfig *string) {
	fs.StringVar(namespace, "namespace", defaultNamespace, "Kubernetes namespace to manage")
	fs.StringVar(kubeconfig, "kubeconfig", "", "Path to a kubeconfig (default: in-cluster config, then KUBECONFIG / ~/.kube/config)")
}

// envVar parses "K=V" entries (V may contain '=').
func parseEnvVars(entries []string) (out []envVar, err error) {
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q (want K=V)", e)
		}
		out = append(out, envVar{Key: k, Value: v})
	}
	return out, nil
}

// envVar is a plain key/value so the renderer stays decoupled from corev1 in
// parse code paths (it is converted in the renderer).
type envVar struct {
	Key   string
	Value string
}

// parseKeyValues parses repeated "K=V" entries (values may contain '=').
func parseKeyValues(entries []string, what string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --%s %q (want K=V)", what, e)
		}
		out[k] = v
	}
	return out, nil
}

// parseGPUs parses "resource=count" entries like "amd.com/gpu=1"; the count
// is a positive whole number — GPUs are exclusive, non-shareable devices, so
// fractions and words like "many" are errors, not quantities.
func parseGPUs(entries []string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range entries {
		res, count, ok := strings.Cut(e, "=")
		if !ok || res == "" || count == "" {
			return nil, fmt.Errorf("invalid --gpu %q (want resource=count, e.g. amd.com/gpu=1)", e)
		}
		n, err := strconv.ParseInt(count, 10, 32)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid --gpu %q: count %q must be a positive whole number", e, count)
		}
		out[res] = count
	}
	return out, nil
}

// parseResources parses repeated "name=quantity" entries for resource
// requests or limits, validating the quantities up front.
func parseResources(entries []string, what string) (map[string]string, error) {
	m, err := parseKeyValues(entries, what)
	if err != nil {
		return nil, err
	}
	for k, v := range m {
		if _, err := resource.ParseQuantity(v); err != nil {
			return nil, fmt.Errorf("invalid --%s quantity %s=%q: %v", what, k, v, err)
		}
	}
	return m, nil
}

// servicePortSpec is one parsed --service-port entry: name:port.
type servicePortSpec struct {
	Name string
	Port int32
}

// validPortName reports whether s is a lowercase RFC 1123 label (a legal
// Service port name).
func validPortName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i != 0 && i != len(s)-1:
		default:
			return false
		}
	}
	return true
}

// parseServicePorts parses "name:port" entries, rejecting duplicate names.
// Container port names must be unique within the container, so the same
// name on a different port is a duplicate too — renderPorts would emit
// two ports with one name and the API server would reject the Deployment.
func parseServicePorts(entries []string) ([]servicePortSpec, error) {
	out := make([]servicePortSpec, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		name, portS, ok := strings.Cut(e, ":")
		if !ok || portS == "" {
			return nil, fmt.Errorf("invalid --service-port %q (want name:port)", e)
		}
		if !validPortName(name) {
			return nil, fmt.Errorf("invalid --service-port name %q (want lowercase alphanumerics and dashes)", name)
		}
		port, err := strconv.Atoi(portS)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid --service-port port %q (want 1-65535)", portS)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate --service-port %q", e)
		}
		seen[name] = true
		out = append(out, servicePortSpec{Name: name, Port: int32(port)})
	}
	return out, nil
}

// parseTolerations parses "key:operator:value:effect" entries, validating
// against the values Kubernetes accepts: operator is Equal or Exists (empty
// defaults to Equal), effect is NoSchedule, PreferNoSchedule or NoExecute —
// or empty, which tolerates every effect (e.g. "dedicated:Exists::" or the
// tolerate-everything "::Exists:"). A value is an error with operator
// Exists. Invalid fields are rejected here, before renderTolerations turns
// them into corev1 types ("All" was never a valid effect).
func parseTolerations(entries []string) ([]toleration, error) {
	out := make([]toleration, 0, len(entries))
	for _, e := range entries {
		parts := strings.Split(e, ":")
		if len(parts) != 4 {
			return nil, fmt.Errorf("invalid --toleration %q (want key:operator:value:effect)", e)
		}
		key, op, value, effect := parts[0], parts[1], parts[2], parts[3]
		if op == "" {
			op = "Equal"
		}
		switch op {
		case "Equal", "Exists":
		default:
			return nil, fmt.Errorf("invalid --toleration %q: operator %q (want Equal or Exists)", e, op)
		}
		switch effect {
		case "", "NoSchedule", "PreferNoSchedule", "NoExecute":
		default:
			return nil, fmt.Errorf("invalid --toleration %q: effect %q (want NoSchedule, PreferNoSchedule, NoExecute, or empty for any effect)", e, effect)
		}
		if op == "Exists" && value != "" {
			return nil, fmt.Errorf("invalid --toleration %q: value must be empty when operator is Exists", e)
		}
		out = append(out, toleration{
			Key: key, Operator: op, Value: value, Effect: effect,
		})
	}
	return out, nil
}

type toleration struct {
	Key      string
	Operator string
	Value    string
	Effect   string
}

// volumeKind is a parsed --volume kind prefix.
type volumeKind string

const (
	volPVC      volumeKind = "pvc"
	volEmptyDir volumeKind = "emptydir"
	volHostPath volumeKind = "hostpath"
)

// volumeSpec is one parsed --volume entry: kind:name:path[:ro].
type volumeSpec struct {
	Kind     volumeKind
	Name     string
	Path     string
	ReadOnly bool
}

// parseVolumes parses "kind:name:path[:ro]" entries. kind is pvc, emptydir or
// hostpath; for hostpath the "name" field is the path on the host.
func parseVolumes(entries []string) ([]volumeSpec, error) {
	out := make([]volumeSpec, 0, len(entries))
	for _, e := range entries {
		fields := strings.Split(e, ":")
		if len(fields) < 3 || len(fields) > 4 {
			return nil, fmt.Errorf("invalid --volume %q (want pvc|emptydir|hostpath:name:path[:ro])", e)
		}
		kind := volumeKind(fields[0])
		switch kind {
		case volPVC, volEmptyDir, volHostPath:
		default:
			return nil, fmt.Errorf("invalid --volume kind %q (want pvc, emptydir or hostpath)", fields[0])
		}
		if fields[1] == "" || fields[2] == "" {
			return nil, fmt.Errorf("invalid --volume %q (name and mount path are required)", e)
		}
		spec := volumeSpec{Kind: kind, Name: fields[1], Path: fields[2]}
		if len(fields) == 4 {
			if fields[3] != "ro" {
				return nil, fmt.Errorf("invalid --volume %q (access mode must be 'ro')", e)
			}
			spec.ReadOnly = true
		}
		out = append(out, spec)
	}
	return out, nil
}

// sortedKeys returns keys in stable order (deterministic manifests).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
