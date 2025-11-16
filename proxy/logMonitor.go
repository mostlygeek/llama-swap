package proxy

import (
	"container/ring"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/event"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

type LogMonitor struct {
	eventbus *event.Dispatcher
	mu       sync.RWMutex
	buffer   *ring.Ring
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
		buffer:     ring.New(10 * 1024), // keep 10KB of buffered logs
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
	bufferCopy := make([]byte, len(p))
	copy(bufferCopy, p)
	w.buffer.Value = bufferCopy
	w.buffer = w.buffer.Next()
	w.bufferMu.Unlock()

	w.broadcast(bufferCopy)
	return n, nil
}

func (w *LogMonitor) GetHistory() []byte {
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()

	var history []byte
	w.buffer.Do(func(p any) {
		if p != nil {
			if content, ok := p.([]byte); ok {
				history = append(history, content...)
			}
		}
	})
	return history
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
