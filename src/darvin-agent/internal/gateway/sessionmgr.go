// Package gateway 内部:Gateway 的 per-session 状态索引。
//
// SessionManager 持有从 session id 到 *SessionEntry 的 in-memory map。
// 持久化走 agent/store,这里只承载"当前活跃哪些 session"的状态。
package gateway

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/agent/session"
)

const (
	sessionIDLen    = 21
	sessionAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// DefaultSessionID 是 Gateway 当前唯一预置的 session id。
	// main.go 启动时订阅它,renderer 可以早于首个 user session 建连。
	DefaultSessionID = "default"

	// DefaultMaxSessions 是 SessionManager 的软上限。超出时新 GetOrCreateEntry
	// 先 reap/evict idle entry;若全是 active run 则返回 ErrSessionsLimit。
	DefaultMaxSessions = 5000

	// DefaultIdleTTL 是 idle entry 在内存中的最长存活时间。
	DefaultIdleTTL = 24 * time.Hour

	// DefaultStopWindow 是 Stop 后的拒绝窗口:同一个 session 在该窗口内的新
	// prompt 返回 ErrSessionStalled,避免 abort race。
	DefaultStopWindow = 1000 * time.Millisecond
)

// activeRunState 描述一个 in-flight turn;Stop 通过 cancelRun 中断 LLM 流。
type activeRunState struct {
	runId     string
	cancelRun context.CancelFunc
	startedMs int64
	msgId     string
}

// SessionEntry 是 SessionManager 为单个 session 持有的状态。
// 字段由 SessionManager 的 mutex 保护,handler 不直接写。
type SessionEntry struct {
	Session *session.Session

	lastTouchedMs  int64
	stoppedUntilMs int64

	cancel    context.CancelFunc
	activeRun *activeRunState

	// idleElem 是该 entry 在 LRU 链表中的位置;nil 表示已被移出(reap 时)。
	idleElem *list.Element
}

var (
	// ErrSessionsLimit:GetOrCreateEntry 在 cap 已满且无可驱逐 idle 时返回。
	// 语义:不要为了塞新 session 而打断 active run,让 caller 提示用户关掉后台。
	ErrSessionsLimit = errors.New("sessionmgr: sessions limit reached")

	// ErrSessionNotFound:Stop 收到未知 sessionID。
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrRunMismatch:Stop 收到与当前 active run 不匹配的 runId。
	// 这通常意味着该 run 已经结束或被别的调用停了,Stop 是 no-op。
	ErrRunMismatch = errors.New("sessionmgr: run id mismatch")

	// ErrSessionStalled:prompt 落在 Stop 之后的拒绝窗口内。
	ErrSessionStalled = errors.New("sessionmgr: session stalled, retry after stop window")
)

// SessionManager:从 session id 到 *SessionEntry 的 in-memory 索引。
// 进程本地,跨进程不共享。
type SessionManager struct {
	mu sync.Mutex

	byID      map[string]*SessionEntry
	idleOrder *list.List

	maxSessions int
	idleTtl     time.Duration
	stopWindow  time.Duration

	// 测试可注入 fake clock。
	nowMs func() int64

	idGen func() string
}

// NewSessionManager 用默认上限与 DefaultSessionID 预置的 manager。
// 默认 session 在构造时即注册 —— 这样 subscribe 可以在首个 prompt 之前完成,
// 否则客户端要跟 prompt 回复抢时序,可能漏掉 run 的开场事件。
func NewSessionManager() *SessionManager {
	m := &SessionManager{
		byID:        make(map[string]*SessionEntry),
		idleOrder:   list.New(),
		maxSessions: DefaultMaxSessions,
		idleTtl:     DefaultIdleTTL,
		stopWindow:  DefaultStopWindow,
		nowMs:       func() int64 { return time.Now().UnixMilli() },
		idGen:       nanoid.MustCustomASCII(sessionAlphabet, sessionIDLen),
	}
	if _, err := m.GetOrCreateEntry(DefaultSessionID); err != nil {
		// DefaultMaxSessions=0 才会触发,正常构造里不会发生;留 panic 是为了让
		// 配置错误在启动期爆炸而不是后面 silent。
		panic("sessionmgr: NewSessionManager failed to register default session: " + err.Error())
	}
	return m
}

// DefaultID 返回 DefaultSessionID。
func (m *SessionManager) DefaultID() string { return DefaultSessionID }

// Has 报告 id 是否已被 SessionManager 见过。subscribe handler 用它来在
// 触碰 ledger 前对未知 id 早失败。
func (m *SessionManager) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.byID[id]
	return ok
}

// GetOrCreateEntry 返回 id 对应的 SessionEntry;未知 id 时创建。
//
// 命中现有 entry 时,先检查 stoppedUntilMs(落在拒绝窗口内返回 ErrSessionStalled),
// 再刷新 lastTouchedMs 并把 entry 提到 LRU 头部。
//
// 达到 maxSessions 时先 reapIdleLocked(TTL 收割),如果仍超,evict 一个 LRU
// tail 的 idle entry;若全部都是 active run,返回 ErrSessionsLimit ——
// 不主动 evict active run,避免打断正在跑的 turn。
func (m *SessionManager) GetOrCreateEntry(id string) (*SessionEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.byID[id]; ok {
		if e.stoppedUntilMs > m.nowMs() {
			return nil, ErrSessionStalled
		}
		e.lastTouchedMs = m.nowMs()
		m.touchLRU(e)
		return e, nil
	}

	if len(m.byID) >= m.maxSessions {
		m.reapIdleLocked()
		if len(m.byID) >= m.maxSessions {
			if !m.evictLRULocked() {
				return nil, ErrSessionsLimit
			}
			if len(m.byID) >= m.maxSessions {
				return nil, ErrSessionsLimit
			}
		}
	}

	now := m.nowMs()
	e := &SessionEntry{
		Session:       session.NewSession(id),
		lastTouchedMs: now,
	}
	m.byID[id] = e
	e.idleElem = m.idleOrder.PushFront(id)
	return e, nil
}

// CreateOrGet 保留 (*session.Session, msgID) 的旧 API。改用 GetOrCreateEntry
// 之后,handler PR 4 会切到新接口。
func (m *SessionManager) CreateOrGet(id string) (*session.Session, string, error) {
	if id == "" {
		id = DefaultSessionID
	}
	e, err := m.GetOrCreateEntry(id)
	if err != nil {
		return nil, "", err
	}
	return e.Session, m.idGen(), nil
}

// Get:CreateOrGet 的无 err 版。
func (m *SessionManager) Get(id string) (*session.Session, string) {
	sess, msgID, _ := m.CreateOrGet(id)
	return sess, msgID
}

// Stop 中止 (sessionID, runId) 对应的 turn。
//
//   - session 未知:ErrSessionNotFound
//   - entry 没有 active run,或 runId 不匹配:ErrRunMismatch
//   - 成功:调 activeRun.cancelRun() 中断 LLM 流,并把 stoppedUntilMs
//     推到 now()+StopWindow,GetOrCreateEntry 在该窗口内会返 ErrSessionStalled
func (m *SessionManager) Stop(sessionID, runId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.byID[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if e.activeRun == nil || e.activeRun.runId != runId {
		return ErrRunMismatch
	}
	if e.activeRun.cancelRun != nil {
		e.activeRun.cancelRun()
	}
	e.stoppedUntilMs = m.nowMs() + m.stopWindow.Milliseconds()
	return nil
}

// reapIdleSessions 是定时回调入口。它把 TTL 过期且无 active run 的 entry
// 移出 (byID + LRU),Stop 窗口或长跑的 entry 不动。
func (m *SessionManager) reapIdleSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapIdleLocked()
}

// Len 返回当前 entry 数。测试用。
func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

// reapIdleLocked:从 LRU 尾往前扫,丢弃超 TTL 且无 active run 的 entry。
// 撞到 active 或未超期的 entry 时停止 —— LRU 上的"分隔带"意味着后面的
// 更年轻,可以下一轮再扫。调用方持 m.mu。
func (m *SessionManager) reapIdleLocked() {
	cutoff := m.nowMs() - m.idleTtl.Milliseconds()
	for {
		elem := m.idleOrder.Back()
		if elem == nil {
			return
		}
		id := elem.Value.(string)
		e := m.byID[id]
		if e.activeRun != nil {
			return
		}
		if e.lastTouchedMs > cutoff {
			return
		}
		m.evictLocked(id)
	}
}

// evictLRULocked:从 LRU 尾找一个无 active run 的 entry 驱逐。
// 返回是否真的驱逐了 —— 全是 active run 时返 false,调用方应理解为
// "现在不能缩容",把责任抛回。
func (m *SessionManager) evictLRULocked() bool {
	for elem := m.idleOrder.Back(); elem != nil; elem = elem.Prev() {
		id := elem.Value.(string)
		e := m.byID[id]
		if e.activeRun == nil {
			m.evictLocked(id)
			return true
		}
	}
	return false
}

// evictLocked:从 byID + LRU 中移除 id。Entry 有 active run 时是 no-op,
// 这是兜底防御 —— 调用方应该先判断 active run 状态。
func (m *SessionManager) evictLocked(id string) {
	e, ok := m.byID[id]
	if !ok {
		return
	}
	if e.activeRun != nil {
		return
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	delete(m.byID, id)
	if e.idleElem != nil {
		m.idleOrder.Remove(e.idleElem)
		e.idleElem = nil
	}
}

// touchLRU:把 e 挪到 LRU 头。调用方持 m.mu。
func (m *SessionManager) touchLRU(e *SessionEntry) {
	if e.idleElem != nil {
		m.idleOrder.MoveToFront(e.idleElem)
	}
}
