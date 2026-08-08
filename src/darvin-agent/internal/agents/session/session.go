// Package session holds the in-memory conversation history for one Agent
// run. The SessionStore interface in the store package defines how this
// gets persisted (in-memory today; SQLite-backed implementation is in store/).
package session

import (
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/protocol"
)

// Session is a single conversation history. All exported methods are safe
// for concurrent use.
type Session struct {
	ID        string
	Key       string
	AgentID   string
	Status    Status
	CreatedAt time.Time

	mu        sync.RWMutex
	updatedAt time.Time
	messages  []protocol.Message
}

// Status describes the lifecycle state of a session.
type Status string

const (
	// StatusActive is the default state for a freshly created session.
	StatusActive Status = "active"
	// StatusArchived marks a session whose conversation history has been
	// preserved but is no longer expected to receive new turns.
	StatusArchived Status = "archived"
	// StatusSuspended marks a session temporarily parked (e.g. user paused
	// a long-running conversation and intends to resume later).
	StatusSuspended Status = "suspended"
)

// NewSession constructs a fresh session with the given id. CreatedAt /
// UpdatedAt default to time.Now(); Status defaults to StatusActive; Key
// and AgentID default to empty strings.
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		Status:    StatusActive,
		CreatedAt: now,
		updatedAt: now,
	}
}

// Append adds m to the history and refreshes UpdatedAt.
func (s *Session) Append(m protocol.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	s.updatedAt = time.Now()
}

// Messages returns a deep copy of the current message history.
func (s *Session) Messages() []protocol.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]protocol.Message, len(s.messages))
	for i, m := range s.messages {
		out[i] = cloneMessage(m)
	}
	return out
}

// Len returns the current message count. Useful for tests.
func (s *Session) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// ReplaceAll overwrites the history wholesale. Intended for use by
// SessionStore.Load to restore a persisted session.
func (s *Session) ReplaceAll(messages []protocol.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := make([]protocol.Message, len(messages))
	for i, m := range messages {
		cloned[i] = cloneMessage(m)
	}
	s.messages = cloned
	s.updatedAt = time.Now()
}

// Meta returns a snapshot of session metadata without copying messages.
func (s *Session) Meta() SessionMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SessionMeta{
		ID:           s.ID,
		Key:          s.Key,
		AgentID:      s.AgentID,
		Status:       s.Status,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.updatedAt,
		MessageCount: len(s.messages),
	}
}

// UpdatedAt returns the current UpdatedAt timestamp under the read lock.
func (s *Session) UpdatedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

// ReplaceAllMeta overwrites the persistable metadata fields in one shot.
// SessionStore implementations call this from Load to restore a row read
// from storage. Messages are intentionally not handled here — see
// SessionStore.ReplaceAll for that path.
func (s *Session) ReplaceAllMeta(
	key, agentID string,
	status Status,
	createdAt, updatedAt time.Time,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Key = key
	s.AgentID = agentID
	s.Status = status
	s.CreatedAt = createdAt
	if updatedAt.After(s.updatedAt) {
		s.updatedAt = updatedAt
	}
}

func cloneMessage(m protocol.Message) protocol.Message {
	out := protocol.Message{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ID:         m.ID,
		Timestamp:  m.Timestamp,
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]protocol.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			out.ToolCalls[i] = cloneToolCall(tc)
		}
	}
	return out
}

func cloneToolCall(tc protocol.ToolCall) protocol.ToolCall {
	out := protocol.ToolCall{
		ID:   tc.ID,
		Name: tc.Name,
	}
	if tc.Arguments != nil {
		out.Arguments = make(map[string]any, len(tc.Arguments))
		for k, v := range tc.Arguments {
			out.Arguments[k] = v
		}
	}
	if tc.Result != nil {
		r := *tc.Result
		out.Result = &r
	}
	return out
}

// SessionMeta is a serialisable summary of a Session.
type SessionMeta struct {
	ID           string
	Key          string
	AgentID      string
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
}
