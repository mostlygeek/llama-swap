package docs

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/docs/internal/docsgen"
)

func TestDocs_GeneratedFilesAreCurrent(t *testing.T) {
	want, err := docsgen.Build("..")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range want {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%v: run `go generate ./docs`", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("docs/%s is out of date: run `go generate ./docs`", name)
		}
	}
}

func TestDocs_AgentPromptIsEmbedded(t *testing.T) {
	if !strings.Contains(AgentPrompt, "`docs__search_docs`") {
		t.Errorf("AgentPrompt does not list docs__search_docs:\n%.300s", AgentPrompt)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(ConfigSchema), []byte("{")) {
		t.Error("ConfigSchema is not JSON")
	}
}

func TestDocsgen_PromptUnescapes(t *testing.T) {
	got, err := docsgen.Prompt("export const DOCS_AGENT_SYSTEM_PROMPT = `Use \\`ttl\\` and \\\\n.`;")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Use `ttl` and \\n."; got != want {
		t.Errorf("Prompt = %q, want %q", got, want)
	}
	if _, err := docsgen.Prompt("export const DOCS_AGENT_SYSTEM_PROMPT = `a ${b}`;"); err == nil {
		t.Error("interpolation should be rejected")
	}
}
