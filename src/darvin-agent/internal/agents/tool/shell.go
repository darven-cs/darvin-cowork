package tool

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"darvin-cowork/backend/internal/agents/llm"
)

// defaultShellAllowlist is the conservative list of commands the built-in
// shell tool will accept. Anything outside this set is rejected with
// "command not allowed". Operators can override via NewShellTool(allowlist).
var defaultShellAllowlist = []string{
	"ls", "cat", "head", "tail", "wc", "grep", "find", "echo", "pwd", "date",
	"file", "which", "env", "printenv", "uname",
	"mkdir", "cp", "mv", "rm", "stat", "du", "df", "touch",
	"tr", "cut", "sort", "uniq", "tee", "xargs", "basename", "dirname",
	"sed", "awk", "test", "true", "false",
	// 工作区可以是真实代码仓库：git + 常见构建/脚本运行时。危险子命令
	// （git push --force / git reset --hard / rm -r 等）由权限门审批拦截。
	"git", "node", "npm", "npx", "pnpm", "yarn",
	"python", "python3", "pip", "pip3",
	"go", "make", "tar", "unzip",
}

// maxShellTimeout is the hard cap on a single shell call regardless of
// caller-provided timeout_ms.
const maxShellTimeout = 5 * time.Minute

// defaultShellTimeout is used when the caller does not specify timeout_ms.
const defaultShellTimeout = 30 * time.Second

// maxShellBytes is the default cap on captured stdout / stderr per stream.
// maxHardShellBytes is the ceiling for the caller-provided max_output_bytes.
const (
	maxShellBytes     int64 = 1 << 20  // 1 MiB
	maxHardShellBytes int64 = 16 << 20 // 16 MiB
)

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
			"command": {
				Type:        "string",
				Enum:        t.allowlistSlice(),
				Description: "Command name; must be in the allowlist.",
			},
			"args":             {Type: "array", Items: &llm.ParameterProperty{Type: "string"}, Description: "Command-line arguments (string array)."},
			"cwd":              {Type: "string", Format: "path", MaxLength: ptrInt(4096), Description: "Working directory; must be inside the workspace. Defaults to the workspace root."},
			"timeout_ms":       {Type: "integer", Minimum: ptrFloat64(0), Maximum: ptrFloat64(float64(maxShellTimeout / time.Millisecond)), Description: "Timeout in milliseconds (default 30000, max 300000)."},
			"max_output_bytes": {Type: "integer", Minimum: ptrFloat64(0), Maximum: ptrFloat64(float64(maxHardShellBytes)), Description: "Per-stream output cap in bytes (default 1 MiB, max 16 MiB)."},
		},
		Required:             []string{"command", "args"},
		AdditionalProperties: ptrBool(false),
	}
}

// allowlistSlice returns the configured allowlist as a sorted slice so the
// schema's Enum is stable and reflects custom allowlists.
func (t *shellTool) allowlistSlice() []string {
	out := make([]string, 0, len(t.allowlist))
	for c := range t.allowlist {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
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

	capBytes := maxShellBytes
	if v, ok := args["max_output_bytes"].(float64); ok && v > 0 {
		capBytes = int64(v)
		if capBytes > maxHardShellBytes {
			capBytes = maxHardShellBytes
		}
	}

	c := exec.CommandContext(cctx, cmd, argv...)
	c.Dir = cwd
	out := &limitWriter{cap: capBytes}
	errOut := &limitWriter{cap: capBytes}
	c.Stdout = out
	c.Stderr = errOut
	err := c.Run()

	content := out.String()
	if errOut.Len() > 0 {
		if content != "" {
			content += "\n"
		}
		content += "[stderr]\n" + errOut.String()
	}
	if out.Truncated() {
		content += fmt.Sprintf("\n[stdout truncated at %d bytes]", capBytes)
	}
	if errOut.Truncated() {
		content += fmt.Sprintf("\n[stderr truncated at %d bytes]", capBytes)
	}
	if err != nil {
		if content != "" {
			content += "\n"
		}
		return Result{IsError: true, Content: content + "[exit] " + err.Error()}
	}
	return Result{Content: content}
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
