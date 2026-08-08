// Sanitises server identifiers into safe filesystem path components.

package mcp

import "strings"

// sanitizeForPath collapses any character outside [a-zA-Z0-9._-] to a
// single dash, strips leading/trailing dashes, and falls back to "mcp"
// when the result would be empty — so the segment is always a stable,
// non-empty path component compatible with all three target OSes.
// Mirrors LobsterAI's mcpLaunchResolverManager.sanitizeForPath (TypeScript).
//
// Used to derive the per-(server, package) install dir under the
// mcp-packages root, so that user-provided ids (which may include
// "@scope/name", spaces, CJK, etc.) cannot escape the dir or produce
// an empty component that would collide with siblings.
func sanitizeForPath(value string) string {
	if value == "" {
		return "mcp"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "mcp"
	}
	return s
}
