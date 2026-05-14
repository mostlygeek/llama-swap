package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// parseModelPath extracts the local file path from a model's cmd string by
// finding the argument after -m or --model. Returns "" if none found.
func parseModelPath(cmd string) string {
	parts, err := config.SanitizeCommand(cmd)
	if err != nil || len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		if (part == "-m" || part == "--model") && i+1 < len(parts) {
			return parts[i+1]
		}
		// handle --model=path form
		if strings.HasPrefix(part, "--model=") {
			return strings.TrimPrefix(part, "--model=")
		}
	}
	return ""
}

// modelsDir returns the configured models directory, or infers it from model cmds.
// Keys are iterated in sorted order for deterministic results.
func (pm *ProxyManager) modelsDir() string {
	if pm.config.ModelsDir != "" {
		return pm.config.ModelsDir
	}
	// Infer from the first model (sorted) that has a resolvable path.
	ids := make([]string, 0, len(pm.config.Models))
	for id := range pm.config.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if p := parseModelPath(pm.config.Models[id].Cmd); p != "" {
			return filepath.Dir(p)
		}
	}
	return ""
}

// pullRequest is the body for POST /api/models/pull.
type pullRequest struct {
	// Model identifier: "owner/repo/filename.gguf" or a full HuggingFace HTTPS URL.
	Model string `json:"model" binding:"required"`
	// HF bearer token; overrides the HF_TOKEN environment variable.
	Token string `json:"token"`
	// Stream progress updates (default true).
	Stream *bool `json:"stream"`
	// Subdir within modelsDir to write the file into.
	// E.g. "mistral-small-3.1-24b" → modelsDir/mistral-small-3.1-24b/filename.gguf
	// Created automatically if it does not exist.
	Subdir string `json:"subdir"`
	// Register, if set, adds the downloaded model to the config after pull completes.
	// The server reloads automatically so the model is immediately available.
	Register *pullRegister `json:"register"`
}

// pullRegister describes the config entry to create after a successful pull.
type pullRegister struct {
	// Model ID as it will appear in the config and API. Defaults to the GGUF filename without extension.
	ID string `json:"id"`
	// Optional display name.
	Name string `json:"name"`
	// Optional description.
	Description string `json:"description"`
	// Extra llama-server flags appended after --model <path>.
	// E.g. "--ctx-size 32768 --n-gpu-layers 99 --threads 8"
	// If empty, flags are inherited from an existing model's cmd as a template.
	Flags string `json:"flags"`
	// TTL in seconds; 0 = use global, -1 = never unload.
	TTL *int `json:"ttl"`
}

// isHuggingFaceHost returns true if host is huggingface.co or a subdomain.
func isHuggingFaceHost(host string) bool {
	h := strings.ToLower(host)
	return h == "huggingface.co" || strings.HasSuffix(h, ".huggingface.co")
}

// resolveHFSource parses a model identifier into a download URL and destination filename.
// Accepts:
//   - full HTTPS URL: https://huggingface.co/owner/repo/resolve/main/file.gguf
//   - short form:     owner/repo/file.gguf  (resolve/main inferred)
//
// For full URLs, the host must be huggingface.co or a subdomain (prevents SSRF),
// with an exception for localhost/127.0.0.1 (used in tests and local servers).
func resolveHFSource(model string) (downloadURL, filename string, err error) {
	if strings.HasPrefix(model, "https://") || strings.HasPrefix(model, "http://") {
		u, parseErr := url.Parse(model)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid URL: %v", parseErr)
		}
		host := strings.ToLower(u.Hostname())
		isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLoopback {
			if u.Scheme != "https" {
				return "", "", fmt.Errorf("only HTTPS URLs are supported for remote downloads")
			}
			if !isHuggingFaceHost(host) {
				return "", "", fmt.Errorf("URL host %q is not allowed; only huggingface.co domains are supported", host)
			}
		}
		// strip query string for filename detection
		clean := model
		if idx := strings.Index(clean, "?"); idx != -1 {
			clean = clean[:idx]
		}
		filename = filepath.Base(clean)
		if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
			return "", "", fmt.Errorf("cannot derive filename from URL %q", model)
		}
		downloadURL = model
		return
	}
	// Expect owner/repo/filename.gguf
	parts := strings.SplitN(model, "/", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("model must be 'owner/repo/filename.gguf' or a full HuggingFace HTTPS URL, got %q", model)
	}
	owner, repo, file := parts[0], parts[1], parts[2]
	filename = filepath.Base(file)
	downloadURL = fmt.Sprintf("https://huggingface.co/%s/%s/resolve/main/%s", owner, repo, file)
	return
}

// hfToken returns the HuggingFace token to use: request body → HF_TOKEN env.
func hfToken(reqToken string) string {
	if reqToken != "" {
		return reqToken
	}
	return os.Getenv("HF_TOKEN")
}

type pullProgress struct {
	Status    string `json:"status"`
	Filename  string `json:"filename,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// apiPullModel implements POST /api/models/pull.
// Downloads a model from HuggingFace to the models directory, streaming progress.
// HTTP status is determined before streaming begins so errors always carry the correct code.
func (pm *ProxyManager) apiPullModel(c *gin.Context) {
	var req pullRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	baseDir := pm.modelsDir()
	if baseDir == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "models directory unknown; set modelsDir in config",
		})
		return
	}
	dir := baseDir
	if req.Subdir != "" {
		clean := filepath.Clean(req.Subdir)
		if strings.Contains(clean, "..") || filepath.IsAbs(clean) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subdir: path traversal detected"})
			return
		}
		dir = filepath.Join(baseDir, clean)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create subdir: %v", err)})
			return
		}
	}

	downloadURL, filename, err := resolveHFSource(req.Model)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Only attach the bearer token to HuggingFace hosts to avoid leaking credentials.
	token := ""
	if u, parseErr := url.Parse(downloadURL); parseErr == nil && isHuggingFaceHost(u.Hostname()) {
		token = hfToken(req.Token)
	}

	hreq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, downloadURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if token != "" {
		hreq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	// Verify upstream status before committing to a stream response.
	// This ensures errors always carry the correct HTTP status code.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		msg := fmt.Sprintf("HuggingFace returned %d — model may be gated; provide a token", resp.StatusCode)
		c.JSON(resp.StatusCode, gin.H{"error": msg})
		return
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(b))})
		return
	}

	stream := req.Stream == nil || *req.Stream

	if stream {
		c.Header("Content-Type", "application/x-ndjson")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Accel-Buffering", "no")
	}

	writeOk := true
	sendJSON := func(v pullProgress) {
		if !writeOk {
			return
		}
		b, _ := json.Marshal(v)
		if stream {
			if _, err := c.Writer.Write(b); err != nil {
				writeOk = false
				return
			}
			if _, err := c.Writer.Write([]byte("\n")); err != nil {
				writeOk = false
				return
			}
			c.Writer.Flush()
		}
	}

	dest := filepath.Join(dir, filename)
	// Prevent path traversal via filename using Rel-based check.
	rel, relErr := filepath.Rel(filepath.Clean(dir), filepath.Clean(dest))
	if relErr != nil || strings.HasPrefix(rel, "..") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename: path traversal detected"})
		return
	}

	// Write to a temp file then rename so interrupted downloads leave no partial file.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	total := resp.ContentLength
	var completed int64
	buf := make([]byte, 32*1024)
	lastReport := int64(0)

	sendJSON(pullProgress{Status: "downloading", Filename: filename, Total: total, Completed: 0})

	for {
		if !writeOk || c.Request.Context().Err() != nil {
			f.Close()
			os.Remove(tmp)
			return
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				f.Close()
				os.Remove(tmp)
				sendJSON(pullProgress{Status: "error", Error: writeErr.Error()})
				if !stream {
					c.JSON(http.StatusInternalServerError, gin.H{"error": writeErr.Error()})
				}
				return
			}
			completed += int64(n)
			// Report every 10 MB or at end.
			if completed-lastReport >= 10*1024*1024 || readErr == io.EOF {
				sendJSON(pullProgress{Status: "downloading", Filename: filename, Total: total, Completed: completed})
				lastReport = completed
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(tmp)
			sendJSON(pullProgress{Status: "error", Error: readErr.Error()})
			if !stream {
				c.JSON(http.StatusInternalServerError, gin.H{"error": readErr.Error()})
			}
			return
		}
	}

	// Check close error before rename to avoid promoting a truncated file.
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	sendJSON(pullProgress{Status: "success", Filename: filename, Path: dest})

	// Auto-register the model in the config if requested.
	if req.Register != nil {
		if pm.configFile == "" {
			sendJSON(pullProgress{Status: "register_failed", Error: "config file path not set; cannot auto-register"})
		} else {
			reg := req.Register
			id := reg.ID
			if id == "" {
				base := filename
				if i := len(base) - len(".gguf"); i > 0 && strings.HasSuffix(base, ".gguf") {
					base = base[:i]
				}
				id = strings.ToLower(base)
			}
			cmd := pm.buildCmd(dest, reg.Flags)
			mc := config.ModelConfig{
				Cmd:         cmd,
				Proxy:       "http://localhost:${PORT}",
				Name:        reg.Name,
				Description: reg.Description,
				UnloadAfter: config.MODEL_CONFIG_DEFAULT_TTL,
			}
			if reg.TTL != nil {
				mc.UnloadAfter = *reg.TTL
			}
			if writeErr := pm.writeModelToConfig(id, &mc); writeErr == nil {
				sendJSON(pullProgress{Status: "registered", Filename: id, Path: dest})
				pm.triggerReload()
			} else {
				sendJSON(pullProgress{Status: "register_failed", Error: writeErr.Error()})
			}
		}
	}

	if !stream {
		c.JSON(http.StatusOK, gin.H{"status": "success", "filename": filename, "path": dest})
	}
}

// apiDeleteModel implements DELETE /api/models/*model.
// Unloads the model (if running) then deletes its weight file from disk.
// The config entry is preserved — the model will fail to start until the file
// is restored or the config entry is removed.
func (pm *ProxyManager) apiDeleteModel(c *gin.Context) {
	requestedModel := strings.TrimPrefix(c.Param("model"), "/")
	realModelName, found := pm.config.RealModelName(requestedModel)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "Model not found")
		return
	}

	modelConfig := pm.config.Models[realModelName]
	filePath := parseModelPath(modelConfig.Cmd)
	if filePath == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity,
			fmt.Sprintf("cannot determine model file path for %q (no -m / --model flag in cmd)", realModelName))
		return
	}

	// Unload first so the process releases the file.
	// "not found" means already stopped — safe to proceed; any other error aborts.
	isStopNotFound := func(err error) bool { return err != nil && strings.Contains(err.Error(), "not found") }
	if pm.matrix != nil {
		if err := pm.matrix.StopProcess(realModelName, StopImmediately); err != nil && !isStopNotFound(err) {
			pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("unload failed: %v", err))
			return
		}
	} else {
		if pg := pm.findGroupByModelName(realModelName); pg != nil {
			if err := pg.StopProcess(realModelName, StopImmediately); err != nil && !isStopNotFound(err) {
				pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("unload failed: %v", err))
				return
			}
		}
	}

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			pm.sendErrorResponse(c, http.StatusNotFound,
				fmt.Sprintf("model file not found on disk: %s", filePath))
			return
		}
		pm.sendErrorResponse(c, http.StatusInternalServerError,
			fmt.Sprintf("failed to delete %s: %v", filePath, err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"deleted": filePath,
		"model":   realModelName,
	})
}
