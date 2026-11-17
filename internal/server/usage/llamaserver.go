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
	// Each field only replaces what the usage block reported when it is
	// present. Without cache_n, prompt_n alone would undercount a prompt the
	// usage block already counted in full, so it is used only when there is
	// no usage count (e.g. /infill).
	cached := timings.Get("cache_n")
	if prompt := timings.Get("prompt_n"); prompt.Exists() && (cached.Exists() || u.Tokens.InputTokens == 0) {
		u.Tokens.InputTokens = int(prompt.Int() + cached.Int())
	}
	if v := timings.Get("predicted_n"); v.Exists() {
		u.Tokens.OutputTokens = int(v.Int())
	}
	if v := timings.Get("prompt_per_second"); v.Exists() {
		u.Tokens.PromptPerSecond = v.Float()
	}
	if v := timings.Get("predicted_per_second"); v.Exists() {
		u.Tokens.TokensPerSecond = v.Float()
	}
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
