package ctxengine

import (
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

func TestEstimateMessageTokens_PlainContent(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},        // 4 chars → ceil(4/4)=1
		{"abcde", 2},       // 5 chars → ceil(5/4)=2
		{"hello world", 3}, // 11 chars → ceil(11/4)=3
		{"中文", 1},          // 2 runes → 1
		{"🎉🎉🎉🎉🎉", 2},       // 5 runes → 2
	}
	for _, c := range cases {
		m := llm.Message{Role: llm.RoleUser, Content: c.content}
		if got := EstimateMessageTokens(m); got != c.want {
			t.Errorf("EstimateMessageTokens(%q) = %d, want %d", c.content, got, c.want)
		}
	}
}

func TestEstimateMessageTokens_ToolCalls(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call-1",
				Name: "read_file", // 9 runes
				Arguments: map[string]any{
					"path": "/tmp/foo.txt", // 4 + 12 = 16 runes
				},
			},
		},
	}
	// 9 + 4 + 12 = 25 → ceil(25/4) = 7
	if got := EstimateMessageTokens(m); got != 7 {
		t.Errorf("EstimateMessageTokens with single ToolCall = %d, want 7", got)
	}
}

func TestEstimateMessageTokens_NestedArgs(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				Name: "shell", // 5 runes
				Arguments: map[string]any{
					"cmd":   "ls",                               // 3 + 2 = 5
					"env":   map[string]any{"PATH": "/usr/bin"}, // 3 + 4 + 8 = 15
					"items": []any{"a", "bb"},                   // 5 + 1 + 2 = 8
				},
			},
		},
	}
	// 5 + 5 + 15 + 8 = 33 → ceil(33/4) = 9
	if got := EstimateMessageTokens(m); got != 9 {
		t.Errorf("EstimateMessageTokens with nested args = %d, want 9", got)
	}
}

func TestEstimateMessageTokens_ToolCallID(t *testing.T) {
	m := llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "abc123def456", // 12 chars → ceil(12/4) = 3
	}
	if got := EstimateMessageTokens(m); got != 3 {
		t.Errorf("EstimateMessageTokens with ToolCallID = %d, want 3", got)
	}
}

func TestEstimateMessageTokens_Combined(t *testing.T) {
	m := llm.Message{
		Role:       llm.RoleTool,
		Content:    "result data", // 11 chars
		ToolCallID: "abc123",      // 6 chars
	}
	// 11 + 6 = 17 → ceil(17/4) = 5
	if got := EstimateMessageTokens(m); got != 5 {
		t.Errorf("EstimateMessageTokens(combined) = %d, want 5", got)
	}
}

func TestEstimateMessageTokens_BoolArg(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				Name: "f",
				Arguments: map[string]any{
					"flag": true,
				},
			},
		},
	}
	// Name=1, key=4, bool=1 → 1+4+1 = 6 → ceil(6/4) = 2
	if got := EstimateMessageTokens(m); got != 2 {
		t.Errorf("EstimateMessageTokens(bool arg) = %d, want 2", got)
	}
}

func TestEstimateMessageTokens_NumericArg(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				Name: "f",
				Arguments: map[string]any{
					"n": 12345, // 5 chars via Sprintf
				},
			},
		},
	}
	// Name=1, key=1, int "12345"=5 → 1+1+5 = 7 → ceil(7/4) = 2
	if got := EstimateMessageTokens(m); got != 2 {
		t.Errorf("EstimateMessageTokens(numeric arg) = %d, want 2", got)
	}
}
