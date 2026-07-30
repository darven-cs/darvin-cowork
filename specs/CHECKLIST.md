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

- [ ] `go.mod` 加 `github.com/gorilla/websocket`
- [ ] `internal/gateway/server.go` WS server + `<port>...</port>` stdout 输出
- [ ] `internal/gateway/jsonrpc.go` JSON-RPC 2.0 envelope（Request / Response / Notification / RPCError）
- [ ] `internal/gateway/client.go` WS connection lifecycle + ping-pong + batch dispatch
- [ ] `internal/gateway/handlers.go` 3 个 stub：agent.prompt / abort / subscribe_events
- [ ] `internal/gateway/sessionmgr.go` SessionManager（nanoid 21 字符 / CreateOrGet / Callbacks）
- [ ] `internal/gateway/eventledger.go` EventLedger stub（EmitStub fake event）
- [ ] `internal/gateway/server_test.go` / `jsonrpc_test.go` / `sessionmgr_test.go` / `eventledger_test.go`
- [ ] `cmd/app/main.go` 接入 Gateway（但 `select{}` 占位，不做优雅关闭）
- [ ] 验收：`go test ./internal/gateway/... -race` 全绿；启动后 stdout 唯一一行 `<port>NNNNN</port>`；wscat 调 prompt 收到 `{sessionId, messageId}`

---

## S4 · agent-acp-loop（Phase 2 Go #3）

- [ ] `internal/acp/loop.go` Loop：接 prompt handler → Agent.Prompt/Run
- [ ] `internal/acp/queue.go` 接入现有 `internal/agent/queue` 3 通道
- [ ] `internal/acp/steer.go` SteerControl interface + v0 no-op
- [ ] `internal/agent/event/event.go` 增 EventCommon（SessionID / MessageID）+ 各事件嵌入
- [ ] `internal/gateway/eventledger.go` 实装 AttachBus + Go↔TS event 映射表
- [ ] `internal/gateway/handlers.go` prompt 真接 acp.Loop.Prompt（替换 stub）
- [ ] `cmd/app/main.go` `signal.NotifyContext(SIGTERM, SIGINT, SIGQUIT)` 优雅关闭
- [ ] `internal/acp/loop_test.go` 端到端单测
- [ ] 验收：发 prompt → WS 推 text_delta + done 流；SIGTERM ≤3s 走完 6 步（Abort → flush → WS shutdown → DB close → exit 0）；stderr "graceful shutdown complete"

---

## S5 · electron-runtime-client（Phase 3 Electron）

- [ ] `package.json` 加 `ws ^8.18`
- [ ] `src/main/runtime/manager.ts` RuntimeMgr class：spawn + stdout port 解析（5s 超时）+ stop（SIGTERM 4s 兜底 SIGKILL）
- [ ] `src/main/runtime/client.ts` AgentClient class：WS + JSON-RPC promise-id-mux + onEvent fanout
- [ ] `src/preload/index.ts` contextBridge 真接 `ipcRenderer.invoke / on`（签名同 S1）
- [ ] `src/main/index.ts` bootstrap 时序：whenReady → mgr.start → client.connect → createWindow
- [ ] `src/main/index.ts` ipcMain.handle 转发 prompt / abort / status
- [ ] `src/main/index.ts` 客户端 onEvent → webContents.send('darvin:event', ev) 全 window 推
- [ ] `src/main/index.ts` `before-quit` graceful shutdown（disconnect + mgr.stop）
- [ ] `src/renderer/components/ChatHeader.vue` runtime status badge（online / offline / no-binary）
- [ ] 删除 `src/renderer/services/mock-agent.ts`
- [ ] 验收：`npm start` 启动后 DevTools `window.darvin.prompt('ping')` 真接 Anthropic 流；UI 流式显示；关 Electron 主进程 3s 内子进程 graceful shutdown

---

## S6 · agent-e2e-integration（Phase 4 E2E）

- [ ] `scripts/smoke.sh` headless 端到端脚本
- [ ] `scripts/ws-smoke-client.js` Node WS 客户端（Node 22+ 用内置 WebSocket）
- [ ] `package.json` 加 `scripts.smoke`
- [ ] `src/darvin-agent/configs/config.example.yaml` 加 `llm.anthropic_api_key` 模板
- [ ] `src/darvin-agent/.gitignore` 加 `configs/config.yaml`
- [ ] `.gitignore` 加 `bin/` / `sessions.db`
- [ ] `src/darvin-agent/internal/acp/loop.go` onRunEnd 调 SaveSession + SaveMessage
- [ ] `src/darvin-agent/internal/gateway/handlers.go` 增 `agent.list_sessions` / `agent.get_messages` dispatch
- [ ] `src/darvin-agent/internal/agent/store/sqlite.go` 实装 ListSessions / ListMessages
- [ ] `src/shared/darvin-api.ts` 补 listSessions / getMessages 签名 + DarvinSession / DarvinMessage 类型
- [ ] `src/preload/index.ts` 增 listSessions / getMessages IPC
- [ ] `src/main/runtime/client.ts` 增 listSessions / getMessages 方法
- [ ] `src/main/index.ts` 增 ipcMain.handle('darvin:list_sessions' / 'darvin:get_messages')
- [ ] `src/renderer/composables/useSessionHistory.ts` onMounted load history
- [ ] `src/renderer/components/MessageList.vue` 初始化调 loadLatest
- [ ] `README.md` "First Run" 5 步指南 + Troubleshooting
- [ ] 验收：`npm run smoke` exit 0 ≤10s；重 UI 端到端（场景 1-3 §2）；session 跨重启可见；graceful shutdown ≤3s + sessions.db integrity ok

---

## 全局收口

- [ ] `go build ./...` 通过
- [ ] `go test ./... -race` 全绿
- [ ] `npm run lint` 通过
- [ ] `npm start` 启动后 UI 可用
- [ ] 6 份 spec 全部 ✓ 验收
