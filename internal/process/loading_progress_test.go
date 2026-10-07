package process

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

func TestProcessCommand_ParseLoadingProgress(t *testing.T) {
	f := func(v float64) *float64 { return &v }

	tests := []struct {
		name   string
		body   string
		want   LoadingProgress
		wantOK bool
	}{
		{"progress and message", `{"progress":0.25,"message":"loading tensors"}`, LoadingProgress{Progress: f(0.25), Message: "loading tensors"}, true},
		{"progress only", `{"progress":0}`, LoadingProgress{Progress: f(0)}, true},
		{"message only", `{"message":"  warming up  "}`, LoadingProgress{Message: "warming up"}, true},
		{"progress clamped high", `{"progress":7}`, LoadingProgress{Progress: f(1)}, true},
		{"progress clamped low", `{"progress":-1}`, LoadingProgress{Progress: f(0)}, true},
		{"llama-server loading body", `{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`, LoadingProgress{}, false},
		{"empty message", `{"message":""}`, LoadingProgress{}, false},
		{"not json", `Service Unavailable`, LoadingProgress{}, false},
		{"empty body", ``, LoadingProgress{}, false},
		{"wrong type", `{"progress":"half"}`, LoadingProgress{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLoadingProgress([]byte(tt.body))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !got.equal(&tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestProcessCommand_ParseLoadingProgressTrimsMessage(t *testing.T) {
	// Multi-byte characters check the cut lands on a character boundary.
	long := strings.Repeat("é", maxLoadingMessageLen+10)
	got, ok := parseLoadingProgress([]byte(fmt.Sprintf(`{"message":%q}`, long)))
	if !ok {
		t.Fatal("expected a progress report")
	}
	if n := len([]rune(got.Message)); n != maxLoadingMessageLen {
		t.Errorf("message is %d characters, want %d", n, maxLoadingMessageLen)
	}
	if got.Message != strings.Repeat("é", maxLoadingMessageLen) {
		t.Error("message was not cut on a character boundary")
	}
}

// progressUpstream serves a health check that reports loading progress for
// the first `reports` polls and then becomes ready. When advance is false
// every report is the same, so no progress is ever made.
func progressUpstream(t *testing.T, reports int64, advance bool) *httptest.Server {
	t.Helper()
	var polls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		if n > reports {
			w.WriteHeader(http.StatusOK)
			return
		}
		step := int64(1)
		if advance {
			step = n
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"progress":%g,"message":"step %d"}`, float64(step)/float64(reports+1), step)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestProcessCommand_LoadingProgressExtendsTimeout checks that a load which
// keeps reporting progress is not cut off by the health check timeout, and that
// each report is published on the status and as an event.
func TestProcessCommand_LoadingProgressExtendsTimeout(t *testing.T) {
	skipIfNoSimpleResponder(t)

	// Three reports one second apart take longer than the timeout below.
	mock := progressUpstream(t, 3, true)
	cmd, _ := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:           cmd,
		Proxy:         mock.URL,
		CheckEndpoint: "/health",
	})

	events := make(chan swaputil.ProcessLoadingEvent, 16)
	cancel := event.On(func(e swaputil.ProcessLoadingEvent) {
		if e.ProcessName == p.id {
			events <- e
		}
	})
	defer cancel()

	var sawStatus atomic.Bool
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		for {
			select {
			case <-stopWatch:
				return
			case <-time.After(testPollInterval):
				if st := p.Status(); st.State == StateStarting && st.Loading != nil && st.Loading.Progress != nil {
					sawStatus.Store(true)
				}
			}
		}
	}()

	ctx, cancelCtx := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCtx()
	if err := p.EnsureReady(ctx, 1500*time.Millisecond); err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	defer p.Stop(testStopTimeout)

	if !sawStatus.Load() {
		t.Error("Status() never carried loading progress while starting")
	}
	if st := p.Status(); st.Loading != nil {
		t.Errorf("Loading = %+v after ready, want nil", st.Loading)
	}

	select {
	case e := <-events:
		if e.Progress == nil || e.Message != "step 1" {
			t.Errorf("first event = %+v, want progress and message \"step 1\"", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ProcessLoadingEvent emitted")
	}
}

// TestProcessCommand_LoadingProgressStalls checks that repeating the same
// progress report does not hold the start open past the health check timeout.
func TestProcessCommand_LoadingProgressStalls(t *testing.T) {
	skipIfNoSimpleResponder(t)

	mock := progressUpstream(t, 1000, false)
	cmd, _ := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:           cmd,
		Proxy:         mock.URL,
		CheckEndpoint: "/health",
	})

	ctx, cancelCtx := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCtx()
	err := p.EnsureReady(ctx, 1500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "without loading progress") {
		t.Fatalf("EnsureReady error = %v, want a timeout without loading progress", err)
	}
}
