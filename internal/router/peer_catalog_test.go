package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memCache is an in-memory store.CacheRepository.
type memCache struct {
	mu      sync.Mutex
	entries map[string]store.CacheEntry
}

func newMemCache() *memCache { return &memCache{entries: map[string]store.CacheEntry{}} }

func (c *memCache) Get(_ context.Context, key string) (store.CacheEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok, nil
}

func (c *memCache) Set(_ context.Context, e store.CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[e.Key] = e
	return nil
}

func (c *memCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

func (c *memCache) Prune(context.Context) error { return nil }

func (c *memCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

const testCatalog = `{"object":"list","data":[
	{"id":"qwen-8b","name":"Qwen 8B","context_length":32768},
	{"id":"qwen-embed"},
	{"id":"other/x","meta":{"llamaswap":{"type":"peer","peerID":"other"}}},
	{"id":"q8","meta":{"llamaswap":{"type":"alias","modelID":"qwen-8b"}}}
]}`

// catalogPeer serves a model list at /v1/models and echoes the model of
// other requests. status, when set, overrides the catalog response.
type catalogPeer struct {
	*httptest.Server
	status  atomic.Int32
	fetches atomic.Int32
	apiKey  atomic.Value
}

func newCatalogPeer(t *testing.T, body string) *catalogPeer {
	t.Helper()
	p := &catalogPeer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			p.fetches.Add(1)
			p.apiKey.Store(r.Header.Get("Authorization"))
			if s := p.status.Load(); s != 0 {
				w.WriteHeader(int(s))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Write(b)
	}))
	t.Cleanup(p.Close)
	return p
}

func loadPeerConfig(t *testing.T, yamlStr string) config.Config {
	t.Helper()
	cfg, err := config.LoadConfigFromReader(strings.NewReader(yamlStr))
	require.NoError(t, err)
	return cfg
}

func startPeer(t *testing.T, cfg config.Config, cache store.CacheRepository, onChange func(string)) *Peer {
	t.Helper()
	pr, err := NewPeer(cfg, testLogger)
	require.NoError(t, err)
	pr.StartCatalog(cache, onChange)
	t.Cleanup(func() { pr.Shutdown(time.Second) })
	return pr
}

// startPeerWith is startPeer without a cache, with setup run on the router
// before its catalogs start.
func startPeerWith(t *testing.T, cfg config.Config, setup func(*Peer), onChange func(string)) *Peer {
	t.Helper()
	pr, err := NewPeer(cfg, testLogger)
	require.NoError(t, err)
	setup(pr)
	pr.StartCatalog(nil, onChange)
	t.Cleanup(func() { pr.Shutdown(time.Second) })
	return pr
}

func TestPeer_CatalogDiscovery(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
    apiKey: secret
    models:
      - static
      - add qwen
      - remove embed
`)
	cache := newMemCache()
	changed := make(chan string, 4)
	pr := startPeer(t, cfg, cache, func(id string) { changed <- id })

	select {
	case id := <-changed:
		assert.Equal(t, "kitchen", id)
	case <-time.After(5 * time.Second):
		t.Fatal("catalog was not applied")
	}

	assert.True(t, pr.Handles("static"))
	assert.True(t, pr.Handles("qwen-8b"))
	assert.True(t, pr.Handles("kitchen/qwen-8b"))
	assert.False(t, pr.Handles("qwen-embed"), "removed")
	assert.False(t, pr.Handles("kitchen/other/x"), "transitive peer entry skipped")
	assert.False(t, pr.Handles("q8"), "alias entry skipped")
	assert.Equal(t, "Bearer secret", peer.apiKey.Load())

	record, ok := cfg.PeerModelRecord("kitchen", "qwen-8b")
	require.True(t, ok)
	assert.Equal(t, "Qwen 8B", record["name"])

	assert.Equal(t, 1, cache.len(), "catalog cached")

	status := pr.CatalogStatus()
	require.Len(t, status, 1)
	assert.Equal(t, "live", status[0].Source)
	assert.Equal(t, 2, status[0].Available)
	assert.Equal(t, 1, status[0].Models)
	assert.Empty(t, status[0].LastError)

	// A discovered model is proxied with the peer's own model ID.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kitchen/qwen-8b"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	pr.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"model":"qwen-8b"}`, w.Body.String())
}

func TestPeer_CatalogTransitive(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
    catalog:
      transitive: true
`)
	changed := make(chan string, 4)
	pr := startPeer(t, cfg, nil, func(id string) { changed <- id })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog was not applied")
	}
	assert.True(t, pr.Handles("kitchen/other/x"))
	assert.True(t, pr.Handles("kitchen/q8"))
}

func TestPeer_CatalogFromCache(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	peer.status.Store(http.StatusInternalServerError)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
`)
	cache := newMemCache()
	cachedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, cache.Set(context.Background(), store.CacheEntry{
		Key:       peerCatalogCacheKey("kitchen", cfg.Peers["kitchen"]),
		Data:      []byte(`{"data":[{"id":"cached-model"}]}`),
		Timestamp: cachedAt,
	}))

	pr := startPeer(t, cfg, cache, nil)

	// Cached models route as soon as StartCatalog returns.
	assert.True(t, pr.Handles("cached-model"))

	require.Eventually(t, func() bool {
		s := pr.CatalogStatus()
		return len(s) == 1 && s[0].LastError != ""
	}, 5*time.Second, 10*time.Millisecond)

	// The failed live fetch keeps the cached list.
	assert.True(t, pr.Handles("cached-model"))
	status := pr.CatalogStatus()[0]
	assert.Equal(t, "cache", status.Source)
	assert.True(t, status.FetchedAt.Equal(cachedAt))
	assert.Contains(t, status.LastError, "500")
}

func TestPeer_CatalogFailureKeepsModels(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
`)
	pr, err := NewPeer(cfg, testLogger)
	require.NoError(t, err)
	t.Cleanup(func() { pr.Shutdown(time.Second) })
	pr.catalogStatus = map[string]*PeerCatalogStatus{"kitchen": {PeerID: "kitchen"}}
	member := pr.byID["kitchen"]

	pr.refreshCatalog(nil, member, cfg.Peers["kitchen"], nil)
	require.True(t, pr.Handles("qwen-8b"))

	peer.status.Store(http.StatusBadGateway)
	pr.refreshCatalog(nil, member, cfg.Peers["kitchen"], nil)
	assert.True(t, pr.Handles("qwen-8b"))
	assert.Contains(t, pr.CatalogStatus()[0].LastError, "502")

	peer.status.Store(0)
	pr.refreshCatalog(nil, member, cfg.Peers["kitchen"], nil)
	assert.Empty(t, pr.CatalogStatus()[0].LastError)
}

func TestPeer_CatalogInvalidResponse(t *testing.T) {
	peer := newCatalogPeer(t, `{"not":"a model list"}`)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
`)
	pr, err := NewPeer(cfg, testLogger)
	require.NoError(t, err)
	t.Cleanup(func() { pr.Shutdown(time.Second) })
	pr.catalogStatus = map[string]*PeerCatalogStatus{"kitchen": {PeerID: "kitchen"}}
	cache := newMemCache()

	pr.refreshCatalog(cache, pr.byID["kitchen"], cfg.Peers["kitchen"], nil)
	assert.Contains(t, pr.CatalogStatus()[0].LastError, "no data array")
	assert.Zero(t, cache.len(), "an invalid catalog is not cached")
}

func TestPeer_CatalogShutdownDoesNotWaitForFetch(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		// Ignore the client going away to model a peer that never answers.
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		hung.Close()
	})

	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+hung.URL+`
`)
	pr, err := NewPeer(cfg, testLogger)
	require.NoError(t, err)
	cache := newMemCache()
	pr.StartCatalog(cache, nil)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog fetch did not start")
	}

	done := make(chan error, 1)
	go func() { done <- pr.Shutdown(time.Second) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown waited on a hung catalog fetch")
	}

	assert.Zero(t, cache.len(), "nothing cached after shutdown")
	assert.Empty(t, pr.CatalogStatus()[0].LastError, "a cancelled fetch is not reported as a failure")
}

func TestPeer_RefreshCatalog(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	other := newCatalogPeer(t, testCatalog)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
    catalog:
      refresh: 3600
  static:
    proxy: `+other.URL+`
    models:
      - fixed
`)
	changed := make(chan string, 4)
	pr := startPeerWith(t, cfg, func(pr *Peer) { pr.minManualRefresh = 0 }, func(id string) { changed <- id })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog was not applied")
	}
	require.EqualValues(t, 1, peer.fetches.Load())

	t.Run("fetches now", func(t *testing.T) {
		refreshed, err := pr.RefreshCatalog(context.Background(), "kitchen")
		require.NoError(t, err)
		assert.True(t, refreshed)
		assert.EqualValues(t, 2, peer.fetches.Load())
	})

	t.Run("reports a failed fetch", func(t *testing.T) {
		peer.status.Store(http.StatusBadGateway)
		t.Cleanup(func() { peer.status.Store(0) })
		refreshed, err := pr.RefreshCatalog(context.Background(), "kitchen")
		assert.True(t, refreshed)
		assert.ErrorContains(t, err, "502")
		assert.True(t, pr.Handles("kitchen/qwen-8b"), "last model list kept")
	})

	t.Run("no-op without model rules", func(t *testing.T) {
		refreshed, err := pr.RefreshCatalog(context.Background(), "static")
		require.NoError(t, err)
		assert.False(t, refreshed)
		assert.Zero(t, other.fetches.Load())
	})

	t.Run("unknown peer", func(t *testing.T) {
		_, err := pr.RefreshCatalog(context.Background(), "nope")
		assert.ErrorIs(t, err, ErrPeerNotFound)
	})

	t.Run("caller gives up", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := pr.RefreshCatalog(ctx, "kitchen")
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestPeer_RefreshCatalogCoalesces(t *testing.T) {
	// The peer holds catalog fetches at the gate while hold is set.
	var fetches atomic.Int32
	var hold atomic.Bool
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		if hold.Load() {
			<-gate
		}
		io.WriteString(w, testCatalog)
	}))
	t.Cleanup(srv.Close)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+srv.URL+`
    catalog:
      refresh: 3600
`)
	changed := make(chan string, 4)
	pr := startPeerWith(t, cfg, func(pr *Peer) { pr.minManualRefresh = 0 }, func(id string) { changed <- id })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog was not applied")
	}
	require.EqualValues(t, 1, fetches.Load())

	hold.Store(true)
	const callers = 10
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			_, err := pr.RefreshCatalog(context.Background(), "kitchen")
			errs <- err
		})
	}
	// One fetch is held at the gate; give the other callers time to join it.
	require.Eventually(t, func() bool { return fetches.Load() == 2 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
	assert.EqualValues(t, 2, fetches.Load(), "concurrent refreshes share one fetch")

	// After it finishes, the next refresh fetches again.
	hold.Store(false)
	_, err := pr.RefreshCatalog(context.Background(), "kitchen")
	require.NoError(t, err)
	assert.EqualValues(t, 3, fetches.Load())
}

func TestPeer_RefreshCatalogRateLimited(t *testing.T) {
	peer := newCatalogPeer(t, testCatalog)
	cfg := loadPeerConfig(t, `
peers:
  kitchen:
    proxy: `+peer.URL+`
    catalog:
      refresh: 3600
`)
	changed := make(chan string, 4)
	pr := startPeerWith(t, cfg, func(pr *Peer) { pr.minManualRefresh = 200 * time.Millisecond }, func(id string) { changed <- id })
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog was not applied")
	}
	require.EqualValues(t, 1, peer.fetches.Load())

	// Right after a fetch: answered with its result, no new fetch.
	for range 3 {
		refreshed, err := pr.RefreshCatalog(context.Background(), "kitchen")
		require.NoError(t, err)
		assert.True(t, refreshed)
	}
	assert.EqualValues(t, 1, peer.fetches.Load())

	// The last fetch's error is what a limited refresh reports.
	time.Sleep(250 * time.Millisecond)
	peer.status.Store(http.StatusBadGateway)
	_, err := pr.RefreshCatalog(context.Background(), "kitchen")
	assert.ErrorContains(t, err, "502")
	require.EqualValues(t, 2, peer.fetches.Load())
	peer.status.Store(0)
	_, err = pr.RefreshCatalog(context.Background(), "kitchen")
	assert.ErrorContains(t, err, "502", "limited: last result")
	assert.EqualValues(t, 2, peer.fetches.Load())

	time.Sleep(250 * time.Millisecond)
	_, err = pr.RefreshCatalog(context.Background(), "kitchen")
	assert.NoError(t, err)
	assert.EqualValues(t, 3, peer.fetches.Load())
}

func TestPeer_CatalogCacheKeyIncludesCredentials(t *testing.T) {
	peer := config.PeerConfig{Proxy: "http://kitchen", ApiKey: "account-a", Catalog: &config.PeerCatalogConfig{Path: "/v1/models"}}
	keyA := peerCatalogCacheKey("kitchen", peer)
	peer.ApiKey = "account-b"
	keyB := peerCatalogCacheKey("kitchen", peer)
	assert.NotEqual(t, keyA, keyB, "another account at the same URL")
	assert.NotContains(t, keyA, "account-a", "the key holds a hash, not the secret")
	peer.ApiKey = "account-a"
	assert.Equal(t, keyA, peerCatalogCacheKey("kitchen", peer), "stable for the same settings")
}
