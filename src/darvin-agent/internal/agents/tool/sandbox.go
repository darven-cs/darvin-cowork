package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// ErrNeedsPermission is returned by ResolveRead when a path is neither
	// inside the workspace root nor in the run's granted-read set. The
	// executor gate turns this into a permission_request (user approval).
	ErrNeedsPermission = errors.New("sandbox: path outside authorized roots, permission required")
)

// fsSandbox restricts file tool access to a single absolute root directory.
// All file tool paths are resolved through Resolve / openRootFile; any path
// that ends up outside the root (lexically or via symlinks) is rejected.
type fsSandbox struct {
	root       string // lexical root (as passed at construction)
	realRoot   string // EvalSymlinks(root)
	exclusions []compiledExclusion

	// grantedReads holds absolute paths the user attached for the current
	// message; read_file may open them even though they sit outside the
	// workspace root ("attach = authorize"). Mutated once per run by the
	// dispatcher, read concurrently by tool goroutines.
	mu           sync.RWMutex
	grantedReads []string
	// approvedPaths holds paths the user approved one-shot via the
	// permission modal during this run; they bypass containment for any
	// operation (read AND write). Cleared at run start/end together with
	// grantedReads.
	approvedPaths []string
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
		// 用户在权限弹窗里单次放行的路径允许越界（读或写）。
		if s.isApproved(abs) {
			return abs, nil
		}
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

// setGrantedReads replaces the run's granted-read set (absolute paths the
// user attached for the current message). Also resets the one-shot approved
// paths so every run starts from a clean slate. Called by the dispatcher
// before RunConversation and cleared after; safe for concurrent readers.
func (s *fsSandbox) setGrantedReads(paths []string) {
	s.mu.Lock()
	s.grantedReads = append([]string(nil), paths...)
	s.approvedPaths = nil
	s.mu.Unlock()
}

// approvePath records a path the user allowed one-shot via the permission
// modal; ResolveRead / Resolve then let it through despite being outside the
// workspace root.
func (s *fsSandbox) approvePath(p string) {
	ap := cleanAbsPath(p)
	if ap == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.approvedPaths {
		if x == ap {
			return
		}
	}
	s.approvedPaths = append(s.approvedPaths, ap)
}

// isApproved reports whether the cleaned absolute path was user-approved
// for this run.
func (s *fsSandbox) isApproved(cleanAbs string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.approvedPaths {
		if x == cleanAbs {
			return true
		}
	}
	return false
}

// isGranted reports whether the cleaned absolute path is in the run's
// granted-read set (same lexical path — the user attached it explicitly).
func (s *fsSandbox) isGranted(cleanAbs string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.grantedReads {
		if cleanAbsPath(g) == cleanAbs {
			return true
		}
	}
	return false
}

// ResolveRead resolves a path for reading. Paths inside the workspace root
// resolve normally (subject to exclusions); paths outside the root fall
// back to the run's granted-read set (attached files). Anything else returns
// ErrNeedsPermission so the executor can request user approval.
func (s *fsSandbox) ResolveRead(p string) (string, error) {
	abs, err := s.Resolve(p)
	if err == nil {
		return abs, nil
	}
	if errors.Is(err, ErrPathExcluded) {
		// inside the workspace but excluded by policy — the tool itself
		// rejects this; it is not an "outside authorized roots" escape.
		return "", err
	}
	ap := cleanAbsPath(p)
	if ap != "" && (s.isGranted(ap) || s.isApproved(ap)) {
		return ap, nil
	}
	return "", fmt.Errorf("%w: %q", ErrNeedsPermission, p)
}

// openFileLimitedAt opens an already-resolved absolute path and reads up to
// maxBytes bytes starting at offset. Used by read_file after ResolveRead so
// granted (out-of-workspace) attachments are openable too.
func (s *fsSandbox) openFileLimitedAt(abs, label string, offset, maxBytes int64) (*os.File, []byte, bool, error) {
	if maxBytes <= 0 || maxBytes > maxHardReadBytes {
		return nil, nil, false, fmt.Errorf("%w: maxBytes=%d", ErrReadTooLarge, maxBytes)
	}
	if offset < 0 {
		return nil, nil, false, fmt.Errorf("sandbox: negative offset %d", offset)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, false, fmt.Errorf("%s: open %q: %w", label, abs, err)
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
		return nil, nil, false, fmt.Errorf("%s: read %q: %w", label, abs, err)
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return f, data, truncated, nil
}

// cleanAbsPath returns the cleaned absolute form of p, or "" when the path
// cannot be made absolute (practically never for the tool arg shapes we see).
func cleanAbsPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
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
