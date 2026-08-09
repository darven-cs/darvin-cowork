// Implements the recursive search tools grep and glob, gated by the
// workspace sandbox and bounded by a walk timeout.

package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"darvin-cowork/backend/internal/llm"
)

const (
	defaultSearchTimeout = 30 * time.Second
	maxSearchTimeout     = 300 * time.Second
	// grepMaxMatches is the default hit cap per call; grepMaxMatchesParam
	// is the schema ceiling for the caller-provided max_matches.
	grepMaxMatches      = 200
	grepMaxMatchesParam = 1000
	globMaxResults      = 1000
	maxGrepFileBytes    = 1 << 20 // per-file read cap: larger files are skipped
	grepScanLineBytes   = 1 << 20 // per-line cap for bufio.Scanner
)

// resolveSearchBase resolves the search root. An empty path uses the
// workspace root; otherwise the path may be a workspace file/dir or a
// user-granted read (attached absolute path).
func (s *Sandbox) resolveSearchBase(p string) (string, error) {
	if p == "" {
		return s.Root(), nil
	}
	abs, err := s.ResolveRead(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// isHiddenName reports whether a walk entry is hidden (leading dot), used
// to skip hidden files / dirs during recursive search.
func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "."
}

// searchFile reads a regular file and runs fn per line. Binary files are
// skipped (first NUL byte). The file must stay under maxGrepFileBytes.
func searchFile(ctx context.Context, path string, fn func(line string, ln int) error) error {
	f, err := os.Open(path)
	if err != nil {
		return nil // unreadable files are skipped silently
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGrepFileBytes {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), grepScanLineBytes)
	ln := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		ln++
		line := sc.Text()
		if strings.IndexByte(line, 0) >= 0 {
			return nil // binary, skip the whole file
		}
		if err := fn(line, ln); err != nil {
			return err
		}
	}
	return nil
}

// grepTool searches a file or directory tree for a regex pattern.
type grepTool struct {
	sb *Sandbox
}

func (t *grepTool) Name() string { return "grep" }
func (t *grepTool) Description() string {
	return "Search for a regular expression (RE2) in a file, or recursively under a directory. Skips hidden files and workspace-excluded dirs (e.g. .git, node_modules). Returns matching lines as path:line:text, capped at max_matches."
}
func (t *grepTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"pattern":         {Type: "string", Description: "Regular expression (RE2 syntax)."},
			"path":            {Type: "string", Description: "File or directory to search (defaults to the workspace root)."},
			"timeout_seconds": {Type: "integer", Minimum: ptrFloat64(1), Maximum: ptrFloat64(float64(maxSearchTimeout / time.Second)), Description: "Abort and return partial matches after this many seconds (default 30, max 300)."},
			"max_matches":     {Type: "integer", Minimum: ptrFloat64(1), Maximum: ptrFloat64(grepMaxMatchesParam), Description: "Maximum number of matches to return (default 200, max 1000)."},
		},
		Required:             []string{"pattern"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *grepTool) Execute(ctx context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	pattern, _ := args["pattern"].(string)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{IsError: true, Content: "invalid pattern: " + err.Error()}
	}
	searchPath, _ := args["path"].(string)
	base, err := t.sb.resolveSearchBase(searchPath)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	timeout := defaultSearchTimeout
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
		if timeout > maxSearchTimeout {
			timeout = maxSearchTimeout
		}
	}
	maxMatches := grepMaxMatches
	if v, ok := args["max_matches"].(float64); ok && v > 0 {
		maxMatches = int(v)
		if maxMatches > grepMaxMatchesParam {
			maxMatches = grepMaxMatchesParam
		}
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var out []string
	truncated := false
	// displayBase is what results are shown relative to: the search root for
	// a tree, or the file's parent dir for a single-file search (so the
	// output names the file, not ".").
	displayBase := base
	add := func(abs, line string, ln int) error {
		rel, _ := filepath.Rel(displayBase, abs)
		out = append(out, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), ln, line))
		if len(out) >= maxMatches {
			truncated = true
			return io.EOF
		}
		return nil
	}
	searchOne := func(abs string) error {
		return searchFile(sctx, abs, func(line string, ln int) error {
			if re.MatchString(line) {
				if err := add(abs, line, ln); err != nil {
					return err
				}
			}
			return nil
		})
	}

	if info, statErr := os.Stat(base); statErr == nil && !info.IsDir() {
		displayBase = filepath.Dir(base)
		if err := searchOne(base); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return Result{IsError: true, Content: err.Error()}
		}
	} else {
		if err := walkTree(sctx, t.sb, base, searchOne); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return Result{IsError: true, Content: err.Error()}
		}
	}
	if len(out) == 0 {
		return Result{Content: "(no matches)"}
	}
	content := strings.Join(out, "\n")
	if truncated {
		content += fmt.Sprintf("\n[truncated at %d matches]", maxMatches)
	}
	return Result{Content: content}
}

// walkTree walks base recursively, skipping excluded / hidden dirs, and
// calls fn for each regular file. Returns filepath.SkipAll when fn returns
// it, so callers can short-circuit a walk.
func walkTree(ctx context.Context, sb *Sandbox, base string, fn func(abs string) error) error {
	return filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != base && (isHiddenName(d.Name()) || sb.IsExcluded(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if isHiddenName(d.Name()) || sb.IsExcluded(path) {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(path)
	})
}

// globTool finds files matching a glob pattern under a base directory.
type globTool struct {
	sb *Sandbox
}

func (t *globTool) Name() string { return "glob" }
func (t *globTool) Description() string {
	return "Find files matching a glob pattern under a directory. Supports * ? [] and recursive ** (e.g. \"*.go\", \"internal/*/*.go\", \"**/*.test.ts\"). Paths are relative to the base directory. Skips hidden files and workspace-excluded dirs."
}
func (t *globTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"pattern":         {Type: "string", Description: "Glob pattern (supports ** for recursive matching)."},
			"path":            {Type: "string", Description: "Base directory (defaults to the workspace root)."},
			"timeout_seconds": {Type: "integer", Minimum: ptrFloat64(1), Maximum: ptrFloat64(float64(maxSearchTimeout / time.Second)), Description: "Abort and return partial matches after this many seconds (default 30, max 300)."},
		},
		Required:             []string{"pattern"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *globTool) Execute(ctx context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	pattern, _ := args["pattern"].(string)
	basePath, _ := args["path"].(string)
	base, err := t.sb.resolveSearchBase(basePath)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	timeout := defaultSearchTimeout
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
		if timeout > maxSearchTimeout {
			timeout = maxSearchTimeout
		}
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var out []string
	truncated := false
	if err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if sctx.Err() != nil {
			return sctx.Err()
		}
		if d.IsDir() {
			if path != base && (isHiddenName(d.Name()) || t.sb.IsExcluded(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if isHiddenName(d.Name()) || t.sb.IsExcluded(path) {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		matched, matchErr := doublestar.Match(pattern, filepath.ToSlash(rel))
		if matchErr != nil || !matched {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= globMaxResults {
			truncated = true
			return io.EOF
		}
		return nil
	}); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		return Result{IsError: true, Content: err.Error()}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return Result{Content: "(no matches)"}
	}
	content := strings.Join(out, "\n")
	if truncated {
		content += fmt.Sprintf("\n[truncated at %d results]", globMaxResults)
	}
	return Result{Content: content}
}

func init() {
	RegisterBuiltinFactory("grep", func(cfg BuiltinConfig) (Tool, error) {
		return &grepTool{sb: cfg.Sandbox}, nil
	})
}

func init() {
	RegisterBuiltinFactory("glob", func(cfg BuiltinConfig) (Tool, error) {
		return &globTool{sb: cfg.Sandbox}, nil
	})
}
