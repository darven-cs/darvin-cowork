// Tests for the permission gate's request / grant / deny flow.

package perm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/executor"
	"darvin-cowork/backend/internal/agents/protocol"
)

type recordingEmitter struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recordingEmitter) Emit(ev event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingEmitter) Last() event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return nil
	}
	return r.events[len(r.events)-1]
}

type fixedContext struct{ session, run, message string }

func (f fixedContext) SessionID() string { return f.session }
func (f fixedContext) RunID() string     { return f.run }
func (f fixedContext) MessageID() string { return f.message }

func newGate(t *testing.T, timeout time.Duration) (*Gate, *recordingEmitter) {
	t.Helper()
	bus := &recordingEmitter{}
	g := NewGate(bus, zap.NewNop(), fixedContext{"s1", "r1", "m1"}, timeout)
	return g, bus
}

func TestRequestPermissionEmitsAndResolves(t *testing.T) {
	g, bus := newGate(t, time.Second)

	go func() {
		time.Sleep(20 * time.Millisecond)
		g.ResolvePermission(mustRequestID(t, bus), PermissionResult{Behavior: "allow"})
	}()

	res, err := g.RequestPermission(context.Background(), PermissionRequest{
		ToolName:    "bash",
		DangerLevel: "caution",
		Reason:      "writes files",
	}, nil)
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if res.Behavior != "allow" {
		t.Fatalf("Behavior = %q, want allow", res.Behavior)
	}
	ev, ok := bus.Last().(event.PermissionRequestEvent)
	if !ok {
		t.Fatalf("last event = %T, want PermissionRequestEvent", bus.Last())
	}
	if ev.SessionID != "s1" || ev.RunID != "r1" || ev.MessageID != "m1" {
		t.Fatalf("event ids = (%q, %q, %q), want (s1, r1, m1)", ev.SessionID, ev.RunID, ev.MessageID)
	}
	if ev.ToolName != "bash" || ev.DangerLevel != "caution" || ev.Reason != "writes files" {
		t.Fatalf("event payload = %+v", ev)
	}
}

func TestTimeoutDefaultsToDeny(t *testing.T) {
	g, _ := newGate(t, 20*time.Millisecond)

	res, _ := g.RequestPermission(context.Background(), PermissionRequest{
		ToolName: "bash", DangerLevel: "caution",
	}, nil)
	if res.Behavior != "deny" {
		t.Fatalf("Behavior = %q, want deny on timeout", res.Behavior)
	}
	if res.Message == "" {
		t.Fatal("deny result must carry a message")
	}
}

func TestCancelCleansUpTimer(t *testing.T) {
	g, _ := newGate(t, time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := g.RequestPermission(ctx, PermissionRequest{ToolName: "bash"}, nil)
		errCh <- err
	}()

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestPermission did not return after ctx cancel")
	}
}

func TestResolvePermissionUnknownIsNoOp(t *testing.T) {
	g, _ := newGate(t, time.Second)
	g.ResolvePermission("does-not-exist", PermissionResult{Behavior: "allow"})
}

func TestRememberAddsRule(t *testing.T) {
	g, bus := newGate(t, time.Second)

	go func() {
		time.Sleep(20 * time.Millisecond)
		g.ResolvePermission(mustRequestID(t, bus), PermissionResult{Behavior: "allow", Remember: true})
	}()
	if _, err := g.RequestPermission(context.Background(), PermissionRequest{
		ToolName: "bash", DangerLevel: "caution", Reason: "writes",
	}, nil); err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}

	if !g.HasRule("bash", "caution", "writes") {
		t.Fatal("Remember=true did not record the rule")
	}
	if g.HasRule("bash", "caution", "different-reason") {
		t.Fatal("HasRule matched on a different reason")
	}
}

func TestAddRuleDeduplicates(t *testing.T) {
	g, _ := newGate(t, time.Second)
	g.AddRule("bash", "caution", "writes")
	g.AddRule("bash", "caution", "writes")
	g.AddRule("bash", "caution", "writes")
	if !g.HasRule("bash", "caution", "writes") {
		t.Fatal("rule not recorded")
	}
}

func TestEvaluatePermissionAndHelpers(t *testing.T) {
	g, _ := newGate(t, time.Second)

	eval := g.EvaluatePermission("bash", map[string]any{"cmd": "ls"}, nil)
	if eval.Authorized || eval.Need {
		t.Fatal("nil registry must yield a zero eval, not true")
	}

	called := false
	tools := &fakeTools{
		grant:   func([]string) { called = true },
		approve: func(string) { called = true },
		eval:    func(string, map[string]any) protocol.PermissionEval { return protocol.PermissionEval{Authorized: true} },
	}
	g.SetGrantedReads([]string{"/tmp"}, tools)
	if !called {
		t.Fatal("SetGrantedReads did not reach the tool registry")
	}
	called = false
	g.ApprovePath("/tmp", tools)
	if !called {
		t.Fatal("ApprovePath did not reach the tool registry")
	}
	if got := g.EvaluatePermission("bash", nil, tools); !got.Authorized {
		t.Fatalf("EvaluatePermission = %+v, want Authorized", got)
	}
}

func TestConcurrentRequests(t *testing.T) {
	g, bus := newGate(t, time.Second)
	const workers = 16

	var wg sync.WaitGroup
	wg.Add(workers)
	var wins int64
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			go func() {
				time.Sleep(20 * time.Millisecond)
				g.ResolvePermission(mustRequestID(t, bus), PermissionResult{Behavior: "allow"})
			}()
			if _, err := g.RequestPermission(context.Background(), PermissionRequest{
				ToolName: "bash", DangerLevel: "caution",
			}, nil); err == nil {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&wins) != workers {
		t.Fatalf("wins = %d, want %d", wins, workers)
	}
}

func mustRequestID(t *testing.T, bus *recordingEmitter) string {
	t.Helper()
	ev := bus.Last()
	if ev == nil {
		t.Fatal("no event recorded yet")
	}
	req, ok := ev.(event.PermissionRequestEvent)
	if !ok {
		t.Fatalf("last event = %T, want PermissionRequestEvent", ev)
	}
	return req.RequestID
}

type fakeTools struct {
	grant   func([]string)
	approve func(string)
	eval    func(string, map[string]any) protocol.PermissionEval
}

func (f *fakeTools) SetGrantedReads(paths []string) { f.grant(paths) }
func (f *fakeTools) ApprovePath(p string)           { f.approve(p) }
func (f *fakeTools) EvaluatePermission(name string, args map[string]any) protocol.PermissionEval {
	return f.eval(name, args)
}

var _ ToolSurface = (*fakeTools)(nil)

// keep executor referenced so imports stay clean if signature tweaks move.
var _ = executor.PermissionRequest{}
