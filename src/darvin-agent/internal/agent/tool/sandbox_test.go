package tool

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxResolveInside(t *testing.T) {
	sb, err := newFsSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := sb.Resolve("a/b/c.txt")
	if err != nil {
		t.Fatalf("Resolve inside: %v", err)
	}
	if !strings.HasPrefix(got, sb.Root()) {
		t.Errorf("Resolve = %q, not under root %q", got, sb.Root())
	}
}

func TestSandboxRejectEscape(t *testing.T) {
	sb, err := newFsSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Resolve("../etc/passwd"); err == nil {
		t.Error("expected escape error for ../etc/passwd")
	}
	if _, err := sb.Resolve("/etc/hosts"); err == nil {
		t.Error("expected escape error for /etc/hosts")
	}
}

func TestSandboxRootAbsolute(t *testing.T) {
	tmp := t.TempDir()
	abs, _ := filepath.Abs(tmp)
	sb, err := newFsSandbox(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Root() != abs {
		t.Errorf("Root = %q, want %q", sb.Root(), abs)
	}
}
