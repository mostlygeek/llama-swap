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

func TestCapcompat_DetectLlamaServerAudioAndUnknownModality(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props_omni.json"),
	})

	info, err := Detect(context.Background(), up.client(t), "model-a")
	require.NoError(t, err)

	// A modality llama-swap does not know about is ignored rather than
	// passed through, which would fail ModelCapConfig.Validate.
	assert.Equal(t, []string{"text", "image", "audio"}, info.Capabilities.In)
	require.NoError(t, info.Capabilities.Validate())
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
