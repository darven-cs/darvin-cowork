// Tests for the grep and glob search tools.

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSearchTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root, DefaultPathExclusions()...)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&grepTool{sb: sb})
	r.MustRegister(&globTool{sb: sb})
	return r, root
}

func TestGrepSingleFile(t *testing.T) {
	r, root := newSearchTools(t)
	writeTestFile(t, root, "hello.txt", "alpha\nbeta\nalpha\n")
	ctx := context.Background()

	res := r.Get("grep").Execute(ctx, map[string]any{
		"pattern": "alpha",
		"path":    "hello.txt",
	})
	if res.IsError {
		t.Fatalf("grep: %v", res.Content)
	}
	if !strings.Contains(res.Content, "hello.txt:1:alpha") || !strings.Contains(res.Content, "hello.txt:3:alpha") {
		t.Errorf("grep output wrong: %q", res.Content)
	}
	if strings.Contains(res.Content, "beta") {
		t.Errorf("grep matched non-matching line: %q", res.Content)
	}
}

func TestGrepNoMatch(t *testing.T) {
	r, root := newSearchTools(t)
	writeTestFile(t, root, "a.txt", "hello\n")
	res := r.Get("grep").Execute(context.Background(), map[string]any{
		"pattern": "zzz",
		"path":    "a.txt",
	})
	if res.IsError || res.Content != "(no matches)" {
		t.Errorf("grep no-match: %q isError=%v", res.Content, res.IsError)
	}
}

func TestGrepRecursiveAndSkipsHiddenExcluded(t *testing.T) {
	r, root := newSearchTools(t)
	writeTestFile(t, root, "src/a.go", "package main\n// needle\n")
	writeTestFile(t, root, ".hidden/h.go", "needle\n")
	writeTestFile(t, root, "node_modules/x.go", "needle\n")
	res := r.Get("grep").Execute(context.Background(), map[string]any{
		"pattern": "needle",
	})
	if res.IsError {
		t.Fatalf("grep: %v", res.Content)
	}
	if !strings.Contains(res.Content, "src/a.go") {
		t.Errorf("expected match in src/a.go: %q", res.Content)
	}
	if strings.Contains(res.Content, ".hidden") || strings.Contains(res.Content, "node_modules") {
		t.Errorf("grep did not skip hidden/excluded dirs: %q", res.Content)
	}
}

func TestGrepMaxMatches(t *testing.T) {
	r, root := newSearchTools(t)
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("line\n")
	}
	writeTestFile(t, root, "many.txt", sb.String())
	res := r.Get("grep").Execute(context.Background(), map[string]any{
		"pattern":     "line",
		"path":        "many.txt",
		"max_matches": float64(10),
	})
	if res.IsError {
		t.Fatalf("grep: %v", res.Content)
	}
	if !strings.Contains(res.Content, "truncated at 10 matches") {
		t.Errorf("grep did not truncate: %q", res.Content)
	}
	if n := len(strings.Split(res.Content, "\n")); n != 11 { // 10 matches + truncation marker
		t.Errorf("grep match count wrong (want 11 lines, got %d): %q", n, res.Content)
	}
}

func TestGlobRecursive(t *testing.T) {
	r, root := newSearchTools(t)
	writeTestFile(t, root, "a.go", "x")
	writeTestFile(t, root, "src/b.go", "x")
	writeTestFile(t, root, "src/deep/c.go", "x")
	writeTestFile(t, root, "src/readme.md", "x")
	res := r.Get("glob").Execute(context.Background(), map[string]any{
		"pattern": "**/*.go",
	})
	if res.IsError {
		t.Fatalf("glob: %v", res.Content)
	}
	for _, want := range []string{"a.go", "src/b.go", "src/deep/c.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("glob missing %s: %q", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "readme.md") {
		t.Errorf("glob matched non-.go: %q", res.Content)
	}
}

func TestGlobSimplePattern(t *testing.T) {
	r, root := newSearchTools(t)
	writeTestFile(t, root, "top.go", "x")
	writeTestFile(t, root, "sub/top.go", "x")
	res := r.Get("glob").Execute(context.Background(), map[string]any{
		"pattern": "*.go",
	})
	if res.IsError {
		t.Fatalf("glob: %v", res.Content)
	}
	if !strings.Contains(res.Content, "top.go") {
		t.Errorf("glob missing top-level: %q", res.Content)
	}
	if strings.Contains(res.Content, "sub/top.go") {
		t.Errorf("glob * crossed separator: %q", res.Content)
	}
}

func TestGlobNoMatch(t *testing.T) {
	r, _ := newSearchTools(t)
	res := r.Get("glob").Execute(context.Background(), map[string]any{
		"pattern": "**/*.xyz",
	})
	if res.IsError || res.Content != "(no matches)" {
		t.Errorf("glob no-match: %q isError=%v", res.Content, res.IsError)
	}
}
