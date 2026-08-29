package reference

import (
	"fmt"
	"regexp"
	"strings"
)

// configDocPrefix namespaces the synthesized config.example.yaml section docs.
const configDocPrefix = "reference/config/"

// introDocID holds the file header and usage notes that precede the first
// section.
const introDocID = configDocPrefix + "_intro"

// topLevelKey matches a top-level YAML key at column 0. Deliberately anchored
// to column 0: nested keys are part of their section, not sections themselves.
var topLevelKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):`)

// parseConfigExample turns each top-level key of config.example.yaml into a
// doc, keeping the leading comment block that documents it.
//
// The section table is parsed rather than hardcoded because config.example.yaml
// is edited in most configuration PRs and a hardcoded table would rot. The
// comments *are* the documentation here, which is why this is a line scanner
// and not a yaml.Node walk -- the YAML decoder discards them.
func parseConfigExample(content string) []*doc {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")

	type bounds struct {
		key     string
		start   int // index of the first line of the leading comment block
		keyLine int
		endLine int // exclusive
	}

	var found []bounds
	for i, line := range lines {
		match := topLevelKey.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		// Walk backwards over the contiguous comment block directly above the
		// key. Note that commented-out keys are NOT treated as section starts:
		// "# store:" and the "# routing:" inside the hooks example would both
		// produce phantom or duplicate sections. The cost is that a fully
		// commented-out block belongs to the preceding section, which is fine
		// because search still finds it.
		start := i
		for start > 0 && strings.HasPrefix(lines[start-1], "#") {
			start--
		}
		found = append(found, bounds{key: match[1], start: start, keyLine: i})
	}

	if len(found) == 0 {
		return nil
	}

	for i := range found {
		if i+1 < len(found) {
			found[i].endLine = found[i+1].start
		} else {
			found[i].endLine = len(lines)
		}
	}

	docs := make([]*doc, 0, len(found)+1)

	// Everything before the first section is the file header and usage notes.
	if intro := trimBlank(lines[:found[0].start]); len(intro) > 0 {
		docs = append(docs, &doc{
			meta: DocMeta{
				ID:       introDocID,
				Title:    "config.example.yaml overview",
				Summary:  "Header and usage notes from the annotated configuration reference.",
				Category: "reference",
			},
			lines:      intro,
			source:     configExampleFile,
			sourceLine: 1,
			fence:      "yaml",
		})
	}

	for _, b := range found {
		body := lines[b.start:b.endLine]
		for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
			body = body[:len(body)-1]
		}
		docs = append(docs, &doc{
			meta: DocMeta{
				ID:         configDocPrefix + b.key,
				Title:      fmt.Sprintf("config.example.yaml: %s", b.key),
				Summary:    sectionSummary(lines, b.start, b.keyLine, b.key),
				Category:   "reference",
				ConfigKeys: []string{b.key},
			},
			lines:      body,
			source:     configExampleFile,
			sourceLine: b.start + 1,
			fence:      "yaml",
		})
	}

	return docs
}

// sectionSummary reads the first line of a section's comment block, stripping
// the "# " marker and a leading "<key>: " so the summary reads as prose.
func sectionSummary(lines []string, start, keyLine int, key string) string {
	// Scan forward through the comment block for the first line with content.
	// A block that opens with a bare "# routing:" carries its description on
	// the following lines.
	for i := start; i < keyLine; i++ {
		text := strings.TrimSpace(strings.TrimPrefix(lines[i], "#"))
		text = strings.TrimSpace(strings.TrimPrefix(text, key+":"))
		if text == "" || text == "-" {
			continue
		}
		return truncateSummary(text, maxSummaryLen)
	}
	return fmt.Sprintf("The %s configuration section.", key)
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
