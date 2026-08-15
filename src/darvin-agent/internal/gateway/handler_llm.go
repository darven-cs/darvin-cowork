// LLM metadata JSON-RPC handlers (agent.llm.*).

package gateway

import (
	"context"
	"encoding/json"

	"darvin-cowork/backend/internal/agents/protocol"
)

// ModelDescriptorWire is the IPC-safe model metadata shape. It carries no
// credentials or pricing so it can cross the process boundary freely.
type ModelDescriptorWire struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	APIKind       string   `json:"apiKind"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
}

// ListModelsResult is the JSON-RPC result for agent.llm.list_models.
type ListModelsResult struct {
	Models []ModelDescriptorWire `json:"models"`
}

// handleLLMListModels returns the full registered model catalog so the
// renderer can drive the model picker / settings dropdown from the same
// metadata the agent runtime reads.
func handleLLMListModels(_ context.Context, id json.RawMessage, _ *client, _ *Handler) *Response {
	all := protocol.DefaultModelRegistry.All()
	out := make([]ModelDescriptorWire, 0, len(all))
	for _, m := range all {
		inputs := make([]string, 0, len(m.Input))
		for _, in := range m.Input {
			inputs = append(inputs, string(in))
		}
		out = append(out, ModelDescriptorWire{
			ID:            m.ID,
			Name:          m.Name,
			Provider:      m.Provider,
			APIKind:       string(m.APIVersion),
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
			Reasoning:     m.Reasoning,
			Input:         inputs,
		})
	}
	return successResp(id, ListModelsResult{Models: out})
}
