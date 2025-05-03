package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
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
	ginEngine        *gin.Engine

	// logging
	proxyLogger    *LogMonitor
	upstreamLogger *LogMonitor
	muxLogger      *LogMonitor
}

// NewWithLoggers creates a new ProxyManager with provided loggers.
// This allows loggers to be defined at a higher level and shared across instances.
func NewWithLoggers(config *Config, muxLogger *LogMonitor, proxyLogger *LogMonitor, upstreamLogger *LogMonitor) *ProxyManager {
	pm := &ProxyManager{
		config:           config,
		currentProcesses: make(map[string]*Process),
		ginEngine:        gin.New(),

		proxyLogger:    proxyLogger,
		muxLogger:      muxLogger,
		upstreamLogger: upstreamLogger,
	}

	pm.setupGinEngine()
	return pm
}

// New creates a new ProxyManager with default loggers.
func New(config *Config) *ProxyManager {
	// set up loggers
	stdoutLogger := NewLogMonitorWriter(os.Stdout)
	upstreamLogger := NewLogMonitorWriter(stdoutLogger)
	proxyLogger := NewLogMonitorWriter(stdoutLogger)

	if config.LogRequests {
		proxyLogger.Warn("LogRequests configuration is deprecated. Use logLevel instead.")
	}

	switch strings.ToLower(strings.TrimSpace(config.LogLevel)) {
	case "debug":
		proxyLogger.SetLogLevel(LevelDebug)
		upstreamLogger.SetLogLevel(LevelDebug)
	case "info":
		proxyLogger.SetLogLevel(LevelInfo)
		upstreamLogger.SetLogLevel(LevelInfo)
	case "warn":
		proxyLogger.SetLogLevel(LevelWarn)
		upstreamLogger.SetLogLevel(LevelWarn)
	case "error":
		proxyLogger.SetLogLevel(LevelError)
		upstreamLogger.SetLogLevel(LevelError)
	default:
		proxyLogger.SetLogLevel(LevelInfo)
		upstreamLogger.SetLogLevel(LevelInfo)
	}

	return NewWithLoggers(config, stdoutLogger, proxyLogger, upstreamLogger)
}

// setupGinEngine configures the Gin engine with all necessary routes and middleware
func (pm *ProxyManager) setupGinEngine() {
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

		pm.proxyLogger.Infof("Request %s \"%s %s %s\" %d %d \"%s\" %v",
			clientIP,
			method,
			path,
			c.Request.Proto,
			statusCode,
			bodySize,
			c.Request.UserAgent(),
			duration,
		)
	})

	// see: issue: #81, #77 and #42 for CORS issues
	// respond with permissive OPTIONS for any endpoint
	pm.ginEngine.Use(func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")

			// allow whatever the client requested by default
			if headers := c.Request.Header.Get("Access-Control-Request-Headers"); headers != "" {
				sanitized := SanitizeAccessControlRequestHeaderValues(headers)
				c.Header("Access-Control-Allow-Headers", sanitized)
			} else {
				c.Header(
					"Access-Control-Allow-Headers",
					"Content-Type, Authorization, Accept, X-Requested-With",
				)
			}
			c.Header("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
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
	pm.ginEngine.POST("/v1/audio/transcriptions", pm.proxyOAIPostFormHandler)

	pm.ginEngine.GET("/v1/models", pm.listModelsHandler)

	// in proxymanager_loghandlers.go
	pm.ginEngine.GET("/logs", pm.sendLogsHandlers)
	pm.ginEngine.GET("/logs/stream", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE", pm.streamLogsHandlerSSE)
	pm.ginEngine.GET("/logs/stream/:logMonitorID", pm.streamLogsHandler)
	pm.ginEngine.GET("/logs/streamSSE/:logMonitorID", pm.streamLogsHandlerSSE)

	pm.ginEngine.GET("/upstream", pm.upstreamIndex)
	pm.ginEngine.Any("/upstream/:model_id/*upstreamPath", pm.proxyToUpstream)

	pm.ginEngine.GET("/unload", pm.unloadAllModelsHandler)

	pm.ginEngine.GET("/running", pm.listRunningProcessesHandler)

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
}

// ServeHTTP implements http.Handler interface
func (pm *ProxyManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pm.ginEngine.ServeHTTP(w, r)
}

// StopProcesses acquires a lock and stops all running upstream processes.
// This is the public method safe for concurrent calls.
func (pm *ProxyManager) StopProcesses() {
	pm.Lock()
	defer pm.Unlock()

	pm.stopProcesses()
}

// stopProcesses stops all running upstream processes.
// This internal method assumes the caller holds the necessary lock.
func (pm *ProxyManager) stopProcesses() {
	if len(pm.currentProcesses) == 0 {
		pm.proxyLogger.Debug("stopProcesses called, no processes are running.")
		return
	}

	pm.proxyLogger.Debugf("Stopping %d running upstream process(es)...", len(pm.currentProcesses))

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

// Shutdown stops all processes managed by this ProxyManager
func (pm *ProxyManager) Shutdown() {
	pm.Lock()
	defer pm.Unlock()

	// First, call Shutdown() on all processes to cancel their contexts
	for _, process := range pm.currentProcesses {
		process.Shutdown()
	}

	// Then clear the map (processes are already shut down)
	pm.currentProcesses = make(map[string]*Process)
	pm.proxyLogger.Debug("ProxyManager shutdown complete")
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
		pm.proxyLogger.Debugf("No-swap, using existing process for model [%s]", requestedModel)
		return process, nil
	}

	// stop all running models
	pm.proxyLogger.Infof("Swapping model to [%s]", requestedModel)
	pm.stopProcesses()
	if profileName == "" {
		modelConfig, modelID, found := pm.config.FindConfig(realModelName)
		if !found {
			return nil, fmt.Errorf("could not find configuration for %s", realModelName)
		}

		process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.upstreamLogger, pm.proxyLogger)
		processKey := ProcessKeyName(profileName, modelID)
		pm.currentProcesses[processKey] = process
	} else {
		for _, modelName := range pm.config.Profiles[profileName] {
			if realModelName, found := pm.config.RealModelName(modelName); found {
				modelConfig, modelID, found := pm.config.FindConfig(realModelName)
				if !found {
					return nil, fmt.Errorf("could not find configuration for %s in group %s", realModelName, profileName)
				}

				process := NewProcess(modelID, pm.config.HealthCheckTimeout, modelConfig, pm.upstreamLogger, pm.proxyLogger)
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

	requestedModel := gjson.GetBytes(bodyBytes, "model").String()
	if requestedModel == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' key")
	}

	process, err := pm.swapModel(requestedModel)

	if err != nil {
		pm.sendErrorResponse(c, http.StatusNotFound, fmt.Sprintf("unable to swap to model, %s", err.Error()))
		return
	}

	// issue #69 allow custom model names to be sent to upstream
	if process.config.UseModelName != "" {
		bodyBytes, err = sjson.SetBytes(bodyBytes, "model", process.config.UseModelName)
		if err != nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error updating JSON: %s", err.Error()))
			return
		}
	} else {
		profileName, modelName := splitRequestedModel(requestedModel)
		if profileName != "" {
			bodyBytes, err = sjson.SetBytes(bodyBytes, "model", modelName)
			if err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error updating JSON: %s", err.Error()))
				return
			}
		}
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// dechunk it as we already have all the body bytes see issue #11
	c.Request.Header.Del("transfer-encoding")
	c.Request.Header.Add("content-length", strconv.Itoa(len(bodyBytes)))

	process.ProxyRequest(c.Writer, c.Request)

}

func (pm *ProxyManager) proxyOAIPostFormHandler(c *gin.Context) {
	// We need to reconstruct the multipart form in any case since the body is consumed
	// Create a new buffer for the reconstructed request
	var requestBuffer bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBuffer)

	// Parse multipart form
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32MB max memory, larger files go to tmp disk
		pm.sendErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("error parsing multipart form: %s", err.Error()))
		return
	}

	// Get model parameter from the form
	requestedModel := c.Request.FormValue("model")
	if requestedModel == "" {
		pm.sendErrorResponse(c, http.StatusBadRequest, "missing or invalid 'model' parameter in form data")
		return
	}

	// Swap to the requested model
	process, err := pm.swapModel(requestedModel)
	if err != nil {
		pm.sendErrorResponse(c, http.StatusNotFound, fmt.Sprintf("unable to swap to model, %s", err.Error()))
		return
	}

	// Get profile name and model name from the requested model
	profileName, modelName := splitRequestedModel(requestedModel)

	// Copy all form values
	for key, values := range c.Request.MultipartForm.Value {
		for _, value := range values {
			fieldValue := value
			// If this is the model field and we have a profile, use just the model name
			if key == "model" {
				if process.config.UseModelName != "" {
					fieldValue = process.config.UseModelName
				} else if profileName != "" {
					fieldValue = modelName
				}
			}
			field, err := multipartWriter.CreateFormField(key)
			if err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, "error recreating form field")
				return
			}
			if _, err = field.Write([]byte(fieldValue)); err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, "error writing form field")
				return
			}
		}
	}

	// Copy all files from the original request
	for key, fileHeaders := range c.Request.MultipartForm.File {
		for _, fileHeader := range fileHeaders {
			formFile, err := multipartWriter.CreateFormFile(key, fileHeader.Filename)
			if err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, "error recreating form file")
				return
			}

			file, err := fileHeader.Open()
			if err != nil {
				pm.sendErrorResponse(c, http.StatusInternalServerError, "error opening uploaded file")
				return
			}

			if _, err = io.Copy(formFile, file); err != nil {
				file.Close()
				pm.sendErrorResponse(c, http.StatusInternalServerError, "error copying file data")
				return
			}
			file.Close()
		}
	}

	// Close the multipart writer to finalize the form
	if err := multipartWriter.Close(); err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, "error finalizing multipart form")
		return
	}

	// Create a new request with the reconstructed form data
	modifiedReq, err := http.NewRequestWithContext(
		c.Request.Context(),
		c.Request.Method,
		c.Request.URL.String(),
		&requestBuffer,
	)
	if err != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, "error creating modified request")
		return
	}

	// Copy the headers from the original request
	modifiedReq.Header = c.Request.Header.Clone()
	modifiedReq.Header.Set("Content-Type", multipartWriter.FormDataContentType())

	// Use the modified request for proxying
	process.ProxyRequest(c.Writer, modifiedReq)
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
}

func (pm *ProxyManager) listRunningProcessesHandler(context *gin.Context) {
	context.Header("Content-Type", "application/json")
	runningProcesses := make([]gin.H, 0) // Default to an empty response.

	for _, process := range pm.currentProcesses {

		// Append the process ID and State (multiple entries if profiles are being used).
		runningProcesses = append(runningProcesses, gin.H{
			"model": process.ID,
			"state": process.state,
		})

	}

	// Put the results under the `running` key.
	response := gin.H{
		"running": runningProcesses,
	}

	context.JSON(http.StatusOK, response) // Always return 200 OK
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
