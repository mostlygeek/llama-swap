package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

var testLogger = logmon.NewWriter(os.Stdout)

func init() {
	testLogger.SetLogLevel(logmon.LevelWarn)
}

func TestNewPeer_EmptyPeers(t *testing.T) {
	pr, err := NewPeer(config.Config{}, testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil {
		t.Fatal("expected non-nil Peer")
	}
	if len(pr.peers) != 0 {
		t.Fatalf("expected empty peers map, got %d entries", len(pr.peers))
	}
}

func TestNewPeer_SinglePeer(t *testing.T) {
	proxyURL, _ := url.Parse("http://peer1.example.com:8080")
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    "http://peer1.example.com:8080",
			ProxyURL: proxyURL,
			ApiKey:   "test-key",
			Models:   []string{"model-a", "model-b"},
		},
	}

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.peers) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(pr.peers))
	}
	if _, ok := pr.peers["model-a"]; !ok {
		t.Error("expected model-a to be mapped")
	}
	if _, ok := pr.peers["model-b"]; !ok {
		t.Error("expected model-b to be mapped")
	}
	if _, ok := pr.peers["model-c"]; ok {
		t.Error("expected model-c to not be mapped")
	}
}

func TestNewPeer_MultiplePeers(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.peers) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(pr.peers))
	}
	for _, m := range []string{"model-a", "model-b", "model-c", "model-d"} {
		if _, ok := pr.peers[m]; !ok {
			t.Errorf("expected %s to be mapped", m)
		}
	}
}

func TestNewPeer_DuplicateModel(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.peers) != 1 {
		t.Fatalf("expected 1 entry for duplicate model, got %d", len(pr.peers))
	}
	if _, ok := pr.peers["duplicate-model"]; !ok {
		t.Error("expected duplicate-model to be mapped")
	}
}

func TestPeer_ServeHTTP_Success(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "response from peer" {
		t.Errorf("expected 'response from peer', got %q", w.Body.String())
	}
}

func TestPeer_ServeHTTP_ModelNotFoundInContext(t *testing.T) {
	pr, err := NewPeer(config.Config{}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPeer_ServeHTTP_PeerModelNotFound(t *testing.T) {
	pr, err := NewPeer(config.Config{}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "nonexistent-model", ModelID: "nonexistent-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPeer_ServeHTTP_ApiKeyInjection(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if receivedAuthHeader != "Bearer secret-api-key" {
		t.Errorf("expected 'Bearer secret-api-key', got %q", receivedAuthHeader)
	}
}

func TestPeer_ServeHTTP_NoApiKey(t *testing.T) {
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
			ApiKey:   "",
			Models:   []string{"test-model"},
		},
	}

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if receivedAuthHeader != "" {
		t.Errorf("expected no auth header, got %q", receivedAuthHeader)
	}
}

func TestPeer_ServeHTTP_HostHeaderSet(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if !strings.HasPrefix(receivedHost, "127.0.0.1:") {
		t.Errorf("expected Host to start with '127.0.0.1:', got %q", receivedHost)
	}
}

func TestPeer_ServeHTTP_SSEHeaderModification(t *testing.T) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("expected X-Accel-Buffering=no, got %q", w.Header().Get("X-Accel-Buffering"))
	}
}

func TestPeer_ServeHTTP_ShutdownRejectsNewRequests(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	err = pr.Shutdown(0)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "shutting down") {
		t.Errorf("expected 'shutting down' in body, got %q", w.Body.String())
	}
}

func TestPeer_ServeHTTP_WaitsForInflightDuringShutdown(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-released
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		pr.ServeHTTP(w, req)
	}()

	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- pr.Shutdown(500 * time.Millisecond)
	}()

	// Shutdown should be waiting on inflight. If it finished already something is wrong.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-shutdownDone:
		t.Errorf("shutdown completed before inflight finished: %v", err)
	default:
	}

	close(released)
	wg.Wait()

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("shutdown errored after inflight completed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("shutdown did not complete after inflight finished")
	}
}

func TestPeer_ServeHTTP_ShutdownTimeoutCancelsInflight(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-released
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

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "test-model", ModelID: "test-model"}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		pr.ServeHTTP(w, req)
	}()

	<-started

	err = pr.Shutdown(100 * time.Millisecond)
	if err == nil {
		t.Error("expected timeout error from shutdown")
	}

	close(released)
	wg.Wait()
}

func TestPeer_ShutdownMultiple(t *testing.T) {
	pr, err := NewPeer(config.Config{}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	err = pr.Shutdown(0)
	if err != nil {
		t.Fatal(err)
	}

	err = pr.Shutdown(0)
	if err == nil {
		t.Error("expected error on second shutdown")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress', got %q", err.Error())
	}
}

func TestPeer_ServeHTTP_ModelExtractedFromBody(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"extracted-model"},
		},
	}

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"model":"extracted-model","prompt":"hello"}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPeer_ServeHTTP_ContextOverridesBodyModel(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer testServer.Close()

	proxyURL, _ := url.Parse(testServer.URL)
	peers := config.PeerDictionaryConfig{
		"peer1": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"context-model"},
		},
		"peer2": config.PeerConfig{
			Proxy:    testServer.URL,
			ProxyURL: proxyURL,
			Models:   []string{"body-model"},
		},
	}

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"model":"body-model","prompt":"hello"}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	*req = *req.WithContext(SetContext(req.Context(), ReqContextData{Model: "context-model", ModelID: "context-model"}))
	w := httptest.NewRecorder()

	pr.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNewPeer_CustomTimeouts(t *testing.T) {
	proxyURL, _ := url.Parse("http://localhost:8080")
	peers := config.PeerDictionaryConfig{
		"test-peer": config.PeerConfig{
			Proxy:    "http://localhost:8080",
			ProxyURL: proxyURL,
			Models:   []string{"model1"},
			Timeouts: config.TimeoutsConfig{
				Connect:        45,
				ResponseHeader: 300,
				TLSHandshake:   15,
				ExpectContinue: 2,
				IdleConn:       120,
			},
		},
	}

	pr, err := NewPeer(config.Config{Peers: peers}, testLogger)
	if err != nil {
		t.Fatal(err)
	}

	member, ok := pr.peers["model1"]
	if !ok {
		t.Fatal("expected model1 to be mapped")
	}

	transport, ok := member.reverseProxy.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected Transport to be *http.Transport")
	}

	if transport.ResponseHeaderTimeout != 300*time.Second {
		t.Errorf("expected ResponseHeaderTimeout=%v, got %v", 300*time.Second, transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 15*time.Second {
		t.Errorf("expected TLSHandshakeTimeout=%v, got %v", 15*time.Second, transport.TLSHandshakeTimeout)
	}
	if transport.ExpectContinueTimeout != 2*time.Second {
		t.Errorf("expected ExpectContinueTimeout=%v, got %v", 2*time.Second, transport.ExpectContinueTimeout)
	}
	if transport.IdleConnTimeout != 120*time.Second {
		t.Errorf("expected IdleConnTimeout=%v, got %v", 120*time.Second, transport.IdleConnTimeout)
	}
	if !transport.ForceAttemptHTTP2 {
		t.Error("expected ForceAttemptHTTP2 to be true")
	}
}
