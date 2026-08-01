package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxHardReadBytes is the hard cap on a single read window regardless of
// caller-provided limit. read_file's `limit` param may not exceed it.
const maxHardReadBytes = 16 << 20 // 16 MiB

var (
	// ErrPathEscapes is returned when a path escapes the sandbox root
	// lexically (e.g. `../etc/passwd`).
	ErrPathEscapes = errors.New("sandbox: path escapes sandbox root")
	// ErrPathEscapesViaSymlink is returned when a path resolves (through
	// symlinks) to a location outside the sandbox root.
	ErrPathEscapesViaSymlink = errors.New("sandbox: path escapes sandbox root via symlink")
	// ErrPathExcluded is returned when a path component matches a
	// workspace exclusion pattern.
	ErrPathExcluded = errors.New("sandbox: path excluded by workspace filter")
	// ErrReadTooLarge is returned when a read window exceeds maxHardReadBytes.
	ErrReadTooLarge = errors.New("sandbox: read exceeds hard limit")
)

// fsSandbox restricts file tool access to a single absolute root directory.
// All file tool paths are resolved through Resolve / openRootFile; any path
// that ends up outside the root (lexically or via symlinks) is rejected.
type fsSandbox struct {
	root       string // lexical root (as passed at construction)
	realRoot   string // EvalSymlinks(root)
	exclusions []compiledExclusion
}

// newFsSandbox creates a sandbox rooted at workdir. workdir is converted to
// an absolute, cleaned path; its symlink-resolved form is stored as the
// containment baseline. exclusions defaults to none when omitted; production
// wiring passes DefaultPathExclusions via NewBuiltins.
func newFsSandbox(workdir string, exclusions ...string) (*fsSandbox, error) {
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
	abs = filepath.Clean(abs)
	real := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		real = r
	}
	excl, err := compileExclusions(exclusions)
	if err != nil {
		return nil, fmt.Errorf("sandbox: compile exclusions: %w", err)
	}
	return &fsSandbox{root: abs, realRoot: real, exclusions: excl}, nil
}

// Resolve takes a path (absolute or relative) and returns its absolute,
// cleaned form if and only if it is inside the sandbox root. Otherwise it
// returns an error. Relative paths are anchored at the sandbox root, NOT at
// the process's current working directory — that is the whole point of the
// sandbox.
//
// Containment is checked against the symlink-resolved form of the path:
// existing symlinks are followed (a target outside the root is rejected),
// and for non-existent paths the deepest existing ancestor is resolved and
// the literal remainder is appended. A non-existent path is not an error at
// this layer (callers get the resolved abs and see ENOENT themselves).
func (s *fsSandbox) Resolve(p string) (string, error) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(s.root, p))
	}
	if pattern, ok := matchExclusion(s.exclusions, abs); ok {
		return "", fmt.Errorf("%w: %q matches pattern %q", ErrPathExcluded, abs, pattern)
	}
	real, err := evalPathReal(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrPathEscapesViaSymlink, p, err)
	}
	if pattern, ok := matchExclusion(s.exclusions, real); ok {
		return "", fmt.Errorf("%w: %q matches pattern %q", ErrPathExcluded, real, pattern)
	}
	if err := s.checkContained(real); err != nil {
		return "", fmt.Errorf("%w: %q resolves to %q outside root %q",
			ErrPathEscapesViaSymlink, p, real, s.realRoot)
	}
	return abs, nil
}

// checkContained reports whether real is inside the symlink-resolved root.
func (s *fsSandbox) checkContained(real string) error {
	rel, err := filepath.Rel(s.realRoot, real)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrPathEscapes
	}
	return nil
}

// openRootFile opens a file inside the root, re-evaluating its symlink
// resolution immediately before Open so a swap between resolution and open
// cannot redirect the fd to a different inode. The caller owns the returned
// *os.File and must Close it.
func (s *fsSandbox) openRootFile(p string, label string) (*os.File, string, error) {
	abs, err := s.Resolve(p)
	if err != nil {
		return nil, "", err
	}
	real, err := evalPathReal(abs)
	if err != nil {
		return nil, "", fmt.Errorf("%s: resolve %q: %w", label, p, err)
	}
	if err := s.checkContained(real); err != nil {
		return nil, "", fmt.Errorf("%s: %w: %q resolves outside root", label, ErrPathEscapesViaSymlink, p)
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, "", fmt.Errorf("%s: open %q: %w", label, real, err)
	}
	return f, real, nil
}

// openRootFileLimited opens a file inside the root, seeks to offset, and
// reads up to maxBytes bytes. offset semantics are preserved (a caller that
// passes offset reads from there, not from the file start). The returned
// fd is already positioned past the window; the caller owns it and must
// Close it. truncated reports whether the window was cut short.
func (s *fsSandbox) openRootFileLimited(p, label string, offset, maxBytes int64) (*os.File, []byte, bool, error) {
	if maxBytes <= 0 || maxBytes > maxHardReadBytes {
		return nil, nil, false, fmt.Errorf("%w: maxBytes=%d", ErrReadTooLarge, maxBytes)
	}
	if offset < 0 {
		return nil, nil, false, fmt.Errorf("sandbox: negative offset %d", offset)
	}
	f, real, err := s.openRootFile(p, label)
	if err != nil {
		return nil, nil, false, err
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, nil, false, fmt.Errorf("%s: seek offset %d: %w", label, offset, err)
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		f.Close()
		return nil, nil, false, fmt.Errorf("%s: read %q: %w", label, real, err)
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return f, data, truncated, nil
}

// evalPathReal returns the symlink-resolved form of abs. When abs (or an
// intermediate component) does not exist, it resolves the deepest existing
// ancestor and appends the literal remainder, so containment checks still
// work for paths that are about to be created.
func evalPathReal(abs string) (string, error) {
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return real, nil
	}
	if !os.IsNotExist(err) {
		// ELOOP / EACCES on resolution: we cannot prove containment.
		return "", err
	}
	var tail []string
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		if r, e := filepath.EvalSymlinks(parent); e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// DefaultPathExclusions returns the conservative built-in component-level
// exclusion list. Any file-tool path that contains one of these components
// is rejected regardless of whether it sits inside the sandbox root.
func DefaultPathExclusions() []string {
	return []string{
		".git",
		"node_modules",
		".venv",
		"venv",
		"__pycache__",
		".env",
		".env.*",
		"target",
		"dist",
		"build",
		".next",
		".turbo",
		".DS_Store",
		"Thumbs.db",
	}
}

// Root returns the lexical sandbox root (for diagnostics).
func (s *fsSandbox) Root() string { return s.root }

// RealRoot returns the symlink-resolved sandbox root (for diagnostics).
func (s *fsSandbox) RealRoot() string { return s.realRoot }
