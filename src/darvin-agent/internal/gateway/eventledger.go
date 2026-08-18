// Per-session event subscription ledger that maps agent events to wire events.

package gateway

import (
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
)

// EventLedger bridges the agent event bus and WS clients. Each event
// from a subscription is forwarded as an "agent.event" notification to
// the subscribers of the event's session. bySession is session-scoped;
// UnsubscribeAll walks all sets in O(nSessions) so a disconnect is cheap.
type EventLedger struct {
	mu        sync.RWMutex
	bySession map[string]map[*client]struct{}
	// allConns holds every currently active connection (without a
	// session subscription). Broadcast walks it for global fanout
	// (skills changes, runtime health pings) that has no session key.
	allConns map[*client]struct{}
	log      *zap.Logger

	// fakeDelay is the inter-event sleep used by EmitStub. Tests
	// collapse it to zero.
	fakeDelay time.Duration
}

// NewEventLedger builds an empty ledger. fakeDelay defaults to 50ms —
// enough that the renderer has time to subscribe before the first
// text_delta fires, but short enough for two-event bursts in tests.
func NewEventLedger(log *zap.Logger) *EventLedger {
	return &EventLedger{
		bySession: make(map[string]map[*client]struct{}),
		allConns:  make(map[*client]struct{}),
		log:       log,
		fakeDelay: 50 * time.Millisecond,
	}
}

// RegisterConnection adds c to the global connection set so Broadcast
// can reach it without a session subscription. Read loop calls this
// once on connect; UnsubscribeAll is the symmetric remove.
func (l *EventLedger) RegisterConnection(c *client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.allConns[c] = struct{}{}
}

// Broadcast fans a notification out to every active connection (events
// with no per-session key). Drops on a wedged client are tolerated —
// the next read error tears the connection down.
func (l *EventLedger) Broadcast(method string, params any) {
	l.mu.RLock()
	clients := make([]*client, 0, len(l.allConns))
	for c := range l.allConns {
		clients = append(clients, c)
	}
	l.mu.RUnlock()
	for _, c := range clients {
		c.SendNotification(method, params)
	}
}

// Subscribe registers c to receive notifications for sessionID. Safe to
// call multiple times with the same (sessionID, c) pair; the set semantics
// collapse duplicates.
func (l *EventLedger) Subscribe(sessionID string, c *client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	set, ok := l.bySession[sessionID]
	if !ok {
		set = make(map[*client]struct{})
		l.bySession[sessionID] = set
	}
	set[c] = struct{}{}
}

// UnsubscribeAll removes c from every session's subscriber set. Read
// loop calls this on exit; subsequent EmitStub calls targeting an
// already-detached client are no-ops.
func (l *EventLedger) UnsubscribeAll(c *client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for sid, set := range l.bySession {
		delete(set, c)
		if len(set) == 0 {
			delete(l.bySession, sid)
		}
	}
	delete(l.allConns, c)
}

// AttachSubscription wires a real agent event bus into the ledger. Each
// event from sub.C() is routed via publishLocked to subscribers of its
// EventCommon.SessionID; events with no SessionID are dropped. Called
// once by main.go after constructing the Agent; the goroutine runs for
// the WS-server lifetime and exits when sub.Unsubscribe closes the
// channel.
func (l *EventLedger) AttachSubscription(sub *event.Subscription) {
	go func() {
		for ev := range sub.C() {
			sid := ev.Common().SessionID
			if sid == "" {
				// Skip uncorrelated events — they have no fan-out target.
				continue
			}
			l.publishLocked(sid, ev)
		}
	}()
}

// publishLocked fans ev out to every subscriber of sessionID. Caller
// must NOT hold l.mu — SendNotification takes the client's own write
// mutex and must not be re-entered. Writes are best-effort: a wedged
// client blocks SendNotification until its write timeout / ctx
// expires; ledger-level fanout does not drop. (event.Bus.Emit drops
// on a full subscription channel — that's a different layer.)
func (l *EventLedger) publishLocked(sessionID string, ev event.Event) {
	l.mu.RLock()
	set := l.bySession[sessionID]
	clients := make([]*client, 0, len(set))
	for c := range set {
		clients = append(clients, c)
	}
	l.mu.RUnlock()

	params := mapEventToTS(ev, sessionID)
	for _, c := range clients {
		c.SendNotification("agent.event", params)
	}
}

// EmitStub is a fake event source: pushes a text_delta followed by an
// agent_end to a session's subscribers. The initial fakeDelay exists so
// the renderer's subscribe_events call (a JSON-RPC roundtrip on
// localhost) lands before the first text_delta fires.
func (l *EventLedger) EmitStub(sessionID, msgID, content string) {
	go func() {
		time.Sleep(l.fakeDelay)
		l.publishLocked(sessionID, event.TextDeltaEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: sessionID, MessageID: msgID}},
			Delta:     "Echo: " + truncate(content, 80),
		})
		time.Sleep(2 * l.fakeDelay)
		l.publishLocked(sessionID, event.AgentEndEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: sessionID, MessageID: msgID}},
		})
		l.log.Info("EmitStub done", zap.String("sessionId", sessionID), zap.String("messageId", msgID))
	}()
}

// mapEventToTS converts an event.Event into the JSON object the
// renderer consumes as DarvinEvent. sessionId / runId come from the
// embedded EventCommon (omitted when empty so consumers don't
// special-case missing fields). Unmatched events fall through to a
// bare {type, ...} envelope so a new event class can't accidentally
// disappear.
func mapEventToTS(ev event.Event, _ string) any {
	common := ev.Common()
	withCommon := func(m map[string]any) map[string]any {
		if common.SessionID != "" {
			m["sessionId"] = common.SessionID
		}
		if common.RunID != "" {
			m["runId"] = common.RunID
		}
		return m
	}
	switch e := ev.(type) {
	case event.PromptReceivedEvent:
		return withCommon(map[string]any{
			"type":      ev.EventName(),
			"content":   e.Content,
			"messageId": ev.Common().MessageID,
		})
	case event.TextDeltaEvent:
		return withCommon(map[string]any{
			"type":      ev.EventName(),
			"delta":     e.Delta,
			"messageId": ev.Common().MessageID,
		})
	case event.ThinkingDeltaEvent:
		return withCommon(map[string]any{
			"type":      ev.EventName(),
			"delta":     e.Delta,
			"messageId": ev.Common().MessageID,
		})
	case event.LLMEndEvent:
		out := withCommon(map[string]any{
			"type":      "done",
			"messageId": ev.Common().MessageID,
		})
		// Surface Usage as an optional `usage` block. The field is omitted
		// when zero so consumers that don't care can ignore it and
		// zero-usage turns don't carry a misleading `{totalTokens: 0}`.
		if e.Usage != (llm.Usage{}) {
			out["usage"] = map[string]any{
				"inputTokens":  e.Usage.PromptTokens,
				"outputTokens": e.Usage.CompletionTokens,
				"totalTokens":  e.Usage.TotalTokens,
			}
		}
		return out
	case event.AgentEndEvent:
		return withCommon(map[string]any{
			"type": ev.EventName(),
		})
	case event.ToolStartEvent:
		start := map[string]any{
			"type":      ev.EventName(),
			"tool":      e.Name,
			"input":     e.Arguments,
			"messageId": ev.Common().MessageID,
			"message":   map[string]any{"id": e.CallID},
		}
		addToolKindFields(start, e.ToolKind, e.SkillID, e.McpServerID)
		return withCommon(start)
	case event.ToolEndEvent:
		end := map[string]any{
			"type":      ev.EventName(),
			"tool":      e.Result.Content,
			"messageId": ev.Common().MessageID,
			"message":   map[string]any{"id": e.CallID},
		}
		addToolKindFields(end, e.ToolKind, e.SkillID, e.McpServerID)
		return withCommon(end)
	case event.AgentErrorEvent:
		// Field names match the DarvinEvent 'error' variant in
		// src/shared/darvin-api.ts; renderer looks up by messageId.
		return withCommon(map[string]any{
			"type":      "error",
			"messageId": ev.Common().MessageID,
			"message":   e.Err.Error(),
		})
	case event.CompactionEvent:
		reason := "auto"
		if e.Note == "manual" {
			reason = "manual"
		}
		return withCommon(map[string]any{
			"type":         ev.EventName(),
			"reason":       reason,
			"checkpointId": "cp-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			"createdAt":    time.Now().UnixMilli(),
			"beforeTokens": e.Before,
			"afterTokens":  e.After,
		})
	case event.PermissionRequestEvent:
		return withCommon(map[string]any{
			"type":        ev.EventName(),
			"requestId":   e.RequestID,
			"toolName":    e.ToolName,
			"toolInput":   e.ToolInput,
			"dangerLevel": e.DangerLevel,
			"reason":      e.Reason,
			"messageId":   ev.Common().MessageID,
		})
	case event.ArtifactEvent:
		return withCommon(map[string]any{
			"type":       ev.EventName(),
			"artifactId": e.ArtifactID,
			"kind":       e.Kind,
			"name":       e.Name,
			"content":    e.Content,
			"filePath":   e.FilePath,
			"url":        e.URL,
			"messageId":  ev.Common().MessageID,
			"createdAt":  e.CreatedAt,
		})
	case event.ContextUsageEvent:
		// status stays "unknown" — the renderer derives the 5-state ring
		// from percent thresholds via deriveContextStatus.
		usage := map[string]any{
			"sessionId":     common.SessionID,
			"usedTokens":    e.UsedTokens,
			"contextTokens": e.ContextTokens,
			"percent":       e.Percent,
			"status":        "unknown",
			"updatedAt":     time.Now().UnixMilli(),
		}
		return withCommon(map[string]any{"type": "context_usage", "usage": usage})
	default:
		return withCommon(map[string]any{"type": ev.EventName()})
	}
}

// addToolKindFields appends the kind-attribution fields to a tool
// event's wire payload. Empty values are omitted so old events stay
// backward-compatible.
func addToolKindFields(m map[string]any, toolKind, skillID, mcpServerID string) {
	if toolKind != "" {
		m["toolKind"] = toolKind
	}
	if skillID != "" {
		m["skillId"] = skillID
	}
	if mcpServerID != "" {
		m["mcpServerId"] = mcpServerID
	}
}

// truncate clamps the user-visible "Echo: ..." payload at n chars so
// a long paste does not dominate test output.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
