package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// llamaServerExtractor reads the API usage block plus llama-server's
// timings block, which is more precise and overrides the counts.
type llamaServerExtractor struct{}

func (llamaServerExtractor) newParser(path string, start time.Time) usageParser {
	return &llamaServerParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type llamaServerParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings gjson.Result
}

func (p *llamaServerParser) event(ev gjson.Result) {
	ev = lastArrayElement(ev)
	p.read(ev, &p.api)
	if t := ev.Get("timings"); t.IsObject() {
		p.timings = t
	}
}

func (p *llamaServerParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTimings(&u, p.timings)
	return u
}

// lastArrayElement unwraps /infill responses, which are arrays with the
// usage and timings in the last element (#463).
func lastArrayElement(ev gjson.Result) gjson.Result {
	if !ev.IsArray() {
		return ev
	}
	arr := ev.Array()
	if len(arr) == 0 {
		return gjson.Result{}
	}
	return arr[len(arr)-1]
}

// applyTimings applies a llama-server timings block. prompt_n counts only
// the prompt tokens processed and cache_n the ones reused from the cache, so
// the whole prompt is prompt_n + cache_n (the same number usage.prompt_tokens
// reports). predicted_n is the output count, prompt_per_second and
// predicted_per_second the rates, prompt_ms + predicted_ms the duration
// (when longer than wall time), and draft_n / draft_n_accepted the
// speculative decoding counters.
func applyTimings(u *Usage, timings gjson.Result) {
	if !timings.Exists() {
		return
	}
	u.Found = true
	cached := timings.Get("cache_n")
	u.Tokens.InputTokens = int(timings.Get("prompt_n").Int() + cached.Int())
	u.Tokens.OutputTokens = int(timings.Get("predicted_n").Int())
	u.Tokens.PromptPerSecond = timings.Get("prompt_per_second").Float()
	u.Tokens.TokensPerSecond = timings.Get("predicted_per_second").Float()
	if ms := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float()); ms > u.DurationMs {
		u.DurationMs = ms
	}
	if cached.Exists() {
		u.Tokens.CachedTokens = int(cached.Int())
	}
	if drafted, accepted := timings.Get("draft_n"), timings.Get("draft_n_accepted"); drafted.Exists() && accepted.Exists() {
		u.Tokens.DraftTokens = int(drafted.Int())
		u.Tokens.DraftAccTokens = int(accepted.Int())
	}
}
