// Tests for OpenAI-compatible request / response conversion.

package openai

import (
	"encoding/json"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

func TestBuildRequest_RequiredFields(t *testing.T) {
	_, err := buildRequest(&llm.CompletionRequest{Model: ""}, false)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	_, err = buildRequest(&llm.CompletionRequest{Model: "gpt-4o"}, false)
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestBuildRequest_StreamFlag(t *testing.T) {
	req := &llm.CompletionRequest{
		Model:    "gpt-4o",
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

func TestConvertMessages_SystemFirst(t *testing.T) {
	got := convertMessages([]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, "sys prompt")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0]["role"] != "system" || got[0]["content"] != "sys prompt" {
		t.Errorf("first message = %+v, want system", got[0])
	}
}

func TestConvertMessages_UserWithImage(t *testing.T) {
	got := convertMessages([]llm.Message{{
		Role:    llm.RoleUser,
		Content: "what is this?",
		Images:  []llm.ImageBlock{{MediaType: "image/png", Data: "aGk="}},
	}}, "")
	content, ok := got[0]["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content type %T, want []map[string]any", got[0]["content"])
	}
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2 (image + text)", len(content))
	}
	url, ok := content[0]["image_url"].(map[string]any)["url"].(string)
	if !ok || url != "data:image/png;base64,aGk=" {
		t.Errorf("image_url = %v, want data URL", content[0])
	}
}

func TestConvertMessages_ToolRoundTrip(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "beijing"}},
		}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "sunny"},
	}
	got := convertMessages(msgs, "")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	calls, ok := got[0]["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %+v, want 1 entry", got[0])
	}
	fn := calls[0]["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", fn["name"])
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(fn["arguments"].(string)), &args); err != nil {
		t.Fatalf("arguments not JSON: %v", err)
	}
	if args["city"] != "beijing" {
		t.Errorf("args = %v, want city=beijing", args)
	}
	if got[1]["role"] != "tool" || got[1]["tool_call_id"] != "call_1" {
		t.Errorf("tool result = %+v, want role=tool", got[1])
	}
}

func TestConvertToolChoice(t *testing.T) {
	cases := []struct {
		in   llm.ToolChoice
		want any
	}{
		{llm.ToolChoice{}, nil},
		{llm.ToolChoice{Type: "auto"}, "auto"},
		{llm.ToolChoice{Type: "none"}, "none"},
		{llm.ToolChoice{Type: "any"}, "required"},
		{llm.ToolChoice{Type: "tool", Name: "get_weather"}, map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}},
	}
	for _, c := range cases {
		got := convertToolChoice(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("ToolChoice{%q} = %+v, want nil", c.in.Type, got)
			}
			continue
		}
		if w, ok := c.want.(map[string]any); ok {
			g, ok := got.(map[string]any)
			if !ok {
				t.Errorf("ToolChoice{%q} = %T, want map", c.in.Type, got)
				continue
			}
			if g["type"] != w["type"] {
				t.Errorf("ToolChoice{%q} type = %v, want %v", c.in.Type, g["type"], w["type"])
			}
			continue
		}
		if got != c.want {
			t.Errorf("ToolChoice{%q} = %v, want %v", c.in.Type, got, c.want)
		}
	}
}

func TestParseResponse_ToolCall(t *testing.T) {
	body := `{
		"id": "chatcmpl-1",
		"model": "gpt-4o",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "", "tool_calls": [{
				"id": "call_9",
				"type": "function",
				"function": {"name": "search", "arguments": "{\"q\":\"mcp\"}"}
			}]},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`
	resp, err := parseResponse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.FinishReason != llm.FinishReasonToolCalls {
		t.Errorf("finish_reason = %v, want tool_calls", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "search" {
		t.Fatalf("tool_calls = %+v, want 1 search", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments["q"] != "mcp" {
		t.Errorf("args = %v, want q=mcp", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}
