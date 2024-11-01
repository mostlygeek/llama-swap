package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProxyManager struct {
	sync.Mutex

	config        *Config
	currentCmd    *exec.Cmd
	currentConfig ModelConfig
	logMonitor    *LogMonitor
}

func New(config *Config) *ProxyManager {
	return &ProxyManager{config: config, logMonitor: NewLogMonitor()}
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
		pm.proxyRequest(w, r)
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
	modelConfig, found := pm.config.FindConfig(requestedModel)
	if !found {
		return fmt.Errorf("could not find configuration for %s", requestedModel)
	}

	// no need to swap llama.cpp instances
	if pm.currentConfig.Cmd == modelConfig.Cmd {
		return nil
	}

	// kill the current running one to swap it
	if pm.currentCmd != nil {
		pm.currentCmd.Process.Signal(syscall.SIGTERM)

		// wait for it to end
		pm.currentCmd.Process.Wait()
	}

	pm.currentConfig = modelConfig

	args, err := modelConfig.SanitizedCommand()
	if err != nil {
		return fmt.Errorf("unable to get sanitized command: %v", err)
	}
	cmd := exec.Command(args[0], args[1:]...)

	// logMonitor only writes to stdout
	// so the upstream's stderr will go to os.Stdout
	cmd.Stdout = pm.logMonitor
	cmd.Stderr = pm.logMonitor

	cmd.Env = modelConfig.Env

	err = cmd.Start()
	if err != nil {
		return err
	}
	pm.currentCmd = cmd

	if err := pm.checkHealthEndpoint(); err != nil {
		return err
	}

	return nil
}

func (pm *ProxyManager) checkHealthEndpoint() error {

	if pm.currentConfig.Proxy == "" {
		return fmt.Errorf("no upstream available to check /health")
	}

	checkEndpoint := strings.TrimSpace(pm.currentConfig.CheckEndpoint)

	if checkEndpoint == "none" {
		return nil
	}

	// keep default behaviour
	if checkEndpoint == "" {
		checkEndpoint = "/health"
	}

	proxyTo := pm.currentConfig.Proxy
	maxDuration := time.Second * time.Duration(pm.config.HealthCheckTimeout)
	healthURL, err := url.JoinPath(proxyTo, checkEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create health url with with %s and path %s", proxyTo, checkEndpoint)
	}
	client := &http.Client{}
	startTime := time.Now()

	for {
		req, err := http.NewRequest("GET", healthURL, nil)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(req.Context(), 250*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)
		resp, err := client.Do(req)

		if err != nil {
			if time.Since(startTime) >= maxDuration {
				return fmt.Errorf("failed to check health from: %s", healthURL)
			}

			// wait a bit longer for TCP connection issues
			if strings.Contains(err.Error(), "connection refused") {
				fmt.Fprintf(pm.logMonitor, "Connection refused on %s\n", healthURL)
				time.Sleep(5 * time.Second)
			} else {
				time.Sleep(time.Second)
			}

			continue
		}

		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if time.Since(startTime) >= maxDuration {
			return fmt.Errorf("failed to check health from: %s", healthURL)
		}

		time.Sleep(time.Second)
	}
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
		http.Error(w, fmt.Sprintf("unable to swap to model: %s", err.Error()), http.StatusNotFound)
		return
	}

	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	pm.proxyRequest(w, r)
}

func (pm *ProxyManager) proxyRequest(w http.ResponseWriter, r *http.Request) {
	if pm.currentConfig.Proxy == "" {
		http.Error(w, "No upstream proxy", http.StatusInternalServerError)
		return
	}

	proxyTo := pm.currentConfig.Proxy

	client := &http.Client{}
	req, err := http.NewRequest(r.Method, proxyTo+r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header = r.Header
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// faster than io.Copy when streaming
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				http.Error(w, writeErr.Error(), http.StatusInternalServerError)
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
}
