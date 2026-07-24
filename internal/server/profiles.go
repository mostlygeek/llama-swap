package server

import (
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// CreateProfileMiddleware applies the request's active profile snapshot before
// the normal request pipeline resolves the rewritten model.
func CreateProfileMiddleware(s *Server) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			profile, ok := s.cfg.Profiles[s.ActiveProfile()]
			if ok {
				var model, replacement string
				var pinned bool
				if strings.HasPrefix(r.URL.Path, "/upstream/") {
					model, replacement, pinned = upstreamProfilePin(r.PathValue("upstreamPath"), profile.Pins)
				} else {
					model, _ = shared.ExtractModel(r)
					if model != "" {
						replacement, pinned = profile.Pins[model]
					}
				}
				if pinned {
					updated, err := shared.ReplaceRequestModel(r, model, replacement)
					if err != nil {
						shared.SendResponse(w, r, http.StatusBadRequest, err.Error())
						return
					}
					r = updated
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func upstreamProfilePin(upstreamPath string, pins map[string]string) (model, replacement string, found bool) {
	upstreamPath = strings.TrimPrefix(upstreamPath, "/")
	matchedPin := ""
	for pin, candidate := range pins {
		switch {
		case upstreamPath == pin:
			if len(pin) > len(matchedPin) {
				matchedPin = pin
				replacement = candidate
			}
		case strings.HasPrefix(upstreamPath, pin+"/"):
			if len(pin) > len(matchedPin) {
				matchedPin = pin
				replacement = candidate
			}
		}
	}
	return matchedPin, replacement, matchedPin != ""
}
