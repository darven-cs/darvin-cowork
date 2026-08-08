package runtime

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/memory"
)

// WorkspaceBootstrap is the workspace-level cache of the three
// bootstrap files (IDENTITY.md / SOUL.md / USER.md). Process-level
// singleton: every AgentLoopSession sees the same snapshot, and a
// bootstrap.write RPC invalidates the entry so the next Assemble
// re-reads from disk.
type WorkspaceBootstrap struct {
	memMgr *memory.Manager
	log    *zap.Logger

	mu      sync.RWMutex
	content map[string]string

	hookID string
}

// NewWorkspaceBootstrap constructs the singleton and primes its cache.
// The bootstrap-changed hook is registered under a stable id so
// Dispose can unregister by id (anonymous functions have no handle).
func NewWorkspaceBootstrap(memMgr *memory.Manager, log *zap.Logger) *WorkspaceBootstrap {
	if log == nil {
		log = zap.NewNop()
	}
	wb := &WorkspaceBootstrap{
		memMgr:  memMgr,
		log:     log,
		content: map[string]string{},
		hookID:  "workspace-bootstrap",
	}
	wb.RefreshAll(context.Background())
	if memMgr != nil {
		memMgr.RegisterBootstrapChanged(wb.hookID, wb.onBootstrapChanged)
	}
	return wb
}

// Get returns the cached content for the named bootstrap file. Empty
// string when missing or when the memory subsystem is disabled
// (FR-12 graceful degrade).
func (wb *WorkspaceBootstrap) Get(name string) string {
	if wb == nil {
		return ""
	}
	wb.mu.RLock()
	defer wb.mu.RUnlock()
	return wb.content[name]
}

// Invalidate drops the cached entry for name. The next Get returns ""
// until onBootstrapChanged re-primes or RefreshAll runs.
func (wb *WorkspaceBootstrap) Invalidate(name string) {
	if wb == nil {
		return
	}
	wb.mu.Lock()
	delete(wb.content, name)
	wb.mu.Unlock()
}

// RefreshAll re-reads every bootstrap file into the cache.
func (wb *WorkspaceBootstrap) RefreshAll(ctx context.Context) {
	if wb == nil || wb.memMgr == nil {
		return
	}
	names := []string{memory.BootstrapIdentity, memory.BootstrapSoul, memory.BootstrapUser}
	next := make(map[string]string, len(names))
	for _, n := range names {
		if v, err := readBootstrap(ctx, wb.memMgr, n); err == nil {
			next[n] = v
		} else {
			next[n] = ""
		}
	}
	wb.mu.Lock()
	wb.content = next
	wb.mu.Unlock()
}

// Dispose unregisters the change-notification hook and clears the
// content map so any in-flight Get returns "".
func (wb *WorkspaceBootstrap) Dispose() {
	if wb == nil {
		return
	}
	if wb.memMgr != nil {
		wb.memMgr.UnregisterBootstrapChanged(wb.hookID)
	}
	wb.mu.Lock()
	wb.content = map[string]string{}
	wb.mu.Unlock()
}

// onBootstrapChanged is the hook registered with memory.Manager.
func (wb *WorkspaceBootstrap) onBootstrapChanged(name string) {
	if wb == nil {
		return
	}
	wb.Invalidate(name)
	// Eagerly re-prime so the next Get sees the new value without
	// waiting for the next RefreshAll. Failure is logged-and-skipped.
	if wb.memMgr != nil {
		ctx := context.Background()
		if v, err := readBootstrap(ctx, wb.memMgr, name); err == nil {
			wb.mu.Lock()
			wb.content[name] = v
			wb.mu.Unlock()
		} else if wb.log != nil {
			wb.log.Debug("workspace bootstrap re-read failed",
				zap.String("name", name), zap.Error(err))
		}
	}
}

func readBootstrap(ctx context.Context, memMgr *memory.Manager, name string) (string, error) {
	if memMgr == nil || !memMgr.Enabled() {
		return "", nil
	}
	if !memory.IsBootstrapName(name) {
		return "", nil
	}
	content := memMgr.ReadBootstrap(ctx, name)
	if content == "" {
		return "", nil
	}
	return content, nil
}
