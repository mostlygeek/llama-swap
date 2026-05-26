package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// modelRecord is one entry in the OpenAI-compatible /v1/models listing.
type modelRecord struct {
	ID          string         `json:"id"`
	Object      string         `json:"object"`
	Created     int64          `json:"created"`
	OwnedBy     string         `json:"owned_by"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// handleListModels serves the OpenAI-compatible model listing: local models
// (with optional aliases) plus peer models.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	created := time.Now().Unix()
	data := make([]modelRecord, 0, len(s.cfg.Models))

	newRecord := func(id, name, description string, metadata map[string]any) modelRecord {
		rec := modelRecord{
			ID:          id,
			Object:      "model",
			Created:     created,
			OwnedBy:     "llama-swap",
			Name:        strings.TrimSpace(name),
			Description: strings.TrimSpace(description),
		}
		if len(metadata) > 0 {
			rec.Meta = map[string]any{"llamaswap": metadata}
		}
		return rec
	}

	for id, mc := range s.cfg.Models {
		if mc.Unlisted {
			continue
		}
		data = append(data, newRecord(id, mc.Name, mc.Description, mc.Metadata))

		if s.cfg.IncludeAliasesInList {
			for _, alias := range mc.Aliases {
				if alias := strings.TrimSpace(alias); alias != "" {
					data = append(data, newRecord(alias, mc.Name, mc.Description, mc.Metadata))
				}
			}
		}
	}

	for peerID, peer := range s.cfg.Peers {
		for _, modelID := range peer.Models {
			data = append(data, newRecord(modelID, peerID+": "+modelID, "", map[string]any{"peerID": peerID}))
		}
	}

	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })

	// Echo the Origin so browser clients can read the listing.
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

// runningModel is one entry in the /running listing.
type runningModel struct {
	Model       string `json:"model"`
	State       string `json:"state"`
	Cmd         string `json:"cmd"`
	Proxy       string `json:"proxy"`
	TTL         int    `json:"ttl"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// handleUnload stops every running local process. Peer models are remote and
// unaffected.
func (s *Server) handleUnload(w http.ResponseWriter, r *http.Request) {
	s.local.Unload(0)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleRunning lists local processes that are not stopped, joining each model
// ID against its config for the cmd/proxy/ttl/name/description metadata.
func (s *Server) handleRunning(w http.ResponseWriter, r *http.Request) {
	states := s.local.RunningModels()
	list := make([]runningModel, 0, len(states))
	for id, state := range states {
		mc := s.cfg.Models[id]
		list = append(list, runningModel{
			Model:       id,
			State:       string(state),
			Cmd:         mc.Cmd,
			Proxy:       mc.Proxy,
			TTL:         mc.UnloadAfter,
			Name:        mc.Name,
			Description: mc.Description,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Model < list[j].Model })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"running": list})
}

// discardResponseWriter satisfies http.ResponseWriter for preload requests,
// dropping the body while capturing the status code.
type discardResponseWriter struct {
	header http.Header
	status int
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

func (d *discardResponseWriter) WriteHeader(status int) { d.status = status }

// startPreload fires a background GET / at every model named in
// Hooks.OnStartup.Preload so they are warm before the first real request.
// Preload names are already resolved to real model IDs by config loading.
func (s *Server) startPreload() {
	models := s.cfg.Hooks.OnStartup.Preload
	if len(models) == 0 {
		return
	}
	go func() {
		for _, modelID := range models {
			if !s.local.Handles(modelID) {
				s.proxylog.Warnf("preload: model %s is not a local model, skipping", modelID)
				continue
			}
			s.proxylog.Infof("preloading model: %s", modelID)

			req, err := http.NewRequestWithContext(s.shutdownCtx, http.MethodGet, "/", nil)
			if err != nil {
				continue
			}
			req = req.WithContext(router.SetContext(req.Context(), router.ReqContextData{Model: modelID, ModelID: modelID}))

			dw := &discardResponseWriter{status: http.StatusOK}
			s.local.ServeHTTP(dw, req)

			success := dw.status < http.StatusBadRequest
			if !success {
				s.proxylog.Errorf("failed to preload model %s: status %d", modelID, dw.status)
			}
			event.Emit(shared.ModelPreloadedEvent{ModelName: modelID, Success: success})
		}
	}()
}

// handleMetrics serves Prometheus-format performance metrics. Returns 503 when
// performance monitoring is disabled.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.perf == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("# performance monitor not available\n"))
		return
	}
	s.perf.MetricsHandler().ServeHTTP(w, r)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func handleRootRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui", http.StatusFound)
}

func handleUpstreamRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/models", http.StatusFound)
}

// handleUpstream proxies ANY request under /upstream/<model>/<path> directly to
// the model's process, bypassing model dispatch by body/query inspection.
func (s *Server) handleUpstream(w http.ResponseWriter, r *http.Request) {
	upstreamPath := r.PathValue("upstreamPath")

	searchName, modelID, remainingPath, found := findModelInPath(s.cfg, "/"+upstreamPath)
	if !found {
		router.SendResponse(w, r, http.StatusNotFound, "model not found")
		return
	}

	// Redirect /upstream/model to /upstream/model/ so relative URLs in upstream
	// responses resolve. 301 for GET/HEAD, 308 otherwise to preserve the method.
	if remainingPath == "/" && !strings.HasSuffix(r.URL.Path, "/") {
		newPath := "/upstream/" + searchName + "/"
		if r.URL.RawQuery != "" {
			newPath += "?" + r.URL.RawQuery
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, newPath, http.StatusMovedPermanently)
		} else {
			http.Redirect(w, r, newPath, http.StatusPermanentRedirect)
		}
		return
	}

	// Strip the /upstream/<model> prefix before forwarding.
	r.URL.Path = remainingPath
	// Pin the resolved model so the router skips body/query extraction.
	*r = *r.WithContext(router.SetContext(r.Context(), router.ReqContextData{Model: searchName, ModelID: modelID}))

	switch {
	case s.local.Handles(modelID):
		s.local.ServeHTTP(w, r)
	case s.peer.Handles(modelID):
		s.peer.ServeHTTP(w, r)
	default:
		router.SendResponse(w, r, http.StatusNotFound, "no router for model "+modelID)
	}
}

// findModelInPath walks a slash-separated path, building up segments until one
// matches a configured model. This resolves model names that contain slashes
// (e.g. "author/model"). Returns the matched name, its real model ID, the
// remaining path, and whether a match was found.
func findModelInPath(cfg config.Config, path string) (searchName, realName, remainingPath string, found bool) {
	parts := strings.Split(strings.TrimSpace(path), "/")
	name := ""

	for i, part := range parts {
		if part == "" {
			continue
		}
		if name == "" {
			name = part
		} else {
			name = name + "/" + part
		}

		if modelID, ok := cfg.RealModelName(name); ok {
			return name, modelID, "/" + strings.Join(parts[i+1:], "/"), true
		}
	}

	return "", "", "", false
}
