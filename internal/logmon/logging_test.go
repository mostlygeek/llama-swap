package logmon

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogMonitor_Close(t *testing.T) {
	var stdout bytes.Buffer
	m := NewWriter(&stdout)
	m.Write([]byte("before"))
	closer, ok := any(m).(io.Closer)
	if !ok {
		t.Fatal("Monitor has no Close method")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := m.Write([]byte("after")); err != nil || n != 5 {
		t.Fatalf("Write after Close: %d, %v", n, err)
	}
	if stdout.String() != "beforeafter" {
		t.Fatal("Close must preserve stdout writes")
	}
	if len(m.GetHistory()) != 0 {
		t.Fatal("closed monitor retained history")
	}
	m.OnLogData(func([]byte) { t.Error("subscribed after Close") })()
}

func TestLogMonitor_CloseConcurrent(t *testing.T) {
	m := NewWriter(io.Discard)
	closer, ok := any(m).(io.Closer)
	if !ok {
		t.Fatal("Monitor has no Close method")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				cancel := m.OnLogData(func([]byte) {})
				m.Write([]byte("log"))
				cancel()
				if j == 100 {
					closer.Close()
				}
			}
		}()
	}
	wg.Wait()
	closer.Close()
}

func TestLogMonitor_CloseBackpressure(t *testing.T) {
	m := NewWriter(io.Discard)
	closer, ok := any(m).(io.Closer)
	if !ok {
		t.Fatal("Monitor has no Close method")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cancel := m.OnLogData(func([]byte) { once.Do(func() { close(entered) }); <-release })
	defer cancel()
	defer close(release)
	m.Write([]byte("first"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not start")
	}
	for i := 0; i < 10000; i++ {
		m.Write([]byte("queued"))
	}
	done := make(chan struct{})
	go func() { closer.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind subscriber")
	}
	if len(m.GetHistory()) != 0 {
		t.Fatal("closed monitor retained history")
	}
}

func TestLogMonitor_CloseReleasesGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		m := NewWriter(io.Discard)
		closer, ok := any(m).(io.Closer)
		if !ok {
			t.Fatal("Monitor has no Close method")
		}
		m.OnLogData(func([]byte) {})
		m.Write([]byte("log"))
		closer.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	after := runtime.NumGoroutine()
	t.Logf("goroutines before=%d after=%d", before, after)
	if after > before {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, after)
	}
}

// A Close invoked from inside a subscriber callback must return without waiting
// for that callback, and must not leave the events queued behind it to be
// delivered afterwards: only the running callback may outlive the shutdown.
func TestLogMonitor_CloseFromCallback(t *testing.T) {
	m := NewWriter(io.Discard)
	closer, ok := any(m).(io.Closer)
	if !ok {
		t.Fatal("Monitor has no Close method")
	}

	entered, release := make(chan struct{}), make(chan struct{})
	closed := make(chan struct{})
	var enterOnce sync.Once
	cancel := m.OnLogData(func(data []byte) {
		switch string(data) {
		case "1":
			enterOnce.Do(func() {
				close(entered)
				<-release
			})
		case "2":
			if err := closer.Close(); err != nil {
				t.Error(err)
			}
			close(closed)
		}
	})
	defer cancel()
	defer closer.Close()

	// A second consumer of the same group acts as a fence: Broadcast appends to
	// every subscriber under one lock, so once this one has been handed the
	// third line the first consumer is already holding both queued lines.
	reached := make(chan struct{})
	cancelFence := m.OnLogData(func(data []byte) {
		if string(data) == "3" {
			close(reached)
		}
	})
	defer cancelFence()

	for _, line := range []string{"1", "2", "3"} {
		if _, err := m.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first callback did not start")
	}
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("queued lines never reached the broadcast group")
	}
	close(release)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close from a subscriber callback never returned")
	}

	if len(m.GetHistory()) != 0 {
		t.Fatal("closed monitor retained history")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLogMonitor(t *testing.T) {
	logMonitor := NewWriter(io.Discard)

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

	wg.Wait()

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
	lm := NewWriter(io.Discard)

	msg := []byte("Hello, World!")
	lenmsg := len(msg)

	n, err := lm.Write(msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if n != lenmsg {
		t.Errorf("Expected %d bytes written but got %d", lenmsg, n)
	}

	msg[0] = 'B'

	history := lm.GetHistory()

	expected := []byte("Hello, World!")
	if !bytes.Equal(history, expected) {
		t.Errorf("Expected history to be %q, got %q", expected, history)
	}
}

func TestWrite_LogTimeFormat(t *testing.T) {
	lm := NewWriter(io.Discard)

	lm.timeFormat = time.RFC3339

	lm.Info("Hello, World!")

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
	cb := newCircularBuffer(10)

	cb.Write([]byte("hello"))
	if got := string(cb.GetHistory()); got != "hello" {
		t.Errorf("Expected 'hello', got %q", got)
	}

	cb.Write([]byte("world"))
	if got := string(cb.GetHistory()); got != "helloworld" {
		t.Errorf("Expected 'helloworld', got %q", got)
	}

	cb.Write([]byte("12345"))
	if got := string(cb.GetHistory()); got != "world12345" {
		t.Errorf("Expected 'world12345', got %q", got)
	}

	cb.Write([]byte("abcdefghijklmnop"))
	if got := string(cb.GetHistory()); got != "ghijklmnop" {
		t.Errorf("Expected 'ghijklmnop', got %q", got)
	}
}

func TestCircularBuffer_BoundaryConditions(t *testing.T) {
	cb := newCircularBuffer(10)
	if got := cb.GetHistory(); got != nil {
		t.Errorf("Expected nil for empty buffer, got %q", got)
	}

	cb.Write([]byte("1234567890"))
	if got := string(cb.GetHistory()); got != "1234567890" {
		t.Errorf("Expected '1234567890', got %q", got)
	}

	cb = newCircularBuffer(10)
	cb.Write([]byte("12345"))
	cb.Write([]byte("67890"))
	if got := string(cb.GetHistory()); got != "1234567890" {
		t.Errorf("Expected '1234567890', got %q", got)
	}
}

func TestLogMonitor_LazyInit(t *testing.T) {
	lm := NewWriter(io.Discard)

	if lm.buffer != nil {
		t.Error("Expected buffer to be nil before first write")
	}

	if got := lm.GetHistory(); got != nil {
		t.Errorf("Expected nil history before first write, got %q", got)
	}

	lm.Write([]byte("test"))

	if lm.buffer == nil {
		t.Error("Expected buffer to be initialized after write")
	}

	if got := string(lm.GetHistory()); got != "test" {
		t.Errorf("Expected 'test', got %q", got)
	}
}

func TestLogMonitor_Clear(t *testing.T) {
	lm := NewWriter(io.Discard)

	lm.Write([]byte("hello"))
	if got := string(lm.GetHistory()); got != "hello" {
		t.Errorf("Expected 'hello', got %q", got)
	}

	lm.Clear()

	if lm.buffer != nil {
		t.Error("Expected buffer to be nil after Clear")
	}

	if got := lm.GetHistory(); got != nil {
		t.Errorf("Expected nil history after Clear, got %q", got)
	}
}

func TestLogMonitor_ClearAndReuse(t *testing.T) {
	lm := NewWriter(io.Discard)

	lm.Write([]byte("first"))
	lm.Clear()
	lm.Write([]byte("second"))

	if got := string(lm.GetHistory()); got != "second" {
		t.Errorf("Expected 'second' after clear and reuse, got %q", got)
	}
}

// TestLogMonitor_DropsWhenSubscriberBlocked verifies that a stalled subscriber
// can never block Write (the upstream process's stdout drain) and that dropped
// data is reported in-stream with a marker once delivery resumes. See #875.
func TestLogMonitor_DropsWhenSubscriberBlocked(t *testing.T) {
	lm := NewWriter(io.Discard)

	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var received [][]byte

	cancel := lm.OnLogData(func(data []byte) {
		// Block the first delivery, stalling the broadcaster goroutine so the
		// hand-off channel and event queue fill and subsequent writes drop.
		once.Do(func() { <-release })
		mu.Lock()
		received = append(received, append([]byte(nil), data...))
		mu.Unlock()
	})
	defer cancel()

	// Flood well past the hand-off channel (1024) + event queue (1000)
	// capacity. None of these writes may block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 4000; i++ {
			lm.Write([]byte("x"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked while subscriber was stalled")
	}

	close(release)

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		found := false
		for _, d := range received {
			if strings.Contains(string(d), "bytes dropped") {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			return
		}
		select {
		case <-deadline:
			t.Fatal("expected a 'bytes dropped' marker after resuming delivery")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func BenchmarkLogMonitorWrite(b *testing.B) {
	smallMsg := []byte("small message\n")
	mediumMsg := []byte(strings.Repeat("medium message content ", 10) + "\n")
	largeMsg := []byte(strings.Repeat("large message content for benchmarking ", 100) + "\n")

	b.Run("SmallWrite", func(b *testing.B) {
		lm := NewWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(smallMsg)
		}
	})

	b.Run("MediumWrite", func(b *testing.B) {
		lm := NewWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(mediumMsg)
		}
	})

	b.Run("LargeWrite", func(b *testing.B) {
		lm := NewWriter(io.Discard)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(largeMsg)
		}
	})

	b.Run("WithSubscribers", func(b *testing.B) {
		lm := NewWriter(io.Discard)
		for i := 0; i < 5; i++ {
			lm.OnLogData(func(data []byte) {})
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.Write(mediumMsg)
		}
	})

	b.Run("GetHistory", func(b *testing.B) {
		lm := NewWriter(io.Discard)
		for i := 0; i < 1000; i++ {
			lm.Write(mediumMsg)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lm.GetHistory()
		}
	})
}
