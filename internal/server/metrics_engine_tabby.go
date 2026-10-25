package server

import "github.com/tidwall/gjson"

// tabbyExtractor reads TabbyAPI's prompt/completion rates, which it embeds in
// the OpenAI usage block.
type tabbyExtractor struct {
	usage gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor { return &tabbyExtractor{} })
}

func (*tabbyExtractor) Name() string  { return "tabbyapi" }
func (*tabbyExtractor) Priority() int { return 20 }

func (e *tabbyExtractor) Observe(obj gjson.Result) bool {
	usage := findUsage(obj)
	if !usage.Exists() {
		return false
	}
	e.usage = usage
	return usage.Get("prompt_tokens_per_sec").Exists() ||
		usage.Get("completion_tokens_per_sec").Exists()
}

func (e *tabbyExtractor) Apply(acc *metricsAccumulator) {
	if !e.usage.Exists() {
		return
	}
	if v := e.usage.Get("prompt_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
		acc.promptPerSecond = v.Float()
	}
	if v := e.usage.Get("completion_tokens_per_sec"); v.Exists() && v.Type != gjson.Null {
		acc.tokensPerSecond = v.Float()
	}
}
