package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

// parseModelPath extracts the local file path from a model's cmd string by
// finding the argument after -m or --model. Returns "" if none found.
func parseModelPath(cmd string) string {
	parts, err := sanitizeCommand(cmd)
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

// sanitizeCommand splits a shell-like command string into args, respecting quotes.
func sanitizeCommand(cmd string) ([]string, error) {
	var args []string
	var current strings.Builder
	inSingle, inDouble := false, false

	for _, r := range cmd {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

// modelsDir returns the configured models directory, or infers it from model cmds.
func (pm *ProxyManager) modelsDir() string {
	if pm.config.ModelsDir != "" {
		return pm.config.ModelsDir
	}
	// Infer from the first model that has a resolvable path.
	for _, mc := range pm.config.Models {
		if p := parseModelPath(mc.Cmd); p != "" {
			return filepath.Dir(p)
		}
	}
	return ""
}

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

// pullRequest is the body for POST /api/models/pull.
type pullRequest struct {
	// Model identifier: "owner/repo/filename.gguf" or a full HuggingFace URL.
	Model string `json:"model" binding:"required"`
	// HF bearer token; overrides the HF_TOKEN environment variable.
	Token string `json:"token"`
	// Stream progress updates (default true).
	Stream *bool `json:"stream"`
}

// resolveHFSource parses a model identifier into a download URL and destination filename.
// Accepts:
//   - full URL:  https://huggingface.co/owner/repo/resolve/main/file.gguf
//   - short:     owner/repo/file.gguf  (resolve/main inferred)
func resolveHFSource(model string) (downloadURL, filename string, err error) {
	if strings.HasPrefix(model, "https://") || strings.HasPrefix(model, "http://") {
		// strip query string for filename detection
		clean := model
		if idx := strings.Index(clean, "?"); idx != -1 {
			clean = clean[:idx]
		}
		filename = filepath.Base(clean)
		downloadURL = model
		return
	}
	// Expect owner/repo/filename.gguf
	parts := strings.SplitN(model, "/", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("model must be 'owner/repo/filename.gguf' or a full HuggingFace URL, got %q", model)
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
func (pm *ProxyManager) apiPullModel(c *gin.Context) {
	var req pullRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dir := pm.modelsDir()
	if dir == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "models directory unknown; set modelsDir in config",
		})
		return
	}

	downloadURL, filename, err := resolveHFSource(req.Model)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stream := req.Stream == nil || *req.Stream

	if stream {
		c.Header("Content-Type", "application/x-ndjson")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Accel-Buffering", "no")
	}

	sendJSON := func(v pullProgress) {
		b, _ := json.Marshal(v)
		if stream {
			c.Writer.Write(b)
			c.Writer.Write([]byte("\n"))
			c.Writer.Flush()
		}
	}

	sendJSON(pullProgress{Status: "resolving", Filename: filename})

	token := hfToken(req.Token)
	hreq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, downloadURL, nil)
	if err != nil {
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if token != "" {
		hreq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		msg := fmt.Sprintf("HuggingFace returned %d — model may be gated; provide a token", resp.StatusCode)
		sendJSON(pullProgress{Status: "error", Error: msg})
		if !stream {
			c.JSON(resp.StatusCode, gin.H{"error": msg})
		}
		return
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(b))
		sendJSON(pullProgress{Status: "error", Error: msg})
		if !stream {
			c.JSON(http.StatusBadGateway, gin.H{"error": msg})
		}
		return
	}

	dest := filepath.Join(dir, filename)
	// Prevent path traversal.
	if !strings.HasPrefix(filepath.Clean(dest), filepath.Clean(dir)) {
		msg := "invalid filename: path traversal detected"
		sendJSON(pullProgress{Status: "error", Error: msg})
		if !stream {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		}
		return
	}

	// Write to a temp file then rename so interrupted downloads leave no partial file.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	total := resp.ContentLength
	var completed int64
	buf := make([]byte, 32*1024)
	lastReport := int64(0)

	sendJSON(pullProgress{Status: "downloading", Filename: filename, Total: total, Completed: 0})

	for {
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
	f.Close()

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		sendJSON(pullProgress{Status: "error", Error: err.Error()})
		if !stream {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	sendJSON(pullProgress{Status: "success", Filename: filename, Path: dest})
	if !stream {
		c.JSON(http.StatusOK, gin.H{"status": "success", "filename": filename, "path": dest})
	}
}

// apiDeleteModel implements DELETE /api/models/:model.
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
	if pm.matrix != nil {
		_ = pm.matrix.StopProcess(realModelName, StopImmediately)
	} else {
		if pg := pm.findGroupByModelName(realModelName); pg != nil {
			_ = pg.StopProcess(realModelName, StopImmediately)
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
