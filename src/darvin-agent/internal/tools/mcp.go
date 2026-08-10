// Bridges connected MCP servers into the tool registry as KindMcp tools.

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"darvin-cowork/backend/internal/jsonschema"
	"darvin-cowork/backend/internal/mcp"
)

// McpToolSource is the subset of the MCP registry the plugin needs. The
// concrete *mcp.Registry satisfies it; tests inject a fake.
type McpToolSource interface {
	List() []mcp.ServerStatus
	GetSpec(serverID string) (mcp.ServerSpec, bool)
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
			canon := jsonschema.CanonicalizeSchema(raw)
			if err := jsonschema.ValidateToolSchema(canon); err != nil {
				slog.Info("mcp tool schema invalid, skipped",
					"server", status.ServerID,
					"tool", td.Name,
					"error", truncate(err.Error(), 200))
				continue
			}
			var stored = canon
			if len(stored) == 0 {
				stored = json.RawMessage(`{"type":"object"}`)
			}
			var trust string
			if spec, ok := p.source.GetSpec(status.ServerID); ok {
				trust = spec.EffectiveTrustLevel()
				// First-party bundled servers are trusted by default so the
				// filesystem tools do not prompt on every call; third-party
				// servers default to ask.
				if spec.IsBuiltIn && spec.TrustLevel == "" {
					trust = mcp.TrustTrusted
				}
			} else {
				trust = mcp.TrustAsk
			}
			var ann *mcp.ToolAnnotation
			if td.Annotations != nil {
				ann = td.Annotations
			}
			t := &McpTool{
				serverID:      status.ServerID,
				toolDesc:      td,
				source:        p.source,
				parametersRaw: stored,
				trustLevel:    trust,
				annotations:   ann,
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
// server's client. It implements DangerClassifier so the executor's
// permission gate can prompt for tools that mutate external state.
type McpTool struct {
	serverID      string
	toolDesc      mcp.ToolDescriptor
	source        McpToolSource
	parametersRaw json.RawMessage // canonical+validated bytes, set at Register
	// trustLevel is the owning server's resolved policy at Register time
	// ("trusted" skips approval, "ask" prompts for non-read-only tools).
	trustLevel string
	// annotations is the server-declared safety block, nil when the server
	// did not send one (2024-11-05 protocol).
	annotations *mcp.ToolAnnotation
}

// mcpResultCap bounds how much of a tools/call result is retained for the
// agent context. Servers can return arbitrarily large payloads; anything
// past the cap is dropped with a truncation marker so the LLM sees the
// budgeted slice and knows more was available.
const mcpResultCap = 256 << 10 // 256 KiB

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
// result so the agent loop reports them to the LLM. The returned content is
// capped at mcpResultCap; overflow is dropped and a truncation marker
// appended so an oversized payload never floods the agent context.
func (t *McpTool) Execute(ctx context.Context, args map[string]any) Result {
	res, err := t.source.CallTool(ctx, t.serverID, t.toolDesc.Name, args)
	if err != nil {
		return Result{Content: err.Error(), IsError: true}
	}
	lw := &limitWriter{cap: mcpResultCap}
	for _, c := range res.Content {
		_, _ = lw.Write([]byte(c.Text))
	}
	content := lw.String()
	if lw.Truncated() {
		content += fmt.Sprintf("\n[mcp result truncated: %d bytes total, kept %d]", lw.Len(), mcpResultCap)
	}
	return Result{Content: content, IsError: res.IsError}
}

// ClassifyDanger implements DangerClassifier. A server marked "trusted"
// passes every tool through; otherwise a tool that is annotated destructive
// is blocked behind approval, a tool with no readOnlyHint is treated as
// caution (it may mutate external state), and a read-only tool passes.
func (t *McpTool) ClassifyDanger(_ map[string]any) (level, reason string, need bool) {
	if t.trustLevel == mcp.TrustTrusted {
		return "safe", "", false
	}
	if t.annotations != nil && t.annotations.DestructiveHint != nil && *t.annotations.DestructiveHint {
		return "destructive", "MCP tool declares destructiveHint: " + t.Name(), true
	}
	if t.annotations != nil && t.annotations.ReadOnlyHint != nil && *t.annotations.ReadOnlyHint {
		return "safe", "", false
	}
	return "caution", "MCP tool has no readOnlyHint (may mutate external state): " + t.Name(), true
}

// truncate returns s truncated to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
