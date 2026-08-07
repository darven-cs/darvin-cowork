package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- parseNpxArgs ---

func TestParseNpxArgs_ScopedWithVersion(t *testing.T) {
	pkg, extra, err := parseNpxArgs([]string{"-y", "@scope/name@1.0.0", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "@scope/name" || pkg.Version != "1.0.0" {
		t.Fatalf("pkg = %+v", pkg)
	}
	if len(extra) != 1 || extra[0] != "--flag" {
		t.Fatalf("extra = %v", extra)
	}
}

func TestParseNpxArgs_UnscopedLatest(t *testing.T) {
	pkg, extra, err := parseNpxArgs([]string{"-y", "name"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "name" || pkg.Version != "latest" {
		t.Fatalf("pkg = %+v", pkg)
	}
	if len(extra) != 0 {
		t.Fatalf("extra = %v, want []", extra)
	}
}

func TestParseNpxArgs_ScopedNoVersion(t *testing.T) {
	pkg, _, err := parseNpxArgs([]string{"-y", "@scope/name"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "@scope/name" || pkg.Version != "latest" {
		t.Fatalf("pkg = %+v, want @scope/name@latest", pkg)
	}
}

func TestParseNpxArgs_NoPackage(t *testing.T) {
	_, _, err := parseNpxArgs([]string{"-y", "--yes"})
	if err == nil {
		t.Fatal("expected error for missing package")
	}
}

func TestParseNpxArgs_FirstNonFlagWins(t *testing.T) {
	pkg, extra, err := parseNpxArgs([]string{"--yes", "-y", "alpha@1.0.0", "beta", "--opt"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "alpha" || pkg.Version != "1.0.0" {
		t.Fatalf("pkg = %+v", pkg)
	}
	if len(extra) != 2 || extra[0] != "beta" || extra[1] != "--opt" {
		t.Fatalf("extra = %v", extra)
	}
}

// --- stubResolver ---

func TestStubResolver_Unsupported(t *testing.T) {
	r := &stubResolver{kind: ResolverUvx, msg: "test"}
	if r.Kind() != ResolverUvx {
		t.Fatalf("Kind = %s", r.Kind())
	}
	res, err := r.Resolve(context.Background(), ServerSpec{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnsupported {
		t.Fatalf("status = %s, want unsupported", res.Status)
	}
	if res.ResolverKind != ResolverUvx {
		t.Fatalf("kind = %s", res.ResolverKind)
	}
	if res.Error == "" {
		t.Fatal("expected error message")
	}
}

// --- npxResolver end-to-end via a fake npm shim ---

// fakeNpmScript returns the directory of a fake `npm` binary that
// fakes both `view` (prints "1.2.3") and `install` (writes a
// package.json with a bin entry). The real test only needs the shim
// to be invoked; everything else is exercised by production code.
func fakeNpmScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prog := filepath.Join(dir, "npm")
	script := `#!/bin/sh
set -e
cmd="$1"; shift
case "$cmd" in
  view)
    echo "1.2.3"
    ;;
  install)
    prefix=""
    spec=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --prefix) prefix="$2"; shift 2;;
        --*) shift;;
        *) spec="$1"; shift;;
      esac
    done
    # strip trailing @<version> so the dir matches what production
    # code expects (e.g. node_modules/@scope/name, not @scope/name@1.0.0)
    case "$spec" in
      @*@*) pkg="${spec%@*}" ;;
      *@*)  pkg="${spec%@*}" ;;
      *)    pkg="$spec" ;;
    esac
    mkdir -p "$prefix/node_modules/$pkg/bin"
    cat > "$prefix/node_modules/$pkg/package.json" <<EOF
{"name":"$pkg","version":"1.2.3","bin":{"$pkg":"bin/server.js"}}
EOF
    cat > "$prefix/node_modules/$pkg/bin/server.js" <<EOF
#!/usr/bin/env node
console.log("fake server");
EOF
    chmod +x "$prefix/node_modules/$pkg/bin/server.js"
    ;;
esac
`
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNpxResolver_HappyPath(t *testing.T) {
	binDir := fakeNpmScript(t)
	rootDir := t.TempDir()
	rm := NewResolverManager(rootDir).withExecutor(func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		// Redirect the spawn so we never touch the real npm binary.
		if name == "npm" {
			name = filepath.Join(binDir, "npm")
		}
		cmd := exec.CommandContext(ctx, name, args...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return []byte(stdout.String()), []byte(stderr.String()), err
	})
	res, err := rm.pickResolver(ServerSpec{Transport: TransportStdio, Command: "npx"}).Resolve(context.Background(), ServerSpec{
		ID:        "filesystem",
		Transport: TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusReady {
		t.Fatalf("status = %s, want ready (err=%s)", res.Status, res.Error)
	}
	if res.Command != "node" {
		t.Fatalf("command = %q, want node", res.Command)
	}
	if res.ResolvedVersion != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", res.ResolvedVersion)
	}
	if len(res.Args) < 1 {
		t.Fatal("args empty")
	}
	binPath := res.Args[0]
	if !filepath.IsAbs(binPath) {
		t.Fatalf("bin path not absolute: %s", binPath)
	}
	if filepath.Base(binPath) != "server.js" {
		t.Fatalf("bin path = %s, want */server.js", binPath)
	}
	if len(res.Args) != 2 || res.Args[1] != "/tmp" {
		t.Fatalf("args = %v, want [bin, /tmp]", res.Args)
	}
	// installDir is the per-(server, package) landing point shared
	// across sessions — <rootDir>/<sanitize(server.id)>-<sanitize(pkg.name)>.
	wantInstallDir := filepath.Join(rootDir,
		sanitizeForPath("filesystem")+"-"+sanitizeForPath("@modelcontextprotocol/server-filesystem"))
	if res.InstallDir != wantInstallDir {
		t.Errorf("installDir = %q, want %q", res.InstallDir, wantInstallDir)
	}
	if _, err := os.Stat(filepath.Join(wantInstallDir, "node_modules")); err != nil {
		t.Errorf("expected node_modules under %q: %v", wantInstallDir, err)
	}
}

func TestNpxResolver_NpmViewFails(t *testing.T) {
	rm := NewResolverManager(t.TempDir()).withExecutor(func(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		return nil, nil, errors.New("network down")
	})
	res, err := rm.pickResolver(ServerSpec{Transport: TransportStdio, Command: "npx"}).Resolve(context.Background(), ServerSpec{
		ID:        "x",
		Transport: TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "pkg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if !strings.Contains(res.Error, "npm view") {
		t.Fatalf("error = %q, want npm view prefix", res.Error)
	}
}

func TestNpxResolver_NpmInstallFails(t *testing.T) {
	// view → ok, install → fail. Distinct by first arg.
	rm := NewResolverManager(t.TempDir()).withExecutor(func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "view" {
			return []byte("1.2.3"), nil, nil
		}
		return nil, nil, errors.New("disk full")
	})
	res, err := rm.pickResolver(ServerSpec{Transport: TransportStdio, Command: "npx"}).Resolve(context.Background(), ServerSpec{
		ID:        "x",
		Transport: TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "pkg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if !strings.Contains(res.Error, "npm install") {
		t.Fatalf("error = %q", res.Error)
	}
}

// --- Resolve lifecycle: in-flight dedupe ---

func TestResolverManager_DedupesConcurrentResolve(t *testing.T) {
	// Track how many times the resolver spawns npm view — a second
	// caller during in-flight must reuse the existing task, not start
	// a new one.
	var viewCalls int
	rm := NewResolverManager(t.TempDir()).withExecutor(func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "view" {
			viewCalls++
			time.Sleep(50 * time.Millisecond) // widen the dedup window
			return []byte("1.0.0"), nil, nil
		}
		return nil, nil, errors.New("install not stubbed")
	})
	spec := ServerSpec{ID: "x", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}

	// Issue two Resolves back-to-back; the second lands while the first
	// is still in flight. Both channels should produce a result.
	type pair struct {
		r   LaunchResolution
		err error
	}
	results := make(chan pair, 2)
	go func() {
		ch := rm.Resolve(context.Background(), spec, "fp")
		r := <-ch
		results <- pair{r, nil}
	}()
	go func() {
		ch := rm.Resolve(context.Background(), spec, "fp")
		r := <-ch
		results <- pair{r, nil}
	}()
	p1 := <-results
	p2 := <-results
	if p1.r.ServerID != "x" || p2.r.ServerID != "x" {
		t.Fatalf("server id missing: p1=%+v p2=%+v", p1.r, p2.r)
	}
	// dedup means view runs exactly once even with two callers.
	if viewCalls != 1 {
		t.Fatalf("view invocations = %d, want 1 (dedup should suppress the second)", viewCalls)
	}
}

// --- pickBinEntry ---

func TestPickBinEntry_String(t *testing.T) {
	got, err := pickBinEntry("bin/x", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bin/x" {
		t.Fatalf("got %q", got)
	}
}

func TestPickBinEntry_MapByBasename(t *testing.T) {
	bin := map[string]interface{}{"name": "bin/name.js", "other": "bin/other.js"}
	got, err := pickBinEntry(bin, "@scope/name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bin/name.js" {
		t.Fatalf("got %q, want bin/name.js", got)
	}
}

func TestPickBinEntry_MapFallback(t *testing.T) {
	bin := map[string]interface{}{"x": "bin/x.js"}
	got, err := pickBinEntry(bin, "@scope/name")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bin/x.js" {
		t.Fatalf("got %q", got)
	}
}

func TestPickBinEntry_Nil(t *testing.T) {
	if _, err := pickBinEntry(nil, "pkg"); err == nil {
		t.Fatal("expected error")
	}
}
