package capcompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/store"
)

// countingCache is an in-memory store.CacheRepository that records how often
// it is read, so the memo can be asserted.
type countingCache struct {
	mu      sync.Mutex
	entries map[string]store.CacheEntry
	gets    atomic.Int64
	getErr  error
	setErr  error
}

func newCountingCache() *countingCache {
	return &countingCache{entries: map[string]store.CacheEntry{}}
}

func (c *countingCache) Get(_ context.Context, key string) (store.CacheEntry, bool, error) {
	c.gets.Add(1)
	if c.getErr != nil {
		return store.CacheEntry{}, false, c.getErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok, nil
}

func (c *countingCache) Set(_ context.Context, entry store.CacheEntry) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[entry.Key] = entry
	return nil
}

func (c *countingCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

func (c *countingCache) Prune(context.Context) error { return nil }

func (c *countingCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[key]
	return ok
}

func TestCapcompat_RefreshStoresDiscoveredCapabilities(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props_vision.json"),
	})
	cache := newCountingCache()
	svc := New(cache, nil)

	require.NoError(t, svc.Refresh(context.Background(), "k", up.client(t), "model-a"))

	assert.True(t, cache.has("k"))
	entry, _, err := cache.Get(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, cacheTTL, entry.TTL)
	assert.False(t, entry.Timestamp.IsZero())

	caps, found := svc.Lookup(context.Background(), "k")
	require.True(t, found)
	assert.Equal(t, []string{"text", "image"}, caps.In)
	assert.Equal(t, 8192, caps.Context)
}

func TestCapcompat_RefreshRemembersUnsupportedUpstreamAsMiss(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "unsupported_v1_models.json"),
	})
	cache := newCountingCache()
	svc := New(cache, nil)

	// An upstream with nothing to report is not an error the caller must
	// handle; it just has no capabilities.
	require.NoError(t, svc.Refresh(context.Background(), "k", up.client(t), "model-a"))
	assert.False(t, cache.has("k"), "nothing to cache")

	before := cache.gets.Load()
	_, found := svc.Lookup(context.Background(), "k")
	assert.False(t, found)
	assert.Equal(t, before, cache.gets.Load(), "a remembered miss must not read the store")
}

// fastRetries shortens the probe retry delay for the duration of a test.
func fastRetries(t *testing.T) {
	t.Helper()
	original := probeRetryDelay
	probeRetryDelay = time.Millisecond
	t.Cleanup(func() { probeRetryDelay = original })
}

func TestCapcompat_RefreshRetriesUntilUpstreamAnswers(t *testing.T) {
	fastRetries(t)

	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A model with checkEndpoint "none" is ready before it listens, so
		// the first probe legitimately fails.
		if attempts.Add(1) == 1 && r.URL.Path == "/v1/models" {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write(fixture(t, "llama-server", "v1_models.json"))
		case "/props":
			w.Write(fixture(t, "llama-server", "props.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	svc := New(newCountingCache(), nil)

	require.NoError(t, svc.Refresh(context.Background(), "k", NewClient(base, config.TimeoutsConfig{}), "model-a"))

	caps, found := svc.Lookup(context.Background(), "k")
	require.True(t, found)
	assert.Equal(t, 32768, caps.Context)
}

func TestCapcompat_RefreshDoesNotRetryUnsupportedUpstream(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "unsupported_v1_models.json"),
	})

	require.NoError(t, New(newCountingCache(), nil).Refresh(context.Background(), "k", up.client(t), "model-a"))
	assert.Equal(t, 1, up.hits["/v1/models"], "retrying cannot change an unsupported answer")
}

func TestCapcompat_RefreshReturnsErrorWhenUpstreamNeverAnswers(t *testing.T) {
	fastRetries(t)
	up := newUpstream(t, map[string][]byte{})
	svc := New(newCountingCache(), nil)

	err := svc.Refresh(context.Background(), "k", up.client(t), "model-a")
	require.Error(t, err)
	assert.Equal(t, probeAttempts, up.hits["/v1/models"])
}

func TestCapcompat_RefreshHonoursContextCancellation(t *testing.T) {
	fastRetries(t)
	up := newUpstream(t, map[string][]byte{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := New(newCountingCache(), nil).Refresh(ctx, "k", up.client(t), "model-a")
	require.Error(t, err)
}

func TestCapcompat_RefreshSkipsWhenAlreadyInFlight(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write(fixture(t, "llama-server", "v1_models.json"))
		case "/props":
			w.Write(fixture(t, "llama-server", "props.json"))
		}
	}))
	defer server.Close()

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	svc := New(newCountingCache(), nil)

	done := make(chan error, 1)
	go func() {
		done <- svc.Refresh(context.Background(), "k", NewClient(base, config.TimeoutsConfig{}), "model-a")
	}()
	<-entered

	// A model flapping ready/stopped must not stack probes on the same key.
	require.NoError(t, svc.Refresh(context.Background(), "k", NewClient(base, config.TimeoutsConfig{}), "model-a"))

	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, int64(1), calls.Load())
}

func TestCapcompat_LookupMemoizesStoreReads(t *testing.T) {
	cache := newCountingCache()
	info := Info{Upstream: "vllm", Capabilities: config.ModelCapConfig{Context: 4096}, DetectedAt: time.Now()}
	require.NoError(t, cache.Set(context.Background(), store.CacheEntry{Key: "k", Data: mustJSON(t, info)}))

	svc := New(cache, nil)
	for i := 0; i < 5; i++ {
		caps, found := svc.Lookup(context.Background(), "k")
		require.True(t, found)
		assert.Equal(t, 4096, caps.Context)
	}
	assert.Equal(t, int64(1), cache.gets.Load(), "/v1/models polling must not hit the store per request")
}

func TestCapcompat_LookupMemoizesMisses(t *testing.T) {
	cache := newCountingCache()
	svc := New(cache, nil)

	for i := 0; i < 3; i++ {
		_, found := svc.Lookup(context.Background(), "missing")
		assert.False(t, found)
	}
	assert.Equal(t, int64(1), cache.gets.Load())
}

func TestCapcompat_LookupDoesNotMemoizeStoreErrors(t *testing.T) {
	cache := newCountingCache()
	cache.getErr = errors.New("database is locked")
	svc := New(cache, nil)

	for i := 0; i < 3; i++ {
		_, found := svc.Lookup(context.Background(), "k")
		assert.False(t, found)
	}
	assert.Equal(t, int64(3), cache.gets.Load(), "a transient failure must be retried")
}

func TestCapcompat_LookupDropsUnreadableEntry(t *testing.T) {
	cache := newCountingCache()
	require.NoError(t, cache.Set(context.Background(), store.CacheEntry{Key: "k", Data: []byte("not json")}))

	_, found := New(cache, nil).Lookup(context.Background(), "k")
	assert.False(t, found)
	assert.False(t, cache.has("k"), "a row that cannot be decoded is removed")
}

func TestCapcompat_LookupTreatsEmptyCapabilitiesAsMiss(t *testing.T) {
	cache := newCountingCache()
	info := Info{Upstream: "llama-server", Capabilities: config.ModelCapConfig{}}
	require.NoError(t, cache.Set(context.Background(), store.CacheEntry{Key: "k", Data: mustJSON(t, info)}))

	_, found := New(cache, nil).Lookup(context.Background(), "k")
	assert.False(t, found)
}

func TestCapcompat_RefreshReplacesMemoizedValue(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props.json"),
	})
	svc := New(newCountingCache(), nil)

	_, found := svc.Lookup(context.Background(), "k")
	require.False(t, found, "nothing discovered yet")

	require.NoError(t, svc.Refresh(context.Background(), "k", up.client(t), "model-a"))

	caps, found := svc.Lookup(context.Background(), "k")
	require.True(t, found, "a refresh must replace the remembered miss")
	assert.Equal(t, 32768, caps.Context)
}

func TestCapcompat_ForgetDropsMemo(t *testing.T) {
	cache := newCountingCache()
	svc := New(cache, nil)

	_, _ = svc.Lookup(context.Background(), "k")
	svc.Forget("k")
	_, _ = svc.Lookup(context.Background(), "k")

	assert.Equal(t, int64(2), cache.gets.Load())
}

func TestCapcompat_RefreshReportsCacheWriteFailure(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props.json"),
	})
	cache := newCountingCache()
	cache.setErr = errors.New("disk is full")
	svc := New(cache, nil)

	err := svc.Refresh(context.Background(), "k", up.client(t), "model-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "caching")

	// The probe still succeeded, so the value is not lost for this process.
	_, found := svc.Lookup(context.Background(), "k")
	assert.False(t, found, "a value that could not be stored is not remembered as present")
}

func TestCapcompat_NilServiceIsSafe(t *testing.T) {
	var svc *Service

	caps, found := svc.Lookup(context.Background(), "k")
	assert.False(t, found)
	assert.True(t, caps.Empty())
	assert.NoError(t, svc.Refresh(context.Background(), "k", nil, "model-a"))
	svc.Forget("k")
}

func TestCapcompat_ServiceWithoutCacheStillMemoizes(t *testing.T) {
	up := newUpstream(t, map[string][]byte{
		"/v1/models": fixture(t, "llama-server", "v1_models.json"),
		"/props":     fixture(t, "llama-server", "props.json"),
	})
	svc := New(nil, nil)

	require.NoError(t, svc.Refresh(context.Background(), "k", up.client(t), "model-a"))
	caps, found := svc.Lookup(context.Background(), "k")
	require.True(t, found)
	assert.Equal(t, 32768, caps.Context)
}

func TestCapcompat_LocalKey(t *testing.T) {
	base := config.ModelConfig{Cmd: "llama-server -m a.gguf", Proxy: "http://localhost:9001"}

	key := LocalKey("model-a", base)
	assert.Contains(t, key, "capcompat:v1:local:model-a:")
	assert.Equal(t, key, LocalKey("model-a", base), "same config yields a stable key")

	t.Run("changes with cmd", func(t *testing.T) {
		changed := base
		changed.Cmd = "llama-server -m b.gguf"
		assert.NotEqual(t, key, LocalKey("model-a", changed))
	})

	t.Run("changes with proxy", func(t *testing.T) {
		changed := base
		changed.Proxy = "http://localhost:9002"
		assert.NotEqual(t, key, LocalKey("model-a", changed))
	})

	t.Run("changes with useModelName", func(t *testing.T) {
		changed := base
		changed.UseModelName = "upstream-name"
		assert.NotEqual(t, key, LocalKey("model-a", changed))
	})

	t.Run("differs per model id", func(t *testing.T) {
		assert.NotEqual(t, key, LocalKey("model-b", base))
	})
}

func TestCapcompat_UpstreamModelName(t *testing.T) {
	assert.Equal(t, "model-a", UpstreamModelName("model-a", config.ModelConfig{}))
	assert.Equal(t, "real-name", UpstreamModelName("model-a", config.ModelConfig{UseModelName: "real-name"}))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
