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

// restoreTestServer returns a server whose on_unload hook restores m1 after a
// short delay, and a channel receiving every request the local router got.
func restoreTestServer(t *testing.T) (*Server, *stubRouter, chan swaputil.ReqContextData) {
	t.Helper()
	got := make(chan swaputil.ReqContextData, 10)
	local := newStubRouter([]string{"m1", "m2"}, "ok")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		data, _ := swaputil.ReadContext(r.Context())
		got <- data
		w.WriteHeader(http.StatusOK)
	}
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Hooks: config.HooksConfig{
		OnUnload: config.HookOnUnload{Restore: []string{"m1"}, Delay: 1},
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

func TestServer_Restore_StopsWithServer(t *testing.T) {
	s, _, got := restoreTestServer(t)
	s.restore.stop()

	emitStop("m2", string(process.StateStopping))

	expectNoRestore(t, got)
}
