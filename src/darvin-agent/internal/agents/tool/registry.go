package tool

import (
	"errors"
	"sort"
	"sync"

	"darvin-cowork/backend/internal/llm"
)

// ErrAlreadyRegistered is returned by Register when a tool with the same
// name is already present.
var ErrAlreadyRegistered = errors.New("tool: already registered")

// Registry holds the active set of tools. Goroutine-safe.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	// sb is the workspace sandbox shared by the built-in file/shell tools.
	// Set by NewBuiltins; nil for hand-assembled registries.
	sb *fsSandbox
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]*Entry{}}
}

// Register adds t as a built-in tool. Returns ErrAlreadyRegistered if a
// tool with the same name already exists.
func (r *Registry) Register(t Tool) error {
	return r.RegisterTool(t, KindBuiltIn, nil)
}

// MustRegister is Register that panics on error. Intended for init-time
// wiring of the built-in tool set.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// RegisterTool adds t under its name with the given kind and metadata.
// meta may carry "pluginID" to mark ownership for UnregisterByPlugin.
// Returns ErrAlreadyRegistered if the name is taken.
func (r *Registry) RegisterTool(t Tool, kind Kind, meta map[string]any) error {
	if t == nil {
		return errors.New("tool: nil tool")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, ok := r.entries[name]; ok {
		return ErrAlreadyRegistered
	}
	pluginID, _ := meta["pluginID"].(string)
	r.entries[name] = &Entry{Tool: t, Kind: kind, Metadata: meta, PluginID: pluginID}
	return nil
}

// Unregister removes the tool registered under name. Idempotent — no-op
// when the name is absent.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, name)
	return nil
}

// UnregisterTool is the ToolRegistrar form of Unregister.
func (r *Registry) UnregisterTool(name string) error {
	return r.Unregister(name)
}

// UnregisterByPlugin removes every entry registered by the named plugin.
func (r *Registry) UnregisterByPlugin(pluginID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, e := range r.entries {
		if e.PluginID == pluginID {
			delete(r.entries, name)
		}
	}
	return nil
}

// Get returns the tool registered under name, or nil.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e := r.entries[name]; e != nil {
		return e.Tool
	}
	return nil
}

// GetEntry returns the full registration for name, or ok=false.
func (r *Registry) GetEntry(name string) (*Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e := r.entries[name]
	return e, e != nil
}

// Names returns the registered tool names sorted alphabetically.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for n := range r.entries {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SetGrantedReads replaces the run's granted-read set (absolute paths the
// user attached for the current message). Only meaningful for registries
// built by NewBuiltins; a no-op otherwise.
func (r *Registry) SetGrantedReads(paths []string) {
	if r.sb == nil {
		return
	}
	r.sb.setGrantedReads(paths)
}

// ApprovePath records a path the user allowed one-shot via the permission
// modal; the sandbox then lets it through outside the workspace root.
func (r *Registry) ApprovePath(p string) {
	if r.sb == nil {
		return
	}
	r.sb.approvePath(p)
}

// Specs returns the LLM-visible tool descriptions in deterministic order
// (by name). Used to populate CompletionRequest.Tools.
func (r *Registry) Specs() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]llm.Tool, 0, len(names))
	for _, n := range names {
		e := r.entries[n]
		out = append(out, llm.Tool{
			Type:        "function",
			Name:        e.Tool.Name(),
			Description: e.Tool.Description(),
			Parameters:  e.Tool.Parameters(),
		})
	}
	return out
}

// ToolsForSkill returns the tools a skill is allowed to invoke. Every
// active tool is returned so the runner can present the LLM with the full
// surface when the skill is enabled; per-skill scoping is not applied.
func (r *Registry) ToolsForSkill(skillID string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// List returns all entries. Order is unspecified.
func (r *Registry) List() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	return out
}

// ListByKind returns the entries of one kind, sorted by tool name.
func (r *Registry) ListByKind(kind Kind) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool.Name() < out[j].Tool.Name() })
	return out
}
