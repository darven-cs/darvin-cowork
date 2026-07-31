// Package gateway:Gateway 的 per-session 状态索引。SessionManager 持有
// session id → *SessionEntry 的内存映射;持久化走 agent/store,这里
// 只承载"当前活跃哪些 session"。每个 entry 在首次 prompt 触发懒建
// AcpSession,subscribe 路径只建 SessionEntry 不建 AcpSession,避免
// renderer 订历史 session 时拉起 5000 个 Agent。
package gateway

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jaevor/go-nanoid"

	"darvin-cowork/backend/internal/acp"
	"darvin-cowork/backend/internal/agent/session"
)

const (
	sessionIDLen    = 21
	sessionAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// DefaultSessionID 兼容 / 迁移期保留的特殊 id。renderer 仍可拿这个
	// id 发 prompt / subscribe,GetOrCreateEntry 走未知 id 分支,与
	// 任意新 id 走同一路径。
	DefaultSessionID = "default"

	// DefaultMaxSessions 是 SessionManager 的软上限。超出时先 reap / evict
	// idle entry,全是 active run 才返 ErrSessionsLimit。
	DefaultMaxSessions = 5000

	// DefaultIdleTTL 是 idle entry 在内存中的最长存活时间。
	DefaultIdleTTL = 24 * time.Hour

	// DefaultStopWindow 是 Stop 后的拒绝窗口:同一 session 在该窗口内
	// 的新 prompt 返回 ErrSessionStalled。
	DefaultStopWindow = 1000 * time.Millisecond
)

// SessionEntry 是 SessionManager 为单个 session 持有的状态,字段由
// SessionManager 的 mutex 保护,handler 不直接写。
//
// Acp 在首次 prompt 触发懒建;空 Acp 的 entry 只能订阅、不能 Submit
// / Stop。
type SessionEntry struct {
	Session *session.Session
	Acp     *acp.AcpSession

	lastTouchedMs  int64
	stoppedUntilMs int64

	// cancel 触发监听 ctx 的后台 goroutine 调 AcpSession.Loop.Close,
	// 见 attachAcpLocked。evict 路径调它。
	cancel context.CancelFunc

	idleElem *list.Element
}

var (
	// ErrSessionsLimit:GetOrCreateEntry 在 cap 已满且无可驱逐 idle 时返回。
	// 不要为了塞新 session 而打断 active run。
	ErrSessionsLimit = errors.New("sessionmgr: sessions limit reached")

	// ErrSessionNotFound:Stop 收到未知 sessionID。
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrRunMismatch:Stop 收到与当前 active run 不匹配的 runId,或该
	// session 还没建 AcpSession。Stop 在这两种情况下都是 no-op。
	ErrRunMismatch = errors.New("sessionmgr: run id mismatch")

	// ErrSessionStalled:prompt 落在 Stop 之后的拒绝窗口内。
	ErrSessionStalled = errors.New("sessionmgr: session stalled, retry after stop window")
)

// SessionManager 是 session id → *SessionEntry 的进程本地索引。
type SessionManager struct {
	mu sync.Mutex

	byID      map[string]*SessionEntry
	idleOrder *list.List

	maxSessions int
	idleTtl     time.Duration
	stopWindow  time.Duration

	nowMs func() int64

	idGen func() string

	// factory 在 GetOrCreateEntry 未知 id 分支里懒建 AcpSession;nil 时
	// 不建(handler 测试 / 老 main 走"只建 session"路径)。
	factory *acp.AgentFactory

	// ledger 在懒建路径里挂该 AcpSession 的事件 bus 订阅。
	ledger *EventLedger
}

// SessionManagerOption 配置 NewSessionManager。
type SessionManagerOption func(*SessionManager)

// WithAgentFactory 打开懒建路径:factory 注入后,GetOrCreateEntry 在未知
// id 上调 factory.NewAcpSession(id)。
func WithAgentFactory(f *acp.AgentFactory) SessionManagerOption {
	return func(m *SessionManager) { m.factory = f }
}

// WithEventLedger 把新建 AcpSession 的事件订阅挂到 WS 事件 ledger 上,
// 该 AcpSession 的事件会 fan-out 到订阅其 session id 的客户端。
func WithEventLedger(l *EventLedger) SessionManagerOption {
	return func(m *SessionManager) { m.ledger = l }
}

// NewSessionManager 构造空 manager,不预置 default session。renderer 仍
// 可拿 DefaultSessionID 发 prompt,GetOrCreateEntry 走未知 id 路径。
func NewSessionManager(opts ...SessionManagerOption) *SessionManager {
	m := &SessionManager{
		byID:        make(map[string]*SessionEntry),
		idleOrder:   list.New(),
		maxSessions: DefaultMaxSessions,
		idleTtl:     DefaultIdleTTL,
		stopWindow:  DefaultStopWindow,
		nowMs:       func() int64 { return time.Now().UnixMilli() },
		idGen:       nanoid.MustCustomASCII(sessionAlphabet, sessionIDLen),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// DefaultID 返回 DefaultSessionID。
func (m *SessionManager) DefaultID() string { return DefaultSessionID }

// Has 报告 id 是否已被 SessionManager 见过;subscribe handler 在触碰
// ledger 前用它对未知 id 早失败。
func (m *SessionManager) Has(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.byID[id]
	return ok
}

// GetOrCreateEntry 返回 id 对应的 SessionEntry;未知 id 时创建并在
// factory 注入时懒建 AcpSession。
//
// 命中现有 entry 时先检查 stoppedUntilMs(落在窗口内返回 ErrSessionStalled),
// 再刷新 lastTouchedMs 并把 entry 提到 LRU 头。FR-8 阶段 2:subscribe 早于
// prompt 留下的空 Acp entry 在首个 prompt 触发 AcpSession 懒建。懒建失败
// 时回滚 byID + LRU 防止半残 entry 卡住下次重试。cap 已满且全是 active
// run 时返回 ErrSessionsLimit —— 不主动打断 active run。
func (m *SessionManager) GetOrCreateEntry(id string) (*SessionEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.byID[id]; ok {
		if e.stoppedUntilMs > m.nowMs() {
			return nil, ErrSessionStalled
		}
		e.lastTouchedMs = m.nowMs()
		m.touchLRU(e)
		if e.Acp == nil && m.factory != nil {
			if err := m.attachAcpLocked(e); err != nil {
				return nil, err
			}
		}
		return e, nil
	}

	e, err := m.createEntryLocked(id)
	if err != nil {
		return nil, err
	}
	if m.factory != nil {
		if err := m.attachAcpLocked(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// EnsureEntry 返回 id 对应的 SessionEntry;未知 id 时只建 SessionEntry
// 不触发 AcpSession 懒建。subscribe handler 用它,避免 renderer 订
// 历史 session 时拉起 5000 个 Agent / Loop / 订阅。
//
// stoppedUntilMs / LRU / maxSessions 行为与 GetOrCreateEntry 一致。
func (m *SessionManager) EnsureEntry(id string) (*SessionEntry, error) {
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
	return m.createEntryLocked(id)
}

// createEntryLocked 在 byID + LRU 上挂一份空的 SessionEntry。超出
// maxSessions 时先 reap TTL 再 LRU 驱逐 idle,全 active 才返 ErrSessionsLimit。
// 调用方持 m.mu。
func (m *SessionManager) createEntryLocked(id string) (*SessionEntry, error) {
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

// attachAcpLocked 调 factory.NewAcpSession 把结果挂到 e 上;ledger 注入
// 时同时挂事件订阅。失败时回滚 byID + LRU。调用方持 m.mu。
func (m *SessionManager) attachAcpLocked(e *SessionEntry) error {
	acpSess, err := m.factory.NewAcpSession(e.Session.ID)
	if err != nil {
		delete(m.byID, e.Session.ID)
		if e.idleElem != nil {
			m.idleOrder.Remove(e.idleElem)
			e.idleElem = nil
		}
		return err
	}
	e.Acp = acpSess
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	// Loop.Close 会阻塞到 run goroutine 退出,放后台跑避免拖 evict。
	go func() {
		<-ctx.Done()
		acpSess.Loop.Close()
	}()
	if m.ledger != nil {
		sub := acpSess.Agent.Subscribe(64)
		m.ledger.AttachSubscription(sub)
	}
	return nil
}

// CreateOrGet 保留 (*session.Session, msgID) 的旧 API。空 id 视为
// DefaultSessionID。handler 切到 GetOrCreateEntry 后这个方法可以删。
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

// Get 是 CreateOrGet 的无 err 版。
func (m *SessionManager) Get(id string) (*session.Session, string) {
	sess, msgID, _ := m.CreateOrGet(id)
	return sess, msgID
}

// Stop 中止 (sessionID, runId) 对应的 turn。
//
//   - session 未知:ErrSessionNotFound
//   - entry 没有 AcpSession(subscribe 早于 prompt):ErrRunMismatch
//   - entry 的 Loop.Stop(runId) 报 false(run id 不匹配或当前 idle):ErrRunMismatch
//   - 成功:Loop.Stop 内部 cancel in-flight turn,本方法把 stoppedUntilMs
//     推到 now()+StopWindow,GetOrCreateEntry 在该窗口内会返 ErrSessionStalled
func (m *SessionManager) Stop(sessionID, runId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.byID[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if e.Acp == nil {
		return ErrRunMismatch
	}
	if !e.Acp.Loop.Stop(runId) {
		return ErrRunMismatch
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

// reapIdleLocked 从 LRU 尾往前扫,丢弃超 TTL 且无 active run 的 entry。
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
		if e.Acp != nil && e.Acp.Loop.ActiveRunID() != "" {
			return
		}
		if e.lastTouchedMs > cutoff {
			return
		}
		m.evictLocked(id)
	}
}

// evictLRULocked 从 LRU 尾找一个无 active run 的 entry 驱逐。全是 active
// run 时返 false,调用方理解为"现在不能缩容",把责任抛回。
func (m *SessionManager) evictLRULocked() bool {
	for elem := m.idleOrder.Back(); elem != nil; elem = elem.Prev() {
		id := elem.Value.(string)
		e := m.byID[id]
		if e.Acp == nil || e.Acp.Loop.ActiveRunID() == "" {
			m.evictLocked(id)
			return true
		}
	}
	return false
}

// evictLocked 从 byID + LRU 中移除 id。Entry 有 active run 时是 no-op,
// 这是兜底防御 —— 调用方应该先判断 active run 状态。
func (m *SessionManager) evictLocked(id string) {
	e, ok := m.byID[id]
	if !ok {
		return
	}
	if e.Acp != nil && e.Acp.Loop.ActiveRunID() != "" {
		return
	}
	if e.Acp != nil {
		e.Acp.Loop.Abort(context.Background())
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

// touchLRU 把 e 挪到 LRU 头。调用方持 m.mu。
func (m *SessionManager) touchLRU(e *SessionEntry) {
	if e.idleElem != nil {
		m.idleOrder.MoveToFront(e.idleElem)
	}
}
