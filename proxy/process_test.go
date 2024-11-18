package proxy

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Check if the binary exists
func TestMain(m *testing.M) {
	binaryPath := getBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fmt.Printf("simple-responder not found at %s, did you `make simple-responder`?\n", binaryPath)
		os.Exit(1)
	}
	m.Run()
}

// Helper function to get the binary path
func getBinaryPath() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	return filepath.Join("..", "build", fmt.Sprintf("simple-responder_%s_%s", goos, goarch))
}

func TestProcess_ProcessStartStop(t *testing.T) {
	// Define the range
	min := 12000
	max := 13000

	// Generate a random number between 12000 and 13000
	randomPort := rand.Intn(max-min+1) + min

	binaryPath := getBinaryPath()

	// Create a log monitor
	logMonitor := NewLogMonitor()

	expectedMessage := "testing91931"

	// Create a process configuration
	config := ModelConfig{
		Cmd:           fmt.Sprintf("%s --port %d --respond '%s'", binaryPath, randomPort, expectedMessage),
		Proxy:         fmt.Sprintf("http://127.0.0.1:%d", randomPort),
		CheckEndpoint: "/health",
	}

	// Create a process
	process := NewProcess("test-process", config, logMonitor)

	// Start the process
	t.Logf("Starting %s on port %d", binaryPath, randomPort)
	err := process.Start(5)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Create a test request
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Proxy the request
	process.ProxyRequest(w, req)

	// Check the response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	if !strings.Contains(w.Body.String(), expectedMessage) {
		t.Errorf("Expected body to contain '%s', got %q", expectedMessage, w.Body.String())
	}

	// Stop the process
	process.Stop()

	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()

	// Proxy the request
	process.ProxyRequest(w, req)

	// Check the response
	if w.Code == http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
	}
}
