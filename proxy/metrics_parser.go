package proxy

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/event"
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

// TokenMetricsEvent represents a token metrics event
type TokenMetricsEvent struct {
	Metrics TokenMetrics
}

func (e TokenMetricsEvent) Type() uint32 {
	return TokenMetricsEventID
}

// MetricsParser parses llama-server output for token statistics
type MetricsParser struct {
	mu                sync.RWMutex
	metrics           []TokenMetrics
	maxMetrics        int
	promptEvalRegex   *regexp.Regexp
	evalRegex         *regexp.Regexp
	debugLogger       *LogMonitor
	eventbus          *event.Dispatcher
	useServerResponse bool
}

// NewMetricsParser creates a new metrics parser
func NewMetricsParser(config *Config, debugLogger *LogMonitor) *MetricsParser {
	maxMetrics := config.MetricsMaxInMemory
	if maxMetrics <= 0 {
		maxMetrics = 1000 // Default fallback
	}

	mp := &MetricsParser{
		maxMetrics:        maxMetrics,
		promptEvalRegex:   regexp.MustCompile(`prompt eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
		evalRegex:         regexp.MustCompile(`eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
		debugLogger:       debugLogger,
		eventbus:          event.NewDispatcherConfig(1000),
		useServerResponse: config.MetricsUseServerResponse,
	}

	return mp
}

// addMetrics adds a new metric to the collection and publishes an event
func (mp *MetricsParser) addMetrics(metric TokenMetrics) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	mp.metrics = append(mp.metrics, metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}

	// Publish event
	event.Publish(mp.eventbus, TokenMetricsEvent{Metrics: metric})
}

// ParseLogLine parses a single log line for token metrics
func (mp *MetricsParser) ParseLogLine(line string, modelName string) {
	if matches := mp.promptEvalRegex.FindStringSubmatch(line); matches != nil {
		// Check for prompt evaluation metrics (input tokens)
		durationMs, _ := strconv.ParseFloat(matches[1], 64)
		tokens, _ := strconv.Atoi(matches[2])
		tokensPerSecond, _ := strconv.ParseFloat(matches[4], 64)

		metrics := TokenMetrics{
			Timestamp:       time.Now(),
			Model:           modelName,
			InputTokens:     tokens,
			OutputTokens:    0,
			TokensPerSecond: tokensPerSecond,
			DurationMs:      int(durationMs),
		}
		mp.addMetrics(metrics)
	} else if matches := mp.evalRegex.FindStringSubmatch(line); matches != nil {
		// Check for evaluation metrics (output tokens)
		durationMs, _ := strconv.ParseFloat(matches[1], 64)
		tokens, _ := strconv.Atoi(matches[2])
		tokensPerSecond, _ := strconv.ParseFloat(matches[4], 64)

		metrics := TokenMetrics{
			Timestamp:       time.Now(),
			Model:           modelName,
			InputTokens:     0,
			OutputTokens:    tokens,
			TokensPerSecond: tokensPerSecond,
			DurationMs:      int(durationMs),
		}
		mp.addMetrics(metrics)
	}
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsParser) GetMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}

// GetMetrics returns a copy of the current metrics
func (mp *MetricsParser) GetMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// SubscribeToMetrics subscribes to new metrics events
func (mp *MetricsParser) SubscribeToMetrics(callback func(TokenMetricsEvent)) context.CancelFunc {
	return event.Subscribe(mp.eventbus, callback)
}

// Close closes the event dispatcher
func (mp *MetricsParser) Close() error {
	return mp.eventbus.Close()
}
