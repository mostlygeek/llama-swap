package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedClock is an arbitrary instant with a non-zero time-of-day, so a zone
// conversion visibly changes the rendered value.
var fixedClock = time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)

func newTestSys() *SysProvider {
	return NewSysProvider(func() time.Time { return fixedClock })
}

func callSys(t *testing.T, p *SysProvider, args string) Result {
	t.Helper()

	var parsed map[string]json.RawMessage
	if args != "" {
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			t.Fatalf("bad test args: %v", err)
		}
	}

	result, err := p.Call(context.Background(), "now", parsed)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return result
}

func TestSysProvider_Tools(t *testing.T) {
	tools, err := newTestSys().Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "now" {
		t.Fatalf("tools = %v, want one named %q (local, unqualified)", namesOf(tools), "now")
	}

	// Read-only, but deliberately not idempotent: the answer changes on every
	// call. The two hints are independent, which a blanket helper gets wrong.
	annotations := tools[0].Annotations
	if annotations == nil {
		t.Fatal("no annotations")
	}
	if !annotations.ReadOnlyHint {
		t.Error("readOnlyHint = false, want true")
	}
	if annotations.IdempotentHint {
		t.Error("idempotentHint = true, want false: the clock changes between calls")
	}
}

func TestSysProvider_NowDefaultsToUTC(t *testing.T) {
	got := callSys(t, newTestSys(), "")

	if got.IsError {
		t.Fatalf("isError = true: %s", got.Content)
	}
	for _, want := range []string{
		"UTC: 2026-03-14T15:09:26Z",
		"server local:",
		"unix: 1773500966",
	} {
		if !strings.Contains(got.Content, want) {
			t.Errorf("content missing %q:\n%s", want, got.Content)
		}
	}
}

func TestSysProvider_NowInNamedZone(t *testing.T) {
	if _, err := time.LoadLocation("Asia/Tokyo"); err != nil {
		t.Skip("no system zoneinfo database available")
	}

	got := callSys(t, newTestSys(), `{"timezone":"Asia/Tokyo"}`)

	if got.IsError {
		t.Fatalf("isError = true: %s", got.Content)
	}
	// 15:09:26Z is 00:09:26 the next day in JST (UTC+9).
	if !strings.Contains(got.Content, "Asia/Tokyo: 2026-03-15T00:09:26+09:00") {
		t.Errorf("converted time missing:\n%s", got.Content)
	}
	if !strings.Contains(got.Content, "UTC+09:00") {
		t.Errorf("offset not spelled out:\n%s", got.Content)
	}
	// UTC stays in the answer so the model has an anchor.
	if !strings.Contains(got.Content, "UTC: 2026-03-14T15:09:26Z") {
		t.Errorf("UTC dropped when a zone was requested:\n%s", got.Content)
	}
}

// An unknown zone is something the model can correct, so it is a tool error
// carrying the fallback, not a protocol error.
func TestSysProvider_UnknownZoneIsAToolError(t *testing.T) {
	got := callSys(t, newTestSys(), `{"timezone":"Mars/Olympus_Mons"}`)

	if !got.IsError {
		t.Fatal("isError = false, want true")
	}
	if !strings.Contains(got.Content, "unknown timezone") {
		t.Errorf("content = %q", got.Content)
	}
	// The usable answer is still there rather than being withheld.
	if !strings.Contains(got.Content, "UTC: 2026-03-14T15:09:26Z") {
		t.Errorf("fallback times missing:\n%s", got.Content)
	}
}

func TestSysProvider_UnknownTool(t *testing.T) {
	if _, err := newTestSys().Call(context.Background(), "nope", nil); err == nil {
		t.Fatal("Call succeeded, want an error for an unknown tool")
	}
}

func TestSysProvider_DefaultsTheClock(t *testing.T) {
	before := time.Now()
	result, err := NewSysProvider(nil).Call(context.Background(), "now", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("isError = true: %s", result.Content)
	}
	if !strings.Contains(result.Content, before.UTC().Format("2006-01-02")) {
		t.Errorf("a nil clock did not fall back to time.Now:\n%s", result.Content)
	}
}
