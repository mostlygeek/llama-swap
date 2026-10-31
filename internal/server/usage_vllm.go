package server

import (
	"time"

	"github.com/tidwall/gjson"
)

// vllmExtractor reads the API usage block plus vLLM's metrics block.
type vllmExtractor struct{}

func (vllmExtractor) newParser(path string, start time.Time) usageParser {
	return &vllmParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type vllmParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	metrics gjson.Result
}

func (p *vllmParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if m := ev.Get("metrics"); m.IsObject() {
		p.metrics = m
	}
}

func (p *vllmParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyVLLMMetrics(&u, p.metrics)
	return u
}

// applyVLLMMetrics applies a vLLM response metrics block. The prompt rate
// is the uncached prompt tokens over the prefill time, which is
// time_to_first_token_ms minus queue_time_ms (vLLM measures time to first
// token from arrival, so it includes time spent queued), the generation
// rate is 1000 / mean_itl_ms, and speculative_decoding carries draft counts
// when vLLM runs with --per-request-spec-decode-metrics (#1032). Both draft
// counters are required so the acceptance rate is never derived from a
// half-filled object.
func applyVLLMMetrics(u *Usage, metrics gjson.Result) {
	if !metrics.Exists() {
		return
	}
	u.Found = true
	if spec := metrics.Get("speculative_decoding"); spec.Exists() {
		drafted, accepted := spec.Get("num_draft_tokens"), spec.Get("num_accepted_draft_tokens")
		if drafted.Exists() && accepted.Exists() {
			u.Tokens.DraftTokens = int(drafted.Int())
			u.Tokens.DraftAccTokens = int(accepted.Int())
		}
	}
	if ttft := metrics.Get("time_to_first_token_ms").Float(); ttft > 0 {
		prefill := ttft
		if queued := metrics.Get("queue_time_ms").Float(); queued > 0 && queued < ttft {
			prefill -= queued
		}
		cached := max(u.Tokens.CachedTokens, 0)
		u.Tokens.PromptPerSecond = float64(u.Tokens.InputTokens-cached) / (prefill / 1000)
	}
	if itl := metrics.Get("mean_itl_ms").Float(); itl > 0 {
		u.Tokens.TokensPerSecond = 1000 / itl
	}
}
