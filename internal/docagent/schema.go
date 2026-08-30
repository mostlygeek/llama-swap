package docagent

import (
	"encoding/json"
	"sort"
	"strings"
)

// SchemaProp is one direct child of a schema node. Only names, types and
// descriptions are carried: expanding further is what blows up a small model's
// context window, so the model drills down with another call instead.
type SchemaProp struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// SchemaFragment is a single node of config-schema.json, flattened for display.
type SchemaFragment struct {
	Path        string          `json:"path"`
	Type        string          `json:"type,omitempty"`
	Description string          `json:"description,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Enum        json.RawMessage `json:"enum,omitempty"`
	Required    []string        `json:"required,omitempty"`
	Properties  []SchemaProp    `json:"properties,omitempty"`
	MapKeys     bool            `json:"map_keys,omitempty"` // children are addressed with "*"
}

// SchemaFragment resolves a dotted path into config-schema.json.
//
// Path grammar: dot separated, with "*" descending through a map's
// additionalProperties (or an array's items). An empty path returns the root.
// For example "models.*.filters" reaches a model's filters block.
func (d *Docs) SchemaFragment(path string) (SchemaFragment, bool) {
	if d == nil || d.schema == nil {
		return SchemaFragment{}, false
	}

	node := d.resolveRef(d.schema)
	path = strings.Trim(strings.TrimSpace(path), ".")

	if path != "" {
		for _, part := range strings.Split(path, ".") {
			next, ok := d.descend(node, part)
			if !ok {
				return SchemaFragment{}, false
			}
			node = next
		}
	}

	return d.renderFragment(path, node), true
}

// SchemaPaths lists the addressable child paths under prefix. Used to build a
// helpful message when a lookup misses.
func (d *Docs) SchemaPaths(prefix string) []string {
	fragment, ok := d.SchemaFragment(prefix)
	if !ok {
		if fragment, ok = d.SchemaFragment(""); !ok {
			return nil
		}
		prefix = ""
	}

	base := ""
	if prefix != "" {
		base = prefix + "."
	}

	if fragment.MapKeys {
		return []string{base + "*"}
	}

	out := make([]string, 0, len(fragment.Properties))
	for _, prop := range fragment.Properties {
		out = append(out, base+prop.Name)
	}
	sort.Strings(out)
	return out
}

func (d *Docs) descend(node map[string]any, part string) (map[string]any, bool) {
	if part == "*" {
		if child, ok := node["additionalProperties"].(map[string]any); ok {
			return d.resolveRef(child), true
		}
		if child, ok := node["items"].(map[string]any); ok {
			return d.resolveRef(child), true
		}
		return nil, false
	}

	props, ok := node["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	child, ok := props[part].(map[string]any)
	if !ok {
		return nil, false
	}
	return d.resolveRef(child), true
}

// resolveRef follows "#/definitions/<name>" indirection. The schema uses it for
// macros, timeouts, groupsConfig and matrixConfig.
func (d *Docs) resolveRef(node map[string]any) map[string]any {
	for i := 0; i < 8; i++ {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		name := ref[strings.LastIndexByte(ref, '/')+1:]
		target, ok := d.schemaDefs[name].(map[string]any)
		if !ok {
			return node
		}
		node = target
	}
	return node
}

func (d *Docs) renderFragment(path string, node map[string]any) SchemaFragment {
	fragment := SchemaFragment{Path: path}

	if v, ok := node["type"].(string); ok {
		fragment.Type = v
	}
	if v, ok := node["description"].(string); ok {
		fragment.Description = v
	}
	if v, ok := node["default"]; ok {
		if raw, err := json.Marshal(v); err == nil {
			fragment.Default = raw
		}
	}
	if v, ok := node["enum"]; ok {
		if raw, err := json.Marshal(v); err == nil {
			fragment.Enum = raw
		}
	}

	required := map[string]bool{}
	if list, ok := node["required"].([]any); ok {
		for _, item := range list {
			if name, ok := item.(string); ok {
				required[name] = true
				fragment.Required = append(fragment.Required, name)
			}
		}
		sort.Strings(fragment.Required)
	}

	if props, ok := node["properties"].(map[string]any); ok {
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			child, ok := props[name].(map[string]any)
			if !ok {
				continue
			}
			child = d.resolveRef(child)
			prop := SchemaProp{Name: name, Required: required[name]}
			if v, ok := child["type"].(string); ok {
				prop.Type = v
			}
			if v, ok := child["description"].(string); ok {
				prop.Description = truncateSummary(firstLine(v), maxSummaryLen)
			}
			fragment.Properties = append(fragment.Properties, prop)
		}
	} else if _, ok := node["additionalProperties"].(map[string]any); ok {
		fragment.MapKeys = true
	} else if _, ok := node["items"].(map[string]any); ok {
		fragment.MapKeys = true
	}

	return fragment
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
