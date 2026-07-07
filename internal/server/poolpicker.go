package server

import (
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// failureQuarantine is how long a member whose pick ended in a server error
// is excluded from cold-spill candidates (as long as alternatives exist).
// Protects against a broken member (e.g. its pinned GPU is occupied) turning
// into a spill blackhole of immediate 500s.
const failureQuarantine = 15 * time.Second

// poolPicker resolves a pool name to one of its member model IDs.
//
// Selection is least-busy with round-robin tie-breaking, plus dynamic
// scale-out: while the least-busy READY member is below the pool's spillover
// threshold it receives the request; otherwise a cold or starting member is
// picked so it gets warmed (scale-in is the members' ttl). Members in
// StateStopping are never picked: dispatching into the stop window would
// wedge the upstream swap (Stop does not notify WaitReady waiters).
//
// Pick RESERVES the returned member (in-flight++) under the same mutex hold
// that inspected the counters, so concurrent bursts cannot all pick the same
// member off a stale snapshot. Callers must pair every successful Pick with
// Release. Direct requests addressed to a member's own model ID are counted
// via Acquire/Release so pool balancing sees them too.
type poolPicker struct {
	pools    map[string]*poolState
	memberOf map[string]string // member model ID -> pool name
}

type poolState struct {
	cfg config.PoolConfig

	mu          sync.Mutex
	rr          uint64
	inflight    map[string]int
	lastFailure map[string]time.Time
}

// newPoolPicker initialises the picker from the parsed config. It assumes
// pools have already been validated by config.LoadConfigFromReader.
func newPoolPicker(cfg config.Config) *poolPicker {
	pools := make(map[string]*poolState, len(cfg.Pools))
	memberOf := make(map[string]string)
	for poolID, poolCfg := range cfg.Pools {
		pools[poolID] = &poolState{
			cfg:         poolCfg,
			inflight:    make(map[string]int, len(poolCfg.Members)),
			lastFailure: make(map[string]time.Time, len(poolCfg.Members)),
		}
		for _, m := range poolCfg.Members {
			memberOf[m] = poolID
		}
	}
	return &poolPicker{pools: pools, memberOf: memberOf}
}

// IsPool reports whether name is a configured pool.
func (p *poolPicker) IsPool(name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.pools[name]
	return ok
}

// MemberPool returns the pool that modelID is a member of, if any.
func (p *poolPicker) MemberPool(modelID string) (string, bool) {
	if p == nil {
		return "", false
	}
	pool, ok := p.memberOf[modelID]
	return pool, ok
}

// Acquire counts a request dispatched to member outside Pick (direct requests
// addressed to the member's own model ID). Pair with Release.
func (p *poolPicker) Acquire(name, member string) {
	if ps := p.state(name); ps != nil {
		ps.mu.Lock()
		ps.inflight[member]++
		ps.mu.Unlock()
	}
}

// Release records the completion of a request counted by Pick or Acquire.
func (p *poolPicker) Release(name, member string) {
	if ps := p.state(name); ps != nil {
		ps.mu.Lock()
		if ps.inflight[member] > 0 {
			ps.inflight[member]--
		}
		ps.mu.Unlock()
	}
}

// NoteFailure quarantines member as a cold-spill target for
// failureQuarantine. Called when a pool-picked request ends in a server
// error; only cold members are affected by quarantine, so a ready member
// returning errors keeps serving (and draining) normally.
func (p *poolPicker) NoteFailure(name, member string) {
	if ps := p.state(name); ps != nil {
		ps.mu.Lock()
		ps.lastFailure[member] = time.Now()
		ps.mu.Unlock()
	}
}

func (p *poolPicker) state(name string) *poolState {
	if p == nil {
		return nil
	}
	return p.pools[name]
}

// Pick returns one member of the named pool and reserves one in-flight slot
// on it. ok is false when name is not a pool. Callers must call Release with
// the returned member when the request completes.
func (p *poolPicker) Pick(name string, running map[string]process.ProcessState) (string, bool) {
	ps := p.state(name)
	if ps == nil || len(ps.cfg.Members) == 0 {
		return "", false
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	ready, spill := ps.classify(running)

	if len(ready) > 0 {
		if ps.minInflight(ready) < ps.cfg.Spillover || len(spill) == 0 {
			return ps.take(ready), true
		}
	}
	if len(spill) > 0 {
		return ps.take(spill), true
	}
	if len(ready) > 0 {
		return ps.take(ready), true
	}
	// Last resort (e.g. every member mid-stop): route to the least-busy
	// member anyway rather than failing the request.
	return ps.take(ps.cfg.Members), true
}

// classify splits members into ready and spill candidates. Stopping members
// are excluded entirely; cold members under failure quarantine are used only
// when no other spill candidate exists. Caller holds ps.mu.
func (ps *poolState) classify(running map[string]process.ProcessState) (ready, spill []string) {
	now := time.Now()
	var quarantined []string
	for _, m := range ps.cfg.Members {
		st, exists := running[m]
		switch {
		case exists && st == process.StateReady:
			ready = append(ready, m)
		case exists && (st == process.StateStopping || st == process.StateShutdown):
			// draining: never route new work into the stop window
		case exists && st == process.StateStarting:
			spill = append(spill, m)
		default: // stopped or unknown = cold
			if t, ok := ps.lastFailure[m]; ok && now.Sub(t) < failureQuarantine {
				quarantined = append(quarantined, m)
			} else {
				spill = append(spill, m)
			}
		}
	}
	if len(spill) == 0 {
		spill = quarantined
	}
	return ready, spill
}

// take picks the least-busy candidate (ties broken round-robin), reserves
// one in-flight slot on it and returns it. Caller holds ps.mu.
func (ps *poolState) take(candidates []string) string {
	min := ps.minInflight(candidates)
	tied := make([]string, 0, len(candidates))
	for _, m := range candidates {
		if ps.inflight[m] == min {
			tied = append(tied, m)
		}
	}
	idx := ps.rr
	ps.rr++
	m := tied[idx%uint64(len(tied))]
	ps.inflight[m]++
	return m
}

// minInflight returns the smallest in-flight count among candidates. It does
// not advance the round-robin counter. Caller holds ps.mu.
func (ps *poolState) minInflight(candidates []string) int {
	min := -1
	for _, m := range candidates {
		if n := ps.inflight[m]; min < 0 || n < min {
			min = n
		}
	}
	return min
}
