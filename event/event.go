package event

import (
	"sync"
)

type EventType int

type Event interface {
	Type() EventType
}

// Dispatcher manages event subscriptions and dispatching
type Dispatcher struct {
	mu        sync.RWMutex
	listeners map[EventType]map[int]func(Event) // Use map for O(1) removal
	nextId    int                               // Unique ID for each subscription
	eventChan chan Event
}

func NewDispatcher() *Dispatcher {
	d := &Dispatcher{
		listeners: make(map[EventType]map[int]func(Event)),
		nextId:    0,
		eventChan: make(chan Event, 10000), // Buffered channel for events
	}

	// Single background processor
	go d.processEvents()

	return d
}

func (d *Dispatcher) processEvents() {
	for event := range d.eventChan {
		d.mu.RLock()
		listeners := d.listeners[event.Type()]
		d.mu.RUnlock()

		for _, listener := range listeners {
			listener(event)
		}
	}
}

// Subscribe registers a typed event handler and returns a cancel function
func Subscribe[T Event](d *Dispatcher, handler func(T)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Get the event type from the generic type parameter
	var zero T
	eventType := zero.Type()

	// Wrap the typed handler to work with the generic Event interface
	wrapper := func(e Event) {
		if typedEvent, ok := e.(T); ok {
			handler(typedEvent)
		}
	}

	if d.listeners[eventType] == nil {
		d.listeners[eventType] = make(map[int]func(Event))
	}

	// Add to listeners
	listenerID := d.nextId
	d.listeners[eventType][listenerID] = wrapper
	d.nextId++

	// Return cancel function that removes by function pointer comparison
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.listeners[eventType], listenerID)
	}
}

func Publish[T Event](d *Dispatcher, event T) {
	d.mu.RLock()
	listeners := d.listeners[event.Type()]
	d.mu.RUnlock()

	select {
	case d.eventChan <- event:
	default:
		// channel full, handle it synchronously
		for _, listener := range listeners {
			listener(event)
		}
	}
}
