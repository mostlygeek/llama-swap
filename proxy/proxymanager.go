package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	pm.ginEngine.GET("/logs", pm.sendLogsHandlers)
	pm.ginEngine.GET("/logs/stream", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE", pm.streamLogsHandlerSSE)
	pm.ginEngine.NoRoute(pm.proxyRequestHandler)

	// Disable console color for testing
	gin.DisableConsoleColor()

	return pm
}

func (pm *ProxyManager) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	pm.ginEngine.ServeHTTP(w, r)
}

func (pm *ProxyManager) sendLogsHandlers(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	history := pm.logMonitor.GetHistory()
	_, err := c.Writer.Write(history)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
}

func (pm *ProxyManager) streamLogsHandler(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Content-Type-Options", "nosniff")

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := c.Request.Context().Done()
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("Streaming unsupported"))
		return
	}

	_, skipHistory := c.GetQuery("no-history")
	// Send history first if not skipped

	if !skipHistory {
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			_, err := c.Writer.Write(history)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			flusher.Flush()
		}
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			_, err := c.Writer.Write(msg)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func (pm *ProxyManager) streamLogsHandlerSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := c.Request.Context().Done()

	// Send history first if not skipped
	_, skipHistory := c.GetQuery("skip")
	if !skipHistory {
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			c.SSEvent("message", string(history))
			c.Writer.Flush()
		}
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			c.SSEvent("message", string(msg))
			c.Writer.Flush()
		case <-notify:
			return
		}
	}
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
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("Error encoding JSON"))
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
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}
	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("Invalid JSON"))
		return
	}
	model, ok := requestBody["model"].(string)
	if !ok {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("Missing or invalid 'model' key"))
		return
	}

	if err := pm.swapModel(model); err != nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("unable to swap to model, %s", err.Error()))
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	pm.currentProcess.ProxyRequest(c.Writer, c.Request)
}

func (pm *ProxyManager) proxyRequestHandler(c *gin.Context) {
	if pm.currentProcess != nil {
		pm.currentProcess.ProxyRequest(c.Writer, c.Request)
	} else {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("no strategy to handle request"))
	}
}
