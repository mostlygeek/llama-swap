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

func TestExtractModel_GET(t *testing.T) {
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
			got, err := ExtractModel(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got)
			}
		})
	}
}

func TestExtractModel_JSON(t *testing.T) {
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
			got, err := ExtractModel(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got)
			}
		})
	}
}

func TestExtractModel_URLEncodedForm(t *testing.T) {
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
			got, err := ExtractModel(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got)
			}
		})
	}
}

func TestExtractModel_MultipartForm(t *testing.T) {
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
			got, err := ExtractModel(r)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if got != tt.wantModel {
				t.Errorf("want %q got %q", tt.wantModel, got)
			}
		})
	}
}

func TestExtractModel_JSONBodyRestored(t *testing.T) {
	body := `{"model":"llama3","stream":true}`
	r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	if _, err := ExtractModel(r); err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractModel: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("body not restored: want %q got %q", body, string(remaining))
	}
}

func TestExtractModel_MultipartBodyRestored(t *testing.T) {
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

	if _, err := ExtractModel(r); err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractModel: %v", err)
	}
	if !bytes.Equal(remaining, original) {
		t.Errorf("multipart body not restored: want %d bytes got %d bytes", len(original), len(remaining))
	}
}

func TestExtractModel_URLEncodedBodyRestored(t *testing.T) {
	body := "model=whisper-1&extra=value"
	r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if _, err := ExtractModel(r); err != nil {
		t.Fatalf("ExtractModel: %v", err)
	}

	remaining, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading body after ExtractModel: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("url-encoded body not restored: want %q got %q", body, string(remaining))
	}
}

func TestSetModel(t *testing.T) {
	ctx := SetModel(context.Background(), "llama3")
	v, ok := ctx.Value(ModelKey).(string)
	if !ok {
		t.Fatalf("ModelKey not set or wrong type")
	}
	if v != "llama3" {
		t.Errorf("want %q got %q", "llama3", v)
	}
}

func TestSetModel_DoesNotMutateParent(t *testing.T) {
	parent := context.Background()
	_ = SetModel(parent, "llama3")
	if v := parent.Value(ModelKey); v != nil {
		t.Errorf("parent context was mutated: %v", v)
	}
}

func TestGetModel(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		wantStr  string
		wantBool bool
	}{
		{
			name:     "model present",
			ctx:      SetModel(context.Background(), "llama3"),
			wantStr:  "llama3",
			wantBool: true,
		},
		{
			name:     "model absent",
			ctx:      context.Background(),
			wantStr:  "",
			wantBool: false,
		},
		{
			name:     "model is empty string",
			ctx:      SetModel(context.Background(), ""),
			wantStr:  "",
			wantBool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GetModel(tt.ctx)
			if got != tt.wantStr || ok != tt.wantBool {
				t.Errorf("want (%q, %v) got (%q, %v)", tt.wantStr, tt.wantBool, got, ok)
			}
		})
	}
}
