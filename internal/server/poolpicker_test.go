package server

import (
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
)

func newTestPickerN(members []string, spillover int) *poolPicker {
	return newPoolPicker(config.Config{
		Pools: map[string]config.PoolConfig{
			"pool": {Members: members, Spillover: spillover},
		},
	})
}

func newTestPicker(members []string) *poolPicker {
	return newTestPickerN(members, 1)
}

func TestPoolPicker_NotAPool(t *testing.T) {
	p := newTestPicker([]string{"a", "b"})
	if _, ok := p.Pick("not-a-pool", nil); ok {
		t.Fatal("expected ok=false for non-pool name")
	}
	if _, ok := p.Pick("a", nil); ok {
		t.Fatal("expected ok=false for a member's own name")
	}
}

func TestPoolPicker_ColdBurstDistributes(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	// 6 concurrent picks with no releases (a burst): reservation must spread
	// them evenly - exactly 2 per member.
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		m, ok := p.Pick("pool", nil)
		if !ok {
			t.Fatal("Pick: ok=false")
		}
		seen[m]++
	}
	for _, m := range []string{"a", "b", "c"} {
		if seen[m] != 2 {
			t.Fatalf("burst distribution uneven: %v", seen)
		}
	}
}

func TestPoolPicker_BurstOnSingleReadySpills(t *testing.T) {
	// THE race that motivated reserve-on-pick: one warm member, burst of 3.
	// Request 1 takes the warm member; requests 2+3 must go to DIFFERENT cold
	// members (not herd onto the warm one, not both onto one cold member).
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{"a": process.StateReady}

	m1, _ := p.Pick("pool", running)
	if m1 != "a" {
		t.Fatalf("pick 1: got %q, want a", m1)
	}
	m2, _ := p.Pick("pool", running)
	m3, _ := p.Pick("pool", running)
	if m2 == "a" || m3 == "a" {
		t.Fatalf("burst herded onto warm member: m2=%q m3=%q", m2, m3)
	}
	if m2 == m3 {
		t.Fatalf("burst stacked on one cold member: m2=%q m3=%q", m2, m3)
	}
}

func TestPoolPicker_PreferReadyWhenIdle(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{
		"b": process.StateReady,
		"c": process.StateReady,
	}
	seen := map[string]int{}
	for i := 0; i < 10; i++ {
		m, ok := p.Pick("pool", running)
		if !ok {
			t.Fatal("Pick: ok=false")
		}
		if m == "a" {
			t.Fatalf("picked cold member while idle ready members existed")
		}
		seen[m]++
		p.Release("pool", m)
	}
	if seen["b"] == 0 || seen["c"] == 0 {
		t.Fatalf("ready members not both used: seen=%v", seen)
	}
}

func TestPoolPicker_SpilloverThreshold2(t *testing.T) {
	p := newTestPickerN([]string{"a", "b"}, 2)
	running := map[string]process.ProcessState{"a": process.StateReady}

	m, _ := p.Pick("pool", running)
	if m != "a" {
		t.Fatalf("pick 1: got %q, want a", m)
	}
	m, _ = p.Pick("pool", running)
	if m != "a" {
		t.Fatalf("pick 2: got %q, want a (below spillover=2)", m)
	}
	m, _ = p.Pick("pool", running)
	if m != "b" {
		t.Fatalf("pick 3: got %q, want b (a saturated)", m)
	}
}

func TestPoolPicker_ReleaseReturnsTrafficToWarm(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{"a": process.StateReady}

	m1, _ := p.Pick("pool", running) // a
	p.Release("pool", m1)
	m2, _ := p.Pick("pool", running)
	if m2 != "a" {
		t.Fatalf("after release: got %q, want a", m2)
	}
}

func TestPoolPicker_SaturatedNoColdFallsBackToReady(t *testing.T) {
	p := newTestPicker([]string{"a", "b"})
	running := map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateReady,
	}
	p.Pick("pool", running)
	p.Pick("pool", running)
	// everything ready and saturated, no cold members -> least busy ready
	m, ok := p.Pick("pool", running)
	if !ok || (m != "a" && m != "b") {
		t.Fatalf("saturated pick: got %q ok=%v", m, ok)
	}
}

func TestPoolPicker_QueueBalancesOnStarting(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateStarting,
	}
	p.Pick("pool", running)    // a (ready, idle)
	p.Acquire("pool", "b")     // b warming with one queued
	m, _ := p.Pick("pool", running)
	if m != "c" {
		t.Fatalf("got %q, want c (least busy non-ready)", m)
	}
}

func TestPoolPicker_StoppingNeverPicked(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateStopping,
	}
	p.Pick("pool", running) // a, now saturated
	// spill must skip b (stopping) and take c (cold)
	m, _ := p.Pick("pool", running)
	if m != "c" {
		t.Fatalf("got %q, want c (stopping member must never be picked)", m)
	}
}

func TestPoolPicker_AllStoppingLastResort(t *testing.T) {
	p := newTestPicker([]string{"a", "b"})
	running := map[string]process.ProcessState{
		"a": process.StateStopping,
		"b": process.StateStopping,
	}
	// degenerate case: still return something rather than failing
	if _, ok := p.Pick("pool", running); !ok {
		t.Fatal("expected a last-resort pick")
	}
}

func TestPoolPicker_FailureQuarantine(t *testing.T) {
	p := newTestPicker([]string{"a", "b", "c"})
	running := map[string]process.ProcessState{"a": process.StateReady}
	p.Pick("pool", running) // a saturated

	p.NoteFailure("pool", "b")
	m, _ := p.Pick("pool", running)
	if m != "c" {
		t.Fatalf("got %q, want c (b is quarantined)", m)
	}
	// when every cold candidate is quarantined, fall back to them anyway
	p.NoteFailure("pool", "c")
	p.Release("pool", m)      // free the reservation on c from above
	p.Pick("pool", running)   // re-saturate... a is still at 1; this pick:
	// ready a saturated, spill = quarantined fallback {b, c}
	m2, _ := p.Pick("pool", running)
	if m2 == "a" {
		t.Fatalf("got %q, want a quarantined cold member as last spill resort", m2)
	}
}

func TestPoolPicker_QuarantineExpires(t *testing.T) {
	p := newTestPicker([]string{"a", "b"})
	running := map[string]process.ProcessState{"a": process.StateReady}
	ps := p.pools["pool"]
	ps.lastFailure["b"] = time.Now().Add(-failureQuarantine - time.Second)
	p.Pick("pool", running) // a saturated
	m, _ := p.Pick("pool", running)
	if m != "b" {
		t.Fatalf("got %q, want b (quarantine expired)", m)
	}
}

func TestPoolPicker_MemberPoolAndDirectAccounting(t *testing.T) {
	p := newTestPicker([]string{"a", "b"})
	pool, ok := p.MemberPool("a")
	if !ok || pool != "pool" {
		t.Fatalf("MemberPool(a): got %q ok=%v", pool, ok)
	}
	if _, ok := p.MemberPool("x"); ok {
		t.Fatal("MemberPool(x): want ok=false")
	}
	// direct traffic on a must steer pool traffic to b
	running := map[string]process.ProcessState{
		"a": process.StateReady,
		"b": process.StateReady,
	}
	p.Acquire("pool", "a")
	m, _ := p.Pick("pool", running)
	if m != "b" {
		t.Fatalf("got %q, want b (a carries direct load)", m)
	}
}

func TestPoolPicker_ReleaseNeverGoesNegative(t *testing.T) {
	p := newTestPicker([]string{"a"})
	p.Release("pool", "a")
	p.Release("pool", "a")
	running := map[string]process.ProcessState{"a": process.StateReady}
	m, ok := p.Pick("pool", running)
	if !ok || m != "a" {
		t.Fatalf("got %q ok=%v, want a/true", m, ok)
	}
}

func TestPoolPicker_NilSafe(t *testing.T) {
	var p *poolPicker
	if p.IsPool("anything") {
		t.Fatal("nil picker IsPool: want false")
	}
	if _, ok := p.Pick("anything", nil); ok {
		t.Fatal("nil picker Pick: want ok=false")
	}
	if _, ok := p.MemberPool("a"); ok {
		t.Fatal("nil picker MemberPool: want ok=false")
	}
	p.Acquire("anything", "a")
	p.Release("anything", "a")
	p.NoteFailure("anything", "a")
}
