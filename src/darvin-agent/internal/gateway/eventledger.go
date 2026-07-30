package gateway

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
)

// EventLedger is the bridge between the agent's event bus and WS clients.
// S3 attaches no real subscription (handlers stub their own EmitStub); S4
// will call AttachSubscription with a *event.Subscription and the bridge
// will forward every event as an "agent.event" notification to subscribers
// of the event's session.
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

// AttachSubscription is the S4 hook: a real *event.Subscription is wired
// into a goroutine that decodes bus events and re-publishes them through
// publishLocked. S3 keeps it a no-op so the package still compiles and
// the handler stub path is observable.
func (l *EventLedger) AttachSubscription(_ *event.Subscription) {
	// S4 will: go func() { for ev := range sub.C() { l.publishLocked(ev.SessionID, ev) } }()
}

// publishLocked fans ev out to every subscriber of sessionID. The caller
// must NOT hold l.mu — SendNotification writes to the WS under the
// client's own write mutex and must not be re-entered.
//
// Drop semantics: writes are best-effort. If a client is wedged, the
// goroutine blocks in SendNotification until its write timeout / ctx
// expires; ledger-level fanout does not itself drop. (event.Bus.Emit, on
// the S4 path, drops-oldest on a full subscription channel — that's a
// different layer.)
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

// EmitStub is the S3-only fake event source: a goroutine that pushes a
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
		l.publishLocked(sessionID, event.TextDeltaEvent{Delta: "Echo: " + truncate(content, 80)})
		time.Sleep(2 * l.fakeDelay)
		l.publishLocked(sessionID, event.AgentEndEvent{SessionID: sessionID})
		l.log.Info("EmitStub done", zap.String("sessionId", sessionID), zap.String("messageId", msgID))
	}()
}

// mapEventToTS converts an event.Event into the JSON object the renderer
// consumes as DarvinEvent. S3 only populates the two types EmitStub
// actually emits; everything else gets a bare {type, ...} envelope. S4
// will fill in the tool / error shapes.
//
// No sessionId is forwarded: the TS contract (src/shared/darvin-api.ts)
// ties each event to the WS message id, not the session. A client
// multiplexes sessions over a single WS, so a per-event sessionId would
// duplicate the routing state already held in the client's own store.
func mapEventToTS(ev event.Event, _ string) any {
	switch e := ev.(type) {
	case event.TextDeltaEvent:
		return map[string]any{
			"type":    ev.EventName(),
			"delta":   e.Delta,
			"message": map[string]any{"id": ""},
		}
	case event.ThinkingDeltaEvent:
		return map[string]any{
			"type":  ev.EventName(),
			"delta": e.Delta,
		}
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
		return map[string]any{
			"type":   "error",
			"error":  e.Err.Error(),
			"detail": map[string]any{"id": ""},
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
