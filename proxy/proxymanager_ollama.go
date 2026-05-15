package proxy

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/internal/perf"
)

// --- Ollama-compatible types ---

type ollamaModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type ollamaModelEntry struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	ModifiedAt time.Time          `json:"modified_at"`
	Size       int64              `json:"size"`
	Digest     string             `json:"digest"`
	Details    ollamaModelDetails `json:"details"`
}

// apiOllamaTags implements GET /api/tags — Ollama-compatible model list.
// Enables Open WebUI, Chatbox, Msty, and other Ollama-mode frontends.
func (pm *ProxyManager) apiOllamaTags(c *gin.Context) {
	modelIDs := make([]string, 0, len(pm.config.Models))
	for id := range pm.config.Models {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)

	entries := make([]ollamaModelEntry, 0, len(modelIDs))
	for _, id := range modelIDs {
		mc := pm.config.Models[id]
		if mc.Unlisted {
			continue
		}
		filename := ""
		if p := parseModelPath(mc.Cmd); p != "" {
			filename = filepath.Base(p)
		}
		entry := ollamaModelEntry{
			Name:    id,
			Model:   id,
			Digest:  "",
			Details: inferModelDetails(id, filename),
		}
		if p := parseModelPath(mc.Cmd); p != "" {
			if fi, err := os.Stat(p); err == nil {
				entry.Size = fi.Size()
				entry.ModifiedAt = fi.ModTime()
			}
		}
		if entry.ModifiedAt.IsZero() {
			entry.ModifiedAt = time.Now()
		}
		entries = append(entries, entry)
	}
	c.JSON(http.StatusOK, gin.H{"models": entries})
}

// apiOllamaShow implements POST /api/show — Ollama model details.
func (pm *ProxyManager) apiOllamaShow(c *gin.Context) {
	var req struct {
		Model   string `json:"model"`
		Name    string `json:"name"`
		Verbose bool   `json:"verbose"`
	}
	_ = c.ShouldBindJSON(&req)
	name := req.Model
	if name == "" {
		name = req.Name
	}
	if name == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "model or name field required")
		return
	}
	realName, found := pm.config.RealModelName(name)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found")
		return
	}
	mc := pm.config.Models[realName]
	filename := ""
	if p := parseModelPath(mc.Cmd); p != "" {
		filename = filepath.Base(p)
	}
	details := inferModelDetails(realName, filename)

	resp := gin.H{
		"model":      realName,
		"details":    details,
		"model_info": gin.H{},
		"template":   "",
	}
	if mc.Name != "" {
		resp["name"] = mc.Name
	}
	if mc.Description != "" {
		resp["description"] = mc.Description
	}
	if p := parseModelPath(mc.Cmd); p != "" {
		if fi, err := os.Stat(p); err == nil {
			resp["modified_at"] = fi.ModTime()
			resp["size"] = fi.Size()
		}
	}
	c.JSON(http.StatusOK, resp)
}

// apiOllamaDelete implements DELETE /api/delete — Ollama-compatible model delete.
// Body: {"name": "model-id"} or {"model": "model-id"}.
// Unloads the process then removes the weight file from disk.
func (pm *ProxyManager) apiOllamaDelete(c *gin.Context) {
	var req struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	name := req.Name
	if name == "" {
		name = req.Model
	}
	if name == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "name or model field required")
		return
	}

	realName, found := pm.config.RealModelName(name)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found")
		return
	}
	modelConfig := pm.config.Models[realName]
	filePath := parseModelPath(modelConfig.Cmd)
	if filePath == "" {
		pm.sendErrorResponse(c, http.StatusUnprocessableEntity,
			fmt.Sprintf("cannot determine model file path for %q", realName))
		return
	}

	if pm.matrix != nil {
		_ = pm.matrix.StopProcess(realName, StopImmediately)
	} else {
		if pg := pm.findGroupByModelName(realName); pg != nil {
			_ = pg.StopProcess(realName, StopImmediately)
		}
	}

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		pm.sendErrorResponse(c, http.StatusInternalServerError,
			fmt.Sprintf("failed to delete %s: %v", filePath, err))
		return
	}
	// Ollama returns 200 with empty body on success.
	c.Status(http.StatusOK)
}

// inferModelDetails derives Ollama-style metadata from a model ID and optional
// filename (e.g. "Mistral-24B-Q4_K_M.gguf"). The filename is checked first for
// quantization since model IDs often omit it.
func inferModelDetails(id, filename string) ollamaModelDetails {
	lower := strings.ToLower(id + " " + filename)
	d := ollamaModelDetails{
		Format:   "gguf",
		Family:   "unknown",
		Families: []string{},
	}

	for _, q := range []string{
		"iq4_nl", "iq3_m", "iq2_m",
		"q4_k_m", "q4_k_s", "q5_k_m", "q5_k_s", "q6_k", "q8_0", "q4_0", "q2_k",
	} {
		if strings.Contains(lower, q) {
			d.QuantizationLevel = strings.ToUpper(q)
			break
		}
	}

	// Longer matches first to avoid "1b" matching inside "31b"
	for _, size := range []string{
		"110b", "90b", "72b", "70b", "35b", "32b", "30b", "27b", "24b", "14b", "13b",
		"9b", "8b", "7b", "3b", "1.5b", "1b", "0.5b",
	} {
		if strings.Contains(lower, size) {
			d.ParameterSize = strings.ToUpper(size)
			break
		}
	}

	families := []struct{ key, name string }{
		{"codellama", "codellama"}, {"deepseek", "deepseek"}, {"starcoder", "starcoder"},
		{"mixtral", "mixtral"}, {"mistral", "mistral"}, {"llama", "llama"},
		{"qwen", "qwen"}, {"phi", "phi"}, {"gemma", "gemma"}, {"falcon", "falcon"},
		{"solar", "solar"}, {"yi", "yi"}, {"smollm", "llama"},
	}
	for _, f := range families {
		if strings.Contains(lower, f.key) {
			d.Family = f.name
			d.Families = []string{f.name}
			break
		}
	}

	return d
}

// --- Unified resources endpoint ---

// apiGetResources implements GET /api/resources.
// Returns a single point-in-time snapshot of disk storage, system memory, and GPU stats.
// On Apple Silicon (darwin/arm64) memory is reported as "unified" since GPU and RAM share it.
func (pm *ProxyManager) apiGetResources(c *gin.Context) {
	resp := gin.H{}

	// Storage
	if dir := pm.modelsDir(); dir != "" {
		if stats, ok := diskStorageStats(dir); ok {
			stats["models_dir"] = dir
			resp["storage"] = stats
		}
	}

	// Memory and GPU from perf monitor
	if pm.perfMonitor != nil {
		sysStats, gpuStats := pm.perfMonitor.Current()
		if len(sysStats) > 0 {
			s := sysStats[len(sysStats)-1]
			memType := "system"
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				memType = "unified"
			}
			resp["memory"] = gin.H{
				"total_mb":  s.MemTotalMB,
				"used_mb":   s.MemUsedMB,
				"free_mb":   s.MemFreeMB,
				"type":      memType,
				"load_avg1": s.LoadAvg1,
			}
		}

		// Collect latest reading per GPU ID from the time-series.
		latest := make(map[int]perf.GpuStat)
		for _, g := range gpuStats {
			if prev, ok := latest[g.ID]; !ok || g.Timestamp.After(prev.Timestamp) {
				latest[g.ID] = g
			}
		}
		// On Apple Silicon with no GPU stats, synthesise from unified memory.
		if len(latest) == 0 && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			if len(sysStats) > 0 {
				s := sysStats[len(sysStats)-1]
				latest[0] = perf.GpuStat{
					ID:         0,
					Name:       "Apple Silicon (unified)",
					MemTotalMB: s.MemTotalMB,
					MemUsedMB:  s.MemUsedMB,
				}
			}
		}

		ids := make([]int, 0, len(latest))
		for id := range latest {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		gpuList := make([]gin.H, 0, len(ids))
		for _, id := range ids {
			g := latest[id]
			gpuList = append(gpuList, gin.H{
				"id":              g.ID,
				"name":            g.Name,
				"vram_total_mb":   g.MemTotalMB,
				"vram_used_mb":    g.MemUsedMB,
				"utilization_pct": g.GpuUtilPct,
				"temp_c":          g.TempC,
				"power_draw_w":    g.PowerDrawW,
			})
		}
		resp["gpu"] = gpuList
	}

	c.JSON(http.StatusOK, resp)
}
