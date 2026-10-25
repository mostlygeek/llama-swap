package server

import "github.com/tidwall/gjson"

// omlxExtractor reads oMLX's throughput fields, which it embeds in the OpenAI
// usage block under long-form names.
type omlxExtractor struct {
	usage gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor { return &omlxExtractor{} })
}

func (*omlxExtractor) Name() string  { return "omlx" }
func (*omlxExtractor) Priority() int { return 10 }

func (e *omlxExtractor) Observe(obj gjson.Result) bool {
	usage := findUsage(obj)
	if !usage.Exists() {
		return false
	}
	e.usage = usage
	return usage.Get("prompt_tokens_per_second").Exists() ||
		usage.Get("generation_tokens_per_second").Exists()
}

func (e *omlxExtractor) Apply(acc *metricsAccumulator) {
	if !e.usage.Exists() {
		return
	}
	if v := e.usage.Get("prompt_tokens_per_second"); v.Type == gjson.Number {
		acc.promptPerSecond = v.Float()
	}
	if v := e.usage.Get("generation_tokens_per_second"); v.Type == gjson.Number {
		acc.tokensPerSecond = v.Float()
	}
}
