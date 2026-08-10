// Skills, tools, and user-skill-invocation JSON-RPC handlers.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"darvin-cowork/backend/internal/sessionruntime"
	"darvin-cowork/backend/internal/skills"
)

type ListSkillsResult struct {
	Skills []skills.SkillSummaryWire `json:"skills"`
}

// SetSkillEnabledParams is the JSON-RPC params for
// agent.skills.set_enabled. Caller is main: it persists to its own SQLite
// first, then asks Go to flip the in-memory flag.
type SetSkillEnabledParams struct {
	SkillID string `json:"skillId"`
	Enabled bool   `json:"enabled"`
}

// SetSkillEnabledResult is the JSON-RPC result for agent.skills.set_enabled.
type SetSkillEnabledResult struct {
	OK bool `json:"ok"`
}

// BootstrapSkillsParams is the JSON-RPC params for agent.skills.bootstrap.
// main is the source of truth for the enabled state: at startup it
// bulk-pushes the enabled flags from its SQLite, and Go overwrites the
// local defaults with the main-side values.
type BootstrapSkillsParams struct {
	Skills []skills.SkillSummaryWire `json:"skills"`
}

// BootstrapSkillsResult is the JSON-RPC result for agent.skills.bootstrap.
type BootstrapSkillsResult struct {
	OK bool `json:"ok"`
}

// ToolDescriptorWire is one entry of agent.tools.list. Mirrors
// DarvinToolDescriptor in src/shared/darvin-api.ts.
type ToolDescriptorWire struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ListToolsResult is the JSON-RPC result for agent.tools.list.
type ListToolsResult struct {
	Tools []ToolDescriptorWire `json:"tools"`
}

// ListToolsParams is the optional sessionId for agent.tools.list. When
// omitted the default session's tool surface is returned — all sessions
// share the same plugin registries, so the surface is identical.
type ListToolsParams struct {
	SessionID string `json:"sessionId,omitempty"`
}

// handleListTools returns the merged agent tool registry view for the
// given session (default when omitted). When the session has not yet
// built an SessionRuntime we lazily build it once so the first query
// already includes the full skill / mcp plugin surface.
func handleListTools(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Sessions == nil {
		return successResp(id, ListToolsResult{Tools: []ToolDescriptorWire{}})
	}
	var p ListToolsParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	entry, err := h.Sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), err)
	}
	if entry.SessionRuntime == nil {
		return successResp(id, ListToolsResult{Tools: []ToolDescriptorWire{}})
	}
	reg := entry.SessionRuntime.Agent.Tools()
	entries := reg.List()
	out := make([]ToolDescriptorWire, 0, len(entries))
	for _, e := range entries {
		out = append(out, ToolDescriptorWire{
			Name:        e.Tool.Name(),
			Kind:        string(e.Kind),
			Description: e.Tool.Description(),
			InputSchema: rawSchemaToMap(e.Tool.Parameters()),
			Metadata:    e.Metadata,
		})
	}
	return successResp(id, ListToolsResult{Tools: out})
}

func rawSchemaToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"type": "object"}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// handleListSkills returns the current snapshot of the Go-side
// registry. Go loads both the bundled 5 skills from embed and the
// user-directory skills; the list is one of the sources of truth
// (the enabled state is overridden by the main-side bootstrap).
func handleListSkills(id json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return successResp(id, ListSkillsResult{Skills: []skills.SkillSummaryWire{}})
	}
	entries := h.Skills.Snapshot()
	return successResp(id, ListSkillsResult{Skills: skills.ToSummaries(entries)})
}

// handleSetSkillEnabled flips the in-memory enabled flag for a skill,
// then broadcasts agent.skills.changed on every active WS so main can
// refresh its own cache. Errors are returned as JSON-RPC errors; the
// Go-side state is left untouched on failure.
func handleSetSkillEnabled(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SetSkillEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.SkillID) == "" {
		return errorResp(id, CodeInvalidParams, "skillId is required", nil)
	}
	if err := h.Skills.SetEnabled(p.SkillID, p.Enabled); err != nil {
		return errorResp(id, CodeInvalidParams, err.Error(), err)
	}
	h.broadcastSkillsChanged()
	h.refreshToolsIfNeeded()
	return successResp(id, SetSkillEnabledResult{OK: true})
}

// refreshToolsIfNeeded re-runs plugin registration for every session
// after a skill / mcp state change so the agent tool surface stays
// current. No-op when SessionManager is nil.
func (h *Handler) refreshToolsIfNeeded() {
	if h.Sessions != nil {
		h.Sessions.RefreshAllTools()
	}
}

// InvokeSkillUserParams is the JSON-RPC params for agent.skill.invoke_user.
// Content is the raw `/skill-name args` command the user sent; when empty
// the handler reconstructs it from SkillID + Args so the persisted user
// message matches the renderer bubble.
type InvokeSkillUserParams struct {
	SessionID string `json:"sessionId,omitempty"`
	SkillID   string `json:"skillId"`
	Args      string `json:"args,omitempty"`
	Content   string `json:"content,omitempty"`
}

// InvokeSkillUserResult is the JSON-RPC result for agent.skill.invoke_user.
// Mirrors PromptResult so the renderer can start an assistant bubble keyed
// by messageId and correlate the run's events.
type InvokeSkillUserResult struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	MessageID string `json:"messageId"`
	OK        bool   `json:"ok"`
}

// handleInvokeSkillUser implements explicit `/skill-name args`
// invocations from the user. Validation (exists + enabled +
// userInvocable) is synchronous — failures are returned as JSON-RPC
// errors so main can surface a toast; on success the skill context is
// handed to the session's Loop to run a mini agent loop
// asynchronously, with an event stream identical to a normal prompt.
func handleInvokeSkillUser(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil || h.SkillRunner == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p InvokeSkillUserParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.SkillID) == "" {
		return errorResp(id, CodeInvalidParams, "skillId is required", nil)
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}

	sec, err := h.SkillRunner.ExecuteByUserInvocation(context.Background(), p.SkillID, p.Args)
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrSkillNotFound):
			return errorResp(id, CodeSkillNotFound, err.Error(), err)
		case errors.Is(err, skills.ErrSkillDisabled):
			return errorResp(id, CodeSkillDisabled, err.Error(), err)
		case errors.Is(err, skills.ErrSkillNotUserInvocable):
			return errorResp(id, CodeSkillNotUserInvocable, err.Error(), err)
		default:
			return errorResp(id, CodeInternalError, "skill invoke", err)
		}
	}

	entry, err := h.Sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionStalled) {
			return errorResp(id, CodeSessionStalled, "session stalled", err)
		}
		return errorResp(id, CodeAgentInitFailed, "get session", err)
	}
	if entry.SessionRuntime == nil {
		return errorResp(id, CodeNoSessionRuntime, "no SessionRuntime bound", nil)
	}

	content := p.Content
	if strings.TrimSpace(content) == "" {
		content = "/" + p.SkillID
		if p.Args != "" {
			content += " " + p.Args
		}
	}
	ticket, err := entry.SessionRuntime.Loop.SubmitSkill(sessionruntime.SkillInvocation{
		SystemPrompt: sec.SystemPrompt,
		Content:      content,
		Tools:        sec.Tools,
	})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop submit", err)
	}
	return successResp(id, InvokeSkillUserResult{
		SessionID: entry.Session.ID,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		OK:        true,
	})
}

// handleBootstrapSkills accepts the initial enabled state pushed by
// main and overwrites the Go-side registry flags one by one. Unknown
// ids are no-ops (so main can re-trigger a reload instead of receiving
// an error here), keeping the protocol idempotent.
func handleBootstrapSkills(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Skills == nil {
		return errorResp(id, CodeMethodNotFound, "skills not configured", nil)
	}
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p BootstrapSkillsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	for _, s := range p.Skills {
		_ = h.Skills.SetEnabled(s.ID, s.Enabled)
	}
	h.broadcastSkillsChanged()
	return successResp(id, BootstrapSkillsResult{OK: true})
}

// broadcastSkillsChanged pushes agent.skills.changed to every active
// WS. It routes through EventLedger.Broadcast rather than the
// per-session path — skill state is global, and main is the sole
// subscriber.
func (h *Handler) broadcastSkillsChanged() {
	if h.Skills == nil || h.Ledger == nil {
		return
	}
	entries := h.Skills.Snapshot()
	params := map[string]any{
		"skills": skills.ToSummaries(entries),
	}
	h.Ledger.Broadcast("agent.skills.changed", params)
}

// --- MCP handlers ---

// ListMcpServersResult is the JSON-RPC result for agent.mcp.list.
// Mirrors DarvinListMcpServersResponse in src/shared/darvin-api.ts.
