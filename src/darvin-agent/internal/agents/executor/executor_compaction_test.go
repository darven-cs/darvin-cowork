// Tests for the executor's compaction hand-off to the assembler.

package executor

import (
	"context"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/protocol"
	"darvin-cowork/backend/internal/llm"
)

// captureAssembler returns SystemAddition + (optionally) signals that
// compaction fired and supplies a replacement slice. Tests inspect the
// captured fields afterwards.
type captureAssembler struct {
	ctxengine.ContextEngine
	mu        sync.Mutex
	gotReqs   []*llm.CompletionRequest
	systemAdd string
	trigger   bool
	retained  []protocol.Message
	firstKept string
	summary   string
}

func (a *captureAssembler) Assemble(_ context.Context, p ctxengine.AssembleParams) ctxengine.AssembleResult {
	a.mu.Lock()
	a.gotReqs = append(a.gotReqs, nil)
	a.mu.Unlock()
	msgs := p.Messages
	if a.retained != nil {
		msgs = a.retained
	}
	return ctxengine.AssembleResult{
		Messages:           msgs,
		Budget:             p.ToolBudget,
		SystemAddition:     a.systemAdd,
		Stats:              ctxengine.AssembleStats{CompactionTriggered: a.trigger},
		CompactSummary:     a.summary,
		FirstKeptID:        a.firstKept,
		FirstKeptTimestamp: 42,
		EstimatedTokens:    100,
	}
}

// TestReqSystem_IncludesSystemAddition pins the FR fix: req.System must
// be Instructions() + SystemAddition. Pre-v2 it was Instructions()
// alone, silently dropping all bootstrap / skill / memory blocks.
func TestReqSystem_IncludesSystemAddition(t *testing.T) {
	d := newFakeDeps(t, &scriptedProvider{script: [][]llm.StreamEvent{{
		llm.StartEvent{},
		llm.TextDeltaEvent{Delta: "ok"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}}, nil)
	d.instructions = "BASE"
	asm := &captureAssembler{systemAdd: "<memory>x</memory>"}
	d.assembler = asm
	d.assemblerEnabled = true

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}

	scripted := d.provider.(*scriptedProvider)
	scripted.mu.Lock()
	defer scripted.mu.Unlock()
	if len(scripted.gotReqs) == 0 {
		t.Fatalf("provider never received a request")
	}
	got := scripted.gotReqs[len(scripted.gotReqs)-1].System
	want := "BASE\n\n<memory>x</memory>"
	if got != want {
		t.Errorf("req.System = %q, want %q", got, want)
	}
}

// TestAutoCompact_ReplacesAndPersists pins the second FR fix: when the
// assembler flags CompactionTriggered the executor must ReplaceAll the
// session messages with the compacted slice AND call PersistCompaction
// so the digest lands in session_digests.
func TestAutoCompact_ReplacesAndPersists(t *testing.T) {
	d := newFakeDeps(t, &scriptedProvider{script: [][]llm.StreamEvent{{
		llm.StartEvent{},
		llm.TextDeltaEvent{Delta: "ok"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}}, nil)
	compacted := []protocol.Message{
		{Role: protocol.RoleAssistant, Content: "[Conversation Summary]\ncompressed", ID: "d1"},
		{Role: protocol.RoleUser, Content: "u-tail", ID: "u1", Timestamp: 100},
	}
	asm := &captureAssembler{
		trigger:   true,
		retained:  compacted,
		firstKept: "first-kept",
		summary:   "[Conversation Summary]\ncompressed",
	}
	d.assembler = asm
	d.assemblerEnabled = true

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}

	got := d.sess.Messages()
	// The session now contains the compacted slice (replaceAll) plus
	// the assistant message appended after the LLM call. Verify the
	// compacted prefix is verbatim at the start.
	if len(got) < len(compacted) {
		t.Fatalf("session len = %d, want >= %d (compacted slice)", len(got), len(compacted))
	}
	for i, m := range compacted {
		if got[i].ID != m.ID {
			t.Errorf("session[%d].ID = %q, want %q", i, got[i].ID, m.ID)
		}
	}

	if d.persistCalls != 1 {
		t.Errorf("PersistCompaction called %d times, want 1", d.persistCalls)
	}
	if d.lastCompact.FirstKeptID != "first-kept" {
		t.Errorf("PersistCompaction.FirstKeptID = %q, want %q", d.lastCompact.FirstKeptID, "first-kept")
	}
	if d.lastCompact.Reason != "budget_exceeded" {
		t.Errorf("PersistCompaction.Reason = %q, want budget_exceeded", d.lastCompact.Reason)
	}
}

// TestAutoCompact_NotTriggered_NoPersist is the negative case.
func TestAutoCompact_NotTriggered_NoPersist(t *testing.T) {
	d := newFakeDeps(t, &scriptedProvider{script: [][]llm.StreamEvent{{
		llm.StartEvent{},
		llm.TextDeltaEvent{Delta: "ok"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}}, nil)
	asm := &captureAssembler{trigger: false}
	d.assembler = asm
	d.assemblerEnabled = true

	ex := New()
	if err := ex.RunConversation(context.Background(), d); err != nil {
		t.Fatalf("RunConversation: %v", err)
	}
	if d.persistCalls != 0 {
		t.Errorf("PersistCompaction called %d times, want 0 when no compact", d.persistCalls)
	}
}
