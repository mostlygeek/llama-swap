package capcompat

import (
	"context"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
)

// llamaServerOwner is what llama-server and its forks report in /v1/models.
const llamaServerOwner = "llamacpp"

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

// warnFunc logs a warning about a /props field that could not be read as
// expected. The probe continues with the field's zero value.
type warnFunc func(format string, args ...any)

// noWarn is the warnFunc for a body no field can be read from; one warning
// about the body explains every field that then reads as its zero value.
var noWarn warnFunc = func(string, ...any) {}

// llamaServerProber reads capabilities from llama-server's /props endpoint.
// It also covers the forks that keep llama.cpp's server API, such as
// ik_llama.cpp, because they report the same owner.
//
// Fields are read out of the response one at a time with gjson rather than
// unmarshalling the whole body: builds add fields of mixed types, such as
// b12017's chat_template_caps carrying a list alongside its booleans, and
// one unexpected field must not take the rest of the probe down. A field
// that is missing or of the wrong type logs a WARN and reads as its zero
// value, so the probe continues with what it can work out.
type llamaServerProber struct{}

var _ Prober = llamaServerProber{}

func (llamaServerProber) Name() string { return "llama-server" }

func (llamaServerProber) Upstream(ModelsResponse) hint.Upstream { return hint.LlamaServer }

func (llamaServerProber) Matches(models ModelsResponse) bool {
	return strings.EqualFold(models.OwnedBy(), llamaServerOwner)
}

func (p llamaServerProber) Probe(ctx context.Context, c *Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error) {
	body, err := c.GetBody(ctx, "/props")
	if err != nil {
		return config.ModelCapConfig{}, err
	}

	props := gjson.ParseBytes(body)
	warn := noWarn
	if gjson.ValidBytes(body) {
		warn = c.Warnf
	} else {
		// gjson can still read fields that appear before a truncation, so
		// the body must not answer any field at all: the warning explains
		// every field that then reads as its zero value.
		c.Warnf("capcompat: /props is not valid JSON, continuing with defaults")
		props = gjson.Result{}
	}

	caps := config.ModelCapConfig{
		// llama-server always accepts text and, except for decision models,
		// produces it. Everything else is additive and depends on what the
		// build reports.
		In:  []string{"text"},
		Out: []string{"text"},
	}

	// A decision model only answers /v1/systemone and generates no text.
	// /props has no output field to say so, so this is the one place the
	// listing is read: llama-server reports output_modalities ["decisions"].
	if entry, ok := models.Find(modelName); ok && slices.Contains(entry.Architecture.OutputModalities, "decisions") {
		caps.Out = []string{"decisions"}
	}

	switch m := props.Get("modalities"); {
	case !m.Exists():
		warn("capcompat: /props has no modalities, assuming text only")
	case !m.IsObject():
		warn("capcompat: /props modalities is not an object, ignoring it")
	default:
		for prop, modality := range modalityForProp {
			if propBool(props, "modalities."+prop, warn) {
				caps.In = append(caps.In, modality)
			}
		}
	}
	// Map iteration order is random, so keep the list stable for callers and
	// for the cached blob.
	sortModalities(caps.In)

	// Tool calls are text, so a model that generates none cannot make them,
	// whatever its chat template reports.
	caps.Tools = slices.Contains(caps.Out, "text") && p.supportsTools(props, warn)

	// reranker is not reported by /props. A reranking server answers
	// /v1/rerank, but probing it would mean sending a real request, so it
	// stays a manual setting.
	caps.Context = propInt(props, "default_generation_settings.n_ctx", warn)

	return caps, nil
}

// propBool reads a boolean field. A missing field reads as false: builds
// report different subsets of the keys, such as an older build that has no
// video key. A field that is present but not a boolean logs a WARN and also
// reads as false, so one odd value cannot take the rest of the probe down.
func propBool(props gjson.Result, path string, warn warnFunc) bool {
	v := props.Get(path)
	if !v.Exists() {
		return false
	}
	if b, ok := v.Value().(bool); ok {
		return b
	}
	warn("capcompat: /props %s is not a boolean, ignoring it", path)
	return false
}

// propInt reads an integer field. A missing or non-numeric field logs a WARN
// and reads as 0, so the value falls back to what the model's config sets.
func propInt(props gjson.Result, path string, warn warnFunc) int {
	v := props.Get(path)
	if !v.Exists() {
		warn("capcompat: /props has no %s, ignoring it", path)
		return 0
	}
	if n, ok := v.Value().(float64); ok {
		return int(n)
	}
	warn("capcompat: /props %s is not a number, ignoring it", path)
	return 0
}

// supportsTools reports whether the loaded chat template can carry tools.
//
// llama.cpp reports this in chat_template_caps, where supports_tools means
// the template renders a tool list and supports_tool_calls means it can read
// tool calls back. Both are required for a usable round trip, and requiring
// both also guards against the C++ struct's true defaults leaking through on
// a template llama.cpp could not fully inspect. A caps value that is not a
// boolean, such as the newer builds' reasoning_effort_levels list, is ignored
// with a WARN rather than failing the whole probe.
//
// The template source itself is never a signal: it can mention tools the
// server cannot actually use. Builds predating chat_template_caps therefore
// simply report no tool support.
func (llamaServerProber) supportsTools(props gjson.Result, warn warnFunc) bool {
	caps := props.Get("chat_template_caps")
	if caps.Exists() {
		switch v := caps.Value().(type) {
		case map[string]any:
			// An empty object reports no tool support, as it did when the
			// caps were decoded as a map.
			if len(v) > 0 {
				return propBool(props, "chat_template_caps.supports_tools", warn) &&
					propBool(props, "chat_template_caps.supports_tool_calls", warn)
			}
		default:
			warn("capcompat: /props chat_template_caps is not an object, ignoring it")
		}
	}
	return false
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
