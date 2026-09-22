package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/capcompat"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// llamaServerProps is a /props body shaped like llama-server's, for a model
// with vision and no tool support.
const llamaServerProps = `{
  "default_generation_settings": {"n_ctx": 8192},
  "total_slots": 1,
  "model_path": "/models/vision.gguf",
  "chat_template": "{%- for message in messages %}{{ message.content }}{%- endfor %}",
  "chat_template_caps": {"supports_tools": false, "supports_tool_calls": false},
  "modalities": {"vision": true, "audio": false}
}`

const llamaServerModels = `{
  "object": "list",
  "data": [{"id": "/models/vision.gguf", "object": "model", "owned_by": "llamacpp",
            "meta": {"n_ctx_train": 131072}}]
}`

// newCapUpstream starts a fake llama-server.
func newCapUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(llamaServerModels))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(llamaServerProps))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// capServer builds a test server holding one model.
func capServer(t *testing.T, modelID string, mc config.ModelConfig) *Server {
	t.Helper()
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{modelID: mc}}
	return s
}

// seedCapabilities writes a discovered value into the server's cache as if a
// probe had already run for this model.
func seedCapabilities(t *testing.T, s *Server, modelID string, mc config.ModelConfig, caps config.ModelCapConfig) {
	t.Helper()
	blob, err := json.Marshal(capcompat.Info{
		Upstream:     "llama-server",
		Capabilities: caps,
		DetectedAt:   time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, s.store.Cache().Set(context.Background(), store.CacheEntry{
		Key:  capcompat.LocalKey(modelID, mc),
		Data: blob,
	}))
}

// listModel returns the single record /v1/models produces.
func listModel(t *testing.T, s *Server) modelRecord {
	t.Helper()
	records := listModels(t, s)
	require.Len(t, records, 1)
	return records[0]
}

func listModels(t *testing.T, s *Server) []modelRecord {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []modelRecord `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data
}

func TestAPI_ListModelsFillsCapabilitiesFromCache(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: "http://localhost:9001"}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Out:     []string{"text"},
		Context: 8192,
	})

	rec := listModel(t, s)
	require.NotNil(t, rec.Architecture)
	assert.Equal(t, []any{"text", "image"}, rec.Architecture["input_modalities"])
	assert.Equal(t, "text+image->text", rec.Architecture["modality"])
	assert.Equal(t, true, rec.Capabilities["vision"])
	assert.Equal(t, 8192, rec.ContextLength)
	assert.Equal(t, 8192, rec.ContextWindow)
}

func TestAPI_ListModelsWithoutCacheAdvertisesNothing(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m a.gguf", Proxy: "http://localhost:9001"}
	rec := listModel(t, capServer(t, "m", mc))

	assert.Nil(t, rec.Architecture)
	assert.Nil(t, rec.Capabilities)
	assert.Zero(t, rec.ContextLength)
}

func TestAPI_ListModelsConfigWinsPerField(t *testing.T) {
	// The vllm shape: only context is discoverable, and the user hand-set
	// tools. Both must survive.
	mc := config.ModelConfig{
		Cmd:          "vllm serve a",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{Tools: true},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text"},
		Out:     []string{"text"},
		Context: 131072,
	})

	rec := listModel(t, s)
	assert.Equal(t, true, rec.Capabilities["function_calling"], "configured tools survives")
	assert.Equal(t, []string{"tools", "tool_choice"}, rec.SupportedParameters)
	assert.Equal(t, 131072, rec.ContextLength, "discovered context fills the gap")
}

func TestAPI_ListModelsConfiguredFieldOverridesDiscovered(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:          "llama-server -m a.gguf",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{Context: 999},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Out:     []string{"text"},
		Context: 8192,
	})

	rec := listModel(t, s)
	assert.Equal(t, 999, rec.ContextLength, "configured context wins")
	assert.Equal(t, []any{"text", "image"}, rec.Architecture["input_modalities"], "unset modalities still come from discovery")
}

func TestAPI_ListModelsDisableAutoIgnoresCache(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:          "llama-server -m a.gguf",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{DisableAuto: true},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Context: 8192,
	})

	rec := listModel(t, s)
	assert.Nil(t, rec.Architecture, "disableAuto means the cache is not consulted")
	assert.Zero(t, rec.ContextLength)
}

func TestAPI_ListModelsDisableAutoKeepsConfiguredValues(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:   "llama-server -m a.gguf",
		Proxy: "http://localhost:9001",
		Capabilities: config.ModelCapConfig{
			DisableAuto: true,
			Context:     4096,
		},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{Context: 8192})

	rec := listModel(t, s)
	assert.Equal(t, 4096, rec.ContextLength)
}

func TestAPI_ListModelsStaleCacheAfterCmdChange(t *testing.T) {
	// The key covers cmd, so values discovered from an older command are not
	// served after the command changes.
	old := config.ModelConfig{Cmd: "llama-server -m old.gguf", Proxy: "http://localhost:9001"}
	updated := old
	updated.Cmd = "llama-server -m new.gguf"

	s := capServer(t, "m", updated)
	seedCapabilities(t, s, "m", old, config.ModelCapConfig{Context: 8192})

	rec := listModel(t, s)
	assert.Zero(t, rec.ContextLength, "the entry belongs to the previous command")
}

func TestAPI_ListModelsAliasesShareResolvedCapabilities(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:     "llama-server -m a.gguf",
		Proxy:   "http://localhost:9001",
		Aliases: []string{"m-alias"},
	}
	s := capServer(t, "m", mc)
	s.cfg.IncludeAliasesInList = true
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Out:     []string{"text"},
		Context: 8192,
	})

	records := listModels(t, s)
	require.Len(t, records, 2)
	for _, rec := range records {
		assert.Equal(t, 8192, rec.ContextLength, "%s should advertise the model's context", rec.ID)
		assert.Equal(t, true, rec.Capabilities["vision"], "%s should advertise vision", rec.ID)
	}
}

func TestAPI_RefreshCapabilitiesOnReady(t *testing.T) {
	upstream := newCapUpstream(t)
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: upstream.URL}
	s := capServer(t, "m", mc)

	// The listener lives in New, which the test server does not call, so the
	// event is delivered to the handler directly. That is the same function
	// New subscribes.
	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m",
		OldState:    string(process.StateStarting),
		NewState:    string(process.StateReady),
	})

	requireCached(t, s, "m", mc)

	rec := listModel(t, s)
	assert.Equal(t, 8192, rec.ContextLength)
	assert.Equal(t, []any{"text", "image"}, rec.Architecture["input_modalities"])
	assert.Nil(t, rec.Capabilities["function_calling"], "this template reports no tool support")
}

func TestAPI_RefreshCapabilitiesIgnoresNonReadyStates(t *testing.T) {
	upstream := newCapUpstream(t)
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: upstream.URL}
	s := capServer(t, "m", mc)

	for _, state := range []process.ProcessState{
		process.StateStarting, process.StateStopping, process.StateStopped, process.StateShutdown,
	} {
		s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
			ProcessName: "m",
			NewState:    string(state),
		})
	}

	assert.Zero(t, listModel(t, s).ContextLength, "only a ready process can be probed")
}

func TestAPI_RefreshCapabilitiesSkipsDisableAuto(t *testing.T) {
	upstream := newCapUpstream(t)
	mc := config.ModelConfig{
		Cmd:          "llama-server -m vision.gguf",
		Proxy:        upstream.URL,
		Capabilities: config.ModelCapConfig{DisableAuto: true},
	}
	s := capServer(t, "m", mc)

	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m",
		NewState:    string(process.StateReady),
	})

	// Nothing should ever be written for a model that opted out.
	key := capcompat.LocalKey("m", mc)
	assert.Never(t, func() bool {
		_, found, err := s.store.Cache().Get(context.Background(), key)
		return err == nil && found
	}, 250*time.Millisecond, 25*time.Millisecond)
}

func TestAPI_RefreshCapabilitiesIgnoresUnknownModel(t *testing.T) {
	s := capServer(t, "m", config.ModelConfig{Cmd: "a", Proxy: "http://localhost:9001"})

	// A process event for a model that is not in this config must not panic
	// or probe. Hot reloads make this reachable.
	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "not-in-config",
		NewState:    string(process.StateReady),
	})
}

func TestAPI_RefreshCapabilitiesSurvivesUnreachableUpstream(t *testing.T) {
	// Port 1 is not listening, so every probe attempt is refused. The
	// listing must still answer, just without capabilities.
	mc := config.ModelConfig{Cmd: "a", Proxy: "http://127.0.0.1:1"}
	s := capServer(t, "m", mc)

	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m",
		NewState:    string(process.StateReady),
	})

	assert.Zero(t, listModel(t, s).ContextLength)
}

func TestAPI_ShutdownUnsubscribesCapabilityListener(t *testing.T) {
	// The dispatcher is process-wide, so a Server that has shut down must
	// stop listening or a hot reload leaves two probing at once.
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	calls := make(chan struct{}, 1)
	s.capcompatCancel = event.On(func(e swaputil.ProcessStateChangeEvent) {
		select {
		case calls <- struct{}{}:
		default:
		}
	})

	require.NoError(t, s.Shutdown(time.Second))

	event.Emit(swaputil.ProcessStateChangeEvent{ProcessName: "m", NewState: string(process.StateReady)})
	select {
	case <-calls:
		t.Fatal("listener still subscribed after Shutdown")
	case <-time.After(100 * time.Millisecond):
	}
}

// requireCached waits for the background probe to land in the cache.
func requireCached(t *testing.T, s *Server, modelID string, mc config.ModelConfig) {
	t.Helper()
	key := capcompat.LocalKey(modelID, mc)
	require.Eventually(t, func() bool {
		_, found, err := s.store.Cache().Get(context.Background(), key)
		return err == nil && found
	}, 5*time.Second, 10*time.Millisecond, "probe never reached the cache")
}

// The models dashboard does not read /v1/models. It renders the modelStatus
// payload pushed over /api/events, which is built separately, so it needs its
// own coverage: the two listings must not disagree about a model.

func TestAPI_ModelStatusIncludesDiscoveredCapabilities(t *testing.T) {
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: "http://localhost:9001"}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Out:     []string{"text"},
		Context: 8192,
	})

	status := s.modelStatus()
	require.Len(t, status, 1)
	assert.Equal(t, true, status[0].Capabilities["vision"])
	assert.Equal(t, 8192, status[0].ContextLength)
}

func TestAPI_ModelStatusConfigWinsPerField(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:          "vllm serve a",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{Tools: true},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{Context: 131072})

	status := s.modelStatus()
	require.Len(t, status, 1)
	assert.Equal(t, true, status[0].Capabilities["function_calling"])
	assert.Equal(t, 131072, status[0].ContextLength)
}

func TestAPI_ModelStatusDisableAutoIgnoresCache(t *testing.T) {
	mc := config.ModelConfig{
		Cmd:          "llama-server -m a.gguf",
		Proxy:        "http://localhost:9001",
		Capabilities: config.ModelCapConfig{DisableAuto: true},
	}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{In: []string{"text", "image"}, Context: 8192})

	status := s.modelStatus()
	require.Len(t, status, 1)
	assert.Nil(t, status[0].Capabilities)
	assert.Zero(t, status[0].ContextLength)
}

func TestAPI_ModelStatusAgreesWithListModels(t *testing.T) {
	// The two surfaces are built by different code. Pin them together so a
	// change to one is not silently missed in the other.
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: "http://localhost:9001"}
	s := capServer(t, "m", mc)
	seedCapabilities(t, s, "m", mc, config.ModelCapConfig{
		In:      []string{"text", "image"},
		Out:     []string{"text"},
		Tools:   true,
		Context: 8192,
	})

	rec := listModel(t, s)
	status := s.modelStatus()
	require.Len(t, status, 1)

	assert.Equal(t, rec.Capabilities, status[0].Capabilities)
	assert.Equal(t, rec.ContextLength, status[0].ContextLength)
}

// Discovery finishes after the process reported itself ready, so the model
// list pushed on that state change predates the probe. These cover the event
// that tells the UI to re-read.

func TestAPI_RefreshCapabilitiesAnnouncesNewCapabilities(t *testing.T) {
	upstream := newCapUpstream(t)
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: upstream.URL}
	s := capServer(t, "m", mc)

	changed := make(chan swaputil.ModelCapabilitiesChangedEvent, 1)
	cancel := event.On(func(e swaputil.ModelCapabilitiesChangedEvent) {
		select {
		case changed <- e:
		default:
		}
	})
	defer cancel()

	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m",
		NewState:    string(process.StateReady),
	})

	select {
	case e := <-changed:
		assert.Equal(t, "m", e.ModelID)
	case <-time.After(5 * time.Second):
		t.Fatal("discovery never announced the capabilities it found")
	}

	// The push the UI receives must carry the discovered values.
	status := s.modelStatus()
	require.Len(t, status, 1)
	assert.Equal(t, 8192, status[0].ContextLength)
}

func TestAPI_RefreshCapabilitiesSilentWhenNothingChanged(t *testing.T) {
	upstream := newCapUpstream(t)
	mc := config.ModelConfig{Cmd: "llama-server -m vision.gguf", Proxy: upstream.URL}
	s := capServer(t, "m", mc)

	// First start discovers and announces.
	first := make(chan struct{}, 1)
	cancelFirst := event.On(func(e swaputil.ModelCapabilitiesChangedEvent) {
		select {
		case first <- struct{}{}:
		default:
		}
	})
	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m", NewState: string(process.StateReady),
	})
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("first discovery never announced")
	}
	cancelFirst()

	// Every later start re-probes the same server. Nothing changed, so the
	// UI must not be pushed a fresh model list on every model load.
	again := make(chan struct{}, 1)
	cancelAgain := event.On(func(e swaputil.ModelCapabilitiesChangedEvent) {
		select {
		case again <- struct{}{}:
		default:
		}
	})
	defer cancelAgain()

	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m", NewState: string(process.StateReady),
	})

	select {
	case <-again:
		t.Fatal("a refresh that learned nothing new should stay quiet")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestAPI_RefreshCapabilitiesSilentOnUnsupportedUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"sd","owned_by":"comfyui"}]}`))
	}))
	defer server.Close()

	mc := config.ModelConfig{Cmd: "sd-server", Proxy: server.URL}
	s := capServer(t, "m", mc)

	fired := make(chan struct{}, 1)
	cancel := event.On(func(e swaputil.ModelCapabilitiesChangedEvent) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	defer cancel()

	s.onProcessStateChange(swaputil.ProcessStateChangeEvent{
		ProcessName: "m", NewState: string(process.StateReady),
	})

	select {
	case <-fired:
		t.Fatal("an upstream with nothing to report should not announce a change")
	case <-time.After(500 * time.Millisecond):
	}
}
