package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// streamTranslator wraps gin.ResponseWriter to intercept a streaming upstream
// response, decode it into IR StreamEvents (parser), and re-encode it into
// the client's protocol (emitter). It also rewrites Content-Type and strips
// Content-Length / Transfer-Encoding so clients see the right media type.
type streamTranslator struct {
	gin.ResponseWriter
	parser          xl.StreamParser
	emitter         xl.StreamEmitter
	clientProto     xl.Protocol
	headerRewritten bool
	parserErr       error
	// fallback buffer for non-2xx responses where we should pass bytes
	// through rather than try to parse them as a stream.
	passThrough bool
}

func newStreamTranslator(w gin.ResponseWriter, upstream, client xl.Protocol) (*streamTranslator, error) {
	sp, err := xl.NewStreamParser(upstream)
	if err != nil {
		return nil, err
	}
	se, err := xl.NewStreamEmitter(client)
	if err != nil {
		return nil, err
	}
	return &streamTranslator{
		ResponseWriter: w,
		parser:         sp,
		emitter:        se,
		clientProto:    client,
	}, nil
}

func (t *streamTranslator) WriteHeader(code int) {
	if t.headerRewritten {
		return
	}
	t.headerRewritten = true
	h := t.ResponseWriter.Header()
	h.Del("Content-Length")
	h.Del("Transfer-Encoding")
	if code >= 200 && code < 300 {
		h.Set("Content-Type", t.emitter.ContentType())
		h.Set("X-Accel-Buffering", "no")
		h.Set("Cache-Control", "no-cache")
	} else {
		// error body: forward verbatim, keep JSON as a safe default.
		t.passThrough = true
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", "application/json")
		}
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *streamTranslator) Write(p []byte) (int, error) {
	if !t.headerRewritten {
		t.WriteHeader(http.StatusOK)
	}
	if t.passThrough {
		return t.ResponseWriter.Write(p)
	}
	if t.parserErr != nil {
		// once we've started emitting a translated stream, upstream parse
		// errors turn into an error event and subsequent writes are dropped.
		return len(p), nil
	}
	events, err := t.parser.Feed(p)
	if err != nil {
		t.parserErr = err
		_ = t.emitter.Emit(t.ResponseWriter, xl.StreamEvent{Type: xl.StreamError, Err: err.Error()})
		t.flush()
		return len(p), nil
	}
	for _, ev := range events {
		if err := t.emitter.Emit(t.ResponseWriter, ev); err != nil {
			return len(p), err
		}
	}
	t.flush()
	return len(p), nil
}

// WriteString satisfies gin.ResponseWriter; delegate to Write.
func (t *streamTranslator) WriteString(s string) (int, error) {
	return t.Write([]byte(s))
}

// Flush is called by ReverseProxy to push frames to the client.
func (t *streamTranslator) Flush() {
	t.flush()
}

func (t *streamTranslator) flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack is forwarded so that things like websocket upgrades (not used here
// but defensive) still work if the underlying writer supports it.
func (t *streamTranslator) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := t.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported")
}

// close drains any buffered state in the parser and emits a terminal frame
// if upstream aborted before sending one.
func (t *streamTranslator) close() {
	if t.passThrough {
		return
	}
	events, err := t.parser.Close()
	if err != nil && t.parserErr == nil {
		_ = t.emitter.Emit(t.ResponseWriter, xl.StreamEvent{Type: xl.StreamError, Err: err.Error()})
	}
	for _, ev := range events {
		_ = t.emitter.Emit(t.ResponseWriter, ev)
	}
	// ensure a terminal stop is always sent so clients don't hang.
	_ = t.emitter.Emit(t.ResponseWriter, xl.StreamEvent{Type: xl.StreamStop, FinishReason: xl.FinishStop})
	t.flush()
}

// compile-time guard: used by translation_proxy at some point.
var _ = bytes.Buffer{}
