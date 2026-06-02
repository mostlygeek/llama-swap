package config

import (
	"strings"
	"testing"
	"time"
)

func TestScheduling_DisabledWhenAbsent(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ${PORT}
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Scheduling.Enabled() {
		t.Fatal("scheduling should be disabled when section is absent")
	}
}

func TestScheduling_EnabledWithDefaults(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ${PORT}
scheduling:
  priorities:
    bulk: 1
    interactive: 10
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Scheduling.Enabled() {
		t.Fatal("scheduling should be enabled when section is present")
	}
	if got := cfg.Scheduling.MaxWait; got != 30*time.Second {
		t.Fatalf("default maxWait = %v, want 30s", got)
	}
	if got := cfg.Scheduling.PriorityFor("interactive"); got != 10 {
		t.Fatalf("PriorityFor(interactive) = %d, want 10", got)
	}
	if got := cfg.Scheduling.PriorityFor("unknown"); got != defaultSchedulingPriority {
		t.Fatalf("PriorityFor(unknown) = %d, want default %d", got, defaultSchedulingPriority)
	}
}

func TestScheduling_CustomValues(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ${PORT}
scheduling:
  defaultPriority: 7
  maxWait: 5s
  maxQueueDepth: 4
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Scheduling.DefaultPriority != 7 {
		t.Fatalf("defaultPriority = %d, want 7", cfg.Scheduling.DefaultPriority)
	}
	if cfg.Scheduling.MaxWait != 5*time.Second {
		t.Fatalf("maxWait = %v, want 5s", cfg.Scheduling.MaxWait)
	}
	if cfg.Scheduling.MaxQueueDepth != 4 {
		t.Fatalf("maxQueueDepth = %d, want 4", cfg.Scheduling.MaxQueueDepth)
	}
}

func TestScheduling_RejectsNegativeMaxQueueDepth(t *testing.T) {
	_, err := LoadConfigFromReader(strings.NewReader(`
models:
  m1:
    cmd: echo ${PORT}
scheduling:
  maxQueueDepth: -1
`))
	if err == nil || !strings.Contains(err.Error(), "maxQueueDepth") {
		t.Fatalf("expected maxQueueDepth validation error, got %v", err)
	}
}
