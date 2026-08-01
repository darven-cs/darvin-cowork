// Package anthropic implements the ModelProvider contract against the
// Anthropic Messages API (https://api.anthropic.com/v1/messages).
//
// The package is intentionally small: convert.go owns the request /
// response shape, stream.go owns SSE parsing, provider.go wires the two
// together and exposes the *Provider type.
package anthropic

import (
	"encoding/json"
	"strings"

	"darvin-cowork/backend/internal/agent/llm"
)

// anthropicVersion is the API version header value. Anthropic pins this
// rather than negotiating it per request.
const anthropicVersion = "2023-06-01"

// messagesPath is the v1/messages endpoint suffix appended to BaseURL.
const messagesPath = "/v1/messages"

// buildRequest converts the unified CompletionRequest into the Anthropic
// request body shape.
//
// The stream flag is set when req.Stream is true; callers that want a
// streaming response pass that explicitly.
func buildRequest(req *llm.CompletionRequest, stream bool) (map[string]any, error) {
	if req.Model == "" {
		return nil, errInvalidRequest("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errInvalidRequest("at least one message is required")
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": convertMessages(req.Messages),
		"stream":   stream,
	}

	if req.MaxTokens > 0 {
		out["max_tokens"] = req.MaxTokens
	} else {
		// Anthropic requires max_tokens even for non-streaming calls.
		// Fall back to a safe default; the caller should set this explicitly
		// but we don't want to ship 400s for absent defaults.
		out["max_tokens"] = 1024
	}

	if req.System != "" {
		out["system"] = req.System
	}
	if req.Temperature > 0 {
		out["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		out["top_p"] = req.TopP
	}
	if req.TopK > 0 {
		out["top_k"] = req.TopK
	}
	if len(req.StopSequences) > 0 {
		out["stop_sequences"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if req.ToolChoice.Type != "" {
		out["tool_choice"] = convertToolChoice(req.ToolChoice)
	}
	if req.Extra != nil {
		for k, v := range req.Extra {
			out[k] = v
		}
	}
	return out, nil
}

// convertMessages walks the unified messages and produces the Anthropic
// "messages" array.
//
// The shape is { role: "user"|"assistant", content: <text or array> }.
// Tool calls on assistant messages are expanded into the Anthropic
// content_block shape; tool results are emitted on a user message as
// tool_result blocks.
func convertMessages(msgs []llm.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	// pendingToolResults 累积连续的工具结果。Anthropic 要求紧随 tool_use
	// 的 user 消息必须一次性携带全部 tool_result block——拆成多条会触发
	// 400 invalid_request_error（"tool_use ids ... without tool_result"）。
	var pendingToolResults []map[string]any
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		out = append(out, map[string]any{
			"role":    "user",
			"content": pendingToolResults,
		})
		pendingToolResults = nil
	}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			// Anthropic takes system as a top-level field, not in the
			// messages array; the caller is responsible for routing it
			// via req.System. We drop it here to avoid duplicate injection.
			continue
		case llm.RoleTool:
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			})
		case llm.RoleUser:
			flushToolResults()
			out = append(out, map[string]any{
				"role":    "user",
				"content": m.Content,
			})
		case llm.RoleAssistant:
			flushToolResults()
			blocks := make([]map[string]any, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": tc.Arguments,
				})
			}
			out = append(out, map[string]any{
				"role":    "assistant",
				"content": blocks,
			})
		default:
			// Unknown role: skip silently to avoid breaking a streaming run.
			// Validation belongs in buildRequest at a higher level if needed.
		}
	}
	flushToolResults()
	return out
}

// convertTools maps unified Tool definitions to the Anthropic tools array.
func convertTools(tools []llm.Tool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return nil, errInvalidRequest("tool.name is required")
		}
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.Parameters,
		})
	}
	return out, nil
}

// convertToolChoice normalises the unified ToolChoice into the Anthropic
// tool_choice object. Empty Type means "let the provider default"; we do
// not emit the field in that case.
func convertToolChoice(tc llm.ToolChoice) map[string]any {
	out := map[string]any{"type": tc.Type}
	if tc.Type == "tool" && tc.Name != "" {
		out["name"] = tc.Name
	}
	return out
}

// parseResponse converts a non-streaming Anthropic response body into the
// unified CompletionResponse. The Anthropic shape is:
//
//	{
//	  "id": "msg_...",
//	  "type": "message",
//	  "role": "assistant",
//	  "content": [{"type":"text","text":"..."} | {"type":"tool_use",...}],
//	  "model": "claude-...",
//	  "stop_reason": "end_turn" | "max_tokens" | "tool_use" | "stop_sequence",
//	  "usage": {"input_tokens":N,"output_tokens":M}
//	}
func parseResponse(body []byte) (*llm.CompletionResponse, error) {
	var raw struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errInvalidRequest("unmarshal response: " + err.Error())
	}

	resp := &llm.CompletionResponse{
		Model:        raw.Model,
		FinishReason: mapStopReason(raw.StopReason),
		Usage: llm.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		},
	}
	for _, b := range raw.Content {
		switch b.Type {
		case "text":
			// Concatenate text blocks in case the provider emits more than one.
			resp.Content += b.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: b.Input,
			})
		}
	}
	return resp, nil
}

// mapStopReason normalises Anthropic's stop_reason onto the unified
// FinishReason vocabulary.
func mapStopReason(s string) llm.FinishReason {
	switch s {
	case "end_turn", "stop_sequence":
		return llm.FinishReasonStop
	case "max_tokens":
		return llm.FinishReasonLength
	case "tool_use":
		return llm.FinishReasonToolCalls
	default:
		// Treat unknown stop reasons as generic stop; provider-specific
		// diagnostics should live in the log layer.
		return llm.FinishReasonStop
	}
}

// errInvalidRequest builds a ProviderError with ErrCodeInvalidRequest.
func errInvalidRequest(msg string) error {
	return llm.NewProviderError("anthropic", llm.ErrCodeInvalidRequest, msg, 0, nil)
}

// headerValue is a tiny helper to keep the header map tidy.
func headerValue(v string) string { return strings.TrimSpace(v) }
