package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
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
	logPath           string
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
		logPath:           config.MetricsLogPath,
		promptEvalRegex:   regexp.MustCompile(`prompt eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
		evalRegex:         regexp.MustCompile(`eval time\s*=\s*(\d+(?:\.\d+)?)\s*ms\s*/\s*(\d+)\s*tokens\s*\(\s*(\d+(?:\.\d+)?)\s*ms per token,\s*(\d+(?:\.\d+)?)\s*tokens per second\s*\)`),
		debugLogger:       debugLogger,
		eventbus:          event.NewDispatcherConfig(1000),
		useServerResponse: config.MetricsUseServerResponse,
	}

	// Load existing metrics from file if path is provided
	if config.MetricsLogPath != "" {
		_ = mp.LoadMetrics() // Only warn, don't error as requested
		_ = mp.LoadMetrics()
	}

	return mp
}

// LoadMetrics loads metrics from the JSONL file
func (mp *MetricsParser) LoadMetrics() error {
	if mp.logPath == "" {
		return nil
	}

	file, err := os.Open(mp.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, which is fine
			return nil
		}
		if mp.debugLogger != nil {
			mp.debugLogger.Warnf("Failed to open metrics log file for reading: %v", err)
		}
		return err
	}
	defer file.Close()

	mp.mu.Lock()
	defer mp.mu.Unlock()

	// Use bufio.Scanner to read line by line
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}

		var metric TokenMetrics
		if err := json.Unmarshal([]byte(line), &metric); err != nil {
			if mp.debugLogger != nil {
				mp.debugLogger.Warnf("Skipping malformed metrics line %d: %v", lineNum, err)
			}
			continue
		}
		mp.metrics = append(mp.metrics, metric)
	}

	// Keep only the most recent metrics if we loaded too many
	if len(mp.metrics) > mp.maxMetrics {
		mp.metrics = mp.metrics[len(mp.metrics)-mp.maxMetrics:]
	}

	if err := scanner.Err(); err != nil && mp.debugLogger != nil {
		mp.debugLogger.Warnf("Error reading metrics log file: %v", err)
	}

	return scanner.Err()
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

	// Append to JSONL file if path is configured
	if mp.logPath != "" {
		mp.appendToFile(metric)
	}
}

// appendToFile appends a single metric to the JSONL file
func (mp *MetricsParser) appendToFile(metric TokenMetrics) {
	file, err := os.OpenFile(mp.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		if mp.debugLogger != nil {
			mp.debugLogger.Warnf("Failed to open metrics log file for appending: %v", err)
		}
		return
	}
	defer file.Close()

	jsonData, err := json.Marshal(metric)
	if err != nil {
		if mp.debugLogger != nil {
			mp.debugLogger.Warnf("Failed to marshal metrics data: %v", err)
		}
		return
	}

	// Append newline and write
	jsonData = append(jsonData, '\n')
	if _, err := file.Write(jsonData); err != nil {
		if mp.debugLogger != nil {
			mp.debugLogger.Warnf("Failed to write metrics to log file: %v", err)
		}
	}
	file.Write(jsonData)
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
