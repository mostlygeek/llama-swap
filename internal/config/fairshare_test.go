package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFairShareConfig_PriorityFor(t *testing.T) {
	cfg := FairShareConfig{
		Priorities:      map[string]int{"sk-vip": 10},
		ModelPriorities: map[string]int{"chat": 7},
		DefaultPriority: 3,
	}
	// Explicit caller wins over everything.
	assert.Equal(t, 10, cfg.PriorityFor("sk-vip", "chat"))
	// Unlisted caller falls to the model default.
	assert.Equal(t, 7, cfg.PriorityFor("sk-other", "chat"))
	// Unlisted caller and model fall to the default.
	assert.Equal(t, 3, cfg.PriorityFor("sk-other", "embeddings"))
	// Anonymous caller resolves the same way.
	assert.Equal(t, 7, cfg.PriorityFor("", "chat"))
}

func TestFairShareConfig_Validate(t *testing.T) {
	assert.NoError(t, (&FairShareConfig{}).Validate())
	assert.Error(t, (&FairShareConfig{MaxWait: -1}).Validate())
	assert.Error(t, (&FairShareConfig{MaxQueueDepth: -1}).Validate())
	assert.Error(t, (&FairShareConfig{Mode: "bogus"}).Validate())
	assert.Error(t, (&FairShareConfig{PriorityIncreasePerSecondWaiting: -1}).Validate())
}

func TestFairShareConfig_ResolvedMode(t *testing.T) {
	assert.Equal(t, ModeAbsolute, (&FairShareConfig{}).ResolvedMode())
	assert.Equal(t, ModeAbsolute, (&FairShareConfig{Mode: ModeAbsolute}).ResolvedMode())
	assert.Equal(t, ModeProportion, (&FairShareConfig{Mode: ModeProportion}).ResolvedMode())
}

func TestLoadConfig_FairShareDefaults(t *testing.T) {
	content := `
models:
  chat:
    cmd: echo chat ${PORT}
routing:
  scheduler:
    use: fairshare
    settings:
      fairshare:
        modelPriorities:
          chat: 9
`
	config, err := LoadConfigFromReader(strings.NewReader(content))
	if !assert.NoError(t, err) {
		t.Fatalf("load: %v", err)
	}
	fs := config.Routing.Scheduler.Settings.FairShare
	assert.Equal(t, "fairshare", config.Routing.Scheduler.Use)
	assert.Equal(t, 1, fs.DefaultPriority)             // default applied
	assert.Equal(t, 30*time.Second, fs.MaxWait)        // default applied
	assert.Equal(t, ModeAbsolute, fs.ResolvedMode())   // default mode
	assert.Equal(t, 9, fs.PriorityFor("anon", "chat")) // model priority honored
}

func TestLoadConfig_FairShareUnknownModelPriority(t *testing.T) {
	content := `
models:
  chat:
    cmd: echo chat ${PORT}
routing:
  scheduler:
    use: fairshare
    settings:
      fairshare:
        modelPriorities:
          ghost: 9
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
}

func TestLoadConfig_UnknownScheduler(t *testing.T) {
	content := `
routing:
  scheduler:
    use: bogus
`
	_, err := LoadConfigFromReader(strings.NewReader(content))
	assert.Error(t, err)
}
