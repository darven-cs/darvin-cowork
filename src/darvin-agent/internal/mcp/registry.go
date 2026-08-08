// Indexes MCP servers and their live clients by server ID.

package mcp

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// Registry is the process-wide index of MCP servers. One entry per
// ServerSpec; the entry holds the live Client plus the most recent
// LaunchResolution. Reads are O(1) by ServerID; writes go through a
// single mutex so concurrent Register/SetEnabled calls cannot corrupt
// the map.
//
// v0 lifecycle:
//  1. Register(spec)  — install + connect + ListTools
//  2. SetEnabled(false) — close transport; keep spec for re-enable
//  3. SetEnabled(true)  — re-run resolver if fingerprint changed, else connect
//  4. Unregister(serverID) — close + remove from map
//
// The registry never rebuilds a Client's transport on its own; transport
// reconnection is owned by Client.WithReconnectFactory, set up in
// connectServer. That factory closes the old transport and rebuilds.
type Registry struct {
	mu          sync.RWMutex
	servers     map[string]*serverEntry
	resolver    *ResolverManager
	persistence ResolutionPersistence
	notifier    Notifier

	logger *zap.Logger
}

type serverEntry struct {
	spec        ServerSpec
	status      ServerStatus
	client      *Client
	fingerprint string
}

// NewRegistry returns an empty registry. Persistence is required — use
// NewInMemoryResolutionPersistence for tests and the v0 binary, swap in
// the SQLite impl when wiring the SQLite store.
func NewRegistry(resolver *ResolverManager, persistence ResolutionPersistence) *Registry {
	return &Registry{
		servers:     make(map[string]*serverEntry),
		resolver:    resolver,
		persistence: persistence,
		notifier:    noopNotifier(),
	}
}

// SetNotifier installs the callbacks that the registry fires on
// connection / resolution state changes. Safe to call once after
// construction; the gateway package uses this to wire a registry
// back to the handler that hosts it (handler embeds the registry,
// registry calls back into the handler).
func (r *Registry) SetNotifier(n Notifier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n.OnConnectionChanged == nil {
		n.OnConnectionChanged = func(string, ConnectionStatus, string) {}
	}
	if n.OnResolutionChanged == nil {
		n.OnResolutionChanged = func(string, LaunchResolution) {}
	}
	r.notifier = n
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

// Update replaces the server's spec with patch. Resolver fingerprint is
// recomputed; if enabled the entry is re-resolved and reconnected.
func (r *Registry) Update(ctx context.Context, serverID string, patch ServerSpec) error {
	r.mu.Lock()
	r.resolver.Cancel(serverID)
	entry, ok := r.servers[serverID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("mcp registry: %s not registered", serverID)
	}
	merged := cloneSpec(entry.spec)
	if patch.Name != "" {
		merged.Name = patch.Name
	}
	if patch.Description != "" {
		merged.Description = patch.Description
	}
	merged.Enabled = patch.Enabled
	if patch.Transport != "" {
		merged.Transport = patch.Transport
	}
	if patch.Command != "" {
		merged.Command = patch.Command
	}
	if patch.Args != nil {
		merged.Args = append([]string(nil), patch.Args...)
	}
	if patch.Env != nil {
		merged.Env = CloneStringMap(patch.Env)
	}
	if patch.URL != "" {
		merged.URL = patch.URL
	}
	if patch.Headers != nil {
		merged.Headers = CloneStringMap(patch.Headers)
	}
	if patch.GitHubURL != "" {
		merged.GitHubURL = patch.GitHubURL
	}
	if patch.RegistryID != "" {
		merged.RegistryID = patch.RegistryID
	}
	fp := ComputeFingerprint(merged)
	old := entry.client
	entry.spec = merged
	entry.status.Enabled = merged.Enabled
	entry.status.Resolution = nil
	entry.status.Tools = nil
	entry.status.Connected = false
	entry.status.ConnectionError = ""
	entry.fingerprint = fp
	notifier := r.notifier
	r.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if merged.Enabled {
		go r.connectServer(serverID)
	} else {
		notifier.OnConnectionChanged(serverID, ConnectionDisconnected, "")
	}
	return nil
}

// Unregister closes the client (if any), removes the entry, and drops
// any persisted LaunchResolution. The next Register starts clean.
func (r *Registry) Unregister(_ context.Context, serverID string) error {
	r.mu.Lock()
	r.resolver.Cancel(serverID)
	entry, ok := r.servers[serverID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	if entry.client != nil {
		_ = entry.client.Close()
	}
	delete(r.servers, serverID)
	notifier := r.notifier
	r.mu.Unlock()
	if r.persistence != nil {
		_ = r.persistence.DeleteResolution(context.Background(), serverID)
	}
	notifier.OnConnectionChanged(serverID, ConnectionDisconnected, "")
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
		notifier := r.notifier
		r.mu.Unlock()
		notifier.OnConnectionChanged(serverID, ConnectionDisconnected, "")
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

// GetSpec returns a copy of the spec for serverID. Used by the gateway
// handler to wire a list / update result back to the renderer.
func (r *Registry) GetSpec(serverID string) (ServerSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.servers[serverID]
	if !ok {
		return ServerSpec{}, false
	}
	return cloneSpec(e.spec), true
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

// GetToolsByName finds which server exposes a tool by name. Names are
// unique within a server but not across servers — the registry returns
// the first match in arbitrary order.
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

// CallTool invokes toolName on serverID. Errors when the server is
// missing or not connected; the caller (McpTool) surfaces that back to
// the agent loop.
func (r *Registry) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*CallToolResult, error) {
	r.mu.RLock()
	entry, ok := r.servers[serverID]
	var client *Client
	var connected bool
	if ok {
		client = entry.client
		connected = entry.status.Connected
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %s not registered", serverID)
	}
	if client == nil || !connected {
		return nil, fmt.Errorf("mcp: server %s not connected", serverID)
	}
	return client.CallTool(ctx, toolName, args)
}

// Test returns the current connection state for serverID without dialing.
// ok=true means the client is connected; tools is a copy of the most recent
// ListTools payload. The caller (gateway) maps ConnectionError straight
// to the IPC response; main renders it as a toast.
