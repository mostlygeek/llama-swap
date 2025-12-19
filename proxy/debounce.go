package proxy

import (
	"sync"
	"time"
)

// debouncer delays function execution until a quiet period has elapsed.
// Multiple calls to trigger() within the delay window reset the timer,
// ensuring the function only executes once after activity stops.
type debouncer struct {
	mu      sync.Mutex
	delay   time.Duration
	fn      func()
	timer   *time.Timer
	stopped bool
}

// newDebouncer creates a debouncer that will call fn after delay has elapsed
// since the last trigger() call.
func newDebouncer(delay time.Duration, fn func()) *debouncer {
	return &debouncer{
		delay: delay,
		fn:    fn,
	}
}

// trigger resets the debounce timer. The function will execute after delay
// has elapsed with no additional trigger() calls.
func (d *debouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stopped {
		return
	}

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		d.fn()
	})
}

// stop cancels any pending execution and prevents future triggers from firing.
func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
