package server

import "github.com/tidwall/gjson"

// llamacppExtractor reads llama-server's timings block.
type llamacppExtractor struct {
	timings gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor { return &llamacppExtractor{} })
}

func (*llamacppExtractor) Name() string  { return "llamacpp" }
func (*llamacppExtractor) Priority() int { return 30 }

func (e *llamacppExtractor) Observe(obj gjson.Result) bool {
	timings := obj.Get("timings")
	if !timings.Exists() {
		return false
	}
	e.timings = timings
	return true
}

func (e *llamacppExtractor) Apply(acc *metricsAccumulator) {
	if !e.timings.Exists() {
		return
	}

	timings := e.timings
	acc.inputTokens = timings.Get("prompt_n").Int()
	acc.outputTokens = timings.Get("predicted_n").Int()
	acc.promptPerSecond = timings.Get("prompt_per_second").Float()
	acc.tokensPerSecond = timings.Get("predicted_per_second").Float()
	acc.setDurationAtLeast(int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float()))
	if cached := timings.Get("cache_n"); cached.Exists() {
		acc.cachedTokens = cached.Int()
	}
	if timings.Get("draft_n").Exists() && timings.Get("draft_n_accepted").Exists() {
		acc.draftTokens = int(timings.Get("draft_n").Int())
		acc.draftAccTokens = int(timings.Get("draft_n_accepted").Int())
	}
}
