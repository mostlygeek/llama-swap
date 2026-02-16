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

func TestBuildBenchyArgsIntelligenceFlags(t *testing.T) {
	maxConcurrent := 12
	opts := benchyRunOptions{
		PP:                  []int{2048},
		TG:                  []int{32},
		Runs:                2,
		EnableIntelligence:  true,
		IntelligencePlugins: []string{"mmlu", "arc-c", "gsm8k"},
		AllowCodeExec:       false,
		DatasetCacheDir:     "/tmp/bench-cache",
		OutputDir:           "/tmp/bench-runs",
		MaxConcurrent:       &maxConcurrent,
	}

	got := buildBenchyArgs("http://localhost:8080/v1", "model-a", "model-a", "tok-a", "", opts)
	want := []string{
		"--base-url", "http://localhost:8080/v1",
		"--model", "model-a",
		"--served-model-name", "model-a",
		"--tokenizer", "tok-a",
		"--runs", "2",
		"--pp", "2048",
		"--tg", "32",
		"--enable-intelligence",
		"--output-dir", "/tmp/bench-runs",
		"--intelligence-plugins", "mmlu", "arc-c", "gsm8k",
		"--dataset-cache-dir", "/tmp/bench-cache",
		"--max-concurrent", "12",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestNormalizeIntelligencePlugins(t *testing.T) {
	got, err := normalizeIntelligencePlugins([]string{"MMLU", "gsm8k", " mmlu ", "arc-c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mmlu", "gsm8k", "arc-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected plugins\nwant: %#v\ngot:  %#v", want, got)
	}
}
