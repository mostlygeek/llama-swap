package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type configEditorState struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type configEditorUpdateRequest struct {
	Content string `json:"content"`
}

func (pm *ProxyManager) apiGetConfigEditor(c *gin.Context) {
	state, err := pm.readConfigEditorState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) apiSaveConfigEditor(c *gin.Context) {
	var req configEditorUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	parsedConfig, err := config.LoadConfigFromReader(bytes.NewReader([]byte(req.Content)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("invalid config: %v", err),
		})
		return
	}

	configPath, err := pm.getConfigPath()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := writeConfigRawFile(configPath, []byte(req.Content)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to write config: %v", err),
		})
		return
	}

	pm.Lock()
	pm.config = parsedConfig
	pm.Unlock()

	// Notify UI subscribers that config-backed model state changed.
	event.Emit(ConfigFileChangedEvent{ReloadingState: ReloadingStateEnd})

	state, err := pm.readConfigEditorState()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"path":    configPath,
			"content": req.Content,
		})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) readConfigEditorState() (configEditorState, error) {
	configPath, err := pm.getConfigPath()
	if err != nil {
		return configEditorState{}, err
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return configEditorState{}, err
	}

	state := configEditorState{
		Path:    configPath,
		Content: string(raw),
	}

	if info, err := os.Stat(configPath); err == nil {
		state.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
	}

	return state, nil
}

func writeConfigRawFile(configPath string, raw []byte) error {
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}
