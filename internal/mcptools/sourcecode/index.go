package sourcecode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// maxFileBytes skips files no one reads line by line: lockfiles and
// generated bundles. Everything hand-written in the repository is far smaller.
const maxFileBytes = 1 << 20

// skipDirs are never searched: version control internals and installed
// dependencies, which are not llama-swap's code.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

type sourceFile struct {
	path string // slash separated, relative to the checkout root
	text string
	// starts holds the byte offset of each line's first character.
	starts []int
}

func (f *sourceFile) lineCount() int { return len(f.starts) }

// line returns line n, 1-based, without its newline.
func (f *sourceFile) line(n int) string {
	start := f.starts[n-1]
	end := len(f.text)
	if n < len(f.starts) {
		end = f.starts[n] - 1
	}
	return strings.TrimSuffix(strings.TrimSuffix(f.text[start:end], "\n"), "\r")
}

// lineAt returns the 1-based line holding byte offset off.
func (f *sourceFile) lineAt(off int) int {
	return sort.Search(len(f.starts), func(i int) bool { return f.starts[i] > off })
}

// Index searches a checkout on disk. It holds only the checkout's path: every
// call reads the files again, which the kernel's page cache makes cheap, so
// llama-swap never keeps its own copy of the source in memory.
type Index struct {
	root string
}

// LoadIndex opens the checkout at root.
func LoadIndex(root string) (*Index, error) {
	idx := &Index{root: root}
	paths, err := idx.paths()
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no source files found in %s", root)
	}
	return idx, nil
}

// paths lists the checkout's regular files in path order. Large and binary
// files are only filtered out when load reads them.
func (idx *Index) paths() ([]string, error) {
	var out []string
	err := filepath.WalkDir(idx.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(idx.root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// openRoot opens the checkout for one call. Every path from a tool call is
// resolved through it, which keeps "..", absolute paths, Windows drive letters
// and symlinks from reaching outside the checkout. It is opened per call, not
// kept, so a refresh that swaps the directory is seen by the next call.
func (idx *Index) openRoot() (*os.Root, error) {
	return os.OpenRoot(idx.root)
}

// load reads one file. It reports false for anything that is not searched: a
// missing path, a file over maxFileBytes, or binary content.
func (idx *Index) load(root *os.Root, rel string) (*sourceFile, bool) {
	for _, part := range strings.Split(rel, "/") {
		if skipDirs[part] {
			return nil, false
		}
	}
	// Check and read the same open file, so the path is resolved only once.
	fh, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, false
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return nil, false
	}
	// The same test git and ripgrep use for binary content.
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, false
	}

	f := &sourceFile{path: rel, text: string(data), starts: []int{0}}
	for i := 0; i < len(f.text); i++ {
		if f.text[i] == '\n' && i+1 < len(f.text) {
			f.starts = append(f.starts, i+1)
		}
	}
	return f, true
}

// SearchOptions controls Search.
type SearchOptions struct {
	// Pattern is an RE2 regular expression, the syntax ripgrep uses by default.
	Pattern    string
	Literal    bool
	IgnoreCase bool
	// Path limits the search to a directory or a file, and Glob to the files
	// whose names match. See Index.selector.
	Path string
	Glob string
	// Context is how many lines to show either side of each match.
	Context    int
	MaxMatches int
}

// Match is one matching line.
type Match struct {
	Path string
	Line int
	Text string
	// Before and After are context lines, nearest last and first respectively.
	Before []string
	After  []string
}

// SearchResult is what Search found. Like ripgrep, a line counts once however
// many times the pattern occurs on it.
type SearchResult struct {
	Matches       []Match
	TotalMatches  int
	FilesMatched  int
	FilesSearched int
}

// Search finds lines matching opts.Pattern.
func (idx *Index) Search(opts SearchOptions) (SearchResult, error) {
	pattern := opts.Pattern
	if opts.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	// Multi-line mode lets ^ and $ anchor at each line, as they do in grep.
	flags := "(?m)"
	if opts.IgnoreCase {
		flags = "(?mi)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return SearchResult{}, err
	}
	keep, err := idx.selector(opts.Path, opts.Glob)
	if err != nil {
		return SearchResult{}, err
	}
	root, err := idx.openRoot()
	if err != nil {
		return SearchResult{}, err
	}
	defer root.Close()

	all, err := idx.paths()
	if err != nil {
		return SearchResult{}, err
	}
	var paths []string
	for _, p := range all {
		if keep(p) {
			paths = append(paths, p)
		}
	}

	// Files are read and searched in parallel, like ripgrep. Each file keeps
	// at most MaxMatches of its own matches, which is all the merge below can
	// use.
	perFile := make([]fileMatches, len(paths))
	var wg sync.WaitGroup
	next := atomic.Int64{}
	for range min(runtime.GOMAXPROCS(0), len(paths)) {
		wg.Go(func() {
			for i := int(next.Add(1) - 1); i < len(paths); i = int(next.Add(1) - 1) {
				if f, ok := idx.load(root, paths[i]); ok {
					perFile[i] = f.search(re, opts.MaxMatches, opts.Context)
					perFile[i].searched = true
				}
			}
		})
	}
	wg.Wait()

	var res SearchResult
	for _, fm := range perFile {
		if fm.searched {
			res.FilesSearched++
		}
		if fm.total == 0 {
			continue
		}
		res.FilesMatched++
		res.TotalMatches += fm.total
		room := opts.MaxMatches - len(res.Matches)
		res.Matches = append(res.Matches, fm.matches[:min(room, len(fm.matches))]...)
	}
	return res, nil
}

type fileMatches struct {
	matches  []Match
	total    int
	searched bool
}

func (f *sourceFile) search(re *regexp.Regexp, maxMatches, context int) fileMatches {
	var out fileMatches
	// After each match, resume at the next line: one hit per line, and a
	// pattern that matches the empty string cannot produce one per byte.
	// pos is always a line start, so (?m)^ means the same in the slice.
	for pos := 0; pos < len(f.text); {
		loc := re.FindStringIndex(f.text[pos:])
		if loc == nil {
			break
		}
		n := f.lineAt(pos + loc[0])
		out.total++
		if len(out.matches) < maxMatches {
			out.matches = append(out.matches, f.match(n, context))
		}
		if n >= f.lineCount() {
			break
		}
		pos = f.starts[n]
	}
	return out
}

func (f *sourceFile) match(n, context int) Match {
	m := Match{Path: f.path, Line: n, Text: f.line(n)}
	for i := max(1, n-context); i < n; i++ {
		m.Before = append(m.Before, f.line(i))
	}
	for i := n + 1; i <= min(f.lineCount(), n+context); i++ {
		m.After = append(m.After, f.line(i))
	}
	return m
}

// Find lists the files under dir whose names match glob, in path order.
func (idx *Index) Find(dir, glob string) ([]string, error) {
	keep, err := idx.selector(dir, glob)
	if err != nil {
		return nil, err
	}
	paths, err := idx.paths()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// List returns one directory's entries sorted case-insensitively, with a
// trailing "/" on directories.
func (idx *Index) List(dir string) ([]string, error) {
	root, err := idx.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rel := cleanPath(dir)
	if rel == "" {
		rel = "."
	}
	entries, err := fs.ReadDir(root.FS(), rel)
	if err != nil {
		return nil, fmt.Errorf("path not found: %s", dir)
	}
	var out []string
	for _, e := range entries {
		switch {
		case e.IsDir() && skipDirs[e.Name()]:
		case e.IsDir():
			out = append(out, e.Name()+"/")
		default:
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, nil
}

// FileLines is a slice of one file.
type FileLines struct {
	Path   string
	Offset int // 1-based line number of Lines[0]
	Lines  []string
	Total  int
}

var errNoFile = errors.New("no such file")

// Read returns up to limit lines of a file starting at line offset, 1-based.
// An offset past the end of the file is an error, as in Pi and Codex.
func (idx *Index) Read(p string, offset, limit int) (FileLines, error) {
	root, err := idx.openRoot()
	if err != nil {
		return FileLines{}, err
	}
	defer root.Close()
	f, ok := idx.load(root, cleanPath(p))
	if !ok {
		return FileLines{}, errNoFile
	}
	offset = max(offset, 1)
	if offset > f.lineCount() {
		return FileLines{}, fmt.Errorf("offset %d is beyond the end of the file (%d lines total)", offset, f.lineCount())
	}
	out := FileLines{Path: f.path, Offset: offset, Total: f.lineCount()}
	for n := offset; n <= f.lineCount() && len(out.Lines) < limit; n++ {
		out.Lines = append(out.Lines, f.line(n))
	}
	return out, nil
}

// Suggest returns file paths resembling p, for correcting a guessed path.
func (idx *Index) Suggest(p string, limit int) []string {
	base := strings.ToLower(path.Base(p))
	stem := strings.TrimSuffix(base, path.Ext(base))
	paths, _ := idx.paths()
	var out []string
	for _, p := range paths {
		name := strings.ToLower(path.Base(p))
		if name == base || (stem != "" && strings.Contains(name, stem)) {
			out = append(out, p)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

// cleanPath turns a path from a tool call into one relative to the checkout
// root, "" for the root itself. Windows separators are accepted, but the path
// is resolved with "/" only, so `..\` is treated like "../".
func cleanPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// selector builds the predicate for a path and glob pair, the way ripgrep and
// fd treat them: dir is a directory or file to search, and glob filters the
// files below it. "*" stops at "/", "**" does not, and "{go,ts}" picks
// alternatives. A glob with no "/" matches file names anywhere, so "*.go" is
// every Go file; one with "/" matches the path relative to dir.
func (idx *Index) selector(dir, glob string) (func(string) bool, error) {
	dir = cleanPath(dir)
	if dir != "" {
		root, err := idx.openRoot()
		if err != nil {
			return nil, err
		}
		_, err = root.Stat(filepath.FromSlash(dir))
		root.Close()
		if err != nil {
			return nil, fmt.Errorf("path not found: %s", dir)
		}
	}
	inDir := func(p string) (string, bool) {
		if dir == "" {
			return p, true
		}
		if p == dir {
			return path.Base(p), true
		}
		rest, ok := strings.CutPrefix(p, dir+"/")
		return rest, ok
	}

	glob = strings.TrimPrefix(strings.TrimSpace(glob), "./")
	if glob == "" {
		return func(p string) bool { _, ok := inDir(p); return ok }, nil
	}
	re, err := globRegexp(glob)
	if err != nil {
		return nil, err
	}
	byName := !strings.Contains(glob, "/")
	return func(p string) bool {
		rel, ok := inDir(p)
		if !ok {
			return false
		}
		if byName {
			return re.MatchString(path.Base(rel))
		}
		return re.MatchString(rel)
	}, nil
}

func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	inBraces := false
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed [ in glob %q", glob)
			}
			b.WriteString(glob[i : i+end+1])
			i += end
		case c == '{' && !inBraces:
			inBraces = true
			b.WriteString("(?:")
		case c == '}' && inBraces:
			inBraces = false
			b.WriteString(")")
		case c == ',' && inBraces:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if inBraces {
		return nil, fmt.Errorf("unclosed { in glob %q", glob)
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
