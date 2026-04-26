package proxy

import (
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// listOllamaTagsHandler implements GET /api/tags. Only models whose declared
// protocols include "ollama" are listed so Ollama-native clients cannot pick
// an incompatible model.
func (pm *ProxyManager) listOllamaTagsHandler(c *gin.Context) {
	var models []gin.H
	for modelID, mc := range pm.config.Models {
		if mc.Unlisted {
			continue
		}
		if !slices.Contains(mc.Protocols, "ollama") {
			continue
		}
		models = append(models, ollamaTagEntry(modelID, mc.Name))
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

// showOllamaModelHandler implements POST /api/show. Returns a minimal show
// payload for a single model, again only if that model supports ollama.
func (pm *ProxyManager) showOllamaModelHandler(c *gin.Context) {
	var req struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, "invalid show payload")
		return
	}
	name := req.Model
	if name == "" {
		name = req.Name
	}
	realName, ok := pm.config.RealModelName(name)
	if !ok {
		pm.sendErrorResponse(c, http.StatusNotFound, "model not found")
		return
	}
	mc := pm.config.Models[realName]
	if !slices.Contains(mc.Protocols, "ollama") {
		pm.sendErrorResponse(c, http.StatusNotFound, "model does not expose the ollama protocol")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"license":    "",
		"modelfile":  "",
		"parameters": "",
		"template":   "",
		"details": gin.H{
			"format":             "gguf",
			"family":             "unknown",
			"families":           []string{"unknown"},
			"parameter_size":     "",
			"quantization_level": "",
		},
		"model_info":   gin.H{},
		"capabilities": []string{"completion", "tools"},
	})
}

func ollamaTagEntry(modelID, displayName string) gin.H {
	name := modelID
	if displayName != "" {
		name = displayName
	}
	return gin.H{
		"name":        modelID,
		"model":       modelID,
		"modified_at": time.Now().UTC().Format(time.RFC3339),
		"size":        0,
		"digest":      "",
		"details": gin.H{
			"format":             "gguf",
			"family":             "unknown",
			"families":           []string{"unknown"},
			"parameter_size":     "",
			"quantization_level": "",
		},
		"display_name": name,
	}
}
