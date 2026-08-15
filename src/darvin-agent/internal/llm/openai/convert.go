// OpenAI-compatible Chat Completions request / response conversion.

package openai

import (
	"encoding/json"

	"darvin-cowork/backend/internal/llm"
)

// chatPath is the chat completions endpoint suffix appended to BaseURL.
const chatPath = "/chat/completions"

// buildRequest converts the unified CompletionRequest into the OpenAI
// chat.completions request body shape.
func buildRequest(req *llm.CompletionRequest, stream bool) (map[string]any, error) {
	if req.Model == "" {
		return nil, errInvalidRequest("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errInvalidRequest("at least one message is required")
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": convertMessages(req.Messages, req.System),
		"stream":   stream,
	}

	if req.MaxTokens > 0 {
		out["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		out["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		out["top_p"] = req.TopP
	}
	if len(req.StopSequences) > 0 {
		out["stop"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out["tools"] = tools
	}
	if tc := convertToolChoice(req.ToolChoice); tc != nil {
		out["tool_choice"] = tc
	}
	if req.Extra != nil {
		for k, v := range req.Extra {
			out[k] = v
		}
	}
	return out, nil
}

// convertMessages walks the unified messages and produces the OpenAI
// "messages" array. The system prompt travels as the first system message;
// RoleSystem entries in the history are dropped to avoid duplication.
// Tool results map to role:"tool" + tool_call_id; assistant tool calls map
// to the tool_calls array with their arguments re-serialised as JSON text.
func convertMessages(msgs []llm.Message, system string) []map[string]any {
	out := make([]map[string]any, 0, len(msgs)+1)
	if system != "" {
		out = append(out, map[string]any{
			"role":    "system",
			"content": system,
		})
	}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			continue
		case llm.RoleTool:
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      m.Content,
			})
		case llm.RoleUser:
			out = append(out, map[string]any{
				"role":    "user",
				"content": convertUserContent(m),
			})
		case llm.RoleAssistant:
			msg := map[string]any{
				"role":    "assistant",
				"content": m.Content,
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					args := tc.Arguments
					if args == nil {
						args = map[string]any{}
					}
					raw, err := json.Marshal(args)
					if err != nil {
						raw = []byte("{}")
					}
					calls = append(calls, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(raw),
						},
					})
				}
				msg["tool_calls"] = calls
			}
			out = append(out, msg)
		default:
			// Unknown role: skip silently to avoid breaking a streaming run.
		}
	}
	return out
}

// convertUserContent renders one user message for OpenAI. With images it
// emits a content array of text / image_url parts; without images it stays
// a plain string so the wire shape is unchanged for existing callers.
func convertUserContent(m llm.Message) any {
	if len(m.Images) == 0 {
		return m.Content
	}
	parts := make([]map[string]any, 0, 1+len(m.Images))
	for _, img := range m.Images {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "data:" + img.MediaType + ";base64," + img.Data,
			},
		})
	}
	if m.Content != "" {
		parts = append(parts, map[string]any{"type": "text", "text": m.Content})
	}
	return parts
}

// convertTools maps unified Tool definitions to the OpenAI tools array.
func convertTools(tools []llm.Tool) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return nil, errInvalidRequest("tool.name is required")
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}
	return out, nil
}

// convertToolChoice normalises the unified ToolChoice into the OpenAI
// tool_choice value. Empty Type returns nil so the field is omitted.
func convertToolChoice(tc llm.ToolChoice) any {
	switch tc.Type {
	case "", "auto":
		if tc.Type == "" {
			return nil
		}
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		if tc.Name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": tc.Name,
				},
			}
		}
		return "auto"
	default:
		return "auto"
	}
}

// parseResponse converts a non-streaming OpenAI chat.completions body into
// the unified CompletionResponse.
func parseResponse(body []byte) (*llm.CompletionResponse, error) {
	var raw struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errInvalidRequest("unmarshal response: " + err.Error())
	}

	resp := &llm.CompletionResponse{
		Model: raw.Model,
		Usage: llm.Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		},
	}
	if len(raw.Choices) > 0 {
		c := raw.Choices[0]
		resp.Content = c.Message.Content
		resp.FinishReason = mapFinishReason(c.FinishReason)
		for _, tc := range c.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: parseToolArgs(tc.Function.Arguments),
			})
		}
	}
	return resp, nil
}

// mapFinishReason normalises OpenAI's finish_reason onto the unified
// FinishReason vocabulary.
func mapFinishReason(s string) llm.FinishReason {
	switch s {
	case "stop":
		return llm.FinishReasonStop
	case "length":
		return llm.FinishReasonLength
	case "tool_calls", "function_call":
		return llm.FinishReasonToolCalls
	case "content_filter":
		return llm.FinishReasonContentFilter
	default:
		return llm.FinishReasonStop
	}
}

// parseToolArgs converts the accumulated argument JSON into a map. Empty
// input yields an empty (non-nil) map.
func parseToolArgs(s string) map[string]any {
	s = trimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{"_raw": s, "_error": err.Error()}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// errInvalidRequest builds a ProviderError with ErrCodeInvalidRequest.
func errInvalidRequest(msg string) error {
	return llm.NewProviderError("openai", llm.ErrCodeInvalidRequest, msg, 0, nil)
}
