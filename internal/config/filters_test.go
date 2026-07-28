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

func TestFilters_MatchRuleValidate(t *testing.T) {
	validSet := map[string]any{"top_p": 0.9}

	tests := []struct {
		name    string
		rule    MatchRule
		wantErr string
	}{
		{
			name: "valid rule",
			rule: MatchRule{Key: "reasoning_effort", Match: "high", Set: validSet},
		},
		{
			name:    "empty key",
			rule:    MatchRule{Match: "high", Set: validSet},
			wantErr: "key must not be empty",
		},
		{
			name:    "protected key",
			rule:    MatchRule{Key: "model", Match: "x", Set: validSet},
			wantErr: "is a protected parameter",
		},
		{
			name:    "dotted key",
			rule:    MatchRule{Key: "a.b", Match: "x", Set: validSet},
			wantErr: "must contain only letters",
		},
		{
			name:    "wildcard key",
			rule:    MatchRule{Key: "a*", Match: "x", Set: validSet},
			wantErr: "must contain only letters",
		},
		{
			name:    "empty set",
			rule:    MatchRule{Key: "reasoning_effort", Match: "high"},
			wantErr: "set must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestFilters_MatchRuleSanitizedSet(t *testing.T) {
	t.Run("empty set", func(t *testing.T) {
		params, keys := MatchRule{Key: "k", Match: "v"}.SanitizedSet()
		assert.Nil(t, params)
		assert.Nil(t, keys)
	})

	t.Run("keys sorted and protected removed", func(t *testing.T) {
		rule := MatchRule{Key: "k", Match: "v", Set: map[string]any{
			"top_p": 0.9,
			"model": "hijacked",
			"temp":  0.1,
		}}
		params, keys := rule.SanitizedSet()
		assert.Equal(t, []string{"temp", "top_p"}, keys)
		assert.Equal(t, map[string]any{"temp": 0.1, "top_p": 0.9}, params)
	})

	t.Run("only protected params", func(t *testing.T) {
		rule := MatchRule{Key: "k", Match: "v", Set: map[string]any{"model": "x"}}
		params, keys := rule.SanitizedSet()
		assert.Nil(t, params)
		assert.Nil(t, keys)
	})
}
