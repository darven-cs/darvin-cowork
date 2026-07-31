package gateway

import (
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

// withFakeClock 给 SessionManager 注入 fake clock 与可调上限。
//
// NewSessionManager 用真实时间戳注册 default,所以这里把它踢掉用 fake
// 时钟重新注册,后续 TTL 比较才不会被真实时钟污染。
func withFakeClock(maxSessions int, idleTtl time.Duration) (*SessionManager, *nowCounter) {
	clk := &nowCounter{t: 1_700_000_000_000}
	m := NewSessionManager()
	m.nowMs = clk.now
	m.maxSessions = maxSessions
	m.idleTtl = idleTtl

	m.mu.Lock()
	if def, ok := m.byID[DefaultSessionID]; ok {
		def.lastTouchedMs = clk.t
		if def.idleElem != nil {
			m.idleOrder.MoveToBack(def.idleElem)
		}
	}
	m.mu.Unlock()

	return m, clk
}

func TestDefaultSessionRegisteredUpFront(t *testing.T) {
	m := NewSessionManager()
	if !m.Has(DefaultSessionID) {
		t.Fatalf("expected Has(%q) true before any CreateOrGet", DefaultSessionID)
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
	if n := m.Len(); n != 2 {
		t.Fatalf("Len = %d, want 2 (default + shared)", n)
	}
}

// TestLRUEvictsIdleOnCap:软上限路径。max 已满时新 id 触发 LRU 驱逐 LRU
// 尾的 idle entry;active run 不在被驱逐之列。
func TestLRUEvictsIdleOnCap(t *testing.T) {
	m, _ := withFakeClock(3, time.Hour)
	if _, err := m.GetOrCreateEntry("s1"); err != nil {
		t.Fatalf("seed s1: %v", err)
	}
	if _, err := m.GetOrCreateEntry("s2"); err != nil {
		t.Fatalf("seed s2: %v", err)
	}
	if n := m.Len(); n != 3 {
		t.Fatalf("Len before cap-hit = %d, want 3", n)
	}
	if _, err := m.GetOrCreateEntry("s3"); err != nil {
		t.Fatalf("GetOrCreateEntry s3: %v", err)
	}
	if n := m.Len(); n != 3 {
		t.Fatalf("Len after eviction = %d, want 3", n)
	}
	if m.Has(DefaultSessionID) {
		t.Fatalf("expected %q evicted (LRU tail)", DefaultSessionID)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if !m.Has(id) {
			t.Fatalf("expected %q retained", id)
		}
	}
}

// TestActiveRunNotEvicted:核心不变量。default 被钉成 active run 后,新 id
// 的 LRU 驱逐会跳过它,退而求其次驱逐 s1。
func TestActiveRunNotEvicted(t *testing.T) {
	m, _ := withFakeClock(2, time.Hour)
	def, err := m.GetOrCreateEntry(DefaultSessionID)
	if err != nil {
		t.Fatalf("GetOrCreateEntry default: %v", err)
	}
	def.activeRun = &activeRunState{runId: "default-run"}
	// 把 default 挪到 LRU 尾部,这样它就会成为 evictLRULocked 第一个扫到的。
	m.mu.Lock()
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

// TestActiveRunOnlyReturnsLimit:全是 active 时 evictLRULocked 找不到候选,
// 上抛 ErrSessionsLimit 而不是无视 active run 驱逐。
func TestActiveRunOnlyReturnsLimit(t *testing.T) {
	m, _ := withFakeClock(2, time.Hour)
	for _, id := range []string{DefaultSessionID, "s1"} {
		e, err := m.GetOrCreateEntry(id)
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		e.activeRun = &activeRunState{runId: id + "-run"}
	}
	_, err := m.GetOrCreateEntry("s2")
	if !errors.Is(err, ErrSessionsLimit) {
		t.Fatalf("err = %v, want ErrSessionsLimit", err)
	}
}

// TestReapIdleSessionsRemovesStale:fake clock 驱动 TTL。
func TestReapIdleSessionsRemovesStale(t *testing.T) {
	m, clk := withFakeClock(100, 100*time.Millisecond)
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

// TestReapIdleSessionsKeepsActive:active run 不被 reap。
func TestReapIdleSessionsKeepsActive(t *testing.T) {
	m, clk := withFakeClock(100, 50*time.Millisecond)
	def, err := m.GetOrCreateEntry(DefaultSessionID)
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	def.activeRun = &activeRunState{runId: "run-1", cancelRun: func() {}}
	clk.advance(200 * time.Millisecond)
	m.reapIdleSessions()
	if !m.Has(DefaultSessionID) {
		t.Fatalf("active-run session must survive reap")
	}
}

// TestStopByRunIdMatches:Stop 命中 active run,触发 cancel + 写 stoppedUntil。
func TestStopByRunIdMatches(t *testing.T) {
	m, _ := withFakeClock(100, time.Hour)
	def, err := m.GetOrCreateEntry(DefaultSessionID)
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	calls := atomic.Int32{}
	def.activeRun = &activeRunState{
		runId:     "run-1",
		startedMs: 1,
		msgId:     "m-1",
		cancelRun: func() { calls.Add(1) },
	}
	if err := m.Stop(DefaultSessionID, "run-1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelRun called %d times, want 1", calls.Load())
	}
	if def.stoppedUntilMs <= def.lastTouchedMs {
		t.Fatalf("stoppedUntilMs (%d) should be > lastTouchedMs (%d)",
			def.stoppedUntilMs, def.lastTouchedMs)
	}
}

// TestStopReturnsNotFoundAndMismatch:Stop 拒绝未匹配的三种情况。
func TestStopReturnsNotFoundAndMismatch(t *testing.T) {
	m, _ := withFakeClock(100, time.Hour)
	if err := m.Stop("nope", "r"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session: err = %v, want ErrSessionNotFound", err)
	}
	def, err := m.GetOrCreateEntry(DefaultSessionID)
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := m.Stop(DefaultSessionID, "r"); !errors.Is(err, ErrRunMismatch) {
		t.Fatalf("no-run stop: err = %v, want ErrRunMismatch", err)
	}
	def.activeRun = &activeRunState{runId: "actual"}
	if err := m.Stop(DefaultSessionID, "stale"); !errors.Is(err, ErrRunMismatch) {
		t.Fatalf("wrong-id stop: err = %v, want ErrRunMismatch", err)
	}
}

// TestStoppedUntilBlocksPrompt:Stop 后的拒绝窗口把同一 session 的下一个 prompt
// 挡掉;窗口外放行。
func TestStoppedUntilBlocksPrompt(t *testing.T) {
	m, clk := withFakeClock(100, time.Hour)
	def, err := m.GetOrCreateEntry(DefaultSessionID)
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}
	def.activeRun = &activeRunState{runId: "run-1", cancelRun: func() {}}
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
