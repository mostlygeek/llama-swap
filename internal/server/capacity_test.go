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

// TestServer_CapacityProcessLifecycle exercises real subprocesses through server
// dispatch, including preload, model resolution, unload, TTL, and a fresh budget.
func TestServer_CapacityProcessLifecycle(t *testing.T) {
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
    use: capacity
    settings:
      capacity:
        pools: {ram: 20}
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
    capacities: {ram: 10}
    evict_cost: 100
  b:
    capacities: {ram: 5}
    evict_cost: 1
    cmd: '"%s" -port ${PORT} -silent'
  c:
    capacities: {ram: 5}
    evict_cost: 1
    cmd: '"%s" -port ${PORT} -silent'
  d:
    capacities: {ram: 10}
    evict_cost: 1
    cmd: '"%s" -port ${PORT} -silent'
    ttl: 1
`, filepath.ToSlash(binary), filepath.ToSlash(binary), filepath.ToSlash(binary), filepath.ToSlash(binary))
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
	s, err := New(cfg, logmon.NewGroup(io.Discard, true, true, true), nil, st, BuildInfo{}, nil, nil, nil)
	require.NoError(t, err)
	require.IsType(t, &router.Capacity{}, s.local)
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
			total += cfg.Models[id].Capacities["ram"]
		}
		require.LessOrEqual(t, total, cfg.Routing.Router.Settings.Capacity.Pools["ram"])
	}
	request("b")
	resident("a", "b")
	before := s.local.RunningStatus()["a"].ReadySince
	request("alias-a")
	resident("a", "b")
	_, err = s.setActiveProfile("test")
	require.NoError(t, err)
	request("public")
	resident("a", "b", "c") // Exact fit; nothing is evicted.
	request("d")
	resident("a", "d") // Evict B+C (cost 2), retaining A (cost 100).
	require.Equal(t, before, s.local.RunningStatus()["a"].ReadySince, "retained process must not restart")
	require.Eventually(t, func() bool { _, ok := s.local.RunningModels()["d"]; return !ok }, 4*time.Second, 20*time.Millisecond)
	request("b")
	resident("a", "b")
	s.local.Unload(time.Second, "b")
	resident("a")
	require.NoError(t, s.Shutdown(3*time.Second))
	require.Empty(t, s.local.RunningModels())
	// Reload uses a fresh server; no stale capacity or resident accounting.
	cfg.Routing.Router.Settings.Capacity.Pools["ram"] = 10
	cfg.Hooks.OnStartup.Preload = nil
	s, err = New(cfg, logmon.NewGroup(io.Discard, true, true, true), nil, st, BuildInfo{}, nil, nil, nil)
	require.NoError(t, err)
	defer s.Shutdown(3 * time.Second)
	request("a")
	request("b")
	resident("b")
}
