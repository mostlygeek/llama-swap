package reference

import (
	"sort"
	"strings"
)

// SearchHit is one match, always carrying the doc id so the model's obvious
// next move is a get_doc call.
type SearchHit struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Line    int    `json:"line"` // 1-based, within the doc body
	Snippet string `json:"snippet"`
	Score   int    `json:"score"`
}

// SearchOptions bounds a search. Zero values use the defaults below.
type SearchOptions struct {
	MaxHits      int
	ContextLines int
}

const (
	defaultMaxHits      = 5
	maxSearchHits       = 15
	defaultContextLines = 2
	maxContextLines     = 5
	maxSnippetLineWidth = 200
	minTokenLen         = 2
)

// Search scores every indexed line against the query tokens and returns the
// best matches with surrounding context. Ordering is fully deterministic so
// results are testable.
func (d *Docs) Search(query string, opts SearchOptions) []SearchHit {
	if !d.Enabled() {
		return nil
	}

	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}

	maxHits := clamp(opts.MaxHits, defaultMaxHits, 1, maxSearchHits)
	context := clamp(opts.ContextLines, defaultContextLines, 0, maxContextLines)

	type scored struct {
		doc  *doc
		line int
		hits int
	}
	var candidates []scored

	for _, dc := range d.ordered {
		titleTags := strings.ToLower(dc.meta.Title + " " + strings.Join(dc.meta.Tags, " "))
		metaBonus := 0
		for _, tok := range tokens {
			if strings.Contains(titleTags, tok) {
				metaBonus += 3
			}
		}

		matchedBody := false
		for i, line := range dc.lines {
			lower := strings.ToLower(line)
			matched := 0
			for _, tok := range tokens {
				if strings.Contains(lower, tok) {
					matched++
				}
			}
			if matched == 0 {
				continue
			}

			score := matched*4 + metaBonus
			trimmed := strings.TrimSpace(line)
			// A key line or a heading is far more likely to be the definition
			// the model wants than a passing mention in prose.
			if strings.HasPrefix(trimmed, "#") || isYAMLKeyLine(trimmed) {
				score += 2
			}
			if dc.source == configExampleFile {
				score++
			}

			matchedBody = true
			candidates = append(candidates, scored{doc: dc, line: i, hits: score})
		}

		// A doc whose title or tags match but whose body never mentions the
		// term still deserves a hit -- searching for a tag should surface the
		// article it tags. Anchor it at the top of the doc, and weight it so a
		// deliberate tag beats a passing prose mention elsewhere.
		if !matchedBody && metaBonus > 0 {
			candidates = append(candidates, scored{doc: dc, line: 0, hits: metaBonus * 2})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].hits != candidates[j].hits {
			return candidates[i].hits > candidates[j].hits
		}
		if candidates[i].doc.meta.ID != candidates[j].doc.meta.ID {
			return candidates[i].doc.meta.ID < candidates[j].doc.meta.ID
		}
		return candidates[i].line < candidates[j].line
	})

	// Merge candidates that fall within the context window of an already
	// selected hit in the same doc, so one dense paragraph does not consume
	// the whole result budget.
	var hits []SearchHit
	claimed := map[string][]int{}

	for _, c := range candidates {
		if len(hits) >= maxHits {
			break
		}
		if overlaps(claimed[c.doc.meta.ID], c.line, context) {
			continue
		}
		claimed[c.doc.meta.ID] = append(claimed[c.doc.meta.ID], c.line)

		hits = append(hits, SearchHit{
			ID:      c.doc.meta.ID,
			Title:   c.doc.meta.Title,
			Line:    c.line + 1,
			Snippet: snippet(c.doc.lines, c.line, context),
			Score:   c.hits,
		})
	}

	return hits
}

func snippet(lines []string, at, context int) string {
	start := at - context
	if start < 0 {
		start = 0
	}
	end := at + context + 1
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, end-start)
	for _, line := range lines[start:end] {
		if len(line) > maxSnippetLineWidth {
			line = line[:maxSnippetLineWidth] + "..."
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func overlaps(claimed []int, line, context int) bool {
	for _, c := range claimed {
		diff := c - line
		if diff < 0 {
			diff = -diff
		}
		if diff <= context*2 {
			return true
		}
	}
	return false
}

func tokenize(query string) []string {
	var tokens []string
	seen := map[string]bool{}
	for _, field := range strings.Fields(strings.ToLower(query)) {
		field = strings.Trim(field, `.,:;"'()[]{}`)
		if len(field) < minTokenLen || seen[field] {
			continue
		}
		seen[field] = true
		tokens = append(tokens, field)
	}
	return tokens
}

// isYAMLKeyLine reports whether a trimmed line looks like "key:" or "key: value".
func isYAMLKeyLine(trimmed string) bool {
	idx := strings.IndexByte(trimmed, ':')
	if idx <= 0 {
		return false
	}
	for _, r := range trimmed[:idx] {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '"' {
			continue
		}
		return false
	}
	return true
}

func clamp(value, fallback, min, max int) int {
	if value <= 0 {
		value = fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
