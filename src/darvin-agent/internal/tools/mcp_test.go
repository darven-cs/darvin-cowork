package tool

import (
	"context"
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
	ps := mt.Parameters()
	if ps.Type != "object" {
		t.Errorf("Type = %q, want object fallback", ps.Type)
	}
}
