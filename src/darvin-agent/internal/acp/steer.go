package acp

import (
	"context"
	"errors"

	"darvin-cowork/backend/internal/agents"
)

// ErrSteerNotImplemented is returned by SteerControl.Redirect. Redirect
// needs real abort-and-redirect plumbing not yet wired into the single-
// session model.
var ErrSteerNotImplemented = errors.New("acp: Redirect not implemented")

// SteerControl re-prioritises or redirects mid-run. Steer delegates to
// agent.Steer; Redirect returns ErrSteerNotImplemented.
type SteerControl interface {
	Steer(ctx context.Context, content string) error
	Redirect(ctx context.Context, content string) error
}

// NewSteerControl constructs the partial SteerControl implementation.
// Redirect rejects every call until the single-session limit is lifted.
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
