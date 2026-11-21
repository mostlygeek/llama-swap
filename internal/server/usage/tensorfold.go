package usage

import (
	"math"
	"time"

	"github.com/tidwall/gjson"
)

// tensorFoldExtractor reads TensorFold usage: the OpenAI counts plus the
// top-level tensorfold object, which carries phase durations, the
// generation rate, and the cached prompt token count on older servers, and
// the speculative object beside it, which carries the draft counters. In a
// stream the objects arrive on the finish_reason chunk and usage follows in
// a later chunk, so both are retained.
type tensorFoldExtractor struct{}

func (tensorFoldExtractor) newParser(path string, start time.Time) usageParser {
	return &tensorFoldParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type tensorFoldParser struct {
	start       time.Time
	read        apiReader
	api         apiUsage
	tensorfold  gjson.Result
	speculative gjson.Result
}

func (p *tensorFoldParser) event(ev gjson.Result) {
	p.read(ev, &p.api)
	if tf := ev.Get("tensorfold"); tf.IsObject() {
		p.tensorfold = tf
	}
	if sp := ev.Get("speculative"); sp.IsObject() {
		p.speculative = sp
	}
}

func (p *tensorFoldParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTensorFold(&u, p.tensorfold, p.speculative)
	return u
}

// applyTensorFold applies a tensorfold object (#1226) and the speculative
// object beside it. cached replaces the usage-derived cached count. The
// prompt rate is the uncached prompt tokens over the prefill duration
// ("prefill_s" in #1226, "prefill_seconds" in current servers). The
// generation rate is the server-reported tokens_per_second when present,
// otherwise output tokens over decode_s. Python 0.6.6's "seconds" covers the
// whole request and is not a decode duration; native servers call their
// decode duration "seconds" but always report tokens_per_second, so it is not
// read as a fallback either. The draft counters live in the speculative
// object on native servers and inside the block on older ones (see
// applyTensorFoldDrafts). A duration or rate that is missing, not a number,
// not positive, or not finite leaves the rate unknown. The phase durations do
// not change DurationMs, which stays the wall time of the request.
func applyTensorFold(u *Usage, tensorfold, speculative gjson.Result) {
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
	applyTensorFoldDrafts(u, speculative, tensorfold)
}

// applyTensorFoldDrafts maps the speculative counters to DraftTokens and
// DraftAccTokens. Native servers put drafted and accepted in a top-level
// speculative object beside the block; older ones keep them inside the
// tensorfold block. Both counters must be counts and accepted must fit
// drafted, the same test the server's own benchmark reader applies, so a
// half-filled or inconsistent object never yields an acceptance rate. Zero is
// a real answer: a native server reports drafted 0, accepted 0 when drafting
// is off.
func applyTensorFoldDrafts(u *Usage, speculative, tensorfold gjson.Result) {
	drafted, accepted := speculative.Get("drafted"), speculative.Get("accepted")
	if drafted.Type != gjson.Number || accepted.Type != gjson.Number {
		drafted, accepted = tensorfold.Get("drafted"), tensorfold.Get("accepted")
	}
	if drafted.Type != gjson.Number || accepted.Type != gjson.Number {
		return
	}
	d, a := drafted.Int(), accepted.Int()
	if d < 0 || a < 0 || a > d {
		return
	}
	u.Tokens.DraftTokens = int(d)
	u.Tokens.DraftAccTokens = int(a)
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
