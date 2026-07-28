package session

import (
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

func TestAppendAndLen(t *testing.T) {
	s := NewSession("s1")
	if s.Len() != 0 {
		t.Fatalf("initial Len = %d, want 0", s.Len())
	}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if s.Len() != 1 {
		t.Fatalf("after Append Len = %d, want 1", s.Len())
	}
}

func TestMessagesDeepCopy(t *testing.T) {
	s := NewSession("s2")
	s.Append(llm.Message{
		Role:    llm.RoleAssistant,
		Content: "use tool",
		ToolCalls: []llm.ToolCall{{
			ID:        "t1",
			Name:      "read_file",
			Arguments: map[string]any{"path": "/tmp/x"},
		}},
	})
	got := s.Messages()
	// mutate the returned slice
	got[0].Content = "MUTATED"
	got[0].ToolCalls[0].Name = "MUTATED"
	got[0].ToolCalls[0].Arguments["path"] = "MUTATED"
	// re-read
	again := s.Messages()
	if again[0].Content != "use tool" {
		t.Errorf("Content leaked: %q", again[0].Content)
	}
	if again[0].ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCall.Name leaked: %q", again[0].ToolCalls[0].Name)
	}
	if again[0].ToolCalls[0].Arguments["path"] != "/tmp/x" {
		t.Errorf("ToolCall.Arguments leaked: %v", again[0].ToolCalls[0].Arguments)
	}
}

func TestReplaceAll(t *testing.T) {
	s := NewSession("s3")
	s.Append(llm.Message{Role: llm.RoleUser, Content: "old"})
	s.ReplaceAll([]llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
	})
	if s.Len() != 2 {
		t.Fatalf("after ReplaceAll Len = %d, want 2", s.Len())
	}
	got := s.Messages()
	if got[0].Content != "a" || got[1].Content != "b" {
		t.Errorf("ReplaceAll content = (%q, %q), want (a, b)", got[0].Content, got[1].Content)
	}
}

func TestMeta(t *testing.T) {
	s := NewSession("s4")
	s.Append(llm.Message{Role: llm.RoleUser, Content: "x"})
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: "y"})
	m := s.Meta()
	if m.ID != "s4" || m.MessageCount != 2 {
		t.Errorf("Meta = %+v, want id=s4 count=2", m)
	}
}
