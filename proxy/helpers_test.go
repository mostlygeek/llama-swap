package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

var (
	nextTestPort        int = 12000
	portMutex           sync.Mutex
	testLogger          = NewLogMonitorWriter(os.Stdout)
	simpleResponderPath = getSimpleResponderPath()
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

	if goos == "windows" {
		return filepath.Join("..", "build", "simple-responder.exe")
	} else {
		return filepath.Join("..", "build", fmt.Sprintf("simple-responder_%s_%s", goos, goarch))
	}
}

func getTestPort() int {
	portMutex.Lock()
	defer portMutex.Unlock()

	port := nextTestPort
	nextTestPort++

	return port
}

func getTestSimpleResponderConfig(expectedMessage string) ModelConfig {
	return getTestSimpleResponderConfigPort(expectedMessage, getTestPort())
}

func getTestSimpleResponderConfigPort(expectedMessage string, port int) ModelConfig {
	// Create a YAML string with just the values we want to set
	yamlStr := fmt.Sprintf(`
cmd: '%s --port %d --silent --respond %s'
proxy: "http://127.0.0.1:%d"
`, simpleResponderPath, port, expectedMessage, port)

	var cfg ModelConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &cfg); err != nil {
		panic(fmt.Sprintf("failed to unmarshal test config: %v in [%s]", err, yamlStr))
	}

	return cfg
}
