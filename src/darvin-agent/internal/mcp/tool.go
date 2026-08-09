// Tool descriptors: the tools/list and tools/call payload shapes the
// client returns and the registry re-exposes to the gateway.

package mcp

// ToolDescriptor is one tool the server exposes via `tools/list`. Input
// schema stays generic; the registry turns it into a typed schema.
type ToolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallToolResult is the typed payload of `tools/call`. The MCP spec
// allows a richer content union (text / image / audio / resource);
// widen as needed.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is one block inside CallToolResult.Content. Type is a
// discriminator the renderer uses to pick a UI affordance.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
