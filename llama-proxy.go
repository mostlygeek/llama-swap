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

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Cmd   string `yaml:"cmd"`
	Proxy string `yaml:"proxy"`
}

type Config struct {
	Models map[string]ModelConfig `yaml:"models"`
}

type ServiceState struct {
	sync.Mutex
	currentCmd   *exec.Cmd
	currentModel string
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func startService(command string) (*exec.Cmd, error) {
	args := strings.Fields(command)
	cmd := exec.Command(args[0], args[1:]...)

	// write it to the stdout/stderr of the proxy
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return cmd, nil
}

func checkHealthEndpoint(client *http.Client, healthURL string, maxDuration time.Duration) error {
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

func proxyRequest(w http.ResponseWriter, r *http.Request, config *Config, state *ServiceState) {
	client := &http.Client{}

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
		err = checkHealthEndpoint(client, healthURL, 30*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	req, err := http.NewRequest(r.Method, modelConfig.Proxy+r.URL.String(), io.NopCloser(bytes.NewBuffer(bodyBytes)))
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

	io.Copy(w, resp.Body)
}

func main() {
	// Define a command-line flag for the port
	configPath := flag.String("config", "config.yaml", "config file name")
	listenStr := flag.String("listen", ":8080", "listen ip/port")

	flag.Parse() // Parse the command-line flags

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	serviceState := &ServiceState{}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			proxyRequest(w, r, config, serviceState)
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
