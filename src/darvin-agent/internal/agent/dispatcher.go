package agent

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/llm"
	"darvin-cowork/backend/internal/agent/queue"
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
	defer func() {
		a.bus.Emit(event.AgentEndEvent{
			SessionID:  a.session.ID,
			TotalTurns: totalTurns,
			TotalUsage: totalUsage,
		})
	}()

	a.bus.Emit(event.RunStartEvent{SessionID: a.session.ID})

	for {
		msg, mode, ok := a.queue.Dequeue(runCtx)
		if !ok {
			if errors.Is(runCtx.Err(), context.Canceled) {
				return ErrAborted
			}
			return runCtx.Err()
		}
		a.bus.Emit(event.PromptReceivedEvent{Content: msg.Content, Mode: event.Mode(mode)})
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg.Content})

		turnsBefore := a.session.Len()
		if err := a.exec.RunConversation(runCtx, a); err != nil {
			if errors.Is(err, context.Canceled) {
				a.bus.Emit(event.AgentErrorEvent{Err: ErrAborted})
				return ErrAborted
			}
			a.bus.Emit(event.AgentErrorEvent{Err: err})
			return err
		}
		// approximate turn count from messages: each turn adds at least one
		// assistant message. We compare session length before / after.
		turnsThisRun := a.approxTurns(turnsBefore)
		totalTurns += turnsThisRun
		a.bus.Emit(event.RunEndEvent{Turns: turnsThisRun})

		// if no followup is queued, exit; otherwise loop and consume it
		if a.queue.Len() == 0 {
			return nil
		}
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
