// Copyright (c) Roman Atachiants and contributors. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for details.

package event

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Event represents an event contract
type Event interface {
	Type() uint32
}

// registry holds an immutable sorted array of event mappings
type registry struct {
	keys []uint32 // Event types (sorted)
	grps []any    // Corresponding subscribers
}

// ------------------------------------- Dispatcher -------------------------------------

// Dispatcher represents an event dispatcher.
type Dispatcher struct {
	subs     atomic.Pointer[registry] // Atomic pointer to immutable array
	done     chan struct{}            // Cancellation
	maxQueue int                      // Maximum queue size per consumer
	mu       sync.Mutex               // Only for writes (subscribe/unsubscribe)
}

// NewDispatcher creates a new dispatcher of events.
func NewDispatcher() *Dispatcher {
	return NewDispatcherConfig(50000)
}

// NewDispatcherConfig creates a new dispatcher with configurable max queue size
func NewDispatcherConfig(maxQueue int) *Dispatcher {
	d := &Dispatcher{
		done:     make(chan struct{}),
		maxQueue: maxQueue,
	}

	d.subs.Store(&registry{
		keys: make([]uint32, 0, 16),
		grps: make([]any, 0, 16),
	})
	return d
}

// Close closes the dispatcher
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isClosed() {
		return nil
	}
	close(d.done)
	for _, grp := range d.subs.Load().grps {
		grp.(interface{ Close() }).Close()
	}
	return nil
}

// isClosed returns whether the dispatcher is closed or not
func (d *Dispatcher) isClosed() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

// findGroup performs a lock-free binary search for the event type
func (d *Dispatcher) findGroup(eventType uint32) any {
	reg := d.subs.Load()
	keys := reg.keys

	// Inlined binary search for better cache locality
	left, right := 0, len(keys)
	for left < right {
		mid := left + (right-left)/2
		if keys[mid] < eventType {
			left = mid + 1
		} else {
			right = mid
		}
	}

	if left < len(keys) && keys[left] == eventType {
		return reg.grps[left]
	}
	return nil
}

// Subscribe subscribes to an event, the type of the event will be automatically
// inferred from the provided type. Must be constant for this to work.
func Subscribe[T Event](broker *Dispatcher, handler func(T)) context.CancelFunc {
	var event T
	return SubscribeTo(broker, event.Type(), handler)
}

// SubscribeTo subscribes to an event with the specified event type.
func SubscribeTo[T Event](broker *Dispatcher, eventType uint32, handler func(T)) context.CancelFunc {
	broker.mu.Lock()
	defer broker.mu.Unlock()

	if broker.isClosed() {
		panic(errClosed)
	}

	// Check if group already exists
	if existing := broker.findGroup(eventType); existing != nil {
		grp := groupOf[T](eventType, existing)
		sub := grp.Add(handler)
		return func() {
			grp.Del(sub)
		}
	}

	// Create new group
	grp := &group[T]{cond: sync.NewCond(new(sync.Mutex)), maxQueue: broker.maxQueue}
	sub := grp.Add(handler)

	// Copy-on-write: insert new entry in sorted position
	old := broker.subs.Load()
	idx := sort.Search(len(old.keys), func(i int) bool {
		return old.keys[i] >= eventType
	})

	// Create new arrays with space for one more element
	newKeys := make([]uint32, len(old.keys)+1)
	newGrps := make([]any, len(old.grps)+1)

	// Copy elements before insertion point
	copy(newKeys[:idx], old.keys[:idx])
	copy(newGrps[:idx], old.grps[:idx])

	// Insert new element
	newKeys[idx] = eventType
	newGrps[idx] = grp

	// Copy elements after insertion point
	copy(newKeys[idx+1:], old.keys[idx:])
	copy(newGrps[idx+1:], old.grps[idx:])

	// Atomically store the new registry (mutex ensures no concurrent writers)
	newReg := &registry{keys: newKeys, grps: newGrps}
	broker.subs.Store(newReg)

	return func() {
		grp.Del(sub)
	}
}

// Publish writes an event into the dispatcher
func Publish[T Event](broker *Dispatcher, ev T) {
	eventType := ev.Type()
	if sub := broker.findGroup(eventType); sub != nil {
		group := groupOf[T](eventType, sub)
		group.Broadcast(ev)
	}
}

// Count counts the number of subscribers, this is for testing only.
func (d *Dispatcher) count(eventType uint32) int {
	if group := d.findGroup(eventType); group != nil {
		return group.(interface{ Count() int }).Count()
	}
	return 0
}

// groupOf casts the subscriber group to the specified generic type
func groupOf[T Event](eventType uint32, subs any) *group[T] {
	if group, ok := subs.(*group[T]); ok {
		return group
	}

	panic(errConflict[T](eventType, subs))
}

// ------------------------------------- Subscriber -------------------------------------

// consumer represents a consumer with a message queue. It owns both delivery
// buffers, so a shutdown can release every event that has not been handed to a
// callback yet. The group's condition lock guards queue and stop. deliveryMu
// guards pending; when both are needed, the group lock comes first.
type consumer[T Event] struct {
	queue      []T
	stop       bool
	deliveryMu sync.Mutex
	pending    []T
}

// Listen listens to the event queue and processes events. It owns the pending
// batch: exactly one event is popped under the lock at a time and its slot
// reference is cleared, so only the callback that is currently running can
// outlive a group shutdown. Every other undelivered event lives in a field the
// group can drop. The listener removes itself from the group's live registry
// once it returns.
func (s *consumer[T]) Listen(g *group[T], c *sync.Cond, fn func(T)) {
	defer g.detach(s)

	for {
		c.L.Lock()

		// Wait for work, then swap the two reusable buffers. The previous
		// delivery buffer is fully drained here, so its capacity is recycled.
		for len(s.queue) == 0 && !s.stop {
			c.Wait()
		}
		if len(s.queue) == 0 {
			c.L.Unlock()
			return
		}
		s.deliveryMu.Lock()
		drained := s.pending
		s.pending = s.queue
		s.queue = drained[:0]
		s.deliveryMu.Unlock()
		// Only backpressured publishers need a wakeup when a batch is taken.
		if g.maxLen >= g.maxQueue {
			c.Broadcast()
		}
		c.L.Unlock()

		for i := 0; ; i++ {
			// Consumers do not contend with each other or publishers while
			// claiming events, but Close can still discard the pending batch.
			s.deliveryMu.Lock()
			if i >= len(s.pending) {
				s.deliveryMu.Unlock()
				break
			}
			ev := s.pending[i]
			var zero T
			s.pending[i] = zero
			s.deliveryMu.Unlock()

			// Callbacks may publish, cancel, or close their dispatcher.
			fn(ev)
		}
	}
}

// ------------------------------------- Subscriber Group -------------------------------------

// group represents a consumer group
type group[T Event] struct {
	cond     *sync.Cond
	subs     []*consumer[T] // Active publish targets
	live     []*consumer[T] // Every listener, until it returns (ownership)
	maxQueue int            // Maximum queue size per consumer
	maxLen   int            // Current maximum queue length across all consumers
	closed   bool
}

// Broadcast sends an event to all consumers
func (s *group[T]) Broadcast(ev T) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	// Calculate current maximum queue length
	s.maxLen = 0
	for _, sub := range s.subs {
		if len(sub.queue) > s.maxLen {
			s.maxLen = len(sub.queue)
		}
	}

	// Backpressure: wait if queues are full
	for s.maxLen >= s.maxQueue && !s.closed {
		s.cond.Wait()

		// Recalculate after wakeup
		s.maxLen = 0
		for _, sub := range s.subs {
			if len(sub.queue) > s.maxLen {
				s.maxLen = len(sub.queue)
			}
		}
	}
	if s.closed {
		return
	}

	// Add event to all queues and track new maximum
	newMax := 0
	for _, sub := range s.subs {
		sub.queue = append(sub.queue, ev)
		if len(sub.queue) > newMax {
			newMax = len(sub.queue)
		}
	}
	s.maxLen = newMax
	s.cond.Broadcast() // Wake consumers
}

// Add adds a subscriber to the list
func (s *group[T]) Add(handler func(T)) *consumer[T] {
	sub := &consumer[T]{
		queue: make([]T, 0, 64),
	}

	// Register the consumer as a publish target and as a live listener. The
	// live entry outlives a cancellation so a shutdown can still release the
	// events a draining consumer has not delivered yet.
	s.cond.L.Lock()
	s.subs = append(s.subs, sub)
	s.live = append(s.live, sub)
	s.cond.L.Unlock()

	// Start listening
	go sub.Listen(s, s.cond, handler)
	return sub
}

// Del removes a subscriber from the publish targets. It is a cancellation, not
// a shutdown: the queued and pending events are kept and the listener drains
// them before it returns and removes itself from the live registry.
func (s *group[T]) Del(sub *consumer[T]) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	// Search and remove the subscriber
	sub.stop = true
	for i, v := range s.subs {
		if v == sub {
			copy(s.subs[i:], s.subs[i+1:])
			s.subs[len(s.subs)-1] = nil
			s.subs = s.subs[:len(s.subs)-1]
			break
		}
	}
	s.cond.Broadcast()
}

// detach drops a listener that has returned. It runs once per subscription.
func (s *group[T]) detach(sub *consumer[T]) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	sub.queue = nil
	sub.deliveryMu.Lock()
	sub.pending = nil
	sub.deliveryMu.Unlock()

	for i, v := range s.live {
		if v == sub {
			s.live[i] = s.live[len(s.live)-1]
			s.live[len(s.live)-1] = nil
			s.live = s.live[:len(s.live)-1]
			break
		}
	}
	s.cond.Broadcast()
}

// Close releases queued events and wakes listeners and backpressured publishers.
// A callback already running is allowed to return without blocking Close; every
// other undelivered event, including the ones held by a cancelled consumer that
// is still draining, is dropped here.
func (s *group[T]) Close() {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	s.closed = true
	for _, sub := range s.live {
		sub.stop = true
		sub.queue = nil
		sub.deliveryMu.Lock()
		sub.pending = nil
		sub.deliveryMu.Unlock()
	}
	s.subs = nil
	s.cond.Broadcast()
}

// ------------------------------------- Debugging -------------------------------------

var errClosed = fmt.Errorf("event dispatcher is closed")

// Count returns the number of subscribers in this group
func (s *group[T]) Count() int {
	return len(s.subs)
}

// String returns string representation of the type
func (s *group[T]) String() string {
	typ := reflect.TypeOf(s).String()
	idx := strings.LastIndex(typ, "/")
	typ = typ[idx+1 : len(typ)-1]
	return typ
}

// errConflict returns a conflict message
func errConflict[T any](eventType uint32, existing any) string {
	var want T
	return fmt.Sprintf(
		"conflicting event type, want=<%T>, registered=<%s>, event=0x%v",
		want, existing, eventType,
	)
}
