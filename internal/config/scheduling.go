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
// per-model queue and admitted by caller priority (highest first, FIFO within a
// priority) as slots free. A request is rejected with 429 + Retry-After only
// when the queue is full or its estimated wait would exceed MaxWait. The
// Retry-After value is derived from a measured EWMA of the model's service time.
type SchedulingConfig struct {
	// Priorities maps a caller id to a priority. The caller id is the API key
	// presented on the request (Authorization Bearer/Basic password, or
	// x-api-key). Higher priority is admitted first under contention.
	Priorities map[string]int `yaml:"priorities"`

	// DefaultPriority is assigned to callers absent from Priorities (including
	// unauthenticated callers when no API keys are configured).
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

// PriorityFor returns the configured priority for a caller id, or
// DefaultPriority when the caller is not listed.
func (s *SchedulingConfig) PriorityFor(callerID string) int {
	if p, ok := s.Priorities[callerID]; ok {
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
	return nil
}
