package tool

import (
	"path/filepath"
	"strings"
)

// compiledExclusion is one prepared exclusion pattern. A trailing `.*`
// becomes a prefix match (e.g. `.env.*` matches `.env.local`); any other
// pattern is a whole-component equality match.
type compiledExclusion struct {
	pattern   string // lowercased original pattern
	prefix    string // for `.*` patterns: lowercased prefix including the dot
	hasSuffix bool
}

// compileExclusions turns raw patterns into matchers. Patterns that end in
// `.*` match any component starting with the literal prefix (`.env.*` → any
// component beginning with `.env.`); everything else matches a component
// exactly. Component matching is case-insensitive.
func compileExclusions(patterns []string) ([]compiledExclusion, error) {
	out := make([]compiledExclusion, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		lp := strings.ToLower(p)
		c := compiledExclusion{pattern: lp}
		if strings.HasSuffix(lp, ".*") {
			c.hasSuffix = true
			c.prefix = strings.TrimSuffix(lp, ".*") + "."
		}
		out = append(out, c)
	}
	return out, nil
}

// matchExclusion reports whether any path component of abs matches an
// exclusion pattern, returning the matching pattern. Matching is done at
// component granularity so `proj/sub/.git/foo` is rejected by the `.git`
// pattern but `not-git/foo` is not.
func matchExclusion(excl []compiledExclusion, abs string) (string, bool) {
	sep := string(filepath.Separator)
	for _, comp := range strings.Split(abs, sep) {
		lc := strings.ToLower(comp)
		for _, c := range excl {
			if c.hasSuffix {
				if strings.HasPrefix(lc, c.prefix) {
					return c.pattern, true
				}
			} else if lc == c.pattern {
				return c.pattern, true
			}
		}
	}
	return "", false
}
