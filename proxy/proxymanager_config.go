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
	if !isValidModelID(req.ID) {
		pm.sendErrorResponse(c, http.StatusBadRequest, "model ID contains invalid characters; use A-Za-z0-9 . _ : / -")
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
	realID, found := pm.config.RealModelName(id)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found in config")
		return
	}
	if pm.configFile == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity, "config file path not set")
		return
	}
	if err := pm.removeModelFromConfig(realID); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("write config: %v", err))
		return
	}
	pm.triggerReload()
	c.JSON(http.StatusOK, gin.H{"id": realID, "status": "removed"})
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
// Uses yaml.Node to preserve comments, key ordering, and node styles in the rest of the file.
func (pm *ProxyManager) writeModelToConfig(id string, mc *config.ModelConfig) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	root, err := readYAMLRoot(pm.configFile)
	if err != nil {
		return err
	}

	modelsNode := yamlMapGet(root, "models")
	if modelsNode == nil {
		modelsNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		yamlMapSet(root, "models", modelsNode)
	}

	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	yamlMapSet(entry, "cmd", yamlScalar(mc.Cmd))
	if mc.Proxy != "" {
		yamlMapSet(entry, "proxy", yamlScalar(mc.Proxy))
	}
	if mc.Name != "" {
		yamlMapSet(entry, "name", yamlScalar(mc.Name))
	}
	if mc.Description != "" {
		yamlMapSet(entry, "description", yamlScalar(mc.Description))
	}
	if len(mc.Aliases) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, a := range mc.Aliases {
			seq.Content = append(seq.Content, yamlScalar(a))
		}
		yamlMapSet(entry, "aliases", seq)
	}
	if mc.UnloadAfter != config.MODEL_CONFIG_DEFAULT_TTL {
		yamlMapSet(entry, "ttl", yamlInt(mc.UnloadAfter))
	}
	yamlMapSet(modelsNode, id, entry)

	return writeYAMLRoot(pm.configFile, root, 0o644)
}

// patchModelInConfig reads the config YAML, applies a partial model update, and
// writes the result back while preserving unrelated fields and comments.
func (pm *ProxyManager) patchModelInConfig(id string, req configModelPatchRequest) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	root, err := readYAMLRoot(pm.configFile)
	if err != nil {
		return err
	}
	modelsNode := yamlMapGet(root, "models")
	if modelsNode == nil {
		return fmt.Errorf("models section missing")
	}
	entryNode := yamlMapGet(modelsNode, id)
	if entryNode == nil || entryNode.Kind != yaml.MappingNode {
		return fmt.Errorf("model %q not found", id)
	}

	if req.Cmd != nil {
		yamlMapSet(entryNode, "cmd", yamlScalar(*req.Cmd))
	}
	if req.Name != nil {
		yamlMapSet(entryNode, "name", yamlScalar(*req.Name))
	}
	if req.Description != nil {
		yamlMapSet(entryNode, "description", yamlScalar(*req.Description))
	}
	if req.Aliases != nil {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, a := range *req.Aliases {
			seq.Content = append(seq.Content, yamlScalar(a))
		}
		yamlMapSet(entryNode, "aliases", seq)
	}
	if req.TTL != nil {
		yamlMapSet(entryNode, "ttl", yamlInt(*req.TTL))
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
		cmd := ""
		if n := yamlMapGet(entryNode, "cmd"); n != nil {
			cmd = n.Value
		}
		patched, err := patchCommandFlags(cmd, flags)
		if err != nil {
			return err
		}
		yamlMapSet(entryNode, "cmd", yamlScalar(patched))
	}

	return writeYAMLRoot(pm.configFile, root, 0o644)
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

// isValidModelID rejects IDs with characters that would break YAML keys or route matching.
func isValidModelID(id string) bool {
	if len(id) == 0 {
		return false
	}
	for _, c := range id {
		if !('A' <= c && c <= 'Z') && !('a' <= c && c <= 'z') && !('0' <= c && c <= '9') &&
			c != '.' && c != '_' && c != ':' && c != '/' && c != '-' {
			return false
		}
	}
	return true
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
// Uses yaml.Node to preserve comments and ordering of all other entries.
func (pm *ProxyManager) removeModelFromConfig(id string) error {
	pm.configMu.Lock()
	defer pm.configMu.Unlock()

	root, err := readYAMLRoot(pm.configFile)
	if err != nil {
		return err
	}
	if modelsNode := yamlMapGet(root, "models"); modelsNode != nil {
		yamlMapDelete(modelsNode, id)
	}
	return writeYAMLRoot(pm.configFile, root, 0o644)
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
	// Sort IDs so template selection is deterministic across calls.
	ids := make([]string, 0, len(pm.config.Models))
	for id := range pm.config.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		mc := pm.config.Models[id]
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

// atomicWriteFile writes data to path via a temp file + rename so a crash or
// disk-full between truncate and write cannot leave the target file empty.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readYAMLRoot parses path and returns the root mapping node.
// Comments, key ordering, and node styles are fully preserved.
func readYAMLRoot(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping at root of %s", path)
	}
	return root, nil
}

// writeYAMLRoot wraps root in a document node and atomically writes YAML.
func writeYAMLRoot(path string, root *yaml.Node, perm os.FileMode) error {
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return atomicWriteFile(path, out, perm)
}

// yamlMapGet returns the value node for key in a mapping node, or nil.
func yamlMapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlMapSet sets key=val in a mapping node, appending if the key is absent.
func yamlMapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		val,
	)
}

// yamlMapDelete removes key from a mapping node.
func yamlMapDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func yamlScalar(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: s, Tag: "!!str"}
}

func yamlInt(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprint(n), Tag: "!!int"}
}
