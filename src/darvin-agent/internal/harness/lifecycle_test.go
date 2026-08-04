package harness

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunAttemptSuccess(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	h.run = func(context.Context, RunAttemptParams) (*AttemptResult, error) {
		return &AttemptResult{Status: AttemptOK, AssistantText: "done", TotalTurns: 2}, nil
	}

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1",
		Prompt:    "hi",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if res.Status != AttemptOK || res.AssistantText != "done" || res.TotalTurns != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res.Superseded {
		t.Fatal("result marked superseded without a competing reset")
	}
}

func TestRunAttemptValidatesParams(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	cases := []struct {
		name   string
		params RunAttemptParams
		want   error
	}{
		{"no session", RunAttemptParams{Prompt: "hi"}, ErrSessionIDRequired},
		{"blank session", RunAttemptParams{SessionID: "  ", Prompt: "hi"}, ErrSessionIDRequired},
		{"no prompt", RunAttemptParams{SessionID: "s1"}, ErrPromptRequired},
		{"blank prompt", RunAttemptParams{SessionID: "s1", Prompt: "  "}, ErrPromptRequired},
	}
	for _, tc := range cases {
		if _, err := RunAttemptWithLifecycle(context.Background(), h, tc.params); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := RunAttemptWithLifecycle(context.Background(), nil, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	}); !errors.Is(err, ErrHarnessRequired) {
		t.Fatalf("nil harness err = %v, want ErrHarnessRequired", err)
	}
}

func TestRunAttemptMintsMissingIDsAndKeepsGivenOnes(t *testing.T) {
	resetGlobals(t)

	var got RunAttemptParams
	h := newStub("alpha")
	h.run = func(_ context.Context, p RunAttemptParams) (*AttemptResult, error) {
		got = p
		return &AttemptResult{Status: AttemptOK}, nil
	}

	if _, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	}); err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if got.RunID == "" || got.MessageID == "" || got.UserMessageID == "" {
		t.Fatalf("ids not minted: %+v", got)
	}
	if got.MessageID == got.UserMessageID {
		t.Fatal("assistant and user message ids collide")
	}

	if _, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi", RunID: "run-1", MessageID: "msg-1", UserMessageID: "user-1",
	}); err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if got.RunID != "run-1" || got.MessageID != "msg-1" || got.UserMessageID != "user-1" {
		t.Fatalf("caller-supplied ids overwritten: %+v", got)
	}
}

func TestRunAttemptError(t *testing.T) {
	resetGlobals(t)

	boom := errors.New("provider exploded")
	h := newStub("alpha")
	h.run = func(context.Context, RunAttemptParams) (*AttemptResult, error) { return nil, boom }

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the harness error", err)
	}
	if res == nil {
		t.Fatal("result is nil on the error path")
	}
	if res.Status != AttemptError || !errors.Is(res.LastError, boom) {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunAttemptAbort(t *testing.T) {
	resetGlobals(t)

	var aborted bool
	h := newStub("alpha")
	h.run = func(ctx context.Context, _ RunAttemptParams) (*AttemptResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := RunAttemptWithLifecycle(ctx, h, RunAttemptParams{
		SessionID:      "s1",
		Prompt:         "hi",
		OnAttemptAbort: func() { aborted = true },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Status != AttemptAborted {
		t.Fatalf("status = %q, want %q", res.Status, AttemptAborted)
	}
	if !aborted {
		t.Fatal("OnAttemptAbort was not called")
	}
}

func TestRunAttemptTimeout(t *testing.T) {
	resetGlobals(t)

	var timedOut bool
	h := newStub("alpha")
	h.run = func(ctx context.Context, _ RunAttemptParams) (*AttemptResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID:        "s1",
		Prompt:           "hi",
		TimeoutMs:        20,
		OnAttemptTimeout: func() { timedOut = true },
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if res.Status != AttemptTimeout {
		t.Fatalf("status = %q, want %q", res.Status, AttemptTimeout)
	}
	if !timedOut {
		t.Fatal("OnAttemptTimeout was not called")
	}
}

func TestRunAttemptContainsPanic(t *testing.T) {
	resetGlobals(t)

	h := newStub("rogue")
	h.run = func(context.Context, RunAttemptParams) (*AttemptResult, error) {
		panic("plugin bug")
	}

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	})
	if err == nil {
		t.Fatal("a panicking harness did not surface as an error")
	}
	if res.Status != AttemptError {
		t.Fatalf("status = %q, want %q", res.Status, AttemptError)
	}
}

func TestRunAttemptSupersededByReset(t *testing.T) {
	resetGlobals(t)

	h := newStub("alpha")
	h.run = func(context.Context, RunAttemptParams) (*AttemptResult, error) {
		BumpLifecycleGeneration("s1")
		return &AttemptResult{Status: AttemptOK}, nil
	}

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if !res.Superseded {
		t.Fatal("an attempt raced by a reset was not marked superseded")
	}
	if res.LifecycleGen != 0 {
		t.Fatalf("LifecycleGen = %d, want the generation the attempt started under", res.LifecycleGen)
	}
}

func TestLifecycleGenerationIsPerSession(t *testing.T) {
	resetGlobals(t)

	if got := LifecycleGeneration("s1"); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	if got := BumpLifecycleGeneration("s1"); got != 1 {
		t.Fatalf("bumped generation = %d, want 1", got)
	}
	if got := LifecycleGeneration("s2"); got != 0 {
		t.Fatalf("s2 generation = %d, want an independent counter", got)
	}
}

func TestRunAttemptClassifies(t *testing.T) {
	resetGlobals(t)

	h := classifyStub{stubHarness: newStub("alpha"), label: ClassificationDrift}
	h.caps.Classify = true

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if res.Classification != ClassificationDrift {
		t.Fatalf("Classification = %q, want %q", res.Classification, ClassificationDrift)
	}
}

func TestRunAttemptSkipsUndeclaredClassifier(t *testing.T) {
	resetGlobals(t)

	h := classifyStub{stubHarness: newStub("alpha"), label: ClassificationDrift}

	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID: "s1", Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if res.Classification != "" {
		t.Fatalf("Classification = %q, want empty for an undeclared classifier", res.Classification)
	}
}

func TestRunAttemptReportsProgressCallbacks(t *testing.T) {
	resetGlobals(t)

	var started bool
	var phases []ExecutionPhase
	h := newStub("alpha")
	h.run = func(_ context.Context, p RunAttemptParams) (*AttemptResult, error) {
		p.OnExecutionPhase(PhaseModel)
		p.OnRunProgress(RunProgress{Turn: 1, Phase: PhaseModel})
		time.Sleep(time.Millisecond)
		return &AttemptResult{Status: AttemptOK}, nil
	}

	var progress []RunProgress
	res, err := RunAttemptWithLifecycle(context.Background(), h, RunAttemptParams{
		SessionID:          "s1",
		Prompt:             "hi",
		OnExecutionStarted: func() { started = true },
		OnExecutionPhase:   func(p ExecutionPhase) { phases = append(phases, p) },
		OnRunProgress:      func(p RunProgress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatalf("RunAttemptWithLifecycle: %v", err)
	}
	if !started {
		t.Fatal("OnExecutionStarted was not called")
	}
	want := []ExecutionPhase{PhaseStarting, PhaseModel, PhaseSettling}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
	if len(progress) != 1 || progress[0].Turn != 1 {
		t.Fatalf("progress = %v", progress)
	}
	if res.DurationMs < 0 {
		t.Fatalf("DurationMs = %d", res.DurationMs)
	}
}
