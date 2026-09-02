package ollama

import (
	"bytes"
	"net/http"
	"strconv"
)

// BufferingWriter wraps an http.ResponseWriter and captures the upstream
// response in memory instead of forwarding it. The non-streaming Ollama
// handlers use this to read the entire OpenAI reply, translate it, and emit
// an Ollama-shaped response on the way back to the client.
//
// The wrapper preserves the underlying writer's Header() map (so headers set
// by intermediate code like the metrics monitor remain in place) and only
// intercepts WriteHeader/Write. Call CommitTranslated to send a new body, or
// CommitPassThrough to forward the captured body unchanged (e.g. on error).
type BufferingWriter struct {
	http.ResponseWriter
	body           bytes.Buffer
	capturedStatus int
	headerCaptured bool
	committed      bool
}

// NewBufferingWriter wraps w. The wrapper is single-use: call Commit* exactly
// once after the upstream handler returns.
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

// Flush is a no-op until committed; once committed it forwards to the
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

// CapturedStatus returns the upstream status code (200 if WriteHeader was
// never called explicitly, mirroring net/http semantics).
func (b *BufferingWriter) CapturedStatus() int {
	if !b.headerCaptured {
		return http.StatusOK
	}
	return b.capturedStatus
}

// CapturedContentType returns the Content-Type the upstream produced (taken
// from the underlying header map, where intermediate writers and the reverse
// proxy set it).
func (b *BufferingWriter) CapturedContentType() string {
	return b.ResponseWriter.Header().Get("Content-Type")
}

// CommitTranslated commits the response with a translated body and the given
// content type. The captured upstream body is discarded.
func (b *BufferingWriter) CommitTranslated(body []byte, contentType string, statusCode int) {
	h := b.ResponseWriter.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Del("Content-Encoding") // we send uncompressed
	b.committed = true
	b.ResponseWriter.WriteHeader(statusCode)
	_, _ = b.ResponseWriter.Write(body)
}

// CommitPassThrough flushes the captured response unchanged. Use this when
// the upstream returned an error and we don't want to translate.
func (b *BufferingWriter) CommitPassThrough() {
	code := b.CapturedStatus()
	body := b.body.Bytes()
	h := b.ResponseWriter.Header()
	// Set Content-Length so the client doesn't see chunked encoding.
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Del("Content-Encoding")
	b.committed = true
	b.ResponseWriter.WriteHeader(code)
	_, _ = b.ResponseWriter.Write(body)
}
