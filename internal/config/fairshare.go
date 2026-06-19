package config

import (
	"fmt"
	"time"
)

// SchedulingMode selects how the priority number is interpreted under
// contention by the fairshare scheduler.
type SchedulingMode string

const (
	// ModeAbsolute serves the highest-priority waiter first (FIFO within a
	// priority). This is the default. With PriorityIncreasePerSecondWaiting > 0
	// it becomes starvation-free: a waiter's effective priority climbs the
	// longer it waits.
	ModeAbsolute SchedulingMode = "absolute"

	// ModeProportion treats the priority number as a weight and admits callers
	// in proportion to their weights (weighted fair queuing). A weight-10 caller
	// gets ~10x the throughput of a weight-1 caller under contention; an idle
	// class never holds back a busy one (work-conserving).
	ModeProportion SchedulingMode = "proportion"
)

// Defaults applied to a fairshare config when fields are left unset.
const (
	defaultFairSharePriority = 1
	defaultFairShareMaxWait  = 30 * time.Second
)

// FairShareConfig configures the "fairshare" scheduler (routing.scheduler.use:
// fairshare). It adds priority-aware fair admission in front of each model's
// concurrencyLimit: when a ready model is at its limit, over-limit requests are
// queued and admitted as slots free according to Mode — "absolute" (highest
// priority first, FIFO within a priority) or "proportion" (priority as a weight,
// weighted fair queuing). PriorityIncreasePerSecondWaiting ages a waiter's
// effective priority up over time in either mode. A request is rejected
// (429 + Retry-After) only when the queue is full or its estimated wait exceeds
// MaxWait; Retry-After is a measured EWMA of the model's service time.
type FairShareConfig struct {
	// Mode selects how the priority number is interpreted: "absolute" (highest
	// first) or "proportion" (weighted shares). Defaults to "absolute".
	Mode SchedulingMode `yaml:"mode"`

	// PriorityIncreasePerSecondWaiting adds to a waiter's effective priority for
	// every second it has waited, composing with either mode. 0 (default)
	// disables aging.
	PriorityIncreasePerSecondWaiting float64 `yaml:"priorityIncreasePerSecondWaiting"`

	// Priorities maps a caller id to a priority. The caller id is the API key
	// presented on the request (Authorization Bearer/Basic password, or
	// x-api-key). Higher priority is admitted first under contention.
	Priorities map[string]int `yaml:"priorities"`

	// ModelPriorities maps a model id to the default priority for that model. It
	// applies to callers absent from Priorities (e.g. a single client like Open
	// WebUI that can't be distinguished by API key), letting priority be driven
	// by which model is requested. Models absent here fall back to
	// DefaultPriority.
	ModelPriorities map[string]int `yaml:"modelPriorities"`

	// DefaultPriority is assigned to callers absent from both Priorities and
	// ModelPriorities (including unauthenticated callers). Defaults to 1.
	DefaultPriority int `yaml:"defaultPriority"`

	// MaxWait bounds how long a queued request may wait for a slot before it is
	// rejected with 429 + Retry-After. A request whose estimated wait already
	// exceeds MaxWait at arrival is rejected immediately. Defaults to 30s.
	MaxWait time.Duration `yaml:"maxWait"`

	// MaxQueueDepth bounds the number of waiters per model. Arrivals beyond this
	// are rejected with 429 + Retry-After. 0 (default) means unlimited depth
	// (only MaxWait bounds the queue).
	MaxQueueDepth int `yaml:"maxQueueDepth"`
}

// applyDefaults fills unset fields with their defaults. Called by
// LoadConfigFromReader when the fairshare scheduler is selected.
func (s *FairShareConfig) applyDefaults() {
	if s.DefaultPriority == 0 {
		s.DefaultPriority = defaultFairSharePriority
	}
	if s.MaxWait == 0 {
		s.MaxWait = defaultFairShareMaxWait
	}
	if s.Mode == "" {
		s.Mode = ModeAbsolute
	}
}

// ResolvedMode returns the configured mode, defaulting to ModeAbsolute.
func (s *FairShareConfig) ResolvedMode() SchedulingMode {
	if s.Mode == ModeProportion {
		return ModeProportion
	}
	return ModeAbsolute
}

// PriorityFor returns the priority for a request. An explicitly listed caller
// wins (operator intent), then the model's default, then DefaultPriority.
func (s *FairShareConfig) PriorityFor(callerID, modelID string) int {
	if p, ok := s.Priorities[callerID]; ok {
		return p
	}
	if p, ok := s.ModelPriorities[modelID]; ok {
		return p
	}
	return s.DefaultPriority
}

// Validate checks the fairshare values and returns an error if invalid.
func (s *FairShareConfig) Validate() error {
	if s.MaxWait < 0 {
		return fmt.Errorf("maxWait must be >= 0, got %v", s.MaxWait)
	}
	if s.MaxQueueDepth < 0 {
		return fmt.Errorf("maxQueueDepth must be >= 0, got %d", s.MaxQueueDepth)
	}
	if s.Mode != "" && s.Mode != ModeAbsolute && s.Mode != ModeProportion {
		return fmt.Errorf("mode must be %q or %q, got %q", ModeAbsolute, ModeProportion, s.Mode)
	}
	if s.PriorityIncreasePerSecondWaiting < 0 {
		return fmt.Errorf("priorityIncreasePerSecondWaiting must be >= 0, got %v", s.PriorityIncreasePerSecondWaiting)
	}
	return nil
}
