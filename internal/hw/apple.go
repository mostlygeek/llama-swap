package hw

import "regexp"

// metalFamilyKeys are the system_profiler SPDisplaysDataType keys that report
// Metal GPU-family support, newest naming first. macOS renamed this key over
// time (`spdisplays_metal` → `spdisplays_metal_support`/`spdisplays_metalfamily`
// → `spdisplays_mtlgpufamilysupport`), so we probe all known spellings.
var metalFamilyKeys = []string{
	"spdisplays_mtlgpufamilysupport",
	"spdisplays_metalfamily",
	"spdisplays_metal_support",
	"spdisplays_metal",
}

var metalVersionPattern = regexp.MustCompile(`(?i)metal\s*([0-9]+)`)

// metalVersion extracts the Metal family version from a system_profiler value
// such as "spdisplays_metal4" ("4") or "Metal 3" ("3"). It returns "" for
// values that carry no version (e.g. the legacy "spdisplays_supported").
func metalVersion(value string) string {
	if match := metalVersionPattern.FindStringSubmatch(value); len(match) == 2 {
		return match[1]
	}
	return ""
}
