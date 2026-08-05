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

func TestReadFileOffset(t *testing.T) {
	r, root := newFsTools(t)
	ctx := context.Background()
	// 150 bytes so offset 100 + limit 50 ends exactly at EOF (no truncation).
	content := strings.Repeat("0123456789", 15)
	if err := os.WriteFile(filepath.Join(root, "off.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := r.Get("read_file").(Tool)
	res := rf.Execute(ctx, map[string]any{"path": "off.txt", "offset": float64(100), "limit": float64(50)})
	if res.IsError {
		t.Fatalf("read_file offset: %v", res.Content)
	}
	if want := content[100:150]; res.Content != want {
		t.Errorf("offset read = %q, want %q", res.Content, want)
	}
	// offset with no limit should still honor the offset (not restart at 0)
	res = rf.Execute(ctx, map[string]any{"path": "off.txt", "offset": float64(100)})
	if res.IsError {
		t.Fatalf("read_file offset-only: %v", res.Content)
	}
	if want := content[100:]; res.Content != want {
		t.Errorf("offset-only read = %q, want %q", res.Content, want)
	}
}

func TestReadFileMaxBytesTruncation(t *testing.T) {
	r, root := newFsTools(t)
	ctx := context.Background()
	big := make([]byte, maxReadBytes+4096)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "huge.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	rf := r.Get("read_file").(Tool)
	res := rf.Execute(ctx, map[string]any{"path": "huge.txt"})
	if res.IsError {
		t.Fatalf("read_file huge: %v", res.Content)
	}
	if !strings.Contains(res.Content, "[truncated at offset 0") {
		t.Errorf("truncation note missing in content of len %d", len(res.Content))
	}
}

func TestReadFileSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "leak")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	r := NewRegistry()
	r.MustRegister(&readFileTool{sb: sb})
	res := r.Get("read_file").(Tool).Execute(context.Background(), map[string]any{"path": "leak"})
	// an out-of-workspace read now surfaces as ErrNeedsPermission
	// ("authorized roots") instead of a bare symlink-escape; both are hard
	// errors at the tool layer (approval gating happens in the executor).
	if !res.IsError || !(strings.Contains(res.Content, "escapes sandbox") || strings.Contains(res.Content, "authorized roots")) {
		t.Errorf("read_file symlink escape = %+v, want sandbox error", res)
	}
}

func TestWriteFileContentTooLarge(t *testing.T) {
	r, _ := newFsTools(t)
	big := strings.Repeat("a", maxHardWriteBytes+1)
	res := r.Get("write_file").(Tool).Execute(context.Background(), map[string]any{
		"path":    "x.txt",
		"content": big,
	})
	if !res.IsError {
		t.Error("write_file with content over maxHardWriteBytes should be rejected")
	}
}
