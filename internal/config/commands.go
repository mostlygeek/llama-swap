package config

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/billziss-gh/golib/shlex"
)

func SanitizeCommand(cmdStr string) ([]string, error) {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Handle trailing backslashes by replacing with space
		if strings.HasSuffix(trimmed, "\\") {
			cleanedLines = append(cleanedLines, strings.TrimSuffix(trimmed, "\\")+" ")
		} else {
			cleanedLines = append(cleanedLines, line)
		}
	}

	// put it back together
	cmdStr = strings.Join(cleanedLines, "\n")

	// Split the command into arguments
	var args []string
	if runtime.GOOS == "windows" {
		args = shlex.Windows.Split(cmdStr)
	} else {
		args = shlex.Posix.Split(cmdStr)
	}

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}

func StripComments(cmdStr string) string {
	var cleanedLines []string
	for _, line := range strings.Split(cmdStr, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	return strings.Join(cleanedLines, "\n")
}

// HasFixedReasoningBudget reports whether the upstream command or environment
// overrides llama.cpp's request-level thinking budget.
func HasFixedReasoningBudget(args, env []string) bool {
	for i, arg := range args {
		if arg == "--reasoning-budget" && i+1 < len(args) {
			return true
		}
		if strings.HasPrefix(arg, "--reasoning-budget=") {
			return true
		}
	}
	for _, value := range env {
		if strings.HasPrefix(value, "LLAMA_ARG_THINK_BUDGET=") {
			return true
		}
	}
	return false
}
