// Restores a session's persisted message history into the in-memory session before a turn.

package sessionruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/todos"
)

const hydrateTimeout = 5 * time.Second

// hydrateSession loads the persisted history for sess.ID into the
// in-memory Session. Two-table split:
//   - messages table → pure UI history (no digest rows)
//   - session_digests table → accumulated digests (sequence asc)
//
// Final slice is [digests...] + [tail of messages after the latest
// digest's FirstKeptID/FirstKeptTimestamp].
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
	history := make([]protocol.Message, 0, len(rows))
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
		history = append(history, converted...)
	}
	seedTodosFromHistory(sess.ID, history)

	var digests []store.SessionDigest
	if f.DigestStore != nil {
		if d, derr := f.DigestStore.List(ctx, sess.ID); derr == nil {
			digests = d
		} else if f.Logger != nil {
			f.Logger.Warn("hydrate digests failed",
				zap.String("session_id", sess.ID), zap.Error(derr))
		}
	}

	tail := history
	if len(digests) > 0 {
		latest := digests[len(digests)-1]
		tail = splitAtBoundary(history, latest.FirstKeptID, latest.FirstKeptTimestamp)
	}

	msgs := make([]protocol.Message, 0, len(digests)+len(tail))
	for _, d := range digests {
		msgs = append(msgs, protocol.Message{
			Role: protocol.RoleAssistant,
			Content: "[Conversation Summary]\n" + d.Summary +
				fmt.Sprintf("\n\n(Compacted at %s; sequence #%d)",
					time.UnixMilli(d.CreatedAt).Format(time.RFC3339), d.Sequence),
			ID:        d.ID,
			Timestamp: d.CreatedAt,
		})
	}
	msgs = append(msgs, tail...)
	sess.ReplaceAll(msgs)
}

// splitAtBoundary returns the tail slice beginning at the message
// whose ID equals id (preferred), falling back to the first message
// whose Timestamp >= ts. Returns msgs unchanged when no boundary
// matches so the hydrate is always safe.
func splitAtBoundary(msgs []protocol.Message, id string, ts int64) []protocol.Message {
	for i, m := range msgs {
		if id != "" && m.ID == id {
			return msgs[i:]
		}
		if ts > 0 && m.Timestamp >= ts {
			return msgs[i:]
		}
	}
	return msgs
}

// seedTodosFromHistory restores the host-side current task list from the
// full persisted history. It runs before the compaction-boundary slice so a
// todo_write that predates the latest digest still re-seeds the store on
// restart — PersistCompaction keeps the messages table intact; only the
// in-memory slice is cut at the digest boundary.
func seedTodosFromHistory(sessionID string, msgs []protocol.Message) {
	args := lastTodoWriteArgs(msgs)
	if args == nil {
		return
	}
	items, ok := todos.ParseArgs(args)
	if !ok {
		return
	}
	if len(items) == 0 {
		todos.Clear(sessionID)
		return
	}
	todos.Set(sessionID, items)
}

// lastTodoWriteArgs returns the arguments of the most recent todo_write call
// in msgs, or nil when none exists.
func lastTodoWriteArgs(msgs []protocol.Message) map[string]any {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != protocol.RoleAssistant {
			continue
		}
		for _, tc := range msgs[i].ToolCalls {
			if tc.Name == todos.WriteToolName {
				return tc.Arguments
			}
		}
	}
	return nil
}

// recordToMessages converts one persisted MessageRecord into the
// protocol messages the LLM context expects.
//
// Skips rows the LLM context must not see:
//   - system rows (providers drop system from the messages array)
//   - in-flight assistant rows with Done=false (streaming interrupted
//     before persistAssistantMessages sealed them)
//   - unknown roles
func recordToMessages(rec store.MessageRecord) ([]protocol.Message, error) {
	switch rec.Role {
	case string(protocol.RoleUser):
		if !rec.Done {
			return nil, nil
		}
		return []protocol.Message{{
			Role: protocol.RoleUser, Content: rec.Content,
			ID: rec.ID, Timestamp: rec.Timestamp,
		}}, nil

	case string(protocol.RoleAssistant):
		if !rec.Done {
			return nil, nil
		}
		msgs := make([]protocol.Message, 0, 2)
		assistant := protocol.Message{
			Role: protocol.RoleAssistant, Content: rec.Content,
			ID: rec.ID, Timestamp: rec.Timestamp,
		}
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
					Role: protocol.RoleTool, Content: tc.Result.Content,
					ToolCallID: tc.ID,
					Timestamp:  rec.Timestamp,
				})
			}
		}
		msgs = append([]protocol.Message{assistant}, msgs...)
		return msgs, nil

	default:
		return nil, nil
	}
}
