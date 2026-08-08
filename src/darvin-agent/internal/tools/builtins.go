// Constructs the registry of built-in file and shell tools behind a workspace sandbox.

package tool

import "errors"

// NewBuiltins constructs a Registry pre-populated with the 5 built-in
// tools (read_file / write_file / edit_file / list_dir / shell) gated by
// a workspace sandbox rooted at workdir.
//
// If workdir is empty, the process's current working directory is used.
// If allowlist is nil, DefaultShellAllowlist is used.
//
// Returns an error only if workdir cannot be resolved to an absolute path.
func NewBuiltins(workdir string, allowlist []string) (*Registry, error) {
	sb, err := newFsSandbox(workdir, DefaultPathExclusions()...)
	if err != nil {
		return nil, err
	}
	reg := NewRegistry()
	reg.sb = sb
	reg.MustRegister(&readFileTool{sb: sb})
	reg.MustRegister(&writeFileTool{sb: sb})
	reg.MustRegister(&editFileTool{sb: sb})
	reg.MustRegister(&listDirTool{sb: sb})
	reg.MustRegister(newShellTool(sb, allowlist))
	return reg, nil
}

// ErrWorkdirInvalid is returned by NewBuiltins when the workdir cannot be
// resolved to an absolute path.
var ErrWorkdirInvalid = errors.New("tool: invalid workdir")
