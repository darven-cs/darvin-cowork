package session

import (
	"testing"
	"time"

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
	s.Key = "user-key"
	s.AgentID = "agent-1"
	s.Append(llm.Message{Role: llm.RoleUser, Content: "x"})
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: "y"})
	m := s.Meta()
	if m.ID != "s4" {
		t.Errorf("Meta.ID = %q, want s4", m.ID)
	}
	if m.Key != "user-key" {
		t.Errorf("Meta.Key = %q, want user-key", m.Key)
	}
	if m.AgentID != "agent-1" {
		t.Errorf("Meta.AgentID = %q, want agent-1", m.AgentID)
	}
	if m.Status != StatusActive {
		t.Errorf("Meta.Status = %q, want %q", m.Status, StatusActive)
	}
	if m.MessageCount != 2 {
		t.Errorf("Meta.MessageCount = %d, want 2", m.MessageCount)
	}
}

func TestNewSessionDefaults(t *testing.T) {
	before := time.Now()
	s := NewSession("x")
	after := time.Now()

	if s.ID != "x" {
		t.Errorf("ID = %q, want x", s.ID)
	}
	if s.Key != "" {
		t.Errorf("Key = %q, want empty", s.Key)
	}
	if s.AgentID != "" {
		t.Errorf("AgentID = %q, want empty", s.AgentID)
	}
	if s.Status != StatusActive {
		t.Errorf("Status = %q, want %q", s.Status, StatusActive)
	}
	if s.CreatedAt.Before(before) || s.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, want in [%v, %v]", s.CreatedAt, before, after)
	}
	updated := s.UpdatedAt()
	if updated.Before(before) || updated.After(after) {
		t.Errorf("UpdatedAt = %v, want in [%v, %v]", updated, before, after)
	}
}
