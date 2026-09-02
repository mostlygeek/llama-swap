package mcptools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultPageSize is how many tools one tools/list page carries when the caller
// does not say.
const DefaultPageSize = 50

// ErrInvalidCursor is returned for a cursor the registry did not issue. The
// transport maps it to JSON-RPC -32602, as the MCP spec requires.
var ErrInvalidCursor = errors.New("invalid cursor")

// Registry aggregates providers into one namespaced tool surface.
//
// It owns namespacing: providers deal in local names, and the registry
// qualifies them on the way out and strips them on the way in. Routing a call
// is therefore a prefix split rather than a scan across providers, which is the
// practical payoff of namespacing every tool.
type Registry struct {
	providers map[string]Provider
	ids       []string // sorted, for deterministic listing
}

// New builds a registry, validating provider IDs and rejecting duplicates.
//
// Tool names are validated lazily in Tools rather than here: a provider that
// proxies an upstream would have to make a network call to enumerate, and
// construction is not the place for that.
func New(providers ...Provider) (*Registry, error) {
	r := &Registry{providers: make(map[string]Provider, len(providers))}

	for _, p := range providers {
		id := p.ID()
		if err := ValidateName(id); err != nil {
			return nil, fmt.Errorf("provider ID: %w", err)
		}
		if strings.Contains(id, NameSeparator) {
			return nil, fmt.Errorf("provider ID %q must not contain %q", id, NameSeparator)
		}
		if _, exists := r.providers[id]; exists {
			return nil, fmt.Errorf("duplicate provider ID %q", id)
		}
		r.providers[id] = p
		r.ids = append(r.ids, id)
	}

	sort.Strings(r.ids)
	return r, nil
}

// HasTools reports whether any provider is registered. Used to decide whether
// to advertise the tools capability, without the I/O a full listing implies.
func (r *Registry) HasTools() bool {
	return r != nil && len(r.providers) > 0
}

// Tools returns one page of qualified tools plus the cursor for the next page,
// empty when the page is the last. Ordering is by qualified name and is stable
// across calls, which is what makes the cursor meaningful.
func (r *Registry) Tools(ctx context.Context, cursor string, pageSize int) ([]Tool, string, error) {
	if r == nil {
		return nil, "", nil
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}

	all, err := r.allTools(ctx)
	if err != nil {
		return nil, "", err
	}

	start := 0
	if cursor != "" {
		after, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		// Names are sorted, so the next page starts at the first name after
		// the one the cursor recorded.
		start = sort.Search(len(all), func(i int) bool { return all[i].Name > after })
	}

	end := start + pageSize
	if end >= len(all) {
		return all[start:], "", nil
	}
	page := all[start:end]
	return page, encodeCursor(page[len(page)-1].Name), nil
}

// allTools collects every provider's tools, qualified and sorted. Providers are
// queried concurrently because a proxy provider's listing is a network call.
func (r *Registry) allTools(ctx context.Context) ([]Tool, error) {
	var (
		mu   sync.Mutex
		all  []Tool
		errs []error
		wg   sync.WaitGroup
	)

	for _, id := range r.ids {
		wg.Add(1)
		go func(id string, p Provider) {
			defer wg.Done()

			tools, err := p.Tools(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("provider %q: %w", id, err))
				return
			}
			for _, tool := range tools {
				local := tool.Name
				tool.Name = QualifyName(id, local)
				if err := ValidateName(tool.Name); err != nil {
					errs = append(errs, fmt.Errorf("provider %q: %w", id, err))
					return
				}
				// Qualification must invert, or Call could never route back to
				// this tool: it would be advertised and permanently
				// unreachable. An empty local name is the way that happens.
				if gotID, gotName, ok := SplitName(tool.Name); !ok || gotID != id || gotName != local {
					errs = append(errs, fmt.Errorf("provider %q: tool name %q does not round-trip", id, local))
					return
				}
				all = append(all, tool)
			}
		}(id, r.providers[id])
	}
	wg.Wait()

	if len(errs) > 0 {
		// Sorted so the message is deterministic regardless of goroutine order.
		sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
		return nil, errors.Join(errs...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// Call routes a qualified tool name to its provider. The bool reports whether
// the tool's provider exists; an unknown tool is a protocol error, unlike a
// tool that ran and could not answer.
func (r *Registry) Call(ctx context.Context, name string, args map[string]json.RawMessage) (Result, bool, error) {
	if r == nil {
		return Result{}, false, nil
	}

	providerID, local, ok := SplitName(name)
	if !ok {
		return Result{}, false, nil
	}
	provider, ok := r.providers[providerID]
	if !ok {
		return Result{}, false, nil
	}

	result, err := provider.Call(ctx, local, args)
	if err != nil {
		return Result{}, true, err
	}
	return result, true, nil
}

// Shutdown releases every provider's resources.
func (r *Registry) Shutdown(timeout time.Duration) error {
	if r == nil {
		return nil
	}

	var errs []error
	for _, id := range r.ids {
		if err := r.providers[id].Shutdown(timeout); err != nil {
			errs = append(errs, fmt.Errorf("provider %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Cursors are opaque to clients but are just the last name of the previous
// page. Encoding keeps clients from constructing one by hand and depending on
// the ordering staying what it is today.
func encodeCursor(lastName string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastName))
}

func decodeCursor(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvalidCursor
	}
	return string(raw), nil
}
