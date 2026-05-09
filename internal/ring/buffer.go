package ring

type Buffer[T any] struct {
	buf  []T
	head int
	size int
}

func NewBuffer[T any](capacity int) Buffer[T] {
	if capacity < 1 {
		capacity = 1
	}
	return Buffer[T]{buf: make([]T, capacity)}
}

// Push adds v, overwriting the oldest entry when the buffer is full.
func (r *Buffer[T]) Push(v T) {
	cap := len(r.buf)
	if r.size < cap {
		r.buf[(r.head+r.size)%cap] = v
		r.size++
	} else {
		r.buf[r.head] = v
		r.head = (r.head + 1) % cap
	}
}

// Slice returns all entries in insertion order as a new slice.
func (r *Buffer[T]) Slice() []T {
	if r.size == 0 {
		return nil
	}
	cap := len(r.buf)
	result := make([]T, r.size)
	for i := 0; i < r.size; i++ {
		result[i] = r.buf[(r.head+i)%cap]
	}
	return result
}
