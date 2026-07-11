package config

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MODEL_CONFIG_DEFAULT_TTL = -1
)

var validModalities = map[string]struct{}{
	"text":  {},
	"audio": {},
	"image": {},
}

var validReasoningEfforts = map[string]struct{}{
	"none":   {},
	"low":    {},
	"medium": {},
	"high":   {},
	"xhigh":  {},
}

// ModelReasoningConfig defines the reasoning modes and their token budgets.
// A non-empty Efforts map marks a model as supporting reasoning selection.
type ModelReasoningConfig struct {
	Default string         `yaml:"default"`
	Efforts map[string]int `yaml:"efforts"`
}

func (c ModelReasoningConfig) Enabled() bool {
	return len(c.Efforts) > 0
}

// ModelCapConfig defines what modalities and features a model supports.
// Used in /v1/models to inform clients. An empty block (all zero values) is
// treated as not configured.
//
// The custom UnmarshalYAML stores the raw YAML node so macro substitution
// can be applied later via ResolveMacros. After ResolveMacros is called the
// typed fields (In, Out, Tools, Reranker, Context, MaxOutputTokens, Reasoning) are populated.
type ModelCapConfig struct {
	In              []string             `yaml:"in"`
	Out             []string             `yaml:"out"`
	Tools           bool                 `yaml:"tools"`
	Reranker        bool                 `yaml:"reranker"`
	Context         int                  `yaml:"context"`
	MaxOutputTokens int                  `yaml:"max_output_tokens"`
	Reasoning       ModelReasoningConfig `yaml:"reasoning"`

	rawNode *yaml.Node
}

// UnmarshalYAML stores the raw YAML node so macro substitution via
// ResolveMacros can apply before the typed fields are decoded.
func (c *ModelCapConfig) UnmarshalYAML(value *yaml.Node) error {
	c.rawNode = value
	// Pre-populate typed fields for the common case (no macros).
	// If this succeeds we have valid data; if not (e.g. string in int field
	// because of a macro reference like "${default_ctx}"), we keep the raw
	// node and resolve after macro substitution.
	type rawCap ModelCapConfig
	if err := value.Decode((*rawCap)(c)); err != nil {
		// Reset fields to zero; macro resolution will re-decode.
		c.In = nil
		c.Out = nil
		c.Tools = false
		c.Reranker = false
		c.Context = 0
		c.MaxOutputTokens = 0
		c.Reasoning = ModelReasoningConfig{}
	}
	return nil
}

// ResolveMacros marshals the raw YAML node back to YAML, substitutes all
// macros (LIFO order matching LoadConfigFromReader), and re-decodes the
// result into the typed fields.
func (c *ModelCapConfig) ResolveMacros(macros MacroList) error {
	if c.rawNode == nil {
		return nil
	}
	if len(macros) == 0 {
		return c.Validate()
	}

	rawYAML, err := yaml.Marshal(c.rawNode)
	if err != nil {
		return fmt.Errorf("capabilities: failed to marshal raw node: %w", err)
	}

	s := string(rawYAML)

	// Substitute macros in LIFO order (same as LoadConfigFromReader).
	for i := len(macros) - 1; i >= 0; i-- {
		entry := macros[i]
		macroSlug := fmt.Sprintf("${%s}", entry.Name)
		macroStr := fmt.Sprintf("%v", entry.Value)
		s = strings.ReplaceAll(s, macroSlug, macroStr)
	}

	// Decode the substituted YAML back into the typed struct.
	*c = ModelCapConfig{}
	type rawCap ModelCapConfig
	if err := yaml.Unmarshal([]byte(s), (*rawCap)(c)); err != nil {
		return fmt.Errorf("capabilities: failed to decode after macro substitution: %w", err)
	}

	return c.Validate()
}

// Empty returns true when all fields are at their zero values.
func (c ModelCapConfig) Empty() bool {
	return len(c.In) == 0 && len(c.Out) == 0 && !c.Tools && !c.Reranker && c.Context == 0 && c.MaxOutputTokens == 0 && !c.Reasoning.Enabled() && c.Reasoning.Default == ""
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
	if c.MaxOutputTokens < 0 {
		return errors.New("capabilities.max_output_tokens: must be >= 0")
	}
	if c.Context > 0 && c.MaxOutputTokens > c.Context {
		return errors.New("capabilities.max_output_tokens: must be <= capabilities.context")
	}
	if !c.Reasoning.Enabled() {
		if c.Reasoning.Default != "" {
			return errors.New("capabilities.reasoning.default: requires capabilities.reasoning.efforts")
		}
		return nil
	}
	if c.MaxOutputTokens == 0 {
		return errors.New("capabilities.reasoning: requires capabilities.max_output_tokens > 0")
	}
	if _, ok := validReasoningEfforts[c.Reasoning.Default]; !ok {
		return fmt.Errorf("capabilities.reasoning.default: invalid effort %q", c.Reasoning.Default)
	}
	if _, ok := c.Reasoning.Efforts[c.Reasoning.Default]; !ok {
		return fmt.Errorf("capabilities.reasoning.default: %q is not configured in capabilities.reasoning.efforts", c.Reasoning.Default)
	}
	for effort, budget := range c.Reasoning.Efforts {
		if _, ok := validReasoningEfforts[effort]; !ok {
			return fmt.Errorf("capabilities.reasoning.efforts: invalid effort %q", effort)
		}
		if effort == "none" {
			if budget != 0 {
				return errors.New("capabilities.reasoning.efforts.none: must be 0")
			}
			continue
		}
		if budget <= 0 {
			return fmt.Errorf("capabilities.reasoning.efforts.%s: must be > 0", effort)
		}
		if budget >= c.MaxOutputTokens {
			return fmt.Errorf("capabilities.reasoning.efforts.%s: must be less than capabilities.max_output_tokens", effort)
		}
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
