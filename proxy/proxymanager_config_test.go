package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

func TestPatchCommandFlags(t *testing.T) {
	got, err := patchCommandFlags(
		"llama-server --port ${PORT} --model /models/a.gguf --ctx-size 8192 --n-gpu-layers=35",
		map[string]string{"--ctx-size": "32768", "--n-gpu-layers": "99", "--threads": "8"},
	)
	if err != nil {
		t.Fatalf("patchCommandFlags: %v", err)
	}
	for _, want := range []string{"--ctx-size 32768", "--n-gpu-layers=99", "--threads 8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched cmd %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "8192") || strings.Contains(got, "35") {
		t.Fatalf("patched cmd kept old flag values: %q", got)
	}
}

func TestProxyManager_ApiConfigPatchModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "llama-swap.yaml")
	if err := os.WriteFile(configPath, []byte(`
modelsDir: /models
models:
  coder:
    cmd: llama-server --port ${PORT} --model /models/coder.gguf --ctx-size 8192 --n-gpu-layers 35
    name: Old name
    aliases:
      - old-alias
    ttl: 300
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pm := &ProxyManager{
		configFile: configPath,
		config: config.Config{Models: map[string]config.ModelConfig{
			"coder": {Cmd: "llama-server --port ${PORT} --model /models/coder.gguf --ctx-size 8192 --n-gpu-layers 35"},
		}},
		ginEngine: gin.New(),
	}
	reloaded := make(chan struct{}, 1)
	pm.reloadFn = func() {
		reloaded <- struct{}{}
	}
	addApiHandlers(pm)

	body, _ := json.Marshal(map[string]any{
		"ctx_size":     32768,
		"n_gpu_layers": 99,
		"ttl":          -1,
		"name":         "Coder",
		"flags": map[string]any{
			"threads": 8,
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/config/models/coder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	pm.ginEngine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-reloaded:
	case <-time.After(time.Second):
		t.Fatal("reloadFn was not called")
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	for _, want := range []string{
		"--ctx-size 32768",
		"--n-gpu-layers 99",
		"--threads 8",
		"ttl: -1",
		"name: Coder",
		"modelsDir: /models",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}
