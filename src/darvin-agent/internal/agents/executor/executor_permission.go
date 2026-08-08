// Permission request / result types for the executor's approval flow.

package executor

// PermissionRequest is a tool call the executor wants user approval for
// before running. RequestID is minted by the Agent.
type PermissionRequest struct {
	ToolName    string
	ToolInput   map[string]any
	DangerLevel string // safe | caution | destructive
	Reason      string
}

// PermissionResult is the renderer's answer (via agent.permission_response).
type PermissionResult struct {
	Behavior     string // "allow" | "deny"
	UpdatedInput map[string]any
	Message      string
	Interrupt    bool
	Remember     bool
}
