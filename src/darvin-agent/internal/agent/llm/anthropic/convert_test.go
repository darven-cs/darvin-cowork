package anthropic

import (
	"encoding/json"
	"reflect"
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

func TestBuildRequest_RequiredFields(t *testing.T) {
	_, err := buildRequest(&llm.CompletionRequest{Model: ""}, false)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	_, err = buildRequest(&llm.CompletionRequest{Model: "claude-x"}, false)
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestBuildRequest_DefaultsMaxTokens(t *testing.T) {
	// Anthropic rejects 0 / absent max_tokens; verify we default sensibly
	// instead of failing in the HTTP layer.
	req := &llm.CompletionRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}
	out, err := buildRequest(req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["max_tokens"] != 1024 {
		t.Errorf("default max_tokens = %v, want 1024", out["max_tokens"])
	}
}

func TestBuildRequest_StreamFlag(t *testing.T) {
	req := &llm.CompletionRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}
	out, err := buildRequest(req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["stream"] != true {
		t.Errorf("stream flag = %v, want true", out["stream"])
	}
}

func TestConvertMessages_User(t *testing.T) {
	got := convertMessages([]llm.Message{{Role: llm.RoleUser, Content: "hello"}})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0]["role"] != "user" || got[0]["content"] != "hello" {
		t.Errorf("got %+v", got[0])
	}
}

func TestConvertMessages_SystemDropped(t *testing.T) {
	// System must live on req.System, not in the messages array; convert
	// should silently drop it.
	got := convertMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: "be helpful"},
		{Role: llm.RoleUser, Content: "hi"},
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (system dropped)", len(got))
	}
	if got[0]["role"] != "user" {
		t.Errorf("got %+v", got[0])
	}
}

func TestConvertMessages_AssistantWithToolCall(t *testing.T) {
	got := convertMessages([]llm.Message{{
		Role:    llm.RoleAssistant,
		Content: "let me check",
		ToolCalls: []llm.ToolCall{{
			ID:        "t1",
			Name:      "get_weather",
			Arguments: map[string]any{"loc": "SF"},
		}},
	}})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	content, ok := got[0]["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content type %T", got[0]["content"])
	}
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2 (text + tool_use)", len(content))
	}
	if content[0]["type"] != "text" {
		t.Errorf("block 0 type = %v, want text", content[0]["type"])
	}
	if content[1]["type"] != "tool_use" {
		t.Errorf("block 1 type = %v, want tool_use", content[1]["type"])
	}
	if content[1]["name"] != "get_weather" {
		t.Errorf("block 1 name = %v", content[1]["name"])
	}
}

func TestConvertMessages_ToolResult(t *testing.T) {
	got := convertMessages([]llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: "t1",
		Content:    "72F sunny",
	}})
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0]["role"] != "user" {
		t.Errorf("role = %v, want user (tool result wrapped)", got[0]["role"])
	}
	content := got[0]["content"].([]map[string]any)
	if content[0]["type"] != "tool_result" {
		t.Errorf("type = %v", content[0]["type"])
	}
	if content[0]["tool_use_id"] != "t1" {
		t.Errorf("tool_use_id = %v", content[0]["tool_use_id"])
	}
}

func TestConvertMessages_MultipleToolResultsMerged(t *testing.T) {
	// Anthropic requires every tool_result for a turn's tool_use calls to
	// live in ONE user message immediately after the assistant message.
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "analyze"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "call_00", Name: "shell", Arguments: map[string]any{"command": "cat"}},
			{ID: "call_01", Name: "shell", Arguments: map[string]any{"command": "wc"}},
		}},
		{Role: llm.RoleTool, ToolCallID: "call_00", Content: "a"},
		{Role: llm.RoleTool, ToolCallID: "call_01", Content: "b"},
	}
	got := convertMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (user + assistant + merged tool_result)", len(got))
	}
	assistant := got[1]
	assistantBlocks := assistant["content"].([]map[string]any)
	if len(assistantBlocks) != 2 || assistantBlocks[0]["type"] != "tool_use" || assistantBlocks[1]["type"] != "tool_use" {
		t.Fatalf("assistant blocks = %+v, want 2 tool_use", assistantBlocks)
	}
	toolUser := got[2]
	if toolUser["role"] != "user" {
		t.Fatalf("tool result role = %v, want user", toolUser["role"])
	}
	blocks := toolUser["content"].([]map[string]any)
	if len(blocks) != 2 {
		t.Fatalf("tool_result blocks = %d, want 2 merged into one user message", len(blocks))
	}
	if blocks[0]["tool_use_id"] != "call_00" || blocks[1]["tool_use_id"] != "call_01" {
		t.Errorf("tool_result ids = %v / %v, want call_00 / call_01", blocks[0]["tool_use_id"], blocks[1]["tool_use_id"])
	}
}

func TestConvertTools_Empty(t *testing.T) {
	got, err := convertTools(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestConvertTools_RequiresName(t *testing.T) {
	_, err := convertTools([]llm.Tool{{Name: ""}})
	if err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestConvertTools_Shape(t *testing.T) {
	got, err := convertTools([]llm.Tool{{
		Name:        "f",
		Description: "do something",
		Parameters: llm.ParameterSchema{
			Type: "object",
			Properties: map[string]llm.ParameterProperty{
				"x": {Type: "string"},
			},
			Required: []string{"x"},
		},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0]["name"] != "f" {
		t.Errorf("name = %v", got[0]["name"])
	}
	if got[0]["input_schema"] == nil {
		t.Errorf("input_schema missing")
	}
}

func TestConvertToolChoice(t *testing.T) {
	got := convertToolChoice(llm.ToolChoice{Type: "tool", Name: "f"})
	if got["type"] != "tool" || got["name"] != "f" {
		t.Errorf("got %+v", got)
	}
	// Type without name: should not emit "name" key.
	got = convertToolChoice(llm.ToolChoice{Type: "auto"})
	if _, ok := got["name"]; ok {
		t.Errorf("auto choice should not include name, got %+v", got)
	}
}

func TestParseResponse_TextOnly(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant",
		"model":"claude-x",
		"content":[{"type":"text","text":"Hello!"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":5}
	}`)
	resp, err := parseResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("FinishReason = %v", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected zero tool calls, got %+v", resp.ToolCalls)
	}
}

func TestParseResponse_ToolUse(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant",
		"model":"claude-x",
		"content":[
			{"type":"text","text":"checking..."},
			{"type":"tool_use","id":"t1","name":"get_weather","input":{"location":"SF"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":10,"output_tokens":5}
	}`)
	resp, err := parseResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "checking..." {
		t.Errorf("Content = %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "t1" || tc.Name != "get_weather" {
		t.Errorf("ToolCall = %+v", tc)
	}
	if !reflect.DeepEqual(tc.Arguments, map[string]any{"location": "SF"}) {
		t.Errorf("Arguments = %+v", tc.Arguments)
	}
	if resp.FinishReason != llm.FinishReasonToolCalls {
		t.Errorf("FinishReason = %v", resp.FinishReason)
	}
}

func TestParseResponse_MultipleTextBlocks(t *testing.T) {
	// Some Anthropic versions emit multiple text blocks; verify they
	// concatenate rather than overwriting.
	body := []byte(`{
		"model":"claude-x","role":"assistant","type":"message",
		"content":[
			{"type":"text","text":"hello "},
			{"type":"text","text":"world"}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)
	resp, err := parseResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello world" {
		t.Errorf("Content = %q, want %q", resp.Content, "hello world")
	}
}

func TestMapStopReason(t *testing.T) {
	cases := map[string]llm.FinishReason{
		"end_turn":      llm.FinishReasonStop,
		"stop_sequence": llm.FinishReasonStop,
		"max_tokens":    llm.FinishReasonLength,
		"tool_use":      llm.FinishReasonToolCalls,
		"future_reason": llm.FinishReasonStop, // unknown → generic stop
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("mapStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildRequest_RoundTrip ensures the produced payload is valid JSON
// so downstream parsers don't choke on it.
func TestBuildRequest_RoundTrip(t *testing.T) {
	req := &llm.CompletionRequest{
		Model:       "claude-x",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		System:      "be brief",
		Temperature: 0.5,
		MaxTokens:   256,
		Tools: []llm.Tool{{
			Name:        "f",
			Description: "do x",
			Parameters: llm.ParameterSchema{
				Type: "object",
				Properties: map[string]llm.ParameterProperty{
					"q": {Type: "string"},
				},
			},
		}},
		ToolChoice: llm.ToolChoice{Type: "auto"},
	}
	out, err := buildRequest(req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	// Round-trip back into a generic map.
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if back["model"] != "claude-x" {
		t.Errorf("round-trip model = %v", back["model"])
	}
	if back["system"] != "be brief" {
		t.Errorf("round-trip system = %v", back["system"])
	}
}
