package apiconv

import (
	"bytes"
	"net/http"
	"strconv"
)

// BufferingWriter wraps an http.ResponseWriter and captures the upstream
// response in memory instead of forwarding it, so a non-streaming reply can be
// read in full, translated, and re-emitted in the client's format. It mirrors
// the proven writer used by the ollama compatibility layer
// (internal/ollama/buffer.go); kept here so apiconv carries no dependency on
// that package.
//
// The wrapper preserves the underlying writer's Header() map and only
// intercepts WriteHeader/Write. Call CommitTranslated or CommitPassThrough
// exactly once after the upstream handler returns.
type BufferingWriter struct {
	http.ResponseWriter
	body           bytes.Buffer
	capturedStatus int
	headerCaptured bool
	committed      bool
}

// NewBufferingWriter wraps w. The wrapper is single-use.
func NewBufferingWriter(w http.ResponseWriter) *BufferingWriter {
	return &BufferingWriter{ResponseWriter: w}
}

func (b *BufferingWriter) WriteHeader(code int) {
	if b.committed {
		b.ResponseWriter.WriteHeader(code)
		return
	}
	b.capturedStatus = code
	b.headerCaptured = true
}

func (b *BufferingWriter) Write(p []byte) (int, error) {
	if b.committed {
		return b.ResponseWriter.Write(p)
	}
	return b.body.Write(p)
}

func (b *BufferingWriter) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}

// Flush is a no-op until committed: the buffered response is held until
// CommitTranslated/CommitPassThrough. Once committed it forwards to the
// underlying writer when it supports http.Flusher.
func (b *BufferingWriter) Flush() {
	if b.committed {
		if f, ok := b.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// CapturedBody returns the buffered upstream response body.
func (b *BufferingWriter) CapturedBody() []byte { return b.body.Bytes() }

// CapturedStatus returns the upstream status code (200 if WriteHeader was never
// called explicitly, mirroring net/http semantics).
func (b *BufferingWriter) CapturedStatus() int {
	if !b.headerCaptured {
		return http.StatusOK
	}
	return b.capturedStatus
}

// CommitTranslated commits the response with a translated body and content type.
// The captured upstream body is discarded.
func (b *BufferingWriter) CommitTranslated(body []byte, contentType string, statusCode int) {
	h := b.ResponseWriter.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Del("Content-Encoding") // we send uncompressed
	b.committed = true
	b.ResponseWriter.WriteHeader(statusCode)
	_, _ = b.ResponseWriter.Write(body)
}

// CommitPassThrough flushes the captured response unchanged. Use this when the
// upstream returned an error and we don't want to translate.
func (b *BufferingWriter) CommitPassThrough() {
	code := b.CapturedStatus()
	body := b.body.Bytes()
	h := b.ResponseWriter.Header()
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Del("Content-Encoding")
	b.committed = true
	b.ResponseWriter.WriteHeader(code)
	_, _ = b.ResponseWriter.Write(body)
}
