package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/internal/store/sqlite"
	"github.com/stretchr/testify/require"
)

// Exercise real subprocesses through server dispatch, including preload, alias
// and profile resolution, explicit unload, TTL, and constructing a new budget.
func TestServer_ResourceProcessLifecycle(t *testing.T) {
	name := fmt.Sprintf("simple-responder_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name = "simple-responder.exe"
	}
	binary, err := filepath.Abs(filepath.Join("..", "..", "build", name))
	require.NoError(t, err)
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("run make simple-responder to test real processes")
	}
	st, err := sqlite.New("")
	require.NoError(t, err)
	defer st.Close()
	input := fmt.Sprintf(`routing:
  router:
    use: resource
    settings:
      resource:
        capacity: 10
        requirements: {a: 4, b: 6, c: 6}
        evict_costs: {a: 10}
hooks:
  on_startup:
    preload: [a]
profiles:
  test:
    pins: {public: c}
models:
  a:
    cmd: '"%s" -port ${PORT} -silent'
    aliases: [alias-a]
  b:
    cmd: '"%s" -port ${PORT} -silent'
  c:
    cmd: '"%s" -port ${PORT} -silent'
    ttl: 1
`, filepath.ToSlash(binary), filepath.ToSlash(binary), filepath.ToSlash(binary))
	cfg, err := config.LoadConfigFromReader(strings.NewReader(input))
	require.NoError(t, err)
	// Use ephemeral ports rather than depending on fixed ports being available.
	for id, model := range cfg.Models {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		require.NoError(t, listener.Close())
		model.Cmd = fmt.Sprintf("%q -port %d -silent", filepath.ToSlash(binary), port)
		model.Proxy = fmt.Sprintf("http://127.0.0.1:%d", port)
		cfg.Models[id] = model
	}
	s, err := New(cfg, logmon.NewGroup(io.Discard, true, true, true), nil, st, BuildInfo{}, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &router.Resource{}, s.local)
	// Shutdown before closing the shared test store, including on failure.
	defer s.Shutdown(3 * time.Second)
	require.Eventually(t, func() bool { return s.local.RunningModels()["a"] == process.StateReady }, 5*time.Second, 10*time.Millisecond)
	request := func(id string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[]}`, id)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	resident := func(ids ...string) {
		t.Helper()
		running := s.local.RunningModels()
		require.Len(t, running, len(ids))
		var total int64
		for _, id := range ids {
			require.Equal(t, process.StateReady, running[id])
			total += cfg.Routing.Router.Settings.Resource.Requirements[id]
		}
		require.LessOrEqual(t, total, cfg.Routing.Router.Settings.Resource.Capacity)
	}
	request("b")
	resident("a", "b")
	before := s.local.RunningStatus()["a"].ReadySince
	request("alias-a")
	resident("a", "b")
	_, err = s.setActiveProfile("test")
	require.NoError(t, err)
	request("public")
	resident("a", "c")
	require.Equal(t, before, s.local.RunningStatus()["a"].ReadySince, "retained process must not restart")
	require.Eventually(t, func() bool { _, ok := s.local.RunningModels()["c"]; return !ok }, 4*time.Second, 20*time.Millisecond)
	request("b")
	resident("a", "b")
	s.local.Unload(time.Second, "b")
	resident("a")
	require.NoError(t, s.Shutdown(3*time.Second))
	require.Empty(t, s.local.RunningModels())
	// Reload uses a fresh server; no stale capacity or resident accounting.
	cfg.Routing.Router.Settings.Resource.Capacity = 6
	cfg.Hooks.OnStartup.Preload = nil
	s, err = New(cfg, logmon.NewGroup(io.Discard, true, true, true), nil, st, BuildInfo{}, nil, nil)
	require.NoError(t, err)
	defer s.Shutdown(3 * time.Second)
	request("a")
	request("b")
	resident("b")
}
