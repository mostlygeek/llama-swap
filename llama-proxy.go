package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mostlygeek/go-llama-cpp-proxy/config"
)

type ServiceState struct {
	sync.Mutex
	currentCmd   *exec.Cmd
	currentModel string
}

func startService(command string) (*exec.Cmd, error) {
	args := strings.Fields(command)
	cmd := exec.Command(args[0], args[1:]...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return cmd, nil
}

func checkHealthEndpoint(healthURL string, maxDuration time.Duration) error {
	client := &http.Client{}

	startTime := time.Now()
	for {
		req, err := http.NewRequest("GET", healthURL, nil)
		if err != nil {
			return err
		}

		// Set request timeout
		ctx, cancel := context.WithTimeout(req.Context(), 250*time.Millisecond)
		defer cancel()

		// Execute the request with the context
		req = req.WithContext(ctx)
		resp, err := client.Do(req)
		if err != nil {
			// Log error and check elapsed time before retrying
			if time.Since(startTime) >= maxDuration {
				return fmt.Errorf("failed to get a healthy response from: %s", healthURL)
			}

			// Wait a second before retrying
			time.Sleep(time.Second)
			continue
		}

		// Close response body
		defer resp.Body.Close()

		// Check if we got a 200 OK response
		if resp.StatusCode == http.StatusOK {
			return nil // Health check succeeded
		}

		// Check elapsed time before retrying
		if time.Since(startTime) >= maxDuration {
			return fmt.Errorf("failed to get a healthy response from: %s", healthURL)
		}

		// Wait a second before retrying
		time.Sleep(time.Second)
	}
}

func proxyChatRequest(w http.ResponseWriter, r *http.Request, config *config.Config, state *ServiceState) {
	// Read the original request body
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

	modelConfig, ok := config.Models[model]
	if !ok {
		http.Error(w, "Model not found in configuration", http.StatusNotFound)
		return
	}

	err = error(nil)
	state.Lock()
	defer state.Unlock()

	if state.currentModel != model {
		if state.currentCmd != nil {
			state.currentCmd.Process.Signal(syscall.SIGTERM)
		}
		state.currentCmd, err = startService(modelConfig.Cmd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.currentModel = model

		// Check the /health endpoint
		healthURL := modelConfig.Proxy + "/health"
		err = checkHealthEndpoint(healthURL, 30*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	// replace r.Body so it can be read again
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	proxyRequest(modelConfig.Proxy, w, r)
}

func proxyRequest(proxyHost string, w http.ResponseWriter, r *http.Request) {
	client := &http.Client{}
	req, err := http.NewRequest(r.Method, proxyHost+r.URL.String(), r.Body)
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

	buf := make([]byte, 32*1024) // Buffer size set to 32KB
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				http.Error(w, writeErr.Error(), http.StatusInternalServerError)
				return
			}
			// Flush the buffer to the client
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

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", ":8080", "listen ip/port")

	flag.Parse() // Parse the command-line flags

	config, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	serviceState := &ServiceState{}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			proxyChatRequest(w, r, config, serviceState)
		} else {
			http.Error(w, "Endpoint not supported", http.StatusNotFound)
		}
	})

	fmt.Println("Proxy server started on :8080")
	if err := http.ListenAndServe(*listenStr, nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}
