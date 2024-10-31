package proxy

import (
	"container/ring"
	"os"
	"sync"
)

type LogMonitor struct {
	clients  map[chan string]bool
	mu       sync.RWMutex
	buffer   *ring.Ring
	bufferMu sync.RWMutex
}

func NewLogMonitor() *LogMonitor {
	return &LogMonitor{
		clients: make(map[chan string]bool),
		buffer:  ring.New(10 * 1024), // keep 10KB of buffered logs
	}
}

func (w *LogMonitor) Write(p []byte) (n int, err error) {
	n, err = os.Stdout.Write(p)
	if err != nil {
		return n, err
	}

	content := string(p)

	w.bufferMu.Lock()
	w.buffer.Value = content
	w.buffer = w.buffer.Next()
	w.bufferMu.Unlock()

	w.Broadcast(content)
	return n, nil
}

func (w *LogMonitor) getHistory() string {
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()

	var history string
	w.buffer.Do(func(p interface{}) {
		if p != nil {
			if content, ok := p.(string); ok {
				history += content
			}
		}
	})
	return history
}

func (w *LogMonitor) Subscribe() chan string {
	w.mu.Lock()
	defer w.mu.Unlock()

	ch := make(chan string, 100)
	w.clients[ch] = true
	return ch
}

func (w *LogMonitor) Unsubscribe(ch chan string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.clients, ch)
	close(ch)
}

func (w *LogMonitor) Broadcast(msg string) {
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
