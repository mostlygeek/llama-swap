package router

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
		{"model missing", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got, err := ExtractContext(r)
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
		{"model empty string", `{"model":""}`, "", true},
		{"model key missing", `{"stream":true}`, "", true},
		{"invalid json", `not-json`, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			got, err := ExtractContext(r)
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
		{"model missing", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.formModel != "" {
				form.Set("model", tt.formModel)
			}
			r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			got, err := ExtractContext(r)
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
		{"model missing", "", "", true},
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
			got, err := ExtractContext(r)
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

	if _, err := ExtractContext(r); err != nil {
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

	if _, err := ExtractContext(r); err != nil {
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

	if _, err := ExtractContext(r); err != nil {
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
	data, ok := ctx.Value(ContextKey).(ReqContextData)
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
	data, _ := ctx.Value(ContextKey).(ReqContextData)
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
	if v := parent.Value(ContextKey); v != nil {
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
