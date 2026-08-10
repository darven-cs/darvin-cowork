// MCP server lifecycle JSON-RPC handlers and the notifier callbacks.

package gateway

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/mcp"
)

type ListMcpServersResult struct {
	Servers []McpServerWire `json:"servers"`
}

// McpServerWire is the IPC wire shape for a server. CreatedAt /
// UpdatedAt are unix ms; LaunchStatus / ConnectionStatus / etc. are
// nilable so the renderer can distinguish "not yet reported" from
// "reported as disconnected".
type McpServerWire struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Enabled          bool                       `json:"enabled"`
	TransportType    string                     `json:"transportType"`
	Command          string                     `json:"command,omitempty"`
	Args             []string                   `json:"args,omitempty"`
	Env              map[string]string          `json:"env,omitempty"`
	URL              string                     `json:"url,omitempty"`
	Headers          map[string]string          `json:"headers,omitempty"`
	IsBuiltIn        bool                       `json:"isBuiltIn"`
	GithubURL        string                     `json:"githubUrl,omitempty"`
	RegistryID       string                     `json:"registryId,omitempty"`
	TrustLevel       string                     `json:"trustLevel,omitempty"`
	CreatedAt        int64                      `json:"createdAt"`
	UpdatedAt        int64                      `json:"updatedAt"`
	LaunchStatus     string                     `json:"launchStatus,omitempty"`
	LaunchError      string                     `json:"launchError,omitempty"`
	ConnectionStatus string                     `json:"connectionStatus,omitempty"`
	ConnectionError  string                     `json:"connectionError,omitempty"`
	ExposedTools     []McpServerExposedToolWire `json:"exposedTools,omitempty"`
}

type McpServerExposedToolWire struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	InputSchema     map[string]any `json:"inputSchema"`
	ReadOnlyHint    *bool          `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool          `json:"destructiveHint,omitempty"`
	OpenWorldHint   *bool          `json:"openWorldHint,omitempty"`
}

// McpLaunchResolutionWire is the wire shape for the resolution payload
// of mcp.resolution_changed.
type McpLaunchResolutionWire struct {
	ServerID          string            `json:"serverId"`
	ResolverKind      string            `json:"resolverKind"`
	SourceFingerprint string            `json:"sourceFingerprint"`
	Status            string            `json:"status"`
	PackageName       string            `json:"packageName,omitempty"`
	RequestedVersion  string            `json:"requestedVersion,omitempty"`
	ResolvedVersion   string            `json:"resolvedVersion,omitempty"`
	InstallDir        string            `json:"installDir,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	Error             string            `json:"error,omitempty"`
	FailureStage      string            `json:"failureStage,omitempty"`
	FailureElapsedMs  int64             `json:"failureElapsedMs,omitempty"`
	FailureStderr     string            `json:"failureStderr,omitempty"`
	InstalledAt       *int64            `json:"installedAt,omitempty"`
	ResolvedAt        *int64            `json:"resolvedAt,omitempty"`
	UpdatedAt         int64             `json:"updatedAt"`
}

// wireFromTool converts one MCP tool descriptor to its wire shape,
// carrying the safety annotations (readOnly / destructive / openWorld) so
// the renderer can surface security badges next to each tool.
func wireFromTool(t mcp.ToolDescriptor) McpServerExposedToolWire {
	w := McpServerExposedToolWire{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
	}
	if t.Annotations != nil {
		w.ReadOnlyHint = t.Annotations.ReadOnlyHint
		w.DestructiveHint = t.Annotations.DestructiveHint
		w.OpenWorldHint = t.Annotations.OpenWorldHint
	}
	return w
}

func wireFromServer(s mcp.ServerSpec, st mcp.ServerStatus, now int64) McpServerWire {
	w := McpServerWire{
		ID:            s.ID,
		Name:          s.Name,
		Description:   s.Description,
		Enabled:       st.Enabled,
		TransportType: string(s.Transport),
		Command:       s.Command,
		Args:          append([]string(nil), s.Args...),
		URL:           s.URL,
		Headers:       mcp.CloneStringMap(s.Headers),
		IsBuiltIn:     s.IsBuiltIn,
		GithubURL:     s.GitHubURL,
		RegistryID:    s.RegistryID,
		TrustLevel:    s.TrustLevel,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if len(s.Env) > 0 {
		w.Env = mcp.CloneStringMap(s.Env)
	}
	if st.Connected {
		w.ConnectionStatus = string(mcp.ConnectionConnected)
	} else if st.ConnectionError != "" {
		w.ConnectionStatus = string(mcp.ConnectionError)
		w.ConnectionError = mcp.RedactString(st.ConnectionError)
	} else if st.Resolving {
		w.ConnectionStatus = string(mcp.ConnectionConnecting)
	} else {
		w.ConnectionStatus = string(mcp.ConnectionDisconnected)
	}
	if st.Resolution != nil {
		w.LaunchStatus = string(st.Resolution.Status)
		w.LaunchError = mcp.RedactString(st.Resolution.Error)
	}
	if len(st.Tools) > 0 {
		w.ExposedTools = make([]McpServerExposedToolWire, 0, len(st.Tools))
		for _, t := range st.Tools {
			w.ExposedTools = append(w.ExposedTools, wireFromTool(t))
		}
	}
	return w
}

func wireFromResolution(r mcp.LaunchResolution) McpLaunchResolutionWire {
	// Redact error / stderr text and secret-shaped env values before the
	// resolution crosses the IPC boundary; the renderer and main-side
	// SQLite must not persist credentials.
	r = mcp.RedactResolution(r)
	w := McpLaunchResolutionWire{
		ServerID:          r.ServerID,
		ResolverKind:      string(r.ResolverKind),
		SourceFingerprint: r.SourceFingerprint,
		Status:            string(r.Status),
		PackageName:       r.PackageName,
		RequestedVersion:  r.RequestedVersion,
		ResolvedVersion:   r.ResolvedVersion,
		InstallDir:        r.InstallDir,
		Command:           r.Command,
		Args:              append([]string(nil), r.Args...),
		Env:               mcp.CloneStringMap(r.Env),
		Error:             r.Error,
		FailureStage:      r.FailureStage,
		FailureElapsedMs:  r.FailureElapsed.Milliseconds(),
		FailureStderr:     r.FailureStderr,
		UpdatedAt:         r.UpdatedAt.UnixMilli(),
	}
	if !r.InstalledAt.IsZero() {
		ms := r.InstalledAt.UnixMilli()
		w.InstalledAt = &ms
	}
	if !r.ResolvedAt.IsZero() {
		ms := r.ResolvedAt.UnixMilli()
		w.ResolvedAt = &ms
	}
	return w
}

func handleMcpList(id json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, ListMcpServersResult{Servers: []McpServerWire{}})
	}
	statuses := h.Mcp.List()
	now := time.Now().UnixMilli()
	out := make([]McpServerWire, 0, len(statuses))
	for _, st := range statuses {
		spec, ok := h.Mcp.GetSpec(st.ServerID)
		if !ok {
			continue
		}
		out = append(out, wireFromServer(spec, st, now))
	}
	return successResp(id, ListMcpServersResult{Servers: out})
}

type McpRegisterParams struct {
	Server mcp.ServerSpec `json:"server"`
}

func handleMcpRegister(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpRegisterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.Server.ID == "" {
		return errorResp(id, CodeInvalidParams, "server.id required", nil)
	}
	if err := h.Mcp.Register(context.Background(), p.Server); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpUpdateParams struct {
	ID    string         `json:"id"`
	Patch mcp.ServerSpec `json:"patch"`
}

func handleMcpUpdate(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpUpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.ID == "" {
		return errorResp(id, CodeInvalidParams, "id required", nil)
	}
	if err := h.Mcp.Update(context.Background(), p.ID, p.Patch); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	spec, ok := h.Mcp.GetSpec(p.ID)
	if !ok {
		return errorResp(id, CodeInternalError, "server disappeared after update", nil)
	}
	st, _ := h.Mcp.Get(p.ID)
	return successResp(id, map[string]any{"server": wireFromServer(spec, st, time.Now().UnixMilli())})
}

type McpServerIDParams struct {
	ID string `json:"id"`
}

func handleMcpUnregister(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.Unregister(context.Background(), p.ID); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpSetEnabledParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func handleMcpSetEnabled(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpSetEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.SetEnabled(context.Background(), p.ID, p.Enabled); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

func handleMcpTest(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"ok": false, "error": "mcp registry not configured"})
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	ok, errMsg, tools := h.Mcp.Test(p.ID)
	resp := map[string]any{
		"ok":    ok,
		"error": mcp.RedactString(errMsg),
	}
	// Attach an auth diagnosis (none / possible / required) so the renderer
	// can guide OAuth / credential fixes instead of showing a bare error.
	if spec, exists := h.Mcp.GetSpec(p.ID); exists {
		diag := mcp.DiagnoseAuth(spec.Transport, ok, errMsg, spec.URL, mcp.HasExplicitCredentials(spec))
		resp["authStatus"] = diag.Status
		if diag.Suggestion != "" {
			resp["authSuggestion"] = diag.Suggestion
		}
	}
	if len(tools) > 0 {
		wire := make([]McpServerExposedToolWire, 0, len(tools))
		for _, t := range tools {
			wire = append(wire, wireFromTool(t))
		}
		resp["tools"] = wire
	}
	return successResp(id, resp)
}

func handleMcpRetryResolution(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if err := h.Mcp.RetryResolution(p.ID); err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"ok": true})
}

type McpBootstrapParams struct {
	Servers []mcp.ServerSpec `json:"servers"`
}

func handleMcpBootstrap(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"ok": true})
	}
	var p McpBootstrapParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	for i := range p.Servers {
		if err := h.Mcp.Register(context.Background(), p.Servers[i]); err != nil {
			if h.Log != nil {
				h.Log.Warn("mcp bootstrap register failed",
					zap.String("id", p.Servers[i].ID),
					zap.Error(err))
			}
		}
	}
	return successResp(id, map[string]any{"ok": true})
}

// OnMcpConnectionChanged is the mcp.Notifier callback wired up by
// main.go after both registry and handler exist. It broadcasts the
// mcp.connection_changed notification; main forwards the payload to
// renderer via darvin:push:mcp-connection-changed.
func (h *Handler) OnMcpConnectionChanged(serverID string, status mcp.ConnectionStatus, errMsg string) {
	if h.Ledger != nil {
		h.Ledger.Broadcast("mcp.connection_changed", map[string]any{
			"id":     serverID,
			"status": string(status),
			"error":  errMsg,
		})
	}
	h.refreshToolsIfNeeded()
}

// OnMcpResolutionChanged is the mcp.Notifier callback for resolver
// output (pending / installing / ready / failed). main persists to
// SQLite and pushes the renderer via darvin:push:mcp-servers-changed
// (launchStatus field).
func (h *Handler) OnMcpResolutionChanged(serverID string, res mcp.LaunchResolution) {
	if h.Ledger == nil {
		return
	}
	h.Ledger.Broadcast("mcp.resolution_changed", map[string]any{
		"serverId":   serverID,
		"resolution": wireFromResolution(res),
	})
}

// OnMcpToolsChanged is the mcp.Notifier callback for runtime tool-list
// changes (notifications/tools/list_changed). The tool surface must be
// re-applied so new tools reach the agent and the renderer list refreshes.
func (h *Handler) OnMcpToolsChanged(serverID string) {
	h.refreshToolsIfNeeded()
	if h.Ledger != nil {
		h.Ledger.Broadcast("mcp.connection_changed", map[string]any{
			"id":     serverID,
			"status": string(mcp.ConnectionConnected),
		})
	}
}

// handleMcpLogsGet returns the recent runtime log lines for a server.
func handleMcpLogsGet(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"lines": []string{}})
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	lines := h.Mcp.ServerLogs(p.ID)
	if lines == nil {
		lines = []string{}
	}
	return successResp(id, map[string]any{"lines": lines})
}

type McpResourceReadParams struct {
	ID  string `json:"id"`
	URI string `json:"uri"`
}

// handleMcpResourcesList returns the cached resource listing for a server.
func handleMcpResourcesList(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"resources": []any{}})
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	resources, err := h.Mcp.ListResources(p.ID)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"resources": resources})
}

// handleMcpResourceRead fetches one resource's content.
func handleMcpResourceRead(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpResourceReadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.URI == "" {
		return errorResp(id, CodeInvalidParams, "uri required", nil)
	}
	contents, err := h.Mcp.ReadResource(ctx, p.ID, p.URI)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"contents": contents})
}

// handleMcpPromptsList returns the cached prompt listing for a server.
func handleMcpPromptsList(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return successResp(id, map[string]any{"prompts": []any{}})
	}
	var p McpServerIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	prompts, err := h.Mcp.ListPrompts(p.ID)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"prompts": prompts})
}

type McpPromptGetParams struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// handleMcpPromptGet renders one prompt template.
func handleMcpPromptGet(ctx context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoSessionRuntime, "mcp registry not configured", nil)
	}
	var p McpPromptGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.Name == "" {
		return errorResp(id, CodeInvalidParams, "name required", nil)
	}
	messages, err := h.Mcp.GetPrompt(ctx, p.ID, p.Name, p.Arguments)
	if err != nil {
		return errorResp(id, CodeInternalError, err.Error(), nil)
	}
	return successResp(id, map[string]any{"messages": messages})
}
