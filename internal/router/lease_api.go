package router

import (
	"fmt"
	"sort"
	"time"

	"github.com/mostlygeek/llama-swap/internal/process"
)

// ErrUnknownModel is returned when a lease operation names a model this router
// does not handle.
var ErrUnknownModel = fmt.Errorf("model not handled by this router")

// LoadVerdict is the read-only answer to "if I load this model now, what
// happens?" — the can-i-load preflight. It reports the structural eviction the
// planner would perform and any leases that would refuse it. Memory-budget
// admission is still evaluated at actual load time; a Fits==true verdict means
// "not blocked by a lease", not "guaranteed to fit under the GPU budget".
type LoadVerdict struct {
	Model      string    `json:"model"`
	Running    bool      `json:"running"`
	WouldEvict []string  `json:"would_evict"`
	Blocked    bool      `json:"blocked"`
	BlockedBy  []Blocker `json:"blocked_by,omitempty"`
}

// AcquireLease creates a lease protecting model from eviction. It rejects
// unknown models and models that are currently mid-eviction.
func (b *baseRouter) AcquireLease(model, holder, reason string, ttl time.Duration) (Lease, error) {
	if b.leases == nil {
		return Lease{}, fmt.Errorf("leases are disabled")
	}
	if !b.Handles(model) {
		return Lease{}, ErrUnknownModel
	}
	return b.leases.Acquire(model, holder, reason, ttl)
}

// ReleaseLease drops a lease by id. false means the id was unknown (already
// expired, killed, or never existed); clients treat that as "re-acquire".
func (b *baseRouter) ReleaseLease(id string) bool {
	if b.leases == nil {
		return false
	}
	return b.leases.Release(id)
}

// ExtendLease pushes a live lease's expiry out by ttl (clamped to the cap).
// false means the id was unknown or already expired.
func (b *baseRouter) ExtendLease(id string, ttl time.Duration) (Lease, bool) {
	if b.leases == nil {
		return Lease{}, false
	}
	return b.leases.Extend(id, ttl)
}

// KillLeases force-removes every live lease matching the selector (exactly one
// of id/model/holder). It is the deliberate operator override.
func (b *baseRouter) KillLeases(id, model, holder string) []Lease {
	if b.leases == nil {
		return nil
	}
	return b.leases.Kill(id, model, holder)
}

// ListLeases returns a point-in-time view of every live lease, each annotated
// with its model's active request count.
func (b *baseRouter) ListLeases() []LeaseView {
	if b.leases == nil {
		return nil
	}
	return b.leases.List(b.InFlight)
}

// CanLoad answers the can-i-load preflight for model: which models the planner
// would evict to make room, and whether any of those hold a live lease that
// would refuse the load. Read-only; it starts nothing.
func (b *baseRouter) CanLoad(model string) (LoadVerdict, error) {
	if !b.Handles(model) {
		return LoadVerdict{}, ErrUnknownModel
	}
	v := LoadVerdict{Model: model}

	running := make([]string, 0, len(b.processes))
	for id, p := range b.RunningModels() {
		running = append(running, id)
		if id == model && p == process.StateReady {
			v.Running = true
		}
	}
	sort.Strings(running)

	if b.planner != nil {
		v.WouldEvict = b.planner.EvictionFor(model, running)
	}
	if b.leases != nil {
		v.BlockedBy = b.leases.Blockers(v.WouldEvict)
		v.Blocked = len(v.BlockedBy) > 0
	}
	return v, nil
}
