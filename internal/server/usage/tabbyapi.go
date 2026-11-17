package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// tabbyAPIExtractor reads TabbyAPI usage: the OpenAI counts plus rates and
// total time that TabbyAPI adds inside the same usage block.
type tabbyAPIExtractor struct{}

func (tabbyAPIExtractor) newParser(_ string, start time.Time) usageParser {
	return &tabbyAPIParser{start: start, api: newAPIUsage()}
}

type tabbyAPIParser struct {
	start time.Time
	api   apiUsage
	block gjson.Result // last usage block
}

func (p *tabbyAPIParser) event(ev gjson.Result) {
	readOpenAIUsage(ev, &p.api)
	if b := ev.Get("usage"); b.IsObject() {
		p.block = b
	}
}

func (p *tabbyAPIParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTabbyAPIRates(&u, p.block)
	return u
}

// applyTabbyAPIRates reads prompt_tokens_per_sec, completion_tokens_per_sec
// and total_time (seconds) from a TabbyAPI usage block. total_time replaces
// the duration when it is longer than wall time.
func applyTabbyAPIRates(u *Usage, block gjson.Result) {
	if v := block.Get("prompt_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
		u.Tokens.PromptPerSecond = v.Float()
	}
	if v := block.Get("completion_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
		u.Tokens.TokensPerSecond = v.Float()
	}
	if v := block.Get("total_time"); v.Exists() {
		if ms := int(v.Float() * 1000); ms > u.DurationMs {
			u.DurationMs = ms
		}
	}
}
