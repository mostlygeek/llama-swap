package server

import (
	"fmt"
	"net/http"

	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// handleProps serves GET /props. A request carrying autoload=false must not
// start a model, as in llama-server's router mode, so it is refused with 409
// when its local model is not running. Clients that read the context size of
// every listed model (llama.vscode) would otherwise load them one after
// another. Every other request, and every model that is running, goes to the
// normal dispatch.
//
// Only "true" and "1" mean autoload, matching llama-server; an empty or absent
// parameter keeps the default of loading the model. A model that is starting is
// let through: the request waits for it like any other and causes no swap.
func (s *Server) handleProps(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("autoload"); v != "" && v != "true" && v != "1" {
		if data, err := swaputil.FetchContext(r, s.cfg); err == nil && s.local.Handles(data.ModelID) {
			state := s.local.RunningModels()[data.ModelID]
			if state != process.StateReady && state != process.StateStarting {
				swaputil.SendResponse(w, r, http.StatusConflict,
					fmt.Sprintf("model %s is not loaded; autoload=false requests cannot start it", data.ModelID))
				return
			}
		}
	}
	s.localPeerHandler(w, r)
}
