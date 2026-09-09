//go:build js && wasm

// Tests for the fetch bridge. They run under Node, which supplies the same
// Response, Headers and ReadableStream constructors a browser does, and they
// stand in an in-process HTTP server on a net.Pipe where the Tailcat tunnel
// would be. That covers everything between the transport and the JS Response
// without needing a DERP relay or a peer to connect to.
//
// Run them with `make test-wasm`; a normal `go test ./...` excludes this file.
package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

const testTimeout = 20 * time.Second

// pipeListener hands http.Server the far end of each net.Pipe the client dials.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "server.tailcat:80" }

// serveOverPipe installs h as the node the bridge talks to. It replaces the
// package's connection the same way connect() does, minus Tailcat.
func serveOverPipe(t *testing.T, h http.Handler) {
	t.Helper()
	listener := newPipeListener()
	srv := &http.Server{Handler: h}
	go srv.Serve(listener)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			near, far := net.Pipe()
			select {
			case listener.conns <- far:
				return near, nil
			case <-listener.closed:
				return nil, net.ErrClosed
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}}

	conn.Lock()
	conn.http = client
	conn.Unlock()

	t.Cleanup(func() {
		conn.Lock()
		conn.http = nil
		conn.Unlock()
		listener.Close()
		srv.Close()
	})
}

// await resolves a JS promise. Blocking on the channel parks this goroutine and
// lets the JS event loop run, which is what lets the promise settle at all.
func await(t *testing.T, promise js.Value) (js.Value, error) {
	t.Helper()
	type outcome struct {
		value js.Value
		err   error
	}
	done := make(chan outcome, 1)

	onResolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
		done <- outcome{value: args[0]}
		return nil
	})
	defer onResolve.Release()
	onReject := js.FuncOf(func(_ js.Value, args []js.Value) any {
		message := "rejected"
		if len(args) > 0 && args[0].Truthy() {
			message = args[0].Get("message").String()
		}
		done <- outcome{err: errors.New(message)}
		return nil
	})
	defer onReject.Release()

	promise.Call("then", onResolve, onReject)

	timer := time.NewTimer(testTimeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.value, result.err
	case <-timer.C:
		t.Fatal("timed out waiting for a promise to settle")
		return js.Undefined(), nil
	}
}

func mustAwait(t *testing.T, promise js.Value) js.Value {
	t.Helper()
	value, err := await(t, promise)
	if err != nil {
		t.Fatalf("promise rejected: %v", err)
	}
	return value
}

// wireRequest builds the argument shape transport.ts sends.
func wireRequest(method, url string, headers [][2]string, body []byte, signal js.Value) js.Value {
	pairs := make([]any, 0, len(headers))
	for _, header := range headers {
		pairs = append(pairs, []any{header[0], header[1]})
	}
	request := map[string]any{
		"method":  method,
		"url":     url,
		"headers": pairs,
		"body":    nil,
		"signal":  nil,
	}
	if signal.Truthy() {
		request["signal"] = signal
	}
	value := js.ValueOf(request)
	if body != nil {
		buffer := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(buffer, body)
		value.Set("body", buffer)
	}
	return value
}

func fetchOverBridge(t *testing.T, request js.Value) js.Value {
	t.Helper()
	return mustAwait(t, doFetch(js.Undefined(), []js.Value{request}).(js.Value))
}

// readChunk pulls one chunk from a response body reader, returning "" at EOF.
func readChunk(t *testing.T, reader js.Value) (string, bool) {
	t.Helper()
	result := mustAwait(t, reader.Call("read"))
	if result.Get("done").Bool() {
		return "", true
	}
	value := result.Get("value")
	chunk := make([]byte, value.Get("length").Int())
	js.CopyBytesToGo(chunk, value)
	return string(chunk), false
}

func TestTailcatBridge_FetchReturnsResponse(t *testing.T) {
	serveOverPipe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, `{"ok":true}`)
	}))

	resp := fetchOverBridge(t, wireRequest(http.MethodGet, "http://server.tailcat/v1/models", nil, nil, js.Undefined()))

	if got := resp.Get("status").Int(); got != http.StatusTeapot {
		t.Errorf("status = %d, want %d", got, http.StatusTeapot)
	}
	if got := resp.Get("statusText").String(); got != "I'm a teapot" {
		t.Errorf("statusText = %q, want %q", got, "I'm a teapot")
	}
	if got := resp.Get("ok").Bool(); got {
		t.Error("ok = true for a 418 response")
	}
	if got := resp.Get("headers").Call("get", "content-type").String(); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Get("headers").Call("get", "x-seen-path").String(); got != "/v1/models" {
		t.Errorf("the node saw path %q, want /v1/models", got)
	}

	// json() is what stores/api.ts calls on the model listing.
	body := mustAwait(t, resp.Call("json"))
	if !body.Get("ok").Bool() {
		t.Error("response body did not decode as the JSON the node sent")
	}
}

func TestTailcatBridge_FetchSendsMethodBodyAndHeaders(t *testing.T) {
	type seen struct {
		method      string
		contentType string
		session     string
		body        string
	}
	got := make(chan seen, 1)

	serveOverPipe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- seen{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			session:     r.Header.Get("X-Session-ID"),
			body:        string(body),
		}
		w.WriteHeader(http.StatusOK)
	}))

	headers := [][2]string{
		{"Content-Type", "application/json"},
		{"X-Session-ID", "lspg-test"},
	}
	fetchOverBridge(t, wireRequest(
		http.MethodPost,
		"http://server.tailcat/v1/chat/completions",
		headers,
		[]byte(`{"model":"chat-test"}`),
		js.Undefined(),
	))

	select {
	case request := <-got:
		if request.method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.method)
		}
		if request.contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", request.contentType)
		}
		if request.session != "lspg-test" {
			t.Errorf("X-Session-ID = %q, want lspg-test", request.session)
		}
		if request.body != `{"model":"chat-test"}` {
			t.Errorf("body = %q, want the JSON the caller passed", request.body)
		}
	case <-time.After(testTimeout):
		t.Fatal("the node never received the request")
	}
}

// The Chat tab reads server-sent events off response.body. If the bridge
// buffered the body instead of streaming it, tokens would all arrive at once
// at the end of a generation, so this asserts the first chunk is readable
// before the second one has been written.
func TestTailcatBridge_FetchStreamsBodyIncrementally(t *testing.T) {
	firstRead := make(chan struct{})

	serveOverPipe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()

		select {
		case <-firstRead:
		case <-time.After(testTimeout):
			return
		}
		io.WriteString(w, "data: second\n\n")
		w.(http.Flusher).Flush()
	}))

	resp := fetchOverBridge(t, wireRequest(http.MethodPost, "http://server.tailcat/v1/chat/completions", nil, []byte("{}"), js.Undefined()))
	reader := resp.Get("body").Call("getReader")

	chunk, done := readChunk(t, reader)
	if done || chunk != "data: first\n\n" {
		t.Fatalf("first chunk = %q (done=%v), want the first event", chunk, done)
	}
	// Only now does the handler get to write the second event.
	close(firstRead)

	chunk, done = readChunk(t, reader)
	if done || chunk != "data: second\n\n" {
		t.Fatalf("second chunk = %q (done=%v), want the second event", chunk, done)
	}
}

func TestTailcatBridge_FetchNoContentHasNullBody(t *testing.T) {
	serveOverPipe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	resp := fetchOverBridge(t, wireRequest(http.MethodGet, "http://server.tailcat/health", nil, nil, js.Undefined()))

	if got := resp.Get("status").Int(); got != http.StatusNoContent {
		t.Errorf("status = %d, want 204", got)
	}
	// The Response constructor throws if a 204 is given any body at all, so
	// reaching here at all is most of the assertion.
	if !resp.Get("body").IsNull() {
		t.Error("a 204 response has a body")
	}
}

// Aborting is how the Playground stops a chat mid-generation. The handler here
// keeps streaming until the client goes away, so the test can check both halves
// of that: the page's reader fails, and the node stops being asked to produce
// tokens nobody will read.
func TestTailcatBridge_FetchAbortStopsTheRequest(t *testing.T) {
	released := make(chan struct{})

	serveOverPipe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		deadline := time.After(testTimeout)
		for {
			if _, err := io.WriteString(w, "data: token\n\n"); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-deadline:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))

	controller := js.Global().Get("AbortController").New()
	request := wireRequest(http.MethodPost, "http://server.tailcat/v1/chat/completions", nil, []byte("{}"), controller.Get("signal"))

	resp := fetchOverBridge(t, request)
	reader := resp.Get("body").Call("getReader")
	if chunk, done := readChunk(t, reader); done || chunk == "" {
		t.Fatalf("first chunk = %q (done=%v), want a streamed event", chunk, done)
	}

	controller.Call("abort")

	// Reading on drains what is already buffered and then fails, which is what
	// the Playground's stream loop sees when the user cancels.
	failed := false
	for range 100 {
		if _, err := await(t, reader.Call("read")); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Error("the response stream kept delivering chunks after abort")
	}

	select {
	case <-released:
	case <-time.After(testTimeout):
		t.Fatal("the node kept generating after the client aborted")
	}
}

func TestTailcatBridge_FetchWithoutAConnection(t *testing.T) {
	conn.Lock()
	conn.http = nil
	conn.Unlock()

	request := wireRequest(http.MethodGet, "http://server.tailcat/v1/models", nil, nil, js.Undefined())
	_, err := await(t, doFetch(js.Undefined(), []js.Value{request}).(js.Value))
	if err == nil {
		t.Fatal("fetching with no connection resolved instead of rejecting")
	}
	if want := "not connected"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

func TestTailcatBridge_IdentityIsStableAndRecoverable(t *testing.T) {
	first, ok := identity(js.Undefined(), nil).(map[string]any)
	if !ok {
		t.Fatal("identity did not return an object")
	}
	saved := first["privateKeyJSON"].(string)
	nodeKey := first["nodeKey"].(string)
	if !strings.HasPrefix(nodeKey, "nodekey:") {
		t.Fatalf("nodeKey = %q, want a nodekey: prefix", nodeKey)
	}
	if first["regenerated"].(bool) {
		t.Error("a first-time identity reported itself as regenerated")
	}

	again := identity(js.Undefined(), []js.Value{js.ValueOf(saved)}).(map[string]any)
	if again["nodeKey"].(string) != nodeKey {
		t.Error("reloading the saved key produced a different node key")
	}

	// A key the page cannot read is replaced rather than reported, so the page
	// still loads; the flag is what tells the user their allowlist is stale.
	broken := identity(js.Undefined(), []js.Value{js.ValueOf("{not json")}).(map[string]any)
	if !broken["regenerated"].(bool) {
		t.Error("an unreadable saved key was not reported as regenerated")
	}
	if broken["nodeKey"].(string) == nodeKey {
		t.Error("an unreadable saved key produced the old node key")
	}
}
