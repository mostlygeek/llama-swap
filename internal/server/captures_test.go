package server

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
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

func TestServer_CaptureFieldsFor(t *testing.T) {
	if got := captureFieldsFor("/v1/chat/completions"); got != captureAll {
		t.Fatalf("default = %b, want captureAll", got)
	}
	if got := captureFieldsFor("/v1/audio/speech"); got != captureReqAll|captureRespHeaders {
		t.Fatalf("/v1/audio/speech = %b", got)
	}
}

// jsonBody builds an embeddings-style JSON body of roughly n bytes.
func jsonBody(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, 0, n+32)
	b = append(b, `{"data":[{"index":0,"embedding":[`...)
	for len(b) < n {
		b = append(b, fmt.Sprintf("%.7f,", r.Float64()*2-1)...)
	}
	return append(b, `0]}]}`...)
}

// A body larger than the encoder window (1 MiB) and block size takes the
// multi-block path with history shifting; it must still round-trip intact.
func TestServer_CaptureCompressLargeBody(t *testing.T) {
	orig := &ReqRespCapture{
		ID:       11,
		ReqPath:  "/v1/embeddings",
		ReqBody:  jsonBody(512<<10, 1),
		RespBody: jsonBody(3<<20, 2),
	}

	compressed, uncompressed, err := compressCapture(orig)
	if err != nil {
		t.Fatalf("compressCapture: %v", err)
	}
	if len(compressed) >= uncompressed {
		t.Fatalf("expected compression: compressed=%d uncompressed=%d", len(compressed), uncompressed)
	}

	got, err := decompressCapture(compressed)
	if err != nil {
		t.Fatalf("decompressCapture: %v", err)
	}
	if got.ID != orig.ID || !bytes.Equal(got.ReqBody, orig.ReqBody) || !bytes.Equal(got.RespBody, orig.RespBody) {
		t.Fatal("large body roundtrip mismatch")
	}
}

// More goroutines than zstdConcurrency share the single encoder/decoder;
// every goroutine must get back exactly what it compressed.
func TestServer_CaptureCompressConcurrent(t *testing.T) {
	const workers = 16
	const rounds = 8

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				orig := &ReqRespCapture{
					ID:       w*rounds + i,
					ReqPath:  "/v1/chat/completions",
					RespBody: jsonBody(64<<10, int64(w*rounds+i)),
				}
				compressed, _, err := compressCapture(orig)
				if err != nil {
					errs <- err
					return
				}
				got, err := decompressCapture(compressed)
				if err != nil {
					errs <- err
					return
				}
				if got.ID != orig.ID || !bytes.Equal(got.RespBody, orig.RespBody) {
					errs <- fmt.Errorf("worker %d round %d: roundtrip mismatch", w, i)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// BenchmarkCaptureCompress reports the transient allocations of compressing a
// 2 MiB embeddings-style capture through the shared encoder.
func BenchmarkCaptureCompress(b *testing.B) {
	capture := &ReqRespCapture{
		ID:       1,
		ReqPath:  "/v1/embeddings",
		ReqBody:  jsonBody(4<<10, 1),
		RespBody: jsonBody(2<<20, 2),
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := compressCapture(capture); err != nil {
			b.Fatal(err)
		}
	}
}

// heapInuse returns the live heap after a full GC.
func heapInuse() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

// BenchmarkCaptureEncoderRetained measures the heap one encoder retains once
// every sub-encoder has been used, which is what an encoder converges to on a
// busy server. "before" is the library defaults the capture sync.Pool used
// before #1106 (GOMAXPROCS sub-encoders, 8 MiB window); multiply its
// retained-MiB by the peak number of concurrent requests to get the old pool
// footprint. "after" is zstdEncOptions; since one encoder is shared, its
// retained-MiB is the total cap for capture compression.
//
// Measured on a 4-core host with a 2 MiB embeddings-style body:
//
//	before  82.7 retained-MiB per pooled encoder (20.7 MiB per core)
//	after   26.7 retained-MiB total
func BenchmarkCaptureEncoderRetained(b *testing.B) {
	cases := []struct {
		name string
		opts []zstd.EOption
	}{
		{"before", []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedBetterCompression)}},
		{"after", zstdEncOptions},
	}
	src := jsonBody(2<<20, 3)
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			base := heapInuse()
			enc, err := zstd.NewWriter(nil, tc.opts...)
			if err != nil {
				b.Fatal(err)
			}
			// Touch every sub-encoder so each allocates its history buffer.
			for i := 0; i < runtime.GOMAXPROCS(0)+1; i++ {
				enc.EncodeAll(src, nil)
			}
			retained := float64(heapInuse()-base) / (1 << 20)
			runtime.KeepAlive(enc)

			b.ReportAllocs()
			for b.Loop() {
				enc.EncodeAll(src, nil)
			}
			b.ReportMetric(retained, "retained-MiB")
		})
	}
}
