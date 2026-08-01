package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/queue"
	"darvin-cowork/backend/internal/agent/store"
)

// Prompt enqueues content for immediate processing. Returns ErrAgentBusy
// if the Agent is already running; callers should use Steer / FollowUp
// instead.
func (a *Agent) Prompt(_ context.Context, content string) error {
	return a.enqueue(queue.ModePrompt, content)
}

// Steer enqueues content for the next iteration, cancelling any current
// run. The cancellation is observed by the executor on the next provider
// stream read. If the Agent is idle, Steer is equivalent to Prompt.
func (a *Agent) Steer(ctx context.Context, content string) error {
	// cancel any in-flight run so the current turn's ctx fires
	a.Abort(ctx)
	return a.enqueue(queue.ModeSteer, content)
}

// FollowUp enqueues content to be processed after the current Run ends.
// If the Agent is idle, FollowUp immediately starts a new Run via the
// caller's subsequent Run() invocation; FollowUp itself does not start
// a goroutine.
func (a *Agent) FollowUp(_ context.Context, content string) error {
	return a.enqueue(queue.ModeFollowUp, content)
}

// Abort cancels the current Run's context. It does not modify the queue.
// Safe to call when idle (no-op).
func (a *Agent) Abort(_ context.Context) error {
	a.runMu.Lock()
	cancel := a.cancelFn
	a.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Run blocks until Agent.Run returns. The Agent processes messages from
// its queue, one or more per Run; it returns when the queue drains AND
// no in-flight turn remains, OR when ctx is cancelled.
//
// The first return after a successful natural termination is nil. After
// Abort / ctx cancel, Run returns ErrAborted (or ctx.Err() if it fired
// directly).
func (a *Agent) Run(ctx context.Context) error {
	a.runMu.Lock()
	if a.state == stateRunning {
		a.runMu.Unlock()
		return errors.New("agent: Run already in progress")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.state = stateRunning
	a.cancelFn = cancel
	a.runMu.Unlock()

	defer func() {
		a.runMu.Lock()
		a.state = stateIdle
		a.cancelFn = nil
		a.runMu.Unlock()
		cancel()
	}()

	var totalTurns int
	var totalUsage llm.Usage
	// runMsgID is overwritten after each Dequeue below; the AgentEndEvent
	// defer reads the most recent snapshot so it always reflects the last
	// prompt the run consumed (across FollowUp iterations).
	var runMsgID string
	// runUserMsgID mirrors runMsgID but carries the user message's own id
	// (minted by the Loop), so persistUserMessage's row is not overwritten
	// by the assistant row keyed by runMsgID (spec FR-4).
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
		// Snapshot the messageID for this dequeued prompt. The executor
		// reads it via Deps.CurrentMessageID on every emit, so events
		// tagged to this prompt all carry the same MessageID.
		runMsgID = a.CurrentMessageID()
		// Snapshot the user message's own id. Fall back to runMsgID for
		// paths without a userMsgID src (steer agent / unit tests) so the
		// old behaviour is preserved there.
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
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg.Content})
		// Hook 1 of 3: persist the user message before the LLM call so a
		// crash mid-Run still leaves the question in sessions.db. The row is
		// keyed by runUserMsgID (not runMsgID) so persistAssistantMessages
		// cannot overwrite it with the assistant content (spec FR-4).
		a.persistUserMessage(runCtx, runUserMsgID, msg.Content)

		turnsBefore := a.session.Len()
		// Hooks 2 and 3 must fire on every iteration — even when
		// RunConversation errors — so the session row reflects the
		// activity (list_sessions surfaces the session as
		// recently-touched). RunEndEvent is emitted only on success;
		// the error path emits AgentErrorEvent and returns without
		// RunEndEvent so the renderer can paint an explicit error
		// bubble instead of a "done" state.
		err := a.exec.RunConversation(runCtx, a)
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
			// Sealing write (FR-4): persist done=true + error so a reload
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
		// Sealing write (FR-4): mark the run's message done so a reload
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

		// if no followup is queued, exit; otherwise loop and consume it
		if a.queue.Len() == 0 {
			return nil
		}
	}
}

// persistUserMessage is hook 1 of 3 (spec FR-2.2). It records the
// just-appended user prompt to the messages table so a crash mid-Run
// still leaves the question on disk. No-op when MessageStore is nil
// (the unit-test / fast-path default) or when the message lacks a
// messageID (RunContext hasn't bound one yet — should not happen on the
// dispatch path because Run always snapshots CurrentMessageID before
// calling here).
func (a *Agent) persistUserMessage(ctx context.Context, msgID, content string) {
	if a.msgStore == nil || msgID == "" {
		return
	}
	// Done=true from the start: a user message is complete the moment it is
	// sent — there is no streaming for it. The renderer (StreamingText) shows
	// the "思考中" pulse only when done=false, so a persisted user row must be
	// sealed or the question renders as a spinner after a session reload.
	rec := &store.MessageRecord{
		ID:        msgID,
		SessionID: a.session.ID,
		Role:      string(llm.RoleUser),
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
		if m.Role != llm.RoleAssistant {
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
		rec := &store.MessageRecord{
			ID:        msgID,
			SessionID: a.session.ID,
			Role:      string(llm.RoleAssistant),
			Content:   m.Content,
			ToolCalls: toolCallsJSON,
			Timestamp: now,
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
// list_sessions (FR-3) sees the new updated_at. Errors are logged and
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
		if msgs[i].Role == llm.RoleAssistant {
			count++
		}
	}
	return count
}

func (a *Agent) enqueue(mode queue.Mode, content string) error {
	// Prompt is the only mode that requires the agent to be idle. Steer and
	// FollowUp can both be issued while a Run is in progress: Steer cancels
	// the current run, FollowUp queues for after it returns.
	a.runMu.Lock()
	running := a.state == stateRunning
	a.runMu.Unlock()
	if mode == queue.ModePrompt && running {
		return ErrAgentBusy
	}
	err := a.queue.Enqueue(mode, queue.Message{Content: content})
	if err != nil {
		if errors.Is(err, queue.ErrQueueFull) {
			return ErrAgentBusy
		}
		return err
	}
	return nil
}
