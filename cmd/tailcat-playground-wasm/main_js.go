//go:build js && wasm

// The tailcat-playground-wasm module is the browser half of the standalone
// Tailcat Playground page. It carries a Tailcat client into the browser and
// exposes it as an HTTP transport, so the page can talk to a llama-swap node
// that is only reachable over Tailcat.
//
// The browser cannot do this itself: Tailcat is WireGuard relayed over DERP,
// not something fetch() speaks. All the browser sees is WebSocket traffic to
// DERP relays, which tailscale.com's derphttp package sets up automatically
// under GOOS=js. The HTTP request and response happen in here, over the
// tunnel.
//
// It sets globalThis.llamaSwapTailcat with four functions:
//
//	identity(privateKeyJSON: string|null) -> {nodeKey, privateKeyJSON, regenerated}
//	connect({token, privateKeyJSON, derpMapURL, verbose}) -> Promise<{nodeKey}>
//	fetch({method, url, headers, body, signal}) -> Promise<Response>
//	disconnect() -> undefined
//
// fetch resolves to a real JS Response backed by a ReadableStream, so the
// page's existing API modules work through it unchanged: .json(), .blob(),
// .text() and .body.getReader() for SSE all behave as they would against a
// same-origin server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	tailcatlib "github.com/tailscale/tailcat"
	"tailscale.com/types/logger"
)

const (
	// httpPort is Tailcat's virtual TCP port for llama-swap's HTTP server. It
	// mirrors internal/tailcat.HTTPPort, which cannot be imported here: that
	// package is the server-side adapter and pulls in listener plumbing this
	// build has no use for.
	httpPort = 80

	// handshakeTimeout bounds the meow/meowed handshake, which needs both
	// sides' DERP connections up before it can succeed.
	handshakeTimeout = 60 * time.Second

	// probeTimeout bounds the one unauthenticated GET /health that proves the
	// tunnel carries HTTP, not just packets.
	probeTimeout = 20 * time.Second

	// maxIdleConnsPerHost is generous because the Playground's Load Test tab
	// fans out hard over this single tunnel.
	maxIdleConnsPerHost = 32

	readBufferSize = 64 << 10
)

// conn is the one live connection the page has. Connecting again replaces it.
var conn struct {
	sync.Mutex
	client *tailcatlib.Client
	http   *http.Client
}

func main() {
	js.Global().Set("llamaSwapTailcat", map[string]any{
		"identity":   js.FuncOf(identity),
		"connect":    js.FuncOf(connect),
		"fetch":      js.FuncOf(doFetch),
		"disconnect": js.FuncOf(disconnect),
	})
	if f := js.Global().Get("onLlamaSwapTailcatReady"); f.Type() == js.TypeFunction {
		f.Invoke()
	}
	select {}
}

// identity resolves the browser's client key, minting one when the page has
// none saved. The returned nodeKey is the canonical "nodekey:..." form that a
// server's tailcat.allow list wants.
//
// It cannot fail: a saved key that no longer parses is replaced rather than
// reported, because the alternative is a page that will not load until the
// user clears their browser storage by hand. regenerated says whether that
// happened, so the page can mention that the allowlist needs updating.
func identity(this js.Value, args []js.Value) any {
	var stored string
	if len(args) > 0 && args[0].Type() == js.TypeString {
		stored = args[0].String()
	}
	pk, err := parsePrivateKey(stored)
	regenerated := false
	if err != nil || pk.Private.IsZero() {
		pk = tailcatlib.NewPrivateKey()
		regenerated = stored != ""
	}
	encoded, err := json.Marshal(pk)
	if err != nil {
		// Marshalling a key we just built cannot realistically fail; report
		// rather than hand back a half-filled object.
		return map[string]any{"nodeKey": "", "privateKeyJSON": "", "regenerated": false}
	}
	return map[string]any{
		"nodeKey":        pk.Private.Public().String(),
		"privateKeyJSON": string(encoded),
		"regenerated":    regenerated,
	}
}

// connect brings up a Tailcat client for token and, once the tunnel carries
// HTTP, installs it as the transport every later fetch call uses.
func connect(this js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeObject {
		return rejectedPromise(errors.New("connect requires an options object"))
	}
	opts := args[0]
	token := strings.TrimSpace(optString(opts, "token"))
	keyJSON := optString(opts, "privateKeyJSON")
	derpMapURL := strings.TrimSpace(optString(opts, "derpMapURL"))
	logf := optLogf(opts)

	return makePromise(func() (any, error) {
		if token == "" {
			return nil, errors.New("a Tailcat connection token is required")
		}
		if _, err := tailcatlib.ParseAddr(tailcatlib.Addr(token)); err != nil {
			return nil, fmt.Errorf("that is not a valid Tailcat connection token: %w", err)
		}
		pk, err := parsePrivateKey(keyJSON)
		if err != nil {
			return nil, fmt.Errorf("reading the saved client key: %w", err)
		}

		closeConn()
		cl := &tailcatlib.Client{
			Server:     tailcatlib.Addr(token),
			Key:        pk.Private,
			Logf:       logf,
			DERPMapURL: derpMapURL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
		defer cancel()
		if err := pingUntil(ctx, cl); err != nil {
			cl.Close()
			return nil, fmt.Errorf("could not reach the node over Tailcat. Check the token, "+
				"and that the node's tailcat.allow list includes this browser's node key: %w", err)
		}

		hc := &http.Client{Transport: &http.Transport{
			// Explicitly nil so a proxy in the environment cannot intercept
			// tunnelled requests, matching internal/router/peer.go.
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return cl.DialTCPPort(ctx, httpPort)
			},
			ForceAttemptHTTP2:   false,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
		}}

		if err := probeHealth(hc); err != nil {
			cl.Close()
			return nil, err
		}

		conn.Lock()
		conn.client, conn.http = cl, hc
		conn.Unlock()
		return map[string]any{"nodeKey": pk.Private.Public().String()}, nil
	})
}

// probeHealth confirms the node is serving HTTP over the tunnel. GET /health
// is deliberate: it is the one route llama-swap serves without an API key, so
// a failure here means the tunnel or the server, never the credentials. An
// API key problem surfaces later, on the page's own /v1/models request, where
// it can be reported as such.
func probeHealth(hc *http.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://server.tailcat/health", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("the Tailcat tunnel came up but the node did not answer HTTP: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the node answered GET /health with %s; it may not be a llama-swap server", resp.Status)
	}
	return nil
}

// disconnect tears down the current connection. The page calls it when the
// user goes back to the server list.
func disconnect(this js.Value, args []js.Value) any {
	closeConn()
	return js.Undefined()
}

func closeConn() {
	conn.Lock()
	cl, hc := conn.client, conn.http
	conn.client, conn.http = nil, nil
	conn.Unlock()
	if hc != nil {
		hc.CloseIdleConnections()
	}
	if cl != nil {
		cl.Close()
	}
}

// doFetch performs one HTTP request over the tunnel and resolves to a JS
// Response. The argument mirrors what the page's transport.ts sends:
//
//	{method, url, headers: [[name, value], ...], body: Uint8Array|null, signal}
func doFetch(this js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeObject {
		return rejectedPromise(errors.New("fetch requires a request object"))
	}
	req := args[0]
	method := optString(req, "method")
	if method == "" {
		method = http.MethodGet
	}
	url := optString(req, "url")
	signal := req.Get("signal")

	header := http.Header{}
	if pairs := req.Get("headers"); pairs.Type() == js.TypeObject {
		for i := range pairs.Length() {
			pair := pairs.Index(i)
			header.Add(pair.Index(0).String(), pair.Index(1).String())
		}
	}

	var body []byte
	if b := req.Get("body"); b.Type() == js.TypeObject {
		body = make([]byte, b.Get("length").Int())
		js.CopyBytesToGo(body, b)
	}

	return makePromise(func() (any, error) {
		conn.Lock()
		hc := conn.http
		conn.Unlock()
		if hc == nil {
			return nil, errors.New("not connected to a Tailcat node")
		}
		if url == "" {
			return nil, errors.New("fetch requires a url")
		}

		ctx, cancel := context.WithCancel(context.Background())
		releaseAbort := watchAbort(cancel, signal)
		fail := func(err error) (any, error) {
			releaseAbort()
			cancel()
			return nil, err
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		hreq, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return fail(err)
		}
		hreq.Header = header
		if body != nil {
			hreq.ContentLength = int64(len(body))
		}

		resp, err := hc.Do(hreq)
		if err != nil {
			return fail(err)
		}
		return makeResponse(resp, cancel, releaseAbort), nil
	})
}

// watchAbort cancels ctx when the request's AbortSignal fires, which is how
// the Playground stops a streaming chat mid-flight. The returned function
// detaches the listener; it must be called once the response body is done or
// every aborted-or-not request leaks a js.Func.
func watchAbort(cancel context.CancelFunc, signal js.Value) func() {
	if signal.Type() != js.TypeObject {
		return func() {}
	}
	if signal.Get("aborted").Truthy() {
		cancel()
		return func() {}
	}
	var (
		cb   js.Func
		once sync.Once
	)
	cb = js.FuncOf(func(this js.Value, args []js.Value) any {
		cancel()
		return nil
	})
	signal.Call("addEventListener", "abort", cb, map[string]any{"once": true})
	return func() {
		once.Do(func() {
			signal.Call("removeEventListener", "abort", cb)
			cb.Release()
		})
	}
}

func makeResponse(resp *http.Response, cancel context.CancelFunc, releaseAbort func()) js.Value {
	headers := js.Global().Get("Headers").New()
	for name, values := range resp.Header {
		for _, value := range values {
			headers.Call("append", name, value)
		}
	}
	init := map[string]any{
		"status":     resp.StatusCode,
		"statusText": statusText(resp),
		"headers":    headers,
	}

	// The Response constructor throws if these statuses are given a body at
	// all, even an empty stream.
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified:
		resp.Body.Close()
		releaseAbort()
		cancel()
		return js.Global().Get("Response").New(js.Null(), init)
	}
	return js.Global().Get("Response").New(bodyStream(resp.Body, cancel, releaseAbort), init)
}

// statusText recovers the reason phrase from Go's "200 OK" status line. An
// empty result is fine: the Response constructor then supplies the default.
func statusText(resp *http.Response) string {
	return strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
}

// bodyStream adapts an http response body to a ReadableStream. pull returns a
// promise, which is what gives the stream backpressure and what makes server
// sent events arrive as they are produced rather than in one lump at the end.
func bodyStream(body io.ReadCloser, cancel context.CancelFunc, releaseAbort func()) js.Value {
	buf := make([]byte, readBufferSize)
	var (
		finish  sync.Once
		release sync.Once
		pull    js.Func
		abandon js.Func
	)
	done := func() {
		finish.Do(func() {
			body.Close()
			releaseAbort()
			cancel()
		})
	}
	// The stream contract guarantees neither callback runs again once the
	// stream has closed, errored or been cancelled, which is what makes it
	// safe to release them from inside one of them.
	releaseFuncs := func() {
		release.Do(func() {
			pull.Release()
			abandon.Release()
		})
	}

	pull = js.FuncOf(func(this js.Value, args []js.Value) any {
		controller := args[0]
		return makePromise(func() (any, error) {
			n, err := body.Read(buf)
			if n > 0 {
				chunk := js.Global().Get("Uint8Array").New(n)
				js.CopyBytesToJS(chunk, buf[:n])
				controller.Call("enqueue", chunk)
				return js.Undefined(), nil
			}
			done()
			if err == nil || errors.Is(err, io.EOF) {
				controller.Call("close")
				releaseFuncs()
				return js.Undefined(), nil
			}
			releaseFuncs()
			return nil, err
		})
	})
	abandon = js.FuncOf(func(this js.Value, args []js.Value) any {
		done()
		releaseFuncs()
		return js.Undefined()
	})

	return js.Global().Get("ReadableStream").New(map[string]any{
		"pull":   pull,
		"cancel": abandon,
	})
}

// pingUntil retries the meow/meowed handshake until it succeeds or ctx
// expires. The first pings can be lost while either side's DERP connection is
// still coming up.
func pingUntil(ctx context.Context, cl *tailcatlib.Client) error {
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := cl.Ping(attempt)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
	}
}

// parsePrivateKey reads a saved tailcat.PrivateKey JSON, or returns a fresh
// one when the page has nothing saved yet.
func parsePrivateKey(encoded string) (*tailcatlib.PrivateKey, error) {
	if strings.TrimSpace(encoded) == "" {
		return tailcatlib.NewPrivateKey(), nil
	}
	var pk tailcatlib.PrivateKey
	if err := json.Unmarshal([]byte(encoded), &pk); err != nil {
		return nil, err
	}
	if pk.Private.IsZero() {
		return nil, errors.New("private key is missing")
	}
	return &pk, nil
}

func optString(v js.Value, name string) string {
	if p := v.Get(name); p.Type() == js.TypeString {
		return p.String()
	}
	return ""
}

func optLogf(v js.Value) logger.Logf {
	if v.Get("verbose").Truthy() {
		return log.Printf
	}
	return logger.Discard
}

// makePromise runs f on a new goroutine and returns a JavaScript Promise of
// its result, rejected with a JavaScript Error if f returns an error.
func makePromise(f func() (any, error)) js.Value {
	handler := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			if res, err := f(); err == nil {
				resolve.Invoke(res)
			} else {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
			}
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func rejectedPromise(err error) js.Value {
	return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New(err.Error()))
}
