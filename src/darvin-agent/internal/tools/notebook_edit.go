// Implements the notebook_edit tool: structural Jupyter notebook cell
// editing on .ipynb files (nbformat 4.5+).

package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// notebook and cell mirror the nbformat 4.5+ shape. Metadata / Outputs
// are tolerated as nil so old / partial notebooks do not blow up on read;
// normalizeNotebook rewrites nils back to empty containers before write.
type notebook struct {
	NbFormat      int            `json:"nbformat"`
	NbFormatMinor int            `json:"nbformat_minor"`
	Metadata      map[string]any `json:"metadata"`
	Cells         []cell         `json:"cells"`
}

type cell struct {
	ID             string         `json:"id"`
	CellType       string         `json:"cell_type"`
	Metadata       map[string]any `json:"metadata"`
	Source         []string       `json:"source"`
	Outputs        []any          `json:"outputs,omitempty"`
	ExecutionCount *int           `json:"execution_count,omitempty"`
}

// normalizeNotebook rewrites nil containers to empty ones so the file is
// serialized with {} / [] rather than null. Jupyter tolerates both, but
// null triggers warnings in some downstream tools and breaks naive diff.
func normalizeNotebook(nb *notebook) {
	if nb.Metadata == nil {
		nb.Metadata = map[string]any{}
	}
	for i := range nb.Cells {
		if nb.Cells[i].Metadata == nil {
			nb.Cells[i].Metadata = map[string]any{}
		}
		if nb.Cells[i].CellType == "code" && nb.Cells[i].Outputs == nil {
			nb.Cells[i].Outputs = []any{}
		}
	}
}

// splitSource turns a user-supplied source string into the nbformat
// []string convention. Each line is its own entry; a trailing "\n"
// produces a final empty entry (the conventional nbformat marker for
// "ends with newline").
func splitSource(s string) []string {
	if s == "" {
		return []string{""}
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		lines = append(lines, "")
	}
	return lines
}

// generateCellID returns 8 hex chars (32 bits) — short enough to scan in
// a notebook UI, unique enough for in-file use.
func generateCellID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// notebookEditTool edits Jupyter notebook cells.
type notebookEditTool struct {
	sb *Sandbox
}

func (t *notebookEditTool) Name() string { return "notebook_edit" }
func (t *notebookEditTool) Description() string {
	return "Edit a Jupyter notebook (.ipynb) at the cell level: insert a new code or markdown cell above/below an anchor cell, replace an existing cell's source (outputs / execution_count preserved), or delete a cell. nbformat 4.5+ only."
}
func (t *notebookEditTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":      {Type: "string", Description: "Path to a .ipynb file."},
			"operation": {Type: "string", Enum: []string{"insert", "replace", "delete"}, Description: "Cell operation."},
			"cell_id":   {Type: "string", Description: "Anchor (insert) or target (replace / delete) cell id."},
			"cell_type": {Type: "string", Enum: []string{"code", "markdown"}, Description: "New cell type; required for insert."},
			"source":    {Type: "string", Description: "New cell source; required for insert / replace."},
			"position":  {Type: "string", Enum: []string{"above", "below"}, Description: "Where to insert relative to cell_id; default below."},
		},
		Required:             []string{"path", "operation", "cell_id"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *notebookEditTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	op, _ := args["operation"].(string)
	cellID, _ := args["cell_id"].(string)
	cellType, _ := args["cell_type"].(string)
	source, _ := args["source"].(string)
	position, _ := args["position"].(string)
	if position == "" {
		position = "below"
	}
	if !strings.HasSuffix(path, ".ipynb") {
		return Result{IsError: true, Content: "notebook_edit only supports .ipynb files"}
	}
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{IsError: true, Content: "read: " + err.Error()}
	}
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return Result{IsError: true, Content: "parse: " + err.Error()}
	}
	if nb.NbFormat != 4 || nb.NbFormatMinor < 5 {
		return Result{IsError: true, Content: fmt.Sprintf("notebook format v%d.%d not supported (cell.id requires 4.5+); export to v4.5+ first", nb.NbFormat, nb.NbFormatMinor)}
	}
	normalizeNotebook(&nb)
	switch op {
	case "insert":
		return t.insertCell(&nb, abs, path, cellID, cellType, source, position)
	case "replace":
		return t.replaceCell(&nb, abs, path, cellID, source)
	case "delete":
		return t.deleteCell(&nb, abs, path, cellID)
	}
	return Result{IsError: true, Content: "unknown operation: " + op}
}

// findCell returns the index of the cell with id, or -1.
func findCell(cells []cell, id string) int {
	for i := range cells {
		if cells[i].ID == id {
			return i
		}
	}
	return -1
}

// writeNotebook marshals nb with Jupyter-default indentation and writes to
// abs. Returns an error result on failure; success returns nil.
func writeNotebook(abs string, nb *notebook) error {
	out, err := json.MarshalIndent(nb, "", "    ")
	if err != nil {
		return err
	}
	if len(out) > maxHardWriteBytes {
		return fmt.Errorf("notebook exceeds %d bytes after edit", maxHardWriteBytes)
	}
	return os.WriteFile(abs, out, 0o644)
}

func (t *notebookEditTool) insertCell(nb *notebook, abs, path, anchorID, cellType, source, position string) Result {
	if cellType != "code" && cellType != "markdown" {
		return Result{IsError: true, Content: "operation=insert requires cell_type=code|markdown"}
	}
	if source == "" {
		return Result{IsError: true, Content: "operation=insert requires source"}
	}
	idx := findCell(nb.Cells, anchorID)
	if idx < 0 {
		return Result{IsError: true, Content: fmt.Sprintf("anchor cell %q not found", anchorID)}
	}
	newCell := cell{
		ID:       generateCellID(),
		CellType: cellType,
		Source:   splitSource(source),
		Metadata: map[string]any{},
	}
	if cellType == "code" {
		newCell.Outputs = []any{}
	}
	insertAt := idx + 1
	if position == "above" {
		insertAt = idx
	}
	nb.Cells = append(nb.Cells[:insertAt], append([]cell{newCell}, nb.Cells[insertAt:]...)...)
	if err := writeNotebook(abs, nb); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("notebook_edit: insert %s cell %q in %s\n  position: %s %q (new idx %d)\n  source:   %d line(s)",
		cellType, newCell.ID, path, position, anchorID, insertAt, len(newCell.Source))}
}

func (t *notebookEditTool) replaceCell(nb *notebook, abs, path, cellID, source string) Result {
	if source == "" {
		return Result{IsError: true, Content: "operation=replace requires source"}
	}
	idx := findCell(nb.Cells, cellID)
	if idx < 0 {
		return Result{IsError: true, Content: fmt.Sprintf("cell %q not found", cellID)}
	}
	oldLines := len(nb.Cells[idx].Source)
	nb.Cells[idx].Source = splitSource(source)
	if err := writeNotebook(abs, nb); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("notebook_edit: replace cell %q in %s\n  kind:     %s (outputs/execution_count preserved)\n  source:   was %d line(s), now %d line(s)",
		cellID, path, nb.Cells[idx].CellType, oldLines, len(nb.Cells[idx].Source))}
}

func (t *notebookEditTool) deleteCell(nb *notebook, abs, path, cellID string) Result {
	idx := findCell(nb.Cells, cellID)
	if idx < 0 {
		return Result{IsError: true, Content: fmt.Sprintf("cell %q not found", cellID)}
	}
	nb.Cells = append(nb.Cells[:idx], nb.Cells[idx+1:]...)
	if err := writeNotebook(abs, nb); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("notebook_edit: delete cell %q in %s\n  cells:    %d → %d",
		cellID, path, len(nb.Cells)+1, len(nb.Cells))}
}

func init() {
	RegisterBuiltinFactory("notebook_edit", func(cfg BuiltinConfig) (Tool, error) {
		return &notebookEditTool{sb: cfg.Sandbox}, nil
	})
}
