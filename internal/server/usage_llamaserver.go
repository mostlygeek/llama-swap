package server

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

// applyTimings applies a llama-server timings block: prompt_n and
// predicted_n replace the token counts, prompt_per_second and
// predicted_per_second give the rates, prompt_ms + predicted_ms the
// duration (when longer than wall time), cache_n the cached count, and
// draft_n / draft_n_accepted the speculative decoding counters.
func applyTimings(u *Usage, timings gjson.Result) {
	if !timings.Exists() {
		return
	}
	u.Found = true
	u.Tokens.InputTokens = int(timings.Get("prompt_n").Int())
	u.Tokens.OutputTokens = int(timings.Get("predicted_n").Int())
	u.Tokens.PromptPerSecond = timings.Get("prompt_per_second").Float()
	u.Tokens.TokensPerSecond = timings.Get("predicted_per_second").Float()
	if ms := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float()); ms > u.DurationMs {
		u.DurationMs = ms
	}
	if v := timings.Get("cache_n"); v.Exists() {
		u.Tokens.CachedTokens = int(v.Int())
	}
	if drafted, accepted := timings.Get("draft_n"), timings.Get("draft_n_accepted"); drafted.Exists() && accepted.Exists() {
		u.Tokens.DraftTokens = int(drafted.Int())
		u.Tokens.DraftAccTokens = int(accepted.Int())
	}
}
