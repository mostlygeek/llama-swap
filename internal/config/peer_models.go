package config

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	DefaultPeerCatalogPath    = "/v1/models"
	DefaultPeerCatalogRefresh = 120
	DefaultPeerCatalogLimit   = 512
	MinPeerCatalogRefresh     = 10
)

// PeerCatalogConfig controls model discovery from a peer's model listing.
type PeerCatalogConfig struct {
	// Path is appended to the peer's proxy URL to fetch its model list.
	Path string `yaml:"path"`
	// Refresh is how often, in seconds, the catalog is fetched again.
	Refresh int `yaml:"refresh"`
	// Limit caps how many discovered models are kept from this peer.
	Limit int `yaml:"limit"`
	// Transitive imports entries the peer itself proxies from its own peers,
	// and its aliases. They are skipped by default.
	Transitive bool `yaml:"transitive"`
}

const (
	peerRuleAdd    = "add"
	peerRuleRemove = "remove"
)

// parsePeerModelEntry splits one models list entry. An entry that starts
// with "add" or "remove" followed by whitespace is a rule and the rest is
// its regex. Any other entry is a literal model ID and kind is empty.
func parsePeerModelEntry(entry string) (kind, value string) {
	entry = strings.TrimSpace(entry)
	for _, keyword := range []string{peerRuleAdd, peerRuleRemove} {
		rest, found := strings.CutPrefix(entry, keyword)
		if !found {
			continue
		}
		if rest == "" {
			return keyword, ""
		}
		if rest[0] == ' ' || rest[0] == '\t' {
			return keyword, strings.TrimSpace(rest)
		}
	}
	return "", entry
}

// normalizePeerModels validates the models list and enables the catalog when
// the list has add or remove rules or is omitted.
func normalizePeerModels(c *rawPeerConfig) error {
	if len(c.Models) == 0 {
		// An omitted models list discovers everything the peer lists.
		c.Models = []string{peerRuleAdd + " .*"}
		if c.Catalog == nil {
			c.Catalog = &PeerCatalogConfig{}
		}
	}

	hasAdd := false
	hasRules := false
	for i, entry := range c.Models {
		kind, value := parsePeerModelEntry(entry)
		switch kind {
		case "":
			if value == "" {
				return fmt.Errorf("models[%d]: model ID cannot be empty", i)
			}
			continue
		case peerRuleRemove:
			if !hasAdd {
				return fmt.Errorf("models[%d]: %q has nothing to remove, put an add rule before it", i, entry)
			}
		case peerRuleAdd:
			hasAdd = true
		}
		if value == "" {
			return fmt.Errorf("models[%d]: %q needs a regex after %s", i, entry, kind)
		}
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("models[%d]: invalid regex in %q: %w", i, entry, err)
		}
		hasRules = true
	}
	if hasRules && c.Catalog == nil {
		c.Catalog = &PeerCatalogConfig{}
	}

	if cat := c.Catalog; cat != nil {
		if cat.Path == "" {
			cat.Path = DefaultPeerCatalogPath
		}
		if !strings.HasPrefix(cat.Path, "/") {
			return fmt.Errorf("catalog.path must start with /")
		}
		if cat.Refresh == 0 {
			cat.Refresh = DefaultPeerCatalogRefresh
		}
		if cat.Refresh < MinPeerCatalogRefresh {
			return fmt.Errorf("catalog.refresh must be at least %d seconds", MinPeerCatalogRefresh)
		}
		if cat.Limit == 0 {
			cat.Limit = DefaultPeerCatalogLimit
		}
		if cat.Limit < 0 {
			return fmt.Errorf("catalog.limit cannot be negative")
		}
	}
	return nil
}

// Discovery reports whether the peer's models are discovered from its catalog.
func (c PeerConfig) Discovery() bool {
	return c.Catalog != nil
}

// LiteralModels returns the models list entries that name a model, leaving
// out add and remove rules. Literal models are always routable.
func (c PeerConfig) LiteralModels() []string {
	literals := make([]string, 0, len(c.Models))
	for _, entry := range c.Models {
		if kind, value := parsePeerModelEntry(entry); kind == "" && value != "" {
			literals = append(literals, value)
		}
	}
	return literals
}

// SelectCatalogModels applies the peer's models rules to the IDs in its
// catalog and returns the discovered model IDs, sorted, excluding literal
// models. Rules run top to bottom: add adds matching IDs and remove removes
// matching IDs added before it. Regexes match anywhere in the ID.
// Results beyond the catalog limit are dropped and counted in truncated.
func (c PeerConfig) SelectCatalogModels(ids []string) (selected []string, truncated int) {
	chosen := make(map[string]struct{})
	for _, entry := range c.Models {
		kind, pattern := parsePeerModelEntry(entry)
		if kind == "" || pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			// Validated at load; only hand-built configs reach this.
			continue
		}
		if kind == peerRuleAdd {
			for _, id := range ids {
				if re.MatchString(id) {
					chosen[id] = struct{}{}
				}
			}
		} else {
			for id := range chosen {
				if re.MatchString(id) {
					delete(chosen, id)
				}
			}
		}
	}
	for _, literal := range c.LiteralModels() {
		delete(chosen, literal)
	}

	selected = make([]string, 0, len(chosen))
	for id := range chosen {
		selected = append(selected, id)
	}
	sort.Strings(selected)
	if c.Catalog != nil && c.Catalog.Limit > 0 && len(selected) > c.Catalog.Limit {
		truncated = len(selected) - c.Catalog.Limit
		selected = selected[:c.Catalog.Limit]
	}
	return selected, truncated
}

// DiscoveredPeerModel is one model found in a peer's catalog.
type DiscoveredPeerModel struct {
	ID string
	// Record is the peer's /v1/models entry for the model. It may be nil.
	Record map[string]any
}

// PeerTarget is the peer and the peer's own model ID a name routes to.
type PeerTarget struct {
	PeerID  string
	ModelID string
}

// peerIndex is an immutable view of every routable peer model.
type peerIndex struct {
	discovered map[string][]DiscoveredPeerModel
	models     map[string][]string
	records    map[string]map[string]any
	routes     map[string]PeerTarget
}

// peerModels holds the current peer index. It is shared by every copy of the
// Config it was created for, so discovery updates reach all of them.
type peerModels struct {
	mu  sync.Mutex
	cfg Config
	cur atomic.Pointer[peerIndex]
}

func newPeerModels(cfg Config) *peerModels {
	pm := &peerModels{cfg: cfg}
	idx, _ := buildPeerIndex(&pm.cfg, nil)
	pm.cur.Store(idx)
	return pm
}

func (c *Config) currentPeerIndex() *peerIndex {
	if c.peerModels != nil {
		return c.peerModels.cur.Load()
	}
	// Configs built without LoadConfig (mostly tests) have no shared index.
	idx, _ := buildPeerIndex(c, nil)
	return idx
}

// buildPeerIndex indexes literal peer models plus discovered ones. Discovered
// models whose fully qualified name collides with a local model, alias,
// selector, profile pin or another peer model are dropped and returned.
func buildPeerIndex(c *Config, discovered map[string][]DiscoveredPeerModel) (*peerIndex, []PeerTarget) {
	idx := &peerIndex{
		discovered: make(map[string][]DiscoveredPeerModel),
		models:     make(map[string][]string),
		records:    make(map[string]map[string]any),
		routes:     make(map[string]PeerTarget),
	}

	peerIDs := make([]string, 0, len(c.Peers))
	for peerID := range c.Peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	reserved := func(fqn string) bool {
		if _, ok := c.Models[fqn]; ok {
			return true
		}
		if _, ok := c.aliases[fqn]; ok {
			return true
		}
		for _, model := range c.Models {
			for _, alias := range model.Aliases {
				if alias == fqn {
					return true
				}
			}
		}
		if _, ok := c.Selectors[fqn]; ok {
			return true
		}
		for _, profile := range c.Profiles {
			if _, ok := profile.Pins[fqn]; ok {
				return true
			}
		}
		return false
	}

	seen := make(map[string]map[string]struct{}, len(peerIDs))
	add := func(peerID, modelID string) {
		seen[peerID][modelID] = struct{}{}
		idx.models[peerID] = append(idx.models[peerID], modelID)
		idx.routes[PeerModelFQN(peerID, modelID)] = PeerTarget{PeerID: peerID, ModelID: modelID}
	}

	// Literal models were validated at load and always win.
	for _, peerID := range peerIDs {
		seen[peerID] = make(map[string]struct{})
		for _, modelID := range c.Peers[peerID].LiteralModels() {
			if _, dup := seen[peerID][modelID]; !dup {
				add(peerID, modelID)
			}
		}
	}

	var dropped []PeerTarget
	for _, peerID := range peerIDs {
		for _, model := range discovered[peerID] {
			fqn := PeerModelFQN(peerID, model.ID)
			if _, literal := seen[peerID][model.ID]; literal {
				if model.Record != nil {
					idx.records[fqn] = model.Record
				}
				continue
			}
			if _, taken := idx.routes[fqn]; taken || reserved(fqn) {
				dropped = append(dropped, PeerTarget{PeerID: peerID, ModelID: model.ID})
				continue
			}
			add(peerID, model.ID)
			idx.discovered[peerID] = append(idx.discovered[peerID], model)
			if model.Record != nil {
				idx.records[fqn] = model.Record
			}
		}
	}

	// An unqualified name routes only when exactly one peer provides it and
	// it is not itself a fully qualified name.
	bare := make(map[string][]PeerTarget)
	for _, peerID := range peerIDs {
		for _, modelID := range idx.models[peerID] {
			bare[modelID] = append(bare[modelID], PeerTarget{PeerID: peerID, ModelID: modelID})
		}
	}
	for modelID, targets := range bare {
		if len(targets) != 1 {
			continue
		}
		if _, isFQN := idx.routes[modelID]; isFQN {
			continue
		}
		idx.routes[modelID] = targets[0]
	}

	return idx, dropped
}

// PeerModelIDs returns every model the peer currently provides: its literal
// models followed by discovered models.
func (c *Config) PeerModelIDs(peerID string) []string {
	return c.currentPeerIndex().models[peerID]
}

// PeerModelRecord returns the peer's /v1/models entry for one of its models,
// when the model was seen in the peer's catalog.
func (c *Config) PeerModelRecord(peerID, modelID string) (map[string]any, bool) {
	record, ok := c.currentPeerIndex().records[PeerModelFQN(peerID, modelID)]
	return record, ok
}

// SetDiscoveredPeerModels replaces the models discovered for a peer. It
// reports the fully qualified names dropped because they collide with another
// name, and whether the set of models or their records changed.
func (c *Config) SetDiscoveredPeerModels(peerID string, models []DiscoveredPeerModel) (dropped []string, changed bool) {
	pm := c.peerModels
	if pm == nil {
		return nil, false
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()

	cur := pm.cur.Load()
	discovered := make(map[string][]DiscoveredPeerModel, len(cur.discovered)+1)
	for id, list := range cur.discovered {
		discovered[id] = list
	}
	discovered[peerID] = models

	next, all := buildPeerIndex(&pm.cfg, discovered)
	changed = !reflect.DeepEqual(cur.models, next.models) || !reflect.DeepEqual(cur.records, next.records)
	pm.cur.Store(next)

	for _, target := range all {
		if target.PeerID == peerID {
			dropped = append(dropped, PeerModelFQN(target.PeerID, target.ModelID))
		}
	}
	return dropped, changed
}

// DiscoveryPeerFor reports whether name has the form <peerID>/<model> for a
// peer that discovers its models. Such names cannot be checked when the
// config loads, so they are accepted and resolved when requested.
func (c *Config) DiscoveryPeerFor(name string) (peerID, modelID string, ok bool) {
	for id, peer := range c.Peers {
		if !peer.Discovery() {
			continue
		}
		if rest, found := strings.CutPrefix(name, id+"/"); found && rest != "" {
			if len(id) > len(peerID) {
				peerID, modelID, ok = id, rest, true
			}
		}
	}
	return peerID, modelID, ok
}
