// Dispatches one turn's LLM call and tool results to the event bus.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/agents/store"
)

// Prompt enqueues content for immediate processing. Returns ErrAgentBusy
// if the Agent is already running (callers Abort first). attachments are
// absolute paths staged for this message (LLM sees a transient system
// note; read_file may access them via the run's granted-read set);
// images become LLM image content blocks.
func (a *Agent) Prompt(_ context.Context, content string, images []queue.ImageRef, attachments ...[]string) error {
	var files []string
	if len(attachments) > 0 {
		files = attachments[0]
	}
	return a.enqueue(queue.ModePrompt, content, files, images)
}

// Abort cancels the current Run's context (no-op when idle). Queue untouched.
func (a *Agent) Abort(_ context.Context) error {
	a.controller.Abort()
	return nil
}

// Run blocks until Agent.Run returns — when the queue drains AND no
// in-flight turn remains, or when ctx is cancelled. Returns nil after a
// natural termination, or ErrAborted / ctx.Err() after Abort / cancel.
func (a *Agent) Run(ctx context.Context) error {
	if !a.controller.TryStart() {
		return errors.New("agent: Run already in progress")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.controller.SetCancel(cancel)

	defer func() {
		cancel()
		a.controller.End()
	}()

	var totalTurns int
	var totalUsage protocol.Usage
	// runMsgID is overwritten after each Dequeue below; the AgentEndEvent
	// defer reads the most recent snapshot so it always reflects the
	// last prompt the run consumed.
	var runMsgID string
	// runUserMsgID mirrors runMsgID but carries the user message's own id
	// (minted by the Loop), so persistUserMessage's row is not overwritten
	// by the assistant row keyed by runMsgID.
	var runUserMsgID string
	defer func() {
		a.bus.Emit(event.AgentEndEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{
				SessionID: a.session.ID,
				RunID:     a.CurrentRunID(),
				MessageID: runMsgID,
			}},
			TotalTurns: totalTurns,
			TotalUsage: totalUsage,
		})
	}()

	a.bus.Emit(event.RunStartEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: a.session.ID, RunID: a.CurrentRunID()}},
	})

	for {
		msg, mode, ok := a.queue.Dequeue(runCtx)
		if !ok {
			if errors.Is(runCtx.Err(), context.Canceled) {
				return ErrAborted
			}
			return runCtx.Err()
		}
		// Snapshot the messageID for this dequeued prompt; the executor
		// reads it via Deps.CurrentMessageID so all events for this prompt
		// carry the same MessageID. runUserMsgID is the user row's own id,
		// falling back to runMsgID on paths without a userMsgID src.
		runMsgID = a.CurrentMessageID()
		runUserMsgID = a.CurrentUserMessageID()
		if runUserMsgID == "" {
			runUserMsgID = runMsgID
		}
		a.bus.Emit(event.PromptReceivedEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{
				SessionID: a.session.ID,
				RunID:     a.CurrentRunID(),
				MessageID: runMsgID,
			}},
			Content: msg.Content,
			Mode:    event.Mode(mode),
		})
		a.session.Append(protocol.Message{
			Role:      protocol.RoleUser,
			Content:   msg.Content,
			Images:    toLLMImages(msg.Images),
			ID:        runUserMsgID,
			Timestamp: time.Now().UnixMilli(),
		})
		// Hook 1 of 3: persist the user message before the LLM call so a
		// crash mid-Run still leaves the question; keyed by runUserMsgID so
		// the assistant write cannot overwrite it.
		a.persistUserMessage(runCtx, runUserMsgID, msg.Content)

		turnsBefore := a.session.Len()
		// Hooks 2 and 3 must fire every iteration (even on error) so the
		// session row reflects activity; RunEndEvent is emitted only on
		// success, the error path emits AgentErrorEvent instead. The
		// staged attachments reach the LLM for this prompt only and feed
		// the sandbox granted-read set (attach = authorize).
		a.runImportedNote = formatImportedNote(msg.Attachments)
		a.SetGrantedReads(msg.Attachments)
		err := a.exec.RunConversation(runCtx, a)
		a.runImportedNote = ""
		a.SetGrantedReads(nil)
		turnsThisRun := a.approxTurns(turnsBefore)
		totalTurns += turnsThisRun
		// Hook 2 of 3: persist the assistant messages RunConversation
		// produced. Persist all rows appended since turnsBefore so a
		// multi-turn (tool_calls loop) replays fully on next launch.
		a.persistAssistantMessages(runCtx, runMsgID, turnsBefore)
		// Hook 3 of 3: refresh session metadata so list_sessions reflects
		// the new updated_at.
		a.persistSession(runCtx)
		if err != nil {
			// Sealing write: persist done=true + error so a reload
			// paints an error bubble. Runs in the canceled branch too — an
			// aborted turn is still a "sealed with error" row.
			if a.msgStore != nil && runMsgID != "" {
				errMsg := err.Error()
				if markErr := a.msgStore.MarkError(runCtx, runMsgID, errMsg); markErr != nil {
					a.logger.Warn("mark message error failed",
						zap.String("message_id", runMsgID), zap.Error(markErr))
				}
			}
			if errors.Is(err, context.Canceled) {
				a.bus.Emit(event.AgentErrorEvent{
					EventBase: event.EventBase{EventCommon: event.EventCommon{
						SessionID: a.session.ID,
						RunID:     a.CurrentRunID(),
						MessageID: runMsgID,
					}},
					Err: ErrAborted,
				})
				return ErrAborted
			}
			a.bus.Emit(event.AgentErrorEvent{
				EventBase: event.EventBase{EventCommon: event.EventCommon{
					SessionID: a.session.ID,
					RunID:     a.CurrentRunID(),
					MessageID: runMsgID,
				}},
				Err: err,
			})
			return err
		}
		// Sealing write: mark the run's message done so a reload
		// sees done=true. RunEndEvent is still forwarded to the renderer
		// via EventRouter; persistence is now purely a store concern.
		if a.msgStore != nil && runMsgID != "" {
			if err := a.msgStore.MarkDone(runCtx, runMsgID); err != nil {
				a.logger.Warn("mark message done failed",
					zap.String("message_id", runMsgID), zap.Error(err))
			}
		}
		a.bus.Emit(event.RunEndEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: a.session.ID, RunID: a.CurrentRunID()}},
			Turns:     turnsThisRun,
		})
		a.emitContextUsage()
		// Persist the per-session usage snapshot so renderer-side
		// contextUsageBySessionId survives a restart / eviction.
		a.persistUsageSnapshot(runCtx)

		// if no followup is queued, exit; otherwise loop and consume it
		if a.queue.Len() == 0 {
			return nil
		}
	}
}

// emitContextUsage pushes a context_usage snapshot after a completed run so
// the renderer can paint the context ring. used prefers the API-reported
// prompt token count (≈ context occupancy); when the provider's stream omits
// input_tokens (common behind a proxy) it falls back to the local rune/4
// estimate over the session. The window comes from the model registry.
// Skipped when no LLM call completed or the model's window is unknown, so a
// 0% ring never appears. The same numbers feed persistUsageSnapshot so the
// snapshot row carries the rendered percent / window alongside the token
// counts (renderer hydrates the indicator without a model registry lookup).
func (a *Agent) emitContextUsage() {
	used, ctx := a.contextUsageInputs()
	if used <= 0 || ctx <= 0 {
		return
	}
	a.bus.Emit(event.ContextUsageEvent{
		EventBase:     event.EventBase{EventCommon: event.EventCommon{SessionID: a.session.ID}},
		UsedTokens:    used,
		ContextTokens: ctx,
		Percent:       int(float64(used) / float64(ctx) * 100),
	})
}

// contextUsageInputs returns the (used, context-window) pair that drives
// the context_usage event and the persisted snapshot. Returns (0,0) when
// no LLM call has completed yet or the model registry has no window for
// the active model — callers treat that as "skip the emit / write".
func (a *Agent) contextUsageInputs() (used, ctxTokens int) {
	used = a.LastUsage().PromptTokens
	if used <= 0 {
		for _, m := range a.session.Messages() {
			used += ctxengine.EstimateMessageTokens(m)
		}
	}
	if used <= 0 {
		return 0, 0
	}
	if d, ok := protocol.DefaultModelRegistry.Get(a.ModelName()); ok {
		ctxTokens = d.ContextWindow
	}
	return used, ctxTokens
}

// persistUserMessage is hook 1 of 3: records the just-appended user
// prompt so a crash mid-Run leaves the question on disk. No-op when
// MessageStore is nil or the message lacks an id.
func (a *Agent) persistUserMessage(ctx context.Context, msgID, content string) {
	if a.msgStore == nil || msgID == "" {
		return
	}
	// Done=true from the start: a user message is complete the moment it
	// is sent (no streaming), so the row must be sealed or the renderer
	// shows a "thinking" spinner after a session reload.
	rec := &store.MessageRecord{
		ID:        msgID,
		SessionID: a.session.ID,
		Role:      string(protocol.RoleUser),
		Content:   content,
		Done:      true,
		Timestamp: time.Now().UnixMilli(),
	}
	if err := a.msgStore.Save(ctx, rec); err != nil {
		a.logger.Warn("persist user message failed",
			zap.String("message_id", msgID),
			zap.String("session_id", a.session.ID),
			zap.Error(err))
	}
}

// persistAssistantMessages is hook 2 of 3. It walks every message
// appended since `beforeLen` and writes the assistant ones to disk.
// Each assistant row is keyed by the run's messageID.
// ToolCalls are JSON-encoded into the ToolCalls column.
func (a *Agent) persistAssistantMessages(ctx context.Context, msgID string, beforeLen int) {
	if a.msgStore == nil || msgID == "" {
		return
	}
	msgs := a.session.Messages()
	if beforeLen >= len(msgs) {
		return
	}
	now := time.Now().UnixMilli()
	for i := beforeLen; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role != protocol.RoleAssistant {
			continue
		}
		var toolCallsJSON string
		if len(m.ToolCalls) > 0 {
			b, err := json.Marshal(m.ToolCalls)
			if err != nil {
				a.logger.Warn("marshal tool_calls failed",
					zap.String("message_id", msgID), zap.Error(err))
				continue
			}
			toolCallsJSON = string(b)
		}
		// Each turn gets its own row id + monotonic timestamp: within the
		// same run, multiple tool-loop rounds no longer overwrite each
		// other, and reload preserves turn order when sorted by
		// timestamp.
		rec := &store.MessageRecord{
			ID:        fmt.Sprintf("%s-%d", msgID, i),
			SessionID: a.session.ID,
			Role:      string(protocol.RoleAssistant),
			Content:   m.Content,
			ToolCalls: toolCallsJSON,
			Timestamp: now + int64(i-beforeLen),
			Done:      true,
		}
		if err := a.msgStore.Save(ctx, rec); err != nil {
			a.logger.Warn("persist assistant message failed",
				zap.String("message_id", msgID),
				zap.Int("index", i),
				zap.Error(err))
		}
	}
}

// persistSession is hook 3 of 3. It saves the session row so
// list_sessions sees the new updated_at. Errors are logged and
// swallowed so a transient DB lock doesn't abort an otherwise successful
// Run — the in-memory session is the source of truth for the live agent.
func (a *Agent) persistSession(ctx context.Context) {
	if a.store == nil {
		return
	}
	if err := a.store.Save(ctx, a.session); err != nil {
		a.logger.Warn("persist session failed",
			zap.String("session_id", a.session.ID),
			zap.Error(err))
	}
}

// approxTurns returns the number of new assistant messages appended since
// the given baseline. This is a coarse proxy for the turn count (each turn
// produces exactly one assistant message). Multi-assistant turns would
// need a dedicated counter threaded through the executor.
func (a *Agent) approxTurns(beforeLen int) int {
	msgs := a.session.Messages()
	count := 0
	for i := beforeLen; i < len(msgs); i++ {
		if msgs[i].Role == protocol.RoleAssistant {
			count++
		}
	}
	return count
}

// formatImportedNote builds the transient system note telling the LLM which
// absolute attachment paths are staged for the current message.
func formatImportedNote(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return "[system] the user attached the following files to this message (absolute paths, authorised for read; use read_file):\n- " + strings.Join(files, "\n- ")
}

// enqueue places content into the Agent's queue under the given mode.
// Prompt is the only mode that requires the Agent to be idle.
func (a *Agent) enqueue(mode queue.Mode, content string, attachments []string, images []queue.ImageRef) error {
	if mode == queue.ModePrompt && a.controller.IsRunning() {
		return ErrAgentBusy
	}
	err := a.queue.Enqueue(mode, queue.Message{Content: content, Attachments: attachments, Images: images})
	if err != nil {
		if errors.Is(err, queue.ErrQueueFull) {
			return ErrAgentBusy
		}
		return err
	}
	return nil
}

// toLLMImages converts base64 image data URLs into provider-facing image
// blocks, splitting "data:<mime>;base64,<data>" into {MediaType, Data}.
// Malformed URLs are skipped so a bad attachment cannot break a run.
func toLLMImages(refs []queue.ImageRef) []protocol.ImageBlock {
	out := make([]protocol.ImageBlock, 0, len(refs))
	for _, r := range refs {
		mediaType, data := splitDataURL(r.DataURL)
		if mediaType == "" || data == "" {
			continue
		}
		out = append(out, protocol.ImageBlock{MediaType: mediaType, Data: data})
	}
	return out
}

// splitDataURL parses a `data:<mime>;base64,<payload>` URL. It returns
// ("", "") for anything that does not match that exact shape.
func splitDataURL(dataURL string) (string, string) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return "", ""
	}
	rest := dataURL[len(prefix):]
	sep := strings.Index(rest, ";base64,")
	if sep < 0 {
		return "", ""
	}
	return rest[:sep], rest[sep+len(";base64,"):]
}
