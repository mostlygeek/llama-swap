package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type recordingResponseWriter struct {
	header http.Header
	data   strings.Builder
}

func newRecordingResponseWriter() *recordingResponseWriter {
	return &recordingResponseWriter{header: make(http.Header)}
}

func (rw *recordingResponseWriter) Header() http.Header {
	return rw.header
}

func (rw *recordingResponseWriter) Write(p []byte) (int, error) {
	return rw.data.WriteString(string(p))
}

func (rw *recordingResponseWriter) WriteHeader(statusCode int) {}

func (rw *recordingResponseWriter) Flush() {}

func TestCoTSessionAttachesAndStreamsLines(t *testing.T) {
	session := newCOTSession()
	session.appendLine("[llama-swap] Swapping backend process")
	session.appendLine("[llama-swap] Starting inference with: Model")
	session.finish()

	body := io.NopCloser(strings.NewReader("data: {\"content\":\"from-body\"}\n\n"))
	stream := session.attach(body)

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	output := string(data)
	first := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"[llama-swap] Swapping backend process\\n\"}}]}\n\n"
	second := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"[llama-swap] Starting inference with: Model\\n\"}}]}\n\n"
	blank := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"\\n\"}}]}\n\n"

	firstIdx := strings.Index(output, first)
	if firstIdx == -1 {
		t.Fatalf("missing first reasoning chunk in %q", output)
	}
	secondIdx := strings.Index(output, second)
	if secondIdx == -1 {
		t.Fatalf("missing second reasoning chunk in %q", output)
	}
	blankIdx := strings.Index(output, blank)
	if blankIdx == -1 {
		t.Fatalf("missing blank reasoning chunk in %q", output)
	}
	bodyIdx := strings.Index(output, "data: {\"content\":\"from-body\"}\n\n")
	if bodyIdx == -1 {
		t.Fatalf("missing body payload in %q", output)
	}

	if !(firstIdx < secondIdx && secondIdx < blankIdx && blankIdx < bodyIdx) {
		t.Fatalf("unexpected ordering: first=%d second=%d blank=%d body=%d", firstIdx, secondIdx, blankIdx, bodyIdx)
	}
}

func TestCoTSessionStreamsAfterAttach(t *testing.T) {
	session := newCOTSession()
	body := io.NopCloser(strings.NewReader(""))
	stream := session.attach(body)

	session.appendLine("[llama-swap] load_tensors: streaming")
	session.finish()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	output := string(data)
	expected := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"[llama-swap] load_tensors: streaming\\n\"}}]}\n\n"
	if !strings.Contains(output, expected) {
		t.Fatalf("missing streamed line in %q", output)
	}
}

func TestCoTSessionAttachWriterDeliversLinesImmediately(t *testing.T) {
	session := newCOTSession()
	session.appendLine("[llama-swap] Swapping backend process")

	rw := newRecordingResponseWriter()
	if !session.attachWriter(rw) {
		t.Fatalf("attachWriter returned false")
	}

	first := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"[llama-swap] Swapping backend process\\n\"}}]}\n\n"
	if !strings.Contains(rw.data.String(), first) {
		t.Fatalf("expected first line to stream immediately, got %q", rw.data.String())
	}

	session.appendLine("[llama-swap] load_tensors: streaming")
	second := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"[llama-swap] load_tensors: streaming\\n\"}}]}\n\n"
	if !strings.Contains(rw.data.String(), second) {
		t.Fatalf("expected second line to stream immediately, got %q", rw.data.String())
	}

	session.finish()
	blank := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"\\n\"}}]}\n\n"
	if !strings.Contains(rw.data.String(), blank) {
		t.Fatalf("expected blank separator after finish, got %q", rw.data.String())
	}

	// Attaching the body should not replay prior lines.
	body := io.NopCloser(strings.NewReader("data: {\"content\":\"from-body\"}\n\n"))
	stream := session.attach(body)
	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if strings.Contains(string(data), "[llama-swap]") {
		t.Fatalf("body unexpectedly contained swap logs: %q", string(data))
	}
}

func TestCoTSessionInlineSegmentsDoNotAppendNewlines(t *testing.T) {
	session := newCOTSession()
	session.appendLine("[llama-swap] load_tensors: streaming")
	session.appendInline(".")
	session.appendInline(".")
	session.finish()

	body := io.NopCloser(strings.NewReader(""))
	stream := session.attach(body)

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	output := string(data)
	if !strings.Contains(output, "reasoning_content\":\".\"") {
		t.Fatalf("expected dot segments in output: %q", output)
	}
	if strings.Contains(output, "reasoning_content\":\".\\n\"") {
		t.Fatalf("unexpected newline appended to dot segments: %q", output)
	}
}

func TestCoTSessionInsertsNewlineAfterInlineSegments(t *testing.T) {
	session := newCOTSession()
	session.appendLine("[llama-swap] load_tensors: streaming")
	session.appendInline(".")
	session.appendInline(".")
	session.appendLine("[llama-swap] Starting inference with: Model")
	session.finish()

	body := io.NopCloser(strings.NewReader(""))
	stream := session.attach(body)

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	output := string(data)
	newlineChunk := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"\\n\"}}]}\n\n"
	if strings.Count(output, newlineChunk) < 2 {
		t.Fatalf("expected newline separator before and after inference start, got %q", output)
	}

	dotIndex := strings.Index(output, "reasoning_content\":\".\"")
	newlineIndex := strings.Index(output, newlineChunk)
	startIndex := strings.Index(output, "reasoning_content\":\"[llama-swap] Starting inference with: Model\\n\"")
	if !(dotIndex != -1 && newlineIndex != -1 && startIndex != -1) {
		t.Fatalf("missing expected segments in output: %q", output)
	}
	if !(dotIndex < newlineIndex && newlineIndex < startIndex) {
		t.Fatalf("newline separator not emitted between dots and next line: dot=%d newline=%d start=%d", dotIndex, newlineIndex, startIndex)
	}
}
