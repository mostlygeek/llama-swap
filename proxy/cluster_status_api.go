package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	clusterAutodiscoverPathEnv = "LLAMA_SWAP_CLUSTER_AUTODISCOVER_PATH"
	clusterKVPrefix            = "__KV__"
)

type clusterNodeStatus struct {
	IP            string `json:"ip"`
	IsLocal       bool   `json:"isLocal"`
	Port22Open    bool   `json:"port22Open"`
	Port22Latency int64  `json:"port22LatencyMs,omitempty"`
	SSHOK         bool   `json:"sshOk"`
	SSHLatency    int64  `json:"sshLatencyMs,omitempty"`
	Error         string `json:"error,omitempty"`
	DGX           *clusterDGXStatus `json:"dgx,omitempty"`
}

type clusterDGXStatus struct {
	Supported       bool   `json:"supported"`
	CheckedAt       string `json:"checkedAt"`
	UpdateAvailable *bool  `json:"updateAvailable,omitempty"`
	RebootRunning   *bool  `json:"rebootRunning,omitempty"`
	UpgradeProgress *int   `json:"upgradeProgress,omitempty"`
	UpgradeStatus   string `json:"upgradeStatus,omitempty"`
	CacheProgress   *int   `json:"cacheProgress,omitempty"`
	CacheStatus     string `json:"cacheStatus,omitempty"`
	Error           string `json:"error,omitempty"`
}

type clusterStatusState struct {
	BackendDir       string              `json:"backendDir"`
	AutodiscoverPath string              `json:"autodiscoverPath"`
	DetectedAt       string              `json:"detectedAt"`
	LocalIP          string              `json:"localIp"`
	CIDR             string              `json:"cidr"`
	EthIF            string              `json:"ethIf"`
	IbIF             string              `json:"ibIf"`
	NodeCount        int                 `json:"nodeCount"`
	RemoteCount      int                 `json:"remoteCount"`
	ReachableBySSH   int                 `json:"reachableBySsh"`
	Overall          string              `json:"overall"`
	Summary          string              `json:"summary"`
	Errors           []string            `json:"errors,omitempty"`
	Nodes            []clusterNodeStatus `json:"nodes"`
}

func (pm *ProxyManager) apiGetClusterStatus(c *gin.Context) {
	state, err := pm.readClusterStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"backendDir":       recipesBackendDir(),
			"autodiscoverPath": clusterAutodiscoverPath(),
		})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) readClusterStatus(parentCtx context.Context) (clusterStatusState, error) {
	backendDir := recipesBackendDir()
	autodiscoverPath := clusterAutodiscoverPath()
	if stat, err := os.Stat(autodiscoverPath); err != nil || stat.IsDir() {
		return clusterStatusState{}, fmt.Errorf(
			"autodiscover.sh not found: %s (set %s or %s)",
			autodiscoverPath,
			clusterAutodiscoverPathEnv,
			recipesBackendDirEnv,
		)
	}

	ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)
	defer cancel()

	values, detectErrors := runAutodiscoverSnapshot(ctx, autodiscoverPath)
	nodes := parseNodesArg(values["NODES_ARG"])
	localIP := strings.TrimSpace(values["LOCAL_IP"])
	if localIP != "" && !containsString(nodes, localIP) {
		nodes = append([]string{localIP}, nodes...)
	}
	if len(nodes) == 0 && localIP != "" {
		nodes = []string{localIP}
	}

	nodeStatuses := make([]clusterNodeStatus, len(nodes))
	var wg sync.WaitGroup
	for idx := range nodes {
		idx := idx
		node := nodes[idx]
		nodeStatuses[idx] = clusterNodeStatus{
			IP:      node,
			IsLocal: node == localIP,
		}

		if nodeStatuses[idx].IsLocal {
			nodeStatuses[idx].Port22Open = true
			nodeStatuses[idx].SSHOK = true
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			errParts := make([]string, 0, 2)

			p22ok, p22lat, p22err := probePort22(node, 2*time.Second)
			nodeStatuses[idx].Port22Open = p22ok
			nodeStatuses[idx].Port22Latency = p22lat
			if p22err != nil {
				errParts = append(errParts, "port22: "+p22err.Error())
			}

			sshOK, sshLat, sshErr := probeSSH(ctx, node, 8*time.Second)
			nodeStatuses[idx].SSHOK = sshOK
			nodeStatuses[idx].SSHLatency = sshLat
			if sshErr != nil {
				errParts = append(errParts, "ssh: "+sshErr.Error())
			}

			if len(errParts) > 0 {
				nodeStatuses[idx].Error = strings.Join(errParts, "; ")
			}
		}()
	}
	wg.Wait()
	populateClusterDGXStatus(ctx, nodeStatuses)

	sort.Slice(nodeStatuses, func(i, j int) bool {
		if nodeStatuses[i].IsLocal != nodeStatuses[j].IsLocal {
			return nodeStatuses[i].IsLocal
		}
		return nodeStatuses[i].IP < nodeStatuses[j].IP
	})

	reachableBySSH := 0
	remoteCount := 0
	for _, n := range nodeStatuses {
		if !n.IsLocal {
			remoteCount++
		}
		if n.SSHOK {
			reachableBySSH++
		}
	}

	overall := "healthy"
	switch {
	case len(nodeStatuses) == 0:
		overall = "error"
	case remoteCount == 0:
		overall = "solo"
	}

	for _, n := range nodeStatuses {
		if !n.IsLocal && (!n.Port22Open || !n.SSHOK) {
			overall = "degraded"
			break
		}
	}
	if len(detectErrors) > 0 && overall == "healthy" {
		overall = "degraded"
	}

	summary := buildClusterSummary(overall, len(nodeStatuses), remoteCount, reachableBySSH, detectErrors)
	return clusterStatusState{
		BackendDir:       backendDir,
		AutodiscoverPath: autodiscoverPath,
		DetectedAt:       time.Now().UTC().Format(time.RFC3339),
		LocalIP:          localIP,
		CIDR:             strings.TrimSpace(values["CIDR"]),
		EthIF:            strings.TrimSpace(values["ETH_IF"]),
		IbIF:             strings.TrimSpace(values["IB_IF"]),
		NodeCount:        len(nodeStatuses),
		RemoteCount:      remoteCount,
		ReachableBySSH:   reachableBySSH,
		Overall:          overall,
		Summary:          summary,
		Errors:           detectErrors,
		Nodes:            nodeStatuses,
	}, nil
}

func clusterAutodiscoverPath() string {
	if v := strings.TrimSpace(os.Getenv(clusterAutodiscoverPathEnv)); v != "" {
		return v
	}
	return filepath.Join(recipesBackendDir(), "autodiscover.sh")
}

func runAutodiscoverSnapshot(ctx context.Context, autodiscoverPath string) (map[string]string, []string) {
	script := strings.Join([]string{
		"set +e",
		fmt.Sprintf("source %s", shellQuote(autodiscoverPath)),
		fmt.Sprintf("kv(){ printf '%s%%s=%%s\\n' \"$1\" \"$2\"; }", clusterKVPrefix),
		"detect_interfaces; _RC_IF=$?",
		"detect_local_ip; _RC_LOCAL=$?",
		"detect_nodes; _RC_NODES=$?",
		"kv DETECT_INTERFACES_RC \"${_RC_IF}\"",
		"kv DETECT_LOCAL_IP_RC \"${_RC_LOCAL}\"",
		"kv DETECT_NODES_RC \"${_RC_NODES}\"",
		"kv LOCAL_IP \"${LOCAL_IP:-}\"",
		"kv ETH_IF \"${ETH_IF:-}\"",
		"kv IB_IF \"${IB_IF:-}\"",
		"kv CIDR \"${CIDR:-}\"",
		"kv NODES_ARG \"${NODES_ARG:-}\"",
	}, "\n")

	cmd := exec.CommandContext(ctx, "bash", "-lc", script)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()

	values := make(map[string]string, 16)
	detectErrors := make([]string, 0, 4)
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, clusterKVPrefix) {
			continue
		}

		kv := strings.TrimPrefix(line, clusterKVPrefix)
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	if runErr != nil && !errorsIsContextCanceled(runErr) {
		detectErrors = append(detectErrors, "autodiscover command failed: "+runErr.Error())
	}
	appendDetectRCError(&detectErrors, "detect_interfaces", values["DETECT_INTERFACES_RC"])
	appendDetectRCError(&detectErrors, "detect_local_ip", values["DETECT_LOCAL_IP_RC"])
	appendDetectRCError(&detectErrors, "detect_nodes", values["DETECT_NODES_RC"])

	return values, detectErrors
}

func appendDetectRCError(errors *[]string, stepName, rcRaw string) {
	if strings.TrimSpace(rcRaw) == "" {
		return
	}
	rc, err := strconv.Atoi(strings.TrimSpace(rcRaw))
	if err != nil {
		return
	}
	if rc != 0 {
		*errors = append(*errors, fmt.Sprintf("%s failed (exit %d)", stepName, rc))
	}
}

func parseNodesArg(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		node := strings.TrimSpace(p)
		if node == "" {
			continue
		}
		if _, ok := seen[node]; ok {
			continue
		}
		seen[node] = struct{}{}
		out = append(out, node)
	}
	return out
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func probePort22(host string, timeout time.Duration) (ok bool, latencyMs int64, err error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), timeout)
	latencyMs = time.Since(start).Milliseconds()
	if err != nil {
		return false, latencyMs, err
	}
	_ = conn.Close()
	return true, latencyMs, nil
}

func probeSSH(parent context.Context, host string, timeout time.Duration) (ok bool, latencyMs int64, err error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(
		ctx,
		"ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		host,
		"true",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	latencyMs = time.Since(start).Milliseconds()
	if runErr != nil {
		msg := strings.TrimSpace(output.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return false, latencyMs, fmt.Errorf("%s", msg)
	}
	return true, latencyMs, nil
}

func buildClusterSummary(overall string, nodeCount, remoteCount, reachableBySSH int, detectErrors []string) string {
	switch overall {
	case "solo":
		return fmt.Sprintf("Modo solo: %d nodo local detectado.", nodeCount)
	case "healthy":
		return fmt.Sprintf("Cluster OK: %d/%d nodos con SSH operativo.", reachableBySSH, nodeCount)
	case "degraded":
		if len(detectErrors) > 0 {
			return fmt.Sprintf("Cluster degradado: %d aviso(s) de autodetección y %d/%d nodos con SSH operativo.", len(detectErrors), reachableBySSH, nodeCount)
		}
		return fmt.Sprintf("Cluster degradado: %d nodo(s) remoto(s), SSH operativo en %d/%d nodos.", remoteCount, reachableBySSH, nodeCount)
	default:
		return "No se pudo determinar el estado del cluster."
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func errorsIsContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "context canceled")
}
