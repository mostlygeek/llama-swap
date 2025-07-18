package proxy

import (
	"bufio"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenMetrics represents parsed token statistics from llama-server logs
type TokenMetrics struct {
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	TokensGenerated int       `json:"tokens_generated"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	PromptTokens    int       `json:"prompt_tokens,omitempty"`
	TotalTokens     int       `json:"total_tokens,omitempty"`
	DurationMs      int       `json:"duration_ms,omitempty"`
}

// MetricsParser parses llama-server output for token statistics
type MetricsParser struct {
	mu            sync.RWMutex
	metrics       []TokenMetrics
	maxMetrics    int
	modelName     string
	tokenRegex    *regexp.Regexp
	speedRegex    *regexp.Regexp
	promptRegex   *regexp.Regexp
	totalRegex    *regexp.Regexp
	durationRegex *regexp.Regexp
}

// NewMetricsParser creates a new metrics parser for a specific model
func NewMetricsParser(modelName string) *MetricsParser {
	return &MetricsParser{
		modelName:     modelName,
		maxMetrics:    1000, // Keep last 1000 metrics
		tokenRegex:    regexp.MustCompile(`(\d+)\s+tokens?`),
		speedRegex:    regexp.MustCompile(`(\d+\.?\d*)\s+t/s`),
		promptRegex:   regexp.MustCompile(`prompt:\s*(\d+)\s+tokens?`),
		totalRegex:    regexp.MustCompile(`total:\s*(\d+)\s+tokens?`),
		durationRegex: regexp.MustCompile(`(\d+)\s*ms`),
	}
}

// ParseLogLine parses a single log line for token metrics
func (mp *MetricsParser) ParseLogLine(line string) *TokenMetrics {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	// Look for common llama-server output patterns
	// Pattern 1: "llama_print_timings: prompt eval time = 123.45 ms / 45 tokens (33.33 ms per token, 30.00 tokens per second)"
	// Pattern 2: "llama_print_timings:        eval time = 234.56 ms / 67 tokens (3.50 ms per token, 285.71 tokens per second)"
	// Pattern 3: "prompt: 45 tokens, total: 112 tokens, t/s: 89.50"

	var tokensGenerated int
	var tokensPerSecond float64
	var promptTokens int
	var totalTokens int
	var durationMs int

	// Try to extract tokens and speed from various patterns
	if strings.Contains(line, "eval time") && strings.Contains(line, "tokens per second") {
		// Pattern: eval time = X ms / Y tokens (... Z tokens per second)
		parts := strings.Split(line, "tokens per second")
		if len(parts) >= 1 {
			// Extract tokens from "X ms / Y tokens"
			tokensMatch := regexp.MustCompile(`(\d+)\s+ms\s*/\s*(\d+)\s+tokens`).FindStringSubmatch(parts[0])
			if len(tokensMatch) >= 3 {
				durationMs, _ = strconv.Atoi(tokensMatch[1])
				tokensGenerated, _ = strconv.Atoi(tokensMatch[2])
			}

			// Extract speed from the end
			speedMatch := regexp.MustCompile(`(\d+\.?\d*)`).FindStringSubmatch(parts[len(parts)-1])
			if len(speedMatch) >= 2 {
				tokensPerSecond, _ = strconv.ParseFloat(speedMatch[1], 64)
			}
		}
	} else if strings.Contains(line, "t/s:") {
		// Pattern: prompt: X tokens, total: Y tokens, t/s: Z
		promptMatch := mp.promptRegex.FindStringSubmatch(line)
		if len(promptMatch) >= 2 {
			promptTokens, _ = strconv.Atoi(promptMatch[1])
		}

		totalMatch := mp.totalRegex.FindStringSubmatch(line)
		if len(totalMatch) >= 2 {
			totalTokens, _ = strconv.Atoi(totalMatch[1])
			tokensGenerated = totalTokens - promptTokens
		}

		speedMatch := regexp.MustCompile(`t/s:\s*(\d+\.?\d*)`).FindStringSubmatch(line)
		if len(speedMatch) >= 2 {
			tokensPerSecond, _ = strconv.ParseFloat(speedMatch[1], 64)
		}
	} else if strings.Contains(line, "decoded") && strings.Contains(line, "tokens in") {
		// Pattern: "decoded 123 tokens in 456.78ms"
		decodedMatch := regexp.MustCompile(`decoded\s+(\d+)\s+tokens?\s+in\s+(\d+\.?\d*)\s*ms`).FindStringSubmatch(line)
		if len(decodedMatch) >= 3 {
			tokensGenerated, _ = strconv.Atoi(decodedMatch[1])
			duration, _ := strconv.ParseFloat(decodedMatch[2], 64)
			durationMs = int(duration)
			if duration > 0 {
				tokensPerSecond = float64(tokensGenerated) / (duration / 1000.0)
			}
		}
	}

	// Only create metrics if we found meaningful data
	if tokensGenerated > 0 || tokensPerSecond > 0 {
		metric := &TokenMetrics{
			Timestamp:       time.Now(),
			Model:           mp.modelName,
			TokensGenerated: tokensGenerated,
			TokensPerSecond: tokensPerSecond,
			PromptTokens:    promptTokens,
			TotalTokens:     totalTokens,
			DurationMs:      durationMs,
		}

		mp.mu.Lock()
		defer mp.mu.Unlock()

		mp.metrics = append(mp.metrics, *metric)
		if len(mp.metrics) > mp.maxMetrics {
			mp.metrics = mp.metrics[1:] // Remove oldest
		}

		return metric
	}

	return nil
}

// ParseLogData parses multiple log lines for metrics
func (mp *MetricsParser) ParseLogData(data []byte) []TokenMetrics {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var newMetrics []TokenMetrics

	for scanner.Scan() {
		if metric := mp.ParseLogLine(scanner.Text()); metric != nil {
			newMetrics = append(newMetrics, *metric)
		}
	}

	return newMetrics
}

// GetMetrics returns all collected metrics
func (mp *MetricsParser) GetMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	// Return a copy
	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsParser) GetMetricsJSON() ([]byte, error) {
	metrics := mp.GetMetrics()
	return json.Marshal(metrics)
}

// GetSummary returns aggregated metrics summary
func (mp *MetricsParser) GetSummary() map[string]interface{} {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	if len(mp.metrics) == 0 {
		return map[string]interface{}{
			"total_requests":        0,
			"total_tokens":          0,
			"avg_tokens_per_second": 0,
			"max_tokens_per_second": 0,
		}
	}

	var totalTokens, totalDuration int
	var maxTPS float64

	for _, m := range mp.metrics {
		totalTokens += m.TokensGenerated
		totalDuration += m.DurationMs
		if m.TokensPerSecond > maxTPS {
			maxTPS = m.TokensPerSecond
		}
	}

	avgTPS := 0.0
	if totalDuration > 0 {
		avgTPS = float64(totalTokens) / (float64(totalDuration) / 1000.0)
	}

	return map[string]interface{}{
		"total_requests":           len(mp.metrics),
		"total_tokens":             totalTokens,
		"avg_tokens_per_second":    avgTPS,
		"max_tokens_per_second":    maxTPS,
		"latest_tokens_per_second": mp.metrics[len(mp.metrics)-1].TokensPerSecond,
	}
}
