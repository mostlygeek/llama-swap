//go:build !windows

package proxy

import (
	"fmt"
	"net/http"
	"syscall"

	"github.com/gin-gonic/gin"
)

// apiGetStorage implements GET /api/storage.
// Returns disk space for the models directory (configured or inferred).
func (pm *ProxyManager) apiGetStorage(c *gin.Context) {
	dir := pm.modelsDir()
	if dir == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "models directory unknown; set modelsDir in config or use --models-dir flag",
		})
		return
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("statfs %s: %v", dir, err)})
		return
	}

	blockSize := uint64(stat.Bsize)
	c.JSON(http.StatusOK, gin.H{
		"models_dir":      dir,
		"total_bytes":     stat.Blocks * blockSize,
		"available_bytes": stat.Bavail * blockSize,
		"used_bytes":      (stat.Blocks - stat.Bfree) * blockSize,
	})
}
