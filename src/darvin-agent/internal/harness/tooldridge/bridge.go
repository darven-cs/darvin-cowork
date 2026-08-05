// Package tooldridge adapts darvin-cowork's existing protocol.ToolRegistry
// to the harness-facing Surface shape and chains ResultMiddleware that
// normalises tool output before it reaches the LLM.
//
// The package has two roles and keeps them separate on purpose:
//
//   - Bridge / Surface is the read side: harnesses ask "what tools are
//     available?" without reaching into internal/agents/protocol.
//   - ResultMiddleware is the write side: a Result goes through the chain
//     before the executor forwards it on. middleware are pure functions;
//     they do not observe or mutate the registry.
//
// The bridge never holds state of its own; it is a thin adapter around a
// protocol.ToolRegistry the wiring layer passes in.
package tooldridge

import (
	"darvin-cowork/backend/internal/agents/protocol"
)

// ResultMiddleware is a pure function that transforms a tool result before
// it is returned to the caller. Chained middleware compose right-to-left:
// the last-added middleware sees the result first.
type ResultMiddleware func(protocol.Result) protocol.Result

// Surface is the harness-facing view of the tool registry.
type Surface interface {
	// Specs returns the tool specs the LLM sees. Surface is the harness's
	// only way to enumerate available tools without importing
	// internal/agents/protocol.
	Specs() []protocol.ToolSpec
	// Names lists the registered tool names. Used by status / debug.
	Names() []string
	// GetEntry fetches one tool's metadata. The second return is false when
	// the tool does not exist.
	GetEntry(name string) (*protocol.Entry, bool)
	// WithMiddleware returns a new Surface with the additional middleware
	// appended to the existing chain. The receiver is not modified.
	WithMiddleware(mw ...ResultMiddleware) Surface
	// ApplyMiddleware runs the chain against one result.
	ApplyMiddleware(r protocol.Result) protocol.Result
}

// New constructs a Surface wrapping reg.
func New(reg protocol.ToolRegistry) Surface {
	return &bridge{reg: reg}
}

type bridge struct {
	reg protocol.ToolRegistry
	mws []ResultMiddleware
}

func (b *bridge) Specs() []protocol.ToolSpec {
	return b.reg.Specs()
}

func (b *bridge) Names() []string {
	return b.reg.Names()
}

func (b *bridge) GetEntry(name string) (*protocol.Entry, bool) {
	entry, ok := b.reg.GetEntry(name)
	return entry, ok
}

func (b *bridge) WithMiddleware(mw ...ResultMiddleware) Surface {
	combined := make([]ResultMiddleware, 0, len(b.mws)+len(mw))
	combined = append(combined, b.mws...)
	combined = append(combined, mw...)
	return &bridge{reg: b.reg, mws: combined}
}

func (b *bridge) ApplyMiddleware(r protocol.Result) protocol.Result {
	cur := r
	for i := len(b.mws) - 1; i >= 0; i-- {
		cur = b.mws[i](cur)
	}
	return cur
}

// Apply is a convenience helper for callers that hold a Surface but do not
// want to type-assert the interface to its ApplyMiddleware method.
func Apply(s Surface, r protocol.Result) protocol.Result {
	if s == nil {
		return r
	}
	return s.ApplyMiddleware(r)
}
