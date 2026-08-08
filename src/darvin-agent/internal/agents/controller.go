// Run-lifecycle state for the Agent: whether a turn is in flight and
// the cancel function bound to that turn.

package agent

import (
	"context"
	"sync"
)

// State is one of Idle / Running.
type State int

const (
	Idle State = iota
	Running
)

// Controller owns the run-lifecycle state. Construct via NewController.
// One Controller per Agent; not goroutine-shared across agents.
type Controller struct {
	mu       sync.Mutex
	state    State
	cancelFn context.CancelFunc
}

// NewController returns a Controller in Idle state with no cancel bound.
func NewController() *Controller { return &Controller{} }

// TryStart transitions Idle → Running; false if already Running.
func (c *Controller) TryStart() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == Running {
		return false
	}
	c.state = Running
	return true
}

// End transitions Running → Idle and cancels any bound context.
func (c *Controller) End() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = Idle
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
}

// SetCancel binds a context cancel function for the next End / Abort.
func (c *Controller) SetCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.cancelFn = cancel
	c.mu.Unlock()
}

// Abort fires the bound cancel without changing state.
func (c *Controller) Abort() {
	c.mu.Lock()
	cancel := c.cancelFn
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsRunning reports whether the controller is currently Running.
func (c *Controller) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == Running
}
