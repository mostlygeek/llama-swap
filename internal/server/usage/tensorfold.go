package usage

import (
	"math"
	"time"

	"github.com/tidwall/gjson"
)

// tensorFoldExtractor reads TensorFold usage: the OpenAI counts plus the
// top-level tensorfold object, which carries phase durations and the cached
// prompt token count. In a stream the object arrives on the finish_reason
// chunk and usage follows in a later chunk, so both are retained.
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
// over prefill_s and the generation rate is the output tokens over
// decode_s. A duration that is missing, not a number, not positive, or so
// small that the rate overflows leaves the rate unknown. The phase durations
// do not change DurationMs, which stays the wall time of the request.
func applyTensorFold(u *Usage, tensorfold gjson.Result) {
	if !tensorfold.Exists() {
		return
	}
	u.Found = true
	if v := tensorfold.Get("cached"); v.Type == gjson.Number && v.Int() >= 0 {
		u.Tokens.CachedTokens = int(v.Int())
	}
	if secs := tensorfold.Get("prefill_s"); secs.Type == gjson.Number {
		uncached := u.Tokens.InputTokens - max(u.Tokens.CachedTokens, 0)
		if rate, ok := tokensPerSecond(uncached, secs.Float()); ok {
			u.Tokens.PromptPerSecond = rate
		}
	}
	if secs := tensorfold.Get("decode_s"); secs.Type == gjson.Number {
		if rate, ok := tokensPerSecond(u.Tokens.OutputTokens, secs.Float()); ok {
			u.Tokens.TokensPerSecond = rate
		}
	}
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
