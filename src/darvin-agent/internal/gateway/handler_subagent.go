// Subagent RPC handlers: list / get_messages / abort / read_result.
// These let the renderer's Subagents artifact tab read run history and
// cancel live runs without going through the LLM tool surface.

package gateway

import (
	"context"
	"encoding/json"
)

// SubagentRunWire is the JSON-RPC result shape for one run; JSON tags
// match the renderer's SubagentRun type in darvin-api.ts.
type SubagentRunWire struct {
	ID              string   `json:"id"`
	ParentID        string   `json:"parentId"`
	Status          string   `json:"status"`
	Prompt          string   `json:"prompt"`
	Description     string   `json:"description"`
	Scope           []string `json:"scope"`
	Model           string   `json:"model"`
	ToolCallID      string   `json:"toolCallId,omitempty"`
	StartedAt       int64    `json:"startedAt"`
	EndedAt         int64    `json:"endedAt"`
	ToolCalls       int      `json:"toolCalls"`
	ErrorMsg        string   `json:"errorMsg,omitempty"`
	DurationMs      int64    `json:"durationMs"`
	ResultText      string   `json:"resultText,omitempty"`
	ResultTruncated bool     `json:"resultTruncated,omitempty"`
}

// SubagentListParams is the JSON-RPC params for agent.subagent.list.
type SubagentListParams struct {
	SessionID string `json:"sessionId"`
}

// SubagentListResult is the JSON-RPC result for agent.subagent.list.
type SubagentListResult struct {
	Subagents []SubagentRunWire `json:"subagents"`
}

// handleSubagentList returns every persisted sub-agent run for a parent
// session, most recent first. Degrades to an empty list when the store
// is nil (handler-test stubs).
func handleSubagentList(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p SubagentListParams
	if err := json.Unmarshal(params, &p); err != nil || p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if h.SubagentStore == nil {
		return successResp(id, SubagentListResult{Subagents: []SubagentRunWire{}})
	}
	rows, err := h.SubagentStore.ListByParent(ctx, p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "subagent list", err)
	}
	out := make([]SubagentRunWire, 0, len(rows))
	for _, r := range rows {
		out = append(out, SubagentRunWire{
			ID:          r.ID,
			ParentID:    r.ParentID,
			Status:      r.Status,
			Prompt:      r.Prompt,
			Description: r.Description,
			Scope:       decodeScope(r.ScopeJSON),
			Model:       r.Model,
			ToolCallID:  r.ToolCallID,
			StartedAt:   r.StartedAt.UnixMilli(),
			EndedAt:     r.EndedAt.UnixMilli(),
			ToolCalls:   r.ToolCalls,
			ErrorMsg:    r.ErrorMsg,
			DurationMs:  r.EndedAt.Sub(r.StartedAt).Milliseconds(),
			ResultText:  r.ResultText,
		})
	}
	return successResp(id, SubagentListResult{Subagents: out})
}

// SubagentGetMessagesParams is the JSON-RPC params for
// agent.subagent.get_messages.
type SubagentGetMessagesParams struct {
	RunID string `json:"runId"`
}

// SubagentGetMessagesResult is the JSON-RPC result.
type SubagentGetMessagesResult struct {
	Messages []MessageRecordWire `json:"messages"`
}

// handleSubagentGetMessages returns the sub-agent's persisted messages
// by reading the MessageStore bucketed under the run id (the sub-agent
// session id is the run id). Degrades to an empty list when no store
// or no messages exist.
func handleSubagentGetMessages(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p SubagentGetMessagesParams
	if err := json.Unmarshal(params, &p); err != nil || p.RunID == "" {
		return errorResp(id, CodeInvalidParams, "runId is required", nil)
	}
	if h.MessageStore == nil {
		return successResp(id, SubagentGetMessagesResult{Messages: []MessageRecordWire{}})
	}
	recs, err := h.MessageStore.List(ctx, p.RunID, 0, 0)
	if err != nil {
		return errorResp(id, CodeInternalError, "subagent get_messages", err)
	}
	out := make([]MessageRecordWire, 0, len(recs))
	for _, r := range recs {
		out = append(out, MessageRecordWire{
			ID:         r.ID,
			SessionID:  r.SessionID,
			Role:       r.Role,
			Content:    r.Content,
			ToolCalls:  r.ToolCalls,
			Timestamp:  r.Timestamp,
			StopReason: r.StopReason,
			ParentID:   r.ParentID,
			Done:       r.Done,
			Error:      r.Error,
			ToolLabel:  r.ToolLabel,
		})
	}
	return successResp(id, SubagentGetMessagesResult{Messages: out})
}

// SubagentAbortParams is the JSON-RPC params for agent.subagent.abort.
type SubagentAbortParams struct {
	RunID string `json:"runId"`
}

// handleSubagentAbort cancels a live sub-agent run by id. It resolves
// the parent session from the run id and cancels via the session's
// agent manager. A run that is already terminal or whose parent session
// is gone is a no-op success.
func handleSubagentAbort(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p SubagentAbortParams
	if err := json.Unmarshal(params, &p); err != nil || p.RunID == "" {
		return errorResp(id, CodeInvalidParams, "runId is required", nil)
	}
	parent := parentOfRunID(p.RunID)
	if parent == "" {
		return errorResp(id, CodeInvalidParams, "runId must be <parentSessionID>/sub/<rand>", nil)
	}
	entry, err := h.Sessions.GetOrCreateEntry(parent)
	if err != nil || entry.AgentLoop == nil || entry.AgentLoop.Agent == nil {
		return successResp(id, map[string]any{"ok": true})
	}
	sm := entry.AgentLoop.Agent.Subagents()
	if sm == nil {
		return successResp(id, map[string]any{"ok": true})
	}
	_ = sm.Abort(p.RunID)
	return successResp(id, map[string]any{"ok": true})
}

// SubagentReadResultParams is the JSON-RPC params for
// agent.subagent.read_result.
type SubagentReadResultParams struct {
	RunID  string `json:"runId"`
	Offset int    `json:"offset_bytes"`
	Limit  int    `json:"limit_bytes"`
}

// SubagentReadResultResult is the JSON-RPC result.
type SubagentReadResultResult struct {
	Text string `json:"text"`
}

// handleSubagentReadResult returns a byte-offset window of the run's
// buffered final result from the live session's manager. A run whose
// parent session was evicted falls back to the persisted ResultText.
func handleSubagentReadResult(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	var p SubagentReadResultParams
	if err := json.Unmarshal(params, &p); err != nil || p.RunID == "" {
		return errorResp(id, CodeInvalidParams, "runId is required", nil)
	}
	parent := parentOfRunID(p.RunID)
	if parent != "" {
		entry, err := h.Sessions.GetOrCreateEntry(parent)
		if err == nil && entry.AgentLoop != nil && entry.AgentLoop.Agent != nil {
			if sm := entry.AgentLoop.Agent.Subagents(); sm != nil {
				text, rerr := sm.ReadResult(p.RunID, p.Offset, p.Limit)
				if rerr == nil {
					return successResp(id, SubagentReadResultResult{Text: text})
				}
			}
		}
	}
	// Fallback: persisted ResultText (truncated at storage threshold).
	if h.SubagentStore != nil {
		row, serr := h.SubagentStore.Get(ctx, p.RunID)
		if serr == nil {
			limit := p.Limit
			if limit <= 0 {
				limit = 12 * 1024
			}
			if limit > 24*1024 {
				limit = 24 * 1024
			}
			offset := p.Offset
			if offset < 0 {
				offset = 0
			}
			t := row.ResultText
			if offset >= len(t) {
				t = ""
			} else if end := offset + limit; end < len(t) {
				t = t[offset:end]
			} else {
				t = t[offset:]
			}
			return successResp(id, SubagentReadResultResult{Text: t})
		}
	}
	return errorResp(id, CodeInternalError, "subagent read_result: run not found", nil)
}

// parentOfRunID extracts the parent session id from a run id shaped
// "<parentSessionID>/sub/<rand>". Returns "" on a malformed id.
func parentOfRunID(runID string) string {
	const sep = "/sub/"
	for i := 0; i+len(sep) <= len(runID); i++ {
		if runID[i:i+len(sep)] == sep {
			return runID[:i]
		}
	}
	return ""
}

// decodeScope unmarshals the ScopeJSON column; a malformed value yields nil.
func decodeScope(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// MessageRecordWire mirrors store.MessageRecord for the RPC wire.
type MessageRecordWire struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionId"`
	Role       string  `json:"role"`
	Content    string  `json:"content"`
	ToolCalls  string  `json:"toolCalls,omitempty"`
	Timestamp  int64   `json:"createdAt"`
	StopReason string  `json:"stopReason,omitempty"`
	ParentID   string  `json:"parentId,omitempty"`
	Done       bool    `json:"done"`
	Error      *string `json:"error,omitempty"`
	ToolLabel  *string `json:"toolLabel,omitempty"`
}
