package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// TestScheduler_AdmitHighestFirst verifies that under contention the
// highest-priority waiter is admitted first, regardless of arrival order.
func TestScheduler_AdmitHighestFirst(t *testing.T) {
	q := newModelQueue(1)
	ctx := context.Background()

	busy := q.acquire(ctx, "", 0, 0, 0, nil) // hold the slot

	results := make(chan int, 3)
	var wg sync.WaitGroup
	for _, p := range []int{1, 9, 5} {
		wg.Add(1)
		pr := p
		go func() {
			defer wg.Done()
			r := q.acquire(ctx, "", pr, 0, 0, nil)
			results <- pr
			r.release()
		}()
		time.Sleep(15 * time.Millisecond) // deterministic enqueue order 1,9,5
	}

	// Wait for all three to be queued.
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := len(q.waiters)
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

	busy := q.acquire(ctx, "", 1, 0, 0, nil) // slot taken
	defer busy.release()

	// First waiter fits (depth 1). Use a goroutine since it blocks.
	go q.acquire(ctx, "", 1, time.Minute, 1, nil)
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := len(q.waiters)
		q.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Second waiter exceeds maxQueueDepth=1 -> immediate reject.
	a := q.acquire(ctx, "", 1, time.Minute, 1, nil)
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

	busy := q.acquire(ctx, "", 1, 0, 0, nil)
	defer busy.release()

	a := q.acquire(ctx, "", 1, 2*time.Second, 0, nil) // est ~10s > 2s maxWait
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
	busy := q.acquire(context.Background(), "", 1, 0, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan admission, 1)
	go func() { done <- q.acquire(ctx, "", 1, time.Minute, 0, nil) }()

	// Wait until queued, then cancel.
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := len(q.waiters)
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
	a1 := q.acquire(ctx, "", 1, 0, 0, nil)
	a2 := q.acquire(ctx, "", 1, 0, 0, nil)
	if !a1.admitted || !a2.admitted {
		t.Fatal("two requests within concurrency 2 should both be admitted")
	}
	a1.release()
	a2.release()
}

// TestScheduler_BroadcastsPosition verifies a position-streaming waiter receives
// its 1-indexed queue position, and that the position moves up as waiters ahead
// of it are admitted.
func TestScheduler_BroadcastsPosition(t *testing.T) {
	q := newModelQueue(1)
	ctx := context.Background()

	busy := q.acquire(ctx, "", 1, 0, 0, nil) // hold the only slot

	// A high-priority waiter sits ahead of our tracked one.
	go q.acquire(ctx, "", 9, time.Minute, 0, nil)
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		n := len(q.waiters)
		q.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	posCh := make(chan int, 1)
	admitted := make(chan struct{})
	go func() {
		a := q.acquire(ctx, "", 1, time.Minute, 0, posCh)
		close(admitted)
		_ = a
	}()

	// First broadcast: behind the priority-9 waiter -> position #2.
	if got := waitPosition(t, posCh); got != 2 {
		t.Fatalf("initial position = %d, want 2", got)
	}

	// Free a slot: the priority-9 waiter is admitted, ours moves to #1.
	busy.release()
	if got := waitPosition(t, posCh); got != 1 {
		t.Fatalf("position after release = %d, want 1", got)
	}
}

// waitPosition reads the next position broadcast, failing on timeout.
func waitPosition(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for position broadcast")
		return 0
	}
}

// TestScheduler_StreamingSkipsMaxWaitTimer verifies that a position-streaming
// waiter (posCh != nil) is not rejected by the late maxWait timer: once it is
// shown a position it is committed to a 200 and must wait until admitted. The
// entry-time estimate gate still applies and is exercised separately.
func TestScheduler_StreamingSkipsMaxWaitTimer(t *testing.T) {
	q := newModelQueue(1)
	ctx := context.Background()

	busy := q.acquire(ctx, "", 1, 0, 0, nil) // hold the slot

	posCh := make(chan int, 1)
	done := make(chan admission, 1)
	// Tiny maxWait that passes the entry estimate gate (seed EWMA is 1s, est for
	// one-ahead is 1s which is <= maxWait) but would fire the late timer quickly
	// for a non-streaming waiter. The streaming waiter must ignore it.
	go func() { done <- q.acquire(ctx, "", 1, 1500*time.Millisecond, 0, posCh) }()

	// It should still be waiting well past maxWait, not 429'd.
	select {
	case a := <-done:
		t.Fatalf("streaming waiter resolved early (admitted=%v); late maxWait timer should be disabled", a.admitted)
	case <-time.After(2 * time.Second):
	}

	// Releasing the slot admits it.
	busy.release()
	select {
	case a := <-done:
		if !a.admitted {
			t.Fatal("streaming waiter should be admitted once a slot frees")
		}
		a.release()
	case <-time.After(time.Second):
		t.Fatal("streaming waiter not admitted after slot freed")
	}
}

// waitForWaiters blocks until the queue has exactly n parked waiters.
func waitForWaiters(t *testing.T, q *modelQueue, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		got := len(q.waiters)
		q.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d waiters (have %d)", n, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestScheduler_AgingOvertakesFreshHigher verifies that with aging on, a
// low-priority request that has waited long enough overtakes a freshly arriving
// higher-priority one (anti-starvation). Without aging the higher base priority
// would win.
func TestScheduler_AgingOvertakesFreshHigher(t *testing.T) {
	q := newModelQueue(1)
	q.agingRate = 1.0 // +1 effective priority per second waited
	base := time.Now()
	var clkMu sync.Mutex
	clk := base
	q.now = func() time.Time { clkMu.Lock(); defer clkMu.Unlock(); return clk }
	setClk := func(d time.Duration) { clkMu.Lock(); clk = base.Add(d); clkMu.Unlock() }

	ctx := context.Background()
	busy := q.acquire(ctx, "", 0, 0, 0, nil) // hold the slot

	results := make(chan string, 2)
	go func() { a := q.acquire(ctx, "low", 1, 0, 0, nil); results <- "low"; a.release() }()
	waitForWaiters(t, q, 1)

	setClk(10 * time.Second) // "low" has now waited 10s -> effective 11
	go func() { a := q.acquire(ctx, "high", 5, 0, 0, nil); results <- "high"; a.release() }()
	waitForWaiters(t, q, 2)

	busy.release()
	if first := <-results; first != "low" {
		t.Fatalf("aged low (eff 11) should beat fresh high (eff 5); got %q first", first)
	}
	<-results
}

// TestScheduler_ProportionWeightedShares verifies that proportion mode admits
// callers in proportion to their weight: a weight-3 caller gets ~3x the
// admissions of a weight-1 caller under sustained contention.
func TestScheduler_ProportionWeightedShares(t *testing.T) {
	q := newModelQueue(1)
	q.mode = config.ModeProportion
	now := time.Now()
	q.now = func() time.Time { return now }

	seq := uint64(0)
	add := func(caller string, weight, n int) {
		for i := 0; i < n; i++ {
			q.waiters = append(q.waiters, &waiter{caller: caller, priority: weight, seq: seq, enqueuedAt: now})
			seq++
		}
	}
	add("A", 3, 40)
	add("B", 1, 40)

	order := q.admissionOrderLocked(now)
	a, b := 0, 0
	for _, w := range order[:16] {
		if w.caller == "A" {
			a++
		} else {
			b++
		}
	}
	// Expect ~12:4 (3:1). Allow slack for boundary effects.
	if a < 10 || a > 14 {
		t.Fatalf("weighted shares off: A=%d B=%d in first 16, want ~12:4", a, b)
	}
}
