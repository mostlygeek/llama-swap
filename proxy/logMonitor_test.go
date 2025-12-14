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
Baseline
| Benchmark | ops/sec | ns/op | bytes/op | allocs/op |
|-----------|---------|-------|----------|-----------|
| SmallWrite (14B) | ~27M | 43 ns | 40 B | 2 |
| MediumWrite (241B) | ~16M | 76 ns | 264 B | 2 |
| LargeWrite (4KB) | ~2.3M | 504 ns | 4120 B | 2 |
| WithSubscribers (5 subs) | ~3.3M | 355 ns | 264 B | 2 |
| GetHistory (after 1000 writes) | ~8K | 145 µs | 1.2 MB | 22 |
*/
