package agent

import (
	"context"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/store"
)

// TextDeltaHook 订阅 Agent bus 上的 text_delta 事件，把 delta 实时追加到
// messages.content（spec FR-4）。这让 streaming 内容在 Go 进程崩溃时也能
// 留下部分结果，而不是等到整轮结束的 persistAssistantMessages 才落库。
//
// Session 维度过滤：EventCommon.SessionID 必须等于本 Agent 的
// session.ID，避免多 session 串扰。落库失败只 Warn，不影响事件推送 —
// 后续同 messageID 的 delta 仍会触发 Append（delta 之间是累加语义），
// 最终 MarkDone 也会再走一次保证封口。
type TextDeltaHook struct {
	msgStore store.MessageStore
	logger   *zap.Logger
	sub      *event.Subscription
}

// NewTextDeltaHook constructs a hook. A nil msgStore makes Attach a no-op
// (the unit-test / fast-path default where nothing is persisted).
func NewTextDeltaHook(ms store.MessageStore, log *zap.Logger) *TextDeltaHook {
	return &TextDeltaHook{msgStore: ms, logger: log}
}

// Attach subscribes to the Agent's event bus and starts the drain
// goroutine. Idempotent: a hook that is already attached is a no-op.
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
// drain goroutine exit. Idempotent. Called from AcpSession.Close on evict.
func (h *TextDeltaHook) Close() {
	if h.sub != nil {
		h.sub.Unsubscribe()
		h.sub = nil
	}
}
