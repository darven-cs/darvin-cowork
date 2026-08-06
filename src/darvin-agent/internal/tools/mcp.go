package tool

import (
	"context"
	"encoding/json"
	"log/slog"

	"darvin-cowork/backend/internal/mcp"
	"darvin-cowork/backend/internal/provider"
)

// McpToolSource is the subset of the MCP registry the plugin needs. The
// concrete *mcp.Registry satisfies it; tests inject a fake.
type McpToolSource interface {
	List() []mcp.ServerStatus
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*mcp.CallToolResult, error)
}

// McpPlugin exposes the tools of every connected MCP server as KindMcp
// tools named mcp__<server>__<tool>. Register is meant to be re-run when a
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

// Register adds one McpTool per connected server's tool. Tools whose schemas
// fail CanonicalizeSchema + ValidateToolSchema are silently skipped — the MCP
// server may expose other valid tools; a single broken schema should not
// remove the whole server from the tool surface.
func (p *McpPlugin) Register(reg ToolRegistrar) error {
	for _, status := range p.source.List() {
		if !status.Connected {
			continue
		}
		for _, td := range status.Tools {
			raw, err := json.Marshal(td.InputSchema)
			if err != nil {
				continue
			}
			canon := provider.CanonicalizeSchema(raw)
			if err := provider.ValidateToolSchema(canon); err != nil {
				slog.Info("mcp tool schema invalid, skipped",
					"server", status.ServerID,
					"tool", td.Name,
					"error", truncate(err.Error(), 200))
				continue
			}
			var stored json.RawMessage = canon
			if len(stored) == 0 {
				stored = json.RawMessage(`{"type":"object"}`)
			}
			t := &McpTool{
				serverID:     status.ServerID,
				toolDesc:     td,
				source:       p.source,
				parametersRaw: stored,
			}
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
	serverID      string
	toolDesc      mcp.ToolDescriptor
	source        McpToolSource
	parametersRaw json.RawMessage // canonical+validated bytes, set at Register
}

// mcpToolName maps a server + tool to its tool name. The separator is
// double-underscore (mcp__<server>__<tool>) so the name only contains
// [a-zA-Z0-9_-], which Anthropic's API requires for tool names.
func mcpToolName(serverID, toolName string) string {
	return "mcp__" + serverID + "__" + toolName
}

// Name returns the tool name (mcp__<server>__<tool>).
func (t *McpTool) Name() string { return mcpToolName(t.serverID, t.toolDesc.Name) }

// Description returns the MCP tool's description for the LLM.
func (t *McpTool) Description() string { return t.toolDesc.Description }

// Parameters returns the canonical+validated JSON Schema bytes cached at
// Register time. Tools with structurally broken schemas are filtered out
// during Register; this method only sees valid schemas. The raw bytes
// preserve every JSON Schema construct (anyOf, $ref, nested properties,
// additionalProperties on items) so nothing is silently truncated.
func (t *McpTool) Parameters() json.RawMessage {
	if len(t.parametersRaw) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.parametersRaw
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

// truncate returns s truncated to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
