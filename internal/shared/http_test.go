package shared

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestExtractContext_GET(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantModel string
		wantErr   bool
	}{
		{"model present", "model=llama3", "llama3", false},
		{"model with slashes", "model=author/model-7b", "author/model-7b", false},
		{"model missing", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got, err := extractContext(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got.Model != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got.Model)
			}
		})
	}
}

func TestExtractContext_JSON(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantModel string
		wantErr   bool
	}{
		{"model present", `{"model":"llama3","stream":true}`, "llama3", false},
		{"model with slashes", `{"model":"author/model-7b"}`, "author/model-7b", false},
		{"model empty string", `{"model":""}`, "", false},
		{"model key missing", `{"stream":true}`, "", false},
		{"invalid json", `not-json`, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			got, err := extractContext(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got.Model != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got.Model)
			}
		})
	}
}

func TestExtractContext_URLEncodedForm(t *testing.T) {
	tests := []struct {
		name      string
		formModel string
		wantModel string
		wantErr   bool
	}{
		{"model present", "whisper-1", "whisper-1", false},
		{"model missing", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.formModel != "" {
				form.Set("model", tt.formModel)
			}
			r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			got, err := extractContext(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got.Model != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got.Model)
			}
		})
	}
}

func TestExtractContext_MultipartForm(t *testing.T) {
	tests := []struct {
		name      string
		formModel string
		wantModel string
		wantErr   bool
	}{
		{"model present", "whisper-1", "whisper-1", false},
		{"model missing", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			if tt.formModel != "" {
				fw, _ := mw.CreateFormField("model")
				fw.Write([]byte(tt.formModel))
			}
			mw.Close()

			r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
			r.Header.Set("Content-Type", mw.FormDataContentType())
			got, err := extractContext(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got.Model != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got.Model)
			}
		})
	}
}

func TestExtractContext_JSONBodyRestored(t *testing.T) {
	body := `{"model":"llama3","stream":true}`
	r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	if _, err := extractContext(r); err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractContext: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("body not restored: want %q got %q", body, string(remaining))
	}
}

func TestExtractContext_MultipartBodyRestored(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormField("model")
	fw.Write([]byte("whisper-1"))
	ff, _ := mw.CreateFormFile("file", "audio.wav")
	ff.Write([]byte("fake-audio-bytes"))
	mw.Close()

	original := buf.Bytes()

	r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(original))
	r.Header.Set("Content-Type", mw.FormDataContentType())

	if _, err := extractContext(r); err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractContext: %v", err)
	}
	if !bytes.Equal(remaining, original) {
		t.Errorf("multipart body not restored: want %d bytes got %d bytes", len(original), len(remaining))
	}
}

func TestExtractContext_URLEncodedBodyRestored(t *testing.T) {
	body := "model=whisper-1&extra=value"
	r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, err := extractContext(r); err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractContext: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("url-encoded body not restored: want %q got %q", body, string(remaining))
	}
}

func TestSetContext(t *testing.T) {
	ctx := SetContext(context.Background(), ReqContextData{Model: "llama3", ModelID: "llama3"})
	data, ok := ctx.Value(ReqContextKey).(ReqContextData)
	if !ok {
		t.Fatalf("ContextKey not set or wrong type")
	}
	if data.Model != "llama3" {
		t.Errorf("want %q got %q", "llama3", data.Model)
	}
	if data.ModelID != "llama3" {
		t.Errorf("want %q got %q", "llama3", data.ModelID)
	}
}

func TestSetContext_WithAlias(t *testing.T) {
	ctx := SetContext(context.Background(), ReqContextData{Model: "llama", ModelID: "llama3"})
	data, _ := ctx.Value(ReqContextKey).(ReqContextData)
	if data.Model != "llama" {
		t.Errorf("want requested %q got %q", "llama", data.Model)
	}
	if data.ModelID != "llama3" {
		t.Errorf("want real %q got %q", "llama3", data.ModelID)
	}
}

func TestSetContext_DoesNotMutateParent(t *testing.T) {
	parent := context.Background()
	_ = SetContext(parent, ReqContextData{Model: "llama3", ModelID: "llama3"})
	if v := parent.Value(ReqContextKey); v != nil {
		t.Errorf("parent context was mutated: %v", v)
	}
}

func TestReadContext(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		wantReq  string
		wantReal string
		wantBool bool
	}{
		{
			name:     "model present, same name",
			ctx:      SetContext(context.Background(), ReqContextData{Model: "llama3", ModelID: "llama3"}),
			wantReq:  "llama3",
			wantReal: "llama3",
			wantBool: true,
		},
		{
			name:     "model present, aliased",
			ctx:      SetContext(context.Background(), ReqContextData{Model: "llama", ModelID: "llama3"}),
			wantReq:  "llama",
			wantReal: "llama3",
			wantBool: true,
		},
		{
			name:     "model absent",
			ctx:      context.Background(),
			wantReq:  "",
			wantReal: "",
			wantBool: false,
		},
		{
			name:     "model is empty string",
			ctx:      SetContext(context.Background(), ReqContextData{Model: "", ModelID: ""}),
			wantReq:  "",
			wantReal: "",
			wantBool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotData, ok := ReadContext(tt.ctx)
			if gotData.Model != tt.wantReq || gotData.ModelID != tt.wantReal || ok != tt.wantBool {
				t.Errorf("want (%q, %q, %v) got (%q, %q, %v)", tt.wantReq, tt.wantReal, tt.wantBool, gotData.Model, gotData.ModelID, ok)
			}
		})
	}
}

func TestExtractContext_Streaming_GET(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		wantStreaming bool
	}{
		{"streaming true", "model=llama3&stream=true", true},
		{"streaming false", "model=llama3&stream=false", false},
		{"no stream param", "model=llama3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got, err := extractContext(r)
			if err != nil {
				t.Fatalf("ExtractContext: %v", err)
			}
			if got.Streaming != tt.wantStreaming {
				t.Errorf("Streaming: want %v, got %v", tt.wantStreaming, got.Streaming)
			}
		})
	}
}

func TestExtractContext_Streaming_JSON(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantStreaming bool
	}{
		{"streaming true", `{"model":"llama3","stream":true}`, true},
		{"streaming false", `{"model":"llama3","stream":false}`, false},
		{"no stream param", `{"model":"llama3"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			got, err := extractContext(r)
			if err != nil {
				t.Fatalf("ExtractContext: %v", err)
			}
			if got.Streaming != tt.wantStreaming {
				t.Errorf("Streaming: want %v, got %v", tt.wantStreaming, got.Streaming)
			}
		})
	}
}

func TestExtractContext_Streaming_URLEncodedForm(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("model=whisper-1&stream=true"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got, err := extractContext(r)
	if err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}
	if !got.Streaming {
		t.Error("Streaming should be true")
	}
}

func TestExtractContext_ApiKey(t *testing.T) {
	basicHeader := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	cases := []struct {
		name    string
		method  string
		ct      string
		body    string
		auth    string
		xapi    string
		wantKey string
	}{
		{"GET bearer", http.MethodGet, "", "", "Bearer sk-get", "", "sk-get"},
		{"GET x-api-key", http.MethodGet, "", "", "", "xk-get", "xk-get"},
		{"GET basic", http.MethodGet, "", "", basicHeader("u", "pw-get"), "", "pw-get"},
		{"JSON bearer", http.MethodPost, "application/json", `{"model":"m"}`, "Bearer sk-json", "", "sk-json"},
		{"JSON x-api-key", http.MethodPost, "application/json", `{"model":"m"}`, "", "xk-json", "xk-json"},
		{"form bearer", http.MethodPost, "application/x-www-form-urlencoded", "model=m", "Bearer sk-form", "", "sk-form"},
		{"no key", http.MethodGet, "", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body io.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			}
			r, _ := http.NewRequest(c.method, "/", body)
			if c.ct != "" {
				r.Header.Set("Content-Type", c.ct)
			}
			if c.auth != "" {
				r.Header.Set("Authorization", c.auth)
			}
			if c.xapi != "" {
				r.Header.Set("x-api-key", c.xapi)
			}
			got, err := extractContext(r)
			if err != nil {
				t.Fatalf("extractContext: %v", err)
			}
			if got.ApiKey != c.wantKey {
				t.Errorf("ApiKey = %q, want %q", got.ApiKey, c.wantKey)
			}
		})
	}
}

func TestSetReqData(t *testing.T) {
	ctx := SetContext(context.Background(), ReqContextData{Model: "llama3", ModelID: "llama3", Metadata: make(map[string]string)})

	if err := SetReqData(ctx, "client", "web"); err != nil {
		t.Fatalf("SetReqData: %v", err)
	}
	if err := SetReqData(ctx, "trace", "abc123"); err != nil {
		t.Fatalf("SetReqData: %v", err)
	}

	data, ok := ReadContext(ctx)
	if !ok {
		t.Fatal("context data missing")
	}
	if data.Metadata["client"] != "web" {
		t.Errorf("client = %q, want %q", data.Metadata["client"], "web")
	}
	if data.Metadata["trace"] != "abc123" {
		t.Errorf("trace = %q, want %q", data.Metadata["trace"], "abc123")
	}
}

func TestSetReqData_Errors(t *testing.T) {
	if err := SetReqData(context.Background(), "k", "v"); err == nil {
		t.Error("expected error when no request context data exists")
	}
	ctx := SetContext(context.Background(), ReqContextData{Model: "llama3", ModelID: "llama3"})
	if err := SetReqData(ctx, "k", "v"); err == nil {
		t.Error("expected error when metadata map is missing")
	}
}

func TestServer_ExtractAPIKey(t *testing.T) {
	basicHeader := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	cases := []struct {
		name string
		auth string
		xapi string
		want string
	}{
		{"none", "", "", ""},
		{"bearer", "Bearer tok123", "", "tok123"},
		{"basic", basicHeader("user", "pw-key"), "", "pw-key"},
		{"x-api-key", "", "xkey", "xkey"},
		{"basic beats bearer", basicHeader("u", "bk"), "", "bk"},
		{"bearer beats x-api-key", "Bearer btok", "xkey", "btok"},
		{"malformed basic falls back to x-api-key", "Basic !!!notbase64", "xkey", "xkey"},
		{"lowercase bearer", "bearer tok123", "", "tok123"},
		{"lowercase basic", "basic " + base64.StdEncoding.EncodeToString([]byte("user:pw-key")), "", "pw-key"},
		{"mixed case BEARER", "BEARER tok456", "", "tok456"},
		{"mixed case bAsIc", "bAsIc " + base64.StdEncoding.EncodeToString([]byte("u:bk")), "", "bk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.auth != "" {
				r.Header.Set("Authorization", c.auth)
			}
			if c.xapi != "" {
				r.Header.Set("x-api-key", c.xapi)
			}
			if got := ExtractAPIKey(r); got != c.want {
				t.Errorf("extractAPIKey() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFetchContext_UpstreamPath(t *testing.T) {
	cfg := config.Config{
		Models: map[string]config.ModelConfig{
			"m1":           {},
			"author/model": {},
			"real":         {Aliases: []string{"nick"}},
		},
	}

	cases := []struct {
		name        string
		path        string
		wantModel   string
		wantModelID string
		wantErr     bool
	}{
		{"known model", "/upstream/m1/v1/chat/completions", "m1", "m1", false},
		{"model with slash", "/upstream/author/model/v1/chat", "author/model", "author/model", false},
		{"unknown model", "/upstream/nope/v1/chat/completions", "", "", true},
		{"bare model path", "/upstream/m1/", "m1", "m1", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(`{}`))
			data, err := FetchContext(r, cfg)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got err=%v", c.wantErr, err)
			}
			if c.wantErr {
				return
			}
			if data.Model != c.wantModel {
				t.Errorf("model = %q, want %q", data.Model, c.wantModel)
			}
			if data.ModelID != c.wantModelID {
				t.Errorf("modelID = %q, want %q", data.ModelID, c.wantModelID)
			}
			if data.Metadata == nil {
				t.Error("metadata map not initialized")
			}
		})
	}
}

func TestFetchContext_UpstreamPath_DoesNotReadBody(t *testing.T) {
	cfg := config.Config{Models: map[string]config.ModelConfig{"m1": {}}}
	body := `{"model":"should-not-matter"}`
	r := httptest.NewRequest(http.MethodPost, "/upstream/m1/v1/chat/completions", strings.NewReader(body))

	_, err := FetchContext(r, cfg)
	if err != nil {
		t.Fatalf("FetchContext: %v", err)
	}

	// The body should be untouched so the upstream handler can still read it.
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != body {
		t.Errorf("body was consumed: %q", string(got))
	}
}
