// Copyright (c) Roman Atachiants and contributore. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for detaile.

package event

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPublish(t *testing.T) {
	d := NewDispatcher()
	var wg sync.WaitGroup

	// Subscribe, must be received in order
	var count int64
	defer Subscribe(d, func(ev MyEvent1) {
		assert.Equal(t, int(atomic.AddInt64(&count, 1)), ev.Number)
		wg.Done()
	})()

	// Publish
	wg.Add(3)
	Publish(d, MyEvent1{Number: 1})
	Publish(d, MyEvent1{Number: 2})
	Publish(d, MyEvent1{Number: 3})

	// Wait and check
	wg.Wait()
	assert.Equal(t, int64(3), count)
}

func TestUnsubscribe(t *testing.T) {
	d := NewDispatcher()
	assert.Equal(t, 0, d.count(TypeEvent1))
	unsubscribe := Subscribe(d, func(ev MyEvent1) {
		// Nothing
	})

	assert.Equal(t, 1, d.count(TypeEvent1))
	unsubscribe()
	assert.Equal(t, 0, d.count(TypeEvent1))
}

func TestConcurrent(t *testing.T) {
	const max = 1000000
	var count int64
	var wg sync.WaitGroup
	wg.Add(1)

	d := NewDispatcher()
	defer Subscribe(d, func(ev MyEvent1) {
		if current := atomic.AddInt64(&count, 1); current == max {
			wg.Done()
		}
	})()

	// Asynchronously publish
	go func() {
		for i := 0; i < max; i++ {
			Publish(d, MyEvent1{})
		}
	}()

	defer Subscribe(d, func(ev MyEvent1) {
		// Subscriber that does nothing
	})()

	wg.Wait()
	assert.Equal(t, max, int(count))
}

func TestSubscribeDifferentType(t *testing.T) {
	d := NewDispatcher()
	assert.Panics(t, func() {
		SubscribeTo(d, TypeEvent1, func(ev MyEvent1) {})
		SubscribeTo(d, TypeEvent1, func(ev MyEvent2) {})
	})
}

func TestPublishDifferentType(t *testing.T) {
	d := NewDispatcher()
	assert.Panics(t, func() {
		SubscribeTo(d, TypeEvent1, func(ev MyEvent2) {})
		Publish(d, MyEvent1{})
	})
}

func TestCloseDispatcher(t *testing.T) {
	d := NewDispatcher()
	defer SubscribeTo(d, TypeEvent1, func(ev MyEvent2) {})()

	assert.NoError(t, d.Close())
	assert.Panics(t, func() {
		SubscribeTo(d, TypeEvent1, func(ev MyEvent2) {})
	})
}

func TestMatrix(t *testing.T) {
	const amount = 1000
	for _, subs := range []int{1, 10, 100} {
		for _, topics := range []int{1, 10} {
			expected := subs * topics * amount
			t.Run(fmt.Sprintf("%dx%d", topics, subs), func(t *testing.T) {
				var count atomic.Int64
				var wg sync.WaitGroup
				wg.Add(expected)

				d := NewDispatcher()
				for i := 0; i < subs; i++ {
					for id := 0; id < topics; id++ {
						defer SubscribeTo(d, uint32(id), func(ev MyEvent3) {
							count.Add(1)
							wg.Done()
						})()
					}
				}

				for n := 0; n < amount; n++ {
					for id := 0; id < topics; id++ {
						go Publish(d, MyEvent3{ID: id})
					}
				}

				wg.Wait()
				assert.Equal(t, expected, int(count.Load()))
			})
		}
	}
}

func TestConcurrentSubscriptionRace(t *testing.T) {
	// This test specifically targets the race condition that occurs when multiple
	// goroutines try to subscribe to different event types simultaneously.
	// Without the CAS loop, subscriptions could be lost due to registry corruption.

	const numGoroutines = 100
	const numEventTypes = 50

	d := NewDispatcher()
	defer d.Close()

	var wg sync.WaitGroup
	var receivedCount int64
	var subscribedTypes sync.Map // Thread-safe map

	wg.Add(numGoroutines)

	// Start multiple goroutines that subscribe to different event types concurrently
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()

			// Each goroutine subscribes to a unique event type
			eventType := uint32(goroutineID%numEventTypes + 1000) // Offset to avoid collision with other tests

			// Subscribe to the event type
			SubscribeTo(d, eventType, func(ev MyEvent3) {
				atomic.AddInt64(&receivedCount, 1)
			})

			// Record that this type was subscribed
			subscribedTypes.Store(eventType, true)
		}(i)
	}

	// Wait for all subscriptions to complete
	wg.Wait()

	// Count the number of unique event types subscribed
	expectedTypes := 0
	subscribedTypes.Range(func(key, value interface{}) bool {
		expectedTypes++
		return true
	})

	// Small delay to ensure all subscriptions are fully processed
	time.Sleep(10 * time.Millisecond)

	// Publish events to each subscribed type
	subscribedTypes.Range(func(key, value interface{}) bool {
		eventType := key.(uint32)
		Publish(d, MyEvent3{ID: int(eventType)})
		return true
	})

	// Wait for all events to be processed
	time.Sleep(50 * time.Millisecond)

	// Verify that we received at least the expected number of events
	// (there might be more if multiple goroutines subscribed to the same event type)
	received := atomic.LoadInt64(&receivedCount)
	assert.GreaterOrEqual(t, int(received), expectedTypes,
		"Should have received at least %d events, got %d", expectedTypes, received)

	// Verify that we have the expected number of unique event types
	assert.Equal(t, numEventTypes, expectedTypes,
		"Should have exactly %d unique event types", numEventTypes)
}

func TestConcurrentHandlerRegistration(t *testing.T) {
	const numGoroutines = 100

	// Test concurrent subscriptions to the same event type
	t.Run("SameEventType", func(t *testing.T) {
		d := NewDispatcher()
		var handlerCount int64
		var wg sync.WaitGroup

		// Start multiple goroutines subscribing to the same event type (0x1)
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				SubscribeTo(d, uint32(0x1), func(ev MyEvent1) {
					atomic.AddInt64(&handlerCount, 1)
				})
			}()
		}

		wg.Wait()

		// Verify all handlers were registered by publishing an event
		atomic.StoreInt64(&handlerCount, 0)
		Publish(d, MyEvent1{})

		// Small delay to ensure all handlers have executed
		time.Sleep(10 * time.Millisecond)

		assert.Equal(t, int64(numGoroutines), atomic.LoadInt64(&handlerCount),
			"Not all handlers were registered due to race condition")
	})

	// Test concurrent subscriptions to different event types
	t.Run("DifferentEventTypes", func(t *testing.T) {
		d := NewDispatcher()
		var wg sync.WaitGroup
		receivedEvents := make(map[uint32]*int64)

		// Create multiple event types and subscribe concurrently
		for i := 0; i < numGoroutines; i++ {
			eventType := uint32(100 + i)
			counter := new(int64)
			receivedEvents[eventType] = counter

			wg.Add(1)
			go func(et uint32, cnt *int64) {
				defer wg.Done()
				SubscribeTo(d, et, func(ev MyEvent3) {
					atomic.AddInt64(cnt, 1)
				})
			}(eventType, counter)
		}

		wg.Wait()

		// Publish events to all types
		for eventType := uint32(100); eventType < uint32(100+numGoroutines); eventType++ {
			Publish(d, MyEvent3{ID: int(eventType)})
		}

		// Small delay to ensure all handlers have executed
		time.Sleep(10 * time.Millisecond)

		// Verify all event types received their events
		for eventType, counter := range receivedEvents {
			assert.Equal(t, int64(1), atomic.LoadInt64(counter),
				"Event type %d did not receive its event", eventType)
		}
	})
}

func TestBackpressure(t *testing.T) {
	d := NewDispatcher()
	d.maxQueue = 10

	var processedCount int64
	unsub := SubscribeTo(d, uint32(0x200), func(ev MyEvent3) {
		atomic.AddInt64(&processedCount, 1)
	})
	defer unsub()

	const eventsToPublish = 1000
	for i := 0; i < eventsToPublish; i++ {
		Publish(d, MyEvent3{ID: 0x200})
	}

	time.Sleep(100 * time.Millisecond)

	// Verify all events were eventually processed
	finalProcessed := atomic.LoadInt64(&processedCount)
	assert.Equal(t, int64(eventsToPublish), finalProcessed)
	t.Logf("Events processed: %d/%d", finalProcessed, eventsToPublish)
}

// ------------------------------------- Test Events -------------------------------------

const (
	TypeEvent1 = 0x1
	TypeEvent2 = 0x2
)

type MyEvent1 struct {
	Number int
}

func (t MyEvent1) Type() uint32 { return TypeEvent1 }

type MyEvent2 struct {
	Text string
}

func (t MyEvent2) Type() uint32 { return TypeEvent2 }

type MyEvent3 struct {
	ID int
}

func (t MyEvent3) Type() uint32 { return uint32(t.ID) }
