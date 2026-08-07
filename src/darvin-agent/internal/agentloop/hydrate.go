package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
)

// hydrateTimeout bounds the SQL read + per-row JSON decode done at
// AgentLoopSession construction. A slow disk must not stall prompt setup
// for tens of seconds; on timeout the session stays empty and the agent
// starts with a fresh context (same as pre-hydrate behaviour).
const hydrateTimeout = 5 * time.Second

// hydrateSession loads the persisted history for sess.ID from the factory's
// MessageStore into the in-memory Session, so an AgentLoopSession rebuilt
// after a restart / eviction still carries the conversation the LLM needs.
//
// No-op when f.MessageStore is nil (unit-test / stripped path) — the
// dispatcher's persist hooks use the same nil-store guard. Errors are
// warn-and-continue: a hydration failure must not block AgentLoopSession
// construction, matching the factory's plugin-failure policy.
func hydrateSession(ctx context.Context, f *AgentFactory, sess *session.Session) {
	if f.MessageStore == nil || sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hydrateTimeout)
	defer cancel()

	rows, err := f.MessageStore.List(ctx, sess.ID, 0, 0)
	if err != nil {
		if f.Logger != nil {
			f.Logger.Warn("hydrate session failed",
				zap.String("session_id", sess.ID), zap.Error(err))
		}
		return
	}
	msgs := make([]protocol.Message, 0, len(rows))
	for _, r := range rows {
		converted, err := recordToMessages(r)
		if err != nil {
			if f.Logger != nil {
				f.Logger.Warn("hydrate message skipped",
					zap.String("session_id", sess.ID),
					zap.String("message_id", r.ID),
					zap.Error(err))
			}
			continue
		}
		msgs = append(msgs, converted...)
	}
	sess.ReplaceAll(msgs)
}

// recordToMessages converts one persisted MessageRecord into the protocol
// messages the LLM context expects. An assistant row carrying
// ToolCalls[].Result expands into the assistant message plus one role=tool
// message per result, mirroring how the live executor appends tool results.
//
// Skips rows the LLM context must not see:
//   - system rows (providers drop system from the messages array; keeping
//     them would leak workspace_event noise into context)
//   - in-flight assistant rows with Done=false (streaming interrupted
//     before persistAssistantMessages sealed them — half-written content)
//   - unknown roles
func recordToMessages(rec store.MessageRecord) ([]protocol.Message, error) {
	switch rec.Role {
	case string(protocol.RoleUser):
		if !rec.Done {
			return nil, nil
		}
		return []protocol.Message{{Role: protocol.RoleUser, Content: rec.Content}}, nil

	case string(protocol.RoleAssistant):
		if !rec.Done {
			return nil, nil
		}
		msgs := make([]protocol.Message, 0, 2)
		assistant := protocol.Message{Role: protocol.RoleAssistant, Content: rec.Content}
		if rec.ToolCalls != "" {
			var calls []protocol.ToolCall
			if err := json.Unmarshal([]byte(rec.ToolCalls), &calls); err != nil {
				return nil, fmt.Errorf("decode tool_calls: %w", err)
			}
			assistant.ToolCalls = calls
			for _, tc := range calls {
				if tc.Result == nil {
					continue
				}
				msgs = append(msgs, protocol.Message{
					Role:       protocol.RoleTool,
					Content:    tc.Result.Content,
					ToolCallID: tc.ID,
				})
			}
		}
		msgs = append([]protocol.Message{assistant}, msgs...)
		return msgs, nil

	default:
		return nil, nil
	}
}
