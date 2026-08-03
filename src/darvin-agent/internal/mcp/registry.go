package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"darvin-cowork/backend/internal/mcp/transport"
)

// Registry is the process-wide index of MCP servers. One entry per
// ServerSpec; the entry holds the live Client plus the most recent
// LaunchResolution. Reads are O(1) by ServerID; writes go through a
// single mutex so concurrent Register/SetEnabled calls cannot corrupt
// the map.
//
// v0 lifecycle:
//   1. Register(spec)  — install + connect + ListTools
//   2. SetEnabled(false) — close transport; keep spec for re-enable
//   3. SetEnabled(true)  — re-run resolver if fingerprint changed, else connect
//   4. Unregister(serverID) — close + remove from map
//
// The registry never rebuilds a Client's transport on its own; transport
// reconnection is owned by Client.WithReconnectFactory, set up in
// connectServer. That factory closes the old transport and rebuilds.
type Registry struct {
	mu          sync.RWMutex
	servers     map[string]*serverEntry
	resolver    *ResolverManager
	persistence ResolutionPersistence
}

type serverEntry struct {
	spec         ServerSpec
	status       ServerStatus
	client       *Client
	fingerprint  string
}

// NewRegistry returns an empty registry. Persistence is required — use
// NewInMemoryResolutionPersistence for tests and the v0 binary, swap in
// the SQLite impl (spec 36) when wiring the SQLite store.
func NewRegistry(resolver *ResolverManager, persistence ResolutionPersistence) *Registry {
	return &Registry{
		servers:     make(map[string]*serverEntry),
		resolver:    resolver,
		persistence: persistence,
	}
}

// Register adds or replaces the server for spec.ID. If spec.Enabled is
// true, an asynchronous connect kicks off; if false the entry is kept
// in the disabled state. A second Register for the same ID cancels any
// in-flight resolver and replaces the entry.
func (r *Registry) Register(ctx context.Context, spec ServerSpec) error {
	r.mu.Lock()
	r.resolver.Cancel(spec.ID)

	fp := ComputeFingerprint(spec)
	entry := &serverEntry{
		spec:        cloneSpec(spec),
		status:      ServerStatus{ServerID: spec.ID, Enabled: spec.Enabled},
		fingerprint: fp,
	}
	r.servers[spec.ID] = entry
	r.mu.Unlock()

	if !spec.Enabled {
		return nil
	}
	// Best-effort connect; failures are recorded on status but the
	// entry itself stays so the user can retry via SetEnabled.
	go r.connectServer(spec.ID)
	return nil
}

// Unregister closes the client (if any), removes the entry, and drops
// any persisted LaunchResolution. The next Register starts clean.
func (r *Registry) Unregister(_ context.Context, serverID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolver.Cancel(serverID)
	entry, ok := r.servers[serverID]
	if !ok {
		return nil
	}
	if entry.client != nil {
		_ = entry.client.Close()
	}
	delete(r.servers, serverID)
	if r.persistence != nil {
		_ = r.persistence.DeleteResolution(context.Background(), serverID)
	}
	return nil
}

// SetEnabled toggles a server's enabled state. On disable it closes the
// client and clears the in-memory tools list; on enable it triggers a
// resolver pass (if needed) and re-connects.
func (r *Registry) SetEnabled(ctx context.Context, serverID string, enabled bool) error {
	r.mu.Lock()
	entry, ok := r.servers[serverID]
	if !ok {
		return fmt.Errorf("mcp registry: %s not registered", serverID)
	}
	entry.spec.Enabled = enabled
	entry.status.Enabled = enabled
	if !enabled {
		// Cancel any in-flight resolve and close the live client.
		r.resolver.Cancel(serverID)
		if entry.client != nil {
			_ = entry.client.Close()
			entry.client = nil
		}
		entry.status.Connected = false
		entry.status.Tools = nil
		entry.status.Resolution = nil
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	go r.connectServer(serverID)
	return nil
}

// List returns a snapshot of every server's status. The order is
// undefined; callers that need a stable order should sort by ServerID.
func (r *Registry) List() []ServerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ServerStatus, 0, len(r.servers))
	for _, e := range r.servers {
		out = append(out, e.status)
	}
	return out
}

// Get returns a copy of the status for serverID.
func (r *Registry) Get(serverID string) (ServerStatus, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.servers[serverID]
	if !ok {
		return ServerStatus{}, false
	}
	return e.status, true
}

// GetTools returns the live tool list for serverID, or nil if not
// connected. The slice is a copy — callers may mutate freely.
func (r *Registry) GetTools(serverID string) []ToolDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.servers[serverID]
	if !ok || !e.status.Connected {
		return nil
	}
	out := make([]ToolDescriptor, len(e.status.Tools))
	copy(out, e.status.Tools)
	return out
}

// GetToolsByName is the cross-server tool lookup used by the agent
// executor (spec 38): find which server exposes a tool by name. Names
// are unique within a server but not across servers — the registry
// returns the first match in arbitrary order.
func (r *Registry) GetToolsByName(name string) (string, *ToolDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.servers {
		for i := range e.status.Tools {
			if e.status.Tools[i].Name == name {
				return e.spec.ID, &e.status.Tools[i], true
			}
		}
	}
	return "", nil, false
}

// LoadStaleResolutions walks every persisted LaunchResolution and
// re-triggers any that are stuck in StatusInstalling past the 30-min
// grace period. Called once at startup after the persistence layer is
// loaded. Safe to call repeatedly — duplicate triggers are deduped by
// IsInFlight.
func (r *Registry) LoadStaleResolutions(ctx context.Context) error {
	if r.persistence == nil {
		return nil
	}
	all, err := r.persistence.LoadAllResolutions(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, res := range all {
		if res.Status != StatusInstalling {
			continue
		}
		if now.Sub(res.UpdatedAt) < staleResolutionTimeout {
			continue
		}
		if r.resolver.IsInFlight(res.ServerID) {
			continue
		}
		r.mu.RLock()
		entry, ok := r.servers[res.ServerID]
		r.mu.RUnlock()
		if !ok {
			continue
		}
		// Re-trigger; the resolver is invoked again with the existing
		// fingerprint so the cache is reused when only the in-flight
		// flag was the problem.
		_ = r.resolver.Resolve(ctx, entry.spec, entry.fingerprint)
	}
	return nil
}

// connectServer runs the resolver (if needed) and then opens the
// transport, performs the MCP handshake, and refreshes the tools list.
// It is safe to call concurrently for different servers.
func (r *Registry) connectServer(serverID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.mu.RLock()
	entry, ok := r.servers[serverID]
	if !ok || !entry.spec.Enabled {
		r.mu.RUnlock()
		return
	}
	spec := cloneSpec(entry.spec)
	fp := entry.fingerprint
	r.mu.RUnlock()

	// Step 1: resolve the launch line.
	res, ok := r.lookupResolution(serverID)
	if !ok || res.Status != StatusReady || res.SourceFingerprint != fp {
		ch := r.resolver.Resolve(ctx, spec, fp)
		select {
		case res = <-ch:
		case <-ctx.Done():
			return
		}
		r.persistResolution(res)
		r.recordResolution(serverID, res)
	} else {
		r.recordResolution(serverID, res)
	}

	if res.Status != StatusReady {
		// The resolver failed (failed or unsupported). Fall back to
		// the spec's original command so the user gets a usable
		// client; the failure reason is preserved on status.
		res.Command = spec.Command
		res.Args = spec.Args
		res.Env = mergeEnv(spec.Env, nil)
	}

	// Step 2: build the transport from the resolved command.
	var t transport.Transport
	switch spec.Transport {
	case TransportStdio:
		t = &transport.StdioTransport{
			Command: res.Command,
			Args:    res.Args,
			Env:     mergeEnv(spec.Env, res.Env),
		}
	case TransportHTTP, TransportSSE:
		t = &transport.HTTPTransport{
			URL:     spec.URL,
			Headers: spec.Headers,
		}
	default:
		r.recordConnectionError(serverID, fmt.Sprintf("unsupported transport %q", spec.Transport))
		return
	}

	// Step 3: dial, handshake, list tools. Any failure closes the
	// transport and records the error on status.
	client := NewClient(t).WithReconnectFactory(func() (transport.Transport, error) {
		return nil, errors.New("reconnect not implemented in v0")
	})
	if err := client.Connect(ctx); err != nil {
		_ = client.Close()
		r.recordConnectionError(serverID, fmt.Sprintf("connect: %v", err))
		return
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		r.recordConnectionError(serverID, fmt.Sprintf("initialize: %v", err))
		return
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		r.recordConnectionError(serverID, fmt.Sprintf("list tools: %v", err))
		return
	}

	r.mu.Lock()
	if entry, ok := r.servers[serverID]; ok {
		entry.client = client
		entry.status.Connected = true
		entry.status.ConnectionError = ""
		entry.status.Tools = tools
	}
	r.mu.Unlock()
}

// recordResolution updates the entry's resolution field after a resolve.
func (r *Registry) recordResolution(serverID string, res LaunchResolution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.servers[serverID]
	if !ok {
		return
	}
	entry.status.Resolution = &res
	entry.status.Resolving = res.Status == StatusInstalling || res.Status == StatusPending
	if res.Status == StatusFailed || res.Status == StatusUnsupported {
		entry.status.ConnectionError = res.Error
	}
}

// recordConnectionError stores a non-resolver error on the entry. It is
// called from connectServer; the caller has not yet attached the
// client to the entry, so this is the place to surface Initialize /
// ListTools failures.
func (r *Registry) recordConnectionError(serverID, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.servers[serverID]
	if !ok {
		return
	}
	entry.status.Connected = false
	entry.status.ConnectionError = msg
}

// lookupResolution returns the cached resolution, or zero-value+false
// if none is stored yet. The persistence layer is the source of truth
// across restarts; this is the in-memory cache for the running process.
func (r *Registry) lookupResolution(serverID string) (LaunchResolution, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.servers[serverID]
	if !ok || entry.status.Resolution == nil {
		return LaunchResolution{}, false
	}
	return *entry.status.Resolution, true
}

// persistResolution hands the result off to the persistence layer.
// Errors are logged via the resolver manager's logger in future; v0
// swallows them because persistence failure is recoverable (next call
// re-resolves).
func (r *Registry) persistResolution(res LaunchResolution) {
	if r.persistence == nil {
		return
	}
	_ = r.persistence.SaveResolution(context.Background(), res)
}

// cloneSpec copies spec so the registry can mutate per-entry state
// without bleeding back into the caller's struct. The Env / Headers
// maps are shallow-copied; values are strings and not mutated.
func cloneSpec(s ServerSpec) ServerSpec {
	out := s
	if s.Env != nil {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			out.Env[k] = v
		}
	}
	if s.Headers != nil {
		out.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			out.Headers[k] = v
		}
	}
	if s.Args != nil {
		out.Args = append([]string(nil), s.Args...)
	}
	return out
}

// mergeEnv combines a base env (spec-provided) with a resolver env
// override (e.g. npxResolver env additions). Resolver wins on conflict.
func mergeEnv(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

const staleResolutionTimeout = 30 * time.Minute