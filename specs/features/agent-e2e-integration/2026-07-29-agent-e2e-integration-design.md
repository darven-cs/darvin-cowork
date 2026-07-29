# Agent E2E Integration 设计文档（S6）

> **Phase 4 / 6 — 端到端阶段**。把 S1-S5 串起来，三层独立跑通后做端到端 smoke：Electron → Go WS → Anthropic → event.Bus → WS notification → UI 渲染流；session 持久化跨重启可见；优雅关闭链路（Electron kill → Agent.Abort → SIGTERM Go → flush → DB close）全链路验证；补充 README "first run" 让新用户能跑通。
> 前置：S1-S5 全部完成且独立验收通过。
> 本 spec 是整个 6 阶段计划的**收口** — 完成后 demo 可走通。

---

## 1. 概述

### 1.1 问题 / 背景

S1-S5 完成后：

| 阶段 | 范畴 | 验收手段 |
|------|------|---------|
| S1 | UI shell + 契约 | 浏览器 DevTools `await window.darvin.prompt('ping')` |
| S2 | sessions.db + GORM | `go test ./internal/store/...` |
| S3 | Gateway WS + JSON-RPC | `wscat -c ws://localhost:NNNNN` |
| S4 | ACP + Agent.Run + 优雅关闭 | Go 独立跑 + SIGTERM |
| S5 | Electron RuntimeMgr + AgentClient | `npm start` + DevTools |

**剩 3 件事**：

1. **三层联调**：UI 真的能从 Go 端拿到 Anthropic 响应（不只是 S5 的 mock-client 推到 UI 那种）；Anthropic 流式响应走完（Anthropic API key + 网络可达）
2. **session 持久化**：UI 关闭 → 重启 → 看到上一轮对话历史（验证 S2 sessions.db 落库 + 读）
3. **graceful shutdown 链路**：Electron 主进程 uninstall → 子进程 SIGTERM → S4 abort 路径 → DB close 全链路 ≤ 3s

外加：

4. **README "first run"**：补一段 5 步跑通指南（新用户 clone 后能 5 分钟内跑通）
5. **冒烟脚本**：仓库根加 `scripts/smoke.sh`（或 `npm run smoke`），跑端到端 happy path（不依赖 UI，纯 CLI 验证）

### 1.2 目标

- 端到端 happy path 走通：从 UI 输入 prompt → 流式收到真实 LLM 响应（Anthropic）
- session 持久化：用户关 UI 重启后，上一轮对话在 message list 可见
- graceful shutdown 链路：Kill Electron 主进程 → 子进程 3s 内退出 + stderr "graceful shutdown complete" + sessions.db 文件没坏
- README 一段 "first run" 让新用户能跑通
- `npm run smoke` 脚本：headless 验证 WS + prompt + 收 event 流（不依赖 Electron）

### 1.3 非目标

- **不**做 Anthropic 之外的 LLM 接入（M2 spec）
- **不**做 Memory / Dreaming / Skills / MCP（后续 spec）
- **不**做生产打包（packaging / signing / notarization 远期）
- **不**做 CI 集成（暂为本地手测）
- **不**做 e2e 详细断言（仅 smoke happy path；详细断言留 Vitest/Playwright 后期 spec）
- **不**改 S1-S5 的内部接口（仅补 happy path 验证 + 文档）

---

## 2. 用户场景

### 场景 1：完整 happy path（Electron UI）

**Given** S1-S5 全部完成，Anthropic API key 已配置（`src/darvin-agent/configs/config.yaml` 或 env）
**When** `npm start` 启动 Electron
**Then** 序列：
1. ◯ 主进程 spawn 子进程，stdout 解析 port
2. ◯ AgentClient 连 WS
3. ◯ UI 启动，header 显示 "Runtime: ready"
4. ◯ 用户输入 "ping" send
5. ◯ main 进程 WS 推 `agent.prompt` 到 Go
6. ◯ Go Agent.Prompt → Run → LLM StartRequest → Anthropic API
7. ◯ Anthropic 流式返回 text_delta → event.Bus emit → EventLedger.AttachBus 推 WS notification
8. ◯ AgentClient 收到 → 推 `darvin:event` 到 renderer
9. ◯ renderer MessageList reactive 追加 delta
10. ◯ Anthropic 完成 → DoneEvent(usage) → `darvin:event(type:"done")` → UI 消息定型
11. ◯ UI 助记 "ping 的回复" + token 计数显示（usage 来自 done event）

### 场景 2：session 持久化跨重启

**Given** 场景 1 已跑通（messages 落 sessions.db）
**When** 关 Electron（kill 主进程）→ 等 ≤3s → 重启 `npm start`
**Then**：
- ◯ UI 启动后，MessageList 自动加载历史 sessions（至少最近一个 session 的所有 messages）
- ◯ 上轮的 text_delta + done 文本完整显示
- ◯ 用户能接着上次的 session 继续发新 prompt（**不**新建 session，复用 sessionId）

### 场景 3：graceful shutdown 链路

**Given** 子进程在 listen，Agent.Run 没在跑
**When** `kill <electron-main-pid>`（模拟 OS 关 app）
**Then** 链路：
1. ◯ Electron `before-quit` 触发
2. ◯ `client.disconnect()` → WS close
3. ◯ `mgr.stop()` → SIGTERM → darvin-agent
4. ◯ darvin-agent `signal.NotifyContext` cancel
5. ◯ S4 shutdown 序列执行（Abort → flush → WS server.Shutdown → DB close → os.Exit(0)）
6. ◯ stderr 最后一行 "graceful shutdown complete"
7. ◯ total 时长 ≤ 3s
8. ◯ sessions.db 文件未损坏（`sqlite3 sessions.db "PRAGMA integrity_check"` 返回 `ok`）

### 场景 4：graceful shutdown 链路（在 Agent.Run 中）

**Given** Agent.Run 正在跑（流式返回中）
**When** `kill <electron-main-pid>`
**Then**：
- ◯ WS close 触发 darvin-agent ctx cancel
- ◯ Agent.Abort → LLMEndEvent.FinishReason = "aborted"
- ◯ done event 推给 client（如果 client 还连着；否则丢弃）
- ◯ 链路仍 ≤ 3s

### 场景 5：Anthropic API 不可达

**Given** Internet 断开或 API key 错
**When** 用户发 prompt
**Then**：
- ◯ Agent.Run 失败 → `agent_error` event type → renderer 显示 error toast
- ◯ UI 仍可继续输入（不卡死）
- ◯ sessions.db 记录 error message

### 场景 6：headless smoke（CI 友好）

**Given** S1-S4 已完成（不需要 Electron）
**When** `npm run smoke`（在仓库根）
**Then** 脚本：
1. 启动 darvin-agent 子进程（用 `bin/darvin-agent-*`）
2. 解析 stdout port
3. 连 WS
4. 发 `agent.prompt`("ping")
5. 收第一个 `text_delta` event
6. 收 `done` event
7. 验证 done.usage.totalTokens > 0
8. SIGTERM child
9. 退出码 0

整个过程 < 10s（Anthropic 首字延迟 < 2s）。

### 场景 7：新用户 first run

**Given** 新用户 clone 仓库，Node 22 + Go 1.22+
**When** 跟着 README "first run" 段落 5 步走
**Then** 5 分钟内能跑通：
1. `npm install`
2. `cp src/darvin-agent/configs/config.example.yaml src/darvin-agent/configs/config.yaml` 并填 ANTHROPIC_API_KEY
3. `npm run build:agent`
4. `npm start`
5. DevTools 输入 prompt → 看到响应

---

## 3. 功能需求

### FR-1：Anthropic API key 配置

`src/darvin-agent/configs/config.yaml`（S2 已建）补 `llm.anthropic_api_key` 段：

```yaml
agent:
  name: darvin-agent
  log_level: debug

database:
  dsn: ./data.db
  sessions_dsn: ./sessions.db

llm:
  provider: anthropic
  anthropic_api_key: ${ANTHROPIC_API_KEY}    # env var 注入
  model: claude-sonnet-4-5
  max_tokens: 8192
```

要点：
- `${VAR}` 语法由现有 config loader 展开（v0 实现已支持）
- 真实 key 不入 git；`config.example.yaml` 写 placeholder
- env fallback：`ANTHROPIC_API_KEY` env var 直接读（`config.go` 已有 os.Getenv 路径）

### FR-2：session 持久化

#### FR-2.1 Go 端 sessions.db 写入

S4 §"非目标"中明确"不把 messages 表写入逻辑"留给 S6 接。本 spec 落地：

- `internal/acp/loop.go` 在 Agent.Run 流程中：
  - 收到 `user` message → `sessions.db.SaveMessage(msg)` (role=user, content, messageId, sessionId)
  - 收到 `text_delta` 累积 → `done` event 时 → `sessions.db.SaveMessage(msg)` (role=assistant, content, completion_tokens, ...)
- 启动时 `SessionManager.LoadAllFromDB()` 加载历史 sessions（让 electron 端重启能 query）

具体而言：

```go
// internal/acp/loop.go (S4 已有框架, S6 补落库)
func (l *Loop) onRunEnd(sess *session.Session, runErr error) {
    // S6 add: 落库
    if l.store != nil {
        l.store.SaveSession(sess)
        for _, msg := range sess.Messages {
            l.store.SaveMessage(msg)
        }
    }
}
```

#### FR-2.2 UI 端 history 加载

Renderer 启动时主动 query：

```ts
// src/renderer/composables/useSessionHistory.ts (S6 新)
import { onMounted, ref } from 'vue';

export function useSessionHistory() {
  const messages = ref<DarvinMessage[]>([]);

  async function loadLatest() {
    const list = await window.darvin.listSessions();
    if (list.sessions.length === 0) return;
    const latest = list.sessions[0];
    messages.value = await window.darvin.getMessages(latest.id);
  }

  onMounted(loadLatest);
  return { messages, loadLatest };
}
```

新增 2 个 RPC（Go 端 handler 也补）：

- `agent.list_sessions` → `{ sessions: [{ id, createdAt, updatedAt, title }] }`
- `agent.get_messages` → `{ messages: [{ id, role, content, createdAt, ... }] }`

Handler 端在 S3 已有 dispatch 表上**加上**这两个 method，调 SessionStore 实现。

> **注**：S3 FR-5 列的 3 个 stub 是 S3 阶段的需求；S4 接管 prompt/abort 是 S4 任务。S6 落地 `list_sessions / get_messages` 是 Git 端首个**新增**的 RPC method（前提：S3 dispatch 的 `default` 分支已支持 method 扩展）。

#### FR-2.3 S1 mock 模式兼容

S1 期间 `window.darvin.listSessions` / `getMessages` 不存在。S6 在 `src/shared/darvin-api.ts` 补这 2 个方法签名 + 在 preload / mock 同步加（即使 S1 mock 阶段返回空）。

### FR-3：graceful shutdown 链路验证

#### FR-3.1 场景 3 自动化

`scripts/smoke.sh`（FR-6 详细）：除 happy path 外，断言：

```bash
# graceful shutdown 验证
START=$(date +%s%N)
kill $ELECTRON_PID
wait $ELECTRON_PID
END=$(date +%s%N)
DURATION_MS=$(( (END - START) / 1000000 ))
if [ $DURATION_MS -gt 3500 ]; then
  echo "FAIL: shutdown took ${DURATION_MS}ms (> 3500)"
  exit 1
fi

# DB integrity
sqlite3 sessions.db "PRAGMA integrity_check" | grep -q "^ok$" || { echo "FAIL: db broken"; exit 1; }
```

#### FR-3.2 S4 链路日志

S4 已在 stderr 6 个阶段打 log（abort / flush / ws shutdown / db close / exit 0）。S6 **不**改 S4 实现，仅验证。

### FR-4：happy path 验证

#### FR-4.1 端到端 happy path 自动化

`scripts/smoke.sh` 全程：

```bash
#!/usr/bin/env bash
set -e
# 1. 启动 darvin-agent
./bin/darvin-agent-$(uname | tr 'A-Z' 'a-z')-$(uname -m) > /tmp/agent.out 2> /tmp/agent.err &
AGENT_PID=$!

# 2. 等 port
PORT=$(grep -oP '<port>\K\d+' /tmp/agent.out | head -1)
[ -n "$PORT" ] || { echo "FAIL: no port"; exit 1; }

# 3. 用 node-wsclient 测 prompt + 收 event
node scripts/ws-smoke-client.js $PORT
RC=$?

# 4. SIGTERM
kill -TERM $AGENT_PID
wait $AGENT_PID 2>/dev/null || true

# 5. 验证 stderr
grep -q "graceful shutdown complete" /tmp/agent.err || { echo "FAIL: no shutdown log"; exit 1; }

# 6. DB integrity
sqlite3 sessions.db "PRAGMA integrity_check" | grep -q "^ok$" || { echo "FAIL: db broken"; exit 1; }

exit $RC
```

#### FR-4.2 WS smoke client

`scripts/ws-smoke-client.js` 用 Node 内置 `WebSocket`（Node 22+）：

```js
const port = process.argv[2];
const ws = new WebSocket(`ws://localhost:${port}/ws`);

let gotTextDelta = false;
let gotDone = false;

ws.on('open', async () => {
  ws.send(JSON.stringify({
    jsonrpc: '2.0', id: '1', method: 'agent.prompt',
    params: { content: 'ping' },
  }));
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  if (msg.method === 'agent.event') {
    if (msg.params.type === 'text_delta') gotTextDelta = true;
    if (msg.params.type === 'done') {
      gotDone = true;
      if (msg.params.usage?.totalTokens > 0) {
        console.log('PASS: got text_delta + done with usage');
        process.exit(0);
      } else {
        console.error('FAIL: done without usage');
        process.exit(1);
      }
    }
  }
  if (msg.error) {
    console.error('FAIL: rpc error', msg.error);
    process.exit(1);
  }
});

setTimeout(() => {
  console.error('FAIL: timeout (10s)');
  process.exit(1);
}, 10000);
```

### FR-5：README "first run"

`README.md`（仓库根）新增一段：

```markdown
## First Run

5 步走通 darvin-cowork（Node 22+ / Go 1.22+）：

1. **Install**：
   ```bash
   npm install
   ```

2. **Configure Anthropic API key**：
   ```bash
   cp src/darvin-agent/configs/config.example.yaml src/darvin-agent/configs/config.yaml
   # 编辑 src/darvin-agent/configs/config.yaml，填入 anthropic_api_key: ${ANTHROPIC_API_KEY}
   export ANTHROPIC_API_KEY=sk-ant-...        # 或直接在 yaml 里替换
   ```

3. **Build agent binary**：
   ```bash
   npm run build:agent
   ```
   产出 `bin/darvin-agent-{platform}-{arch}`。

4. **Run Electron**：
   ```bash
   npm start
   ```
   打开主窗口 + DevTools。看到 "Runtime: ready"。

5. **Try a prompt**：
   - DevTools console: `await window.darvin.prompt({ content: 'ping' })`
   - 或 UI 输入框输入 → send
   - 应看到流式响应 + done

### Verify with headless smoke

```bash
npm run smoke
```

预期：
- happy path PASS（text_delta + done with usage）
- graceful shutdown PASS（stderr "graceful shutdown complete"）
- sessions.db integrity OK
- 总耗时 ≤ 10s

### Troubleshooting

| 症状 | 排查 |
|------|------|
| UI header "Runtime: offline" | `ls bin/darvin-agent-*` 是否存在；不存在则 `npm run build:agent` |
| 流式没有响应 | 检查 `ANTHROPIC_API_KEY` 是否设；`cat src/darvin-agent/configs/config.yaml`；Electron stderr 是否有 Go 日志 |
| sessions.db 损坏 | `rm sessions.db && npm start`（首次会重建）|
| 子进程不退出 | `pgrep darvin-agent` 强 kill；查 S4 SIGTERM 路径日志 |
```

### FR-6：`npm run smoke` 脚本

`package.json` `scripts` 加：

```json
{
  "scripts": {
    "smoke": "bash scripts/smoke.sh"
  }
}
```

`scripts/smoke.sh`（按 FR-4.1 完整）。

### FR-7：CI / 文档占位

- CI 集成**不**做（远期 spec）
- 但留 hook：`scripts/smoke.sh` 退出码 0 / 1 明确，CI 后期接 `npm run smoke` 一行即可

---

## 4. 实现方案

### 4.1 目录结构

```
.
├── scripts/
│   ├── smoke.sh                # 🆕 FR-4.1 / FR-6
│   └── ws-smoke-client.js      # 🆕 FR-4.2
├── src/
│   ├── darvin-agent/
│   │   ├── configs/
│   │   │   ├── config.example.yaml  # 🆕 模板（含 llm 段）
│   │   │   └── config.yaml          # 🆕 gitignore，dev 填
│   │   └── internal/acp/
│   │       └── loop.go              # 改：S6 补 SaveSession / SaveMessage
│   ├── main/
│   │   └── runtime/                 # S5 已实装
│   ├── renderer/
│   │   ├── composables/
│   │   │   └── useSessionHistory.ts # 🆕 FR-2.2
│   │   └── components/
│   │       └── MessageList.vue      # 改：初始化时 load history
│   └── shared/
│       └── darvin-api.ts            # 改：补 listSessions / getMessages 签名
├── package.json                     # 改：scripts + smoke
└── README.md                        # 🆕 改：加 first run 段落
```

### 4.2 时序图

#### 场景 1（happy path）

```
User                                UI (Vue)                    Main (Electron)                  Go (WS)                     Anthropic
 │                                    │                              │                              │                            │
 │ input "ping" + send ───────────►  │                              │                              │                            │
 │                                    │ invoke('darvin:prompt') ──► │ client.prompt(req) ────────► │ recv {agent.prompt}        │
 │                                    │                              │                              │ Agent.Prompt → Run ─────► │
 │                                    │                              │                              │                            │ Anthropic API
 │                                    │                              │ ◄── ws notif text_delta ──── │ ◄── text_delta event ──────│ Δ
 │                                    │ ◄── 'darvin:event' ──────── │                              │                            │
 │ ◄── reactive update ───────────── │                              │                              │                            │
 │                                    │                              │                              │ ... 更多 delta ...         │
 │                                    │                              │ ◄── ws notif done ────────── │ ◄── done event (usage) ─── │
 │                                    │ ◄── 'darvin:event' ──────── │                              │                            │
 │ ◄── 消息定型 ──────────────────── │                              │                              │                            │
```

#### 场景 2（session 持久化）

```
[Process 1]                                  [sessions.db]
[User 发 prompt]                              files written
   ↓
[Agent.Run]  ── SaveSession ────────────►  sessions(id="s-001")
[Run done]    ── SaveMessage user ────────►  messages(id="m-001", session=s-001, role=user)
[Run done]    ── SaveMessage assistant ──►  messages(id="m-002", session=s-001, role=assistant, content=...)

[Electron 关 → 重启]
   ↓
[App.vue mount]  ── useSessionHistory.loadLatest()
   ↓
[window.darvin.listSessions]  ── ipc.invoke('darvin:list_sessions')
   ↓
[main: client.listSessions]  ── ws {method: 'agent.list_sessions'}
   ↓
[Go: sessions.db.ListSessions()]  ── [{id: 's-001', ...}]
   ↓
[UI 收到 → next session 显示在 MessageList]
   ↓
[window.darvin.getMessages('s-001')]  ── 同链路
   ↓
[Go: sessions.db.ListMessages('s-001')]  ── [{role: user, ...}, {role: assistant, ...}]
   ↓
[UI MessageList 渲染]
```

#### 场景 3（graceful shutdown）

```
[User: kill electron main]
   ↓
[Electron 'before-quit' event]
   ↓
[preventDefault; shuttingDown=true]
   ↓
[client.disconnect()]  ── ws close ────► [Go: ctx cancel from S3]
   ↓                                       ├─ ws handler detect close
   ↓                                       └─ 后续 in-flight Run 受影响
   ↓
[mgr.stop()]  ── SIGTERM ─────────────► [Go: signal.NotifyContext 触发]
   ↓                                       ├─ Agent.Abort
   ↓                                       ├─ flush events (≤2s)
   ↓                                       ├─ WS server.Shutdown
   ↓                                       ├─ DB.Close
   ↓                                       └─ stderr "graceful shutdown complete" → os.Exit(0)
   ↓
[child exit observed]
   ↓
[app.quit() → main exit 0]
   ↓
[smoke.sh: total ≤ 3s; sessions.db integrity OK]
```

### 4.3 关键决策

#### 4.3.1 headless smoke 优先于 UI 自动化

S6 不引入 Playwright / Spectron。理由：
- v0 阶段 UI 极简，断言意义不大
- smoke 验证核心 protocol（happy path + shutdown + DB integrity），与 UI 解耦
- CI 后期接 `npm run smoke` 一行即可

UI 端到端**手测**（场景 1-3）作为验收标准，不自动化。

#### 4.3.2 session 持久化用最小侵入

S4 已实装 ACP Loop + Agent.Run；S6 **不**改 S4 内部架构，仅在 Loop 末端（onRunEnd）加 `SaveSession` + `SaveMessage` 调用。S2 已有 `SessionStore` interface，`SaveSession` / `SaveMessage` / `ListSessions` / `ListMessages` 4 个方法 S2 期间落地。

#### 4.3.3 list_sessions / get_messages 是 S6 新增 RPC

S3 阶段 dispatch 表只列了 3 个 method（prompt / abort / subscribe_events）。S6 新增 2 个 method（list_sessions / get_messages）需在 Go handler dispatch 表**注册**。S4 §FR-1 已留 `default` 分支可扩展。

#### 4.3.4 `mock-agent` 移除条件

S5 §FR-7 已声明 `mock-agent.ts` 在 S5 后不再被 import。S6 正式 `rm -rf src/renderer/services/mock-agent.ts`，preload 不再 import。

#### 4.3.5 不做 e2e 详细断言

消息内容、token 数精度、UI 样式不验。S6 验：
- protocol 正确（req/resp/notif 序列）
- 至少 1 条 text_delta + done(usage>0)
- 持久化能 reload
- graceful shutdown 时长
- DB integrity

详细断言（rerank 100 次、token 范围、错误恢复）留给后续 spec。

### 4.4 关键代码骨架

#### acp/loop.go sessions.db 写入（S6 补）

```go
// internal/acp/loop.go S6 增量
func (l *Loop) onRunEnd(sess *session.Session, runErr error) {
    if l.store == nil { return }

    if err := l.store.SaveSession(sess); err != nil {
        log.Printf("acp: save session: %v", err)
    }
    for _, msg := range sess.Messages {
        if err := l.store.SaveMessage(msg); err != nil {
            log.Printf("acp: save message %s: %v", msg.ID, err)
        }
    }
}

// S6 还需：subscribe agent 状态变更
// (S4 已有: l.events.Subscribe → onRunEnd 可通过 EventBus hook)
// 实际路径: S4 Loop.Run 内已经在 RunEndEvent 时 flush, S6 在 RunEndEvent handler 加 SaveSession
```

#### handler dispatch 新增 method（S6 补）

```go
// internal/gateway/handlers.go S6 增量
func dispatch(ctx context.Context, req *Request, sessions *SessionManager, ledger *EventLedger, store *session.Store) *Response {
    switch req.Method {
    // ... S4 已有的 agent.prompt / agent.abort / agent.subscribe_events ...
    case "agent.list_sessions":
        return handleListSessions(ctx, req.ID, store)
    case "agent.get_messages":
        return handleGetMessages(ctx, req.ID, req.Params, store)
    }
}

func handleListSessions(ctx context.Context, id json.RawMessage, store *session.Store) *Response {
    list, err := store.ListSessions()
    if err != nil {
        return errorResp(id, CodeInternalError, "list sessions", err)
    }
    return &Response{
        JSONRPC: "2.0", ID: id,
        Result: map[string]any{"sessions": list},
    }
}

func handleGetMessages(ctx context.Context, id json.RawMessage, params json.RawMessage, store *session.Store) *Response {
    var p struct{ SessionID string `json:"sessionId"` }
    if err := json.Unmarshal(params, &p); err != nil {
        return errorResp(id, CodeInvalidParams, "invalid params", err)
    }
    msgs, err := store.ListMessages(p.SessionID)
    if err != nil {
        return errorResp(id, CodeInternalError, "list messages", err)
    }
    return &Response{
        JSONRPC: "2.0", ID: id,
        Result: map[string]any{"messages": msgs},
    }
}
```

#### preload listSessions / getMessages（S6 补）

```ts
// src/preload/index.ts S6 增量
const darvin = {
  // ... S5 已有的 prompt / abort / onEvent / status ...
  async listSessions(): Promise<{ sessions: DarvinSession[] }> {
    return ipcRenderer.invoke('darvin:list_sessions', {});
  },
  async getMessages(sessionId: string): Promise<{ messages: DarvinMessage[] }> {
    return ipcRenderer.invoke('darvin:get_messages', { sessionId });
  },
};
```

#### main ipc handler 增量

```ts
// src/main/index.ts S6 增量
ipcMain.handle('darvin:list_sessions', () => client.listSessions());
ipcMain.handle('darvin:get_messages', (_e, args) => client.getMessages(args.sessionId));
```

> 路径：preload → ipcMain.handle → client.listSessions/getMessages → AgentClient.request → ws → Go handler → store → 返回。

#### AGENTS.md 同步

`AGENTS.md` / `docs/agent-quickstart.md` 不变；新的"first run"指南在 `README.md` 顶层。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `bin/darvin-agent-*` 不存在 | `smoke.sh` 提前检查 `$BIN` 存在；不存在退出 1 + 提示 |
| `config.yaml` 缺 anthropic_api_key | `smoke.sh` 启动前校验环境；缺则 exit 1 + 提示 |
| Anthropic API 不可达 | smoke → 仍能 connect + prompt，但 `error` event 替代 `done`；smoke script 检测 `error` event 退出 1 |
| Anthropic rate limit | 同上 error event |
| sessionId 不存在（get_messages） | Store.ListMessages 返回空数组; not error |
| sessions.db 文件不存在 | Store 启动时 init; S2 已实装 |
| sessions.db corrupted | `PRAGMA integrity_check` 失败; smoke 退出 1 + 提示 `rm sessions.db` |
| kill -9 Electron（OS 强制） | 不走 before-quit; 子进程成 orphan; smoke 不覆盖此场景 |
| kill -TERM Electron | 走 before-quit; 子进程 SIGTERM 链路; smoke 验证 |
| 子进程启动但 5s 没 port | S5 mgr.start() reject; smoke 启动期 exit 1 |
| WS 连不上 | client.connect reject; smoke exit 1 |
| prompt 后 10s 没收 done | ws-smoke-client.js setTimeout 10s exit 1 |
| done event 无 usage | smoke 验证 `usage.totalTokens > 0`; 否则 exit 1 |
| session 重新载入但 messages 为空 | （意外）ratelimit / 中断 / 写失败; UI MessageList 显示空; 不报错 |
| 多 session 切换 | S6 scope 之外; UI 暂时只显示**最近 1 个** session; 切换 UI 后续 spec |
| children stderr 大量输出 | 默认透传; smoke 不截断 |
| Run 中途 abort | `done` event 的 finishReason="aborted"; usages 可能 partial; smoke 不覆盖 |
| mock-agent.ts 残留 import | S6 显式 `rm` 并 `grep -r mock-agent src/` 验证无引用 |
| list_sessions 返回 0 项 | UI MessageList 显示空; header 显示 "No history yet" |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `scripts/smoke.sh` | 🆕 Bash 脚本：spawn + connect + prompt + shutdown + integrity |
| `scripts/ws-smoke-client.js` | 🆕 Node WS 客户端：收 text_delta + done |
| `src/darvin-agent/configs/config.example.yaml` | 🆕 模板（含 llm 段） |
| `src/darvin-agent/configs/config.yaml` | 🆕 gitignore；dev 填 |
| `src/darvin-agent/internal/acp/loop.go` | 改：S6 增 `onRunEnd` 落库 |
| `src/darvin-agent/internal/gateway/handlers.go` | 改：S6 增 `list_sessions` / `get_messages` dispatch |
| `src/darvin-agent/internal/agent/store/store.go` | S2 已实现 ListSessions / ListMessages 接口；S6 SQLiteStore 补实现 |
| `src/darvin-agent/.gitignore` | 改：加 `configs/config.yaml` |
| `src/shared/darvin-api.ts` | 改：补 `listSessions` / `getMessages` 签名 + `DarvinSession` / `DarvinMessage` 类型 |
| `src/preload/index.ts` | 改：S6 增 listSessions / getMessages IPC |
| `src/main/index.ts` | 改：S6 增 ipcMain.handle 转发 |
| `src/main/runtime/client.ts` | 改：S6 增 listSessions / getMessages 方法 |
| `src/renderer/composables/useSessionHistory.ts` | 🆕 onMounted load history |
| `src/renderer/components/MessageList.vue` | 改：初始化时 load history |
| `src/renderer/services/mock-agent.ts` | 🆕 移除（S5 已不用） |
| `package.json` | 改：scripts 加 `smoke` |
| `README.md` | 🆕 改：加 "First Run" 段落 + Troubleshooting |
| `.gitignore` | 改：加 `bin/`, `sessions.db` |

**不修改**：
- S1-S5 锁定的内部接口（如 `DarvinPromptRequest` 签名、JSON-RPC envelope 格式）
- WS / JSON-RPC 协议（仅加 method，不破坏兼容）
- `cmd/app/main.go` 启动流程
- S4 graceful shutdown 内部实现（仅验证）

---

## 7. 验收标准

### 7.1 自动化 headless smoke

- [ ] `npm run smoke` exit 0
- [ ] smoke 启动 darvin-agent ≤ 1s
- [ ] WS 连上 ≤ 200ms
- [ ] prompt 发出 → 收第一条 text_delta ≤ 3s（Anthropic 首字延迟）
- [ ] 收 done event + `usage.totalTokens > 0`
- [ ] SIGTERM 子进程 ≤ 3s 退出
- [ ] 子进程 stderr 最后一行匹配 `graceful shutdown complete`
- [ ] `sqlite3 sessions.db "PRAGMA integrity_check"` 输出 `ok`
- [ ] 整个 smoke 流程 ≤ 10s

### 7.2 UI 端到端（手测）

- [ ] `npm start` 启动后，UI header 显示 "Runtime: ready"
- [ ] DevTools console `await window.darvin.prompt({ content: 'ping' })` 返回 `{ sessionId, messageId }`
- [ ] UI 输入框输入 "ping" → send → 流式 assistant 消息出现（每条 text_delta 追加）
- [ ] done event 触发 UI 消息定型（cursor 消失）
- [ ] 杀子进程：`kill -9 $(pgrep darvin-agent)` → UI 切换 "Runtime: offline"
- [ ] 重启 Electron：新窗口 message list 显示上轮对话 user + assistant message 完整内容
- [ ] 接着上轮 session 发新 prompt（不新建 sessionId；继续 messageId 累加）

### 7.3 graceful shutdown 链路

- [ ] `kill <electron-main-pid>` → `before-quit` 触发 → 子进程 SIGTERM → S4 6 步走完
- [ ] 总时长 ≤ 3s
- [ ] sessions.db 文件未损坏（PRAGMA integrity_check ok）
- [ ] 子进程 stdout / stderr 各自正确，stdout 唯一一行 `<port>...</port>`，其他都 stderr
- [ ] `pgrep darvin-agent` 关闭后无残留

### 7.4 文档

- [ ] `README.md` "First Run" 段落 5 步走通
- [ ] `config.example.yaml` 含 `llm.anthropic_api_key` 字段 + 注释
- [ ] `config.yaml` 在 `.gitignore`
- [ ] `bin/` 在 `.gitignore`
- [ ] `sessions.db` 在 `.gitignore`

### 7.5 清理

- [ ] `src/renderer/services/mock-agent.ts` 已删除
- [ ] `grep -r mock-agent src/` 无引用
- [ ] S1-S5 内部接口未破坏（lint + type check 通过）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| Memory / Dreaming | 架构文档 §"记忆层"; M2 接入 |
| Skills / MCP | 架构文档 §"Skills" / §"MCP"; M3 接入 |
| Failover / Circuit Breaker | 架构文档 §"Failover"; M2 接入 |
| SubAgent 真实化 | 架构文档 §"SubAgent"; 中期 |
| ContextEngine 3 策略 | 架构文档 §"ContextEngine"; 中期 |
| LLM Provider 矩阵 | 8 provider 全实现; M2 |
| Reconnect / ping-pong | 长期可靠性 |
| Production packaging | electron-builder + extraResources |
| CI integration | GitHub Actions: build + smoke + integration |
| Vitest / Playwright e2e | 详细断言自动化 |
| 多 session 切换 UI | UI 增强 |
| Session 标题生成 | LLM 给最早 user message 生成标题 |
