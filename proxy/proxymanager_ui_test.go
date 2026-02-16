package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
)

func TestProxyManager_UIRouteDeepLinkServesSPA(t *testing.T) {
	reactFS, err := GetReactFS()
	if err != nil {
		t.Skip("ui assets not built; skipping UI integration test")
	}
	indexFile, err := reactFS.Open("index.html")
	if err != nil {
		t.Skip("ui assets not built; skipping UI integration test")
	}
	indexFile.Close()

	proxy := New(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})
	defer proxy.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/ui/models", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, strings.ToLower(w.Body.String()), "<!doctype html>")
	assert.Contains(t, w.Body.String(), "<title>llama-swap</title>")
}

func TestProxyManager_UIRouteMissingAssetReturnsNotFound(t *testing.T) {
	proxy := New(config.Config{
		HealthCheckTimeout: 15,
		Models: map[string]config.ModelConfig{
			"model1": getTestSimpleResponderConfig("model1"),
		},
		LogLevel: "error",
	})
	defer proxy.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/ui/assets/does-not-exist.js", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
