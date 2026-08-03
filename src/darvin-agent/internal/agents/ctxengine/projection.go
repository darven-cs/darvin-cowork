package ctxengine

import (
	"context"
	"errors"
	"sort"
	"time"
)

// ContextProjection is a persisted backend-side view of the context.
// Stored in an in-memory map on *DefaultAssembler; a real backend
// (SQLite store, dreaming) is a follow-up concern.
type ContextProjection struct {
	ID        string
	Type      string // "agent" | "tool" | "memory"
	CreatedAt time.Time
	ExpiresAt *time.Time
	State     map[string]any
}

// ErrProjectionNotFound is returned by ProjectionGet when the requested ID
// has no entry in the in-memory registry.
var ErrProjectionNotFound = errors.New("ctxengine: projection not found")

// ErrProjectionIDEmpty is returned by ProjectionCreate when the caller
// forgets to populate ID. We refuse silently-on-empty to avoid poisoning
// the registry with anonymous entries.
var ErrProjectionIDEmpty = errors.New("ctxengine: projection ID is empty")

// ProjectionCreate inserts p into the in-memory registry. It is an error
// to call with an empty ID or a cancelled context; the caller is otherwise
// free to populate Type / CreatedAt / ExpiresAt / State as they see fit
// (CreatedAt is overwritten with time.Now() if zero).
//
// Method on *DefaultAssembler (not on the 10-method ContextEngine
// interface) — projections are a SubAgent surface that may be promoted
// to a separate interface later.
func (a *DefaultAssembler) ProjectionCreate(ctx context.Context, p ContextProjection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.ID == "" {
		return ErrProjectionIDEmpty
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	a.projectionsMu.Lock()
	a.projections[p.ID] = p
	a.projectionsMu.Unlock()
	return nil
}

// ProjectionGet returns the projection for id and a found flag. Expired
// projections (ExpiresAt set and in the past) return ErrProjectionNotFound
// even if the entry exists, so callers can rely on a clean miss signal.
func (a *DefaultAssembler) ProjectionGet(ctx context.Context, id string) (ContextProjection, error) {
	if err := ctx.Err(); err != nil {
		return ContextProjection{}, err
	}
	a.projectionsMu.RLock()
	p, ok := a.projections[id]
	a.projectionsMu.RUnlock()
	if !ok {
		return ContextProjection{}, ErrProjectionNotFound
	}
	if p.ExpiresAt != nil && !p.ExpiresAt.After(time.Now()) {
		return ContextProjection{}, ErrProjectionNotFound
	}
	return p, nil
}

// ProjectionList returns a snapshot of all non-expired projections, sorted
// by ID for deterministic output. The slice is owned by the caller and
// may be mutated freely; the State maps inside each projection are shared
// by reference (callers must not mutate them).
func (a *DefaultAssembler) ProjectionList(ctx context.Context) []ContextProjection {
	if err := ctx.Err(); err != nil {
		return nil
	}
	now := time.Now()
	a.projectionsMu.RLock()
	out := make([]ContextProjection, 0, len(a.projections))
	for _, p := range a.projections {
		if p.ExpiresAt != nil && !p.ExpiresAt.After(now) {
			continue
		}
		out = append(out, p)
	}
	a.projectionsMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ProjectionDelete removes the projection with the given id. It is not an
// error to delete a missing id (idempotent); the operation only fails if
// the context is cancelled.
func (a *DefaultAssembler) ProjectionDelete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.projectionsMu.Lock()
	delete(a.projections, id)
	a.projectionsMu.Unlock()
	return nil
}
