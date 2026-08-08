package ctxengine

import (
	"context"
	"errors"
	"sort"
	"time"
)

// ContextProjection is a backend-side view of the context, stored in an
// in-memory map on *DefaultAssembler (a real backend is a follow-up).
type ContextProjection struct {
	ID        string
	Type      string // "agent" | "tool" | "memory"
	CreatedAt time.Time
	ExpiresAt *time.Time
	State     map[string]any
}

// ErrProjectionNotFound is returned when the requested ID has no entry.
var ErrProjectionNotFound = errors.New("ctxengine: projection not found")

// ErrProjectionIDEmpty is returned when the caller forgets to populate ID.
var ErrProjectionIDEmpty = errors.New("ctxengine: projection ID is empty")

// ProjectionCreate inserts p into the in-memory registry. Rejects an
// empty ID or cancelled context; CreatedAt is set to time.Now() if zero.
// It is a method on *DefaultAssembler, not on the ContextEngine
// interface — projections are a SubAgent surface that may be promoted
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

// ProjectionGet returns the projection for id. Expired projections
// return ErrProjectionNotFound even if the entry exists.
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

// ProjectionList returns all non-expired projections, sorted by ID for
// deterministic output. The slice is caller-owned; State maps are shared.
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

// ProjectionDelete removes the projection with the given id; deleting a
// missing id is not an error.
func (a *DefaultAssembler) ProjectionDelete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.projectionsMu.Lock()
	delete(a.projections, id)
	a.projectionsMu.Unlock()
	return nil
}
