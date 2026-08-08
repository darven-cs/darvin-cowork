// Package gateway: per-session state index for the gateway.
// SessionManager holds an in-memory session id → *SessionEntry map;
// persistence lives in agent/store and this package only carries the
// "which sessions are currently active" view. Each entry lazily
// builds AgentLoopSession on the first prompt; the subscribe path
// only builds the SessionEntry so subscribing to historical sessions
// from the renderer does not spin up 5000 Agents.
package gateway

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/agentloop"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/agents/session"
)

const (
	sessionIDLen    = 21
	sessionAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// DefaultSessionID is the special id kept for compatibility /
	// migration. The renderer may still send prompt / subscribe with
	// this id; GetOrCreateEntry takes the unknown-id branch and the
	// code path is identical to any other new id.
	DefaultSessionID = "default"

	// DefaultMaxSessions is the soft cap for SessionManager. When
	// exceeded we first reap / evict idle entries; only when every
	// entry is an active run do we return ErrSessionsLimit.
	DefaultMaxSessions = 5000

	// DefaultIdleTTL is the maximum lifetime of an idle entry in
	// memory.
	DefaultIdleTTL = 24 * time.Hour

	// DefaultStopWindow is the refusal window after Stop: prompts
	// for the same session inside the window return ErrSessionStalled.
	DefaultStopWindow = 1000 * time.Millisecond
)

// SessionEntry is the per-session state held by SessionManager. Its
// fields are protected by SessionManager's mutex; handlers do not
// write them directly.
//
// AgentLoop is lazily built on the first prompt; an entry without
// AgentLoop can only be subscribed to, not submitted to / stopped.
type SessionEntry struct {
	Session   *session.Session
	AgentLoop *agentloop.AgentLoopSession

	lastTouchedMs  int64
	stoppedUntilMs int64

	// cancel triggers the background goroutine watching ctx to call
	// AgentLoopSession.Loop.Close — see attachAgentLoopLocked. The
	// evict path uses it.
	cancel context.CancelFunc

	idleElem *list.Element
}

var (
	// ErrSessionsLimit is returned by GetOrCreateEntry when the cap
	// is full and no idle entry can be evicted. We do not interrupt
	// an active run to make room.
	ErrSessionsLimit = errors.New("sessionmgr: sessions limit reached")

	// ErrSessionNotFound is returned by Stop for an unknown sessionID.
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrRunMismatch is returned by Stop when runId does not match the
	// current active run, or when the session has not built an
	// AgentLoopSession yet. Stop is a no-op in both cases.
	ErrRunMismatch = errors.New("sessionmgr: run id mismatch")

	// ErrSessionStalled is returned when a prompt lands inside the
	// refusal window after Stop.
	ErrSessionStalled = errors.New("sessionmgr: session stalled, retry after stop window")
)

// SessionManager is the process-local session id → *SessionEntry index.
type SessionManager struct {
	mu sync.Mutex

	byID      map[string]*SessionEntry
	idleOrder *list.List

	maxSessions int
	idleTtl     time.Duration
	stopWindow  time.Duration

	nowMs func() int64

	idGen func() string

	// factory lazily builds AgentLoopSession on the unknown-id branch
	// of GetOrCreateEntry. nil disables the lazy build (handler tests
	// / legacy main take the "session only" path).
	factory *agentloop.AgentFactory

	// ledger attaches the AgentLoopSession event-bus subscription on
	// the lazy build path.
	ledger *EventLedger
}

// SessionManagerOption configures NewSessionManager.
type SessionManagerOption func(*SessionManager)

// WithAgentFactory enables the lazy build path: once factory is wired,
// GetOrCreateEntry calls factory.NewAgentLoopSession(id) on unknown ids.
func WithAgentFactory(f *agentloop.AgentFactory) SessionManagerOption {
	return func(m *SessionManager) { m.factory = f }
}

// WithEventLedger attaches the newly-built AgentLoopSession's event
// subscription to the WS event ledger so its events fan out to
// clients subscribed to that session id.
func WithEventLedger(l *EventLedger) SessionManagerOption {
	return func(m *SessionManager) { m.ledger = l }
}

// NewSessionManager constructs an empty manager without pre-seeding the
// default session. The renderer can still send prompt with
// DefaultSessionID; GetOrCreateEntry takes the unknown-id path.
func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	m := &SessionManager{
		byID:        make(map[string]*SessionEntry),
		idleOrder:   list.New(),
		maxSessions: DefaultMaxSessions,
		idleTtl:     DefaultIdleTTL,
		stopWindow:  DefaultStopWindow,
		nowMs:       func() int64 { return time.Now().UnixMilli() },
		idGen:       nanoid.MustCustomASCII(sessionAlphabet, sessionIDLen),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// DefaultID returns DefaultSessionID.
func (m *SessionManager) DefaultID() string { return DefaultSessionID }

// MintSessionID returns a fresh session id (21-char nanoid, same
// generator as the other ids). agent.create_session uses it.
func (m *SessionManager) MintSessionID() string { return m.idGen() }

// Remove detaches a session from SessionManager: when an
// AgentLoopSession is present we first Abort the in-flight run, then
// cancel to trigger Close (DeltaHook + Loop), and finally delete the
// byID / LRU entries. Unlike evictLocked, Remove does not skip active
// runs — the delete semantics are precisely to force-end. Unknown id
// returns ErrSessionNotFound.
func (m *SessionManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return ErrSessionNotFound
	}
	if e.AgentLoop != nil {
		e.AgentLoop.Loop.Abort(context.Background())
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	delete(m.byID, id)
	if e.idleElem != nil {
		m.idleOrder.Remove(e.idleElem)
		e.idleElem = nil
	}
	return nil
}

// Has reports whether id has been seen by SessionManager. The
// subscribe handler uses it to fail fast on unknown ids before
// touching the ledger.
func (m *SessionManager) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.byID[id]
	return ok
}

// RefreshAllTools re-runs the factory plugin step
// (Unregister + Register) for every agent with an already-built
// AgentLoopSession, so the tool surface tracks skill / mcp state
// changes. Silently skips when factory is nil or a plugin step fails.
// Returns the count of sessions refreshed.
func (m *SessionManager) RefreshAllTools() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.factory == nil {
		return 0
	}
	var n int
	for _, e := range m.byID {
		if e.AgentLoop == nil {
			continue
		}
		reg := e.AgentLoop.Agent.Tools()
		tr, ok := reg.(protocol.ToolRegistrar)
		if !ok {
			continue
		}
		for _, p := range m.factory.Plugins {
			_ = p.Unregister(tr)
			if err := p.Register(tr); err != nil {
				continue
			}
		}
		n++
	}
	return n
}

// GetOrCreateEntry returns the SessionEntry for id, creating it (and
// lazily building AgentLoopSession when factory is wired) for unknown
// ids.
//
// On a hit we first check stoppedUntilMs (return ErrSessionStalled
// when inside the window), then refresh lastTouchedMs and bump the
// entry to the head of the LRU. When subscribe has pre-created an
// empty AgentLoop entry ahead of prompt, the first prompt triggers the
// lazy AgentLoopSession build. On lazy-build failure we roll back
// byID + LRU so a half-built entry cannot stall the next retry.
// When the cap is full and every entry is an active run we return
// ErrSessionsLimit — we never interrupt an active run.
func (m *SessionManager) GetOrCreateEntry(id string) (*SessionEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.byID[id]; ok {
		if e.stoppedUntilMs > m.nowMs() {
			return nil, ErrSessionStalled
		}
		e.lastTouchedMs = m.nowMs()
		m.touchLRU(e)
		if e.AgentLoop == nil && m.factory != nil {
			if err := m.attachAgentLoopLocked(e); err != nil {
				return nil, err
			}
		}
		return e, nil
	}

	e, err := m.createEntryLocked(id)
	if err != nil {
		return nil, err
	}
	if m.factory != nil {
		if err := m.attachAgentLoopLocked(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// EnsureEntry returns the SessionEntry for id; for unknown ids it
// only creates the SessionEntry without triggering the lazy
// AgentLoopSession build. The subscribe handler uses it so subscribing
// to historical sessions from the renderer does not spin up 5000
// Agents / Loops / subscriptions.
//
// stoppedUntilMs / LRU / maxSessions behaviour is identical to
// GetOrCreateEntry.
func (m *SessionManager) EnsureEntry(id string) (*SessionEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.byID[id]; ok {
		if e.stoppedUntilMs > m.nowMs() {
			return nil, ErrSessionStalled
		}
		e.lastTouchedMs = m.nowMs()
		m.touchLRU(e)
		return e, nil
	}
	return m.createEntryLocked(id)
}

// createEntryLocked attaches a fresh empty SessionEntry on byID + LRU.
// When over maxSessions we first reap TTL and then evict an idle LRU
// entry; only when every entry is active do we return ErrSessionsLimit.
// Caller holds m.mu.
func (m *SessionManager) createEntryLocked(id string) (*SessionEntry, error) {
	if len(m.byID) >= m.maxSessions {
		m.reapIdleLocked()
		if len(m.byID) >= m.maxSessions {
			if !m.evictLRULocked() {
				return nil, ErrSessionsLimit
			}
			if len(m.byID) >= m.maxSessions {
				return nil, ErrSessionsLimit
			}
		}
	}
	now := m.nowMs()
	e := &SessionEntry{
		Session:       session.NewSession(id),
		lastTouchedMs: now,
	}
	m.byID[id] = e
	e.idleElem = m.idleOrder.PushFront(id)
	return e, nil
}

// attachAgentLoopLocked calls factory.NewAgentLoopSession and attaches
// the result to e; when ledger is wired, the event subscription is
// attached in the same step. On failure we roll back byID + LRU.
// Caller holds m.mu.
func (m *SessionManager) attachAgentLoopLocked(e *SessionEntry) error {
	agentLoopSess, err := m.factory.NewAgentLoopSession(e.Session.ID)
	if err != nil {
		delete(m.byID, e.Session.ID)
		if e.idleElem != nil {
			m.idleOrder.Remove(e.idleElem)
			e.idleElem = nil
		}
		return err
	}
	e.AgentLoop = agentLoopSess
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	// Close will close the DeltaHook subscription + Loop (blocking until
	// the run goroutine exits). Run it in the background so evict is
	// not held up.
	go func() {
		<-ctx.Done()
		agentLoopSess.Close()
	}()
	if m.ledger != nil {
		sub := agentLoopSess.Agent.Subscribe(64)
		m.ledger.AttachSubscription(sub)
	}
	return nil
}

// CreateOrGet preserves the legacy (*session.Session, msgID) API.
// Empty id is treated as DefaultSessionID. Once handlers move over
// to GetOrCreateEntry this method can be deleted.
func (m *SessionManager) CreateOrGet(id string) (*session.Session, string, error) {
	if id == "" {
		id = DefaultSessionID
	}
	e, err := m.GetOrCreateEntry(id)
	if err != nil {
		return nil, "", err
	}
	return e.Session, m.idGen(), nil
}

// Get is the err-less twin of CreateOrGet.
func (m *SessionManager) Get(id string) (*session.Session, string) {
	sess, msgID, _ := m.CreateOrGet(id)
	return sess, msgID
}

// Stop aborts the turn matching (sessionID, runId).
//
//   - session unknown: ErrSessionNotFound
//   - entry has no AgentLoopSession (subscribe preceded prompt): ErrRunMismatch
//   - entry's Loop.Stop(runId) reports false (runId mismatch or currently idle): ErrRunMismatch
//   - success: Loop.Stop internally cancels the in-flight turn, this
//     method pushes stoppedUntilMs to now()+StopWindow, and
//     GetOrCreateEntry returns ErrSessionStalled inside that window.
//     推到 now()+StopWindow,GetOrCreateEntry 在该窗口内会返 ErrSessionStalled
func (m *SessionManager) Stop(sessionID, runId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.byID[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if e.AgentLoop == nil {
		return ErrRunMismatch
	}
	if !e.AgentLoop.Loop.Stop(runId) {
		return ErrRunMismatch
	}
	e.stoppedUntilMs = m.nowMs() + m.stopWindow.Milliseconds()
	return nil
}

// reapIdleSessions is the timer-driven entry point. It evicts (from
// byID + LRU) entries whose TTL has expired and that have no active
// run; entries inside the Stop window or still running are left
// alone.
func (m *SessionManager) reapIdleSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapIdleLocked()
}

// Len returns the current entry count. For tests.
func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

// reapIdleLocked walks the LRU from the tail, dropping entries whose
// TTL has expired and that have no active run. It stops on the first
// active or unexpired entry — the "demarcation" on the LRU means
// younger entries sit behind it and can wait for the next pass.
// Caller holds m.mu.
func (m *SessionManager) reapIdleLocked() {
	cutoff := m.nowMs() - m.idleTtl.Milliseconds()
	for {
		elem := m.idleOrder.Back()
		if elem == nil {
			return
		}
		id := elem.Value.(string)
		e := m.byID[id]
		if e.AgentLoop != nil && e.AgentLoop.Loop.ActiveRunID() != "" {
			return
		}
		if e.lastTouchedMs > cutoff {
			return
		}
		m.evictLocked(id)
	}
}

// evictLRULocked scans the LRU tail for an entry without an active
// run to evict. When every entry is active it returns false; callers
// interpret this as "cannot shrink right now" and bubble the
// responsibility back up.
func (m *SessionManager) evictLRULocked() bool {
	for elem := m.idleOrder.Back(); elem != nil; elem = elem.Prev() {
		id := elem.Value.(string)
		e := m.byID[id]
		if e.AgentLoop == nil || e.AgentLoop.Loop.ActiveRunID() == "" {
			m.evictLocked(id)
			return true
		}
	}
	return false
}

// evictLocked removes id from byID + LRU. When the entry has an
// active run it is a no-op — this is a defensive backstop; callers
// should check the active-run state first.
func (m *SessionManager) evictLocked(id string) {
	e, ok := m.byID[id]
	if !ok {
		return
	}
	if e.AgentLoop != nil && e.AgentLoop.Loop.ActiveRunID() != "" {
		return
	}
	if e.AgentLoop != nil {
		e.AgentLoop.Loop.Abort(context.Background())
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	delete(m.byID, id)
	if e.idleElem != nil {
		m.idleOrder.Remove(e.idleElem)
		e.idleElem = nil
	}
}

// touchLRU moves e to the head of the LRU. Caller holds m.mu.
func (m *SessionManager) touchLRU(e *SessionEntry) {
	if e.idleElem != nil {
		m.idleOrder.MoveToFront(e.idleElem)
	}
}
