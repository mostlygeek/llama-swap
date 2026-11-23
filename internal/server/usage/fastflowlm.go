package usage

import "github.com/tidwall/gjson"

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
