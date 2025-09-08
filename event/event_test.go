package event

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test event types
const (
	TestEventType1 EventType = iota + 1
	TestEventType2
)

// Test event implementations
type TestEvent1 struct {
	Message string
}

func (e TestEvent1) Type() EventType {
	return TestEventType1
}

type TestEvent2 struct {
	Value int
}

func (e TestEvent2) Type() EventType {
	return TestEventType2
}

func TestCancelFunction(t *testing.T) {
	dispatcher := NewDispatcher()
	var received1, received2, received3 bool

	cancel1 := Subscribe(dispatcher, func(e TestEvent1) { received1 = true })
	cancel2 := Subscribe(dispatcher, func(e TestEvent1) { received2 = true })
	_ = Subscribe(dispatcher, func(e TestEvent1) { received3 = true })

	// Cancel the first handler
	cancel1()

	// Now cancel the second handler
	cancel2()

	// Publish an event
	Publish(dispatcher, TestEvent1{Message: "test"})
	time.Sleep(10 * time.Millisecond)

	// Only handler3 should have received the event
	if received1 {
		t.Error("Handler1 should not receive event after cancellation")
	}
	if received2 {
		t.Error("Handler2 should not receive event after cancellation")
	}
	if !received3 {
		t.Error("Handler3 should receive event") // This might fail!
	}
}
func TestSubscribeAndPublish(t *testing.T) {
	dispatcher := NewDispatcher()

	// Test basic subscription and publishing
	t.Run("BasicSubscribeAndPublish", func(t *testing.T) {
		var received TestEvent1
		var wg sync.WaitGroup
		wg.Add(1)

		// Subscribe to TestEvent1
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			received = e
			wg.Done()
		})
		defer cancel()

		// Publish event
		event := TestEvent1{Message: "hello world"}
		Publish(dispatcher, event)

		// Wait for event to be processed
		wg.Wait()

		if received.Message != "hello world" {
			t.Errorf("Expected message 'hello world', got '%s'", received.Message)
		}
	})

	// Test multiple subscribers
	t.Run("MultipleSubscribers", func(t *testing.T) {
		var received1, received2 TestEvent1
		var wg sync.WaitGroup
		wg.Add(2)

		// Subscribe with two handlers
		cancel1 := Subscribe(dispatcher, func(e TestEvent1) {
			received1 = e
			wg.Done()
		})
		defer cancel1()

		cancel2 := Subscribe(dispatcher, func(e TestEvent1) {
			received2 = e
			wg.Done()
		})
		defer cancel2()

		// Publish event
		event := TestEvent1{Message: "multiple"}
		Publish(dispatcher, event)

		// Wait for both handlers to complete
		wg.Wait()

		if received1.Message != "multiple" {
			t.Errorf("Handler 1: Expected 'multiple', got '%s'", received1.Message)
		}
		if received2.Message != "multiple" {
			t.Errorf("Handler 2: Expected 'multiple', got '%s'", received2.Message)
		}
	})

	// Test different event types
	t.Run("DifferentEventTypes", func(t *testing.T) {
		var receivedEvent1 TestEvent1
		var receivedEvent2 TestEvent2
		var wg sync.WaitGroup
		wg.Add(2)

		// Subscribe to different event types
		cancel1 := Subscribe(dispatcher, func(e TestEvent1) {
			receivedEvent1 = e
			wg.Done()
		})
		defer cancel1()

		cancel2 := Subscribe(dispatcher, func(e TestEvent2) {
			receivedEvent2 = e
			wg.Done()
		})
		defer cancel2()

		// Publish both event types
		Publish(dispatcher, TestEvent1{Message: "type1"})
		Publish(dispatcher, TestEvent2{Value: 42})

		// Wait for both handlers
		wg.Wait()

		if receivedEvent1.Message != "type1" {
			t.Errorf("Event1: Expected 'type1', got '%s'", receivedEvent1.Message)
		}
		if receivedEvent2.Value != 42 {
			t.Errorf("Event2: Expected 42, got %d", receivedEvent2.Value)
		}
	})

	// Test subscription cancellation
	t.Run("SubscriptionCancellation", func(t *testing.T) {
		eventReceived := false
		var wg sync.WaitGroup

		// Subscribe and immediately cancel
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			eventReceived = true
			wg.Done()
		})
		cancel() // Cancel subscription

		// Publish event
		Publish(dispatcher, TestEvent1{Message: "should not receive"})

		// Wait a bit to ensure event would have been processed if handler was still subscribed
		time.Sleep(100 * time.Millisecond)

		if eventReceived {
			t.Error("Handler should not have received event after cancellation")
		}
	})

	// Test concurrent publishing
	t.Run("ConcurrentPublishing", func(t *testing.T) {
		var mu sync.Mutex
		var receivedMessages []string
		var wg sync.WaitGroup

		// Subscribe to collect all messages
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			mu.Lock()
			receivedMessages = append(receivedMessages, e.Message)
			mu.Unlock()
			wg.Done()
		})
		defer cancel()

		// Publish multiple events concurrently
		numEvents := 10
		wg.Add(numEvents)

		for i := 0; i < numEvents; i++ {
			go func(i int) {
				Publish(dispatcher, TestEvent1{Message: fmt.Sprintf("message-%d", i)})
			}(i)
		}

		// Wait for all events to be processed
		wg.Wait()

		if len(receivedMessages) != numEvents {
			t.Errorf("Expected %d messages, got %d", numEvents, len(receivedMessages))
		}
	})
}

func BenchmarkDispatcher(b *testing.B) {
	// Benchmark single subscriber
	b.Run("SingleSubscriber", func(b *testing.B) {
		dispatcher := NewDispatcher()

		var wg sync.WaitGroup

		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			wg.Done()
		})
		defer cancel()

		event := TestEvent1{Message: "benchmark"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			wg.Add(1)
			Publish(dispatcher, event)
			wg.Wait()
		}
	})

	// Benchmark multiple subscribers - simplified approach
	b.Run("MultipleSubscribers", func(b *testing.B) {
		dispatcher := NewDispatcher()

		numSubscribers := 10
		var counter int64
		var cancels []func()

		// Set up multiple subscribers once
		for i := 0; i < numSubscribers; i++ {
			cancel := Subscribe(dispatcher, func(e TestEvent1) {
				atomic.AddInt64(&counter, 1)
			})
			cancels = append(cancels, cancel)
		}
		defer func() {
			for _, cancel := range cancels {
				cancel()
			}
		}()

		event := TestEvent1{Message: "benchmark"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Publish(dispatcher, event)
		}

		// Wait for all events to be processed
		time.Sleep(10 * time.Millisecond)
	})

	// Benchmark subscription and cancellation overhead
	b.Run("SubscribeUnsubscribe", func(b *testing.B) {
		dispatcher := NewDispatcher()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cancel := Subscribe(dispatcher, func(e TestEvent1) {})
			cancel()
		}
	})

	// Benchmark publishing without synchronization (more realistic)
	b.Run("PublishAsync", func(b *testing.B) {
		dispatcher := NewDispatcher()
		// Simple handler that just increments a counter
		var counter int64
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			atomic.AddInt64(&counter, 1)
		})
		defer cancel()

		event := TestEvent1{Message: "async"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Publish(dispatcher, event)
		}

		// Wait a bit for goroutines to finish
		time.Sleep(10 * time.Millisecond)
	})

	// Benchmark with different event types
	b.Run("MixedEventTypes", func(b *testing.B) {
		dispatcher := NewDispatcher()
		var counter1, counter2 int64

		cancel1 := Subscribe(dispatcher, func(e TestEvent1) {
			atomic.AddInt64(&counter1, 1)
		})
		defer cancel1()

		cancel2 := Subscribe(dispatcher, func(e TestEvent2) {
			atomic.AddInt64(&counter2, 1)
		})
		defer cancel2()

		event1 := TestEvent1{Message: "mixed1"}
		event2 := TestEvent2{Value: 42}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if i%2 == 0 {
				Publish(dispatcher, event1)
			} else {
				Publish(dispatcher, event2)
			}
		}

		// Wait for events to be processed
		time.Sleep(10 * time.Millisecond)
	})

	// Benchmark memory allocation patterns
	b.Run("MemoryAllocation", func(b *testing.B) {
		dispatcher := NewDispatcher()

		// Add a simple subscriber to make it realistic
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			// Minimal work
		})
		defer cancel()

		event := TestEvent1{Message: "memory test"}

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			Publish(dispatcher, event)
		}
	})

	// Benchmark concurrent publishing
	b.Run("ConcurrentPublish", func(b *testing.B) {
		dispatcher := NewDispatcher()
		var counter int64
		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			atomic.AddInt64(&counter, 1)
		})
		defer cancel()

		event := TestEvent1{Message: "concurrent"}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				Publish(dispatcher, event)
			}
		})

		// Wait for all goroutines to finish
		time.Sleep(50 * time.Millisecond)
	})

	// Benchmark scalability with many subscribers
	b.Run("ManySubscribers", func(b *testing.B) {
		dispatcher := NewDispatcher()

		numSubscribers := 100
		var counter int64
		var cancels []func()

		// Set up many subscribers
		for i := 0; i < numSubscribers; i++ {
			cancel := Subscribe(dispatcher, func(e TestEvent1) {
				atomic.AddInt64(&counter, 1)
			})
			cancels = append(cancels, cancel)
		}
		defer func() {
			for _, cancel := range cancels {
				cancel()
			}
		}()

		event := TestEvent1{Message: "scalability"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Publish(dispatcher, event)
		}

		// Wait for events to be processed
		time.Sleep(50 * time.Millisecond)
	})

	// Benchmark with precise synchronization for throughput measurement
	b.Run("SynchronizedThroughput", func(b *testing.B) {
		dispatcher := NewDispatcher()

		// Use a channel for precise synchronization
		done := make(chan struct{}, 1000) // Buffer to prevent blocking

		cancel := Subscribe(dispatcher, func(e TestEvent1) {
			select {
			case done <- struct{}{}:
			default:
				// Drop if channel is full
			}
		})
		defer cancel()

		event := TestEvent1{Message: "throughput"}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Publish(dispatcher, event)
			<-done // Wait for this specific event to be processed
		}
	})
}
