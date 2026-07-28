package tool

import (
	"errors"
	"sort"
	"sync"

	"darvin-cowork/backend/internal/agent/llm"
)

// ErrAlreadyRegistered is returned by Register when a tool with the same
// name is already present.
var ErrAlreadyRegistered = errors.New("tool: already registered")

// Registry holds the active set of tools. Goroutine-safe.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds t. Returns ErrAlreadyRegistered if a tool with the same
// name already exists.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("tool: nil tool")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name()]; ok {
		return ErrAlreadyRegistered
	}
	r.tools[t.Name()] = t
	return nil
}

// MustRegister is Register that panics on error. Intended for init-time
// wiring of the built-in tool set.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get returns the tool registered under name, or nil.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Names returns the registered tool names sorted alphabetically.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Specs returns the LLM-visible tool descriptions in deterministic order
// (by name). Used to populate CompletionRequest.Tools.
func (r *Registry) Specs() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]llm.Tool, 0, len(names))
	for _, n := range names {
		t := r.tools[n]
		out = append(out, llm.Tool{
			Type:        "function",
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return out
}
