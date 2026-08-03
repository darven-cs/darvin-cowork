package agent

import (
	"context"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/llm"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/tool"
)

// recordingProvider captures the last CompletionRequest so the test can
// assert the system prompt and tool surface the mini loop exposed.
type recordingProvider struct {
	mu   sync.Mutex
	reqs []*llm.CompletionRequest
}

func (r *recordingProvider) Name() string { return "recording" }
func (r *recordingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, nil
}
func (r *recordingProvider) Stream(_ context.Context, req *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.TextDeltaEvent{Delta: "ok"}
	ch <- llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}}
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}
func (r *recordingProvider) last() *llm.CompletionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		return nil
	}
	return r.reqs[len(r.reqs)-1]
}

type dummyTool struct{ name string }

func (d *dummyTool) Name() string                    { return d.name }
func (d *dummyTool) Description() string             { return "dummy" }
func (d *dummyTool) Parameters() llm.ParameterSchema { return llm.ParameterSchema{Type: "object"} }
func (d *dummyTool) Execute(_ context.Context, _ map[string]any) tool.Result {
	return tool.Result{Content: "dummy"}
}

// TestRunSkillSessionScopesPromptAndTools verifies the mini loop exposes the
// skill's SKILL.md as the system prompt and only the skill's allowed tools to
// the provider, and that the transient override is cleared afterwards.
func TestRunSkillSessionScopesPromptAndTools(t *testing.T) {
	full := tool.NewRegistry()
	if err := full.RegisterTool(&dummyTool{name: "echo"}, tool.KindBuiltIn, nil); err != nil {
		t.Fatal(err)
	}
	if err := full.RegisterTool(&dummyTool{name: "skill:code-review"}, tool.KindSkill, map[string]any{"skillID": "code-review"}); err != nil {
		t.Fatal(err)
	}

	prov := &recordingProvider{}
	a, err := New(NewAgentConfig{
		Name:         "test",
		Instructions: "base instructions",
		Model:        ModelRef{Provider: "x", Model: "m"},
		Provider:     prov,
		Session:      session.NewSession("s"),
		Tools:        full,
	})
	if err != nil {
		t.Fatal(err)
	}

	allowed := []tool.Tool{&dummyTool{name: "echo"}}
	if err := a.RunSkillSession(context.Background(), "SKILL PROMPT BODY", "/echo hello", allowed); err != nil {
		t.Fatalf("RunSkillSession: %v", err)
	}

	req := prov.last()
	if req == nil {
		t.Fatal("provider never streamed a request")
	}
	if req.System != "SKILL PROMPT BODY" {
		t.Errorf("System = %q, want skill prompt", req.System)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "echo" {
		t.Errorf("Tools = %v, want only [echo] (skill:code-review excluded)", toolNames(req.Tools))
	}
	// Transient override cleared: the agent is back to the full surface.
	if a.Tools() != full {
		t.Errorf("Tools() after run != full registry (override not cleared)")
	}
	if a.Instructions() != "base instructions" {
		t.Errorf("Instructions() after run = %q, want base instructions", a.Instructions())
	}
}

// TestBuildSkillToolsPreservesKinds verifies the scoped registry keeps each
// entry's kind/metadata so event attribution survives the copy.
func TestBuildSkillToolsPreservesKinds(t *testing.T) {
	full := tool.NewRegistry()
	if err := full.RegisterTool(&dummyTool{name: "shell"}, tool.KindBuiltIn, nil); err != nil {
		t.Fatal(err)
	}
	if err := full.RegisterTool(&dummyTool{name: "skill:web-search"}, tool.KindSkill, map[string]any{"skillID": "web-search"}); err != nil {
		t.Fatal(err)
	}
	if err := full.RegisterTool(&dummyTool{name: "mcp:filesystem:list_directory"}, tool.KindMcp, map[string]any{"mcpServerID": "filesystem"}); err != nil {
		t.Fatal(err)
	}

	scoped := buildSkillTools(full, []tool.Tool{
		&dummyTool{name: "skill:web-search"},
		&dummyTool{name: "mcp:filesystem:list_directory"},
	})

	for name, wantKind := range map[string]tool.Kind{
		"skill:web-search":              tool.KindSkill,
		"mcp:filesystem:list_directory": tool.KindMcp,
	} {
		e, ok := scoped.GetEntry(name)
		if !ok {
			t.Errorf("scoped registry missing %q", name)
			continue
		}
		if e.Kind != wantKind {
			t.Errorf("%q kind = %q, want %q", name, e.Kind, wantKind)
		}
	}
	if e, ok := scoped.GetEntry("shell"); ok {
		t.Errorf("scoped registry should exclude shell, got entry kind=%q", e.Kind)
	}
}

// TestBuildSkillToolsEmptyAllowed verifies a skill with no allowed tools
// yields an empty registry (LLM answers from the prompt alone).
func TestBuildSkillToolsEmptyAllowed(t *testing.T) {
	full := tool.NewRegistry()
	if err := full.RegisterTool(&dummyTool{name: "shell"}, tool.KindBuiltIn, nil); err != nil {
		t.Fatal(err)
	}
	scoped := buildSkillTools(full, nil)
	if n := len(scoped.Names()); n != 0 {
		t.Errorf("scoped registry has %d tools, want 0", n)
	}
}

func toolNames(tools []llm.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}
