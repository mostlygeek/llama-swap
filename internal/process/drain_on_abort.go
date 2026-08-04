package process

import (
	"io"
	"sync/atomic"
	"time"
)

// drainMaxDuration and drainMaxBytes bound the background drain so a wedged or
// runaway upstream can never hold a goroutine or a connection open forever.
const (
	drainMaxDuration = 5 * time.Minute
	drainMaxBytes    = 64 << 20 // 64 MiB
)

// activeDrains counts drains currently in flight. Test-only observability.
var activeDrains atomic.Int64

// drainOnAbortBody keeps an upstream generation alive when the downstream client
// disappears mid-stream.
//
// Closing an HTTP response body that has not reached EOF makes Go's transport
// tear the connection down, which the upstream observes as a cancelled request.
// OVMS crashes on exactly that path — its continuous-batching block manager
// asserts on a sequence id it has already freed
// (openvino.genai .../continuous_batching/cache/block_manager.hpp) — and the
// process exit kills every other generation sharing the batch, not just the one
// whose client left.
//
// So on an early Close we hand the remainder to a bounded background reader and
// let the upstream finish writing normally. The client is already gone; nobody
// is waiting on those bytes.
type drainOnAbortBody struct {
	io.ReadCloser

	logger interface {
		Infof(format string, args ...any)
	}
	id       string
	sawEOF   bool
	closed   bool
	drainNow func(io.ReadCloser) // test seam; nil means drain in a goroutine
}

func (b *drainOnAbortBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		// Any terminal read error means there is nothing left worth draining.
		b.sawEOF = true
	}
	return n, err
}

func (b *drainOnAbortBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	if b.sawEOF {
		return b.ReadCloser.Close()
	}
	if b.logger != nil {
		b.logger.Infof("<%s> client left mid-stream; draining upstream response so the generation completes", b.id)
	}
	if b.drainNow != nil {
		b.drainNow(b.ReadCloser)
		return nil
	}
	body := b.ReadCloser
	activeDrains.Add(1)
	go func() {
		defer activeDrains.Add(-1)
		defer body.Close()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(io.Discard, io.LimitReader(body, drainMaxBytes))
		}()
		select {
		case <-done:
		case <-time.After(drainMaxDuration):
			// Bounded: give up and let the deferred Close tear it down.
		}
	}()
	return nil
}
