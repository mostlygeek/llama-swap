package proxy

import (
	"io"
	"sync"
	"testing"
)

func TestLogMonitor(t *testing.T) {
	logMonitor := NewLogMonitorWriter(io.Discard)

	// Test subscription
	client1 := logMonitor.Subscribe()
	client2 := logMonitor.Subscribe()

	defer logMonitor.Unsubscribe(client1)
	defer logMonitor.Unsubscribe(client2)

	client1Messages := make([]byte, 0)
	client2Messages := make([]byte, 0)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			select {
			case data := <-client1:
				client1Messages = append(client1Messages, data...)
			case data := <-client2:
				client2Messages = append(client2Messages, data...)
			default:
				return
			}
		}
	}()

	logMonitor.Write([]byte("1"))
	logMonitor.Write([]byte("2"))
	logMonitor.Write([]byte("3"))

	// Wait for the goroutine to finish
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
