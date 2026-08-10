// Subagent built-in tools: delegate_subagent / list_subagents /
// abort_subagent / parallel_subagents / read_subagent_result. They
// reach the per-session *subagent.Manager through ctx (set by the
// executor) and surface a manager-missing sentinel error otherwise.

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/subagent"
)

// subagentMissingMsg is returned when a tool runs outside a session
// that has a wired subagent manager (e.g. a unit test).
const subagentMissingMsg = "subagent support not available in this context"

// managerFromCtx extracts the *subagent.Manager from ctx, falling back
// to a stub error Result when missing.
func managerFromCtx(ctx context.Context) (*subagent.Manager, protocol.Result) {
	m, ok := subagent.FromContext(ctx)
	if !ok || m == nil {
		return nil, protocol.Result{IsError: true, Content: subagentMissingMsg}
	}
	return m, protocol.Result{}
}

// defaultSubagentTimeoutMs is the default sync run timeout when the
// caller leaves timeout_ms unspecified.
const defaultSubagentTimeoutMs = 300_000

// maxSubagentTimeoutMs caps caller-supplied timeouts at 10 minutes.
const maxSubagentTimeoutMs = 600_000

// maxParallelTasks caps parallel_subagents input size.
const maxParallelTasks = 64

// defaultReadResultLimit mirrors subagent.DefaultPageSize.
const defaultReadResultLimit = 12 * 1024

// maxReadResultLimit mirrors subagent.MaxPageSize.
const maxReadResultLimit = 24 * 1024

// delegateSubagentTool is a built-in Tool.
type delegateSubagentTool struct{}

// Name returns the public tool name.
func (delegateSubagentTool) Name() string { return "delegate_subagent" }

// Description is the LLM-facing tool description.
func (delegateSubagentTool) Description() string {
	return "Delegate a self-contained task to a sub-agent. The sub-agent runs in isolation: it sees only the system prompt + workspace context + the prompt you pass; it does NOT see your conversation history. Use run_in_background=true to fire-and-forget; otherwise the call blocks until the sub-agent finishes. Need parallel runs? Use parallel_subagents instead of issuing multiple delegate_subagent in the same turn."
}

// Parameters is the JSON Schema for the tool.
func (delegateSubagentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt":           {"type": "string", "description": "Self-contained task description."},
			"description":      {"type": "string", "description": "Short label (3-7 chars) used as display name."},
			"scope":            {"type": "array", "items": {"type": "string"}, "description": "Tool whitelist; empty = safe read-only defaults."},
			"model":            {"type": "string", "description": "Override parent model; empty inherits."},
			"run_in_background":{"type": "boolean", "default": false},
			"timeout_ms":       {"type": "integer", "default": 300000, "minimum": 1, "maximum": 600000}
		},
		"required": ["prompt", "description"],
		"additionalProperties": false
	}`)
}

// Execute dispatches the spawn.
func (delegateSubagentTool) Execute(ctx context.Context, args map[string]any) protocol.Result {
	mgr, errRes := managerFromCtx(ctx)
	if errRes.Content != "" {
		return errRes
	}
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return protocol.Result{IsError: true, Content: "prompt is required"}
	}
	description, _ := args["description"].(string)
	spec := protocol.SubagentSpec{
		Prompt:          prompt,
		Description:     description,
		Model:           stringArg(args, "model"),
		RunInBackground: boolArg(args, "run_in_background"),
		TimeoutMs:       intArg(args, "timeout_ms", defaultSubagentTimeoutMs),
	}
	if v, ok := args["scope"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				spec.Scope = append(spec.Scope, s)
			}
		}
	}
	if spec.TimeoutMs > maxSubagentTimeoutMs {
		spec.TimeoutMs = maxSubagentTimeoutMs
	}
	if spec.TimeoutMs <= 0 {
		spec.TimeoutMs = defaultSubagentTimeoutMs
	}

	info, err := mgr.Spawn(ctx, spec)
	if err != nil {
		return protocol.Result{IsError: true, Content: fmt.Sprintf("delegate_subagent failed: %v", err)}
	}
	if spec.RunInBackground {
		return protocol.Result{Content: fmt.Sprintf("started background subagent %s", info.ID)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[subagent %s status: %s | %dms | %d tool calls]\n",
		info.ID, info.Status, info.DurationMs, info.ToolCalls)
	b.WriteString(info.ResultText)
	if info.Status == protocol.SubagentError || info.Status == protocol.SubagentAborted || info.Status == protocol.SubagentTimeout {
		if info.ErrorMsg != "" {
			fmt.Fprintf(&b, "\n[error: %s]", info.ErrorMsg)
		}
	}
	return protocol.Result{Content: b.String()}
}

// listSubagentsTool enumerates the per-session runs.
type listSubagentsTool struct{}

func (listSubagentsTool) Name() string { return "list_subagents" }
func (listSubagentsTool) Description() string {
	return "List sub-agent runs spawned by the current session. Output rows: <id>  <description>  <status>  <duration_ms>."
}
func (listSubagentsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (listSubagentsTool) Execute(ctx context.Context, _ map[string]any) protocol.Result {
	mgr, errRes := managerFromCtx(ctx)
	if errRes.Content != "" {
		return errRes
	}
	rs := mgr.List()
	if len(rs) == 0 {
		return protocol.Result{Content: "no subagents"}
	}
	var b strings.Builder
	for _, r := range rs {
		desc := r.Description
		if desc == "" {
			desc = r.ID
		}
		fmt.Fprintf(&b, "%s  %s  %s  %dms\n", r.ID, desc, r.Status, r.DurationMs)
	}
	return protocol.Result{Content: strings.TrimRight(b.String(), "\n")}
}

// abortSubagentTool cancels a running sub-agent.
type abortSubagentTool struct{}

func (abortSubagentTool) Name() string { return "abort_subagent" }
func (abortSubagentTool) Description() string {
	return "Cancel a running sub-agent by id. Idempotent; returns 'aborted subagent <id>' on success."
}
func (abortSubagentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "Subagent run id returned by delegate_subagent / parallel_subagents."}
		},
		"required": ["id"],
		"additionalProperties": false
	}`)
}

func (abortSubagentTool) Execute(ctx context.Context, args map[string]any) protocol.Result {
	mgr, errRes := managerFromCtx(ctx)
	if errRes.Content != "" {
		return errRes
	}
	id, _ := args["id"].(string)
	if id == "" {
		return protocol.Result{IsError: true, Content: "id is required"}
	}
	if err := mgr.Abort(id); err != nil {
		return protocol.Result{IsError: true, Content: fmt.Sprintf("abort failed: %v", err)}
	}
	return protocol.Result{Content: fmt.Sprintf("aborted subagent %s", id)}
}

// parallelSubagentsTool spawns up to 64 sub-agents and blocks on all.
type parallelSubagentsTool struct{}

func (parallelSubagentsTool) Name() string { return "parallel_subagents" }
func (parallelSubagentsTool) Description() string {
	return "Spawn 1-64 sub-agents in parallel; block until every one finishes. Returns N sections separated by '---', one per task, in input order. Each sub-agent is independent and isolated like delegate_subagent."
}
func (parallelSubagentsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {"type": "string"},
			"tasks": {
				"type": "array",
				"minItems": 1,
				"maxItems": 64,
				"items": {
					"type": "object",
					"properties": {
						"prompt":      {"type": "string"},
						"description": {"type": "string"},
						"scope":       {"type": "array", "items": {"type": "string"}},
						"model":       {"type": "string"},
						"timeout_ms":  {"type": "integer", "default": 300000, "maximum": 600000}
					},
					"required": ["prompt", "description"],
					"additionalProperties": false
				}
			}
		},
		"required": ["tasks"],
		"additionalProperties": false
	}`)
}

type parallelTask struct {
	Prompt      string
	Description string
	Scope       []string
	Model       string
	TimeoutMs   int
}

func (parallelSubagentsTool) Execute(ctx context.Context, args map[string]any) protocol.Result {
	mgr, errRes := managerFromCtx(ctx)
	if errRes.Content != "" {
		return errRes
	}
	rawTasks, _ := args["tasks"].([]any)
	if len(rawTasks) == 0 {
		return protocol.Result{IsError: true, Content: "tasks is required and must be non-empty"}
	}
	if len(rawTasks) > maxParallelTasks {
		return protocol.Result{IsError: true, Content: fmt.Sprintf("parallel_subagents: max %d tasks, got %d", maxParallelTasks, len(rawTasks))}
	}
	tasks := make([]parallelTask, 0, len(rawTasks))
	for i, raw := range rawTasks {
		m, _ := raw.(map[string]any)
		if m == nil {
			return protocol.Result{IsError: true, Content: fmt.Sprintf("tasks[%d] must be an object", i)}
		}
		prompt, _ := m["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			return protocol.Result{IsError: true, Content: fmt.Sprintf("tasks[%d].prompt is required", i)}
		}
		t := parallelTask{
			Prompt:      prompt,
			Description: stringArg(m, "description"),
			Model:       stringArg(m, "model"),
			TimeoutMs:   intArg(m, "timeout_ms", defaultSubagentTimeoutMs),
		}
		if v, ok := m["scope"].([]any); ok {
			for _, item := range v {
				if s, ok := item.(string); ok {
					t.Scope = append(t.Scope, s)
				}
			}
		}
		if t.TimeoutMs > maxSubagentTimeoutMs {
			t.TimeoutMs = maxSubagentTimeoutMs
		}
		if t.TimeoutMs <= 0 {
			t.TimeoutMs = defaultSubagentTimeoutMs
		}
		tasks = append(tasks, t)
	}

	type result struct {
		info protocol.SubagentInfo
		body string
	}
	results := make([]result, len(tasks))
	for i, t := range tasks {
		idx := i
		go func() {
			spec := protocol.SubagentSpec{
				Prompt:      t.Prompt,
				Description: t.Description,
				Scope:       t.Scope,
				Model:       t.Model,
				TimeoutMs:   t.TimeoutMs,
			}
			info, err := mgr.Spawn(ctx, spec)
			var b strings.Builder
			if err != nil {
				fmt.Fprintf(&b, "[task %d failed to spawn: %v]\n", idx, err)
				results[idx] = result{body: b.String()}
				return
			}
			fmt.Fprintf(&b, "[subagent %s status: %s | %dms | %d tool calls]\n",
				info.ID, info.Status, info.DurationMs, info.ToolCalls)
			b.WriteString(info.ResultText)
			if info.Status == protocol.SubagentError || info.Status == protocol.SubagentAborted || info.Status == protocol.SubagentTimeout {
				if info.ErrorMsg != "" {
					fmt.Fprintf(&b, "\n[error: %s]", info.ErrorMsg)
				}
			}
			results[idx] = result{info: *info, body: b.String()}
		}()
	}

	deadline := time.Now().Add(time.Duration(longestTimeoutMs(tasks)) * time.Millisecond)
	for time.Now().Before(deadline) {
		allDone := true
		for _, r := range results {
			if r.info.ID == "" {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		select {
		case <-ctx.Done():
			return protocol.Result{IsError: true, Content: fmt.Sprintf("parallel_subagents cancelled: %v", ctx.Err())}
		case <-time.After(50 * time.Millisecond):
		}
	}

	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(r.body)
	}
	return protocol.Result{Content: b.String()}
}

func longestTimeoutMs(tasks []parallelTask) int {
	max := 0
	for _, t := range tasks {
		if t.TimeoutMs > max {
			max = t.TimeoutMs
		}
	}
	return max
}

// readSubagentResultTool returns a byte-offset window of a run's result.
type readSubagentResultTool struct{}

func (readSubagentResultTool) Name() string { return "read_subagent_result" }
func (readSubagentResultTool) Description() string {
	return "Read the buffered final result of a sub-agent run. Supports byte-offset pagination via offset_bytes (default 0) and limit_bytes (default 12288, max 24576)."
}
func (readSubagentResultTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ref":         {"type": "string", "description": "Subagent run id."},
			"offset_bytes":{"type": "integer", "default": 0, "minimum": 0},
			"limit_bytes": {"type": "integer", "default": 12288, "minimum": 1, "maximum": 24576}
		},
		"required": ["ref"],
		"additionalProperties": false
	}`)
}

func (readSubagentResultTool) Execute(ctx context.Context, args map[string]any) protocol.Result {
	mgr, errRes := managerFromCtx(ctx)
	if errRes.Content != "" {
		return errRes
	}
	ref, _ := args["ref"].(string)
	if ref == "" {
		return protocol.Result{IsError: true, Content: "ref is required"}
	}
	offset := intArg(args, "offset_bytes", 0)
	limit := intArg(args, "limit_bytes", defaultReadResultLimit)
	if limit > maxReadResultLimit {
		limit = maxReadResultLimit
	}
	if limit <= 0 {
		limit = defaultReadResultLimit
	}
	text, err := mgr.ReadResult(ref, offset, limit)
	if err != nil {
		return protocol.Result{IsError: true, Content: fmt.Sprintf("read failed: %v", err)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[offset %d, returned %d of %d bytes]\n", offset, len(text), offset+len(text))
	b.WriteString(text)
	return protocol.Result{Content: b.String()}
}

// Register the five tools at package init so NewBuiltins picks them up.
func init() {
	RegisterBuiltinFactory("delegate_subagent", func(_ BuiltinConfig) (Tool, error) {
		return delegateSubagentTool{}, nil
	})
	RegisterBuiltinFactory("list_subagents", func(_ BuiltinConfig) (Tool, error) {
		return listSubagentsTool{}, nil
	})
	RegisterBuiltinFactory("abort_subagent", func(_ BuiltinConfig) (Tool, error) {
		return abortSubagentTool{}, nil
	})
	RegisterBuiltinFactory("parallel_subagents", func(_ BuiltinConfig) (Tool, error) {
		return parallelSubagentsTool{}, nil
	})
	RegisterBuiltinFactory("read_subagent_result", func(_ BuiltinConfig) (Tool, error) {
		return readSubagentResultTool{}, nil
	})
}

// stringArg reads an optional string arg.
func stringArg(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// intArg reads an optional int arg with default.
func intArg(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

// boolArg reads an optional bool arg.
func boolArg(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}
