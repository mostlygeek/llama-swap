package sourcecode

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mostlygeek/llama-swap/internal/mcptools"
)

// The file tools follow Pi's grep, find, ls and read (badlogic/pi-mono,
// packages/coding-agent/src/core/tools): the same parameters, output shapes and
// actionable truncation notices, with read's line numbers from Codex's former
// read_file. Results are capped at MaxToolResultBytes rather than Pi's 50KB,
// because they go to local models with small context windows.
const (
	defaultGrepLimit = 100
	// maxGrepLimit caps the matches kept in memory. A 16KB result never has
	// room for more lines than this.
	maxGrepLimit     = 1000
	maxContextLines  = 5
	defaultFindLimit = 1000
	defaultLsLimit   = 500
	maxReadLines     = 2000

	// maxLineChars stops one minified or generated line crowding every other
	// grep match out of a result. read returns lines whole.
	maxLineChars = 500
)

func (p *Provider) fileTools() []mcptools.Tool {
	read := mcptools.QualifyName(p.ID(), "read")
	limits := fmt.Sprintf("%dKB", mcptools.MaxToolResultBytes/1024)
	return []mcptools.Tool{
		{
			Name:  "grep",
			Title: "Search llama-swap source code",
			Description: "Search llama-swap's source code at " + p.ref + ", the version this server runs, for a regular expression " +
				"(RE2, ripgrep syntax). Returns matching lines as path:line: text, with paths from the repository root. " +
				fmt.Sprintf("Output is truncated to %d matches or %s, whichever is hit first. Long lines are truncated to %d chars. ",
					defaultGrepLimit, limits, maxLineChars) +
				"Use it to check the documentation against the code: a config key's default, how a feature behaves, " +
				"where an error message comes from. Documentation id 'guides/x/y' is the file docs/kb/guides/x/y.md.",
			Annotations: codeAnnotations(),
			Tags:        []string{"code", "search", "source"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Search pattern (regex or literal string), for example 'globalTTL' or 'func \\(p \\*Process\\) Stop'.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory or file to search, from the repository root, for example 'internal/process' (default: the whole repository).",
					},
					"glob": map[string]any{
						"type":        "string",
						"description": "Filter files by glob pattern, for example '*.go', '*.{go,yaml}' or 'ui/src/**/*.ts'.",
					},
					"ignore_case": map[string]any{
						"type":        "boolean",
						"description": "Case-insensitive search (default: false).",
					},
					"literal": map[string]any{
						"type":        "boolean",
						"description": "Treat pattern as a literal string instead of a regex (default: false).",
					},
					"context": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Number of lines to show before and after each match (default: 0, maximum: %d).", maxContextLines),
						"minimum":     0,
						"maximum":     maxContextLines,
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum number of matches to return (default: %d, maximum: %d).", defaultGrepLimit, maxGrepLimit),
						"minimum":     1,
						"maximum":     maxGrepLimit,
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			Name:  "read",
			Title: "Read a llama-swap source file",
			Description: "Read a file from llama-swap's source code at " + p.ref + ". The first line is a GitHub link to cite, and every line is " +
				"numbered as L12: so you can cite it too. " +
				fmt.Sprintf("Output is truncated to %d lines or %s, whichever is hit first. Lines are returned whole. ",
					maxReadLines, limits) +
				"Use offset and limit for large files. When you need the full file, continue with offset until complete.",
			Annotations: codeAnnotations(),
			Tags:        []string{"code", "source"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file from the repository root, for example 'internal/config/config.go'.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Line number to start reading from (1-indexed). To see a grep match in context, start a few lines before it.",
						"minimum":     1,
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to read.",
						"minimum":     1,
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		{
			Name:  "find",
			Title: "Find llama-swap source files",
			Description: "Find files in llama-swap's source code at " + p.ref + " by glob pattern. Returns file paths from the repository root. " +
				fmt.Sprintf("Output is truncated to %d results or %s, whichever is hit first.", defaultFindLimit, limits),
			Annotations: codeAnnotations(),
			Tags:        []string{"code", "source", "index"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to match files, for example '*.go', '**/*_test.go' or 'docs/kb/**/*.md'.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search in, from the repository root (default: the whole repository).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum number of results (default: %d).", defaultFindLimit),
						"minimum":     1,
					},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			Name:  "ls",
			Title: "List a llama-swap source directory",
			Description: "List a directory in llama-swap's source code at " + p.ref + ". Returns entries sorted alphabetically, with '/' after directories. " +
				fmt.Sprintf("Output is truncated to %d entries or %s, whichever is hit first. ", defaultLsLimit, limits) +
				"Start here to learn the layout, then use " + read + ".",
			Annotations: codeAnnotations(),
			Tags:        []string{"code", "source", "index"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to list, from the repository root (default: the repository root).",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum number of entries to return (default: %d).", defaultLsLimit),
						"minimum":     1,
					},
				},
				"additionalProperties": false,
			},
		},
	}
}

func (p *Provider) grep(idx *Index, args map[string]json.RawMessage) mcptools.Result {
	pattern := mcptools.StringArg(args, "pattern")
	if pattern == "" {
		return mcptools.Result{IsError: true, Content: `Error: "pattern" is required.`}
	}
	limit := min(max(mcptools.IntArg(args, "limit", defaultGrepLimit), 1), maxGrepLimit)

	res, err := idx.Search(SearchOptions{
		Pattern:    pattern,
		Literal:    boolArg(args, "literal"),
		IgnoreCase: boolArg(args, "ignore_case"),
		Path:       mcptools.StringArg(args, "path"),
		Glob:       mcptools.StringArg(args, "glob"),
		Context:    min(max(mcptools.IntArg(args, "context", 0), 0), maxContextLines),
		MaxMatches: limit,
	})
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf("Error: %v.", err)}
	}
	if res.TotalMatches == 0 {
		// An empty result set is a valid answer, not an error.
		return mcptools.Result{Content: "No matches found"}
	}

	// ripgrep's convention: ':' marks the matching line, '-' its context.
	var lines []string
	cut := false
	for _, m := range res.Matches {
		for i, line := range m.Before {
			text, c := clip(line)
			cut = cut || c
			lines = append(lines, fmt.Sprintf("%s-%d- %s", m.Path, m.Line-len(m.Before)+i, text))
		}
		text, c := clip(m.Text)
		cut = cut || c
		lines = append(lines, fmt.Sprintf("%s:%d: %s", m.Path, m.Line, text))
		for i, line := range m.After {
			text, c := clip(line)
			cut = cut || c
			lines = append(lines, fmt.Sprintf("%s-%d- %s", m.Path, m.Line+1+i, text))
		}
	}

	kept, overBytes := fitBytes(lines)
	var notices []string
	switch {
	case len(res.Matches) == res.TotalMatches:
	case limit < maxGrepLimit:
		notices = append(notices, fmt.Sprintf("%d matches limit reached (%d matching lines in all). Use limit=%d for more, or refine pattern",
			limit, res.TotalMatches, min(limit*2, maxGrepLimit)))
	default:
		notices = append(notices, fmt.Sprintf("%d matches limit reached (%d matching lines in all). Refine the pattern, path or glob to see the rest",
			limit, res.TotalMatches))
	}
	if overBytes {
		notices = append(notices, byteLimitNotice())
	}
	if cut {
		notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use %s to see full lines",
			maxLineChars, mcptools.QualifyName(p.ID(), "read")))
	}
	return withNotices(lines[:kept], notices)
}

func (p *Provider) read(idx *Index, args map[string]json.RawMessage) mcptools.Result {
	pathArg := mcptools.StringArg(args, "path")
	if pathArg == "" {
		return mcptools.Result{IsError: true, Content: `Error: "path" is required.`}
	}
	limit := maxReadLines
	userLimit := args["limit"] != nil
	if userLimit {
		limit = min(max(mcptools.IntArg(args, "limit", maxReadLines), 1), maxReadLines)
	}

	file, err := idx.Read(pathArg, mcptools.IntArg(args, "offset", 1), limit)
	if err == errNoFile {
		content := "Error: file not found: " + pathArg + "."
		if suggestions := idx.Suggest(pathArg, 10); len(suggestions) > 0 {
			content += " Did you mean one of: " + strings.Join(suggestions, ", ") + "?"
		}
		return mcptools.Result{IsError: true, Content: content}
	}
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf("Error: %v.", err)}
	}

	// Lines are returned whole, so a long line grep clipped can be read here.
	lines := make([]string, len(file.Lines))
	for i, line := range file.Lines {
		lines[i] = fmt.Sprintf("L%d: %s", file.Offset+i, line)
	}
	kept, overBytes := fitBytes(lines)

	var notices []string
	if kept == 0 {
		// The first line alone is over the byte limit, as in a minified file.
		// Show as much of it as fits rather than nothing.
		lines[0] = cutBytes(lines[0], mcptools.MaxToolResultBytes-1024) + "... [truncated]"
		kept = 1
		notices = append(notices, fmt.Sprintf("Line %d is longer than the %s limit and was cut", file.Offset, byteLimit()))
	}
	first, last := file.Offset, file.Offset+kept-1
	link := fmt.Sprintf("%s/blob/%s/%s#L%d-L%d", strings.TrimSuffix(p.repoURL, ".git"), p.ref, file.Path, first, last)
	lines = append([]string{link}, lines[:kept]...)

	// Pi's continuation notices.
	switch next := last + 1; {
	case next > file.Total:
	case overBytes:
		notices = append(notices, fmt.Sprintf("Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.",
			first, last, file.Total, byteLimit(), next))
	case !userLimit:
		notices = append(notices, fmt.Sprintf("Showing lines %d-%d of %d. Use offset=%d to continue.", first, last, file.Total, next))
	default:
		notices = append(notices, fmt.Sprintf("%d more lines in file. Use offset=%d to continue.", file.Total-last, next))
	}
	result := withNotices(lines, notices)
	result.Truncated = len(notices) > 0
	return result
}

func (p *Provider) find(idx *Index, args map[string]json.RawMessage) mcptools.Result {
	pattern := mcptools.StringArg(args, "pattern")
	if pattern == "" {
		return mcptools.Result{IsError: true, Content: `Error: "pattern" is required.`}
	}
	limit := max(mcptools.IntArg(args, "limit", defaultFindLimit), 1)

	files, err := idx.Find(mcptools.StringArg(args, "path"), pattern)
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf("Error: %v.", err)}
	}
	if len(files) == 0 {
		return mcptools.Result{Content: "No files found matching pattern"}
	}

	var notices []string
	if len(files) > limit {
		files = files[:limit]
		notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", limit, limit*2))
	}
	kept, overBytes := fitBytes(files)
	if overBytes {
		notices = append(notices, byteLimitNotice())
	}
	return withNotices(files[:kept], notices)
}

func (p *Provider) ls(idx *Index, args map[string]json.RawMessage) mcptools.Result {
	limit := max(mcptools.IntArg(args, "limit", defaultLsLimit), 1)

	entries, err := idx.List(mcptools.StringArg(args, "path"))
	if err != nil {
		return mcptools.Result{IsError: true, Content: fmt.Sprintf("Error: %v.", err)}
	}
	if len(entries) == 0 {
		return mcptools.Result{Content: "(empty directory)"}
	}

	var notices []string
	if len(entries) > limit {
		entries = entries[:limit]
		notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", limit, limit*2))
	}
	kept, overBytes := fitBytes(entries)
	if overBytes {
		notices = append(notices, byteLimitNotice())
	}
	return withNotices(entries[:kept], notices)
}

// fitBytes returns how many whole lines fit in a result, leaving room for the
// notices, and whether any had to be left out. Like Pi's truncateHead, it never
// returns part of a line.
func fitBytes(lines []string) (int, bool) {
	budget := mcptools.MaxToolResultBytes - 512
	for i, line := range lines {
		budget -= len(line) + 1
		if budget < 0 {
			return i, true
		}
	}
	return len(lines), false
}

// withNotices joins lines and appends Pi-style notices: bracketed, after a
// blank line, saying what was left out and how to get it.
func withNotices(lines []string, notices []string) mcptools.Result {
	text := strings.Join(lines, "\n")
	if len(notices) == 0 {
		return mcptools.Result{Content: text}
	}
	return mcptools.Result{Content: text + "\n\n[" + strings.Join(notices, ". ") + "]", Truncated: true}
}

func byteLimit() string { return fmt.Sprintf("%dKB", mcptools.MaxToolResultBytes/1024) }

func byteLimitNotice() string { return byteLimit() + " limit reached" }

// cutBytes shortens s to at most n bytes without splitting a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// clip cuts a grep line at maxLineChars, marked the way Pi marks it.
func clip(line string) (string, bool) {
	if len([]rune(line)) <= maxLineChars {
		return line, false
	}
	return string([]rune(line)[:maxLineChars]) + "... [truncated]", true
}

func boolArg(args map[string]json.RawMessage, key string) bool {
	raw, ok := args[key]
	if !ok {
		return false
	}
	var value bool
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	// Small models sometimes send "true" as a string.
	return strings.EqualFold(strings.Trim(strings.TrimSpace(string(raw)), `"`), "true")
}
