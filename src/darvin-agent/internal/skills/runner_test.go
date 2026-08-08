package skills

import (
	"context"
	"encoding/json"
	"testing"

	"darvin-cowork/backend/internal/tools"
)

type toolStub struct{ name string }

func (s *toolStub) Name() string                { return s.name }
func (s *toolStub) Description() string         { return "stub" }
func (s *toolStub) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *toolStub) Execute(_ context.Context, _ map[string]any) tool.Result {
	return tool.Result{}
}

func registryWithEntry(t *testing.T, entry *SkillEntry) *SkillRegistry {
	t.Helper()
	reg := NewSkillRegistry()
	src := &stubSource{entries: []*SkillEntry{entry}}
	if err := reg.Load(context.Background(), []SkillSourceLoader{src}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestRunnerExecuteByIDReturnsSystemPrompt(t *testing.T) {
	entry := &SkillEntry{ID: "code-review", Prompt: "review body", Enabled: true, UserInvocable: true}
	reg := registryWithEntry(t, entry)
	toolReg := tool.NewRegistry()
	if err := toolReg.Register(&toolStub{name: "shell"}); err != nil {
		t.Fatal(err)
	}
	runner := NewSkillRunner(reg, toolReg)
	got, err := runner.ExecuteByID(context.Background(), "code-review", "src/api")
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "review body" {
		t.Fatalf("SystemPrompt = %q", got.SystemPrompt)
	}
	if got.Args != "src/api" {
		t.Fatalf("Args = %q", got.Args)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(got.Tools))
	}
}

func TestRunnerExecuteByIDMissing(t *testing.T) {
	entry := &SkillEntry{ID: "code-review", Prompt: "review body", Enabled: true, UserInvocable: true}
	reg := registryWithEntry(t, entry)
	toolReg := tool.NewRegistry()
	runner := NewSkillRunner(reg, toolReg)
	_, err := runner.ExecuteByID(context.Background(), "missing", "")
	if err != ErrSkillNotFound {
		t.Fatalf("err = %v, want ErrSkillNotFound", err)
	}
}

func TestRunnerExecuteByIDDisabled(t *testing.T) {
	entry := &SkillEntry{ID: "code-review", Prompt: "review body", Enabled: false, UserInvocable: true}
	reg := registryWithEntry(t, entry)
	toolReg := tool.NewRegistry()
	runner := NewSkillRunner(reg, toolReg)
	_, err := runner.ExecuteByID(context.Background(), "code-review", "")
	if err != ErrSkillDisabled {
		t.Fatalf("err = %v, want ErrSkillDisabled", err)
	}
}

func TestRunnerUserInvocationNotInvocable(t *testing.T) {
	entry := &SkillEntry{ID: "code-review", Prompt: "review body", Enabled: true, UserInvocable: false}
	reg := registryWithEntry(t, entry)
	toolReg := tool.NewRegistry()
	runner := NewSkillRunner(reg, toolReg)
	_, err := runner.ExecuteByUserInvocation(context.Background(), "code-review", "")
	if err != ErrSkillNotUserInvocable {
		t.Fatalf("err = %v, want ErrSkillNotUserInvocable", err)
	}
}

func TestRunnerUserInvocationAllowed(t *testing.T) {
	entry := &SkillEntry{ID: "code-review", Prompt: "review body", Enabled: true, UserInvocable: true}
	reg := registryWithEntry(t, entry)
	toolReg := tool.NewRegistry()
	runner := NewSkillRunner(reg, toolReg)
	got, err := runner.ExecuteByUserInvocation(context.Background(), "code-review", "src/api")
	if err != nil {
		t.Fatal(err)
	}
	if got.Args != "src/api" {
		t.Fatalf("Args = %q", got.Args)
	}
}
