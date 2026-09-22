package capcompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// The fixtures under testdata/ are built from the documented response shapes:
// llama.cpp's tools/server/README.md for /props and /v1/models, its
// common/jinja/caps.h for the chat_template_caps key names, and vLLM's
// OpenAI-compatible model listing. They are not captures from a live server.
// Before relying on the tools mapping in production, re-capture props.json and
// props_vision.json from real builds and confirm supports_tools actually
// differs between a tool-capable model and a plain completion model; several
// of those flags are initialised to true in llama.cpp's C++ struct.
//
// The exceptions are real captures. props_capture.json and
// v1_models_capture.json come from a running llama-server, and confirm the
// modality key set (vision, video, audio) and that the loaded context in
// /props differs from meta.n_ctx in the listing. The listing capture was
// truncated in transit inside its meta block; the fields after n_params were
// dropped rather than invented, which the prober does not read.
//
// The halogen fixtures are the exception: v1_models.json and health.json are
// verbatim captures from a running halogen-flash-server. The two other
// halogen files are that same capture with vision turned off, and with the
// supported list and tool-call block removed, to cover builds configured
// differently. Their derivation is noted where they are used.

// fixture reads a testdata file.
func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	require.NoError(t, err)
	return data
}

// upstream is a fake inference server. routes maps a path to a fixture body;
// any path not in the map answers 404.
type upstream struct {
	server *httptest.Server
	hits   map[string]int
}

func newUpstream(t *testing.T, routes map[string][]byte) *upstream {
	t.Helper()
	u := &upstream{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		u.hits[r.URL.Path]++
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	u.server = httptest.NewServer(mux)
	t.Cleanup(u.server.Close)
	return u
}

func (u *upstream) client(t *testing.T) *Client {
	t.Helper()
	base, err := url.Parse(u.server.URL)
	require.NoError(t, err)
	return NewClient(base, config.TimeoutsConfig{})
}

func TestCapcompat_DetectLlamaServer(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "model-a")
	require.NoError(t, err)

	assert.Equal(t, "llama-server", info.Upstream)
	assert.Equal(t, []string{"text"}, info.Capabilities.In)
	assert.Equal(t, []string{"text"}, info.Capabilities.Out)
	assert.True(t, info.Capabilities.Tools)
	assert.Equal(t, 32768, info.Capabilities.Context, "uses the loaded n_ctx, not n_ctx_train")
	assert.False(t, info.Capabilities.Reranker, "reranker is not discoverable")
	assert.False(t, info.DetectedAt.IsZero())
}

func TestCapcompat_DetectLlamaServerVision(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props_vision.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "model-a")
	require.NoError(t, err)

	assert.Equal(t, []string{"text", "image"}, info.Capabilities.In)
	assert.Equal(t, []string{"text"}, info.Capabilities.Out)
	assert.False(t, info.Capabilities.Tools, "template reports no tool support")
	assert.Equal(t, 8192, info.Capabilities.Context)
}

func TestCapcompat_DetectLlamaServerIgnoresUnmappedModalities(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props_omni.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "model-a")
	require.NoError(t, err)

	// This fixture reports the three real modality keys plus a made up one.
	// All three map; the unrecognised key must be dropped rather than passed
	// through, where it would fail ModelCapConfig.Validate.
	assert.Equal(t, []string{"text", "image", "audio", "video"}, info.Capabilities.In)
	require.NoError(t, info.Capabilities.Validate())
}

func TestCapcompat_DetectLlamaServerCapture(t *testing.T) {
	// Verbatim from a running llama-server. A text-only build reports all
	// three modality keys as false, which is what confirmed their names.
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models_capture.json"),
		"/props":     fixture(t, "llama-server", "props_capture.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "gemma")
	require.NoError(t, err)

	assert.Equal(t, "llama-server", info.Upstream)
	assert.Equal(t, []string{"text"}, info.Capabilities.In)

	// The capture is the reason /props wins over the listing: the server can
	// actually serve 220160 tokens, while the listing's meta.n_ctx and
	// n_ctx_train both say 262144. Reading the listing would advertise a
	// window the server refuses to fill.
	assert.Equal(t, 220160, info.Capabilities.Context)
}

func TestCapcompat_DetectLlamaServerIgnoresListingCapabilities(t *testing.T) {
	// The listing carries an Ollama-style models[] block whose capabilities
	// array says "multimodal" for this model, while /props reports every
	// modality false. /props describes what this server will accept, so it
	// wins and nothing reads that array.
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models_capture.json"),
		"/props":     fixture(t, "llama-server", "props_capture.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "gemma")
	require.NoError(t, err)
	assert.Equal(t, []string{"text"}, info.Capabilities.In,
		"a multimodal model served without a projector accepts text only")
}

func TestCapcompat_DetectLlamaServerAudioAndVideo(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props_omni.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "model-a")
	require.NoError(t, err)

	// Order is fixed so the cached blob is byte-stable across probes.
	assert.Equal(t, []string{"text", "image", "audio", "video"}, info.Capabilities.In)
}

func TestCapcompat_DetectLlamaServerNoTemplateCaps(t *testing.T) {
	t.Run("template mentions tools", func(t *testing.T) {
		up := newUpstream(t, map[string][]byte{
			"/v1/models": fixture(t, "llama-server", "v1_models.json"),
			"/props":     fixture(t, "llama-server", "props_no_template_caps.json"),
		})

		info, err := Detect(context.Background(), up.client(t), "model-a")
		require.NoError(t, err)
		assert.True(t, info.Capabilities.Tools)
		assert.Equal(t, 16384, info.Capabilities.Context)
	})

	t.Run("template does not mention tools", func(t *testing.T) {
		up := newUpstream(t, map[string][]byte{
			"/v1/models": fixture(t, "llama-server", "v1_models.json"),
			"/props":     fixture(t, "llama-server", "props_no_template_caps_no_tools.json"),
		})

		info, err := Detect(context.Background(), up.client(t), "model-a")
		require.NoError(t, err)
		assert.False(t, info.Capabilities.Tools)
		assert.Equal(t, 2048, info.Capabilities.Context)
	})
}

func TestCapcompat_DetectVLLM(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "vllm", "v1_models.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "meta-llama/Llama-3.1-8B-Instruct")
	require.NoError(t, err)

	assert.Equal(t, "vllm", info.Upstream)
	assert.Equal(t, 131072, info.Capabilities.Context)
	assert.Equal(t, []string{"text"}, info.Capabilities.In)
	assert.Equal(t, []string{"text"}, info.Capabilities.Out)
	assert.False(t, info.Capabilities.Tools, "vllm exposes nothing about tool support")
	assert.Equal(t, 0, up.hits["/props"], "vllm has no /props to read")
}

func TestCapcompat_VLLMSkipsLoRAAdapters(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "vllm", "v1_models_lora.json"),
	})

	t.Run("unknown name falls back to the base model", func(t *testing.T) {
		info, err := Detect(context.Background(), up.client(t), "a-name-vllm-never-heard-of")
		require.NoError(t, err)
		assert.Equal(t, 131072, info.Capabilities.Context,
			"the adapter is listed first but its max_model_len is not the base model's")
	})

	t.Run("exact adapter name is honoured", func(t *testing.T) {
		info, err := Detect(context.Background(), up.client(t), "sql-lora")
		require.NoError(t, err)
		assert.Equal(t, 4096, info.Capabilities.Context)
	})
}

func TestCapcompat_DetectHalogen(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "halogen", "v1_models.json"),
		"/health":    fixture(t, "halogen", "health.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "halogen-qwen3.8-flash-next")
	require.NoError(t, err)

	assert.Equal(t, "halogen", info.Upstream)
	assert.Equal(t, []string{"text", "image"}, info.Capabilities.In, "this build has a vision tower")
	assert.Equal(t, []string{"text"}, info.Capabilities.Out)
	assert.True(t, info.Capabilities.Tools)
	assert.Equal(t, 262144, info.Capabilities.Context)
	assert.False(t, info.Capabilities.Reranker, "not reported anywhere")
	require.NoError(t, info.Capabilities.Validate())
}

func TestCapcompat_DetectHalogenVisionDisabled(t *testing.T) {
	// The real capture with the vision tower turned off. Images are off
	// unless the server is started with one, so this is the default build.
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "halogen", "v1_models.json"),
		"/health":    fixture(t, "halogen", "health_no_vision.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "halogen-qwen3.8-flash-next")
	require.NoError(t, err)

	assert.Equal(t, []string{"text"}, info.Capabilities.In)
	assert.True(t, info.Capabilities.Tools)
	assert.Equal(t, 262144, info.Capabilities.Context)
}

func TestCapcompat_DetectHalogenWithoutToolSignals(t *testing.T) {
	// The real capture with the supported list and the tool-call block
	// removed, standing in for a build that publishes neither.
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "halogen", "v1_models.json"),
		"/health":    fixture(t, "halogen", "health_no_tools.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "halogen-qwen3.8-flash-next")
	require.NoError(t, err)

	assert.False(t, info.Capabilities.Tools, "nothing in /health claims tool support")
	assert.Equal(t, 262144, info.Capabilities.Context)
}

func TestCapcompat_DetectHalogenFallsBackToListingContext(t *testing.T) {
	// A build whose /health omits context still has three names for it in
	// the model listing.
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "halogen", "v1_models.json"),
		"/health":    []byte(`{"status":"ok","vision":{"enabled":false},"supported":["tools"]}`),
	})

	info, err := Detect(context.Background(), up.client(t), "halogen-qwen3.8-flash-next")
	require.NoError(t, err)
	assert.Equal(t, 262144, info.Capabilities.Context)
}

func TestCapcompat_DetectHalogenHealthUnreachable(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "halogen", "v1_models.json"),
	})

	_, err := Detect(context.Background(), up.client(t), "halogen-qwen3.8-flash-next")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "halogen")
}

func TestCapcompat_ModelEntryContextTokens(t *testing.T) {
	// Servers spell the context length differently, and n_ctx_train is the
	// training limit rather than the loaded window, so it is never used.
	tests := []struct {
		name  string
		entry ModelEntry
		want  int
	}{
		{"max_model_len wins", ModelEntry{MaxModelLen: 1, ContextLength: 2}, 1},
		{"context_length next", ModelEntry{ContextLength: 2}, 2},
		{"meta n_ctx last", func() ModelEntry {
			var e ModelEntry
			e.Meta.NCtx = 3
			return e
		}(), 3},
		{"n_ctx_train is never used", func() ModelEntry {
			var e ModelEntry
			e.Meta.NCtxTrain = 999
			return e
		}(), 0},
		{"nothing reported", ModelEntry{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.entry.ContextTokens())
		})
	}
}

func TestCapcompat_DetectUnsupportedUpstream(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "unsupported_v1_models.json"),
	})

	_, err := Detect(context.Background(), up.client(t), "model-a")
	require.ErrorIs(t, err, ErrUnsupportedUpstream)
	assert.Contains(t, err.Error(), "some-other-server")
}

func TestCapcompat_DetectEmptyListing(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": []byte(`{"object":"list","data":[]}`),
	})

	_, err := Detect(context.Background(), up.client(t), "model-a")
	require.ErrorIs(t, err, ErrUnsupportedUpstream)
	assert.Contains(t, err.Error(), "unknown")
}

func TestCapcompat_DetectModelListUnreachable(t *testing.T) {
	up := newUpstream(t, map[string][]byte{})

	_, err := Detect(context.Background(), up.client(t), "model-a")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnsupportedUpstream,
		"a server that will not answer is a different problem from one that is unsupported")
	assert.Contains(t, err.Error(), "404")
}

func TestCapcompat_DetectPropsUnreachable(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
	})

	_, err := Detect(context.Background(), up.client(t), "model-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llama-server")
}

func TestCapcompat_ClientJoinsBasePathPrefix(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write(fixture(t, "llama-server", "v1_models.json"))
	}))
	defer server.Close()

	base, err := url.Parse(server.URL + "/upstream/prefix")
	require.NoError(t, err)

	var models ModelsResponse
	require.NoError(t, NewClient(base, config.TimeoutsConfig{}).GetJSON(context.Background(), "/v1/models", &models))
	assert.Equal(t, "/upstream/prefix/v1/models", gotPath)
}

func TestCapcompat_ClientSendsHeaders(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	c := NewClient(base, config.TimeoutsConfig{})
	c.SetHeader("Authorization", "Bearer secret")

	var models ModelsResponse
	require.NoError(t, c.GetJSON(context.Background(), "/v1/models", &models))
	assert.Equal(t, "Bearer secret", gotAuth)
}

func TestCapcompat_ModelsResponseFind(t *testing.T) {
	models := ModelsResponse{Data: []ModelEntry{
		{ID: "adapter", Parent: "base", MaxModelLen: 1},
		{ID: "base", MaxModelLen: 2},
	}}

	tests := []struct {
		name   string
		search string
		wantID string
	}{
		{"exact match wins", "adapter", "adapter"},
		{"exact base match", "base", "base"},
		{"unknown name skips adapters", "unknown", "base"},
		{"empty name skips adapters", "", "base"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, found := models.Find(tt.search)
			require.True(t, found)
			assert.Equal(t, tt.wantID, entry.ID)
		})
	}

	t.Run("no entries", func(t *testing.T) {
		_, found := ModelsResponse{}.Find("anything")
		assert.False(t, found)
	})

	t.Run("only adapters falls back to the first entry", func(t *testing.T) {
		only := ModelsResponse{Data: []ModelEntry{{ID: "a", Parent: "gone"}}}
		entry, found := only.Find("unknown")
		require.True(t, found)
		assert.Equal(t, "a", entry.ID)
	})
}
