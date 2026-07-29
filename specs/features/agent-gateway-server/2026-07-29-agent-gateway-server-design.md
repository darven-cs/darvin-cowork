# Agent Gateway Server 设计文档（S3）

> **Phase 2 / 6 — Go 阶段 spec #2**。把 Gateway（WebSocket + JSON-RPC 2.0）、SessionManager、EventLedger 立起来。handler 是 stub（真接 Agent.Run 由 S4），但 WS / JSON-RPC / 会话路由 / 事件透传整条链路必须可独立验收。
> 前置：S2 已落 sessions.db + SessionStore SQLite。
> 依赖：gorilla/websocket（`go get github.com/gorilla/websocket`）。

---

## 1. 概述

### 1.1 问题 / 背景

架构文档 §"顶层架构图" 定义 Gateway 层位于 Go Agent 子进程内，与 ACP 层、Agent Runtime 同进程。本 spec 落地 Gateway 的 3 个核心组件：

1. **WS Server**（`gorilla/websocket`）+ JSON-RPC 2.0 envelope 解析
2. **SessionManager**（内存 session_id → Session 实例映射）
3. **EventLedger**（订阅 event.Bus，事件经 WS notification 反向推送给订阅者）

handler 是 stub（`agent.prompt` 返回 mock sessionId/messageId；`agent.subscribe_events` 仅注册回调；`agent.abort` no-op），目的是让 S3 的 WS / JSON-RPC / 路由 / 事件透传链路独立可验收。**真接 ACP + Agent 由 S4**。

### 1.2 目标

- `internal/gateway/` 新 package，含 server / handlers / sessionmgr / eventledger 4 个文件
- WS server 监听 `localhost:0`（OS 分配端口），启动后向 stdout 打印 `<port>NNNNN</port>` 一行（Electron RuntimeMgr 读此行）
- JSON-RPC 2.0 envelope 严格解析（id / method / params / result / error / notification）
- 3 个 handler stub：
  - `agent.prompt`：返回 `{ sessionId, messageId }`
  - `agent.abort`：no-op 返回 `{ aborted: true }`
  - `agent.subscribe_events`：注册回调，session 结束自动注销
- SessionManager：每个 `agent.prompt` 创建新 session（或复用已有 sessionId）
- EventLedger：subscribe event.Bus，把事件打包成 WS notification 推回订阅者
- `cmd/app/main.go` 接入 Gateway（但**不**做优雅关闭 — S4 才接 SIGTERM）

### 1.3 非目标

- **不**接 Agent.Run / ACP Loop（S4）
- **不**做优雅关闭（SIGTERM / SIGINT 处理留 S4）
- **不**做认证 / 鉴权（v0 阶段；M2+ 引入）
- **不**做 WSS / TLS（仅 ws://）
- **不**做端口固定（OS 分配；通过 stdout 让 Electron 读）
- **不**做 reconnect / ping-pong 心跳（S6+）
- **不**实现 HTTP API（架构文档 §"HTTP API 设计" 留到远期 spec）
- **不**实现 EventLedger 落 sessions.db（S3 仅 in-memory 缓冲；S4 落库）
- **不**做 EventLedger 回放（架构文档 EventLedger 有"会话重放"能力，本 spec 不做）

---

## 2. 用户场景

### 场景 1：启动 Go agent 后 stdout 输出端口

**Given** `cmd/app/main.go` 启动并初始化 Gateway
**When** Gateway server bind `localhost:0` 成功
**Then** stdout 输出一行 `<port>NNNNN</port>`（NNNNN 是 OS 分配的端口号），**只**输出一行（其余日志走 stderr）
**And** 进程不退出，WS server 在 listen

### 场景 2：手测 WS + JSON-RPC prompt

**Given** 端口已知，启动 `wscat -c ws://localhost:NNNNN`
**When** 发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}`
**Then** 收到响应 `{"jsonrpc":"2.0","id":"1","result":{"sessionId":"s-xxx","messageId":"m-xxx"}}`
**And** SessionManager 创建 session 实例，sessionId 是 nanoid 风格 21 字符

### 场景 3：手测 WS subscribe_events + mock event 推送

**Given** 同场景 2 的 WS 连接
**When** 发 `{"jsonrpc":"2.0","id":"2","method":"agent.subscribe_events","params":{"sessionId":"s-xxx"}}`
**Then** 收到响应 `{"jsonrpc":"2.0","id":"2","result":{"subscribed":true}}`
**And** SessionManager 在该 session 上注册 callback

### 场景 4：JSON-RPC 错误响应

**Given** WS 连接已建立
**When** 发 `{"jsonrpc":"2.0","id":"3","method":"unknown_method"}`
**Then** 收到 `{"jsonrpc":"2.0","id":"3","error":{"code":-32601,"message":"Method not found"}}`

### 场景 5：JSON-RPC 批量请求

**Given** WS 连接已建立
**When** 发 `[{...prompt...},{...abort...}]`（JSON-RPC 2.0 数组）
**Then** 收到数组响应，每项独立处理；任一项失败不影响其他项

### 场景 6：handler stub 行为可见

**Given** 已发 agent.prompt
**When** EventLedger 拿到 event.Bus 上的 mock event（S3 期间由 stub 触发：handler.prompt 完成后 emit 一条 fake `text_delta`）
**Then** WS 收到 `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","delta":"hi","sessionId":"s-xxx","messageId":"m-xxx"}}` 一条 notification

---

## 3. 功能需求

### FR-1：依赖引入

`go.mod` 加 `github.com/gorilla/websocket v1.5.3`（或更新稳定版）。

### FR-2：WS Server 启动

`internal/gateway/server.go`：

```go
package gateway

import (
    "context"
    "fmt"
    "log"
    "net"
    "net/http"
    "os"

    "github.com/gorilla/websocket"
)

type Server struct {
    addr    string          // ":0" 表示 OS 分配
    port    int             // bind 后填
    upgrader websocket.Upgrader
    sessions *SessionManager
    ledger   *EventLedger

    httpSrv *http.Server
    listener net.Listener
}

func NewServer(sessions *SessionManager, ledger *EventLedger) *Server {
    return &Server{
        addr:    "localhost:0",
        upgrader: websocket.Upgrader{
            CheckOrigin: func(r *http.Request) bool { return true }, // v0: 接受所有 origin
        },
        sessions: sessions,
        ledger:   ledger,
    }
}

// Start binds the listener and runs http.Server in a goroutine. Returns
// after the port is bound; caller can read s.Port().
func (s *Server) Start(ctx context.Context) error {
    ln, err := net.Listen("tcp", s.addr)
    if err != nil { return fmt.Errorf("gateway: listen: %w", err) }
    s.listener = ln

    // 端口写到 stdout（Electron RuntimeMgr 读这一行）
    tcpAddr := ln.Addr().(*net.TCPAddr)
    s.port = tcpAddr.Port
    fmt.Fprintf(os.Stdout, "<port>%d</port>\n", s.port)
    os.Stdout.Sync()

    mux := http.NewServeMux()
    mux.HandleFunc("/ws", s.handleWS)
    s.httpSrv = &http.Server{Handler: mux}

    go func() {
        if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
            log.Printf("gateway: serve: %v", err)
        }
    }()
    return nil
}

// Port returns the bound port (only valid after Start).
func (s *Server) Port() int { return s.port }

// Shutdown gracefully stops the http.Server. Caller responsible for
// closing the listener (S4 does this with ctx).
func (s *Server) Shutdown(ctx context.Context) error {
    return s.httpSrv.Shutdown(ctx)
}
```

### FR-3：JSON-RPC 2.0 envelope

`internal/gateway/jsonrpc.go`：

```go
package gateway

import (
    "encoding/json"
    "fmt"
)

// Request is a single JSON-RPC 2.0 request or notification.
type Request struct {
    JSONRPC string          `json:"jsonrpc"`           // 必填 "2.0"
    ID      json.RawMessage `json:"id,omitempty"`      // 通知时省略
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`  // array 或 object
}

// Response is a JSON-RPC 2.0 response. Exactly one of Result / Error is set.
type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Result  any             `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 server-pushed event (no id).
type Notification struct {
    JSONRPC string `json:"jsonrpc"`
    Method  string `json:"method"`
    Params  any    `json:"params"`
}

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
    CodeParseError     = -32700
    CodeInvalidRequest = -32600
    CodeMethodNotFound = -32601
    CodeInvalidParams  = -32602
    CodeInternalError  = -32603
)

// ParseRequest parses a single message (object or array element).
func ParseRequest(data []byte) (*Request, error) {
    var req Request
    if err := json.Unmarshal(data, &req); err != nil {
        return nil, fmt.Errorf("jsonrpc: %w", err)
    }
    if req.JSONRPC != "2.0" {
        return nil, fmt.Errorf("jsonrpc: invalid version %q", req.JSONRPC)
    }
    if req.Method == "" {
        return nil, fmt.Errorf("jsonrpc: missing method")
    }
    return &req, nil
}
```

### FR-4：WS connection handler

`internal/gateway/server.go` 续：

```go
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
    conn, err := s.upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("gateway: upgrade: %v", err)
        return
    }
    defer conn.Close()

    client := newClient(conn, s.sessions, s.ledger)
    client.run(r.Context())
}
```

`internal/gateway/client.go`：

```go
package gateway

import (
    "context"
    "encoding/json"
    "log"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

const (
    writeWait      = 10 * time.Second
    pongWait       = 60 * time.Second
    pingPeriod     = (pongWait * 9) / 10
    maxMessageSize = 1 << 20 // 1 MB
)

type client struct {
    conn     *websocket.Conn
    sessions *SessionManager
    ledger   *EventLedger

    writeMu sync.Mutex
    closed  bool
}

func newClient(conn *websocket.Conn, sessions *SessionManager, ledger *EventLedger) *client {
    return &client{conn: conn, sessions: sessions, ledger: ledger}
}

func (c *client) run(ctx context.Context) {
    c.conn.SetReadLimit(maxMessageSize)
    c.conn.SetReadDeadline(time.Now().Add(pongWait))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })

    // ping loop
    go c.writeLoop(ctx)

    for {
        _, data, err := c.conn.ReadMessage()
        if err != nil {
            if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
                log.Printf("gateway: read: %v", err)
            }
            return
        }

        // batch support: data 可能是 array
        var raw json.RawMessage
        if err := json.Unmarshal(data, &raw); err != nil {
            c.sendError(nil, CodeParseError, "parse error", err)
            continue
        }
        if raw[0] == '[' {
            // batch
            var batch []json.RawMessage
            if err := json.Unmarshal(data, &batch); err != nil {
                c.sendError(nil, CodeParseError, "batch parse error", err)
                continue
            }
            responses := make([]json.RawMessage, 0, len(batch))
            for _, item := range batch {
                if r := c.dispatch(ctx, item); r != nil {
                    responses = append(responses, r)
                }
            }
            if len(responses) > 0 {
                c.writeJSON(responses)
            }
        } else {
            if r := c.dispatch(ctx, data); r != nil {
                c.writeJSON(r)
            }
        }
    }
}

// dispatch returns nil if request is a notification (no id).
func (c *client) dispatch(ctx context.Context, data []byte) json.RawMessage {
    req, err := ParseRequest(data)
    if err != nil {
        c.sendError(nil, CodeInvalidRequest, "invalid request", err)
        return nil
    }

    resp := dispatch(ctx, req, c.sessions, c.ledger)

    // 通知（无 id）不返回响应
    if len(req.ID) == 0 || string(req.ID) == "null" {
        return nil
    }
    raw, _ := json.Marshal(resp)
    return raw
}

func (c *client) writeLoop(ctx context.Context) {
    ticker := time.NewTicker(pingPeriod)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.writeMu.Lock()
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            err := c.conn.WriteMessage(websocket.PingMessage, nil)
            c.writeMu.Unlock()
            if err != nil {
                return
            }
        }
    }
}

func (c *client) writeJSON(v any) {
    c.writeMu.Lock()
    defer c.writeMu.Unlock()
    c.conn.SetWriteDeadline(time.Now().Add(writeWait))
    c.writeJSON_unsafe(v)
}

func (c *client) writeJSON_unsafe(v any) {
    data, err := json.Marshal(v)
    if err != nil { return }
    if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
        log.Printf("gateway: write: %v", err)
    }
}

func (c *client) sendError(id json.RawMessage, code int, msg string, data any) {
    resp := Response{
        JSONRPC: "2.0",
        ID:      id,
        Error:   &RPCError{Code: code, Message: msg, Data: data},
    }
    c.writeJSON(resp)
}

// SendNotification pushes a server-initiated notification to the client.
func (c *client) SendNotification(method string, params any) {
    c.writeJSON(Notification{JSONRPC: "2.0", Method: method, Params: params})
}
```

### FR-5：handler dispatch

`internal/gateway/handlers.go`：

```go
package gateway

import (
    "context"
    "encoding/json"
    "fmt"

    "darvin-cowork/backend/internal/agent/session"
)

// PromptParams / AbortParams / SubscribeEventsParams
type PromptParams struct {
    Content   string `json:"content"`
    SessionID string `json:"sessionId,omitempty"`
}

type PromptResult struct {
    SessionID string `json:"sessionId"`
    MessageID string `json:"messageId"`
}

type AbortParams struct {
    SessionID string `json:"sessionId"`
}

type AbortResult struct {
    Aborted   bool   `json:"aborted"`
    SessionID string `json:"sessionId"`
}

type SubscribeEventsParams struct {
    SessionID string `json:"sessionId"`
}

type SubscribeEventsResult struct {
    Subscribed bool `json:"subscribed"`
}

// dispatch routes a parsed Request to the appropriate handler.
func dispatch(ctx context.Context, req *Request, sessions *SessionManager, ledger *EventLedger) *Response {
    var params json.RawMessage = req.Params
    switch req.Method {
    case "agent.prompt":
        return handlePrompt(ctx, req.ID, params, sessions, ledger)
    case "agent.abort":
        return handleAbort(ctx, req.ID, params, sessions)
    case "agent.subscribe_events":
        return handleSubscribeEvents(ctx, req.ID, params, sessions)
    default:
        return &Response{
            JSONRPC: "2.0",
            ID:      req.ID,
            Error:   &RPCError{Code: CodeMethodNotFound, Message: fmt.Sprintf("Method not found: %s", req.Method)},
        }
    }
}

func handlePrompt(ctx context.Context, id json.RawMessage, params json.RawMessage, sessions *SessionManager, ledger *EventLedger) *Response {
    var p PromptParams
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    if p.Content == "" {
        return errorResp(id, CodeInvalidParams, "content is required", nil)
    }

    sess, msgID, err := sessions.CreateOrGet(p.SessionID)
    if err != nil {
        return errorResp(id, CodeInternalError, "create session", err)
    }

    // S3 STUB: 不真跑 Agent；只 emit 一条 fake text_delta + done 让链路可见
    go func() {
        // 让 ledger 在新 session 上 emit 一次 fake event
        ledger.EmitStub(sess.ID, msgID, p.Content)
    }()

    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Result: PromptResult{SessionID: sess.ID, MessageID: msgID},
    }
}

func handleAbort(ctx context.Context, id json.RawMessage, params json.RawMessage, sessions *SessionManager) *Response {
    var p AbortParams
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    // S3 STUB: no-op
    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Result:  AbortResult{Aborted: true, SessionID: p.SessionID},
    }
}

func handleSubscribeEvents(ctx context.Context, id json.RawMessage, params json.RawMessage, sessions *SessionManager) *Response {
    var p SubscribeEventsParams
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    if !sessions.Has(p.SessionID) {
        return errorResp(id, CodeInvalidParams, "unknown sessionId", nil)
    }
    // 注册 callback 到 EventLedger；session 销毁时自动注销
    // sessions.RegisterCallback 在 S4 由 EventLedger 接管；S3 stub 用 sessions.AddCallback
    sessions.AddCallback(p.SessionID, func(ev any) {
        // S3 stub：handler 不会调 WS；S4 由 EventLedger.SendNotification 接管
    })
    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Result:  SubscribeEventsResult{Subscribed: true},
    }
}

func errorResp(id json.RawMessage, code int, msg string, data any) *Response {
    return &Response{
        JSONRPC: "2.0",
        ID:      id,
        Error:   &RPCError{Code: code, Message: msg, Data: data},
    }
}
```

### FR-6：SessionManager

`internal/gateway/sessionmgr.go`：

```go
package gateway

import (
    "sync"
    "time"

    "github.com/jaevor/go-nanoid"

    "darvin-cowork/backend/internal/agent/session"
)

type SessionCallback func(ev any)

type SessionManager struct {
    mu        sync.RWMutex
    sessions  map[string]*session.Session
    callbacks map[string][]SessionCallback
    idGen     func() string
}

func NewSessionManager() *SessionManager {
    gen, _ := nanoid.Custom("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 21)
    return &SessionManager{
        sessions:  make(map[string]*session.Session),
        callbacks: make(map[string][]SessionCallback),
        idGen:     gen,
    }
}

// CreateOrGet returns existing session if id matches; else creates new.
// msgID is always fresh (uuid for the user message).
func (m *SessionManager) CreateOrGet(id string) (*session.Session, string, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    msgID := m.idGen()
    if id != "" {
        if s, ok := m.sessions[id]; ok {
            return s, msgID, nil
        }
    }
    sess := session.NewSession(m.idGen())
    sess.CreatedAt = time.Now()
    sess.UpdatedAt = time.Now()
    m.sessions[sess.ID] = sess
    return sess, msgID, nil
}

func (m *SessionManager) Has(id string) bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    _, ok := m.sessions[id]
    return ok
}

func (m *SessionManager) AddCallback(sessionID string, cb SessionCallback) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.callbacks[sessionID] = append(m.callbacks[sessionID], cb)
}

func (m *SessionManager) RemoveCallbacks(sessionID string) []SessionCallback {
    m.mu.Lock()
    defer m.mu.Unlock()
    cbs := m.callbacks[sessionID]
    delete(m.callbacks, sessionID)
    return cbs
}

func (m *SessionManager) Destroy(id string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.sessions, id)
}
```

### FR-7：EventLedger

`internal/gateway/eventledger.go`：

```go
package gateway

import (
    "log"
    "sync"
    "time"

    "darvin-cowork/backend/internal/agent/event"
)

// EventLedger subscribes to agent.event.Bus and forwards events to
// subscribed WS clients as JSON-RPC notifications.
//
// S3: 仅 in-memory 缓冲 + fake event；不写 sessions.db。
// S4: 落 sessions.db（messages 表）。
type EventLedger struct {
    mu          sync.RWMutex
    bySession   map[string][]chan any  // sessionId → notification channels
    fakeDelay   time.Duration
}

func NewEventLedger() *EventLedger {
    return &EventLedger{
        bySession: make(map[string][]chan any),
        fakeDelay: 50 * time.Millisecond,
    }
}

// Subscribe registers a notification channel for sessionID. Caller
// receives events until Unsubscribe or session destroyed.
func (l *EventLedger) Subscribe(sessionID string) <-chan any {
    ch := make(chan any, 64)
    l.mu.Lock()
    l.bySession[sessionID] = append(l.bySession[sessionID], ch)
    l.mu.Unlock()
    return ch
}

func (l *EventLedger) Unsubscribe(sessionID string, ch <-chan any) {
    l.mu.Lock()
    defer l.mu.Unlock()
    subs := l.bySession[sessionID]
    for i, c := range subs {
        if c == ch {
            l.bySession[sessionID] = append(subs[:i], subs[i+1:]...)
            close(c)
            return
        }
    }
}

// Publish forwards ev to all subscribers of sessionID (non-blocking).
func (l *EventLedger) Publish(sessionID string, ev any) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    for _, ch := range l.bySession[sessionID] {
        select {
        case ch <- ev:
        default:
            log.Printf("eventledger: drop for session %s (subscriber slow)", sessionID)
        }
    }
}

// EmitStub is the S3-only helper that synthesizes a fake text_delta + done
// for the prompt handler. S4 removes this and EventLedger subscribes to
// event.Bus instead.
func (l *EventLedger) EmitStub(sessionID, msgID, content string) {
    go func() {
        // fake text_delta
        l.Publish(sessionID, DarvinEventStub{
            Type:      "text_delta",
            Delta:     "Echo: " + truncate(content, 80),
            SessionID: sessionID,
            MessageID: msgID,
        })
        time.Sleep(l.fakeDelay * 2)
        // fake done
        l.Publish(sessionID, DarvinEventStub{
            Type:      "done",
            SessionID: sessionID,
            MessageID: msgID,
            Usage:     map[string]int{"promptTokens": len(content), "completionTokens": 80, "totalTokens": len(content) + 80},
        })
    }()
}

// DarvinEventStub is the in-memory representation of an event for S3.
// S4: replaced by event.Event directly (mapping per S3 §4.2 table).
type DarvinEventStub struct {
    Type      string         `json:"type"`
    Delta     string         `json:"delta,omitempty"`
    SessionID string         `json:"sessionId"`
    MessageID string         `json:"messageId"`
    Usage     map[string]int `json:"usage,omitempty"`
}

func truncate(s string, n int) string {
    if len(s) <= n { return s }
    return s[:n] + "..."
}

// AttachBus subscribes EventLedger to event.Bus. S4 calls this; S3 stub
// doesn't call (handler uses EmitStub).
func (l *EventLedger) AttachBus(bus *event.Bus) {
    // S3 stub: no-op
    _ = bus
}
```

### FR-8：server.go 整合 run loop 中订阅 → 推 notification

`internal/gateway/client.go` `run()` 续（订阅 EventLedger）：

```go
func (c *client) run(ctx context.Context) {
    // ... existing read loop setup ...

    // S3 stub: subscribe to all sessions (no per-session subscribe RPC yet)
    // S4: 在 handleSubscribeEvents 中通过 c.ledger.Subscribe 注册
    //     此处简化处理：订阅全局 ledger 由 RPC handler 调

    for {
        _, data, err := c.conn.ReadMessage()
        // ... 已有 dispatch 逻辑 ...
    }
}
```

**实际 S3 的 subscribe 行为**：`handleSubscribeEvents` 注册回调到 SessionManager，但**不**真把事件推回 client。这是因为 S3 handler 是 stub，emit fake event 时 SessionManager 没持有 client 句柄。

**S3 验收手段**：handler subscribe 返回成功；fake event 由 ledger 内部 log（不发 WS）；S4 由 EventLedger.SendNotification 接住 client 引用推 notification。

### FR-9：main.go 接入 Gateway

`cmd/app/main.go` 在所有 init 完成后：

```go
// --- Gateway ---
sessions := gateway.NewSessionManager()
ledger := gateway.NewEventLedger()
gs := gateway.NewServer(sessions, ledger)
if err := gs.Start(context.Background()); err != nil {
    log.Error("gateway start failed", zap.Error(err))
    os.Exit(1)
}
log.Info("gateway listening", zap.Int("port", gs.Port()))

// S3: 进程不退出，等 S4 接 SIGTERM
select {}
```

⚠️ `select {}` 是 S3 占位 — S4 替换为 `<-ctx.Done()`（signal.NotifyContext）。

---

## 4. 实现方案

### 4.1 目录结构

```
src/darvin-agent/internal/gateway/
├── server.go         # 🆕 WS server 启动 + port 输出
├── jsonrpc.go        # 🆕 JSON-RPC 2.0 envelope
├── client.go         # 🆕 WS connection lifecycle + dispatch
├── handlers.go       # 🆕 agent.prompt / abort / subscribe_events stub
├── sessionmgr.go     # 🆕 SessionManager
├── eventledger.go    # 🆕 EventLedger
├── server_test.go    # 🆕
├── jsonrpc_test.go   # 🆕
├── sessionmgr_test.go # 🆕
└── eventledger_test.go # 🆕
```

### 4.2 Go ↔ TS 事件映射表（S4 落地）

| Go `event.Event` 类型 | TS `DarvinEvent` 类型 |
|---------------------|---------------------|
| `event.PromptReceivedEvent` | （不暴露给 UI，UI 不需要） |
| `event.RunStartEvent` | （不暴露） |
| `event.TurnStartEvent` | （不暴露） |
| `event.LLMStartEvent` | （不暴露） |
| `event.TextDeltaEvent` | `DarvinTextDeltaEvent` (`text_delta`) |
| `event.ToolStartEvent` | `DarvinToolStartEvent` (`tool_start`) |
| `event.ToolEndEvent` | `DarvinToolEndEvent` (`tool_end`) |
| `event.ThinkingDeltaEvent`（v1 LLM spec 新增） | `DarvinThinkingDeltaEvent` (`thinking_delta`) |
| `event.TurnEndEvent` | （不单独暴露；`done` 已含 StopReason） |
| `event.RunEndEvent` | （不暴露） |
| `event.LLMEndEvent` | （不直接暴露；由 `done` event 携带） |
| `event.DoneEvent` (DoneEvent from llm package) | `DarvinDoneEvent` (`done`) |
| `event.AgentErrorEvent` | `DarvinErrorEvent` (`error`) |
| `event.AgentEndEvent` | `DarvinAgentEndEvent` (`agent_end`) |

S4 实现 `EventLedger.AttachBus` + 映射逻辑；S3 不做。

### 4.3 关键决策

#### 4.3.1 端口通过 stdout 而非环境变量

`<port>NNNNN</port>` 单行约定简单可靠。Electron RuntimeMgr 读这一行即可拿到端口。环境变量在 Windows spawn 同步上易出问题。

#### 4.3.2 CheckOrigin 暂时接受所有

v0 阶段 dev only；正式 release 前需限制 origin（`electron://`）。

#### 4.3.3 handler stub 行为

`agent.prompt` 立即返回 sessionId/messageId，**不**同步等 Agent 结果；异步 goroutine emit fake event 给 ledger（仅 log，不推 WS）。这样 S3 的可验收点：JSON-RPC 协议正确 + SessionManager 创建 session + EventLedger 内部能 publish。

**真接 client → WS notification 由 S4 接入**（EventLedger.SendNotification 持有 client 引用）。

#### 4.3.4 写库等 S4

S3 EventLedger 不写 sessions.db。S4 才把 event.Bus 事件落 `messages` 表（CompactCheckpoint 等）。

#### 4.3.5 go-nanoid 选型

`github.com/jaevor/go-nanoid v1.3.1` — 纯 Go 实现，零依赖，custom alphabet 21 字符。session_id 用 21 字符（够短好读、够唯一）。

### 4.4 关键代码骨架

```go
// internal/gateway/server.go Start 完整版（含 race-safe port 输出）
func (s *Server) Start(ctx context.Context) error {
    ln, err := net.Listen("tcp", s.addr)
    if err != nil { return fmt.Errorf("gateway: listen: %w", err) }
    s.listener = ln

    tcpAddr := ln.Addr().(*net.TCPAddr)
    s.port = tcpAddr.Port

    // stdout 单行（Electron 解析）；其余走 stderr
    fmt.Fprintf(os.Stdout, "<port>%d</port>\n", s.port)
    if err := os.Stdout.Sync(); err != nil { /* 忽略 */ }

    mux := http.NewServeMux()
    mux.HandleFunc("/ws", s.handleWS)
    s.httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

    go func() {
        if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
            log.Printf("gateway: serve: %v", err)
        }
    }()
    return nil
}
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| WS 客户端断开（`IsCloseError`） | `run()` return；defer conn.Close 触发 |
| WS 客户端发送非 JSON | `sendError(id, CodeParseError, ...)` |
| JSON-RPC batch 含 0 项 | 忽略（不响应） |
| handler 抛 panic | `dispatch` 用 recover 包成 `CodeInternalError`；不挂掉 client |
| `<port>` stdout 输出前 Electron 已 wait | Electron RuntimeMgr 用 `child_process.spawn` 的 `stdout.on('data')` 累积直到匹配 regex `<port>(\d+)</port>` |
| 同一 client 多次 `agent.subscribe_events` 同一 session | 允许多个 callback（各自独立） |
| EventLedger 订阅 channel 满 | drop-oldest + stderr warn log |
| `agent.prompt` content 为空字符串 | 返回 `CodeInvalidParams` "content is required" |
| session 不存在时 `agent.subscribe_events` | 返回 `CodeInvalidParams` "unknown sessionId" |
| 并发 `agent.prompt` 同一 sessionId | 复用已有 session，每次 msgID 独立（goroutine safe） |
| gorilla/websocket 重连 | 不实现（S6+） |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/gateway/server.go` | 🆕 WS server + port 输出 |
| `src/darvin-agent/internal/gateway/jsonrpc.go` | 🆕 JSON-RPC 2.0 |
| `src/darvin-agent/internal/gateway/client.go` | 🆕 WS connection |
| `src/darvin-agent/internal/gateway/handlers.go` | 🆕 3 个 handler stub |
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 🆕 SessionManager |
| `src/darvin-agent/internal/gateway/eventledger.go` | 🆕 EventLedger |
| `src/darvin-agent/internal/gateway/server_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/jsonrpc_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/sessionmgr_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/eventledger_test.go` | 🆕 |
| `src/darvin-agent/cmd/app/main.go` | 改：init 后启 Gateway，`select{}` 占位 |
| `src/darvin-agent/go.mod` | 加 `github.com/gorilla/websocket`、`github.com/jaevor/go-nanoid` |
| `src/darvin-agent/go.sum` | 自动更新 |

**不修改**：
- S2 落地的 sessions.db / SessionStore / models
- event.Bus / event.Event（架构文档描述的事件类型 S3 不变）
- executor / ctxengine / llm
- `internal/agent/agent.go`

---

## 7. 验收标准

- [ ] `go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] `go test ./internal/gateway/... -race` 全绿
- [ ] 启动 `./bin/darvin-agent-...` 后 stdout 输出**唯一一行** `<port>NNNNN</port>`，进程不退出
- [ ] stderr 含日志（"gateway listening port=..."），stdout 不再出现其他内容
- [ ] `wscat -c ws://localhost:NNNNN` 连上
- [ ] 发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}` → 收到 `{"jsonrpc":"2.0","id":"1","result":{"sessionId":"<21字符>","messageId":"<21字符>"}}`
- [ ] 发 `{"jsonrpc":"2.0","id":"2","method":"agent.subscribe_events","params":{"sessionId":"s-xxx"}}` → 收到 `{"jsonrpc":"2.0","id":"2","result":{"subscribed":true}}`
- [ ] 发 `{"jsonrpc":"2.0","id":"3","method":"unknown"}` → 收到 `error.code=-32601`
- [ ] 发 `[{"jsonrpc":"2.0","id":"4","method":"agent.prompt","params":{"content":"a"}},{"jsonrpc":"2.0","id":"5","method":"agent.abort","params":{"sessionId":"any"}}]` → 收到数组响应 [id=4 result, id=5 result]
- [ ] 发 `{"jsonrpc":"2.0","id":"6","method":"agent.prompt","params":{}}`（缺 content）→ 收到 `error.code=-32602`
- [ ] handler stub 完成后 ledger 内部 stderr 含 "EmitStub for session ..." 日志
- [ ] SessionManager.CreateOrGet 同 sessionId 两次调用返回同一 *session.Session 实例，msgID 独立
- [ ] go-nanoid 21 字符生成函数 1000 次调用无重复（属性测试或单测跑 10000 次）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S4** agent-acp-loop | handler 替换为真 ACP + Agent.Run；EventLedger.AttachBus 订阅 event.Bus 并推 WS notification；SIGTERM 优雅关闭 |
| Auth / Bearer token | v0 阶段无鉴权；远期 spec 引入 |
| WSS / TLS | 远期 spec |
| reconnect / 心跳 | 当前实现简单 ping-pong；远期可加 reconnect 策略 |
| EventLedger 落 sessions.db | messages 表写入 |
| 跨进程 EventLedger 聚合 | 多 Go Agent 子进程时的事件路由 |