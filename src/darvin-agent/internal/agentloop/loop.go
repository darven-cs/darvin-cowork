// Package acp wraps the agent runtime in a minimal Agent-Client Protocol
// surface used by the gateway handlers. Loop owns one session's turn queue
// and the in-flight messageID.
package agentloop

import (
	"context"
	"errors"
	"sync"

	"github.com/jaevor/go-nanoid"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/queue"
	"darvin-cowork/backend/internal/harness"
	tool "darvin-cowork/backend/internal/tools"
)

const (
	// messageIDLen matches the gateway's session-id length; the alphabet
	// is the same 62-char [A-Za-z0-9] table for URL/JSON-friendliness.
	messageIDLen    = 21
	messageAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// ErrLoopClosed is returned by Submit / Steer after Close: the run
// goroutine is gone, so a queued turn would never be executed.
var ErrLoopClosed = errors.New("acp: loop closed")

// PromptRequest is one turn's input. RunID lets the caller name the turn
// for a later Stop; empty lets Loop mint one. Attachments are absolute
// paths staged for this message ("attach = authorize"); Images are base64
// attachments converted to LLM image content blocks by the dispatcher.
type PromptRequest struct {
	RunID       string
	Content     string
	Attachments []string
	Images      []queue.ImageRef
}

// RunTicket correlates a submitted turn with its events; Queued reports
// the turn is parked behind an in-flight run.
type RunTicket struct {
	RunID     string
	MessageID string
	Queued    bool
}

// SkillInvocation is a user-invoked skill turn: SKILL.md becomes the
// system prompt, Content is the raw `/skill-name args` command, Tools is
// the skill's allowed set.
type SkillInvocation struct {
	SystemPrompt string
	Content      string
	Tools        []tool.Tool
}

// promptReq is a submitted turn plus minted messageIDs. msgID keys the
// assistant message; userMsgID keys the user row so it survives the
// assistant write. skill is non-nil for skill invocations.
type promptReq struct {
	runID       string
	content     string
	attachments []string
	images      []queue.ImageRef
	skill       *SkillInvocation
	msgID       string
	userMsgID   string
}

// activeRunState is the in-flight turn; cancelRun cuts the LLM stream.
type activeRunState struct {
	runID     string
	cancelRun context.CancelFunc
}

// Loop drives one *agent.Agent's turns strictly one at a time: a single
// goroutine consumes the two queues, keeping the Agent free of concurrent
// Run calls. Submit appends to followUpQueue; Steer appends to
// steerQueue and cancels the in-flight turn. The prompt path goes through
// harness.RunAttemptWithLifecycle rather than agent.Prompt + agent.Run.
type Loop struct {
	agent   *agent.Agent
	harness harness.Harness

	// wake signals the run goroutine that a queue grew; capacity 1
	// collapses bursts (the goroutine drains under the lock regardless).
	wake chan struct{}

	mu            sync.Mutex
	activeRun     *activeRunState
	steerQueue    []promptReq
	followUpQueue []promptReq
	curMsg        string
	curUserMsg    string
	curRunID      string
	closed        bool

	idGen func() string

	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}
}

// NewLoop builds a Loop and starts its run goroutine. Ids are 21-char
// nanoids using the gateway's session-id alphabet. The Loop context is
// background-rooted so a handler / WS disconnect cannot cancel a run other
// subscribers are watching — cancellation goes through Stop / Abort /
// Close. h is the harness the prompt path drives; nil is allowed (skill
// turns still work; prompts surface an error).
func NewLoop(a *agent.Agent, h harness.Harness) *Loop {
	ctx, stop := context.WithCancel(context.Background())
	l := &Loop{
		agent:   a,
		harness: h,
		wake:    make(chan struct{}, 1),
		idGen:   nanoid.MustCustomASCII(messageAlphabet, messageIDLen),
		ctx:     ctx,
		stop:    stop,
		done:    make(chan struct{}),
	}
	go l.run()
	return l
}

// Submit queues content as a new turn, running once the session is idle.
func (l *Loop) Submit(req PromptRequest) (RunTicket, error) {
	return l.admit(req, nil, false)
}

// Steer queues ahead of parked follow-ups and cancels the in-flight turn;
// on an idle session it behaves like Submit.
func (l *Loop) Steer(req PromptRequest) (RunTicket, error) {
	return l.admit(req, nil, true)
}

// SubmitSkill queues a user-invoked skill turn through the same machinery,
// driving a mini loop with the skill's system prompt and tools.
func (l *Loop) SubmitSkill(sec SkillInvocation) (RunTicket, error) {
	return l.admit(PromptRequest{}, &sec, false)
}

// admit is the shared entry for Submit / SubmitSkill / Steer. jumpQueue
// selects steerQueue and cancels the in-flight turn so the run goroutine
// reaches the new request immediately.
func (l *Loop) admit(req PromptRequest, skill *SkillInvocation, jumpQueue bool) (RunTicket, error) {
	p := promptReq{
		runID:       req.RunID,
		content:     req.Content,
		attachments: req.Attachments,
		images:      req.Images,
		skill:       skill,
		msgID:       l.idGen(),
		userMsgID:   l.idGen(),
	}
	if p.runID == "" {
		p.runID = l.idGen()
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return RunTicket{}, ErrLoopClosed
	}
	if jumpQueue {
		l.steerQueue = append(l.steerQueue, p)
	} else {
		l.followUpQueue = append(l.followUpQueue, p)
	}
	active := l.activeRun
	l.mu.Unlock()

	select {
	case l.wake <- struct{}{}:
	default:
	}
	if jumpQueue && active != nil {
		active.cancelRun()
	}
	return RunTicket{RunID: p.runID, MessageID: p.msgID, Queued: active != nil}, nil
}

// Stop cancels the in-flight turn named by runID and drops parked
// requests. Reports whether a turn was actually cancelled.
func (l *Loop) Stop(runID string) bool {
	l.mu.Lock()
	active := l.activeRun
	if active == nil || active.runID != runID {
		l.mu.Unlock()
		return false
	}
	l.steerQueue, l.followUpQueue = nil, nil
	l.mu.Unlock()
	active.cancelRun()
	return true
}

// Abort cancels the in-flight turn regardless of runID and drops parked
// queues. No-op on an idle session.
func (l *Loop) Abort(context.Context) error {
	l.mu.Lock()
	active := l.activeRun
	l.steerQueue, l.followUpQueue = nil, nil
	l.mu.Unlock()
	if active != nil {
		active.cancelRun()
	}
	return nil
}

// Close cancels the in-flight turn, rejects further Submit / Steer, and
// waits for the run goroutine to exit. Safe to call more than once.
func (l *Loop) Close() {
	l.mu.Lock()
	alreadyClosed := l.closed
	l.closed = true
	l.steerQueue, l.followUpQueue = nil, nil
	l.mu.Unlock()
	if !alreadyClosed {
		l.stop()
	}
	<-l.done
}

// CurrentMessageID returns the current / last run's messageID, read by
// the executor via Deps.CurrentMessageID to stamp events.
func (l *Loop) CurrentMessageID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curMsg
}

// CurrentUserMessageID returns the current / last run's user-message id,
// distinct from CurrentMessageID so the persisted user row survives.
func (l *Loop) CurrentUserMessageID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curUserMsg
}

// CurrentRunID returns the current / last run's runID, read via
// Deps.CurrentRunID so events can be aborted / demultiplexed by turn.
func (l *Loop) CurrentRunID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curRunID
}

// ActiveRunID returns the in-flight turn's runID, or "" when idle
// (CurrentRunID keeps the last value after a turn ends).
func (l *Loop) ActiveRunID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeRun == nil {
		return ""
	}
	return l.activeRun.runID
}

// run is the single turn-executing goroutine.
func (l *Loop) run() {
	defer close(l.done)
	for {
		req, ok := l.next()
		if !ok {
			return
		}
		l.executeTurn(req)
	}
}

// next takes the next request (steer first), blocking on wake while both
// queues are empty; false once the Loop context is cancelled.
func (l *Loop) next() (promptReq, bool) {
	for {
		l.mu.Lock()
		req, ok := l.popLocked()
		l.mu.Unlock()
		if ok {
			return req, true
		}
		select {
		case <-l.ctx.Done():
			return promptReq{}, false
		case <-l.wake:
		}
	}
}

// popLocked removes the head of steerQueue, else of followUpQueue.
func (l *Loop) popLocked() (promptReq, bool) {
	if len(l.steerQueue) > 0 {
		req := l.steerQueue[0]
		l.steerQueue = l.steerQueue[1:]
		return req, true
	}
	if len(l.followUpQueue) > 0 {
		req := l.followUpQueue[0]
		l.followUpQueue = l.followUpQueue[1:]
		return req, true
	}
	return promptReq{}, false
}

// executeTurn registers req as the active run and drives the Agent
// through it. curMsg is published here (not in admit) so a parked request
// cannot steal the messageID of the turn still emitting events.
func (l *Loop) executeTurn(req promptReq) {
	runCtx, cancelRun := context.WithCancel(l.ctx)
	l.mu.Lock()
	l.activeRun = &activeRunState{runID: req.runID, cancelRun: cancelRun}
	l.curMsg = req.msgID
	l.curUserMsg = req.userMsgID
	l.curRunID = req.runID
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		l.activeRun = nil
		l.mu.Unlock()
		cancelRun()
	}()

	if req.skill != nil {
		if err := l.agent.RunSkillSession(runCtx, req.skill.SystemPrompt, req.skill.Content, req.skill.Tools); err != nil {
			// The enqueue was rejected before any run started, so no run
			// will emit an error for this messageID. Surface it here or the
			// renderer's bubble stays stuck in a streaming state.
			l.agent.Emit(event.AgentErrorEvent{
				EventBase: event.EventBase{EventCommon: event.EventCommon{
					SessionID: l.agent.Session().ID,
					MessageID: req.msgID,
				}},
				Err: err,
			})
		}
		return
	}

	if l.harness == nil {
		// Spec 04 §4.2: every AgentLoopSession is built with a Harness (factory
		// resolveHarness picks one). A nil here is a wiring bug; surface it
		// the same way the agent would have.
		l.agent.Emit(event.AgentErrorEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{
				SessionID: l.agent.Session().ID,
				MessageID: req.msgID,
			}},
			Err: errNoHarness,
		})
		return
	}

	params := harness.RunAttemptParams{
		SessionID:     l.agent.Session().ID,
		Prompt:        req.content,
		Images:        attachmentsToImages(req.images),
		Attachments:   req.attachments,
		Provider:      extractProviderName(l.agent),
		Model:         l.agent.ModelName(),
		RunID:         req.runID,
		MessageID:     req.msgID,
		UserMessageID: req.userMsgID,
	}

	// RunAttemptWithLifecycle is synchronous; it returns when the attempt
	// settles or ctx is cancelled. Errors carry the harness / runner's
	// verdict; the agent's event bus already saw every LLM / tool event
	// the harness emitted, so the renderer has a complete picture.
	_, _ = harness.RunAttemptWithLifecycle(runCtx, l.harness, params)
}
