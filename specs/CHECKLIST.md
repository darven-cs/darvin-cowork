# darvin-cowork 任务清单

> 6 阶段 spec 落地跟踪。勾完即完成。

---

## S1 · agent-ui-shell（Phase 1 UI）

- [ ] `package.json` 加 `vue ^3.5` / `@tailwindcss/vite ^4.0`
- [ ] `src/renderer/index.css` 加 `@import "tailwindcss";`
- [ ] `src/renderer/index.ts` 改成 `createApp(App).mount('#app')`
- [ ] `src/renderer/index.html` 留 `<div id="app">`
- [ ] `src/renderer/App.vue` 主组件（ChatHeader / MessageList / InputBar）
- [ ] `src/renderer/components/ChatHeader.vue`
- [ ] `src/renderer/components/MessageList.vue` + `MessageItem.vue` + `StreamingText.vue`
- [ ] `src/renderer/components/InputBar.vue`
- [ ] `src/renderer/services/mock-agent.ts` mock 流式
- [ ] `src/shared/darvin-api.ts` 类型锁定（DarvinEvent union / prompt / abort）
- [ ] `src/preload/index.ts` contextBridge mock 暴露 `window.darvin.{prompt,abort,onEvent}`
- [ ] 验收：`npm start` 显示 UI；DevTools `await window.darvin.prompt('ping')` 返回 mock

---

## S2 · agent-sessions-store（Phase 2 Go #1）

- [ ] `go.mod` 加 `gorm.io/gorm` / `gorm.io/driver/sqlite` / `github.com/jaevor/go-nanoid`
- [ ] `internal/agent/store/models.go` 4 模型：Session / Message / CompactionCheckpoint / SkillSnapshot
- [ ] `internal/agent/store/sqlite.go` SQLiteStore 实现 4 模型 CRUD
- [ ] `internal/agent/store/store.go` 已有的 SessionStore interface 补齐方法
- [ ] `internal/agent/store/memory.go` 保留作 fallback / 测试用
- [ ] `internal/config/config.go` 加 `sessions_dsn` 字段
- [ ] `configs/config.example.yaml` 加 `database.sessions_dsn`
- [ ] `cmd/app/main.go` 接 SQLiteStore + AutoMigrate
- [ ] `internal/agent/store/store_test.go` / `sqlite_test.go` 全绿
- [ ] 验收：`go test ./internal/agent/store/...` 通过；sessions.db 表结构正确

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
