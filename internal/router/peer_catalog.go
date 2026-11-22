package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/store"
)

const (
	// maxPeerCatalogBytes caps a peer's model list response.
	maxPeerCatalogBytes = 4_000_000

	// peerCatalogCacheTTL lets Prune clear catalogs of peers that were
	// removed. Every successful fetch rewrites the entry.
	peerCatalogCacheTTL = 7 * 24 * time.Hour

	// peerCatalogMinManualRefresh limits manual refreshes to one fetch per
	// peer in this window, counted from the end of the last fetch.
	peerCatalogMinManualRefresh = 5 * time.Second

	peerCatalogSourceCache = "cache"
	peerCatalogSourceLive  = "live"
)

// ErrPeerNotFound is returned for a peer ID that is not configured.
var ErrPeerNotFound = errors.New("peer not found")

// PeerCatalogStatus describes model discovery for one peer.
type PeerCatalogStatus struct {
	PeerID string `json:"peerID"`
	// Source is where the current model list came from: "cache", "live",
	// or empty before any list was loaded.
	Source string `json:"source,omitempty"`
	// FetchedAt is when the peer served the current model list.
	FetchedAt time.Time `json:"fetchedAt,omitzero"`
	// LastAttempt is when the last live fetch finished, successful or not.
	LastAttempt time.Time `json:"lastAttempt,omitzero"`
	// LastError is the error from the last live fetch, empty after a success.
	LastError string `json:"lastError,omitempty"`
	// Available is how many models the peer's catalog listed.
	Available int `json:"available"`
	// Models is how many discovered models are routable.
	Models int `json:"models"`
	// Truncated is how many selected models were dropped by catalog.limit.
	Truncated int `json:"truncated,omitempty"`
	// Dropped lists names that collided with another model name.
	Dropped []string `json:"dropped,omitempty"`
}

// StartCatalog begins model discovery for every peer with a catalog. Cached
// catalogs are applied before it returns, so their models route right away;
// live fetches then run in the background until the router shuts down.
// Shutdown does not wait for them: cancelling shutdownCtx aborts a fetch in
// progress and any result that arrives later is discarded.
// onChange is called with the peer ID whenever its routable models change.
// cache may be nil.
func (r *Peer) StartCatalog(cache store.CacheRepository, onChange func(peerID string)) {
	r.catalogMu.Lock()
	if r.catalogStatus == nil {
		r.catalogStatus = make(map[string]*PeerCatalogStatus)
	}
	r.catalogMu.Unlock()

	for _, member := range r.members {
		peer := r.cfg.Peers[member.peerID]
		if !peer.Discovery() {
			continue
		}
		r.catalogMu.Lock()
		r.catalogStatus[member.peerID] = &PeerCatalogStatus{PeerID: member.peerID}
		r.catalogMu.Unlock()

		if cache != nil {
			r.loadCachedCatalog(cache, member, peer)
		}

		go r.runCatalog(cache, member, peer, onChange)
	}
}

// CatalogStatus returns the discovery status of each peer with a catalog,
// sorted by peer ID.
func (r *Peer) CatalogStatus() []PeerCatalogStatus {
	r.catalogMu.Lock()
	defer r.catalogMu.Unlock()
	out := make([]PeerCatalogStatus, 0, len(r.catalogStatus))
	for _, status := range r.catalogStatus {
		s := *status
		s.Dropped = append([]string(nil), status.Dropped...)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

func peerCatalogCacheKey(peerID string, peer config.PeerConfig) string {
	sum := sha256.Sum256([]byte(peer.Proxy + "\n" + peer.Catalog.Path))
	return "peercatalog:" + peerID + ":" + hex.EncodeToString(sum[:8])
}

func (r *Peer) loadCachedCatalog(cache store.CacheRepository, member *peerMember, peer config.PeerConfig) {
	entry, found, err := cache.Get(r.shutdownCtx, peerCatalogCacheKey(member.peerID, peer))
	if err != nil {
		r.logger.Warnf("peer %s: reading cached catalog: %v", member.peerID, err)
		return
	}
	if !found {
		return
	}
	if _, err := r.applyCatalog(member.peerID, peer, entry.Data, peerCatalogSourceCache, entry.Timestamp); err != nil {
		r.logger.Warnf("peer %s: ignoring cached catalog: %v", member.peerID, err)
		return
	}
	r.logger.Infof("peer %s: loaded catalog cached at %s", member.peerID, entry.Timestamp.Format(time.RFC3339))
}

// catalogRefresh is one manual refresh, shared by every caller that asks
// while it is pending. err is set before done is closed.
type catalogRefresh struct {
	done chan struct{}
	err  error
}

// runCatalog is the only code that fetches a peer's catalog, so fetches for
// one peer never overlap. It fetches on the catalog.refresh interval and on
// manual refreshes, at most one manual fetch per minManualRefresh.
func (r *Peer) runCatalog(cache store.CacheRepository, member *peerMember, peer config.PeerConfig, onChange func(string)) {
	interval := time.Duration(peer.Catalog.Refresh) * time.Second
	var lastDone time.Time
	var lastErr error
	fetch := func() {
		lastErr = r.refreshCatalog(cache, member, peer, onChange)
		lastDone = time.Now()
	}

	fetch()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-r.shutdownCtx.Done():
			// Waiting callers return through shutdownCtx too.
			return
		case <-timer.C:
			fetch()
			timer.Reset(interval)
		case call := <-member.refresh:
			if time.Since(lastDone) >= r.minManualRefresh {
				fetch()
				timer.Reset(interval)
			}
			// Clear pending first, so a caller arriving after this starts a
			// new refresh rather than joining a finished one.
			member.refreshMu.Lock()
			member.pending = nil
			member.refreshMu.Unlock()
			call.err = lastErr
			close(call.done)
		}
	}
}

// RefreshCatalog asks a peer's catalog loop to fetch the model list now and
// waits for the result. Concurrent callers share one pending refresh, and
// within minManualRefresh of the last fetch they get that fetch's result
// without a new one. The catalog.refresh interval restarts after a fetch.
func (r *Peer) RefreshCatalog(ctx context.Context, peerID string) (bool, error) {
	member, ok := r.byID[peerID]
	if !ok {
		return false, ErrPeerNotFound
	}
	r.catalogMu.Lock()
	_, started := r.catalogStatus[peerID]
	r.catalogMu.Unlock()
	if member.refresh == nil || !started {
		return false, nil
	}

	member.refreshMu.Lock()
	call := member.pending
	if call == nil {
		call = &catalogRefresh{done: make(chan struct{})}
		member.pending = call
		// Never blocks: the channel holds one and only the pending refresh
		// is ever in it.
		member.refresh <- call
	}
	member.refreshMu.Unlock()

	select {
	case <-call.done:
		return true, call.err
	case <-ctx.Done():
		return false, ctx.Err()
	case <-r.shutdownCtx.Done():
		return false, r.shutdownCtx.Err()
	}
}

// refreshCatalog fetches and applies the peer's catalog once. It returns the
// fetch or parse error, which is also recorded in the peer's status.
func (r *Peer) refreshCatalog(cache store.CacheRepository, member *peerMember, peer config.PeerConfig, onChange func(string)) error {
	body, err := r.fetchCatalog(member, peer)
	if r.shutdownCtx.Err() != nil {
		// Shutting down: drop the result rather than update a retired router.
		return r.shutdownCtx.Err()
	}
	fetchedAt := time.Now()
	if err == nil {
		var changed bool
		changed, err = r.applyCatalog(member.peerID, peer, body, peerCatalogSourceLive, fetchedAt)
		if err == nil {
			if cache != nil {
				if setErr := cache.Set(r.shutdownCtx, store.CacheEntry{
					Key:       peerCatalogCacheKey(member.peerID, peer),
					Data:      body,
					TTL:       peerCatalogCacheTTL,
					Timestamp: fetchedAt,
				}); setErr != nil {
					r.logger.Warnf("peer %s: caching catalog: %v", member.peerID, setErr)
				}
			}
			if changed && onChange != nil {
				onChange(member.peerID)
			}
		}
	}

	r.catalogMu.Lock()
	status := r.catalogStatus[member.peerID]
	status.LastAttempt = fetchedAt
	status.LastError = ""
	if err != nil {
		status.LastError = err.Error()
	}
	r.catalogMu.Unlock()

	if err != nil {
		r.logger.Warnf("peer %s: catalog fetch failed, keeping the last model list: %v", member.peerID, err)
	}
	return err
}

// fetchCatalog requests the peer's model list through the same transport
// that proxies its requests, so Tailcat, timeouts and the API key apply.
func (r *Peer) fetchCatalog(member *peerMember, peer config.PeerConfig) ([]byte, error) {
	timeout := min(time.Duration(peer.Catalog.Refresh)*time.Second, time.Minute)
	ctx, cancel := context.WithTimeout(r.shutdownCtx, timeout)
	defer cancel()

	u := peer.ProxyURL.JoinPath(peer.Catalog.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if member.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+member.apiKey)
		req.Header.Set("x-api-key", member.apiKey)
	}

	client := &http.Client{
		Transport: member.transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s: unexpected status %s", u.Redacted(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPeerCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxPeerCatalogBytes {
		return nil, fmt.Errorf("GET %s: response is larger than %d bytes", u.Redacted(), maxPeerCatalogBytes)
	}
	return body, nil
}

// applyCatalog parses a catalog body, selects models with the peer's rules
// and publishes them to the config's peer index. It reports whether the
// routable models changed.
func (r *Peer) applyCatalog(peerID string, peer config.PeerConfig, body []byte, source string, fetchedAt time.Time) (bool, error) {
	records, err := parsePeerCatalog(body, peer.Catalog.Transitive)
	if err != nil {
		return false, err
	}

	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	selected, truncated := peer.SelectCatalogModels(ids)

	literals := peer.LiteralModels()
	models := make([]config.DiscoveredPeerModel, 0, len(selected)+len(literals))
	for _, id := range selected {
		models = append(models, config.DiscoveredPeerModel{ID: id, Record: records[id]})
	}
	// Keep the catalog's metadata for literal models it also lists.
	for _, id := range literals {
		if record, ok := records[id]; ok {
			models = append(models, config.DiscoveredPeerModel{ID: id, Record: record})
		}
	}

	dropped, changed := r.cfg.SetDiscoveredPeerModels(peerID, models)

	r.catalogMu.Lock()
	status := r.catalogStatus[peerID]
	status.Source = source
	status.FetchedAt = fetchedAt
	status.Available = len(records)
	status.Models = len(selected) - len(dropped)
	status.Truncated = truncated
	status.Dropped = dropped
	r.catalogMu.Unlock()

	if truncated > 0 {
		r.logger.Warnf("peer %s: catalog.limit %d reached, %d models not added", peerID, peer.Catalog.Limit, truncated)
	}
	for _, fqn := range dropped {
		r.logger.Warnf("peer %s: discovered model %s collides with another model name and was not added", peerID, fqn)
	}
	if changed {
		r.logger.Infof("peer %s: %d models discovered from %s catalog", peerID, len(selected)-len(dropped), source)
	}
	return changed, nil
}

// parsePeerCatalog reads an OpenAI style model list, {"data":[{"id":...}]},
// and returns each record by ID. Unless transitive is set, it skips entries a
// llama-swap peer proxies from its own peers and its aliases, which would
// otherwise chain names, or loop when two instances peer with each other.
func parsePeerCatalog(body []byte, transitive bool) (map[string]map[string]any, error) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing model list: %w", err)
	}
	if list.Data == nil {
		return nil, errors.New("parsing model list: no data array")
	}

	records := make(map[string]map[string]any, len(list.Data))
	for _, record := range list.Data {
		id, _ := record["id"].(string)
		if id == "" {
			continue
		}
		if !transitive {
			switch llamaSwapEntryType(record) {
			case "peer", "alias":
				continue
			}
		}
		if _, dup := records[id]; !dup {
			records[id] = record
		}
	}
	return records, nil
}

// llamaSwapEntryType returns meta.llamaswap.type from a llama-swap model
// record, or "" for records from other servers.
func llamaSwapEntryType(record map[string]any) string {
	meta, _ := record["meta"].(map[string]any)
	ls, _ := meta["llamaswap"].(map[string]any)
	t, _ := ls["type"].(string)
	return t
}
