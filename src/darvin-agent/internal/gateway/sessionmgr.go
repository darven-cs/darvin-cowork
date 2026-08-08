// Package gateway: per-session state index. SessionManager holds an
// in-memory session id → *SessionEntry map; persistence lives in
// agent/store and this package only tracks "which sessions are active".
// Entries lazily build AgentLoopSession on the first prompt; the
// subscribe path builds only the SessionEntry so subscribing to
// historical sessions does not spin up an Agent per session.
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

	// DefaultSessionID is the compatibility / migration id.
	DefaultSessionID = "default"

	// DefaultMaxSessions is the soft cap; we reap / evict idle entries
	// first and return ErrSessionsLimit only when every entry is active.
	DefaultMaxSessions = 5000

	// DefaultIdleTTL is the max lifetime of an idle in-memory entry.
	DefaultIdleTTL = 24 * time.Hour

	// DefaultStopWindow is the refusal window after Stop (prompts inside
	// it return ErrSessionStalled).
	DefaultStopWindow = 1000 * time.Millisecond
)

// SessionEntry is the per-session state held by SessionManager; fields
// are mutex-protected, handlers do not write them directly. AgentLoop
// is lazily built on the first prompt; an entry without it can only be
// subscribed to, not submitted to / stopped.
type SessionEntry struct {
	Session   *session.Session
	AgentLoop *agentloop.AgentLoopSession

	lastTouchedMs  int64
	stoppedUntilMs int64

	// cancel triggers the background goroutine that calls
	// AgentLoopSession.Loop.Close (see attachAgentLoopLocked).
	cancel context.CancelFunc

	idleElem *list.Element
}

var (
	// ErrSessionsLimit: cap full and no idle entry evictable — we do not
	// interrupt an active run to make room.
	ErrSessionsLimit = errors.New("sessionmgr: sessions limit reached")

	// ErrSessionNotFound: Stop for an unknown sessionID.
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrRunMismatch: Stop's runID does not match the active run, or the
	// session has no AgentLoopSession yet. Stop is a no-op in both cases.
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
	idleTTL     time.Duration
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

// NewSessionManager constructs an empty manager (no pre-seeded default
// session); prompt with DefaultSessionID takes the unknown-id path.
func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	m := &SessionManager{
		byID:        make(map[string]*SessionEntry),
		idleOrder:   list.New(),
		maxSessions: DefaultMaxSessions,
		idleTTL:     DefaultIdleTTL,
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

// MintSessionID returns a fresh 21-char nanoid (agent.create_session).
func (m *SessionManager) MintSessionID() string { return m.idGen() }

// Remove detaches a session: Abort the in-flight run, cancel to trigger
// Close (DeltaHook + Loop), then delete byID / LRU. Unlike evictLocked,
// Remove force-ends active runs (delete semantics). Unknown id →
// ErrSessionNotFound.
func (m *SessionManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return ErrSessionNotFound
	}
	if e.AgentLoop != nil {
		_ = e.AgentLoop.Loop.Abort(context.Background())
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

// Has reports whether id has been seen; the subscribe handler fails
// fast on unknown ids before touching the ledger.
func (m *SessionManager) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.byID[id]
	return ok
}

// RefreshAllTools re-runs the factory plugin step (Unregister + Register)
// for every agent with an already-built AgentLoopSession so the tool
// surface tracks skill / mcp changes. Returns the count refreshed.
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
// ids. On a hit it checks stoppedUntilMs (ErrSessionStalled), refreshes
// lastTouchedMs, bumps the LRU head, and lazily builds AgentLoop if
// subscribe pre-created an empty entry. Lazy-build failure rolls back
// byID + LRU so a half-built entry cannot stall the next retry; a full
// cap of active runs returns ErrSessionsLimit (never interrupts a run).
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

// EnsureEntry returns the SessionEntry for id, creating it WITHOUT the
// lazy AgentLoopSession build (the subscribe handler uses it so
// subscribing to historical sessions does not spin up an Agent per
// session). stoppedUntilMs / LRU / maxSessions match GetOrCreateEntry.
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
// the result (plus the ledger subscription); on failure it rolls back
// byID + LRU. Caller holds m.mu.
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
	// Close blocks until the run goroutine exits; run in background so
	// evict is not held up.
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

// Get is CreateOrGet without the error.
func (m *SessionManager) Get(id string) (*session.Session, string) {
	sess, msgID, _ := m.CreateOrGet(id)
	return sess, msgID
}

// Stop aborts the turn matching (sessionID, runID).
//
//   - session unknown → ErrSessionNotFound
//   - no AgentLoopSession / Loop.Stop false → ErrRunMismatch
//   - success: cancels the in-flight turn and pushes stoppedUntilMs to
//     now()+StopWindow (prompts inside the window get ErrSessionStalled).
func (m *SessionManager) Stop(sessionID, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.byID[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if e.AgentLoop == nil {
		return ErrRunMismatch
	}
	if !e.AgentLoop.Loop.Stop(runID) {
		return ErrRunMismatch
	}
	e.stoppedUntilMs = m.nowMs() + m.stopWindow.Milliseconds()
	return nil
}

// reapIdleSessions is the timer entry point; it evicts TTL-expired
// entries without an active run, leaving running / Stop-window entries.
func (m *SessionManager) reapIdleSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapIdleLocked()
}

// Len returns the current entry count (tests).
func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

// reapIdleLocked drops TTL-expired entries without an active run,
// stopping at the first active / unexpired one (younger LRU entries
// wait for the next pass). Caller holds m.mu.
func (m *SessionManager) reapIdleLocked() {
	cutoff := m.nowMs() - m.idleTTL.Milliseconds()
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

// evictLRULocked scans the LRU tail for an entry without an active run
// to evict; false when every entry is active (caller bubbles the cap).
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

// evictLocked removes id from byID + LRU; an active run is a no-op.
func (m *SessionManager) evictLocked(id string) {
	e, ok := m.byID[id]
	if !ok {
		return
	}
	if e.AgentLoop != nil && e.AgentLoop.Loop.ActiveRunID() != "" {
		return
	}
	if e.AgentLoop != nil {
		_ = e.AgentLoop.Loop.Abort(context.Background())
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

// touchLRU moves e to the LRU head. Caller holds m.mu.
func (m *SessionManager) touchLRU(e *SessionEntry) {
	if e.idleElem != nil {
		m.idleOrder.MoveToFront(e.idleElem)
	}
}
