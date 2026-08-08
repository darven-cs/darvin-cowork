// Adapts protocol.ToolRegistry to the harness-facing Surface shape and
// chains ResultMiddleware that normalises tool output. The bridge never
// holds state of its own.

package harness

import (
	"darvin-cowork/backend/internal/agents/protocol"
)

// ResultMiddleware transforms a tool result before it is returned to
// the caller. Chained middleware compose right-to-left.
type ResultMiddleware func(protocol.Result) protocol.Result

// Surface is the harness-facing view of the tool registry.
type Surface interface {
	Specs() []protocol.ToolSpec
	Names() []string
	GetEntry(name string) (*protocol.Entry, bool)
	WithMiddleware(mw ...ResultMiddleware) Surface
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
	return b.reg.GetEntry(name)
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

// Apply is a convenience helper for callers that hold a Surface.
func Apply(s Surface, r protocol.Result) protocol.Result {
	if s == nil {
		return r
	}
	return s.ApplyMiddleware(r)
}
