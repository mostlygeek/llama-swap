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
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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
	loadedModel      string
}

func New(config *Config) *ProxyManager {
	pm := &ProxyManager{
		config:           config,
		currentProcesses: make(map[string]*Process),
		logMonitor:       NewLogMonitor(),
		ginEngine:        gin.New(),
		loadedModel:      "*none*",
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

	// see: https://github.com/mostlygeek/llama-swap/issues/42
	// respond with permissive OPTIONS for any endpoint
	pm.ginEngine.Use(func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

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

	pm.ginEngine.GET("/unload", pm.unloadAllModelsHandler)

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

	// stop Processes in parallel
	var wg sync.WaitGroup
	for _, process := range pm.currentProcesses {
		wg.Add(1)
		go func(process *Process) {
			defer wg.Done()
			process.Stop()
		}(process)
	}
	wg.Wait()

	pm.currentProcesses = make(map[string]*Process)
}

// Shutdown is called to shutdown all upstream processes
// when llama-swap is shutting down.
func (pm *ProxyManager) Shutdown() {
	pm.Lock()
	defer pm.Unlock()

	// shutdown process in parallel
	var wg sync.WaitGroup
	for _, process := range pm.currentProcesses {
		wg.Add(1)
		go func(process *Process) {
			defer wg.Done()
			process.Shutdown()
		}(process)
	}
	wg.Wait()
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

	// Set the Loaded-Model header to the last requested model name or to *none* if no model is currently loaded
	c.Header("Loaded-Model", pm.loadedModel)

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
	profileName, modelName := splitRequestedModel(requestedModel)

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

	// check if model is part of the profile
	if profileName != "" {
		found := false
		for _, item := range pm.config.Profiles[profileName] {
			if item == realModelName {
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("model %s part of profile %s", realModelName, profileName)
		}
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
		process.ProxyRequest(c.Writer, c.Request, &pm.loadedModel)
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

	requestedModel := gjson.GetBytes(bodyBytes, "model").String()
	if requestedModel == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' key")
	}

	if process, err := pm.swapModel(requestedModel); err != nil {
		pm.sendErrorResponse(c, http.StatusNotFound, fmt.Sprintf("unable to swap to model, %s", err.Error()))
		return
	} else {

		// strip
		profileName, modelName := splitRequestedModel(requestedModel)
		if profileName != "" {
			bodyBytes, err = sjson.SetBytes(bodyBytes, "model", modelName)
			if err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error updating JSON: %s", err.Error()))
				return
			}
		}

		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// dechunk it as we already have all the body bytes see issue #11
		c.Request.Header.Del("transfer-encoding")
		c.Request.Header.Add("content-length", strconv.Itoa(len(bodyBytes)))

		process.ProxyRequest(c.Writer, c.Request, &pm.loadedModel)
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

func (pm *ProxyManager) unloadAllModelsHandler(c *gin.Context) {
	pm.StopProcesses()
	c.String(http.StatusOK, "OK")
	pm.loadedModel = "unloaded"
}

func ProcessKeyName(groupName, modelName string) string {
	return groupName + PROFILE_SPLIT_CHAR + modelName
}

func splitRequestedModel(requestedModel string) (string, string) {
	profileName, modelName := "", requestedModel
	if idx := strings.Index(requestedModel, PROFILE_SPLIT_CHAR); idx != -1 {
		profileName = requestedModel[:idx]
		modelName = requestedModel[idx+1:]
	}
	return profileName, modelName
}
