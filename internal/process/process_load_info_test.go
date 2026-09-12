package process

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// TestLoadEstimate pins the median-of-history math the ETA estimate is built
// from: empty history yields no estimate, an even count averages the two middle
// values, and an odd count takes the middle after sorting (so a slow outlier
// does not drag the estimate up).
func TestLoadEstimate(t *testing.T) {
	tests := []struct {
		name string
		in   []int64
		want int64
	}{
		{"nil", nil, 0},
		{"empty", []int64{}, 0},
		{"single", []int64{5000}, 5000},
		{"two averaged", []int64{5000, 9000}, 7000},
		{"two averaged truncates toward zero", []int64{5000, 9001}, 7000}, // 14001/2 == 7000, not 7000.5
		{"three median ignores outlier", []int64{5000, 9000, 100000}, 9000},
		{"three median unsorted", []int64{9000, 5000, 7000}, 7000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loadEstimate(tt.in); got != tt.want {
				t.Errorf("loadEstimate(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestLoadEstimate_DoesNotMutateInput guards the copy-before-sort in
// loadEstimate: callers keep loadHistoryMs in insertion order so the newest
// entries can be capped off the tail, which a sort-in-place would corrupt.
func TestLoadEstimate_DoesNotMutateInput(t *testing.T) {
	in := []int64{9000, 5000, 7000}
	_ = loadEstimate(in)
	want := []int64{9000, 5000, 7000}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("loadEstimate mutated its input: got %v, want %v", in, want)
		}
	}
}

// TestAppendLoadHistory pins the cap behavior the estimate depends on: the
// helper keeps only the most recent loadHistorySize durations, in order, and
// drops the OLDEST when full — a reversed slice direction (keeping the oldest)
// would make the estimate stale forever, and length-only assertions miss it.
func TestAppendLoadHistory(t *testing.T) {
	tests := []struct {
		name string
		in   []int64
		dur  int64
		want []int64
	}{
		{"nil grows", nil, 5000, []int64{5000}},
		{"under cap appends", []int64{5000}, 9000, []int64{5000, 9000}},
		{"fills to cap", []int64{5000, 9000}, 7000, []int64{5000, 9000, 7000}},
		{"over cap drops oldest, keeps newest", []int64{5000, 9000, 7000}, 8000, []int64{9000, 7000, 8000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendLoadHistory(tt.in, tt.dur); !slices.Equal(got, tt.want) {
				t.Errorf("appendLoadHistory(%v, %d) = %v, want %v", tt.in, tt.dur, got, tt.want)
			}
		})
	}
}

// TestProcessCommand_LoadInfo_FirstLoadRecordsEstimate verifies the observable
// contract after a successful load: StartedAt is cleared to 0 (the process is
// no longer starting) and EstimateMs is populated from the load just recorded.
func TestProcessCommand_LoadInfo_FirstLoadRecordsEstimate(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { p.Stop(testStopTimeout) }) //nolint: errcheck

	// Before any load: no in-flight start, no history.
	if li := p.LoadInfo(); li.StartedAt != 0 || li.EstimateMs != 0 {
		t.Fatalf("before load: LoadInfo = %+v, want zero", li)
	}

	_ = runAsync(t, p)

	li := p.LoadInfo()
	if li.StartedAt != 0 {
		t.Errorf("after ready: StartedAt = %d, want 0 (cleared once ready)", li.StartedAt)
	}
	if li.EstimateMs <= 0 {
		t.Errorf("after ready: EstimateMs = %d, want > 0 (first load recorded)", li.EstimateMs)
	}
}

// TestProcessCommand_LoadInfo_StartedAtSetWhileStarting proves StartedAt is
// published while the process sits in StateStarting. The health check points at
// a port nothing listens on, so the child runs but readiness never arrives and
// the process stays starting long enough to observe.
func TestProcessCommand_LoadInfo_StartedAtSetWhileStarting(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, _ := simpleResponderCmd(t, "-silent")
	deadPort := getFreePort(t)
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", deadPort),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 15,
	})
	t.Cleanup(func() { p.Stop(testStopTimeout) }) //nolint: errcheck

	go func() { _ = p.Run(10 * time.Second) }()
	waitForState(t, p, StateStarting)

	li := p.LoadInfo()
	if li.StartedAt <= 0 {
		t.Errorf("while starting: StartedAt = %d, want > 0", li.StartedAt)
	}
	if li.EstimateMs != 0 {
		t.Errorf("while starting with no history: EstimateMs = %d, want 0", li.EstimateMs)
	}
}

// TestProcessCommand_LoadInfo_ClearedOnFailedStart verifies a start that never
// reaches readiness leaves no trace: StartedAt is cleared back to 0 and no
// duration is fed into the estimate (a failed load is not a load time).
func TestProcessCommand_LoadInfo_ClearedOnFailedStart(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, _ := simpleResponderCmd(t, "-silent")
	deadPort := getFreePort(t)
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", deadPort),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 1,
	})
	t.Cleanup(func() { p.Stop(testStopTimeout) }) //nolint: errcheck

	if err := p.Run(5 * time.Second); err == nil {
		t.Fatal("Run: expected a failed start, got nil error")
	}
	waitForState(t, p, StateStopped)

	li := p.LoadInfo()
	if li.StartedAt != 0 {
		t.Errorf("after failed start: StartedAt = %d, want 0", li.StartedAt)
	}
	if li.EstimateMs != 0 {
		t.Errorf("after failed start: EstimateMs = %d, want 0 (failed load not recorded)", li.EstimateMs)
	}
}

// TestProcessCommand_LoadInfo_HistoryIsCapped drives several successful load
// cycles on one process and asserts the retained history never grows past
// loadHistorySize, so the estimate always reflects only the most recent loads.
func TestProcessCommand_LoadInfo_HistoryIsCapped(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 10,
	})
	t.Cleanup(func() { p.Stop(testStopTimeout) }) //nolint: errcheck

	cycles := loadHistorySize + 2
	for i := range cycles {
		runErr := runAsync(t, p)
		if err := p.Stop(testStopTimeout); err != nil {
			t.Fatalf("cycle %d: Stop: %v", i, err)
		}
		select {
		case <-runErr:
		case <-time.After(testReturnTimeout):
			t.Fatalf("cycle %d: Run did not return after Stop", i)
		}
		waitForState(t, p, StateStopped)
	}

	// Reading the unexported, non-atomic loadHistoryMs directly is safe here:
	// it is mutated only inside run()'s success branch, strictly before that
	// goroutine answers the Stop() we awaited above, so the channel round-trip
	// establishes a happens-before with this read. (The cap direction itself is
	// pinned purely by TestAppendLoadHistory.)
	if got := len(p.loadHistoryMs); got != loadHistorySize {
		t.Errorf("after %d cycles: history length = %d, want %d (capped)", cycles, got, loadHistorySize)
	}
	if li := p.LoadInfo(); li.EstimateMs <= 0 {
		t.Errorf("after cycles: EstimateMs = %d, want > 0", li.EstimateMs)
	}
}
