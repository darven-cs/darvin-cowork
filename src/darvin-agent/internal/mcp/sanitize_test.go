// Tests for path sanitisation of server identifiers.

package mcp

import "testing"

func TestSanitizeForPath_AllowedChars(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"github-mcp", "github-mcp"},
		{"a.b_c-d", "a.b_c-d"},
		{"server123", "server123"},
		{"ABC", "ABC"},
	}
	for _, c := range cases {
		if got := sanitizeForPath(c.in); got != c.want {
			t.Errorf("sanitizeForPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeForPath_ScopeNameCollapses(t *testing.T) {
	// "@scope/name" → "-scope-name" → strip leading "-" → "scope-name"
	if got := sanitizeForPath("@scope/name"); got != "scope-name" {
		t.Errorf("sanitizeForPath(@scope/name) = %q, want %q", got, "scope-name")
	}
}

func TestSanitizeForPath_ConsecutiveSeparatorsCollapse(t *testing.T) {
	// Each run of non-allowed chars collapses to ONE dash; allowed
	// chars (including '-' itself) pass through unchanged. This
	// mirrors LobsterAI's TS implementation:
	//   replace(/[^a-zA-Z0-9._-]+/g, '-')
	// which does NOT collapse runs of allowed dashes.
	if got := sanitizeForPath("a///b"); got != "a-b" {
		t.Errorf("sanitizeForPath(a///b) = %q, want %q", got, "a-b")
	}
	// "a - b" → a, space→-, -, space→-,, b → "a---b"
	if got := sanitizeForPath("a - b"); got != "a---b" {
		t.Errorf("sanitizeForPath(a - b) = %q, want %q", got, "a---b")
	}
}

func TestSanitizeForPath_StripsLeadingTrailingDash(t *testing.T) {
	if got := sanitizeForPath("---foo---"); got != "foo" {
		t.Errorf("sanitizeForPath(---foo---) = %q, want %q", got, "foo")
	}
	if got := sanitizeForPath("/abc/"); got != "abc" {
		t.Errorf("sanitizeForPath(/abc/) = %q, want %q", got, "abc")
	}
}

func TestSanitizeForPath_NonAsciiBecomesDash(t *testing.T) {
	// CJK characters are not in [a-zA-Z0-9._-], so each char becomes a
	// dash (run collapsed). Leading dashes are then stripped, so
	// "中文-server" → "--server" → "server".
	if got := sanitizeForPath("中文-server"); got != "server" {
		t.Errorf("sanitizeForPath(中文-server) = %q, want %q", got, "server")
	}
	// All non-allowed → all-collapsed → empty → fallback.
	if got := sanitizeForPath("中文"); got != "mcp" {
		t.Errorf("sanitizeForPath(中文) = %q, want %q", got, "mcp")
	}
}

func TestSanitizeForPath_EmptyAndDotDotFallback(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "mcp"},        // empty → fallback
		{"---", "mcp"},     // all dashes → empty after trim → fallback
		{".", "."},         // "." is allowed, no change
		{"..", ".."},       // both dots allowed, no change
		{"./-/.", ".---."}, // mirrors LobsterAI: run-collapse on "/" leaves dot-dash-dot
	}
	for _, c := range cases {
		if got := sanitizeForPath(c.in); got != c.want {
			t.Errorf("sanitizeForPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeForPath_ControlCharsTreatedAsSeparators(t *testing.T) {
	if got := sanitizeForPath("a\nb\tc"); got != "a-b-c" {
		t.Errorf("sanitizeForPath(a\\nb\\tc) = %q, want %q", got, "a-b-c")
	}
}
