package config

import (
	"fmt"
	"regexp"
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

// defaultGatedPaths matches the inference endpoints that should consume a
// fairshare slot: the OpenAI-compatible /v1 (and versionless /v) generation
// routes, the native llama.cpp /completion and /infill, bare /rerank[ing], and
// the stable-diffusion image routes. Everything else proxied under
// /upstream/<model>/ (the model's web UI, /props, static assets, favicon, model
// listings) is ungated so a busy model stays reachable.
const defaultGatedPaths = `^/(v1|v)/(chat/completions|responses|completions|messages|embeddings|rerank|reranking|audio|images)` +
	`|^/(completion|infill|reranking|rerank)\b` +
	`|^/sdapi/v1/(txt2img|img2img)`

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

	// GatedPaths is a regex matched against a request's model-relative path
	// (e.g. "/v1/chat/completions"). Only matching requests are subject to
	// fairshare admission (concurrency limit, queueing, 429 backpressure);
	// non-matching requests proxied under /upstream/<model>/ pass through
	// without consuming a slot, so a model's web UI and other lightweight
	// endpoints stay reachable while it is saturated with inference. Empty
	// (the default) compiles to defaultGatedPaths.
	GatedPaths string `yaml:"gatedPaths"`

	// gatedRe is the compiled GatedPaths, set by Validate. A nil value gates
	// every request (the pre-regex behavior), which is the safe default for a
	// config built directly in tests without going through LoadConfigFromReader.
	gatedRe *regexp.Regexp

	// InteractiveOrigins is a regex matched against a request's Origin header to
	// identify requests from an interactive browser UI. A request is treated as
	// interactive when it carries a Sec-Fetch-Mode header (browser-initiated; a
	// forbidden header that scripts cannot forge) AND its Origin matches this
	// regex. Empty (the default) matches any Origin, so any browser-initiated
	// request counts. Interactive requests are admitted ahead of all
	// non-interactive (batch/API) requests for the same model — a hard tier on
	// top of the priority/weight ordering.
	InteractiveOrigins string `yaml:"interactiveOrigins"`

	// interactiveRe is the compiled InteractiveOrigins, set by Validate.
	interactiveRe *regexp.Regexp
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
	if s.GatedPaths == "" {
		s.GatedPaths = defaultGatedPaths
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
	re, err := regexp.Compile(s.GatedPaths)
	if err != nil {
		return fmt.Errorf("gatedPaths is not a valid regex: %w", err)
	}
	s.gatedRe = re
	ire, err := regexp.Compile(s.InteractiveOrigins)
	if err != nil {
		return fmt.Errorf("interactiveOrigins is not a valid regex: %w", err)
	}
	s.interactiveRe = ire
	return nil
}

// Gated reports whether a request to the given model-relative path is subject to
// fairshare admission. A nil compiled regex (config not run through Validate,
// e.g. in tests) gates everything, preserving the pre-regex behavior.
func (s *FairShareConfig) Gated(path string) bool {
	if s.gatedRe == nil {
		return true
	}
	return s.gatedRe.MatchString(path)
}

// Interactive reports whether a request from an interactive browser UI should be
// admitted ahead of batch/API traffic. secFetchMode and origin are the request's
// Sec-Fetch-Mode and Origin header values. A request qualifies only when it is
// browser-initiated (Sec-Fetch-Mode present) and its Origin matches the
// configured InteractiveOrigins regex (empty regex matches any Origin).
func (s *FairShareConfig) Interactive(secFetchMode, origin string) bool {
	if secFetchMode == "" {
		return false
	}
	if s.interactiveRe == nil {
		return true
	}
	return s.interactiveRe.MatchString(origin)
}
