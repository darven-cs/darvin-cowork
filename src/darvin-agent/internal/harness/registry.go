package harness

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registration is one registry entry.
type Registration struct {
	Harness       Harness
	OwnerPluginID string
	RegisteredAt  time.Time
}

var registry = struct {
	mu   sync.RWMutex
	byID map[string]*Registration
}{byID: map[string]*Registration{}}

// Register adds h under its own ID, replacing any existing entry with that
// id. ownerPluginID names the plugin that supplied it; "" for built-ins.
//
// Registration fails when the harness declares a capability it does not
// implement, or when it reports a plugin id that disagrees with the owner.
// Both are wiring mistakes, so they surface at startup instead of at the
// first call that depends on them.
func Register(h Harness, ownerPluginID string) error {
	if h == nil {
		return ErrHarnessRequired
	}
	id := strings.TrimSpace(h.ID())
	if id == "" {
		return ErrIDRequired
	}
	if err := VerifyCapabilities(h); err != nil {
		return err
	}
	owner := strings.TrimSpace(ownerPluginID)
	declared := strings.TrimSpace(h.PluginID())
	if declared != "" && owner != "" && declared != owner {
		return fmt.Errorf("%w: harness %q reports %q, registered by %q",
			ErrPluginIDMismatch, id, declared, owner)
	}
	if owner == "" {
		owner = declared
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.byID[id] = &Registration{
		Harness:       h,
		OwnerPluginID: owner,
		RegisteredAt:  time.Now(),
	}
	return nil
}

// Unregister drops the entry for id. Unknown ids are a no-op.
func Unregister(id string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.byID, strings.TrimSpace(id))
}

// Get returns the harness registered under id. An empty id returns the
// healthy harness with the lowest id.
//
// The empty-id form is a diagnostic and last-resort entry point only: it
// ignores provider, context engine and delegation entirely. Real selection
// goes through Rank or Policy.Resolve.
func Get(id string) (Harness, bool) {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		reg, ok := Lookup(trimmed)
		if !ok {
			return nil, false
		}
		return reg.Harness, true
	}
	for _, reg := range List() {
		if reg.Harness.Capabilities().Healthy {
			return reg.Harness, true
		}
	}
	return nil, false
}

// Lookup returns the full registration for id.
func Lookup(id string) (*Registration, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	reg, ok := registry.byID[strings.TrimSpace(id)]
	return reg, ok
}

// List returns every registration ordered by ascending id, so callers never
// observe map iteration order. Priority ordering belongs to Rank, which reads
// it from Supports.
func List() []*Registration {
	registry.mu.RLock()
	out := make([]*Registration, 0, len(registry.byID))
	for _, reg := range registry.byID {
		out = append(out, reg)
	}
	registry.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Harness.ID() < out[j].Harness.ID()
	})
	return out
}

// ResetAll calls Reset on every registered harness. One harness failing does
// not stop the fan-out; every error is joined into the return value so a
// partial failure is neither hidden nor fatal.
func ResetAll(ctx context.Context, params ResetParams) error {
	var errs []error
	for _, reg := range List() {
		if err := reg.Harness.Reset(ctx, params); err != nil {
			errs = append(errs, fmt.Errorf("harness %q reset: %w", reg.Harness.ID(), err))
		}
	}
	return errors.Join(errs...)
}

// DisposeAll calls Dispose on every registered harness with the same fan-out
// semantics as ResetAll. Intended for process shutdown.
func DisposeAll(ctx context.Context) error {
	var errs []error
	for _, reg := range List() {
		if err := reg.Harness.Dispose(ctx); err != nil {
			errs = append(errs, fmt.Errorf("harness %q dispose: %w", reg.Harness.ID(), err))
		}
	}
	return errors.Join(errs...)
}

// MustRegister panics when registration fails. Intended for process startup,
// where a mis-wired harness must not be papered over.
func MustRegister(h Harness, ownerPluginID string) {
	if err := Register(h, ownerPluginID); err != nil {
		panic(fmt.Sprintf("harness: register %q: %v", idOf(h), err))
	}
}

// ResetRegistryForTests clears every registration.
func ResetRegistryForTests() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.byID = map[string]*Registration{}
}

func idOf(h Harness) string {
	if h == nil {
		return ""
	}
	return h.ID()
}
