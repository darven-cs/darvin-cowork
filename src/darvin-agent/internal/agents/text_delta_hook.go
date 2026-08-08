// Persists streaming text deltas to the message store in real time.

package agent

import (
	"context"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/store"
)

// TextDeltaHook appends each text_delta to messages.content in real
// time so partial streaming content survives a Go process crash rather
// than waiting for end-of-turn persistence. Session-scoped filter
// avoids cross-session contamination; persist failures are Warn-only.
type TextDeltaHook struct {
	msgStore store.MessageStore
	logger   *zap.Logger
	sub      *event.Subscription
}

// NewTextDeltaHook constructs a hook. A nil msgStore makes Attach a no-op.
func NewTextDeltaHook(ms store.MessageStore, log *zap.Logger) *TextDeltaHook {
	return &TextDeltaHook{msgStore: ms, logger: log}
}

// Attach subscribes to the Agent's event bus. Idempotent.
func (h *TextDeltaHook) Attach(a *Agent) {
	if h.sub != nil || h.msgStore == nil {
		return
	}
	sub := a.Subscribe(64)
	h.sub = sub
	go func() {
		for ev := range sub.C() {
			td, ok := ev.(event.TextDeltaEvent)
			if !ok {
				continue
			}
			if td.SessionID != a.Session().ID {
				continue
			}
			if td.MessageID == "" {
				continue
			}
			if err := h.msgStore.AppendContent(context.Background(), td.MessageID, td.Delta); err != nil {
				h.logger.Warn("text delta persist failed",
					zap.String("message_id", td.MessageID),
					zap.String("session_id", td.SessionID),
					zap.Error(err))
			}
		}
	}()
}

// Close unsubscribes, which closes the subscription channel and lets the
// drain goroutine exit. Idempotent. Called from AgentLoopSession.Close on evict.
func (h *TextDeltaHook) Close() {
	if h.sub != nil {
		h.sub.Unsubscribe()
		h.sub = nil
	}
}
