// Package todos holds the host-side current task list per session.
//
// The list is written whenever a todo_write tool call succeeds (the model
// re-sends the complete list every call, so the arguments are the
// authoritative state) and read per turn to inject an <active-todos> block
// into the LLM request. Keeping it separate from the conversation history
// makes it immune to context compaction; session hydration re-seeds it from
// the full persisted history.
package todos

import (
	"strings"
	"sync"
)

// WriteToolName is the tool whose arguments feed this store.
const WriteToolName = "todo_write"

// Item mirrors the todo_write item shape the host needs for rendering.
type Item struct {
	Content    string
	Status     string // pending | in_progress | completed
	ActiveForm string
	Level      int // 0 = phase, 1 = sub-step
}

// Store holds the current task list per session.
type Store struct {
	mu        sync.RWMutex
	bySession map[string][]Item
}

var global = &Store{bySession: make(map[string][]Item)}

// Set replaces the current list for sessionID; an empty list clears the entry.
func Set(sessionID string, items []Item) { global.set(sessionID, items) }

// Clear removes the list for sessionID.
func Clear(sessionID string) { global.clear(sessionID) }

// Get returns the current list for sessionID.
func Get(sessionID string) ([]Item, bool) { return global.get(sessionID) }

// Block renders the <active-todos> block for sessionID, or "" when the
// session has no (or an empty) task list.
func Block(sessionID string) string { return global.block(sessionID) }

// ParseArgs extracts the task list from a todo_write call's arguments
// (map[string]any {"todos":[...]}). ok is false when the payload isn't a
// valid list (todos absent or not an array), so callers can distinguish
// "no list" from "empty list" — the latter clears the store.
func ParseArgs(args map[string]any) (items []Item, ok bool) {
	raw, has := args["todos"]
	if !has {
		return nil, false
	}
	list, isArray := raw.([]any)
	if !isArray {
		return nil, false
	}
	items = make([]Item, 0, len(list))
	for _, r := range list {
		m, isMap := r.(map[string]any)
		if !isMap {
			continue
		}
		items = append(items, Item{
			Content:    stringVal(m["content"]),
			Status:     stringVal(m["status"]),
			ActiveForm: stringVal(m["activeForm"]),
			Level:      intVal(m["level"]),
		})
	}
	return items, true
}

func (s *Store) set(sessionID string, items []Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(items) == 0 {
		delete(s.bySession, sessionID)
		return
	}
	cp := make([]Item, len(items))
	copy(cp, items)
	s.bySession[sessionID] = cp
}

func (s *Store) clear(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bySession, sessionID)
}

func (s *Store) get(sessionID string) ([]Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items, ok := s.bySession[sessionID]
	return items, ok
}

func (s *Store) block(sessionID string) string {
	s.mu.RLock()
	items, ok := s.bySession[sessionID]
	s.mu.RUnlock()
	if !ok || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<active-todos>\n")
	b.WriteString("Host-tracked task list - the authoritative current state of your todo_write plan.\n")
	b.WriteString("Re-send the COMPLETE list via todo_write whenever progress changes; keep at most one item in_progress.\n")
	for _, it := range items {
		indent := ""
		if it.Level == 1 {
			indent = "  "
		}
		b.WriteString(indent + "- [" + it.Status + "] " + it.Content + "\n")
	}
	b.WriteString("</active-todos>")
	return b.String()
}

func stringVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
