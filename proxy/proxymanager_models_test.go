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

// TestParseModelPath verifies extraction of the model file path from a cmd string.
func TestParseModelPath(t *testing.T) {
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

// TestResolveHFSource verifies URL and filename parsing for HuggingFace model identifiers.
func TestResolveHFSource(t *testing.T) {
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
		{
			model:   "bad-format",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		url, file, err := resolveHFSource(tc.model)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveHFSource(%q) expected error, got nil", tc.model)
			}
			continue
		}
		if err != nil {
			t.Fatalf("resolveHFSource(%q) unexpected error: %v", tc.model, err)
		}
		if url != tc.wantURL {
			t.Errorf("url = %q, want %q", url, tc.wantURL)
		}
		if file != tc.wantFile {
			t.Errorf("filename = %q, want %q", file, tc.wantFile)
		}
	}
}

// TestAPIGetStorage verifies disk info is returned for the configured models dir.
func TestAPIGetStorage(t *testing.T) {
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
func TestAPIGetStorageInferred(t *testing.T) {
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
func TestAPIPullModel(t *testing.T) {
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

// TestAPIPullModelGated verifies a 401 from HF propagates as an error response.
func TestAPIPullModelGated(t *testing.T) {
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
}

// TestAPIDeleteModel verifies the model file is deleted from disk.
func TestAPIDeleteModel(t *testing.T) {
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
func TestAPIDeleteModelNotFound(t *testing.T) {
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
