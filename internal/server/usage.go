package server

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/tidwall/gjson"
)

// Usage is the normalized usage record every extractor produces. Token
// fields the upstream did not report are -1 (see TokenMetrics).
type Usage struct {
	// Found reports whether the response carried any usage data at all.
	Found bool
	// Task is the kind of inference, set from the endpoint path by the
	// middleware; extractors do not set it.
	Task       hint.Task
	Tokens     TokenMetrics
	DurationMs int
}

// usageExtractor knows how one kind of upstream reports usage. Each
// implementation lives in its own usage_<upstream>.go file.
type usageExtractor interface {
	// newParser returns per-request state. path is the endpoint path the
	// middleware meters, e.g. /v1/chat/completions or /infill. start is when
	// the request began, used for wall-clock duration.
	newParser(path string, start time.Time) usageParser
}

// usageParser is fed every JSON event of one response: each SSE data: line,
// or the whole body once for a non-streaming response.
type usageParser interface {
	event(ev gjson.Result)
	// usage is called after the last event.
	usage() Usage
}

// extractorFor returns the extractor for u. Unknown upstreams, and upstreams
// that pass through another server's format (llama-swap peers), use auto,
// which reads every format llama-swap knows.
func extractorFor(u hint.Upstream) usageExtractor {
	switch u {
	case hint.LlamaServer:
		return llamaServerExtractor{}
	case hint.VLLM:
		return vllmExtractor{}
	case hint.TabbyAPI:
		return tabbyAPIExtractor{}
	case hint.OMLX:
		return omlxExtractor{}
	case hint.Anthropic:
		return anthropicExtractor{}
	case hint.OpenAI, hint.OpenRouter, hint.OpenAICompatible, hint.Halogen:
		return openaiExtractor{}
	default:
		return autoExtractor{}
	}
}

// extractUsage runs a decoded (already decompressed) response body through
// the extractor for u. A server-sent event stream is fed one data: line at a
// time; anything else is parsed as one JSON document.
func extractUsage(u hint.Upstream, path, contentType string, body []byte, start time.Time) (Usage, error) {
	p := extractorFor(u).newParser(path, start)

	if strings.Contains(contentType, "text/event-stream") {
		forEachSSEData(body, func(data []byte) {
			if gjson.ValidBytes(data) {
				p.event(gjson.ParseBytes(data))
			}
		})
		usage := p.usage()
		if !usage.Found {
			return Usage{}, fmt.Errorf("no valid JSON data found in stream")
		}
		return usage, nil
	}

	if !gjson.ValidBytes(body) {
		return Usage{}, fmt.Errorf("invalid JSON in response body")
	}
	parsed := gjson.ParseBytes(body)
	p.event(parsed)
	usage := p.usage()
	// A non-streaming response with a usage object counts as reported even
	// when it names no token field the extractor recognizes.
	if !usage.Found && parsed.Get("usage").Exists() {
		usage.Found = true
	}
	return usage, nil
}

// forEachSSEData calls fn with the payload of every non-empty data: line in
// an event stream, skipping the [DONE] terminator.
func forEachSSEData(body []byte, fn func(data []byte)) {
	prefix := []byte("data:")
	for offset := 0; offset < len(body); {
		nl := bytes.IndexByte(body[offset:], '\n')
		var line []byte
		if nl == -1 {
			line = body[offset:]
			offset = len(body)
		} else {
			line = body[offset : offset+nl]
			offset += nl + 1
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, prefix) {
			continue
		}
		data := bytes.TrimSpace(line[len(prefix):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		fn(data)
	}
}

// usageReaders selects which response blocks a parser reads besides the
// OpenAI/Anthropic-style usage block, which every parser reads.
type usageReaders struct {
	timings bool // llama-server timings
	metrics bool // vLLM metrics
}

// readerParser is the parser shared by every extractor. Extractors differ
// only in which blocks they read.
type readerParser struct {
	readers usageReaders
	acc     usageAccumulator
}

func newReaderParser(readers usageReaders, start time.Time) *readerParser {
	return &readerParser{
		readers: readers,
		acc:     usageAccumulator{start: start, cached: -1, reasoning: -1},
	}
}

func (p *readerParser) event(ev gjson.Result) {
	// /infill responses are arrays; usage lives in the last element (#463).
	if ev.IsArray() {
		arr := ev.Array()
		if len(arr) == 0 {
			return
		}
		ev = arr[len(arr)-1]
	}
	p.acc.readUsage(ev)
	if p.readers.timings {
		p.acc.readTimings(ev)
	}
	if p.readers.metrics {
		p.acc.readMetrics(ev)
	}
}

func (p *readerParser) usage() Usage { return p.acc.build() }

// usagePaths lists the JSON paths where a per-event usage object can live:
// OpenAI chat/completions, the Responses API, and Anthropic message_start.
var usagePaths = []string{"usage", "response.usage", "message.usage"}

// usageAccumulator collects usage across the events of one response. Later
// events win, so a final usage chunk overrides earlier partial ones.
type usageAccumulator struct {
	start                     time.Time
	inputTokens, outputTokens int64
	cached, reasoning         int64 // -1 unknown
	found                     bool
	usage                     gjson.Result
	timings                   gjson.Result
	metrics                   gjson.Result
}

func (a *usageAccumulator) readUsage(ev gjson.Result) {
	for _, path := range usagePaths {
		u := ev.Get(path)
		if !u.Exists() {
			continue
		}
		i, o, c, ok := extractUsageTokens(u)
		if !ok {
			continue
		}
		a.found = true
		if i > 0 {
			a.inputTokens = i
		}
		if o > 0 {
			a.outputTokens = o
		}
		if c >= 0 {
			a.cached = c
		}
		if r := extractReasoningTokens(u); r >= 0 {
			a.reasoning = r
		}
		// Remember the last usage block so build can read the backend rates
		// TabbyAPI and oMLX embed there.
		a.usage = u
	}
}

func (a *usageAccumulator) readTimings(ev gjson.Result) {
	if t := ev.Get("timings"); t.Exists() {
		a.timings = t
		a.found = true
	}
}

func (a *usageAccumulator) readMetrics(ev gjson.Result) {
	if m := ev.Get("metrics"); m.Exists() {
		a.metrics = m
		a.found = true
	}
}

// extractUsageTokens reads input/output/cached token counts from a usage
// gjson.Result, handling the field-name differences across endpoints.
func extractUsageTokens(usage gjson.Result) (input, output, cached int64, ok bool) {
	cached = -1
	if !usage.Exists() {
		return
	}

	if v := usage.Get("prompt_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	}

	if v := usage.Get("completion_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	} else if v := usage.Get("output_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	}

	if v := usage.Get("cache_read_input_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	}
	return
}

// extractReasoningTokens reads the reasoning share of the output tokens:
// completion_tokens_details for chat/completions (OpenAI, vLLM, OpenRouter,
// DeepSeek, Groq) and output_tokens_details for the Responses API. Returns
// -1 when the usage block does not report it.
func extractReasoningTokens(usage gjson.Result) int64 {
	if v := usage.Get("completion_tokens_details.reasoning_tokens"); v.Type == gjson.Number {
		return v.Int()
	}
	if v := usage.Get("output_tokens_details.reasoning_tokens"); v.Type == gjson.Number {
		return v.Int()
	}
	return -1
}

// build composes a Usage from accumulated token counts and optional
// llama-server timings (which override input/output and provide rates) or
// vLLM response metrics (rates and speculative decoding counters). TabbyAPI
// and oMLX embed rates in the usage block, which supplies base rates when
// timings and metrics are absent.
func (a *usageAccumulator) build() Usage {
	inputTokens, outputTokens, cachedTokens := a.inputTokens, a.outputTokens, a.cached
	usage, timings, responseMetrics := a.usage, a.timings, a.metrics

	wallDurationMs := int(time.Since(a.start).Milliseconds())
	durationMs := wallDurationMs
	tokensPerSecond := -1.0
	promptPerSecond := -1.0
	draftTokens := -1
	draftAccTokens := -1

	// TabbyAPI reports prompt/completion rates and total time inside its usage
	// block. These are base values that llama-server timings and vLLM metrics
	// override when present.
	if usage.Exists() {
		if v := usage.Get("prompt_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
			promptPerSecond = v.Float()
		} else if v := usage.Get("prompt_tokens_per_second"); v.Type == gjson.Number {
			// oMLX reports prompt throughput using the long-form suffix.
			promptPerSecond = v.Float()
		}
		if v := usage.Get("completion_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
			tokensPerSecond = v.Float()
		} else if v := usage.Get("generation_tokens_per_second"); v.Type == gjson.Number {
			// oMLX calls completion throughput generation throughput.
			tokensPerSecond = v.Float()
		}
		if v := usage.Get("total_time"); v.Exists() {
			if totalTimeMs := int(v.Float() * 1000); totalTimeMs > durationMs {
				durationMs = totalTimeMs
			}
		}
	}

	if timings.Exists() {
		inputTokens = timings.Get("prompt_n").Int()
		outputTokens = timings.Get("predicted_n").Int()
		promptPerSecond = timings.Get("prompt_per_second").Float()
		tokensPerSecond = timings.Get("predicted_per_second").Float()
		timingsDurationMs := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float())
		if timingsDurationMs > durationMs {
			durationMs = timingsDurationMs
		}
		if cachedValue := timings.Get("cache_n"); cachedValue.Exists() {
			cachedTokens = cachedValue.Int()
		}
		if timings.Get("draft_n").Exists() && timings.Get("draft_n_accepted").Exists() {
			draftTokens = int(timings.Get("draft_n").Int())
			draftAccTokens = int(timings.Get("draft_n_accepted").Int())
		}
	}
	// vLLM reports speculative decoding counts under metrics.speculative_decoding
	// when started with --per-request-spec-decode-metrics (#1032). Both counters
	// are required so the acceptance rate is never derived from a half-filled
	// object.
	if spec := responseMetrics.Get("speculative_decoding"); spec.Exists() {
		drafted := spec.Get("num_draft_tokens")
		accepted := spec.Get("num_accepted_draft_tokens")
		if drafted.Exists() && accepted.Exists() {
			draftTokens = int(drafted.Int())
			draftAccTokens = int(accepted.Int())
		}
	}
	if timeToFirstToken := responseMetrics.Get("time_to_first_token_ms"); timeToFirstToken.Exists() && timeToFirstToken.Float() > 0 {
		cachedForRate := cachedTokens
		if cachedForRate < 0 {
			cachedForRate = 0
		}
		promptPerSecond = float64(inputTokens-cachedForRate) / (timeToFirstToken.Float() / 1000)
	}
	if meanInterTokenLatency := responseMetrics.Get("mean_itl_ms"); meanInterTokenLatency.Exists() && meanInterTokenLatency.Float() > 0 {
		tokensPerSecond = 1000 / meanInterTokenLatency.Float()
	}

	return Usage{
		Found: a.found,
		Tokens: TokenMetrics{
			CachedTokens:    int(cachedTokens),
			DraftTokens:     draftTokens,
			DraftAccTokens:  draftAccTokens,
			InputTokens:     int(inputTokens),
			OutputTokens:    int(outputTokens),
			ReasoningTokens: int(a.reasoning),
			PromptPerSecond: promptPerSecond,
			TokensPerSecond: tokensPerSecond,
		},
		DurationMs: durationMs,
	}
}
