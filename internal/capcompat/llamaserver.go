package capcompat

import (
	"context"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// llamaServerOwner is what llama-server and its forks report in /v1/models.
const llamaServerOwner = "llamacpp"

// propsResponse is the subset of llama-server's GET /props that describes what
// the loaded model can do.
type propsResponse struct {
	// DefaultGenerationSettings.NCtx is the context a request can actually
	// use, which is what a client needs. It is not the same number as
	// meta.n_ctx in /v1/models: a captured server reports 220160 here while
	// the listing says 262144, so reading the listing would advertise a
	// window the server will refuse to fill.
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`

	// Modalities is decoded as a map so a modality added upstream does not
	// break parsing. Known keys are mapped below; the rest are ignored.
	Modalities map[string]bool `json:"modalities"`

	// ChatTemplateCaps is what llama.cpp detected about the model's chat
	// template. Older builds omit it entirely.
	ChatTemplateCaps map[string]bool `json:"chat_template_caps"`

	ChatTemplate string `json:"chat_template"`
}

// modalityForProp maps a llama-server modality key onto a llama-swap input
// modality. Text is always supported and is added unconditionally.
//
// All three keys are confirmed against a running server, which reports
// {"vision":false,"video":false,"audio":false} for a text-only build. Keys
// outside this map are dropped rather than passed through, where an
// unrecognised name would fail ModelCapConfig.Validate.
var modalityForProp = map[string]string{
	"vision": "image",
	"audio":  "audio",
	"video":  "video",
}

// llamaServerProber reads capabilities from llama-server's /props endpoint.
// It also covers the forks that keep llama.cpp's server API, such as
// ik_llama.cpp, because they report the same owner.
type llamaServerProber struct{}

var _ Prober = llamaServerProber{}

func (llamaServerProber) Name() string { return "llama-server" }

func (llamaServerProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), llamaServerOwner)
}

func (p llamaServerProber) Probe(ctx context.Context, c *Client, _ ModelsResponse, _ string) (config.ModelCapConfig, error) {
	var props propsResponse
	if err := c.GetJSON(ctx, "/props", &props); err != nil {
		return config.ModelCapConfig{}, err
	}

	caps := config.ModelCapConfig{
		// llama-server always accepts and produces text. Everything else is
		// additive and depends on what the build reports.
		In:  []string{"text"},
		Out: []string{"text"},
	}

	for prop, modality := range modalityForProp {
		if props.Modalities[prop] {
			caps.In = append(caps.In, modality)
		}
	}
	// Map iteration order is random, so keep the list stable for callers and
	// for the cached blob.
	sortModalities(caps.In)

	caps.Tools = p.supportsTools(props)
	caps.Context = props.DefaultGenerationSettings.NCtx

	// reranker is not reported by /props. A reranking server answers
	// /v1/rerank, but probing it would mean sending a real request, so it
	// stays a manual setting.
	return caps, nil
}

// supportsTools reports whether the loaded chat template can carry tools.
//
// llama.cpp reports this in chat_template_caps, where supports_tools means
// the template renders a tool list and supports_tool_calls means it can read
// tool calls back. Both are required for a usable round trip, and requiring
// both also guards against the C++ struct's true defaults leaking through on
// a template llama.cpp could not fully inspect.
//
// Builds predating chat_template_caps omit the object. There the only signal
// left is the template itself, which is not conclusive, so this falls back to
// looking for a tools loop in the template source.
func (llamaServerProber) supportsTools(props propsResponse) bool {
	if len(props.ChatTemplateCaps) > 0 {
		return props.ChatTemplateCaps["supports_tools"] && props.ChatTemplateCaps["supports_tool_calls"]
	}
	return strings.Contains(props.ChatTemplate, "tools")
}

// modalityOrder fixes the order modalities are listed in, so a discovered
// value is byte-stable across probes.
var modalityOrder = map[string]int{"text": 0, "image": 1, "audio": 2, "video": 3}

func sortModalities(ms []string) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && modalityOrder[ms[j]] < modalityOrder[ms[j-1]]; j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}
