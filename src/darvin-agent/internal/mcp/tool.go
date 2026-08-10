// Tool descriptors: the tools/list and tools/call payload shapes the
// client returns and the registry re-exposes to the gateway.

package mcp

// ToolDescriptor is one tool the server exposes via `tools/list`. Input
// schema stays generic; the registry turns it into a typed schema.
// Annotations carries the MCP 2025-03-26 tool safety hints; servers on the
// older 2024-11-05 protocol simply omit them (nil).
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations *ToolAnnotation `json:"annotations,omitempty"`
}

// ToolAnnotation is the tool-level safety block the MCP spec lets servers
// declare. The three hint pointers distinguish "explicitly false" from
// "not declared" — a nil hint means the server said nothing about it.
type ToolAnnotation struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
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
