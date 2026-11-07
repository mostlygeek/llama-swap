// Package docsgen builds the files `go generate ./docs` copies into docs/
// from elsewhere in the repository. The docs package embeds the copies, since
// go:embed cannot reach files outside its own directory.
package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Sources, relative to the repository root.
const (
	SchemaSource = "config-schema.json"
	PromptSource = "ui/src/lib/prompts/docsAgent.ts"
)

// Outputs, relative to the docs directory.
const (
	SchemaFile = "config-schema.json"
	PromptFile = "docs-agent-prompt.md"
)

var promptLiteral = regexp.MustCompile("(?s)export const DOCS_AGENT_SYSTEM_PROMPT = `(.*?)`;")

// Prompt extracts DOCS_AGENT_SYSTEM_PROMPT from the TypeScript source and
// undoes the template literal's escaping. The UI stays the place the prompt
// is edited; the evals tune it there.
func Prompt(ts string) (string, error) {
	m := promptLiteral.FindStringSubmatch(ts)
	if m == nil {
		return "", fmt.Errorf("DOCS_AGENT_SYSTEM_PROMPT not found in %s", PromptSource)
	}
	text := m[1]
	if strings.Contains(text, "${") {
		return "", fmt.Errorf("DOCS_AGENT_SYSTEM_PROMPT uses ${} interpolation, which cannot be copied as plain text")
	}
	return strings.NewReplacer("\\`", "`", "\\\\", "\\", "\\$", "$").Replace(text), nil
}

// Build returns the generated files, keyed by their name in docs/.
func Build(repoRoot string) (map[string][]byte, error) {
	schema, err := os.ReadFile(filepath.Join(repoRoot, SchemaSource))
	if err != nil {
		return nil, err
	}
	ts, err := os.ReadFile(filepath.Join(repoRoot, PromptSource))
	if err != nil {
		return nil, err
	}
	prompt, err := Prompt(string(ts))
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		SchemaFile: schema,
		PromptFile: []byte(prompt),
	}, nil
}
