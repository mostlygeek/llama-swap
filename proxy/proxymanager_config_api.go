package proxy

import (
	"bytes"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
)

// apiGetConfig returns the raw YAML configuration file contents.
func (pm *ProxyManager) apiGetConfig(c *gin.Context) {
	data, err := os.ReadFile(pm.configPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}

// apiPutConfig validates and atomically writes the configuration file.
// It triggers reload behavior based on the watchConfigEnabled setting.
func (pm *ProxyManager) apiPutConfig(c *gin.Context) {
	// Read entire body as text (accept text/plain or application/x-yaml, but don't hard fail on content-type)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Validate YAML using existing loader
	if _, err := LoadConfigFromReader(bytes.NewReader(body)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Preserve existing file mode when possible; default to 0644
	var mode os.FileMode = 0o644
	if fi, err := os.Stat(pm.configPath); err == nil {
		mode = fi.Mode()
	}

	// Atomic write
	if err := WriteFileAtomic(pm.configPath, body, mode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write config file"})
		return
	}

	// Trigger reload based on watch behavior
	if pm.watchConfigEnabled {
		// Do not call reloadCallback; fsnotify watcher will emit start event and handle reload
	} else {
		// Emit start event then call reload callback
		event.Emit(ConfigFileChangedEvent{
			ReloadingState: ReloadingStateStart,
		})
		if pm.reloadCallback != nil {
			pm.reloadCallback()
		}
	}

	c.JSON(http.StatusOK, gin.H{"msg": "ok"})
}
