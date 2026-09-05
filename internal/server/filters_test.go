package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
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

	t.Run("set-if-undefined applied when request lacks the key", func(t *testing.T) {
		f := config.Filters{
			SetParams: map[string]any{"max_tokens?": 32768},
		}
		out, err := applyFilters([]byte(`{"model":"m"}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "max_tokens").Int(); got != 32768 {
			t.Errorf("max_tokens = %v, want 32768", got)
		}
	})

	t.Run("request value wins over set-if-undefined", func(t *testing.T) {
		f := config.Filters{
			SetParams: map[string]any{"max_tokens?": 32768},
		}
		out, err := applyFilters([]byte(`{"model":"m","max_tokens":64}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "max_tokens").Int(); got != 64 {
			t.Errorf("max_tokens = %v, want 64", got)
		}
	})

	t.Run("request null, zero and false count as sent", func(t *testing.T) {
		f := config.Filters{
			SetParams: map[string]any{
				"max_tokens?": 32768,
				"stream?":     true,
				"stop?":       "</s>",
			},
		}
		out, err := applyFilters([]byte(`{"model":"m","max_tokens":0,"stream":false,"stop":null}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "max_tokens").Int(); got != 0 {
			t.Errorf("max_tokens = %v, want 0", got)
		}
		if got := gjson.GetBytes(out, "stream").Bool(); got != false {
			t.Errorf("stream = %v, want false", got)
		}
		if got := gjson.GetBytes(out, "stop"); got.Type != gjson.Null {
			t.Errorf("stop = %v, want null", got)
		}
	})

	t.Run("stripped param counts as not sent", func(t *testing.T) {
		f := config.Filters{
			StripParams: "temperature",
			SetParams:   map[string]any{"temperature?": 0.2},
		}
		out, err := applyFilters([]byte(`{"model":"m","temperature":0.7}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "temperature").Float(); got != 0.2 {
			t.Errorf("temperature = %v, want 0.2", got)
		}
	})

	t.Run("set-if-undefined keys are paths, consistent with the write target", func(t *testing.T) {
		f := config.Filters{
			SetParams: map[string]any{"chat_template_kwargs.enable_thinking?": true},
		}

		// Nested value present: the check finds it at the path the write
		// would target, so the configured value yields.
		out, err := applyFilters([]byte(`{"model":"m","chat_template_kwargs":{"enable_thinking":false}}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool(); got != false {
			t.Errorf("enable_thinking = %v, want false (request value kept)", got)
		}

		// Absent: the configured value writes the nested location, exactly
		// where a hard setParams key would write.
		out, err = applyFilters([]byte(`{"model":"m"}`), "m", "", f)
		if err != nil {
			t.Fatalf("applyFilters: %v", err)
		}
		if got := gjson.GetBytes(out, "chat_template_kwargs.enable_thinking").Bool(); got != true {
			t.Errorf("enable_thinking = %v, want true (set-if-undefined applied)", got)
		}
	})

	t.Run("set-if-undefined setParamsByID is a no-op when setParams already set the key", func(t *testing.T) {
		// Filters apply like a pipe: stripParams | setParams | setParamsByID.
		// A "?" key only fills what is undefined at its stage, so a value an
		// earlier stage set counts as defined.
		f := config.Filters{
			SetParams:     map[string]any{"top_p": 0.5},
			SetParamsByID: map[string]map[string]any{"alias": {"top_p?": 0.1}},
		}

		for _, body := range []string{`{"model":"alias"}`, `{"model":"alias","top_p":0.9}`} {
			out, err := applyFilters([]byte(body), "alias", "", f)
			if err != nil {
				t.Fatalf("applyFilters(%s): %v", body, err)
			}
			if got := gjson.GetBytes(out, "top_p").Float(); got != 0.5 {
				t.Errorf("top_p = %v, want 0.5 (setParams value, body %s)", got, body)
			}
		}
	})

	t.Run("pipe order: stripParams then setParams then setParamsByID", func(t *testing.T) {
		// The example from issue #1052: strip removes the client value,
		// setParams forces 1000, and both "?" entries are no-ops because
		// setParams has already set the key.
		f := config.Filters{
			StripParams: "max_tokens",
			SetParams:   map[string]any{"max_tokens": 1000},
			SetParamsByID: map[string]map[string]any{
				"my-model":      {"max_tokens?": 500},
				"my-model:high": {"max_tokens?": 2000},
			},
		}

		for _, requested := range []string{"my-model", "my-model:high"} {
			out, err := applyFilters([]byte(`{"model":"m","max_tokens":999}`), requested, "", f)
			if err != nil {
				t.Fatalf("applyFilters(%s): %v", requested, err)
			}
			if got := gjson.GetBytes(out, "max_tokens").Int(); got != 1000 {
				t.Errorf("max_tokens = %v, want 1000 (requested %s)", got, requested)
			}
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
	var gotContext swaputil.ReqContextData
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(swaputil.MaxMultiPartSize); err != nil {
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
		gotContext, _ = swaputil.ReadContext(r.Context())
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
