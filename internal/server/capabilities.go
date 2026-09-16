package server

import (
	"context"
	"net/url"

	"github.com/mostlygeek/llama-swap/internal/capcompat"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// onProcessStateChange refreshes a model's discovered capabilities when its
// process becomes ready, which is the only moment the upstream can be asked.
//
// This runs on the event dispatcher's consumer goroutine, and a consumer that
// falls behind eventually blocks the publisher, which here is the process
// state machine itself. So it must not do any work inline: the probe goes to
// its own goroutine and this returns immediately.
func (s *Server) onProcessStateChange(e swaputil.ProcessStateChangeEvent) {
	if e.NewState != string(process.StateReady) {
		return
	}
	mc, ok := s.cfg.Models[e.ProcessName]
	if !ok || mc.Capabilities.DisableAuto {
		return
	}
	go s.refreshCapabilities(e.ProcessName, mc)
}

// refreshCapabilities probes one ready model and caches what it reports.
//
// The probe dials the model's own proxy address rather than going through the
// local router. Routing a synthetic request through the router would reset the
// model's TTL idle window and occupy a concurrency slot, and could start a
// model that had already stopped again by the time this ran.
func (s *Server) refreshCapabilities(modelID string, mc config.ModelConfig) {
	base, err := url.Parse(mc.Proxy)
	if err != nil {
		s.proxylog.Debugf("capcompat: <%s> unusable proxy URL %q: %v", modelID, mc.Proxy, err)
		return
	}

	client := capcompat.NewClient(base, mc.Timeouts)
	key := capcompat.LocalKey(modelID, mc)
	name := capcompat.UpstreamModelName(modelID, mc)

	if err := s.capcompat.Refresh(s.shutdownCtx, key, client, name); err != nil {
		// Debug, not warn. llama-swap also fronts image, speech and
		// transcription servers with no capability surface, and a model that
		// stopped again mid-probe is normal. Neither is worth a warning on
		// every model start.
		s.proxylog.Debugf("capcompat: <%s> discovery failed: %v", modelID, err)
	}
}

// resolveCapabilities returns the capabilities to advertise for a local model:
// what the config sets, with anything it leaves unset filled in from the last
// successful probe.
//
// Configured values win field by field, so a model that hand-sets one field
// still benefits from discovery for the rest. A model with
// capabilities.disableAuto gets exactly what its config says.
func (s *Server) resolveCapabilities(ctx context.Context, modelID string, mc config.ModelConfig) config.ModelCapConfig {
	caps := mc.Capabilities
	if caps.DisableAuto {
		return caps
	}
	auto, found := s.capcompat.Lookup(ctx, capcompat.LocalKey(modelID, mc))
	if !found {
		return caps
	}
	return caps.Merge(auto)
}
