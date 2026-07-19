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
		out, _, err := applyFilters([]byte(`{"model":"alias","temp":1}`), "alias", "real-model", config.Filters{})
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
		out, _, err := applyFilters([]byte(`{"model":"m","temperature":0.7}`), "m", "", f)
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
		out, _, err := applyFilters([]byte(`{"model":"alias"}`), "alias", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.1 {
			t.Errorf("top_p = %v, want 0.1", got)
		}
	})
}

func TestServer_ApplyReasoningFilter(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	intPtr := func(v int) *int { return &v }

	reasoningFilters := config.Filters{
		Reasoning: &config.ReasoningFilter{
			Presets: map[string]config.ReasoningPreset{
				"none":    {EnableThinking: boolPtr(false)},
				"minimal": {EnableThinking: boolPtr(true), BudgetTokens: intPtr(1024)},
				"low":     {EnableThinking: boolPtr(true), BudgetTokens: intPtr(2048)},
				"medium":  {EnableThinking: boolPtr(true), BudgetTokens: intPtr(8192)},
				"high":    {EnableThinking: boolPtr(true), BudgetTokens: intPtr(32768)},
				"max":     {EnableThinking: boolPtr(true)},
			},
		},
	}

	apply := func(t *testing.T, body string, f config.Filters) ([]byte, string) {
		t.Helper()
		out, warning, err := applyFilters([]byte(body), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "model").String(); got != "m" {
			t.Errorf("model = %q, want m (must never change)", got)
		}
		return out, warning
	}

	t.Run("none disables thinking and omits budget", func(t *testing.T) {
		out, _ := apply(t, `{"model":"m","reasoning_effort":"none"}`, reasoningFilters)
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking"); !got.Exists() || got.Bool() {
			t.Errorf("enable_thinking = %v, want false", got)
		}
		if gjson.GetBytes(out, "thinking_budget_tokens").Exists() {
			t.Error("thinking_budget_tokens should be absent")
		}
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			t.Error("reasoning_effort should be removed")
		}
	})

	t.Run("minimal injects configured budget", func(t *testing.T) {
		out, _ := apply(t, `{"model":"m","reasoning_effort":"minimal"}`, reasoningFilters)
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 1024 {
			t.Errorf("thinking_budget_tokens = %d, want 1024", got)
		}
	})

	t.Run("medium enables thinking with 8192 budget", func(t *testing.T) {
		out, _ := apply(t, `{"model":"m","reasoning_effort":"medium"}`, reasoningFilters)
		if !gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool() {
			t.Error("enable_thinking should be true")
		}
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 8192 {
			t.Errorf("thinking_budget_tokens = %d, want 8192", got)
		}
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			t.Error("reasoning_effort should be removed")
		}
	})

	t.Run("max enables thinking and omits budget", func(t *testing.T) {
		out, _ := apply(t, `{"model":"m","reasoning_effort":"max"}`, reasoningFilters)
		if !gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool() {
			t.Error("enable_thinking should be true")
		}
		if gjson.GetBytes(out, "thinking_budget_tokens").Exists() {
			t.Error("thinking_budget_tokens should be absent")
		}
	})

	t.Run("explicit native fields win over preset", func(t *testing.T) {
		body := `{"model":"m","reasoning_effort":"low","chat_template_kwargs":{"enable_thinking":true,"preserve_thinking":true},"thinking_budget_tokens":12345}`
		out, _ := apply(t, body, reasoningFilters)
		if !gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool() {
			t.Error("explicit enable_thinking=true should be preserved")
		}
		if !gjson.GetBytes(out, "chat_template_kwargs.preserve_thinking").Bool() {
			t.Error("preserve_thinking should be preserved")
		}
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 12345 {
			t.Errorf("thinking_budget_tokens = %d, want explicit 12345", got)
		}
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			t.Error("reasoning_effort should be removed")
		}
	})

	t.Run("merge keeps sibling kwargs intact", func(t *testing.T) {
		body := `{"model":"m","reasoning_effort":"none","chat_template_kwargs":{"preserve_thinking":true}}`
		out, _ := apply(t, body, reasoningFilters)
		if !gjson.GetBytes(out, "chat_template_kwargs.preserve_thinking").Bool() {
			t.Error("preserve_thinking should remain intact")
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking"); !got.Exists() || got.Bool() {
			t.Errorf("enable_thinking = %v, want false", got)
		}
	})

	t.Run("unknown string effort is not mutated", func(t *testing.T) {
		body := `{"model":"m","reasoning_effort":"xhigh","temperature":0.5}`
		out, _ := apply(t, body, reasoningFilters)
		// no other filters configured, so the body must be byte-identical
		if string(out) != body {
			t.Errorf("body mutated: %s", out)
		}
	})

	t.Run("non-string effort values are not mutated", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"m","reasoning_effort":123}`,
			`{"model":"m","reasoning_effort":true}`,
			`{"model":"m","reasoning_effort":null}`,
			`{"model":"m","reasoning_effort":{"effort":"low"}}`,
			`{"model":"m","reasoning_effort":["low"]}`,
		} {
			out, _ := apply(t, body, reasoningFilters)
			if string(out) != body {
				t.Errorf("body mutated: %s -> %s", body, out)
			}
		}
	})

	t.Run("no reasoning config causes no mutation", func(t *testing.T) {
		body := `{"model":"m","reasoning_effort":"medium"}`
		out, _ := apply(t, body, config.Filters{})
		if string(out) != body {
			t.Errorf("body mutated: %s", out)
		}
	})

	t.Run("custom inputField is honored and removed", func(t *testing.T) {
		f := reasoningFilters
		f.Reasoning = &config.ReasoningFilter{
			InputField: "effort",
			Presets:    reasoningFilters.Reasoning.Presets,
		}
		out, _ := apply(t, `{"model":"m","effort":"medium"}`, f)
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 8192 {
			t.Errorf("thinking_budget_tokens = %d, want 8192", got)
		}
		if gjson.GetBytes(out, "effort").Exists() {
			t.Error("effort should be removed")
		}
	})

	t.Run("setParams overrides translated fields", func(t *testing.T) {
		f := reasoningFilters
		f.SetParams = map[string]any{"thinking_budget_tokens": 999}
		out, _ := apply(t, `{"model":"m","reasoning_effort":"medium"}`, f)
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 999 {
			t.Errorf("thinking_budget_tokens = %d, want 999 (setParams overwrites)", got)
		}
	})

	t.Run("null chat_template_kwargs is replaced safely", func(t *testing.T) {
		body := `{"model":"m","reasoning_effort":"medium","chat_template_kwargs":null}`
		out, warning := apply(t, body, reasoningFilters)
		if warning != "" {
			t.Errorf("unexpected warning: %q", warning)
		}
		if !gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool() {
			t.Error("enable_thinking should be true")
		}
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			t.Error("reasoning_effort should be removed after successful translation")
		}
	})

	t.Run("non-object chat_template_kwargs aborts translation", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"m","reasoning_effort":"medium","chat_template_kwargs":"invalid"}`,
			`{"model":"m","reasoning_effort":"medium","chat_template_kwargs":[]}`,
		} {
			out, warning := apply(t, body, reasoningFilters)
			if string(out) != body {
				t.Errorf("body mutated: %s -> %s", body, out)
			}
			if !gjson.GetBytes(out, "reasoning_effort").Exists() {
				t.Error("reasoning_effort must be kept when translation is aborted")
			}
			if warning == "" {
				t.Error("expected a warning for non-object chat_template_kwargs")
			}
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
