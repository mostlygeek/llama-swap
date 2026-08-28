package server

import (
	"sync"
	"testing"
	"time"
)

func newTestSuppressionCounter(interval time.Duration, now *time.Time) *suppressionCounter {
	c := newSuppressionCounter(interval)
	c.now = func() time.Time { return *now }
	c.last = *now
	return c
}

func TestServer_SuppressionCounter_ReportsOncePerInterval(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTestSuppressionCounter(5*time.Second, &now)

	// events within the interval are counted but not reported
	for i := 0; i < 100; i++ {
		if n, ok := c.Add(); ok {
			t.Fatalf("Add() reported %d after %d events, want no report", n, i)
		}
	}

	now = now.Add(5 * time.Second)
	n, ok := c.Add()
	if !ok {
		t.Fatal("Add() did not report after the interval elapsed")
	}
	if n != 101 {
		t.Errorf("reported count = %d, want 101", n)
	}

	// the count resets after reporting
	if n, ok := c.Add(); ok {
		t.Errorf("Add() reported %d immediately after a report, want no report", n)
	}
	now = now.Add(5 * time.Second)
	if n, ok := c.Add(); !ok || n != 2 {
		t.Errorf("Add() = (%d, %v), want (2, true)", n, ok)
	}
}

func TestServer_SuppressionCounter_FlushRemainder(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTestSuppressionCounter(5*time.Second, &now)

	if n, ok := c.Flush(); ok {
		t.Errorf("Flush() = (%d, true) with nothing counted, want (0, false)", n)
	}

	c.Add()
	c.Add()
	n, ok := c.Flush()
	if !ok || n != 2 {
		t.Errorf("Flush() = (%d, %v), want (2, true)", n, ok)
	}

	if n, ok := c.Flush(); ok {
		t.Errorf("second Flush() = (%d, true), want (0, false)", n)
	}
}

func TestServer_SuppressionCounter_ConcurrentAddCountsEveryEvent(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTestSuppressionCounter(time.Hour, &now)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Add()
			}
		}()
	}
	wg.Wait()

	n, ok := c.Flush()
	if !ok || n != 1000 {
		t.Errorf("Flush() = (%d, %v), want (1000, true)", n, ok)
	}
}
