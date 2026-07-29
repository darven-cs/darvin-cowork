package ctxengine

import (
	"fmt"
	"unicode/utf8"

	"darvin-cowork/backend/internal/agent/llm"
)

// TokenEstimator returns the approximate token count for a piece of text.
// The default estimator is EstimateCharsOver4 (rune count / 4, rounded up).
// Custom estimators are injected via (*DefaultAssembler).SetEstimator
// (used by tests and by callers that have a real tokenizer).
type TokenEstimator func(text string) int

// EstimateCharsOver4 is the default TokenEstimator: utf8 rune count / 4
// rounded up. It is intentionally cheap and approximate; precise token
// counts come from each provider's response Usage.
func EstimateCharsOver4(text string) int {
	n := utf8.RuneCountInString(text)
	return (n + 3) / 4
}

// EstimateMessageTokens estimates the token cost of a single message,
// counting Content + each ToolCall (Name + Arguments) + ToolCallID. The
// TokenEstimator field on *DefaultAssembler does not apply here — the
// algorithm is fixed for stability across tests.
func EstimateMessageTokens(m llm.Message) int {
	n := utf8.RuneCountInString(m.Content)
	for _, tc := range m.ToolCalls {
		n += utf8.RuneCountInString(tc.Name)
		for k, v := range tc.Arguments {
			n += utf8.RuneCountInString(k)
			n += estimateAny(v)
		}
	}
	if m.ToolCallID != "" {
		n += utf8.RuneCountInString(m.ToolCallID)
	}
	return (n + 3) / 4
}

// estimateAny recursively counts the rune cost of an arbitrary value
// (typical tool argument shape: string / number / bool / nested map / slice).
func estimateAny(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case string:
		return utf8.RuneCountInString(x)
	case bool:
		return 1
	case int:
		return utf8.RuneCountInString(fmt.Sprintf("%d", x))
	case int64:
		return utf8.RuneCountInString(fmt.Sprintf("%d", x))
	case float64:
		return utf8.RuneCountInString(fmt.Sprintf("%g", x))
	case map[string]any:
		n := 0
		for k, vv := range x {
			n += utf8.RuneCountInString(k)
			n += estimateAny(vv)
		}
		return n
	case []any:
		n := 0
		for _, vv := range x {
			n += estimateAny(vv)
		}
		return n
	default:
		return utf8.RuneCountInString(fmt.Sprintf("%v", x))
	}
}
