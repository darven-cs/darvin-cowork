// Tests for session-prompt composition in Instructions / SystemSections.

package agent

import (
	"testing"

	"darvin-cowork/backend/internal/agents/session"
	tool "darvin-cowork/backend/internal/tools"
)

func newPromptTestAgent(t *testing.T, instructions string, sess *session.Session) *Agent {
	t.Helper()
	a, err := New(NewAgentConfig{
		Session:      sess,
		Provider:     &scriptedProvider{},
		Tools:        tool.NewRegistry(),
		Instructions: instructions,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestInstructionsEmptySessionPromptUnchanged(t *testing.T) {
	a := newPromptTestAgent(t, "base instructions", session.NewSession("s1"))
	if got := a.Instructions(); got != "base instructions" {
		t.Fatalf("Instructions() = %q, want identical base", got)
	}
}

func TestInstructionsAppendsSessionPrompt(t *testing.T) {
	sess := session.NewSession("s2")
	sess.SetPrompt("capability prompt", "")
	a := newPromptTestAgent(t, "base", sess)
	want := "base\n\ncapability prompt"
	if got := a.Instructions(); got != want {
		t.Fatalf("Instructions() = %q, want %q", got, want)
	}
}

func TestInstructionsImportedNoteStaysLast(t *testing.T) {
	sess := session.NewSession("s3")
	sess.SetPrompt("capability", "")
	a := newPromptTestAgent(t, "  base  ", sess)
	a.runImportedNote = "imported note"
	want := "base\n\ncapability\n\nimported note"
	if got := a.Instructions(); got != want {
		t.Fatalf("Instructions() = %q, want %q", got, want)
	}
}

func TestInstructionsSkillPromptDominates(t *testing.T) {
	sess := session.NewSession("s4")
	sess.SetPrompt("capability", "")
	a := newPromptTestAgent(t, "base", sess)
	a.runSkillPrompt = "SKILL body"
	if got := a.Instructions(); got != "SKILL body" {
		t.Fatalf("Instructions() = %q, want skill prompt only", got)
	}
}

func TestSystemSectionsNilWithoutIdentity(t *testing.T) {
	a := newPromptTestAgent(t, "base", session.NewSession("s5"))
	if got := a.SystemSections(); got != nil {
		t.Fatalf("SystemSections() = %v, want nil", got)
	}
}

func TestSystemSectionsIdentitySection(t *testing.T) {
	sess := session.NewSession("s6")
	sess.SetPrompt("", "stock assistant persona")
	a := newPromptTestAgent(t, "base", sess)
	got := a.SystemSections()
	if len(got) != 1 {
		t.Fatalf("SystemSections len = %d, want 1", len(got))
	}
	want := "<IDENTITY>\nstock assistant persona\n</IDENTITY>"
	if got[0].Content != want {
		t.Fatalf("Content = %q, want %q", got[0].Content, want)
	}
	if got[0].Priority != 31 {
		t.Fatalf("Priority = %d, want 31 (after workspace identity, before soul)", got[0].Priority)
	}
}
