package main

import (
	"strings"
	"testing"
)

func TestKubeswap_SanitizeModelID(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"author/model", "author-model", false},
		{"My Model:v1.2", "my-model-v1-2", false},
		{"a", "a", false},
		{"LFM2.5-230M", "lfm2-5-230m", false},
		{"-weird--name-", "weird-name", false},
		{"", "", true},
		{"///", "", true},
		{"Ünïcode", "n-code", false},
	}
	for _, tt := range tests {
		got, err := sanitizeModelID(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("sanitizeModelID(%q): expected error, got %q", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeModelID(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("sanitizeModelID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestKubeswap_SanitizeModelIDLong(t *testing.T) {
	long := strings.Repeat("a", 80)
	got, err := sanitizeModelID(long)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 63 {
		t.Errorf("expected 63 chars, got %d: %q", len(got), got)
	}
}

func TestKubeswap_ServiceName(t *testing.T) {
	if got := serviceName("my-model"); got != "my-model-svc" {
		t.Errorf("serviceName(my-model) = %q, want my-model-svc", got)
	}
	long := strings.Repeat("b", 70)
	got := serviceName(long[:63])
	if len(got) > 63 {
		t.Errorf("serviceName too long: %d", len(got))
	}
	if !strings.HasSuffix(got, "-svc") {
		t.Errorf("expected -svc suffix, got %q", got)
	}
}

func TestKubeswap_Labels(t *testing.T) {
	l := managedLabels("my-model")
	if l[labelManagedBy] != managedByValue || l[labelModel] != "my-model" {
		t.Errorf("managedLabels wrong: %v", l)
	}
	p := podLabels("my-model")
	if p[labelAppName] != appNameValue || p[labelModel] != "my-model" {
		t.Errorf("podLabels wrong: %v", p)
	}
}
