package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPeerProxy_EmptyPeers(t *testing.T) {
	peers := config.PeerDictionaryConfig{}
	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)
	assert.NotNil(t, pm)
	assert.Empty(t, pm.proxyMap)
}

func TestNewPeerProxy_SinglePeer(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			ApiKey:   "test-key",
			Models:   []string{"model-a", "model-b"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 2)
	assert.True(t, pm.HasPeerModel("model-a"))
	assert.True(t, pm.HasPeerModel("model-b"))
	assert.False(t, pm.HasPeerModel("model-c"))
}

func TestNewPeerProxy_MultiplePeers(t *testing.T) {
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL1,
			Models:   []string{"model-a", "model-b"},
		},
		"peer2": config.PeerConfig{
			Proxy:    "http://peer2.example.com:8080",
			ProxyURL: proxyURL2,
			Models:   []string{"model-c", "model-d"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)
	assert.Len(t, pm.proxyMap, 4)
	assert.True(t, pm.HasPeerModel("model-a"))
	assert.True(t, pm.HasPeerModel("model-b"))
	assert.True(t, pm.HasPeerModel("model-c"))
	assert.True(t, pm.HasPeerModel("model-d"))
}

func TestNewPeerProxy_DuplicateModelWarning(t *testing.T) {
	// When the same model is in multiple peers, only the first (lexicographically by peer ID)
	// should be mapped, and a warning should be logged
	proxyURL1, _ := url.Parse("http://peer1.example.com:8080")
	proxyURL2, _ := url.Parse("http://peer2.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"alpha-peer": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL1,
			Models:   []string{"duplicate-model"},
		},
		"beta-peer": config.PeerConfig{
			Proxy:    "http://peer2.example.com:8080",
			ProxyURL: proxyURL2,
			Models:   []string{"duplicate-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)
	// Should only have one entry for the duplicate model
	assert.Len(t, pm.proxyMap, 1)
	assert.True(t, pm.HasPeerModel("duplicate-model"))
}

func TestHasPeerModel(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			Models:   []string{"existing-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	assert.True(t, pm.HasPeerModel("existing-model"))
	assert.False(t, pm.HasPeerModel("non-existing-model"))
}

func TestProxyRequest_ModelNotFound(t *testing.T) {
	peers := config.PeerDictionaryConfig{}
	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("non-existing-model", w, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no peer proxy found for model non-existing-model")
}

func TestProxyRequest_Success(t *testing.T) {
	// Create a test server to act as the peer
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from peer"))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "response from peer", w.Body.String())
}

func TestProxyRequest_ApiKeyInjection(t *testing.T) {
	// Create a test server that checks for the Authorization header
	var receivedAuthHeader string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			ApiKey:   "secret-api-key",
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Equal(t, "Bearer secret-api-key", receivedAuthHeader)
}

func TestProxyRequest_NoApiKey(t *testing.T) {
	// Create a test server that checks for the Authorization header
	var receivedAuthHeader string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			ApiKey:   "", // No API key
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	assert.Empty(t, receivedAuthHeader)
}

func TestProxyRequest_HostHeaderSet(t *testing.T) {
	// Create a test server that checks the Host header
	var receivedHost string
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	// The Host header should be set to the target URL's host
	assert.True(t, strings.HasPrefix(receivedHost, "127.0.0.1:"))
}

func TestProxyRequest_SSEHeaderModification(t *testing.T) {
	// Create a test server that returns SSE content type
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"test-model"},
		},
	}

	pm, err := NewPeerProxy(peers, testLogger)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err = pm.ProxyRequest("test-model", w, req)
	assert.NoError(t, err)
	// The X-Accel-Buffering header should be set to "no" for SSE
	assert.Equal(t, "no", w.Header().Get("X-Accel-Buffering"))
}
