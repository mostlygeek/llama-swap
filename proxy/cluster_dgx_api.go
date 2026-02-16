package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	dgxDashboardServiceUser = "dgx-dashboard-service-user"
	dgxDashboardDBusService = "com.nvidia.dgx.dashboard.admin1"
	dgxDashboardDBusObject  = "/com/nvidia/dgx/dashboard/admin"
	dgxDashboardDBusIface   = "com.nvidia.dgx.dashboard.admin1"
)

type clusterDGXUpdateRequest struct {
	Targets []string `json:"targets"`
}

type clusterDGXUpdateNodeResult struct {
	IP         string `json:"ip"`
	IsLocal    bool   `json:"isLocal"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"durationMs"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type clusterDGXUpdateResponse struct {
	Action      string                       `json:"action"`
	StartedAt   string                       `json:"startedAt"`
	CompletedAt string                       `json:"completedAt"`
	Success     int                          `json:"success"`
	Failed      int                          `json:"failed"`
	Results     []clusterDGXUpdateNodeResult `json:"results"`
}

func (pm *ProxyManager) apiRunClusterDGXUpdate(c *gin.Context) {
	var req clusterDGXUpdateRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid JSON body: " + err.Error(),
			})
			return
		}
	}

	discoverCtx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	targets, localIP, err := discoverClusterNodeIPs(discoverCtx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": err.Error(),
		})
		return
	}
	allowedTargets := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		allowedTargets[target] = struct{}{}
	}

	if len(req.Targets) > 0 {
		requested := uniqueNonEmptyStrings(req.Targets)
		filtered := make([]string, 0, len(requested))
		for _, target := range requested {
			if _, ok := allowedTargets[target]; ok {
				filtered = append(filtered, target)
			}
		}
		targets = filtered
	}
	if len(targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "no target nodes resolved for DGX update action",
		})
		return
	}

	startedAt := time.Now().UTC()
	results := make([]clusterDGXUpdateNodeResult, len(targets))

	var wg sync.WaitGroup
	for idx, host := range targets {
		idx := idx
		host := host
		isLocal := host == localIP

		wg.Add(1)
		go func() {
			defer wg.Done()
			actionCtx, actionCancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer actionCancel()

			start := time.Now()
			output, runErr := runClusterNodeShell(actionCtx, host, isLocal, dgxUpdateAndRebootScript())

			results[idx] = clusterDGXUpdateNodeResult{
				IP:         host,
				IsLocal:    isLocal,
				OK:         runErr == nil,
				DurationMs: time.Since(start).Milliseconds(),
				Output:     strings.TrimSpace(output),
			}
			if runErr != nil {
				results[idx].Error = runErr.Error()
			}
		}()
	}
	wg.Wait()

	success := 0
	for _, r := range results {
		if r.OK {
			success++
		}
	}

	c.JSON(http.StatusOK, clusterDGXUpdateResponse{
		Action:      "update_and_reboot",
		StartedAt:   startedAt.Format(time.RFC3339),
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
		Success:     success,
		Failed:      len(results) - success,
		Results:     results,
	})
}

func populateClusterDGXStatus(parentCtx context.Context, nodes []clusterNodeStatus) {
	var wg sync.WaitGroup
	for idx := range nodes {
		idx := idx
		node := nodes[idx]

		if !node.IsLocal && !node.SSHOK {
			nodes[idx].DGX = &clusterDGXStatus{
				Supported: false,
				CheckedAt: time.Now().UTC().Format(time.RFC3339),
				Error:     "ssh not available",
			}
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes[idx].DGX = queryNodeDGXStatus(parentCtx, node.IP, node.IsLocal)
		}()
	}
	wg.Wait()
}

func queryNodeDGXStatus(parentCtx context.Context, host string, isLocal bool) *clusterDGXStatus {
	status := &clusterDGXStatus{
		Supported: false,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	availableRaw, availableErr := callNodeDGXDBusMethod(parentCtx, host, isLocal, "GetUpdatesAvailable", 20*time.Second)
	if availableErr != nil {
		status.Error = availableErr.Error()
		return status
	}
	available, parseErr := parseDBusBoolResult(availableRaw)
	if parseErr != nil {
		status.Error = "GetUpdatesAvailable parse error: " + parseErr.Error()
		return status
	}
	status.Supported = true
	status.UpdateAvailable = &available

	rebootRaw, rebootErr := callNodeDGXDBusMethod(parentCtx, host, isLocal, "IsUpdateRebootRunning", 10*time.Second)
	if rebootErr == nil {
		if rebootRunning, err := parseDBusBoolResult(rebootRaw); err == nil {
			status.RebootRunning = &rebootRunning
		}
	}

	upgradeRaw, upgradeErr := callNodeDGXDBusMethod(parentCtx, host, isLocal, "StatusUpgrade", 10*time.Second)
	if upgradeErr == nil {
		if progress, state, err := parseDBusIntStringResult(upgradeRaw); err == nil {
			status.UpgradeProgress = &progress
			status.UpgradeStatus = state
		}
	}

	cacheRaw, cacheErr := callNodeDGXDBusMethod(parentCtx, host, isLocal, "StatusUpdateCache", 10*time.Second)
	if cacheErr == nil {
		if progress, state, err := parseDBusIntStringResult(cacheRaw); err == nil {
			status.CacheProgress = &progress
			status.CacheStatus = state
		}
	}

	return status
}

func callNodeDGXDBusMethod(parentCtx context.Context, host string, isLocal bool, method string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	script := fmt.Sprintf(
		"sudo -n -u %s busctl call %s %s %s %s",
		dgxDashboardServiceUser,
		dgxDashboardDBusService,
		dgxDashboardDBusObject,
		dgxDashboardDBusIface,
		method,
	)
	output, err := runClusterNodeShell(ctx, host, isLocal, script)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func runClusterNodeShell(ctx context.Context, host string, isLocal bool, script string) (string, error) {
	var cmd *exec.Cmd
	if isLocal {
		cmd = exec.CommandContext(ctx, "bash", "-lc", script)
	} else {
		cmd = exec.CommandContext(
			ctx,
			"ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=8",
			"-o", "StrictHostKeyChecking=no",
			host,
			fmt.Sprintf("bash -lc %s", shellQuote(script)),
		)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(output.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(output.String()), nil
}

func discoverClusterNodeIPs(parentCtx context.Context) ([]string, string, error) {
	autodiscoverPath := clusterAutodiscoverPath()
	values, detectErrors := runAutodiscoverSnapshot(parentCtx, autodiscoverPath)
	localIP := strings.TrimSpace(values["LOCAL_IP"])
	nodes := parseNodesArg(values["NODES_ARG"])
	if localIP != "" && !containsString(nodes, localIP) {
		nodes = append([]string{localIP}, nodes...)
	}
	if len(nodes) == 0 && localIP != "" {
		nodes = []string{localIP}
	}

	if len(nodes) == 0 {
		detail := strings.Join(detectErrors, "; ")
		if detail == "" {
			detail = "no nodes found"
		}
		return nil, localIP, fmt.Errorf("unable to resolve cluster nodes: %s", detail)
	}

	return uniqueNonEmptyStrings(nodes), localIP, nil
}

func parseDBusBoolResult(raw string) (bool, error) {
	line := firstNonEmptyLine(raw)
	if line == "" {
		return false, fmt.Errorf("empty output")
	}
	if !strings.HasPrefix(line, "b ") {
		return false, fmt.Errorf("unexpected format: %s", line)
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "b "))
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected boolean value: %s", value)
	}
}

func parseDBusIntStringResult(raw string) (int, string, error) {
	line := firstNonEmptyLine(raw)
	if line == "" {
		return 0, "", fmt.Errorf("empty output")
	}
	if !strings.HasPrefix(line, "is ") {
		return 0, "", fmt.Errorf("unexpected format: %s", line)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "is "))
	firstSpace := strings.Index(rest, " ")
	if firstSpace <= 0 {
		return 0, "", fmt.Errorf("invalid integer/string pair: %s", line)
	}
	progressRaw := strings.TrimSpace(rest[:firstSpace])
	progress, err := strconv.Atoi(progressRaw)
	if err != nil {
		return 0, "", fmt.Errorf("invalid progress value: %w", err)
	}

	stateRaw := strings.TrimSpace(rest[firstSpace+1:])
	stateRaw = strings.TrimPrefix(stateRaw, "\"")
	stateRaw = strings.TrimSuffix(stateRaw, "\"")
	return progress, stateRaw, nil
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func uniqueNonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func dgxUpdateAndRebootScript() string {
	return fmt.Sprintf(
		"sudo -n -u %s busctl call %s %s %s UpdateAndReboot",
		dgxDashboardServiceUser,
		dgxDashboardDBusService,
		dgxDashboardDBusObject,
		dgxDashboardDBusIface,
	)
}
