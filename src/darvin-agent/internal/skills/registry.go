// Package skills discovers, loads, registers, and runs user skills.
package skills

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var ErrSkillNotFound = errors.New("skill: not found")
var ErrSkillDisabled = errors.New("skill: disabled")
var ErrSkillNotUserInvocable = errors.New("skill: not user invocable")

type SkillRegistry struct {
	mu     sync.RWMutex
	byID   map[string]*SkillEntry
	byPath map[string]*SkillEntry
}

func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		byID:   map[string]*SkillEntry{},
		byPath: map[string]*SkillEntry{},
	}
}

func (r *SkillRegistry) Load(ctx context.Context, sources []SkillSourceLoader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	merged := make(map[string]*SkillEntry, len(r.byID))
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := src.LoadAll(ctx)
		if err != nil {
			return err
		}
		for _, e := range entries {
			merged[e.ID] = e
		}
	}

	byPath := make(map[string]*SkillEntry, len(merged))
	for id, e := range merged {
		byPath[e.Path] = e
		_ = id
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = merged
	r.byPath = byPath
	return nil
}

func (r *SkillRegistry) Get(id string) (*SkillEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[id]
	return e, ok
}

func (r *SkillRegistry) List() []*SkillEntry {
	return r.Snapshot()
}

func (r *SkillRegistry) ListEnabled() []*SkillEntry {
	entries := r.Snapshot()
	out := entries[:0]
	for _, e := range entries {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return append([]*SkillEntry(nil), out...)
}

func (r *SkillRegistry) ListBySource(source SkillSource) []*SkillEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SkillEntry, 0, len(r.byID))
	for _, e := range r.byID {
		if e.Source == source {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *SkillRegistry) SetEnabled(id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok {
		return ErrSkillNotFound
	}
	e.Enabled = enabled
	return nil
}

func (r *SkillRegistry) Snapshot() []*SkillEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SkillEntry, 0, len(r.byID))
	for _, e := range r.byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
