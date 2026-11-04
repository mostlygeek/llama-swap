package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// hipfireExtractor reads API token counts and Hipfire's timings. The usage
// block already includes cached tokens in the prompt count. decode_tok_s
// excludes prefill; hipfire.tok_s includes it and is not a decode fallback.
// tau/cycles do not provide both drafted and accepted token counts, so
// speculative counters remain unknown.
type hipfireExtractor struct{}

func (hipfireExtractor) newParser(path string, start time.Time) usageParser {
	return &hipfireParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type hipfireParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings gjson.Result
}

func (p *hipfireParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if t := ev.Get("timings"); t.IsObject() {
		p.timings = t
	}
}

func (p *hipfireParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyHipfireTimings(&u, p.timings)
	return u
}

func applyHipfireTimings(u *Usage, timings gjson.Result) {
	if v := timings.Get("prefill_tok_s"); v.Type == gjson.Number && v.Float() >= 0 {
		u.Tokens.PromptPerSecond = v.Float()
		u.Found = true
	}
	if v := timings.Get("decode_tok_s"); v.Type == gjson.Number && v.Float() >= 0 {
		u.Tokens.TokensPerSecond = v.Float()
		u.Found = true
	}
	if v := timings.Get("latency_ms"); v.Type == gjson.Number && v.Float() > float64(u.DurationMs) {
		u.DurationMs = int(v.Float())
	}
}
