package proxy

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
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

	var modelIDs []string
	for modelID, modelConfig := range pm.config.Models {
		if modelConfig.Unlisted {
			continue
		}

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

	notify := c.Request.Context().Done()

	// Stream new events
	for {
		select {
		case <-notify:
			return
		default:
			models := pm.getModelStatus()
			c.SSEvent("message", models)
			c.Writer.Flush()
			<-time.After(1000 * time.Millisecond)
		}
	}
}
