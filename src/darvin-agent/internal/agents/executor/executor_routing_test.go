package executor

import (
	"context"
	"testing"
	"time"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
	tool "darvin-cowork/backend/internal/tools"
)

// toolUseScript drives one tool call then a stop turn, matching the
// existing multi-turn test fixture.
func toolUseScript() [][]llm.StreamEvent {
	return [][]llm.StreamEvent{
		{
			llm.StartEvent{},
			llm.ToolCallStartEvent{ID: "c1", Name: "echo"},
			llm.ToolCallEndEvent{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "hi"}},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonToolCalls}},
		},
		{
			llm.StartEvent{},
			llm.TextDeltaEvent{Delta: "done"},
			llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
		},
	}
}

// captureToolEvents runs one conversation and returns the first tool_start
// and tool_end events observed, if any.
func captureToolEvents(t *testing.T, d *fakeDeps) (event.ToolStartEvent, event.ToolEndEvent, bool, bool) {
	t.Helper()
	sub := d.bus.Subscribe(64)
	defer sub.Unsubscribe()
	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	var ts event.ToolStartEvent
	var te event.ToolEndEvent
	seenStart, seenEnd := false, false
	timeout := time.After(time.Second)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return ts, te, seenStart, seenEnd
			}
			switch e := ev.(type) {
			case event.ToolStartEvent:
				ts, seenStart = e, true
			case event.ToolEndEvent:
				te, seenEnd = e, true
			}
		case <-timeout:
			return ts, te, seenStart, seenEnd
		}
	}
}

func TestDispatchBuiltin_KindField(t *testing.T) {
	prov := &scriptedProvider{script: toolUseScript()}
	d := newFakeDeps(t, prov, []tool.Tool{echoTool{}})
	ts, te, seenStart, seenEnd := captureToolEvents(t, d)
	if !seenStart || !seenEnd {
		t.Fatal("missing tool_start / tool_end events")
	}
	if ts.ToolKind != "builtin" {
		t.Errorf("ToolStart.ToolKind = %q, want builtin", ts.ToolKind)
	}
	if ts.SkillID != "" || ts.McpServerID != "" {
		t.Errorf("ToolStart kind ids = %q/%q, want empty", ts.SkillID, ts.McpServerID)
	}
	if te.ToolKind != "builtin" {
		t.Errorf("ToolEnd.ToolKind = %q, want builtin", te.ToolKind)
	}
}

func TestDispatchSkill_KindField(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.RegisterTool(&echoTool{}, tool.KindSkill, map[string]any{"skillID": "web-search"}); err != nil {
		t.Fatal(err)
	}
	d := newFakeDeps(t, &scriptedProvider{script: toolUseScript()}, nil)
	d.tools = reg
	ts, te, seenStart, seenEnd := captureToolEvents(t, d)
	if !seenStart || !seenEnd {
		t.Fatal("missing tool_start / tool_end events")
	}
	if ts.ToolKind != "skill" {
		t.Errorf("ToolStart.ToolKind = %q, want skill", ts.ToolKind)
	}
	if ts.SkillID != "web-search" {
		t.Errorf("ToolStart.SkillID = %q, want web-search", ts.SkillID)
	}
	if ts.McpServerID != "" {
		t.Errorf("ToolStart.McpServerID = %q, want empty", ts.McpServerID)
	}
	if te.ToolKind != "skill" || te.SkillID != "web-search" {
		t.Errorf("ToolEnd = kind %q skillID %q, want skill/web-search", te.ToolKind, te.SkillID)
	}
}

func TestDispatchMcp_KindField(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.RegisterTool(&echoTool{}, tool.KindMcp, map[string]any{
		"mcpServerID": "filesystem",
		"mcpToolName": "read_file",
	}); err != nil {
		t.Fatal(err)
	}
	d := newFakeDeps(t, &scriptedProvider{script: toolUseScript()}, nil)
	d.tools = reg
	ts, te, seenStart, seenEnd := captureToolEvents(t, d)
	if !seenStart || !seenEnd {
		t.Fatal("missing tool_start / tool_end events")
	}
	if ts.ToolKind != "mcp" {
		t.Errorf("ToolStart.ToolKind = %q, want mcp", ts.ToolKind)
	}
	if ts.McpServerID != "filesystem" {
		t.Errorf("ToolStart.McpServerID = %q, want filesystem", ts.McpServerID)
	}
	if ts.SkillID != "" {
		t.Errorf("ToolStart.SkillID = %q, want empty", ts.SkillID)
	}
	if te.ToolKind != "mcp" || te.McpServerID != "filesystem" {
		t.Errorf("ToolEnd = kind %q mcpServerID %q, want mcp/filesystem", te.ToolKind, te.McpServerID)
	}
}
