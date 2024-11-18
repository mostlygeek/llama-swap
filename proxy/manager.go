package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type ProxyManager struct {
	sync.Mutex

	config         *Config
	currentProcess *Process
	logMonitor     *LogMonitor
}

func New(config *Config) *ProxyManager {
	return &ProxyManager{config: config, currentProcess: nil, logMonitor: NewLogMonitor()}
}

func (pm *ProxyManager) HandleFunc(w http.ResponseWriter, r *http.Request) {

	// https://github.com/ggerganov/llama.cpp/blob/master/examples/server/README.md#api-endpoints
	if r.URL.Path == "/v1/chat/completions" {
		// extracts the `model` from json body
		pm.proxyChatRequest(w, r)
	} else if r.URL.Path == "/v1/models" {
		pm.listModels(w, r)
	} else if r.URL.Path == "/logs" {
		pm.streamLogs(w, r)
	} else {
		if pm.currentProcess != nil {
			pm.currentProcess.ProxyRequest(w, r)
		} else {
			http.Error(w, "no strategy to handle request", http.StatusBadRequest)
		}
	}
}

func (pm *ProxyManager) streamLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := r.Context().Done()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	skipHistory := r.URL.Query().Has("skip")
	if !skipHistory {
		// Send history first
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			w.Write(history)
			flusher.Flush()
		}
	}

	if !r.URL.Query().Has("stream") {
		return
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func (pm *ProxyManager) listModels(w http.ResponseWriter, _ *http.Request) {
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
	w.Header().Set("Content-Type", "application/json")

	// Encode the data as JSON and write it to the response writer
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
		http.Error(w, "Error encoding JSON", http.StatusInternalServerError)
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

func (pm *ProxyManager) proxyChatRequest(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	model, ok := requestBody["model"].(string)
	if !ok {
		http.Error(w, "Missing or invalid 'model' key", http.StatusBadRequest)
		return
	}

	if err := pm.swapModel(model); err != nil {
		http.Error(w, fmt.Sprintf("unable to swap to model, %s", err.Error()), http.StatusNotFound)
		return
	}

	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	pm.currentProcess.ProxyRequest(w, r)
}
