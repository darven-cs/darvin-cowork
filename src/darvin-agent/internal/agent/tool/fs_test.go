package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFsTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&readFileTool{sb: sb})
	r.MustRegister(&writeFileTool{sb: sb})
	r.MustRegister(&editFileTool{sb: sb})
	r.MustRegister(&listDirTool{sb: sb})
	return r, root
}

func TestReadWriteEdit(t *testing.T) {
	r, _ := newFsTools(t)
	ctx := context.Background()

	w := r.Get("write_file").(Tool)
	res := w.Execute(ctx, map[string]any{
		"path":    "hello.txt",
		"content": "alpha\nbeta\nalpha\ngamma\n",
	})
	if res.IsError {
		t.Fatalf("write_file: %v", res.Content)
	}

	rf := r.Get("read_file").(Tool)
	res = rf.Execute(ctx, map[string]any{"path": "hello.txt"})
	if res.IsError {
		t.Fatalf("read_file: %v", res.Content)
	}
	if !strings.Contains(res.Content, "alpha") {
		t.Errorf("read_file content missing 'alpha': %q", res.Content)
	}

	ed := r.Get("edit_file").(Tool)
	// first occurrence only by default
	res = ed.Execute(ctx, map[string]any{
		"path":     "hello.txt",
		"old_text": "alpha",
		"new_text": "ALPHA",
	})
	if res.IsError {
		t.Fatalf("edit_file: %v", res.Content)
	}

	// replace_all
	res = ed.Execute(ctx, map[string]any{
		"path":        "hello.txt",
		"old_text":    "ALPHA",
		"new_text":    "X",
		"replace_all": true,
	})
	if res.IsError {
		t.Fatalf("edit_file replace_all: %v", res.Content)
	}

	// missing old_text
	res = ed.Execute(ctx, map[string]any{
		"path":     "hello.txt",
		"old_text": "nope",
		"new_text": "y",
	})
	if !res.IsError {
		t.Errorf("edit_file with missing old_text should error, got %+v", res)
	}
}

func TestReadFileSandboxEscape(t *testing.T) {
	r, _ := newFsTools(t)
	ctx := context.Background()
	rf := r.Get("read_file").(Tool)

	res := rf.Execute(ctx, map[string]any{"path": "/etc/hosts"})
	if !res.IsError {
		t.Error("read_file /etc/hosts should error, got success")
	}
	res = rf.Execute(ctx, map[string]any{"path": "../outside.txt"})
	if !res.IsError {
		t.Error("read_file ../outside.txt should error, got success")
	}
}

func TestListDir(t *testing.T) {
	r, root := newFsTools(t)
	ctx := context.Background()

	// create some files
	for _, p := range []string{"a.txt", "sub/b.txt", "sub/inner/c.txt"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ld := r.Get("list_dir").(Tool)
	// depth 1
	res := ld.Execute(ctx, map[string]any{"path": ".", "max_depth": float64(1)})
	if res.IsError {
		t.Fatalf("list_dir depth 1: %v", res.Content)
	}
	if !strings.Contains(res.Content, "a.txt") || !strings.Contains(res.Content, "sub") {
		t.Errorf("depth 1 missing entries: %q", res.Content)
	}
	if strings.Contains(res.Content, "b.txt") {
		t.Errorf("depth 1 should not recurse into sub/: %q", res.Content)
	}
	// depth 2
	res = ld.Execute(ctx, map[string]any{"path": ".", "max_depth": float64(2)})
	if res.IsError {
		t.Fatalf("list_dir depth 2: %v", res.Content)
	}
	if !strings.Contains(res.Content, "sub/b.txt") {
		t.Errorf("depth 2 missing sub/b.txt: %q", res.Content)
	}
}

func TestListDirNotDirectory(t *testing.T) {
	r, root := newFsTools(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ld := r.Get("list_dir").(Tool)
	res := ld.Execute(ctx, map[string]any{"path": "file.txt"})
	if !res.IsError {
		t.Errorf("list_dir on file should error, got %+v", res)
	}
}
