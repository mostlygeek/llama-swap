package proxy

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDebounce(t *testing.T) {
	t.Run("single call executes after delay", func(t *testing.T) {
		var count atomic.Int32
		fn := func() { count.Add(1) }

		debounced := newDebouncer(50*time.Millisecond, fn)
		debounced.trigger()

		// Should not have executed yet
		assert.Equal(t, int32(0), count.Load())

		// Wait for debounce
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(1), count.Load())
	})

	t.Run("rapid calls only execute once", func(t *testing.T) {
		var count atomic.Int32
		fn := func() { count.Add(1) }

		debounced := newDebouncer(50*time.Millisecond, fn)

		// Rapid fire triggers
		for i := 0; i < 10; i++ {
			debounced.trigger()
			time.Sleep(10 * time.Millisecond)
		}

		// Wait for debounce
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(1), count.Load())
	})

	t.Run("stop prevents execution", func(t *testing.T) {
		var count atomic.Int32
		fn := func() { count.Add(1) }

		debounced := newDebouncer(50*time.Millisecond, fn)
		debounced.trigger()
		debounced.stop()

		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(0), count.Load())
	})
}
