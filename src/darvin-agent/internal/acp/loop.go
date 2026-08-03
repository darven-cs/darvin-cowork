// Package acp wraps the agent runtime in a minimal Agent-Client Protocol
// surface used by the gateway handlers. Loop owns one session's turn
// queue and the in-flight messageID; Queue is a thin shim around
// agent/queue; SteerControl separates Steer (cancel + enqueue) from
// Redirect.
package acp

import (
	"context"
	"errors"
	"sync"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/agent"
	"darvin-cowork/backend/internal/agent/event"
	"darvin-cowork/backend/internal/agent/queue"
	"darvin-cowork/backend/internal/agent/tool"
)

const (
	// messageIDLen is the length of message ids Loop generates per turn.
	// Same length as the gateway's session ids; the alphabet is the same
	// 62-char [A-Za-z0-9] table for URL/JSON-friendliness.
	messageIDLen = 21
	// messageAlphabet matches gateway.sessionAlphabet — keep them aligned
	// so downstream consumers don't have to special-case any character.
	messageAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// ErrLoopClosed is returned by Submit / Steer after Close: the run
// goroutine is gone, so a queued turn would never be executed.
var ErrLoopClosed = errors.New("acp: loop closed")

// PromptRequest is one turn's input. RunID lets the caller name the turn
// up front so a later Stop can target exactly it; an empty RunID makes
// Loop mint one. Attachments are absolute paths staged for this message:
// the dispatcher injects a transient system note and grants read_file access.
// Images are base64-encoded image attachments; the dispatcher converts them
// into LLM image content blocks.
type PromptRequest struct {
	RunID       string
	Content     string
	Attachments []string
	Images      []queue.ImageRef
}

// RunTicket correlates a submitted turn with the events it will emit.
// Queued reports that the turn is parked behind an in-flight run instead
// of having started immediately.
type RunTicket struct {
	RunID     string
	MessageID string
	Queued    bool
}

// SkillInvocation is a user-invoked skill turn: the skill's SKILL.md body
// becomes the system prompt, content is the raw `/skill-name args` command
// the user sent, and Tools is the skill's allowed tool set.
type SkillInvocation struct {
	SystemPrompt string
	Content      string
	Tools        []tool.Tool
}

// promptReq is a submitted turn plus the messageIDs Loop minted for it.
// msgID keys the assistant message (events carry it for streaming append);
// userMsgID keys the user message so persistUserMessage's row is not
// overwritten by the assistant row that shares msgID (see dispatcher.go).
// skill is non-nil when the turn is a skill invocation (RunSkillSession).
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

// Loop drives one *agent.Agent's turns strictly one at a time. A single
// goroutine consumes the two queues, which is what keeps the Agent's own
// state machine free of concurrent Run calls.
//
// Submit appends to followUpQueue; Steer appends to steerQueue and
// cancels the in-flight turn so its content is what runs next. Requests
// within one queue keep submission order.
type Loop struct {
	agent *agent.Agent

	// wake tells the run goroutine that a queue grew. Capacity 1 collapses
	// bursts: the goroutine drains both queues under the lock anyway, so a
	// dropped token can never hide a queued request.
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
// nanoids using the same alphabet the gateway uses for session ids.
//
// The Loop context is background-rooted on purpose: a handler returning
// or a WS client disconnecting must not cancel a run other subscribers
// are still watching. Cancellation goes through Stop / Abort / Close.
func NewLoop(a *agent.Agent) *Loop {
	ctx, stop := context.WithCancel(context.Background())
	l := &Loop{
		agent: a,
		wake:  make(chan struct{}, 1),
		idGen: nanoid.MustCustomASCII(messageAlphabet, messageIDLen),
		ctx:   ctx,
		stop:  stop,
		done:  make(chan struct{}),
	}
	go l.run()
	return l
}

// Submit queues content as a new turn. It starts as soon as the session
// goes idle; anything already queued runs first.
func (l *Loop) Submit(req PromptRequest) (RunTicket, error) {
	return l.admit(req, nil, false)
}

// Steer queues content ahead of the parked follow-ups and cancels the
// in-flight turn so the new content is what runs next. On an idle
// session Steer behaves like Submit.
func (l *Loop) Steer(req PromptRequest) (RunTicket, error) {
	return l.admit(req, nil, true)
}

// SubmitSkill queues a user-invoked skill turn. It runs through the same
// single-turn machinery (one goroutine, messageIDs minted up front) but the
// executor drives a mini loop with the skill's system prompt and tools.
func (l *Loop) SubmitSkill(sec SkillInvocation) (RunTicket, error) {
	return l.admit(PromptRequest{}, &sec, false)
}

// admit is the shared entry for Submit / SubmitSkill / Steer. jumpQueue
// selects steerQueue over followUpQueue and additionally cancels the
// in-flight turn so the run goroutine reaches the new request immediately.
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

// Stop cancels the in-flight turn when it is the one named by runID and
// drops every request parked behind it. Reports whether a turn was
// actually cancelled — a stale runID is a no-op.
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

// Abort cancels whatever turn is in flight regardless of its runID and
// drops the parked queues. No-op on an idle session.
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

// Close cancels the in-flight turn, rejects further Submit / Steer and
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

// CurrentMessageID returns the messageID of the turn currently running,
// or of the last one that ran when the session is idle. The executor
// reads this via Deps.CurrentMessageID to stamp EventCommon.MessageID on
// every emitted event.
func (l *Loop) CurrentMessageID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curMsg
}

// CurrentUserMessageID returns the messageID minted for the current turn's
// user message, or of the last one that ran when the session is idle. It is
// distinct from CurrentMessageID so the persisted user row survives the
// assistant row that shares CurrentMessageID (spec FR-4).
func (l *Loop) CurrentUserMessageID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curUserMsg
}

// CurrentRunID returns the runID of the turn currently running, or of the
// last one that ran when the session is idle. The executor and
// dispatcher read this via Deps.CurrentRunID to stamp EventCommon.RunID
// on every emitted event so the renderer can abort a specific turn.
func (l *Loop) CurrentRunID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curRunID
}

// ActiveRunID 返回当前 in-flight turn 的 runID,idle 时返 ""。与
// CurrentRunID 的差别:CurrentRunID 在 turn 结束后保留最后一次的 runID
// 直到下一次新 run 开始,ActiveRunID 严格只在有 turn 跑着时非空。
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

// next takes the next request, steer queue first, and blocks on wake
// while both queues are empty. Reports false once the Loop context is
// cancelled.
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
// through it. curMsg is published here rather than in admit so a parked
// request cannot steal the messageID of the turn still emitting events.
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

	if err := l.agent.Prompt(runCtx, req.content, req.images, req.attachments); err != nil {
		// The Agent rejected the enqueue, so no run will emit an error
		// for this messageID. Surface it here or the renderer's bubble
		// stays stuck in a streaming state.
		l.agent.Emit(event.AgentErrorEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{
				SessionID: l.agent.Session().ID,
				MessageID: req.msgID,
			}},
			Err: err,
		})
		return
	}
	_ = l.agent.Run(runCtx)
}
