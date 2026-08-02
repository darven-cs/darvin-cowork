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

	var frontmatter Frontmatter
	if err := yaml.Unmarshal(raw[start:end], &frontmatter); err != nil {
		return Frontmatter{}, "", fmt.Errorf("yaml: %w", err)
	}
	if frontmatter.Name == "" {
		return Frontmatter{}, "", errors.New("frontmatter.name is required")
	}
	if !skillNamePattern.MatchString(frontmatter.Name) {
		return Frontmatter{}, "", fmt.Errorf("invalid name: %q", frontmatter.Name)
	}
	if strings.TrimSpace(frontmatter.Description) == "" {
		return Frontmatter{}, "", errors.New("frontmatter.description is required")
	}
	if len([]rune(strings.TrimSpace(frontmatter.Description))) < 10 {
		return Frontmatter{}, "", errors.New("frontmatter.description too short")
	}

	body := raw[end+len("\n---"):]
	body = bytes.TrimLeft(body, "\r\n")
	return frontmatter, string(body), nil
}
