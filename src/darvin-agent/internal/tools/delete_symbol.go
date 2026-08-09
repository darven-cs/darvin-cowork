// Implements the delete_symbol tool: AST-based removal of a named Go
// declaration, keeping any attached doc comment inside the deleted span.

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// deleteSymbolTool removes a named Go declaration (func / method / type /
// const / var) from a source file using AST parsing.
type deleteSymbolTool struct {
	sb *Sandbox
}

func (t *deleteSymbolTool) Name() string { return "delete_symbol" }
func (t *deleteSymbolTool) Description() string {
	return "Delete a named symbol (function, method, type, const, var) from a Go source file using AST parsing. Non-Go files are not supported — use delete_range with text anchors instead."
}
func (t *deleteSymbolTool) Parameters() json.RawMessage {
	return MarshalSchema(llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path": {Type: "string", Description: "Path to a .go file."},
			"name": {Type: "string", Description: "Name of the symbol to delete."},
			"kind": {Type: "string", Enum: []string{"func", "method", "type", "const", "var"}, Description: "Optional kind to disambiguate; empty matches any."},
		},
		Required:             []string{"path", "name"},
		AdditionalProperties: ptrBool(false),
	})
}

func (t *deleteSymbolTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	path, _ := args["path"].(string)
	name, _ := args["name"].(string)
	kind, _ := args["kind"].(string)
	if !strings.HasSuffix(path, ".go") {
		return Result{IsError: true, Content: "delete_symbol only supports .go files — use delete_range for other files"}
	}
	abs, err := t.sb.Resolve(path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return Result{IsError: true, Content: "read: " + err.Error()}
	}
	fset, file, err := parseGoFile(src, path)
	if err != nil {
		return Result{IsError: true, Content: "parse: " + err.Error()}
	}
	ref, err := findSymbolDecl(fset, file, name, kind)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	updated := append(src[:ref.start], src[ref.end:]...)
	if err := os.WriteFile(abs, updated, 0o644); err != nil {
		return Result{IsError: true, Content: "write: " + err.Error()}
	}
	invalidateCodeIndex(abs)
	return Result{Content: fmt.Sprintf("deleted symbol %q from %s", name, path)}
}

// symbolRef is a half-open byte range [start, end) into the source.
type symbolRef struct {
	start, end int
}

// findSymbolDecl locates the byte range of the named declaration in file.
// Multiple top-level matches are ambiguous and rejected; a const/var whose
// value spec shares names with others is rejected rather than half-deleted.
func findSymbolDecl(fset *token.FileSet, file *ast.File, name, kind string) (symbolRef, error) {
	var matches []symbolRef
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			elem := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				elem = "method"
			}
			if d.Name.Name != name || !kindMatches(kind, elem) {
				continue
			}
			matches = append(matches, refFor(fset, d.Doc, d.Pos(), d.End()))
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != name || !kindMatches(kind, "type") {
						continue
					}
					matches = append(matches, genSpecRef(fset, d, ts))
				}
			case token.CONST, token.VAR:
				elem := "var"
				if d.Tok == token.CONST {
					elem = "const"
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || !containsIdent(vs.Names, name) || !kindMatches(kind, elem) {
						continue
					}
					if len(vs.Names) > 1 {
						return symbolRef{}, fmt.Errorf("symbol %q shares a declaration with others; edit the file manually", name)
					}
					matches = append(matches, genSpecRef(fset, d, vs))
				}
			}
		}
	}
	switch len(matches) {
	case 0:
		return symbolRef{}, fmt.Errorf("symbol %q not found in %s", name, filepath.Base(fset.Position(file.Pos()).Filename))
	case 1:
		return matches[0], nil
	default:
		return symbolRef{}, fmt.Errorf("symbol %q matches %d declarations; disambiguate with kind", name, len(matches))
	}
}

// kindMatches reports whether the requested kind matches the element kind.
// An empty request matches anything.
func kindMatches(want, elem string) bool {
	return want == "" || want == elem
}

// refFor builds a symbolRef from a node, extending the start back onto the
// node's doc comment so the comment is deleted with the declaration.
func refFor(fset *token.FileSet, doc *ast.CommentGroup, pos, end token.Pos) symbolRef {
	start := fset.Position(pos).Offset
	if doc != nil {
		start = fset.Position(doc.Pos()).Offset
	}
	return symbolRef{start: start, end: fset.Position(end).Offset}
}

// genSpecRef returns the byte range covering a GenDecl spec. A single-spec
// decl spans from its doc to its end (removing the whole declaration);
// otherwise only the spec's own span is removed.
func genSpecRef(fset *token.FileSet, decl *ast.GenDecl, spec ast.Spec) symbolRef {
	if len(decl.Specs) == 1 {
		return refFor(fset, decl.Doc, decl.Pos(), decl.End())
	}
	var doc *ast.CommentGroup
	if ts, ok := spec.(*ast.TypeSpec); ok {
		doc = ts.Doc
	}
	return refFor(fset, doc, spec.Pos(), spec.End())
}

// containsIdent reports whether any ident matches name.
func containsIdent(idents []*ast.Ident, name string) bool {
	for _, id := range idents {
		if id.Name == name {
			return true
		}
	}
	return false
}

func init() {
	RegisterBuiltinFactory("delete_symbol", func(cfg BuiltinConfig) (Tool, error) {
		return &deleteSymbolTool{sb: cfg.Sandbox}, nil
	})
}

// parseGoFile parses Go source with comments. filename is recorded in
// token positions (for error messages and Position().Filename); pass "" to
// skip. Shared by delete_symbol and code_index.
func parseGoFile(src []byte, filename string) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	return fset, file, nil
}
