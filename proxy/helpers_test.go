package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

// testConfigFromYAML substitutes {{RESPONDER}} with the simple-responder path and
// loads through the real config pipeline (env vars, macros, port assignment, etc.)
func testConfigFromYAML(t *testing.T, yamlTmpl string) config.Config {
	t.Helper()
	yamlStr := strings.ReplaceAll(yamlTmpl, "{{RESPONDER}}", filepath.ToSlash(simpleResponderPath))
	cfg, err := config.LoadConfigFromReader(strings.NewReader(yamlStr))
	require.NoError(t, err)
	return cfg
}

func getTestSimpleResponderConfig(expectedMessage string) config.ModelConfig {
	return getTestSimpleResponderConfigPort(expectedMessage, getTestPort())
}

func getTestSimpleResponderConfigPort(expectedMessage string, port int) config.ModelConfig {
	// Convert path to forward slashes for cross-platform compatibility
	// Windows handles forward slashes in paths correctly
	cmdPath := filepath.ToSlash(simpleResponderPath)

	// Create a YAML string with just the values we want to set
	yamlStr := fmt.Sprintf(`
cmd: '%s --port %d --silent --respond %s'
proxy: "http://127.0.0.1:%d"
`, cmdPath, port, expectedMessage, port)

	var cfg config.ModelConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &cfg); err != nil {
		panic(fmt.Sprintf("failed to unmarshal test config: %v in [%s]", err, yamlStr))
	}

	return cfg
}

// injectTestHandlers sets a testHandler on every Process in every ProcessGroup
// of the given ProxyManager, bypassing subprocess launches. modelResponses maps
// model IDs to their respond strings; if a model ID is not in the map, the model
// ID itself is used.
func injectTestHandlers(pm *ProxyManager, modelResponses map[string]string) {
	for _, pg := range pm.processGroups {
		for modelID, process := range pg.processes {
			respond := modelID
			if r, ok := modelResponses[modelID]; ok {
				respond = r
			}
			process.testHandler = newTestHandler(respond)
		}
	}
}

// newTestHandler returns an http.Handler that mimics simple-responder's API.
// It supports the endpoints that routing tests depend on, without launching
// any subprocess or binding any port.
func respondJSON(w http.ResponseWriter, respond string, bodyBytes []byte) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"responseMessage":  respond,
		"h_content_length": strconv.Itoa(len(bodyBytes)),
		"request_body":     string(bodyBytes),
		"usage": map[string]any{
			"completion_tokens": 10, "prompt_tokens": 25, "total_tokens": 35,
		},
		"timings": map[string]any{
			"prompt_n": 25, "prompt_ms": 13, "predicted_n": 10,
			"predicted_ms": 17, "predicted_per_second": 10,
		},
	})
}

func newTestHandler(respond string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		isStreaming := r.URL.Query().Get("stream") == "true"

		if wait := r.URL.Query().Get("wait"); wait != "" {
			if d, err := time.ParseDuration(wait); err == nil {
				time.Sleep(d)
			}
		}

		if isStreaming {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher := w.(http.Flusher)

			for i := 0; i < 10; i++ {
				data, _ := json.Marshal(map[string]any{
					"created": time.Now().Unix(),
					"choices": []map[string]any{
						{"index": 0, "delta": map[string]any{"content": "asdf"}, "finish_reason": nil},
					},
				})
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
				flusher.Flush()
			}

			finalData, _ := json.Marshal(map[string]any{
				"usage": map[string]any{
					"completion_tokens": 10, "prompt_tokens": 25, "total_tokens": 35,
				},
				"timings": map[string]any{
					"prompt_n": 25, "prompt_ms": 13, "predicted_n": 10,
					"predicted_ms": 17, "predicted_per_second": 10,
				},
			})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", finalData)
			flusher.Flush()

			fmt.Fprintf(w, "event: message\ndata: [DONE]\n\n")
			flusher.Flush()
		} else {
			respondJSON(w, respond, bodyBytes)
		}
	})

	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelName := gjson.GetBytes(body, "model").String()
		if modelName != respond {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Invalid model: %s, expected: %s", modelName, respond)})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	})

	mux.HandleFunc("/v1/completions", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		respondJSON(w, respond, bodyBytes)
	})

	for _, path := range []string{
		"/chat/completions", "/completions",
		"/responses", "/messages", "/messages/count_tokens",
		"/embeddings", "/rerank", "/reranking",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			bodyBytes, _ := io.ReadAll(r.Body)
			respondJSON(w, respond, bodyBytes)
		})
	}

	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"responseMessage": respond,
			"usage": map[string]any{
				"completion_tokens": 10, "prompt_tokens": 25, "total_tokens": 35,
			},
		})
	})

	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Error parsing multipart form: %s", err)})
			return
		}
		model := r.FormValue("model")
		if model == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Missing model parameter"})
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Error getting file: %s", err)})
			return
		}
		fileBytes, _ := io.ReadAll(file)
		file.Close()
		json.NewEncoder(w).Encode(map[string]any{
			"text":             fmt.Sprintf("The length of the file is %d bytes", len(fileBytes)),
			"model":            model,
			"h_content_type":   r.Header.Get("Content-Type"),
			"h_content_length": r.Header.Get("Content-Length"),
		})
	})

	mux.HandleFunc("/v1/audio/voices", func(w http.ResponseWriter, r *http.Request) {
		model := r.URL.Query().Get("model")
		json.NewEncoder(w).Encode(map[string]any{
			"voices": []string{"voice1"}, "model": model,
		})
	})

	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, respond)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
	})

	mux.HandleFunc("/sdapi/v1/txt2img", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelName := gjson.GetBytes(body, "model").String()
		json.NewEncoder(w).Encode(map[string]any{
			"model": modelName, "images": []string{},
		})
	})

	mux.HandleFunc("/sdapi/v1/img2img", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelName := gjson.GetBytes(body, "model").String()
		json.NewEncoder(w).Encode(map[string]any{
			"model": modelName, "images": []string{},
		})
	})

	mux.HandleFunc("/sdapi/v1/loras", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"loras": []string{},
		})
	})

	return mux
}
