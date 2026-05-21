package cache

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_Add(t *testing.T) {
	t.Run("adds and retrieves item", func(t *testing.T) {
		c := New(1024)
		data := []byte("hello")
		require.NoError(t, c.Add(1, data))

		got, err := c.Get(1)
		require.NoError(t, err)
		assert.Equal(t, data, got)
	})

	t.Run("returns error for oversized item", func(t *testing.T) {
		c := New(10)
		err := c.Add(1, make([]byte, 20))
		assert.ErrorIs(t, err, ErrExceedsMaxSize)
	})

	t.Run("evicts oldest items to make room", func(t *testing.T) {
		c := New(100)

		require.NoError(t, c.Add(1, make([]byte, 40)))
		require.NoError(t, c.Add(2, make([]byte, 40)))
		// Adding item 3 should evict item 1
		require.NoError(t, c.Add(3, make([]byte, 40)))

		assert.False(t, c.Has(1))
		assert.True(t, c.Has(2))
		assert.True(t, c.Has(3))
	})

	t.Run("overwrites existing key", func(t *testing.T) {
		c := New(100)
		require.NoError(t, c.Add(1, []byte("old")))
		require.NoError(t, c.Add(1, []byte("new")))

		got, err := c.Get(1)
		require.NoError(t, err)
		assert.Equal(t, []byte("new"), got)
		assert.Equal(t, 3, c.Size())
	})
}

func TestCache_Get(t *testing.T) {
	t.Run("returns ErrNotFound for missing key", func(t *testing.T) {
		c := New(100)
		_, err := c.Get(99)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestCache_Has(t *testing.T) {
	t.Run("returns true for existing key", func(t *testing.T) {
		c := New(100)
		require.NoError(t, c.Add(1, []byte("data")))
		assert.True(t, c.Has(1))
	})

	t.Run("returns false for missing key", func(t *testing.T) {
		c := New(100)
		assert.False(t, c.Has(1))
	})
}

func TestCache_Size(t *testing.T) {
	t.Run("tracks byte usage", func(t *testing.T) {
		c := New(1000)
		assert.Equal(t, 0, c.Size())

		require.NoError(t, c.Add(1, make([]byte, 100)))
		assert.Equal(t, 100, c.Size())

		require.NoError(t, c.Add(2, make([]byte, 200)))
		assert.Equal(t, 300, c.Size())
	})

	t.Run("updates on eviction", func(t *testing.T) {
		c := New(150)
		require.NoError(t, c.Add(1, make([]byte, 100)))
		require.NoError(t, c.Add(2, make([]byte, 100)))

		// Item 1 should be evicted, size = 100
		assert.Equal(t, 100, c.Size())
	})
}

func TestCache_Clear(t *testing.T) {
	t.Run("removes all items and resets size", func(t *testing.T) {
		c := New(1000)
		require.NoError(t, c.Add(1, []byte("a")))
		require.NoError(t, c.Add(2, []byte("b")))

		c.Clear()

		assert.Equal(t, 0, c.Size())
		assert.False(t, c.Has(1))
		assert.False(t, c.Has(2))
	})
}

func TestCache_Concurrent(t *testing.T) {
	t.Run("concurrent operations are safe", func(t *testing.T) {
		c := New(10000)

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					key := id*100 + j
					_ = c.Add(key, []byte("data"))
					_, _ = c.Get(key)
					_ = c.Has(key)
					_ = c.Size()
				}
			}(i)
		}
		wg.Wait()
	})
}
