package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestKubeswap_ParseEnvVars Verifies env var parsing: valid K=V forms and rejection of malformed ones.
func TestKubeswap_ParseEnvVars(t *testing.T) {
	got, err := parseEnvVars([]string{"A=1", "B=x=y", "PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []envVar{{Key: "A", Value: "1"}, {Key: "B", Value: "x=y"}, {Key: "PATH", Value: "/usr/bin:/bin"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := parseEnvVars([]string{"NOEQUALS"}); err == nil {
		t.Error("expected error for missing '='")
	}
	// Dots and hyphens are part of the API server's env-name rule
	// ([-._a-zA-Z][-._a-zA-Z0-9]*) and must not be rejected here.
	for _, good := range []string{"a.b=v", "MY-ENV.NAME=v", "-abc=v"} {
		if _, err := parseEnvVars([]string{good}); err != nil {
			t.Errorf("expected env name %q to be valid: %v", good, err)
		}
	}
	for _, bad := range []string{"=v", "FOO BAR=v", "1abc=v", ".=v", "..=v", "..foo=v"} {
		if _, err := parseEnvVars([]string{bad}); err == nil {
			t.Errorf("expected error for env name %q", bad)
		}
	}
}

// TestKubeswap_ParseGPUs Verifies GPU parsing accepts only positive whole-number counts.
func TestKubeswap_ParseGPUs(t *testing.T) {
	got, err := parseGPUs([]string{"amd.com/gpu=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["amd.com/gpu"] != "1" {
		t.Errorf("got %v", got)
	}
	if _, err := parseGPUs([]string{"nvidia.com/gpu"}); err == nil {
		t.Error("expected error for missing count")
	}
	// Counts are positive whole numbers: no words, zero or fractions.
	for _, bad := range []string{"nvidia.com/gpu=many", "amd.com/gpu=0", "amd.com/gpu=1.5"} {
		if _, err := parseGPUs([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	got, err = parseGPUs([]string{"amd.com/gpu=2"})
	if err != nil || got["amd.com/gpu"] != "2" {
		t.Errorf("got %v (err %v), want amd.com/gpu=2 preserved", got, err)
	}
}

// TestKubeswap_ParseTolerations Verifies toleration parsing: operator/effect validation and empty-effect preservation.
func TestKubeswap_ParseTolerations(t *testing.T) {
	got, err := parseTolerations([]string{"dedicated:Exists::NoSchedule"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []toleration{{Key: "dedicated", Operator: "Exists", Value: "", Effect: "NoSchedule"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got, err = parseTolerations([]string{"a=b"})
	if err == nil {
		t.Error("expected error for malformed toleration")
	}
	// Empty effect is preserved (it tolerates every effect); "All" is not a
	// Kubernetes effect and must be rejected, as are bad operators and a
	// value under Exists.
	got, err = parseTolerations([]string{":Exists::"}) // tolerate everything
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []toleration{{Key: "", Operator: "Exists", Value: "", Effect: ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{
		":Equal::NoSchedule",      // empty key under Equal
		"key:All::NoSchedule",     // bad operator
		"key:Equal:v:All",         // bad effect
		"key:Exists:v:NoSchedule", // value under Exists
	} {
		if _, err := parseTolerations([]string{bad}); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	got, err = parseTolerations([]string{"key:Equal:v:"})
	if err != nil {
		t.Fatalf("empty effect should be accepted: %v", err)
	}
	if want := []toleration{{Key: "key", Operator: "Equal", Value: "v", Effect: ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestKubeswap_ParseVolumes Verifies volume parsing for the pvc, emptydir and hostpath forms.
func TestKubeswap_ParseVolumes(t *testing.T) {
	got, err := parseVolumes([]string{
		"pvc:llama-swap-models:/models:ro",
		"emptydir:slots:/slots",
		"hostpath:/data/models:/models:ro",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []volumeSpec{
		{Kind: volPVC, Name: "llama-swap-models", Path: "/models", ReadOnly: true},
		{Kind: volEmptyDir, Name: "slots", Path: "/slots"},
		{Kind: volHostPath, Name: "/data/models", Path: "/models", ReadOnly: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"pvc:only-two", "blob:foo:/mnt", "pvc:/models", "pvc:claim:/models:rw"} {
		if _, err := parseVolumes([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// TestKubeswap_ParseKeyValues Verifies K=V parsing, including values that contain '='.
func TestKubeswap_ParseKeyValues(t *testing.T) {
	got, err := parseKeyValues([]string{"k1=v1", "k2=a=b"}, "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["k1"] != "v1" || got["k2"] != "a=b" {
		t.Errorf("got %v", got)
	}
	// A prefixed key is valid label-key form.
	if _, err := parseKeyValues([]string{"feature.node.kubernetes.io/amd-gpu=v"}, "node-selector"); err != nil {
		t.Errorf("prefixed key should be valid: %v", err)
	}
	// Qualified-name names: uppercase, underscores and periods are legal
	// (the API server accepts them), so they must not be rejected here.
	for _, good := range []string{"UPPER=v", "My_Key=v", "a.B_c=v", "feature.node.kubernetes.io/My.Key=v"} {
		if _, err := parseKeyValues([]string{good}, "label"); err != nil {
			t.Errorf("expected label key %q to be valid: %v", good, err)
		}
	}
	for _, bad := range []string{
		"BAD KEY=v",   // space
		"/=v", "a/=v", // empty name / empty prefix
		"My.Prefix/key=v",                               // the prefix must stay a lowercase subdomain
		"_leading=v", "trailing_=v", ".dot=v", "dot.=v", // must start/end alphanumeric
		"a/b/c=v",                            // two slashes
		"a" + strings.Repeat("b", 63) + "=v", // 64 characters
	} {
		if _, err := parseKeyValues([]string{bad}, "label"); err == nil {
			t.Errorf("expected error for label key %q", bad)
		}
	}
}

// TestKubeswap_ParseResources Verifies resource parsing rejects quantities the API would reject.
func TestKubeswap_ParseResources(t *testing.T) {
	got, err := parseResources([]string{"cpu=2", "memory=4Gi", "amd.com/gpu=1"}, "request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got["cpu"] != "2" || got["memory"] != "4Gi" {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"cpu", "cpu=", "cpu=many"} {
		if _, err := parseResources([]string{bad}, "limit"); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

// TestKubeswap_ParseServicePorts Verifies service-port parsing: name rules, port range and duplicate names.
func TestKubeswap_ParseServicePorts(t *testing.T) {
	got, err := parseServicePorts([]string{"metrics:9090", "alt:8081"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []servicePortSpec{{Name: "metrics", Port: 9090}, {Name: "alt", Port: 8081}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{
		"noprefix",              // no name
		"metrics:",              // empty name
		"metrics:abc",           // non-numeric port
		"Metrics:9090",          // uppercase
		"a:b:c",                 // extra colon
		"metrics:70000",         // port out of range
		"8080:9090",             // no letter (must contain at least one a-z)
		"met--rics:9090",        // consecutive hyphens
		"-metrics:9090",         // leading hyphen
		"metrics-:9090",         // trailing hyphen
		"abcdefghijklmnop:9090", // 16 characters
	} {
		if _, err := parseServicePorts([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	// 15 characters is the IANA_SVC_NAME limit and is accepted.
	if _, err := parseServicePorts([]string{"abcdefghijklmno:9090"}); err != nil {
		t.Errorf("15-character name should be valid: %v", err)
	}
	if _, err := parseServicePorts([]string{"m:9090", "m:9090"}); err == nil {
		t.Error("expected error for duplicate port")
	}
	// Container port names must be unique, so the same name on a
	// different port is a duplicate as well.
	if _, err := parseServicePorts([]string{"metrics:9090", "metrics:9091"}); err == nil {
		t.Error("expected error for duplicate name with different port")
	}
}

// TestKubeswap_StatusWatchRejectsNonPositiveInterval verifies --interval
// is validated at parse time: time.NewTicker would panic on a non-positive
// duration after the initial status print.
func TestKubeswap_StatusWatchRejectsNonPositiveInterval(t *testing.T) {
	for _, iv := range []string{"0", "-5s"} {
		err := statusCmd([]string{"--watch", "--interval", iv})
		if err == nil || !strings.Contains(err.Error(), "invalid --interval") {
			t.Errorf("--interval %s: expected invalid --interval error, got %v", iv, err)
		}
	}
}
