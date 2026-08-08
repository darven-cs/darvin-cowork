// Tests for PATH enrichment and registry helpers.

package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestEnrichPATH_AddsDirectories(t *testing.T) {
	// Set a minimal PATH for the test.
	oldPATH := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", oldPATH) }()
	_ = os.Setenv("PATH", "/usr/bin")

	got := enrichPATH(nil)
	if got == nil {
		t.Fatal("enrichPATH returned nil for nil env")
	}
	path, ok := got["PATH"]
	if !ok {
		t.Fatal("PATH not set in enriched env")
	}
	// Should contain the original PATH.
	if !strings.Contains(path, "/usr/bin") {
		t.Fatalf("original PATH not preserved: %s", path)
	}
	// Should contain appended directories.
	if !strings.Contains(path, "/usr/local/bin") {
		t.Fatalf("expected /usr/local/bin in PATH: %s", path)
	}
}

func TestEnrichPATH_NoopWhenComplete(t *testing.T) {
	oldPATH := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", oldPATH) }()
	// Set PATH to include all the directories that would be appended (using
	// expanded paths, not $HOME variables).
	home := os.Getenv("HOME")
	complete := "/usr/bin:/usr/local/bin:/usr/local/sbin:" + home + "/.local/bin:" + home + "/.npm-global/bin:" + home + "/.cargo/bin:/opt/homebrew/bin"
	_ = os.Setenv("PATH", complete)

	got := enrichPATH(nil)
	// When all directories are already present, PATH should be unchanged
	// (function returns nil to signal no change needed).
	if got != nil {
		t.Fatalf("enrichPATH should return nil when PATH is complete: %v", got)
	}
}

func TestEnrichPATH_PreservesExistingEnv(t *testing.T) {
	oldPATH := os.Getenv("PATH")
	defer func() { _ = os.Setenv("PATH", oldPATH) }()
	_ = os.Setenv("PATH", "/usr/bin")

	existing := map[string]string{
		"FOO":  "bar",
		"PATH": "/custom/bin",
	}
	got := enrichPATH(existing)
	if got["FOO"] != "bar" {
		t.Fatal("existing FOO env not preserved")
	}
	if !strings.Contains(got["PATH"], "/custom/bin") {
		t.Fatalf("existing PATH not preserved: %s", got["PATH"])
	}
}

func TestRedactCredentials_EveryPattern(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(got string) bool
	}{
		{
			name:  "github_pat",
			input: "ghp_abcdefghijklmnopqrstuvwxyz1234567890",
			check: func(got string) bool { return !strings.Contains(got, "ghp_") },
		},
		{
			name:  "github_token_classic",
			input: "ghp_abcdefghijklmnopqrstuvwxyz1234567890abcdef",
			check: func(got string) bool { return !strings.Contains(got, "ghp_") },
		},
		{
			name:  "slack_token",
			input: "xoxb-abcdefghijklmnopqrstuv",
			check: func(got string) bool { return !strings.Contains(got, "xoxb-") },
		},
		{
			name:  "openai_key",
			input: "sk-abcdefghijklmnopqrstuvwxyz1234567890",
			check: func(got string) bool { return !strings.Contains(got, "sk-") },
		},
		{
			name:  "aws_key",
			input: "AKIAIOSFODNN7EXAMPLE",
			check: func(got string) bool { return !strings.Contains(got, "AKIA") },
		},
		{
			name:  "clean",
			input: "npm install --legacy-peer-deps express",
			check: func(got string) bool { return got == "npm install --legacy-peer-deps express" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactCredentials(tc.input)
			if !tc.check(got) {
				t.Fatalf("RedactCredentials(%q) = %q; check failed", tc.input, got)
			}
		})
	}
}
