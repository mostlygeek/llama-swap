package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// errInferencePausedMessage is the error returned to every request for a
// local model while inference is paused.
const errInferencePausedMessage = "all inference is paused"

// PauseState records whether inference is paused (issue #1268). It is owned
// by main and shared by every Server a hot config reload creates, so a reload
// while paused neither resumes inference nor preloads models. It is held in
// memory only: a restart always starts unpaused. A nil value is never paused.
type PauseState struct {
	paused atomic.Bool
}

func NewPauseState() *PauseState {
	return &PauseState{}
}

// Paused reports whether inference is paused.
func (p *PauseState) Paused() bool {
	return p != nil && p.paused.Load()
}

// set updates the pause state and reports whether it changed.
func (p *PauseState) set(paused bool) bool {
	return p.paused.Swap(paused) != paused
}

// serveLocal dispatches a request to the local router, or rejects it while
// inference is paused. Every request for a local model goes through here, so
// pausing stops new requests from starting a model on any endpoint. Peer
// models use no local resources and are not affected.
func (s *Server) serveLocal(w http.ResponseWriter, r *http.Request) {
	if s.pause.Paused() {
		swaputil.SendResponse(w, r, http.StatusServiceUnavailable, errInferencePausedMessage)
		return
	}
	s.local.ServeHTTP(w, r)
}

// handleAPIPause pauses inference and then unloads every local model. Pausing
// first means no new request can start a model while the unload runs.
// Requests already in progress are stopped with their model. It returns once
// every model has stopped.
func (s *Server) handleAPIPause(w http.ResponseWriter, r *http.Request) {
	s.setPaused(true)
	s.local.Unload(0)
	writePauseState(w, true)
}

// handleAPIResume lets inference requests through again. Models load on the
// next request as usual.
func (s *Server) handleAPIResume(w http.ResponseWriter, r *http.Request) {
	s.setPaused(false)
	writePauseState(w, false)
}

func (s *Server) setPaused(paused bool) {
	if !s.pause.set(paused) {
		return
	}
	if paused {
		s.logs.ProxyLogs.Info("inference paused")
	} else {
		s.logs.ProxyLogs.Info("inference resumed")
	}
	event.Emit(swaputil.InferencePausedEvent{Paused: paused})
}

func writePauseState(w http.ResponseWriter, paused bool) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"paused": paused})
}
