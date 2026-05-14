//go:build windows

package proxy

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// apiGetStorage implements GET /api/storage.
// Disk usage reporting is not supported on Windows.
func (pm *ProxyManager) apiGetStorage(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "disk storage API not supported on Windows"})
}
