package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilters_SanitizedStripParams(t *testing.T) {
	tests := []struct {
		name        string
		stripParams string
		want        []string
	}{
		{
			name:        "empty string",
			stripParams: "",
			want:        nil,
		},
		{
			name:        "single param",
			stripParams: "temperature",
			want:        []string{"temperature"},
		},
		{
			name:        "multiple params",
			stripParams: "temperature, top_p, top_k",
			want:        []string{"temperature", "top_k", "top_p"}, // sorted
		},
		{
			name:        "model param filtered",
			stripParams: "model, temperature, top_p",
			want:        []string{"temperature", "top_p"},
		},
		{
			name:        "only model param",
			stripParams: "model",
			want:        nil,
		},
		{
			name:        "duplicates removed",
			stripParams: "temperature, top_p, temperature",
			want:        []string{"temperature", "top_p"},
		},
		{
			name:        "extra whitespace",
			stripParams: "  temperature  ,  top_p  ",
			want:        []string{"temperature", "top_p"},
		},
		{
			name:        "empty values filtered",
			stripParams: "temperature,,top_p,",
			want:        []string{"temperature", "top_p"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filters{StripParams: tt.stripParams}
			got := f.SanitizedStripParams()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilters_SanitizedSetParams(t *testing.T) {
	tests := []struct {
		name       string
		setParams  map[string]any
		wantParams map[string]any
		wantKeys   []string
	}{
		{
			name:       "empty setParams",
			setParams:  nil,
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name:       "empty map",
			setParams:  map[string]any{},
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name: "normal params",
			setParams: map[string]any{
				"temperature": 0.7,
				"top_p":       0.9,
			},
			wantParams: map[string]any{
				"temperature": 0.7,
				"top_p":       0.9,
			},
			wantKeys: []string{"temperature", "top_p"},
		},
		{
			name: "protected model param filtered",
			setParams: map[string]any{
				"model":       "should-be-filtered",
				"temperature": 0.7,
			},
			wantParams: map[string]any{
				"temperature": 0.7,
			},
			wantKeys: []string{"temperature"},
		},
		{
			name: "only protected param",
			setParams: map[string]any{
				"model": "should-be-filtered",
			},
			wantParams: nil,
			wantKeys:   nil,
		},
		{
			name: "complex nested values",
			setParams: map[string]any{
				"provider": map[string]any{
					"data_collection": "deny",
					"allow_fallbacks": false,
				},
				"transforms": []string{"middle-out"},
			},
			wantParams: map[string]any{
				"provider": map[string]any{
					"data_collection": "deny",
					"allow_fallbacks": false,
				},
				"transforms": []string{"middle-out"},
			},
			wantKeys: []string{"provider", "transforms"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filters{SetParams: tt.setParams}
			gotParams, gotKeys := f.SanitizedSetParams()

			assert.Equal(t, len(tt.wantKeys), len(gotKeys), "keys length mismatch")
			for i, key := range gotKeys {
				assert.Equal(t, tt.wantKeys[i], key, "key mismatch at %d", i)
			}

			if tt.wantParams == nil {
				assert.Nil(t, gotParams, "expected nil params")
				return
			}

			assert.Equal(t, len(tt.wantParams), len(gotParams), "params length mismatch")
			for key, wantValue := range tt.wantParams {
				gotValue, exists := gotParams[key]
				assert.True(t, exists, "missing key: %s", key)
				// Simple comparison for basic types
				switch v := wantValue.(type) {
				case string, int, float64, bool:
					assert.Equal(t, v, gotValue, "value mismatch for key %s", key)
				}
			}
		})
	}
}

func TestFilters_SanitizedSetParamsByID(t *testing.T) {
	tests := []struct {
		name             string
		setParamsByID    map[string]map[string]any
		requestedModelID string
		wantParams       map[string]any
		wantKeys         []string
	}{
		{
			name:             "empty SetParamsByID returns nil",
			setParamsByID:    nil,
			requestedModelID: "model1",
			wantParams:       nil,
			wantKeys:         nil,
		},
		{
			name:             "empty map returns nil",
			setParamsByID:    map[string]map[string]any{},
			requestedModelID: "model1",
			wantParams:       nil,
			wantKeys:         nil,
		},
		{
			name: "non-matching model ID returns nil",
			setParamsByID: map[string]map[string]any{
				"model2": {"temperature": 0.9},
			},
			requestedModelID: "model1",
			wantParams:       nil,
			wantKeys:         nil,
		},
		{
			name: "matching model ID returns correct params",
			setParamsByID: map[string]map[string]any{
				"model1": {"temperature": 0.7, "top_p": 0.9},
				"model2": {"temperature": 0.5},
			},
			requestedModelID: "model1",
			wantParams: map[string]any{
				"temperature": 0.7,
				"top_p":       0.9,
			},
			wantKeys: []string{"temperature", "top_p"},
		},
		{
			name: "protected param model is filtered out",
			setParamsByID: map[string]map[string]any{
				"model1": {
					"model":       "should-be-filtered",
					"temperature": 0.7,
				},
			},
			requestedModelID: "model1",
			wantParams: map[string]any{
				"temperature": 0.7,
			},
			wantKeys: []string{"temperature"},
		},
		{
			name: "only protected param returns nil",
			setParamsByID: map[string]map[string]any{
				"model1": {
					"model": "should-be-filtered",
				},
			},
			requestedModelID: "model1",
			wantParams:       nil,
			wantKeys:         nil,
		},
		{
			name: "keys are sorted",
			setParamsByID: map[string]map[string]any{
				"model1": {
					"z_param": "z",
					"a_param": "a",
					"m_param": "m",
				},
			},
			requestedModelID: "model1",
			wantParams: map[string]any{
				"z_param": "z",
				"a_param": "a",
				"m_param": "m",
			},
			wantKeys: []string{"a_param", "m_param", "z_param"},
		},
		{
			name: "alias style key lookup",
			setParamsByID: map[string]map[string]any{
				"model1:high": {"reasoning_effort": "high"},
				"model1:low":  {"reasoning_effort": "low"},
			},
			requestedModelID: "model1:high",
			wantParams: map[string]any{
				"reasoning_effort": "high",
			},
			wantKeys: []string{"reasoning_effort"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Filters{SetParamsByID: tt.setParamsByID}
			gotParams, gotKeys := f.SanitizedSetParamsByID(tt.requestedModelID)

			if tt.wantParams == nil {
				assert.Nil(t, gotParams)
				assert.Nil(t, gotKeys)
				return
			}

			assert.Equal(t, tt.wantKeys, gotKeys)
			assert.Equal(t, tt.wantParams, gotParams)
		})
	}
}

func TestProtectedParams(t *testing.T) {
	// Verify that "model" is protected
	assert.Contains(t, ProtectedParams, "model")
}

func TestFilters_ReasoningInputField(t *testing.T) {
	assert.Equal(t, "", Filters{}.ReasoningInputField())
	assert.Equal(t, "reasoning_effort", Filters{Reasoning: &ReasoningFilter{}}.ReasoningInputField())
	assert.Equal(t, "effort", Filters{Reasoning: &ReasoningFilter{InputField: "effort"}}.ReasoningInputField())
}

func TestReasoningFilter_PresetFor(t *testing.T) {
	enabled := true
	rf := &ReasoningFilter{
		Presets: map[string]ReasoningPreset{
			"max": {EnableThinking: &enabled},
		},
	}

	preset, found := rf.PresetFor("max")
	assert.True(t, found)
	assert.Equal(t, &enabled, preset.EnableThinking)
	assert.Nil(t, preset.BudgetTokens)

	_, found = rf.PresetFor("unknown")
	assert.False(t, found)
}

func TestReasoningFilter_Validate(t *testing.T) {
	enabled := true
	budget := 8192
	zero := 0
	validPresets := map[string]ReasoningPreset{
		"medium": {EnableThinking: &enabled, BudgetTokens: &budget},
	}

	tests := []struct {
		name    string
		filter  ReasoningFilter
		wantErr string
	}{
		{
			name:   "valid with default inputField",
			filter: ReasoningFilter{Presets: validPresets},
		},
		{
			name:   "valid with custom inputField",
			filter: ReasoningFilter{InputField: "effort", Presets: validPresets},
		},
		{
			name:    "protected inputField",
			filter:  ReasoningFilter{InputField: "model", Presets: validPresets},
			wantErr: "protected",
		},
		{
			name:    "dotted inputField",
			filter:  ReasoningFilter{InputField: "reasoning.effort", Presets: validPresets},
			wantErr: "must contain only letters, digits, underscores, or hyphens",
		},
		{
			name:    "wildcard inputField",
			filter:  ReasoningFilter{InputField: "re*", Presets: validPresets},
			wantErr: "must contain only letters, digits, underscores, or hyphens",
		},
		{
			name:    "pipe inputField",
			filter:  ReasoningFilter{InputField: "a|b", Presets: validPresets},
			wantErr: "must contain only letters, digits, underscores, or hyphens",
		},
		{
			name:    "empty presets",
			filter:  ReasoningFilter{},
			wantErr: "presets must not be empty",
		},
		{
			name: "preset with no fields",
			filter: ReasoningFilter{Presets: map[string]ReasoningPreset{
				"medium": {},
			}},
			wantErr: "must set enableThinking and/or budgetTokens",
		},
		{
			name: "non-positive budgetTokens",
			filter: ReasoningFilter{Presets: map[string]ReasoningPreset{
				"medium": {BudgetTokens: &zero},
			}},
			wantErr: "greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}
