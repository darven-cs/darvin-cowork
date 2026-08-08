package agentloop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	agent "darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/llm"
	tool "darvin-cowork/backend/internal/tools"
)

// scriptedProvider replays a fixed StreamEvent script and closes the
// channel, which drives the executor to a natural stop.
type scriptedProvider struct{ events []llm.StreamEvent }

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *scriptedProvider) Stream(_ context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return llm.NewStreamingResponse(ch, nil), nil
}

// blockingProvider emits one delta then blocks until ctx is cancelled,
// which is how the abort / busy tests hold the Agent in stateRunning.
type blockingProvider struct{}

func (p *blockingProvider) Name() string { return "blocking" }
func (p *blockingProvider) Complete(_ context.Context, _ *llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *blockingProvider) Stream(ctx context.Context, _ *llm.CompletionRequest) (*llm.StreamingResponse, error) {
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.TextDeltaEvent{Delta: "x"}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return llm.NewStreamingResponse(ch, nil), nil
}

// newLoopForTest builds an Agent bound to session "default" plus the Loop
// wrapping it, with AttachMessageIDSrc wired exactly like main.go so the
// emitted events carry EventCommon.MessageID. The harness is a thin
// forwarder that calls Agent.Prompt + Agent.Run, mirroring what the
// embedded harness's Run closure would do in production.
func newLoopForTest(t *testing.T, p llm.ModelProvider) (*agent.Agent, *Loop) {
	t.Helper()
	a, err := agent.New(agent.NewAgentConfig{
		Session:  session.NewSession("default"),
		Provider: p,
		Tools:    tool.NewRegistry(),
		Store:    store.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	loop := NewLoop(a, NewEmbeddedTestHarness(a))
	a.AttachMessageIDSrc(loop.CurrentMessageID)
	return a, loop
}

// collect drains sub until the deadline or until an agent_end arrives.
func collect(t *testing.T, sub *event.Subscription, budget time.Duration) []event.Event {
	t.Helper()
	var got []event.Event
	deadline := time.After(budget)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return got
			}
			got = append(got, ev)
			if ev.EventName() == "agent_end" {
				return got
			}
		case <-deadline:
			return got
		}
	}
}

func names(evs []event.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.EventName())
	}
	return out
}

func TestLoopEnd2End(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hello "},
		llm.TextDeltaEvent{Delta: "world"},
		llm.DoneEvent{Response: llm.CompletionResponse{
			Model: "test", Content: "hello world", FinishReason: llm.FinishReasonStop,
		}},
	}}
	a, loop := newLoopForTest(t, prov)
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()

	ticket, err := loop.Submit(PromptRequest{Content: "hi"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(ticket.MessageID) != messageIDLen {
		t.Fatalf("messageID len = %d, want %d", len(ticket.MessageID), messageIDLen)
	}
	if len(ticket.RunID) != messageIDLen {
		t.Fatalf("runID len = %d, want %d", len(ticket.RunID), messageIDLen)
	}
	if ticket.Queued {
		t.Fatalf("first Submit on an idle loop must not be queued")
	}

	got := collect(t, sub, 2*time.Second)
	gotNames := names(got)
	if len(gotNames) == 0 || gotNames[len(gotNames)-1] != "agent_end" {
		t.Fatalf("last event should be agent_end, got %v", gotNames)
	}

	// Every event must carry the session id so the gateway can route the
	// notification. messageId is additionally expected on everything the
	// executor emits; run_start / run_end are run-scoped rather than
	// prompt-scoped and deliberately leave it empty.
	var sawTextDelta, sawLLMEnd bool
	for _, ev := range got {
		c := ev.Common()
		if c.SessionID != "default" {
			t.Errorf("%s: sessionId = %q, want \"default\"", ev.EventName(), c.SessionID)
		}
		switch ev.EventName() {
		case "run_start", "run_end":
			if c.MessageID != "" {
				t.Errorf("%s: messageId = %q, want empty", ev.EventName(), c.MessageID)
			}
			continue
		case "text_delta":
			sawTextDelta = true
		case "llm_end":
			sawLLMEnd = true
		}
		if c.MessageID != ticket.MessageID {
			t.Errorf("%s: messageId = %q, want %q", ev.EventName(), c.MessageID, ticket.MessageID)
		}
	}
	if !sawTextDelta {
		t.Errorf("no text_delta in %v", gotNames)
	}
	if !sawLLMEnd {
		t.Errorf("no llm_end in %v", gotNames)
	}
}

// TestLoopPersistsUserAndAssistantWithDistinctIDs is the regression test
// for the user-message-loss bug: persistUserMessage used to key the
// user row with the same messageID persistAssistantMessages uses for the
// assistant row, so the assistant upsert silently overwrote the user's
// question. The Loop must mint a distinct user message id per turn.
func TestLoopPersistsUserAndAssistantWithDistinctIDs(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.TextDeltaEvent{Delta: "hello "},
		llm.TextDeltaEvent{Delta: "world"},
		llm.DoneEvent{Response: llm.CompletionResponse{
			Model: "test", Content: "hello world", FinishReason: llm.FinishReasonStop,
		}},
	}}
	ms := newLoopPersistStore(t)
	a, err := agent.New(agent.NewAgentConfig{
		Name:         "test",
		Session:      session.NewSession("default"),
		Provider:     prov,
		Tools:        tool.NewRegistry(),
		Store:        store.NewMemoryStore(),
		MessageStore: ms,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	loop := NewLoop(a, NewEmbeddedTestHarness(a))
	a.AttachMessageIDSrc(loop.CurrentMessageID)
	a.AttachRunIDSrc(loop.CurrentRunID)
	a.AttachUserMessageIDSrc(loop.CurrentUserMessageID)

	ticket, err := loop.Submit(PromptRequest{Content: "what is 1+1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	sub := a.Subscribe(64)
	got := collect(t, sub, 2*time.Second)
	if len(got) == 0 || got[len(got)-1].EventName() != "agent_end" {
		t.Fatalf("turn did not complete, last event %v", names(got))
	}

	rows, err := ms.List(context.Background(), "default", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (user + assistant), got %d", len(rows))
	}
	var user, asst *store.MessageRecord
	for i := range rows {
		if rows[i].Role == "user" {
			user = &rows[i]
		} else {
			asst = &rows[i]
		}
	}
	if user == nil || asst == nil {
		t.Fatalf("want both user and assistant rows, got %+v", rows)
	}
	if user.ID == asst.ID {
		t.Fatalf("user and assistant rows share id %q: assistant upsert overwrote the user message", user.ID)
	}
	if user.Content != "what is 1+1" {
		t.Errorf("user content = %q, want the prompt", user.Content)
	}
	// The user row must be sealed (done=true) from the start: StreamingText
	// renders the "thinking" pulse when done=false, so an unsealed persisted
	// user row would show as a spinner instead of the question after reload.
	if !user.Done {
		t.Errorf("user row done = false, want true so the reloaded renderer shows the question text")
	}
	if asst.Content != "hello world" {
		t.Errorf("assistant content = %q, want \"hello world\"", asst.Content)
	}
	// Each assistant turn gets its own row id (msgID-{index}) so a multi-turn
	// tool loop replays fully without overwriting; the user row is keyed by
	// runUserMsgID. The renderer reads these ids verbatim on reload.
	if !strings.HasPrefix(asst.ID, ticket.MessageID+"-") {
		t.Errorf("assistant id = %q, want prefix %q-{index}", asst.ID, ticket.MessageID)
	}
	if asst.ID == user.ID {
		t.Errorf("assistant and user rows share id %q", asst.ID)
	}
	if user.ID == ticket.MessageID {
		t.Errorf("user id must differ from the run messageID %q", ticket.MessageID)
	}
}

// newLoopPersistStore returns a fresh SQLiteMessageStore on a temp file.
func newLoopPersistStore(t *testing.T) *store.SQLiteMessageStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "loop.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&store.Session{}, &store.Message{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return store.NewSQLiteMessageStore(db)
}

// TestSubmitHonoursCallerRunID: main mints the runId so Stop can target
// the turn; Loop must not overwrite it.
func TestSubmitHonoursCallerRunID(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	ticket, err := loop.Submit(PromptRequest{RunID: "run-from-main", Content: "hi"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ticket.RunID != "run-from-main" {
		t.Fatalf("runID = %q, want %q", ticket.RunID, "run-from-main")
	}
	waitRunning(t, sub)
	if got := loop.activeRunID(); got != "run-from-main" {
		t.Fatalf("activeRun.runID = %q, want %q", got, "run-from-main")
	}
}

// TestLoop_ActiveRunID: idle returns "", in-flight returns the
// current runID, and after the turn completes it returns "" again —
// strictly reflects only the in-flight state.
func TestLoop_ActiveRunID(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	if got := loop.ActiveRunID(); got != "" {
		t.Fatalf("idle ActiveRunID() = %q, want \"\"", got)
	}

	ticket, err := loop.Submit(PromptRequest{RunID: "live", Content: "hi"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, sub)
	if got := loop.ActiveRunID(); got != ticket.RunID {
		t.Fatalf("in-flight ActiveRunID() = %q, want %q", got, ticket.RunID)
	}

	if !loop.Stop(ticket.RunID) {
		t.Fatalf("Stop(%q) must report true", ticket.RunID)
	}
	waitFor(t, func() bool { return loop.ActiveRunID() == "" })
}

func TestLoopAbort(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()

	if _, err := loop.Submit(PromptRequest{Content: "hi"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, sub)

	if err := loop.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	got := names(collect(t, sub, 2*time.Second))
	if len(got) == 0 || got[len(got)-1] != "agent_end" {
		t.Fatalf("abort should still terminate with agent_end, got %v", got)
	}
}

// TestStopMatchesRunID: Stop is runId-scoped so a stale abort from a
// client that missed the previous turn's end cannot kill the new turn.
func TestStopMatchesRunID(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	if _, err := loop.Submit(PromptRequest{RunID: "live", Content: "hi"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, sub)

	if loop.Stop("stale") {
		t.Fatalf("Stop with a mismatched runID must report false")
	}
	if loop.activeRunID() != "live" {
		t.Fatalf("mismatched Stop must leave the active run alone")
	}
	if !loop.Stop("live") {
		t.Fatalf("Stop with the live runID must report true")
	}
	got := names(collect(t, sub, 2*time.Second))
	if len(got) == 0 || got[len(got)-1] != "agent_end" {
		t.Fatalf("stop should terminate the run with agent_end, got %v", got)
	}
}

// TestSubmitQueuesBehindActiveRun pins same-session serialisation: a
// second Submit parks instead of being rejected, and runs once the first
// turn ends — the messageID only flips at that point.
func TestSubmitQueuesBehindActiveRun(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	first, err := loop.Submit(PromptRequest{RunID: "first", Content: "first"})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	waitRunning(t, sub)

	second, err := loop.Submit(PromptRequest{RunID: "second", Content: "second"})
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if !second.Queued {
		t.Fatalf("second Submit must be queued behind the active run")
	}
	// A parked request must not disturb the in-flight run's messageID —
	// the events it is still emitting have to stay correlated.
	if got := loop.CurrentMessageID(); got != first.MessageID {
		t.Fatalf("CurrentMessageID while queued = %q, want %q", got, first.MessageID)
	}

	if !loop.Stop("first") {
		t.Fatalf("Stop(first) must report true")
	}
	// Stop drops the parked queue, so the queued turn never runs and the
	// messageID stays on the aborted turn.
	waitFor(t, func() bool { return loop.activeRunID() == "" })
	if got := loop.CurrentMessageID(); got != first.MessageID {
		t.Fatalf("CurrentMessageID after Stop = %q, want %q", got, first.MessageID)
	}
	if got := loop.queueLen(); got != 0 {
		t.Fatalf("Stop must drop the parked queue, len = %d", got)
	}
}

// TestQueuedTurnRunsAfterActiveRunEnds: a parked request is picked up on
// its own once the preceding turn terminates naturally.
func TestQueuedTurnRunsAfterActiveRunEnds(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.DoneEvent{Response: llm.CompletionResponse{
			Model: "test", Content: "ok", FinishReason: llm.FinishReasonStop,
		}},
	}}
	a, loop := newLoopForTest(t, prov)
	sub := a.Subscribe(256)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	if _, err := loop.Submit(PromptRequest{RunID: "r1", Content: "first"}); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	second, err := loop.Submit(PromptRequest{RunID: "r2", Content: "second"})
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}

	waitFor(t, func() bool {
		return loop.CurrentMessageID() == second.MessageID && loop.activeRunID() == ""
	})
	if got := loop.queueLen(); got != 0 {
		t.Fatalf("queue should be drained, len = %d", got)
	}
}

// TestSteerJumpsAheadOfFollowUp: steer content is consumed before the
// follow-ups already parked, and it cancels the in-flight turn.
func TestSteerJumpsAheadOfFollowUp(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(256)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	if _, err := loop.Submit(PromptRequest{RunID: "r1", Content: "first"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, sub)
	if _, err := loop.Submit(PromptRequest{RunID: "r2", Content: "follow-up"}); err != nil {
		t.Fatalf("follow-up Submit: %v", err)
	}
	steer, err := loop.Steer(PromptRequest{RunID: "r3", Content: "steer"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if !steer.Queued {
		t.Fatalf("Steer during an active run must be queued")
	}
	// Steer cancels r1, so the run goroutine reaches the queues; steerQueue
	// is served before the parked follow-up.
	waitFor(t, func() bool { return loop.activeRunID() == "r3" })
	if got := loop.CurrentMessageID(); got != steer.MessageID {
		t.Fatalf("CurrentMessageID = %q, want the steer's %q", got, steer.MessageID)
	}
}

// TestSteerOnIdleLoopStartsTurn: with nothing in flight Steer is just a
// Submit — no queue hop, no cancellation.
func TestSteerOnIdleLoopStartsTurn(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	ticket, err := loop.Steer(PromptRequest{RunID: "r1", Content: "hi"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ticket.Queued {
		t.Fatalf("Steer on an idle loop must start immediately")
	}
	waitRunning(t, sub)
	if got := loop.activeRunID(); got != "r1" {
		t.Fatalf("activeRun.runID = %q, want \"r1\"", got)
	}
}

func TestCloseRejectsFurtherSubmit(t *testing.T) {
	a, loop := newLoopForTest(t, &blockingProvider{})
	sub := a.Subscribe(64)
	defer sub.Unsubscribe()

	if _, err := loop.Submit(PromptRequest{Content: "hi"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, sub)

	loop.Close()
	// Idempotent: SessionManager may Close an already-evicted entry.
	loop.Close()

	if _, err := loop.Submit(PromptRequest{Content: "after close"}); !errors.Is(err, ErrLoopClosed) {
		t.Fatalf("Submit after Close: err = %v, want ErrLoopClosed", err)
	}
	if _, err := loop.Steer(PromptRequest{Content: "after close"}); !errors.Is(err, ErrLoopClosed) {
		t.Fatalf("Steer after Close: err = %v, want ErrLoopClosed", err)
	}
}

// TestConcurrentSubmitRunsOneTurnAtATime: the whole point of the single
// run goroutine — parallel Submits never overlap two Agent.Run calls.
func TestConcurrentSubmitRunsOneTurnAtATime(t *testing.T) {
	prov := &scriptedProvider{events: []llm.StreamEvent{
		llm.DoneEvent{Response: llm.CompletionResponse{
			Model: "test", Content: "ok", FinishReason: llm.FinishReasonStop,
		}},
	}}
	a, loop := newLoopForTest(t, prov)
	sub := a.Subscribe(512)
	defer sub.Unsubscribe()
	t.Cleanup(loop.Close)

	const submits = 8
	var wg sync.WaitGroup
	for i := 0; i < submits; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := loop.Submit(PromptRequest{Content: "hi"}); err != nil {
				t.Errorf("Submit %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Every submitted turn eventually runs: count the run_start events.
	starts := 0
	deadline := time.After(5 * time.Second)
	for starts < submits {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatalf("bus closed after %d run_start", starts)
			}
			if ev.EventName() == "run_start" {
				starts++
			}
		case <-deadline:
			t.Fatalf("only %d/%d turns ran", starts, submits)
		}
	}
	waitFor(t, func() bool { return loop.queueLen() == 0 && loop.activeRunID() == "" })
}

// activeRunID reports the in-flight turn's runID, "" when idle.
func (l *Loop) activeRunID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeRun == nil {
		return ""
	}
	return l.activeRun.runID
}

// queueLen reports how many requests are parked across both queues.
func (l *Loop) queueLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.steerQueue) + len(l.followUpQueue)
}

// waitFor polls cond until it holds or the budget runs out. The Loop's
// state transitions happen on its own goroutine, so tests observe them
// rather than being handed a synchronisation point.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within budget")
}

// waitRunning blocks until the first event of a run arrives. Agent.Run
// flips its state to running before emitting run_start, so observing any
// event guarantees the turn is in flight.
func waitRunning(t *testing.T, sub *event.Subscription) {
	t.Helper()
	select {
	case <-sub.C():
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not start running")
	}
}
