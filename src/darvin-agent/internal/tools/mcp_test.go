package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"darvin-cowork/backend/internal/mcp"
)

type fakeMcpSource struct {
	servers []mcp.ServerStatus
	callRes *mcp.CallToolResult
	callErr error
}

func (f *fakeMcpSource) List() []mcp.ServerStatus { return f.servers }
func (f *fakeMcpSource) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.callRes, nil
}

func mcpToolDescriptor(name string) mcp.ToolDescriptor {
	return mcp.ToolDescriptor{
		Name:        name,
		Description: "desc " + name,
		InputSchema: map[string]any{"type": "object"},
	}
}

func TestMcpPluginRegister(t *testing.T) {
	src := &fakeMcpSource{servers: []mcp.ServerStatus{
		{
			ServerID:  "filesystem",
			Connected: true,
			Tools:     []mcp.ToolDescriptor{mcpToolDescriptor("read_file"), mcpToolDescriptor("write_file"), mcpToolDescriptor("list_directory")},
		},
	}}
	reg := NewRegistry()
	p := NewMcpPlugin(src)
	if err := p.Register(reg); err != nil {
		t.Fatal(err)
	}
	if got := reg.Get("mcp__filesystem__read_file"); got == nil {
		t.Fatal("mcp__filesystem__read_file not registered")
	}
	entry, ok := reg.GetEntry("mcp__filesystem__list_directory")
	if !ok {
		t.Fatal("GetEntry(mcp__filesystem__list_directory) missing")
	}
	if entry.Kind != KindMcp {
		t.Errorf("Kind = %q, want mcp", entry.Kind)
	}
	if entry.Metadata["mcpServerID"] != "filesystem" {
		t.Errorf("Metadata[mcpServerID] = %v, want filesystem", entry.Metadata["mcpServerID"])
	}
	if entry.Metadata["mcpToolName"] != "list_directory" {
		t.Errorf("Metadata[mcpToolName] = %v, want list_directory", entry.Metadata["mcpToolName"])
	}
}

func TestMcpPluginRegisterSkipsDisconnected(t *testing.T) {
	src := &fakeMcpSource{servers: []mcp.ServerStatus{
		{ServerID: "offline", Connected: false, Tools: []mcp.ToolDescriptor{mcpToolDescriptor("read_file")}},
	}}
	reg := NewRegistry()
	p := NewMcpPlugin(src)
	if err := p.Register(reg); err != nil {
		t.Fatal(err)
	}
	if n := len(reg.List()); n != 0 {
		t.Errorf("len(List()) = %d, want 0 for disconnected server", n)
	}
}

func TestMcpPluginUnregister(t *testing.T) {
	src := &fakeMcpSource{servers: []mcp.ServerStatus{
		{ServerID: "fs", Connected: true, Tools: []mcp.ToolDescriptor{mcpToolDescriptor("read_file")}},
	}}
	reg := NewRegistry()
	p := NewMcpPlugin(src)
	if err := p.Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := p.Unregister(reg); err != nil {
		t.Fatal(err)
	}
	if reg.Get("mcp__fs__read_file") != nil {
		t.Error("mcp tool survives Unregister")
	}
}

func TestMcpToolName(t *testing.T) {
	mt := &McpTool{serverID: "filesystem", toolDesc: mcpToolDescriptor("read_file")}
	if got := mt.Name(); got != "mcp__filesystem__read_file" {
		t.Errorf("Name() = %q, want mcp__filesystem__read_file", got)
	}
}

func TestMcpToolExecuteSuccess(t *testing.T) {
	src := &fakeMcpSource{
		callRes: &mcp.CallToolResult{
			Content: []mcp.ToolContent{
				{Type: "text", Text: "file contents"},
			},
		},
	}
	mt := &McpTool{serverID: "filesystem", toolDesc: mcpToolDescriptor("read_file"), source: src}
	res := mt.Execute(context.Background(), map[string]any{"path": "/tmp/foo"})
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if res.Content != "file contents" {
		t.Errorf("Content = %q, want file contents", res.Content)
	}
}

func TestMcpToolExecuteError(t *testing.T) {
	src := &fakeMcpSource{callErr: errors.New("transport closed")}
	mt := &McpTool{serverID: "filesystem", toolDesc: mcpToolDescriptor("read_file"), source: src}
	res := mt.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Error("expected IsError on transport failure")
	}
}

func TestMcpToolParametersFallback(t *testing.T) {
	mt := &McpTool{serverID: "fs", toolDesc: mcp.ToolDescriptor{Name: "x", InputSchema: map[string]any{}}}
	raw := mt.Parameters()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters() must be valid JSON: %v (raw=%s)", err, raw)
	}
	if m["type"] != "object" {
		t.Errorf("type = %q, want object fallback", m["type"])
	}
}

func TestMcpToolParametersValidTypePreserved(t *testing.T) {
	mt := &McpTool{serverID: "gh", toolDesc: mcp.ToolDescriptor{
		Name:        "create_issue",
		InputSchema: map[string]any{"type": "object"},
	}}
	raw := mt.Parameters()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters() must be valid JSON: %v (raw=%s)", err, raw)
	}
	if m["type"] != "object" {
		t.Errorf("type = %q, want object", m["type"])
	}
}

func TestMcpToolParametersMissingRootTypeFilled(t *testing.T) {
	// CanonicalizeSchema fills missing root types as "object"; Parameters()
	// returns the cached canonical bytes, so missing types become object.
	mt := &McpTool{serverID: "gh", toolDesc: mcp.ToolDescriptor{
		Name:        "bare",
		InputSchema: map[string]any{}, // no root type — CanonicalizeSchema adds object
	}}
	raw := mt.Parameters()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters() must be valid JSON: %v (raw=%s)", err, raw)
	}
	if m["type"] != "object" {
		t.Errorf("type = %q, want object (filled by CanonicalizeSchema)", m["type"])
	}
}

// TestMcpPluginRegister_FiltersInvalidSchemas verifies the new filter behavior.
// Tools whose schemas fail ValidateToolSchema (e.g. invalid items.type:"")
// are silently skipped during Register so they never reach the LLM.
func TestMcpPluginRegister_FiltersInvalidSchemas(t *testing.T) {
	src := &fakeMcpSource{servers: []mcp.ServerStatus{
		{
			ServerID:  "gh",
			Connected: true,
			Tools: []mcp.ToolDescriptor{
				{Name: "good", InputSchema: map[string]any{"type": "object"}},
				// Invalid: root type is "" — would 400 the LLM.
				{Name: "bad_root", InputSchema: map[string]any{"type": ""}},
				// Invalid: nested items.type is "" — DeepSeek-style filter.
				{Name: "bad_items", InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": ""},
						},
					},
				}},
			},
		},
	}}
	reg := NewRegistry()
	p := NewMcpPlugin(src)
	if err := p.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Only "good" should be registered; "bad_root" and "bad_items" are filtered out.
	if reg.Get("mcp__gh__good") == nil {
		t.Fatal("mcp__gh__good should be registered (valid schema)")
	}
	if reg.Get("mcp__gh__bad_root") != nil {
		t.Error("mcp__gh__bad_root must be filtered out (root type is \"\")")
	}
	if reg.Get("mcp__gh__bad_items") != nil {
		t.Error("mcp__gh__bad_items must be filtered out (nested items.type is \"\")")
	}
}

// TestMcpToolParametersInvalidNestedSchema_PassesThroughAsIs documents the new
// contract: Parameters() returns the cached canonical+validated bytes that
// Register stored on the tool. Since Register filters out invalid schemas
// before the McpTool is constructed, by the time Parameters() is called the
// tool would already be gone — and the cached bytes faithfully preserve the
// original schema verbatim. This test guards against re-introducing a hack
// that mutates the schema on read.
func TestMcpToolParametersInvalidNestedSchema_PassesThroughAsIs(t *testing.T) {
	// Build the canonical+invalid bytes the way Register would have stored
	// them if the filter had not rejected the tool. The bytes must contain
	// the bad items.type "" verbatim — Parameters() does not rewrite them.
	invalid := []byte(`{"type":"object","properties":{"comments":{"type":"array","items":{"type":""}}}}`)
	mt := &McpTool{
		serverID:      "gh",
		toolDesc:      mcp.ToolDescriptor{Name: "create_pull_request_review"},
		parametersRaw: invalid,
	}
	raw := mt.Parameters()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters() must be valid JSON: %v (raw=%s)", err, raw)
	}
	props, _ := m["properties"].(map[string]any)
	comments, _ := props["comments"].(map[string]any)
	items, _ := comments["items"].(map[string]any)
	if items["type"] != "" {
		t.Errorf("nested items.type = %v, want \"\" (Parameters() must not rewrite cached bytes)", items["type"])
	}
}

// TestMcpToolParametersRawPreservesAnyOf is the regression test for the
// GitHub MCP `create_pull_request_review` bug: schemas with comments.items.anyOf
// must round-trip end-to-end so Anthropic accepts the input_schema. Going
// through a strict whitelist struct would silently drop anyOf and re-emit
// {"items":{"type":""}}, which Anthropic rejects with invalid_request_error.
func TestMcpToolParametersRawPreservesAnyOf(t *testing.T) {
	canon := []byte(`{"type":"object","properties":{"comments":{"type":"array","items":{"anyOf":[{"type":"object","properties":{"path":{"type":"string"},"position":{"type":"integer"}}},{"type":"object","properties":{"body":{"type":"string"},"line":{"type":"integer"}}}]}}}}`)
	mt := &McpTool{
		serverID:      "gh",
		toolDesc:      mcp.ToolDescriptor{Name: "create_pull_request_review"},
		parametersRaw: canon,
	}
	raw := mt.Parameters()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters() must be valid JSON: %v (raw=%s)", err, raw)
	}
	props, _ := m["properties"].(map[string]any)
	comments, _ := props["comments"].(map[string]any)
	items, _ := comments["items"].(map[string]any)
	anyOf, ok := items["anyOf"].([]any)
	if !ok {
		t.Fatalf("items.anyOf missing or wrong type: %T (raw=%s)", items["anyOf"], raw)
	}
	if len(anyOf) != 2 {
		t.Errorf("items.anyOf length = %d, want 2 (anyOf construct must survive the round-trip)", len(anyOf))
	}
}
