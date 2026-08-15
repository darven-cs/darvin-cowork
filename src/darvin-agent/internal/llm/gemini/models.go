// Static Gemini model metadata registered by the native gemini provider.

package gemini

import "darvin-cowork/backend/internal/llm"

// geminiModels returns the Gemini model descriptors served over the native
// generateContent wire (matches LobsterAI's Gemini defaultModels).
func geminiModels() []llm.ModelDescriptor {
	image := []llm.InputModality{llm.InputText, llm.InputImage}
	return []llm.ModelDescriptor{
		{
			ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro",
			Provider: "gemini", APIVersion: llm.APIGeminiGenerativeAI,
			ContextWindow: 1000000, MaxTokens: 65536, Reasoning: true,
			Input:  image,
			Cost:   llm.ModelCost{Input: 1.25, Output: 10.0},
			Compat: llm.DefaultGeminiCompat,
		},
		{
			ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash",
			Provider: "gemini", APIVersion: llm.APIGeminiGenerativeAI,
			ContextWindow: 1000000, MaxTokens: 65536, Reasoning: true,
			Input:  image,
			Cost:   llm.ModelCost{Input: 0.30, Output: 2.50},
			Compat: llm.DefaultGeminiCompat,
		},
		{
			ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite",
			Provider: "gemini", APIVersion: llm.APIGeminiGenerativeAI,
			ContextWindow: 1000000, MaxTokens: 65536, Reasoning: true,
			Input:  image,
			Cost:   llm.ModelCost{Input: 0.10, Output: 0.40},
			Compat: llm.DefaultGeminiCompat,
		},
	}
}
