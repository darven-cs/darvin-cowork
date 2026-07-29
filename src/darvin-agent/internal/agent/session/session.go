// Package session holds the in-memory conversation history for one Agent
// run. The SessionStore interface in the store package defines how this
// gets persisted (in-memory today; SQLite-backed implementation is in store/).
package session

import (
	"sync"
	"time"

	"darvin-cowork/backend/internal/agent/llm"
)

// Session is a single conversation history. All exported methods are safe
// for concurrent use.
type Session struct {
	ID        string
	CreatedAt time.Time

	mu        sync.RWMutex
	updatedAt time.Time
	messages  []llm.Message
}

// NewSession constructs a fresh session with the given id. CreatedAt /
// UpdatedAt default to time.Now().
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		CreatedAt: now,
		updatedAt: now,
	}
}

// Append adds m to the history and refreshes UpdatedAt.
func (s *Session) Append(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	s.updatedAt = time.Now()
}

// Messages returns a deep copy of the current message history.
func (s *Session) Messages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]llm.Message, len(s.messages))
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
func (s *Session) ReplaceAll(messages []llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := make([]llm.Message, len(messages))
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

// ReplaceAllMeta overwrites the timestamps in one shot. Intended for
// SessionStore implementations restoring a persisted session.
func (s *Session) ReplaceAllMeta(createdAt, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CreatedAt = createdAt
	if updatedAt.After(s.updatedAt) {
		s.updatedAt = updatedAt
	}
}

func cloneMessage(m llm.Message) llm.Message {
	out := llm.Message{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			out.ToolCalls[i] = cloneToolCall(tc)
		}
	}
	return out
}

func cloneToolCall(tc llm.ToolCall) llm.ToolCall {
	out := llm.ToolCall{
		ID:   tc.ID,
		Name: tc.Name,
	}
	if tc.Arguments != nil {
		out.Arguments = make(map[string]any, len(tc.Arguments))
		for k, v := range tc.Arguments {
			out.Arguments[k] = v
		}
	}
	return out
}

// SessionMeta is a serialisable summary of a Session.
type SessionMeta struct {
	ID           string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
}
