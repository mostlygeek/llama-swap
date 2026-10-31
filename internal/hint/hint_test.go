package hint

import (
	"context"
	"net/url"
	"testing"
)

func TestHint_NewAndFrom(t *testing.T) {
	if From(context.Background()) != nil {
		t.Fatal("From on a bare context should be nil")
	}
	ctx, h := New(context.Background())
	h.Route = RoutePeer
	h.PeerID = "p1"
	h.Upstream = OpenAI

	child, cancel := context.WithCancel(ctx)
	defer cancel()
	got := From(child)
	if got != h {
		t.Fatal("From should return the same Request through derived contexts")
	}
	if got.Route != RoutePeer || got.PeerID != "p1" || got.Upstream != OpenAI {
		t.Fatalf("unexpected hint %+v", got)
	}
}

func TestHint_ForHost(t *testing.T) {
	tests := map[string]Upstream{
		"api.openai.com":      OpenAI,
		"API.OpenAI.com":      OpenAI,
		"api.openai.com:443":  OpenAI,
		"api.anthropic.com":   Anthropic,
		"openrouter.ai":       OpenRouter,
		"eu.openrouter.ai":    OpenRouter,
		"notopenrouter.ai":    Unknown,
		"192.168.1.23":        Unknown,
		"":                    Unknown,
		"api.openai.com.evil": Unknown,
	}
	for host, want := range tests {
		if got := ForHost(host); got != want {
			t.Errorf("ForHost(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestHint_ForURL(t *testing.T) {
	u, _ := url.Parse("https://openrouter.ai/api")
	if got := ForURL(u); got != OpenRouter {
		t.Fatalf("ForURL = %q", got)
	}
	if got := ForURL(nil); got != Unknown {
		t.Fatalf("ForURL(nil) = %q", got)
	}
}

func TestHint_UpstreamKnown(t *testing.T) {
	for _, u := range []Upstream{LlamaServer, VLLM, TabbyAPI, OMLX, Kyojin, Radiance, Halogen, OpenAICompatible, LlamaSwap, OpenAI, Anthropic, OpenRouter} {
		if !u.Known() {
			t.Errorf("%q should be known", u)
		}
	}
	for _, u := range []Upstream{Unknown, "default", "something-else"} {
		if u.Known() {
			t.Errorf("%q should not be known", u)
		}
	}
}
