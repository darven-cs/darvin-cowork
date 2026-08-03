package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"darvin-cowork/backend/internal/agents/ctxengine"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/llm"
	"darvin-cowork/backend/internal/tools"
)

// scriptedProvider is a minimal llm.ModelProvider that returns the same
// event sequence on every Stream call. Used by agent-level tests.
type scriptedProvider struct {
	mu     sync.Mutex
	events []llm.StreamEvent
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *scriptedProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan llm.StreamEvent, len(s.events)+1)
	for _, e := range s.events {
		ch <- e
	}
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}

func TestNewRequiresSessionAndProvider(t *testing.T) {
	if _, err := New(NewAgentConfig{}); !errors.Is(err, ErrSessionRequired) {
		t.Errorf("nil Session: err = %v, want ErrSessionRequired", err)
	}
	_, err := New(NewAgentConfig{Session: session.NewSession("x")})
	if !errors.Is(err, ErrProviderRequired) {
		t.Errorf("nil Provider: err = %v, want ErrProviderRequired", err)
	}
}

func TestNewRespectsCustomTools(t *testing.T) {
	custom := tool.NewRegistry()
	custom.MustRegister(&echoAdapter{})
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Tools().Names(); len(got) != 1 || got[0] != "echo" {
		t.Errorf("Names = %v, want [echo]", got)
	}
}

func TestNewRespectsCustomStore(t *testing.T) {
	var s store.SessionStore = store.NewMemoryStore()
	if s == nil {
		t.Fatal("NewMemoryStore returned nil SessionStore")
	}
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    tool.NewRegistry(),
		Store:    s,
	})
	if err != nil {
		t.Fatal(err)
	}
	// constructed with the custom store
	_ = a
}

type echoAdapter struct{}

func (echoAdapter) Name() string                    { return "echo" }
func (echoAdapter) Description() string             { return "echo" }
func (echoAdapter) Parameters() llm.ParameterSchema { return llm.ParameterSchema{Type: "object"} }
func (echoAdapter) Execute(_ context.Context, _ map[string]any) tool.Result {
	return tool.Result{Content: "ok"}
}

// TestNewAutoConstructsAssembler verifies that when NewAgentConfig.Assembler
// is nil, New() auto-wires a DefaultAssembler from the Config.* knobs and
// returns a non-nil assembler on Agent.Assembler(). The engine is always
// constructed (regardless of AssemblerEnabled) so callers can flip the flag
// at runtime without rebuilding.
func TestNewAutoConstructsAssembler(t *testing.T) {
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    tool.NewRegistry(),
		Config: Config{
			TokenBudget:        12345,
			CompactTailKeep:    7,
			ToolResultMaxBytes: 2048,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Assembler() == nil {
		t.Fatal("Assembler() = nil, want auto-constructed")
	}
	cfg := a.Assembler().Info()
	if cfg.Name != "default" {
		t.Errorf("Assembler().Info().Name = %q, want %q", cfg.Name, "default")
	}
}

// TestNewHonorsCallerAssembler verifies that when NewAgentConfig.Assembler
// is non-nil, New() uses it as-is and does NOT auto-construct a
// DefaultAssembler.
func TestNewHonorsCallerAssembler(t *testing.T) {
	custom := &stubAssembler{}
	a, err := New(NewAgentConfig{
		Session:   session.NewSession("s"),
		Provider:  &scriptedProvider{},
		Tools:     tool.NewRegistry(),
		Assembler: custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Assembler() != custom {
		t.Errorf("Assembler() = %p, want caller-provided %p", a.Assembler(), custom)
	}
}

// TestNewAssemblerEnabledDefaultsToFalse verifies that the Go zero-value
// (false) keeps the assembler pipeline disabled. The cfg.yaml front-end
// is responsible for mapping `assembler_enabled: true` (or the YAML default)
// to AssemblerEnabled: true; in pure Go, callers must opt in explicitly.
func TestNewAssemblerEnabledDefaultsToFalse(t *testing.T) {
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    tool.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.AssemblerEnabled() {
		t.Errorf("AssemblerEnabled() = true, want false (Go zero-value default)")
	}
}

// TestConfigForwardsTokenBudget verifies that Config().TokenBudget flows
// from agent.Config to executor.Config so the assembler path receives the
// configured budget.
func TestConfigForwardsTokenBudget(t *testing.T) {
	a, err := New(NewAgentConfig{
		Session:  session.NewSession("s"),
		Provider: &scriptedProvider{},
		Tools:    tool.NewRegistry(),
		Config:   Config{TokenBudget: 7777},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Config().TokenBudget; got != 7777 {
		t.Errorf("Config().TokenBudget = %d, want 7777", got)
	}
}

// TestAssemblerEnabledTrue_TriggersSeam verifies the end-to-end happy path:
// when AssemblerEnabled: true, running the agent causes the executor to
// dispatch prompt construction through the assembler.
func TestAssemblerEnabledTrue_TriggersSeam(t *testing.T) {
	rec := &stubAssembler{}
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hi"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}
	a, err := New(NewAgentConfig{
		Session:          session.NewSession("s"),
		Provider:         prov,
		Tools:            tool.NewRegistry(),
		Assembler:        rec,
		AssemblerEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.calls < 1 {
		t.Errorf("assembler called %d times, want >= 1 (AssemblerEnabled=true triggers seam)", rec.calls)
	}
}

// TestAssemblerEnabledFalse_TakesFallback verifies that even with a wired
// assembler, the executor takes the legacy d.Session().Messages() path when
// AssemblerEnabled is false.
func TestAssemblerEnabledFalse_TakesFallback(t *testing.T) {
	rec := &stubAssembler{}
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hi"},
		llm.DoneEvent{Response: llm.CompletionResponse{FinishReason: llm.FinishReasonStop}},
	}}
	a, err := New(NewAgentConfig{
		Session:          session.NewSession("s"),
		Provider:         prov,
		Tools:            tool.NewRegistry(),
		Assembler:        rec,
		AssemblerEnabled: false, // explicit disable
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("assembler called %d times, want 0 (AssemblerEnabled=false → fallback)", rec.calls)
	}
}

// stubAssembler is a minimal ContextEngine for tests. It records every
// Assemble call and returns the input messages unchanged so the downstream
// LLM call still gets data.
type stubAssembler struct {
	ctxengine.ContextEngine // embed to inherit the interface signature
	calls                   int
	lastParams              []ctxengine.AssembleParams
}

func (s *stubAssembler) Assemble(_ context.Context, p ctxengine.AssembleParams) ctxengine.AssembleResult {
	s.calls++
	s.lastParams = append(s.lastParams, p)
	return ctxengine.AssembleResult{Messages: p.Messages, Budget: p.ToolBudget}
}

func (s *stubAssembler) Bootstrap(context.Context, ctxengine.BootstrapParams) error { return nil }
func (s *stubAssembler) Maintain(context.Context, ctxengine.MaintainParams) error   { return nil }
func (s *stubAssembler) Dispose(context.Context) error                              { return nil }
func (s *stubAssembler) Ingest(_ context.Context, _ ctxengine.IngestParams) ctxengine.IngestResult {
	return ctxengine.IngestResult{Success: true}
}
func (s *stubAssembler) IngestBatch(_ context.Context, _ ctxengine.IngestBatchParams) ctxengine.IngestResult {
	return ctxengine.IngestResult{Success: true, TokensProcessed: 1}
}
func (s *stubAssembler) AfterTurn(context.Context, ctxengine.AfterTurnParams) error {
	return nil
}
func (s *stubAssembler) Compact(_ context.Context, p ctxengine.CompactParams) ctxengine.CompactResult {
	return ctxengine.CompactResult{Success: true, RetainedMessages: p.Messages}
}
func (s *stubAssembler) PrepareSubagentSpawn(context.Context, ctxengine.SubagentSpawnParams) (*ctxengine.SubagentSpawnPreparation, error) {
	return nil, ctxengine.ErrSubAgentUnsupported
}
func (s *stubAssembler) OnSubagentEnded(context.Context, ctxengine.SubagentEndedParams) error {
	return ctxengine.ErrSubAgentUnsupported
}
func (s *stubAssembler) Info() ctxengine.Info {
	return ctxengine.Info{Name: "stub", Version: "test"}
}
