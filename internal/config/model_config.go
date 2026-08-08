package config

import (
	"errors"
	"fmt"
	"runtime"
)

const (
	MODEL_CONFIG_DEFAULT_TTL   = -1
	MODEL_CONFIG_DEFAULT_PROXY = "http://localhost:${PORT}"
)

// websocketStrategy values. A websocket connection is a single HTTP request
// that stays open for as long as the client wants it, so by default it pins
// the model: llama-swap will not swap out a process that is still serving a
// request. These values let that behaviour be overridden per model.
const (
	// WEBSOCKET_STRATEGY_BLOCK counts websocket connections as ordinary
	// in-flight requests, so an open connection blocks swapping and TTL
	// unloading. This is the default.
	WEBSOCKET_STRATEGY_BLOCK = "block"

	// WEBSOCKET_STRATEGY_IGNORE excludes websocket connections from in-flight
	// accounting, so they never keep a model loaded.
	WEBSOCKET_STRATEGY_IGNORE = "ignore"
)

var validWebsocketStrategies = map[string]struct{}{
	WEBSOCKET_STRATEGY_BLOCK:  {},
	WEBSOCKET_STRATEGY_IGNORE: {},
}

var validModalities = map[string]struct{}{
	"text":  {},
	"audio": {},
	"image": {},
}

// ModelCapConfig defines what modalities and features a model supports.
// Used in /v1/models to inform clients. An empty block (all zero values) is
// treated as not configured.
type ModelCapConfig struct {
	In       []string `yaml:"in"`
	Out      []string `yaml:"out"`
	Tools    bool     `yaml:"tools"`
	Reranker bool     `yaml:"reranker"`
	Context  int      `yaml:"context"`
}

// Empty returns true when all fields are at their zero values.
func (c ModelCapConfig) Empty() bool {
	return len(c.In) == 0 && len(c.Out) == 0 && !c.Tools && !c.Reranker && c.Context == 0
}

// Validate checks that all modality values are recognized and context is
// non-negative. Returns an error if any value is invalid.
func (c ModelCapConfig) Validate() error {
	for _, m := range c.In {
		if _, ok := validModalities[m]; !ok {
			return fmt.Errorf("capabilities.in: invalid modality %q, must be one of: text, audio, image", m)
		}
	}
	for _, m := range c.Out {
		if _, ok := validModalities[m]; !ok {
			return fmt.Errorf("capabilities.out: invalid modality %q, must be one of: text, audio, image", m)
		}
	}
	if c.Context < 0 {
		return errors.New("capabilities.context: must be >= 0")
	}
	return nil
}

// TimeoutsConfig holds timeout settings for proxy connections
// 0 = no timeout
type TimeoutsConfig struct {
	Connect        int `yaml:"connect"`
	KeepAlive      int `yaml:"keepalive"`
	ResponseHeader int `yaml:"responseHeader"`
	TLSHandshake   int `yaml:"tlsHandshake"`
	ExpectContinue int `yaml:"expectContinue"`
	IdleConn       int `yaml:"idleConn"`
}

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	CmdStop       string   `yaml:"cmdStop"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	UnloadTimeout int      `yaml:"unloadTimeout"`
	Unlisted      bool     `yaml:"unlisted"`
	UseModelName  string   `yaml:"useModelName"`

	// #179 for /v1/models
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Limit concurrency of HTTP requests to process
	ConcurrencyLimit int `yaml:"concurrencyLimit"`

	// WebsocketStrategy controls whether websocket connections to this model
	// count as in-flight requests. See WEBSOCKET_STRATEGY_* above.
	WebsocketStrategy string `yaml:"websocketStrategy"`

	// Model filters see issue #174
	Filters ModelFilters `yaml:"filters"`

	// Macros: see #264
	// Model level macros take precedence over the global macros
	Macros MacroList `yaml:"macros"`

	// Metadata: see #264
	// Arbitrary metadata that can be exposed through the API
	Metadata map[string]any `yaml:"metadata"`

	// override global setting
	SendLoadingState *bool `yaml:"sendLoadingState"`

	// Timeout settings for proxy connections
	Timeouts TimeoutsConfig `yaml:"timeouts"`

	// Capabilities defines what modalities and features the model supports.
	Capabilities ModelCapConfig `yaml:"capabilities"`

	// Copy of HealthCheckTimeout from global config
	HealthCheckTimeout int `yaml:"healthCheckTimeout"`
}

func (m *ModelConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelConfig ModelConfig
	defaults := rawModelConfig{
		Cmd:              "",
		CmdStop:          "",
		Proxy:            MODEL_CONFIG_DEFAULT_PROXY,
		Aliases:          []string{},
		Env:              []string{},
		CheckEndpoint:    "/health",
		UnloadAfter:      MODEL_CONFIG_DEFAULT_TTL, // use GlobalTTL
		UnloadTimeout:    0,                        // use global UnloadTimeout
		Unlisted:         false,
		UseModelName:     "",
		ConcurrencyLimit: 0,
		Name:             "",
		Description:      "",

		WebsocketStrategy: WEBSOCKET_STRATEGY_BLOCK,

		// matches http.DefaultTransport
		Timeouts: TimeoutsConfig{
			Connect:        30,
			KeepAlive:      30,
			ResponseHeader: 0,
			TLSHandshake:   10,
			ExpectContinue: 1,
			IdleConn:       90,
		},
	}

	// the default cmdStop to taskkill /f /t /pid ${PID}
	if runtime.GOOS == "windows" {
		defaults.CmdStop = "taskkill /f /t /pid ${PID}"
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*m = ModelConfig(defaults)
	return nil
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

// ValidateWebsocketStrategy checks that websocketStrategy holds a recognized
// value. An empty value is accepted and behaves like the default, "block":
// configs parsed from YAML always get the default filled in, but ModelConfig
// values built directly in Go leave it zero.
func (m ModelConfig) ValidateWebsocketStrategy() error {
	if m.WebsocketStrategy == "" {
		return nil
	}
	if _, ok := validWebsocketStrategies[m.WebsocketStrategy]; !ok {
		return fmt.Errorf("websocketStrategy: invalid value %q, must be one of: block, ignore", m.WebsocketStrategy)
	}
	return nil
}

// IgnoresWebsockets reports whether websocket connections to this model should
// be left out of in-flight request accounting.
func (m ModelConfig) IgnoresWebsockets() bool {
	return m.WebsocketStrategy == WEBSOCKET_STRATEGY_IGNORE
}

// ModelFilters embeds Filters and adds legacy support for strip_params field
// See issue #174
type ModelFilters struct {
	Filters `yaml:",inline"`
}

func (m *ModelFilters) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelFilters ModelFilters
	defaults := rawModelFilters{}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	// Try to unmarshal with the old field name for backwards compatibility
	if defaults.StripParams == "" {
		var legacy struct {
			StripParams string `yaml:"strip_params"`
		}
		if legacyErr := unmarshal(&legacy); legacyErr != nil {
			return errors.New("failed to unmarshal legacy filters.strip_params: " + legacyErr.Error())
		}
		defaults.StripParams = legacy.StripParams
	}

	*m = ModelFilters(defaults)
	return nil
}

// SanitizedStripParams wraps Filters.SanitizedStripParams for backwards compatibility
// Returns ([]string, error) to match existing API
func (f ModelFilters) SanitizedStripParams() ([]string, error) {
	return f.Filters.SanitizedStripParams(), nil
}
