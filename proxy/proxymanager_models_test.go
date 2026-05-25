package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

// TestProxyManager_parseModelPath verifies extraction of the model file path from a cmd string.
func TestProxyManager_parseModelPath(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"llama-server --port 8080 --model /mnt/models/foo.gguf --host 127.0.0.1", "/mnt/models/foo.gguf"},
		{"llama-server -m /mnt/models/bar.gguf --port ${PORT}", "/mnt/models/bar.gguf"},
		{"llama-server --model=/mnt/models/baz.gguf --port 8080", "/mnt/models/baz.gguf"},
		// multiline YAML block scalar (| style) embeds \n between arguments
		{"llama-server --port ${PORT}\n  --model /mnt/models/foo.gguf\n  --ctx-size 16384", "/mnt/models/foo.gguf"},
		{"llama-server --port 8080 --host 127.0.0.1", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := parseModelPath(tc.cmd)
		if got != tc.want {
			t.Errorf("parseModelPath(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// TestProxyManager_resolveHFSource verifies URL and filename parsing for HuggingFace model identifiers.
func TestProxyManager_resolveHFSource(t *testing.T) {
	cases := []struct {
		model    string
		wantURL  string
		wantFile string
		wantErr  bool
	}{
		{
			model:    "bartowski/Qwen2.5-14B-Instruct-GGUF/Qwen2.5-14B-Instruct-Q5_K_M.gguf",
			wantURL:  "https://huggingface.co/bartowski/Qwen2.5-14B-Instruct-GGUF/resolve/main/Qwen2.5-14B-Instruct-Q5_K_M.gguf",
			wantFile: "Qwen2.5-14B-Instruct-Q5_K_M.gguf",
		},
		{
			model:    "https://huggingface.co/owner/repo/resolve/main/model.gguf",
			wantURL:  "https://huggingface.co/owner/repo/resolve/main/model.gguf",
			wantFile: "model.gguf",
		},
		// localhost HTTP is allowed (tests / local servers)
		{
			model:    "http://127.0.0.1:8080/model.gguf",
			wantURL:  "http://127.0.0.1:8080/model.gguf",
			wantFile: "model.gguf",
		},
		{
			model:   "bad-format",
			wantErr: true,
		},
		// SSRF: non-HuggingFace remote host rejected
		{
			model:   "https://evil.example.com/model.gguf",
			wantErr: true,
		},
		// Plain HTTP to remote host rejected
		{
			model:   "http://huggingface.co/owner/repo/resolve/main/model.gguf",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		gotURL, file, err := resolveHFSource(tc.model)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveHFSource(%q) expected error, got nil", tc.model)
			}
			continue
		}
		if err != nil {
			t.Fatalf("resolveHFSource(%q) unexpected error: %v", tc.model, err)
		}
		if gotURL != tc.wantURL {
			t.Errorf("url = %q, want %q", gotURL, tc.wantURL)
		}
		if file != tc.wantFile {
			t.Errorf("filename = %q, want %q", file, tc.wantFile)
		}
	}
}

// TestProxyManager_apiGetStorage verifies disk info is returned for the configured models dir.
func TestProxyManager_apiGetStorage(t *testing.T) {
	dir := t.TempDir()
	pm := New(config.Config{
		ModelsDir: dir,
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/storage", nil)
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/storage: %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["models_dir"] != dir {
		t.Errorf("models_dir = %v, want %q", body["models_dir"], dir)
	}
	if _, ok := body["available_bytes"]; !ok {
		t.Error("missing available_bytes")
	}
	if _, ok := body["total_bytes"]; !ok {
		t.Error("missing total_bytes")
	}
}

// TestAPIGetStorageInferred verifies the models dir is inferred from cmd when not configured.
func TestProxyManager_apiGetStorageInferred(t *testing.T) {
	dir := t.TempDir()
	modelFile := filepath.Join(dir, "model.gguf")
	pm := New(config.Config{
		Models: map[string]config.ModelConfig{
			"stub": {
				Cmd:   "llama-server --port ${PORT} --model " + modelFile,
				Proxy: "http://localhost:${PORT}",
			},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/storage", nil)
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/storage: %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["models_dir"] != dir {
		t.Errorf("models_dir inferred as %v, want %q", body["models_dir"], dir)
	}
}

// TestAPIPullModel verifies streaming download against a local test server.
func TestProxyManager_apiPullModel(t *testing.T) {
	fileContent := strings.Repeat("X", 1024)

	// Fake HuggingFace origin server.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fileContent))
	}))
	defer origin.Close()

	dir := t.TempDir()
	pm := New(config.Config{
		ModelsDir: dir,
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	// Use a full URL pointing at origin so no real HF request is made.
	modelURL := origin.URL + "/model.gguf"
	body := strings.NewReader(`{"model":"` + modelURL + `","stream":false}`)
	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/pull", body)
	req.Header.Set("Content-Type", "application/json")
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/models/pull: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "success" {
		t.Errorf("status = %v, want success", resp["status"])
	}
	destPath, _ := resp["path"].(string)
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("downloaded file not found at %q: %v", destPath, err)
	}
	if string(data) != fileContent {
		t.Errorf("file content mismatch: got %d bytes, want %d", len(data), len(fileContent))
	}
}

// TestAPIPullModelSubdir verifies files land in the requested subdirectory.
func TestProxyManager_apiPullModelSubdir(t *testing.T) {
	fileContent := strings.Repeat("Y", 512)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fileContent))
	}))
	defer origin.Close()

	dir := t.TempDir()
	pm := New(config.Config{
		ModelsDir: dir,
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	body := strings.NewReader(`{"model":"` + origin.URL + `/model.gguf","subdir":"my-model","stream":false}`)
	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/pull", body)
	req.Header.Set("Content-Type", "application/json")
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/models/pull with subdir: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	expectedPath := filepath.Join(dir, "my-model", "model.gguf")
	if resp["path"] != expectedPath {
		t.Errorf("path = %v, want %q", resp["path"], expectedPath)
	}
	if data, err := os.ReadFile(expectedPath); err != nil || string(data) != fileContent {
		t.Errorf("file not found or content mismatch at %q", expectedPath)
	}
}

// TestProxyManager_apiPullModelGated verifies a 401 from HF propagates as an error response
// with the correct status code and a gated-token hint in the body.
func TestProxyManager_apiPullModelGated(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer origin.Close()

	dir := t.TempDir()
	pm := New(config.Config{
		ModelsDir: dir,
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	modelURL := origin.URL + "/model.gguf"
	body := strings.NewReader(`{"model":"` + modelURL + `","stream":false}`)
	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/pull", body)
	req.Header.Set("Content-Type", "application/json")
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("expected 401 or 403, got %d: %s", w.Code, w.Body.String())
	}
	var errResp map[string]any
	if jsonErr := json.NewDecoder(w.Body).Decode(&errResp); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v", jsonErr)
	}
	errMsg, _ := errResp["error"].(string)
	if !strings.Contains(errMsg, "gated") && !strings.Contains(errMsg, "token") {
		t.Errorf("error body %q should mention gated/token hint", errMsg)
	}
}

// TestProxyManager_apiPullModelStreaming verifies ndjson streaming emits downloading + success events.
func TestProxyManager_apiPullModelStreaming(t *testing.T) {
	fileContent := strings.Repeat("Z", 2048)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "2048")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fileContent))
	}))
	defer origin.Close()

	dir := t.TempDir()
	pm := New(config.Config{
		ModelsDir: dir,
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	modelURL := origin.URL + "/stream.gguf"
	// Omit "stream" field so it defaults to true.
	body := strings.NewReader(`{"model":"` + modelURL + `"}`)
	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/pull", body)
	req.Header.Set("Content-Type", "application/json")
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/models/pull (stream): %d %s", w.Code, w.Body.String())
	}

	// Parse ndjson lines.
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid ndjson line %q: %v", line, err)
		}
		events = append(events, ev)
	}

	var sawDownloading, sawSuccess bool
	for _, ev := range events {
		switch ev["status"] {
		case "downloading":
			sawDownloading = true
		case "success":
			sawSuccess = true
		}
	}
	if !sawDownloading {
		t.Error("no 'downloading' event in ndjson stream")
	}
	if !sawSuccess {
		t.Error("no 'success' event in ndjson stream")
	}
}

// TestAPIDeleteModel verifies the model file is deleted from disk.
func TestProxyManager_apiDeleteModel(t *testing.T) {
	dir := t.TempDir()
	modelFile := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelFile, []byte("fake weights"), 0o644); err != nil {
		t.Fatal(err)
	}

	pm := New(config.Config{
		Models: map[string]config.ModelConfig{
			"my-model": {
				Cmd:   "llama-server --port ${PORT} --model " + modelFile,
				Proxy: "http://localhost:${PORT}",
			},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/models/my-model", nil)
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/models/my-model: %d %s", w.Code, w.Body.String())
	}
	if _, statErr := os.Stat(modelFile); !os.IsNotExist(statErr) {
		t.Error("model file still exists after DELETE")
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["deleted"] != modelFile {
		t.Errorf("deleted = %v, want %q", resp["deleted"], modelFile)
	}
}

// TestAPIDeleteModelNotFound verifies 404 for an unknown model ID.
func TestProxyManager_apiDeleteModelNotFound(t *testing.T) {
	pm := New(config.Config{
		Models: map[string]config.ModelConfig{
			"stub": {Cmd: "echo", Proxy: "http://localhost:${PORT}"},
		},
	})
	defer pm.StopProcesses(StopImmediately)

	w := CreateTestResponseRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/models/does-not-exist", nil)
	pm.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
