package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerProxyMember struct {
	peerID       string
	reverseProxy *httputil.ReverseProxy
	apiKey       string
}

type PeerProxy struct {
	peers    config.PeerDictionaryConfig
	proxyMap map[string]*peerProxyMember
}

func NewPeerProxy(peers config.PeerDictionaryConfig, proxyLogger *LogMonitor) (*PeerProxy, error) {
	proxyMap := make(map[string]*peerProxyMember)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	// Create a shared transport with reasonable timeouts for peer connections
	// these can be tuned with feedback later
	peerTransport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, // Time to wait for response headers
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		// Create reverse proxy for this peer
		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)
		reverseProxy.Transport = peerTransport

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

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			proxyLogger.Warnf("peer %s: proxy error: %v", peerID, err)
			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		pp := &peerProxyMember{
			peerID:       peerID,
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

	return &PeerProxy{
		peers:    peers,
		proxyMap: proxyMap,
	}, nil
}

func (p *PeerProxy) HasPeerModel(modelID string) bool {
	_, found := p.proxyMap[modelID]
	return found
}

// GetPeerFilters returns the filters for a peer model, or empty filters if not found
func (p *PeerProxy) GetPeerFilters(modelID string) config.Filters {
	pp, found := p.proxyMap[modelID]
	if !found {
		return config.Filters{}
	}
	// Get the peer config using the peerID
	peer, found := p.peers[pp.peerID]
	if !found {
		return config.Filters{}
	}
	return peer.Filters
}

func (p *PeerProxy) ListPeers() config.PeerDictionaryConfig {
	return p.peers
}

func (p *PeerProxy) ProxyRequest(model_id string, writer http.ResponseWriter, request *http.Request) error {
	pp, found := p.proxyMap[model_id]
	if !found {
		return fmt.Errorf("no peer proxy found for model %s", model_id)
	}

	// Inject API key if configured for this peer
	if pp.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+pp.apiKey)
		request.Header.Set("x-api-key", pp.apiKey)
	}

	pp.reverseProxy.ServeHTTP(writer, request)
	return nil
}
