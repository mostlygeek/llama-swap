package server

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// scheduler provides priority-aware fair admission in front of each model's
// concurrency limit. One modelQueue per model bounds in-flight requests to the
// model's concurrencyLimit and, under contention, admits the highest-priority
// waiter when a slot frees. A request is rejected (429 + Retry-After) only when
// the queue is full or its estimated wait exceeds maxWait.
//
// The scheduler is only constructed when config.Scheduling is enabled; the
// middleware falls back to the legacy semaphore otherwise.
type scheduler struct {
	cfg    config.SchedulingConfig
	queues map[string]*modelQueue
}

// newSchedulerIfEnabled builds a scheduler only when config.Scheduling is
// enabled; otherwise it returns nil and the middleware uses the legacy path.
func newSchedulerIfEnabled(cfg config.Config) *scheduler {
	if !cfg.Scheduling.Enabled() {
		return nil
	}
	return newScheduler(cfg)
}

func newScheduler(cfg config.Config) *scheduler {
	queues := make(map[string]*modelQueue, len(cfg.Models))
	for id, mc := range cfg.Models {
		limit := defaultConcurrencyLimit
		if mc.ConcurrencyLimit > 0 {
			limit = mc.ConcurrencyLimit
		}
		queues[id] = newModelQueue(limit)
	}
	return &scheduler{cfg: cfg.Scheduling, queues: queues}
}

// queueFor returns the queue for a model, or nil for models the scheduler does
// not manage (e.g. peer-routed models without a local config entry).
func (s *scheduler) queueFor(modelID string) *modelQueue {
	return s.queues[modelID]
}

// admission is returned by enqueue. When admitted is true the caller holds a
// slot and MUST call release. When admitted is false the request was rejected
// and retryAfter carries the suggested backoff; the hint fields describe the
// model's capacity so the client can right-size its own executor.
type admission struct {
	admitted   bool
	retryAfter time.Duration
	release    func()

	// Populated on rejection (admitted == false):
	reason      string // "queue_full" or "max_wait"
	concurrency int    // the model's hard slot count — the client's parallelism ceiling
	inflight    int    // slots in use at reject time
	waiting     int    // requests already queued at reject time
}

// enqueue admits the request immediately, blocks until a slot frees, or rejects
// it with a Retry-After estimate. priority is the caller's configured priority
// (higher served first).
func (s *scheduler) enqueue(ctx context.Context, modelID string, priority int) admission {
	q := s.queues[modelID]
	if q == nil {
		// Unmanaged model: admit without limiting, matching the legacy
		// middleware's pass-through for peer models.
		return admission{admitted: true, release: func() {}}
	}
	return q.acquire(ctx, priority, s.cfg.MaxWait, s.cfg.MaxQueueDepth)
}

// modelQueue is a bounded priority admission queue for a single model.
type modelQueue struct {
	concurrency int

	mu       sync.Mutex
	inflight int
	waiters  waiterHeap
	seq      uint64        // tie-breaker: FIFO within equal priority
	ewma     time.Duration // measured service time, exponentially weighted
}

func newModelQueue(concurrency int) *modelQueue {
	return &modelQueue{concurrency: concurrency}
}

// waiter is one request parked awaiting a slot.
type waiter struct {
	priority int
	seq      uint64
	ready    chan struct{} // closed when admitted
	index    int           // heap index, -1 once removed
	canceled bool
}

func (q *modelQueue) acquire(ctx context.Context, priority int, maxWait time.Duration, maxDepth int) admission {
	q.mu.Lock()

	// Fast path: a slot is free and nobody is waiting.
	if q.inflight < q.concurrency && q.waiters.Len() == 0 {
		q.inflight++
		q.mu.Unlock()
		return admission{admitted: true, release: q.release}
	}

	// Estimate the wait: requests ahead (queued + the overflow beyond capacity)
	// divided by how many drain per service interval.
	ahead := q.waiters.Len() + (q.inflight - q.concurrency) + 1
	if ahead < 1 {
		ahead = 1
	}
	est := q.estimateWait(ahead)

	if maxDepth > 0 && q.waiters.Len() >= maxDepth {
		rej := admission{retryAfter: est, reason: "queue_full",
			concurrency: q.concurrency, inflight: q.inflight, waiting: q.waiters.Len()}
		q.mu.Unlock()
		return rej
	}
	if maxWait > 0 && est > maxWait {
		rej := admission{retryAfter: est, reason: "max_wait",
			concurrency: q.concurrency, inflight: q.inflight, waiting: q.waiters.Len()}
		q.mu.Unlock()
		return rej
	}

	w := &waiter{priority: priority, seq: q.seq, ready: make(chan struct{})}
	q.seq++
	heap.Push(&q.waiters, w)
	q.mu.Unlock()

	var timer *time.Timer
	var timeout <-chan time.Time
	if maxWait > 0 {
		timer = time.NewTimer(maxWait)
		timeout = timer.C
		defer timer.Stop()
	}

	select {
	case <-w.ready:
		// Admitted after waiting: tryAdmit already incremented inflight.
		return admission{admitted: true, release: q.release}
	case <-ctx.Done():
		return q.abandon(w)
	case <-timeout:
		a := q.abandon(w)
		if !a.admitted {
			q.mu.Lock()
			a.reason = "max_wait"
			a.concurrency = q.concurrency
			a.inflight = q.inflight
			a.waiting = q.waiters.Len()
			q.mu.Unlock()
		}
		return a
	}
}

// abandon removes a waiter that gave up (context canceled or timed out). If it
// was admitted in the race between firing and locking, it returns an admitted
// admission so the caller still releases the slot.
func (q *modelQueue) abandon(w *waiter) admission {
	q.mu.Lock()
	defer q.mu.Unlock()
	if w.index >= 0 {
		heap.Remove(&q.waiters, w.index)
		return admission{retryAfter: q.estimateWait(1)}
	}
	// Already admitted (ready closed) but we lost the select race; honor the slot.
	select {
	case <-w.ready:
		return admission{admitted: true, release: q.release}
	default:
		w.canceled = true
		return admission{retryAfter: q.estimateWait(1)}
	}
}

// release returns a slot, then admits the next highest-priority waiter if any.
func (q *modelQueue) release() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inflight--
	q.tryAdmitLocked()
}

func (q *modelQueue) tryAdmitLocked() {
	for q.inflight < q.concurrency && q.waiters.Len() > 0 {
		w := heap.Pop(&q.waiters).(*waiter)
		if w.canceled {
			continue
		}
		q.inflight++
		close(w.ready)
	}
}

// observe folds a measured service time into the model's EWMA (alpha = 0.25).
func (q *modelQueue) observe(d time.Duration) {
	if d <= 0 {
		return
	}
	q.mu.Lock()
	if q.ewma <= 0 {
		q.ewma = d
	} else {
		q.ewma = (d + 3*q.ewma) / 4
	}
	q.mu.Unlock()
}

// estimateWait computes ahead/concurrency service intervals at the current
// EWMA. Callers hold q.mu. Falls back to a small seed before any observation.
func (q *modelQueue) estimateWait(ahead int) time.Duration {
	ewma := q.ewma
	if ewma <= 0 {
		ewma = time.Second // pre-observation seed
	}
	batches := (ahead + q.concurrency - 1) / q.concurrency
	if batches < 1 {
		batches = 1
	}
	return ewma * time.Duration(batches)
}

// waiterHeap orders waiters by priority (higher first), then seq (FIFO).
type waiterHeap []*waiter

func (h waiterHeap) Len() int { return len(h) }
func (h waiterHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	return h[i].seq < h[j].seq
}
func (h waiterHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *waiterHeap) Push(x any) {
	w := x.(*waiter)
	w.index = len(*h)
	*h = append(*h, w)
}
func (h *waiterHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	w.index = -1
	*h = old[:n-1]
	return w
}
