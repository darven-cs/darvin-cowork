// Package subagent: Manager orchestrates sub-agent runs.

package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/store"
)

// RunResult is the in-memory payload kept for read_subagent_result.
// It survives only in-process; an off-by-default FullResultPath on the
// Subagent row would be the escape hatch for cross-process reloads.
type RunResult struct {
	mu        sync.Mutex
	full      []byte
	truncated bool
}

// Append appends delta to the buffer; truncates if running over cap.
func (r *RunResult) Append(delta []byte, cap int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.full = append(r.full, delta...)
	if len(r.full) > cap {
		r.full = r.full[:cap]
		r.truncated = true
	}
}

// Snapshot returns the current buffered text.
func (r *RunResult) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.full)
}

// Truncated reports whether the buffer hit its cap.
func (r *RunResult) Truncated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.truncated
}

// runState tracks one in-flight run.
type runState struct {
	info    protocol.SubagentInfo
	result  *RunResult
	cancel  context.CancelFunc
	aborted bool
	started chan struct{}
	waiters []chan protocol.SubagentInfo
	doneCh  chan struct{}
}

// Runner is the pluggable turn-driver. Implementations are wired by
// agentloop.Factory; tests can pass a stub that returns canned output.
// The runner must respect ctx cancellation, return the final assistant
// text, count tool calls, and surface any error (which Manager maps to
// status="error").
type Runner func(ctx context.Context, req RunnerRequest) (RunnerResult, error)

// RunnerRequest bundles the inputs a Runner consumes.
type RunnerRequest struct {
	SubagentID  string
	Prompt      string
	Description string
	Scope       []string
	Model       string
	ToolCallID  string
	ParentID    string
}

// RunnerResult is the Runner's output.
type RunnerResult struct {
	FinalText string
	ToolCalls int
}

// Deps wires the Manager's collaborators.
type Deps struct {
	Store         store.SubagentStore
	ParentSession string
	Runner        Runner
	MaxConcurrent int
	ResultBufCap  int
}

// ErrInvalidSpec is returned when a spec is missing Prompt.
var ErrInvalidSpec = errors.New("subagent: spec missing prompt")

// ErrUnknownID is returned when an id has no run.
var ErrUnknownID = errors.New("subagent: unknown id")

// ErrShuttingDown is returned when Spawn races with Close.
var ErrShuttingDown = errors.New("subagent: manager shutting down")

// Manager orchestrates sub-agent runs for one parent session.
type Manager struct {
	parentID string
	store    store.SubagentStore
	runner   Runner
	maxConc  int
	bufCap   int

	mu        sync.Mutex
	runs      map[string]*runState
	scheduler chan struct{} // semaphore: MaxConcurrent tokens
	closed    bool

	// storeMu serialises every SubagentStore call so the test SQLite
	// driver never sees two open handles at once. Production wiring
	// already has a single gorm.DB pool; this lock is a cheap belt
	// that makes the manager safe under both production and the
	// goroutine-heavy test paths.
	storeMu sync.Mutex
}

// NewManager constructs a Manager. MaxConcurrent <= 0 falls back to 8;
// ResultBufCap <= 0 falls back to 1 MiB.
func NewManager(deps Deps) *Manager {
	if deps.MaxConcurrent <= 0 {
		deps.MaxConcurrent = 8
	}
	if deps.ResultBufCap <= 0 {
		deps.ResultBufCap = 1 << 20
	}
	if deps.Store == nil {
		panic("subagent.NewManager: Store is required")
	}
	if deps.Runner == nil {
		panic("subagent.NewManager: Runner is required")
	}
	return &Manager{
		parentID:  deps.ParentSession,
		store:     deps.Store,
		runner:    deps.Runner,
		maxConc:   deps.MaxConcurrent,
		bufCap:    deps.ResultBufCap,
		runs:      make(map[string]*runState),
		scheduler: make(chan struct{}, deps.MaxConcurrent),
	}
}

// nextID returns "<parentID>/sub/<rand>" where rand is 8 bytes hex.
func nextID(parent string) string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return parent + "/sub/" + hex.EncodeToString(buf[:])
}

// Spawn starts a run. When RunInBackground=false, Spawn blocks until
// the run terminates (success, error, abort, or timeout). When true,
// Spawn returns immediately with a non-pending status; use Wait to
// block later, or Abort to cancel.
func (m *Manager) Spawn(ctx context.Context, spec protocol.SubagentSpec) (*protocol.SubagentInfo, error) {
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil, ErrInvalidSpec
	}
	if m.isClosed() {
		return nil, ErrShuttingDown
	}

	id := nextID(m.parentID)
	scope := ResolveScope(spec.Scope)
	now := time.Now()
	info := protocol.SubagentInfo{
		ID:          id,
		ParentID:    m.parentID,
		Status:      protocol.SubagentPending,
		Prompt:      spec.Prompt,
		Description: spec.Description,
		Scope:       scope,
		Model:       spec.Model,
		ToolCallID:  spec.ToolCallID,
		StartedAt:   now.UnixMilli(),
	}
	state := &runState{
		info:    info,
		result:  &RunResult{},
		started: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	m.mu.Lock()
	m.runs[id] = state
	m.mu.Unlock()

	m.storeMu.Lock()
	err := m.store.Insert(ctx, store.Subagent{
		ID:          id,
		ParentID:    m.parentID,
		Status:      string(protocol.SubagentPending),
		Prompt:      spec.Prompt,
		Description: spec.Description,
		ScopeJSON:   encodeScope(scope),
		Model:       spec.Model,
		ToolCallID:  spec.ToolCallID,
		StartedAt:   now,
	})
	m.storeMu.Unlock()
	if err != nil {
		m.mu.Lock()
		delete(m.runs, id)
		m.mu.Unlock()
		return nil, err
	}

	go m.runSpec(state, spec, scope)

	// Wait for the run goroutine to transition past pending (or finish
	// immediately) so async callers see a non-pending status on return.
	select {
	case <-state.started:
	case <-state.doneCh:
	}

	if !spec.RunInBackground {
		_, _ = m.Wait(id, time.Duration(spec.TimeoutMs)*time.Millisecond)
	}
	got, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	return &got, nil
}

// runSpec is the goroutine body. It transitions the state to either
// running or aborted (close started), then runs the runner.
func (m *Manager) runSpec(state *runState, spec protocol.SubagentSpec, scope []string) {
	defer close(state.doneCh)

	timeout := time.Duration(spec.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	m.mu.Lock()
	switch {
	case m.closed:
		state.info.Status = protocol.SubagentAborted
		state.info.ErrorMsg = "manager closed"
		state.info.EndedAt = time.Now().UnixMilli()
		m.mu.Unlock()
		close(state.started)
		return
	case state.aborted:
		state.info.Status = protocol.SubagentAborted
		state.info.EndedAt = time.Now().UnixMilli()
		m.mu.Unlock()
		close(state.started)
		return
	}
	state.info.Status = protocol.SubagentRunning
	runUpd := store.Subagent{
		ID: state.info.ID, ParentID: state.info.ParentID,
		Status: string(protocol.SubagentRunning),
	}
	m.mu.Unlock()
	m.storeMu.Lock()
	_ = m.store.Update(context.Background(), runUpd)
	m.storeMu.Unlock()

	runCtx, cancel := context.WithTimeout(context.Background(), timeout)
	m.mu.Lock()
	state.cancel = cancel
	m.mu.Unlock()

	// Signal Spawn that the goroutine has transitioned past pending.
	close(state.started)

	res, err := m.runner(runCtx, RunnerRequest{
		SubagentID:  state.info.ID,
		Prompt:      spec.Prompt,
		Description: spec.Description,
		Scope:       scope,
		Model:       spec.Model,
		ToolCallID:  spec.ToolCallID,
		ParentID:    m.parentID,
	})
	cancel()

	m.mu.Lock()
	now := time.Now()
	state.info.EndedAt = now.UnixMilli()
	state.info.DurationMs = now.UnixMilli() - state.info.StartedAt
	state.info.ToolCalls = res.ToolCalls
	state.result.Append([]byte(res.FinalText), m.bufCap)
	state.info.ResultText = res.FinalText
	state.info.ResultTruncated = state.result.Truncated()

	switch {
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		state.info.Status = protocol.SubagentTimeout
		state.info.ErrorMsg = "timeout"
	case err != nil && errors.Is(err, context.Canceled):
		state.info.Status = protocol.SubagentAborted
		state.info.ErrorMsg = "aborted"
	case err != nil:
		state.info.Status = protocol.SubagentError
		state.info.ErrorMsg = err.Error()
	default:
		state.info.Status = protocol.SubagentDone
	}
	waiters := state.waiters
	state.waiters = nil
	m.mu.Unlock()

	update := store.Subagent{
		ID:          state.info.ID,
		ParentID:    state.info.ParentID,
		Status:      string(state.info.Status),
		Prompt:      state.info.Prompt,
		Description: state.info.Description,
		ScopeJSON:   encodeScope(state.info.Scope),
		Model:       state.info.Model,
		ToolCallID:  state.info.ToolCallID,
		StartedAt:   time.UnixMilli(state.info.StartedAt),
		EndedAt:     time.UnixMilli(state.info.EndedAt),
		ResultText:  state.info.ResultText,
		ToolCalls:   state.info.ToolCalls,
		ErrorMsg:    state.info.ErrorMsg,
	}
	m.storeMu.Lock()
	_ = m.store.Update(context.Background(), update)
	m.storeMu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- state.info:
		default:
		}
	}
}

// List returns a snapshot of all runs (running + terminated).
func (m *Manager) List() []protocol.SubagentInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]protocol.SubagentInfo, 0, len(m.runs))
	for _, s := range m.runs {
		out = append(out, s.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

// Get fetches a single run by id.
func (m *Manager) Get(id string) (protocol.SubagentInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.runs[id]
	if !ok {
		return protocol.SubagentInfo{}, ErrUnknownID
	}
	return s.info, nil
}

// Abort cancels a running sub-agent.
func (m *Manager) Abort(id string) error {
	m.mu.Lock()
	s, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownID
	}
	s.aborted = true
	cancel := s.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// ReadResult returns a byte-offset window of the buffered final text.
func (m *Manager) ReadResult(id string, offset, limit int) (string, error) {
	m.mu.Lock()
	s, ok := m.runs[id]
	m.mu.Unlock()
	if !ok {
		return "", ErrUnknownID
	}
	text := s.result.Snapshot()
	return Paginate(text, offset, limit), nil
}

// Wait blocks until the run terminates or timeout elapses. Returns the
// info snapshot. A zero timeout blocks indefinitely.
func (m *Manager) Wait(id string, timeout time.Duration) (protocol.SubagentInfo, error) {
	m.mu.Lock()
	s, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return protocol.SubagentInfo{}, ErrUnknownID
	}
	ch := make(chan protocol.SubagentInfo, 1)
	if s.info.Status.IsTerminal() {
		m.mu.Unlock()
		return s.info, nil
	}
	s.waiters = append(s.waiters, ch)
	doneCh := s.doneCh
	m.mu.Unlock()

	if timeout <= 0 {
		select {
		case <-doneCh:
		case info := <-ch:
			return info, nil
		}
	} else {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-doneCh:
		case info := <-ch:
			return info, nil
		case <-timer.C:
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.runs[id]; ok {
		return s.info, nil
	}
	return protocol.SubagentInfo{}, ErrUnknownID
}

// Close terminates all running runs and releases resources. Idempotent.
// Waits for every runSpec goroutine to finish (including the final
// DB Update) so callers can rely on state.info being final and on
// no goroutine outliving Close.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	drained := make([]chan struct{}, 0, len(m.runs))
	for _, s := range m.runs {
		s.aborted = true
		if s.cancel != nil {
			s.cancel()
		}
		drained = append(drained, s.doneCh)
	}
	m.mu.Unlock()
	for _, ch := range drained {
		<-ch
	}
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// encodeScope serialises the scope list as JSON for the Subagent row.
func encodeScope(scope []string) string {
	if len(scope) == 0 {
		return "[]"
	}
	// Hand-rolled JSON to avoid encoding/json import for trivial case.
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range scope {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(s, `"`, `\"`))
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}
