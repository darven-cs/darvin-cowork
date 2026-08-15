// Prompt / abort / steer / subscribe / compact-context JSON-RPC handlers.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/sessionruntime"
)

type PromptParams struct {
	Content     string           `json:"content"`
	SessionID   string           `json:"sessionId,omitempty"`
	RunID       string           `json:"runId,omitempty"`
	Attachments []string         `json:"attachments,omitempty"`
	Images      []agent.ImageRef `json:"images,omitempty"`
	// Provider / Model are an optional per-turn override applied for this
	// run only; empty values keep the session default.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// PromptResult is the JSON-RPC result for agent.prompt. sessionId and
// messageId are 21-char nanoids with no prefix (the spec rejected the
// s-/m- scheme because the WS frame is opaque to the renderer).
// Queued=true means this turn was parked behind a same-session run;
// it will start once the previous turn completes.
type PromptResult struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	MessageID string `json:"messageId"`
	Queued    bool   `json:"queued,omitempty"`
}

// AbortParams is the JSON-RPC params for agent.abort. Both sessionId and
// runId identify the target turn; an unknown pair returns -32602.
type AbortParams struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
}

// AbortResult is the JSON-RPC result for agent.abort.
type AbortResult struct {
	Aborted   bool   `json:"aborted"`
	SessionID string `json:"sessionId"`
}

// SubscribeEventsParams is the JSON-RPC params for agent.subscribe_events.
// sessionId must reference an existing session (i.e. one created earlier
// on the same WS connection); unknown ids are rejected with -32602.
type SubscribeEventsParams struct {
	SessionID string `json:"sessionId"`
}

// SubscribeEventsResult is the JSON-RPC result for agent.subscribe_events.
type SubscribeEventsResult struct {
	Subscribed bool `json:"subscribed"`
}

// SteerParams is the JSON-RPC params for agent.steer. sessionId is
// required — steer targets a specific session's per-session Loop.
// runId is optional; when omitted the steer lands at the head of the
// session's steerQueue regardless of which turn is in flight.
type SteerParams struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId,omitempty"`
	Content   string `json:"content"`
}

// SteerResult is the JSON-RPC result for agent.steer. RunID /
// MessageID / Queued mirror the RunTicket returned by Loop.Steer so
// the renderer can correlate events the same way it does for prompt.
type SteerResult struct {
	Steered   bool   `json:"steered"`
	RunID     string `json:"runId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
}

// ListSessionsResult is the JSON-RPC result for agent.list_sessions.
// Matches DarvinListSessionsResponse in src/shared/darvin-api.ts:

// handlePrompt routes the prompt to sessionID's SessionRuntime.Loop.
// ErrSessionStalled (Stop refusal window) maps to CodeSessionStalled;
// factory construction failure maps to CodeAgentInitFailed; the
// handler-test stub case (entry.SessionRuntime is nil) maps to
// CodeNoSessionRuntime. Empty sessionId falls back to DefaultSessionID,
// matching the legacy CreateOrGet behaviour.
func handlePrompt(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p PromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = DefaultSessionID
	}

	entry, err := c.sessions.GetOrCreateEntry(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionStalled) {
			return errorResp(id, CodeSessionStalled, "session stalled", err)
		}
		return errorResp(id, CodeAgentInitFailed, "get session", err)
	}
	if entry.SessionRuntime == nil {
		// Reached when the handler-test stub did not wire a factory.
		return errorResp(id, CodeNoSessionRuntime, "no SessionRuntime bound", nil)
	}
	ticket, err := entry.SessionRuntime.Loop.Submit(sessionruntime.PromptRequest{
		RunID:       p.RunID,
		Content:     p.Content,
		Attachments: p.Attachments,
		Images:      p.Images,
		Provider:    p.Provider,
		Model:       p.Model,
	})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop submit", err)
	}
	return successResp(id, PromptResult{
		SessionID: entry.Session.ID,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		Queued:    ticket.Queued,
	})
}

func handleAbort(_ context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	var p AbortParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" || p.RunID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId and runId are required", nil)
	}
	if err := h.Sessions.Stop(p.SessionID, p.RunID); err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return errorResp(id, CodeInvalidParams, "unknown sessionId", nil)
		case errors.Is(err, ErrRunMismatch):
			return errorResp(id, CodeInvalidParams, "runId does not match the active run", nil)
		default:
			return errorResp(id, CodeInternalError, "session stop", err)
		}
	}
	return successResp(id, AbortResult{Aborted: true, SessionID: p.SessionID})
}

// CompactContextParams is the JSON-RPC params for agent.compact_context.
type CompactContextParams struct {
	SessionID string `json:"sessionId"`
}

// CompactContextResult is the JSON-RPC result for agent.compact_context.
type CompactContextResult struct {
	Accepted  bool   `json:"accepted"`
	SessionID string `json:"sessionId"`
}

// handleCompactContext triggers one manual context compaction. When
// the session is not in a compactable state (running / no assembler /
// no SessionRuntime) it returns accepted=false and the renderer
// keeps the spinner as-is without entering the next. The compact
// itself includes an LLM summary call and runs in the background to
// avoid blocking the WS read loop; final success / failure is
// driven by the subsequent compaction events.
func handleCompactContext(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, _ *Handler) *Response {
	var p CompactContextParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResp(id, CodeInvalidParams, "params must be an object", nil)
		}
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	entry, err := c.sessions.GetOrCreateEntry(p.SessionID)
	if err != nil || entry.SessionRuntime == nil || entry.SessionRuntime.Agent == nil {
		return successResp(id, CompactContextResult{Accepted: false, SessionID: p.SessionID})
	}
	a := entry.SessionRuntime.Agent
	if a.IsRunning() || a.Assembler() == nil || !a.AssemblerEnabled() {
		return successResp(id, CompactContextResult{Accepted: false, SessionID: p.SessionID})
	}
	go runManualCompact(a, p.SessionID)
	return successResp(id, CompactContextResult{Accepted: true, SessionID: p.SessionID})
}

func runManualCompact(a *agent.Agent, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res := a.Assembler().Compact(ctx, ctxengine.CompactParams{
		SessionID: sessionID,
		Messages:  a.Session().Messages(),
		Budget:    a.Config().ContextWindow,
		Force:     true,
		Reason:    "manual",
		LastUsage: a.LastUsage(),
	})
	if !res.Success {
		return
	}
	a.Session().ReplaceAll(res.RetainedMessages)
	if persistErr := a.PersistCompaction(ctx, res); persistErr != nil && ctx.Err() == nil {
		// Already logged by PersistCompaction; the live slice is in
		// place, only the digest row failed to land.
		_ = persistErr
	}
	a.Emit(event.CompactionEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: sessionID}},
		Before:    res.TokensBefore,
		After:     res.TokensAfter,
		Note:      "manual",
	})
}

// handleSubscribeEvents runs in two phases: EnsureEntry only creates the
// SessionEntry and does not lazily build SessionRuntime, so subscribing
// to historical sessions from the renderer does not spin up N agents /
// loops / subscriptions. The real event source is materialised on the
// first prompt arrival.
func handleSubscribeEvents(_ context.Context, id json.RawMessage, params json.RawMessage, c *client, _ *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SubscribeEventsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if _, err := c.sessions.EnsureEntry(p.SessionID); err != nil {
		return errorResp(id, CodeInternalError, "subscribe session create", err)
	}
	c.ledger.Subscribe(p.SessionID, c)
	return successResp(id, SubscribeEventsResult{Subscribed: true})
}

// handleSteer routes agent.steer to the target session's per-session
// Loop. The steer lands at the head of the Loop's steerQueue and
// cancels the in-flight turn; idle sessions behave like Submit.
func handleSteer(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SteerParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}
	entry, err := h.Sessions.EnsureEntry(p.SessionID)
	if err != nil {
		return errorResp(id, CodeInternalError, "session lookup", err)
	}
	if entry.SessionRuntime == nil {
		return errorResp(id, CodeNoSessionRuntime, "session has no agent loop", nil)
	}
	ticket, err := entry.SessionRuntime.Loop.Steer(sessionruntime.PromptRequest{
		RunID:   p.RunID,
		Content: p.Content,
	})
	if err != nil {
		return errorResp(id, CodeInternalError, "loop steer", err)
	}
	return successResp(id, SteerResult{
		Steered:   true,
		RunID:     ticket.RunID,
		MessageID: ticket.MessageID,
		Queued:    ticket.Queued,
	})
}

// handleListSessions returns every session row (with Title) known to
// SessionStore, newest first. Sessions with no messages are still
// returned so the renderer's empty state has a row to highlight.
