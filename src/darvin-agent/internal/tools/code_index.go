// Implements the code_index tool: lazy in-memory Go symbol index
// (outline / search / info), invalidated by write tools after they land
// .go files.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"darvin-cowork/backend/internal/llm"
)

// codeIndexMaxMatches caps the search result count.
const codeIndexMaxMatches = 500

// SymbolRef is one flattened top-level declaration extracted from a .go
// file's AST. Receiver is set only for methods (without the leading "*").
type SymbolRef struct {
	Path     string
	Name     string
	Kind     string
	Receiver string
	Pkg      string
	Line     int
	EndLine  int
	Doc      string
}

// codeIndex holds the lazy in-memory symbol cache. byFile is keyed by
// absolute path; byName is keyed by lowercased name. built records which
// paths are already parsed.
type codeIndex struct {
	mu     sync.RWMutex
	byFile map[string][]SymbolRef
	byName map[string][]SymbolRef
	built  map[string]bool
}

func newCodeIndex() *codeIndex {
	return &codeIndex{
		byFile: map[string][]SymbolRef{},
		byName: map[string][]SymbolRef{},
		built:  map[string]bool{},
	}
}

// globalCodeIndex is the process-wide symbol cache. All built-ins in the
// same package share it via invalidateCodeIndex / clearCodeIndex.
var globalCodeIndex = newCodeIndex()

// invalidateCodeIndex drops absPath from the cache when it is a .go file.
// Called by write tools after a successful write.
func invalidateCodeIndex(absPath string) {
	if !strings.HasSuffix(absPath, ".go") {
		return
	}
	globalCodeIndex.Invalidate(absPath)
}

// clearCodeIndex empties the cache. Called on workspace re-anchor.
func clearCodeIndex() {
	globalCodeIndex.Clear()
}

// Invalidate removes absPath's entries from byFile / built. byName entries
// left dangling are GC'd lazily by nextIndexFile.
func (c *codeIndex) Invalidate(absPath string) {
	c.mu.Lock()
	delete(c.byFile, absPath)
	delete(c.built, absPath)
	c.mu.Unlock()
}

// Clear empties the whole index.
func (c *codeIndex) Clear() {
	c.mu.Lock()
	c.byFile = map[string][]SymbolRef{}
	c.byName = map[string][]SymbolRef{}
	c.built = map[string]bool{}
	c.mu.Unlock()
}

// indexFile parses abs and stores its symbols. Unreadable or unparsable
// files are skipped silently (matches grep/glob walk tolerance).
func (c *codeIndex) indexFile(abs string) {
	c.mu.RLock()
	if c.built[abs] {
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()
	data, err := readFileBestEffort(abs)
	if err != nil {
		return
	}
	fset, file, err := parseGoFile(data, abs)
	if err != nil {
		return
	}
	refs := flattenAST(fset, file)
	for i := range refs {
		refs[i].Path = abs
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.built[abs] {
		return
	}
	c.byFile[abs] = refs
	for _, r := range refs {
		key := strings.ToLower(r.Name)
		c.byName[key] = append(c.byName[key], r)
	}
	c.built[abs] = true
}

// readFileBestEffort returns the file bytes when under maxGrepFileBytes,
// treating any error or oversize file as a skip. Mirrors grep's tolerance.
func readFileBestEffort(abs string) ([]byte, error) {
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxGrepFileBytes {
		return nil, fmt.Errorf("skip %s", abs)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// outline returns abs's indexed symbols in declaration order.
func (c *codeIndex) outline(abs string) ([]SymbolRef, bool) {
	c.indexFile(abs)
	c.mu.RLock()
	defer c.mu.RUnlock()
	refs, ok := c.byFile[abs]
	if !ok {
		return nil, false
	}
	out := make([]SymbolRef, len(refs))
	copy(out, refs)
	return out, true
}

// searchFiles walks base (recursive) and yields matching symbols. The
// returned slice is sorted by (path, line). Stops early once limit is hit.
func (c *codeIndex) searchFiles(ctx context.Context, sb *Sandbox, base, query, kind string, limit int) ([]SymbolRef, error) {
	q := strings.ToLower(query)
	var out []SymbolRef
	stop := errors.New("limit reached")
	err := walkTree(ctx, sb, base, func(abs string) error {
		if !strings.HasSuffix(abs, ".go") {
			return nil
		}
		refs, _ := c.outline(abs)
		for _, r := range refs {
			if kind != "" && r.Kind != kind {
				continue
			}
			if !strings.Contains(strings.ToLower(r.Name), q) {
				continue
			}
			out = append(out, r)
			if len(out) >= limit {
				return stop
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// infoByName walks base looking for a single symbol matching query (name or
// Receiver.Name) and kind filter. Disambiguates with kind; ambiguous after
// filter is an error.
func (c *codeIndex) infoByName(ctx context.Context, sb *Sandbox, base, query, kind string) (SymbolRef, error) {
	name, recv := splitInfoQuery(query)
	var matches []SymbolRef
	err := walkTree(ctx, sb, base, func(abs string) error {
		if !strings.HasSuffix(abs, ".go") {
			return nil
		}
		c.indexFile(abs)
		c.mu.RLock()
		cands := c.byName[strings.ToLower(name)]
		for _, r := range cands {
			if r.Path != abs {
				continue
			}
			if recv != r.Receiver {
				continue
			}
			if kind != "" && r.Kind != kind {
				continue
			}
			matches = append(matches, r)
		}
		c.mu.RUnlock()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return SymbolRef{}, err
	}
	switch len(matches) {
	case 0:
		return SymbolRef{}, fmt.Errorf("symbol %q not found", query)
	case 1:
		return matches[0], nil
	default:
		return SymbolRef{}, fmt.Errorf("symbol %q matches %d declarations; add kind to disambiguate", query, len(matches))
	}
}

// splitInfoQuery parses "Name" or "Receiver.Name" (with optional leading "*"
// on Receiver). Returns (name, receiver); receiver has any leading "*" stripped.
func splitInfoQuery(query string) (name, recv string) {
	if i := strings.LastIndex(query, "."); i >= 0 {
		recv = strings.TrimPrefix(query[:i], "*")
		return query[i+1:], recv
	}
	return query, ""
}

// flattenAST walks top-level decls of file and produces one SymbolRef each.
// The caller must set Path on each returned entry before storing.
func flattenAST(fset *token.FileSet, file *ast.File) []SymbolRef {
	pkg := file.Name.Name
	out := make([]SymbolRef, 0, len(file.Decls))
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind, recv := "func", ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind, recv = "method", receiverTypeName(d.Recv.List[0].Type)
			}
			out = append(out, SymbolRef{
				Name:     d.Name.Name,
				Kind:     kind,
				Receiver: recv,
				Pkg:      pkg,
				Line:     fset.Position(d.Pos()).Line,
				EndLine:  fset.Position(d.End()).Line,
				Doc:      docText(d.Doc),
			})
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					out = append(out, SymbolRef{
						Name:    ts.Name.Name,
						Kind:    "type",
						Pkg:     pkg,
						Line:    fset.Position(ts.Pos()).Line,
						EndLine: fset.Position(ts.End()).Line,
						Doc:     docText(ts.Doc),
					})
				}
			case token.CONST, token.VAR:
				kind := "var"
				if d.Tok == token.CONST {
					kind = "const"
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, n := range vs.Names {
						out = append(out, SymbolRef{
							Name:    n.Name,
							Kind:    kind,
							Pkg:     pkg,
							Line:    fset.Position(vs.Pos()).Line,
							EndLine: fset.Position(vs.End()).Line,
							Doc:     docText(vs.Doc),
						})
					}
				}
			}
		}
	}
	return out
}

// receiverTypeName extracts the type name from a method receiver,
// stripping any leading "*". Returns "" for anonymous or unknown forms.
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	}
	return ""
}

func docText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return cg.Text()
}

// codeIndexTool is the builtin registered as "code_index".
type codeIndexTool struct {
	sb *Sandbox
}

func (t *codeIndexTool) Name() string { return "code_index" }
func (t *codeIndexTool) Description() string {
	return "Inspect Go source structure: outline a single file, search for symbol names across the workspace, or fetch one symbol's details. The index is built lazily and stays in memory; writes via other tools invalidate stale entries. .go only."
}
func (t *codeIndexTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"action": {Type: "string", Enum: []string{"outline", "search", "info"}, Description: "What to do."},
			"path":   {Type: "string", Description: "Workspace-relative file or directory. Required for outline; for search/info, defaults to the workspace root."},
			"query":  {Type: "string", Description: "Symbol name substring (search) or exact name / Receiver.Name (info). Required for search and info."},
			"kind":   {Type: "string", Enum: []string{"func", "method", "type", "const", "var"}, Description: "Optional kind filter; empty matches any."},
			"limit":  {Type: "integer", Minimum: ptrFloat64(1), Maximum: ptrFloat64(codeIndexMaxMatches), Description: "Max matches for search (default 50, max 500)."},
		},
		Required:             []string{"action"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *codeIndexTool) Execute(ctx context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	action, _ := args["action"].(string)
	path, _ := args["path"].(string)
	query, _ := args["query"].(string)
	kind, _ := args["kind"].(string)
	limit := 50
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
		if limit > codeIndexMaxMatches {
			limit = codeIndexMaxMatches
		}
	}
	switch action {
	case "outline":
		return t.actionOutline(path)
	case "search":
		return t.actionSearch(ctx, path, query, kind, limit)
	case "info":
		return t.actionInfo(ctx, path, query, kind)
	}
	return Result{IsError: true, Content: "unknown action: " + action}
}

func (t *codeIndexTool) actionOutline(path string) Result {
	if path == "" {
		return Result{IsError: true, Content: "action=outline requires path"}
	}
	if !strings.HasSuffix(path, ".go") {
		return Result{IsError: true, Content: "code_index outline only supports .go files"}
	}
	abs, err := t.sb.ResolveRead(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	refs, ok := globalCodeIndex.outline(abs)
	if !ok || len(refs) == 0 {
		return Result{Content: "(no symbols)"}
	}
	var sb strings.Builder
	sb.WriteString(path)
	sb.WriteString("\n")
	for _, r := range refs {
		lineRange := fmt.Sprintf("L%d", r.Line)
		if r.EndLine > r.Line {
			lineRange = fmt.Sprintf("L%d-%d", r.Line, r.EndLine)
		}
		fmt.Fprintf(&sb, "  %-9s %s %s", lineRange, r.Kind, r.Name)
		if r.Receiver != "" {
			sb.WriteString(" (recv " + r.Receiver + ")")
		}
		sb.WriteString("\n")
	}
	return Result{Content: sb.String()}
}

func (t *codeIndexTool) actionSearch(ctx context.Context, path, query, kind string, limit int) Result {
	if query == "" {
		return Result{IsError: true, Content: "action=search requires query"}
	}
	base, err := t.sb.resolveSearchBase(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	refs, err := globalCodeIndex.searchFiles(ctx, t.sb, base, query, kind, limit)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	if len(refs) == 0 {
		return Result{Content: "(no matches)"}
	}
	var sb strings.Builder
	cur := ""
	for _, r := range refs {
		rel := filepath.ToSlash(relFrom(base, r.Path))
		if rel != cur {
			if cur != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(rel + "\n")
			cur = rel
		}
		fmt.Fprintf(&sb, "  L%-6d %s %s\n", r.Line, r.Kind, r.Name)
	}
	return Result{Content: sb.String()}
}

func (t *codeIndexTool) actionInfo(ctx context.Context, path, query, kind string) Result {
	if query == "" {
		return Result{IsError: true, Content: "action=info requires query"}
	}
	base, err := t.sb.resolveSearchBase(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	ref, err := globalCodeIndex.infoByName(ctx, t.sb, base, query, kind)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	rel := filepath.ToSlash(relFrom(base, ref.Path))
	var sb strings.Builder
	fmt.Fprintf(&sb, "package: %s\n", ref.Pkg)
	fmt.Fprintf(&sb, "file:    %s\n", rel)
	fmt.Fprintf(&sb, "kind:    %s\n", ref.Kind)
	if ref.Receiver != "" {
		fmt.Fprintf(&sb, "recv:    %s\n", ref.Receiver)
	}
	fmt.Fprintf(&sb, "line:    %d", ref.Line)
	if ref.EndLine > ref.Line {
		fmt.Fprintf(&sb, "-%d", ref.EndLine)
	}
	sb.WriteString("\n")
	if ref.Doc != "" {
		fmt.Fprintf(&sb, "doc:\n%s", indentBlock(ref.Doc, "  "))
	}
	return Result{Content: sb.String()}
}

func relFrom(base, abs string) string {
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return abs
	}
	return rel
}

func indentBlock(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func init() {
	RegisterBuiltinFactory("code_index", func(cfg BuiltinConfig) (Tool, error) {
		return &codeIndexTool{sb: cfg.Sandbox}, nil
	})
}
