package server

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// ActiveProfile returns the active profile name and a copy of its alias overlay.
// When no profile is active (or the active name is unknown) it returns an empty
// name and a nil overlay, which all profile-aware resolution treats as "no
// remapping".
func (s *Server) ActiveProfile() (string, map[string]string) {
	s.activeProfileMu.RLock()
	defer s.activeProfileMu.RUnlock()
	if s.activeProfileName == "" {
		return "", nil
	}
	profile, found := s.cfg.Profiles[s.activeProfileName]
	if !found {
		return "", nil
	}
	return s.activeProfileName, maps.Clone(profile.Aliases)
}

// SetActiveProfile selects the active profile by name. An empty name clears the
// active profile. Unknown names are rejected. A ProfileChangedEvent is emitted
// only when the active name actually changes.
func (s *Server) SetActiveProfile(name string) error {
	s.activeProfileMu.Lock()
	if name != "" {
		if _, found := s.cfg.Profiles[name]; !found {
			s.activeProfileMu.Unlock()
			return fmt.Errorf("profile not found: %s", name)
		}
	}
	changed := s.activeProfileName != name
	s.activeProfileName = name
	s.activeProfileMu.Unlock()

	if changed {
		event.Emit(shared.ProfileChangedEvent{ActiveProfileName: name})
	}
	return nil
}

// CreateProfileMiddleware returns middleware that applies the active profile's
// alias overlay before body filtering and model dispatch. It runs after
// CreateRequestContextMiddleware has populated the request context, then
// rewrites the resolved model in the context so every downstream consumer (body
// filters, in-flight tracking, metrics, dispatch) sees the profile-resolved
// model. When no profile is active it is a complete pass through with zero
// overhead.
//
// Only aliases explicitly overridden by the active profile are remapped: model
// IDs always win, static aliases and peer models resolve normally downstream,
// and an alias whose profile target is empty (YAML ~) is reported as not found
// while the profile is active.
func (s *Server) CreateProfileMiddleware() chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, overlay := s.ActiveProfile()
			if len(overlay) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			data, ok := shared.ReadContext(r.Context())
			if !ok {
				// Context not populated yet; let the downstream resolver report
				// the missing-model error.
				next.ServeHTTP(w, r)
				return
			}

			requested := data.Model
			target, overridden := overlay[requested]
			if !overridden {
				next.ServeHTTP(w, r)
				return
			}
			if _, isModel := s.cfg.Models[requested]; isModel {
				// Model IDs always win over profile alias overrides.
				next.ServeHTTP(w, r)
				return
			}
			if target == "" {
				// Alias disabled while this profile is active.
				shared.SendError(w, r, shared.ErrNoLocalModelFound)
				return
			}

			modelID, found := s.cfg.RealModelNameWithProfile(requested, overlay)
			if !found {
				next.ServeHTTP(w, r)
				return
			}

			data.Model = s.cfg.EffectiveRequestName(requested, overlay)
			data.ModelID = modelID
			if mc, ok := s.cfg.Models[modelID]; ok {
				data.SendLoadingState = mc.SendLoadingState != nil && *mc.SendLoadingState
			}
			*r = *r.WithContext(shared.SetContext(r.Context(), data))
			next.ServeHTTP(w, r)
		})
	}
}

// apiProfile is one entry in the GET /api/profiles payload.
type apiProfile struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Aliases     map[string]string `json:"aliases"`
}

// handleAPIListProfiles serves the configured profiles plus the active one.
func (s *Server) handleAPIListProfiles(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(s.cfg.Profiles))
	for name := range s.cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	profiles := make([]apiProfile, 0, len(names))
	for _, name := range names {
		profile := s.cfg.Profiles[name]
		profiles = append(profiles, apiProfile{
			Name:        name,
			Description: profile.Description,
			Aliases:     profile.Aliases,
		})
	}

	active, _ := s.ActiveProfile()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active":   active,
		"profiles": profiles,
	})
}

// handleAPIActivateProfile selects the active profile named in the path. An
// empty name clears the active profile.
func (s *Server) handleAPIActivateProfile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.PathValue("name"), "/")
	if err := s.SetActiveProfile(name); err != nil {
		shared.SendResponse(w, r, http.StatusNotFound, "profile not found")
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
