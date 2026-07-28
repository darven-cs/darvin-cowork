package tool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"darvin-cowork/backend/internal/agent/llm"
)

// defaultShellAllowlist is the conservative list of commands the built-in
// shell tool will accept. Anything outside this set is rejected with
// "command not allowed". Operators can override via NewShellTool(allowlist).
var defaultShellAllowlist = []string{
	"ls", "cat", "head", "tail", "wc", "grep", "find", "echo", "pwd", "date",
	"file", "which", "env", "printenv", "uname",
	"mkdir", "cp", "mv", "rm", "stat", "du", "df",
	"tr", "cut", "sort", "uniq", "tee", "xargs", "basename", "dirname",
	"sed", "awk", "test", "true", "false",
}

// maxShellTimeout is the hard cap on a single shell call regardless of
// caller-provided timeout_ms.
const maxShellTimeout = 5 * time.Minute

// defaultShellTimeout is used when the caller does not specify timeout_ms.
const defaultShellTimeout = 30 * time.Second

// shellTool runs a single allowlisted command in the workspace sandbox.
type shellTool struct {
	sb        *fsSandbox
	allowlist map[string]struct{}
}

func newShellTool(sb *fsSandbox, allowlist []string) *shellTool {
	if allowlist == nil {
		allowlist = defaultShellAllowlist
	}
	set := make(map[string]struct{}, len(allowlist))
	for _, c := range allowlist {
		set[c] = struct{}{}
	}
	return &shellTool{sb: sb, allowlist: set}
}

func (t *shellTool) Name() string { return "shell" }
func (t *shellTool) Description() string {
	return "Run a single shell command. The command must be in the allowlist; the working directory must be inside the workspace. Use timeout_ms to cap runtime (default 30s, max 5min)."
}
func (t *shellTool) Parameters() llm.ParameterSchema {
	return llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"command":    {Type: "string", Description: "Command name; must be in the allowlist."},
			"args":       {Type: "array", Description: "Command-line arguments (string array)."},
			"cwd":        {Type: "string", Description: "Working directory; must be inside the workspace. Defaults to the workspace root."},
			"timeout_ms": {Type: "integer", Description: "Timeout in milliseconds (default 30000, max 300000)."},
		},
		Required: []string{"command", "args"},
	}
}

func (t *shellTool) Execute(ctx context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	cmd, _ := args["command"].(string)
	if _, ok := t.allowlist[cmd]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("command not allowed: %s", cmd)}
	}
	argv := toStrSlice(args["args"])

	cwd := t.sb.root
	if v, ok := args["cwd"].(string); ok && v != "" {
		resolved, err := t.sb.Resolve(v)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}
		}
		cwd = resolved
	}

	timeout := defaultShellTimeout
	if v, ok := args["timeout_ms"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}
	if timeout > maxShellTimeout {
		timeout = maxShellTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(cctx, cmd, argv...)
	c.Dir = cwd
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()

	out := stdout.String()
	if stderr.Len() > 0 {
		if out != "" {
			out += "\n"
		}
		out += "[stderr]\n" + stderr.String()
	}
	if err != nil {
		if out != "" {
			out += "\n"
		}
		return Result{IsError: true, Content: out + "[exit] " + err.Error()}
	}
	return Result{Content: out}
}

func toStrSlice(v any) []string {
	xs, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// DefaultShellAllowlist returns a copy of the built-in allowlist. Useful
// for diagnostics and tests.
func DefaultShellAllowlist() []string {
	out := make([]string, len(defaultShellAllowlist))
	copy(out, defaultShellAllowlist)
	return out
}
