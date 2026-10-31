package server

import (
	"time"

	"github.com/tidwall/gjson"
)

// omlxExtractor reads oMLX usage: the API counts plus the rates oMLX adds
// inside the same usage block.
type omlxExtractor struct{}

func (omlxExtractor) newParser(path string, start time.Time) usageParser {
	return &omlxParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type omlxParser struct {
	start time.Time
	read  apiReader
	api   apiUsage
	block gjson.Result // last usage block
}

func (p *omlxParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if b := ev.Get("usage"); b.IsObject() {
		p.block = b
	}
}

func (p *omlxParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyOMLXRates(&u, p.block)
	return u
}

// applyOMLXRates reads prompt_tokens_per_second and
// generation_tokens_per_second from an oMLX usage block.
func applyOMLXRates(u *Usage, block gjson.Result) {
	if v := block.Get("prompt_tokens_per_second"); v.Type == gjson.Number {
		u.Tokens.PromptPerSecond = v.Float()
	}
	if v := block.Get("generation_tokens_per_second"); v.Type == gjson.Number {
		u.Tokens.TokensPerSecond = v.Float()
	}
}
