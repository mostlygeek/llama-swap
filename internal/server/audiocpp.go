package server

import (
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// audio.cpp UI passthrough. audiocpp_server ships its own web UI and model
// management, so it is served under /audiocpp/ and bound to the reserved
// config.AudioCppModelID model. Requests do not need to name a model. Routes
// are registered in server.go.

func handleAudioCppRedirect(w http.ResponseWriter, r *http.Request) {
	// Relative Location so the redirect keeps a reverse proxy's path prefix
	// (see redirectRelative).
	location := "audiocpp/"
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	status := http.StatusPermanentRedirect
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		status = http.StatusMovedPermanently
	}
	redirectRelative(w, location, status)
}

// handleAudioCpp proxies requests under /audiocpp/ to the fixed local
// audio.cpp model.
func (s *Server) handleAudioCpp(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.cfg.Models[config.AudioCppModelID]; !ok || !s.local.Handles(config.AudioCppModelID) {
		swaputil.SendResponse(w, r, http.StatusNotFound, "local model "+config.AudioCppModelID+" not found")
		return
	}

	// Strip the /audiocpp prefix before forwarding. URL.Path and PathValue are
	// decoded, so retain the matching escaped suffix in RawPath exactly as the
	// generic /upstream handler does.
	remainingPath := "/" + strings.TrimPrefix(r.PathValue("audioCppPath"), "/")
	escapedRemaining := swaputil.EscapedPathSuffix(r.URL.EscapedPath(), "/audiocpp")
	r.URL.Path = remainingPath
	r.URL.RawPath = escapedRemaining

	*r = *r.WithContext(swaputil.SetContext(r.Context(), swaputil.ReqContextData{
		ApiKey:   swaputil.ExtractAPIKey(r),
		Model:    config.AudioCppModelID,
		ModelID:  config.AudioCppModelID,
		Metadata: make(map[string]string),
	}))
	s.local.ServeHTTP(w, r)
}
