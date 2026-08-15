// Native Google Generative AI (Gemini) request / response conversion.

package gemini

import (
	"encoding/json"

	"darvin-cowork/backend/internal/llm"
)

// buildRequest converts the unified CompletionRequest into the Gemini
// generateContent body shape. The endpoint is chosen by the stream flag.
func buildRequest(req *llm.CompletionRequest, stream bool) (map[string]any, error) {
	if req.Model == "" {
		return nil, errInvalidRequest("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errInvalidRequest("at least one message is required")
	}
	out := map[string]any{
		"contents": convertContents(req.Messages),
	}
	if req.System != "" {
		out["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		out["tools"] = convertTools(req.Tools)
	}
	cfg := map[string]any{}
	if req.Temperature > 0 {
		cfg["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		cfg["maxOutputTokens"] = req.MaxTokens
	}
	if len(cfg) > 0 {
		out["generationConfig"] = cfg
	}
	if req.Extra != nil {
		for k, v := range req.Extra {
			out[k] = v
		}
	}
	return out, nil
}

// convertContents maps unified messages to Gemini contents. Roles are
// "user" / "model"; tool calls become model functionCall parts and tool
// results become user functionResponse parts. Gemini correlates a tool
// result to its call by name (it has no tool-call id), so the assistant
// tool-call id is set to the function name at stream time and the tool
// message's ToolCallID (== name) is echoed back here.
func convertContents(msgs []llm.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			// Gemini takes system as systemInstruction; caller routes it via
			// req.System. Dropped here to avoid duplicate injection.
			continue
		case llm.RoleTool:
			out = append(out, map[string]any{
				"role": "user",
				"parts": []map[string]any{{
					"functionResponse": map[string]any{
						"name":     m.ToolCallID,
						"response": map[string]any{"result": m.Content},
					},
				}},
			})
		case llm.RoleUser:
			out = append(out, map[string]any{
				"role":  "user",
				"parts": convertUserParts(m),
			})
		case llm.RoleAssistant:
			parts := make([]map[string]any, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{
						"name": tc.Name,
						"args": tc.Arguments,
					},
				})
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, map[string]any{"role": "model", "parts": parts})
		default:
			// Unknown role: skip silently to avoid breaking a streaming run.
		}
	}
	return out
}

// convertUserParts renders one user message's Gemini parts: inline_data for
// images then text. Without images it stays a single text part.
func convertUserParts(m llm.Message) []map[string]any {
	parts := make([]map[string]any, 0, 1+len(m.Images))
	for _, img := range m.Images {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": img.MediaType,
				"data":     img.Data,
			},
		})
	}
	if m.Content != "" {
		parts = append(parts, map[string]any{"text": m.Content})
	}
	return parts
}

// convertTools maps unified Tool definitions to Gemini functionDeclarations.
func convertTools(tools []llm.Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	fns := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			continue
		}
		fns = append(fns, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		})
	}
	if len(fns) > 0 {
		out = append(out, map[string]any{"functionDeclarations": fns})
	}
	return out
}

// generateResponse is the non-streaming generateContent wire shape.
type generateResponse struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string        `json:"text"`
				FunctionCall *functionCall `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

type functionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// parseResponse converts a non-streaming generateContent body into the
// unified CompletionResponse.
func parseResponse(body []byte) (*llm.CompletionResponse, error) {
	var raw generateResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errInvalidRequest("unmarshal response: " + err.Error())
	}
	resp := &llm.CompletionResponse{
		Usage: llm.Usage{
			PromptTokens:     raw.UsageMetadata.PromptTokenCount,
			CompletionTokens: raw.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      raw.UsageMetadata.TotalTokenCount,
		},
	}
	if len(raw.Candidates) > 0 {
		resp.FinishReason = mapFinishReason(raw.Candidates[0].FinishReason)
		for _, p := range raw.Candidates[0].Content.Parts {
			if p.FunctionCall != nil {
				resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
					ID:        p.FunctionCall.Name,
					Name:      p.FunctionCall.Name,
					Arguments: p.FunctionCall.Args,
				})
				continue
			}
			resp.Content += p.Text
		}
	}
	return resp, nil
}

// mapFinishReason normalises Gemini's finishReason onto the unified
// FinishReason vocabulary. Gemini reports tool calls as STOP; callers
// detect those by the presence of functionCall parts instead.
func mapFinishReason(s string) llm.FinishReason {
	switch s {
	case "MAX_TOKENS":
		return llm.FinishReasonLength
	case "SAFETY", "RECITATION":
		return llm.FinishReasonContentFilter
	default:
		return llm.FinishReasonStop
	}
}

func errInvalidRequest(msg string) error {
	return llm.NewProviderError("gemini", llm.ErrCodeInvalidRequest, msg, 0, nil)
}
