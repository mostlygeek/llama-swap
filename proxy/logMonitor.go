package proxy

import (
	"container/ring"
	"io"
	"os"
	"sync"
)

type LogMonitor struct {
	clients  map[chan []byte]bool
	mu       sync.RWMutex
	buffer   *ring.Ring
	bufferMu sync.RWMutex

	// typically this can be os.Stdout
	stdout io.Writer
}

func NewLogMonitor() *LogMonitor {
	return NewLogMonitorWriter(os.Stdout)
}

func NewLogMonitorWriter(stdout io.Writer) *LogMonitor {
	return &LogMonitor{
		clients: make(map[chan []byte]bool),
		buffer:  ring.New(10 * 1024), // keep 10KB of buffered logs
		stdout:  stdout,
	}
}

func (w *LogMonitor) Write(p []byte) (n int, err error) {
	n, err = w.stdout.Write(p)
	if err != nil {
		return n, err
	}

	w.bufferMu.Lock()
	w.buffer.Value = p
	w.buffer = w.buffer.Next()
	w.bufferMu.Unlock()

	w.broadcast(p)
	return n, nil
}

func (w *LogMonitor) GetHistory() []byte {
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()

	var history []byte
	w.buffer.Do(func(p interface{}) {
		if p != nil {
			if content, ok := p.([]byte); ok {
				history = append(history, content...)
			}
		}
	})
	return history
}

func (w *LogMonitor) Subscribe() chan []byte {
	w.mu.Lock()
	defer w.mu.Unlock()

	ch := make(chan []byte, 100)
	w.clients[ch] = true
	return ch
}

func (w *LogMonitor) Unsubscribe(ch chan []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.clients, ch)
	close(ch)
}

func (w *LogMonitor) broadcast(msg []byte) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for client := range w.clients {
		select {
		case client <- msg:
		default:
			// If client buffer is full, skip
		}
	}
}
