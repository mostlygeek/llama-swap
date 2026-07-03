package router

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// strictTestGate returns a strict-mode gate with a zero maxWait so rejection
// tests do not sit in the bounded-wait loop.
func strictTestGate(budgetMB int, probe memProbe, estimates map[string]int) *memGate {
	g := newTestGate(budgetMB, probe, estimates)
	g.strict = true
	g.maxWait = 0
	return g
}

func TestMemGate_Strict_RejectsModelWithoutEstimate(t *testing.T) {
	g := strictTestGate(1000, staticProbe(0, true), nil)

	err := g.EnsureFits("mystery", map[string]process.Process{}, nil, gateLog())

	var mbe *MemoryBudgetError
	if !errors.As(err, &mbe) {
		t.Fatalf("expected MemoryBudgetError, got %v", err)
	}
	if mbe.StatusCode() != 503 {
		t.Fatalf("expected 503, got %d", mbe.StatusCode())
	}
}

func TestMemGate_Strict_RejectsWhenNoReading(t *testing.T) {
	g := strictTestGate(1000, staticProbe(0, false), map[string]int{"b": 100})

	err := g.EnsureFits("b", map[string]process.Process{}, nil, gateLog())

	if err == nil {
		t.Fatal("strict mode must reject when no memory reading is available")
	}
}

func TestMemGate_Strict_RejectsOverBudgetWithNoVictims(t *testing.T) {
	// used 900 + estimate 500 > budget 1000 and nothing to evict.
	g := strictTestGate(1000, staticProbe(900, true), map[string]int{"b": 500})

	err := g.EnsureFits("b", map[string]process.Process{}, nil, gateLog())

	var mbe *MemoryBudgetError
	if !errors.As(err, &mbe) {
		t.Fatalf("expected MemoryBudgetError, got %v", err)
	}
	if got := mbe.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header on budget rejection")
	}
}

func TestMemGate_Strict_AdmitsAfterEviction(t *testing.T) {
	perModel := map[string]int{"a": 600}
	a := readyProc("a", 100)
	procs := map[string]process.Process{"a": a}

	g := strictTestGate(1000, nil, map[string]int{"b": 500})
	g.probe = residentProbe(procs, perModel)

	if err := g.EnsureFits("b", procs, nil, gateLog()); err != nil {
		t.Fatalf("expected admission after evicting a, got %v", err)
	}
	if a.stopCalls.Load() == 0 {
		t.Fatal("expected a to be evicted")
	}
	if g.reservations["b"] != 500 {
		t.Fatalf("expected reservation of 500 for b, got %d", g.reservations["b"])
	}
}

func TestMemGate_Reservations_BlockJointOvershoot(t *testing.T) {
	// Live usage stays at 1000 (m1's load has not materialized yet), budget
	// 10000. m1 (est 6000) admits and reserves. m2 (est 6000) must then be
	// rejected: 1000 + 6000 reserved + 6000 = 13000 > 10000. This is the
	// 2026-07-01 co-load scenario.
	g := strictTestGate(10000, staticProbe(1000, true), map[string]int{"m1": 6000, "m2": 6000})

	if err := g.EnsureFits("m1", map[string]process.Process{}, nil, gateLog()); err != nil {
		t.Fatalf("m1 should fit: %v", err)
	}
	if err := g.EnsureFits("m2", map[string]process.Process{}, nil, gateLog()); err == nil {
		t.Fatal("m2 must be rejected while m1's reservation is held")
	}

	g.Release("m1")
	if err := g.EnsureFits("m2", map[string]process.Process{}, nil, gateLog()); err != nil {
		t.Fatalf("m2 should fit after m1's release: %v", err)
	}
}

func TestMemGate_Reservations_HolderNeverEvicted(t *testing.T) {
	// m1 holds a reservation and is resident with the OLDEST LastUse; the
	// victim must still be m2.
	perModel := map[string]int{"m1": 400, "m2": 400}
	m1 := readyProc("m1", 100)
	m2 := readyProc("m2", 200)
	procs := map[string]process.Process{"m1": m1, "m2": m2}

	g := strictTestGate(1000, nil, map[string]int{"m3": 500})
	g.probe = residentProbe(procs, perModel)
	g.reservations = map[string]int{"m1": 400}

	// live 800 + reserved(m1, but m1 is also resident: reservation double
	// counts here, which is conservative) + est 500 is over budget either
	// way; what matters is WHO gets evicted.
	_ = g.EnsureFits("m3", procs, nil, gateLog())

	if m1.stopCalls.Load() != 0 {
		t.Fatal("reservation holder m1 must never be picked as victim")
	}
	if m2.stopCalls.Load() == 0 {
		t.Fatal("expected m2 to be evicted")
	}
}

func TestMemGate_HostFloor_BlocksAdmission(t *testing.T) {
	// GPU budget fits comfortably, but the host floor does not:
	// avail 2000 - est 1000 = 1000 < floor 1500.
	g := strictTestGate(10000, staticProbe(100, true), map[string]int{"b": 1000})
	g.hostFloorMB = 1500
	g.hostAvail = func() (int, bool) { return 2000, true }

	err := g.EnsureFits("b", map[string]process.Process{}, nil, gateLog())

	if err == nil {
		t.Fatal("expected rejection on host floor breach")
	}
}

func TestMemGate_HostFloor_PassesWithHeadroom(t *testing.T) {
	g := strictTestGate(10000, staticProbe(100, true), map[string]int{"b": 1000})
	g.hostFloorMB = 1500
	g.hostAvail = func() (int, bool) { return 5000, true }

	if err := g.EnsureFits("b", map[string]process.Process{}, nil, gateLog()); err != nil {
		t.Fatalf("expected admission with host headroom, got %v", err)
	}
}

func TestMemGate_Strict_WaitsForRelease(t *testing.T) {
	// m2 does not fit while m1's reservation is held, but fits once it is
	// released. With a generous maxWait the admission must block, observe the
	// release, and succeed rather than reject.
	g := strictTestGate(10000, staticProbe(1000, true), map[string]int{"m1": 6000, "m2": 6000})
	g.maxWait = 10 * time.Second

	if err := g.EnsureFits("m1", map[string]process.Process{}, nil, gateLog()); err != nil {
		t.Fatalf("m1 should fit: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- g.EnsureFits("m2", map[string]process.Process{}, nil, gateLog())
	}()

	time.Sleep(200 * time.Millisecond)
	g.Release("m1")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("m2 should be admitted after m1's release, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("m2 admission did not complete after release")
	}
}

func TestMemGate_NonStrict_StillFailsOpen(t *testing.T) {
	// The historical contract: non-strict gates never return an error, even
	// when hopelessly over budget with nothing to evict.
	g := newTestGate(1000, staticProbe(5000, true), map[string]int{"b": 500})

	if err := g.EnsureFits("b", map[string]process.Process{}, nil, gateLog()); err != nil {
		t.Fatalf("non-strict gate must fail open, got %v", err)
	}
}

func TestMemGate_ReleaseNilSafe(t *testing.T) {
	var g *memGate
	g.Release("x") // must not panic

	g2 := newTestGate(100, staticProbe(0, true), nil)
	g2.Release("never-reserved") // nil map delete must not panic
}

func TestMemoryBudgetError_RendersAs503(t *testing.T) {
	err := &MemoryBudgetError{Model: "big", Detail: "over budget", RetryAfter: 7}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	shared.SendError(rec, req, err)

	if rec.Code != 503 {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("expected Retry-After 7, got %q", got)
	}
}
