package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withEnv scopes an env override for the duration of the test, then
// restores the original value. failOnCleanupError logs a warning if
// os.Setenv returns an error after the test (rare, only on Windows).
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	prev, hadPrev := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestResolveMCPPackagesDir_FromEnv(t *testing.T) {
	want := "/tmp/darvin-mcp-for-test"
	withEnv(t, "DARVIN_MCP_PACKAGES_DIR", want)
	got, err := resolveMCPPackagesDir()
	if err != nil {
		t.Fatalf("resolveMCPPackagesDir: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveMCPPackagesDir_RejectsRelative(t *testing.T) {
	withEnv(t, "DARVIN_MCP_PACKAGES_DIR", "relative/path")
	_, err := resolveMCPPackagesDir()
	if err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error %q does not mention 'absolute'", err.Error())
	}
}

func TestResolveMCPPackagesDir_RejectsEmptyAsRelative(t *testing.T) {
	// Empty / whitespace env falls through to the UserCacheDir
	// fallback rather than reporting an absolute-path error; verify
	// we don't accidentally trip the IsAbs check on "   ".
	withEnv(t, "DARVIN_MCP_PACKAGES_DIR", "   ")
	got, err := resolveMCPPackagesDir()
	if err != nil {
		t.Fatalf("whitespace should fall through, got error %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("fallback result %q is not absolute", got)
	}
}

func TestResolveMCPPackagesDir_FallbackToUserCache(t *testing.T) {
	t.Setenv("DARVIN_MCP_PACKAGES_DIR", "") // explicit unset
	cache, err := os.UserCacheDir()
	if err != nil || cache == "" {
		t.Skip("UserCacheDir unavailable on this platform")
	}
	got, err := resolveMCPPackagesDir()
	if err != nil {
		t.Fatalf("resolveMCPPackagesDir: %v", err)
	}
	want := filepath.Join(cache, "darvin-cowork", "mcp-packages")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
