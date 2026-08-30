package docagent

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter is the YAML block at the top of every knowledge base article.
// docs/kb/README.md documents the contract; TestKB_FrontmatterIsValid enforces
// it against the real corpus.
type frontmatter struct {
	Title      string   `yaml:"title"`
	Summary    string   `yaml:"summary"`
	Category   string   `yaml:"category"`
	Tags       []string `yaml:"tags"`
	ConfigKeys []string `yaml:"config_keys"`
	Updated    string   `yaml:"updated"`
}

// maxSummaryLen bounds synthesized summaries. Article summaries are validated
// against this in the golden test.
const maxSummaryLen = 200

// parseKB walks docs/kb recursively and returns one doc per article.
// README.md files are contributor guides, not articles, so they are skipped.
func parseKB(fsys fs.FS) []*doc {
	var docs []*doc

	entries, err := fs.ReadDir(fsys, kbDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		category := entry.Name()
		dir := path.Join(kbDir, category)

		_ = fs.WalkDir(fsys, dir, func(source string, file fs.DirEntry, walkErr error) error {
			if walkErr != nil || file.IsDir() || !strings.HasSuffix(file.Name(), ".md") || strings.EqualFold(file.Name(), "README.md") {
				return nil
			}
			raw, err := fs.ReadFile(fsys, source)
			if err != nil {
				return nil
			}
			rel := strings.TrimPrefix(source, kbDir+"/")
			if rel == source || rel == "" {
				return nil
			}
			id := strings.TrimSuffix(rel, ".md")
			if parsed := parseArticle(id, source, string(raw)); parsed != nil {
				docs = append(docs, parsed)
			}
			return nil
		})
	}

	return docs
}

// parseArticle splits frontmatter from body. A file without frontmatter is
// still indexed, falling back to its first heading for a title, so a dropped
// delimiter degrades into a worse entry rather than a missing one.
func parseArticle(id, source, raw string) *doc {
	fm, body, bodyOffset := splitFrontmatter(raw)

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
		bodyOffset++
	}
	if len(lines) == 0 {
		return nil
	}

	title := fm.Title
	if title == "" {
		title = firstHeading(lines)
	}
	if title == "" {
		title = id
	}

	summary := fm.Summary
	if summary == "" {
		summary = firstProse(lines)
	}

	category := fm.Category
	if category == "" {
		category = strings.Split(id, "/")[0]
	}

	return &doc{
		meta: DocMeta{
			ID:         id,
			Title:      title,
			Summary:    truncateSummary(summary, maxSummaryLen),
			Category:   category,
			Tags:       fm.Tags,
			ConfigKeys: fm.ConfigKeys,
		},
		lines:      lines,
		source:     source,
		sourceLine: bodyOffset + 1,
	}
}

// splitFrontmatter returns the parsed frontmatter, the remaining body, and the
// number of lines consumed before the body starts.
func splitFrontmatter(raw string) (frontmatter, string, int) {
	var fm frontmatter

	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return fm, raw, 0
	}

	lines := strings.Split(raw, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") != "---" {
			continue
		}
		block := strings.Join(lines[1:i], "\n")
		// A malformed block is not fatal: fall through to the heading
		// fallbacks rather than dropping the article from the index.
		_ = yaml.Unmarshal([]byte(block), &fm)
		return fm, strings.Join(lines[i+1:], "\n"), i + 1
	}

	return fm, raw, 0
}

func firstHeading(lines []string) string {
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstProse(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip headings, fences, images and badge rows -- README.md opens with
		// a hero image, which makes a useless summary.
		if trimmed == "" ||
			strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "```") ||
			strings.HasPrefix(trimmed, "![") ||
			strings.HasPrefix(trimmed, "<") ||
			strings.HasPrefix(trimmed, "|") ||
			strings.Trim(trimmed, "-") == "" {
			continue
		}
		return trimmed
	}
	return ""
}

// extraMarkdown are top-level docs indexed alongside the knowledge base so
// search covers them too. They carry no frontmatter, so title and summary come
// from the content.
var extraMarkdown = map[string]string{
	"reference/readme": "README.md",
}

func parseExtraMarkdown(fsys fs.FS) []*doc {
	ids := make([]string, 0, len(extraMarkdown))
	for id := range extraMarkdown {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var docs []*doc
	for _, id := range ids {
		file := extraMarkdown[id]
		raw, err := fs.ReadFile(fsys, file)
		if err != nil {
			continue
		}
		parsed := parseArticle(id, file, string(raw))
		if parsed == nil {
			continue
		}
		parsed.meta.Category = "reference"
		docs = append(docs, parsed)
	}
	return docs
}
