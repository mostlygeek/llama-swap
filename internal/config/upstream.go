package config

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// DefaultUpstreamIgnorePathsPattern is the default regular expression applied
// to upstream.ignorePaths when the section is empty or absent from the config.
// It matches common static-asset suffixes so requests for .js/.css/.png/etc.
// files do not trigger a model swap.
const DefaultUpstreamIgnorePathsPattern = `.*\.(js|json|css|png|gif|jpg|jpeg|ico|txt)$`

// DefaultUpstreamIgnorePaths returns the default compiled ignore paths used
// when upstream.ignorePaths is not specified in the config. The returned slice
// is fresh so callers may mutate it without affecting other configs.
func DefaultUpstreamIgnorePaths() []*regexp.Regexp {
	return []*regexp.Regexp{regexp.MustCompile(DefaultUpstreamIgnorePathsPattern)}
}

// UpstreamConfig controls behaviour of the /upstream passthrough endpoint.
type UpstreamConfig struct {
	// IgnorePaths is a slice of compiled regular expressions. Any request to
	// /upstream/<model>/<path> whose remaining path matches any of these
	// expressions will be ignored and not trigger a swap. When the config
	// does not specify any patterns, DefaultUpstreamIgnorePaths is applied.
	IgnorePaths []*regexp.Regexp `yaml:"-"`
}

// rawUpstreamConfig is the intermediate form used to unmarshal the YAML into
// plain strings, which are then compiled into *regexp.Regexp.
type rawUpstreamConfig struct {
	IgnorePaths []string `yaml:"ignorePaths"`
}

// UnmarshalYAML compiles each ignorePaths entry into a *regexp.Regexp. If any
// entry fails to compile, an error is returned.
func (u *UpstreamConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw rawUpstreamConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	patterns := make([]*regexp.Regexp, 0, len(raw.IgnorePaths))
	for _, p := range raw.IgnorePaths {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("upstream.ignorePaths: invalid regular expression %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}
	u.IgnorePaths = patterns
	return nil
}
