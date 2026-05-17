package router

// implementation of the peer router to upstream (non local) LLM
// models.

import (
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerMember struct {
	peerID       string
	reverseProxy *httputil.ReverseProxy
	apiKey       string
}

type PeerRouter struct {
	config config.PeerDictionaryConfig
	logger *logmon.Monitor
	peers  map[string]*peerMember
	// TODO: implement
}

func NewPeer(peers config.PeerDictionaryConfig, logger *logmon.Monitor) (*PeerRouter, error) {
	// TODO: implement
	return &PeerRouter{}, nil
}

func (r *PeerRouter) Shutdown(timeout time.Duration) error {
	// TODO: implement
	return nil
}

func (r *PeerRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// TODO: implement
}
