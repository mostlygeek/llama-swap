package proxy

import (
	"encoding/json"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// TokenMetrics represents parsed token statistics from llama-server logs
type TokenMetrics struct {
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	DurationMs      int       `json:"duration_ms"`
}

// MetricsParser parses llama-server output for token statistics
type MetricsParser struct {
	mu              sync.RWMutex
	metrics         []TokenMetrics
	maxMetrics      int
	promptEvalRegex *regexp.Regexp
	evalRegex       *regexp.Regexp
}

// NewMetricsParser creates a new metrics parser
func NewMetricsParser(maxMetrics int) *MetricsParser {
	if maxMetrics <= 0 {
		maxMetrics = 1000 // Default fallback
	}
	return &MetricsParser{
		maxMetrics:      maxMetrics,
		promptEvalRegex: regexp.MustCompile(`prompt eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
		evalRegex:       regexp.MustCompile(`eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
	}
}

// addMetric adds a new metric to the collection
func (mp *MetricsParser) addMetric(metric *TokenMetrics) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	mp.metrics = append(mp.metrics, *metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}
}

// ParseLogLine parses a single log line for token metrics
func (mp *MetricsParser) ParseLogLine(line string, modelName string) *TokenMetrics {
	// Check for prompt evaluation metrics (input tokens)
	if matches := mp.promptEvalRegex.FindStringSubmatch(line); matches != nil {
		durationMs, _ := strconv.ParseFloat(matches[1], 64)
		tokens, _ := strconv.Atoi(matches[2])
		tokensPerSecond, _ := strconv.ParseFloat(matches[4], 64)

		metric := &TokenMetrics{
			Timestamp:       time.Now(),
			Model:           modelName,
			OutputTokens:    0,
			TokensPerSecond: tokensPerSecond,
			InputTokens:     tokens,
			DurationMs:      int(durationMs),
		}

		mp.addMetric(metric)
		return metric
	}

	// Check for evaluation metrics (output tokens)
	if matches := mp.evalRegex.FindStringSubmatch(line); matches != nil {
		durationMs, _ := strconv.ParseFloat(matches[1], 64)
		tokens, _ := strconv.Atoi(matches[2])
		tokensPerSecond, _ := strconv.ParseFloat(matches[4], 64)

		metric := &TokenMetrics{
			Timestamp:       time.Now(),
			Model:           modelName,
			OutputTokens:    tokens,
			TokensPerSecond: tokensPerSecond,
			DurationMs:      int(durationMs),
		}

		mp.addMetric(metric)
		return metric
	}

	return nil
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsParser) GetMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}
