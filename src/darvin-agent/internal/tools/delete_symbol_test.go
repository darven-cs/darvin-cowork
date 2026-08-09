// Tests for the AST-based delete_symbol tool.

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDeleteSymbolTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&writeFileTool{sb: sb})
	r.MustRegister(&deleteSymbolTool{sb: sb})
	return r, root
}

const deleteSymbolFixture = `// Package foo is a fixture.
package foo

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

var version = "1.0"

func (p Point) XPlus() int {
	return p.X + 1
}
`

func TestDeleteSymbolFuncWithDoc(t *testing.T) {
	r, root := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": deleteSymbolFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "Greet", "kind": "func"})
	if res.IsError {
		t.Fatalf("delete_symbol: %v", res.Content)
	}
	got := readFileContent(t, root, "foo.go")
	if strings.Contains(got, "Greet") || strings.Contains(got, "says hello") {
		t.Errorf("func+doc not fully deleted:\n%s", got)
	}
	if !strings.Contains(got, "func helper()") {
		t.Errorf("unrelated func deleted:\n%s", got)
	}
}

func TestDeleteSymbolType(t *testing.T) {
	r, root := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": deleteSymbolFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "Point", "kind": "type"})
	if res.IsError {
		t.Fatalf("delete_symbol: %v", res.Content)
	}
	got := readFileContent(t, root, "foo.go")
	if strings.Contains(got, "type Point struct") || strings.Contains(got, "// Point is a 2D point") {
		t.Errorf("type or its doc not deleted:\n%s", got)
	}
	if !strings.Contains(got, "type Shape interface") {
		t.Errorf("unrelated type deleted:\n%s", got)
	}
}

func TestDeleteSymbolMethod(t *testing.T) {
	r, root := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": deleteSymbolFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "XPlus", "kind": "method"})
	if res.IsError {
		t.Fatalf("delete_symbol: %v", res.Content)
	}
	if got := readFileContent(t, root, "foo.go"); strings.Contains(got, "XPlus") {
		t.Errorf("method not deleted:\n%s", got)
	}
}

func TestDeleteSymbolConstFromGroup(t *testing.T) {
	r, root := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": deleteSymbolFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "MaxCount", "kind": "const"})
	if res.IsError {
		t.Fatalf("delete_symbol: %v", res.Content)
	}
	got := readFileContent(t, root, "foo.go")
	if strings.Contains(got, "MaxCount") {
		t.Errorf("const not deleted:\n%s", got)
	}
	if !strings.Contains(got, "MinCount") {
		t.Errorf("sibling const removed:\n%s", got)
	}
}

func TestDeleteSymbolSharedNamesRejected(t *testing.T) {
	r, _ := newDeleteSymbolTools(t)
	src := "// Package a.\npackage a\n\nconst SharedA, SharedB = 1, 2\n"
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": src}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "SharedA", "kind": "const"})
	if !res.IsError || !strings.Contains(res.Content, "shares a declaration") {
		t.Errorf("shared-names const should be rejected: %q", res.Content)
	}
}

func TestDeleteSymbolNonGoRejected(t *testing.T) {
	r, _ := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "f.txt", "content": "text"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "f.txt", "name": "x"})
	if !res.IsError || !strings.Contains(res.Content, "only supports .go") {
		t.Errorf("non-go should be rejected: %q", res.Content)
	}
}

func TestDeleteSymbolNotFound(t *testing.T) {
	r, _ := newDeleteSymbolTools(t)
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "foo.go", "content": deleteSymbolFixture}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "foo.go", "name": "Missing"})
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("missing symbol should fail: %q", res.Content)
	}
}

func TestDeleteSymbolWholeFile(t *testing.T) {
	r, root := newDeleteSymbolTools(t)
	src := "// Package a.\npackage a\n\nfunc Only() {}\n"
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": "only.go", "content": src}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_symbol").Execute(context.Background(), map[string]any{"path": "only.go", "name": "Only"})
	if res.IsError {
		t.Fatalf("delete_symbol: %v", res.Content)
	}
	b, err := os.ReadFile(filepath.Join(root, "only.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "package a") {
		t.Errorf("package decl removed: %q", string(b))
	}
	if strings.Contains(string(b), "Only") {
		t.Errorf("func still present: %q", string(b))
	}
}
