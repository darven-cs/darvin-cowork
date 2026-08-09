// Tests for the code_index tool (outline / search / info) and the
// invalidateCodeIndex hooks on the write tools.

package tool

import (
	"context"
	"strings"
	"testing"
)

func newCodeIndexTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&codeIndexTool{sb: sb})
	r.MustRegister(&writeFileTool{sb: sb})
	return r, root
}

const codeIndexFixture = `// Package foo is a fixture.
package foo

import "fmt"

// Greet says hello.
func Greet() string {
	return "hi"
}

func helper() {}

// Point is a 2D point.
type Point struct {
	X, Y int
}

type Shape interface {
	Area() float64
}

const (
	MaxCount = 10
	MinCount = 1
)

func (p *Point) XPlus() int {
	return p.X + 1
}

func (p Point) YMinus() int {
	return p.Y - 1
}

var version = "1.0"
`

func TestCodeIndexOutline(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{
		"action": "outline",
		"path":   "foo.go",
	})
	if res.IsError {
		t.Fatalf("code_index outline: %v", res.Content)
	}
	got := res.Content
	for _, want := range []string{"Greet", "helper", "Point", "Shape", "MaxCount", "MinCount", "XPlus", "YMinus", "version"} {
		if !strings.Contains(got, want) {
			t.Errorf("outline missing %s: %q", want, got)
		}
	}
	if !strings.Contains(got, "method") {
		t.Errorf("outline should classify methods: %q", got)
	}
	if !strings.Contains(got, "type") {
		t.Errorf("outline should classify types: %q", got)
	}
}

func TestCodeIndexOutlineEmpty(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "empty.go", "content": "package x\n"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "outline", "path": "empty.go"})
	if res.IsError {
		t.Fatalf("code_index outline: %v", res.Content)
	}
	if res.Content != "(no symbols)" {
		t.Errorf("empty file should report no symbols, got %q", res.Content)
	}
}

func TestCodeIndexSearch(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{
		"action": "search",
		"query":  "nt", // matches Point, MaxCount, MinCount (case-insensitive substring)
	})
	if res.IsError {
		t.Fatalf("code_index search: %v", res.Content)
	}
	for _, want := range []string{"Point", "MaxCount", "MinCount"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("search should find %s: %q", want, res.Content)
		}
	}
}

func TestCodeIndexSearchKindFilter(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{
		"action": "search",
		"query":  "Plus",
		"kind":   "method",
	})
	if res.IsError {
		t.Fatalf("code_index search: %v", res.Content)
	}
	if !strings.Contains(res.Content, "XPlus") {
		t.Errorf("kind=method should keep methods: %q", res.Content)
	}
}

func TestCodeIndexSearchNoMatch(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "search", "query": "NothingMatchesThis"})
	if res.IsError || res.Content != "(no matches)" {
		t.Errorf("no-match: %q isError=%v", res.Content, res.IsError)
	}
}

func TestCodeIndexInfoTopLevel(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "info", "query": "Greet"})
	if res.IsError {
		t.Fatalf("code_index info: %v", res.Content)
	}
	for _, want := range []string{"package: foo", "kind:    func", "line:", "Greet says hello"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("info missing %q: %q", want, res.Content)
		}
	}
}

func TestCodeIndexInfoMethodWithReceiver(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "info", "query": "*Point.XPlus"})
	if res.IsError {
		t.Fatalf("code_index info: %v", res.Content)
	}
	if !strings.Contains(res.Content, "kind:    method") || !strings.Contains(res.Content, "recv:    Point") {
		t.Errorf("method info wrong: %q", res.Content)
	}
}

func TestCodeIndexInfoNotFound(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": codeIndexFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "info", "query": "Missing"})
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("missing symbol should fail: %q isError=%v", res.Content, res.IsError)
	}
}

func TestCodeIndexInvalidatedByWrite(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	v1 := "package foo\n\nfunc Old() {}\n"
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": v1}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "search", "query": "Old"})
	if !strings.Contains(res.Content, "Old") {
		t.Fatalf("expected Old in initial search: %q", res.Content)
	}
	v2 := "package foo\n\nfunc Renamed() {}\n"
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "foo.go", "content": v2}); res.IsError {
		t.Fatal(res.Content)
	}
	res = r.Get("code_index").Execute(ctx, map[string]any{"action": "search", "query": "Old"})
	if !strings.Contains(res.Content, "(no matches)") {
		t.Errorf("after rewrite, stale Old should be gone: %q", res.Content)
	}
	res = r.Get("code_index").Execute(ctx, map[string]any{"action": "search", "query": "Renamed"})
	if !strings.Contains(res.Content, "Renamed") {
		t.Errorf("after rewrite, Renamed should appear: %q", res.Content)
	}
}

func TestCodeIndexRejectsNonGo(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "outline", "path": "foo.txt"})
	if !res.IsError || !strings.Contains(res.Content, ".go") {
		t.Errorf("non-.go outline should fail: %q", res.Content)
	}
}

func TestCodeIndexEscapeRejected(t *testing.T) {
	r, _ := newCodeIndexTools(t)
	ctx := context.Background()
	res := r.Get("code_index").Execute(ctx, map[string]any{"action": "outline", "path": "../etc/passwd"})
	if !res.IsError {
		t.Errorf("outside-workspace outline should fail: %q isError=%v", res.Content, res.IsError)
	}
}
