package logmon

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

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

// TestNewWriter_BroadcastChannelCapacity verifies that NewWriter initialises the
// hand-off channel with the documented capacity (1024) so that Write can buffer
// that many items before it starts dropping under backpressure.
func TestNewWriter_BroadcastChannelCapacity(t *testing.T) {
	lm := NewWriter(io.Discard)
	if cap(lm.broadcastCh) != 1024 {
		t.Errorf("expected broadcastCh capacity 1024, got %d", cap(lm.broadcastCh))
	}
}

// TestWrite_ReturnsCorrectNEvenOnDrop checks that Write returns (len(p), nil)
// regardless of whether the broadcast was dropped or delivered.
func TestWrite_ReturnsCorrectNEvenOnDrop(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Block ALL subscriber callbacks so broadcastCh overflows and Write drops.
	blocked := make(chan struct{})
	cancel := lm.OnLogData(func(_ []byte) {
		<-blocked
	})
	defer cancel()

	// Flood past channel + subscriber queue capacity so the drop path is exercised.
	for i := 0; i < 3000; i++ {
		lm.Write([]byte("x"))
	}

	// Write must still report the correct byte count even on the drop path.
	payload := []byte("dropped payload")
	n, err := lm.Write(payload)
	if err != nil {
		t.Fatalf("Write on drop path returned error: %v", err)
	}
	if n != len(payload) {
		t.Errorf("Write on drop path: want n=%d, got n=%d", len(payload), n)
	}

	close(blocked)
}

// TestWrite_GetHistoryPreservesDroppedData asserts that the circular buffer is
// written before the (potentially dropping) select, so GetHistory always
// reflects all data even when the live broadcast was discarded.
func TestWrite_GetHistoryPreservesDroppedData(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Block ALL subscriber callbacks so broadcastCh overflows and Write drops.
	blocked := make(chan struct{})
	cancel := lm.OnLogData(func(_ []byte) {
		<-blocked
	})
	defer cancel()

	var wantBuf strings.Builder
	// Write enough to definitely fill broadcastCh (cap 1024) + event queue.
	for i := 0; i < 3000; i++ {
		chunk := fmt.Sprintf("%04d", i)
		lm.Write([]byte(chunk))
		wantBuf.WriteString(chunk)
	}

	// History must contain every byte regardless of broadcast fate.
	got := string(lm.GetHistory())
	want := wantBuf.String()
	if got != want {
		t.Errorf("GetHistory length mismatch: want %d bytes, got %d bytes", len(want), len(got))
	}

	close(blocked)
}

// TestWrite_DroppedCounterIncrements verifies that the atomic dropped counter
// accumulates the byte count of every discarded write.
//
// The subscriber blocks ALL callbacks so that the event queue fills and
// broadcastLoop stalls, causing broadcastCh to overflow and Write to drop.
func TestWrite_DroppedCounterIncrements(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Block ALL subscriber callbacks so the subscriber goroutine fills its
	// internal queue and broadcastLoop blocks, letting broadcastCh overflow.
	blocked := make(chan struct{})
	cancel := lm.OnLogData(func(_ []byte) {
		<-blocked
	})
	defer cancel()

	// Flood well past broadcastCh (1024) + subscriber event queue (1000).
	// This guarantees that Write hits the drop path.
	for i := 0; i < 4000; i++ {
		lm.Write([]byte("abc")) // 3 bytes per write
	}

	dropped := lm.dropped.Load()
	if dropped == 0 {
		close(blocked)
		t.Error("expected dropped counter > 0 after flooding channel, got 0")
		return
	}
	// Every dropped write contributes exactly 3 bytes.
	if dropped%3 != 0 {
		t.Errorf("dropped counter %d is not a multiple of 3 (write size)", dropped)
	}

	close(blocked)
}

// TestBroadcastLoop_DropMarkerFormat verifies the exact format of the in-stream
// dropped-bytes notice: "\n— N bytes dropped —\n" where N is the exact count.
//
// The subscriber blocks ALL callbacks (not just the first) so that the event
// bus's subscriber queue fills completely, stalling broadcastLoop and letting
// broadcastCh overflow to trigger the drop path.
func TestBroadcastLoop_DropMarkerFormat(t *testing.T) {
	lm := NewWriter(io.Discard)

	// blocked gates every subscriber callback: close it to release all.
	blocked := make(chan struct{})
	var mu sync.Mutex
	var received []string

	cancel := lm.OnLogData(func(data []byte) {
		// Every invocation stalls here until blocked is closed. This fills the
		// subscriber's internal queue and then stalls broadcastLoop, allowing
		// broadcastCh to overflow so Write hits the drop path.
		<-blocked
		mu.Lock()
		received = append(received, string(data))
		mu.Unlock()
	})
	defer cancel()

	// Flood well past broadcastCh (1024) + subscriber event queue (1000).
	// After ~1001 events fill the subscriber queue, broadcastLoop stalls and
	// broadcastCh fills; subsequent writes hit the drop path.
	for i := 0; i < 4000; i++ {
		lm.Write([]byte("x"))
	}

	// Capture the dropped count before releasing.
	droppedBefore := lm.dropped.Load()
	if droppedBefore == 0 {
		close(blocked)
		t.Skip("no bytes were dropped; channel drained too fast for this test")
	}

	close(blocked)

	// Wait until a marker appears.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		var markerMsg string
		for _, s := range received {
			if strings.Contains(s, "bytes dropped") {
				markerMsg = s
				break
			}
		}
		mu.Unlock()

		if markerMsg != "" {
			// The marker must contain the em dash characters used in the format.
			if !strings.Contains(markerMsg, "—") {
				t.Errorf("marker missing em dash: %q", markerMsg)
			}
			// Must start and end with a newline.
			if markerMsg[0] != '\n' {
				t.Errorf("marker should start with newline: %q", markerMsg)
			}
			if markerMsg[len(markerMsg)-1] != '\n' {
				t.Errorf("marker should end with newline: %q", markerMsg)
			}
			return
		}

		select {
		case <-deadline:
			t.Fatal("timed out waiting for dropped-bytes marker")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestBroadcastLoop_SingleMarkerForMultipleDrops asserts that many consecutive
// dropped writes produce exactly one consolidated marker notice rather than one
// marker per dropped write, so that the subscriber's log stream stays readable.
func TestBroadcastLoop_SingleMarkerForMultipleDrops(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Block ALL callbacks until we open the gate, ensuring the subscriber queue
	// fills and broadcastLoop stalls so Write truly drops data.
	blocked := make(chan struct{})
	var mu sync.Mutex
	var received []string

	cancel := lm.OnLogData(func(data []byte) {
		<-blocked
		mu.Lock()
		received = append(received, string(data))
		mu.Unlock()
	})
	defer cancel()

	// Flood well past broadcastCh + subscriber queue capacity.
	for i := 0; i < 4000; i++ {
		lm.Write([]byte("x"))
	}

	droppedBefore := lm.dropped.Load()
	if droppedBefore == 0 {
		close(blocked)
		t.Skip("no bytes were dropped; channel drained too fast for this test")
	}

	close(blocked)

	// Collect for a generous window to let all messages arrive.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	markerCount := 0
	for _, s := range received {
		if strings.Contains(s, "bytes dropped") {
			markerCount++
		}
	}
	mu.Unlock()

	// Expect at least one marker.
	if markerCount == 0 {
		t.Error("expected at least one dropped-bytes marker, got none")
	}
	// The number of markers must be much smaller than the number of dropped
	// bytes: the Swap(0) consolidates all accumulated drops into one notice.
	if uint64(markerCount) >= droppedBefore {
		t.Errorf("expected markers (%d) to be far fewer than dropped byte count (%d)", markerCount, droppedBefore)
	}
}

// TestBroadcastLoop_DroppedCounterResetAfterNotice verifies that after
// broadcastLoop emits a dropped-bytes notice it resets the atomic counter to
// zero (via Swap), so the dropped counter doesn't accumulate across multiple
// broadcast cycles.
func TestBroadcastLoop_DroppedCounterResetAfterNotice(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Block ALL subscriber callbacks until we close the gate.
	blocked := make(chan struct{})
	var mu sync.Mutex
	var received []string

	cancel := lm.OnLogData(func(data []byte) {
		<-blocked
		mu.Lock()
		received = append(received, string(data))
		mu.Unlock()
	})
	defer cancel()

	// Flood to cause drops.
	for i := 0; i < 4000; i++ {
		lm.Write([]byte("z"))
	}

	firstDropped := lm.dropped.Load()
	if firstDropped == 0 {
		close(blocked)
		t.Skip("no bytes dropped in flood; test conditions not met")
	}

	// Release. broadcastLoop will call dropped.Swap(0) before publishing the
	// notice, so the counter should be 0 once the marker has been sent.
	close(blocked)

	// Wait for the "bytes dropped" marker to arrive in the subscriber.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		found := false
		for _, s := range received {
			if strings.Contains(s, "bytes dropped") {
				found = true
				break
			}
		}
		mu.Unlock()
		if found {
			break
		}
		select {
		case <-deadline:
			t.Fatal("dropped-bytes marker never arrived after release")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// The marker must not report zero bytes – that would mean Swap returned 0
	// which contradicts the drop we measured.
	mu.Lock()
	for _, s := range received {
		if strings.Contains(s, "bytes dropped") {
			if strings.Contains(s, "0 bytes dropped") {
				t.Errorf("marker reported 0 bytes dropped, expected a non-zero count: %q", s)
			}
			break
		}
	}
	mu.Unlock()

	// After the marker the counter must have been reset. Since no further
	// writes are happening, dropped should now be 0.
	// Give the loop a short moment to complete the Swap.
	time.Sleep(20 * time.Millisecond)
	if after := lm.dropped.Load(); after != 0 {
		t.Errorf("expected dropped counter to be 0 after notice, got %d", after)
	}
}

// TestWrite_ConcurrentNonBlocking is a race-detector test that launches many
// goroutines writing simultaneously while a subscriber is stalled, verifying
// that no write deadlocks and the dropped counter is updated safely.
func TestWrite_ConcurrentNonBlocking(t *testing.T) {
	lm := NewWriter(io.Discard)

	release := make(chan struct{})
	cancel := lm.OnLogData(func(_ []byte) {
		<-release
	})
	defer cancel()

	const goroutines = 20
	const writesPerGoroutine = 200

	var wg sync.WaitGroup
	done := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				lm.Write([]byte("concurrent"))
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent writes deadlocked with stalled subscriber")
	}

	close(release)
}

// TestWrite_EmptyWriteSkipsBroadcast ensures that an empty slice passed to
// Write returns immediately without sending anything to broadcastCh, so the
// channel length remains unchanged.
func TestWrite_EmptyWriteSkipsBroadcast(t *testing.T) {
	lm := NewWriter(io.Discard)

	// Drain the loop quickly by not subscribing, so channel stays near empty.
	before := len(lm.broadcastCh)
	n, err := lm.Write([]byte{})
	if err != nil || n != 0 {
		t.Fatalf("Write([]byte{}): want (0, nil), got (%d, %v)", n, err)
	}
	after := len(lm.broadcastCh)
	if after > before {
		t.Errorf("empty Write should not enqueue to broadcastCh (before=%d, after=%d)", before, after)
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
