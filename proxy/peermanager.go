package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerProxy struct {
	reverseProxy *httputil.ReverseProxy
	apiKey       string
}

type PeerManager struct {
	peers config.PeerDictionaryConfig

	proxyMap map[string]*peerProxy
}

func NewPeerManager(peers config.PeerDictionaryConfig, proxyLogger *LogMonitor) (*PeerManager, error) {
	proxyMap := make(map[string]*peerProxy)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		// Create reverse proxy for this peer
		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)

		// Wrap Director to set Host header for remote hosts (not localhost)
		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			// Ensure Host header matches target URL for remote proxying
			req.Host = req.URL.Host
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}

		pp := &peerProxy{
			reverseProxy: reverseProxy,
			apiKey:       peer.ApiKey,
		}

		// Map each model to this peer's proxy
		for _, modelID := range peer.Models {
			if _, found := proxyMap[modelID]; found {
				proxyLogger.Warnf("peer %s: model %s already mapped to another peer, skipping", peerID, modelID)
				continue
			}
			proxyMap[modelID] = pp
		}
	}

	return &PeerManager{
		peers:    peers,
		proxyMap: proxyMap,
	}, nil
}

func (p *PeerManager) HasPeerModel(modelID string) bool {
	_, found := p.proxyMap[modelID]
	return found
}

func (p *PeerManager) ProxyRequest(model_id string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[model_id]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", model_id)
	}

	// Inject API key if configured for this peer
	if pp.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+pp.apiKey)
	}

	pp.reverseProxy.ServeHTTP(writer, request)
	return nil
}
