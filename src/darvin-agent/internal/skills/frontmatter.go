// Parses and validates the YAML frontmatter block of a SKILL.md.

package skills

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Frontmatter is the YAML block at the top of a SKILL.md. Field names use
// explicit yaml tags so the on-disk spelling matches Anthropic / DeepSeek
// conventions (runAs, allowed-tools, auto-use, etc.).
type Frontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	Version                string `yaml:"version"`
	Invocation             string `yaml:"invocation"`
	UserInvocable          bool   `yaml:"userInvocable"`
	DisableModelInvocation bool   `yaml:"disableModelInvocation"`
	RunAs                  string `yaml:"runAs"`
	Context                string `yaml:"context"`
	Agent                  string `yaml:"agent"`
	AllowedTools           string `yaml:"allowed-tools"`
	Model                  string `yaml:"model"`
	Effort                 string `yaml:"effort"`
	ReadOnly               string `yaml:"read-only"`
	Triggers               string `yaml:"triggers"`
	NegativeTriggers       string `yaml:"negative-triggers"`
	AutoUse                string `yaml:"auto-use"`
	NeedsFreshData         string `yaml:"needs-fresh-data"`
	Cost                   string `yaml:"cost"`
	Color                  string `yaml:"color"`
	Requires               string `yaml:"requires"`
	Profiles               string `yaml:"profiles"`
}

func ParseFrontmatter(raw []byte) (Frontmatter, string, error) {
	if !bytes.HasPrefix(raw, []byte("---")) {
		return Frontmatter{}, "", errors.New("missing frontmatter")
	}

	start := 3
	if len(raw) == start || (raw[start] != '\n' && raw[start] != '\r') {
		return Frontmatter{}, "", errors.New("invalid frontmatter delimiter")
	}
	end := bytes.Index(raw[start:], []byte("\n---"))
	if end < 0 {
		return Frontmatter{}, "", errors.New("unterminated frontmatter")
	}
	end += start

	var fm Frontmatter
	if err := yaml.Unmarshal(raw[start:end], &fm); err != nil {
		return Frontmatter{}, "", fmt.Errorf("yaml: %w", err)
	}
	if fm.Name == "" {
		return Frontmatter{}, "", errors.New("frontmatter.name is required")
	}
	if !skillNamePattern.MatchString(fm.Name) {
		return Frontmatter{}, "", fmt.Errorf("invalid name: %q", fm.Name)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return Frontmatter{}, "", errors.New("frontmatter.description is required")
	}
	if len([]rune(strings.TrimSpace(fm.Description))) < 10 {
		return Frontmatter{}, "", errors.New("frontmatter.description too short")
	}

	body := raw[end+len("\n---"):]
	body = bytes.TrimLeft(body, "\r\n")
	return fm, string(body), nil
}

// parseRunAs maps frontmatter to a run mode. An explicit runAs=subagent
// wins; otherwise `context: fork` or a non-empty `agent:` (cross-tool
// conventions) signals subagent isolation. Unknown runAs values default
// to the safe (non-spawning) inline mode.
func parseRunAs(runAs, context, agent string) string {
	if strings.EqualFold(strings.TrimSpace(runAs), "subagent") {
		return "subagent"
	}
	if strings.EqualFold(strings.TrimSpace(context), "fork") {
		return "subagent"
	}
	if strings.TrimSpace(agent) != "" {
		return "subagent"
	}
	return "inline"
}

// parseCSVFrontmatter splits comma-separated values, with optional
// surrounding [...] brackets. Empty values are dropped.
func parseCSVFrontmatter(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if t := strings.Trim(strings.TrimSpace(p), `"'`); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseBoolFrontmatter accepts true/yes/1/on (case-insensitive) as truthy.
func parseBoolFrontmatter(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "1", "on":
		return true
	default:
		return false
	}
}

// parseAutoUse maps to a known auto-use bucket. Unknown values are
// normalised to "" so callers can distinguish "explicit off" from
// "unparseable".
func parseAutoUse(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "suggest", "prefer", "require":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

// parseCost maps to a known cost bucket. Unknown values are normalised
// to "".
func parseCost(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

// parseInvocation maps to auto (default) or manual.
func parseInvocation(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "manual") {
		return "manual"
	}
	return "auto"
}

// parseProfilesFrontmatter keeps only economy|balanced|delivery values
// and returns the rejected ones separately so future diagnostics can
// surface typos instead of the parser hiding them.
func parseProfilesFrontmatter(raw string) (valid, invalid []string) {
	seen := map[string]bool{}
	for _, p := range parseCSVFrontmatter(raw) {
		p = strings.ToLower(strings.TrimSpace(p))
		switch p {
		case "economy", "balanced", "delivery":
			if !seen[p] {
				seen[p] = true
				valid = append(valid, p)
			}
		case "":
		default:
			if !seen[p] {
				seen[p] = true
				invalid = append(invalid, p)
			}
		}
	}
	return valid, invalid
}
