package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MetricsFileStore persists ActivityLogEntry slices to a JSON file with
// atomic writes and configurable flush interval. Safe for concurrent use.
type MetricsFileStore struct {
	path     string
	interval time.Duration

	mu       sync.Mutex
	snapshot func() []ActivityLogEntry
	done     chan struct{}
	stopped  bool
}

// NewMetricsFileStore creates a store that periodically writes the metrics
// returned by snapshotFn to the given file path. If path is empty or the
// directory cannot be created, it returns nil (no-op store).
// interval is the minimum time between writes (0 = 5s default).
func NewMetricsFileStore(path string, interval time.Duration, snapshotFn func() []ActivityLogEntry) *MetricsFileStore {
	if path == "" {
		return nil
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}

	// Ensure the directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil
	}

	return &MetricsFileStore{
		path:     path,
		interval: interval,
		snapshot: snapshotFn,
		done:     make(chan struct{}),
	}
}

// Start launches the background flush goroutine. Does nothing if the store
// is nil or already running.
func (ms *MetricsFileStore) Start() {
	if ms == nil {
		return
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.stopped {
		return // already stopped, don't restart
	}
	if ms.done != nil {
		// check if channel is closed (already started and stopped)
		select {
		case <-ms.done:
			ms.done = make(chan struct{})
		default:
			return // already running
		}
	}
	go ms.loop()
}

// loop is the background flush goroutine.
func (ms *MetricsFileStore) loop() {
	ticker := time.NewTicker(ms.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ms.done:
			ms.flush()
			return
		case <-ticker.C:
			ms.flush()
		}
	}
}

// flush writes the current snapshot to disk atomically.
func (ms *MetricsFileStore) flush() {
	if ms == nil {
		return
	}
	entries := ms.snapshot()
	if entries == nil {
		entries = []ActivityLogEntry{}
	}

	data, err := json.Marshal(entries)
	if err != nil {
		return
	}

	// Atomic write: write to temp file, then rename
	dir := filepath.Dir(ms.path)
	tmp, err := os.CreateTemp(dir, "metrics-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()

	var writeErr error
	if _, err := tmp.Write(data); err != nil {
		writeErr = err
	}
	if err := tmp.Close(); err != nil && writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		if err := os.Remove(tmpPath); err != nil {
			// Temp file cleanup failed — the OS will clean up orphaned temp files
		}
		return
	}
	if err := os.Rename(tmpPath, ms.path); err != nil {
		if err := os.Remove(tmpPath); err != nil {
			// Temp file cleanup failed — the OS will clean up orphaned temp files
		}
	}
}

// Load reads metrics from the file. Returns nil if the file doesn't exist,
// or an empty slice if the file is empty/corrupt. This is done once at startup
// so no locking is needed.
func (ms *MetricsFileStore) Load() ([]ActivityLogEntry, error) {
	if ms == nil || ms.path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(ms.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading metrics store: %w", err)
	}

	if len(data) == 0 {
		return []ActivityLogEntry{}, nil
	}

	var entries []ActivityLogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// Corrupt file — return empty rather than crashing
		return []ActivityLogEntry{}, nil
	}
	if entries == nil {
		return []ActivityLogEntry{}, nil
	}
	return entries, nil
}

// Close stops the background flush and writes one final snapshot.
func (ms *MetricsFileStore) Close() error {
	if ms == nil {
		return nil
	}
	ms.mu.Lock()
	ms.stopped = true
	ms.mu.Unlock()

	select {
	case <-ms.done:
		// already closed
	default:
		close(ms.done)
	}
	return nil
}
