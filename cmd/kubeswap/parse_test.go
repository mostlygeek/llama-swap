package main

import (
	"reflect"
	"testing"
)

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
	if _, err := parseEnvVars([]string{"=v"}); err == nil {
		t.Error("expected error for empty key")
	}
}

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
}

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

func TestKubeswap_ParseKeyValues(t *testing.T) {
	got, err := parseKeyValues([]string{"k1=v1", "k2=a=b"}, "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["k1"] != "v1" || got["k2"] != "a=b" {
		t.Errorf("got %v", got)
	}
}

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

func TestKubeswap_ParseServicePorts(t *testing.T) {
	got, err := parseServicePorts([]string{"metrics:9090", "alt:8081"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []servicePortSpec{{Name: "metrics", Port: 9090}, {Name: "alt", Port: 8081}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"noprefix", "metrics:", "metrics:abc", "Metrics:9090", "a:b:c", "metrics:70000"} {
		if _, err := parseServicePorts([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
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
