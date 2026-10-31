package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// radianceExtractor reads radiance usage. Radiance reports a llama-server
// style timings block (including draft_n and draft_n_accepted) and adds
// total_ms, the whole request time, which is used as the duration when it
// is longer than prompt_ms + predicted_ms.
type radianceExtractor struct{}

func (radianceExtractor) newParser(path string, start time.Time) usageParser {
	return &radianceParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type radianceParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings gjson.Result
}

func (p *radianceParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if t := ev.Get("timings"); t.IsObject() {
		p.timings = t
	}
}

func (p *radianceParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTimings(&u, p.timings)
	applyRadianceTotal(&u, p.timings)
	return u
}

// applyRadianceTotal uses total_ms from a radiance timings block as the
// duration when it is longer than what is already measured.
func applyRadianceTotal(u *Usage, timings gjson.Result) {
	if v := timings.Get("total_ms"); v.Type == gjson.Number {
		if ms := int(v.Float()); ms > u.DurationMs {
			u.DurationMs = ms
		}
	}
}
