package proxy

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/event"
)

// circularBuffer is a fixed-size circular byte buffer that overwrites
// oldest data when full. It provides O(1) writes and O(n) reads.
type circularBuffer struct {
	data []byte // pre-allocated capacity
	head int    // next write position
	size int    // current number of bytes stored (0 to cap)
}

func newCircularBuffer(capacity int) *circularBuffer {
	return &circularBuffer{
		data: make([]byte, capacity),
		head: 0,
		size: 0,
	}
}

// Write appends bytes to the buffer, overwriting oldest data when full.
// Data is copied into the internal buffer (not stored by reference).
func (cb *circularBuffer) Write(p []byte) {
	if len(p) == 0 {
		return
	}

	cap := len(cb.data)

	// If input is larger than capacity, only keep the last cap bytes
	if len(p) >= cap {
		copy(cb.data, p[len(p)-cap:])
		cb.head = 0
		cb.size = cap
		return
	}

	// Calculate how much space is available from head to end of buffer
	firstPart := cap - cb.head
	if firstPart >= len(p) {
		// All data fits without wrapping
		copy(cb.data[cb.head:], p)
		cb.head = (cb.head + len(p)) % cap
	} else {
		// Data wraps around
		copy(cb.data[cb.head:], p[:firstPart])
		copy(cb.data[:len(p)-firstPart], p[firstPart:])
		cb.head = len(p) - firstPart
	}

	// Update size
	cb.size += len(p)
	if cb.size > cap {
		cb.size = cap
	}
}

// GetHistory returns all buffered data in correct order (oldest to newest).
// Returns a new slice (copy), not a view into internal buffer.
func (cb *circularBuffer) GetHistory() []byte {
	if cb.size == 0 {
		return nil
	}

	result := make([]byte, cb.size)
	cap := len(cb.data)

	// Calculate start position (oldest data)
	start := (cb.head - cb.size + cap) % cap

	if start+cb.size <= cap {
		// Data is contiguous, single copy
		copy(result, cb.data[start:start+cb.size])
	} else {
		// Data wraps around, two copies
		firstPart := cap - start
		copy(result[:firstPart], cb.data[start:])
		copy(result[firstPart:], cb.data[:cb.size-firstPart])
	}

	return result
}

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError

	LogBufferSize = 100 * 1024
)

type LogMonitor struct {
	eventbus *event.Dispatcher
	mu       sync.RWMutex
	buffer   *circularBuffer
	bufferMu sync.RWMutex

	// typically this can be os.Stdout
	stdout io.Writer

	// logging levels
	level  LogLevel
	prefix string

	// timestamps
	timeFormat string
}

func NewLogMonitor() *LogMonitor {
	return NewLogMonitorWriter(os.Stdout)
}

func NewLogMonitorWriter(stdout io.Writer) *LogMonitor {
	return &LogMonitor{
		eventbus:   event.NewDispatcherConfig(1000),
		buffer:     nil, // lazy initialized on first Write
		stdout:     stdout,
		level:      LevelInfo,
		prefix:     "",
		timeFormat: "",
	}
}

func (w *LogMonitor) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	n, err = w.stdout.Write(p)
	if err != nil {
		return n, err
	}

	w.bufferMu.Lock()
	if w.buffer == nil {
		w.buffer = newCircularBuffer(LogBufferSize)
	}
	w.buffer.Write(p)
	w.bufferMu.Unlock()

	// Make a copy for broadcast to preserve immutability
	bufferCopy := make([]byte, len(p))
	copy(bufferCopy, p)
	w.broadcast(bufferCopy)
	return n, nil
}

func (w *LogMonitor) GetHistory() []byte {
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()
	if w.buffer == nil {
		return nil
	}
	return w.buffer.GetHistory()
}

// Clear releases the buffer memory, making it eligible for GC.
// The buffer will be lazily re-allocated on the next Write.
func (w *LogMonitor) Clear() {
	w.bufferMu.Lock()
	w.buffer = nil
	w.bufferMu.Unlock()
}

func (w *LogMonitor) OnLogData(callback func(data []byte)) context.CancelFunc {
	return event.Subscribe(w.eventbus, func(e LogDataEvent) {
		callback(e.Data)
	})
}

func (w *LogMonitor) broadcast(msg []byte) {
	event.Publish(w.eventbus, LogDataEvent{Data: msg})
}

func (w *LogMonitor) SetPrefix(prefix string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prefix = prefix
}

func (w *LogMonitor) SetLogLevel(level LogLevel) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.level = level
}

func (w *LogMonitor) SetLogTimeFormat(timeFormat string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timeFormat = timeFormat
}

func (w *LogMonitor) formatMessage(level string, msg string) []byte {
	prefix := ""
	if w.prefix != "" {
		prefix = fmt.Sprintf("[%s] ", w.prefix)
	}
	timestamp := ""
	if w.timeFormat != "" {
		timestamp = fmt.Sprintf("%s ", time.Now().Format(w.timeFormat))
	}
	return []byte(fmt.Sprintf("%s%s[%s] %s\n", timestamp, prefix, level, msg))
}

func (w *LogMonitor) log(level LogLevel, msg string) {
	if level < w.level {
		return
	}
	w.Write(w.formatMessage(level.String(), msg))
}

func (w *LogMonitor) Debug(msg string) {
	w.log(LevelDebug, msg)
}

func (w *LogMonitor) Info(msg string) {
	w.log(LevelInfo, msg)
}

func (w *LogMonitor) Warn(msg string) {
	w.log(LevelWarn, msg)
}

func (w *LogMonitor) Error(msg string) {
	w.log(LevelError, msg)
}

func (w *LogMonitor) Debugf(format string, args ...interface{}) {
	w.log(LevelDebug, fmt.Sprintf(format, args...))
}

func (w *LogMonitor) Infof(format string, args ...interface{}) {
	w.log(LevelInfo, fmt.Sprintf(format, args...))
}

func (w *LogMonitor) Warnf(format string, args ...interface{}) {
	w.log(LevelWarn, fmt.Sprintf(format, args...))
}

func (w *LogMonitor) Errorf(format string, args ...interface{}) {
	w.log(LevelError, fmt.Sprintf(format, args...))
}

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}
