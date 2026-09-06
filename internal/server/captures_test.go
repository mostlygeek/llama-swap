package server

import (
	"bytes"
	"io"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/cache"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

func TestServer_CaptureCompressRoundtrip(t *testing.T) {
	orig := &ReqRespCapture{
		ID:          7,
		ReqPath:     "/v1/chat/completions",
		ReqHeaders:  map[string]string{"Content-Type": "application/json"},
		ReqBody:     []byte(`{"model":"m"}`),
		RespHeaders: map[string]string{"Content-Type": "application/json"},
		RespBody:    []byte(`{"usage":{}}`),
	}

	compressed, uncompressed, err := compressCapture(orig)
	if err != nil {
		t.Fatalf("compressCapture: %v", err)
	}
	if uncompressed == 0 || len(compressed) == 0 {
		t.Fatalf("unexpected sizes: uncompressed=%d compressed=%d", uncompressed, len(compressed))
	}

	got, err := decompressCapture(compressed)
	if err != nil {
		t.Fatalf("decompressCapture: %v", err)
	}
	if got.ID != orig.ID || got.ReqPath != orig.ReqPath ||
		!bytes.Equal(got.ReqBody, orig.ReqBody) || !bytes.Equal(got.RespBody, orig.RespBody) {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestServer_CaptureStoreAndRetrieve(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 100, 5)
	if !mm.enableCaptures {
		t.Fatal("captures should be enabled with non-zero buffer")
	}

	capture := ReqRespCapture{ID: 3, ReqPath: "/v1/chat/completions", ReqBody: []byte("hello")}
	if !mm.addCapture(capture) {
		t.Fatal("addCapture returned false")
	}

	got := mm.getCaptureByID(3)
	if got == nil || !bytes.Equal(got.ReqBody, []byte("hello")) {
		t.Fatalf("getCaptureByID = %+v", got)
	}
	if mm.getCaptureByID(999) != nil {
		t.Fatal("expected nil for unknown capture ID")
	}
}

func TestServer_CaptureDisabled(t *testing.T) {
	mm := newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 100, 0)
	if mm.enableCaptures {
		t.Fatal("captures should be disabled with zero buffer")
	}
	if mm.addCapture(ReqRespCapture{ID: 1}) {
		t.Fatal("addCapture should return false when disabled")
	}
	if mm.getCaptureByID(1) != nil {
		t.Fatal("getCaptureByID should return nil when disabled")
	}
}

// failingStore is a captureStore whose every method fails with err.
type failingStore struct{ err error }

func (f failingStore) Add(int, []byte) error   { return f.err }
func (f failingStore) Get(int) ([]byte, error) { return nil, f.err }
func (f failingStore) Has(int) bool            { return false }

// TestServer_CaptureTierPartialFailureIsReported verifies a capture the memory
// tier accepts but the disk tier rejects still reports success (it is stored,
// for now) yet warns: operators must know the copy will vanish after eviction
// or restart. When no tier stores the capture the error must be returned.
func TestServer_CaptureTierPartialFailureIsReported(t *testing.T) {
	var buf bytes.Buffer
	logger := logmon.NewWriter(&buf)
	layered := combineCapture(logger, cache.New(1<<20), failingStore{err: errExceedsCaptureMax})

	if err := layered.Add(1, []byte("payload")); err != nil {
		t.Fatalf("Add must succeed when one tier stored the capture: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("capture 1")) || !bytes.Contains(buf.Bytes(), []byte("exceeds")) {
		t.Fatalf("partial tier failure not reported, log = %q", buf.Bytes())
	}

	buf.Reset()
	if err := combineCapture(logger, cache.New(1<<20), cache.New(1<<20)).Add(2, []byte("ok")); err != nil {
		t.Fatalf("all tiers succeeding must not error: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("no warnings expected when every tier stored, log = %q", buf.Bytes())
	}

	buf.Reset()
	if err := combineCapture(logger, failingStore{err: errExceedsCaptureMax}).Add(3, []byte("x")); err == nil {
		t.Fatal("Add must fail when no tier stores the capture")
	}
}

func TestServer_CaptureFieldsFor(t *testing.T) {
	if got := captureFieldsFor("/v1/chat/completions"); got != captureAll {
		t.Fatalf("default = %b, want captureAll", got)
	}
	if got := captureFieldsFor("/v1/audio/speech"); got != captureReqAll|captureRespHeaders {
		t.Fatalf("/v1/audio/speech = %b", got)
	}
}
