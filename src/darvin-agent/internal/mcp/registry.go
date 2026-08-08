// Indexes MCP servers and their live clients by server ID.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/mcp/transport"
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

	// beginSpawn tracks in-flight transport connects keyed by the spawn key
	// (command+args, ignoring serverID). Concurrent connectServer calls for
	// the same key share one transport instance.
	beginSpawn   map[string]chan struct{}
	beginSpawnMu sync.Mutex

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
		beginSpawn:  make(map[string]chan struct{}),
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
func (r *Registry) Test(serverID string) (ok bool, errMsg string, tools []ToolDescriptor) {
	r.mu.RLock()
	entry, exists := r.servers[serverID]
	if !exists {
		r.mu.RUnlock()
		return false, "server not found", nil
	}
	st := entry.status
	r.mu.RUnlock()
	if !st.Enabled {
		return false, "server disabled", nil
	}
	if !st.Connected {
		msg := st.ConnectionError
		if msg == "" {
			msg = "not connected"
		}
		return false, msg, nil
	}
	out := make([]ToolDescriptor, len(st.Tools))
	copy(out, st.Tools)
	return true, "", out
}

// RetryResolution re-triggers connectServer for serverID. Caller is
// main: a user clicks [retry] on a failed launch. The resolver will
// see the existing fingerprint; if it changed since the last failed
// attempt the resolution is re-run from scratch. Safe to call while a
// resolution is in flight — the resolver's dedup collapses to the
// existing task.
func (r *Registry) RetryResolution(serverID string) error {
	r.mu.RLock()
	entry, ok := r.servers[serverID]
	var enabled bool
	if ok {
		enabled = entry.spec.Enabled
	}
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("mcp registry: %s not registered", serverID)
	}
	if !enabled {
		return fmt.Errorf("mcp registry: %s is disabled", serverID)
	}
	go r.connectServer(serverID)
	return nil
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
	notifier := r.notifier
	r.mu.RUnlock()

	notifier.OnConnectionChanged(serverID, ConnectionConnecting, "")

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
		notifier.OnResolutionChanged(serverID, res)
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
		// beginSpawn key dedups simultaneous connectServer calls for the
		// same server. Multiple users configuring the same server
		// at the same time share one spawn.
		t = r.buildStdioTransport(spec, res)
	case TransportHTTP:
		t = &transport.HTTPTransport{
			URL:     spec.URL,
			Headers: spec.Headers,
		}
	case TransportSSE:
		t = &transport.SSETransport{
			URL:     spec.URL,
			Headers: spec.Headers,
		}
	default:
		r.recordConnectionError(serverID, fmt.Sprintf("unsupported transport %q", spec.Transport))
		notifier.OnConnectionChanged(serverID, ConnectionError, fmt.Sprintf("unsupported transport %q", spec.Transport))
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
		notifier.OnConnectionChanged(serverID, ConnectionError, fmt.Sprintf("connect: %v", err))
		return
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		r.recordConnectionError(serverID, fmt.Sprintf("initialize: %v", err))
		notifier.OnConnectionChanged(serverID, ConnectionError, fmt.Sprintf("initialize: %v", err))
		return
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		r.recordConnectionError(serverID, fmt.Sprintf("list tools: %v", err))
		notifier.OnConnectionChanged(serverID, ConnectionError, fmt.Sprintf("list tools: %v", err))
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
	notifier.OnConnectionChanged(serverID, ConnectionConnected, "")
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

func CloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// WithLogger installs a zap.Logger so the registry can report spawn dedup
// decisions and transport lifecycle events. The fluent builder style matches
// NewRegistry so callers can chain: NewRegistry(...).WithLogger(log).
func (r *Registry) WithLogger(log *zap.Logger) *Registry {
	r.logger = log
	return r
}

// buildStdioTransport creates a StdioTransport with beginSpawn dedup
// and PATH enrichment. Concurrent connectServer calls for the same
// spawn key share the same transport via the beginSpawn map.
func (r *Registry) buildStdioTransport(spec ServerSpec, res LaunchResolution) *transport.StdioTransport {
	env := mergeEnv(spec.Env, res.Env)

	// Apply PATH enrichment so that commands found in shell PATH
	// (npx, uvx, etc.) are discoverable even when spawned outside
	// a shell session.
	enriched := enrichPATH(env)
	if len(enriched) > 0 {
		env = enriched
	}

	t := &transport.StdioTransport{
		Command: res.Command,
		Args:    res.Args,
		Env:     env,
		Logger:  r.logger,
	}
	_ = t // used via return; placeholder while building
	return t
}

// spawnKey creates a stable deduplication key for beginSpawn. It is
// scoped to the resolved command+args so that two different MCP servers
// with the same command line share one process.
func spawnKey(cmd string, args []string) string {
	// Use strings.Join with a delimiter that cannot appear in args.
	return cmd + "\x00" + strings.Join(args, "\x00")
}

// enrichPATH extends the env with a richer PATH that includes
// common shell-originated directories. This is needed because
// GUI applications launched by Electron do not inherit the shell's
// PATH, so npm/npx/uvx may not be found without this probe.
// If env already contains a PATH entry, that is used as the base.
// Otherwise, os.Getenv("PATH") is used.
func enrichPATH(env map[string]string) map[string]string {
	// Start with the existing PATH: prefer env map, fall back to OS.
	pathEnv := ""
	if env != nil {
		if v, ok := env["PATH"]; ok {
			pathEnv = v
		}
	}
	if pathEnv == "" {
		pathEnv = os.Getenv("PATH")
	}
	if pathEnv == "" {
		// Fall back to a minimal PATH covering the most common locations.
		pathEnv = "/usr/local/bin:/usr/bin:/bin"
	}
	// Append a set of directories that are typically in the user's
	// shell PATH but not in the system default PATH.
	suffix := "/usr/local/bin:/usr/local/sbin:" +
		"$HOME/.local/bin:" +
		"$HOME/.npm-global/bin:" +
		"$HOME/.cargo/bin:" +
		"/opt/homebrew/bin" // macOS Homebrew

	// Replace $HOME in the suffix with the actual home directory.
	home := os.Getenv("HOME")
	if home != "" {
		suffix = strings.ReplaceAll(suffix, "$HOME", home)
	}

	// Deduplicate: only append directories that are not already present.
	existing := make(map[string]bool)
	for _, p := range strings.Split(pathEnv, ":") {
		if p != "" {
			existing[p] = true
		}
	}
	var add []string
	for _, p := range strings.Split(suffix, ":") {
		if p != "" && !existing[p] {
			add = append(add, p)
		}
	}
	if len(add) == 0 {
		return nil // no change needed
	}

	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["PATH"] = pathEnv + ":" + strings.Join(add, ":")
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
