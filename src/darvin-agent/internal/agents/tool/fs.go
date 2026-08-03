package tool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"darvin-cowork/backend/internal/agents/llm"
)

// maxReadBytes is the default read_file window. Anything beyond is truncated
// and the truncation is noted in the returned content.
const maxReadBytes = 1 << 20 // 1 MiB

// maxHardWriteBytes caps a single write_file / edit_file payload so a model
// cannot push a huge string through the tool boundary.
const maxHardWriteBytes = 32 << 20 // 32 MiB

// readFileTool reads a UTF-8 text file, optionally limited to a window.
type readFileTool struct {
	sb *fsSandbox
}

func (t *readFileTool) Name() string { return "read_file" }
func (t *readFileTool) Description() string {
	return "Read the contents of a UTF-8 text file. Optional limit/offset slice large files."
}
func (t *readFileTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":   {Type: "string", Description: "Path relative to the workspace, or absolute within the workspace."},
			"limit":  {Type: "integer", Description: "Maximum number of bytes to return (default 1 MiB, hard cap 16 MiB).", Minimum: ptrFloat64(0), Maximum: ptrFloat64(maxHardReadBytes)},
			"offset": {Type: "integer", Description: "Byte offset to start reading from.", Minimum: ptrFloat64(0)},
		},
		Required:             []string{"path"},
		AdditionalProperties: ptrBool(false),
	}
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
	sb *fsSandbox
}

func (t *writeFileTool) Name() string { return "write_file" }
func (t *writeFileTool) Description() string {
	return "Write UTF-8 text content to a file, overwriting any existing content. Creates parent directories as needed."
}
func (t *writeFileTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":    {Type: "string", Description: "Destination path."},
			"content": {Type: "string", Description: "UTF-8 text content to write.", MaxLength: ptrInt(maxHardWriteBytes)},
		},
		Required:             []string{"path", "content"},
		AdditionalProperties: ptrBool(false),
	}
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
	sb *fsSandbox
}

func (t *editFileTool) Name() string { return "edit_file" }
func (t *editFileTool) Description() string {
	return "Find old_text in a file and replace it with new_text. By default replaces the first occurrence; set replace_all=true to replace every occurrence."
}
func (t *editFileTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":        {Type: "string", Description: "File to edit."},
			"old_text":    {Type: "string", Description: "Existing text to find.", MaxLength: ptrInt(maxHardWriteBytes)},
			"new_text":    {Type: "string", Description: "Replacement text.", MaxLength: ptrInt(maxHardWriteBytes)},
			"replace_all": {Type: "boolean", Description: "Replace every occurrence (default false)."},
		},
		Required:             []string{"path", "old_text", "new_text"},
		AdditionalProperties: ptrBool(false),
	}
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
	original := string(data)
	var updated string
	var count int
	if replaceAll {
		count = strings.Count(original, oldText)
		if count == 0 {
			return Result{IsError: true, Content: "old_text not found in " + path}
		}
		updated = strings.ReplaceAll(original, oldText, newText)
	} else {
		if !strings.Contains(original, oldText) {
			return Result{IsError: true, Content: "old_text not found in " + path}
		}
		updated = strings.Replace(original, oldText, newText, 1)
		count = 1
	}
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	return Result{Content: fmt.Sprintf("replaced %d occurrence(s) in %s", count, path)}
}

// listDirTool lists entries under a directory.
type listDirTool struct {
	sb *fsSandbox
}

func (t *listDirTool) Name() string { return "list_dir" }
func (t *listDirTool) Description() string {
	return "List immediate entries under a directory. Optional max_depth walks sub-directories up to N levels (1-based, 1 = immediate children)."
}
func (t *listDirTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path":      {Type: "string", Description: "Directory to list."},
			"max_depth": {Type: "integer", Description: "Recursion depth; default 1 (no recursion).", Minimum: ptrFloat64(0), Maximum: ptrFloat64(20)},
		},
		Required:             []string{"path"},
		AdditionalProperties: ptrBool(false),
	}
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
