package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/tidwall/gjson"
)

func TestServer_ApplyFilters(t *testing.T) {
	t.Run("useModelName rewrite", func(t *testing.T) {
		out, err := applyFilters([]byte(`{"model":"alias","temp":1}`), "alias", "real-model", config.Filters{})
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "model").String(); got != "real-model" {
			t.Errorf("model = %q, want real-model", got)
		}
	})

	t.Run("strip and set params", func(t *testing.T) {
		f := config.Filters{
			StripParams: "temperature",
			SetParams:   map[string]any{"top_p": 0.9},
		}
		out, err := applyFilters([]byte(`{"model":"m","temperature":0.7}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if gjson.GetBytes(out, "temperature").Exists() {
			t.Error("temperature should be stripped")
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got)
		}
	})

	t.Run("setParamsByID overrides setParams", func(t *testing.T) {
		f := config.Filters{
			SetParams:     map[string]any{"top_p": 0.5},
			SetParamsByID: map[string]map[string]any{"alias": {"top_p": 0.1}},
		}
		out, err := applyFilters([]byte(`{"model":"alias"}`), "alias", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.1 {
			t.Errorf("top_p = %v, want 0.1", got)
		}
	})
}

func TestServer_RewriteMultipartModel(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("model", "old-name")
	mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	fw.Write([]byte("RIFFdata"))
	mw.Close()

	r := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}

	body, contentType, err := rewriteMultipartModel(r.MultipartForm, "new-name")
	if err != nil {
		t.Fatalf("rewriteMultipartModel: %v", err)
	}

	parsed, err := multipart.NewReader(bytes.NewReader(body), boundaryOf(t, contentType)).ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got := parsed.Value["model"][0]; got != "new-name" {
		t.Errorf("model = %q, want new-name", got)
	}
	if got := parsed.Value["language"][0]; got != "en" {
		t.Errorf("language = %q, want en", got)
	}
	fh := parsed.File["file"][0]
	f, _ := fh.Open()
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "RIFFdata" {
		t.Errorf("file data = %q, want RIFFdata", data)
	}
}

func boundaryOf(t *testing.T, contentType string) string {
	t.Helper()
	_, params, ok := strings.Cut(contentType, "boundary=")
	if !ok {
		t.Fatalf("no boundary in %q", contentType)
	}
	return params
}

func TestServer_FormFilterMiddleware(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{
		"whisper": {UseModelName: "whisper-large-v3"},
	}}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("model", "whisper")
	fw, _ := mw.CreateFormFile("file", "a.wav")
	fw.Write([]byte("xx"))
	mw.Close()

	r := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	var gotModel string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(32 << 20)
		gotModel = r.MultipartForm.Value["model"][0]
	})
	CreateFormFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

	if gotModel != "whisper-large-v3" {
		t.Errorf("model rewritten to %q, want whisper-large-v3", gotModel)
	}
}

func TestServer_ResolveFilters_PeerAlias(t *testing.T) {
	cfg := config.Config{
		Peers: config.PeerDictionaryConfig{
			"modelbest": config.PeerConfig{
				Models: []string{"MiniCPM-V-4.6-Thinking"},
				Aliases: map[string]string{
					"MiniCPM":      "MiniCPM-V-4.6-Thinking",
					"qwen3.5-lite": "MiniCPM-V-4.6-Thinking",
				},
				Filters: config.Filters{StripParams: "temperature"},
			},
		},
	}

	t.Run("peer alias returns upstream name as useModelName", func(t *testing.T) {
		useModelName, filters, ok := resolveFilters(cfg, "MiniCPM")
		if !ok {
			t.Fatal("expected ok=true for peer alias")
		}
		if useModelName != "MiniCPM-V-4.6-Thinking" {
			t.Errorf("useModelName = %q, want MiniCPM-V-4.6-Thinking", useModelName)
		}
		if filters.StripParams != "temperature" {
			t.Errorf("filters.StripParams = %q, want temperature", filters.StripParams)
		}
	})

	t.Run("peer model without alias returns empty useModelName", func(t *testing.T) {
		useModelName, _, ok := resolveFilters(cfg, "MiniCPM-V-4.6-Thinking")
		if !ok {
			t.Fatal("expected ok=true for peer model")
		}
		if useModelName != "" {
			t.Errorf("useModelName = %q, want empty", useModelName)
		}
	})

	t.Run("unknown model returns ok=false", func(t *testing.T) {
		_, _, ok := resolveFilters(cfg, "unknown")
		if ok {
			t.Error("expected ok=false for unknown model")
		}
	})

	t.Run("applyFilters rewrites model name for peer alias", func(t *testing.T) {
		out, err := applyFilters([]byte(`{"model":"MiniCPM","prompt":"hi"}`), "MiniCPM", "MiniCPM-V-4.6-Thinking", config.Filters{})
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "model").String(); got != "MiniCPM-V-4.6-Thinking" {
			t.Errorf("model = %q, want MiniCPM-V-4.6-Thinking", got)
		}
	})
}

func TestServer_QueryFilterMiddleware(t *testing.T) {
	t.Run("rewrites GET ?model alias to upstream name", func(t *testing.T) {
		cfg := config.Config{
			Peers: config.PeerDictionaryConfig{
				"modelbest": config.PeerConfig{
					Models:  []string{"MiniCPM-V-4.6-Thinking"},
					Aliases: map[string]string{"friendly-alias": "MiniCPM-V-4.6-Thinking"},
				},
			},
		}

		r := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=friendly-alias", nil)
		var gotModel string
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotModel = r.URL.Query().Get("model")
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if gotModel != "MiniCPM-V-4.6-Thinking" {
			t.Errorf("model query param = %q, want MiniCPM-V-4.6-Thinking", gotModel)
		}
	})

	t.Run("leaves non-aliased GET model untouched", func(t *testing.T) {
		cfg := config.Config{
			Peers: config.PeerDictionaryConfig{
				"modelbest": config.PeerConfig{Models: []string{"real-model"}},
			},
		}

		r := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=real-model", nil)
		var gotModel string
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotModel = r.URL.Query().Get("model")
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if gotModel != "real-model" {
			t.Errorf("model query param = %q, want real-model (unchanged)", gotModel)
		}
	})

	t.Run("skips POST requests", func(t *testing.T) {
		cfg := config.Config{
			Peers: config.PeerDictionaryConfig{
				"modelbest": config.PeerConfig{
					Models:  []string{"real-model"},
					Aliases: map[string]string{"alias": "real-model"},
				},
			},
		}

		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?model=alias", nil)
		var gotModel string
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotModel = r.URL.Query().Get("model")
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if gotModel != "alias" {
			t.Errorf("POST model query param = %q, want alias (must not rewrite POST)", gotModel)
		}
	})

	t.Run("skips GET without model query param", func(t *testing.T) {
		cfg := config.Config{
			Peers: config.PeerDictionaryConfig{
				"modelbest": config.PeerConfig{
					Models:  []string{"real-model"},
					Aliases: map[string]string{"alias": "real-model"},
				},
			},
		}

		r := httptest.NewRequest(http.MethodGet, "/v1/audio/voices", nil)
		called := false
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if !called {
			t.Error("expected next handler to be called even without model param")
		}
	})

	t.Run("preserves other query parameters", func(t *testing.T) {
		cfg := config.Config{
			Peers: config.PeerDictionaryConfig{
				"modelbest": config.PeerConfig{
					Models:  []string{"real-model"},
					Aliases: map[string]string{"alias": "real-model"},
				},
			},
		}

		r := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=alias&stream=true&other=hi", nil)
		var gotModel, gotStream, gotOther string
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			gotModel = q.Get("model")
			gotStream = q.Get("stream")
			gotOther = q.Get("other")
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if gotModel != "real-model" {
			t.Errorf("model = %q, want real-model", gotModel)
		}
		if gotStream != "true" {
			t.Errorf("stream = %q, want true", gotStream)
		}
		if gotOther != "hi" {
			t.Errorf("other = %q, want hi", gotOther)
		}
	})

	t.Run("handles local model useModelName", func(t *testing.T) {
		cfg := config.Config{Models: map[string]config.ModelConfig{
			"local": {UseModelName: "real-local"},
		}}

		r := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?model=local", nil)
		var gotModel string
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotModel = r.URL.Query().Get("model")
		})
		CreateQueryFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

		if gotModel != "real-local" {
			t.Errorf("model query param = %q, want real-local", gotModel)
		}
	})
}
