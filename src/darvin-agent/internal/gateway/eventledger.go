package gateway

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
)

// EventLedger is the bridge between the agent's event bus and WS clients.
// AttachSubscription feeds it a *event.Subscription; every event read from
// that channel is forwarded as an "agent.event" notification to the
// subscribers of the event's session.
//
// Subscriptions are stored session-scoped: bySession maps sessionID → the
// set of clients that want events for that session. A client subscribing
// to many sessions shows up in many sets; UnsubscribeAll walks them all
// in O(nSessions) so a disconnect is cheap.
type EventLedger struct {
	mu        sync.RWMutex
	bySession map[string]map[*client]struct{}
	log       *zap.Logger

	// fakeDelay is the inter-event sleep used by EmitStub. Public so
	// tests can collapse it to zero.
	fakeDelay time.Duration
}

// NewEventLedger builds an empty ledger. The fakeDelay defaults to 50ms —
// short enough that wscat sees the two-event burst within 1s, long enough
// to keep the order observable in human-driven testing.
func NewEventLedger(log *zap.Logger) *EventLedger {
	return &EventLedger{
		bySession: make(map[string]map[*client]struct{}),
		log:       log,
		fakeDelay: 50 * time.Millisecond,
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

// UnsubscribeAll removes c from every session's subscriber set. Called
// when a WS connection's read/write loop exits; the next EmitStub that
// targets an already-detached client will be a no-op.
func (l *EventLedger) UnsubscribeAll(c *client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for sid, set := range l.bySession {
		delete(set, c)
		if len(set) == 0 {
			delete(l.bySession, sid)
		}
	}
}

// AttachSubscription wires a real agent event bus into the ledger. Each
// event read from sub.C() is routed to subscribers of its EventCommon.
// SessionID via publishLocked; events with no SessionID (e.g. system
// events emitted before any session is established) are dropped.
//
// main.go calls this once after constructing the Agent — the goroutine
// runs for the lifetime of the WS server and exits when sub.Unsubscribe
// closes the channel.
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

// publishLocked fans ev out to every subscriber of sessionID. The caller
// must NOT hold l.mu — SendNotification writes to the WS under the
// client's own write mutex and must not be re-entered.
//
// Drop semantics: writes are best-effort. If a client is wedged, the
// goroutine blocks in SendNotification until its write timeout / ctx
// expires; ledger-level fanout does not itself drop. (event.Bus.Emit
// drops-oldest on a full subscription channel — that's a different layer.)
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

// EmitStub is a fake event source: a goroutine that pushes a
// text_delta followed by an agent_end to the session's subscribers.
//
// The initial fakeDelay is intentional: the spec's flow is
// agent.prompt → agent.subscribe_events → events. Without a delay
// the first text_delta fires before the renderer has had time to
// register the subscription and gets dropped. One fakeDelay (~50ms)
// is enough for the JSON-RPC roundtrip on a localhost connection.
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

// mapEventToTS converts an event.Event into the JSON object the renderer
// consumes as DarvinEvent. The session-scoped subscribe path uses these
// shapes; events not matched here fall through to a bare {type, ...}
// envelope so a new event class can't accidentally disappear.
//
// No sessionId is forwarded: the TS contract (src/shared/darvin-api.ts)
// ties each event to the WS message id, not the session. A client
// multiplexes sessions over a single WS, so a per-event sessionId would
// duplicate the routing state already held in the client's own store.
func mapEventToTS(ev event.Event, _ string) any {
	switch e := ev.(type) {
	case event.TextDeltaEvent:
		return map[string]any{
			"type":      ev.EventName(),
			"delta":     e.Delta,
			"messageId": ev.Common().MessageID,
		}
	case event.ThinkingDeltaEvent:
		return map[string]any{
			"type":      ev.EventName(),
			"delta":     e.Delta,
			"messageId": ev.Common().MessageID,
		}
	case event.LLMEndEvent:
		out := map[string]any{
			"type":      "done",
			"messageId": ev.Common().MessageID,
		}
		// Surface Usage as an optional `usage` block. Token accounting is
		// the renderer's hook for displaying cost / progress; the field is
		// omitted (rather than zero-valued) so consumers that don't care
		// can ignore it and zero-usage turns don't carry a misleading
		// `{totalTokens: 0}` payload.
		if e.Usage != (llm.Usage{}) {
			out["usage"] = map[string]any{
				"inputTokens":  e.Usage.PromptTokens,
				"outputTokens": e.Usage.CompletionTokens,
				"totalTokens":  e.Usage.TotalTokens,
			}
		}
		return out
	case event.AgentEndEvent:
		return map[string]any{
			"type": ev.EventName(),
		}
	case event.ToolStartEvent:
		return map[string]any{
			"type":    ev.EventName(),
			"tool":    e.Name,
			"input":   e.Arguments,
			"message": map[string]any{"id": e.CallID},
		}
	case event.ToolEndEvent:
		return map[string]any{
			"type":    ev.EventName(),
			"tool":    e.Result.Content,
			"message": map[string]any{"id": e.CallID},
		}
	case event.AgentErrorEvent:
		// Field names match the DarvinEvent 'error' variant in
		// src/shared/darvin-api.ts: the renderer looks the message up by
		// messageId and renders `message` on the bubble.
		return map[string]any{
			"type":      "error",
			"messageId": ev.Common().MessageID,
			"message":   e.Err.Error(),
		}
	default:
		return map[string]any{"type": ev.EventName()}
	}
}

// truncate is a bounds-clamp for the user-visible "Echo: ..." payload.
// 80 chars keeps a long paste from dominating the test output without
// truncating the common case.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
