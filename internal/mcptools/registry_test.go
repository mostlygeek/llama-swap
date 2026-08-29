package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeProvider is a Provider whose behaviour each test dictates.
type fakeProvider struct {
	id       string
	tools    []Tool
	toolsErr error

	// calledWith records the context and local name Call received, which is
	// how the context-propagation tests observe the plumbing.
	calledWith  context.Context
	calledName  string
	result      Result
	callErr     error
	shutdownErr error
	shutdowns   int
}

func (p *fakeProvider) ID() string { return p.id }

func (p *fakeProvider) Tools(context.Context) ([]Tool, error) {
	return p.tools, p.toolsErr
}

func (p *fakeProvider) Call(ctx context.Context, name string, _ map[string]json.RawMessage) (Result, error) {
	p.calledWith, p.calledName = ctx, name
	return p.result, p.callErr
}

func (p *fakeProvider) Shutdown(time.Duration) error {
	p.shutdowns++
	return p.shutdownErr
}

func tool(name string) Tool {
	return Tool{Name: name, Description: name, InputSchema: map[string]any{"type": "object"}}
}

func namesOf(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func TestRegistry_QualifiesNames(t *testing.T) {
	r, err := New(&fakeProvider{id: "alpha", tools: []Tool{tool("one"), tool("two")}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tools, next, err := r.Tools(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if next != "" {
		t.Errorf("nextCursor = %q, want empty", next)
	}

	got := namesOf(tools)
	if len(got) != 2 || got[0] != "alpha__one" || got[1] != "alpha__two" {
		t.Errorf("names = %v, want [alpha__one alpha__two]", got)
	}
}

// Providers deal in local names; the registry owns the namespace. A provider
// that already qualified its own names would double-prefix.
func TestRegistry_MergesProvidersInStableOrder(t *testing.T) {
	r, err := New(
		&fakeProvider{id: "zeta", tools: []Tool{tool("b"), tool("a")}},
		&fakeProvider{id: "alpha", tools: []Tool{tool("y")}},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var first []string
	for i := 0; i < 5; i++ {
		tools, _, err := r.Tools(context.Background(), "", 0)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		got := namesOf(tools)
		if i == 0 {
			first = got
			want := []string{"alpha__y", "zeta__a", "zeta__b"}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("names = %v, want %v", got, want)
			}
			continue
		}
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("run %d = %v, want %v (ordering must be stable for cursors)", i, got, first)
		}
	}
}

func TestRegistry_RejectsBadProviders(t *testing.T) {
	tests := []struct {
		name      string
		providers []Provider
		wantHas   string
	}{
		{"duplicate id", []Provider{&fakeProvider{id: "a"}, &fakeProvider{id: "a"}}, "duplicate provider"},
		{"empty id", []Provider{&fakeProvider{id: ""}}, "empty"},
		{"id with a dot", []Provider{&fakeProvider{id: "a.b"}}, "not allowed"},
		{"id with the separator", []Provider{&fakeProvider{id: "a__b"}}, "must not contain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.providers...)
			if err == nil {
				t.Fatal("New succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantHas) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantHas)
			}
		})
	}
}

// A name a client cannot use is worse than no tool at all, so listing fails
// loudly rather than advertising something the Playground would reject.
func TestRegistry_RejectsUnusableToolNames(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		wantHas string
	}{
		{"a dot is valid in MCP but not in an OpenAI function name", "search.code", "not allowed"},
		{"over the length budget", strings.Repeat("x", MaxNameLen), "over the"},
		{"empty local name would be unreachable", "", "does not round-trip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := New(&fakeProvider{id: "p", tools: []Tool{tool(tt.tool)}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, _, err = r.Tools(context.Background(), "", 0)
			if err == nil {
				t.Fatal("Tools succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantHas) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantHas)
			}
		})
	}
}

func TestRegistry_ReportsProviderListingErrors(t *testing.T) {
	r, err := New(
		&fakeProvider{id: "good", tools: []Tool{tool("a")}},
		&fakeProvider{id: "bad", toolsErr: errors.New("upstream unreachable")},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := r.Tools(context.Background(), "", 0); err == nil {
		t.Fatal("Tools succeeded, want an error")
	} else if !strings.Contains(err.Error(), `provider "bad"`) {
		t.Errorf("error = %q, want it to name the provider", err)
	}
}

func TestRegistry_Paginates(t *testing.T) {
	var tools []Tool
	const total = 120
	for i := 0; i < total; i++ {
		tools = append(tools, tool(fmt.Sprintf("t%03d", i)))
	}
	r, err := New(&fakeProvider{id: "p", tools: tools})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("walks to exhaustion without gaps or repeats", func(t *testing.T) {
		seen := map[string]bool{}
		cursor := ""
		pages := 0

		for {
			page, next, err := r.Tools(context.Background(), cursor, 25)
			if err != nil {
				t.Fatalf("page %d: %v", pages, err)
			}
			pages++
			for _, tool := range page {
				if seen[tool.Name] {
					t.Fatalf("%s returned twice", tool.Name)
				}
				seen[tool.Name] = true
			}
			if next == "" {
				break
			}
			cursor = next
			if pages > 20 {
				t.Fatal("cursor never terminated")
			}
		}

		if len(seen) != total {
			t.Errorf("saw %d tools across %d pages, want %d", len(seen), pages, total)
		}
		if pages != 5 {
			t.Errorf("pages = %d, want 5 for %d tools at 25 per page", pages, total)
		}
	})

	t.Run("a full final page does not advertise another", func(t *testing.T) {
		_, next, err := r.Tools(context.Background(), "", total)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if next != "" {
			t.Errorf("nextCursor = %q, want empty when the page holds everything", next)
		}
	})

	t.Run("defaults the page size", func(t *testing.T) {
		page, next, err := r.Tools(context.Background(), "", 0)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if len(page) != DefaultPageSize || next == "" {
			t.Errorf("page = %d tools, next = %q; want %d and a cursor", len(page), next, DefaultPageSize)
		}
	})

	t.Run("a cursor we did not issue is rejected", func(t *testing.T) {
		if _, _, err := r.Tools(context.Background(), "!!not base64!!", 25); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("error = %v, want ErrInvalidCursor", err)
		}
	})
}

func TestRegistry_RoutesToTheOwningProvider(t *testing.T) {
	alpha := &fakeProvider{id: "alpha", tools: []Tool{tool("go")}, result: Result{Content: "from alpha"}}
	beta := &fakeProvider{id: "beta", tools: []Tool{tool("go")}, result: Result{Content: "from beta"}}

	r, err := New(alpha, beta)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, found, err := r.Call(context.Background(), "beta__go", nil)
	if err != nil || !found {
		t.Fatalf("Call: found=%v err=%v", found, err)
	}
	if result.Content != "from beta" {
		t.Errorf("content = %q, want the beta provider's", result.Content)
	}
	// The provider receives the local name; the namespace is stripped.
	if beta.calledName != "go" {
		t.Errorf("provider saw name %q, want the unqualified %q", beta.calledName, "go")
	}
	if alpha.calledName != "" {
		t.Error("the wrong provider was called")
	}
}

func TestRegistry_UnknownToolIsNotFound(t *testing.T) {
	r, err := New(&fakeProvider{id: "alpha", tools: []Tool{tool("go")}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, name := range []string{"alpha__missing", "nope__go", "unqualified", "", "__go", "alpha__"} {
		if _, found, _ := r.Call(context.Background(), name, nil); found && name != "alpha__missing" {
			t.Errorf("Call(%q) found = true, want false", name)
		}
	}
}

// Context has to reach the provider, or a proxy provider cannot honour client
// cancellation or a deadline.
func TestRegistry_PropagatesContext(t *testing.T) {
	provider := &fakeProvider{id: "alpha", tools: []Tool{tool("go")}}
	r, err := New(provider)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "marker"))

	if _, _, err := r.Call(ctx, "alpha__go", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if provider.calledWith == nil {
		t.Fatal("provider received no context")
	}
	if provider.calledWith.Value(key{}) != "marker" {
		t.Error("provider received a different context than the caller's")
	}

	cancel()
	if provider.calledWith.Err() == nil {
		t.Error("cancelling the caller's context did not reach the provider's")
	}
}

// A provider's error is a protocol failure; Result.IsError is a tool that ran
// and could not answer. The registry must not blur them.
func TestRegistry_SeparatesErrorsFromIsError(t *testing.T) {
	t.Run("provider error surfaces as an error", func(t *testing.T) {
		r, _ := New(&fakeProvider{id: "p", tools: []Tool{tool("go")}, callErr: errors.New("boom")})
		_, found, err := r.Call(context.Background(), "p__go", nil)
		if !found {
			t.Fatal("found = false, want true: the tool exists, it failed")
		}
		if err == nil || err.Error() != "boom" {
			t.Errorf("err = %v, want boom", err)
		}
	})

	t.Run("IsError surfaces as a result", func(t *testing.T) {
		r, _ := New(&fakeProvider{id: "p", tools: []Tool{tool("go")},
			result: Result{Content: "could not answer", IsError: true}})
		result, found, err := r.Call(context.Background(), "p__go", nil)
		if !found || err != nil {
			t.Fatalf("found=%v err=%v, want a clean result", found, err)
		}
		if !result.IsError || result.Content != "could not answer" {
			t.Errorf("result = %+v, want IsError with the content preserved", result)
		}
	})
}

func TestRegistry_ShutsDownEveryProvider(t *testing.T) {
	alpha := &fakeProvider{id: "alpha"}
	beta := &fakeProvider{id: "beta", shutdownErr: errors.New("stuck")}

	r, err := New(alpha, beta)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = r.Shutdown(time.Second)
	if err == nil || !strings.Contains(err.Error(), `provider "beta"`) {
		t.Errorf("err = %v, want it to name the failing provider", err)
	}
	// One provider failing must not stop the others being released.
	if alpha.shutdowns != 1 || beta.shutdowns != 1 {
		t.Errorf("shutdowns: alpha=%d beta=%d, want 1 each", alpha.shutdowns, beta.shutdowns)
	}
}

// A Server built without a registry must not panic; every method is
// nil-receiver safe, matching reference.Docs.
func TestRegistry_NilIsSafe(t *testing.T) {
	var r *Registry

	if r.HasTools() {
		t.Error("HasTools = true on a nil registry")
	}
	if tools, next, err := r.Tools(context.Background(), "", 0); tools != nil || next != "" || err != nil {
		t.Errorf("Tools = (%v, %q, %v), want all zero", tools, next, err)
	}
	if _, found, err := r.Call(context.Background(), "a__b", nil); found || err != nil {
		t.Errorf("Call found=%v err=%v, want (false, nil)", found, err)
	}
	if err := r.Shutdown(0); err != nil {
		t.Errorf("Shutdown = %v, want nil", err)
	}
}

func TestSplitName(t *testing.T) {
	tests := []struct {
		in               string
		wantID, wantName string
		wantOK           bool
	}{
		{"docs__get_doc", "docs", "get_doc", true},
		{"a__b__c", "a", "b__c", true}, // only the first separator is significant
		{"unqualified", "", "", false},
		{"__trailing", "", "", false},
		{"leading__", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			id, name, ok := SplitName(tt.in)
			if id != tt.wantID || name != tt.wantName || ok != tt.wantOK {
				t.Errorf("SplitName(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.in, id, name, ok, tt.wantID, tt.wantName, tt.wantOK)
			}
		})
	}
}
