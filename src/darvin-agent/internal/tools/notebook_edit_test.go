// Tests for the notebook_edit tool (insert / replace / delete) on
// .ipynb files at nbformat 4.5+.

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newNotebookEditTools(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	sb, err := newFsSandbox(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.MustRegister(&writeFileTool{sb: sb})
	r.MustRegister(&notebookEditTool{sb: sb})
	return r, root
}

// notebookFixture is a minimal nbformat 4.5 notebook with one code and
// one markdown cell, plus pre-populated outputs / execution_count.
const notebookFixture = `{
  "cells": [
    {
      "cell_type": "code",
      "id": "cell-1",
      "metadata": {},
      "source": ["print('hello')\n"],
      "outputs": [{"output_type": "stream", "name": "stdout", "text": ["hello\n"]}],
      "execution_count": 1
    },
    {
      "cell_type": "markdown",
      "id": "cell-2",
      "metadata": {},
      "source": ["# Title\n"]
    }
  ],
  "metadata": {
    "kernelspec": {"name": "python3", "display_name": "Python 3"},
    "language_info": {"name": "python"}
  },
  "nbformat": 4,
  "nbformat_minor": 5
}
`

func writeNotebookFixture(t *testing.T, r *Registry, path, content string) {
	t.Helper()
	if res := r.Get("write_file").Execute(context.Background(), map[string]any{"path": path, "content": content}); res.IsError {
		t.Fatal(res.Content)
	}
}

func readNotebookFile(t *testing.T, root, rel string) *notebook {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var nb notebook
	if err := json.Unmarshal(b, &nb); err != nil {
		t.Fatal(err)
	}
	return &nb
}

func TestNotebookEditInsertCodeBelow(t *testing.T) {
	r, root := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "n.ipynb", notebookFixture)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "insert",
		"cell_id":   "cell-1",
		"cell_type": "code",
		"source":    "x = 1\ny = 2",
		"position":  "below",
	})
	if res.IsError {
		t.Fatalf("insert: %v", res.Content)
	}
	nb := readNotebookFile(t, root, "n.ipynb")
	if len(nb.Cells) != 3 {
		t.Fatalf("cells: want 3, got %d", len(nb.Cells))
	}
	inserted := nb.Cells[1]
	if inserted.CellType != "code" {
		t.Errorf("inserted cell type: want code, got %q", inserted.CellType)
	}
	if inserted.ID == "" || inserted.ID == "cell-1" {
		t.Errorf("inserted cell id should be auto-generated and unique: %q", inserted.ID)
	}
	if len(inserted.Outputs) != 0 {
		t.Errorf("fresh code cell outputs should be empty: %v", inserted.Outputs)
	}
	if inserted.ExecutionCount != nil {
		t.Errorf("fresh code cell execution_count should be nil: %v", inserted.ExecutionCount)
	}
	if len(inserted.Source) != 2 {
		t.Errorf("source split: want 2 lines, got %d (%q)", len(inserted.Source), inserted.Source)
	}
}

func TestNotebookEditInsertMarkdownAbove(t *testing.T) {
	r, root := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "n.ipynb", notebookFixture)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "insert",
		"cell_id":   "cell-2",
		"cell_type": "markdown",
		"source":    "intro",
		"position":  "above",
	})
	if res.IsError {
		t.Fatalf("insert: %v", res.Content)
	}
	nb := readNotebookFile(t, root, "n.ipynb")
	if nb.Cells[1].CellType != "markdown" {
		t.Errorf("inserted cell should be markdown: %q", nb.Cells[1].CellType)
	}
}

func TestNotebookEditReplacePreservesOutputs(t *testing.T) {
	r, root := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "n.ipynb", notebookFixture)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "replace",
		"cell_id":   "cell-1",
		"source":    "print('goodbye')\n",
	})
	if res.IsError {
		t.Fatalf("replace: %v", res.Content)
	}
	nb := readNotebookFile(t, root, "n.ipynb")
	if len(nb.Cells) != 2 {
		t.Fatalf("replace should not change cell count: got %d", len(nb.Cells))
	}
	cell := nb.Cells[0]
	if cell.CellType != "code" {
		t.Errorf("cell type preserved? got %q", cell.CellType)
	}
	if len(cell.Outputs) == 0 {
		t.Errorf("outputs should be preserved")
	}
	if cell.ExecutionCount == nil || *cell.ExecutionCount != 1 {
		t.Errorf("execution_count should be preserved: %v", cell.ExecutionCount)
	}
	if !strings.Contains(strings.Join(cell.Source, ""), "goodbye") {
		t.Errorf("source not updated: %q", cell.Source)
	}
}

func TestNotebookEditDelete(t *testing.T) {
	r, root := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "n.ipynb", notebookFixture)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "delete",
		"cell_id":   "cell-2",
	})
	if res.IsError {
		t.Fatalf("delete: %v", res.Content)
	}
	nb := readNotebookFile(t, root, "n.ipynb")
	if len(nb.Cells) != 1 {
		t.Errorf("cell count after delete: want 1, got %d", len(nb.Cells))
	}
	if nb.Cells[0].ID != "cell-1" {
		t.Errorf("surviving cell id wrong: %q", nb.Cells[0].ID)
	}
}

func TestNotebookEditNormalizesNilContainers(t *testing.T) {
	r, root := newNotebookEditTools(t)
	// file with intentionally missing metadata / outputs
	src := `{"cells":[{"cell_type":"code","id":"a","metadata":null,"source":["x\n"],"outputs":null,"execution_count":null}],"metadata":null,"nbformat":4,"nbformat_minor":5}`
	writeNotebookFixture(t, r, "n.ipynb", src)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "replace",
		"cell_id":   "a",
		"source":    "y = 2\n",
	})
	if res.IsError {
		t.Fatalf("replace on sparse file: %v", res.Content)
	}
	b, err := os.ReadFile(filepath.Join(root, "n.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"metadata":null`) || strings.Contains(string(b), `"outputs":null`) {
		t.Errorf("normalized output should not contain null containers: %s", b)
	}
}

func TestNotebookEditRejectsNonIPYNB(t *testing.T) {
	r, _ := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "f.txt", "x")
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "f.txt",
		"operation": "delete",
		"cell_id":   "anything",
	})
	if !res.IsError || !strings.Contains(res.Content, ".ipynb") {
		t.Errorf("non-.ipynb should fail: %q", res.Content)
	}
}

func TestNotebookEditRejectsOldFormat(t *testing.T) {
	r, _ := newNotebookEditTools(t)
	src := `{"cells":[],"metadata":{},"nbformat":4,"nbformat_minor":3}`
	writeNotebookFixture(t, r, "n.ipynb", src)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "delete",
		"cell_id":   "x",
	})
	if !res.IsError || !strings.Contains(res.Content, "4.5+") {
		t.Errorf("old format should fail: %q", res.Content)
	}
}

func TestNotebookEditCellNotFound(t *testing.T) {
	r, _ := newNotebookEditTools(t)
	writeNotebookFixture(t, r, "n.ipynb", notebookFixture)
	res := r.Get("notebook_edit").Execute(context.Background(), map[string]any{
		"path":      "n.ipynb",
		"operation": "delete",
		"cell_id":   "missing",
	})
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("missing cell should fail: %q", res.Content)
	}
}
