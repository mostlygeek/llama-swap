package process

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

// closeAfterStreamUpstream mimics llama-server after a streamed response
// (issue #1205): it advertises Keep-Alive on the stream, then drops the
// connection instead of serving another request on it. The drop is made
// deterministic by waiting until the next request has arrived before closing,
// which is the worst case of the race: the proxy has already written the
// request to a connection the upstream will never answer.
type closeAfterStreamUpstream struct {
	ln    net.Listener
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newCloseAfterStreamUpstream(t *testing.T) *closeAfterStreamUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	u := &closeAfterStreamUpstream{ln: ln, conns: map[net.Conn]struct{}{}}
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u.mu.Lock()
			u.conns[c] = struct{}{}
			u.mu.Unlock()
			u.wg.Add(1)
			go func() {
				defer u.wg.Done()
				u.serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		// Close open connections too, so a serve goroutine blocked reading a
		// pooled connection cannot hang the test.
		ln.Close()
		u.mu.Lock()
		for c := range u.conns {
			c.Close()
		}
		u.mu.Unlock()
		u.wg.Wait()
	})
	return u
}

func (u *closeAfterStreamUpstream) URL() string { return "http://" + u.ln.Addr().String() }

func (u *closeAfterStreamUpstream) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		req.Body.Close()

		if req.URL.Path == "/health" {
			_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nKeep-Alive: timeout=5, max=100\r\n\r\nok")
			continue
		}

		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n"+
			"Content-Type: text/event-stream\r\n"+
			"Transfer-Encoding: chunked\r\n"+
			"Keep-Alive: timeout=5, max=100\r\n\r\n")
		// One chunk (0xe == len("data: [DONE]\n\n")), then the terminating chunk.
		_, _ = io.WriteString(c, "e\r\ndata: [DONE]\n\n\r\n0\r\n\r\n")

		// Hold the connection until the next request lands on it, then close
		// without answering. Reading it in full first makes the close a clean
		// FIN, so the proxy sees the same "EOF" as in the report rather than a
		// reset.
		if next, err := http.ReadRequest(br); err == nil {
			_, _ = io.Copy(io.Discard, next.Body)
		}
		return
	}
}

// TestProcessCommand_UpstreamClosesAfterStream is the regression test for
// issue #1205: a request that follows a streamed response must not fail with
// a 502 "proxy error: EOF" because the upstream closed the connection that the
// transport kept in its idle pool.
func TestProcessCommand_UpstreamClosesAfterStream(t *testing.T) {
	skipIfNoSimpleResponder(t)

	upstream := newCloseAfterStreamUpstream(t)

	logBuf := &syncBuffer{}
	cmd, _ := simpleResponderCmd(t, "-silent")
	p, err := New(context.Background(), t.Name(), config.ModelConfig{
		Cmd:                cmd,
		Proxy:              upstream.URL(),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	}, logmon.NewWriter(io.Discard), logmon.NewWriter(logBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { p.Stop(testStopTimeout) }) //nolint: errcheck

	_ = runAsync(t, p)

	front := httptest.NewServer(p)
	t.Cleanup(front.Close)

	for i := 1; i <= 3; i++ {
		resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"m","stream":true}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (body %q, proxy log %q)",
				i, resp.StatusCode, body, logBuf.String())
		}
	}
}
