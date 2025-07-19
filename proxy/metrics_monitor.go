package proxy

import (
	"context"
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
	return TokenMetricsEventID // defined in events.go
}

// MetricsMonitor parses llama-server output for token statistics
type MetricsMonitor struct {
	mu          sync.RWMutex
	metrics     []TokenMetrics
	maxMetrics  int
	nextID      int
	debugLogger *LogMonitor
	eventbus    *event.Dispatcher
}

// NewMetricsParser creates a new metrics parser
func NewMetricsParser(config *Config, debugLogger *LogMonitor) *MetricsMonitor {
	maxMetrics := config.MetricsMaxInMemory
	if maxMetrics <= 0 {
		maxMetrics = 1000 // Default fallback
	}

	mp := &MetricsMonitor{
		maxMetrics:  maxMetrics,
		debugLogger: debugLogger,
		eventbus:    event.NewDispatcherConfig(maxMetrics),
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

	// Publish event
	event.Publish(mp.eventbus, TokenMetricsEvent{Metrics: metric})
}

// GetMetricsJSON returns metrics as JSON
func (mp *MetricsMonitor) GetMetricsJSON() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return json.Marshal(mp.metrics)
}

// GetMetricsJSONByLines returns metrics as JSON lines
func (mp *MetricsMonitor) GetMetricsJSONByLines() ([]byte, error) {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	var result []byte
	for _, metric := range mp.metrics {
		jsonData, err := json.Marshal(metric)
		if err != nil {
			return nil, err
		}
		result = append(result, jsonData...)
		result = append(result, '\n')
	}
	return result, nil
}

// GetMetrics returns a copy of the current metrics
func (mp *MetricsMonitor) GetMetrics() []TokenMetrics {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	result := make([]TokenMetrics, len(mp.metrics))
	copy(result, mp.metrics)
	return result
}

// SubscribeToMetrics subscribes to new metrics events
func (mp *MetricsMonitor) SubscribeToMetrics(callback func(TokenMetricsEvent)) context.CancelFunc {
	return event.Subscribe(mp.eventbus, callback)
}

// Close closes the event dispatcher
func (mp *MetricsMonitor) Close() error {
	return mp.eventbus.Close()
}
