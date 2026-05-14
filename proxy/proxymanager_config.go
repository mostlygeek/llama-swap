package proxy

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"gopkg.in/yaml.v3"
)

// configModelRequest is the body for POST /api/config/models.
type configModelRequest struct {
	// Required: model ID as it appears in the models map.
	ID string `json:"id" binding:"required"`
	// Full llama-server command including ${PORT} placeholder.
	// Use {model_path} as a placeholder that gets substituted with the actual file path.
	Cmd string `json:"cmd" binding:"required"`
	// Optional display fields.
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	// TTL in seconds; 0 = use global TTL, -1 = never unload.
	TTL *int `json:"ttl"`
}

// configModelPatchRequest is the body for PATCH /api/config/models/:id.
type configModelPatchRequest struct {
	Cmd         *string        `json:"cmd"`
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	Aliases     *[]string      `json:"aliases"`
	TTL         *int           `json:"ttl"`
	CtxSize     *int           `json:"ctx_size"`
	CtxSizeDash *int           `json:"ctx-size"`
	NGPULayers  *int           `json:"n_gpu_layers"`
	NGPUDash    *int           `json:"n-gpu-layers"`
	Flags       map[string]any `json:"flags"`
}

// apiConfigInfo implements GET /api/config/info.
// Returns the config file path, models directory, and per-model file-existence check.
func (pm *ProxyManager) apiConfigInfo(c *gin.Context) {
	type modelInfo struct {
		ID         string `json:"id"`
		FilePath   string `json:"file_path,omitempty"`
		FileExists bool   `json:"file_exists"`
	}

	models := make([]modelInfo, 0, len(pm.config.Models))
	for id, mc := range pm.config.Models {
		mi := modelInfo{ID: id}
		if p := parseModelPath(mc.Cmd); p != "" {
			mi.FilePath = p
			_, err := os.Stat(p)
			mi.FileExists = err == nil
		}
		models = append(models, mi)
	}

	c.JSON(http.StatusOK, gin.H{
		"config_file": pm.configFile,
		"models_dir":  pm.modelsDir(),
		"model_count": len(pm.config.Models),
		"models":      models,
	})
}

// apiConfigAddModel implements POST /api/config/models.
// Adds or replaces a model entry in the on-disk config YAML and triggers reload.
func (pm *ProxyManager) apiConfigAddModel(c *gin.Context) {
	var req configModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if pm.configFile == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity, "config file path not set; restart llama-swap with --config flag")
		return
	}

	mc := config.ModelConfig{
		Cmd:         req.Cmd,
		Name:        req.Name,
		Description: req.Description,
		Aliases:     req.Aliases,
	}
	if req.TTL != nil {
		mc.UnloadAfter = *req.TTL
	} else {
		mc.UnloadAfter = config.MODEL_CONFIG_DEFAULT_TTL
	}

	if err := pm.writeModelToConfig(req.ID, &mc); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("write config: %v", err))
		return
	}

	pm.triggerReload()
	c.JSON(http.StatusOK, gin.H{"id": req.ID, "status": "added"})
}

// apiConfigPatchModel implements PATCH /api/config/models/:id.
// It updates selected config fields and common llama-server flags without
// requiring callers to reconstruct the whole command string.
func (pm *ProxyManager) apiConfigPatchModel(c *gin.Context) {
	id := c.Param("id")
	realID, found := pm.config.RealModelName(id)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found in config")
		return
	}
	if pm.configFile == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity, "config file path not set")
		return
	}

	var req configModelPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := pm.patchModelInConfig(realID, req); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("write config: %v", err))
		return
	}
	pm.triggerReload()
	c.JSON(http.StatusOK, gin.H{"id": realID, "status": "updated"})
}

// apiConfigRemoveModel implements DELETE /api/config/models/:id.
// Removes the model entry from the config YAML without touching the file on disk.
func (pm *ProxyManager) apiConfigRemoveModel(c *gin.Context) {
	id := c.Param("id")
	if _, found := pm.config.RealModelName(id); !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found in config")
		return
	}
	if pm.configFile == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity, "config file path not set")
		return
	}
	if err := pm.removeModelFromConfig(id); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("write config: %v", err))
		return
	}
	pm.triggerReload()
	c.JSON(http.StatusOK, gin.H{"id": id, "status": "removed"})
}

// apiConfigReload implements POST /api/config/reload.
// Re-reads the config file from disk and restarts the proxy manager.
func (pm *ProxyManager) apiConfigReload(c *gin.Context) {
	if pm.reloadFn == nil {
		pm.sendErrorResponse(c, http.StatusServiceUnavailable, "reload not available; restart llama-swap manually")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "reloading"})
	go pm.reloadFn()
}

// triggerReload calls reloadFn in a goroutine if it is set.
func (pm *ProxyManager) triggerReload() {
	if pm.reloadFn != nil {
		go pm.reloadFn()
	}
}

// writeModelToConfig reads the config YAML, sets models[id] = mc, and writes it back.
func (pm *ProxyManager) writeModelToConfig(id string, mc *config.ModelConfig) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	raw, err := os.ReadFile(pm.configFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", pm.configFile, err)
	}

	// Unmarshal into a generic map to preserve all top-level keys.
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse %s: %w", pm.configFile, err)
	}
	if root == nil {
		root = make(map[string]any)
	}

	models, _ := root["models"].(map[string]any)
	if models == nil {
		models = make(map[string]any)
	}

	entry := map[string]any{
		"cmd":   mc.Cmd,
		"proxy": mc.Proxy,
	}
	if mc.Name != "" {
		entry["name"] = mc.Name
	}
	if mc.Description != "" {
		entry["description"] = mc.Description
	}
	if len(mc.Aliases) > 0 {
		entry["aliases"] = mc.Aliases
	}
	if mc.UnloadAfter != config.MODEL_CONFIG_DEFAULT_TTL {
		entry["ttl"] = mc.UnloadAfter
	}

	models[id] = entry
	root["models"] = models

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(pm.configFile, out, 0o644)
}

// patchModelInConfig reads the config YAML, applies a partial model update, and
// writes the result back while preserving unrelated top-level config keys.
func (pm *ProxyManager) patchModelInConfig(id string, req configModelPatchRequest) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	raw, err := os.ReadFile(pm.configFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", pm.configFile, err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse %s: %w", pm.configFile, err)
	}
	models, _ := root["models"].(map[string]any)
	if models == nil {
		return fmt.Errorf("models section missing")
	}
	entry, _ := models[id].(map[string]any)
	if entry == nil {
		return fmt.Errorf("model %q not found", id)
	}

	if req.Cmd != nil {
		entry["cmd"] = *req.Cmd
	}
	if req.Name != nil {
		entry["name"] = *req.Name
	}
	if req.Description != nil {
		entry["description"] = *req.Description
	}
	if req.Aliases != nil {
		entry["aliases"] = *req.Aliases
	}
	if req.TTL != nil {
		entry["ttl"] = *req.TTL
	}

	flags := make(map[string]string, len(req.Flags)+2)
	for k, v := range req.Flags {
		flags[normalizeCmdFlag(k)] = flagValueString(v)
	}
	if req.CtxSize != nil {
		flags["--ctx-size"] = fmt.Sprint(*req.CtxSize)
	}
	if req.CtxSizeDash != nil {
		flags["--ctx-size"] = fmt.Sprint(*req.CtxSizeDash)
	}
	if req.NGPULayers != nil {
		flags["--n-gpu-layers"] = fmt.Sprint(*req.NGPULayers)
	}
	if req.NGPUDash != nil {
		flags["--n-gpu-layers"] = fmt.Sprint(*req.NGPUDash)
	}
	if len(flags) > 0 {
		cmd, _ := entry["cmd"].(string)
		patched, err := patchCommandFlags(cmd, flags)
		if err != nil {
			return err
		}
		entry["cmd"] = patched
	}

	models[id] = entry
	root["models"] = models
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(pm.configFile, out, 0o644)
}

func patchCommandFlags(cmd string, flags map[string]string) (string, error) {
	parts, err := config.SanitizeCommand(cmd)
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("cmd is empty")
	}

	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, normalizeCmdFlag(k))
	}
	sort.Strings(keys)

	for _, flag := range keys {
		value := strings.TrimSpace(flags[flag])
		if value == "" {
			continue
		}
		found := false
		for i := 0; i < len(parts); i++ {
			if parts[i] == flag && i+1 < len(parts) {
				parts[i+1] = value
				found = true
				break
			}
			if strings.HasPrefix(parts[i], flag+"=") {
				parts[i] = flag + "=" + value
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, flag, value)
		}
	}
	return strings.Join(parts, " "), nil
}

func normalizeCmdFlag(flag string) string {
	flag = strings.TrimSpace(flag)
	flag = strings.TrimPrefix(flag, "--")
	return "--" + strings.ReplaceAll(flag, "_", "-")
}

func flagValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

// removeModelFromConfig reads the config YAML, deletes models[id], and writes it back.
func (pm *ProxyManager) removeModelFromConfig(id string) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	raw, err := os.ReadFile(pm.configFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", pm.configFile, err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if models, ok := root["models"].(map[string]any); ok {
		delete(models, id)
		root["models"] = models
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(pm.configFile, out, 0o644)
}

// buildCmd constructs a llama-server command for modelPath.
// If extraFlags is non-empty it is appended after the --model argument.
// When extraFlags is empty the function tries to clone the binary/port/host
// prefix from the first existing model's cmd as a template.
func (pm *ProxyManager) buildCmd(modelPath, extraFlags string) string {
	if extraFlags != "" {
		return fmt.Sprintf("llama-server --port ${PORT} --model %s %s", modelPath, strings.TrimSpace(extraFlags))
	}
	// Use the first model's cmd as a structural template: keep everything up
	// to (and including) --model, replace the path, drop the old path value.
	for _, mc := range pm.config.Models {
		parts, err := config.SanitizeCommand(mc.Cmd)
		if err != nil || len(parts) == 0 {
			continue
		}
		var rebuilt []string
		pathInserted := false
		skip := false
		for _, p := range parts {
			if skip {
				// Replace old model path with new one.
				rebuilt = append(rebuilt, modelPath)
				pathInserted = true
				skip = false
				continue
			}
			if p == "-m" || p == "--model" {
				rebuilt = append(rebuilt, p)
				skip = true
				continue
			}
			if strings.HasPrefix(p, "--model=") {
				rebuilt = append(rebuilt, "--model="+modelPath)
				pathInserted = true
				continue
			}
			rebuilt = append(rebuilt, p)
		}
		if pathInserted {
			return strings.Join(rebuilt, " ")
		}
	}
	return fmt.Sprintf("llama-server --port ${PORT} --model %s --n-gpu-layers 99", modelPath)
}
