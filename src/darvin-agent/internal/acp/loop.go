// Package acp wraps the agent runtime in a minimal Agent-Client Protocol
// surface used by the gateway handlers. Loop owns the in-flight
// messageID; Queue is a thin shim around agent/queue; SteerControl
// separates Steer (cancel + enqueue) from Redirect.
package acp

import (
	"context"
	"sync"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/agent"
)

const (
	// messageIDLen is the length of message ids Loop generates per Prompt.
	// Same length as the gateway's session ids; the alphabet is the same
	// 62-char [A-Za-z0-9] table for URL/JSON-friendliness.
	messageIDLen = 21
	// messageAlphabet matches gateway.sessionAlphabet — keep them aligned
	// so downstream consumers don't have to special-case any character.
	messageAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// ErrAgentBusy is the sentinel Loop.Prompt propagates when the
// underlying Agent is already running. It aliases agent.ErrAgentBusy so
// callers can match on it without importing the agent package.
var ErrAgentBusy = agent.ErrAgentBusy

// Loop wraps a single *agent.Agent with prompt/abort plumbing. v0 is
// single-session: only the agent's own session.ID is the live one. The
// gateway enforces that via SessionManager.DefaultID().
type Loop struct {
	agent  *agent.Agent
	mu     sync.Mutex
	curMsg string
	msgGen func() string
}

// NewLoop builds a Loop whose messageID generator is a 21-char nanoid
// using the same alphabet the gateway uses for session ids.
func NewLoop(a *agent.Agent) *Loop {
	return &Loop{
		agent:  a,
		msgGen: nanoid.MustCustomASCII(messageAlphabet, messageIDLen),
	}
}

// Prompt assigns a fresh messageID, enqueues content, and spawns the
// Agent's Run goroutine. The handler returns the messageID to the
// caller before Run finishes; events emitted by the run all carry it via
// EventCommon.MessageID. Returns ErrAgentBusy when the Agent rejects the
// Prompt because a Run is already in progress.
//
// curMsg is published only after the enqueue succeeds. A rejected Prompt
// must leave the previous id intact, otherwise the run that caused the
// rejection would emit its remaining events with a cleared messageID.
func (l *Loop) Prompt(ctx context.Context, content string) (string, error) {
	msgID := l.msgGen()
	if err := l.agent.Prompt(ctx, content); err != nil {
		return "", err
	}
	l.mu.Lock()
	l.curMsg = msgID
	l.mu.Unlock()

	go func() {
		// background ctx: handler / WS disconnect must NOT cancel the run
		// (other clients may still be subscribed). Cancellation goes
		// through Agent.Abort.
		_ = l.agent.Run(context.Background())
	}()
	return msgID, nil
}

// Abort cancels the in-flight Run via Agent.Abort.
func (l *Loop) Abort(ctx context.Context) error { return l.agent.Abort(ctx) }

// CurrentMessageID returns the messageID Loop assigned to the most
// recent Prompt that wasn't rejected with ErrAgentBusy. Returns "" when
// no Prompt is in flight. The executor reads this via Deps.CurrentMessageID
// to embed EventCommon.MessageID on every emitted event.
func (l *Loop) CurrentMessageID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.curMsg
}
