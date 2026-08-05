// Package perm owns the per-Agent permission state machine: the in-flight
// request channels waiting on the renderer, the "remember this session"
// auto-allow rules, and the 60-second default-deny timeout.
//
// The Gate is owned by exactly one *Agent and is not goroutine-shared
// across agents. It implements the executor.Deps surface that the tool
// loop depends on, so the executor keeps working with the new layout.
package perm

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/executor"
	"darvin-cowork/backend/internal/agents/protocol"
)

// DefaultTimeout is how long a pending permission_request waits for the
// renderer before defaulting to deny.
const DefaultTimeout = 60 * time.Second

// PermissionRequest / PermissionResult / PermissionEval are aliases of the
// executor and protocol types. Keeping them as type aliases rather than
// copies lets perm.Gate interoperate with the executor without dragging
// unrelated fields along.
type (
	PermissionRequest = executor.PermissionRequest
	PermissionResult  = executor.PermissionResult
	PermissionEval    = protocol.PermissionEval
)

// EventEmitter is the narrow surface the Gate needs to publish a
// permission_request. *event.Bus satisfies it; declaring it here keeps perm
// from importing the agent root package.
type EventEmitter interface {
	Emit(ev event.Event)
}

// EventContext feeds the in-flight turn ids onto the permission_request
// event. It is satisfied by *agent.Agent's facade methods; declaring it
// here keeps perm from depending on the agent root.
type EventContext interface {
	SessionID() string
	RunID() string
	MessageID() string
}

// ToolSurface is the subset of the tool registry perm.Gate talks to.
// Declaring it here lets tests wire a stub instead of the full registry.
type ToolSurface interface {
	SetGrantedReads(paths []string)
	ApprovePath(p string)
	EvaluatePermission(toolName string, args map[string]any) protocol.PermissionEval
}

// pendingPermission is one in-flight request awaiting the renderer's
// answer.
type pendingPermission struct {
	ch      chan PermissionResult
	timeout *time.Timer
}

// rule is a "remember this session" auto-allow entry: a request that
// matches (toolName, level, reason) skips the modal.
type rule struct {
	toolName string
	level    string
	reason   string
}

// Gate is the permission state machine.
type Gate struct {
	mu      sync.Mutex
	bus     EventEmitter
	logger  *zap.Logger
	ctx     EventContext
	timeout time.Duration

	pending map[string]*pendingPermission
	rules   []rule
}

// NewGate constructs a Gate. timeout ≤ 0 falls back to DefaultTimeout. bus
// or ctx may be nil; the gate degrades gracefully (no event published, no
// ids stamped).
func NewGate(bus EventEmitter, logger *zap.Logger, ctx EventContext, timeout time.Duration) *Gate {
	if bus == nil {
		bus = noopEmitter{}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Gate{
		bus:     bus,
		logger:  logger,
		ctx:     ctx,
		timeout: timeout,
		pending: make(map[string]*pendingPermission),
	}
}

// SetGrantedReads is a thin forward to the tool registry; the dispatcher
// drives both, the Gate just keeps the wiring in one place.
func (g *Gate) SetGrantedReads(paths []string, tools ToolSurface) {
	if tools == nil {
		return
	}
	tools.SetGrantedReads(paths)
}

// ApprovePath grants the sandbox one-shot access to a path the user
// allowed via the permission modal.
func (g *Gate) ApprovePath(p string, tools ToolSurface) {
	if tools == nil {
		return
	}
	tools.ApprovePath(p)
}

// EvaluatePermission returns the combined path-containment + danger
// classification for a tool call.
func (g *Gate) EvaluatePermission(toolName string, args map[string]any, tools ToolSurface) PermissionEval {
	if tools == nil {
		return PermissionEval{}
	}
	return tools.EvaluatePermission(toolName, args)
}

// RequestPermission emits a permission_request event and blocks until the
// renderer answers via ResolvePermission, the timeout fires (default deny),
// or ctx is cancelled. When the renderer returns Remember=true, the
// (tool, level, reason) tuple is recorded so future identical requests
// skip the modal.
func (g *Gate) RequestPermission(ctx context.Context, req PermissionRequest, _ ToolSurface) (PermissionResult, error) {
	id := uuid.NewString()
	ch := make(chan PermissionResult, 1)
	pp := &pendingPermission{ch: ch}
	pp.timeout = time.AfterFunc(g.timeout, func() {
		g.deliver(id, ch, PermissionResult{Behavior: "deny", Message: "审批超时"})
	})
	g.mu.Lock()
	g.pending[id] = pp
	g.mu.Unlock()

	g.bus.Emit(event.PermissionRequestEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{
			SessionID: g.ctx.SessionID(),
			RunID:     g.ctx.RunID(),
			MessageID: g.ctx.MessageID(),
		}},
		RequestID:   id,
		ToolName:    req.ToolName,
		ToolInput:   req.ToolInput,
		DangerLevel: req.DangerLevel,
		Reason:      req.Reason,
	})

	select {
	case r := <-ch:
		if r.Remember {
			g.AddRule(req.ToolName, req.DangerLevel, req.Reason)
		}
		return r, nil
	case <-ctx.Done():
		g.mu.Lock()
		if cur, ok := g.pending[id]; ok {
			delete(g.pending, id)
			cur.timeout.Stop()
		}
		g.mu.Unlock()
		return PermissionResult{Behavior: "deny", Message: "运行已中断"}, ctx.Err()
	}
}

// ResolvePermission delivers the renderer's answer to the blocked executor
// call. Unknown requestID (already timed out / cancelled) is a no-op.
func (g *Gate) ResolvePermission(requestID string, result PermissionResult) {
	g.mu.Lock()
	pp, ok := g.pending[requestID]
	if ok {
		delete(g.pending, requestID)
	}
	g.mu.Unlock()
	if !ok {
		return
	}
	pp.timeout.Stop()
	g.deliver(requestID, pp.ch, result)
}

// HasRule reports whether an identical (tool, level, reason) request was
// allowed + remembered this session.
func (g *Gate) HasRule(toolName, level, reason string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.rules {
		if r.toolName == toolName && r.level == level && r.reason == reason {
			return true
		}
	}
	return false
}

// AddRule records an auto-allow rule, deduplicating so the same rule
// requested twice is recorded once.
func (g *Gate) AddRule(toolName, level, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.rules {
		if r.toolName == toolName && r.level == level && r.reason == reason {
			return
		}
	}
	g.rules = append(g.rules, rule{toolName: toolName, level: level, reason: reason})
}

// deliver removes the pending entry (idempotent) and sends the result to
// the waiting channel. Used by the timeout path and ResolvePermission.
func (g *Gate) deliver(id string, ch chan PermissionResult, r PermissionResult) {
	g.mu.Lock()
	if _, ok := g.pending[id]; ok {
		delete(g.pending, id)
	}
	g.mu.Unlock()
	select {
	case ch <- r:
	default:
	}
}

// AttachContext swaps the EventContext the Gate reads turn ids from. Tests
// use it; production wires once at construction.
func (g *Gate) AttachContext(ctx EventContext) {
	g.mu.Lock()
	g.ctx = ctx
	g.mu.Unlock()
}

type noopEmitter struct{}

func (noopEmitter) Emit(event.Event) {}
