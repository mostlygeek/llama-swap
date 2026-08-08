package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/shared"
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

func TestServer_ApplySetParamsByMatch(t *testing.T) {
	// reasoningRules mirrors the documented reasoning-effort use case.
	reasoningRules := []config.MatchRule{
		{
			Key:   "reasoning_effort",
			Match: "none",
			Set:   map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}},
		},
		{
			Key:   "reasoning_effort",
			Match: "high",
			Set: map[string]any{
				"chat_template_kwargs":   map[string]any{"enable_thinking": true},
				"thinking_budget_tokens": 8192,
			},
		},
	}

	t.Run("no rules leaves body untouched", func(t *testing.T) {
		in := []byte(`{"model":"m","reasoning_effort":"high"}`)
		out, err := applySetParamsByMatch(in, config.Filters{})
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if !bytes.Equal(in, out) {
			t.Errorf("body = %s, want unchanged", out)
		}
	})

	t.Run("key absent leaves body untouched", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: reasoningRules}
		out, err := applySetParamsByMatch([]byte(`{"model":"m"}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if gjson.GetBytes(out, "chat_template_kwargs").Exists() {
			t.Errorf("chat_template_kwargs should not be injected, got %s", out)
		}
	})

	t.Run("key present but no rule matches", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: reasoningRules}
		out, err := applySetParamsByMatch([]byte(`{"model":"m","reasoning_effort":"medium"}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if gjson.GetBytes(out, "chat_template_kwargs").Exists() {
			t.Errorf("chat_template_kwargs should not be injected, got %s", out)
		}
	})

	t.Run("match applies every key in set", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: reasoningRules}
		out, err := applySetParamsByMatch([]byte(`{"model":"m","reasoning_effort":"high"}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool(); !got {
			t.Errorf("enable_thinking = %v, want true", got)
		}
		if got := gjson.GetBytes(out, "thinking_budget_tokens").Int(); got != 8192 {
			t.Errorf("thinking_budget_tokens = %d, want 8192", got)
		}
	})

	t.Run("merge preserves client-sent sibling kwargs", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: reasoningRules}
		in := []byte(`{"model":"m","reasoning_effort":"none","chat_template_kwargs":{"custom_flag":true}}`)
		out, err := applySetParamsByMatch(in, f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.custom_flag").Bool(); !got {
			t.Errorf("custom_flag was dropped, body = %s", out)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool(); got {
			t.Errorf("enable_thinking = %v, want false", got)
		}
	})

	t.Run("non-object value is overwritten wholesale", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: reasoningRules}
		in := []byte(`{"model":"m","reasoning_effort":"none","chat_template_kwargs":"bogus"}`)
		out, err := applySetParamsByMatch(in, f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool(); got {
			t.Errorf("enable_thinking = %v, want false", got)
		}
	})

	t.Run("later matching rule wins", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: []config.MatchRule{
			{Key: "effort", Match: "x", Set: map[string]any{"top_p": 0.1}},
			{Key: "effort", Match: "x", Set: map[string]any{"top_p": 0.9}},
		}}
		out, err := applySetParamsByMatch([]byte(`{"effort":"x"}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got)
		}
	})

	t.Run("non-string json values match by string form", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: []config.MatchRule{
			{Key: "depth", Match: "3", Set: map[string]any{"hit_number": true}},
			{Key: "flag", Match: "true", Set: map[string]any{"hit_bool": true}},
		}}
		out, err := applySetParamsByMatch([]byte(`{"depth":3,"flag":true}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if !gjson.GetBytes(out, "hit_number").Bool() {
			t.Errorf("numeric value did not match, body = %s", out)
		}
		if !gjson.GetBytes(out, "hit_bool").Bool() {
			t.Errorf("bool value did not match, body = %s", out)
		}
	})

	t.Run("protected params are ignored", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: []config.MatchRule{
			{Key: "effort", Match: "x", Set: map[string]any{"model": "hijacked", "top_p": 0.3}},
		}}
		out, err := applySetParamsByMatch([]byte(`{"model":"m","effort":"x"}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, "model").String(); got != "m" {
			t.Errorf("model = %q, want m", got)
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.3 {
			t.Errorf("top_p = %v, want 0.3", got)
		}
	})

	t.Run("sub-keys with dots are written literally", func(t *testing.T) {
		f := config.Filters{SetParamsByMatch: []config.MatchRule{
			{Key: "effort", Match: "x", Set: map[string]any{
				"kwargs": map[string]any{"a.b": 1},
			}},
		}}
		out, err := applySetParamsByMatch([]byte(`{"effort":"x","kwargs":{"keep":true}}`), f)
		if err != nil {
			t.Fatalf("applySetParamsByMatch: %v", err)
		}
		if got := gjson.GetBytes(out, `kwargs.a\.b`).Int(); got != 1 {
			t.Errorf("kwargs['a.b'] = %d, want 1, body = %s", got, out)
		}
		if !gjson.GetBytes(out, "kwargs.keep").Bool() {
			t.Errorf("sibling key dropped, body = %s", out)
		}
	})

	t.Run("setParams overrides setParamsByMatch", func(t *testing.T) {
		f := config.Filters{
			SetParamsByMatch: []config.MatchRule{
				{Key: "effort", Match: "x", Set: map[string]any{"top_p": 0.1}},
			},
			SetParams: map[string]any{"top_p": 0.9},
		}
		out, err := applyFilters([]byte(`{"model":"m","effort":"x"}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "top_p").Float(); got != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got)
		}
	})

	t.Run("runs after stripParams", func(t *testing.T) {
		// stripping the matched key means no rule can fire
		f := config.Filters{
			StripParams: "effort",
			SetParamsByMatch: []config.MatchRule{
				{Key: "effort", Match: "x", Set: map[string]any{"top_p": 0.1}},
			},
		}
		out, err := applyFilters([]byte(`{"model":"m","effort":"x"}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if gjson.GetBytes(out, "top_p").Exists() {
			t.Errorf("rule should not have matched after strip, body = %s", out)
		}
	})
}

func TestServer_ResolveFilters_QualifiedPeer(t *testing.T) {
	want := config.Filters{StripParams: "temperature"}
	cfg := config.Config{Peers: config.PeerDictionaryConfig{
		"remote": {
			Models:  []string{"org/model"},
			Filters: want,
		},
	}}

	useModelName, got, ok := resolveFilters(cfg, "remote/org/model")
	if !ok {
		t.Fatal("qualified peer filters were not resolved")
	}
	if useModelName != "" {
		t.Fatalf("useModelName = %q, want empty for peer", useModelName)
	}
	if got.StripParams != want.StripParams {
		t.Fatalf("StripParams = %q, want %q", got.StripParams, want.StripParams)
	}
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

	var gotModel, gotFilename, gotFileBody string
	var gotContext shared.ReqContextData
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(shared.MaxMultiPartSize); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			return
		}
		gotModel = r.MultipartForm.Value["model"][0]
		fileHeader := r.MultipartForm.File["file"][0]
		gotFilename = fileHeader.Filename
		file, err := fileHeader.Open()
		if err != nil {
			t.Errorf("open file: %v", err)
			return
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Errorf("read file: %v", err)
			return
		}
		gotFileBody = string(data)
		gotContext, _ = shared.ReadContext(r.Context())
	})
	CreateFormFilterMiddleware(cfg)(final).ServeHTTP(httptest.NewRecorder(), r)

	if gotModel != "whisper-large-v3" {
		t.Errorf("model rewritten to %q, want whisper-large-v3", gotModel)
	}
	if gotFilename != "a.wav" {
		t.Errorf("filename = %q, want a.wav", gotFilename)
	}
	if gotFileBody != "xx" {
		t.Errorf("file body = %q, want xx", gotFileBody)
	}
	if gotContext.Model != "whisper" || gotContext.ModelID != "whisper" {
		t.Errorf("request context = %+v, want original whisper model", gotContext)
	}
}
