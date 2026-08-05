package tool

import (
	"errors"
	"regexp"
	"strings"

	"darvin-cowork/backend/internal/agents/protocol"
)

// PermissionEval is the outcome of evaluating whether a tool call needs user
// approval before execution (permission gate).
type PermissionEval = protocol.PermissionEval

var (
	// destructivePatterns match commands that can irreversibly destroy data.
	// Checked before cautionPatterns; the first hit wins.
	destructivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-r(?:f)?\b`),                          // rm -r / rm -rf
		regexp.MustCompile(`\bgit\s+[^\n;|]*\bpush\b[^\n;|]*--?force\b`), // git push --force / -f
		regexp.MustCompile(`\bgit\s+[^\n;|]*\breset\b[^\n;|]*--hard\b`),  // git reset --hard
		regexp.MustCompile(`\bdd\b`),                                     // block-device copy
		regexp.MustCompile(`\bmkfs(\.[a-z0-9]+)?\b`),                     // mkfs.*
		regexp.MustCompile(`\bshutdown\b|\breboot\b|\binit\s+0\b`),
		regexp.MustCompile(`:\(\)\{`), // fork bomb
		regexp.MustCompile(`\bchmod\s+-R\b|\bchown\s+-R\b`),
		regexp.MustCompile(`\bfind\s+[^\n;|]*\s-delete\b`),
	}
	// cautionPatterns match operations that are risky but reversible.
	cautionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brm\b`),                    // single-file rm
		regexp.MustCompile(`\bgit\s+[^\n;|]*\bpush\b`),  // git push
		regexp.MustCompile(`\bgit\s+[^\n;|]*\bclean\b`), // git clean
		regexp.MustCompile(`\bchmod\b|\bchown\b`),
		regexp.MustCompile(`\bkill\b|\bpkill\b`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bmv\b|\bcp\b`),
	}
)

// commandLine reconstructs a shell command line from the tool's command +
// args params for pattern matching.
func commandLine(args map[string]any) string {
	cmd, _ := args["command"].(string)
	argv := toStrSlice(args["args"])
	parts := make([]string, 0, len(argv)+1)
	if cmd != "" {
		parts = append(parts, cmd)
	}
	parts = append(parts, argv...)
	return strings.Join(parts, " ")
}

// ClassifyPermission returns the danger level for a tool call. Only the shell
// tool carries command-level danger today; file tools inside the workspace are
// the authorized generation area and stay safe (write/edit = authorized root).
func ClassifyPermission(toolName string, args map[string]any) (level, reason string, need bool) {
	if toolName != "shell" {
		return "safe", "", false
	}
	line := commandLine(args)
	for _, p := range destructivePatterns {
		if p.MatchString(line) {
			return "destructive", "destructive command: " + line, true
		}
	}
	for _, p := range cautionPatterns {
		if p.MatchString(line) {
			return "caution", "potentially destructive command: " + line, true
		}
	}
	return "safe", "", false
}

// EvaluatePermission combines path-containment ("authorized roots") with
// command-level danger into one decision for the executor gate.
func (r *Registry) EvaluatePermission(toolName string, args map[string]any) PermissionEval {
	if reason, path, escaped := r.pathEscape(toolName, args); escaped {
		return PermissionEval{Authorized: false, Need: true, Level: "caution", Reason: reason, EscapedPath: path}
	}
	if level, reason, need := ClassifyPermission(toolName, args); need {
		return PermissionEval{Authorized: true, Need: true, Level: level, Reason: reason}
	}
	return PermissionEval{Authorized: true, Need: false, Level: "safe"}
}

// pathEscape reports whether a tool's path arguments leave the authorized
// roots. read_file may also touch the run's granted-read set (attached files);
// write/edit/list/shell must stay inside the workspace root. Paths rejected by
// an exclusion pattern (inside the workspace but filtered) are not "escapes" —
// the tool denies them on its own, no approval loop.
func (r *Registry) pathEscape(toolName string, args map[string]any) (reason, path string, escaped bool) {
	if r.sb == nil {
		// hand-assembled registry (tests / custom tools): no containment
		// baseline, so nothing counts as escaping.
		return "", "", false
	}
	switch toolName {
	case "read_file":
		p, _ := args["path"].(string)
		_, err := r.sb.ResolveRead(p)
		if err == nil {
			return "", "", false
		}
		if errors.Is(err, ErrPathExcluded) {
			return "", "", false
		}
		return "read path outside authorized roots: " + p, p, true
	case "write_file", "edit_file", "list_dir":
		p, _ := args["path"].(string)
		_, err := r.sb.Resolve(p)
		if err == nil {
			return "", "", false
		}
		if errors.Is(err, ErrPathExcluded) {
			return "", "", false
		}
		return "path outside workspace: " + p, p, true
	case "shell":
		cwd, _ := args["cwd"].(string)
		if cwd == "" {
			return "", "", false
		}
		if _, err := r.sb.Resolve(cwd); err == nil {
			return "", "", false
		}
		return "working directory outside workspace: " + cwd, cwd, true
	}
	return "", "", false
}
