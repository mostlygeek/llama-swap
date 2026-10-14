package spl

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Library holds named policies that programs reference with apply. It is
// immutable after construction and safe for concurrent use.
type Library struct {
	policies map[string]*Program
}

// NewLibrary parses every policy, checks that apply references resolve, that
// no program writes a protected body parameter, and that the apply graph has
// no cycles. Errors name the policy: "policies.<name>: line L:C: ...".
func NewLibrary(policies map[string]string, protected []string) (*Library, error) {
	lib := &Library{policies: make(map[string]*Program, len(policies))}

	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prog, err := Parse(policies[name])
		if err != nil {
			return nil, fmt.Errorf("policies.%s: %w", name, err)
		}
		lib.policies[name] = prog
	}

	for _, name := range names {
		if err := lib.policies[name].Validate(lib, protected); err != nil {
			return nil, fmt.Errorf("policies.%s: %w", name, err)
		}
	}

	if cycle := lib.findCycle(names); cycle != nil {
		return nil, fmt.Errorf("policies: cycle detected: %s", strings.Join(cycle, " -> "))
	}
	return lib, nil
}

// Lookup returns the named policy.
func (l *Library) Lookup(name string) (*Program, bool) {
	if l == nil {
		return nil, false
	}
	prog, ok := l.policies[name]
	return prog, ok
}

// Names returns the policy names in sorted order.
func (l *Library) Names() []string {
	if l == nil {
		return nil
	}
	names := make([]string, 0, len(l.policies))
	for name := range l.policies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// findCycle walks the apply graph depth-first and returns the first cycle
// found as a path (a -> b -> a), or nil.
func (l *Library) findCycle(names []string) []string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(names))
	var stack []string

	var visit func(name string) []string
	visit = func(name string) []string {
		state[name] = visiting
		stack = append(stack, name)
		for _, dep := range l.policies[name].applies {
			switch state[dep] {
			case visiting:
				start := slices.Index(stack, dep)
				cycle := append([]string{}, stack[start:]...)
				return append(cycle, dep)
			case unvisited:
				if cycle := visit(dep); cycle != nil {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
		return nil
	}

	for _, name := range names {
		if state[name] == unvisited {
			if cycle := visit(name); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// Validate checks a program against a library: every apply must name a policy
// in lib (a nil lib means no apply is allowed) and no default, set or remove
// may target a protected body parameter.
func (p *Program) Validate(lib *Library, protected []string) error {
	for i := range p.clauses {
		c := &p.clauses[i]
		switch c.action {
		case actApply:
			if _, ok := lib.Lookup(c.policy); !ok {
				return errorAt(c.line, c.col, "apply references unknown policy %q", c.policy)
			}
		case actDefault, actSet, actRemove:
			if key := c.path.firstKey(); key != "" && slices.Contains(protected, key) {
				verb := "set"
				if c.action == actRemove {
					verb = "remove"
				}
				return errorAt(c.path.line, c.path.col, "cannot %s protected parameter %q", verb, key)
			}
		}
	}
	return nil
}
