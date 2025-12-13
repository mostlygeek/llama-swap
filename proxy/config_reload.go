package proxy

import (
	"slices"

	"github.com/mostlygeek/llama-swap/proxy/config"
)

// modelNeedsRestart returns true if the model config changed in a way that requires restart
func modelNeedsRestart(old, new config.ModelConfig) bool {
	// These fields require restart if changed
	if old.Cmd != new.Cmd {
		return true
	}
	if old.CmdStop != new.CmdStop {
		return true
	}
	if old.Proxy != new.Proxy {
		return true
	}
	if old.CheckEndpoint != new.CheckEndpoint {
		return true
	}
	if !slices.Equal(old.Env, new.Env) {
		return true
	}
	if old.ConcurrencyLimit != new.ConcurrencyLimit {
		return true
	}
	return false
}

// shouldRestartModel returns true if the model should be restarted based on config changes and restart settings
func shouldRestartModel(old, new config.ModelConfig, globalRestart bool) bool {
	if !modelNeedsRestart(old, new) {
		return false
	}

	// Per-model setting overrides global
	if new.ForceRestart != nil {
		return *new.ForceRestart
	}

	return globalRestart
}
