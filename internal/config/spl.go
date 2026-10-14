package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/spl"
)

// SPLPrograms holds the compiled Swap Policy Language programs from a
// configuration. Programs are compiled once at load time and are safe for
// concurrent use.
type SPLPrograms struct {
	// Library holds the named policies from the top-level policies key.
	Library *spl.Library
	// OnRequest is hooks.on_request, or nil when blank.
	OnRequest *spl.Program
	// Models maps a model ID to its compiled filters.policy. Models with a
	// blank policy have no entry.
	Models map[string]*spl.Program
	// Peers maps a peer ID to its compiled filters.policy. Peers with a
	// blank policy have no entry.
	Peers map[string]*spl.Program
}

// policyNamePattern restricts policy names to what `apply <name>` can parse.
var policyNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// validateSPL compiles every SPL program in the configuration and stores the
// result in config.spl. It is left nil when no program is configured so a
// Config without SPL is indistinguishable from one loaded before SPL existed.
func validateSPL(config *Config) error {
	for name := range config.Policies {
		if !policyNamePattern.MatchString(name) {
			return fmt.Errorf("policies: invalid policy name %q, must match %s", name, policyNamePattern.String())
		}
	}

	lib, err := spl.NewLibrary(config.Policies, ProtectedParams)
	if err != nil {
		return err
	}

	programs := &SPLPrograms{
		Library: lib,
		Models:  make(map[string]*spl.Program),
		Peers:   make(map[string]*spl.Program),
	}
	found := len(config.Policies) > 0

	compile := func(src string) (*spl.Program, error) {
		if strings.TrimSpace(src) == "" {
			return nil, nil
		}
		prog, err := spl.Parse(src)
		if err != nil {
			return nil, err
		}
		if err := prog.Validate(lib, ProtectedParams); err != nil {
			return nil, err
		}
		return prog, nil
	}

	if prog, err := compile(config.Hooks.OnRequest); err != nil {
		return fmt.Errorf("hooks.on_request: %w", err)
	} else if prog != nil {
		programs.OnRequest = prog
		found = true
	}

	modelIDs := make([]string, 0, len(config.Models))
	for id := range config.Models {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)
	for _, id := range modelIDs {
		prog, err := compile(config.Models[id].Filters.Policy)
		if err != nil {
			return fmt.Errorf("model %s: filters.policy: %w", id, err)
		}
		if prog != nil {
			programs.Models[id] = prog
			found = true
		}
	}

	peerIDs := make([]string, 0, len(config.Peers))
	for id := range config.Peers {
		peerIDs = append(peerIDs, id)
	}
	sort.Strings(peerIDs)
	for _, id := range peerIDs {
		prog, err := compile(config.Peers[id].Filters.Policy)
		if err != nil {
			return fmt.Errorf("peer %s: filters.policy: %w", id, err)
		}
		if prog != nil {
			programs.Peers[id] = prog
			found = true
		}
	}

	if found {
		config.spl = programs
	}
	return nil
}
