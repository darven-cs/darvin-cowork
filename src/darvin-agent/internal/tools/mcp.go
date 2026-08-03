package tool

import (
	"context"
	"encoding/json"

	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/mcp"
)

// McpToolSource is the subset of the MCP registry the plugin needs. The
// concrete *mcp.Registry satisfies it; tests inject a fake.
type McpToolSource interface {
	List() []mcp.ServerStatus
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*mcp.CallToolResult, error)
}

// McpPlugin exposes the tools of every connected MCP server as KindMcp
// tools named mcp:<server>:<tool>. Register is meant to be re-run when a
// server connects or disconnects so the tool surface matches live state.
type McpPlugin struct {
	pluginID string
	source   McpToolSource
}

// NewMcpPlugin builds a plugin over an MCP tool source.
func NewMcpPlugin(src McpToolSource) *McpPlugin {
	return &McpPlugin{pluginID: "mcp", source: src}
}

// PluginID returns the owning identifier used for UnregisterByPlugin.
func (p *McpPlugin) PluginID() string { return p.pluginID }

// Register adds one McpTool per tool of each connected server.
func (p *McpPlugin) Register(reg ToolRegistrar) error {
	for _, status := range p.source.List() {
		if !status.Connected {
			continue
		}
		for _, td := range status.Tools {
			t := &McpTool{serverID: status.ServerID, toolDesc: td, source: p.source}
			if err := reg.RegisterTool(t, KindMcp, map[string]any{
				"pluginID":    p.pluginID,
				"mcpServerID": status.ServerID,
				"mcpToolName": td.Name,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unregister removes every MCP tool this plugin owns.
func (p *McpPlugin) Unregister(reg ToolRegistrar) error {
	return reg.UnregisterByPlugin(p.pluginID)
}

// McpTool adapts a single MCP tool descriptor to the Tool interface.
// Execute forwards to the MCP source, which routes to the owning
// server's client.
type McpTool struct {
	serverID string
	toolDesc mcp.ToolDescriptor
	source   McpToolSource
}

// mcpToolName maps a server + tool to its tool name.
func mcpToolName(serverID, toolName string) string { return "mcp:" + serverID + ":" + toolName }

// Name returns the tool name (mcp:<server>:<tool>).
func (t *McpTool) Name() string { return mcpToolName(t.serverID, t.toolDesc.Name) }

// Description returns the MCP tool's description for the LLM.
func (t *McpTool) Description() string { return t.toolDesc.Description }

// Parameters converts the MCP JSON schema to the provider schema. The
// conversion is best-effort: fields that do not map onto the minimal
// schema are dropped, and an unconvertible schema falls back to a bare
// object so the LLM still sees the tool.
func (t *McpTool) Parameters() protocol.ParameterSchema {
	if len(t.toolDesc.InputSchema) == 0 {
		return protocol.ParameterSchema{Type: "object"}
	}
	b, err := json.Marshal(t.toolDesc.InputSchema)
	if err != nil {
		return protocol.ParameterSchema{Type: "object"}
	}
	var ps protocol.ParameterSchema
	if err := json.Unmarshal(b, &ps); err != nil {
		return protocol.ParameterSchema{Type: "object"}
	}
	return ps
}

// Execute calls the MCP server. Transport errors surface as an IsError
// result so the agent loop reports them to the LLM.
func (t *McpTool) Execute(ctx context.Context, args map[string]any) Result {
	res, err := t.source.CallTool(ctx, t.serverID, t.toolDesc.Name, args)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	var content string
	for _, c := range res.Content {
		content += c.Text
	}
	return Result{Content: content, IsError: res.IsError}
}
