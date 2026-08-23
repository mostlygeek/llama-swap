package server

import (
	"sync"
	"time"
)

// suppressionCounter counts repeated events and rate limits reporting them so
// a burst produces a single summary instead of one log line per event. It is
// safe for concurrent use.
type suppressionCounter struct {
	mu       sync.Mutex
	interval time.Duration
	count    int
	last     time.Time

	// now is swappable for testing
	now func() time.Time
}

func newSuppressionCounter(interval time.Duration) *suppressionCounter {
	c := &suppressionCounter{
		interval: interval,
		now:      time.Now,
	}
	c.last = c.now()
	return c
}

// Add records one suppressed event. It returns the number of events suppressed
// since the last report and true when the reporting interval has elapsed. The
// counter is reset whenever it returns true.
func (c *suppressionCounter) Add() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.count++
	now := c.now()
	if now.Sub(c.last) < c.interval {
		return 0, false
	}

	count := c.count
	c.count = 0
	c.last = now
	return count, true
}

// Flush returns any events counted but not yet reported, and true when there
// are any. Use it to report the remainder when the counter goes away.
func (c *suppressionCounter) Flush() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.count == 0 {
		return 0, false
	}

	count := c.count
	c.count = 0
	c.last = c.now()
	return count, true
}
