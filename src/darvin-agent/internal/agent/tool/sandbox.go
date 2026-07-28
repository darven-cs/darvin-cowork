package tool

import (
	"fmt"
	"path/filepath"
	"strings"
)

// fsSandbox restricts file tool access to a single absolute root directory.
// All file tool paths are resolved through Resolve; any path that ends up
// outside the root is rejected.
type fsSandbox struct {
	root string
}

// newFsSandbox creates a sandbox rooted at workdir. workdir is converted
// to an absolute, cleaned path. If workdir is empty, the process's current
// working directory is used.
func newFsSandbox(workdir string) (*fsSandbox, error) {
	if workdir == "" {
		wd, err := filepath.Abs(".")
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve cwd: %w", err)
		}
		workdir = wd
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: abs %q: %w", workdir, err)
	}
	return &fsSandbox{root: filepath.Clean(abs)}, nil
}

// Resolve takes a path (absolute or relative) and returns its absolute,
// cleaned form if and only if it is inside the sandbox root. Otherwise it
// returns an error. Relative paths are anchored at the sandbox root, NOT
// at the process's current working directory — that is the whole point of
// the sandbox. A non-existent path is not an error at this layer (callers
// get the resolved abs and see ENOENT themselves).
func (s *fsSandbox) Resolve(p string) (string, error) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(s.root, p))
	}
	rel, err := filepath.Rel(s.root, abs)
	if err != nil {
		return "", fmt.Errorf("sandbox: %q escapes sandbox: %w", p, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("sandbox: %q escapes sandbox root %q", p, s.root)
	}
	return abs, nil
}

// Root returns the sandbox root (for diagnostics).
func (s *fsSandbox) Root() string { return s.root }
