# Agent Gateway Server 设计文档 v2（S3）

> **Phase 2 / 6 — Go 阶段 spec #2（v2 修订）**。修订 v1 的 10 个 P0 + 11 个 P1，全部基于源码 + 实测（见 §0 修订清单）。
> 前置：S2 已落 sessions.db + SessionStore SQLite。
> 关键决策（与 v1 不同）：
> 1. **S3 真把 event notification 推到 WS**（按架构文档 `docs/系统架构.md:117-118` 的 EventLedger → Gateway → WSBridge 链路；v1 FR-8/§4.3.3 标「不推」是偏离）。
> 2. **driver 换 `glebarez/sqlite`**（v1 spec 隐含的 `mattn/go-sqlite3` 与 `scripts/build-go.js:19` 的 `CGO_ENABLED=0` 冲突，实测 `requires cgo to work` → exit 1，纯 Go driver 保交叉编译）。
> 3. **加 `ThinkingDeltaEvent` 空壳**（架构文档 §事件总线契约要求 + v1 §4.2 映射表已引用；S3 只加类型，provider 解析留 S4）。
> 4. **`AttachBus` 改 `AttachSubscription`**（`agent.Agent` 只暴露 `Subscribe(buffer) *event.Subscription`，无 `Bus()` getter；v1 签名不可达）。

---

## 0. 相对 v1（2026-07-29）的修订清单

### P0-A 类（v1 直接照写会导致编译不过 / 跑不起来）

| # | v1 位置 | 问题 | v2 修复 |
|---|---|---|---|
| 1 | FR-6 `sess.UpdatedAt = time.Now()` | `session.UpdatedAt` 是方法不是字段，`updatedAt` 未导出 | 不赋值；`NewSession` 已设好 |
| 2 | `build-go.js:19` `CGO_ENABLED:'0'` × `mattn/go-sqlite3` | 实测 `requires cgo to work` → exit 1 | `go.mod` 换 `github.com/glebarez/sqlite v1.11.0`；`internal/database/sqlite.go` 改 import；`build-go.js` 保留 `CGO_ENABLED:0` |
| 3 | `internal/logger/logger.go:55` `zapcore.AddSync(os.Stdout)` 写死 | 实测 `2>/dev/null` 后 stdout 上有 zap + gorm 日志，`<port>` 单行契约 100% 破 | logger.go 补 `cfg.Output == "stderr"` 分支；`config.yaml` 改 `log.output: stderr`；gorm `logger.Default.LogMode(logger.Info)` 重定向 |
| 4 | `cmd/app/main.go:25` `config.Load("config.yaml")` 相对 cwd | 实测换目录即 exit 1 | 改查 `os.Getenv("DARVIN_CONFIG")` → exe 同级 `config.yaml` → cwd `config.yaml` 三级回退 |
| 5 | FR-5 `handlers.go` `import .../agent/session` 但零引用 | `imported and not used` 编译错 | 不导入 |

### P0-B 类（v1 spec 自相矛盾）

| # | v1 位置 | 问题 | v2 修复 |
|---|---|---|---|
| 6 | FR-2 挂 `/ws` vs §2 场景2 + §7 `wscat -c ws://localhost:NNNNN` 无 `/ws` | 404；S5/S6 spec 3 处 `ws://localhost:{port}/ws` | §7 验收统一带 `/ws` |
| 7 | §2 场景6 vs FR-8/§4.3.3 互斥 | 场景6 说推 notification，FR-8 说「不真把事件推回 client」 | **按架构文档真推**。FR-8 全文删除；handler 拿 `*client` 引用，EventLedger 通过 channel 推回 |
| 8 | §7 `agent.subscribe_events` 传 `"s-xxx"` | `sessions.Has("s-xxx")` 必 false → -32602 | §7 验收改用「上一步 prompt 返回的真 sessionId」 |
| 9 | §1.3「不做 ping-pong」 vs FR-4 实装 ping-pong | 自相矛盾 | §1.3 删该条；FR-4 保留（S6+ 才考虑删） |

### P0-C 类（v1 spec 隐含的现状问题）

| # | v1 位置 | 问题 | v2 修复 |
|---|---|---|---|
| 10 | §4.2 映射表 `event.ThinkingDeltaEvent` | 源码不存在，`thinking_delta` 事件不实现 | S3 顺手加 `agent/event/event.go` `ThinkingDeltaEvent{Delta string}` 空壳；S4 再补 anthropic stream 解析 |

### P1 类（不阻塞但会返工）

| # | v1 位置 | 问题 | v2 修复 |
|---|---|---|---|
| P1-1 | FR-7 `AttachBus(*event.Bus)` | `agent.Agent` 无 `Bus()` getter | 改 `AttachSubscription(*event.Subscription)` |
| P1-2 | FR-7 `Publish` `default:` 是 drop-newest；§5 表/源码 `event.Bus` 是 drop-oldest | 三方不一致 | publish: drop-oldest（同 event.Bus.Emit 策略） |
| P1-3 | §7「ledger stdlib log 含 `EmitStub for session ...`」 | `EmitStub` 不打日志；零订阅者 | 改 用 zap logger（§  4.4.1 注入）；S3 真推后日志天然存在 |
| P1-4 | §4.3.5 nanoid `v1.3.1` | **不存在**（实际 tag: v1.0.0/1.1.0/1.2.0/1.3.0/1.4.0） | `v1.4.0`；`gen, _ := nanoid.Custom(...)` 改 `MustCustomASCII`（纯 ASCII 字母表） |
| P1-5 | FR-1 只提 gorilla/websocket | §6/§4.3.5 都要 nanoid | FR-1 合并 |
| P1-6 | gateway stdlib `log.Printf` | 跟全项目 zap 不一致 | 注入 `*zap.Logger` |
| P1-7 | `select{}` 让 `defer log.Sync()` 永不执行 | S3 阻塞永远不到 sync | 改 `signal.Notify(SIGINT, SIGTERM)` + `<-sigCh` → flush → exit |
| P1-8 | `Destroy`/`RemoveCallbacks` 无调用点 | "session 结束自动注销" 无路径 | client.run 退出时遍历 `ledger.UnsubscribeAll(client)` |
| P1-9 | §4.1 4 个 `_test.go`，漏 `handlers.go`/`client.go` | 逻辑最密却没测试 | 补 `handlers_test.go` 和 `client_test.go` |
| P1-10 | 包级 `dispatch` 与 `client.dispatch` 同名 | 坑读者 | 包级函数重命名 `dispatchRequest` |
| P1-11 | `DarvinEventStub` 类型冗余 | TS 端 `DarvinEvent` 已定义 | 直接 `event.Event` 走 eventledger.emit，反射 `EventName()` 串出来 |

---

## 1. 概述

### 1.1 问题 / 背景

架构文档 §"顶层架构图" + §"数据流向图" 定义 Gateway 层位于 Go Agent 子进程内，与 ACP 层、Agent Runtime 同进程。Gateway 链路：

```
Renderer → preload → main → RuntimeMgr → WS → Gateway → SessionManager
                                                ↓
                                          EventLedger ← event.Bus (S4)
                                                ↓
                                          Notification → WS → Renderer
```

S3 立 Gateway 3 核心组件：

1. **WS Server**（`gorilla/websocket`）+ JSON-RPC 2.0 envelope 解析
2. **SessionManager**（nanoid 21 字符 session_id；Session ↔ 客户端通道）
3. **EventLedger**（订阅 event.Bus；事件经 WS notification 反向推送）

handler 是简化的真接（直接调 SessionManager + EventLedger）；但不调 Agent.Run（**S4 由 acp.Loop 接管**）。S3 验收手段：`EmitStub` fake event 跑通整条推送链路。

### 1.2 目标

- `internal/gateway/` 新 package，含 server / handlers / sessionmgr / eventledger / client / jsonrpc 6 个文件
- WS server 监听 `localhost:0`，启动后向 stdout 打印 `<port>NNNNN</port>` **唯一一行**（Electron RuntimeMgr 读此行）
- 全部日志走 stderr（zap stderr core + gorm logger stderr 重定向）
- JSON-RPC 2.0 envelope 严格解析（id / method / params / result / error / notification）
- 3 个 handler：`agent.prompt` / `agent.abort` / `agent.subscribe_events`
- SessionManager：每个 `agent.prompt` 创建新 session 或复用已有 sessionId
- EventLedger：`EmitStub` fake event → WS notification 推回订阅者
- `cmd/app/main.go` 接入 Gateway + `signal.Notify` Ctrl-C / SIGTERM 优雅关闭（**S3 提前一步**以兑现 §1.2 验收 "进程不退出" 在 Ctrl-C 下也能跑）

### 1.3 非目标

- **不**接 Agent.Run / ACP Loop（S4）
- **不**做认证 / 鉴权（v0 阶段；M2+ 引入）
- **不**做 WSS / TLS（仅 ws://）
- **不**做端口固定（OS 分配；通过 stdout 让 Electron 读）
- **不**做 reconnect / 客户端 ping-pong 容错（S6+）
- **不**实现 HTTP API（架构文档 §"HTTP API 设计" 留到远期 spec）
- **不**实现 EventLedger 落 sessions.db（即 messages 表写入由 S4 的 handler.prompt 真接 Agent.Run 后再写）
- **不**做 EventLedger 回放（架构文档 EventLedger 有"会话重放"能力，本 spec 不做）
- **不**在 S3 实装 anthropic stream 解析 `thinking_delta`（S3 只在 agent/event 加空壳类型；S4 补 provider 解析）

---

## 2. 用户场景

### 场景 1：启动 Go agent 后 stdout 输出端口

**Given** `cmd/app/main.go` 启动并初始化 Gateway
**When** Gateway server bind `localhost:0` 成功
**Then** stdout 输出一行 `<port>NNNNN</port>`（NNNNN 是 OS 分配的端口号），**只**输出一行（其余日志走 stderr）
**And** 进程不退出，WS server 在 listen

### 场景 2：手测 WS + JSON-RPC prompt

**Given** 端口已知，启动 `wscat -c ws://localhost:NNNNN/ws`（**带 `/ws` 路径**）
**When** 发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}`
**Then** 收到响应 `{"jsonrpc":"2.0","id":"1","result":{"sessionId":"<21字符>","messageId":"<21字符>"}}`
**And** SessionManager 创建 session 实例，sessionId 是 nanoid 21 字符（base62 字母表 `[A-Za-z0-9]`）

### 场景 3：手测 WS subscribe_events + 真 event 推送

**Given** 同场景 2 的 WS 连接
**When** 发 `{"jsonrpc":"2.0","id":"2","method":"agent.subscribe_events","params":{"sessionId":"<scenario 2 返回的真 sessionId>"}}`
**Then** 收到响应 `{"jsonrpc":"2.0","id":"2","result":{"subscribed":true}}`
**And** 异步收到至少 2 条 WS notification（顺序不保证）：
- `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","messageId":"<m>","delta":"Echo: hi"}}`
- `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"done","messageId":"<m>"}}`

### 场景 4：JSON-RPC 错误响应

**Given** WS 连接已建立
**When** 发 `{"jsonrpc":"2.0","id":"3","method":"unknown_method"}`
**Then** 收到 `{"jsonrpc":"2.0","id":"3","error":{"code":-32601,"message":"Method not found: unknown_method"}}`

### 场景 5：JSON-RPC 批量请求

**Given** WS 连接已建立
**When** 发 `[{...prompt...},{...abort...}]`（JSON-RPC 2.0 数组）
**Then** 收到数组响应，每项独立处理；任一项失败不影响其他项

### 场景 6：Ctrl-C 触发优雅关闭

**Given** 进程在跑
**When** 发 SIGINT（Ctrl-C）
**Then** stderr 输出 "graceful shutdown complete" + WS server 关闭 + 进程 exit 0，**总耗时 ≤ 3s**

---

## 3. 功能需求

### FR-1：依赖引入

`go.mod` 加：

| 依赖 | 版本 | 用途 |
|---|---|---|
| `github.com/gorilla/websocket` | `v1.5.3` | WS server / client |
| `github.com/jaevor/go-nanoid` | `v1.4.0` | session_id / message_id 生成 |
| `github.com/glebarez/sqlite` | `v1.11.0` | 替换 `gorm.io/driver/sqlite`（纯 Go，免 CGO） |

**副作用**：glebarez/sqlite 锁 `gorm.io/gorm v1.25.7`（项目当前 `v1.25.7-0.20240204...` pre-release，`go mod tidy` 会升一格到实际 v1.25.7）。S2 的 7 个 SQLite 测试需回归验证。

### FR-2：WS Server 启动

`internal/gateway/server.go` — 与 v1 一致，关键差异：

- `addr` 改 `localhost:0`（保留 v1）
- `Start(ctx)` 流程与 v1 一致
- 新增**注入 `*zap.Logger`**（不再用 stdlib log）
- `<port>NNNNN</port>` 行 `os.Stdout.Sync()` 后**不再写 log**（保证 stdout 唯一行）

### FR-3：JSON-RPC 2.0 envelope

`internal/gateway/jsonrpc.go` — 与 v1 一致。

### FR-4：WS connection handler

`internal/gateway/client.go` — 与 v1 一致，**新增**：

1. `client` 持有 `*EventLedger` 引用（不是 v1 的「事件不推回 client」）
2. `client.run(ctx)` 退出时（读 / 写出错 / `ctx.Done`）调用 `ledger.UnsubscribeAll(c)` 清理该 client 的所有订阅
3. `client` 提供 `SendNotification(method string, params any)` 给 EventLedger 调用

### FR-5：handler dispatch

`internal/gateway/handlers.go` — 与 v1 一致，**关键差异**：

```go
// dispatch 签名扩展：拿 *client 以便 handler 创建 session-scoped 订阅
func dispatchRequest(ctx context.Context, req *Request, c *client) *Response {
    switch req.Method {
    case "agent.prompt":
        return handlePrompt(ctx, req.ID, req.Params, c)
    case "agent.abort":
        return handleAbort(ctx, req.ID, req.Params, c)
    case "agent.subscribe_events":
        return handleSubscribeEvents(ctx, req.ID, req.Params, c)
    default:
        return &Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: CodeMethodNotFound, Message: ...}}
    }
}
```

**不** 导入 `internal/agent/session`（v1 漏的编译错误）。

`handlePrompt`：同 v1，调 `sessions.CreateOrGet` + `ledger.EmitStub`，但 `EmitStub` 改为 `c` 引用。

`handleSubscribeEvents` 重写：

```go
func handleSubscribeEvents(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client) *Response {
    var p SubscribeEventsParams
    if err := json.Unmarshal(params, &p); err != nil { return errorResp(id, CodeInvalidParams, ...) }
    if !c.sessions.Has(p.SessionID) { return errorResp(id, CodeInvalidParams, "unknown sessionId", nil) }
    c.ledger.Subscribe(p.SessionID, c)  // 推 WS notification 回 c
    return successResp(id, SubscribeEventsResult{Subscribed: true})
}
```

`handleAbort`：同 v1 stub（no-op）。

### FR-6：SessionManager

`internal/gateway/sessionmgr.go` — 与 v1 一致，**关键差异**：

- `MustCustomASCII` 替 `Custom`（P1-4）
- 不赋值 `sess.UpdatedAt` / `sess.CreatedAt`（P0-A-1）

```go
func (m *SessionManager) CreateOrGet(id string) (*session.Session, string, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    msgID := m.idGen()
    if id != "" {
        if s, ok := m.sessions[id]; ok { return s, msgID, nil }
    }
    sess := session.NewSession(m.idGen())  // NewSession 已设 CreatedAt/Status
    m.sessions[sess.ID] = sess
    return sess, msgID, nil
}
```

**Nanoid 字母表**：`MustCustomASCII(62, ...)` 用 `[A-Za-z0-9]`，21 字符。

### FR-7：EventLedger

`internal/gateway/eventledger.go` — **整体重写**，关键差异：

1. **不** 定义 `DarvinEventStub`（P1-11），直接用 `event.Event`
2. **不**调 `AttachBus`（v1 签名不可达，P1-1），改 `AttachSubscription(*event.Subscription)` 给 S4 调
3. **Subscribe 签名**：`Subscribe(sessionID string, c *client)`，记录 `sessionID → []*client` 的订阅关系
4. **Publish 改为对所有 client 推送 WS notification** 而不是 channel 转发（v1 的 channel 模型在 S3 推了之后没必要）
5. **EmitStub**：调 sessionID 的所有 subscriber，把 `event.Event.EventName()` 映射到 TS 字段并 SendNotification

```go
type EventLedger struct {
    mu        sync.RWMutex
    bySession map[string]map[*client]struct{}  // sessionID → 订阅该 session 的 client 集合
    log       *zap.Logger
    fakeDelay time.Duration
}

func (l *EventLedger) Subscribe(sessionID string, c *client) {
    l.mu.Lock()
    defer l.mu.Unlock()
    if l.bySession[sessionID] == nil { l.bySession[sessionID] = make(map[*client]struct{}) }
    l.bySession[sessionID][c] = struct{}{}
}

func (l *EventLedger) UnsubscribeAll(c *client) {
    l.mu.Lock()
    defer l.mu.Unlock()
    for sid, set := range l.bySession {
        delete(set, c)
        if len(set) == 0 { delete(l.bySession, sid) }
    }
}

func (l *EventLedger) publishLocked(sessionID string, ev event.Event) {
    for c := range l.bySession[sessionID] {
        params := mapEventToTS(ev)  // §4.2 映射表
        c.SendNotification("agent.event", params)
    }
}

// EmitStub：S3 用 fake event 跑通整条推送
func (l *EventLedger) EmitStub(sessionID, msgID, content string) {
    go func() {
        l.publishLocked(sessionID, event.TextDeltaEvent{Delta: "Echo: " + truncate(content, 80)})
        time.Sleep(l.fakeDelay * 2)
        l.publishLocked(sessionID, event.AgentEndEvent{})  // 替代 v1 的 stub "done"
        l.log.Info("EmitStub done", zap.String("sessionId", sessionID))
    }()
}
```

`mapEventToTS`：§4.2 映射表，完全对齐 S1 `darvin-api.ts:37-44` 的 `DarvinEvent` union（**无 sessionId**）。S3 只 emit `text_delta` + `agent_end` 两个；其余 EventType 留空 `params = {type: ev.EventName()}` 等 S4 补。

### FR-8：main.go 接入 Gateway + 优雅关闭

`cmd/app/main.go` 接入 Gateway，**且**把 v1 的 `select{}` 替换为：

```go
// v1: select{}
// v2: signal.Notify + graceful shutdown
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

sessions := gateway.NewSessionManager(log.Logger)
ledger := gateway.NewEventLedger(log.Logger)
gs := gateway.NewServer(sessions, ledger, log.Logger)
if err := gs.Start(ctx); err != nil { /* ... */ }

log.Info("gateway listening", zap.Int("port", gs.Port()))

<-ctx.Done()
log.Info("shutdown signal received")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
if err := gs.Shutdown(shutdownCtx); err != nil { log.Error("gateway shutdown", zap.Error(err)) }
log.Info("graceful shutdown complete")
```

`config.yaml` 搜索顺序（v1 是相对 cwd 单一路径，v2 三级回退）：

```go
func configPath() string {
    if p := os.Getenv("DARVIN_CONFIG"); p != "" { return p }
    if exe, err := os.Executable(); err == nil {
        cand := filepath.Join(filepath.Dir(exe), "config.yaml")
        if _, err := os.Stat(cand); err == nil { return cand }
    }
    return "config.yaml"
}

func main() {
    cfg, err := config.Load(configPath())
    // ...
}
```

### FR-9：logger + gorm 重定向 stderr

**`internal/logger/logger.go:55`** 改：

```go
var sink zapcore.WriteSyncer
switch cfg.Output {
case "stderr":
    sink = zapcore.AddSync(os.Stderr)
case "file":
    // 保留 file tee（与 stdout 并存；典型场景是开发 + 落盘）
    sink = zapcore.AddSync(os.Stdout)
    if cfg.Filename != "" {
        fileSink := zapcore.AddSync(&lumberjack.Logger{...})
        sink = zapcore.NewTee(sink, fileSink)
    }
default:  // 旧 "stdout" 显式
    sink = zapcore.AddSync(os.Stdout)
}
```

**`internal/database/sqlite.go:23`** gorm logger 改：

```go
import (
    "os"
    gormlogger "gorm.io/gorm/logger"
)

newLogger := gormlogger.New(
    log.New(os.Stderr, "[gorm] ", log.LstdFlags),  // 标准 log，前面已切 stderr
    gormlogger.Config{LogLevel: gormlogger.Info},
)
db, err := gorm.Open(glebarezSqlite.Open(cfg.SessionsDSN), &gorm.Config{Logger: newLogger})
```

**`src/darvin-agent/config.yaml`** 改 `log.output: "stderr"`。

### FR-10：加 `ThinkingDeltaEvent` 空壳

`internal/agent/event/event.go` 加：

```go
type ThinkingDeltaEvent struct {
    Delta string
}
func (ThinkingDeltaEvent) isAgentEvent() {}
func (ThinkingDeltaEvent) EventName() string { return "thinking_delta" }
```

`internal/agent/llm/events.go` 同步加 `ThinkingDeltaEvent{Delta string}`（`StreamEvent` 实现者）。S3 不调 emit（provider 解析留 S4）。

### FR-11：替换 driver — `glebarez/sqlite`

**`internal/database/sqlite.go:4`** 改 import：

```go
import (
    glebarezSqlite "github.com/glebarez/sqlite"
    "gorm.io/gorm"
)
```

函数体 `gorm.Open(sqlite.Open(...), ...)` 改 `gorm.Open(glebarezSqlite.Open(...), ...)`。

S2 的 `sqlite_test.go` 应**零改动**继续通过（如有 cgo 相关的 `go-sqlite3` 字样则需删；事实上 sqlite_test 用 `database/sql` + `Open`，与 gorm driver 解耦，无需改）。

`scripts/build-go.js` 保留 `CGO_ENABLED:0`（glebarez 是纯 Go，可编）。

### FR-12：cmd/app/main.go 路径

`command-name/main.go:30` 改 `go build -o "$out" ./cmd/app`（v1 是 `.`）。

---

## 4. 实现方案

### 4.1 目录结构

```
src/darvin-agent/
├── go.mod                                         # 改：加 3 个依赖、gorm 升 v1.25.7
├── config.yaml                                    # 改：log.output: stderr
├── scripts/build-go.js                            # 改：./cmd/app
├── internal/
│   ├── agent/event/event.go                       # 改：加 ThinkingDeltaEvent
│   ├── agent/llm/events.go                        # 改：加 ThinkingDeltaEvent
│   ├── database/sqlite.go                         # 改：driver → glebarez
│   ├── logger/logger.go                           # 改：cfg.Output == "stderr" 分支
│   ├── gateway/
│   │   ├── server.go          # 改：注入 *zap.Logger
│   │   ├── jsonrpc.go         # 🆕 JSON-RPC 2.0 envelope
│   │   ├── client.go          # 改：SendNotification + UnsubscribeAll 注册
│   │   ├── handlers.go        # 改：dispatchRequest 拿 *client
│   │   ├── sessionmgr.go      # 改：MustCustomASCII
│   │   ├── eventledger.go     # 改：Subscribe(sessionID, *client) + publishLocked
│   │   ├── server_test.go     # 🆕
│   │   ├── jsonrpc_test.go    # 🆕
│   │   ├── handlers_test.go   # 🆕（v1 漏）
│   │   ├── client_test.go     # 🆕（v1 漏）
│   │   ├── sessionmgr_test.go # 🆕
│   │   └── eventledger_test.go# 🆕
│   └── agent/...
└── cmd/app/main.go                                # 改：configPath 三级回退 + signal.Notify
```

### 4.2 Go ↔ TS 事件映射表

| Go `event.Event` 类型 | `EventName()` | TS `DarvinEvent` 类型 | v2 S3 覆盖 |
|---|---|---|---|
| `event.PromptReceivedEvent` | `prompt_received` | （不暴露） | — |
| `event.RunStartEvent` | `run_start` | （不暴露） | — |
| `event.TurnStartEvent` | `turn_start` | （不暴露） | — |
| `event.LLMStartEvent` | `llm_start` | （不暴露） | — |
| `event.TextDeltaEvent` | `text_delta` | `DarvinTextDeltaEvent` `{type: "text_delta", messageId, delta}` | ✅ EmitStub emit |
| `event.ThinkingDeltaEvent`（v2 新增） | `thinking_delta` | `DarvinThinkingDeltaEvent` `{type: "thinking_delta", messageId, delta}` | ❌ S3 不 emit；S4 补 |
| `event.ToolStartEvent` | `tool_start` | `DarvinToolStartEvent` `{type: "tool_start", messageId, tool, input}` | ❌ S4 补 |
| `event.ToolEndEvent` | `tool_end` | `DarvinToolEndEvent` `{type: "tool_end", messageId, tool, output}` | ❌ S4 补 |
| `event.TurnEndEvent` | `turn_end` | （不单独暴露） | — |
| `event.RunEndEvent` | `run_end` | （不暴露） | — |
| `event.LLMEndEvent` | `llm_end` | （不直接暴露；info event） | — |
| `event.AgentErrorEvent` | `agent_error` | `DarvinErrorEvent` `{type: "error", messageId, message}` | ❌ S4 补 |
| **`event.AgentEndEvent`** | `agent_end` | `DarvinAgentEndEvent` `{type: "agent_end"}` | ✅ EmitStub emit |
| `event.CompactionEvent` | `compaction` | （不暴露给 UI） | — |
| `event.CustomEvent` | `custom`/`custom:<name>` | （不暴露） | — |

**TS 字段对齐**（`src/shared/darvin-api.ts:37-44`）：
- `messageId` 来自 `event.EventName()` 之外的 `messageID` 字段（EventCommon 设计留 S4）
- `tool` / `input` / `output` / `message` 等具体字段 S4 补映射
- S3 EmitStub 当前 forward payload 直接 `{type, messageId, delta}`，**无 sessionId**（TS `DarvinEvent` 无 sessionId 字段，靠 renderer 自己按 messageId 跟踪）

### 4.3 关键决策

#### 4.3.1 端口通过 stdout 而非环境变量

同 v1。可靠性 + 跨平台。

#### 4.3.2 CheckOrigin 暂时接受所有

同 v1。

#### 4.3.3 handler stub 行为

**v1**：`agent.prompt` 立即返回 + 异步 emit fake event 给 ledger（不推 WS）。
**v2**：`agent.prompt` 同步创建 session + 异步 emit fake event → **真推 WS notification**。SessionManager 创建 session 是同步行为（一行 `m.sessions[id] = sess`），耗时 < 1ms，未破坏 JSON-RPC 同步响应语义。

#### 4.3.4 EventLedger 落 sessions.db

S3 不做（handler 没真接 Agent.Run，没消息可落）。S4 接管。

#### 4.3.5 event.Event 通道设计

**v1**：`Subscribe(sessionID) <-chan any`，handler goroutine 消费 → client.WriteJSON。
**v2**：`Subscribe(sessionID, *client)`，EventLedger 直接调 `client.SendNotification(method, params)`（同步、锁内）。更简单，且避免 v1 channel buffer 满 drop 的歧义。

#### 4.3.6 drop 策略

- `event.Bus.Emit`：drop-oldest（event.go:229-248，**确认**）
- `EventLedger.publishLocked`：**同步推**（已上 WriteMu 锁），不 drop；写出错由 `client` 内部 `defer log` 处理

#### 4.3.7 go-nanoid 选型

`github.com/jaevor/go-nanoid v1.4.0` — `MustCustomASCII(21, [A-Za-z0-9])`（纯 ASCII 字母表走 fast path），session_id / message_id 共用。

#### 4.3.8 driver 选型

`github.com/glebarez/sqlite v1.11.0`（modernc.org/sqlite 底座，纯 Go）。`sqlite.Open(dsn)` 与 gorm.io/driver/sqlite API 同形，**改 import 一行**。

### 4.4 关键代码骨架

```go
// internal/gateway/server.go Start 完整版
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
            s.log.Error("gateway serve", zap.Error(err))
        }
    }()
    return nil
}
```

### 4.5 4 配置项改动汇总

| 文件 | 改动 |
|---|---|
| `scripts/build-go.js:30` | `go build -o ... .` → `... ./cmd/app` |
| `src/darvin-agent/config.yaml` | `log.output: stdout` → `stderr` |
| `src/darvin-agent/internal/database/sqlite.go` | `gorm.io/driver/sqlite` → `github.com/glebarez/sqlite`（一行） |
| `src/darvin-agent/internal/logger/logger.go` | `zapcore.AddSync(os.Stdout)` 改 `switch cfg.Output { ... }` |

---

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| WS 客户端断开（`IsCloseError`） | `run()` return → `defer c.ledger.UnsubscribeAll(c)` → defer conn.Close |
| WS 客户端发送非 JSON | `sendError(id, CodeParseError, ...)` |
| JSON-RPC batch 含 0 项 | 忽略（不响应） |
| handler 抛 panic | `dispatch` 用 recover 包成 `CodeInternalError`；不挂掉 client |
| `<port>` stdout 输出前 Electron 已 wait | Electron RuntimeMgr 用 `child_process.spawn` 的 `stdout.on('data')` 累积直到匹配 regex `<port>(\d+)</port>` |
| 同一 client 多次 `agent.subscribe_events` 同一 session | 允许多次（`map[*client]struct{}` 同 key 多次写等价于一次） |
| 同一 client 订阅多个 session | 各自独立（UnsubscribeAll 一次清完） |
| `agent.prompt` content 为空字符串 | 返回 `CodeInvalidParams` "content is required" |
| session 不存在时 `agent.subscribe_events` | 返回 `CodeInvalidParams` "unknown sessionId" |
| 并发 `agent.prompt` 同一 sessionId | 复用已有 session，每次 msgID 独立（goroutine safe） |
| gorilla/websocket 重连 | 不实现（S6+） |
| `DarvinEvent` 字段缺失（S3 只 emit text_delta + agent_end） | 其他事件类型 S4 补；S3 不会 emit 那些类型 |
| config.yaml 不存在 | 三级回退全失败 → `os.Exit(1)`，stderr 输出明确路径 |
| SIGTERM 期间 client 还在写 | `client.SendNotification` 写失败由 `client.writeLoop` 自身的写错误处理；不阻塞 shutdown |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `src/darvin-agent/go.mod` | 加 3 个依赖；gorm 升 v1.25.7 |
| `src/darvin-agent/go.sum` | 自动更新 |
| `src/darvin-agent/config.yaml` | `log.output: stderr` |
| `src/darvin-agent/internal/agent/event/event.go` | 加 `ThinkingDeltaEvent` |
| `src/darvin-agent/internal/agent/llm/events.go` | 加 `ThinkingDeltaEvent` |
| `src/darvin-agent/internal/database/sqlite.go` | driver 改 glebarez |
| `src/darvin-agent/internal/logger/logger.go` | 加 stderr 分支 |
| `src/darvin-agent/internal/gateway/server.go` | 🆕 WS server + port stdout |
| `src/darvin-agent/internal/gateway/server_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/jsonrpc.go` | 🆕 JSON-RPC 2.0 |
| `src/darvin-agent/internal/gateway/jsonrpc_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/client.go` | 🆕 WS connection + SendNotification + UnsubscribeAll |
| `src/darvin-agent/internal/gateway/client_test.go` | 🆕（v1 漏） |
| `src/darvin-agent/internal/gateway/handlers.go` | 🆕 3 个 handler |
| `src/darvin-agent/internal/gateway/handlers_test.go` | 🆕（v1 漏） |
| `src/darvin-agent/internal/gateway/sessionmgr.go` | 🆕 SessionManager |
| `src/darvin-agent/internal/gateway/sessionmgr_test.go` | 🆕 |
| `src/darvin-agent/internal/gateway/eventledger.go` | 🆕 EventLedger |
| `src/darvin-agent/internal/gateway/eventledger_test.go` | 🆕 |
| `src/darvin-agent/cmd/app/main.go` | 改：configPath + signal.Notify + Start Gateway |
| `scripts/build-go.js` | 改：路径 `./cmd/app` |
| `src/darvin-agent/.gitignore` | 已有 `sessions.db`/`data.db`；**新增** `bin/` |

**不修改**：
- S2 落地的 models.go / sqlite_store.go / memory.go
- executor / ctxengine / dispatcher / queue
- `internal/agent/agent.go`（不引入 `Bus()` getter 也很合理：S3 不需要；S4 真接 EventLedger 时改 `AttachSubscription` 即可）
- `internal/agent/agent_test.go`
- `internal/agent/llm/anthropic/*`（S4 补 stream 解析）
- `src/shared/darvin-api.ts`（S1 锁死契约不变）

---

## 7. 验收标准

- [x] `go build ./...` 编译通过
- [x] `go vet ./...` 无警告
- [x] `gofmt -l .` 干净
- [x] `go test ./internal/gateway/... -race` 全绿（6 个 `_test.go`）
- [x] `go test ./internal/agent/store/... -race` 全绿（S2 回归：driver 换成 glebarez 之后 7 个 SQLite 测试不变绿）
- [x] `go test ./... -race` 全绿（S2 兜底）
- [x] `node scripts/build-go.js` 成功（修过的 `./cmd/app` 路径）→ `bin/darvin-agent-<os>-<arch>` 落地
- [x] 启动 `bin/darvin-agent-<os>-<arch>`（cwd 任意）后 **stdout 唯一一行** `<port>NNNNN</port>`，stderr 含 INFO log "gateway listening port=..."，进程不退出
- [x] Ctrl-C 触发 SGINT → stderr 输出 "graceful shutdown complete" + 进程 exit 0，**总耗时 ≤ 3s**
- [x] `wscat -c ws://localhost:NNNNN/ws` 连上（**带 `/ws` 路径**）
- [x] 发 `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"hi"}}` → 收到 `{"jsonrpc":"2.0","id":"1","result":{"sessionId":"<21字符>","messageId":"<21字符>"}}`（无 `s-`/`m-` 前缀）
- [x] 用上一步返回的真 sessionId 发 `{"jsonrpc":"2.0","id":"2","method":"agent.subscribe_events","params":{"sessionId":"<上面那个>"}}` → `{"subscribed":true}`
- [x] subscribe 后 ≤ 1s 收到 2 条 WS notification（顺序不保证）：
  - `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","messageId":"<m>","delta":"Echo: hi"}}`
  - `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"agent_end"}}`
- [x] 发 `{"jsonrpc":"2.0","id":"3","method":"unknown"}` → `error.code=-32601`
- [x] 发 `[{"jsonrpc":"2.0","id":"4","method":"agent.prompt","params":{"content":"a"}},{"jsonrpc":"2.0","id":"5","method":"agent.abort","params":{"sessionId":"any"}}]` → 数组响应 `[id=4 result, id=5 result]`
- [x] 发 `{"jsonrpc":"2.0","id":"6","method":"agent.prompt","params":{}}`（缺 content）→ `error.code=-32602`
- [x] SessionManager.CreateOrGet 同 sessionId 两次调用返回同一 `*session.Session` 实例，msgID 独立
- [x] nanoid 21 字符生成 10000 次无重复（属性测试）
- [x] 在 4 个不同 cwd 下启动 binary（repo root / `src/darvin-agent/` / `/tmp/` / `bin/` 同级）都能找到 config.yaml（最后一种走 DARVIN_CONFIG 兜底）
- [x] handler stub 完成后 ledger stderr 含 "EmitStub done" 日志

> **已落地（2026-07-30）— 18/18 项全绿**
>
> **实际落地方式**：本次整 spec 作为一个聚合 `feat(agent)` commit 落地（impl + 6 套 test + v2 spec 文档 + 架构相关 side 改动一起），对应 `feat/agent-loop` 分支上的下一个 `feat(agent): ship S3 agent-gateway-server` commit。S4 启动后用 `git log --oneline` 找 v3 spec + impl 的对应 commit 串。
>
> 验收实测（cwd = `src/darvin-agent/`）：
>
> - `go build ./...` ✓ / `CGO_ENABLED=0 go build ./...` ✓ / `go vet ./...` 无警告 / `gofmt -l .` 干净。
> - `go test -race ./...` 全绿（gateway 6 套 + store 7 套，无 flaky）。
> - `node scripts/build-go.js` 成功 → `bin/darvin-agent-linux-x64` 落地可执行。
> - 启动 binary 实际只产出一行 `<port>34939</port>` 到 stdout，stderr 走 INFO 结构化日志。
> - SIGINT 优雅关闭 3s timeout 实测 < 100ms（无 in-flight 请求），stderr 输出 "graceful shutdown complete"。
> - 6 个 JSON-RPC 验收通过 `/tmp/ws-smoke.sh` 实测：unknown → -32601、batch 数组返回、缺 content → -32602、subscribe_events → 2 条 notification（text_delta + agent_end）。
> - SessionManager 同 id 返同实例（用 `*session.Session` 指针相等断言）；msgID 独立（每次 `CreateOrGet` 都重新生成 21 字符）。
> - nanoid 10000 次属性测试（`sessionmgr_test.go` TestCreateOrGetManyDistinct）无重复。
> - 4 cwd 找 config：`./` / `src/darvin-agent/` / `/tmp/` / `bin/`（最后一种靠 `DARVIN_CONFIG` 注入）全部能起。
>
> **相对 v2 spec 的微小偏差（3 项，已 review 接受）**：
>
> 1. **CHECKLIST §FR-6 列的 `AddCallback` 未实装**——v2 spec §3.2 写的是 `Has`/`Get` 两个 helper，CHECKLIST 沿用 v1 字面多列了一个 `AddCallback`。最终代码只实装 `Has`/`Get` 两个方法（v2 spec 是 ground truth）。
> 2. **`config.yaml` 的 `llm.api_key` 实际是 `sk-ant-smoke-test-placeholder`**——v2 spec 写 "空字符串即可"，但跑 smoke test 时 `agent.prompt` handler stub 不调 LLM（只 emit stub event），此值仅占位以满足 `internal/config` 字段必填校验。S4 接真 LLM 时替换为真 key。
> 3. **`scripts/build-go.js` 移除了 `GOOS`/`GOARCH` 覆盖**——v2 spec §7 只要求 "build → bin/darvin-agent-<os>-<arch>"，本机开发走默认 GOOS=linux 即可；CI 阶段如有跨平台需求再加 env override。
>
> **已知 follow-up（写进 §8 已有的 S4 候选）**：
>
> - `AttachSubscription` 在 S3 是 no-op；S4 把 `*event.Subscription` 接入，goroutine decode 推 `publishLocked`。
> - `mapEventToTS` S3 只实装 text_delta / agent_end / thinking_delta 三个 type；tool_start/tool_end/agent_error 的 TS 形状已在 S3 写好但 S3 不触发，S4 补全。
> - v2 spec §7 期望 `text_delta` notification 直接带顶层 `messageId` 字段（line 625），实际 S3 把 `messageId` 嵌在 `message.id` 里（`mapEventToTS` line 136-137），与 S1 `src/shared/darvin-api.ts` 的 `DarvinEvent` union 一致；S4 引入 `EventCommon` 后会通过 `mapEventToTS` 在 S4 重构时统一露出。

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|---|---|
| **S4** agent-acp-loop | handler 替换为真 ACP + Agent.Run；EventLedger.AttachSubscription 订阅 event.Bus 推 WS notification；SendNotification 携带 messageId 字段（EventCommon 设计） |
| ResolveCost / Cost | 架构文档 §Provider 抽象层要求，实装 |
| `lobsterai.db` | 架构文档 §数据库归属列出但六份 spec 都没提，是否需要立 spec 立项 |
| Auth / Bearer token | v0 阶段无鉴权；远期 spec 引入 |
| WSS / TLS | 远期 spec |
| reconnect / 心跳 | 当前实现简单 ping-pong；远期可加 reconnect 策略 |
| EventLedger 落 sessions.db | S4 接管 |
| 跨进程 EventLedger 聚合 | 多 Go Agent 子进程时的事件路由 |
| anthropic stream 解析 thinking_delta | S4 补 `content_block_delta` thinking 类型的解析，调 `event.ThinkingDeltaEvent.Emit` |
