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
	peers        config.PeerDictionaryConfig
	proxyMap     map[string]*peerProxyMember
	peerAliases  map[string]string         // alias → base model ID (from setParamsByID keys)
	modelFilters map[string]config.Filters // base model ID → filters with ${MODEL_ID} expanded
}

func NewPeerProxy(peers config.PeerDictionaryConfig, proxyLogger *LogMonitor) (*PeerProxy, error) {
	proxyMap := make(map[string]*peerProxyMember)

	// Sort peer IDs for consistent iteration order
	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := peers[peerID]

		// Create a transport with per-peer timeout configuration
		peerTransport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(peer.Timeouts.Connect) * time.Second,
				KeepAlive: time.Duration(peer.Timeouts.KeepAlive) * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   time.Duration(peer.Timeouts.TLSHandshake) * time.Second,
			ResponseHeaderTimeout: time.Duration(peer.Timeouts.ResponseHeader) * time.Second,
			ExpectContinueTimeout: time.Duration(peer.Timeouts.ExpectContinue) * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       time.Duration(peer.Timeouts.IdleConn) * time.Second,
		}

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

	// Build per-model expanded filters and alias map from setParamsByID keys
	peerAliases := make(map[string]string)
	modelFilters := make(map[string]config.Filters)

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		for _, modelID := range peer.Models {
			if _, found := proxyMap[modelID]; !found {
				continue // model was skipped as duplicate
			}
			expanded := expandPeerFiltersForModel(peer.Filters, modelID)
			modelFilters[modelID] = expanded

			for key := range expanded.SetParamsByID {
				if key == modelID {
					continue
				}
				if _, exists := proxyMap[key]; exists {
					proxyLogger.Warnf("peer %s: setParamsByID key '%s' conflicts with an existing model, skipping alias", peerID, key)
					continue
				}
				if existingModel, exists := peerAliases[key]; exists {
					if existingModel != modelID {
						proxyLogger.Warnf("peer %s: duplicate setParamsByID alias '%s' already registered for model %s, skipping", peerID, key, existingModel)
					}
					continue
				}
				peerAliases[key] = modelID
			}
		}
	}

	return &PeerProxy{
		peers:        peers,
		proxyMap:     proxyMap,
		peerAliases:  peerAliases,
		modelFilters: modelFilters,
	}, nil
}

// expandPeerFiltersForModel returns a copy of f with ${MODEL_ID} replaced by modelID in setParamsByID keys.
func expandPeerFiltersForModel(f config.Filters, modelID string) config.Filters {
	if len(f.SetParamsByID) == 0 {
		return f
	}
	expanded := config.Filters{
		StripParams: f.StripParams,
		SetParams:   f.SetParams,
	}
	expanded.SetParamsByID = make(map[string]map[string]any, len(f.SetParamsByID))
	const modelIDMacro = "${MODEL_ID}"
	for key, params := range f.SetParamsByID {
		newKey := strings.ReplaceAll(key, modelIDMacro, modelID)
		expanded.SetParamsByID[newKey] = params
	}
	return expanded
}

func (p *PeerProxy) HasPeerModel(modelID string) bool {
	if _, found := p.proxyMap[modelID]; found {
		return true
	}
	_, found := p.peerAliases[modelID]
	return found
}

// RealPeerModelName resolves an alias or model ID to the base model ID.
func (p *PeerProxy) RealPeerModelName(modelID string) (string, bool) {
	if _, found := p.proxyMap[modelID]; found {
		return modelID, true
	}
	if realID, found := p.peerAliases[modelID]; found {
		return realID, true
	}
	return "", false
}

// GetPeerFilters returns the expanded filters for the given model ID (or alias).
func (p *PeerProxy) GetPeerFilters(modelID string) config.Filters {
	realID, found := p.RealPeerModelName(modelID)
	if !found {
		return config.Filters{}
	}
	filters, ok := p.modelFilters[realID]
	if !ok {
		return config.Filters{}
	}
	return filters
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
