package proxy

import (
	"sync"
	"time"
)

type debouncer struct {
	mu      sync.Mutex
	delay   time.Duration
	fn      func()
	timer   *time.Timer
	stopped bool
}

func newDebouncer(delay time.Duration, fn func()) *debouncer {
	return &debouncer{
		delay: delay,
		fn:    fn,
	}
}

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

func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
