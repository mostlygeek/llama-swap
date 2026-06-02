package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestScheduler_AdmitHighestFirst verifies that under contention the
// highest-priority waiter is admitted first, regardless of arrival order.
func TestScheduler_AdmitHighestFirst(t *testing.T) {
	q := newModelQueue(1)
	ctx := context.Background()

	busy := q.acquire(ctx, 0, 0, 0) // hold the slot

	results := make(chan int, 3)
	var wg sync.WaitGroup
	for _, p := range []int{1, 9, 5} {
		wg.Add(1)
		pr := p
		go func() {
			defer wg.Done()
			r := q.acquire(ctx, pr, 0, 0)
			results <- pr
			r.release()
		}()
		time.Sleep(15 * time.Millisecond) // deterministic enqueue order 1,9,5
	}

	// Wait for all three to be queued.
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := q.waiters.Len()
		q.mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	busy.release() // release one slot at a time by draining
	first := <-results
	if first != 9 {
		t.Fatalf("highest priority (9) should be admitted first, got %d", first)
	}
	wg.Wait()
}

// TestScheduler_RejectMaxQueueDepth verifies a 429 with Retry-After once the
// queue is full.
func TestScheduler_RejectMaxQueueDepth(t *testing.T) {
	q := newModelQueue(1)
	ctx := context.Background()

	busy := q.acquire(ctx, 1, 0, 0) // slot taken
	defer busy.release()

	// First waiter fits (depth 1). Use a goroutine since it blocks.
	go q.acquire(ctx, 1, time.Minute, 1)
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := q.waiters.Len()
		q.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Second waiter exceeds maxQueueDepth=1 -> immediate reject.
	a := q.acquire(ctx, 1, time.Minute, 1)
	if a.admitted {
		t.Fatal("request beyond maxQueueDepth should be rejected")
	}
	if a.retryAfter <= 0 {
		t.Fatal("rejection must carry a positive Retry-After estimate")
	}
	// Rejection must carry the capacity hint so the client can right-size.
	if a.reason != "queue_full" {
		t.Fatalf("reason = %q, want queue_full", a.reason)
	}
	if a.concurrency != 1 {
		t.Fatalf("hint concurrency = %d, want 1", a.concurrency)
	}
}

// TestScheduler_RejectMaxWait verifies rejection when the estimated wait exceeds
// maxWait at arrival.
func TestScheduler_RejectMaxWait(t *testing.T) {
	q := newModelQueue(1)
	q.observe(10 * time.Second) // seed EWMA high
	ctx := context.Background()

	busy := q.acquire(ctx, 1, 0, 0)
	defer busy.release()

	a := q.acquire(ctx, 1, 2*time.Second, 0) // est ~10s > 2s maxWait
	if a.admitted {
		t.Fatal("should reject when estimated wait exceeds maxWait")
	}
	if a.retryAfter < 2*time.Second {
		t.Fatalf("Retry-After %v should reflect the estimate", a.retryAfter)
	}
}

// TestScheduler_ContextCancelDuringWait ensures a canceled request frees its
// queue slot and does not leak the reserved concurrency slot.
func TestScheduler_ContextCancelDuringWait(t *testing.T) {
	q := newModelQueue(1)
	busy := q.acquire(context.Background(), 1, 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan admission, 1)
	go func() { done <- q.acquire(ctx, 1, time.Minute, 0) }()

	// Wait until queued, then cancel.
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := q.waiters.Len()
		q.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	a := <-done
	if a.admitted {
		t.Fatal("canceled request should not be admitted")
	}
	// Slot should still be held only by busy; releasing busy must leave inflight 0.
	busy.release()
	q.mu.Lock()
	inflight := q.inflight
	q.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("inflight should be 0 after release, got %d", inflight)
	}
}

// TestScheduler_FastPathNoContention confirms requests within the concurrency
// limit are all admitted immediately, without queueing.
func TestScheduler_FastPathNoContention(t *testing.T) {
	q := newModelQueue(2)
	ctx := context.Background()
	a1 := q.acquire(ctx, 1, 0, 0)
	a2 := q.acquire(ctx, 1, 0, 0)
	if !a1.admitted || !a2.admitted {
		t.Fatal("two requests within concurrency 2 should both be admitted")
	}
	a1.release()
	a2.release()
}
