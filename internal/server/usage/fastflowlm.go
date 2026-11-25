package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// fastFlowLMExtractor reads OpenAI usage and the rates added by FastFlowLM.
type fastFlowLMExtractor struct{}

func (fastFlowLMExtractor) newParser(path string, start time.Time) usageParser {
	return &fastFlowLMParser{
		apiParser: apiParser{start: start, read: apiReaderFor(path), api: newAPIUsage()},
	}
}

type fastFlowLMParser struct {
	apiParser
	block gjson.Result // last usage block
}

func (p *fastFlowLMParser) event(ev gjson.Result) {
	p.apiParser.event(ev)
	if b := ev.Get("usage"); b.IsObject() {
		p.block = b
	}
}

func (p *fastFlowLMParser) usage() Usage {
	u := p.apiParser.usage()
	applyFastFlowLMRates(&u, p.block)
	return u
}

// applyFastFlowLMRates reads the rates FLM adds to OpenAI chat usage.
// Prefill counts only uncached prompt tokens. Decode excludes prefill.
func applyFastFlowLMRates(u *Usage, block gjson.Result) {
	if v := block.Get("prefill_speed_tps"); v.Type == gjson.Number && v.Float() >= 0 {
		u.Tokens.PromptPerSecond = v.Float()
		u.Found = true
	}
	if v := block.Get("decoding_speed_tps"); v.Type == gjson.Number && v.Float() >= 0 {
		u.Tokens.TokensPerSecond = v.Float()
		u.Found = true
	}
}
