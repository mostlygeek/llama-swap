package config

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
)

const (
	MODEL_CONFIG_DEFAULT_TTL = -1
)

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
		UnloadAfter:      MODEL_CONFIG_DEFAULT_TTL, // use GlobalTTL
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
	return nil
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

// ContextSize extracts the effective per-request context size from the model's cmd arguments.
// It looks for --ctx-size / -c (llama.cpp) and --max-model-len (vLLM) flags.
// If --parallel / -np is also set, the context is divided by the parallel count
// since llama.cpp splits the KV cache across slots.
// Returns 0 if no context size is found or the value is not a valid positive integer.
// If specified multiple times, the last occurrence wins.
func (m *ModelConfig) ContextSize() int {
	args, err := SanitizeCommand(m.Cmd)
	if err != nil || len(args) == 0 {
		return 0
	}

	ctxSize := 0
	parallel := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--ctx-size" || arg == "-c" || arg == "--max-model-len":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					ctxSize = n
				}
				i++
			}
		case arg == "--parallel" || arg == "-np":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 1 {
					parallel = n
				}
				i++
			}
		case strings.HasPrefix(arg, "--ctx-size="):
			val := strings.TrimPrefix(arg, "--ctx-size=")
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				ctxSize = n
			}
		case strings.HasPrefix(arg, "--max-model-len="):
			val := strings.TrimPrefix(arg, "--max-model-len=")
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				ctxSize = n
			}
		case strings.HasPrefix(arg, "--parallel="):
			val := strings.TrimPrefix(arg, "--parallel=")
			if n, err := strconv.Atoi(val); err == nil && n > 1 {
				parallel = n
			}
		}
	}

	if ctxSize > 0 && parallel > 1 {
		ctxSize = ctxSize / parallel
	}

	return ctxSize
}

// SupportsVision checks if the model's cmd includes a multimodal projector flag.
// Returns true if --mmproj is found in the command arguments (llama.cpp vision support).
func (m *ModelConfig) SupportsVision() bool {
	args, err := SanitizeCommand(m.Cmd)
	if err != nil {
		return false
	}
	for _, arg := range args {
		if arg == "--mmproj" || strings.HasPrefix(arg, "--mmproj=") {
			return true
		}
	}
	return false
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
