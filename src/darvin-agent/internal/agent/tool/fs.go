package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"darvin-cowork/backend/internal/agent/llm"
)

// maxReadBytes is the upper bound on a single read_file payload. Anything
// beyond is truncated and the truncation is noted in the returned content.
const maxReadBytes = 1 << 20 // 1 MiB

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
			"limit":  {Type: "integer", Description: "Maximum number of bytes to return (default 1 MiB)."},
			"offset": {Type: "integer", Description: "Byte offset to start reading from."},
		},
		Required: []string{"path"},
	}
}

func (t *readFileTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	f, err := os.Open(abs)
	if err != nil {
		return Result{IsError: true, Content: "open: " + err.Error()}
	}
	defer f.Close()

	limit := maxReadBytes
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > maxReadBytes {
			limit = maxReadBytes
		}
	}
	offset := int64(0)
	if v, ok := args["offset"].(float64); ok && v > 0 {
		offset = int64(v)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Result{IsError: true, Content: "seek: " + err.Error()}
	}

	buf := bytes.NewBuffer(make([]byte, 0, min(limit, 4096)))
	truncated := false
	written := 0
	tmp := make([]byte, 4096)
	for written < limit {
		n, rerr := f.Read(tmp)
		if n > 0 {
			remaining := limit - written
			if n > remaining {
				buf.Write(tmp[:remaining])
				written = limit
				truncated = true
			} else {
				buf.Write(tmp[:n])
				written += n
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return Result{IsError: true, Content: "read: " + rerr.Error()}
		}
	}
	content := buf.String()
	if truncated {
		content += fmt.Sprintf("\n[truncated, total >= %d bytes]", offset+int64(written))
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
			"content": {Type: "string", Description: "UTF-8 text content to write."},
		},
		Required: []string{"path", "content"},
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
			"old_text":    {Type: "string", Description: "Existing text to find."},
			"new_text":    {Type: "string", Description: "Replacement text."},
			"replace_all": {Type: "boolean", Description: "Replace every occurrence (default false)."},
		},
		Required: []string{"path", "old_text", "new_text"},
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
			"max_depth": {Type: "integer", Description: "Recursion depth; default 1 (no recursion)."},
		},
		Required: []string{"path"},
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
