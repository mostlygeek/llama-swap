package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// restoreTestServer returns a server whose on_unload hook restores m1 (or the
// given models) after a short delay, and a channel receiving every request the
// local router got.
func restoreTestServer(t *testing.T, restore ...string) (*Server, *stubRouter, chan swaputil.ReqContextData) {
	t.Helper()
	if len(restore) == 0 {
		restore = []string{"m1"}
	}
	got := make(chan swaputil.ReqContextData, 10)
	local := newStubRouter([]string{"m1", "m2"}, "ok")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		data, _ := swaputil.ReadContext(r.Context())
		got <- data
		w.WriteHeader(http.StatusOK)
	}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Hooks: config.HooksConfig{
		OnUnload: config.HookOnUnload{Restore: restore, Delay: 1},
	}}
	s.startRestore()
	s.restore.delay = 20 * time.Millisecond
	t.Cleanup(s.restore.stop)
	return s, local, got
}

func emitStop(model, from string) {
	event.Emit(swaputil.ProcessStateChangeEvent{ProcessName: model, OldState: from, NewState: string(process.StateStopped)})
}

func expectRestore(t *testing.T, got chan swaputil.ReqContextData) {
	t.Helper()
	select {
	case d := <-got:
		if d.ModelID != "m1" || !d.NoEvict {
			t.Fatalf("restore request=%+v want ModelID m1 with NoEvict", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restore request not sent")
	}
}

func expectNoRestore(t *testing.T, got chan swaputil.ReqContextData) {
	t.Helper()
	select {
	case d := <-got:
		t.Fatalf("unexpected restore request %+v", d)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestServer_Restore_AfterModelStops(t *testing.T) {
	_, _, got := restoreTestServer(t)

	emitStop("m2", string(process.StateStopping))

	expectRestore(t, got)
	expectNoRestore(t, got)
}

func TestServer_Restore_DebouncesBurstOfStops(t *testing.T) {
	_, _, got := restoreTestServer(t)

	for range 5 {
		emitStop("m2", string(process.StateStopping))
	}

	expectRestore(t, got)
	expectNoRestore(t, got)
}

func TestServer_Restore_IgnoresFailedStart(t *testing.T) {
	_, _, got := restoreTestServer(t)

	emitStop("m1", string(process.StateStarting))

	expectNoRestore(t, got)
}

func TestServer_Restore_SkipsRunningModel(t *testing.T) {
	_, local, got := restoreTestServer(t)
	local.running = map[string]process.ProcessState{"m1": process.StateReady}

	emitStop("m2", string(process.StateStopping))

	expectNoRestore(t, got)
}

func TestServer_Restore_WaitsForResume(t *testing.T) {
	s, _, got := restoreTestServer(t)
	s.pause.set(true)

	emitStop("m2", string(process.StateStopping))
	expectNoRestore(t, got)

	s.pause.set(false)
	event.Emit(swaputil.InferencePausedEvent{Paused: false})
	expectRestore(t, got)
}

// TestServer_Restore_BoundedWarmUp checks that an upstream which never answers
// the warm-up request does not hold the pass: the next model still gets loaded.
func TestServer_Restore_BoundedWarmUp(t *testing.T) {
	s, local, got := restoreTestServer(t, "m1", "m2")
	s.restore.warmTimeout = 50 * time.Millisecond
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		data, _ := swaputil.ReadContext(r.Context())
		got <- data
		if data.ModelID == "m1" {
			<-r.Context().Done() // healthy but never answers
		}
		w.WriteHeader(http.StatusOK)
	}

	emitStop("m2", string(process.StateStopping))

	for _, want := range []string{"m1", "m2"} {
		select {
		case d := <-got:
			if d.ModelID != want {
				t.Fatalf("request for %s, want %s", d.ModelID, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no restore request for %s", want)
		}
	}
}

// TestServer_Restore_SupersededPassIsSkipped checks that a pass whose timer
// fired while an earlier pass held the lock does nothing once a newer stop
// restarted the quiet period: only the newest pass restores, after its delay.
func TestServer_Restore_SupersededPassIsSkipped(t *testing.T) {
	s, _, got := restoreTestServer(t)
	s.restore.busy.Lock() // an earlier pass is still running

	emitStop("m2", string(process.StateStopping))
	time.Sleep(100 * time.Millisecond) // its timer fires and waits for busy

	s.restore.mu.Lock()
	s.restore.delay = 400 * time.Millisecond
	s.restore.mu.Unlock()
	emitStop("m2", string(process.StateStopping))
	time.Sleep(50 * time.Millisecond) // let the event reach the restorer
	s.restore.busy.Unlock()

	expectNoRestore(t, got) // the stale pass must not run early
	expectRestore(t, got)   // the newest pass runs after its own delay
}

func TestServer_Restore_StopsWithServer(t *testing.T) {
	s, _, got := restoreTestServer(t)
	s.restore.stop()

	emitStop("m2", string(process.StateStopping))

	expectNoRestore(t, got)
}
