package server

import (
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// ComfyUI compatibility endpoint. ComfyUI is a web app rather than an
// OpenAI-compatible server, so it is served through its own passthrough bound
// to the reserved config.ComfyUIModelID model. Routes are registered in
// server.go; the model's compatibility settings are applied while loading
// config.

func handleComfyUIRedirect(w http.ResponseWriter, r *http.Request) {
	location := "/comfyui/"
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	status := http.StatusPermanentRedirect
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		status = http.StatusMovedPermanently
	}
	http.Redirect(w, r, location, status)
}

// comfyUIWebsocketPath is the ComfyUI frontend's websocket endpoint. It is the
// only path under /comfyui/ that may not start an unloaded model.
const comfyUIWebsocketPath = "/ws"

// isComfyUIWebsocketPath reports whether path is the ComfyUI websocket
// endpoint or a sub path of it. path is the decoded path only, so query
// parameters such as ?clientId= never affect the match.
func isComfyUIWebsocketPath(path string) bool {
	return path == comfyUIWebsocketPath ||
		strings.HasPrefix(path, comfyUIWebsocketPath+"/")
}

// handleComfyUI proxies requests under /comfyui/ to the fixed local
// ComfyUI model. Its compatibility settings are applied while loading config.
func (s *Server) handleComfyUI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.cfg.Models[config.ComfyUIModelID]; !ok || !s.local.Handles(config.ComfyUIModelID) {
		swaputil.SendResponse(w, r, http.StatusNotFound, "local model "+config.ComfyUIModelID+" not found")
		return
	}

	// Strip the /comfyui prefix before forwarding. URL.Path and PathValue are
	// decoded, so retain the matching escaped suffix in RawPath exactly as the
	// generic /upstream handler does.
	remainingPath := "/" + strings.TrimPrefix(r.PathValue("comfyPath"), "/")
	escapedRemaining := swaputil.EscapedPathSuffix(r.URL.EscapedPath(), "/comfyui")
	r.URL.Path = remainingPath
	r.URL.RawPath = escapedRemaining

	// An open ComfyUI tab keeps retrying its websocket for as long as it is
	// open, so that one path must not load the model back after an unload.
	// Every other request is a deliberate action and may start it.
	if r.Method == http.MethodGet && isComfyUIWebsocketPath(remainingPath) {
		state, ok := s.local.RunningModels()[config.ComfyUIModelID]
		if !ok || state != process.StateReady {
			swaputil.SendResponse(w, r, http.StatusConflict,
				"model "+config.ComfyUIModelID+" is not loaded; "+comfyUIWebsocketPath+" does not start it")
			return
		}
	}

	*r = *r.WithContext(swaputil.SetContext(r.Context(), swaputil.ReqContextData{
		ApiKey:   swaputil.ExtractAPIKey(r),
		Model:    config.ComfyUIModelID,
		ModelID:  config.ComfyUIModelID,
		Metadata: make(map[string]string),
	}))
	s.local.ServeHTTP(w, r)
}
