// Static model metadata registered by the openai wire provider. The official
// OpenAI family is described here; every other OpenAI-compatible preset
// (deepseek / qwen / ollama / custom) keeps its model list in the shared
// catalog and falls back to a default context window at runtime.

package openai

import "darvin-cowork/backend/internal/llm"

// openAIModels returns the official OpenAI model descriptors served over
// the OpenAI chat.completions wire (matches LobsterAI's OpenAI defaultModels).
func openAIModels() []llm.ModelDescriptor {
	image := []llm.InputModality{llm.InputText, llm.InputImage}
	return []llm.ModelDescriptor{
		{
			ID: "gpt-5.5", Name: "GPT-5.5",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 400000, MaxTokens: 32768, Reasoning: true,
			Input:  image,
			Cost:   llm.ModelCost{Input: 1.25, Output: 10.0},
			Compat: llm.DefaultOpenAICompat,
		},
		{
			ID: "gpt-5.4", Name: "GPT-5.4",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 400000, MaxTokens: 32768, Reasoning: true,
			Input:  image,
			Cost:   llm.ModelCost{Input: 1.25, Output: 10.0},
			Compat: llm.DefaultOpenAICompat,
		},
		{
			ID: "gpt-4o", Name: "GPT-4o",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 128000, MaxTokens: 16384,
			Input:  image,
			Cost:   llm.ModelCost{Input: 2.5, Output: 10.0},
			Compat: llm.DefaultOpenAICompat,
		},
		{
			ID: "gpt-4o-mini", Name: "GPT-4o mini",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 128000, MaxTokens: 16384,
			Input:  image,
			Cost:   llm.ModelCost{Input: 0.15, Output: 0.6},
			Compat: llm.DefaultOpenAICompat,
		},
		{
			ID: "gpt-4.1", Name: "GPT-4.1",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 1052672, MaxTokens: 32768,
			Input:  image,
			Cost:   llm.ModelCost{Input: 2.0, Output: 8.0},
			Compat: llm.DefaultOpenAICompat,
		},
		{
			ID: "gpt-4.1-mini", Name: "GPT-4.1 mini",
			Provider: "openai", APIVersion: llm.APIOpenAICompletions,
			ContextWindow: 1052672, MaxTokens: 32768,
			Input:  []llm.InputModality{llm.InputText},
			Cost:   llm.ModelCost{Input: 0.4, Output: 1.6},
			Compat: llm.DefaultOpenAICompat,
		},
	}
}
