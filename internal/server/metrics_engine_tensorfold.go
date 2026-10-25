package server

import "github.com/tidwall/gjson"

// tensorfoldExtractor reads TensorFold's engine metrics and speculative
// counters. TensorFold emits its metrics on the final stream chunk, before
// the usage-only chunk.
type tensorfoldExtractor struct {
	tensorfold  gjson.Result
	speculative gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor { return &tensorfoldExtractor{} })
}

func (*tensorfoldExtractor) Name() string  { return "tensorfold" }
func (*tensorfoldExtractor) Priority() int { return 50 }

func (e *tensorfoldExtractor) Observe(obj gjson.Result) bool {
	found := false
	if tensorfold := obj.Get("tensorfold"); tensorfold.Exists() {
		e.tensorfold = tensorfold
		found = true
	}
	if speculative := obj.Get("speculative"); speculative.Exists() {
		e.speculative = speculative
		found = true
	}
	return found
}

func (e *tensorfoldExtractor) Apply(acc *metricsAccumulator) {
	if e.tensorfold.Exists() {
		tensorfold := e.tensorfold
		if v := tensorfold.Get("tokens_per_second"); v.Type == gjson.Number {
			acc.tokensPerSecond = v.Float()
		}
		if v := tensorfold.Get("seconds"); v.Type == gjson.Number {
			acc.setDurationAtLeast(int(v.Float() * 1000))
		}
		if acc.inputTokens > 0 {
			cached := acc.cachedTokens
			if cached < 0 {
				cached = 0
			}
			// prefill_seconds is the pure prefill time; time_to_first_token is
			// the fallback when prefill is not reported.
			if v := tensorfold.Get("prefill_seconds"); v.Type == gjson.Number && v.Float() > 0 {
				acc.promptPerSecond = float64(acc.inputTokens-cached) / v.Float()
			} else if v := tensorfold.Get("time_to_first_token"); v.Type == gjson.Number && v.Float() > 0 {
				acc.promptPerSecond = float64(acc.inputTokens-cached) / v.Float()
			}
		}
	}

	// TensorFold tolerates speculative counters nested under tensorfold for
	// older responses; a sibling object takes precedence.
	speculative := e.speculative
	if !speculative.Exists() {
		speculative = e.tensorfold.Get("speculative")
	}
	if speculative.Exists() {
		drafted := speculative.Get("drafted")
		accepted := speculative.Get("accepted")
		if drafted.Exists() && accepted.Exists() {
			acc.draftTokens = int(drafted.Int())
			acc.draftAccTokens = int(accepted.Int())
		}
	}
}
