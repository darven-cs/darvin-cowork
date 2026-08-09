// Implements the built-in file read, write, edit, and list tools.

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"darvin-cowork/backend/internal/llm"
)

// maxReadBytes is the default read_file window. Anything beyond is truncated
// and the truncation is noted in the returned content.
const maxReadBytes = 1 << 20 // 1 MiB

// maxHardWriteBytes caps a single write_file / edit_file payload so a model
// cannot push a huge string through the tool boundary.
const maxHardWriteBytes = 32 << 20 // 32 MiB

// readFileTool reads a UTF-8 text file, optionally limited to a window.
type readFileTool struct {
	sb *Sandbox
}

func (t *readFileTool) Name() string { return "read_file" }
func (t *readFileTool) Description() string {
	return "Read the contents of a UTF-8 text file. Optional limit/offset slice large files."
}
func (t *readFileTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":   {Type: "string", Description: "Path relative to the workspace, or absolute within the workspace."},
			"limit":  {Type: "integer", Description: "Maximum number of bytes to return (default 1 MiB, hard cap 16 MiB).", Minimum: ptrFloat64(0), Maximum: ptrFloat64(maxHardReadBytes)},
			"offset": {Type: "integer", Description: "Byte offset to start reading from.", Minimum: ptrFloat64(0)},
		},
		Required:             []string{"path"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *readFileTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	limit := maxReadBytes
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	offset := int64(0)
	if v, ok := args["offset"].(float64); ok && v > 0 {
		offset = int64(v)
	}
	// ResolveRead: workspace root first, then the run's granted-read set
	// (user-attached absolute paths). Anything else → ErrNeedsPermission.
	abs, err := t.sb.ResolveRead(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	f, data, truncated, err := t.sb.openFileLimitedAt(abs, "read_file", offset, int64(limit))
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	defer f.Close()
	content := utf8ValidString(data)
	if truncated {
		content += fmt.Sprintf("\n[truncated at offset %d, limit %d bytes]", offset, limit)
	}
	return Result{Content: content}
}

// writeFileTool writes a UTF-8 text file, overwriting any existing content.
type writeFileTool struct {
	sb *Sandbox
}

func (t *writeFileTool) Name() string { return "write_file" }
func (t *writeFileTool) Description() string {
	return "Write UTF-8 text content to a file, overwriting any existing content. Creates parent directories as needed."
}
func (t *writeFileTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":    {Type: "string", Description: "Destination path."},
			"content": {Type: "string", Description: "UTF-8 text content to write.", MaxLength: ptrInt(maxHardWriteBytes)},
		},
		Required:             []string{"path", "content"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *writeFileTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{IsError: true, Content: "mkdir: " + err.Error()}
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}
}

// editFileTool performs textual find-and-replace on a file.
type editFileTool struct {
	sb *Sandbox
}

func (t *editFileTool) Name() string { return "edit_file" }
func (t *editFileTool) Description() string {
	return "Find old_text in a file and replace it with new_text. By default replaces the first occurrence; set replace_all=true to replace every occurrence."
}
func (t *editFileTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":        {Type: "string", Description: "File to edit."},
			"old_text":    {Type: "string", Description: "Existing text to find.", MaxLength: ptrInt(maxHardWriteBytes)},
			"new_text":    {Type: "string", Description: "Replacement text.", MaxLength: ptrInt(maxHardWriteBytes)},
			"replace_all": {Type: "boolean", Description: "Replace every occurrence (default false)."},
		},
		Required:             []string{"path", "old_text", "new_text"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *editFileTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	replaceAll, _ := args["replace_all"].(bool)

	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{IsError: true, Content: "read: " + err.Error()}
	}
	updated, count, err := applyEdits(data, []editSpec{{OldText: oldText, NewText: newText, ReplaceAll: replaceAll}}, path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if err := os.WriteFile(abs, updated, 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("replaced %d occurrence(s) in %s", count, path)}
}

// listDirTool lists entries under a directory.
type listDirTool struct {
	sb *Sandbox
}

func (t *listDirTool) Name() string { return "list_dir" }
func (t *listDirTool) Description() string {
	return "List immediate entries under a directory. Optional max_depth walks sub-directories up to N levels (1-based, 1 = immediate children)."
}
func (t *listDirTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":      {Type: "string", Description: "Directory to list."},
			"max_depth": {Type: "integer", Description: "Recursion depth; default 1 (no recursion).", Minimum: ptrFloat64(0), Maximum: ptrFloat64(20)},
		},
		Required:             []string{"path"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *listDirTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	maxDepth := 1
	if v, ok := args["max_depth"].(float64); ok && v > 0 {
		maxDepth = int(v)
	}
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Result{IsError: true, Content: "stat: " + err.Error()}
	}
	if !info.IsDir() {
		return Result{IsError: true, Content: path + " is not a directory"}
	}
	var buf bytes.Buffer
	if err := walkDir(&buf, abs, abs, 0, maxDepth); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if buf.Len() == 0 {
		return Result{Content: "(empty directory)"}
	}
	return Result{Content: buf.String()}
}

func walkDir(buf *bytes.Buffer, root, current string, depth, maxDepth int) error {
	entries, err := os.ReadDir(current)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", current, err)
	}
	for _, e := range entries {
		rel, _ := filepath.Rel(root, filepath.Join(current, e.Name()))
		typ := "file"
		var size int64
		if e.IsDir() {
			typ = "dir"
		} else {
			info, ierr := e.Info()
			if ierr == nil {
				size = info.Size()
			}
		}
		fmt.Fprintf(buf, "%s\t%s\t%d\n", typ, rel, size)
		if e.IsDir() && depth+1 < maxDepth {
			if err := walkDir(buf, root, filepath.Join(current, e.Name()), depth+1, maxDepth); err != nil {
				return err
			}
		}
	}
	return nil
}

// utf8ValidString converts bytes to string, trimming trailing bytes that
// fall in the middle of a multi-byte rune so a truncated read never yields
// a half-mangled character.
func utf8ValidString(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	n := len(b)
	for n > 0 {
		r, size := utf8.DecodeLastRune(b[:n])
		if r != utf8.RuneError || size > 1 {
			return string(b[:n])
		}
		n--
	}
	return ""
}

func init() {
	RegisterBuiltinFactory("read_file", func(cfg BuiltinConfig) (Tool, error) {
		return &readFileTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("write_file", func(cfg BuiltinConfig) (Tool, error) {
		return &writeFileTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("edit_file", func(cfg BuiltinConfig) (Tool, error) {
		return &editFileTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("list_dir", func(cfg BuiltinConfig) (Tool, error) {
		return &listDirTool{sb: cfg.Sandbox}, nil
	})
}

// editSpec is one textual find-and-replace in a sequence.
type editSpec struct {
	OldText    string
	NewText    string
	ReplaceAll bool
}

// applyEdits applies edits to src in memory, sequentially: each edit runs
// against the result of the previous one, and the file is rewritten only
// when every edit succeeds. With multiple edits the error names the failing
// index. A miss on any edit leaves src untouched.
func applyEdits(src []byte, edits []editSpec, path string) ([]byte, int, error) {
	out := src
	applied := 0
	for i, e := range edits {
		original := string(out)
		notFound := func() error {
			if len(edits) > 1 {
				return fmt.Errorf("edit %d: old_text not found in %s", i+1, path)
			}
			return fmt.Errorf("old_text not found in %s", path)
		}
		if e.ReplaceAll {
			n := strings.Count(original, e.OldText)
			if n == 0 {
				return nil, applied, notFound()
			}
			out = []byte(strings.ReplaceAll(original, e.OldText, e.NewText))
			applied += n
			continue
		}
		if !strings.Contains(original, e.OldText) {
			return nil, applied, notFound()
		}
		out = []byte(strings.Replace(original, e.OldText, e.NewText, 1))
		applied++
	}
	return out, applied, nil
}

// moveFileTool moves / renames a file, creating the destination parent
// directory as needed.
type moveFileTool struct {
	sb *Sandbox
}

func (t *moveFileTool) Name() string { return "move_file" }
func (t *moveFileTool) Description() string {
	return "Move or rename a file from source_path to destination_path. Creates the destination parent directory as needed. The destination must not already exist."
}
func (t *moveFileTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"source_path":      {Type: "string", Description: "Existing file path to move."},
			"destination_path": {Type: "string", Description: "Destination file path; must not already exist."},
		},
		Required:             []string{"source_path", "destination_path"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *moveFileTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	src, _ := args["source_path"].(string)
	dst, _ := args["destination_path"].(string)
	srcAbs, err := t.sb.Resolve(src)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	dstAbs, err := t.sb.Resolve(dst)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if srcAbs == dstAbs {
		return Result{IsError: true, Content: "source and destination are the same path"}
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return Result{IsError: true, Content: "destination already exists: " + dst}
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return Result{IsError: true, Content: "mkdir: " + err.Error()}
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		// Cross-device rename fails with EXDEV; fall back to copy + remove.
		if _, cpErr := copyFile(srcAbs, dstAbs); cpErr != nil {
			return Result{IsError: true, Content: "rename: " + err.Error()}
		}
		if rmErr := os.Remove(srcAbs); rmErr != nil {
			return Result{IsError: true, Content: "moved content but could not remove source: " + rmErr.Error()}
		}
	}
	return Result{Content: fmt.Sprintf("moved %s to %s", src, dst)}
}

// copyFile copies src to dst byte-for-byte with 0644 permissions.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}

// maxMultiEdits caps the number of edits in a single multi_edit call.
const maxMultiEdits = 100

// multiEditTool applies a sequence of edits to one file atomically.
type multiEditTool struct {
	sb *Sandbox
}

func (t *multiEditTool) Name() string { return "multi_edit" }
func (t *multiEditTool) Description() string {
	return "Apply a list of edits to a single file atomically: each edit runs against the result of the previous one in memory, and the file is rewritten only if every edit succeeds. A failure in any step leaves the file untouched."
}
func (t *multiEditTool) Parameters() json.RawMessage {
	// Hand-authored: ParameterProperty cannot express nested object
	// properties, so the edit-item shape is described in raw JSON here.
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File to edit."},"edits":{"type":"array","description":"Edits to apply, in order.","items":{"type":"object","properties":{"old_text":{"type":"string","description":"Existing text to find."},"new_text":{"type":"string","description":"Replacement text."},"replace_all":{"type":"boolean","description":"Replace every occurrence (default false)."}},"required":["old_text","new_text"]}}},"required":["path","edits"],"additionalProperties":false}`)
}

func (t *multiEditTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	items, _ := args["edits"].([]any)
	if len(items) == 0 {
		return Result{IsError: true, Content: "edits must not be empty"}
	}
	if len(items) > maxMultiEdits {
		return Result{IsError: true, Content: fmt.Sprintf("too many edits: %d (max %d)", len(items), maxMultiEdits)}
	}
	edits := make([]editSpec, 0, len(items))
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("edits[%d] must be an object", i)}
		}
		oldText, _ := m["old_text"].(string)
		if oldText == "" {
			return Result{IsError: true, Content: fmt.Sprintf("edits[%d].old_text must not be empty", i)}
		}
		newText, _ := m["new_text"].(string)
		replaceAll, _ := m["replace_all"].(bool)
		edits = append(edits, editSpec{OldText: oldText, NewText: newText, ReplaceAll: replaceAll})
	}
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{IsError: true, Content: "read: " + err.Error()}
	}
	updated, count, err := applyEdits(data, edits, path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if err := os.WriteFile(abs, updated, 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("applied %d replacement(s) across %d edit(s) in %s", count, len(edits), path)}
}

// deleteRangeTool deletes a contiguous line range bounded by two text
// anchors, returning a unified diff of the change.
type deleteRangeTool struct {
	sb *Sandbox
}

func (t *deleteRangeTool) Name() string { return "delete_range" }
func (t *deleteRangeTool) Description() string {
	return "Delete a contiguous range of lines from a file using exact start/end text anchors. Each anchor must match exactly one line, with end_text after start_text. Returns a unified diff on success. Use for large deletions — smaller changes should use edit_file."
}
func (t *deleteRangeTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":       {Type: "string", Description: "File to edit."},
			"start_text": {Type: "string", Description: "First line of the range; must match exactly one line."},
			"end_text":   {Type: "string", Description: "Last line of the range; must match exactly one line after start_text."},
		},
		Required:             []string{"path", "start_text", "end_text"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *deleteRangeTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	startText, _ := args["start_text"].(string)
	endText, _ := args["end_text"].(string)
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{IsError: true, Content: "read: " + err.Error()}
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return Result{IsError: true, Content: "file is empty"}
	}
	startIdx, err := findAnchorLine(lines, startText)
	if err != nil {
		return Result{IsError: true, Content: "start_text: " + err.Error()}
	}
	endIdx, err := findAnchorLine(lines, endText)
	if err != nil {
		return Result{IsError: true, Content: "end_text: " + err.Error()}
	}
	if startIdx > endIdx {
		return Result{IsError: true, Content: "end_text appears before start_text"}
	}
	removed := endIdx - startIdx + 1
	updated := make([]string, 0, len(lines)-removed)
	updated = append(updated, lines[:startIdx]...)
	updated = append(updated, lines[endIdx+1:]...)
	if err := os.WriteFile(abs, []byte(strings.Join(updated, "")), 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	diff := unifiedDeleteDiff(path, lines, startIdx, endIdx)
	return Result{Content: fmt.Sprintf("deleted %d line(s) from %s\n%s", removed, path, diff)}
}

// findAnchorLine returns the single line index whose content equals anchor;
// it errors when no line matches or more than one does.
func findAnchorLine(lines []string, anchor string) (int, error) {
	matches := make([]int, 0, 1)
	for i, line := range lines {
		if strings.TrimRight(line, "\r\n") == anchor {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return 0, fmt.Errorf("no line matches %q", anchor)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("%q matches %d lines; must match exactly one", anchor, len(matches))
	}
	return matches[0], nil
}

// diffContext is the number of unchanged context lines shown around a hunk.
const diffContext = 3

// unifiedDeleteDiff renders a line-level unified diff for a pure deletion
// of lines [startIdx, endIdx]. Only deletion is possible, so no general
// diff algorithm is needed: removed lines get a "-" prefix, context " ".
func unifiedDeleteDiff(path string, lines []string, startIdx, endIdx int) string {
	n := len(lines)
	oldStart := startIdx - diffContext
	if oldStart < 0 {
		oldStart = 0
	}
	oldEnd := endIdx + diffContext
	if oldEnd > n {
		oldEnd = n
	}
	oldCount := oldEnd - oldStart
	newCount := oldCount - (endIdx - startIdx + 1)
	var sb strings.Builder
	sb.WriteString("--- " + path + "\n")
	sb.WriteString("+++ " + path + "\n")
	sb.WriteString(hunkHeader(oldStart+1, oldCount, newCount))
	for i := oldStart; i < startIdx; i++ {
		sb.WriteString(" " + lines[i])
	}
	for i := startIdx; i <= endIdx; i++ {
		sb.WriteString("-" + lines[i])
	}
	for i := endIdx + 1; i < oldEnd; i++ {
		sb.WriteString(" " + lines[i])
	}
	return sb.String()
}

// hunkHeader renders the "@@ -a,b +c,d @@" line, using the unified
// convention of "0,0" for empty ranges.
func hunkHeader(oldStart, oldCount, newCount int) string {
	var sb strings.Builder
	sb.WriteString("@@ -")
	switch oldCount {
	case 0:
		sb.WriteString("0,0")
	case 1:
		fmt.Fprintf(&sb, "%d", oldStart)
	default:
		fmt.Fprintf(&sb, "%d,%d", oldStart, oldCount)
	}
	sb.WriteString(" +")
	switch newCount {
	case 0:
		sb.WriteString("0,0")
	case 1:
		fmt.Fprintf(&sb, "%d", oldStart)
	default:
		fmt.Fprintf(&sb, "%d,%d", oldStart, newCount)
	}
	sb.WriteString(" @@\n")
	return sb.String()
}

func init() {
	RegisterBuiltinFactory("move_file", func(cfg BuiltinConfig) (Tool, error) {
		return &moveFileTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("multi_edit", func(cfg BuiltinConfig) (Tool, error) {
		return &multiEditTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("delete_range", func(cfg BuiltinConfig) (Tool, error) {
		return &deleteRangeTool{sb: cfg.Sandbox}, nil
	})
}
