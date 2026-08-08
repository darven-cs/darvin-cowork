package ctxengine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/llm"
)

// msg is a tiny helper for building an llm.Message with a given content.
func msg(content string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: content}
}

// errFromWarning re-derives an error from the warning string that
// IngestResult.Warnings carries when ctx.Err() fires. We can't carry the
// real error value through the result struct (it would break the "warnings
// are []string" contract for cross-package consumers), so we use a sentinel
// prefix + errors.New reconstruction in tests.
func errFromWarning(s string) error {
	if s == context.Canceled.Error() {
		return context.Canceled
	}
	if s == context.DeadlineExceeded.Error() {
		return context.DeadlineExceeded
	}
	return errors.New(s)
}

// fakeDeps is a no-op Deps implementation for tests. Provider is nil so
// methods that would dereference it (Summarize) must not be called.
// memoryBootstrap / memoryFacts are wired through the optional fields
// so tests that want to exercise the bootstrap / FTS path can supply
// canned values; the default zero-value fallbacks are "no blocks".
type fakeDeps struct {
	provider llm.ModelProvider
	model    string
	logger   *zap.Logger
	emit     func(event.Event)

	memoryBootstrap map[string]string
	memoryFacts     []Fact
	memoryFactsFn   func(context.Context) []Fact
}

func (f fakeDeps) Provider() llm.ModelProvider { return f.provider }
func (f fakeDeps) ModelName() string           { return f.model }
func (f fakeDeps) Logger() *zap.Logger         { return f.logger }
func (f fakeDeps) Emit(ev event.Event) {
	if f.emit != nil {
		f.emit(ev)
	}
}
func (f fakeDeps) MemoryBootstrap(name string) string {
	if f.memoryBootstrap == nil {
		return ""
	}
	return f.memoryBootstrap[name]
}
func (f fakeDeps) MemoryFacts(ctx context.Context) []Fact {
	if f.memoryFactsFn != nil {
		return f.memoryFactsFn(ctx)
	}
	return f.memoryFacts
}

// newTestAssembler builds an assembler wired to zap.NewNop().
func newTestAssembler() *DefaultAssembler {
	return NewDefaultAssembler(Config{}, fakeDeps{
		model:  "test-model",
		logger: zap.NewNop(),
	})
}

// TestDefaultAssemblerSatisfiesInterface is a compile-time + runtime check.
func TestDefaultAssemblerSatisfiesInterface(t *testing.T) {
	var _ ContextEngine = (*DefaultAssembler)(nil)
	a := newTestAssembler()
	var _ ContextEngine = a
}

// TestContextEngineInterfaceMethods verifies the interface exposes
// exactly the 11 expected methods (10 methods + Info for identity).
func TestContextEngineInterfaceMethods(t *testing.T) {
	iface := reflect.TypeOf((*ContextEngine)(nil)).Elem()

	expected := []string{
		"Info",
		"Bootstrap",
		"Maintain",
		"Dispose",
		"Ingest",
		"IngestBatch",
		"AfterTurn",
		"Assemble",
		"Compact",
		"PrepareSubagentSpawn",
		"OnSubagentEnded",
	}

	if got := iface.NumMethod(); got != len(expected) {
		t.Errorf("ContextEngine has %d methods, expected %d", got, len(expected))
	}
	for _, name := range expected {
		if _, ok := iface.MethodByName(name); !ok {
			t.Errorf("ContextEngine interface missing method %q", name)
		}
	}
}

// TestSubAgentReturnsNotImplemented verifies the SubAgent seams.
func TestSubAgentReturnsNotImplemented(t *testing.T) {
	a := newTestAssembler()

	_, err := a.PrepareSubagentSpawn(context.Background(), SubagentSpawnParams{})
	if err == nil {
		t.Fatal("PrepareSubagentSpawn: expected error, got nil")
	}
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("PrepareSubagentSpawn: errors.Is(err, ErrNotImplemented) = false; got %v", err)
	}
	if !errors.Is(err, ErrSubAgentUnsupported) {
		t.Errorf("PrepareSubagentSpawn: errors.Is(err, ErrSubAgentUnsupported) = false; got %v", err)
	}

	if err := a.OnSubagentEnded(context.Background(), SubagentEndedParams{}); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("OnSubagentEnded: errors.Is(err, ErrNotImplemented) = false; got %v", err)
	}
}

// TestLifecycleStubsReturnNil verifies the lifecycle stubs.
func TestLifecycleStubsReturnNil(t *testing.T) {
	a := newTestAssembler()

	if err := a.Bootstrap(context.Background(), BootstrapParams{}); err != nil {
		t.Errorf("Bootstrap: %v", err)
	}
	if err := a.Maintain(context.Background(), MaintainParams{}); err != nil {
		t.Errorf("Maintain: %v", err)
	}
	if err := a.Dispose(context.Background()); err != nil {
		t.Errorf("Dispose: %v", err)
	}
	if err := a.AfterTurn(context.Background(), AfterTurnParams{}); err != nil {
		t.Errorf("AfterTurn: %v", err)
	}
}

// TestIngestReturnsSuccess verifies Ingest / IngestBatch record lastIngestAt.
func TestIngestReturnsSuccess(t *testing.T) {
	a := newTestAssembler()

	res := a.Ingest(context.Background(), IngestParams{SessionID: "s1"})
	if !res.Success {
		t.Errorf("Ingest: expected Success=true, got %+v", res)
	}

	res = a.IngestBatch(context.Background(), IngestBatchParams{
		SessionID: "s1",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if !res.Success {
		t.Errorf("IngestBatch: expected Success=true, got %+v", res)
	}
	if res.TokensProcessed != 1 {
		t.Errorf("IngestBatch: TokensProcessed = %d, want 1", res.TokensProcessed)
	}
}

// TestIngestEmptySessionNoOp verifies the empty-session short-circuit.
func TestIngestEmptySessionNoOp(t *testing.T) {
	a := newTestAssembler()
	res := a.Ingest(context.Background(), IngestParams{SessionID: ""})
	if !res.Success {
		t.Errorf("Ingest(empty): expected Success=true, got %+v", res)
	}
}

// TestCompact_RealImpl_NoOpOnSmallInput verifies Compact returns Success=true
// with no summarizer call for input within budget.
func TestCompact_RealImpl_NoOpOnSmallInput(t *testing.T) {
	a := newTestAssembler()
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  msgs,
		Budget:    10000,
	})
	if !res.Success {
		t.Errorf("Compact should return Success=true for small input; got %+v", res)
	}
	if len(res.RetainedMessages) != 1 {
		t.Errorf("RetainedMessages length = %d, want 1", len(res.RetainedMessages))
	}
}

func TestInfo(t *testing.T) {
	a := newTestAssembler()
	info := a.Info()
	if info.Name != "default" {
		t.Errorf("Info.Name = %q, want %q", info.Name, "default")
	}
	if info.Version != "" {
		t.Errorf("Info.Version = %q, want %q", info.Version, "")
	}
}

// TestNewDefaultAssemblerDefaults verifies default values applied at construction.
func TestNewDefaultAssemblerDefaults(t *testing.T) {
	a := NewDefaultAssembler(Config{}, fakeDeps{logger: zap.NewNop()})
	cfg := a.Cfg()
	if cfg.RecentKeep != DefaultRecentKeep {
		t.Errorf("RecentKeep default = %d, want %d", cfg.RecentKeep, DefaultRecentKeep)
	}
	if cfg.CompactTailTokens != DefaultTailTokens {
		t.Errorf("CompactTailTokens default = %d, want %d", cfg.CompactTailTokens, DefaultTailTokens)
	}
	if cfg.CompactRatio != DefaultCompactRatio {
		t.Errorf("CompactRatio default = %v, want %v", cfg.CompactRatio, DefaultCompactRatio)
	}
}

// TestNewDefaultAssemblerOverrides verifies caller values are honoured.
func TestNewDefaultAssemblerOverrides(t *testing.T) {
	a := NewDefaultAssembler(Config{
		RecentKeep:         12,
		ToolResultMaxBytes: 1024,
		ContextWindow:      8000,
	}, fakeDeps{logger: zap.NewNop()})
	cfg := a.Cfg()
	if cfg.RecentKeep != 12 {
		t.Errorf("RecentKeep = %d, want 12", cfg.RecentKeep)
	}
	if cfg.ToolResultMaxBytes != 1024 {
		t.Errorf("ToolResultMaxBytes = %d, want 1024", cfg.ToolResultMaxBytes)
	}
	if cfg.ContextWindow != 8000 {
		t.Errorf("ContextWindow = %d, want 8000", cfg.ContextWindow)
	}
}

// TestEstimateCharsOver4 verifies the default token estimator.
func TestEstimateCharsOver4(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},           // 1 char → ceil(1/4)=1
		{"abcd", 1},        // 4 chars → ceil(4/4)=1
		{"abcde", 2},       // 5 chars → ceil(5/4)=2
		{"hello world", 3}, // 11 chars → ceil(11/4)=3
		{"中文", 1},          // 2 runes → ceil(2/4)=1
		{"🎉🎉🎉🎉🎉", 2},       // 5 runes → ceil(5/4)=2
	}
	for _, c := range cases {
		if got := EstimateCharsOver4(c.in); got != c.want {
			t.Errorf("EstimateCharsOver4(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
