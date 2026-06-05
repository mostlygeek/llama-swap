package config

import (
	"fmt"
	"time"
)

// SchedulingConfig enables priority-aware fair admission in front of each
// model's concurrency limit. It is opt-in: when the top-level `scheduling`
// section is absent, llama-swap keeps its original behavior (a request that
// cannot immediately acquire a concurrency slot is rejected with a bare 429).
//
// When enabled, a request that cannot immediately acquire a slot is held in a
// per-model queue and admitted as slots free according to Mode: "absolute"
// (highest priority first, FIFO within a priority) or "proportion" (priority as
// a weight, weighted fair queuing). PriorityIncreasePerSecondWaiting optionally
// ages a waiter's effective priority up over time in either mode. A request is
// rejected with 429 + Retry-After only when the queue is full or its estimated
// wait would exceed MaxWait; the Retry-After is a measured EWMA of service time.
// SchedulingMode selects how the priority number is interpreted under
// contention.
type SchedulingMode string

const (
	// ModeAbsolute serves the highest-priority waiter first (FIFO within a
	// priority). This is the default and matches the original behavior. With
	// PriorityIncreasePerSecondWaiting > 0 it becomes starvation-free: a
	// waiter's effective priority climbs the longer it waits.
	ModeAbsolute SchedulingMode = "absolute"

	// ModeProportion treats the priority number as a weight and admits callers
	// in proportion to their weights (weighted fair queuing). A weight-10 caller
	// gets ~10x the throughput of a weight-1 caller under contention; an idle
	// class never holds back a busy one (work-conserving).
	ModeProportion SchedulingMode = "proportion"
)

type SchedulingConfig struct {
	// Mode selects how the priority number is interpreted: "absolute" (highest
	// first) or "proportion" (weighted shares). Defaults to "absolute".
	Mode SchedulingMode `yaml:"mode"`

	// PriorityIncreasePerSecondWaiting adds to a waiter's effective priority for
	// every second it has waited, composing with either mode. 0 (default)
	// disables aging. In absolute mode it prevents starvation (low priority
	// eventually overtakes); in proportion mode it grows a long-waiter's share.
	PriorityIncreasePerSecondWaiting float64 `yaml:"priorityIncreasePerSecondWaiting"`

	// Priorities maps a caller id to a priority. The caller id is the API key
	// presented on the request (Authorization Bearer/Basic password, or
	// x-api-key). Higher priority is admitted first under contention.
	Priorities map[string]int `yaml:"priorities"`

	// ModelPriorities maps a model id to the default priority for that model.
	// It applies to callers absent from Priorities (e.g. a single client like
	// Open WebUI that can't be distinguished by API key), letting priority be
	// driven by which model is requested. Models absent here fall back to
	// DefaultPriority.
	ModelPriorities map[string]int `yaml:"modelPriorities"`

	// DefaultPriority is assigned to callers absent from both Priorities and
	// ModelPriorities (including unauthenticated callers when no API keys are
	// configured).
	DefaultPriority int `yaml:"defaultPriority"`

	// MaxWait bounds how long a queued request may wait for a slot before it is
	// rejected with 429 + Retry-After. A request whose estimated wait already
	// exceeds MaxWait at arrival is rejected immediately.
	MaxWait time.Duration `yaml:"maxWait"`

	// MaxQueueDepth bounds the number of waiters per model. Arrivals beyond this
	// are rejected with 429 + Retry-After. 0 means unlimited depth (only MaxWait
	// bounds the queue).
	MaxQueueDepth int `yaml:"maxQueueDepth"`

	// enabled records whether the section was present in the YAML, so the
	// middleware can preserve legacy behavior when it was not.
	enabled bool
}

// Defaults applied when the scheduling section is present but leaves a field unset.
const (
	defaultSchedulingPriority = 1
	defaultSchedulingMaxWait  = 30 * time.Second
)

func (s *SchedulingConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawSchedulingConfig SchedulingConfig
	raw := rawSchedulingConfig{
		DefaultPriority: defaultSchedulingPriority,
		MaxWait:         defaultSchedulingMaxWait,
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*s = SchedulingConfig(raw)
	s.enabled = true
	return nil
}

// Enabled reports whether the scheduling section was configured.
func (s *SchedulingConfig) Enabled() bool {
	return s != nil && s.enabled
}

// ResolvedMode returns the configured mode, defaulting to ModeAbsolute.
func (s *SchedulingConfig) ResolvedMode() SchedulingMode {
	if s.Mode == ModeProportion {
		return ModeProportion
	}
	return ModeAbsolute
}

// PriorityFor returns the priority for a request. An explicitly listed caller
// wins (operator intent), then the model's default, then DefaultPriority.
func (s *SchedulingConfig) PriorityFor(callerID, modelID string) int {
	if p, ok := s.Priorities[callerID]; ok {
		return p
	}
	if p, ok := s.ModelPriorities[modelID]; ok {
		return p
	}
	return s.DefaultPriority
}

// Validate checks the scheduling values and returns an error if invalid.
func (s *SchedulingConfig) Validate() error {
	if !s.Enabled() {
		return nil
	}
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
