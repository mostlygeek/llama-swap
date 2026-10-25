package server

import "github.com/tidwall/gjson"

// usagePaths lists the JSON paths where a per-event usage object can live.
var usagePaths = []string{"usage", "response.usage", "message.usage"}

// usageExtractor handles the OpenAI-compatible usage block shared by every
// engine. It accumulates token counts across streamed chunks and keeps the
// last block for the duration it reports.
type usageExtractor struct {
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	usage        gjson.Result
}

func init() {
	registerMetricsExtractor(func() metricsExtractor {
		return &usageExtractor{cachedTokens: -1}
	})
}

func (*usageExtractor) Name() string  { return "usage" }
func (*usageExtractor) Priority() int { return 0 }

func (e *usageExtractor) Observe(obj gjson.Result) bool {
	found := false
	for _, path := range usagePaths {
		usage := obj.Get(path)
		if !usage.Exists() {
			continue
		}
		found = true
		e.usage = usage

		input, output, cached, ok := extractUsageTokens(usage)
		if !ok {
			continue
		}
		// Streamed chunks may carry partial counts; keep the last non-zero
		// value for input/output and the last reported cache count.
		if input > 0 {
			e.inputTokens = input
		}
		if output > 0 {
			e.outputTokens = output
		}
		if cached >= 0 {
			e.cachedTokens = cached
		}
	}
	return found
}

func (e *usageExtractor) Apply(acc *metricsAccumulator) {
	if !e.usage.Exists() {
		return
	}
	acc.inputTokens = e.inputTokens
	acc.outputTokens = e.outputTokens
	acc.cachedTokens = e.cachedTokens

	if v := e.usage.Get("total_time"); v.Exists() {
		acc.setDurationAtLeast(int(v.Float() * 1000))
	}
}

// extractUsageTokens reads input/output/cached token counts from a usage
// gjson.Result, handling the field-name differences across endpoints.
func extractUsageTokens(usage gjson.Result) (input, output, cached int64, ok bool) {
	cached = -1
	if !usage.Exists() {
		return
	}

	if v := usage.Get("prompt_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens"); v.Exists() {
		input = v.Int()
		ok = true
	}

	if v := usage.Get("completion_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	} else if v := usage.Get("output_tokens"); v.Exists() {
		output = v.Int()
		ok = true
	}

	if v := usage.Get("cache_read_input_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("input_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	} else if v := usage.Get("prompt_tokens_details.cached_tokens"); v.Exists() {
		cached = v.Int()
		ok = true
	}
	return
}

// findUsage returns the last usage block an object carries.
func findUsage(obj gjson.Result) gjson.Result {
	var usage gjson.Result
	for _, path := range usagePaths {
		if u := obj.Get(path); u.Exists() {
			usage = u
		}
	}
	return usage
}
