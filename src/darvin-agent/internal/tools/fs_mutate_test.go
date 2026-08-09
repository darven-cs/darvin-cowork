// Tests for move_file / multi_edit / delete_range and the shared applyEdits.

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newMutateTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&writeFileTool{sb: sb})
	r.MustRegister(&editFileTool{sb: sb})
	r.MustRegister(&moveFileTool{sb: sb})
	r.MustRegister(&multiEditTool{sb: sb})
	r.MustRegister(&deleteRangeTool{sb: sb})
	return r, root
}

func readFileContent(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMoveFile(t *testing.T) {
	r, root := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "a.txt", "content": "hello\n"}); res.IsError {
		t.Fatalf("write: %v", res.Content)
	}
	res := r.Get("move_file").Execute(ctx, map[string]any{
		"source_path":      "a.txt",
		"destination_path": "sub/b.txt",
	})
	if res.IsError {
		t.Fatalf("move_file: %v", res.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("source still exists after move")
	}
	if got := readFileContent(t, root, "sub/b.txt"); got != "hello\n" {
		t.Errorf("moved content wrong: %q", got)
	}
}

func TestMoveFileToExistingRejected(t *testing.T) {
	r, _ := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "a.txt", "content": "a"}); res.IsError {
		t.Fatal(res.Content)
	}
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "b.txt", "content": "b"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("move_file").Execute(ctx, map[string]any{"source_path": "a.txt", "destination_path": "b.txt"})
	if !res.IsError || !strings.Contains(res.Content, "already exists") {
		t.Errorf("move onto existing should fail: %q", res.Content)
	}
}

func TestMultiEditAtomic(t *testing.T) {
	r, root := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "f.go", "content": "one\ntwo\nthree\n"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("multi_edit").Execute(ctx, map[string]any{
		"path": "f.go",
		"edits": []any{
			map[string]any{"old_text": "one", "new_text": "ONE"},
			map[string]any{"old_text": "three", "new_text": "THREE"},
		},
	})
	if res.IsError {
		t.Fatalf("multi_edit: %v", res.Content)
	}
	if got := readFileContent(t, root, "f.go"); got != "ONE\ntwo\nTHREE\n" {
		t.Errorf("multi_edit result wrong: %q", got)
	}
}

func TestMultiEditFailureLeavesFileUntouched(t *testing.T) {
	r, root := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "f.go", "content": "aaa\n"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("multi_edit").Execute(ctx, map[string]any{
		"path": "f.go",
		"edits": []any{
			map[string]any{"old_text": "aaa", "new_text": "bbb"},
			map[string]any{"old_text": "zzz", "new_text": "yyy"},
		},
	})
	if !res.IsError {
		t.Fatalf("second edit should fail: %q", res.Content)
	}
	if !strings.Contains(res.Content, "edit 2") {
		t.Errorf("error should name failing edit: %q", res.Content)
	}
	if got := readFileContent(t, root, "f.go"); got != "aaa\n" {
		t.Errorf("file modified on failed multi_edit: %q", got)
	}
}

func TestDeleteRangeByAnchors(t *testing.T) {
	r, root := newMutateTools(t)
	ctx := context.Background()
	content := "keep1\n\ndelete me\nmiddle\nend me\n\nkeep2\n"
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "f.txt", "content": content}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_range").Execute(ctx, map[string]any{
		"path":       "f.txt",
		"start_text": "delete me",
		"end_text":   "end me",
	})
	if res.IsError {
		t.Fatalf("delete_range: %v", res.Content)
	}
	if got := readFileContent(t, root, "f.txt"); got != "keep1\n\n\nkeep2\n" {
		t.Errorf("delete_range result wrong: %q", got)
	}
	if !strings.Contains(res.Content, "deleted 3 line(s)") {
		t.Errorf("missing count: %q", res.Content)
	}
	if !strings.Contains(res.Content, "-delete me") || !strings.Contains(res.Content, "-end me") {
		t.Errorf("unified diff missing removed lines: %q", res.Content)
	}
}

func TestDeleteRangeAmbiguousAnchor(t *testing.T) {
	r, _ := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "f.txt", "content": "dup\ndup\n"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_range").Execute(ctx, map[string]any{"path": "f.txt", "start_text": "dup", "end_text": "dup"})
	if !res.IsError || !strings.Contains(res.Content, "must match exactly one") {
		t.Errorf("ambiguous anchor should fail: %q", res.Content)
	}
}

func TestDeleteRangeOrdering(t *testing.T) {
	r, _ := newMutateTools(t)
	ctx := context.Background()
	if res := r.Get("write_file").Execute(ctx, map[string]any{"path": "f.txt", "content": "a\nb\n"}); res.IsError {
		t.Fatal(res.Content)
	}
	res := r.Get("delete_range").Execute(ctx, map[string]any{"path": "f.txt", "start_text": "b", "end_text": "a"})
	if !res.IsError || !strings.Contains(res.Content, "before start_text") {
		t.Errorf("reversed anchors should fail: %q", res.Content)
	}
}

func TestApplyEditsReplaceAll(t *testing.T) {
	out, n, err := applyEdits([]byte("x x x"), []editSpec{{OldText: "x", NewText: "y", ReplaceAll: true}}, "f")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "y y y" || n != 3 {
		t.Errorf("applyEdits replace_all wrong: %q n=%d", out, n)
	}
}
