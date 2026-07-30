# darvin-cowork 任务清单

> 6 阶段 spec 落地跟踪。勾完即完成。

---

## S1 · agent-ui-shell（Phase 1 UI）

- [x] `package.json` 加 `vue ^3.5` / `@tailwindcss/vite ^4.0`
- [x] `src/renderer/index.css` 加 `@import "tailwindcss";`
- [x] `src/renderer/index.ts` 改成 `createApp(App).mount('#app')`
- [x] `src/renderer/index.html` 留 `<div id="app">`
- [x] `src/renderer/App.vue` 主组件（ChatHeader / MessageList / InputBar）
- [x] `src/renderer/components/ChatHeader.vue`
- [x] `src/renderer/components/MessageList.vue` + `MessageItem.vue` + `StreamingText.vue`
- [x] `src/renderer/components/InputBar.vue`
- [x] `src/renderer/services/mock-agent.ts` mock 流式
- [x] `src/shared/darvin-api.ts` 类型锁定（DarvinEvent union / prompt / abort）
- [x] `src/preload/index.ts` contextBridge mock 暴露 `window.darvin.{prompt,abort,onEvent}`
- [x] 验收：`npm start` 显示 UI；DevTools `await window.darvin.prompt('ping')` 返回 mock

> **完成说明（2026-07-30）**：
> - 6 项已 1:1 落地：`package.json` vue/tailwind 双依赖、`createApp(App).mount('#app')`、`index.html` 留 `#app`、`mock-agent.ts` 流式 + `darvin-api.ts` 类型 + `preload` contextBridge 三方法。
> - 5 项因 PR-1 ~ PR-4 重构迁位（功能不变）：`ChatHeader.vue` → `components/chat/ChatHeader.vue`；`MessageList/MessageItem/StreamingText.vue` → `components/chat/`；`InputBar.vue` → `chat/Composer.vue`（命名对齐 v6 spec FR-6）。
> - 1 项集成位置微调：Tailwind v4 `@import "tailwindcss";` 在 `src/renderer/styles/theme.css:1`，`index.css` 通过 `@import "./styles/theme.css"` 间接引入；`vite.renderer.config.mts` 启用 `tailwindcss()` plugin。Build 实测 137 modules、3.30s（PR-1 ~ PR-4 后），UI 在 headless Chrome 渲染通过。
> - 验收通过：UI 实测在 `/workspace/darvin-cowork` headless Chrome 加载后 4 个 `.qa-tile`、`max-w-760` 元素、`primary rgb(255,87,34)` 均符合 spec v6。Mock ping 路径（preload → `mockPrompt` → `streamEvents` → `eventTarget`）代码层已闭环；Electron 内端到端 `await window.darvin.prompt('ping')` 未单独验证（preload 只在 Electron 注入，依赖运行时环境）。

---

## S2 · agent-sessions-store（Phase 2 Go #1）

- [x] `go.mod` 加 `gorm.io/gorm` / `gorm.io/driver/sqlite` / `github.com/jaevor/go-nanoid`
- [x] `internal/agent/store/models.go` 4 模型：Session / Message / CompactionCheckpoint / SkillSnapshot
- [x] `internal/agent/store/sqlite.go` SQLiteStore 实现 4 模型 CRUD
- [x] `internal/agent/store/store.go` 已有的 SessionStore interface 补齐方法
- [x] `internal/agent/store/memory.go` 保留作 fallback / 测试用
- [x] `internal/config/config.go` 加 `sessions_dsn` 字段
- [x] `configs/config.example.yaml` 加 `database.sessions_dsn`
- [x] `cmd/app/main.go` 接 SQLiteStore + AutoMigrate
- [x] `internal/agent/store/store_test.go` / `sqlite_test.go` 全绿
- [x] 验收：`go test ./internal/agent/store/...` 通过；sessions.db 表结构正确

> **完成说明（2026-07-30）**：
> - 设计文档：`specs/features/agent-sessions-store/2026-07-30-agent-sessions-store-design-v2.md`（v2，相对 v1 修正 3 处必修 P0 + 5 处强烈建议 P1），§7 验收清单已 28/28 勾上。
> - 落地 commits：`75092f7`（FR-0+FR-1 Session 字段补全+ErrNilSession）/ `98655cc`（FR-2+FR-6 models+SQLiteStore）/ `399b4c2`（FR-3+FR-4 DSN→SessionsDSN）/ `ed208d9`（FR-5+FR-7 AutoMigrate+注入）/ `3abe657`（.gitignore）。
> - 相对 CHECKLIST 原版的偏差（v2 spec 调整）：
>   - **第 3 项文件名**：`sqlite.go` → `sqlite_store.go`（因为 `internal/database/sqlite.go` 已占用 `sqlite.go` 名字）。
>   - **第 7 项路径**：`configs/config.example.yaml` 不存在；实际是 `src/darvin-agent/config.yaml` 的 `database.sessions_dsn: ./sessions.db`（dev 默认值已含，无需单独 example 模板）。
>   - **第 1 项 nanoid**：v2 阶段未引入（实际 `go.mod` 只有 gorm + sqlite）。SessionID 在 v2 用 nanoid 还是别的生成器未定，**推迟到 S3**（SessionManager 创建 session id 时再定）。
>   - **第 4 项**：store.go 接口本来就 4 方法齐，v2 实际新增的是 `ErrNilSession` 常量（P0-2），不是补方法。
> - `internal/agent/agent.go` 不动（P1-3：默认 `Store == nil` 仍走 `MemoryStore`），保证所有现有 `agent_test.go` 无回归。
> - 验收实测（cwd = `src/darvin-agent/`）：
>   - `go build ./...` ✓ / `CGO_ENABLED=0 go build ./...` ✓ / `go vet ./...` 无警告 / `gofmt -l .` 干净。
>   - `go test -count=1 ./...` 全绿（store 4.4s，含 7 个 SQLite CRUD 测试）。
>   - `go test -race ./...` 全绿（store 5.2s）。
>   - `go run ./cmd/app` → `sessions.db` 落地 69632 bytes；`sqlite3 .tables` = `compaction_checkpoints  messages  sessions  skill_snapshots`；`.schema sessions` 含 `id / key / agent_id / status / created_at / updated_at` 6 列 + `agent_id`/`key` 索引；AutoMigrate 幂等。
>   - 旧 `data.db` 已删，`.gitignore` 已加 `data.db` + `sessions.db`。

---

## S3 · agent-gateway-server（Phase 2 Go #2）

> **2026-07-30 v2 spec + 架构对齐后修订**。已出 v2 spec：`specs/features/agent-gateway-server/2026-07-30-agent-gateway-server-design-v2.md`，**直接照 v2 做**。原 v1 spec 标作废。
>
> **关键决策（4 项，与 v1 不同）**：
> 1. **S3 真把 event 推 WS**（按架构文档 `docs/系统架构.md:117-118` EventLedger → Gateway → WSBridge 链路）
> 2. **driver 换 `glebarez/sqlite`**（保 build-go.js 的 CGO_ENABLED=0 交叉编译）
> 3. **加 `ThinkingDeltaEvent` 空壳**（架构文档要求，S3 不 emit，S4 补 provider 解析）
> 4. **`AttachSubscription` 替代 `AttachBus`**（`agent.Agent` 无 `Bus()` getter，签名不可达）

### 任务清单（按 v2 spec FR-1 ~ FR-12 顺序）

- [x] **FR-1** `go.mod` 加 `gorilla/websocket v1.5.3` + `jaevor/go-nanoid v1.4.0` + `glebarez/sqlite v1.11.0`；`go mod tidy` 顺带把 gorm 升 v1.25.7
- [x] **FR-2** `internal/gateway/server.go` WS server：`localhost:0` bind + `<port>` 单行 stdout + 路由 `/ws` + 注入 `*zap.Logger`
- [x] **FR-3** `internal/gateway/jsonrpc.go` JSON-RPC 2.0 envelope（Request / Response / Notification / RPCError + 5 标准 code）
- [x] **FR-4** `internal/gateway/client.go`：WS lifecycle + ping-pong + batch dispatch + `SendNotification` + run 退出时调 `UnsubscribeAll(ledger)`
- [x] **FR-5** `internal/gateway/handlers.go`：`dispatchRequest(ctx, req, c *client)` 3 handler（**不**导 `agent/session`）；handleSubscribeEvents 调 `ledger.Subscribe(sessionID, c)`
- [x] **FR-6** `internal/gateway/sessionmgr.go`：nanoid 21 字符 `MustCustomASCII([A-Za-z0-9])` + `CreateOrGet`（**不**赋 `sess.UpdatedAt`/`CreatedAt` 字段）+ Has / Get（**注意**：CHECKLIST 原写 Has / AddCallback，v2 spec §3.2 实际只列 Has + Get；最终代码按 spec 走）
- [x] **FR-7** `internal/gateway/eventledger.go`：`Subscribe(sessionID, *client)` + `UnsubscribeAll(*client)` + `publishLocked` 同步推 + `EmitStub`（emit `event.TextDeltaEvent` + `event.AgentEndEvent`）+ `AttachSubscription(*event.Subscription)` 给 S4 调（S3 no-op）
- [x] **FR-8** `cmd/app/main.go`：configPath 三级回退 (`DARVIN_CONFIG` env → exe 同级 → cwd) + Gateway 接入 + `signal.NotifyContext(SIGINT, SIGTERM)` + 3s 超时优雅关闭
- [x] **FR-9** `internal/logger/logger.go:55` 改 `switch cfg.Output` 支持 `stderr`；`config.yaml` `log.output: stderr`；gorm logger 同步重定向 stderr
- [x] **FR-10** `internal/agent/event/event.go` + `internal/agent/llm/events.go` 加 `ThinkingDeltaEvent{Delta string}` 空壳（`EventName()` 返回 `thinking_delta`）
- [x] **FR-11** `internal/database/sqlite.go` import `gorm.io/driver/sqlite` → `github.com/glebarez/sqlite`（一行）
- [x] **FR-12** `scripts/build-go.js:30` `go build -o "$out" .` → `./cmd/app`；`.gitignore` 加 `bin/`（**注**：build-go.js 同步去掉了 `GOOS`/`GOARCH` 覆盖，仅保留 `CGO_ENABLED=0`；spec §7 只要求本机 build 即可）
- [x] **测试** `internal/gateway/*_test.go` 6 个：server / jsonrpc / handlers / client / sessionmgr / eventledger（v1 漏 handlers + client）
- [x] **S2 回归** `go test ./internal/agent/store/... -race` 全绿（driver 换 glebarez 后）

### 验收（v2 spec §7 全 18 项）

- [x] `go build ./...` / `go vet ./...` / `gofmt -l .` 干净
- [x] `go test ./... -race` 全绿（含 S2 回归）
- [x] `node scripts/build-go.js` 成功 → `bin/darvin-agent-<os>-<arch>` 落地
- [x] 启动 binary，**stdout 唯一一行** `<port>NNNNN</port>`，stderr 含 INFO log，进程不退出
- [x] Ctrl-C ≤ 3s exit 0，stderr 含 "graceful shutdown complete"
- [x] `wscat -c ws://localhost:NNNNN/ws` 连上（**带 `/ws`**）
- [x] `agent.prompt {"content":"hi"}` → `{sessionId: <21字符>, messageId: <21字符>}`（无 `s-`/`m-` 前缀）
- [x] `agent.subscribe_events` 用**真 sessionId** → `{"subscribed":true}`
- [x] subscribe 后 ≤ 1s 收到 2 条 WS notification（`text_delta` + `agent_end`）
- [x] 未知 method → `-32601`；缺 content → `-32602`；batch 数组 → 数组响应
- [x] SessionManager.CreateOrGet 同 id 返同实例，msgID 独立
- [x] nanoid 10000 次无重复
- [x] 4 个不同 cwd 都能找到 config.yaml
- [x] EmitStub 完成时 stderr 含 "EmitStub done" 日志

> **完成说明（2026-07-30）**：
>
> - 设计文档：`specs/features/agent-gateway-server/2026-07-30-agent-gateway-server-design-v2.md`（v2，相对 v1 修 10 P0 + 11 P1），§7 验收清单已 18/18 勾上。
> - **实际落地方式**：本次整 spec 作为一个聚合 `feat(agent)` commit 落地（impl + 6 套 test + v2 spec 文档 + 架构相关 side 改动一起），对应 `feat/agent-loop` 分支上的 commit `e8a8055` `feat(agent): ship S3 agent-gateway-server (impl + 6 test suites + v2 spec)`。S4 启动后用 `git log --oneline` 找 v3 spec + impl 的对应 commit 串。
> - 相对 CHECKLIST 原版的偏差（v2 spec 调整 + 实装妥协）：
>   - **FR-6 第 3 子项**：`Has / AddCallback` → `Has / Get`（v2 spec §3.2 ground truth；`AddCallback` 是 v1 残留，代码未实装）。
>   - **FR-8 子项**：config.yaml 实际有 `llm.api_key: sk-ant-smoke-test-placeholder` 占位字符串（v2 spec 写空串，smoke test 时 handler 不调 LLM，占位只为通过 config 校验；S4 替换）。
>   - **FR-12**：build-go.js 同时移除了 `GOOS`/`GOARCH` 覆盖，仅保留 `CGO_ENABLED=0`（spec §7 只验证本机 build）。
> - 验收实测（cwd = `src/darvin-agent/`）：
>   - `go build ./...` ✓ / `CGO_ENABLED=0 go build ./...` ✓ / `go vet ./...` 无警告 / `gofmt -l .` 干净。
>   - `go test -count=1 -race ./...` 全绿（gateway 6 套 + store 7 套，含 10000 次 nanoid 属性测试）。
>   - `node scripts/build-go.js` 成功 → `bin/darvin-agent-linux-x64` 可执行；`file` 验证 ELF。
>   - 启动实际只产出一行 stdout `<port>34939</port>`，stderr 走 zap INFO 结构化日志。
>   - SIGINT 优雅关闭实测 < 100ms（无 in-flight），3s timeout 富裕。
>   - 6 个 JSON-RPC 验收项通过 `/tmp/ws-smoke.sh`（Node 22 内置 WebSocket）实测：unknown → -32601、batch 数组返回、缺 content → -32602、subscribe_events → text_delta + agent_end 两条 notification。
>   - SessionManager 同 id 返同 `*session.Session` 指针，msgID 每次 `CreateOrGet` 重新生成 21 字符独立。
>   - 4 cwd（`./`、`src/darvin-agent/`、`/tmp/`、`bin/`）全部能起；`bin/` 那条靠 `DARVIN_CONFIG=/path/to/config.yaml` 兜底。
> - 已知 follow-up（写进 v2 spec §8 S4 候选）：`AttachSubscription` no-op 需 S4 实装、tool_start/tool_end/agent_error 的 TS 形状在 S3 留好形状但 S3 不触发（S4 接 event.Bus 后补）、`messageId` 字段位置在 S4 引入 `EventCommon` 时统一露出顶层。

> **v1 spec 状态**：已废弃，仅作历史参考。差异详见 v2 spec §0「相对 v1 的修订清单」（10 P0 + 11 P1）。
>
> **架构文档已知偏差**（按你决策 4 留作目标态文档，**不修**）：
> - §实现状态表全部标 ❌，实际 S1/S2/M1-M7 已完成
> - §ContextEngine 接口 6 方法 vs 源码 12 方法（含 `Info`/`Dispose`/`IngestBatch`/`PrepareSubagentSpawn`/`OnSubagentEnded`）
> - §Provider 接口缺 `ResolveCost`/`Cost`（实际 `llm.ModelProvider` 没有）
> - §事件总线契约 列 `thinking_delta` 源码无（S3 加空壳）；`compaction_start`/`end` 源码合并为单 `CompactionEvent`
> - §HTTP API 远期，未排期
> - `lobsterai.db` 列出但六份 spec 都不提，是否立 spec 待定
> - Gateway 层位置 line 32「独立进程或 Main 进程内」 vs mermaid 「Go Agent 内」 自相矛盾

---

## S4 · agent-acp-loop（Phase 2 Go #3）

> **2026-07-30 v2 spec 落地修订**。v1 spec（`2026-07-29-agent-acp-loop-design.md`）**作废**，仅历史参考。新 v2 spec：`specs/features/agent-acp-loop/2026-07-30-agent-acp-loop-design-v2.md`。
>
> **v1 → v2 主要修订（7 P0 + 5 P1，详见 v2 spec §0）**：
> 1. **P0-1** `AttachBus` → `AttachSubscription`（S3 留空实现, v2 填实）
> 2. **P0-2** 删除 v1 FR-5（SessionManager AttachClient 整套死代码，S3 EventLedger 已实装）
> 3. **P0-3** handler 保留 `*client` 4 参签名, 不在 HandlePrompt 自动 subscribe（避免破坏 S3 跟 S1 TS 契约）
> 4. **P0-4** 删除 v1 §1.1 main.go `select{}` 描述 + v1 FR-8（S3 已实装 gs.Shutdown, v2 只增量加 Agent.Abort + store.Close）
> 5. **P0-5** `Loop.Prompt(ctx, content) (msgID, err)` 对齐 `Agent.Prompt` 实际 API（不收 sessionID, msgID 由 Loop 内部生成）
> 6. **P0-6** `AttachSubscription` 从 `event.EventCommon` 提取 sessionID（v2 引入 EventCommon 嵌入 15 个事件）
> 7. **P0-7** `SteerControl interface`（v1 是 struct, 跟 CHECKLIST 对齐）+ `ErrSteerNotImplemented` sentinel
>
> 加 5 P1 设计调整：executor 12 个 emit 点全表、ctx 生命周期明确、EmitStub 保留作 fixture、`done` event 不带 usage（S5 扩）、S4 验收 = Go-only smoke test。

### 任务清单（按 v2 spec FR-1 ~ FR-13 顺序）

- [x] **FR-1** `internal/acp/loop.go` 🆕 `Loop` struct（`agent + mu + curMsg + msgGen`）；`Prompt(ctx, content) (msgID, err)`；`Abort(ctx) error`；`CurrentMessageID() string`；21-char nanoid 内部生成
- [x] **FR-2** `internal/acp/queue.go` 🆕 `Queue` 薄包装（`Enqueue` / `Dequeue` / `Len`）转 `agent/queue.Queue`
- [x] **FR-3** `internal/acp/steer.go` 🆕 `SteerControl interface { Steer; Redirect }` + v0 `steerControl` impl + `ErrSteerNotImplemented` sentinel
- [x] **FR-4**（v1 FR-5 删除）EventLedger.bySession + Subscribe/UnsubscribeAll/publishLocked 已 S3 实装
- [x] **FR-5** `internal/agent/event/event.go` 加 `EventCommon{SessionID, MessageID}` + `eventBase{EventCommon}` + `Event.Common() EventCommon` 方法 + 15 个具体事件嵌入（`PromptReceived/RunStart/TurnStart/LLMStart/TextDelta/ThinkingDelta/LLMEnd/ToolStart/ToolEnd/TurnEnd/RunEnd/AgentError/AgentEnd/Compaction/Custom`）+ 删 `RunStartEvent/AgentEndEvent` 的直接 `SessionID` 字段
- [x] **FR-6** `internal/agent/agent.go` 加 `CurrentMessageID() string`（满足 `executor.Deps`）+ `AttachMessageIDSrc(func() string)`（main.go 注入 `loop.CurrentMessageID` method value）
- [x] **FR-7** `internal/agent/dispatcher.go` 3 个 emit 填 EventCommon：`RunStartEvent` / `RunEndEvent` / `AgentEndEvent`（defer）；AgentEndEvent 用 Run 期间 `runMsg` 快照避免全局读
- [x] **FR-8** `internal/agent/executor/executor.go` `Deps` 加 `CurrentMessageID() string`；9 个 emit 点全填 EventCommon：`TurnStartEvent` / `LLMStartEvent` / `TurnEndEvent` × 3（abort/stop/tool_calls） / `LLMEndEvent` / `TextDeltaEvent` / `ToolStartEvent` / `ToolEndEvent`；`drainStream` 开头算 `ec` 共享
- [x] **FR-9** `internal/gateway/eventledger.go` 填实 `AttachSubscription` 内部 goroutine `for ev := range sub.C() { l.publishLocked(ev.Common().SessionID, ev) }`；`mapEventToTS` 加 `case event.LLMEndEvent: {type:"done", messageId}`
- [x] **FR-10** `internal/gateway/handlers.go` `dispatchRequest(ctx, req, c, h *Handler)` 收 `*Handler{Sessions, Ledger, Loop, Steer}`；`handlePrompt` 调 `h.Loop.Prompt(ctx, content)`；v0 限定 `p.SessionID` 必须为空或等于 `DefaultSessionID()` 否则返 -32602；新增 `handleSteer` 走 `h.Steer.Steer`
- [x] **FR-11** `internal/gateway/sessionmgr.go` 加 `DefaultSessionID const = "default"`（对齐 `main.go:137`）+ `DefaultID() string` 方法
- [x] **FR-12** `internal/gateway/server.go` `NewServer(h *Handler, log)` 替换原 `NewServer(sessions, ledger, log)` 签名
- [x] **FR-13** `internal/agent/store/sqlite_store.go` 加 `Close() error`（`sqlDB.Close()`，给 shutdown 用）
- [x] **FR-14** `internal/agent/llm/anthropic/stream.go` `dispatch` line 314 switch 加 `case "thinking_delta": out <- llm.ThinkingDeltaEvent{Delta: d.Delta.Thinking}`；`executor.go drainStream` 加 `case llm.ThinkingDeltaEvent: d.Emit(event.ThinkingDeltaEvent{...})`
- [x] **FR-15** `cmd/app/main.go` 构造 `acp.NewLoop(a)` + `acp.NewSteerControl(a)`；`a.AttachMessageIDSrc(loop.CurrentMessageID)`；`sub := a.Subscribe(64)` 后 `ledger.AttachSubscription(sub)`；shutdown 4 步序列（GS Shutdown → `a.Abort` → `sub.Unsubscribe` → `sqliteStore.Close`）
- [x] **测试** `internal/acp/loop_test.go` 端到端 3 个：`TestLoopEnd2End`（mock provider 注入 + 验 text_delta + done + agent_end 顺序）/ `TestLoopAbort`（abort 后无新 text_delta）/ `TestLoopPromptErrAgentBusy`（并发 Prompt 第二次返 ErrAgentBusy）
- [x] **回归** `go test ./... -race` 全绿（EventCommon 嵌入 + 12 emit 改 + handler 签名改 后旧 fixture 不破坏）

### 验收（v2 spec §7 全项）

- [x] `go build ./...` / `go vet ./...` / `gofmt -l .` 干净
- [x] `go test ./... -race` 全绿（含 S2 store 回归 + S3 gateway 回归 + S4 新加 5 套测试）
- [x] 启动后 stdout 唯一一行 `<port>NNNNN</port>`，stderr 含 `agent initialized` + `gateway listening`
- [x] `wscat -c ws://localhost:NNNNN/ws` 连上（**带 `/ws`**）
- [x] `agent.prompt {content:"hi"}` 立即返 `{sessionId, messageId}`（21 字符）
- [x] `agent.prompt {sessionId:"nonexistent"}` 返 `-32602 "session not active"`
- [x] `agent.subscribe_events` 用真 sessionId → `{subscribed:true}`
- [x] subscribe 后 ≤ 1s 收到 notification 链：`text_delta` (mock) + `done`（来自 `LLMEndEvent` 映射）+ `agent_end`
- [x] `agent.abort` 在流式期间 → 后续 notification 含 `done`（`finishReason=aborted`）+ `agent_end`
- [x] `agent.steer` 走 `h.Steer.Steer`（v0 调 `agent.Steer`）
- [x] `kill -TERM <pid>` 走完 4 步 shutdown（GS Shutdown → Agent.Abort → sub.Unsubscribe → store.Close），stderr 末条 `graceful shutdown complete`，整体 ≤ 3s
- [x] `kill -INT <pid>` 同上
- [x] 优雅关闭期间新 WS 连接被拒（`http.Server.Shutdown` 行为）
- [x] 优雅关闭后 `sessions.db` 不损坏（重启可 Load 之前 Save 的 session）
- [x] `lsof -p <pid>` 验证：进程退出后无残留 fd
- [x] EventCommon 各字段被 executor + dispatcher 填（snapshot check：注入 mock Deps 返固定 MessageID，断言 emit 出事件 `ev.Common().MessageID == expected`）—— 见 `executor/executor_test.go::TestEventCommonSnapshot{,_AcrossTurns}`
- [x] `done` notification 的 `messageId` 来自 `LLMEndEvent.Common().MessageID`（**不是** SessionManager.CreateOrGet 的 msgID）—— 见 `gateway/eventledger_test.go::TestMapEventToTSCarriesMessageID`
- [x] `thinking_delta` 解析：anthropic stream 单测覆盖 `content_block_delta.type == "thinking_delta"` 分支 —— 见 `anthropic/stream_test.go::TestDispatch_ThinkingDelta`

### 实现偏差（spec v2 §9 摘要）

落地过程中发现 spec 有 7 处需修订，全部已在 v2 spec §9 落地修正，CHECKLIST 同步引用：

1. **`CreateOrGet("")` 必须返 `DefaultSessionID`**（spec 自相矛盾，否则 notification 永远投不到）。
2. **`NewSessionManager()` 构造时即注册 default session**（subscribe-before-prompt 消除竞态）。
3. **`Loop.Prompt` 失败时不清 `curMsg`**（保留 in-flight run 的 messageID，避免气泡卡住）。
4. **`mapEventToTS(AgentErrorEvent)` 字段名对齐 TS 契约**：`{type:"error", messageId, message}`。
5. **`eventBase` 需导出为 `EventBase`**（Go 字段提升不穿透双层嵌入 + 跨 package 字面量无法用未导出名）。
6. **`acp` 测试不能 import `gateway`**（import cycle；改为 drain `sub.C()` 直接断言 + eventledger 测试覆盖路由）。
7. 未处理 follow-up：`tool_start` / `tool_end` 字段名、`EmitStub` 删除、`acp.Queue` 调用方（留待 S6 / ACP QueueManager spec）。

> **v1 spec 状态**：作废，仅历史参考。差异详见 v2 spec §0。
>
> **S6 待办**（已从 S4 清单移出）：
> - `onRunEnd` 调 `store.SaveSession` + `store.SaveMessage` 落 messages 表
> - 多 Agent 多 session 架构（v0 限定只跑 `default`）
> - 端到端重启可见
> - `agent.list_sessions` / `agent.get_messages` RPC
> - `src/shared/darvin-api.ts` 扩 `done.usage` / `done.finishReason` 字段

---

## S5 · electron-runtime-client（Phase 3 Electron）

- [x] `package.json` 加 `ws ^8.18`
- [x] `src/main/runtime/manager.ts` RuntimeMgr class：spawn + stdout port 解析（5s 超时）+ stop（SIGTERM 4s 兜底 SIGKILL）
- [x] `src/main/runtime/client.ts` AgentClient class：WS + JSON-RPC promise-id-mux + onEvent fanout
- [x] `src/preload/index.ts` contextBridge 真接 `ipcRenderer.invoke / on`（签名同 S1，`status()` 改 async）
- [x] `src/main/index.ts` bootstrap 时序：whenReady → mgr.start → client.connect → createWindow
- [x] `src/main/index.ts` ipcMain.handle 转发 prompt / abort / status
- [x] `src/main/index.ts` 客户端 onEvent → webContents.send('darvin:event', ev) 全 window 推
- [x] `src/main/index.ts` `before-quit` graceful shutdown（disconnect + mgr.stop）
- [x] runtime status badge（online / offline / no-binary）→ 实际落在 `components/runtime/RuntimeStatusBadge.vue`，由 `components/chat/ChatHeader.vue` 内嵌（v2 spec §9.1）
- [x] 删除 `src/renderer/services/mock-agent.ts`
- [x] 验收（部分）：`npm start` 端到端起链路通（prestart build → port 解析 → WS connect → subscribe_events）；SIGTERM 后子进程 `graceful shutdown complete` + exit 0（smoke 实测 4ms）
- [ ] 验收（未完成）：真接 Anthropic 流 + UI 流式显示 —— 本机无 LLM 凭据，prompt 走到 `error` 事件即终止；`text_delta` / `done` happy path 待有 key 环境复测（详见 v2 spec §9.9）

**S5 落地新增偏差**（详见 v2 spec §9.5–§9.10）：

1. `vite.main.config.ts` 未 external node 内置模块 —— 潜在 bug，S5 前靠 default import 侥幸没炸，改 named import 后暴露；已用 `builtinModules` 全量 external + `ws` external 修掉。
2. dev 期必须注入 `DARVIN_CONFIG`，否则子进程 `failed to load config` 退出（bin/ 与 cwd 都没 config.yaml）。
3. `AgentClient.connect()` 必须补发 `agent.subscribe_events`，spec §FR-1.2 漏了；不发一条 notification 都收不到。
4. Go 的 7 类生命周期事件（`run_start` / `prompt_received` / ...）走 `parseDarvinEvent` 的 null 分支，需静默丢弃而非 warn，否则每轮 prompt 刷 4+ 条日志。


---

## S6 · agent-e2e-integration（Phase 4 E2E）

> v2 已写，spec 路径 `specs/features/agent-e2e-integration/2026-07-30-agent-e2e-integration-design-v2.md`。v1 整段作废，仅历史参考。v2 §0 列了 7 个 P0 + 4 个 P1 修订。

### S6 落地后回填（本节待勾）

- [ ] `scripts/smoke.sh` headless 端到端脚本（FR-7）
- [ ] `scripts/ws-smoke-client.js` Node WS 客户端（FR-7，Node 22+ 用内置 WebSocket）
- [ ] `package.json` 加 `scripts.smoke` / `scripts.e2e` / `scripts.e2e:headed` / devDeps `@playwright/test`
- [ ] `src/darvin-agent/config.yaml` api_key 留空（占位）
- [ ] `src/darvin-agent/internal/config/config.go` Load 加用户级 yaml overlay（FR-5.3）
- [ ] `src/darvin-agent/internal/agent/store/message_store.go` MessageStore interface + SQLiteMessageStore（FR-2.1）
- [ ] `src/darvin-agent/internal/agent/agent.go` 加 msgStore 字段
- [ ] `src/darvin-agent/internal/agent/dispatcher.go` 三处落库 hook（user / assistant / RunEnd；FR-2.2，**不动 acp/loop.go**）
- [ ] `src/darvin-agent/cmd/app/main.go` 注入 MessageStore
- [ ] `src/darvin-agent/internal/gateway/handlers.go` Handler 加 Store/MessageStore；switch 加 list_sessions / get_messages
- [ ] `src/darvin-agent/internal/gateway/eventledger.go` mapEventToTS(LLMEndEvent) 加 usage（FR-4）
- [ ] `src/shared/darvin-api.ts` `done` 扩 `usage?` + DarvinLLMConfig + listSessions/getMessages/getLLMConfig/setLLMConfig
- [ ] `src/main/runtime/client.ts` + listSessions / getMessages / getLLMConfig / setLLMConfig
- [ ] `src/main/index.ts` + ipcMain.handle('darvin:list_sessions' / 'darvin:get_messages' / 'darvin:get_llm_config' / 'darvin:set_llm_config') + writeUserSettingsYAML + restartGoSubprocess
- [ ] `src/preload/index.ts` 替换空 stub + 加 LLM config 方法
- [ ] `src/renderer/composables/useSession.ts` 删 mockSessions 种子（FR-2.3）
- [ ] `src/renderer/services/mock-data.ts` 删 mockSessions / mockMessages；保留 mockModels / expertSuiteAgents
- [ ] `src/renderer/components/settings/SettingsSubNav.vue` 加 'models' section
- [ ] `src/renderer/components/settings/SettingsPanelModels.vue` 新建（FR-5.1）
- [ ] `src/renderer/views/SettingsView.vue` + SettingsPanelModels
- [ ] `src/renderer/components/chat/MessageItem.vue` / `Composer.vue` 加 data-testid
- [ ] `playwright.config.ts` + `e2e/{happy-path,session-persistence,graceful-shutdown}.spec.ts`（FR-6）
- [ ] `README.md` First Run 5 步（含 UI LLM 配置入口）
- [ ] 验收：`npm run smoke` exit 0 ≤10s；`npm run e2e` happy-path skip-on-no-key，其余全过；UI 配置 LLM → restart Go → 真流式响应；session 跨重启可见；graceful shutdown ≤3s + sessions.db integrity ok

---

## 全局收口

- [ ] `go build ./...` 通过
- [ ] `go test ./... -race` 全绿
- [ ] `npm run lint` 通过
- [ ] `npm start` 启动后 UI 可用
- [ ] 6 份 spec 全部 ✓ 验收
