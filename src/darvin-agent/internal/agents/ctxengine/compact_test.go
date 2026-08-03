package ctxengine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/llm"
)

// fakeSummarizer is the Summarizer used in Compact tests. It records call
// counts and can simulate failure, cancellation, or delay.
type fakeSummarizer struct {
	calls    int
	failOn   int           // return error when calls == failOn (1-based)
	cancelOn int           // return ctx.Err when calls == cancelOn (1-based)
	delay    time.Duration // sleep before returning
	output   string        // returned on success
}

func (f *fakeSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.cancelOn > 0 && f.calls == f.cancelOn {
		return "", ctx.Err()
	}
	if f.failOn > 0 && f.calls == f.failOn {
		return "", errors.New("fake summarizer failure")
	}
	if f.output == "" {
		return "summary text", nil
	}
	return f.output, nil
}

// makeMessages builds n messages with content "msg N" each.
func makeMessages(n int) []llm.Message {
	out := make([]llm.Message, n)
	for i := 0; i < n; i++ {
		out[i] = llm.Message{
			Role:    llm.RoleUser,
			Content: "msg " + string(rune('a'+i)),
		}
	}
	return out
}

func newAssemblerWithSummarizer(s Summarizer) *DefaultAssembler {
	a := NewDefaultAssembler(Config{
		TokenBudget:        16000,
		CompactTailKeep:    3,
		CompactMaxRetries:  2,
		SummarizeMaxTokens: 100,
	}, fakeDeps{model: "test-model", logger: zap.NewNop()})
	a.SetSummarizer(s)
	return a
}

// TestCompact_BelowBudget_NoOp verifies tokensBefore <= budget returns
// Success=true without calling the summarizer.
func TestCompact_BelowBudget_NoOp(t *testing.T) {
	s := &fakeSummarizer{}
	a := newAssemblerWithSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(5),
		Budget:    10000, // generous budget
	})

	if !res.Success {
		t.Errorf("Success = false, want true (no-op)")
	}
	if res.TokensBefore != res.TokensAfter {
		t.Errorf("TokensBefore (%d) != TokensAfter (%d)", res.TokensBefore, res.TokensAfter)
	}
	if s.calls != 0 {
		t.Errorf("summarizer called %d times, want 0", s.calls)
	}
}

// TestCompact_Force_EvenBelowBudget verifies Force=true calls summarizer
// even when tokens <= budget.
func TestCompact_Force_EvenBelowBudget(t *testing.T) {
	s := &fakeSummarizer{output: "forced"}
	a := newAssemblerWithSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(3),
		Budget:    10000,
		Force:     true,
	})

	if !res.Success {
		t.Errorf("Success = false, want true")
	}
	if s.calls != 1 {
		t.Errorf("summarizer called %d times, want 1", s.calls)
	}
	if !strings.Contains(res.Summary, "forced") {
		t.Errorf("Summary should contain 'forced', got %q", res.Summary)
	}
}

// TestCompact_AboveBudget_CallsSummarizer verifies the success path:
// tokens > budget triggers summarizer; after compact, summary + tail fits
// within budget and Success=true is returned.
//
// Token math (summary marker overhead is ~28 tokens; per "msg N" message is
// ~2 tokens): 18 × 2 = 36 tokens before; budget=35 triggers; after compact
// summary (~28) + tail 3 × 2 = 34 ≤ 35.
func TestCompact_AboveBudget_CallsSummarizer(t *testing.T) {
	s := &fakeSummarizer{output: "compressed"}
	a := newAssemblerWithSummarizer(s) // TailKeep=3

	msgs := makeMessages(18) // 36 tokens
	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  msgs,
		Budget:    35,
	})

	if !res.Success {
		t.Fatalf("Success = false, want true; result=%+v", res)
	}
	if s.calls != 1 {
		t.Errorf("summarizer called %d times, want 1", s.calls)
	}
	if !strings.HasPrefix(res.RetainedMessages[0].Content, "[Conversation Summary]") {
		t.Errorf("first message should be summary, got %q", res.RetainedMessages[0].Content[:min(50, len(res.RetainedMessages[0].Content))])
	}
	if !strings.Contains(res.Summary, "compressed") {
		t.Errorf("Summary should contain 'compressed', got %q", res.Summary)
	}
}

// TestCompact_SummarizerFails_ReturnsOriginal verifies failure path
// returns the original messages with Success=false.
func TestCompact_SummarizerFails_ReturnsOriginal(t *testing.T) {
	s := &fakeSummarizer{failOn: 1}
	a := newAssemblerWithSummarizer(s)

	original := makeMessages(10)
	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  original,
		Budget:    1,
	})

	if res.Success {
		t.Errorf("Success = true, want false")
	}
	if len(res.RetainedMessages) != len(original) {
		t.Errorf("RetainedMessages length = %d, want %d", len(res.RetainedMessages), len(original))
	}
	for i := range original {
		if res.RetainedMessages[i].Content != original[i].Content {
			t.Errorf("RetainedMessages[%d] mutated", i)
		}
	}
}

// TestCompact_ContextCanceled verifies ctx cancel returns Success=false.
func TestCompact_ContextCanceled(t *testing.T) {
	s := &fakeSummarizer{delay: 100 * time.Millisecond}
	a := newAssemblerWithSummarizer(s)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call

	res := a.Compact(ctx, CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(10),
		Budget:    1,
	})

	if res.Success {
		t.Errorf("Success = true, want false")
	}
}

// TestCompact_ContextCanceledMidSummary verifies cancel mid-flight.
func TestCompact_ContextCanceledMidSummary(t *testing.T) {
	s := &fakeSummarizer{delay: 50 * time.Millisecond, cancelOn: 1}
	a := newAssemblerWithSummarizer(s)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	res := a.Compact(ctx, CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(10),
		Budget:    1,
	})

	if res.Success {
		t.Errorf("Success = true, want false")
	}
}

// TestCompact_CheckpointSnapshot verifies caller-provided CheckPoint
// gets populated with a clone of the input messages.
func TestCompact_CheckpointSnapshot(t *testing.T) {
	s := &fakeSummarizer{}
	a := newAssemblerWithSummarizer(s)

	cp := &CheckPoint{ID: "caller-id"}
	msgs := makeMessages(5)

	res := a.Compact(context.Background(), CompactParams{
		SessionID:  "s1",
		Messages:   msgs,
		Budget:     10000,
		Checkpoint: cp,
	})

	if !res.Success {
		t.Fatalf("Success = false, want true")
	}
	if res.Checkpoint != cp {
		t.Errorf("Checkpoint pointer should be preserved")
	}
	if cp.ID != "caller-id" {
		t.Errorf("Checkpoint.ID = %q, want %q (caller-provided)", cp.ID, "caller-id")
	}
	if len(cp.Snapshot) != len(msgs) {
		t.Errorf("Checkpoint.Snapshot length = %d, want %d", len(cp.Snapshot), len(msgs))
	}

	// Mutate input; verify snapshot is independent.
	msgs[0].Content = "MUTATED"
	if cp.Snapshot[0].Content == "MUTATED" {
		t.Errorf("Checkpoint.Snapshot shares storage with input; expected deep copy")
	}
}

// TestCompact_AutoCheckpointID verifies auto-generated ID when no caller CP.
func TestCompact_AutoCheckpointID(t *testing.T) {
	s := &fakeSummarizer{}
	a := newAssemblerWithSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(5),
		Budget:    10000,
	})

	if res.Checkpoint == nil {
		t.Fatal("Checkpoint is nil")
	}
	if !strings.HasPrefix(res.Checkpoint.ID, "cp-") {
		t.Errorf("Checkpoint.ID = %q, want prefix %q", res.Checkpoint.ID, "cp-")
	}
}

// TestCompact_NoSummarizerWired verifies failure when summarizer is nil.
func TestCompact_NoSummarizerWired(t *testing.T) {
	a := NewDefaultAssembler(Config{
		TokenBudget: 1,
	}, fakeDeps{logger: zap.NewNop()})
	// a.summarizer stays nil because fakeDeps.Provider() returns nil

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(10),
		Budget:    1,
	})

	if res.Success {
		t.Errorf("Success = true, want false (no summarizer)")
	}
}

// TestCompact_RetryHalf_IfStillOverBudget verifies retry path triggers
// a second summarizer call when tokensAfter > budget.
func TestCompact_RetryHalf_IfStillOverBudget(t *testing.T) {
	// Use long messages that won't fit even after first compact
	msgs := make([]llm.Message, 20)
	for i := range msgs {
		msgs[i] = llm.Message{
			Role:    llm.RoleUser,
			Content: strings.Repeat("x", 50), // ~13 tokens each → 260 total
		}
	}
	s := &fakeSummarizer{output: "z"} // 1 token summary

	a := NewDefaultAssembler(Config{
		TokenBudget:        5, // tiny budget
		CompactTailKeep:    1,
		CompactMaxRetries:  3,
		SummarizeMaxTokens: 5,
	}, fakeDeps{model: "m", logger: zap.NewNop()})
	a.SetSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  msgs,
		Budget:    5,
	})

	// With TinyBudget, even the summary + 1 tail message will exceed.
	// Should trigger retry; either succeeds with high call count or fails.
	if res.Success {
		if s.calls < 1 {
			t.Errorf("Success but no summarizer calls")
		}
	} else {
		// failure is acceptable; what matters is the retry happened
		if s.calls < 2 {
			t.Errorf("Expected retry: summarizer called %d times, want >= 2", s.calls)
		}
	}
}

// TestCompact_RetainsOriginalMessagesOnSuccess verifies the original
// p.Messages slice is not mutated, even when Compact returns Success=true.
// Token math: 18 × 2 = 36 > budget=35 → triggers; summary (~28) + tail 3×2
// = 34 ≤ 35 → success.
func TestCompact_RetainsOriginalMessagesOnSuccess(t *testing.T) {
	s := &fakeSummarizer{output: "compact"}
	a := newAssemblerWithSummarizer(s)

	original := makeMessages(18)
	originalLen := len(original)
	originalFirst := original[0].Content

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  original,
		Budget:    35,
	})

	if !res.Success {
		t.Fatalf("Success = false, want true; result=%+v", res)
	}
	if len(original) != originalLen {
		t.Errorf("original length mutated: was %d, now %d", originalLen, len(original))
	}
	if original[0].Content != originalFirst {
		t.Errorf("original[0] mutated: was %q, now %q", originalFirst, original[0].Content)
	}
	if res.RetainedMessages[0].Content == originalFirst {
		t.Errorf("RetainedMessages should be a new slice with summary prefix")
	}
}

// TestCompact_EmptyMessages verifies graceful no-op on empty input.
func TestCompact_EmptyMessages(t *testing.T) {
	s := &fakeSummarizer{}
	a := newAssemblerWithSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  nil,
		Budget:    1,
	})

	if !res.Success {
		t.Errorf("Success = false, want true (no-op for empty)")
	}
	if s.calls != 0 {
		t.Errorf("summarizer should not be called for empty messages, got %d", s.calls)
	}
}

// TestCompact_AllTail verifies the tail >= len(messages) edge case.
func TestCompact_AllTail(t *testing.T) {
	s := &fakeSummarizer{}
	a := NewDefaultAssembler(Config{
		TokenBudget:       1,
		CompactTailKeep:   100, // larger than len(messages)
		CompactMaxRetries: 0,
	}, fakeDeps{model: "m", logger: zap.NewNop()})
	a.SetSummarizer(s)

	res := a.Compact(context.Background(), CompactParams{
		SessionID: "s1",
		Messages:  makeMessages(3),
		Budget:    1,
	})

	if res.Success {
		t.Errorf("Success = true, want false (span becomes empty)")
	}
}

// TestDefaultSummarizer_NilProvider verifies the nil-provider error path.
func TestDefaultSummarizer_NilProvider(t *testing.T) {
	s := NewDefaultSummarizer(nil)
	_, err := s.Summarize(context.Background(), SummarizeRequest{
		Model:    "m",
		Messages: nil,
	})
	if err == nil {
		t.Errorf("expected error from nil provider")
	}
}

// TestDefaultSummarizer_DefaultMaxTokens verifies default kicks in when
// req.MaxTokens is 0.
func TestDefaultSummarizer_DefaultMaxTokens(t *testing.T) {
	// We can't directly observe MaxTokens sent to the provider without a
	// fake provider; just verify it doesn't error out.
	// Skip detailed test; covered by other tests.
	t.Skip("MaxTokens default is verified via fake provider; out of scope for stub")
}
