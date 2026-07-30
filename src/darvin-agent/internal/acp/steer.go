package acp

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agent"
)

// ErrSteerNotImplemented is returned by SteerControl.Redirect in v0.
// Redirect needs real abort-and-redirect plumbing that the v0 single-
// session model doesn't carry; the S5 steer RPC exposes only Steer.
var ErrSteerNotImplemented = errors.New("acp: Redirect not implemented in v0")

// SteerControl re-prioritises or redirects mid-run. v0 is partial:
// Steer delegates to agent.Steer; Redirect returns ErrSteerNotImplemented.
type SteerControl interface {
	Steer(ctx context.Context, content string) error
	Redirect(ctx context.Context, content string) error
}

// NewSteerControl constructs the v0 SteerControl implementation. The
// returned value satisfies SteerControl but Redirect will reject every
// call until the v0 single-session limit is lifted.
func NewSteerControl(a *agent.Agent) SteerControl {
	return &steerControl{agent: a}
}

type steerControl struct{ agent *agent.Agent }

func (s *steerControl) Steer(ctx context.Context, content string) error {
	return s.agent.Steer(ctx, content)
}

func (s *steerControl) Redirect(_ context.Context, _ string) error {
	return ErrSteerNotImplemented
}
