package audit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// markerRe matches a gosec suppression and captures its rule id, e.g. the
// "G115" in "// #nosec G115 -- reason".
var markerRe = regexp.MustCompile(`#nosec\s+(G\d+)`)

// breakdownRe matches one "G115 ×18" entry in the ledger's summary line. The
// separator is the multiplication sign U+00D7.
var breakdownRe = regexp.MustCompile(`(G\d+)\s*\x{00D7}\s*(\d+)`)

// TestNosecLedgerInSync fails when the // #nosec markers in internal/ drift from
// the per-rule counts documented in docs/gosec-suppressions.md, forcing the
// ledger to be updated alongside any suppression that is added or removed.
func TestNosecLedgerInSync(t *testing.T) {
	root := repoRoot(t)

	actual := countMarkersByRule(t, filepath.Join(root, "internal"))
	documented := ledgerCountsByRule(t, filepath.Join(root, "docs", "gosec-suppressions.md"))

	if !equalCounts(actual, documented) {
		t.Fatalf("gosec #nosec markers in internal/ are out of sync with docs/gosec-suppressions.md.\n"+
			"  in code:   %s\n"+
			"  in ledger: %s\n"+
			"Update docs/gosec-suppressions.md (the summary counts and the rule section) to match.\n"+
			"Live list: grep -rn \"#nosec\" internal/",
			format(actual), format(documented))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file == <root>/internal/audit/ledger_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// countMarkersByRule counts // #nosec markers per rule across non-test .go files
// under dir. Test files are skipped so this guard never counts its own literals.
func countMarkersByRule(t *testing.T, dir string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- test walks the repo's own source tree
		if err != nil {
			return err
		}
		for _, m := range markerRe.FindAllSubmatch(data, -1) {
			counts[string(m[1])]++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	if len(counts) == 0 {
		t.Fatalf("found no #nosec markers under %s; the guard is likely misconfigured", dir)
	}
	return counts
}

// ledgerCountsByRule parses the "G115 ×18, ..." summary line from the ledger.
func ledgerCountsByRule(t *testing.T, path string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test reads the repo's own ledger file
	if err != nil {
		t.Fatalf("reading ledger %s: %v", path, err)
	}
	counts := map[string]int{}
	for _, m := range breakdownRe.FindAllSubmatch(data, -1) {
		n, _ := strconv.Atoi(string(m[2]))
		counts[string(m[1])] += n
	}
	if len(counts) == 0 {
		t.Fatalf("could not parse any 'G<rule> ×<count>' entries from %s", path)
	}
	return counts
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func format(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"×"+strconv.Itoa(m[k]))
	}
	return strings.Join(parts, ", ")
}
