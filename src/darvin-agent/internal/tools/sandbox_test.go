// Tests for workspace sandbox path confinement.

package tool

import (
	"errors"
	"os"
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

func TestSandboxResolveSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = sb.Resolve("link")
	if !errors.Is(err, ErrPathEscapesViaSymlink) {
		t.Errorf("Resolve(link) err = %v, want ErrPathEscapesViaSymlink", err)
	}
}

func TestSandboxResolveNestedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "b"), filepath.Join(root, "a", "b")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(outside, "b", "c")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Resolve("a/b/c"); !errors.Is(err, ErrPathEscapesViaSymlink) {
		t.Errorf("Resolve(a/b/c) err = %v, want ErrPathEscapesViaSymlink", err)
	}
}

func TestSandboxResolveNonExistentPath(t *testing.T) {
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sb.Resolve("subdir/new.txt")
	if err != nil {
		t.Fatalf("Resolve non-existent path: %v", err)
	}
	if want := filepath.Join(root, "subdir", "new.txt"); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
	if _, _, err := sb.openRootFile("subdir/new.txt", "test"); err == nil {
		t.Error("openRootFile on non-existent path should error (ENOENT)")
	}
}

func TestSandboxRootIsSymlink(t *testing.T) {
	real := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "proj")
	if err := os.Symlink(real, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	sb, err := newFsSandbox(linkRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := sb.Resolve("foo.txt"); err != nil {
		t.Fatalf("Resolve inside symlinked root: %v", err)
	} else if want := filepath.Join(linkRoot, "foo.txt"); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
	if sb.RealRoot() != real {
		t.Errorf("RealRoot = %q, want %q", sb.RealRoot(), real)
	}
}

func TestExclusionDefault(t *testing.T) {
	for _, comp := range []string{".git", "node_modules", ".env", "__pycache__", "target", "dist", ".DS_Store"} {
		root := t.TempDir()
		dir := filepath.Join(root, comp)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		sb, err := newFsSandbox(root, DefaultPathExclusions()...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sb.Resolve(comp); !errors.Is(err, ErrPathExcluded) {
			t.Errorf("Resolve(%q) err = %v, want ErrPathExcluded", comp, err)
		}
	}
}

func TestExclusionComponentLevel(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proj", "sub", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sb, err := newFsSandbox(root, DefaultPathExclusions()...)
	if err != nil {
		t.Fatal(err)
	}
	// The excluded component is matched at any depth, but a sibling name
	// that merely contains the pattern is fine.
	if _, err := sb.Resolve("proj/sub/.git/foo"); !errors.Is(err, ErrPathExcluded) {
		t.Errorf("Resolve(proj/sub/.git/foo) err = %v, want ErrPathExcluded", err)
	}
	if _, err := sb.Resolve("proj/not-git/foo"); err != nil {
		t.Errorf("Resolve(proj/not-git/foo) should pass, got %v", err)
	}
}

func TestExclusionCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "NODE_MODULES"), 0o755); err != nil {
		t.Fatal(err)
	}
	sb, err := newFsSandbox(root, DefaultPathExclusions()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Resolve("NODE_MODULES"); !errors.Is(err, ErrPathExcluded) {
		t.Errorf("Resolve(NODE_MODULES) err = %v, want ErrPathExcluded", err)
	}
}

func TestSandboxOpenRootFileLimited(t *testing.T) {
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, maxReadBytes+10)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(root, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, got, truncated, err := sb.openRootFileLimited("big.bin", "test", 0, maxReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !truncated {
		t.Error("expected truncation for file larger than maxReadBytes")
	}
	if int64(len(got)) != maxReadBytes {
		t.Errorf("read bytes = %d, want %d", len(got), maxReadBytes)
	}
}

func TestSandboxOpenRootFileLimitedWithOffset(t *testing.T) {
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("0123456789ABCDEFGHIJ"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, got, _, err := sb.openRootFileLimited("data.txt", "test", 100, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// offset beyond EOF → empty read (no error, fd already positioned)
	if len(got) != 0 {
		t.Errorf("read beyond EOF = %q, want empty", got)
	}
	f2, got2, _, err := sb.openRootFileLimited("data.txt", "test", 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	if string(got2) != "56789" {
		t.Errorf("offset window = %q, want %q", got2, "56789")
	}
}
