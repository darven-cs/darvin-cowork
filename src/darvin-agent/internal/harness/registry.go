package harness

import (
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
// implement, so a mis-wired backend is rejected at startup instead of at the
// first call that needs the capability.
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
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.byID[id] = &Registration{
		Harness:       h,
		OwnerPluginID: ownerPluginID,
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
// highest-priority healthy harness, or false when none is registered.
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

// List returns every registration ordered by descending auto-selection
// priority then ascending id, so callers never observe map iteration order.
func List() []*Registration {
	registry.mu.RLock()
	out := make([]*Registration, 0, len(registry.byID))
	for _, reg := range registry.byID {
		out = append(out, reg)
	}
	registry.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := autoPriority(out[i].Harness), autoPriority(out[j].Harness)
		if pi != pj {
			return pi > pj
		}
		return out[i].Harness.ID() < out[j].Harness.ID()
	})
	return out
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

func autoPriority(h Harness) int {
	sel, ok := h.(AutoSelector)
	if !ok {
		return 0
	}
	hint := sel.AutoSelection()
	if hint == nil {
		return 0
	}
	return hint.Priority
}

func idOf(h Harness) string {
	if h == nil {
		return ""
	}
	return h.ID()
}
