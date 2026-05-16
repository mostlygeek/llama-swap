package process

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

func TestProcess_StartStop(t *testing.T) {
	// Real HTTP server: health check client dials it directly.
	healthCheckCalled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			select {
			case healthCheckCalled <- struct{}{}:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	conf := config.ModelConfig{
		Proxy:              server.URL,
		Cmd:                "echo hello", // SanitizedCommand() is called before createTestHandler branch
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 5,
	}

	logger := logmon.NewWriter(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := New(ctx, "test-model", conf, logger, logger)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	p.createTestHandler = func() (http.HandlerFunc, error) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}, nil
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	select {
	case <-healthCheckCalled:
	default:
		t.Error("expected health check to be called during Start()")
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if p.state != StateStopped {
		t.Errorf("expected state %s after Stop(), got %s", StateStopped, p.state)
	}
}

func TestProcess_SimpleResponder_StartStop(t *testing.T) {
	if _, err := os.Stat(simpleResponderPath); os.IsNotExist(err) {
		t.Skipf("simple-responder not found at %s, run `make simple-responder`", simpleResponderPath)
	}

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	conf := config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	}

	logger := logmon.NewWriter(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := New(ctx, "simple-responder", conf, logger, logger)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/test", port))
	if err != nil {
		t.Fatalf("GET /test error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("expected body %q, got %q", "hello", string(body))
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if p.state != StateStopped {
		t.Errorf("expected state %s after Stop(), got %s", StateStopped, p.state)
	}
}
