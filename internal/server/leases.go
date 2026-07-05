package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/router"
)

// leaseRequest is the POST /leases body.
type leaseRequest struct {
	Model  string `json:"model"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
	// TTL is a Go duration string ("5h", "30m"); empty means "the maximum".
	TTL string `json:"ttl"`
}

// extendRequest is the POST /leases/{id}/extend body.
type extendRequest struct {
	TTL string `json:"ttl"`
}

// killRequest is the POST /leases/kill body. Exactly one field must be set.
type killRequest struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Holder string `json:"holder"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// handleLeaseAcquire handles POST /leases.
func (s *Server) handleLeaseAcquire(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model is required"})
		return
	}
	ttl, err := parseTTL(req.TTL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	lease, err := s.local.AcquireLease(req.Model, req.Holder, req.Reason, ttl)
	if err != nil {
		if errors.Is(err, router.ErrUnknownModel) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		// Mid-eviction or leases-disabled: transient/again-later.
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, lease)
}

// handleLeaseRelease handles DELETE /leases/{id}.
func (s *Server) handleLeaseRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.local.ReleaseLease(id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Unknown id: the client treats this as "re-acquire", so it is not an error
	// condition per se, but 404 tells it the id is gone.
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown lease id"})
}

// handleLeaseExtend handles POST /leases/{id}/extend.
func (s *Server) handleLeaseExtend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req extendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ttl, err := parseTTL(req.TTL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	lease, ok := s.local.ExtendLease(id, ttl)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown or expired lease id"})
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

// handleLeaseList handles GET /leases.
func (s *Server) handleLeaseList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"leases": s.local.ListLeases()})
}

// handleLeaseKill handles POST /leases/kill.
func (s *Server) handleLeaseKill(w http.ResponseWriter, r *http.Request) {
	var req killRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Model = strings.TrimSpace(req.Model)
	req.Holder = strings.TrimSpace(req.Holder)
	set := 0
	for _, v := range []string{req.ID, req.Model, req.Holder} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exactly one of id, model, or holder is required"})
		return
	}
	killed := s.local.KillLeases(req.ID, req.Model, req.Holder)
	writeJSON(w, http.StatusOK, map[string]any{"killed": killed})
}

// handleCanLoad handles GET /leases/can-load/{model...}.
func (s *Server) handleCanLoad(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model is required"})
		return
	}
	verdict, err := s.local.CanLoad(model)
	if err != nil {
		if errors.Is(err, router.ErrUnknownModel) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, verdict)
}

// parseTTL parses an optional Go duration string. Empty => 0 (meaning "the
// server maximum"). A negative or unparseable value is an error.
func parseTTL(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("invalid ttl: use a Go duration like 30m or 4h")
	}
	if d < 0 {
		return 0, errors.New("ttl must not be negative")
	}
	return d, nil
}
