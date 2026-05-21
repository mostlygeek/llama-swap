package server

import (
	"bytes"
	"io"
	"testing"

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
	mm := newMetricsMonitor(logmon.NewWriter(io.Discard), 100, 5)
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
	mm := newMetricsMonitor(logmon.NewWriter(io.Discard), 100, 0)
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

func TestServer_CaptureFieldsFor(t *testing.T) {
	if got := captureFieldsFor("/v1/chat/completions"); got != captureAll {
		t.Fatalf("default = %b, want captureAll", got)
	}
	if got := captureFieldsFor("/v1/audio/speech"); got != captureReqAll|captureRespHeaders {
		t.Fatalf("/v1/audio/speech = %b", got)
	}
}
