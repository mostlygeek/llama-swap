package cache

import (
	"errors"
	"sync"
)

var (
	ErrExceedsMaxSize = errors.New("item exceeds maximum cache size")
	ErrNotFound       = errors.New("item not found")
)

type Cache struct {
	mu      sync.Mutex
	items   map[int][]byte
	order   []int
	size    int
	maxSize int
}

func New(maxBytes int) *Cache {
	return &Cache{
		items:   make(map[int][]byte),
		order:   make([]int, 0),
		maxSize: maxBytes,
	}
}

func (c *Cache) Add(id int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dataSize := len(data)
	if dataSize > c.maxSize {
		return ErrExceedsMaxSize
	}

	// If key already exists, remove old entry from size and order
	if old, exists := c.items[id]; exists {
		c.size -= len(old)
		c.removeOrder(id)
	}

	// Evict oldest (FIFO) until room available
	for c.size+dataSize > c.maxSize && len(c.order) > 0 {
		oldestID := c.order[0]
		c.order = c.order[1:]
		if evicted, exists := c.items[oldestID]; exists {
			c.size -= len(evicted)
			delete(c.items, oldestID)
		}
	}

	c.items[id] = data
	c.order = append(c.order, id)
	c.size += dataSize
	return nil
}

func (c *Cache) removeOrder(id int) {
	for i, v := range c.order {
		if v == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *Cache) Get(id int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, exists := c.items[id]
	if !exists {
		return nil, ErrNotFound
	}
	return data, nil
}

func (c *Cache) Has(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, exists := c.items[id]
	return exists
}

func (c *Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.size
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[int][]byte)
	c.order = c.order[:0]
	c.size = 0
}
