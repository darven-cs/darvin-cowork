// Implements the tool registry: registration, lookup, and sandbox re-anchoring.

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

// SetWorkspaceRoot re-anchors the shared file/shell sandbox to a new root.
// The registry holds the sandbox reference from NewBuiltins, so updating it
// re-anchors every built-in file tool at once. Also clears the in-memory
// code_index cache, whose absolute paths would otherwise point at the old
// root. No-op when the registry has no sandbox (hand-assembled registries
// without NewBuiltins).
func (r *Registry) SetWorkspaceRoot(newRoot string) error {
	r.mu.RLock()
	sb := r.sb
	r.mu.RUnlock()
	if sb == nil {
		return nil
	}
	if err := sb.SetRoot(newRoot); err != nil {
		return err
	}
	clearCodeIndex()
	return nil
}

// Registry holds the active set of tools. Goroutine-safe.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	// sb is the workspace sandbox shared by the built-in file/shell tools.
	// Set by NewBuiltins; nil for hand-assembled registries.
	sb *Sandbox
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

// ScopedForSkill returns a registry containing only the named tools,
// preserving each entry's kind/metadata so the executor's event attribution
// (toolKind / skillId / mcpServerId) stays intact. An empty allow set yields
// an empty registry — the skill is not allowed to call tools, so the LLM
// answers from the skill prompt alone.
func (r *Registry) ScopedForSkill(allow []string) ToolRegistry {
	names := make(map[string]struct{}, len(allow))
	for _, n := range allow {
		if n != "" {
			names[n] = struct{}{}
		}
	}
	reg := NewRegistry()
	for _, e := range r.List() {
		if _, ok := names[e.Tool.Name()]; ok {
			// RegisterTool on an empty registry never collides; ignore the
			// returned ErrAlreadyRegistered defensively.
			_ = reg.RegisterTool(e.Tool, e.Kind, e.Metadata)
		}
	}
	return reg
}

// BuiltinConfig carries the runtime wiring every built-in tool needs:
// the shared workspace sandbox and the shell command allowlist.
type BuiltinConfig struct {
	Sandbox   *Sandbox
	Allowlist []string
}

// BuiltinFactory constructs one built-in tool from cfg.
type BuiltinFactory func(cfg BuiltinConfig) (Tool, error)

var (
	builtinFactories = map[string]BuiltinFactory{}
	builtinMu        sync.RWMutex
)

// RegisterBuiltinFactory registers a named built-in tool constructor.
// Panics on empty name / nil factory / duplicate name, mirroring
// llm.RegisterProvider.
func RegisterBuiltinFactory(name string, factory BuiltinFactory) {
	if name == "" || factory == nil {
		panic("tool: invalid builtin factory")
	}
	builtinMu.Lock()
	defer builtinMu.Unlock()
	if _, dup := builtinFactories[name]; dup {
		panic("tool: builtin factory already registered: " + name)
	}
	builtinFactories[name] = factory
}

// RegisteredBuiltinFactories returns the registered built-in names sorted.
func RegisteredBuiltinFactories() []string {
	builtinMu.RLock()
	defer builtinMu.RUnlock()
	out := make([]string, 0, len(builtinFactories))
	for n := range builtinFactories {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
