package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/mostlygeek/llama-swap/internal/mcptools"
	"gopkg.in/yaml.v3"
)

// maxConfigResultBytes bounds one get_config result. The caller chooses what
// comes back by writing a jq query, so this cap is a backstop against a query
// that asks for more than a context window can hold. When it trips the caller
// is told to narrow the query rather than handed a document that stops
// mid-value.
const maxConfigResultBytes = 32 * 1024

// configQueryTimeout bounds evaluation. jq is a real language and the queries
// come from a model, so a non-terminating expression is a question of when,
// not if.
const configQueryTimeout = 5 * time.Second

// errConfigResultTooLarge signals that rendering stopped because the result
// passed maxConfigResultBytes.
var errConfigResultTooLarge = errors.New("config query result is too large")

// ConfigProvider serves the running llama-swap configuration so an agent can
// give advice about the setup in front of it rather than in the abstract.
//
// The configuration is captured by value when the provider is built. A hot
// reload constructs a fresh Server and therefore a fresh registry, so the
// snapshot always matches what is actually running.
type ConfigProvider struct {
	cfg Config
}

// NewConfigProvider builds the provider around a configuration snapshot.
func NewConfigProvider(cfg Config) *ConfigProvider {
	return &ConfigProvider{cfg: cfg}
}

var _ mcptools.Provider = (*ConfigProvider)(nil)

func (p *ConfigProvider) ID() string { return "config" }

func (p *ConfigProvider) Shutdown(time.Duration) error { return nil }

// Reading the configuration is read-only. It is not idempotent across a hot
// reload, but within one process the snapshot never changes.
func configAnnotations() *mcptools.Annotations {
	return &mcptools.Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

func (p *ConfigProvider) Tools(context.Context) ([]mcptools.Tool, error) {
	return []mcptools.Tool{
		{
			Name:  "get_config",
			Title: "Query the current llama-swap configuration",
			Description: "Query the configuration llama-swap is running right now with a jq expression. " +
				"The result is YAML, with API keys and other credentials redacted. Values are fully " +
				"resolved: ${env.*} and macro references are already expanded, and values equal to " +
				"their defaults are omitted. Pass \"query\" to select exactly the part you need, so " +
				"nothing has to be truncated; omit it for the whole document. For example: " +
				"\".models | keys\" lists the configured model ids, \".models.qwen3\" shows one model, " +
				"\".models | to_entries | map({id: .key, ttl: .value.ttl})\" pulls one field from every " +
				"model, and \".peers\" shows the peers. Quote any id that is not a plain word: " +
				"\".models[\"qwen3-8b\"]\". Use this to review or troubleshoot the active setup before " +
				"suggesting changes.",
			Annotations: configAnnotations(),
			Tags:        []string{"config", "configuration", "troubleshooting"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type": "string",
						"description": "jq expression selecting part of the config, for example " +
							"'.models | keys', '.models.qwen3', '.models[\"qwen3-8b\"].ttl', or '.macros'. " +
							"Defaults to '.', the whole config.",
					},
				},
				"additionalProperties": false,
			},
		},
	}, nil
}

func (p *ConfigProvider) Call(ctx context.Context, name string, args map[string]json.RawMessage) (mcptools.Result, error) {
	if name != "get_config" {
		return mcptools.Result{}, fmt.Errorf("unknown tool %q", name)
	}

	query := mcptools.StringArg(args, "query")
	if query == "" {
		// get_config took a dotted "path" before it took a jq query. Name the
		// equivalent expression instead of silently returning the whole config,
		// which is the answer a caller with the old argument least expects.
		if path := mcptools.StringArg(args, "path"); path != "" {
			return mcptools.Result{IsError: true, Content: fmt.Sprintf(
				"Error: get_config takes a jq \"query\", not a \"path\". Call it again with query=%s",
				jqPathQuery(path))}, nil
		}
		query = "."
	}

	code, err := compileConfigQuery(query)
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"Error: %s is not a valid jq query: %v. An id that is not a plain word has to be "+
				"quoted, as in \".models[\"qwen3-8b\"]\"; \".models.qwen3-8b\" is a subtraction to jq. "+
				"Use \".\" for the whole config, or \".models | keys\" for the model ids.",
			query, err)}, nil
	}

	document, err := p.queryDocument()
	if err != nil {
		return mcptools.Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, configQueryTimeout)
	defer cancel()

	out, err := runConfigQuery(ctx, code, document, maxConfigResultBytes)
	switch {
	case errors.Is(err, errConfigResultTooLarge):
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"Error: the result of %s is larger than %dKB. Ask for less rather than everything: "+
				"\".models | keys\" lists the model ids, \".models.<id>\" reads one model, and "+
				"\".models | to_entries | map({id: .key, ttl: .value.ttl})\" pulls one field from all "+
				"of them. %s", query, maxConfigResultBytes/1024, p.topLevelKeyHint())}, nil
	case errors.Is(err, context.DeadlineExceeded):
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"Error: the query %s did not finish within %s. Simplify it.", query, configQueryTimeout)}, nil
	case err != nil:
		return mcptools.Result{IsError: true, Content: fmt.Sprintf(
			"Error: the query %s failed: %v", query, err)}, nil
	}

	if out.results == 0 {
		return mcptools.Result{Content: fmt.Sprintf(
			"The query %s matched nothing. %s", query, p.topLevelKeyHint())}, nil
	}

	var b strings.Builder
	if query == "." {
		b.WriteString("Current llama-swap configuration (credentials redacted, values resolved):\n\n")
	} else {
		fmt.Fprintf(&b, "Current llama-swap configuration, jq query %s (credentials redacted, values resolved):\n\n", query)
	}
	b.WriteString("```yaml\n")
	b.WriteString(strings.TrimRight(out.body, "\n"))
	b.WriteString("\n```\n")
	if out.nullOnly {
		// jq answers a key that is not there with null, which reads like a
		// value. Say what it means, since an omitted default looks the same.
		fmt.Fprintf(&b, "\nnull means nothing is set there: either the key does not exist, "+
			"or its value is the default and defaults are omitted. %s\n", p.topLevelKeyHint())
	}
	return mcptools.Result{Content: b.String()}, nil
}

// configQueryOutput is what one evaluation produced: the results rendered as
// YAML documents, how many there were, and whether every one of them was null.
type configQueryOutput struct {
	body     string
	results  int
	nullOnly bool
}

// runConfigQuery evaluates code over document and renders each result as a YAML
// document. It gives up as soon as the rendered output passes limit, so an
// expression like `range(1e9)` cannot build a huge string in memory.
func runConfigQuery(ctx context.Context, code *gojq.Code, document any, limit int) (configQueryOutput, error) {
	out := configQueryOutput{nullOnly: true}

	var b strings.Builder
	iter := code.RunWithContext(ctx, document)
	for {
		value, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := value.(error); isErr {
			return configQueryOutput{}, err
		}

		buf, err := yaml.Marshal(value)
		if err != nil {
			return configQueryOutput{}, fmt.Errorf("rendering result as YAML: %w", err)
		}
		if out.results > 0 {
			// Several results are several YAML documents, which is what the
			// document separator is for.
			b.WriteString("---\n")
		}
		b.Write(buf)

		out.results++
		if value != nil {
			out.nullOnly = false
		}
		if b.Len() > limit {
			return configQueryOutput{}, errConfigResultTooLarge
		}
	}

	out.body = b.String()
	return out, nil
}

// compileConfigQuery parses and compiles one jq expression. gojq keeps the
// process environment out of reach by default; the loader is passed explicitly
// because `env` would otherwise hand back exactly the credentials that
// redaction removes from the config.
func compileConfigQuery(query string) (*gojq.Code, error) {
	parsed, err := gojq.Parse(query)
	if err != nil {
		return nil, err
	}
	return gojq.Compile(parsed, gojq.WithEnvironLoader(func() []string { return nil }))
}

// queryDocument renders the effective configuration as the plain JSON-shaped
// values gojq evaluates over.
func (p *ConfigProvider) queryDocument() (any, error) {
	yamlText, found, err := p.cfg.RedactedYAML("")
	if err != nil {
		return nil, fmt.Errorf("rendering config: %w", err)
	}
	if !found { // RedactedYAML for the root always succeeds; retain this guard.
		return nil, fmt.Errorf("rendering config: root configuration was not found")
	}

	var current map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &current); err != nil {
		return nil, fmt.Errorf("parsing rendered config: %w", err)
	}

	defaults, err := configProviderDefaults(p.cfg)
	if err != nil {
		return nil, fmt.Errorf("building config defaults: %w", err)
	}
	filtered, empty := pruneConfigDefaults(current, defaults, "")
	if empty {
		filtered = map[string]any{}
	}

	// A round trip through JSON is what makes the tree safe to hand to gojq: it
	// flattens the YAML-only types (timestamps, for one) that gojq cannot walk.
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, fmt.Errorf("encoding config for the query: %w", err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decoding config for the query: %w", err)
	}
	return normalizeQueryNumbers(document), nil
}

// normalizeQueryNumbers turns whole float64 values back into ints. JSON decoding
// makes every number a float64, and gojq passes that straight through, so
// without this a port renders as 8.08e+03.
func normalizeQueryNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeQueryNumbers(child)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = normalizeQueryNumbers(child)
		}
		return typed
	case float64:
		if typed == math.Trunc(typed) && math.Abs(typed) < 1<<53 {
			return int(typed)
		}
		return typed
	default:
		return value
	}
}

// topLevelKeyHint names the sections a query can start from, so a caller that
// asked for something that is not there can correct itself in one turn.
func (p *ConfigProvider) topLevelKeyHint() string {
	keys := p.cfg.ConfigTopLevelKeys()
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return "Top-level keys: " + strings.Join(keys, ", ") + "."
}

// bareJQKey matches the field names jq accepts after a dot without quoting.
var bareJQKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// jqPathQuery renders a dotted config path as the jq expression selecting the
// same subtree, quoting the segments jq would not take bare.
func jqPathQuery(path string) string {
	var b strings.Builder
	for _, segment := range strings.Split(path, ".") {
		if bareJQKey.MatchString(segment) {
			b.WriteString("." + segment)
		} else {
			fmt.Fprintf(&b, "[%q]", segment)
		}
	}
	if b.Len() == 0 {
		return "."
	}
	return b.String()
}

// configProviderDefaults returns rendered defaults in the same YAML shape as
// RedactedYAML. Defaults for entries in maps (models, peers, selectors, and
// groups) are added by pruneConfigDefaults because an empty config has none.
func configProviderDefaults(cfg Config) (map[string]any, error) {
	base, err := LoadConfigFromReader(strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	yamlText, _, err := base.RedactedYAML("")
	if err != nil {
		return nil, err
	}
	var defaults map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &defaults); err != nil {
		return nil, err
	}

	// Some model defaults inherit the resolved global configuration, so use the
	// active values rather than the empty-config baseline.
	model := ModelConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &model); err != nil {
		return nil, err
	}
	model.HealthCheckTimeout = cfg.HealthCheckTimeout
	model.UnloadAfter = cfg.GlobalTTL
	model.UnloadTimeout = cfg.UnloadTimeout
	sendLoadingState := cfg.SendLoadingState
	model.SendLoadingState = &sendLoadingState
	modelDefaults, err := configProviderYAMLMap(model)
	if err != nil {
		return nil, err
	}
	defaults["__model"] = modelDefaults

	// PeerConfig validates its required fields during unmarshaling. Supply
	// placeholders solely to obtain the defaults, then remove those placeholders.
	peer := PeerConfig{}
	if err := yaml.Unmarshal([]byte("proxy: http://default.invalid\nmodels: [default]\n"), &peer); err != nil {
		return nil, err
	}
	peerDefaults, err := configProviderYAMLMap(peer)
	if err != nil {
		return nil, err
	}
	delete(peerDefaults, "proxy")
	delete(peerDefaults, "models")
	defaults["__peer"] = peerDefaults

	selector := SelectorConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &selector); err != nil {
		return nil, err
	}
	selectorDefaults, err := configProviderYAMLMap(selector)
	if err != nil {
		return nil, err
	}
	defaults["__selector"] = selectorDefaults

	group := GroupConfig{}
	if err := yaml.Unmarshal([]byte("{}"), &group); err != nil {
		return nil, err
	}
	groupDefaults, err := configProviderYAMLMap(group)
	if err != nil {
		return nil, err
	}
	defaults["__group"] = groupDefaults
	return defaults, nil
}

func configProviderYAMLMap(v any) (map[string]any, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// pruneConfigDefaults recursively drops settings equal to their defaults.
// Map-entry defaults need special handling because the empty base config has no
// representative model, peer, selector, or group entry to compare against.
func pruneConfigDefaults(value, defaults any, path string) (any, bool) {
	currentMap, isMap := value.(map[string]any)
	defaultMap, defaultsAreMap := defaults.(map[string]any)
	if !isMap || !defaultsAreMap {
		return value, reflect.DeepEqual(value, defaults)
	}

	out := make(map[string]any, len(currentMap))
	for key, current := range currentMap {
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}

		childDefaults, known := defaultMap[key]
		switch childPath {
		case "models", "peers", "selectors", "groups", "routing.router.settings.groups":
			entryDefaults := defaultMap["__"+strings.TrimSuffix(childPath, "s")]
			if childPath == "routing.router.settings.groups" {
				// The recursive defaults at this path are the routing defaults,
				// not the root defaults where __group is stored.
				entryDefaults = map[string]any{
					"swap":       true,
					"exclusive":  true,
					"persistent": false,
					"members":    []any{},
				}
			}
			entries, ok := current.(map[string]any)
			if !ok {
				out[key] = current
				continue
			}
			prunedEntries := make(map[string]any, len(entries))
			for entryKey, entry := range entries {
				if pruned, empty := pruneConfigDefaults(entry, entryDefaults, childPath+"."+entryKey); !empty {
					prunedEntries[entryKey] = pruned
				}
			}
			if len(prunedEntries) > 0 {
				out[key] = prunedEntries
			}
			continue
		}

		if !known {
			out[key] = current
			continue
		}
		if pruned, empty := pruneConfigDefaults(current, childDefaults, childPath); !empty {
			out[key] = pruned
		}
	}
	return out, len(out) == 0
}
