// Package subagent manages sub-agent runs spawned from a parent session.
// Each run uses a scoped tool registry (read-only by default); the
// Manager schedules against MaxConcurrent and persists lifecycle to
// SubagentStore.
package subagent

// defaultScope is the read-only whitelist for an empty Spec.Scope.
func defaultScope() []string {
	return []string{
		"read_file",
		"grep",
		"glob",
		"list_dir",
		"web_fetch",
		"code_index",
	}
}

// alwaysForbidden lists tools no sub-agent scope may include. Sub-agent
// tools enforce depth=1; shell / jobs tools prevent bg-job escalation.
var alwaysForbidden = map[string]struct{}{
	"shell":                {},
	"bash_output":          {},
	"wait":                 {},
	"kill_shell":           {},
	"delegate_subagent":    {},
	"list_subagents":       {},
	"abort_subagent":       {},
	"parallel_subagents":   {},
	"read_subagent_result": {},
}

// ResolveScope merges the requested whitelist with default + forbidden
// policy: empty input → defaultScope, forbidden / duplicate names are
// dropped, and an all-forbidden input falls back to defaultScope.
func ResolveScope(requested []string) []string {
	if len(requested) == 0 {
		out := make([]string, len(defaultScope()))
		copy(out, defaultScope())
		return out
	}
	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))
	for _, name := range requested {
		if _, banned := alwaysForbidden[name]; banned {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		out = append(out, defaultScope()...)
	}
	return out
}
