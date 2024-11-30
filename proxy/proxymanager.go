package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type ProxyManager struct {
	sync.Mutex

	config           *Config
	currentProcesses map[string]*Process
	logMonitor       *LogMonitor
	ginEngine        *gin.Engine
}

func New(config *Config) *ProxyManager {
	pm := &ProxyManager{
		config:           config,
		currentProcesses: make(map[string]*Process),
		logMonitor:       NewLogMonitor(),
		ginEngine:        gin.New(),
	}

	// Set up routes using the Gin engine
	pm.ginEngine.POST("/v1/chat/completions", pm.proxyChatRequestHandler)

	// Support legacy /v1/completions api, see issue #12
	pm.ginEngine.POST("/v1/completions", pm.proxyChatRequestHandler)

	pm.ginEngine.GET("/v1/models", pm.listModelsHandler)

	// in proxymanager_loghandlers.go
	pm.ginEngine.GET("/logs", pm.sendLogsHandlers)
	pm.ginEngine.GET("/logs/stream", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE", pm.streamLogsHandlerSSE)

	pm.ginEngine.NoRoute(pm.proxyNoRouteHandler)

	// Disable console color for testing
	gin.DisableConsoleColor()

	return pm
}

func (pm *ProxyManager) Run(addr ...string) error {
	return pm.ginEngine.Run(addr...)
}

func (pm *ProxyManager) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	pm.ginEngine.ServeHTTP(w, r)
}

func (pm *ProxyManager) StopProcesses() {
	pm.Lock()
	defer pm.Unlock()

	pm.stopProcesses()
}

// for internal usage
func (pm *ProxyManager) stopProcesses() {
	if len(pm.currentProcesses) == 0 {
		return
	}

	for _, process := range pm.currentProcesses {
		process.Stop()
	}

	pm.currentProcesses = make(map[string]*Process)
}

func (pm *ProxyManager) listModelsHandler(c *gin.Context) {
	data := []interface{}{}
	for id := range pm.config.Models {
		data = append(data, map[string]interface{}{
			"id":       id,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "llama-swap",
		})
	}

	// Set the Content-Type header to application/json
	c.Header("Content-Type", "application/json")

	// Encode the data as JSON and write it to the response writer
	if err := json.NewEncoder(c.Writer).Encode(map[string]interface{}{"data": data}); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("error encoding JSON"))
		return
	}
}

func (pm *ProxyManager) swapModel(requestedModel string) (*Process, error) {
	pm.Lock()
	defer pm.Unlock()

	// Check if requestedModel contains a /
	groupName, modelName := "", requestedModel
	if idx := strings.Index(requestedModel, "/"); idx != -1 {
		groupName = requestedModel[:idx]
		modelName = requestedModel[idx+1:]
	}

	if groupName != "" {
		if _, found := pm.config.Profiles[groupName]; !found {
			return nil, fmt.Errorf("model group not found %s", groupName)
		}
	}

	// de-alias the real model name and get a real one
	realModelName, found := pm.config.RealModelName(modelName)
	if !found {
		return nil, fmt.Errorf("could not find modelID for %s", requestedModel)
	}

	// exit early when already running, otherwise stop everything and swap
	requestedProcessKey := groupName + "/" + realModelName
	if process, found := pm.currentProcesses[requestedProcessKey]; found {
		return process, nil
	}

	// stop all running models
	pm.stopProcesses()

	if groupName == "" {
		modelConfig, modelID, found := pm.config.FindConfig(realModelName)
		if !found {
			return nil, fmt.Errorf("could not find configuration for %s", realModelName)
		}

		process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.logMonitor)
		processKey := groupName + "/" + modelID
		pm.currentProcesses[processKey] = process
	} else {
		for _, modelName := range pm.config.Profiles[groupName] {
			if realModelName, found := pm.config.RealModelName(modelName); found {
				modelConfig, modelID, found := pm.config.FindConfig(realModelName)
				if !found {
					return nil, fmt.Errorf("could not find configuration for %s in group %s", realModelName, groupName)
				}

				process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.logMonitor)
				processKey := groupName + "/" + modelID
				pm.currentProcesses[processKey] = process
			}
		}
	}

	// requestedProcessKey should exist due to swap
	return pm.currentProcesses[requestedProcessKey], nil
}

func (pm *ProxyManager) proxyChatRequestHandler(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}
	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}
	model, ok := requestBody["model"].(string)
	if !ok {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("missing or invalid 'model' key"))
		return
	}

	if process, err := pm.swapModel(model); err != nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("unable to swap to model, %s", err.Error()))
		return
	} else {
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// dechunk it as we already have all the body bytes see issue #11
		c.Request.Header.Del("transfer-encoding")
		c.Request.Header.Add("content-length", strconv.Itoa(len(bodyBytes)))

		process.ProxyRequest(c.Writer, c.Request)
	}
}

func (pm *ProxyManager) proxyNoRouteHandler(c *gin.Context) {
	// since maps are unordered, just use the first available process if one exists
	for _, process := range pm.currentProcesses {
		process.ProxyRequest(c.Writer, c.Request)
		return
	}

	c.AbortWithError(http.StatusBadRequest, fmt.Errorf("no strategy to handle request"))
}
