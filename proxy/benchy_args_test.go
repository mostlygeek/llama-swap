package proxy

import (
	"reflect"
	"testing"
)

func TestBuildBenchyArgsRequired(t *testing.T) {
	opts := benchyRunOptions{
		PP:   []int{512, 2048},
		TG:   []int{32},
		Runs: 5,
	}

	got := buildBenchyArgs("http://127.0.0.1:8081/v1", "hf/model", "served-model", "hf/model", "secret-key", opts)
	want := []string{
		"--base-url", "http://127.0.0.1:8081/v1",
		"--model", "hf/model",
		"--served-model-name", "served-model",
		"--tokenizer", "hf/model",
		"--runs", "5",
		"--api-key", "secret-key",
		"--pp", "512", "2048",
		"--tg", "32",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestBuildBenchyArgsOptionalFlags(t *testing.T) {
	adaptPrompt := false
	opts := benchyRunOptions{
		PP:                  []int{1024},
		TG:                  []int{64, 128},
		Depth:               []int{0, 512},
		Concurrency:         []int{1, 2, 4},
		Runs:                3,
		LatencyMode:         "generation",
		NoCache:             true,
		NoWarmup:            true,
		AdaptPrompt:         &adaptPrompt,
		EnablePrefixCaching: true,
	}

	got := buildBenchyArgs("http://localhost:8080/v1", "model-a", "model-a", "tok-a", "", opts)
	want := []string{
		"--base-url", "http://localhost:8080/v1",
		"--model", "model-a",
		"--served-model-name", "model-a",
		"--tokenizer", "tok-a",
		"--runs", "3",
		"--pp", "1024",
		"--tg", "64", "128",
		"--depth", "0", "512",
		"--concurrency", "1", "2", "4",
		"--latency-mode", "generation",
		"--no-cache",
		"--no-warmup",
		"--no-adapt-prompt",
		"--enable-prefix-caching",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args\nwant: %#v\ngot:  %#v", want, got)
	}
}
