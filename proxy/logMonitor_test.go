package proxy

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogMonitor(t *testing.T) {
	logMonitor := NewLogMonitorWriter(io.Discard)

	// A WaitGroup is used to wait for all the expected writes to complete
	var wg sync.WaitGroup

	client1Messages := make([]byte, 0)
	client2Messages := make([]byte, 0)

	defer logMonitor.OnLogData(func(data []byte) {
		client1Messages = append(client1Messages, data...)
		wg.Done()
	})()

	defer logMonitor.OnLogData(func(data []byte) {
		client2Messages = append(client2Messages, data...)
		wg.Done()
	})()

	wg.Add(6) // 2 x 3 writes

	logMonitor.Write([]byte("1"))
	logMonitor.Write([]byte("2"))
	logMonitor.Write([]byte("3"))

	// wait for all writes to complete
	wg.Wait()

	// Check the buffer
	expectedHistory := "123"
	history := string(logMonitor.GetHistory())

	if history != expectedHistory {
		t.Errorf("Expected history: %s, got: %s", expectedHistory, history)
	}

	c1Data := string(client1Messages)
	if c1Data != expectedHistory {
		t.Errorf("Client1 expected %s, got: %s", expectedHistory, c1Data)
	}

	c2Data := string(client2Messages)
	if c2Data != expectedHistory {
		t.Errorf("Client2 expected %s, got: %s", expectedHistory, c2Data)
	}
}

func TestWrite_ImmutableBuffer(t *testing.T) {
	// Create a new LogMonitor instance
	lm := NewLogMonitorWriter(io.Discard)

	// Prepare a message to write
	msg := []byte("Hello, World!")
	lenmsg := len(msg)

	// Write the message to the LogMonitor
	n, err := lm.Write(msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != lenmsg {
		t.Errorf("Expected %d bytes written but got %d", lenmsg, n)
	}

	// Change the original message
	msg[0] = 'B' // This should not affect the buffer

	// Get the history from the LogMonitor
	history := lm.GetHistory()

	// Check that the history contains the original message, not the modified one
	expected := []byte("Hello, World!")
	if !bytes.Equal(history, expected) {
		t.Errorf("Expected history to be %q, got %q", expected, history)
	}
}

func TestWrite_LogTimeFormat(t *testing.T) {
	// Create a new LogMonitor instance
	lm := NewLogMonitorWriter(io.Discard)

	// Enable timestamps
	lm.timeFormat = time.RFC3339

	// Write the message to the LogMonitor
	lm.Info("Hello, World!")

	// Get the history from the LogMonitor
	history := lm.GetHistory()

	timestamp := ""
	fields := strings.Fields(string(history))
	if len(fields) > 0 {
		timestamp = fields[0]
	} else {
		t.Fatalf("Cannot extract string from history")
	}

	_, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		t.Fatalf("Cannot find timestamp: %v", err)
	}
}

func TestCircularBuffer_WrapAround(t *testing.T) {
	// Create a small buffer to test wrap-around
	cb := newCircularBuffer(10)

	// Write "hello" (5 bytes)
	cb.Write([]byte("hello"))
	if got := string(cb.GetHistory()); got != "hello" {
		t.Errorf("Expected 'hello', got %q", got)
	}

	// Write "world" (5 bytes) - buffer now full
	cb.Write([]byte("world"))
	if got := string(cb.GetHistory()); got != "helloworld" {
		t.Errorf("Expected 'helloworld', got %q", got)
	}

	// Write "12345" (5 bytes) - should overwrite "hello"
	cb.Write([]byte("12345"))
	if got := string(cb.GetHistory()); got != "world12345" {
		t.Errorf("Expected 'world12345', got %q", got)
	}

	// Write data larger than buffer capacity
	cb.Write([]byte("abcdefghijklmnop")) // 16 bytes, only last 10 kept
	if got := string(cb.GetHistory()); got != "ghijklmnop" {
		t.Errorf("Expected 'ghijklmnop', got %q", got)
	}
}

func TestCircularBuffer_BoundaryConditions(t *testing.T) {
	// Test empty buffer
	cb := newCircularBuffer(10)
	if got := cb.GetHistory(); got != nil {
		t.Errorf("Expected nil for empty buffer, got %q", got)
	}

	// Test exact capacity
	cb.Write([]byte("1234567890"))
	if got := string(cb.GetHistory()); got != "1234567890" {
		t.Errorf("Expected '1234567890', got %q", got)
	}

	// Test write exactly at capacity boundary
	cb = newCircularBuffer(10)
	cb.Write([]byte("12345"))
	cb.Write([]byte("67890"))
	if got := string(cb.GetHistory()); got != "1234567890" {
		t.Errorf("Expected '1234567890', got %q", got)
	}
}

func TestLogMonitor_LazyInit(t *testing.T) {
	lm := NewLogMonitorWriter(io.Discard)

	// Buffer should be nil before any writes
	if lm.buffer != nil {
		t.Error("Expected buffer to be nil before first write")
	}

	// GetHistory should return nil when buffer is nil
	if got := lm.GetHistory(); got != nil {
		t.Errorf("Expected nil history before first write, got %q", got)
	}

	// Write should lazily initialize the buffer
	lm.Write([]byte("test"))

	if lm.buffer == nil {
		t.Error("Expected buffer to be initialized after write")
	}

	if got := string(lm.GetHistory()); got != "test" {
		t.Errorf("Expected 'test', got %q", got)
	}
}

func TestLogMonitor_Clear(t *testing.T) {
	lm := NewLogMonitorWriter(io.Discard)

	// Write some data
	lm.Write([]byte("hello"))
	if got := string(lm.GetHistory()); got != "hello" {
		t.Errorf("Expected 'hello', got %q", got)
	}

	// Clear should release the buffer
	lm.Clear()

	if lm.buffer != nil {
		t.Error("Expected buffer to be nil after Clear")
	}

	if got := lm.GetHistory(); got != nil {
		t.Errorf("Expected nil history after Clear, got %q", got)
	}
}

func TestLogMonitor_ClearAndReuse(t *testing.T) {
	lm := NewLogMonitorWriter(io.Discard)

	// Write, clear, then write again
	lm.Write([]byte("first"))
	lm.Clear()
	lm.Write([]byte("second"))

	if got := string(lm.GetHistory()); got != "second" {
		t.Errorf("Expected 'second' after clear and reuse, got %q", got)
	}
}

func BenchmarkLogMonitorWrite(b *testing.B) {
	// Test data of varying sizes
	smallMsg := []byte("small message\n")
	mediumMsg := []byte(strings.Repeat("medium message content ", 10) + "\n")
	largeMsg := []byte(strings.Repeat("large message content for benchmarking ", 100) + "\n")

	b.Run("SmallWrite", func(b *testing.B) {
		lm := NewLogMonitorWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(smallMsg)
		}
	})

	b.Run("MediumWrite", func(b *testing.B) {
		lm := NewLogMonitorWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(mediumMsg)
		}
	})

	b.Run("LargeWrite", func(b *testing.B) {
		lm := NewLogMonitorWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(largeMsg)
		}
	})

	b.Run("WithSubscribers", func(b *testing.B) {
		lm := NewLogMonitorWriter(io.Discard)
		// Add some subscribers
		for i := 0; i < 5; i++ {
			lm.OnLogData(func(data []byte) {})
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(mediumMsg)
		}
	})

	b.Run("GetHistory", func(b *testing.B) {
		lm := NewLogMonitorWriter(io.Discard)
		// Pre-populate with data
		for i := 0; i < 1000; i++ {
			lm.Write(mediumMsg)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.GetHistory()
		}
	})
}

/*
Benchmark Results - MBP M1 Pro

Before (ring.Ring):
| Benchmark                       | ns/op      | bytes/op | allocs/op |
|---------------------------------|------------|----------|-----------|
| SmallWrite (14B)                | 43 ns      | 40 B     | 2         |
| MediumWrite (241B)              | 76 ns      | 264 B    | 2         |
| LargeWrite (4KB)                | 504 ns     | 4,120 B  | 2         |
| WithSubscribers (5 subs)        | 355 ns     | 264 B    | 2         |
| GetHistory (after 1000 writes)  | 145,000 ns | 1.2 MB   | 22        |

After (circularBuffer 10KB):
| Benchmark                       | ns/op      | bytes/op | allocs/op |
|---------------------------------|------------|----------|-----------|
| SmallWrite (14B)                | 26 ns      | 16 B     | 1         |
| MediumWrite (241B)              | 67 ns      | 240 B    | 1         |
| LargeWrite (4KB)                | 774 ns     | 4,096 B  | 1         |
| WithSubscribers (5 subs)        | 325 ns     | 240 B    | 1         |
| GetHistory (after 1000 writes)  | 1,042 ns   | 10,240 B | 1         |

After (circularBuffer 100KB):
| Benchmark                       | ns/op      | bytes/op  | allocs/op |
|---------------------------------|------------|-----------|-----------|
| SmallWrite (14B)                | 26 ns      | 16 B      | 1         |
| MediumWrite (241B)              | 66 ns      | 240 B     | 1         |
| LargeWrite (4KB)                | 753 ns     | 4,096 B   | 1         |
| WithSubscribers (5 subs)        | 309 ns     | 240 B     | 1         |
| GetHistory (after 1000 writes)  | 7,788 ns   | 106,496 B | 1         |

Summary:
- GetHistory: 139x faster (10KB), 18x faster (100KB)
- Allocations: reduced from 2 to 1 across all operations
- Small/medium writes: ~1.1-1.6x faster
*/
