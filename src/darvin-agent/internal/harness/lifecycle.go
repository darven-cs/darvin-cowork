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
// its result can be discarded rather than overwriting live state.
//
// This has no counterpart in the reference design, which expresses the same
// concern through explicit terminal / external-abort flags.
var lifecycle = struct {
	mu  sync.Mutex
	gen map[string]uint64
}{gen: map[string]uint64{}}

// observer holds the process-wide diagnostic observer. Nil by default.
var observer struct {
	mu sync.RWMutex
	o  Observer
}

// Observer receives harness-level diagnostics.
//
// It is not the application event bus. The embedded runtime already emits the
// run and turn event stream, so wiring leaves this nil for any harness that
// emits its own events; setting it there would duplicate every event a
// subscriber sees. It exists for backends that emit nothing of their own.
type Observer interface {
	AttemptStarted(ObserverAttempt)
	AttemptCompleted(ObserverAttempt, *AttemptResult)
	AttemptFailed(ObserverAttempt, error)
}

// ObserverAttempt identifies the attempt a diagnostic refers to.
type ObserverAttempt struct {
	HarnessID  string
	PluginID   string
	SessionID  string
	RunID      string
	MessageID  string
	Provider   string
	Model      string
	DurationMs int64
}

// SetObserver installs the diagnostic observer, replacing any previous one.
// Passing nil disables diagnostics.
func SetObserver(o Observer) {
	observer.mu.Lock()
	observer.o = o
	observer.mu.Unlock()
}

func currentObserver() Observer {
	observer.mu.RLock()
	defer observer.mu.RUnlock()
	return observer.o
}

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

// ResetLifecycleForTests clears every tracked generation and the observer.
func ResetLifecycleForTests() {
	lifecycle.mu.Lock()
	lifecycle.gen = map[string]uint64{}
	lifecycle.mu.Unlock()
	SetObserver(nil)
}

// RunAttemptWithLifecycle is the entry point callers use instead of calling
// Harness.RunAttempt directly. It validates the params, asserts the harness
// can host the caller's context engine, mints the missing correlation ids,
// applies the attempt timeout, contains a panicking backend, normalises the
// result, and runs the optional Classify pass.
//
// The context engine assertion runs here rather than only in Rank because a
// pinned harness and a direct call both bypass ranking, and either would
// otherwise run an engine the harness cannot host.
func RunAttemptWithLifecycle(ctx context.Context, h Harness, params RunAttemptParams) (*AttemptResult, error) {
	if err := validateRunAttemptParams(h, &params); err != nil {
		return nil, err
	}
	if err := assertContextEngineHost(h, params.ContextEngine); err != nil {
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

	obs := currentObserver()
	attempt := ObserverAttempt{
		HarnessID: h.ID(),
		PluginID:  h.PluginID(),
		SessionID: params.SessionID,
		RunID:     params.RunID,
		MessageID: params.MessageID,
		Provider:  params.Provider,
		Model:     params.Model,
	}
	if obs != nil {
		obs.AttemptStarted(attempt)
	}
	if params.OnExecutionStarted != nil {
		params.OnExecutionStarted()
	}
	if params.OnExecutionPhase != nil {
		params.OnExecutionPhase(PhaseStarting)
	}

	started := time.Now()
	result, err := runGuarded(attemptCtx, h, params)
	result = normalizeResult(result, err, h, params, time.Since(started))

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
	classifyResult(ctx, h, result, &params)

	if params.OnExecutionPhase != nil {
		params.OnExecutionPhase(PhaseSettling)
	}
	if obs != nil {
		attempt.DurationMs = result.DurationMs
		if err != nil {
			obs.AttemptFailed(attempt, err)
		} else {
			obs.AttemptCompleted(attempt, result)
		}
	}
	return result, err
}

// classifyResult clears any classification already on the result before
// asking the harness, so a retry or a wrapper cannot preserve a label from an
// earlier attempt. A classifier that declines to answer yields ok.
func classifyResult(ctx context.Context, h Harness, result *AttemptResult, params *RunAttemptParams) {
	result.Classification = ""
	if Implements(h, CapClassify) {
		result.Classification = h.(Classifier).Classify(ctx, result, params)
	}
	if result.Classification == "" {
		result.Classification = ClassificationOK
	}
}

// assertContextEngineHost rejects an attempt whose context engine needs host
// facilities the harness does not advertise.
func assertContextEngineHost(h Harness, req *ContextEngineRequirement) error {
	missing := h.Capabilities().MissingHostCapabilities(req)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrContextEngineUnsupported,
		describeMissingHostCapabilities(h, req, missing))
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

// normalizeResult guarantees a non-nil result carrying the producing harness,
// the attempt's generation and duration, and a status consistent with err.
func normalizeResult(result *AttemptResult, err error, h Harness, params RunAttemptParams, took time.Duration) *AttemptResult {
	if result == nil {
		result = &AttemptResult{}
	}
	result.HarnessID = h.ID()
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
