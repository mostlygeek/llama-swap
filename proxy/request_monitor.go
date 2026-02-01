package proxy

import (
	"bytes"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/event"
)

type RequestEntry struct {
	ID           int           `json:"id"`
	Timestamp    time.Time     `json:"timestamp"`
	Method       string        `json:"method"`
	Path         string        `json:"path"`
	Model        string        `json:"model"`
	Status       int           `json:"status"`
	Duration     time.Duration `json:"duration"`
	RequestBody  string        `json:"request_body,omitempty"`
	ResponseBody string        `json:"response_body,omitempty"`
	Pending      bool          `json:"pending"`
	lastEmit     time.Time
	respBuf      bytes.Buffer
}

type RequestEvent struct {
	Entry RequestEntry
}

func (e RequestEvent) Type() uint32 {
	return RequestEventID
}

type requestMonitor struct {
	mu         sync.RWMutex
	entries    []*RequestEntry
	maxEntries int
	nextID     int
}

func newRequestMonitor(maxEntries int) *requestMonitor {
	return &requestMonitor{
		maxEntries: maxEntries,
		entries:    make([]*RequestEntry, 0),
	}
}

func (rm *requestMonitor) Add(entry *RequestEntry) int {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	entry.ID = rm.nextID
	rm.nextID++
	entry.Pending = true
	entry.lastEmit = time.Now()

	rm.entries = append(rm.entries, entry)
	if len(rm.entries) > rm.maxEntries {
		rm.entries = rm.entries[len(rm.entries)-rm.maxEntries:]
	}

	event.Emit(RequestEvent{Entry: *entry})
	return entry.ID
}

func (rm *requestMonitor) Update(id int, status int, duration time.Duration, responseBody string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for _, e := range rm.entries {
		if e.ID == id {
			e.Status = status
			e.Duration = duration
			if responseBody != "" {
				e.ResponseBody = responseBody
			} else if e.respBuf.Len() > 0 {
				e.ResponseBody = e.respBuf.String()
			}
			e.Pending = false
			// Create a copy to emit to avoid race conditions if the pointer is modified later
			entryCopy := *e
			event.Emit(RequestEvent{Entry: entryCopy})
			return
		}
	}
}

func (rm *requestMonitor) AppendResponse(id int, data string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for _, e := range rm.entries {
		if e.ID == id {
			// Limit streaming buffer to 1MB total per request
			if e.respBuf.Len() < 1024*1024 {
				e.respBuf.WriteString(data)

				// Throttle emissions during streaming to 10 per second
				if time.Since(e.lastEmit) > 100*time.Millisecond {
					e.lastEmit = time.Now()
					e.ResponseBody = e.respBuf.String()
					event.Emit(RequestEvent{Entry: *e})
				}
			}
			return
		}
	}
}

func (rm *requestMonitor) GetEntries() []RequestEntry {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make([]RequestEntry, len(rm.entries))
	for i, e := range rm.entries {
		result[i] = *e
	}
	return result
}

func (rm *requestMonitor) GetEntry(id int) (RequestEntry, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, e := range rm.entries {
		if e.ID == id {
			return *e, true
		}
	}
	return RequestEntry{}, false
}
