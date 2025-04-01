package proxy

import "testing"

func TestSanitizeAccessControlRequestHeaderValues(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: "",
		},
		{
			name:     "single valid value",
			input:    "content-type",
			expected: "content-type",
		},
		{
			name:     "multiple valid values",
			input:    "content-type, authorization, x-requested-with",
			expected: "content-type, authorization, x-requested-with",
		},
		{
			name:     "values with extra spaces",
			input:    "  content-type  ,  authorization  ",
			expected: "content-type, authorization",
		},
		{
			name:     "values with tabs",
			input:    "content-type,\tauthorization",
			expected: "content-type, authorization",
		},
		{
			name:     "values with invalid characters",
			input:    "content-type, auth\n, x-requested-with\r",
			expected: "content-type, auth, x-requested-with",
		},
		{
			name:     "empty values in list",
			input:    "content-type,,authorization",
			expected: "content-type, authorization",
		},
		{
			name:     "leading and trailing commas",
			input:    ",content-type,authorization,",
			expected: "content-type, authorization",
		},
		{
			name:     "mixed valid and invalid values",
			input:    "content-type, \x00invalid, x-requested-with",
			expected: "content-type, x-requested-with",
		},
		{
			name:     "mixed case values",
			input:    "Content-Type, my-Valid-Header, Another-hEader",
			expected: "Content-Type, my-Valid-Header, Another-hEader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeAccessControlRequestHeaderValues(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeAccessControlRequestHeaderValues(%q) = %q, want %q",
					tt.input, got, tt.expected)
			}
		})
	}
}
