package gateway

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agents"
	"darvin-cowork/backend/internal/agents/event"
	"darvin-cowork/backend/internal/agents/session"
	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/harness"
	"darvin-cowork/backend/internal/tools"
)

// newTestAgentFactoryForSessionMgr builds the factory used by the
// sessionmgr tests. Spec 04 added harness resolution; we wire the no-op
// test harness so resolveHarness passes without the production registry.
// newTestAgentFactoryForSessionMgr builds the factory used by the
// sessionmgr tests. Spec 04 added harness resolution; we wire the no-op
// test harness so resolveHarness passes without the production registry.
//
// Tests that need the prompt path to actually start a turn (e.g.
// stoppedUntil / evict tests that observe Acp.Agent.IsRunning) need an
// embedded harness that drives Agent.Prompt + Agent.Run. The selector here
// returns the no-op harness because attachAcp in this file uses
// installRunningAcp to side-step the factory path and keep the turn
// observable.
func newTestAgentFactoryForSessionMgr() *acp.AgentFactory {
	return &acp.AgentFactory{
		Provider: &blockingProvider{},
		Tools:    tool.NewRegistry(),
		Store:    store.NewMemoryStore(),
		Logger:   zap.NewNop(),
		Selector: func(*agent.Agent, *acp.AgentFactory) (harness.Harness, error) { return acp.HarnessForTest, nil },
	}
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{21}$`)

type nowCounter struct {
	mu sync.Mutex
	t  int64
}

func (n *nowCounter) now() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.t
}

func (n *nowCounter) advance(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.t += d.Milliseconds()
}

func withFakeClock(maxSessions int, idleTtl time.Duration) (*SessionManager, *nowCounter) {
	clk := &nowCounter{t: 1_700_000_000_000}
	m := NewSessionManager()
	m.nowMs = clk.now
	m.maxSessions = maxSessions
	m.idleTtl = idleTtl
	return m, clk
}

func waitForRunning(t *testing.T, sub *event.Subscription) {
	t.Helper()
	select {
	case <-sub.C():
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not start running")
	}
}

func waitForCondition(t *testing.T, cond func() bool) {
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

// installRunningAcp 用 blocking provider 构 AcpSession 并把一个 turn
// 推入 in-flight 状态,然后手动挂到 m 的 entry 上 —— 等价于懒建路径
// 跑完之后的样子,只是绕开 GetOrCreateEntry 的工厂调用,避免在不动
// factory 的测试里双建。
//
// 后台 goroutine 是镜像 attachAcpLocked 的 cancel 监听;evict 测试
// 靠 poll Submit 拿 ErrLoopClosed 验证 Loop 确实被关掉。
func installRunningAcp(t *testing.T, m *SessionManager, id, runID string) *acp.AcpSession {
	t.Helper()
	// The factory's selector runs after Build, so it can wire a harness
	// whose Run closure drives the freshly-built agent.
	factory := newTestAgentFactoryForSessionMgr()
	factory.Selector = func(a *agent.Agent, _ *acp.AgentFactory) (harness.Harness, error) {
		return acp.NewEmbeddedTestHarness(a), nil
	}
	sess, err := factory.NewAcpSession(id)
	if err != nil {
		t.Fatalf("NewAcpSession(%q): %v", id, err)
	}
	sub := sess.Agent.Subscribe(64)
	t.Cleanup(func() {
		sub.Unsubscribe()
		m.mu.Lock()
		e := m.byID[id]
		var cancel context.CancelFunc
		if e != nil && e.cancel != nil {
			cancel = e.cancel
			e.cancel = nil
		}
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		sess.Close()
	})
	ticket, err := sess.Loop.Submit(acp.PromptRequest{RunID: runID, Content: "test"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if ticket.RunID != runID {
		t.Fatalf("RunID = %q, want %q", ticket.RunID, runID)
	}
	waitForRunning(t, sub)
	m.mu.Lock()
	e, ok := m.byID[id]
	if !ok {
		e = &SessionEntry{
			Session:       session.NewSession(id),
			lastTouchedMs: m.nowMs(),
		}
		m.byID[id] = e
		e.idleElem = m.idleOrder.PushFront(id)
	}
	e.Acp = sess
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		sess.Loop.Close()
	}()
	return sess
}

func TestNewSessionManagerStartsEmpty(t *testing.T) {
	m := NewSessionManager()
	if m.Has(DefaultSessionID) {
		t.Fatalf("expected fresh manager to have no entries; got %q pre-registered", DefaultSessionID)
	}
	if n := m.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
}

func TestCreateOrGetDefaultsToDefaultSession(t *testing.T) {
	m := NewSessionManager()
	sess, msgID, err := m.CreateOrGet("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sess.ID != DefaultSessionID {
		t.Fatalf("session id = %q, want %q", sess.ID, DefaultSessionID)
	}
	if !idRe.MatchString(msgID) {
		t.Fatalf("message id shape: %q", msgID)
	}
	if !m.Has(sess.ID) {
		t.Fatalf("expected Has(%s) true", sess.ID)
	}
}

func TestCreateOrGetEmptyAndDefaultAgree(t *testing.T) {
	m := NewSessionManager()
	a, _, _ := m.CreateOrGet("")
	b, _, _ := m.CreateOrGet(DefaultSessionID)
	if a != b {
		t.Fatalf("expected same session instance, got %p vs %p", a, b)
	}
}

func TestCreateOrGetReusesSession(t *testing.T) {
	m := NewSessionManager()
	a, _, _ := m.CreateOrGet("")
	b, msgID2, _ := m.CreateOrGet(a.ID)
	if a != b {
		t.Fatalf("expected same session instance, got %p vs %p", a, b)
	}
	if !idRe.MatchString(msgID2) {
		t.Fatalf("msgID shape: %q", msgID2)
	}
}

func TestCreateOrGetDistinctMessageIDs(t *testing.T) {
	m := NewSessionManager()
	_, a, _ := m.CreateOrGet("")
	_, b, _ := m.CreateOrGet("")
	if a == b {
		t.Fatalf("expected distinct message ids: %q", a)
	}
}

func TestHasReturnsFalseForUnknown(t *testing.T) {
	m := NewSessionManager()
	if m.Has("nope") {
		t.Fatalf("expected Has false for unknown id")
	}
}

func TestNanoidUniqueness(t *testing.T) {
	m := NewSessionManager()
	seen := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		_, msgID, _ := m.CreateOrGet("")
		if !idRe.MatchString(msgID) {
			t.Fatalf("message id shape at %d: %q", i, msgID)
		}
		if _, dup := seen[msgID]; dup {
			t.Fatalf("collision at %d: %q", i, msgID)
		}
		seen[msgID] = struct{}{}
	}
}

func TestGetOrCreateEntryReuses(t *testing.T) {
	m, _ := withFakeClock(100, time.Hour)
	a, err := m.GetOrCreateEntry("a")
	if err != nil {
		t.Fatalf("first GetOrCreateEntry: %v", err)
	}
	b, err := m.GetOrCreateEntry("a")
	if err != nil {
		t.Fatalf("second GetOrCreateEntry: %v", err)
	}
	if a != b {
		t.Fatalf("expected same entry pointer, got %p vs %p", a, b)
	}
}

func TestGetOrCreateEntrySerializes(t *testing.T) {
	m := NewSessionManager()
	const goroutines = 16
	var wg sync.WaitGroup
	results := make([]*SessionEntry, goroutines)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := m.GetOrCreateEntry("shared")
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			results[i] = e
		}(i)
	}
	wg.Wait()
	for i := 1; i < goroutines; i++ {
		if results[0] != results[i] {
			t.Fatalf("goroutine %d got a different entry: %p vs %p", i, results[i], results[0])
		}
	}
	if n := m.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1 (shared only)", n)
	}
}

func TestLRUEvictsIdleOnCap(t *testing.T) {
	m, _ := withFakeClock(3, time.Hour)
	for _, id := range []string{"s1", "s2", "s3"} {
		if _, err := m.GetOrCreateEntry(id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if n := m.Len(); n != 3 {
		t.Fatalf("Len before cap-hit = %d, want 3", n)
	}
	for _, id := range []string{"s2", "s3", "s4"} {
		if _, err := m.GetOrCreateEntry(id); err != nil {
			t.Fatalf("touch %s: %v", id, err)
		}
	}
	if n := m.Len(); n != 3 {
		t.Fatalf("Len after eviction = %d, want 3", n)
	}
	if m.Has("s1") {
		t.Fatalf("s1 must be evicted (LRU tail)")
	}
	for _, id := range []string{"s2", "s3", "s4"} {
		if !m.Has(id) {
			t.Fatalf("expected %q retained", id)
		}
	}
}

func TestActiveRunNotEvicted(t *testing.T) {
	m, _ := withFakeClock(2, time.Hour)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	installRunningAcp(t, m, DefaultSessionID, "default-run")
	m.mu.Lock()
	def := m.byID[DefaultSessionID]
	if def.idleElem != nil {
		m.idleOrder.MoveToBack(def.idleElem)
	}
	m.mu.Unlock()

	if _, err := m.GetOrCreateEntry("s1"); err != nil {
		t.Fatalf("seed s1: %v", err)
	}
	if _, err := m.GetOrCreateEntry("s2"); err != nil {
		t.Fatalf("GetOrCreateEntry s2: %v", err)
	}
	if !m.Has(DefaultSessionID) {
		t.Fatalf("default session with active run must NOT be evicted")
	}
	if !m.Has("s2") {
		t.Fatalf("s2 must be retained (newly created)")
	}
	if m.Has("s1") {
		t.Fatalf("s1 should be the LRU victim (idle), but it remained")
	}
}

func TestActiveRunOnlyReturnsLimit(t *testing.T) {
	m, _ := withFakeClock(2, time.Hour)
	for _, id := range []string{DefaultSessionID, "s1"} {
		if _, err := m.GetOrCreateEntry(id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		installRunningAcp(t, m, id, id+"-run")
	}
	_, err := m.GetOrCreateEntry("s2")
	if !errors.Is(err, ErrSessionsLimit) {
		t.Fatalf("err = %v, want ErrSessionsLimit", err)
	}
}

func TestReapIdleSessionsRemovesStale(t *testing.T) {
	m, clk := withFakeClock(100, 100*time.Millisecond)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if _, err := m.GetOrCreateEntry("fresh"); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	clk.advance(150 * time.Millisecond)
	if _, err := m.GetOrCreateEntry("after-window"); err != nil {
		t.Fatalf("seed after-window: %v", err)
	}
	m.reapIdleSessions()
	if m.Has(DefaultSessionID) || m.Has("fresh") {
		t.Fatalf("expected default + fresh reaped; got default=%v fresh=%v",
			m.Has(DefaultSessionID), m.Has("fresh"))
	}
	if !m.Has("after-window") {
		t.Fatalf("after-window must survive")
	}
}

func TestReapIdleSessionsKeepsActive(t *testing.T) {
	m, clk := withFakeClock(100, 50*time.Millisecond)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	installRunningAcp(t, m, DefaultSessionID, "run-1")
	clk.advance(200 * time.Millisecond)
	m.reapIdleSessions()
	if !m.Has(DefaultSessionID) {
		t.Fatalf("active-run session must survive reap")
	}
}

func TestStopByRunIdMatches(t *testing.T) {
	m, _ := withFakeClock(100, time.Hour)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	sess := installRunningAcp(t, m, DefaultSessionID, "run-1")
	if err := m.Stop(DefaultSessionID, "run-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForCondition(t, func() bool { return sess.Loop.ActiveRunID() == "" })
	m.mu.Lock()
	def := m.byID[DefaultSessionID]
	m.mu.Unlock()
	if def.stoppedUntilMs <= def.lastTouchedMs {
		t.Fatalf("stoppedUntilMs (%d) should be > lastTouchedMs (%d)",
			def.stoppedUntilMs, def.lastTouchedMs)
	}
}

func TestStopReturnsNotFoundAndMismatch(t *testing.T) {
	m, _ := withFakeClock(100, time.Hour)
	if err := m.Stop("nope", "r"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := m.Stop(DefaultSessionID, "r"); !errors.Is(err, ErrRunMismatch) {
		t.Fatalf("no-Acp stop: err = %v, want ErrRunMismatch", err)
	}
	installRunningAcp(t, m, DefaultSessionID, "actual")
	if err := m.Stop(DefaultSessionID, "stale"); !errors.Is(err, ErrRunMismatch) {
		t.Fatalf("wrong-id stop: err = %v, want ErrRunMismatch", err)
	}
}

func TestStoppedUntilBlocksPrompt(t *testing.T) {
	m, clk := withFakeClock(100, time.Hour)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	installRunningAcp(t, m, DefaultSessionID, "run-1")
	if err := m.Stop(DefaultSessionID, "run-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	clk.advance(500 * time.Millisecond)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); !errors.Is(err, ErrSessionStalled) {
		t.Fatalf("inside-window prompt: err = %v, want ErrSessionStalled", err)
	}
	clk.advance(600 * time.Millisecond)
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		t.Fatalf("past-window prompt returned %v, want nil", err)
	}
}

func TestSessionManager_LazyBuildPerSession(t *testing.T) {
	prov := &blockingProvider{}
	factory := &acp.AgentFactory{
		Provider: prov,
		Tools:    tool.NewRegistry(),
		Store:    store.NewMemoryStore(),
		Logger:   zap.NewNop(),
		Selector: func(*agent.Agent, *acp.AgentFactory) (harness.Harness, error) { return acp.HarnessForTest, nil },
	}
	m := NewSessionManager(WithAgentFactory(factory))
	a, err := m.GetOrCreateEntry("a")
	if err != nil {
		t.Fatalf("GetOrCreateEntry a: %v", err)
	}
	b, err := m.GetOrCreateEntry("b")
	if err != nil {
		t.Fatalf("GetOrCreateEntry b: %v", err)
	}
	if a.Acp == nil || b.Acp == nil {
		t.Fatalf("expected both entries to have AcpSession built; got a=%v b=%v", a.Acp, b.Acp)
	}
	if a.Acp == b.Acp {
		t.Fatalf("two ids share the same AcpSession — per-session isolation broken")
	}
	if a.Acp.SessionID != "a" || b.Acp.SessionID != "b" {
		t.Fatalf("AcpSessionID mismatch: a=%q b=%q", a.Acp.SessionID, b.Acp.SessionID)
	}
}

func TestSessionManager_StopGoesToPerSessionLoop(t *testing.T) {
	m := NewSessionManager()
	sa := installRunningAcp(t, m, "a", "run-a")
	if _, err := m.GetOrCreateEntry("b"); err != nil {
		t.Fatalf("GetOrCreateEntry b: %v", err)
	}
	sb := installRunningAcp(t, m, "b", "run-b")

	if err := m.Stop("a", "run-a"); err != nil {
		t.Fatalf("Stop a/run-a: %v", err)
	}
	waitForCondition(t, func() bool { return sa.Loop.ActiveRunID() == "" })
	if got := sb.Loop.ActiveRunID(); got != "run-b" {
		t.Fatalf("b's loop.ActiveRunID = %q, want %q (must NOT have been cancelled)", got, "run-b")
	}
}

func TestSessionManager_EvictClosesAcpSession(t *testing.T) {
	m, _ := withFakeClock(2, time.Hour)
	if _, err := m.GetOrCreateEntry("a"); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	sa := installRunningAcp(t, m, "a", "run-a")
	if err := m.Stop("a", "run-a"); err != nil {
		t.Fatalf("Stop a: %v", err)
	}
	waitForCondition(t, func() bool { return sa.Loop.ActiveRunID() == "" })

	if _, err := m.GetOrCreateEntry("b"); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := m.GetOrCreateEntry("c"); err != nil {
		t.Fatalf("seed c: %v", err)
	}
	if m.Has("a") {
		t.Fatalf("a must be evicted as LRU tail")
	}
	waitForCondition(t, func() bool {
		_, err := sa.Loop.Submit(acp.PromptRequest{Content: "after-evict"})
		return errors.Is(err, acp.ErrLoopClosed)
	})
}

// Provider 故意为 nil 触发 agent.New 的 ErrProviderRequired —— 真实启动
// 期会遇到的失败(配置错误 / 拿不到 key)比 mock factory 更贴实际。
func TestSessionManager_LazyBuildFailureRollsBack(t *testing.T) {
	factory := &acp.AgentFactory{
		Provider: nil,
		Logger:   zap.NewNop(),
	}
	m := NewSessionManager(WithAgentFactory(factory))

	_, err := m.GetOrCreateEntry("doomed")
	if !errors.Is(err, agent.ErrProviderRequired) {
		t.Fatalf("err = %v, want ErrProviderRequired", err)
	}
	if m.Has("doomed") {
		t.Fatalf("failed entry leaked into byID; subsequent GetOrCreateEntry would skip the factory retry")
	}
	if n := m.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
}

// FR-8 阶段 2 回归:renderer 启动期给历史 session 发 subscribe_events 留下
// 大量 AcpSession=nil 的 entry,首个 prompt 到该 id 时 GetOrCreateEntry 命中
// "现有 entry"分支,需要补建 AcpSession,不能直接返 CodeNoAcpSession。
func TestSessionManager_PromptUpgradesEntryCreatedBySubscribe(t *testing.T) {
	factory := &acp.AgentFactory{
		Provider: &blockingProvider{},
		Tools:    tool.NewRegistry(),
		Store:    store.NewMemoryStore(),
		Logger:   zap.NewNop(),
		Selector: func(*agent.Agent, *acp.AgentFactory) (harness.Harness, error) { return acp.HarnessForTest, nil },
	}
	m := NewSessionManager(WithAgentFactory(factory))

	if _, err := m.EnsureEntry("sub-then-prompt"); err != nil {
		t.Fatalf("EnsureEntry: %v", err)
	}
	m.mu.Lock()
	empty := m.byID["sub-then-prompt"]
	m.mu.Unlock()
	if empty.Acp != nil {
		t.Fatalf("EnsureEntry leaked AcpSession: %+v", empty)
	}

	upgraded, err := m.GetOrCreateEntry("sub-then-prompt")
	if err != nil {
		t.Fatalf("GetOrCreateEntry upgrade: %v", err)
	}
	if upgraded.Acp == nil {
		t.Fatalf("expected AcpSession built on the upgrade path; got nil")
	}
	if upgraded.Acp.SessionID != "sub-then-prompt" {
		t.Fatalf("AcpSessionID = %q, want %q", upgraded.Acp.SessionID, "sub-then-prompt")
	}
	if n := m.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1 (no leak)", n)
	}

	second, err := m.GetOrCreateEntry("sub-then-prompt")
	if err != nil {
		t.Fatalf("second GetOrCreateEntry: %v", err)
	}
	if second.Acp != upgraded.Acp {
		t.Fatalf("second call must reuse the upgraded AcpSession, not rebuild")
	}
}
