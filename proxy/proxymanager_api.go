package proxy

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/kelindar/event"
)

type Model struct {
	Id    string `json:"id"`
	State string `json:"state"`
}

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	apiGroup := pm.ginEngine.Group("/api")
	{
		apiGroup.GET("/models", pm.apiListModels)
		apiGroup.GET("/modelsSSE", pm.apiListModelsSSE)
		apiGroup.POST("/models/unload", pm.apiUnloadAllModels)
	}
}

func (pm *ProxyManager) apiUnloadAllModels(c *gin.Context) {
	pm.StopProcesses(StopImmediately)
	c.JSON(http.StatusOK, gin.H{"msg": "ok"})
}

func (pm *ProxyManager) getModelStatus() []Model {
	// Extract keys and sort them
	models := []Model{}

	modelIDs := make([]string, 0, len(pm.config.Models))
	for modelID := range pm.config.Models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)

	// Iterate over sorted keys
	for _, modelID := range modelIDs {
		// Get process state
		processGroup := pm.findGroupByModelName(modelID)
		state := "unknown"
		if processGroup != nil {
			process := processGroup.processes[modelID]
			if process != nil {
				var stateStr string
				switch process.CurrentState() {
				case StateReady:
					stateStr = "ready"
				case StateStarting:
					stateStr = "starting"
				case StateStopping:
					stateStr = "stopping"
				case StateShutdown:
					stateStr = "shutdown"
				case StateStopped:
					stateStr = "stopped"
				default:
					stateStr = "unknown"
				}
				state = stateStr
			}
		}
		models = append(models, Model{
			Id:    modelID,
			State: state,
		})
	}

	return models
}

func (pm *ProxyManager) apiListModels(c *gin.Context) {
	c.JSON(http.StatusOK, pm.getModelStatus())
}

// stream the models as a SSE
func (pm *ProxyManager) apiListModelsSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")

	// flush the first one
	c.SSEvent("message", pm.getModelStatus())
	c.Writer.Flush()

	// send whenever the any process state
	defer event.On(func(e ProcessStateChangeEvent) {
		if c != nil && c.Writer != nil {
			models := pm.getModelStatus()
			c.SSEvent("message", models)
			c.Writer.Flush()
		}
	})()

	// resend the models when the config is reloaded
	defer event.On(func(e ConfigFileChangedEvent) {
		if c != nil && c.Writer != nil && e.ReloadingState == "end" {
			models := pm.getModelStatus()
			c.SSEvent("message", models)
			c.Writer.Flush()
		}
	})()

	select {
	case <-c.Request.Context().Done():
	case <-pm.shutdownCtx.Done():
	}

}
