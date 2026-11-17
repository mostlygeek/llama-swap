// Package changelog embeds llama-swap's release notes, one markdown
// file per release, and makes them available to importing programs by
// version number.
//
// It returns the raw markdown; rendering is left to the consumer.
//
//	versions := changelog.Versions()  // ["v200", ..., "v271"]
//	latest, err := changelog.Latest() // "v271"
//	note, err := changelog.Get("v271") // raw markdown
package changelog

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Files contains the release notes, one markdown file per release,
// named v<number>.md.
//
//go:embed v*.md
var Files embed.FS

// Get returns the raw markdown of the release notes for version, which
// may be given with or without the leading "v", for example "v271" or
// "271".
func Get(version string) (string, error) {
	n, err := number(version)
	if err != nil {
		return "", err
	}
	b, err := Files.ReadFile(name(n))
	if err != nil {
		return "", fmt.Errorf("%s: %w", version, err)
	}
	return string(b), nil
}

// Versions lists the available versions, for example ["v200", ...,
// "v271"], in ascending order of version number.
func Versions() []string {
	names, err := fs.Glob(Files, "v*.md")
	if err != nil {
		return nil
	}

	nums := make([]int, 0, len(names))
	for _, file := range names {
		if n, err := number(strings.TrimSuffix(file, ".md")); err == nil {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = "v" + strconv.Itoa(n)
	}
	return out
}

// Latest returns the highest available version number.
func Latest() (string, error) {
	vs := Versions()
	if len(vs) == 0 {
		return "", errors.New("changelog: no releases")
	}
	return vs[len(vs)-1], nil
}

func name(n int) string {
	return fmt.Sprintf("v%d.md", n)
}

func number(v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if err != nil {
		return 0, fmt.Errorf("changelog: %q: invalid version number", v)
	}
	return n, nil
}
