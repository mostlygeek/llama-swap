package ollama

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestWriter returns an httptest recorder both as the http.ResponseWriter the
// stream writer wraps and for inspecting what was written. The recorder
// implements http.Flusher, so the writer's flush path behaves normally.
func newTestWriter() (http.ResponseWriter, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return rec, rec
}

// writeSSE feeds the given SSE body through the stream writer in one shot,
// emulating the reverse proxy's buffered Write behavior.
func writeSSE(t *testing.T, w *StreamWriter, contentType string, body string) {
	t.Helper()
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func readNDJSONLines(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var lines []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("invalid NDJSON line: %v\n%s", err, line)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestStreamWriter_ChatBasicStream(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewChatStreamWriter(gw, "qwen3:8b")

	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{"content":" there"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	writeSSE(t, w, "text/event-stream", sse)

	if got := rec.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type: got %q, want application/x-ndjson", got)
	}

	lines := readNDJSONLines(t, rec.Body.Bytes())
	if len(lines) != 3 {
		t.Fatalf("expected 3 NDJSON lines (2 deltas + final), got %d:\n%s", len(lines), rec.Body.String())
	}

	// Delta lines
	for i, want := range []string{"Hi", " there"} {
		if lines[i]["done"] != false {
			t.Errorf("line %d: done should be false", i)
		}
		msg := lines[i]["message"].(map[string]any)
		if msg["content"] != want {
			t.Errorf("line %d content: got %v, want %q", i, msg["content"], want)
		}
	}

	// Final line
	final := lines[2]
	if final["done"] != true {
		t.Errorf("final: done should be true, got %v", final["done"])
	}
	if final["done_reason"] != "stop" {
		t.Errorf("final done_reason: got %v", final["done_reason"])
	}
	if final["prompt_eval_count"] != float64(5) {
		t.Errorf("final prompt_eval_count: got %v", final["prompt_eval_count"])
	}
	if final["eval_count"] != float64(2) {
		t.Errorf("final eval_count: got %v", final["eval_count"])
	}
	if td, ok := final["total_duration"].(float64); !ok || td <= 0 {
		t.Errorf("final total_duration should be positive: %v", final["total_duration"])
	}
}

func TestStreamWriter_GenerateMode(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewGenerateStreamWriter(gw, "qwen3:8b")

	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"42"},"finish_reason":null}]}`,
		``,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	writeSSE(t, w, "text/event-stream", sse)

	lines := readNDJSONLines(t, rec.Body.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (1 delta + final), got %d:\n%s", len(lines), rec.Body.String())
	}
	if lines[0]["response"] != "42" {
		t.Errorf("delta response: got %v", lines[0]["response"])
	}
	if _, ok := lines[0]["message"]; ok {
		t.Errorf("generate mode should not emit 'message' field")
	}
	if lines[1]["done"] != true {
		t.Errorf("final done: %v", lines[1]["done"])
	}
}

func TestStreamWriter_ChunkedWrites(t *testing.T) {
	// SSE arriving across multiple Write calls, including a split mid-frame.
	gw, rec := newTestWriter()
	w := NewChatStreamWriter(gw, "m")

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	chunks := []string{
		`data: {"choices":[{"delta":{"con`,
		`tent":"hello"},"finish_reason":null}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}
	for _, c := range chunks {
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	lines := readNDJSONLines(t, rec.Body.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), rec.Body.String())
	}
	msg := lines[0]["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content reassembled across chunks: got %v", msg["content"])
	}
}

func TestStreamWriter_PassThroughOnError(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewChatStreamWriter(gw, "m")

	// Upstream returns 500 with JSON error body — must pass through unchanged.
	errBody := `{"error":"upstream exploded"}`
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(errBody)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := rec.Code; got != http.StatusInternalServerError {
		t.Errorf("status: got %d", got)
	}
	if !strings.Contains(rec.Body.String(), "upstream exploded") {
		t.Errorf("pass-through body lost: %s", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type should not be rewritten on non-SSE: got %q", got)
	}
}

func TestStreamWriter_FinalizeWithoutDONE(t *testing.T) {
	// Some clients may close the upstream stream without a final [DONE].
	// Finalize() should still emit a terminating done:true frame.
	gw, rec := newTestWriter()
	w := NewChatStreamWriter(gw, "m")

	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"abc"},"finish_reason":null}]}`,
		``,
		``,
	}, "\n")
	writeSSE(t, w, "text/event-stream", sse)

	w.Finalize()

	lines := readNDJSONLines(t, rec.Body.Bytes())
	if len(lines) != 2 {
		t.Fatalf("expected delta + final, got %d:\n%s", len(lines), rec.Body.String())
	}
	if lines[1]["done"] != true {
		t.Errorf("Finalize should emit done:true frame")
	}
}

func TestStreamWriter_FinalizeIsIdempotent(t *testing.T) {
	gw, rec := newTestWriter()
	w := NewChatStreamWriter(gw, "m")
	sse := `data: {"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	writeSSE(t, w, "text/event-stream", sse)
	before := rec.Body.Len()
	w.Finalize()
	w.Finalize()
	if rec.Body.Len() != before {
		t.Errorf("Finalize wrote extra bytes after [DONE]")
	}
}
