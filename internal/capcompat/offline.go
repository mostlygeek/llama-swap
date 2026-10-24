package capcompat

import (
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// This file implements an offline context estimator used when a model has
// never been loaded and no upstream server can be probed.
//
// An unloaded model reports no context length, and clients that size their
// requests from /v1/models (or a cache of it) must then guess. But for the
// llama-server family the serving window is usually spelled out in the model
// entry's own command line: llama-server's -c flag. If the operator wrote
// `-c 32768`, that is exactly what the backend will run with when it starts,
// so the value can be derived from the config without touching the upstream.
//
// The estimator reads only what the config says. Nothing is probed, nothing
// is cached, and a models entry without a parsable -c stays at its zero value
// (the capabilities merge then leaves discovery in charge).

// splitArgs splits a command line into whitespace-separated tokens, honouring
// single and double quotes like a shell would. llama-server commands embed
// quoted values (chat templates, JSON kwargs), so a plain whitespace split
// would mangle them.
func splitArgs(cmd string) []string {
	var args []string
	var cur strings.Builder
	inSingle, inDouble, started := false, false, false
	flush := func() {
		if started {
			args = append(args, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range cmd {
		switch {
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(r)
				started = true
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			} else {
				cur.WriteRune(r)
				started = true
			}
		case r == '\'':
			inSingle = true
			started = true
		case r == '"':
			inDouble = true
			started = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return args
}

// parseUnsignedInt reports whether s is a plain non-negative decimal integer.
// Anything else — negative numbers, macro templates such as ${PORT}, suffixed
// units — is rejected, because a command line with an unexpanded macro has no
// meaningful numeric value until it runs.
func parseUnsignedInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// offlineContextFromCmd extracts the context length a llama-server-style
// command will run with, from its -c/--ctx-size flag. It recognises the
// space-separated and equals-separated spellings. A command that does not set
// the flag reports 0.
func offlineContextFromCmd(cmd string) int {
	args := splitArgs(cmd)
	for i, arg := range args {
		var (
			val   string
			found bool
		)
		switch arg {
		case "-c", "--ctx-size":
			if i+1 < len(args) {
				val, found = args[i+1], true
			}
		default:
			if strings.HasPrefix(arg, "--ctx-size=") {
				val, found = strings.TrimPrefix(arg, "--ctx-size="), true
			} else if strings.HasPrefix(arg, "-c=") {
				val, found = strings.TrimPrefix(arg, "-c="), true
			}
		}
		if !found {
			continue
		}
		if n, ok := parseUnsignedInt(val); ok {
			return n
		}
	}
	return 0
}

// EstimateOffline returns capabilities derived without a running upstream.
// The only field it can answer for a llama-server-style entry is the context
// length, when the command states it explicitly. The second return value is
// false when the entry says nothing.
func EstimateOffline(mc config.ModelConfig) (config.ModelCapConfig, bool) {
	ctx := offlineContextFromCmd(mc.Cmd)
	if ctx == 0 {
		return config.ModelCapConfig{}, false
	}
	return config.ModelCapConfig{In: []string{"text"}, Out: []string{"text"}, Context: ctx}, true
}
