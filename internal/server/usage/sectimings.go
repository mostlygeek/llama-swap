package usage

import (
	"math"

	"github.com/tidwall/gjson"
)

// applySecondsTimings applies a timings block of durations in seconds, as
// sent by 1Cat-vLLM nested under usage ("usage.timings"). Prompt rate is
// the uncached prompt tokens over prefill_s, generation rate the output
// tokens over decode_s (the same phase math as applyTensorFold). Each rate
// only replaces the value that is still unknown, so a more precise source
// (vLLM's top-level metrics block, llama-server's timings) keeps
// precedence. A non-positive, missing or non-finite duration leaves the
// rate unknown.
func applySecondsTimings(u *Usage, timings gjson.Result) {
	if !timings.Exists() {
		return
	}
	u.Found = true
	if u.Tokens.PromptPerSecond < 0 {
		if secs := timings.Get("prefill_s").Float(); secs > 0 {
			// Same phase math as applyVLLMMetrics: uncached prompt tokens
			// over the prefill time, 0 when the prompt is fully cached.
			uncached := u.Tokens.InputTokens - max(u.Tokens.CachedTokens, 0)
			rate := float64(uncached) / secs
			if !math.IsNaN(rate) && !math.IsInf(rate, 0) {
				u.Tokens.PromptPerSecond = rate
			}
		}
	}
	if u.Tokens.TokensPerSecond < 0 {
		if secs := timings.Get("decode_s").Float(); secs > 0 {
			if rate, ok := tokensPerSecond(u.Tokens.OutputTokens, secs); ok {
				u.Tokens.TokensPerSecond = rate
			}
		}
	}
}
