package server

import "github.com/tidwall/gjson"

// vllmExtractor reads vLLM's response metrics block, including speculative
// decoding counters when --per-request-spec-decode-metrics is enabled (#1032).
type vllmExtractor struct {
	metrics gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor { return &vllmExtractor{} })
}

func (*vllmExtractor) Name() string  { return "vllm" }
func (*vllmExtractor) Priority() int { return 40 }

func (e *vllmExtractor) Observe(obj gjson.Result) bool {
	metrics := obj.Get("metrics")
	if !metrics.Exists() {
		return false
	}
	e.metrics = metrics
	return true
}

func (e *vllmExtractor) Apply(acc *metricsAccumulator) {
	if !e.metrics.Exists() {
		return
	}

	// Both counters are required so the acceptance rate is never derived from
	// a half-filled object.
	if spec := e.metrics.Get("speculative_decoding"); spec.Exists() {
		drafted := spec.Get("num_draft_tokens")
		accepted := spec.Get("num_accepted_draft_tokens")
		if drafted.Exists() && accepted.Exists() {
			acc.draftTokens = int(drafted.Int())
			acc.draftAccTokens = int(accepted.Int())
		}
	}

	if ttft := e.metrics.Get("time_to_first_token_ms"); ttft.Exists() && ttft.Float() > 0 {
		cached := acc.cachedTokens
		if cached < 0 {
			cached = 0
		}
		acc.promptPerSecond = float64(acc.inputTokens-cached) / (ttft.Float() / 1000)
	}
	if itl := e.metrics.Get("mean_itl_ms"); itl.Exists() && itl.Float() > 0 {
		acc.tokensPerSecond = 1000 / itl.Float()
	}
}
