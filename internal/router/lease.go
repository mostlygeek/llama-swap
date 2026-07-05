package router

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LeaseState is the lifecycle position of a lease.
type LeaseState string

const (
	// LeasePending is a lease whose model load has not yet been confirmed. It
	// still protects the model from eviction and counts as live. (Reserved for
	// the load-admission integration; client-created leases start active.)
	LeasePending LeaseState = "pending"
	// LeaseActive is a live lease protecting its model from eviction.
	LeaseActive LeaseState = "active"
)

// defaultLeaseSweep is how often the GC sweeper drops expired leases. Expiry is
// always evaluated directly against now() at read time, so the sweeper is
// garbage-collection only and never authoritative for a protection decision.
const defaultLeaseSweep = time.Minute

// Lease protects a single llama-swap model from eviction across a work session.
// Protection is refcounted per model: a model is protected while it has at least
// one live (non-expired) lease. A lease is broken only by the host-OOM hardline
// or an explicit kill; ordinary loads that would need to evict a leased model
// are refused, not allowed to break it.
type Lease struct {
	ID         string     `json:"id"`
	Model      string     `json:"model"`
	Holder     string     `json:"holder"`
	Reason     string     `json:"reason"`
	AcquiredAt time.Time  `json:"acquired_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	State      LeaseState `json:"state"`
}

// LeaseView is a lease plus derived, point-in-time fields for the API response.
type LeaseView struct {
	Lease
	ActiveRequests int   `json:"active_requests"`
	TTLRemainingMS int64 `json:"ttl_remaining_ms"`
}

// Blocker names one lease standing in the way of a load that would have evicted
// its model. It is the body of the refuse-don't-break rejection.
type Blocker struct {
	Model       string `json:"model"`
	Holder      string `json:"holder"`
	Reason      string `json:"reason"`
	ExpiresInMS int64  `json:"expires_in_ms"`
}

// leaseTable is the in-memory, mutex-guarded set of active leases plus the
// eviction claims that make lease protection race-free.
//
// It is consulted by both eviction paths — the scheduler's structural planning
// on the run loop, and the memGate's top-up eviction in a swap goroutine — to
// decide which models must not be evicted. Its mutex is a strict LEAF: nothing
// under it calls Stop, waits on a goroutine or the run loop, takes memGate.mu,
// or performs blocking I/O. Persistence snapshots are handed to a dedicated
// writer goroutine, so no disk write ever happens under the lock. This keeps the
// lock safe to take from the single-threaded run loop.
//
// Lock order: memGate.mu may be held while taking leaseTable.mu (the memGate
// top-up path claims a victim before stopping it). The reverse never happens.
type leaseTable struct {
	mu     sync.Mutex
	leases map[string]*Lease
	// evicting refcounts models currently being stopped for a swap. A lease
	// cannot be acquired on an eviction-claimed model, and a model with a live
	// lease cannot be claimed for eviction. This closes the check-then-stop
	// TOCTOU on both eviction paths: a claim and a lease are mutually exclusive.
	evicting    map[string]int
	maxDuration time.Duration
	now         func() time.Time

	persistCh chan []Lease
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func newLeaseTable(maxDuration time.Duration, persistPath string) *leaseTable {
	if maxDuration <= 0 {
		maxDuration = 4 * time.Hour
	}
	t := &leaseTable{
		leases:      make(map[string]*Lease),
		evicting:    make(map[string]int),
		maxDuration: maxDuration,
		now:         time.Now,
		stopCh:      make(chan struct{}),
	}
	if persistPath != "" {
		t.persistCh = make(chan []Lease, 1)
		go t.persistLoop(persistPath)
	}
	return t
}

// stop tears down the persistence writer. Safe to call more than once.
func (t *leaseTable) stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
}

// clampTTL bounds a requested TTL to (0, maxDuration]. A non-positive request
// is treated as the maximum, matching "protect until I say otherwise, capped".
func (t *leaseTable) clampTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 || ttl > t.maxDuration {
		return t.maxDuration
	}
	return ttl
}

// Acquire creates and returns a new active lease for model. It fails when the
// model is currently being evicted (the eviction already won the race); the
// client should retry, at which point the model is gone and reloadable. holder
// and reason are sanitized; the TTL is clamped to maxDuration.
func (t *leaseTable) Acquire(model, holder, reason string, ttl time.Duration) (Lease, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return Lease{}, fmt.Errorf("lease requires a model")
	}
	now := t.now()
	l := &Lease{
		ID:         newLeaseID(now),
		Model:      model,
		Holder:     sanitizeField(holder, 64),
		Reason:     sanitizeField(reason, 120),
		AcquiredAt: now,
		ExpiresAt:  now.Add(t.clampTTL(ttl)),
		State:      LeaseActive,
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.evicting[model] > 0 {
		return Lease{}, &evictingError{model: model}
	}
	t.leases[l.ID] = l
	t.saveLocked()
	return *l, nil
}

// Release removes the caller's own lease by id. It reports whether a lease was
// removed (false = unknown/already-gone id, which the client treats as "re-acquire").
func (t *leaseTable) Release(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.leases[id]; !ok {
		return false
	}
	delete(t.leases, id)
	t.saveLocked()
	return true
}

// Extend pushes a live lease's expiry to now+ttl (clamped). It never shortens
// below the existing expiry and cannot exceed the cap. An expired or unknown
// lease returns false, which the client treats as "re-acquire".
func (t *leaseTable) Extend(id string, ttl time.Duration) (Lease, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[id]
	if !ok || !t.liveLocked(l) {
		return Lease{}, false
	}
	newExpiry := t.now().Add(t.clampTTL(ttl))
	if newExpiry.After(l.ExpiresAt) {
		l.ExpiresAt = newExpiry
	}
	t.saveLocked()
	return *l, true
}

// Kill removes every live lease matching the selector (exactly one of id,
// model, or holder must be set) and returns what it removed. It is the
// deliberate operator override; nothing else breaks a lease short of host-OOM.
func (t *leaseTable) Kill(id, model, holder string) []Lease {
	t.mu.Lock()
	defer t.mu.Unlock()
	var killed []Lease
	for lid, l := range t.leases {
		match := (id != "" && lid == id) ||
			(model != "" && l.Model == model) ||
			(holder != "" && l.Holder == holder)
		if match {
			killed = append(killed, *l)
			delete(t.leases, lid)
		}
	}
	if len(killed) > 0 {
		t.saveLocked()
	}
	sort.Slice(killed, func(i, j int) bool { return killed[i].ID < killed[j].ID })
	return killed
}

// List returns a point-in-time view of every live lease, sorted by model then
// id. inFlight, when non-nil, supplies each model's active request count.
//
// inFlight is queried AFTER the lock is released: the production callback
// (baseRouter.InFlight) round-trips through the single run loop, and the run
// loop itself takes t.mu (via TryClaimEviction). Calling it under t.mu would
// deadlock the whole router. The lock is a strict leaf; nothing that waits on
// the run loop may run under it.
func (t *leaseTable) List(inFlight func(string) int) []LeaseView {
	t.mu.Lock()
	now := t.now()
	live := make([]Lease, 0, len(t.leases))
	for _, l := range t.leases {
		if l.ExpiresAt.After(now) {
			live = append(live, *l)
		}
	}
	t.mu.Unlock()

	out := make([]LeaseView, 0, len(live))
	for i := range live {
		l := live[i]
		reqs := 0
		if inFlight != nil {
			reqs = inFlight(l.Model)
		}
		out = append(out, LeaseView{
			Lease:          l,
			ActiveRequests: reqs,
			TTLRemainingMS: l.ExpiresAt.Sub(now).Milliseconds(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// TryClaimEviction atomically verifies that none of `models` holds a live lease
// and, if the set is clear, marks every model eviction-claimed. It returns the
// blocking leases when any model is protected (in which case NOTHING is
// claimed); an empty return means the caller now owns an eviction claim on the
// whole set and MUST call ReleaseEvictionClaim once the stops complete.
//
// An empty `models` claims nothing and always succeeds (the no-eviction case).
func (t *leaseTable) TryClaimEviction(models []string) []Blocker {
	if len(models) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()

	set := make(map[string]struct{}, len(models))
	for _, m := range models {
		set[m] = struct{}{}
	}
	var blockers []Blocker
	for _, l := range t.leases {
		if _, hit := set[l.Model]; !hit {
			continue
		}
		if !l.ExpiresAt.After(now) {
			continue
		}
		blockers = append(blockers, Blocker{
			Model:       l.Model,
			Holder:      l.Holder,
			Reason:      l.Reason,
			ExpiresInMS: l.ExpiresAt.Sub(now).Milliseconds(),
		})
	}
	if len(blockers) > 0 {
		sort.Slice(blockers, func(i, j int) bool {
			if blockers[i].Model != blockers[j].Model {
				return blockers[i].Model < blockers[j].Model
			}
			return blockers[i].Holder < blockers[j].Holder
		})
		return blockers
	}
	for _, m := range models {
		t.evicting[m]++
	}
	return nil
}

// Blockers returns, read-only, the live leases protecting any of `models`. It
// claims nothing; it exists to explain a rejection ("these leases are why the
// load could not free memory").
func (t *leaseTable) Blockers(models []string) []Blocker {
	if len(models) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(models))
	for _, m := range models {
		set[m] = struct{}{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var blockers []Blocker
	for _, l := range t.leases {
		if _, hit := set[l.Model]; !hit {
			continue
		}
		if !l.ExpiresAt.After(now) {
			continue
		}
		blockers = append(blockers, Blocker{
			Model:       l.Model,
			Holder:      l.Holder,
			Reason:      l.Reason,
			ExpiresInMS: l.ExpiresAt.Sub(now).Milliseconds(),
		})
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].Model != blockers[j].Model {
			return blockers[i].Model < blockers[j].Model
		}
		return blockers[i].Holder < blockers[j].Holder
	})
	return blockers
}

// Lookup returns the live lease with the given id. ok is false for an unknown
// or expired id.
func (t *leaseTable) Lookup(id string) (Lease, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[id]
	if !ok || !t.liveLocked(l) {
		return Lease{}, false
	}
	return *l, true
}

// IsProtected reports whether model has at least one live lease. It is the
// per-model exclusion used by memGate victim selection.
func (t *leaseTable) IsProtected(model string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, l := range t.leases {
		if l.Model == model && l.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// ReleaseEvictionClaim clears the claims placed by a successful
// TryClaimEviction. It must be called exactly once per successful claim, with
// the same set of models.
func (t *leaseTable) ReleaseEvictionClaim(models []string) {
	if len(models) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range models {
		if t.evicting[m] <= 1 {
			delete(t.evicting, m)
			continue
		}
		t.evicting[m]--
	}
}

// sweepExpired drops expired leases. Purely GC: protection decisions never rely
// on it having run.
func (t *leaseTable) sweepExpired() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	changed := false
	for id, l := range t.leases {
		if !l.ExpiresAt.After(now) {
			delete(t.leases, id)
			changed = true
		}
	}
	if changed {
		t.saveLocked()
	}
}

// runSweeper periodically GCs expired leases until stopped.
func (t *leaseTable) runSweeper(every time.Duration) {
	if every <= 0 {
		every = defaultLeaseSweep
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.sweepExpired()
		case <-t.stopCh:
			return
		}
	}
}

// liveLocked reports whether a lease is unexpired. Caller holds t.mu.
func (t *leaseTable) liveLocked(l *Lease) bool {
	return l.ExpiresAt.After(t.now())
}

// saveLocked hands the current lease set to the persistence writer. Caller holds
// t.mu. It builds the snapshot under the lock (cheap; the set is small) but does
// NOT touch the disk here — the write happens on the writer goroutine, so the
// lock is never held across I/O and the run loop can always take it promptly.
// A drain-and-replace send keeps only the latest snapshot; because every caller
// holds t.mu, sends are totally ordered and the writer converges on newest state.
func (t *leaseTable) saveLocked() {
	if t.persistCh == nil {
		return
	}
	snap := make([]Lease, 0, len(t.leases))
	for _, l := range t.leases {
		snap = append(snap, *l)
	}
	select {
	case t.persistCh <- snap:
	default:
		select {
		case <-t.persistCh:
		default:
		}
		select {
		case t.persistCh <- snap:
		default:
		}
	}
}

// persistLoop owns the state file: it is the only goroutine that writes it.
func (t *leaseTable) persistLoop(path string) {
	for {
		select {
		case snap := <-t.persistCh:
			writeLeaseFile(path, snap)
		case <-t.stopCh:
			// Flush a final pending snapshot if one is queued.
			select {
			case snap := <-t.persistCh:
				writeLeaseFile(path, snap)
			default:
			}
			return
		}
	}
}

// writeLeaseFile atomically replaces path with the JSON-encoded leases. Failures
// are silent: leases remain authoritative in memory, and persistence is a
// best-effort convenience for surviving restarts.
func writeLeaseFile(path string, leases []Lease) {
	data, err := json.Marshal(leases)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// loadAndReconcile reads a persisted table (if any) and re-populates the
// in-memory map, dropping entries that have already expired against the current
// wall clock. Persisted expiry is wall-clock (monotonic readings do not survive
// a restart), so reconciliation compares against time.Now directly. Unreadable
// or absent state is not an error: the table simply starts empty. Called once at
// startup, before the table serves any request.
func (t *leaseTable) loadAndReconcile(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var leases []Lease
	if err := json.Unmarshal(data, &leases); err != nil {
		return err
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range leases {
		l := leases[i]
		if !l.ExpiresAt.After(now) {
			continue
		}
		if l.State == LeasePending {
			// A pending lease's load never confirmed before the restart; do not
			// resurrect it as protection.
			continue
		}
		lc := l
		t.leases[lc.ID] = &lc
	}
	t.saveLocked()
	return nil
}

// LeaseBlockedError is the refuse-don't-break rejection: a load was refused
// because completing it would have evicted one or more lease-protected models.
// It renders as HTTP 503 with a blocked_by body naming the holding leases, so
// the client that tried to load learns exactly which lease stopped it.
type LeaseBlockedError struct {
	// Model is the model whose load was refused (comma-joined when the refusal
	// covered an eviction set).
	Model     string
	BlockedBy []Blocker
}

func (e *LeaseBlockedError) Error() string {
	holders := make([]string, 0, len(e.BlockedBy))
	for _, b := range e.BlockedBy {
		holders = append(holders, fmt.Sprintf("%s (%s: %s)", b.Model, b.Holder, b.Reason))
	}
	return fmt.Sprintf("loading %s refused: blocked by lease(s) on %s", e.Model, strings.Join(holders, ", "))
}

func (e *LeaseBlockedError) StatusCode() int { return http.StatusServiceUnavailable }

func (e *LeaseBlockedError) Header() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Retry-After", "10")
	return h
}

func (e *LeaseBlockedError) Body() []byte {
	b, _ := json.Marshal(map[string]any{
		"error":      e.Error(),
		"blocked_by": e.BlockedBy,
	})
	return b
}

// LeaseHeader is the optional request header linking a request to a lease. When
// present and naming a live lease for a DIFFERENT model than the request, the
// request is rejected as an obvious mismatch; an unknown/expired id is ignored
// (the client re-acquires on its own).
const LeaseHeader = "X-Llama-Swap-Lease"

// LeaseMismatchError is returned when an X-Llama-Swap-Lease header names a lease
// whose model does not match the request's model. It renders as HTTP 400.
type LeaseMismatchError struct {
	LeaseID      string
	LeaseModel   string
	RequestModel string
}

func (e *LeaseMismatchError) Error() string {
	return fmt.Sprintf("lease %s protects model %q but the request targets %q",
		e.LeaseID, e.LeaseModel, e.RequestModel)
}

func (e *LeaseMismatchError) StatusCode() int { return http.StatusBadRequest }

func (e *LeaseMismatchError) Header() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return h
}

func (e *LeaseMismatchError) Body() []byte {
	b, _ := json.Marshal(map[string]string{"error": e.Error()})
	return b
}

// evictingError is returned by Acquire when the model is mid-eviction.
type evictingError struct{ model string }

func (e *evictingError) Error() string {
	return fmt.Sprintf("model %q is being evicted; retry to re-acquire once it reloads", e.model)
}

// sanitizeField trims a user-supplied string to a single line and a max length.
func sanitizeField(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// crockford is the ULID base32 alphabet.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// leaseIDFallback seeds id randomness only if crypto/rand ever fails.
var leaseIDFallback atomic.Uint64

// newLeaseID returns a lexicographically sortable, unique id: a 48-bit
// millisecond timestamp followed by 80 bits of randomness, Crockford base32
// encoded (a ULID). Sortable-by-time makes tie-breaks and logs read naturally.
func newLeaseID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := io.ReadFull(rand.Reader, b[6:]); err != nil {
		// crypto/rand should never fail; if it does, fall back to a strictly
		// increasing counter mixed with the nanosecond clock so ids in the same
		// millisecond still differ rather than colliding on a zeroed suffix.
		n := leaseIDFallback.Add(1)
		mix := uint64(now.UnixNano()) ^ n
		for i := range 10 {
			b[6+i] = byte(mix >> (uint(i%8) * 8))
		}
	}

	var out [26]byte
	// Encode 128 bits as 26 base32 chars (Crockford), high bits first.
	out[0] = crockford[(b[0]&224)>>5]
	out[1] = crockford[b[0]&31]
	out[2] = crockford[(b[1]&248)>>3]
	out[3] = crockford[((b[1]&7)<<2)|((b[2]&192)>>6)]
	out[4] = crockford[(b[2]&62)>>1]
	out[5] = crockford[((b[2]&1)<<4)|((b[3]&240)>>4)]
	out[6] = crockford[((b[3]&15)<<1)|((b[4]&128)>>7)]
	out[7] = crockford[(b[4]&124)>>2]
	out[8] = crockford[((b[4]&3)<<3)|((b[5]&224)>>5)]
	out[9] = crockford[b[5]&31]
	out[10] = crockford[(b[6]&248)>>3]
	out[11] = crockford[((b[6]&7)<<2)|((b[7]&192)>>6)]
	out[12] = crockford[(b[7]&62)>>1]
	out[13] = crockford[((b[7]&1)<<4)|((b[8]&240)>>4)]
	out[14] = crockford[((b[8]&15)<<1)|((b[9]&128)>>7)]
	out[15] = crockford[(b[9]&124)>>2]
	out[16] = crockford[((b[9]&3)<<3)|((b[10]&224)>>5)]
	out[17] = crockford[b[10]&31]
	out[18] = crockford[(b[11]&248)>>3]
	out[19] = crockford[((b[11]&7)<<2)|((b[12]&192)>>6)]
	out[20] = crockford[(b[12]&62)>>1]
	out[21] = crockford[((b[12]&1)<<4)|((b[13]&240)>>4)]
	out[22] = crockford[((b[13]&15)<<1)|((b[14]&128)>>7)]
	out[23] = crockford[(b[14]&124)>>2]
	out[24] = crockford[((b[14]&3)<<3)|((b[15]&224)>>5)]
	out[25] = crockford[b[15]&31]
	return string(out[:])
}
