package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
)

type Model struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	State       string `json:"state"`
	Unlisted    bool   `json:"unlisted"`
}

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	apiGroup := pm.ginEngine.Group("/api")
	{
		apiGroup.POST("/models/unload", pm.apiUnloadAllModels)
		apiGroup.GET("/events", pm.apiSendEvents)
		apiGroup.GET("/metrics", pm.apiGetMetrics)
		apiGroup.GET("/metrics/:model", pm.apiGetModelMetrics)
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
			Id:          modelID,
			Name:        pm.config.Models[modelID].Name,
			Description: pm.config.Models[modelID].Description,
			State:       state,
			Unlisted:    pm.config.Models[modelID].Unlisted,
		})
	}

	return models
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
)

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// sends a stream of different message types that happen on the server
func (pm *ProxyManager) apiSendEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")

	sendBuffer := make(chan messageEnvelope, 25)
	ctx, cancel := context.WithCancel(c.Request.Context())
	sendModels := func() {
		data, err := json.Marshal(pm.getModelStatus())
		if err == nil {
			msg := messageEnvelope{Type: msgTypeModelStatus, Data: string(data)}
			select {
			case sendBuffer <- msg:
			case <-ctx.Done():
				return
			default:
			}

		}
	}

	sendLogData := func(source string, data []byte) {
		data, err := json.Marshal(gin.H{
			"source": source,
			"data":   string(data),
		})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeLogData, Data: string(data)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	/**
	 * Send updated models list
	 */
	defer event.On(func(e ProcessStateChangeEvent) {
		sendModels()
	})()
	defer event.On(func(e ConfigFileChangedEvent) {
		sendModels()
	})()

	/**
	 * Send Log data
	 */
	defer pm.proxyLogger.OnLogData(func(data []byte) {
		sendLogData("proxy", data)
	})()
	defer pm.upstreamLogger.OnLogData(func(data []byte) {
		sendLogData("upstream", data)
	})()

	// send initial batch of data
	sendLogData("proxy", pm.proxyLogger.GetHistory())
	sendLogData("upstream", pm.upstreamLogger.GetHistory())
	sendModels()

	for {
		select {
		case <-c.Request.Context().Done():
			cancel()
			return
		case <-pm.shutdownCtx.Done():
			cancel()
			return
		case msg := <-sendBuffer:
			c.SSEvent("message", msg)
			c.Writer.Flush()
		}
	}
}

func (pm *ProxyManager) apiGetMetrics(c *gin.Context) {
	if pm.metricsLogger == nil || pm.config.MetricsLogPath == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metrics logging not enabled"})
		return
	}

	file, err := os.Open(pm.config.MetricsLogPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to open metrics log: %v", err)})
		return
	}
	defer file.Close()

	var metrics []map[string]interface{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
			metrics = append(metrics, entry)
		}
	}

	if err := scanner.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read metrics log: %v", err)})
		return
	}

	// Reverse to show newest first
	for i, j := 0, len(metrics)-1; i < j; i, j = i+1, j-1 {
		metrics[i], metrics[j] = metrics[j], metrics[i]
	}

	c.JSON(http.StatusOK, gin.H{"metrics": metrics})
}
