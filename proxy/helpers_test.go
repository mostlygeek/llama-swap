package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

var (
	nextTestPort int = 12000
	portMutex    sync.Mutex
	testLogger   = NewLogMonitorWriter(os.Stdout)
)

// Check if the binary exists
func TestMain(m *testing.M) {
	binaryPath := getSimpleResponderPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		fmt.Printf("simple-responder not found at %s, did you `make simple-responder`?\n", binaryPath)
		os.Exit(1)
	}

	gin.SetMode(gin.TestMode)

	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		testLogger.SetLogLevel(LevelDebug)
	case "warn":
		testLogger.SetLogLevel(LevelWarn)
	case "info":
		testLogger.SetLogLevel(LevelInfo)
	default:
		testLogger.SetLogLevel(LevelWarn)
	}

	m.Run()
}

// Helper function to get the binary path
func getSimpleResponderPath() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	return filepath.Join("..", "build", fmt.Sprintf("simple-responder_%s_%s", goos, goarch))
}

func getTestSimpleResponderConfig(expectedMessage string) ModelConfig {
	portMutex.Lock()
	defer portMutex.Unlock()

	port := nextTestPort
	nextTestPort++

	return getTestSimpleResponderConfigPort(expectedMessage, port)
}

func getTestSimpleResponderConfigPort(expectedMessage string, port int) ModelConfig {
	binaryPath := getSimpleResponderPath()

	// Create a process configuration
	return ModelConfig{
		Cmd:           fmt.Sprintf("%s --port %d --silent --respond %s", binaryPath, port, expectedMessage), // Re-added --silent
		Proxy:         fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint: "/health",
	}
}
