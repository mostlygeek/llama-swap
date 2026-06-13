package router

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

func TestLoadingWriter_SSEHeadersAndInitialMessage(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)

	if ct := lw.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: want text/event-stream, got %q", ct)
	}
	if cc := lw.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control: want no-cache, got %q", cc)
	}
	if conn := lw.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection: want keep-alive, got %q", conn)
	}

	body := w.Body.String()
	if !strings.HasPrefix(body, "data: ") {
		t.Errorf("expected SSE data: prefix, got: %s", body)
	}

	content := extractStreamedContent(body)
	if !strings.Contains(content, "━━━━━\n") {
		t.Errorf("missing separator in streamed content: %q", content)
	}
	if !strings.Contains(content, "llama-swap loading model: test-model\n") {
		t.Errorf("missing initial message in streamed content: %q", content)
	}
}

func TestLoadingWriter_WriteHeaderOnce(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.WriteHeader(http.StatusCreated)

	if w.Code != http.StatusOK {
		t.Errorf("first WriteHeader: want %d, got %d", http.StatusOK, w.Code)
	}
}

func TestLoadingWriter_WritePassthrough(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.Write([]byte("hello"))
	lw.Flush()

	body := w.Body.String()
	if !strings.Contains(body, "hello") {
		t.Errorf("Write passthrough failed, body: %s", body)
	}
}

func TestLoadingWriter_StartStopsOnCancel(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.tickDuration = 10 * time.Millisecond
	lw.loopStarted = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())

	go lw.start(ctx)
	<-lw.loopStarted
	cancel()

	if !lw.waitForCompletion(time.Second) {
		t.Fatal("waitForCompletion timed out")
	}

	body := w.Body.String()
	if !strings.Contains(body, "Done!") {
		t.Errorf("expected Done! message, body: %s", body)
	}
}

func TestLoadingWriter_StartShowsSetUpdate(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.tickDuration = 10 * time.Millisecond
	lw.charPerSecond = 0
	lw.loopStarted = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	go lw.start(ctx)
	<-lw.loopStarted

	lw.setUpdate("custom status message")
	time.Sleep(50 * time.Millisecond)
	cancel()

	if !lw.waitForCompletion(time.Second) {
		t.Fatal("waitForCompletion timed out")
	}

	body := w.Body.String()
	content := extractStreamedContent(body)
	if !strings.Contains(content, "custom status message") {
		t.Errorf("expected setUpdate message in output, got: %q", content)
	}
}

func TestLoadingWriter_SendDataFormat(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.sendData("hello world")

	body := w.Body.String()
	if !strings.Contains(body, `"reasoning_content":"hello world"`) {
		t.Errorf("expected reasoning_content in SSE data, body: %s", body)
	}
	if !strings.HasPrefix(body, "data: ") {
		t.Errorf("expected data: prefix, got: %s", body)
	}
}

func TestLoadingWriter_SendLine(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.charPerSecond = 0

	// Capture only the content from this sendLine call
	before := w.Body.Len()
	lw.sendLine("line content")
	after := w.Body.Len()
	chunkBody := w.Body.String()[before:after]

	content := extractStreamedContent(chunkBody)
	if content != "line content\n" {
		t.Errorf("expected complete streamed line, got: %q", content)
	}
}

func TestLoadingWriter_FlushesPeriodicallyDuringStatusUpdates(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	lw.tickDuration = 10 * time.Millisecond
	lw.charPerSecond = 0
	lw.loopStarted = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		lw.start(ctx)
		close(done)
	}()

	<-lw.loopStarted
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	lines := countSSEMessages(body)
	if lines < 2 {
		t.Errorf("expected multiple SSE messages from periodic updates, got %d", lines)
	}
}

func TestLoadingWriter_ReqStored(t *testing.T) {
	logger := logmon.NewWriter(io.Discard)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	lw := newLoadingWriter(logger, "test-model", w, req)
	if lw.req != req {
		t.Fatal("req not stored")
	}
}

func TestIsLoadingPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/v1/chat/completions", true},
		{"/v1/chat/completions/extra", true},
		{"/v1/completions", false},
		{"/v1/embeddings", false},
		{"/health", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isLoadingPath(tt.path); got != tt.want {
				t.Errorf("isLoadingPath(%q) = %v, want %v", tt.path, got, tt.want)
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
			got, err := ExtractContext(r)
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
			got, err := ExtractContext(r)
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
	got, err := ExtractContext(r)
	if err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}
	if !got.Streaming {
		t.Error("Streaming should be true")
	}
}

func countSSEMessages(s string) int {
	scanner := bufio.NewScanner(strings.NewReader(s))
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			count++
		}
	}
	return count
}

func extractStreamedContent(body string) string {
	var result strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonData := strings.TrimPrefix(line, "data: ")
		var msg struct {
			Choices []struct {
				Delta struct {
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(jsonData), &msg); err != nil {
			continue
		}
		if len(msg.Choices) > 0 {
			result.WriteString(msg.Choices[0].Delta.ReasoningContent)
		}
	}
	return result.String()
}
