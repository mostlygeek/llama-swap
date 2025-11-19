package config

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// HTTPEndpoint represents a single HTTP endpoint configuration
type HTTPEndpoint struct {
	Endpoint string `yaml:"endpoint"` // URL path (e.g., "/wake_up")
	Method   string `yaml:"method"`   // HTTP method (GET, POST, PUT, PATCH)
	Body     string `yaml:"body"`     // Optional request body (JSON string)
	Timeout  int    `yaml:"timeout"`  // Optional per-endpoint timeout (seconds)
}

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	CmdStop       string   `yaml:"cmdStop"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	Unlisted      bool     `yaml:"unlisted"`
	UseModelName  string   `yaml:"useModelName"`

	// Array-based sleep/wake configuration
	SleepEndpoints []HTTPEndpoint `yaml:"sleepEndpoints"`
	WakeEndpoints  []HTTPEndpoint `yaml:"wakeEndpoints"`

	// #179 for /v1/models
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Limit concurrency of HTTP requests to process
	ConcurrencyLimit int `yaml:"concurrencyLimit"`

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
}

func (m *ModelConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelConfig ModelConfig
	defaults := rawModelConfig{
		Cmd:              "",
		CmdStop:          "",
		Proxy:            "http://localhost:${PORT}",
		Aliases:          []string{},
		Env:              []string{},
		CheckEndpoint:    "/health",
		UnloadAfter:      0,
		Unlisted:         false,
		UseModelName:     "",
		ConcurrencyLimit: 0,
		Name:             "",
		Description:      "",
	}

	// the default cmdStop to taskkill /f /t /pid ${PID}
	if runtime.GOOS == "windows" {
		defaults.CmdStop = "taskkill /f /t /pid ${PID}"
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*m = ModelConfig(defaults)

	// Validation: if one is set, both must be set
	hasSleep := len(m.SleepEndpoints) > 0
	hasWake := len(m.WakeEndpoints) > 0

	if hasSleep && !hasWake {
		return errors.New("wakeEndpoints required when sleepEndpoints is configured")
	}
	if hasWake && !hasSleep {
		return errors.New("sleepEndpoints required when wakeEndpoints is configured")
	}

	// Validate and normalize each endpoint
	for i := range m.SleepEndpoints {
		if err := m.validateEndpoint(&m.SleepEndpoints[i]); err != nil {
			return fmt.Errorf("sleepEndpoints[%d]: %v", i, err)
		}
	}

	for i := range m.WakeEndpoints {
		if err := m.validateEndpoint(&m.WakeEndpoints[i]); err != nil {
			return fmt.Errorf("wakeEndpoints[%d]: %v", i, err)
		}
	}

	return nil
}

func (m *ModelConfig) validateEndpoint(ep *HTTPEndpoint) error {
	// Endpoint path is required
	if ep.Endpoint == "" {
		return errors.New("endpoint path is required")
	}

	// Default method to POST if not specified
	if ep.Method == "" {
		ep.Method = "POST"
	}

	// Validate HTTP method
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true}
	upperMethod := strings.ToUpper(ep.Method)
	if !validMethods[upperMethod] {
		return fmt.Errorf("invalid method %q (must be GET, POST, PUT, or PATCH)", ep.Method)
	}
	ep.Method = upperMethod

	// Timeout validation (must be non-negative)
	if ep.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative, got %d", ep.Timeout)
	}

	return nil
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

// ModelFilters see issue #174
type ModelFilters struct {
	StripParams string `yaml:"stripParams"`
}

func (m *ModelFilters) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawModelFilters ModelFilters
	defaults := rawModelFilters{
		StripParams: "",
	}

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

func (f ModelFilters) SanitizedStripParams() ([]string, error) {
	if f.StripParams == "" {
		return nil, nil
	}

	params := strings.Split(f.StripParams, ",")
	cleaned := make([]string, 0, len(params))
	seen := make(map[string]bool)

	for _, param := range params {
		trimmed := strings.TrimSpace(param)
		if trimmed == "model" || trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}

	// sort cleaned
	slices.Sort(cleaned)
	return cleaned, nil
}
