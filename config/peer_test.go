package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Tests that defaults are set when unmarshaling an empty/minimal YAML.
func TestPeerConfig_Defaults(t *testing.T) {
	var pc PeerConfig
	data := `{}`

	if err := yaml.Unmarshal([]byte(data), &pc); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if pc.Name != "" {
		t.Errorf("Name expected %q, got %q", "", pc.Name)
	}
	if pc.Description != "" {
		t.Errorf("Description expected %q, got %q", "", pc.Description)
	}
	if pc.BaseURL != "" {
		t.Errorf("BaseURL expected %q, got %q", "", pc.BaseURL)
	}
	if pc.ApiKey != "" {
		t.Errorf("ApiKey expected %q, got %q", "", pc.ApiKey)
	}
	if pc.Priority != 0 {
		t.Errorf("Priority expected %d, got %d", 0, pc.Priority)
	}
	if len(pc.Filters) != 0 {
		t.Errorf("Filters expected length %d, got %d", 0, len(pc.Filters))
	}
	if len(pc.reFilters) != 0 {
		t.Errorf("reFilters expected length %d, got %d", 0, len(pc.reFilters))
	}
}

// Tests that valid regex patterns in Filters are compiled into reFilters and work as expected.
func TestPeerConfig_RegexCompileSuccess(t *testing.T) {
	var pc PeerConfig
	data := `
filters:
  - "^foo.*"
  - "ba[rz]$"
`

	if err := yaml.Unmarshal([]byte(data), &pc); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if len(pc.Filters) != 2 {
		t.Fatalf("expected Filters length 2, got %d", len(pc.Filters))
	}
	if len(pc.reFilters) != 2 {
		t.Fatalf("expected reFilters length 2, got %d", len(pc.reFilters))
	}

	// first pattern ^foo.*
	if !pc.reFilters[0].MatchString("foobar") {
		t.Errorf("expected pattern %q to match %q", pc.Filters[0], "foobar")
	}
	if pc.reFilters[0].MatchString("barfoo") {
		t.Errorf("expected pattern %q NOT to match %q", pc.Filters[0], "barfoo")
	}

	// second pattern ba[rz]$
	if !pc.reFilters[1].MatchString("bar") {
		t.Errorf("expected pattern %q to match %q", pc.Filters[1], "bar")
	}
	if !pc.reFilters[1].MatchString("baz") {
		t.Errorf("expected pattern %q to match %q", pc.Filters[1], "baz")
	}
	if pc.reFilters[1].MatchString("bax") {
		t.Errorf("expected pattern %q NOT to match %q", pc.Filters[1], "bax")
	}
}

// Tests that an invalid regex produces an error during Unmarshal.
func TestPeerConfig_RegexCompileFailure(t *testing.T) {
	var pc PeerConfig
	data := `
filters:
  - "("
`

	err := yaml.Unmarshal([]byte(data), &pc)
	if err == nil {
		t.Fatalf("expected error compiling invalid regex, got nil")
	}
	// Optionally ensure our error message path was used
	if !strings.Contains(err.Error(), "failed to compile peer filter") {
		t.Logf("warning: error did not contain expected text; full error: %v", err)
		t.Fail()
	}
}
