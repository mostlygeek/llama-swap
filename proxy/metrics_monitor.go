package proxy

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/event"
)

// TokenMetrics represents parsed token statistics from llama-server logs
type TokenMetrics struct {
	ID              int       `json:"id"`
	Timestamp       time.Time `json:"timestamp"`
	Model           string    `json:"model"`
	CachedTokens    int       `json:"cache_tokens"`
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	PromptPerSecond float64   `json:"prompt_per_second"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	DurationMs      int       `json:"duration_ms"`
}

// TokenMetricsEvent represents a token metrics event
type TokenMetricsEvent struct {
	Metrics TokenMetrics
}

func (e TokenMetricsEvent) Type() uint32 {
	return TokenMetricsEventID // defined in events.go
}

// MetricsMonitor parses llama-server output for token statistics
type MetricsMonitor struct {
	mu         sync.RWMutex
	metrics    []TokenMetrics
	maxMetrics int
	nextID     int
}

func NewMetricsMonitor(config *Config) *MetricsMonitor {
	maxMetrics := config.MetricsMaxInMemory
	if maxMetrics <= 0 {
		maxMetrics = 1000 // Default fallback
	}

	mp := &MetricsMonitor{
		maxMetrics: maxMetrics,
	}

	return mp
}

// addMetrics adds a new metric to the collection and publishes an event
func (mp *MetricsMonitor) addMetrics(metric TokenMetrics) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	metric.ID = mp.nextID
	mp.nextID++
	mp.metrics = append(mp.metrics, metric)
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}
	event.Emit(TokenMetricsEvent{Metrics: metric})
}

// GetMetrics returns a copy of the current metrics
func (mp *MetricsMonitor) GetMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsMonitor) GetMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}
