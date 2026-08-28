package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// identityMapPaths is the set of dotted paths whose direct children are
// identity-keyed maps. A child key present in two sources is a hard error;
// such keys name discrete entities (a model, a group, a peer, etc.) and a
// duplicate means the user has split one entity across files by mistake.
var identityMapPaths = map[string]bool{
	"models":                         true,
	"groups":                         true,
	"profiles":                       true,
	"selectors":                      true,
	"peers":                          true,
	"matrix":                         true,
	"routing.router.settings.groups": true,
	"routing.router.settings.matrix": true,
}

// LoadConfigSources loads and merges configuration from -config (optional)
// and -config-dir (optional). At least one must be provided. The -config file
// is loaded first; *.yml/*.yaml files directly under -config-dir are then
// merged in sorted filename order. The merged document is passed through the
// existing LoadConfigFromReader pipeline unchanged.
func LoadConfigSources(configPath, configDir string) (Config, error) {
	if configPath == "" && configDir == "" {
		return Config{}, fmt.Errorf("at least one of -config or -config-dir must be provided")
	}

	var sourcePaths []string

	if configPath != "" {
		sourcePaths = append(sourcePaths, configPath)
	}

	if configDir != "" {
		dirFiles, err := listYAMLFiles(configDir)
		if err != nil {
			return Config{}, fmt.Errorf("-config-dir %s: %w", configDir, err)
		}

		if configPath != "" {
			absConfig, err := filepath.Abs(configPath)
			if err != nil {
				return Config{}, fmt.Errorf("failed to resolve -config path: %w", err)
			}
			for _, f := range dirFiles {
				absF, err := filepath.Abs(f)
				if err != nil {
					return Config{}, fmt.Errorf("failed to resolve config dir file %s: %w", f, err)
				}
				if absConfig == absF {
					return Config{}, fmt.Errorf("-config path %s is also present in -config-dir %s; remove it from one", configPath, configDir)
				}
			}
		}

		sourcePaths = append(sourcePaths, dirFiles...)
	}

	if len(sourcePaths) == 0 {
		return Config{}, fmt.Errorf("no configuration sources found")
	}

	var merged *yaml.Node
	for _, p := range sourcePaths {
		node, err := parseSource(p)
		if err != nil {
			return Config{}, err
		}
		if node == nil {
			continue // empty file
		}
		if merged == nil {
			merged = node
			continue
		}
		if err := mergeNodes(merged, node, "", p); err != nil {
			return Config{}, err
		}
	}

	if merged == nil {
		// All sources were empty; run the pipeline on empty input so defaults
		// and validation still apply (e.g. startPort, performance defaults).
		return LoadConfigFromReader(strings.NewReader(""))
	}

	out, err := yaml.Marshal(merged)
	if err != nil {
		return Config{}, fmt.Errorf("failed to marshal merged config: %w", err)
	}
	return LoadConfigFromReader(strings.NewReader(string(out)))
}

// listYAMLFiles returns the top-level *.yml and *.yaml files in dir, sorted by
// filename for deterministic merge order. Subdirectories are not traversed.
func listYAMLFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}

// parseSource reads and parses one YAML config file into a root mapping node.
// Returns a nil node (no error) when the file is empty or contains only
// comments.
//
// Env macros (${env.VAR}) are substituted at the string level before YAML
// parsing so that flow-style constructs like [${env.API_KEY}] parse
// correctly — the brace would otherwise be interpreted as a flow mapping.
func parseSource(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	yamlStr, err := substituteEnvMacros(string(data))
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlStr), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	// yaml.Unmarshal into a yaml.Node yields a DocumentNode whose Content[0]
	// is the actual root. Unwrap it so callers see the real top-level node.
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind == 0 || root.Content == nil {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %s: top-level YAML must be a mapping", path)
	}
	return root, nil
}

// mergeNodes merges src into dst (both MappingNodes) in place. Keys present in
// only one side are kept; shared keys are merged recursively under the rules
// in mergeValue. srcPath is included in error messages to identify the file
// that introduced the conflict.
func mergeNodes(dst, src *yaml.Node, path, srcPath string) error {
	srcIdx := indexMapping(src)

	// First pass: merge shared keys in place.
	for i := 0; i+1 < len(dst.Content); i += 2 {
		keyNode := dst.Content[i]
		dstVal := dst.Content[i+1]
		key := keyNode.Value

		srcVal, ok := srcIdx[key]
		if !ok {
			continue // dst-only key, keep as-is
		}

		childPath := joinPath(path, key)

		if identityMapPaths[childPath] {
			// Identity-keyed map: each child key names a discrete entity
			// (a model, group, peer, ...). A shared child key is a hard
			// error; src-only children are appended in the second pass.
			if err := mergeIdentityMap(dstVal, srcVal, childPath, key, srcPath); err != nil {
				return err
			}
			continue
		}

		if err := mergeValue(dstVal, srcVal, childPath, srcPath); err != nil {
			return err
		}
	}

	// Second pass: append src-only keys.
	dstIdx := indexMapping(dst)
	for i := 0; i+1 < len(src.Content); i += 2 {
		keyNode := src.Content[i]
		srcVal := src.Content[i+1]
		key := keyNode.Value

		if _, ok := dstIdx[key]; ok {
			continue // already merged above
		}
		keyCopy := *keyNode
		valCopy := *srcVal
		dst.Content = append(dst.Content, &keyCopy, &valCopy)
	}

	return nil
}

// mergeIdentityMap merges two identity-keyed mapping nodes (e.g. `models`,
// `groups`, `peers`). Any child key present in both sides is a duplicate
// entity and produces an error naming the conflicting key and source file.
// src-only keys are appended to dst.
func mergeIdentityMap(dst, src *yaml.Node, path, mapName, srcPath string) error {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return fmt.Errorf("conflict at %q: expected a mapping, introduced by %s", path, srcPath)
	}
	dstIdx := indexMapping(dst)
	for i := 0; i+1 < len(src.Content); i += 2 {
		keyNode := src.Content[i]
		srcVal := src.Content[i+1]
		key := keyNode.Value
		if _, dup := dstIdx[key]; dup {
			return fmt.Errorf("duplicate %s %q found in %s (already defined in another config source)", mapName, key, srcPath)
		}
		keyCopy := *keyNode
		valCopy := *srcVal
		dst.Content = append(dst.Content, &keyCopy, &valCopy)
	}
	return nil
}

// mergeValue merges srcVal into dstVal (both pointing into the parent's
// Content slice). Mapping+Mapping recurses; Sequence+Sequence concatenates;
// Scalar+Scalar errors on value mismatch; null on either side yields to the
// non-null side.
func mergeValue(dstVal, srcVal *yaml.Node, path, srcPath string) error {
	switch {
	case dstVal.Kind == yaml.MappingNode && srcVal.Kind == yaml.MappingNode:
		return mergeNodes(dstVal, srcVal, path, srcPath)

	case dstVal.Kind == yaml.SequenceNode && srcVal.Kind == yaml.SequenceNode:
		dstVal.Content = append(dstVal.Content, srcVal.Content...)
		return nil

	case dstVal.Kind == yaml.ScalarNode && srcVal.Kind == yaml.ScalarNode:
		if isNullScalar(dstVal) {
			*dstVal = *srcVal
			return nil
		}
		if isNullScalar(srcVal) {
			return nil
		}
		if dstVal.Value == srcVal.Value && dstVal.Tag == srcVal.Tag {
			return nil
		}
		return fmt.Errorf("conflict at %q: %s sets a different value than a previous source", path, srcPath)

	case isNull(dstVal):
		*dstVal = *srcVal
		return nil

	case isNull(srcVal):
		return nil

	default:
		return fmt.Errorf("conflict at %q: incompatible YAML node kinds (kind %d vs %d) introduced by %s", path, dstVal.Kind, srcVal.Kind, srcPath)
	}
}

// isNull reports whether n represents a YAML null (empty or !!null).
func isNull(n *yaml.Node) bool {
	if n == nil || n.Kind == 0 {
		return true
	}
	return isNullScalar(n)
}

func isNullScalar(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Tag == "") && n.Value == ""
}

// indexMapping builds a key -> value-node index for a mapping node.
func indexMapping(n *yaml.Node) map[string]*yaml.Node {
	idx := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		idx[n.Content[i].Value] = n.Content[i+1]
	}
	return idx
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
