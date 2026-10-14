package config

import (
	"strings"
	"testing"
)

const defaultModelYAML = `
models:
  "model-a":
    cmd: /bin/true --port ${PORT}
  "model-b":
    cmd: /bin/true --port ${PORT}
    aliases: [bee]
`

func TestDefaultModelResolvesAlias(t *testing.T) {
	cfg, err := LoadConfigFromReader(strings.NewReader(defaultModelYAML + "defaultModel: bee\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, _ := cfg.RealModelName(cfg.DefaultModel); got != "model-b" {
		t.Fatalf("alias did not resolve: got %q", got)
	}
}

func TestDefaultModelUnknownIsRejected(t *testing.T) {
	_, err := LoadConfigFromReader(strings.NewReader(defaultModelYAML + "defaultModel: nope\n"))
	if err == nil {
		t.Fatal("expected an error for an unknown defaultModel")
	}
	if !strings.Contains(err.Error(), "defaultModel") {
		t.Fatalf("error should name defaultModel, got: %v", err)
	}
}

func TestDefaultModelEmptyIsAllowed(t *testing.T) {
	if _, err := LoadConfigFromReader(strings.NewReader(defaultModelYAML)); err != nil {
		t.Fatalf("config without defaultModel must load: %v", err)
	}
}
