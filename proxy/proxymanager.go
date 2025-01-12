package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	PROFILE_SPLIT_CHAR = ":"
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

	if config.LogRequests {
		pm.ginEngine.Use(func(c *gin.Context) {
			// Start timer
			start := time.Now()

			// capture these because /upstream/:model rewrites them in c.Next()
			clientIP := c.ClientIP()
			method := c.Request.Method
			path := c.Request.URL.Path

			// Process request
			c.Next()

			// Stop timer
			duration := time.Since(start)

			statusCode := c.Writer.Status()
			bodySize := c.Writer.Size()

			fmt.Fprintf(pm.logMonitor, "[llama-swap] %s [%s] \"%s %s %s\" %d %d \"%s\" %v\n",
				clientIP,
				time.Now().Format("2006-01-02 15:04:05"),
				method,
				path,
				c.Request.Proto,
				statusCode,
				bodySize,
				c.Request.UserAgent(),
				duration,
			)
		})
	}

	// Set up routes using the Gin engine
	pm.ginEngine.POST("/v1/chat/completions", pm.proxyOAIHandler)
	// Support legacy /v1/completions api, see issue #12
	pm.ginEngine.POST("/v1/completions", pm.proxyOAIHandler)

	// Support embeddings
	pm.ginEngine.POST("/v1/embeddings", pm.proxyOAIHandler)
	pm.ginEngine.POST("/v1/rerank", pm.proxyOAIHandler)

	// Support audio/speech endpoint
	pm.ginEngine.POST("/v1/audio/speech", pm.proxyOAIHandler)

	pm.ginEngine.GET("/v1/models", pm.listModelsHandler)

	// in proxymanager_loghandlers.go
	pm.ginEngine.GET("/logs", pm.sendLogsHandlers)
	pm.ginEngine.GET("/logs/stream", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE", pm.streamLogsHandlerSSE)

	pm.ginEngine.GET("/upstream", pm.upstreamIndex)
	pm.ginEngine.Any("/upstream/:model_id/*upstreamPath", pm.proxyToUpstream)

	pm.ginEngine.GET("/", func(c *gin.Context) {
		// Set the Content-Type header to text/html
		c.Header("Content-Type", "text/html")

		// Write the embedded HTML content to the response
		htmlData, err := getHTMLFile("index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		_, err = c.Writer.Write(htmlData)
		if err != nil {
			c.String(http.StatusInternalServerError, fmt.Sprintf("failed to write response: %v", err))
			return
		}
	})

	pm.ginEngine.GET("/favicon.ico", func(c *gin.Context) {
		if data, err := getHTMLFile("favicon.ico"); err == nil {
			c.Data(http.StatusOK, "image/x-icon", data)
		} else {
			c.String(http.StatusInternalServerError, err.Error())
		}
	})

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
	for id, modelConfig := range pm.config.Models {
		if modelConfig.Unlisted {
			continue
		}

		data = append(data, map[string]interface{}{
			"id":       id,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "llama-swap",
		})
	}

	// Set the Content-Type header to application/json
	c.Header("Content-Type", "application/json")

	if origin := c.Request.Header.Get("Origin"); origin != "" {
		c.Header("Access-Control-Allow-Origin", origin)
	}

	// Encode the data as JSON and write it to the response writer
	if err := json.NewEncoder(c.Writer).Encode(map[string]interface{}{"data": data}); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error encoding JSON %s", err.Error()))
		return
	}
}

func (pm *ProxyManager) swapModel(requestedModel string) (*Process, error) {
	pm.Lock()
	defer pm.Unlock()

	// Check if requestedModel contains a PROFILE_SPLIT_CHAR
	profileName, modelName := "", requestedModel
	if idx := strings.Index(requestedModel, PROFILE_SPLIT_CHAR); idx != -1 {
		profileName = requestedModel[:idx]
		modelName = requestedModel[idx+1:]
	}

	if profileName != "" {
		if _, found := pm.config.Profiles[profileName]; !found {
			return nil, fmt.Errorf("model group not found %s", profileName)
		}
	}

	// de-alias the real model name and get a real one
	realModelName, found := pm.config.RealModelName(modelName)
	if !found {
		return nil, fmt.Errorf("could not find modelID for %s", requestedModel)
	}

	// exit early when already running, otherwise stop everything and swap
	requestedProcessKey := ProcessKeyName(profileName, realModelName)

	if process, found := pm.currentProcesses[requestedProcessKey]; found {
		return process, nil
	}

	// stop all running models
	pm.stopProcesses()

	if profileName == "" {
		modelConfig, modelID, found := pm.config.FindConfig(realModelName)
		if !found {
			return nil, fmt.Errorf("could not find configuration for %s", realModelName)
		}

		process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.logMonitor)
		processKey := ProcessKeyName(profileName, modelID)
		pm.currentProcesses[processKey] = process
	} else {
		for _, modelName := range pm.config.Profiles[profileName] {
			if realModelName, found := pm.config.RealModelName(modelName); found {
				modelConfig, modelID, found := pm.config.FindConfig(realModelName)
				if !found {
					return nil, fmt.Errorf("could not find configuration for %s in group %s", realModelName, profileName)
				}

				process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.logMonitor)
				processKey := ProcessKeyName(profileName, modelID)
				pm.currentProcesses[processKey] = process
			}
		}
	}

	// requestedProcessKey should exist due to swap
	return pm.currentProcesses[requestedProcessKey], nil
}

func (pm *ProxyManager) proxyToUpstream(c *gin.Context) {
	requestedModel := c.Param("model_id")

	if requestedModel == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "model id required in path")
		return
	}

	if process, err := pm.swapModel(requestedModel); err != nil {
		pm.sendErrorResponse(c, http.StatusNotFound, fmt.Sprintf("unable to swap to model, %s", err.Error()))
	} else {
		// rewrite the path
		c.Request.URL.Path = c.Param("upstreamPath")
		process.ProxyRequest(c.Writer, c.Request)
	}
}

func (pm *ProxyManager) upstreamIndex(c *gin.Context) {
	var html strings.Builder

	html.WriteString("<!doctype HTML>\n<html><body><h1>Available Models</h1><ul>")

	// Extract keys and sort them
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
		html.WriteString(fmt.Sprintf("<li><a href=\"/upstream/%s\">%s</a></li>", modelID, modelID))
	}
	html.WriteString("</ul></body></html>")
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html.String())
}

func (pm *ProxyManager) proxyOAIHandler(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, "could not ready request body")
		return
	}

	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		pm.sendErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %s", err.Error()))
		return
	}
	model, ok := requestBody["model"].(string)
	if !ok {
		pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' key")
		return
	}

	if process, err := pm.swapModel(model); err != nil {
		pm.sendErrorResponse(c, http.StatusNotFound, fmt.Sprintf("unable to swap to model, %s", err.Error()))
		return
	} else {
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// dechunk it as we already have all the body bytes see issue #11
		c.Request.Header.Del("transfer-encoding")
		c.Request.Header.Add("content-length", strconv.Itoa(len(bodyBytes)))

		process.ProxyRequest(c.Writer, c.Request)
	}
}

func (pm *ProxyManager) sendErrorResponse(c *gin.Context, statusCode int, message string) {
	acceptHeader := c.GetHeader("Accept")

	if strings.Contains(acceptHeader, "application/json") {
		c.JSON(statusCode, gin.H{"error": message})
	} else {
		c.String(statusCode, message)
	}
}

func ProcessKeyName(groupName, modelName string) string {
	return groupName + PROFILE_SPLIT_CHAR + modelName
}
