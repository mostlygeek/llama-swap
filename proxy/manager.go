package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProxyManager struct {
	sync.Mutex

	config       *Config
	currentCmd   *exec.Cmd
	currentModel string
	currentProxy string
}

func New(config *Config) *ProxyManager {
	return &ProxyManager{config: config}
}

func (pm *ProxyManager) HandleFunc(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/chat/completions" {
		pm.proxyChatRequest(w, r)
	} else {
		http.Error(w, "Endpoint not supported", http.StatusNotFound)
	}
}

func (pm *ProxyManager) swapModel(model string) error {
	pm.Lock()
	defer pm.Unlock()

	if model == pm.currentModel {
		return nil
	}

	modelConfig, ok := pm.config.Models[model]
	if !ok {
		return fmt.Errorf("unknown model %s", model)
	}

	if pm.currentCmd != nil {
		pm.currentCmd.Process.Signal(syscall.SIGTERM)
	}

	pm.currentModel = model
	pm.currentProxy = modelConfig.Proxy

	args := strings.Fields(modelConfig.Cmd)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	if err != nil {
		return err
	}
	pm.currentCmd = cmd

	if err := pm.checkHealthEndpoint(60 * time.Second); err != nil {
		return err
	}

	return nil
}

func (pm *ProxyManager) checkHealthEndpoint(maxDuration time.Duration) error {

	if pm.currentProxy == "" {
		return fmt.Errorf("no upstream available to check /health")
	}

	healthURL := pm.currentProxy + "/health"
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
				return fmt.Errorf("failed to check /healthy from: %s", healthURL)
			}
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if time.Since(startTime) >= maxDuration {
			return fmt.Errorf("failed to check /healthy from: %s", healthURL)
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

	pm.swapModel(model)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	pm.proxyRequest(w, r)
}

func (pm *ProxyManager) proxyRequest(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{}
	req, err := http.NewRequest(r.Method, pm.currentProxy+r.URL.String(), r.Body)
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
