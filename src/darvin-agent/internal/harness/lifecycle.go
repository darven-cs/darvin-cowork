package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// lifecycle tracks a monotonic generation per session. Reset bumps it; an
// attempt that started under an older generation is reported as superseded so
// its result can be discarded instead of racing the newer run.
var lifecycle = struct {
	mu  sync.Mutex
	gen map[string]uint64
}{gen: map[string]uint64{}}

// BumpLifecycleGeneration invalidates every in-flight attempt for sessionID
// and returns the new generation.
func BumpLifecycleGeneration(sessionID string) uint64 {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.gen[sessionID]++
	return lifecycle.gen[sessionID]
}

// LifecycleGeneration returns the current generation for sessionID.
func LifecycleGeneration(sessionID string) uint64 {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.gen[sessionID]
}

// ResetLifecycleForTests clears every tracked generation.
func ResetLifecycleForTests() {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.gen = map[string]uint64{}
}

// RunAttemptWithLifecycle is the entry point callers use instead of calling
// Harness.RunAttempt directly. It validates the params, mints the missing
// correlation ids, applies the attempt timeout, contains a panicking backend,
// normalises the result, and runs the optional Classify pass.
//
// It deliberately emits no events: the embedded runtime already emits the
// run/turn event stream, and a second emitter here would duplicate every
// event a subscriber sees.
func RunAttemptWithLifecycle(ctx context.Context, h Harness, params RunAttemptParams) (*AttemptResult, error) {
	if err := validateRunAttemptParams(h, &params); err != nil {
		return nil, err
	}

	gen := LifecycleGeneration(params.SessionID)
	params.LifecycleGen = gen

	attemptCtx := ctx
	if params.TimeoutMs > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(params.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	if params.OnExecutionStarted != nil {
		params.OnExecutionStarted()
	}
	if params.OnExecutionPhase != nil {
		params.OnExecutionPhase(PhaseStarting)
	}

	started := time.Now()
	result, err := runGuarded(attemptCtx, h, params)
	result = normalizeResult(result, err, params, time.Since(started))

	switch result.Status {
	case AttemptTimeout:
		if params.OnAttemptTimeout != nil {
			params.OnAttemptTimeout()
		}
	case AttemptAborted:
		if params.OnAttemptAbort != nil {
			params.OnAttemptAbort()
		}
	}

	if LifecycleGeneration(params.SessionID) != gen {
		result.Superseded = true
	}
	if c, ok := h.(Classifier); ok && h.Capabilities().Classify {
		if label := c.Classify(ctx, result, &params); label != "" {
			result.Classification = label
		}
	}
	if params.OnExecutionPhase != nil {
		params.OnExecutionPhase(PhaseSettling)
	}
	return result, err
}

// runGuarded converts a panic inside the backend into an error so a
// third-party harness cannot take the process down.
func runGuarded(ctx context.Context, h Harness, params RunAttemptParams) (result *AttemptResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("harness %q panicked: %v", h.ID(), r)
		}
	}()
	return h.RunAttempt(ctx, params)
}

func validateRunAttemptParams(h Harness, params *RunAttemptParams) error {
	if h == nil {
		return ErrHarnessRequired
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return ErrSessionIDRequired
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return ErrPromptRequired
	}
	if params.RunID == "" {
		params.RunID = uuid.NewString()
	}
	if params.MessageID == "" {
		params.MessageID = uuid.NewString()
	}
	if params.UserMessageID == "" {
		params.UserMessageID = uuid.NewString()
	}
	return nil
}

// normalizeResult guarantees a non-nil result carrying the attempt's
// generation, duration and a status consistent with err.
func normalizeResult(result *AttemptResult, err error, params RunAttemptParams, took time.Duration) *AttemptResult {
	if result == nil {
		result = &AttemptResult{}
	}
	result.LifecycleGen = params.LifecycleGen
	result.DurationMs = took.Milliseconds()
	if err != nil && result.LastError == nil {
		result.LastError = err
	}
	if err != nil || result.Status == "" {
		result.Status = statusFor(err)
	}
	return result
}

func statusFor(err error) AttemptStatus {
	switch {
	case err == nil:
		return AttemptOK
	case errors.Is(err, context.DeadlineExceeded):
		return AttemptTimeout
	case errors.Is(err, context.Canceled):
		return AttemptAborted
	default:
		return AttemptError
	}
}
