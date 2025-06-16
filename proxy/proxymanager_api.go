package proxy

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	apiGroup := pm.ginEngine.Group("/api")
	{
		apiGroup.GET("/models/", pm.apiListModels)
		apiGroup.POST("/models/unload", func(c *gin.Context) {})
	}
}

type Model struct {
	Id    string `json:"id"`
	State string `json:"state"`
}

func (pm *ProxyManager) apiListModels(c *gin.Context) {
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
					stateStr = "Ready"
				case StateStarting:
					stateStr = "Starting"
				case StateStopping:
					stateStr = "Stopping"
				case StateShutdown:
					stateStr = "Shutdown"
				case StateStopped:
					stateStr = "Stopped"
				default:
					stateStr = "Unknown"
				}
				state = stateStr
			}
		}
		models = append(models, Model{
			Id:    modelID,
			State: state,
		})
	}
	c.JSON(http.StatusOK, models)
}
