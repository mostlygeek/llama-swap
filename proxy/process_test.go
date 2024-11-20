package proxy

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func getTestSimpleResponderConfig(expectedMessage string) ModelConfig {
	// Define the range
	min := 12000
	max := 13000

	// Generate a random number between 12000 and 13000
	randomPort := rand.Intn(max-min+1) + min
	binaryPath := getBinaryPath()

	// Create a process configuration
	return ModelConfig{
		Cmd:           fmt.Sprintf("%s --port %d --respond '%s'", binaryPath, randomPort, expectedMessage),
		Proxy:         fmt.Sprintf("http://127.0.0.1:%d", randomPort),
		CheckEndpoint: "/health",
	}
}

func TestProcess_AutomaticallyStartsUpstream(t *testing.T) {
	logMonitor := NewLogMonitorWriter(io.Discard)
	expectedMessage := "testing91931"
	config := getTestSimpleResponderConfig(expectedMessage)

	// Create a process
	process := NewProcess("test-process", 5, config, logMonitor)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// process is automatically started
	assert.False(t, process.IsRunning())
	process.ProxyRequest(w, req)
	assert.True(t, process.IsRunning())

	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), expectedMessage)

	// Stop the process
	process.Stop()

	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()

	// Proxy the request
	process.ProxyRequest(w, req)

	// should have automatically started the process again
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
}

// test that the automatic start returns the expected error type
func TestProcess_BrokenModelConfig(t *testing.T) {
	// Create a process configuration
	config := ModelConfig{
		Cmd:           "nonexistant-command",
		Proxy:         "http://127.0.0.1:9913",
		CheckEndpoint: "/health",
	}

	process := NewProcess("broken", 1, config, NewLogMonitor())
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	process.ProxyRequest(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "unable to start process")
}

func TestProcess_UnloadAfterTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long auto unload TTL test")
	}

	expectedMessage := "I_sense_imminent_danger"
	config := getTestSimpleResponderConfig(expectedMessage)
	assert.Equal(t, 0, config.UnloadAfter)
	config.UnloadAfter = 3 // seconds
	assert.Equal(t, 3, config.UnloadAfter)

	process := NewProcess("ttl", 2, config, NewLogMonitorWriter(io.Discard))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Proxy the request (auto start)
	process.ProxyRequest(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "Expected status code %d, got %d", http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), expectedMessage)

	assert.True(t, process.IsRunning())

	// wait 5 seconds
	time.Sleep(5 * time.Second)

	assert.False(t, process.IsRunning())
}
