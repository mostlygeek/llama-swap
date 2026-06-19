package scheduler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// BackpressureError is the shared.HTTPError fairshare returns when it sheds a
// request under contention: a 429 carrying the model's capacity on
// X-RateLimit-* headers and Retry-After, plus a JSON body describing the
// contention so a client can right-size its own parallelism instead of fanning
// out and competing with itself. router.SendError renders it verbatim.
type BackpressureError struct {
	Model       string
	Reason      string // "queue_full" or "max_wait"
	RetryAfter  int    // whole seconds, >= 1
	Concurrency int    // the model's hard slot count
	Inflight    int    // slots in use at reject time
	Waiting     int    // requests already queued at reject time
}

func (e *BackpressureError) Error() string {
	return fmt.Sprintf("too many requests for model %s (%s): retry after %ds", e.Model, e.Reason, e.RetryAfter)
}

func (e *BackpressureError) StatusCode() int { return http.StatusTooManyRequests }

func (e *BackpressureError) Header() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Retry-After", strconv.Itoa(e.RetryAfter))
	h.Set("X-RateLimit-Limit", strconv.Itoa(e.Concurrency))
	h.Set("X-RateLimit-Inflight", strconv.Itoa(e.Inflight))
	h.Set("X-RateLimit-Waiting", strconv.Itoa(e.Waiting))
	return h
}

func (e *BackpressureError) Body() []byte {
	payload := map[string]any{
		"error":       "Too many requests",
		"reason":      e.Reason,
		"retry_after": e.RetryAfter,
		"model":       e.Model,
		"hint": map[string]int{
			"max_concurrency": e.Concurrency,
			"inflight":        e.Inflight,
			"waiting":         e.Waiting,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"error":"Too many requests"}`)
	}
	return b
}

// fairWaiter is one request parked in the fairshare queue awaiting either a
// model swap or a free concurrency slot.
type fairWaiter struct {
	req        HandlerReq
	caller     string
	priority   int
	seq        uint64
	enqueuedAt time.Time
}

// callerOf resolves a request's caller id (the API key) from its context, or ""
// when none was sent. The request-context middleware stores it on
// shared.ReqContextData; the nil-Ctx guard keeps non-fairshare callers and
// tests that omit a context safe.
func callerOf(req HandlerReq) string {
	if req.Ctx == nil {
		return ""
	}
	data, _ := shared.ReadContext(req.Ctx)
	return data.ApiKey
}

// maskCaller renders a caller id for the activity log without leaking the raw
// API key: "" becomes "anonymous", a short value passes through (too short to
// be a secret), and anything longer is reduced to a 5-char prefix plus its
// length (e.g. "sk-ragtag" -> "sk-ra…9") so distinct callers stay
// distinguishable without exposing the key.
func maskCaller(caller string) string {
	if caller == "" {
		return "anonymous"
	}
	if len(caller) <= 5 {
		return caller
	}
	return caller[:5] + "…" + strconv.Itoa(len(caller))
}

// modelSchedState holds the per-model bookkeeping the fairshare scheduler keeps
// outside the run-loop-shared maps: an EWMA of service time (for Retry-After
// estimates) and the weighted-fair-queuing virtual clock used in proportion
// mode.
type modelSchedState struct {
	ewma   time.Duration // measured service time, exponentially weighted (alpha 0.25)
	starts []time.Time   // grant start times, FIFO-matched on serve-done for the EWMA

	// proportion mode virtual-time state:
	vtime       float64            // global virtual time = start of last admit
	classFinish map[string]float64 // per-caller virtual finish time
}

// FairShare is a priority-aware fair-admission scheduler. It makes the same
// swap decisions as FIFO, but additionally enforces each model's concurrency
// limit itself: when a ready model is at its limit, over-limit requests are
// queued and admitted as slots free in priority order rather than served
// immediately. Two modes interpret the priority number differently —
// "absolute" (rank: highest served first, FIFO within a rank) and "proportion"
// (weight: callers admitted in proportion to weight, weighted fair queuing) —
// and PriorityIncreasePerSecondWaiting ages a waiter's effective priority up
// over time in either mode.
//
// Like every Scheduler, all methods run on the baseRouter run-loop goroutine,
// so no internal locking is needed.
type FairShare struct {
	name    string
	logger  *logmon.Monitor
	planner Swapper
	cfg     config.FairShareConfig
	limits  map[string]int // model ID -> concurrency limit
	effects Effects
	now     func() time.Time

	active   map[string]*activeSwap // in-flight swaps (waiters list unused; queue holds waiters)
	inFlight map[string]int
	queued   []*fairWaiter
	seq      uint64

	state map[string]*modelSchedState
}

// NewFairShare builds a fairshare scheduler. limits maps each model ID to its
// concurrency limit (the router derives this from each model's concurrencyLimit,
// defaulting unset entries). It matches scheduler.Factory once a planner and
// config are captured in a closure.
func NewFairShare(name string, logger *logmon.Monitor, planner Swapper, cfg config.FairShareConfig, limits map[string]int, eff Effects) *FairShare {
	return &FairShare{
		name:     name,
		logger:   logger,
		planner:  planner,
		cfg:      cfg,
		limits:   limits,
		effects:  eff,
		now:      time.Now,
		active:   make(map[string]*activeSwap),
		inFlight: make(map[string]int),
		state:    make(map[string]*modelSchedState),
	}
}

// OnRequest mirrors FIFO's decision tree, with one addition: a ready model that
// is at its concurrency limit does not fast-path serve — the request is queued
// (or shed with a 429) and admitted later in priority order.
func (s *FairShare) OnRequest(req HandlerReq) {
	state, ok := s.effects.ModelState(req.Model)
	if !ok {
		s.logger.Debugf("%s: model %s not handled by this router", s.name, req.Model)
		s.effects.GrantError(req, ErrModelNotFound)
		return
	}

	prio := s.cfg.PriorityFor(callerOf(req), req.Model)

	// A swap for this model is already in flight: it can't serve until the swap
	// completes, so park it. OnSwapDone will drain it.
	if _, ok := s.active[req.Model]; ok {
		s.enqueue(req, prio)
		return
	}

	running := s.runningSet(req.Model)
	evict := s.planner.EvictionFor(req.Model, running)

	if state == process.StateReady && len(evict) == 0 && !collidesWith(req.Model, evict, s.active) {
		// Ungated (non-inference) request to a ready model: serve immediately
		// without consuming a concurrency slot or queueing behind inference, so
		// the model's web UI and other lightweight endpoints stay reachable
		// while it is saturated. Cold-model ungated requests fall through to the
		// normal path below (they load the model and count once).
		if !s.cfg.Gated(req.Path) {
			s.effects.GrantServeUntracked(req, req.Model)
			return
		}
		// Ready and nothing to evict — gate on the concurrency limit.
		if s.inFlight[req.Model] < s.limit(req.Model) && s.countWaiters(req.Model) == 0 {
			s.grantHandler(req, req.Model)
			return
		}
		if s.inFlight[req.Model] >= s.limit(req.Model) {
			if rej := s.boundedReject(req.Model); rej != nil {
				s.logger.Debugf("%s: shedding request for model %s (%s)", s.name, req.Model, rej.Reason)
				s.effects.GrantError(req, rej)
				return
			}
		}
		s.enqueue(req, prio)
		s.drainQueue()
		return
	}

	// Not servable now: collision or would-evict-busy means wait; otherwise a
	// swap is needed. In all cases park the request and let drainQueue decide
	// (starting the swap if appropriate).
	s.enqueue(req, prio)
	s.drainQueue()
}

// OnSwapDone errors every queued waiter for a failed swap, drops the swap from
// active tracking, then drains the queue: a finished swap may have made a model
// ready or freed a collision.
func (s *FairShare) OnSwapDone(ev SwapDone) {
	if _, ok := s.active[ev.ModelID]; !ok {
		return
	}
	delete(s.active, ev.ModelID)

	if ev.Err != nil {
		s.errorModelWaiters(ev.ModelID, ev.Err)
	}
	s.drainQueue()
}

// OnServeDone decrements the model's in-flight count, folds the measured service
// time into its EWMA, and drains the queue — a freed slot may admit the next
// waiter (and a fully-drained model may now be evictable).
func (s *FairShare) OnServeDone(ev ServeDoneEvent) {
	if s.inFlight[ev.ModelID] > 0 {
		s.inFlight[ev.ModelID]--
	}
	s.observeServiceTime(ev.ModelID)
	if s.inFlight[ev.ModelID] <= 0 {
		delete(s.inFlight, ev.ModelID)
	}
	s.drainQueue()
	s.resetIfIdle(ev.ModelID)
}

// OnUnload errors waiters for the unloaded models, stops the processes
// (synchronously), then drains the queue.
func (s *FairShare) OnUnload(targets []string, timeout time.Duration) {
	unloadErr := fmt.Errorf("%s: model unloaded", s.name)

	targetSet := make(map[string]bool, len(targets))
	for _, id := range targets {
		targetSet[id] = true
	}
	for id := range targetSet {
		delete(s.active, id)
		s.errorModelWaiters(id, unloadErr)
	}

	s.effects.StopProcesses(timeout, targets)
	s.drainQueue()
}

// OnShutdown errors every queued waiter.
func (s *FairShare) OnShutdown(err error) {
	for _, w := range s.queued {
		s.effects.GrantError(w.req, err)
	}
	s.queued = nil
}

// OnCancel prunes a request whose client disconnected before it was granted, so
// it never triggers a model load or admission. Fairshare parks all waiters in
// the queue (active swaps hold none), so removing the matching queue entry and
// re-running the drain/position broadcast is sufficient. Matching is by the
// request's Respond channel, which uniquely identifies the in-flight caller.
func (s *FairShare) OnCancel(req HandlerReq) {
	removed := false
	kept := s.queued[:0]
	for _, w := range s.queued {
		if w.req.Respond == req.Respond {
			removed = true
			continue
		}
		kept = append(kept, w)
	}
	s.queued = kept
	if removed {
		s.logger.Debugf("%s: cancelled request for model %s pruned from queue", s.name, req.Model)
		s.drainQueue()
	}
}

// grantHandler hands the caller a tracked handler and, only if the caller was
// still there to receive it, bumps in-flight and records the grant time for the
// service-time EWMA. It also records the request's masked caller and the
// priority/weight (and mode) it was scheduled under as request metadata, which
// the metrics middleware surfaces in the activity log.
func (s *FairShare) grantHandler(req HandlerReq, modelID string) bool {
	caller := callerOf(req)
	if err := shared.SetReqData(req.Ctx, "caller", maskCaller(caller)); err != nil {
		s.logger.Debugf("%s: failed to set request metadata: %v", s.name, err)
	} else {
		shared.SetReqData(req.Ctx, "fairshare_priority", strconv.Itoa(s.cfg.PriorityFor(caller, modelID)))
		shared.SetReqData(req.Ctx, "fairshare_mode", string(s.cfg.ResolvedMode()))
	}
	if s.effects.GrantServe(req, modelID) {
		s.inFlight[modelID]++
		st := s.modelState(modelID)
		st.starts = append(st.starts, s.now())
		return true
	}
	return false
}

// startSwap records the swap as active and launches it via Effects. The
// triggering waiters stay in the queue and are served on OnSwapDone.
func (s *FairShare) startSwap(modelID string, evict, running []string) {
	s.active[modelID] = &activeSwap{modelID: modelID, evict: evict}
	s.planner.OnSwapStart(modelID, running)
	s.effects.StartSwap(modelID, evict)
}

// enqueue parks a request, computing its base priority and arrival ordering.
func (s *FairShare) enqueue(req HandlerReq, priority int) {
	w := &fairWaiter{req: req, caller: callerOf(req), priority: priority, seq: s.seq, enqueuedAt: s.now()}
	s.seq++
	s.queued = append(s.queued, w)
	s.broadcastPositions()
}

// drainQueue repeatedly walks the models that have waiters, in priority order,
// serving what can be served until nothing changes. For each model it either
// admits waiters up to the concurrency limit (in admission order), starts a
// swap, or leaves the waiters parked.
func (s *FairShare) drainQueue() {
	for {
		progress := false
		for _, m := range s.modelsByPriority() {
			if s.drainModel(m) {
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	s.broadcastPositions()
}

// drainModel makes whatever progress it can for a single model, returning
// whether it changed any state.
func (s *FairShare) drainModel(m string) bool {
	if _, ok := s.active[m]; ok {
		return false // swap in flight; wait for OnSwapDone
	}
	state, ok := s.effects.ModelState(m)
	if !ok {
		s.errorModelWaiters(m, ErrModelNotFound)
		return true
	}
	running := s.runningSet(m)
	evict := s.planner.EvictionFor(m, running)

	if state == process.StateReady && len(evict) == 0 && !collidesWith(m, evict, s.active) {
		progressed := false
		for s.inFlight[m] < s.limit(m) {
			w := s.bestWaiterForModel(m)
			if w == nil {
				break
			}
			s.removeWaiter(w)
			s.commitProportion(m, w)
			s.grantHandler(w.req, m) // a false return means the caller left; still progress
			progressed = true
		}
		return progressed
	}

	if collidesWith(m, evict, s.active) || conflictsWithInFlight(evict, s.inFlight) {
		return false // would collide or evict a busy process; wait
	}
	if s.countWaiters(m) == 0 {
		return false
	}
	s.logger.Debugf("%s: starting swap for model %s, evicting %v", s.name, m, evict)
	s.startSwap(m, evict, running)
	return true
}

// boundedReject returns a BackpressureError if a new waiter for a saturated
// model should be shed (queue full or estimated wait beyond MaxWait), or nil if
// it may queue. Called only when the model is ready and at its limit.
func (s *FairShare) boundedReject(modelID string) *BackpressureError {
	waiting := s.countWaiters(modelID)
	inflight := s.inFlight[modelID]
	concurrency := s.limit(modelID)
	ahead := waiting + 1
	est := s.estimateWait(modelID, ahead)

	mk := func(reason string) *BackpressureError {
		return &BackpressureError{
			Model:       modelID,
			Reason:      reason,
			RetryAfter:  retryAfterSecs(est),
			Concurrency: concurrency,
			Inflight:    inflight,
			Waiting:     waiting,
		}
	}
	if s.cfg.MaxQueueDepth > 0 && waiting >= s.cfg.MaxQueueDepth {
		return mk("queue_full")
	}
	if s.cfg.MaxWait > 0 && est > s.cfg.MaxWait {
		return mk("max_wait")
	}
	return nil
}

// errorModelWaiters errors and removes every queued waiter for a model.
func (s *FairShare) errorModelWaiters(modelID string, err error) {
	kept := s.queued[:0]
	for _, w := range s.queued {
		if w.req.Model == modelID {
			s.effects.GrantError(w.req, err)
			continue
		}
		kept = append(kept, w)
	}
	s.queued = kept
}

// bestWaiterForModel returns the waiter for modelID that should be admitted
// next, or nil if none are queued.
func (s *FairShare) bestWaiterForModel(modelID string) *fairWaiter {
	order := s.admissionOrderForModel(modelID)
	if len(order) == 0 {
		return nil
	}
	return order[0]
}

// admissionOrderForModel returns modelID's queued waiters in the order they
// would be admitted. It does not mutate scheduler state, so it drives both the
// next-admit decision and the position display.
func (s *FairShare) admissionOrderForModel(modelID string) []*fairWaiter {
	var ws []*fairWaiter
	for _, w := range s.queued {
		if w.req.Model == modelID {
			ws = append(ws, w)
		}
	}
	if len(ws) <= 1 {
		return ws
	}
	// Hard interactive tier: every interactive waiter is admitted before any
	// non-interactive one, with the configured mode ordering applied within each
	// tier. When only one tier is present, order the whole set directly.
	var interactive, batch []*fairWaiter
	for _, w := range ws {
		if w.req.Interactive {
			interactive = append(interactive, w)
		} else {
			batch = append(batch, w)
		}
	}
	if len(interactive) > 0 && len(batch) > 0 {
		order := s.orderTier(modelID, interactive)
		return append(order, s.orderTier(modelID, batch)...)
	}
	return s.orderTier(modelID, ws)
}

// orderTier returns the admission order for a single tier of waiters using the
// configured mode (absolute priority+aging, or proportional weighted-fair).
func (s *FairShare) orderTier(modelID string, ws []*fairWaiter) []*fairWaiter {
	if len(ws) <= 1 {
		return ws
	}
	now := s.now()
	if s.cfg.ResolvedMode() == config.ModeProportion {
		return s.proportionOrder(modelID, ws, now)
	}
	sort.SliceStable(ws, func(i, j int) bool {
		ei, ej := s.effective(ws[i], now), s.effective(ws[j], now)
		if ei != ej {
			return ei > ej
		}
		return ws[i].seq < ws[j].seq
	})
	return ws
}

// proportionOrder simulates weighted-fair-queuing admission over the waiters on
// a clone of the model's virtual clock, returning admission order. Each caller's
// next request gets a virtual start of max(its finish, vtime); the smallest
// start is served next and that caller's finish advances by 1/weight, so
// higher-weight callers are served proportionally more often.
func (s *FairShare) proportionOrder(modelID string, ws []*fairWaiter, now time.Time) []*fairWaiter {
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].seq < ws[j].seq })

	st := s.modelState(modelID)
	finish := make(map[string]float64, len(st.classFinish))
	for k, v := range st.classFinish {
		finish[k] = v
	}
	vtime := st.vtime

	heads := map[string]int{}
	remaining := len(ws)
	order := make([]*fairWaiter, 0, len(ws))
	for remaining > 0 {
		var pick *fairWaiter
		var pickStart float64
		for i, w := range ws {
			if w == nil {
				continue
			}
			if idx, seen := heads[w.caller]; seen && idx != i {
				continue
			}
			start := finish[w.caller]
			if start < vtime {
				start = vtime
			}
			if pick == nil || start < pickStart || (start == pickStart && w.seq < pick.seq) {
				pick, pickStart = w, start
			}
		}
		weight := s.effective(pick, now)
		if weight < 1 {
			weight = 1
		}
		vtime = pickStart
		finish[pick.caller] = pickStart + 1.0/weight
		order = append(order, pick)
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

// commitProportion applies the real virtual-clock side effect of admitting w in
// proportion mode (no-op in absolute mode), mirroring proportionOrder's first
// step so the display and the actual admission agree.
func (s *FairShare) commitProportion(modelID string, w *fairWaiter) {
	if s.cfg.ResolvedMode() != config.ModeProportion {
		return
	}
	st := s.modelState(modelID)
	if st.classFinish == nil {
		st.classFinish = map[string]float64{}
	}
	start := st.classFinish[w.caller]
	if start < st.vtime {
		start = st.vtime
	}
	weight := s.effective(w, s.now())
	if weight < 1 {
		weight = 1
	}
	st.vtime = start
	st.classFinish[w.caller] = start + 1.0/weight
}

// effective returns a waiter's priority/weight with aging applied.
func (s *FairShare) effective(w *fairWaiter, now time.Time) float64 {
	p := float64(w.priority)
	if s.cfg.PriorityIncreasePerSecondWaiting > 0 {
		p += s.cfg.PriorityIncreasePerSecondWaiting * now.Sub(w.enqueuedAt).Seconds()
	}
	return p
}

// modelsByPriority returns the distinct models with queued waiters, ordered by
// the highest effective priority among each model's waiters (so the most urgent
// model gets first crack at swap capacity).
func (s *FairShare) modelsByPriority() []string {
	now := s.now()
	best := map[string]float64{}
	var models []string
	for _, w := range s.queued {
		e := s.effective(w, now)
		if cur, ok := best[w.req.Model]; !ok || e > cur {
			if !ok {
				models = append(models, w.req.Model)
			}
			best[w.req.Model] = e
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		return best[models[i]] > best[models[j]]
	})
	return models
}

// broadcastPositions sends every queued waiter its current 1-indexed position
// in its model's admission order.
func (s *FairShare) broadcastPositions() {
	seen := map[string]bool{}
	for _, w := range s.queued {
		if seen[w.req.Model] {
			continue
		}
		seen[w.req.Model] = true
		for i, ow := range s.admissionOrderForModel(w.req.Model) {
			sendPosition(ow.req.PositionCh, i+1)
		}
	}
}

// observeServiceTime folds the oldest outstanding grant's duration into the
// model's EWMA (alpha 0.25). FIFO-matched to grants, it is an approximation but
// good enough for a Retry-After hint.
func (s *FairShare) observeServiceTime(modelID string) {
	st := s.state[modelID]
	if st == nil || len(st.starts) == 0 {
		return
	}
	d := s.now().Sub(st.starts[0])
	st.starts = st.starts[1:]
	if d <= 0 {
		return
	}
	if st.ewma <= 0 {
		st.ewma = d
	} else {
		st.ewma = (d + 3*st.ewma) / 4
	}
}

// estimateWait returns ahead/limit service intervals at the model's current
// EWMA, with a 1s seed before any observation.
func (s *FairShare) estimateWait(modelID string, ahead int) time.Duration {
	ewma := time.Second
	if st := s.state[modelID]; st != nil && st.ewma > 0 {
		ewma = st.ewma
	}
	limit := s.limit(modelID)
	batches := (ahead + limit - 1) / limit
	if batches < 1 {
		batches = 1
	}
	return ewma * time.Duration(batches)
}

// resetIfIdle clears a model's WFQ virtual clock and EWMA grant tracking once it
// is fully idle, so shares are accounted within a contention episode and the
// float counters cannot drift without bound.
func (s *FairShare) resetIfIdle(modelID string) {
	if s.inFlight[modelID] != 0 || s.countWaiters(modelID) != 0 {
		return
	}
	if st := s.state[modelID]; st != nil {
		st.vtime = 0
		st.classFinish = nil
		st.starts = nil
	}
}

// runningSet is the live model set handed to the Swapper: every running process
// unioned with the targets of in-flight swaps, excluding the model being
// decided. Sorted for determinism.
func (s *FairShare) runningSet(excludeActive string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for id := range s.effects.RunningModels() {
		add(id)
	}
	for _, id := range activeTargets(s.active, excludeActive) {
		add(id)
	}
	sort.Strings(out)
	return out
}

func (s *FairShare) modelState(modelID string) *modelSchedState {
	st := s.state[modelID]
	if st == nil {
		st = &modelSchedState{classFinish: map[string]float64{}}
		s.state[modelID] = st
	}
	return st
}

func (s *FairShare) countWaiters(modelID string) int {
	n := 0
	for _, w := range s.queued {
		if w.req.Model == modelID {
			n++
		}
	}
	return n
}

func (s *FairShare) removeWaiter(target *fairWaiter) {
	for i, w := range s.queued {
		if w == target {
			s.queued = append(s.queued[:i], s.queued[i+1:]...)
			return
		}
	}
}

func (s *FairShare) limit(modelID string) int {
	if l, ok := s.limits[modelID]; ok && l > 0 {
		return l
	}
	return defaultConcurrencyLimit
}

// retryAfterSecs renders a duration as whole seconds (ceil), floored at 1.
func retryAfterSecs(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}

// sendPosition does a non-blocking latest-wins send: a full channel is drained
// and the newest value queued, so the scheduler never blocks on a slow consumer.
func sendPosition(ch chan int, pos int) {
	if ch == nil {
		return
	}
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
