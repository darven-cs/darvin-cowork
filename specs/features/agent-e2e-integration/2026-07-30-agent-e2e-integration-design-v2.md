# Agent E2E Integration + UI LLM 配置 + Playwright e2e 设计文档 v2（S6）

> **Phase 4 / 6 — 端到端收口**。把 S1-S5 串起来：三层端到端 happy path（Electron → Go WS → Anthropic → event.Bus → WS notification → UI 流式渲染）+ session 持久化跨重启可见 + 优雅关闭链路 + **新增** UI 配置 LLM（不需手改 yaml）+ **新增** Playwright CLI 端到端测试。
>
> 前置：S1-S5 全部完成且独立验收通过。
>
> 本 spec 是整个 6 阶段计划的**收口** — 完成后 demo 可走通 + e2e 可自动回归。

---

## 0. 相对 v1 的修订清单

### P0（硬冲突：v1 与当前代码或 S5 实装直接矛盾，落地即报错）

1. **LLM 配置 key 写错**：v1 §FR-1 写 `llm.anthropic_api_key`，实际 `internal/config/config.go:39` 的字段是 `api_key`（mapstructure tag）。`viper.AutomaticEnv()` 把 `LLM_API_KEY` env var 绑定到这个 key；不是 `ANTHROPIC_API_KEY`。
2. **viper 不做 `${VAR}` 替换**：v1 §FR-1 写的 `anthropic_api_key: ${ANTHROPIC_API_KEY}` 是想当然。真实机制是：要么 yaml 里直接写 key，要么 export `LLM_API_KEY` 让 `AutomaticEnv` 覆盖。S6 不再承诺 `${VAR}` 语法，只承诺 `LLM_API_KEY` env var 自动生效。
3. **messages 落库 hook 点写错**：v1 §4.4 说在 `internal/acp/loop.go onRunEnd` 落库。**真实 hook 点是 `internal/agent/dispatcher.go`**：
   - L115 `a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg.Content})` → user message 入库点
   - `RunConversation` 返回后（第 ~135 行附近）→ assistant message 累积点
   - L140 `bus.Emit(event.RunEndEvent{...})` → run 结束点
   S6 落库逻辑挂这三个位置，**不动** acp/loop.go。
4. **dispatch 签名错**：v1 §4.4 写 `dispatch(ctx, req, sessions, ledger, store)`。真实签名是 `dispatchRequest(ctx, req, c *client, h *Handler)`；`Store` 应作为字段加到 `Handler` struct（`handlers.go:62-67`）。
5. **S2 没实现 MessageStore**：v1 §6 写"S2 已实现 ListSessions / ListMessages，S6 SQLiteStore 补实现"。**事实**：S2 只定义了 `SessionStore` 接口（4 方法：`Save/Load/List/Delete`，对象是 `*session.Session`），**messages 表至今零 CRUD 路径**。S6 必须新增 `MessageStore` 接口 + `SQLiteMessageStore` 实现。
6. **`done.usage` 在 S5 union 不存在**：v1 §7.1 断言 `done.usage.totalTokens > 0`，但 S5 落地的 `DarvinEvent` `done` 变体是 `{ type: 'done'; messageId }`，**没 usage 字段**。要么扩 union（轻微契约改动），要么放宽断言。S6 选**扩 union**（smoke 真要验 Anthropic 报回的 token 计数，否则 happy path 没有任何 token 验证手段）。
7. **`useSession.ts` 还在 mock 种子**：v1 §FR-7 只说删 `mock-agent.ts`（S5 已删），但 `src/renderer/services/mock-data.ts` 里的 `mockSessions` 仍被 `src/renderer/composables/useSession.ts:3,11` 引用，**让 AppShell 拿到的真 sessions 被遮盖**。S6 必清。

### P1（设计选择：需审过）

1. **UI LLM 配置落盘 = 用户级 yaml**：`~/.config/darvin-cowork/settings.yaml`（Linux 走 `$XDG_CONFIG_HOME`，macOS 走 `~/Library/Application Support/darvin-cowork/settings.yaml`，Windows 走 `%APPDATA%/darvin-cowork/settings.yaml`）。Go 启动时若该文件存在则 overlay 在 bundled `config.yaml` 之上；UI 保存 = main 写文件 + restart Go 子进程（RuntimeMgr.stop → start → client.connect），Electron 窗口不关。
2. **Playwright e2e 与 headless smoke 并存**：smoke 给 CI/无人值守（不动）；Playwright 给 UI 集成 + UI 配置 LLM 的验证（新增）。
3. **Playwright e2e 跑真 Go + 真 Anthropic**：1 条 happy path（key 缺失时 skip，不 fail CI）+ 几条不依赖网络的 UI 路径（settings UI、graceful shutdown、session persistence）。
4. **mock-data.ts 拆分会话/消息部分**：`mockSessions` + `mockMessages` 移到删除路径；`expertSuiteAgents` / `mockModels` 不在 S6 scope，留给后续 spec。

### 已知非问题（v1 描述正确，v2 仅微调）

- 3 层联调 happy path 仍按 v1 §场景 1 描述
- session 持久化跨重启场景仍按 v1 §场景 2 描述
- graceful shutdown 链路场景仍按 v1 §场景 3、4 描述
- README first run 5 步仍按 v1 §FR-5 描述

---

## 1. 概述

### 1.1 实际 gap（基于当前源码 + S5 实装）

| 阶段 | 范畴 | 验收手段 | 状态 |
|------|------|---------|------|
| S1 | UI shell + 契约 | 浏览器 DevTools `await window.darvin.prompt('ping')` | ✓ |
| S2 | sessions.db schema + GORM | `go test ./internal/store/...` | ✓（仅 metadata） |
| S3 | Gateway WS + JSON-RPC | `wscat -c ws://localhost:NNNNN` | ✓ |
| S4 | ACP + Agent.Run + 优雅关闭 | Go 独立跑 + SIGTERM | ✓（未持久化 messages） |
| S5 | Electron RuntimeMgr + AgentClient | `npm start` + DevTools | ✓ |

**剩 5 件事**：

1. **messages 持久化**：dispatcher.go 在 user message append + RunEnd 时调 `store.SaveMessage(...)`，SQLite 落 `messages` 表
2. **sessions.list / messages.read RPC**：`agent.list_sessions` / `agent.get_messages` 在 gateway dispatch 表注册；preload / client / ipcMain 链路接通
3. **renderer history 加载**：AppShell 启动时 `listSessions` + `getMessages(currentSession)` 替代 mock 种子
4. **UI 配置 LLM**：新增 Settings → Models 段；用户级 yaml 持久化；保存重启 Go 子进程
5. **Playwright CLI 端到端**：happy path 走 UI 流式渲染；session persistence 跨重启；graceful shutdown 验证

外加：

6. `npm run smoke` headless 脚本（按 v1 §FR-4/6 重写，补 DB integrity check + usage 断言）
7. README "first run"（按 v1 §FR-5 重写，更新为 UI 配置 LLM 入口）
8. `mock-data.ts` 会话/消息部分清除

### 1.2 目标

- 端到端 happy path 走通：UI 输入 prompt → 流式收到真实 LLM 响应（Anthropic）
- session 持久化：用户关 UI 重启后，上一轮对话在 message list 可见
- 优雅关闭链路：Kill Electron 主进程 → 子进程 ≤ 3s 退出 + `graceful shutdown complete` + `sessions.db` integrity ok
- UI 可配置 LLM（API key / provider / base_url / model）：不需要手改 yaml
- `npm run smoke`：headless 验证 WS + prompt + 收 event 流 + DB integrity
- `npm run e2e`：Playwright CLI 走真实 UI（含 UI 配置 LLM 路径）

### 1.3 非目标

- **不**做 Anthropic 之外的 LLM 接入（M2 spec）
- **不**做 Memory / Dreaming / Skills / MCP（后续 spec）
- **不**做生产打包（packaging / signing / notarization 远期）
- **不**做多 session 切换 UI（v0 限定只跑 default session；切 UI 后续 spec）
- **不**做 hot reload LLM 配置（保存即重启 Go 子进程；Electron 不关）
- **不**改 S1-S5 已锁定的内部接口（仅补 list_sessions / get_messages 两个新 method + 扩 `done.usage`）

### 1.4 前置依赖（S5 实装 API 表面）

| 接口 | 现状 |
|------|------|
| `DarvinApi.listSessions/getMessages` | ✓ 类型已定义；preload 返空 stub |
| `DarvinEvent.done` | `{ type: 'done'; messageId }`（v2 扩 `usage?` 字段） |
| `RuntimeMgr.start/stop` | ✓ S5 已实装，可重复调用 |
| `AgentClient.request<T>` | ✓ 通用 JSON-RPC 入口 |
| `gateway.Handler` | `{ Sessions, Ledger, Loop, Steer }`（v2 加 `Store`） |
| `agent.Agent.store` | `SessionStore`（v2 加 `MessageStore`） |

---

## 2. 用户场景

### 场景 1：完整 happy path（Electron UI → Anthropic）

**Given** S1-S5 全部完成，UI Settings → Models 已配 API key
**When** `npm start` 启动 Electron
**Then** 序列：
1. ◯ 主进程 spawn 子进程，stdout 解析 port
2. ◯ AgentClient 连 WS，subscribe_events
3. ◯ UI 启动，header 显示 "Runtime: ready"
4. ◯ AppShell onMounted → `listSessions()` 拿到 sessions；currentSessionId 切到最近一个
5. ◯ `getMessages(default)` 拿到 messages list 填充 MessageList
6. ◯ 用户输入 "ping" send
7. ◯ main 进程 WS 推 `agent.prompt` 到 Go
8. ◯ dispatcher.go 收 user message → 追加到 in-memory session → `store.SaveMessage(userMsg)`（NEW）
9. ◯ Agent.Prompt → Run → LLM StartRequest → Anthropic API
10. ◯ Anthropic 流式返回 text_delta → event.Bus emit → EventLedger.AttachBus 推 WS notification
11. ◯ AgentClient 收到 → 推 `darvin:event` 到 renderer
12. ◯ renderer MessageList reactive 追加 delta
13. ◯ Anthropic 完成 → RunEnd → dispatcher.go 累积 assistant message → `store.SaveMessage(assistantMsg)`（NEW）→ `store.SaveSession(sess)`（NEW）→ emit RunEndEvent
14. ◯ `done(usage)` event 推 UI → 消息定型 + 显示 token 数
15. ◯ UI 助记 "ping 的回复" + token 计数

### 场景 2：session 持久化跨重启

**Given** 场景 1 已跑通（user + assistant messages 已落 `messages` 表）
**When** 关 Electron（kill 主进程）→ 等 ≤ 3s → 重启 `npm start`
**Then**：
- ◯ UI 启动后，AppShell onMounted → `listSessions()` 拿到最近一个 session
- ◯ `getMessages(default)` 拿到 user + assistant 完整文本
- ◯ MessageList 直接渲染历史消息
- ◯ 用户能接着发新 prompt（继续 messageId 累加，sessionId 仍是 default）

### 场景 3：UI 配置 LLM

**Given** 首次启动，UI header 显示 "Runtime: no binary" 或 API key 未配
**When** 用户点 Settings → Models 段，输入 API key → 保存
**Then**：
1. ◯ UI 弹 "正在应用…" spinner
2. ◯ IPC `darvin:set_llm_config` → main 写 `~/.config/darvin-cowork/settings.yaml`（XDG-aware）
3. ◯ main `mgr.stop()` → SIGTERM Go → 等 ≤ 4s
4. ◯ main `mgr.start()` → spawn 新 Go → port 解析
5. ◯ main `client.connect(port)` → 新 WS + subscribe_events
6. ◯ RuntimeStatusBadge 短暂 amber → green
7. ◯ UI 弹 "已应用"
8. ◯ 用户发 prompt → 真实 LLM 响应

### 场景 4：graceful shutdown 链路

**Given** 子进程在 listen，Agent.Run 没在跑
**When** `kill <electron-main-pid>`（模拟 OS 关 app）
**Then** 链路：
1. ◯ Electron `before-quit` 触发
2. ◯ `client.disconnect()` → WS close
3. ◯ `mgr.stop()` → SIGTERM → darvin-agent
4. ◯ darvin-agent `signal.NotifyContext` cancel
5. ◯ S4 shutdown 序列执行（Abort → flush → WS server.Shutdown → DB close → os.Exit(0)）
6. ◯ stderr 最后一行 "graceful shutdown complete"
7. ◯ total ≤ 3s
8. ◯ `sessions.db` integrity ok

### 场景 5：Playwright happy path（真 Go + 真 Anthropic）

**Given** `ANTHROPIC_API_KEY` env var 已设；`npm run build:agent` 已跑
**When** `npm run e2e`（Playwright CLI 启动 Electron）
**Then** 脚本（playwright.config.ts + e2e/happy-path.spec.ts）：
1. ◯ Playwright `_electron.launch({ args: ['.vite/build/main/index.js'] })` 起 Electron
2. ◯ 第一窗口：导航到 Settings → Models → 输入 `process.env.ANTHROPIC_API_KEY` → 保存 → 等 spinner 消失
3. ◯ 导航回 ChatView → 等 "Runtime: ready"
4. ◯ 输入 "ping" → send → 等 ≤ 3s 内第一条 text_delta 出现
5. ◯ 等 ≤ 10s 内 `done` event（含 usage）
6. ◯ 验证 message bubble 包含响应文本 + token 数 > 0
7. ◯ 关闭 Electron → 验证 `pgrep darvin-agent` 无残留
8. ◯ **key 缺失时**：spec.skip()，不 fail CI

### 场景 6：Playwright session persistence（不依赖 LLM）

**Given** UI 上一次对话已落 messages 表
**When** 启动 Playwright，重新打开 Electron
**Then**：
- ◯ MessageList 直接渲染上次的 user + assistant message
- ◯ 不需要发新 prompt

### 场景 7：headless smoke（CI 友好，不依赖 UI）

**Given** S1-S5 已完成
**When** `npm run smoke`（在仓库根）
**Then** 脚本（按 v1 §FR-4.1）：
1. 启动 darvin-agent 子进程
2. 解析 stdout port
3. WS connect + subscribe_events
4. 发 `agent.prompt`("ping")
5. 收至少 1 条 text_delta
6. 收 done event 且 `usage.totalTokens > 0`
7. SIGTERM child
8. `sessions.db` integrity ok
9. exit 0
10. 整个过程 ≤ 10s

### 场景 8：Anthropic API 不可达

**Given** Internet 断开或 API key 错
**When** 用户发 prompt
**Then**：
- ◯ Agent.Run 失败 → `error` event type → renderer 显示 error toast
- ◯ UI 仍可继续输入（不卡死）
- ◯ sessions.db 仍记录 error message
- ◯ 不污染 sessions.db integrity

---

## 3. 功能需求

### FR-1：LLM 配置（修订）

#### FR-1.1 bundled config（保持现状）

`src/darvin-agent/config.yaml` 维持当前结构，`llm.api_key` 字段为占位：

```yaml
llm:
  provider: anthropic
  api_key: ""                       # 默认空；占位不写真 key
  base_url: ""
  model: claude-sonnet-4-5
```

#### FR-1.2 用户级 override（新增）

- 路径（OS-aware）：
  - Linux: `$XDG_CONFIG_HOME/darvin-cowork/settings.yaml`（默认 `~/.config/darvin-cowork/settings.yaml`）
  - macOS: `~/Library/Application Support/darvin-cowork/settings.yaml`
  - Windows: `%APPDATA%/darvin-cowork/settings.yaml`
- 文件不存在 = 无 override；bundled config 全生效
- 文件存在 = overlay：`llm.api_key` / `llm.base_url` / `llm.provider` 字段按 key 覆盖 bundled
- 文件格式：
  ```yaml
  llm:
    provider: anthropic
    api_key: sk-ant-...
    base_url: ""
    model: claude-sonnet-4-5
  ```
- 用户级文件**不进 git**（家目录本来就不跟踪）；`README.md` 注明位置

#### FR-1.3 `LLM_API_KEY` env var（保持现状）

- `viper.AutomaticEnv()` 已绑定 `LLM_API_KEY` → `llm.api_key`
- 优先级：用户级 yaml > env var > bundled yaml
- 真实 key 不入 git；`config.yaml` 留空 / 占位

### FR-2：messages 持久化（修订 hook 点）

#### FR-2.1 Go 端 `MessageStore` + SQLite 实现（NEW）

新增 `internal/agent/store/message_store.go`：

```go
type MessageStore interface {
    Save(ctx context.Context, m *MessageRecord) error
    List(ctx context.Context, sessionID string, limit, offset int) ([]MessageRecord, error)
    Count(ctx context.Context, sessionID string) (int, error)
}

type MessageRecord struct {
    ID         string
    SessionID  string
    Role       string
    Content    string
    ToolCalls  string  // JSON
    Timestamp  int64
    StopReason string
    ParentID   string
}

// SQLiteMessageStore wraps *gorm.DB; same lifecycle as SQLiteStore.
type SQLiteMessageStore struct { db *gorm.DB }
```

注：`store.Message` struct（`models.go:23`）已是 GORM row 表，新增 MessageStore 的入参是 `MessageRecord`，Save 时映射到 `store.Message`。

#### FR-2.2 dispatcher.go 落库 hook（NEW / 修订 hook 点）

`internal/agent/dispatcher.go` 三处改：

```go
// (1) user message 落库
a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg.Content})
if a.msgStore != nil {                       // 新字段
    _ = a.msgStore.Save(ctx, &store.MessageRecord{
        ID: runMsgID, SessionID: a.session.ID,
        Role: "user", Content: msg.Content,
        Timestamp: time.Now().UnixMilli(), ParentID: "",
    })
}

// (2) RunConversation 返回后,累加 assistant message
turnsThisRun := a.approxTurns(turnsBefore)
for _, m := range a.session.Messages() {     // 新增消息 = turnsThisRun 个 assistant
    if a.msgStore != nil {
        _ = a.msgStore.Save(ctx, &store.MessageRecord{
            ID: runMsgID, SessionID: a.session.ID,
            Role: "assistant", Content: m.Content, ...
        })
    }
}

// (3) RunEndEvent 之前落 session 元数据
if a.store != nil { _ = a.store.Save(ctx, a.session) }
a.bus.Emit(event.RunEndEvent{...})
```

**不动 `internal/acp/loop.go`**。

#### FR-2.3 UI 端 history 加载（修订）

`src/renderer/layout/AppShell.vue` S5 已接 `listSessions` + `getMessages`，但被 `useSession` 的 mock 种子遮盖。S6 改 `src/renderer/composables/useSession.ts`：

```ts
// useSession.ts: S6 remove
import { mockSessions } from '../services/mock-data';
// → const sessions = ref<DarvinSession[]>([]);
```

这样 AppShell onMounted `listSessions()` 拿到的真会话不会被 mock 覆盖。

### FR-2.4 删 mock-data 会话/消息部分（NEW）

- `src/renderer/services/mock-data.ts` 删除 `mockSessions` + `mockMessages` + `DarvinSession` mock 数据源
- `mockModels` + `expertSuiteAgents` 保留（属 expert suite 范畴，不在 S6）
- grep 验证无引用：`grep -rn "mock-data" src/` 应只剩 expert suite 的引用

### FR-3：list_sessions / get_messages RPC（修订）

#### FR-3.1 Go handler dispatch（修订）

`internal/gateway/handlers.go`：

```go
type Handler struct {
    Sessions     *SessionManager
    Ledger       *EventLedger
    Loop         *acp.Loop
    Steer        acp.SteerControl
    SessionStore store.SessionStore    // 新字段
    MessageStore store.MessageStore    // 新字段
}
```

`dispatchRequest` switch 加 2 case：

```go
case "agent.list_sessions":
    return handleListSessions(ctx, req.ID, h.SessionStore)
case "agent.get_messages":
    return handleGetMessages(ctx, req.ID, req.Params, h.MessageStore)
```

`handleListSessions` / `handleGetMessages` 返回格式按 `DarvinListSessionsResponse` / `DarvinGetMessagesResponse`（`src/shared/darvin-api.ts` 已定义）。

#### FR-3.2 主进程 + preload + client 链路

- `src/main/runtime/client.ts`：`listSessions()` + `getMessages(sessionId)`（参考已存在的 `prompt/abort`）
- `src/main/index.ts`：2 个 `ipcMain.handle` 转发
- `src/preload/index.ts`：替换返空 stub 为真 invoke

### FR-4：done event 扩 usage 字段（修订）

`src/shared/darvin-api.ts`：

```ts
| { type: 'done'; messageId: string; usage?: { totalTokens: number; inputTokens?: number; outputTokens?: number } }
```

`internal/gateway/eventledger.go` `mapEventToTS(LLMEndEvent)` 加 `usage` 字段：

```go
case event.LLMEndEvent:
    u := e.Usage
    out := map[string]any{
        "type": "done", "messageId": ev.Common().MessageID,
    }
    if u.TotalTokens > 0 || u.InputTokens > 0 || u.OutputTokens > 0 {
        out["usage"] = map[string]any{
            "totalTokens":  u.TotalTokens,
            "inputTokens":  u.InputTokens,
            "outputTokens": u.OutputTokens,
        }
    }
    return out
```

`llm.Usage` 字段确认存在（spec 已查：`agent.go:267-269` 有 `LastUsage() llm.Usage`）。

### FR-5：UI 配置 LLM（新增）

#### FR-5.1 Settings → Models 段

`src/renderer/components/settings/SettingsSubNav.vue` 加 `'models'` section id：

```ts
const items = [
  { id: 'account', label: '账户' },
  { id: 'models', label: '模型 / API Key' },     // NEW
  { id: 'appearance', label: '外观' },
  { id: 'shortcuts', label: '快捷键' },
  { id: 'about', label: '关于' },
];
```

新建 `src/renderer/components/settings/SettingsPanelModels.vue`：

- 字段：Provider (select: anthropic)、API Key (input type=password，显示 ●●●●，可切换可见)、Base URL (input)、Model (input 或 select: claude-sonnet-4-5 / claude-opus-4-5)
- 按钮：保存（disabled 当某字段 dirty 中）
- 加载：onMounted `window.darvin.getLLMConfig()` 取当前配置（masked）
- 保存：`window.darvin.setLLMConfig({...})` → 等响应 → 弹 toast

#### FR-5.2 IPC + main 写文件

`src/shared/darvin-api.ts`：

```ts
export interface DarvinLLMConfig {
  provider: string;
  apiKey: string;          // 序列化时 mask 掉
  baseURL: string;
  model: string;
}

export interface DarvinApi {
  // ... S5 已有
  getLLMConfig(): Promise<DarvinLLMConfig>;
  setLLMConfig(req: DarvinLLMConfig): Promise<{ applied: boolean }>;
}
```

`src/main/index.ts`：

```ts
ipcMain.handle('darvin:get_llm_config', () => readUserSettingsYAML());
ipcMain.handle('darvin:set_llm_config', async (_e, cfg) => {
  writeUserSettingsYAML(cfg);
  await restartGoSubprocess();   // mgr.stop → start → client.connect
  return { applied: true };
});
```

`writeUserSettingsYAML` 写到 OS-aware 路径（`os.UserConfigDir()` + `darvin-cowork/settings.yaml`），目录不存在则 mkdir。

#### FR-5.3 Go 端读用户级 yaml overlay

`src/darvin-agent/internal/config/config.go` `Load` 末尾加：

```go
// Overlay: ~/.config/darvin-cowork/settings.yaml
if overlayPath, ok := overlayConfigPath(); ok {
    viper.SetConfigFile(overlayPath)
    if err := viper.MergeInConfig(); err == nil {
        if err := viper.Unmarshal(&cfg); err != nil {
            return nil, err
        }
    }
}
```

`overlayConfigPath()` 返回 OS-aware 路径，文件不存在返 `("", false)`。

### FR-6：Playwright e2e（新增）

#### FR-6.1 依赖 + 配置

- `package.json` devDeps：加 `@playwright/test ^1.55`
- `package.json` scripts：
  ```json
  {
    "e2e": "playwright test",
    "e2e:headed": "playwright test --headed",
    "e2e:install": "playwright install chromium"
  }
  ```
- 新建 `playwright.config.ts`：
  ```ts
  export default defineConfig({
    testDir: './e2e',
    timeout: 30_000,
    use: { trace: 'retain-on-failure' },
    reporter: [['list'], ['html', { open: 'never' }]],
  });
  ```
- `e2e/.gitignore`：`node_modules/`、`test-results/`、`playwright-report/`、`blob-report/`

#### FR-6.2 spec 文件

新建 `e2e/happy-path.spec.ts`：

```ts
import { test, expect, _electron as electron } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';

const ROOT = path.resolve(__dirname, '..');
const HOOK_BIN = path.join(ROOT, 'bin');

test.skip(!process.env.ANTHROPIC_API_KEY, 'ANTHROPIC_API_KEY not set; skipping happy path');

test('UI → real Anthropic happy path', async () => {
  // 1. 起 Electron
  const app = await electron.launch({ args: ['.vite/build/main/index.js'] });
  const win = await app.firstWindow();

  // 2. Settings → Models → 写 API key
  await win.getByRole('button', { name: '设置' }).click();
  await win.getByLabel('API Key').fill(process.env.ANTHROPIC_API_KEY!);
  await win.getByRole('button', { name: '保存' }).click();
  await expect(win.getByText(/已应用|Applying/i)).toBeVisible({ timeout: 15_000 });

  // 3. 回 ChatView
  await win.getByRole('button', { name: '会话' }).first().click();
  await expect(win.getByText(/Runtime: ready/)).toBeVisible({ timeout: 15_000 });

  // 4. 发 prompt
  await win.getByPlaceholder('给 Darvin 发送消息').fill('ping');
  await win.getByRole('button', { name: '发送' }).click();

  // 5. 等响应
  await expect(win.locator('.assistant-bubble').last()).toContainText(/ping|hello/i, { timeout: 15_000 });
  // 验证 token 数
  await expect(win.locator('[data-testid=token-count]').last()).toContainText(/\d+/);

  await app.close();

  // 6. 无残留
  // (Linux 假设) pgrep 不再返 darvin-agent
});
```

注：选择器（`getByPlaceholder` / `[data-testid=token-count]` / 按钮 name）走 i18n 文案，依赖 `services/i18n.ts` 的现有 key。S6 实现时若文案对不上需调整。

#### FR-6.3 spec 文件 (不依赖 LLM)

新建 `e2e/session-persistence.spec.ts`：复用上次对话历史，断言 message 出现。
新建 `e2e/graceful-shutdown.spec.ts`：关窗口，断言 darvin-agent 进程退出。

### FR-7：headless smoke（按 v1 §FR-4/6 重写）

按 v1 §FR-4 完整保留，补：
- `usage.totalTokens > 0` 断言（依赖 FR-4 扩字段）
- `sqlite3 sessions.db "PRAGMA integrity_check"` 输出 `ok` 断言
- 路径修正：`./bin/darvin-agent-$(uname | tr A-Z a-z)-$(uname -m)`

`package.json` scripts 加：`"smoke": "bash scripts/smoke.sh"`

### FR-8：README first run（按 v1 §FR-5 修订）

5 步走通，第 2 步改：

```markdown
2. **配置 Anthropic API key**（二选一）：
   - (推荐) 启动后到 Settings → Models → 输入 API Key → 保存
   - 或环境变量：`export LLM_API_KEY=sk-ant-...`
```

加 Troubleshooting：用户在 UI 改过 API key 后想清除 = `rm ~/.config/darvin-cowork/settings.yaml`。

### FR-9：mock-agent + mock-data 清理（保留 + 扩展）

- S5 已删 `src/renderer/services/mock-agent.ts` ✓
- S6 删 `src/renderer/services/mock-data.ts` 的 `mockSessions` + `mockMessages` + 相关类型
- `mockModels` / `expertSuiteAgents` 保留（属 expert suite 范畴）

---

## 4. 实现方案

### 4.1 目录结构（diff 视角）

```
.
├── bin/                                                # S5 已建
├── docs/系统架构.md                                    # 不动
├── e2e/                                                # 🆕 FR-6
│   ├── .gitignore                                      # 🆕
│   ├── happy-path.spec.ts                              # 🆕 FR-6.2
│   ├── session-persistence.spec.ts                     # 🆕 FR-6.3
│   └── graceful-shutdown.spec.ts                       # 🆕 FR-6.3
├── scripts/
│   ├── build-go.js                                     # 不动
│   ├── smoke.sh                                        # 🆕 FR-7
│   └── ws-smoke-client.js                              # 🆕 FR-7
├── src/
│   ├── darvin-agent/
│   │   ├── config.yaml                                 # 改：清空 api_key 占位
│   │   ├── internal/
│   │   │   ├── acp/
│   │   │   │   └── loop.go                             # 不动
│   │   │   ├── agent/
│   │   │   │   ├── agent.go                            # 改：加 msgStore 字段 + dispatcher 三处落库
│   │   │   │   └── dispatcher.go                       # 改：同上
│   │   │   ├── config/
│   │   │   │   └── config.go                           # 改：FR-5.3 overlay
│   │   │   ├── gateway/
│   │   │   │   ├── eventledger.go                      # 改：done 加 usage
│   │   │   │   └── handlers.go                         # 改：加 list_sessions / get_messages dispatch + Handler.Store/MessageStore
│   │   │   └── store/
│   │   │       ├── message_store.go                    # 🆕 MessageStore interface + SQLiteMessageStore
│   │   │       └── sqlite_store.go                     # 不动
│   │   └── cmd/app/main.go                             # 改：注入 msgStore
│   ├── main/
│   │   ├── index.ts                                    # 改：+ ipc get_llm_config / set_llm_config + restartGoSubprocess
│   │   └── runtime/client.ts                           # 改：+ listSessions / getMessages
│   ├── preload/
│   │   └── index.ts                                    # 改：替换空 stub + 加 getLLMConfig / setLLMConfig
│   ├── renderer/
│   │   ├── components/settings/
│   │   │   ├── SettingsSubNav.vue                      # 改：+ 'models' section
│   │   │   └── SettingsPanelModels.vue                 # 🆕 FR-5.1
│   │   ├── composables/useSession.ts                   # 改：删 mockSessions 种子
│   │   ├── services/
│   │   │   ├── mock-agent.ts                           # S5 已删 ✓
│   │   │   └── mock-data.ts                            # 改：删会话/消息部分，保留 models/experts
│   │   ├── views/SettingsView.vue                      # 改：+ SettingsPanelModels
│   │   └── layout/AppShell.vue                         # 不动（S5 已接真 RPC）
│   └── shared/
│       └── darvin-api.ts                               # 改：+ DarvinLLMConfig / listSessions / getMessages / usage
├── package.json                                        # 改：+ @playwright/test + smoke / e2e scripts
├── playwright.config.ts                                # 🆕 FR-6.1
└── README.md                                           # 🆕 / 改：first run 5 步 + UI LLM 配置路径
```

### 4.2 关键决策

#### 4.2.1 UI LLM 配置落盘 = 用户级 yaml

理由（user picked）：不污染 git、不动 SQLite 契约、不需要改 Go 热重载路径。重启 Go 子进程 = `mgr.stop()` → `mgr.start()` → `client.connect()`，S5 已实装，三步 ≤ 2s。

#### 4.2.2 Playwright 跑真后端，key 缺失 skip

`test.skip(!process.env.ANTHROPIC_API_KEY, ...)` 让 CI 不阻塞。其他 spec（session persistence / graceful shutdown）不依赖 LLM 可全跑。

#### 4.2.3 done.usage 是契约扩展

`DarvinEvent.done` 加 `usage?` 是 **optional**，旧 client 不会崩（旧字段没变）。Go 端只在 `LLMEndEvent.Usage > 0` 时附 `usage` 字段，避免 nil 污染。

#### 4.2.4 mock-data 拆分会话/消息

`mock-data.ts` 拆成两块：`mock-session.ts`（彻底删）+ `mock-expert.ts`（保留 models / experts）。S6 只动会话/消息相关。

#### 4.2.5 dispatch hook 在 dispatcher.go，不在 loop.go

v1 写错了。`acp/loop.go` 只做 prompt/abort 调度，没有 message lifecycle 决策权。**真实落库点都在 `dispatcher.go`**。

### 4.3 时序图

#### 场景 1（happy path）

```
User                  UI (Vue)             Main (Electron)              Go (WS)                  Anthropic
 │ input "ping" + send │                       │                            │                         │
 │ ───────────────────►│                       │                            │                         │
 │                      │ invoke('darvin:prompt')                           │                         │
 │                      │ ────────────────────►│ client.prompt(req)         │                         │
 │                      │                       │ ─────────────────────────► │ recv {agent.prompt}     │
 │                      │                       │                            │ dispatcher.go           │
 │                      │                       │                            │   user msg append        │
 │                      │                       │                            │   store.SaveMessage     ─┐
 │                      │                       │                            │ Agent.Prompt → Run       │
 │                      │                       │                            │ LLM.StartRequest ──────► │
 │                      │                       │                            │                          │ Anthropic API
 │                      │                       │ ◄─ ws notif text_delta ─── │ ◄── text_delta event ────│ Δ
 │                      │ ◄── 'darvin:event' ───│                            │                          │
 │ ◄── reactive update │                       │                            │                          │
 │                      │                       │                            │ ... 更多 delta ...       │
 │                      │                       │ ◄─ ws notif done(usage) ── │ ◄── LLMEnd + agent_end ─│
 │                      │                       │                            │ dispatcher.go            │
 │                      │                       │                            │   RunEnd                 │
 │                      │                       │                            │   store.SaveMessage(assistant) ─┐
 │                      │                       │                            │   store.SaveSession(sess)        │
 │                      │ ◄── 'darvin:event' ───│                            │                          │
 │ ◄── 消息定型 ───────│                       │                            │                          │
```

#### 场景 3（UI 配置 LLM）

```
User                UI Settings            Main (Electron)                  Go (WS)               FileSystem
 │ API key + 保存     │                       │                                  │                       │
 │ ──────────────────►│                       │                                  │                       │
 │                    │ invoke('darvin:set_llm_config')                         │                       │
 │                    │ ─────────────────────►│ writeUserSettingsYAML(cfg)        │                       │
 │                    │                       │ ────────────────────────────────────────────────────────►│ ~/.config/...yaml
 │                    │                       │ mgr.stop()                       │                       │
 │                    │                       │ SIGTERM ─────────────────────►  │ shutdown             │
 │                    │                       │ mgr.start()                       │                       │
 │                    │                       │ spawn ────────────────────────► │ new instance         │
 │                    │                       │ ◄─ <port>...</port> ──────────  │                       │
 │                    │                       │ client.connect(port)             │                       │
 │                    │                       │ ws + subscribe_events ────────►  │ subscribed            │
 │                    │ ◄── { applied: true } │                                  │                       │
 │ ◄── "已应用" ─────│                       │                                  │                       │
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `bin/darvin-agent-*` 不存在 | smoke 提前检查；UI Settings → Models 保存时 main 检查并报错 |
| `~/.config/darvin-cowork/settings.yaml` 不存在 | 用 bundled config；UI 显示"未配置" |
| `LLM_API_KEY` env var 设了但 yaml 也有 | env var 优先级低（viper.MergeInConfig 后 env var 仍生效），但 S6 改成 yaml 优先 |
| 用户在 UI 改 key 中途 kill Electron | `~/.config/darwin-cowork/settings.yaml` 可能写了一半；用 yaml.Marshal + 原子 rename 防半文件 |
| 旧 session 里有 `done.usage` 缺失 | `usage?` optional，UI 不显示 token 数但不崩 |
| 旧 session 消息没有 messageId | `messages.id` 是 PK，必填；旧版不会进 sessions.db |
| Playwright 选不到中文 i18n 文案 | S6 实现时统一加 `data-testid` 属性 |
| Playwright 跑 CI 没设 key | `test.skip` 不 fail |
| mock-data.ts 拆分会话后被引用残留 | `grep -rn "mockSessions\|mockMessages" src/` 必须空 |
| 旧 `done` event 没 usage | TS union `usage?` optional，向后兼容 |
| Go 子进程 restart 时 port 冲突 | S5 实现的 port 解析走动态绑，理论上不冲突；smoke 测多 restart 不出问题 |
| `MessageStore` Save 失败 | `dispatcher.go` 落库失败 log warn 不 panic；event 仍照常 emit |
| SQLite migration 加 MessageStore 表 | AutoMigrate 已包含 `&store.Message{}`，不需要新 migration |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `package.json` | 改：devDeps `+@playwright/test`；scripts `+smoke` `+e2e` `+e2e:headed` `+e2e:install` |
| `playwright.config.ts` | 🆕 FR-6.1 |
| `e2e/happy-path.spec.ts` | 🆕 FR-6.2 |
| `e2e/session-persistence.spec.ts` | 🆕 FR-6.3 |
| `e2e/graceful-shutdown.spec.ts` | 🆕 FR-6.3 |
| `e2e/.gitignore` | 🆕 标准 Playwright gitignore |
| `scripts/smoke.sh` | 🆕 FR-7 |
| `scripts/ws-smoke-client.js` | 🆕 FR-7 |
| `src/darvin-agent/config.yaml` | 改：api_key 留空 |
| `src/darvin-agent/internal/config/config.go` | 改：Load 加 overlay（FR-5.3） |
| `src/darvin-agent/internal/agent/store/message_store.go` | 🆕 FR-2.1 |
| `src/darvin-agent/internal/agent/agent.go` | 改：加 msgStore 字段（Agent struct + NewAgentConfig） |
| `src/darvin-agent/internal/agent/dispatcher.go` | 改：三处落库（FR-2.2） |
| `src/darvin-agent/cmd/app/main.go` | 改：注入 MessageStore + 传 Agent.NewAgentConfig |
| `src/darvin-agent/internal/gateway/handlers.go` | 改：Handler 加 Store/MessageStore；switch 加 list_sessions/get_messages |
| `src/darvin-agent/internal/gateway/eventledger.go` | 改：mapEventToTS(LLMEndEvent) 加 usage（FR-4） |
| `src/main/index.ts` | 改：+ 2 ipc handler + writeUserSettingsYAML + restartGoSubprocess |
| `src/main/runtime/client.ts` | 改：+ listSessions / getMessages / getLLMConfig / setLLMConfig 方法 |
| `src/preload/index.ts` | 改：替换 stub + 加 LLM config 方法 |
| `src/shared/darvin-api.ts` | 改：`done` 扩 `usage?` + `DarvinLLMConfig` + `DarvinApi` 新方法 |
| `src/renderer/composables/useSession.ts` | 改：删 mockSessions 种子 |
| `src/renderer/components/settings/SettingsSubNav.vue` | 改：加 `'models'` section |
| `src/renderer/components/settings/SettingsPanelModels.vue` | 🆕 FR-5.1 |
| `src/renderer/services/mock-data.ts` | 改：删 mockSessions/mockMessages |
| `src/renderer/views/SettingsView.vue` | 改：+ SettingsPanelModels |
| `src/renderer/components/chat/MessageItem.vue` | 改：+ `data-testid="token-count"` |
| `src/renderer/components/chat/Composer.vue` | 改：+ `data-testid="composer-textarea"` |
| `README.md` | 🆕 first run 5 步 + UI LLM 入口 |

**不修改**：
- S1-S5 锁定的内部接口（除 `done.usage` 扩字段）
- WS / JSON-RPC envelope 格式
- `acp/loop.go`（hook 在 dispatcher.go）
- `cmd/app/main.go` 启动流程（仅注入新 store）

---

## 7. 验收标准

### 7.1 静态 / 构建

- [ ] `npm run lint` 通过
- [ ] `npm run build:agent` exit 0
- [ ] `npm run e2e:install` exit 0（首次装 Chromium）
- [ ] `playwright.config.ts` + `e2e/` 编译通过

### 7.2 headless smoke

- [ ] `npm run smoke` exit 0
- [ ] smoke 启动 darvin-agent ≤ 1s
- [ ] WS 连上 ≤ 200ms
- [ ] prompt 发出 → 收第一条 text_delta ≤ 3s
- [ ] 收 done event + `usage.totalTokens > 0`（依赖 FR-4）
- [ ] SIGTERM 子进程 ≤ 3s 退出
- [ ] stderr 最后一行匹配 `graceful shutdown complete`
- [ ] `sqlite3 sessions.db "PRAGMA integrity_check"` 输出 `ok`
- [ ] 整个 smoke ≤ 10s

### 7.3 Playwright e2e

- [ ] `npm run e2e` exit 0（key 缺失时 skip happy-path，不 fail）
- [ ] happy-path.spec.ts：UI 配置 API key → 发 prompt → 见 text_delta → 见 done with usage
- [ ] session-persistence.spec.ts：第二次启动见上次消息
- [ ] graceful-shutdown.spec.ts：关 Electron 后 pgrep 无 darvin-agent
- [ ] trace 在失败时保留

### 7.4 UI 端到端（手测）

- [ ] `npm start` → Settings → Models → 输入 API key → 保存 → 弹 "已应用"
- [ ] RuntimeStatusBadge 短暂 amber → green
- [ ] ChatView 输入 "ping" → 流式响应 → done with token 数
- [ ] 关 Electron → 重启 → 历史消息可见

### 7.5 graceful shutdown

- [ ] `kill <electron-main-pid>` → 子进程 SIGTERM → 6 步走完
- [ ] 总时长 ≤ 3s
- [ ] sessions.db integrity ok
- [ ] `pgrep darvin-agent` 无残留

### 7.6 清理

- [ ] `src/renderer/services/mock-data.ts` 的 `mockSessions` / `mockMessages` 已删
- [ ] `grep -rn "mockSessions\|mockMessages" src/` 空
- [ ] S1-S5 接口未破坏（lint + 静态通过）
- [ ] done event 在缺 usage 时仍可被旧 client 消费（`usage?` optional）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| Memory / Dreaming | 架构文档 §"记忆层"；M2 接入 |
| Skills / MCP | 架构文档 §"Skills" / §"MCP"；M3 接入 |
| Failover / Circuit Breaker | 架构文档 §"Failover"；M2 接入 |
| SubAgent 真实化 | 架构文档 §"SubAgent"；中期 |
| ContextEngine 3 策略 | 架构文档 §"ContextEngine"；中期 |
| LLM Provider 矩阵 | 8 provider 全实现；M2 |
| Reconnect / ping-pong | 长期可靠性 |
| Production packaging | electron-builder + extraResources |
| CI integration | GitHub Actions: lint + smoke + e2e |
| 多 session 切换 UI | UI 增强 |
| Session 标题生成 | LLM 给最早 user message 生成标题 |
| mock-expert.ts 拆分 | Expert suite mock data 独立模块 |
| 热重载 LLM 配置 | 不重启 Go 子进程切换 provider |

---

> **v1 spec 状态**：作废，仅历史参考。差异详见 §0。
>
> **完成说明**：v2 待审 / 待落地。审计已就绪（P0 + P1 列表），等用户点头后开始实现。