//go:build windows

package proxy

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// apiGetStorage implements GET /api/storage.
func (pm *ProxyManager) apiGetStorage(c *gin.Context) {
	dir := pm.modelsDir()
	if dir == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "models directory unknown; set modelsDir in config or use --models-dir flag",
		})
		return
	}

	stats, ok := diskStorageStats(dir)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("could not stat %s", dir)})
		return
	}
	stats["models_dir"] = dir
	c.JSON(http.StatusOK, stats)
}
