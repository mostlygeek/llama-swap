package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type ProxyManager struct {
	sync.Mutex

	config         *Config
	currentProcess *Process
	logMonitor     *LogMonitor
	ginEngine      *gin.Engine
}

func New(config *Config) *ProxyManager {
	pm := &ProxyManager{
		config:         config,
		currentProcess: nil,
		logMonitor:     NewLogMonitor(),
		ginEngine:      gin.New(),
	}

	// Set up routes using the Gin engine
	pm.ginEngine.POST("/v1/chat/completions", pm.proxyChatRequestHandler)
	pm.ginEngine.GET("/v1/models", pm.listModelsHandler)

	// in proxymanager_loghandlers.go
	pm.ginEngine.GET("/logs", pm.sendLogsHandlers)
	pm.ginEngine.GET("/logs/stream", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE", pm.streamLogsHandlerSSE)

	pm.ginEngine.NoRoute(pm.proxyRequestHandler)

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

func (pm *ProxyManager) swapModel(requestedModel string) error {
	pm.Lock()
	defer pm.Unlock()

	// find the model configuration matching requestedModel
	modelConfig, modelID, found := pm.config.FindConfig(requestedModel)
	if !found {
		return fmt.Errorf("could not find configuration for %s", requestedModel)
	}

	// do nothing as it's already the correct process
	if pm.currentProcess != nil {
		if pm.currentProcess.ID == modelID {
			return nil
		} else {
			pm.currentProcess.Stop()
		}
	}

	pm.currentProcess = NewProcess(modelID, modelConfig, pm.logMonitor)
	return pm.currentProcess.Start(pm.config.HealthCheckTimeout)
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

	if err := pm.swapModel(model); err != nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("unable to swap to model, %s", err.Error()))
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// dechunk it as we already have all the body bytes see issue #11
	c.Request.Header.Del("transfer-encoding")
	c.Request.Header.Add("content-length", strconv.Itoa(len(bodyBytes)))

	pm.currentProcess.ProxyRequest(c.Writer, c.Request)
}

func (pm *ProxyManager) proxyRequestHandler(c *gin.Context) {
	if pm.currentProcess != nil {
		pm.currentProcess.ProxyRequest(c.Writer, c.Request)
	} else {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("no strategy to handle request"))
	}
}
