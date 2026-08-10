// context.go: WithContext / FromContext let tool implementations reach
// the parent session's *Manager through ctx, mirroring the pattern used
// by internal/jobs.

package subagent

import "context"

type ctxKey struct{}

// WithContext returns a derived ctx carrying the Manager.
func WithContext(ctx context.Context, m *Manager) context.Context {
	if m == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, m)
}

// FromContext returns the Manager stored in ctx, if any.
func FromContext(ctx context.Context) (*Manager, bool) {
	m, ok := ctx.Value(ctxKey{}).(*Manager)
	return m, ok && m != nil
}
