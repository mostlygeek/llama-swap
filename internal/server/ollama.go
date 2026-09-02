package server

import (
	"net/http"

	"github.com/mostlygeek/llama-swap/internal/ollama"
	"github.com/mostlygeek/llama-swap/internal/process"
)

// ollamaDispatcher adapts the Server to the ollama.Dispatcher interface so the
// Ollama compatibility handlers can resolve models and forward translated
// requests through the dispatch pipeline.
type ollamaDispatcher struct {
	s *Server
}

// DispatchJSON forwards an already-translated (OpenAI-shaped) body through the
// dispatch pipeline. The handler installs any response-translating writer as w
// before calling this.
func (od *ollamaDispatcher) DispatchJSON(w http.ResponseWriter, r *http.Request, body []byte) {
	od.s.dispatchTranslated(w, r, body)
}

// ListModels returns every non-unlisted model, plus aliases when the operator
// enabled includeAliasesInList.
func (od *ollamaDispatcher) ListModels() []ollama.ModelInfo {
	cfg := od.s.cfg
	out := make([]ollama.ModelInfo, 0, len(cfg.Models))
	for id, mc := range cfg.Models {
		if mc.Unlisted {
			continue
		}
		out = append(out, ollama.ModelInfo{
			ID:                id,
			Aliases:           mc.Aliases,
			Name:              mc.Name,
			Description:       mc.Description,
			Metadata:          mc.Metadata,
			PassthroughOllama: mc.PassthroughOllama,
		})
		if cfg.IncludeAliasesInList {
			for _, alias := range mc.Aliases {
				out = append(out, ollama.ModelInfo{
					ID:                alias,
					Name:              mc.Name,
					Description:       mc.Description,
					Metadata:          mc.Metadata,
					PassthroughOllama: mc.PassthroughOllama,
				})
			}
		}
	}
	return out
}

// FindModel resolves a name or alias to its info.
func (od *ollamaDispatcher) FindModel(name string) (ollama.ModelInfo, bool) {
	realID, ok := od.s.cfg.RealModelName(name)
	if !ok {
		return ollama.ModelInfo{}, false
	}
	mc := od.s.cfg.Models[realID]
	return ollama.ModelInfo{
		ID:                realID,
		Aliases:           mc.Aliases,
		Name:              mc.Name,
		Description:       mc.Description,
		Metadata:          mc.Metadata,
		PassthroughOllama: mc.PassthroughOllama,
	}, true
}

// RunningModels returns the IDs of models currently in the ready state.
func (od *ollamaDispatcher) RunningModels() []string {
	var out []string
	for id, state := range od.s.local.RunningModels() {
		if state == process.StateReady {
			out = append(out, id)
		}
	}
	return out
}
