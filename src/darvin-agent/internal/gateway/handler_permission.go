// Permission-response JSON-RPC handler for the approval modal flow.

package gateway

import (
	"context"
	"encoding/json"

	"darvin-cowork/backend/internal/agents/executor"
)

type PermissionResponseParams struct {
	SessionID    string         `json:"sessionId"`
	RequestID    string         `json:"requestId"`
	Behavior     string         `json:"behavior"` // allow | deny
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Message      string         `json:"message,omitempty"`
	Interrupt    bool           `json:"interrupt,omitempty"`
	Remember     bool           `json:"remember,omitempty"`
}

// handlePermissionResponse delivers the renderer's answer to the Agent's
// pending permission_request, unblocking the waiting tool call.
func handlePermissionResponse(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p PermissionResponseParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" || p.RequestID == "" || (p.Behavior != "allow" && p.Behavior != "deny") {
		return errorResp(id, CodeInvalidParams, "sessionId, requestId and behavior (allow|deny) are required", nil)
	}
	entry, err := c.sessions.GetOrCreateEntry(p.SessionID)
	if err != nil || entry.SessionRuntime == nil || entry.SessionRuntime.Agent == nil {
		return errorResp(id, CodeAgentInitFailed, "no agent bound for session", nil)
	}
	entry.SessionRuntime.Agent.ResolvePermission(p.RequestID, executor.PermissionResult{
		Behavior:     p.Behavior,
		UpdatedInput: p.UpdatedInput,
		Message:      p.Message,
		Interrupt:    p.Interrupt,
		Remember:     p.Remember,
	})
	return successResp(id, map[string]bool{"resolved": true})
}

// toImportedFileWire projects a store.ImportedFile onto the wire shape.
