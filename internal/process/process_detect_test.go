package process

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/capcompat"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/hint"
)

// fakeVLLM answers the health check and /v1/models the way vLLM does, so
// detection identifies it from owned_by.
func fakeVLLM(t *testing.T, modelName string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model","owned_by":"vllm","max_model_len":8192}]}`, modelName)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitDetected(t *testing.T, ch <-chan capcompat.DetectedEvent) capcompat.DetectedEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no DetectedEvent")
		return capcompat.DetectedEvent{}
	}
}

func TestProcessCommand_DetectsUpstreamBeforeReady(t *testing.T) {
	skipIfNoSimpleResponder(t)

	got := make(chan capcompat.DetectedEvent, 4)
	cancel := event.On(func(e capcompat.DetectedEvent) {
		if e.ModelID == t.Name() {
			got <- e
		}
	})
	defer cancel()

	// The command only has to stay running; requests go to the fake upstream.
	cmd, _ := simpleResponderCmd(t, "-silent")
	upstream := fakeVLLM(t, t.Name())
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              upstream.URL,
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	runErr := runAsync(t, p)
	// Detection runs before the process is marked ready, so the upstream is
	// known as soon as it serves.
	if u := p.Status().Upstream; u != hint.VLLM {
		t.Errorf("Status().Upstream = %q, want vllm", u)
	}
	e := waitDetected(t, got)
	if e.Err != nil || e.Info.Upstream != hint.VLLM {
		t.Errorf("event = %+v, want vllm without error", e)
	}
	if e.Key == "" {
		t.Error("event has no capability key")
	}

	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-runErr
	if u := p.Status().Upstream; u != hint.Unknown {
		t.Errorf("stopped Status().Upstream = %q, want unknown", u)
	}
}

func TestProcessCommand_DetectsUpstreamWithoutHealthCheck(t *testing.T) {
	skipIfNoSimpleResponder(t)

	got := make(chan capcompat.DetectedEvent, 4)
	cancel := event.On(func(e capcompat.DetectedEvent) {
		if e.ModelID == t.Name() {
			got <- e
		}
	})
	defer cancel()

	cmd, _ := simpleResponderCmd(t, "-silent")
	upstream := fakeVLLM(t, t.Name())
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:           cmd,
		Proxy:         upstream.URL,
		CheckEndpoint: "none",
	})

	runErr := runAsync(t, p)
	e := waitDetected(t, got)
	if e.Err != nil || e.Info.Upstream != hint.VLLM {
		t.Errorf("event = %+v, want vllm without error", e)
	}
	if u := p.Status().Upstream; u != hint.VLLM {
		t.Errorf("Status().Upstream = %q, want vllm", u)
	}

	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-runErr
}

func TestProcessCommand_DetectionFailureStillEmits(t *testing.T) {
	skipIfNoSimpleResponder(t)

	got := make(chan capcompat.DetectedEvent, 4)
	cancel := event.On(func(e capcompat.DetectedEvent) {
		if e.ModelID == t.Name() {
			got <- e
		}
	})
	defer cancel()

	// simple-responder has no /v1/models, so detection fails but the model
	// still becomes ready.
	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})

	runErr := runAsync(t, p)
	e := waitDetected(t, got)
	if e.Err == nil {
		t.Errorf("event = %+v, want a detection error", e)
	}
	if u := p.Status().Upstream; u != hint.Unknown {
		t.Errorf("Status().Upstream = %q, want unknown", u)
	}

	if err := p.Stop(testStopTimeout); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-runErr
}
