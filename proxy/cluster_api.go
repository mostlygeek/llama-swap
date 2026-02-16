package proxy

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type clusterStopResponse struct {
	Message string `json:"message"`
	Script  string `json:"script"`
	Output  string `json:"output,omitempty"`
}

func (pm *ProxyManager) apiStopCluster(c *gin.Context) {
	// Always unload currently managed llama-swap processes first.
	pm.StopProcesses(StopImmediately)

	scriptPath := filepath.Join(recipesBackendDir(), "launch-cluster.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":  fmt.Sprintf("cluster script not found: %s", scriptPath),
			"script": scriptPath,
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", scriptPath, "stop")
	output, err := cmd.CombinedOutput()
	outputText := strings.TrimSpace(string(output))

	if ctx.Err() == context.DeadlineExceeded {
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error":  "cluster stop timed out after 2m",
			"script": scriptPath,
			"output": outputText,
		})
		return
	}

	if err != nil {
		pm.proxyLogger.Errorf("cluster stop failed: %v output=%s", err, outputText)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  fmt.Sprintf("cluster stop failed: %v", err),
			"script": scriptPath,
			"output": outputText,
		})
		return
	}

	if outputText != "" {
		pm.proxyLogger.Infof("cluster stop output: %s", outputText)
	}

	c.JSON(http.StatusOK, clusterStopResponse{
		Message: "llama-swap processes unloaded and cluster stop executed",
		Script:  scriptPath,
		Output:  outputText,
	})
}
