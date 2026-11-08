package usage

import (
	"math"
	"time"

	"github.com/tidwall/gjson"
)

// tensorFoldExtractor reads TensorFold usage: the OpenAI counts plus the
// top-level tensorfold object, which carries phase durations, the
// generation rate, and the cached prompt token count on older servers. In
// a stream the object arrives on the finish_reason chunk and usage follows
// in a later chunk, so both are retained.
type tensorFoldExtractor struct{}

func (tensorFoldExtractor) newParser(path string, start time.Time) usageParser {
	return &tensorFoldParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type tensorFoldParser struct {
	start      time.Time
	read       apiReader
	api        apiUsage
	tensorfold gjson.Result
}

func (p *tensorFoldParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if tf := ev.Get("tensorfold"); tf.IsObject() {
		p.tensorfold = tf
	}
}

func (p *tensorFoldParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTensorFold(&u, p.tensorfold)
	return u
}

// applyTensorFold applies a tensorfold object (#1226). cached replaces the
// usage-derived cached count. The prompt rate is the uncached prompt tokens
// over the prefill duration ("prefill_s" in #1226, "prefill_seconds" in
// current servers). The generation rate is the server-reported
// tokens_per_second when present, otherwise output tokens over decode_s;
// "seconds" covers the whole generation including prefill and is not a
// decode duration. A duration or rate that is missing, not a number, not
// positive, or not finite leaves the rate unknown. The phase durations do
// not change DurationMs, which stays the wall time of the request.
func applyTensorFold(u *Usage, tensorfold gjson.Result) {
	if !tensorfold.Exists() {
		return
	}
	u.Found = true
	if v := tensorfold.Get("cached"); v.Type == gjson.Number && v.Int() >= 0 {
		u.Tokens.CachedTokens = int(v.Int())
	}
	prefill := tensorfold.Get("prefill_s")
	if prefill.Type != gjson.Number {
		prefill = tensorfold.Get("prefill_seconds")
	}
	if prefill.Type == gjson.Number {
		uncached := u.Tokens.InputTokens - max(u.Tokens.CachedTokens, 0)
		if rate, ok := tokensPerSecond(uncached, prefill.Float()); ok {
			u.Tokens.PromptPerSecond = rate
		}
	}
	if tps := tensorfold.Get("tokens_per_second"); tps.Type == gjson.Number && positiveFinite(tps.Float()) {
		u.Tokens.TokensPerSecond = tps.Float()
	} else if secs := tensorfold.Get("decode_s"); secs.Type == gjson.Number {
		if rate, ok := tokensPerSecond(u.Tokens.OutputTokens, secs.Float()); ok {
			u.Tokens.TokensPerSecond = rate
		}
	}
}

// positiveFinite reports whether f is a usable rate.
func positiveFinite(f float64) bool {
	return f > 0 && !math.IsInf(f, 0) && !math.IsNaN(f)
}

// tokensPerSecond returns tokens / seconds when both are positive and the
// result is finite.
func tokensPerSecond(tokens int, seconds float64) (float64, bool) {
	if tokens <= 0 || seconds <= 0 {
		return 0, false
	}
	rate := float64(tokens) / seconds
	if math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0, false
	}
	return rate, true
}
