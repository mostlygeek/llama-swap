package config

import "fmt"

// validProtocols is the closed set of wire-format names that may appear in
// ModelConfig.Protocols. Kept in the config package so config does not
// depend on the translate package.
var validProtocols = map[string]bool{
	"openai":    true,
	"anthropic": true,
	"ollama":    true,
}

func validateProtocols(ps []string) error {
	if len(ps) == 0 {
		return fmt.Errorf("protocols: must list at least one of openai, anthropic, ollama")
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if !validProtocols[p] {
			return fmt.Errorf("protocols: unknown value %q (valid: openai, anthropic, ollama)", p)
		}
		if seen[p] {
			return fmt.Errorf("protocols: duplicate entry %q", p)
		}
		seen[p] = true
	}
	return nil
}
