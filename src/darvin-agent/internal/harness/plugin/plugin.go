// Package plugin hosts runtime-loadable harness factories.
//
// A Plugin is the wiring-layer shape of an extension: it has an ID, a
// version, a HarnessFactory that produces a Harness, and optional OnLoad /
// OnUnload hooks. Manager is a process-level singleton that calls the
// factory, runs the hooks, and registers / unregisters the harness with
// the global harness registry.
//
// Plugins are statically linked in this codebase; the spec deliberately
// defers .so dynamic loading to a later phase. The Manager API is shaped
// so the loader can be swapped later without touching call sites.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/harness"
)

// Hooks carries optional lifecycle callbacks. Either field may be nil.
type Hooks struct {
	OnLoad   func(ctx context.Context) error
	OnUnload func(ctx context.Context) error
}

// PluginConfig carries settings parsed from config.yaml. It is kept as a
// generic map today; per-plugin typed config lives in the factory closure.
type PluginConfig struct {
	Enabled  bool
	Path     string
	Settings map[string]any
}

// HarnessFactory builds the Harness instance this plugin contributes. It
// runs once during Load.
type HarnessFactory func() (harness.Harness, error)

// Plugin is the wiring-layer shape of one runtime extension.
type Plugin struct {
	ID             string
	Version        string
	HarnessFactory HarnessFactory
	Hooks          *Hooks
	Config         PluginConfig
}

// loadedPlugin is the manager's internal record. It exists separately from
// Plugin so the manager can keep metadata Load did not need (LoadedAt,
// resolved Harness) without forcing every Plugin author to set them.
type loadedPlugin struct {
	Plugin   *Plugin
	Harness  harness.Harness
	Hooks    *Hooks
	LoadedAt time.Time
}

// EventEmitter is the narrow surface the manager uses to publish load /
// unload events. *event.Bus satisfies it.
type EventEmitter interface {
	Emit(ev event.Event)
}

// Manager is a goroutine-safe collection of loaded plugins. The default
// manager is process-global; tests construct their own.
type Manager struct {
	mu     sync.RWMutex
	loaded map[string]*loadedPlugin
	bus    EventEmitter
}

// NewManager returns an empty Manager. A nil bus is allowed; events are
// then silently dropped.
func NewManager(bus EventEmitter) *Manager {
	return &Manager{loaded: make(map[string]*loadedPlugin), bus: bus}
}

var defaultManager = NewManager(nil)

// DefaultManager returns the process-wide Manager.
func DefaultManager() *Manager { return defaultManager }

// AttachBus swaps the event bus the default manager publishes to. Tests
// can call this to inject a recording emitter.
func AttachBus(bus EventEmitter) { defaultManager.bus = bus }

// Load registers the plugin's harness, runs the OnLoad hook, and records
// the plugin in the manager. Loading is idempotent on ID: a second Load
// with the same ID replaces the prior entry.
func (m *Manager) Load(ctx context.Context, p *Plugin) error {
	if err := validatePlugin(p); err != nil {
		return err
	}
	built, err := p.HarnessFactory()
	if err != nil {
		return fmt.Errorf("plugin %q factory: %w", p.ID, err)
	}
	if p.Hooks != nil && p.Hooks.OnLoad != nil {
		if err := p.Hooks.OnLoad(ctx); err != nil {
			return fmt.Errorf("plugin %q OnLoad: %w", p.ID, err)
		}
	}
	if err := harness.Register(built, p.ID); err != nil {
		return fmt.Errorf("plugin %q register: %w", p.ID, err)
	}
	m.mu.Lock()
	m.loaded[p.ID] = &loadedPlugin{Plugin: p, Harness: built, Hooks: p.Hooks, LoadedAt: time.Now()}
	m.mu.Unlock()
	m.emit(event.PluginLoadedEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: ""}},
		PluginID:  p.ID,
		Version:   p.Version,
		HarnessID: built.ID(),
	})
	return nil
}

// Unload removes the plugin: emit event, run OnUnload, unregister the
// harness, drop the entry. Each step records its error; the function
// returns the joined errors so a partial failure is neither hidden nor
// fatal.
func (m *Manager) Unload(ctx context.Context, id string) error {
	m.mu.Lock()
	lp, ok := m.loaded[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.loaded, id)
	harnessID := ""
	hooks := (*Hooks)(nil)
	if lp.Harness != nil {
		harnessID = lp.Harness.ID()
	}
	if lp.Hooks != nil {
		hooks = lp.Hooks
	}
	m.mu.Unlock()

	m.emit(event.PluginUnloadedEvent{
		EventBase: event.EventBase{EventCommon: event.EventCommon{SessionID: ""}},
		PluginID:  id,
		HarnessID: harnessID,
	})

	var errs []error
	if hooks != nil && hooks.OnUnload != nil {
		if err := hooks.OnUnload(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q OnUnload: %w", id, err))
		}
	}
	if harnessID != "" {
		harness.Unregister(harnessID)
	}
	return errors.Join(errs...)
}

// ListLoaded returns the loaded plugins ordered by id.
func (m *Manager) ListLoaded() []*Plugin {
	m.mu.RLock()
	out := make([]*Plugin, 0, len(m.loaded))
	for _, lp := range m.loaded {
		out = append(out, lp.Plugin)
	}
	m.mu.RUnlock()

	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Get returns the plugin registered under id, if any.
func (m *Manager) Get(id string) (*Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lp, ok := m.loaded[id]
	if !ok {
		return nil, false
	}
	return lp.Plugin, true
}

// LoadPlugin loads p via the default Manager.
func LoadPlugin(ctx context.Context, p *Plugin) error { return defaultManager.Load(ctx, p) }

// UnloadPlugin unloads p via the default Manager.
func UnloadPlugin(ctx context.Context, id string) error { return defaultManager.Unload(ctx, id) }

// ListLoaded is the default-manager shortcut for ListLoaded.
func ListLoaded() []*Plugin { return defaultManager.ListLoaded() }

// Get is the default-manager shortcut for Get.
func Get(id string) (*Plugin, bool) { return defaultManager.Get(id) }

// ResetForTests clears the default Manager. Tests that build plugins from
// scratch need a clean slate.
func ResetForTests() {
	defaultManager = NewManager(nil)
}

func (m *Manager) emit(ev event.Event) {
	if m.bus == nil {
		return
	}
	m.bus.Emit(ev)
}

func validatePlugin(p *Plugin) error {
	if p == nil {
		return errors.New("plugin: nil")
	}
	if p.ID == "" {
		return errors.New("plugin: ID is required")
	}
	if p.Version == "" {
		return errors.New("plugin: Version is required")
	}
	if p.HarnessFactory == nil {
		return errors.New("plugin: HarnessFactory is required")
	}
	return nil
}
