package process

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type countingBody struct {
	io.Reader
	closed  chan struct{}
	once    sync.Once
	readAll *bool
}

func (c *countingBody) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// TestDrainOnAbortBody_EarlyCloseDrainsInsteadOfTearingDown proves the whole
// point of the type: a Close() before EOF must NOT close the upstream body
// immediately, because that is what the transport turns into a cancelled
// upstream request and what crashes an OVMS child mid-generation.
func TestProcessCommand_SurviveClientAbort_EarlyCloseDrainsInsteadOfTearingDown(t *testing.T) {
	closed := make(chan struct{})
	inner := &countingBody{Reader: strings.NewReader(strings.Repeat("x", 4096)), closed: closed}

	var drained io.ReadCloser
	b := &drainOnAbortBody{
		ReadCloser: inner,
		id:         "test",
		drainNow:   func(rc io.ReadCloser) { drained = rc },
	}

	// Read a little, like a client that consumed a few SSE chunks and vanished.
	buf := make([]byte, 16)
	if _, err := b.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if drained == nil {
		t.Fatal("early Close must hand the remainder to the drainer, not close it")
	}
	select {
	case <-closed:
		t.Fatal("upstream body was closed before EOF — this is the cancellation that crashes OVMS")
	default:
	}
}

// After a full read the body is finished, so Close must pass straight through
// and MUST NOT spawn a drain.
func TestProcessCommand_SurviveClientAbort_CloseAfterEOFPassesThrough(t *testing.T) {
	closed := make(chan struct{})
	inner := &countingBody{Reader: strings.NewReader("hello"), closed: closed}

	drainCalled := false
	b := &drainOnAbortBody{
		ReadCloser: inner,
		id:         "test",
		drainNow:   func(io.ReadCloser) { drainCalled = true },
	}

	if _, err := io.ReadAll(b); err != nil {
		t.Fatalf("readall: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if drainCalled {
		t.Fatal("a fully-read body must not be drained")
	}
	select {
	case <-closed:
	default:
		t.Fatal("a fully-read body must be closed normally")
	}
}

func TestProcessCommand_SurviveClientAbort_CloseIsIdempotent(t *testing.T) {
	inner := &countingBody{Reader: strings.NewReader("abc"), closed: make(chan struct{})}
	n := 0
	b := &drainOnAbortBody{ReadCloser: inner, id: "t", drainNow: func(io.ReadCloser) { n++ }}
	_ = b.Close()
	_ = b.Close()
	if n != 1 {
		t.Fatalf("drain ran %d times, want exactly 1", n)
	}
}

// TestUpstreamSurvivesClientDisconnect is the end-to-end proof against a real
// HTTP server: the client hangs up mid-stream and the upstream handler must
// still run to completion with a live, uncancelled request context.
func TestProcessCommand_SurviveClientAbort_UpstreamSurvivesClientDisconnect(t *testing.T) {
	finished := make(chan error, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 40; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
			if err := r.Context().Err(); err != nil {
				finished <- err // the abort we must never see
				return
			}
		}
		finished <- nil
	}))
	defer upstream.Close()

	// Mirrors the production wiring: WithoutCancel plus the draining body.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(context.WithoutCancel(r.Context()), "GET", upstream.URL, nil)
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Errorf("roundtrip: %v", err)
			return
		}
		body := &drainOnAbortBody{ReadCloser: resp.Body, id: "test"}
		defer body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, body)
	}))
	defer proxy.Close()

	// Read a couple of chunks, then hard-close mid-stream.
	client := &http.Client{}
	resp, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	buf := make([]byte, 32)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	resp.Body.Close()
	client.CloseIdleConnections()

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("upstream saw a cancelled context (%v) — the client disconnect propagated, which is the OVMS crash trigger", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("upstream never finished")
	}
}

// With surviveClientAbort off — the default — nothing is wrapped and the client's
// cancellation must still reach the upstream, which is what frees a llama.cpp slot.
func TestProcessCommand_SurviveClientAbort_DisabledPropagatesCancellation(t *testing.T) {
	saw := make(chan error, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 200; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
			if err := r.Context().Err(); err != nil {
				saw <- err
				return
			}
		}
		saw <- nil
	}))
	defer upstream.Close()

	// No WithoutCancel and no draining body: the untouched proxy path.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), "GET", upstream.URL, nil)
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	buf := make([]byte, 32)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	resp.Body.Close()
	http.DefaultClient.CloseIdleConnections()

	select {
	case err := <-saw:
		if err == nil {
			t.Fatal("with the option off the upstream must still see the client's cancellation")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("upstream never reported")
	}
}
