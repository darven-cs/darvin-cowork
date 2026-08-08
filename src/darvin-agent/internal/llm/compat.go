// Default capability flags shared by the models each provider family ships.

package llm

// Default capability flags per provider family. Provider packages reuse
// these when registering their Model descriptors so the values stay
// consistent across every model a provider ships.
var (
	DefaultAnthropicCompat = Compat{
		SupportsToolCalls:     true,
		SupportsImageInput:    true,
		SupportsUsageInStream: true,
	}

	DefaultOpenAICompat = Compat{
		SupportsToolCalls:     true,
		SupportsImageInput:    true,
		SupportsUsageInStream: true,
	}

	DefaultGeminiCompat = Compat{
		SupportsToolCalls:     true,
		SupportsImageInput:    true,
		SupportsUsageInStream: false,
	}
)
