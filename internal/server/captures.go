package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

// ReqRespCapture is a stored request/response pair for a single metered request.
type ReqRespCapture struct {
	ID          int               `json:"id"`
	ReqPath     string            `json:"req_path"`
	ReqHeaders  map[string]string `json:"req_headers"`
	ReqBody     []byte            `json:"req_body"`
	RespHeaders map[string]string `json:"resp_headers"`
	RespBody    []byte            `json:"resp_body"`
}

// captureStore persists compressed (zstd+CBOR) capture blobs keyed by activity
// ID. The in-memory cache.Cache and the file-backed diskCapture both satisfy it.
type captureStore interface {
	Add(id int, data []byte) error
	Get(id int) ([]byte, error)
	Has(id int) bool
}

// combineCapture layers the given tiers, ignoring unconfigured (nil) ones. It
// returns a single store when one tier is set, a tiered store when several are,
// or nil when none is. Reads probe tiers in order (memory before disk); writes
// go to every tier so a capture evicted from memory still survives on disk.
func combineCapture(logger *logmon.Monitor, tiers ...captureStore) captureStore {
	var active []captureStore
	for _, tier := range tiers {
		if tier != nil {
			active = append(active, tier)
		}
	}
	switch len(active) {
	case 0:
		return nil
	case 1:
		return active[0]
	default:
		return &tieredCapture{tiers: active, logger: logger}
	}
}

// tieredCapture spans an ordered set of captureStore tiers.
type tieredCapture struct {
	tiers  []captureStore
	logger *logmon.Monitor
}

// Add writes every tier and reports partial failures: a capture the first
// tiers accept but a later one rejects would silently vanish once the working
// tiers evict it, so the failure is warned about instead of hidden.
func (t *tieredCapture) Add(id int, data []byte) error {
	var lastErr error
	stored := false
	for _, tier := range t.tiers {
		if err := tier.Add(id, data); err != nil {
			lastErr = err
		} else {
			stored = true
		}
	}
	if !stored {
		return lastErr
	}
	if lastErr != nil && t.logger != nil {
		t.logger.Warnf("capture %d stored in only some tiers: %v (may be lost after eviction or restart)", id, lastErr)
	}
	return nil
}

func (t *tieredCapture) Get(id int) ([]byte, error) {
	var lastErr error
	for _, tier := range t.tiers {
		data, err := tier.Get(id)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errCaptureNotFound
	}
	return nil, lastErr
}

func (t *tieredCapture) Has(id int) bool {
	for _, tier := range t.tiers {
		if tier.Has(id) {
			return true
		}
	}
	return false
}

// Close releases each tier that is an io.Closer (the disk tier's reconcile
// goroutine; the in-memory tier is not a Closer and is skipped).
func (t *tieredCapture) Close() error {
	var errs []error
	for _, tier := range t.tiers {
		if closer, ok := tier.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// captureFields is a bitmask controlling what a route stores in a ReqRespCapture.
type captureFields uint

const (
	captureReqHeaders captureFields = 1 << iota
	captureReqBody
	captureRespHeaders
	captureRespBody
)

const (
	captureReqAll  = captureReqHeaders | captureReqBody
	captureRespAll = captureRespHeaders | captureRespBody
	captureAll     = captureReqAll | captureRespAll
)

// captureFieldsByPath overrides the default capture mask for routes carrying
// large binary payloads (audio/image) where storing the full body is wasteful.
var captureFieldsByPath = map[string]captureFields{
	"/v1/audio/speech":         captureReqAll | captureRespHeaders,
	"/v1/audio/voices":         captureReqHeaders | captureRespAll,
	"/v1/audio/transcriptions": captureReqHeaders | captureRespHeaders | captureRespBody,
	"/v1/images/generations":   captureReqAll | captureRespHeaders,
	"/v1/images/edits":         captureReqHeaders | captureRespHeaders,
	"/sdapi/v1/txt2img":        captureReqAll | captureRespHeaders,
	"/sdapi/v1/img2img":        captureReqHeaders | captureRespHeaders,
}

// captureFieldsFor returns the capture mask for a request path. Unlisted routes
// (the OpenAI-compatible JSON endpoints) capture everything.
func captureFieldsFor(path string) captureFields {
	if cf, ok := captureFieldsByPath[path]; ok {
		return cf
	}
	return captureAll
}

// zstdEncOptions are the shared zstd encoder options for maximum compression.
var zstdEncOptions = []zstd.EOption{
	zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
}

// zstdEncPool pools zstd.Encoder instances to reduce allocations.
var zstdEncPool = &sync.Pool{
	New: func() interface{} {
		enc, _ := zstd.NewWriter(nil, zstdEncOptions...)
		return enc
	},
}

// zstdDecPool pools zstd.Decoder instances to reduce allocations.
var zstdDecPool = &sync.Pool{
	New: func() interface{} {
		dec, _ := zstd.NewReader(nil)
		return dec
	},
}

// compressCapture marshals a ReqRespCapture to CBOR and compresses it with zstd.
// Returns the compressed bytes and the original CBOR byte count for logging.
func compressCapture(c *ReqRespCapture) ([]byte, int, error) {
	cborBytes, err := cbor.Marshal(c)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal capture: %w", err)
	}
	zenc := zstdEncPool.Get().(*zstd.Encoder)
	defer zstdEncPool.Put(zenc)
	return zenc.EncodeAll(cborBytes, nil), len(cborBytes), nil
}

// decompressCapture decompresses zstd-compressed CBOR into a ReqRespCapture.
func decompressCapture(data []byte) (*ReqRespCapture, error) {
	dec := zstdDecPool.Get().(*zstd.Decoder)
	defer zstdDecPool.Put(dec)
	cborBytes, err := dec.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress capture: %w", err)
	}
	var capture ReqRespCapture
	if err := cbor.Unmarshal(cborBytes, &capture); err != nil {
		return nil, fmt.Errorf("unmarshal capture: %w", err)
	}
	return &capture, nil
}

// addCapture compresses and stores a capture in the cache. Returns true if the
// capture was stored.
func (mp *metricsMonitor) addCapture(capture ReqRespCapture) bool {
	if !mp.enableCaptures {
		return false
	}

	compressed, uncompressedBytes, err := compressCapture(&capture)
	if err != nil {
		mp.logger.Warnf("failed to compress capture: %v, skipping", err)
		return false
	}

	if err := mp.captureCache.Add(capture.ID, compressed); err != nil {
		mp.logger.Warnf("capture %d too large (%d bytes), skipping: %v", capture.ID, len(compressed), err)
		return false
	}

	compressionRatio := (1 - float64(len(compressed))/float64(uncompressedBytes)) * 100
	mp.logger.Debugf("Capture %d compressed and saved: %d bytes -> %d bytes (%.1f%% compression)", capture.ID, uncompressedBytes, len(compressed), compressionRatio)
	return true
}

// getCaptureByID decompresses and unmarshals a capture by ID. Returns nil if
// the capture is not found or decompression fails.
func (mp *metricsMonitor) getCaptureByID(id int) *ReqRespCapture {
	if mp.captureCache == nil {
		return nil
	}
	data, err := mp.captureCache.Get(id)
	if err != nil {
		return nil
	}
	capture, err := decompressCapture(data)
	if err != nil {
		mp.logger.Warnf("failed to decompress capture %d: %v", id, err)
		return nil
	}
	return capture
}

// sensitiveHeaders lists headers that are redacted in captures.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
}

// headerMap flattens an http.Header to a single-value map.
func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for key, values := range h {
		if len(values) > 0 {
			m[key] = values[0]
		}
	}
	return m
}

// redactHeaders replaces sensitive header values in-place with "[REDACTED]".
func redactHeaders(headers map[string]string) {
	for key := range headers {
		if sensitiveHeaders[strings.ToLower(key)] {
			headers[key] = "[REDACTED]"
		}
	}
}
