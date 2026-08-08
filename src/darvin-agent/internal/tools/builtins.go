// Constructs the registry of built-in file and shell tools behind a workspace sandbox.

package tool

import (
	"errors"
	"fmt"
)

// NewBuiltins constructs a Registry pre-populated with every registered
// built-in tool (read_file / write_file / edit_file / list_dir / shell),
// each gated by a workspace sandbox rooted at workdir.
//
// If workdir is empty, the process's current working directory is used.
// If allowlist is nil, DefaultShellAllowlist is used.
//
// Returns an error only if workdir cannot be resolved to an absolute path
// or a registered factory fails.
func NewBuiltins(workdir string, allowlist []string) (*Registry, error) {
	sb, err := newFsSandbox(workdir, DefaultPathExclusions()...)
	if err != nil {
		return nil, err
	}
	reg := NewRegistry()
	reg.sb = sb
	cfg := BuiltinConfig{Sandbox: sb, Allowlist: allowlist}
	for _, name := range RegisteredBuiltinFactories() {
		t, err := builtinFactories[name](cfg)
		if err != nil {
			return nil, fmt.Errorf("tool: builtin %s: %w", name, err)
		}
		reg.MustRegister(t)
	}
	return reg, nil
}

// ErrWorkdirInvalid is returned by NewBuiltins when the workdir cannot be
// resolved to an absolute path.
var ErrWorkdirInvalid = errors.New("tool: invalid workdir")
