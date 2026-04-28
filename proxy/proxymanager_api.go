package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
)

type Model struct {
	Id          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Unlisted    bool     `json:"unlisted"`
	PeerID      string   `json:"peerID"`
	Aliases     []string `json:"aliases,omitempty"`
}

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	// Protected with API key authentication
	apiGroup := pm.ginEngine.Group("/api", pm.apiKeyAuth())
	{
		apiGroup.POST("/models/unload", pm.apiUnloadAllModels)
		apiGroup.POST("/models/unload/*model", pm.apiUnloadSingleModelHandler)
		apiGroup.GET("/events", pm.apiSendEvents)
		apiGroup.GET("/metrics", pm.apiGetMetrics)
		apiGroup.GET("/version", pm.apiGetVersion)
		apiGroup.GET("/captures/:id", pm.apiGetCapture)
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
		state := "unknown"
		var process *Process
		if pm.matrix != nil {
			process, _ = pm.matrix.GetProcess(modelID)
		} else {
			processGroup := pm.findGroupByModelName(modelID)
			if processGroup != nil {
				process = processGroup.processes[modelID]
			}
		}
		if process != nil {
			switch process.CurrentState() {
			case StateReady:
				state = "ready"
			case StateStarting:
				state = "starting"
			case StateStopping:
				state = "stopping"
			case StateShutdown:
				state = "shutdown"
			case StateStopped:
				state = "stopped"
			}
		}
		models = append(models, Model{
			Id:          modelID,
			Name:        pm.config.Models[modelID].Name,
			Description: pm.config.Models[modelID].Description,
			State:       state,
			Unlisted:    pm.config.Models[modelID].Unlisted,
			Aliases:     pm.config.Models[modelID].Aliases,
		})
	}

	// Iterate over the peer models
	if pm.peerProxy != nil {
		for peerID, peer := range pm.peerProxy.ListPeers() {
			for _, modelID := range peer.Models {
				models = append(models, Model{
					Id:     modelID,
					PeerID: peerID,
				})
			}
		}
	}

	return models
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
	msgTypeMetrics     messageType = "metrics"
	msgTypeInFlight    messageType = "inflight"
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
	// prevent nginx from buffering SSE
	c.Header("X-Accel-Buffering", "no")

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

	sendMetrics := func(metrics []TokenMetrics) {
		filtered := pm.filterExcludedMetrics(metrics)
		if len(filtered) == 0 {
			return
		}
		jsonData, err := json.Marshal(filtered)
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeMetrics, Data: string(jsonData)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendInFlight := func(total int) {
		jsonData, err := json.Marshal(gin.H{"total": total})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeInFlight, Data: string(jsonData)}:
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

	/**
	 * Send Metrics data
	 */
	defer event.On(func(e TokenMetricsEvent) {
		sendMetrics([]TokenMetrics{e.Metrics})
	})()

	/**
	 * Send in-flight request stats related to token stats "Waiting: N" count.
	 */
	defer event.On(func(e InFlightRequestsEvent) {
		sendInFlight(e.Total)
	})()

	// send initial batch of data
	sendLogData("proxy", pm.proxyLogger.GetHistory())
	sendLogData("upstream", pm.upstreamLogger.GetHistory())
	sendModels()
	sendMetrics(pm.metricsMonitor.getMetrics())
	sendInFlight(pm.inFlightCounter.Current())

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
	metrics := pm.metricsMonitor.getMetrics()
	filtered := pm.filterExcludedMetrics(metrics)
	jsonData, err := json.Marshal(filtered)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get metrics"})
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (pm *ProxyManager) filterExcludedMetrics(metrics []TokenMetrics) []TokenMetrics {
	if len(metrics) == 0 {
		return metrics
	}

	excludedModels := make(map[string]bool)
	for modelID, modelConfig := range pm.config.Models {
		if modelConfig.ExcludeFromMetrics {
			excludedModels[modelID] = true
		}
	}

	if len(excludedModels) == 0 {
		return metrics
	}

	filtered := make([]TokenMetrics, 0, len(metrics))
	for _, m := range metrics {
		if !excludedModels[m.Model] {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func (pm *ProxyManager) apiUnloadSingleModelHandler(c *gin.Context) {
	requestedModel := strings.TrimPrefix(c.Param("model"), "/")
	realModelName, found := pm.config.RealModelName(requestedModel)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "Model not found")
		return
	}

	var stopErr error
	if pm.matrix != nil {
		stopErr = pm.matrix.StopProcess(realModelName, StopImmediately)
	} else {
		processGroup := pm.findGroupByModelName(realModelName)
		if processGroup == nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("process group not found for model %s", requestedModel))
			return
		}
		stopErr = processGroup.StopProcess(realModelName, StopImmediately)
	}

	if stopErr != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error stopping process: %s", stopErr.Error()))
		return
	}
	c.String(http.StatusOK, "OK")
}

func (pm *ProxyManager) apiGetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]string{
		"version":    pm.version,
		"commit":     pm.commit,
		"build_date": pm.buildDate,
	})
}

func (pm *ProxyManager) apiGetCapture(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture ID"})
		return
	}

	data, exists := pm.metricsMonitor.getCompressedBytes(id)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "capture not found"})
		return
	}

	c.Header("Vary", "Accept-Encoding")

	// ¯\_(ツ)_/¯ quality weights are too fancy for us anyway
	hasZstd := strings.Contains(c.GetHeader("Accept-Encoding"), "zstd")

	if hasZstd {
		c.Header("Content-Encoding", "zstd")
		c.Data(http.StatusOK, "application/json", data)
	} else {
		decompressed, err := decompressCapture(data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decompress capture"})
			return
		}
		c.Data(http.StatusOK, "application/json", decompressed)
	}
}
