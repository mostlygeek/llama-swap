package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

type selectorContextKey struct{}

func withSelectorContext(r *http.Request, selectorID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), selectorContextKey{}, selectorID))
}

func selectorFromContext(ctx context.Context) string {
	selectorID, _ := ctx.Value(selectorContextKey{}).(string)
	return selectorID
}

type spilloverTarget struct {
	target  string
	modelID string
}

type selectorSpilloverState struct {
	mu        sync.Mutex
	spillover int
	targets   []spilloverTarget
	inflight  map[string]int
	rr        uint64
}

type selectorSpilloverTracker struct {
	states map[string]*selectorSpilloverState
}

func newSelectorSpilloverTracker(cfg config.Config) *selectorSpilloverTracker {
	tracker := &selectorSpilloverTracker{states: make(map[string]*selectorSpilloverState)}
	for selectorID, selector := range cfg.Selectors {
		if selector.Strategy != config.SelectorStrategySpillover {
			continue
		}
		state := &selectorSpilloverState{
			spillover: selector.Settings.Spillover,
			targets:   make([]spilloverTarget, 0, len(selector.Targets)),
			inflight:  make(map[string]int, len(selector.Targets)),
		}
		for _, target := range selector.Targets {
			modelID, _ := cfg.RealModelName(target)
			state.targets = append(state.targets, spilloverTarget{target: target, modelID: modelID})
		}
		tracker.states[selectorID] = state
	}
	return tracker
}

func (t *selectorSpilloverTracker) release(selectorID, modelID string) {
	if t == nil {
		return
	}
	state := t.states[selectorID]
	if state == nil {
		return
	}
	state.mu.Lock()
	if state.inflight[modelID] > 0 {
		state.inflight[modelID]--
	}
	state.mu.Unlock()
}

// CreateSelectorMiddleware resolves selector model IDs after profile rewrites
// and before the normal request context, filters, routing, and metrics pipeline.
func CreateSelectorMiddleware(s *Server) chain.Middleware {
	spillovers := newSelectorSpilloverTracker(s.cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(s.cfg.Selectors) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			model, err := shared.ExtractModel(r)
			if err != nil || model == "" {
				next.ServeHTTP(w, r)
				return
			}
			selector, found := s.cfg.Selectors[model]
			if !found {
				next.ServeHTTP(w, r)
				return
			}

			var target string
			switch selector.Strategy {
			case config.SelectorStrategyPin:
				target, err = strategyPin(selector)
			case config.SelectorStrategyWarm:
				target, err = strategyWarm(s.cfg, selector, s.local.RunningModels())
			case config.SelectorStrategySpillover:
				target, err = strategySpillover(model, spillovers, s.local.RunningModels())
			default:
				err = fmt.Errorf("unknown selector strategy %q", selector.Strategy)
			}
			if err != nil {
				shared.SendResponse(w, r, http.StatusServiceUnavailable, err.Error())
				return
			}

			updated, err := shared.ReplaceRequestModel(r, model, target)
			if err != nil {
				if selector.Strategy == config.SelectorStrategySpillover {
					if modelID, found := s.cfg.RealModelName(target); found {
						spillovers.release(model, modelID)
					}
				}
				shared.SendResponse(w, r, http.StatusBadRequest, err.Error())
				return
			}

			s.proxylog.Debugf("selector: id=%s target=%s", model, target)

			if selector.Strategy == config.SelectorStrategySpillover {
				modelID, _ := s.cfg.RealModelName(target)
				defer spillovers.release(model, modelID)
			}
			next.ServeHTTP(w, withSelectorContext(updated, model))
		})
	}
}

func strategyPin(selector config.SelectorConfig) (string, error) {
	if len(selector.Targets) == 0 {
		return "", fmt.Errorf("selector has no targets")
	}
	return selector.Targets[0], nil
}

func strategyWarm(cfg config.Config, selector config.SelectorConfig, running map[string]process.ProcessState) (string, error) {
	if len(selector.Targets) == 0 {
		return "", fmt.Errorf("selector has no targets")
	}

	for _, target := range selector.Targets {
		modelID, _ := cfg.RealModelName(target)
		if running[modelID] == process.StateReady {
			return target, nil
		}
	}
	for _, target := range selector.Targets {
		modelID, _ := cfg.RealModelName(target)
		if running[modelID] == process.StateStarting {
			return target, nil
		}
	}
	return selector.Targets[0], nil
}

func strategySpillover(selectorID string, tracker *selectorSpilloverTracker, running map[string]process.ProcessState) (string, error) {
	if tracker == nil || tracker.states[selectorID] == nil {
		return "", fmt.Errorf("spillover selector %q is not configured", selectorID)
	}
	state := tracker.states[selectorID]
	state.mu.Lock()
	defer state.mu.Unlock()

	active := make([]spilloverTarget, 0, len(state.targets))
	cold := make([]spilloverTarget, 0, len(state.targets))
	for _, target := range state.targets {
		processState, runningNow := running[target.modelID]
		switch {
		case processState == process.StateStopping || processState == process.StateShutdown:
			continue
		case processState == process.StateReady || processState == process.StateStarting:
			active = append(active, target)
		case state.inflight[target.modelID] > 0:
			active = append(active, target)
		case !runningNow || processState == process.StateStopped:
			cold = append(cold, target)
		}
	}

	if len(active) == 0 {
		if len(cold) == 0 {
			return "", fmt.Errorf("selector %q has no available spillover targets", selectorID)
		}
		return state.reserve(cold[0]), nil
	}

	minimum := state.minimum(active)
	if minimum < state.spillover {
		return state.reserveLeastBusy(active), nil
	}
	if len(cold) > 0 {
		return state.reserve(cold[0]), nil
	}
	return state.reserveLeastBusy(active), nil
}

func (s *selectorSpilloverState) reserve(target spilloverTarget) string {
	s.inflight[target.modelID]++
	return target.target
}

func (s *selectorSpilloverState) reserveLeastBusy(targets []spilloverTarget) string {
	minimum := s.minimum(targets)
	tied := make([]spilloverTarget, 0, len(targets))
	for _, target := range targets {
		if s.inflight[target.modelID] == minimum {
			tied = append(tied, target)
		}
	}
	target := tied[s.rr%uint64(len(tied))]
	s.rr++
	return s.reserve(target)
}

func (s *selectorSpilloverState) minimum(targets []spilloverTarget) int {
	minimum := -1
	for _, target := range targets {
		if count := s.inflight[target.modelID]; minimum < 0 || count < minimum {
			minimum = count
		}
	}
	return minimum
}
