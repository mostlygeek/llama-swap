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
	case hint.Kyojin:
		return kyojinExtractor{}
	case hint.Radiance:
		return radianceExtractor{}
	case hint.Halogen:
		return halogenExtractor{}
	case hint.OpenAI, hint.Anthropic, hint.OpenRouter, hint.OpenAICompatible:
		return apiExtractor{}
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

// apiUsage holds the token counts read from an API usage block. Each wire
// format has its own reader (usage_openai.go, usage_anthropic.go); servers
// add their own fields on top.
type apiUsage struct {
	input, output     int
	cached, reasoning int // -1 when not reported
	found             bool
}

func newAPIUsage() apiUsage { return apiUsage{cached: -1, reasoning: -1} }

// apiReader reads the usage block of one wire format from an event into u.
// Streams report usage across several events, so a reader keeps earlier
// values unless the event carries a new non-zero count.
type apiReader func(ev gjson.Result, u *apiUsage)

// apiReaderFor picks the wire format from the endpoint path. The Anthropic
// Messages API (/v1/messages, or /v/messages without the version) has its
// own usage shape; every other metered endpoint uses OpenAI's.
func apiReaderFor(path string) apiReader {
	if strings.HasSuffix(path, "/messages") {
		return readAnthropicUsage
	}
	return readOpenAIUsage
}

// toUsage turns the counts into a Usage with every server-reported field
// (rates, draft counts) unknown and the duration measured from start.
func (a apiUsage) toUsage(start time.Time) Usage {
	return Usage{
		Found: a.found,
		Tokens: TokenMetrics{
			InputTokens:     a.input,
			OutputTokens:    a.output,
			CachedTokens:    a.cached,
			ReasoningTokens: a.reasoning,
			PromptPerSecond: -1,
			TokensPerSecond: -1,
			DraftTokens:     -1,
			DraftAccTokens:  -1,
		},
		DurationMs: int(time.Since(start).Milliseconds()),
	}
}

// setCount stores v in dst when it is a non-zero number and reports whether
// the field was present at all.
func setCount(dst *int, v gjson.Result) bool {
	if !v.Exists() {
		return false
	}
	if n := int(v.Int()); n > 0 {
		*dst = n
	}
	return true
}

// setReported stores v in dst when it is a number, zero included. Used for
// counts where 0 is a real answer (cached, reasoning).
func setReported(dst *int, v gjson.Result) bool {
	if v.Type != gjson.Number {
		return false
	}
	*dst = int(v.Int())
	return true
}

// apiExtractor reads only the API usage block. It serves hosted APIs
// (OpenAI, Anthropic, OpenRouter) and OpenAI-compatible servers that add
// nothing to it.
type apiExtractor struct{}

func (apiExtractor) newParser(path string, start time.Time) usageParser {
	return &apiParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type apiParser struct {
	start time.Time
	read  apiReader
	api   apiUsage
}

func (p *apiParser) event(ev gjson.Result) { p.read(ev, &p.api) }
func (p *apiParser) usage() Usage          { return p.api.toUsage(p.start) }
