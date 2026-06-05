package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// scheduler provides configurable admission in front of each model's
// concurrency limit. One modelQueue per model bounds in-flight requests to the
// model's concurrencyLimit and, under contention, admits a waiter when a slot
// frees according to the configured mode:
//
//   - absolute:   highest effective priority first (FIFO within a priority)
//   - proportion: weighted fair queuing — callers admitted in proportion to
//     their priority used as a weight
//
// In either mode, PriorityIncreasePerSecondWaiting ages a waiter's effective
// priority up over time. A request is rejected (429 + Retry-After) only when
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
		q := newModelQueue(limit)
		q.mode = cfg.Scheduling.ResolvedMode()
		q.agingRate = cfg.Scheduling.PriorityIncreasePerSecondWaiting
		queues[id] = q
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
// it with a Retry-After estimate. caller identifies the weighting class
// (proportion mode); priority is the request's base priority/weight.
//
// posCh, when non-nil, receives the request's live 1-indexed queue position
// while it waits — the scheduler's analog of the swap queue's position display.
// Supplying it also opts the request out of the late maxWait timer: once a
// streaming caller is shown a position it is committed to a 200 response, so it
// waits until admitted or the client disconnects rather than being 429'd
// mid-stream. The entry-time rejections (queue full, hopeless estimate) still
// apply, before any position is sent.
func (s *scheduler) enqueue(ctx context.Context, modelID, caller string, priority int, posCh chan int) admission {
	q := s.queues[modelID]
	if q == nil {
		// Unmanaged model: admit without limiting, matching the legacy
		// middleware's pass-through for peer models.
		return admission{admitted: true, release: func() {}}
	}
	return q.acquire(ctx, caller, priority, s.cfg.MaxWait, s.cfg.MaxQueueDepth, posCh)
}

// modelQueue is a bounded admission queue for a single model.
type modelQueue struct {
	concurrency int
	mode        config.SchedulingMode
	agingRate   float64          // effective priority gained per second waited
	now         func() time.Time // injectable clock (tests)

	mu       sync.Mutex
	inflight int
	waiters  []*waiter
	seq      uint64        // tie-breaker: FIFO within equal priority
	ewma     time.Duration // measured service time, exponentially weighted

	// proportion mode (weighted fair queuing) virtual-time state:
	vtime       float64            // global virtual time = start of last admit
	classFinish map[string]float64 // per-caller virtual finish time
}

func newModelQueue(concurrency int) *modelQueue {
	return &modelQueue{
		concurrency: concurrency,
		mode:        config.ModeAbsolute,
		now:         time.Now,
		classFinish: map[string]float64{},
	}
}

// waiter is one request parked awaiting a slot.
type waiter struct {
	caller     string
	priority   int
	seq        uint64
	enqueuedAt time.Time
	ready      chan struct{} // closed when admitted
	posCh      chan int      // non-nil to receive live position updates
}

func (q *modelQueue) acquire(ctx context.Context, caller string, priority int, maxWait time.Duration, maxDepth int, posCh chan int) admission {
	q.mu.Lock()

	// Fast path: a slot is free and nobody is waiting.
	if q.inflight < q.concurrency && len(q.waiters) == 0 {
		q.inflight++
		q.mu.Unlock()
		return admission{admitted: true, release: q.release}
	}

	// Estimate the wait: requests ahead (queued + the overflow beyond capacity)
	// divided by how many drain per service interval.
	ahead := len(q.waiters) + (q.inflight - q.concurrency) + 1
	if ahead < 1 {
		ahead = 1
	}
	est := q.estimateWait(ahead)

	if maxDepth > 0 && len(q.waiters) >= maxDepth {
		rej := admission{retryAfter: est, reason: "queue_full",
			concurrency: q.concurrency, inflight: q.inflight, waiting: len(q.waiters)}
		q.mu.Unlock()
		return rej
	}
	if maxWait > 0 && est > maxWait {
		rej := admission{retryAfter: est, reason: "max_wait",
			concurrency: q.concurrency, inflight: q.inflight, waiting: len(q.waiters)}
		q.mu.Unlock()
		return rej
	}

	w := &waiter{caller: caller, priority: priority, seq: q.seq, enqueuedAt: q.now(), ready: make(chan struct{}), posCh: posCh}
	q.seq++
	q.waiters = append(q.waiters, w)
	q.broadcastPositionsLocked()
	q.mu.Unlock()

	var timer *time.Timer
	var timeout <-chan time.Time
	// A position-streaming waiter has committed to a 200 response and cannot be
	// 429'd mid-stream, so it skips the late maxWait timer and waits until
	// admitted or the client disconnects. The entry estimate gate above still
	// rejected it earlier if the wait looked hopeless.
	if posCh == nil && maxWait > 0 {
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
			a.waiting = len(q.waiters)
			q.mu.Unlock()
		}
		return a
	}
}

// abandon removes a waiter that gave up (context canceled or timed out). If it
// was already admitted by tryAdmit (so no longer in the queue), it returns an
// admitted admission so the caller still releases the slot.
func (q *modelQueue) abandon(w *waiter) admission {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.removeWaiterLocked(w) {
		q.broadcastPositionsLocked()
		q.resetIfIdleLocked()
		return admission{retryAfter: q.estimateWait(1)}
	}
	// Not in the queue: tryAdmit already pulled it (ready is closed). Honor the
	// slot so inflight stays balanced.
	select {
	case <-w.ready:
		return admission{admitted: true, release: q.release}
	default:
		return admission{retryAfter: q.estimateWait(1)}
	}
}

// removeWaiterLocked drops w from the queue by identity, returning whether it
// was present.
func (q *modelQueue) removeWaiterLocked(w *waiter) bool {
	for i, x := range q.waiters {
		if x == w {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return true
		}
	}
	return false
}

// release returns a slot, then admits the next waiter(s) if any.
func (q *modelQueue) release() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inflight--
	q.tryAdmitLocked()
	q.resetIfIdleLocked()
}

func (q *modelQueue) tryAdmitLocked() {
	admitted := false
	for q.inflight < q.concurrency && len(q.waiters) > 0 {
		order := q.admissionOrderLocked(q.now())
		w := order[0]
		q.commitLocked(w, q.now())
		q.removeWaiterLocked(w)
		q.inflight++
		close(w.ready)
		admitted = true
	}
	if admitted {
		// The front of the queue moved; refresh everyone still waiting.
		q.broadcastPositionsLocked()
	}
}

// resetIfIdleLocked clears the weighted-fair-queuing virtual clock once the
// model is fully idle, so shares are accounted within a contention episode and
// the float counters can't drift without bound.
func (q *modelQueue) resetIfIdleLocked() {
	if q.inflight == 0 && len(q.waiters) == 0 {
		q.vtime = 0
		if len(q.classFinish) > 0 {
			q.classFinish = map[string]float64{}
		}
	}
}

// effective returns a waiter's priority/weight with aging applied.
func (q *modelQueue) effective(w *waiter, now time.Time) float64 {
	p := float64(w.priority)
	if q.agingRate > 0 {
		p += q.agingRate * now.Sub(w.enqueuedAt).Seconds()
	}
	return p
}

// admissionOrderLocked returns the current waiters in the order they would be
// admitted. It does not mutate scheduler state, so it drives both the next-admit
// decision and the position display (keeping them consistent).
func (q *modelQueue) admissionOrderLocked(now time.Time) []*waiter {
	ordered := make([]*waiter, len(q.waiters))
	copy(ordered, q.waiters)
	if q.mode == config.ModeProportion {
		return q.proportionOrder(ordered, now)
	}
	// absolute: highest effective priority first, FIFO within a priority.
	sort.SliceStable(ordered, func(i, j int) bool {
		ei, ej := q.effective(ordered[i], now), q.effective(ordered[j], now)
		if ei != ej {
			return ei > ej
		}
		return ordered[i].seq < ordered[j].seq
	})
	return ordered
}

// proportionOrder simulates weighted-fair-queuing admission over the given
// waiters on a clone of the virtual clock, returning admission order. Each
// caller's next request gets a virtual start of max(its finish, global vtime);
// the smallest start is served next and the caller's finish advances by
// 1/weight, so higher-weight callers are served proportionally more often.
func (q *modelQueue) proportionOrder(ws []*waiter, now time.Time) []*waiter {
	// Stable per-caller FIFO order.
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].seq < ws[j].seq })

	finish := make(map[string]float64, len(q.classFinish))
	for k, v := range q.classFinish {
		finish[k] = v
	}
	vtime := q.vtime

	heads := map[string]int{} // caller -> index of next unserved request in ws
	remaining := len(ws)
	order := make([]*waiter, 0, len(ws))
	for remaining > 0 {
		var pick *waiter
		var pickStart float64
		for i, w := range ws {
			if w == nil {
				continue
			}
			if idx, seen := heads[w.caller]; seen && idx != i {
				continue // not this caller's current head
			}
			start := finish[w.caller]
			if start < vtime {
				start = vtime
			}
			if pick == nil || start < pickStart || (start == pickStart && w.seq < pick.seq) {
				pick, pickStart = w, start
			}
		}
		// Serve pick: advance its caller's virtual finish and the global clock.
		weight := q.effective(pick, now)
		if weight < 1 {
			weight = 1
		}
		vtime = pickStart
		finish[pick.caller] = pickStart + 1.0/weight
		order = append(order, pick)
		// Remove pick from ws and advance the caller's head.
		for i := range ws {
			if ws[i] == pick {
				ws[i] = nil
				break
			}
		}
		next := -1
		for i, w := range ws {
			if w != nil && w.caller == pick.caller {
				next = i
				break
			}
		}
		if next >= 0 {
			heads[pick.caller] = next
		} else {
			delete(heads, pick.caller)
		}
		remaining--
	}
	return order
}

// commitLocked applies the real virtual-clock side effect of admitting w in
// proportion mode (no-op in absolute mode). It mirrors the first step of
// proportionOrder so the display and the actual admission agree.
func (q *modelQueue) commitLocked(w *waiter, now time.Time) {
	if q.mode != config.ModeProportion {
		return
	}
	start := q.classFinish[w.caller]
	if start < q.vtime {
		start = q.vtime
	}
	weight := q.effective(w, now)
	if weight < 1 {
		weight = 1
	}
	q.vtime = start
	q.classFinish[w.caller] = start + 1.0/weight
}

// broadcastPositionsLocked sends every waiter that asked for live updates its
// current 1-indexed position in admission order. Callers hold q.mu. Sends are
// non-blocking and latest-wins so a slow consumer never stalls the scheduler.
func (q *modelQueue) broadcastPositionsLocked() {
	if len(q.waiters) == 0 {
		return
	}
	for i, w := range q.admissionOrderLocked(q.now()) {
		if w.posCh != nil {
			sendPosition(w.posCh, i+1)
		}
	}
}

// sendPosition does a non-blocking latest-wins send: a full channel is drained
// and the newest value queued, so the scheduler never blocks on a consumer.
func sendPosition(ch chan int, pos int) {
	select {
	case ch <- pos:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- pos:
		default:
		}
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
