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

func TestKubeswap_Names(t *testing.T) {
	dep, err := deploymentName("my-model")
	if err != nil {
		t.Fatalf("deploymentName: %v", err)
	}
	svc, err := serviceName("my-model")
	if err != nil {
		t.Fatalf("serviceName: %v", err)
	}
	if svc != dep+"-svc" {
		t.Errorf("serviceName = %q, want %q-svc", svc, dep)
	}
	if !strings.HasPrefix(dep, "my-model-") {
		t.Errorf("deploymentName = %q, want my-model-<hash>", dep)
	}

	// Deterministic: same model ID, same name.
	dep2, _ := deploymentName("my-model")
	if dep != dep2 {
		t.Errorf("deploymentName not deterministic: %q vs %q", dep, dep2)
	}

	// Long model IDs stay within DNS-1123 limits (names and services).
	long := strings.Repeat("a", 200)
	dl, err := deploymentName(long)
	if err != nil {
		t.Fatalf("deploymentName(long): %v", err)
	}
	if len(dl) > 63 {
		t.Errorf("deploymentName too long: %d", len(dl))
	}
	sl, err := serviceName(long)
	if err != nil {
		t.Fatalf("serviceName(long): %v", err)
	}
	if len(sl) > 63 {
		t.Errorf("serviceName too long: %d", len(sl))
	}
	if !strings.HasSuffix(sl, "-svc") {
		t.Errorf("expected -svc suffix, got %q", sl)
	}
}

func TestKubeswap_NamesCollisionFree(t *testing.T) {
	// Distinct IDs that sanitize to the same string must get distinct names.
	a, err := deploymentName("Model_A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := deploymentName("model-a")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("Model_A and model-a both map to %q", a)
	}

	// Distinct IDs sharing a long sanitized prefix must get distinct names.
	prefix := strings.Repeat("x", 70)
	c, _ := deploymentName(prefix + "-tail-1")
	d, _ := deploymentName(prefix + "-tail-2")
	if c == d {
		t.Fatalf("prefix-sharing IDs both map to %q", c)
	}

	// Names must be valid DNS-1123 labels.
	for _, n := range []string{a, b, c, d} {
		if n != strings.ToLower(n) || strings.ContainsAny(n, "_:/.") {
			t.Errorf("invalid label: %q", n)
		}
	}
}

func TestKubeswap_Labels(t *testing.T) {
	l := managedLabels("my-model")
	if l[labelManagedBy] != managedByValue || l[labelModel] != "my-model" {
		t.Errorf("managedLabels wrong: %v", l)
	}
	s := podSelectorLabels("my-model", "my-model-abc12345")
	if s[labelManagedBy] != managedByValue || s[labelModel] != "my-model" || s[labelDeployment] != "my-model-abc12345" {
		t.Errorf("podSelectorLabels wrong: %v", s)
	}
	p := podLabels("my-model", "my-model-abc12345")
	if p[labelAppName] != appNameValue || p[labelModel] != "my-model" || p[labelDeployment] != "my-model-abc12345" {
		t.Errorf("podLabels wrong: %v", p)
	}
}
