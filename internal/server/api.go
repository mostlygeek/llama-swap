package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
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
	*r = *r.WithContext(router.SetModel(r.Context(), searchName, modelID))

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
