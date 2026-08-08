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
	CreatedAt        int64                      `json:"createdAt"`
	UpdatedAt        int64                      `json:"updatedAt"`
	LaunchStatus     string                     `json:"launchStatus,omitempty"`
	LaunchError      string                     `json:"launchError,omitempty"`
	ConnectionStatus string                     `json:"connectionStatus,omitempty"`
	ConnectionError  string                     `json:"connectionError,omitempty"`
	ExposedTools     []McpServerExposedToolWire `json:"exposedTools,omitempty"`
}

type McpServerExposedToolWire struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
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
	InstalledAt       *int64            `json:"installedAt,omitempty"`
	ResolvedAt        *int64            `json:"resolvedAt,omitempty"`
	UpdatedAt         int64             `json:"updatedAt"`
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
		w.ConnectionError = st.ConnectionError
	} else if st.Resolving {
		w.ConnectionStatus = string(mcp.ConnectionConnecting)
	} else {
		w.ConnectionStatus = string(mcp.ConnectionDisconnected)
	}
	if st.Resolution != nil {
		w.LaunchStatus = string(st.Resolution.Status)
		w.LaunchError = st.Resolution.Error
	}
	if len(st.Tools) > 0 {
		w.ExposedTools = make([]McpServerExposedToolWire, 0, len(st.Tools))
		for _, t := range st.Tools {
			w.ExposedTools = append(w.ExposedTools, McpServerExposedToolWire{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	return w
}

func wireFromResolution(r mcp.LaunchResolution) McpLaunchResolutionWire {
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
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
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
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
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
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
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
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
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
		"error": errMsg,
	}
	if len(tools) > 0 {
		wire := make([]McpServerExposedToolWire, 0, len(tools))
		for _, t := range tools {
			wire = append(wire, McpServerExposedToolWire{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		resp["tools"] = wire
	}
	return successResp(id, resp)
}

func handleMcpRetryResolution(id json.RawMessage, params json.RawMessage, h *Handler) *Response {
	if h.Mcp == nil {
		return errorResp(id, CodeNoAgentLoopSession, "mcp registry not configured", nil)
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
