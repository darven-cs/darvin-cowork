# Electron Runtime Client 设计文档 v2（S5）

> **Phase 3 / 6 — Electron 阶段**。把 Electron 主进程和 Go Agent 子进程接起来：`runtime/manager.ts` 真 spawn 子进程并解析 stdout 端口；`runtime/client.ts` 真 WS + JSON-RPC 2.0 envelope；`preload/index.ts` 把 S1 mock 换成真 `ipcRenderer.invoke / on`；`main/index.ts` 串 lifecycle（启动 → spawn → connect → 暴露 → 关闭 → SIGTERM）；`ChatHeader.vue` 装 runtime status badge。
>
> **v2 状态（2026-07-30）**：基于 v1 spec（`2026-07-29-electron-runtime-client-design.md`）+ 当前源码 + S4 实装落地（commit `33933cd`）审计后重写。**v1 作废**，仅历史参考。修订清单见 §0。
>
> **前置**：S1（renderer contract `darvin-api.ts`）+ S2（sessions.db）+ S3（Gateway WS+JSON-RPC）+ S4（Agent.Run + EventLedger + EventCommon）实装完成（commit `33933cd`，二进制 `bin/darvin-agent-linux-x64` 可 spawn）。
>
> **本 spec 落地后**：用户能在 DevTools console 用 `await window.darvin.prompt({content:'ping', sessionId:'default'})` 真调 Go Agent；UI 流式显示；关 Electron 主进程 ≤ 3s 内子进程 graceful shutdown。

---

## 0. 相对 v1 的修订清单（5 P0 + 5 P1）

### P0（硬冲突：v1 与当前代码或 S4 落地直接矛盾，落地即报错或跑偏）

| # | v1 描述 | v2 修正 |
|---|---------|---------|
| **P0-1** | v1 §1.1 给的「当前 3 文件状态」全是错的 | `runtime/manager.ts` 只有 `resolveAgentBinaryPath()` + 占位桩；`runtime/client.ts` 只有接口骨架 + `throw`；`main/index.ts` 是 Hello World；`preload/index.ts` 仍用 `mockPrompt()`。v2 重写 §1.1 实际 gap 表格 |
| **P0-2** | v1 §1.3 「不改 S1 锁死的 `darvin-api.ts`」 vs §7 「Final: `{type:"done","usage":{...}}`」 自相矛盾 | v2 决定：**最小扩契约**——`done` 事件加可选 `usage?` 字段（`PromptTokens / CompletionTokens / TotalTokens`），S4 §9.5 follow-up 落地。S5 acceptance 不引用 `usage`（v0 renderer 忽略 usage，forward-compat 即可） |
| **P0-3** | v1 没有 `prestart` 钩——`npm start` 不会先编译 Go 二进制 | v2 加 `package.json` `prestart: npm run build:agent`；dev 用户开箱即跑。否则 dev 期每次改 Go 后必须手动 build |
| **P0-4** | v1 §FR-2 用 `ipcRenderer.sendSync('darvin:status')` + §FR-3 用 `ipcMain.on` 设 `event.returnValue` —— 是反模式，Electron 文档警告 sendSync 会阻塞 renderer thread | v2 改：`ipcMain.handle('darvin:status', () => 'online' \| 'offline' \| 'no-binary')` + preload `status(): async invoke` + 2s poll cache（`ref<DarvinRuntimeStatus>`，避免每 2s 同步阻塞） |
| **P0-5** | v1 §FR-6 描述「`src/renderer/components/ChatHeader.vue`」; v1 §6 「`App.vue` 改：组件挂 ChatHeader」—— 都跟当前代码不符 | 实际：`App.vue:1-7` 已退化成 `<template><AppShell /></template>`，`AppShell.vue` 经 `<component :is="currentView">` 渲染 `ChatView`，`ChatView` 内部才挂 `ChatHeader.vue`（路径 `components/chat/ChatHeader.vue`）。v2 描述 `ChatHeader.vue` 内部加 `<RuntimeStatusBadge />`（独立组件 + i18n），不动 AppShell/ChatView 装配层 |

### P1（设计选择：需审过）

| # | v1 描述 | v2 修正 |
|---|---------|---------|
| **P1-1** | v1 §1.3 / §5 不提 S4 实装的 3 个强约束：(a) `agent.prompt` 在 Agent 跑时会返 `ErrAgentBusy` → RPC `-32603`；(b) `sessionId` 必填或为空（自动归一 `default`）；(c) `subscribe_events` 必须用 `sessionId` = `"default"` 才匹配 EventLedger 路由 | v2 §5 加 3 条边界 + §FR-3 / §FR-4 主进程 handler 把这些错误转译成 `IPC reject(reason)`，UI 弹 toast 或 banner |
| **P1-2** | v1 §6 「删除 `src/renderer/services/mock-agent.ts`」 + §1.3 「不改 S1 契约」冲突 | v2 §6 拆开：`mock-agent.ts` 删（只有 `preload/index.ts` 引用），但 `mock-data.ts` 保留——S6 才会实装 `listSessions` / `getMessages` RPC。preload 内联 `listSessions/getMessages` 返空 `[]`（不再返 mockData）或保留占位直到 S6。v2 决定：**保留占位返空**，不返假数据，避免污染 UI |
| **P1-3** | v1 §1.3 说 status 是 `'online' \| 'offline' \| 'no-binary'`，但 `src/shared/darvin-api.ts:12` 实际声明 `'ready' \| 'offline' \| 'no-binary' \| 'online'` 4 值，多了死的 `'ready'` 分支 | v2 扩契约保留 `'ready'`（v0 不发；远期 supervisor 用，子进程不仅「在跑」且「in-flight 队列空」时返）；v0 renderer / preload / badge 只认 `'online' \| 'offline' \| 'no-binary'` 三态，多余分支当 noop |
| **P1-4** | v1 §7 验收「WS 帧序列 raw 可读：`Final: {type:"done","usage":{...}}`」 —— 但 S4 v2 spec §4.2.4 / §9.5 明确把 `done.usage` 扩列为 S5 范围 | v2 §7 验收**不**引用 usage shape（够写 spec §0 P0-2 fix 的效果落到代码里被验收）；改成「`done` event 至少含 `messageId`，可含 `usage`，renderer 不消费 usage」 |
| **P1-5** | v1 §4.3.5 复用 S1 签名，但 `S5` 调用顺序：先 `npm start` → 启动 Electron 1s 后才能 DevTools console 调 prompt，无状态提示 | v2 加：badge 的 v0 文案分三态——`Runtime: ready`（绿）/ `Runtime: offline`（amber）/ `Runtime: no-binary`（红）。badge 通过 `runtimeReady` / `clientConnected` / `mgrPID` 三源 derive，单一函数 `computeStatus()`。事件不发到 renderer（v0 简化）；2s 轮询足够 |

### 已知非问题（v1 描述正确，v2 仅微调）

- v1 §FR-1 `RuntimeMgr` 类用 `EventEmitter` + `resolve()` / `reject()` 处理 port parse + 5s 超时 + SIGTERM/SIGKILL 兜底 —— **设计正确**，v2 保留 + 把 `resolve/reject` 闭包改成 signal-like（`PendingPromise` 内部 struct），允许 `stop()` 期间 resolve cancel
- v1 §FR-1.2 AgentClient.id 单 connection +1，pending Map 防冲突 —— **设计正确**，v2 保留
- v1 §FR-5 `before-quit` 是唯一保证 graceful shutdown 钩子 —— **设计正确**，v2 保留 + 加 `shuttingDown` 标志位幂等
- v1 §4.2 启动时序图 —— **设计正确**，v2 保留
- v1 §5 边界 18 条 —— v2 重排后保留 16 条，丢弃 2 条（重复「WS close 后 reject pending」/「timeout 内多次 reset」）

---

## 1. 概述

### 1.1 实际 gap（基于当前源码 + S4 实装）

| 文件 | 当前状态 | v2 S5 落地 |
|------|---------|-----------|
| `src/main/runtime/manager.ts` | 只有 `resolveAgentBinaryPath()` + 占位函数 `startAgentRuntime()`（`console.log` placeholder，S3 阶段原地放） | 改写为 `RuntimeMgr` 类：spawn + stdout port parse + `stop()` SIGTERM/SIGKILL 兜底 + `EventEmitter` emit `'exit'` / `'spawn-failed'`。`resolveAgentBinaryPath()` 保留 |
| `src/main/runtime/client.ts` | 只有 `interface AgentClient` 骨架 + `createAgentClient()` 返 `throw new Error('未实现')` | 改写为 `AgentClient` 类：`connect(port)` WS + reconnect-once（v0：`offline` 后**不**自动重连，`manager.on('exit')` 触发 disconnect）+ `request<T>(method, params) → Promise<T>` id-mux + `onEvent(cb) → unsubscribe` |
| `src/preload/index.ts` | 还是 S1 桩：`pump()` 内直接调 `mockPrompt()`，events 走 `EventTarget` 派发；`abort / listSessions / getMessages` 全走 `mock-data.ts` 占位 | 改写：保留 `DarvinApi` 签名；实现走 `ipcRenderer.invoke('darvin:prompt', req)` + `ipcRenderer.on('darvin:event', cb)`；`listSessions / getMessages` 保留**inline 返回 `[]`**（直到 S6） |
| `src/main/index.ts` | Hello World：`app.on('ready')` 调 `installAppMenu()` + `createWindow()` | 改写：bootstrap 时序 `whenReady → mgr.start() → client.connect → createWindow`；加 `ipcMain.handle('darvin:prompt/abort/status')`；加 `app.on('before-quit')` graceful disconnect+stop |
| `package.json` | `dependencies: { electron-squirrel-startup }` | 加 `"ws": "^8.18"` 到 deps；加 `"prestart": "npm run build:agent"` |
| `src/renderer/components/chat/ChatHeader.vue` | 只有 title + 主题切换 + side-panel 开关 | 改：在右侧加 `<RuntimeStatusBadge />`（独立组件），由 `AppShell.vue` 同级 import |
| `src/renderer/components/runtime/RuntimeStatusBadge.vue` 🆕 | 不存在 | 新增独立组件；status 来自由 `preload.status()` 装的 reactive ref |
| `src/renderer/services/mock-agent.ts` | 仍被 `preload/index.ts` 直接 `import { mockPrompt }`；只有 `mockPrompt` 函数 + `streamEvents` generator + 字符切分 | 删除（dead code，preload 不再 import） |
| `src/renderer/services/mock-data.ts` | 仍被 `preload/index.ts` `import { mockMessages, mockSessions }`；被 `AppShell.vue` 直接 `import` 用作 session list 兜底 | **保留**——S6 才实装 `listSessions/getMessages` RPC。v2 S5 把 `preload` 对 `listSessions/getMessages` 改为返 `[]`（占位 until S6） |
| `docs/系统架构.md` §三层通信矩阵 / §数据流向图 | 已正确描述 Electron↔Agent 用 WebSocket JSON-RPC | 不改 |

### 1.2 目标

- `src/main/runtime/manager.ts` 实装：`RuntimeMgr` class，spawn + stdout port 解析 + SIGTERM/SIGKILL + EventEmitter
- `src/main/runtime/client.ts` 实装：`AgentClient` class，WS + JSON-RPC 2.0 envelope + id-mux + 通知 fanout
- `src/preload/index.ts` 真接 `ipcRenderer.invoke / on`（保持 S1 DarvinApi 签名）
- `src/main/index.ts` bootstrap 时序：whenReady → mgr.start → client.connect → createWindow → ipcMain.handle 三件套 → before-quit graceful
- `package.json` 加 `ws ^8.18` + `prestart` 钩
- `ChatHeader.vue` 加 `<RuntimeStatusBadge />`（独立组件）
- 删除 `mock-agent.ts`（`mock-data.ts` 留 S6 处理）

### 1.3 非目标

- **不**做 WS reconnect / ping-pong 容错（v0 简化：子进程死了 badge 转 offline；用户重启 Electron）
- **不**做 spawn 超时优化（v0 信任 ms 级启动 + 5s 兜底；超过即 reject）
- **不**做 privileged-bundle / sandbox / code signing（远期 spec）
- **不**做 supervisor / sub-process 自动重启
- **不**做 multi-window 多 user 隔离（v0 全局 fanout）
- **不**改 S1 `DarvinApi` 接口签名（仅替换 `prompt / abort / onEvent / status` 实现 + 删 `listSessions / getMessages` 内联 mock-data 引用，改返空 `[]`）
- **不**做 production `extraResources` 打包（dev 路径即可；`path.join(process.resourcesPath, 'bin', name)` 留 hook）
- **不**动 `AppShell.vue` 装配层；badge 仅是 ChatHeader 内嵌独立组件

### 1.4 前置依赖（S4 实装 API 表面）

```go
// darvin-agent 子进程 stdout 输出格式（main.go:171-185）
// 唯一一行:
<port>NNNNN</port>

// WebSocket 路径（S3 服务器）
ws://localhost:{port}/ws

// JSON-RPC 2.0 envelope
{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"...","sessionId":"..."}}
{"jsonrpc":"2.0","id":"1","result":{"sessionId":"default","messageId":"<21>"}}
{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","messageId":"...","delta":"..."}}

// S4 v0 强约束:
- sessionId 必填或为空（空归一 "default"）
- 非空非 "default" → -32602 "session not active"
- agent.busy → -32603 "loop prompt"
- subscribe_events 必须用 sessionId="default"
// S5 §1.3 P1-1 必须转译这些到 IPC reject
```

```ts
// src/shared/darvin-api.ts 当前 DarvinApi
interface DarvinApi {
  prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse>
  abort(sessionId: string): Promise<DarvinAbortResponse>
  listSessions(): Promise<DarvinListSessionsResponse>
  getMessages(sessionId: string): Promise<DarvinGetMessagesResponse>
  onEvent(handler: (e: DarvinEvent) => void): () => void
  status(): DarvinRuntimeStatus  // v2 改 async（P0-4）
}
```

---

## 2. 用户场景

### 场景 1：`npm start` 启动后端到端通

**Given**：S4 已 commit（`33933cd`）；`bin/darvin-agent-*` 已 build（`npm run build:agent`）。
**When**：开发者 `npm start`（electron-forge 启动主进程）。
**Then** 时序：
1. `prestart` 钩自动 `node scripts/build-go.js`（如有改动）→ `bin/darvin-agent-linux-x64` 更新
2. Electron 主进程 `app.whenReady` 后调 `mgr.start()` spawn 子进程
3. 子进程 stdout 累积直到 `<port>NNNNN</port>` 解析（最长 5s）
4. `AgentClient` 用 `ws://localhost:{port}/ws` 建连
5. `createWindow()` 创建 BrowserWindow + DevTools
6. 用户在 DevTools console 输入 `await window.darvin.prompt({content:'ping', sessionId:'default'})` → 收到 `{sessionId:'default', messageId:'<21>'}`
7. 后台：S4 `Agent.Run` → LLM 流 → WS notification → renderer `useMessages.appendEvent()` 被调

### 场景 2：UI 流式追加

**Given**：场景 1 已通；UI 在 chat view。
**When**：用户在 ChatView 输入框输入 "ping" 并 send（自动 `sessionId = useSession.currentSessionId`）。
**Then** 消息列表显示 user 消息（右侧），assistant 消息逐字符从左到右出现（每条 `text_delta` appendEvent 一次），最后 `done` 出现后定型 `done: true`。
**And** 端到端延迟（send → done）< 10s（Anthropic 流式）。

### 场景 3：Electron 退出触发 graceful shutdown

**Given**：场景 1 已通；子进程在跑。
**When**：用户关 Electron 主窗口（macOS 不退；Win/Linux 全关触发 `app.quit`）。
**Then**：
1. `app.on('before-quit')` 触发 `shuttingDown=true` + `client.disconnect()` + `mgr.stop()`
2. `mgr.stop()` 转发 SIGTERM
3. Go S4 graceful shutdown 路径在 ≤ 3s 内走完 4 步：`gs.Shutdown → Agent.Abort → sub.Unsubscribe → sqliteStore.Close`
4. 主进程退出码 0
5. `pgrep darvin-agent` 无残留

### 场景 4：Go 二进制缺失（dev 忘 build）

**Given**：`bin/darvin-agent-*` 不存在。
**When**：`npm start`。
**Then**：
1. `prestart` 跑 `build-go.js` 重新生成
2. 若 build 失败：主进程 stderr 警告「darvin-agent 二进制不存在」+ 不 block createWindow
3. BrowserWindow 仍打开，badge 显示 `Runtime: no-binary`（红）
4. `window.darvin.status()` 返 `'no-binary'`
5. `window.darvin.prompt(...)` reject `'agent offline'`

### 场景 5：子进程 crash / 异常退出

**Given**：子进程已启动 + WS 已连。
**When**：Go panic / `kill -9`。
**Then**：
1. `mgr.on('exit')` 触发
2. 主进程 stderr 打印 `[runtime] darvin-agent exited code=.../signal=KILL`
3. `client.disconnect()`（拒绝所有 pending）
4. Badge 轮询 2s 内感知 → `Runtime: offline`（amber）
5. `window.darvin.prompt(...)` reject `'agent offline'`

### 场景 6：fe↔be 协议 mismatch 防御

**Given**：S1 锁 `darvin-api.ts` union。
**When**：Go emit 一条不在 union 里的 event（如 `{type:"unknown_event"}`）。
**Then**：
1. preload `onEvent` 收到 raw JSON
2. 用 `parseDarvinEvent(raw)` 收口（v2 不引入 zod；走 `if/else if` 按 `raw.type` 分发，未匹配打 `console.warn('[darvin] 未知 event type:', raw.type)`，**不抛错**）
3. UI 继续接后续合法 event
4. 关键事件 `text_delta / done / error` 始终有强路径 + messageId 检索

### 场景 7：Agent 处于 running 期间发新 prompt

**Given**：场景 2 streaming 进行中。
**When**：用户连发第二次 prompt（按钮没禁用——v0 简化）。
**Then**：
1. Go `agent.Prompt` 返 `ErrAgentBusy`
2. Gateway `handlePrompt` 返 `{code:-32603, message:"loop prompt"}`
3. Main `ipcMain.handle('darvin:prompt')` 收到 `{code:-32603}` → reject IPC
4. preload `prompt()` reject `Error('rpc -32603: loop prompt')`
5. renderer catch → banner 提示「Agent 忙，请等待」

### 场景 8：subscribe_events 必须用 default

**Given**：场景 2 streaming 进行中。
**When**：renderer 想 subscribe 前端用 `sessionId` = useSession.currentSessionId.
**Then**（v2 由 AppShell 内置 subscribe）：
1. S4 EventLedger 路由按 `ev.Common().SessionID == "default"` 匹配
2. 若 renderer 用其他 sessionId subscribe，gateway 返 `-32602 "session not active"`
3. v2 S5 封装进 AppShell.ts onMounted 单点 fix，不暴露给 renderer

---

## 3. 功能需求

### FR-1：进程模型 `RuntimeMgr`

#### FR-1.1 `RuntimeMgr` class

`src/main/runtime/manager.ts` 整体改写为 `RuntimeMgr` 类。**保留** `resolveAgentBinaryPath()` 函数（v0 现状 OK）。

```ts
// src/main/runtime/manager.ts
import { app } from 'electron';
import { spawn, ChildProcess } from 'node:child_process';
import path from 'node:path';
import fs from 'node:fs';
import { EventEmitter } from 'node:events';

export interface ResolvedAgent {
  port: number;
  pid: number;
}

interface PendingResolve {
  resolve: (r: ResolvedAgent) => void;
  reject: (e: Error) => void;
  killTimer?: NodeJS.Timeout;
}

export class RuntimeMgr extends EventEmitter {
  private proc: ChildProcess | undefined;
  private resolvedPort: number | undefined;
  private pending: PendingResolve | undefined;
  private stdoutBuf = '';
  private stderrForward = true;

  /**
   * Spawn darvin-agent. Resolves when stdout emits <port>NNN</port>;
   * rejects after 5s if no match. Emits 'exit' / 'offline'.
   */
  start(): Promise<ResolvedAgent> {
    if (this.proc) {
      return Promise.reject(new Error('runtime: already started'));
    }
    const bin = resolveAgentBinaryPath();
    if (!bin) {
      return Promise.reject(
        new Error('darvin-agent 二进制不存在；运行 npm run build:agent'),
      );
    }

    return new Promise<ResolvedAgent>((resolve, reject) => {
      this.pending = { resolve, reject };
      this.proc = spawn(bin, [], {
        stdio: ['ignore', 'pipe', 'pipe'],
        env: { ...process.env, DARVIN_DEV: '1' },
        // detached: false (default) — 子进程跟随主进程 pgid, kill 时一并收
      });

      // 5s startup timeout
      const timer = setTimeout(() => {
        if (this.pending) {
          this.pending.reject(new Error('启动超时：5s 内未读到 <port>...</port>'));
          this.pending = undefined;
        }
        this.killIfAlive('SIGKILL');
      }, 5000);
      this.pending.killTimer = timer;

      this.proc.on('exit', (code, signal) => {
        if (this.pending) {
          // 还没成功就退 → 算启动失败
          this.pending.reject(new Error(`启动失败 exit=${code} signal=${signal}`));
          this.pending = undefined;
        }
        this.emit('exit', { code, signal });
        this.resolvedPort = undefined;
        this.proc = undefined;
      });

      this.proc.stderr?.on('data', (chunk: Buffer) => {
        if (this.stderrForward) {
          process.stderr.write(`[darvin-agent] ${chunk}`);
        }
      });

      this.proc.stdout?.on('data', (chunk: Buffer) => {
        const text = this.stdoutBuf + chunk.toString('utf8');
        const m = text.match(/<port>(\d+)<\/port>/);
        if (m) {
          this.stdoutBuf = '';
          this.resolvedPort = Number(m[1]);
          if (!this.pending || !this.proc) return;
          const pid = this.proc.pid ?? -1;
          clearTimeout(this.pending.killTimer);
          this.pending.resolve({ port: this.resolvedPort, pid });
          this.pending = undefined;
        } else {
          this.stdoutBuf = text;
        }
      });
    });
  }

  /** SIGTERM; fallback SIGKILL after 4s if still alive. */
  stop(): Promise<void> {
    if (!this.proc) return Promise.resolve();
    const proc = this.proc;
    return new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        if (proc.exitCode === null) {
          try { proc.kill('SIGKILL'); } catch { /* already exited */ }
        }
        resolve();
      }, 4000);
      proc.once('exit', () => {
        clearTimeout(t);
        resolve();
      });
      try { proc.kill('SIGTERM'); } catch { /* already exited */ }
    });
  }

  pid(): number | undefined { return this.proc?.pid; }
  isResolved(): boolean { return this.resolvedPort !== undefined; }
  port(): number | undefined { return this.resolvedPort; }
}

export function resolveAgentBinaryPath(): string | undefined {
  const { platform, arch } = process;
  const exeSuffix = platform === 'win32' ? '.exe' : '';
  const name = `darvin-agent-${platform}--${arch}${exeSuffix}`;
  let p: string;
  if (app.isPackaged) {
    p = path.join(process.resourcesPath, 'bin', name);
  } else {
    // 开发态 __dirname ≈ .vite/build/main/runtime/, 回溯四级到 repo root
    p = path.join(__dirname, '..', '..', '..', '..', 'bin', name);
  }
  return fs.existsSync(p) ? p : undefined;
}
```

**关键设计点**：
- 单一 `pending` slot（v0 简化）；不允许并发 `start()`
- 5s 启动超时：未读到 `<port>` → reject + SIGKILL 兜底
- `killTimer` 提前 clear（port 解析成功后立即清掉）
- `stderr` 透传 `[darvin-agent] ...` 前缀；用户 DevTools 看不到 Go 日志（v0 简化）

#### FR-1.2 `AgentClient` class

`src/main/runtime/client.ts` 整体改写。

```ts
// src/main/runtime/client.ts
import { WebSocket } from 'ws';
import { EventEmitter } from 'node:events';
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortResponse,
  DarvinEvent,
} from '../../shared/darvin-api';

interface Pending {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
}

type DarvinRuntimeStatus = 'ready' | 'offline' | 'no-binary' | 'online';

export class AgentClient extends EventEmitter {
  private ws: WebSocket | undefined;
  private nextId = 1;
  private pending = new Map<string, Pending>();
  private notifListeners = new Set<(e: DarvinEvent) => void>();
  private status: DarvinRuntimeStatus = 'offline';

  constructor(private opts: { logger?: { warn: (msg: string, ...a: any[]) => void } } = {}) {}

  async connect(port: number): Promise<void> {
    if (this.ws) return;
    return new Promise<void>((resolve, reject) => {
      const url = `ws://localhost:${port}/ws`;
      const ws = new WebSocket(url);
      const onError = (e: Error) => {
        ws.removeAllListeners();
        reject(e);
      };
      ws.once('open', () => {
        this.ws = ws;
        this.status = 'online';
        resolve();
      });
      ws.once('error', onError);
      ws.on('close', () => {
        const wasOpen = this.ws === ws;
        this.ws = undefined;
        if (wasOpen) {
          for (const p of this.pending.values()) {
            p.reject(new Error('ws closed'));
          }
          this.pending.clear();
          this.status = 'offline';
          this.emit('offline');
        }
      });
      ws.on('message', (data: Buffer) => {
        try {
          this.handleIncoming(JSON.parse(data.toString('utf8')));
        } catch (e) {
          this.opts.logger?.warn('[agentclient] bad json:', (e as Error).message);
        }
      });
    });
  }

  private handleIncoming(msg: any) {
    if (msg && msg.id !== undefined && msg.id !== null) {
      // response
      const key = String(msg.id);
      const p = this.pending.get(key);
      if (!p) return;
      this.pending.delete(key);
      if (msg.error) {
        p.reject(new Error(`rpc ${msg.error.code}: ${msg.error.message ?? ''}`));
      } else {
        p.resolve(msg.result);
      }
    } else if (msg && msg.method === 'agent.event' && msg.params) {
      // notification fanout
      const rawParams = msg.params as Record<string, unknown>;
      const ev = parseDarvinEvent(rawParams);
      if (ev) {
        for (const cb of this.notifListeners) {
          try { cb(ev); } catch { /* swallow */ }
        }
      } else {
        this.opts.logger?.warn('[agentclient] 未知 event type:', rawParams.type);
      }
    }
  }

  async request<T>(method: string, params?: unknown): Promise<T> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('agent offline'));
    }
    const id = String(this.nextId++);
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as any, reject });
      this.ws!.send(JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} }));
    });
  }

  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return this.request<DarvinPromptResponse>('agent.prompt', req);
  }
  async abort(req: { sessionId: string }): Promise<DarvinAbortResponse> {
    return this.request<DarvinAbortResponse>('agent.abort', req);
  }

  onEvent(cb: (e: DarvinEvent) => void): () => void {
    this.notifListeners.add(cb);
    return () => { this.notifListeners.delete(cb); };
  }

  isConnected(): boolean { return this.ws?.readyState === WebSocket.OPEN; }
  getStatus(): DarvinRuntimeStatus { return this.status; }

  async disconnect(): Promise<void> {
    if (!this.ws) return;
    const ws = this.ws;
    return new Promise<void>((resolve) => {
      if (ws.readyState === WebSocket.CLOSED) { resolve(); return; }
      ws.once('close', () => resolve());
      try { ws.close(); } catch { resolve(); }
    });
  }
}

// parseDarvinEvent 收口：未知 type 返 null；DarvinEvent union 强校验
import type { DarvinEvent as DE } from '../../shared/darvin-api';
function parseDarvinEvent(raw: Record<string, unknown>): DE | null {
  const t = raw.type;
  switch (t) {
    case 'text_delta':
    case 'thinking_delta':
    case 'tool_start':
    case 'tool_end':
    case 'done':
    case 'error':
    case 'agent_end':
      return raw as unknown as DE;  // 类型断言; 运行时已按 type 收口
    default:
      return null;
  }
}
```

**关键设计点**：
- `nextId` 单 connection 严格 +1；不复用 0
- `pending` Map 存 promise；ws close 时**全 reject** 防止 leak
- notification `method === 'agent.event'` 是 S3 约定的推送通道
- `parseDarvinEvent` 兜底未知 type，不抛错、warn 即可（容错 S4 unexpected 事件）
- `status` 状态机：`offline → online → offline → ...`；emit `'offline'` 通知主进程

---

### FR-2：preload contextBridge 真接

`src/preload/index.ts` 整体改写：

```ts
// src/preload/index.ts
import { contextBridge, ipcRenderer } from 'electron';
import type {
  DarvinApi, DarvinPromptRequest, DarvinPromptResponse,
  DarvinAbortResponse, DarvinEvent,
  DarvinListSessionsResponse, DarvinGetMessagesResponse,
} from '../shared/darvin-api';

const api: DarvinApi = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  async abort(sessionId: string): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort', { sessionId });
  },
  // listSessions / getMessages — S6 才实装真 RPC, v0 返空
  async listSessions(): Promise<DarvinListSessionsResponse> {
    return { sessions: [] };
  },
  async getMessages(_sessionId: string): Promise<DarvinGetMessagesResponse> {
    return { messages: [] };
  },
  onEvent(handler: (e: DarvinEvent) => void): () => void {
    const wrap = (_: unknown, ev: DarvinEvent) => handler(ev);
    ipcRenderer.on('darvin:event', wrap);
    return () => { ipcRenderer.off('darvin:event', wrap); };
  },
  async status(): Promise<'ready' | 'offline' | 'no-binary' | 'online'> {
    return ipcRenderer.invoke('darvin:status') as any;
  },
};

contextBridge.exposeInMainWorld('darvin', api);
```

**改动要点**：
- `DarvinApi.status` 从同步改 async（契约扩）—— §0 P0-4 fix
- `listSessions / getMessages` 改为返空 `[]`（S6 替换）—— §0 P1-2 fix
- `prompt / abort` 改 `ipcRenderer.invoke`（抛错给 renderer）
- `onEvent` 改 `ipcRenderer.on`（保留返回 unsubscribe 闭包）

> **契约变化摘要**：
> - `DarvinApi.status(): DarvinRuntimeStatus` → `DarvinApi.status(): Promise<DarvinRuntimeStatus>`
> - `listSessions / getMessages` 实现变化，签名不变（v0 返空）
> - 其余 4 项签名不变

---

### FR-3：主进程 IPC handler

`src/main/index.ts` 增量加 `ipcMain.handle` 三件套：

```ts
// src/main/index.ts (新增段落)
import { ipcMain, app, BrowserWindow } from 'electron';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';
import { installAppMenu } from './menu';

const mgr = new RuntimeMgr();
const client = new AgentClient({ logger: console });
let shuttingDown = false;

ipcMain.handle('darvin:prompt', async (_e, req) => client.prompt(req));
ipcMain.handle('darvin:abort', async (_e, req) => client.abort(req));
ipcMain.handle('darvin:status', () => {
  if (!mgr.isResolved() && mgr.pid() === undefined) return 'no-binary';
  if (!client.isConnected()) return 'offline';
  return 'online';
});

// notify fanout: client → 所有 BrowserWindow
client.onEvent((ev) => {
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) win.webContents.send('darvin:event', ev);
  }
});

mgr.on('exit', ({ code, signal }) => {
  console.error(`[runtime] darvin-agent exited code=${code} signal=${signal}`);
});
```

**关键设计点**：
- `darvin:status` 三态推导：`no-binary > offline > online`（前两个常因 startup 阶段混读）
- `darvin:event` 是单向推；renderer 无须 ack
- `mgr.on('exit')` 日志 + 隐式让 status 下次轮询变 offline（**不**做主动 disconnect 联动；client 自然 close 时 `onclose` handler 会清 ws，status → offline）

---

### FR-4：main bootstrap 时序

```ts
// src/main/index.ts 替换 app.on('ready', ...)
app.whenReady().then(async () => {
  installAppMenu();
  // 1. spawn 子进程 (5s timeout)
  let resolved: Awaited<ReturnType<RuntimeMgr['start']>> | null = null;
  try {
    resolved = await mgr.start();
  } catch (e) {
    console.error(`[runtime] ${(e as Error).message}`);
  }
  // 2. WS connect (失败不阻塞窗口)
  if (resolved) {
    try {
      await client.connect(resolved.port);
    } catch (e) {
      console.error(`[runtime] ws connect: ${(e as Error).message}`);
    }
  }
  // 3. 创建窗口（**先**于 ws connect —— 已异步 await 过；事件到时 window 已 ready）
  createWindow();
});
```

**关键设计点**：
- 子进程启动失败**不**block 窗口创建（场景 4：UI 仍能起，header 显示 `no-binary`）
- `client.connect` 在 `createWindow` 前 await；保证 `client.onEvent` 注册早于任何 `darvin:event` 推送
- 第三方窗口事件（DarvinEvent → `webContents.send`）在 `createWindow()` 之后触发；彼时 `BrowserWindow.getAllWindows()` 已含主窗

---

### FR-5：graceful shutdown

```ts
// src/main/index.ts
app.on('before-quit', async (e) => {
  if (shuttingDown) return;
  e.preventDefault();
  shuttingDown = true;
  try { await client.disconnect(); } catch { /* swallow */ }
  try { await mgr.stop(); } catch { /* swallow */ }
  app.quit();  // 二次 quit 才会真正退（before-quit 已被 preventDefault）
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
```

**关键设计点**：
- `before-quit` 唯一保证 graceful 的钩子；先 disconnect WS，再 SIGTERM
- `shuttingDown` 标志位幂等（多次触发只走一次路径）
- macOS 保留 `window-all-closed` 不退（标准 Electron 行为）

---

### FR-6：renderer 状态指示 `RuntimeStatusBadge`

**新组件** `src/renderer/components/runtime/RuntimeStatusBadge.vue`：

```vue
<template>
  <span :class="['badge', `badge--${status}`]" :title="title">
    <span class="dot"></span>
    <span class="label">{{ label }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { t } from '../../services/i18n';

const status = ref<'online' | 'offline' | 'no-binary' | 'ready'>('offline');
let pollTimer: number | undefined;

onMounted(async () => {
  // 初次同步拉一次
  try { status.value = await window.darvin.status() as any; } catch {}
  // 2s 轮询（v0 简化；远期 supervisor 推送）
  pollTimer = window.setInterval(async () => {
    try {
      status.value = await window.darvin.status() as any;
    } catch {
      status.value = 'offline';
    }
  }, 2000);
});
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer);
});

const label = computed(() => {
  switch (status.value) {
    case 'online':    return t('runtime.badge.online');
    case 'offline':   return t('runtime.badge.offline');
    case 'no-binary': return t('runtime.badge.no_binary');
    case 'ready':     return t('runtime.badge.ready');
  }
});
const title = computed(() => label.value);
</script>

<style scoped>
.badge { display: inline-flex; align-items: center; gap: 6px; padding: 2px 8px;
  border-radius: 999px; font-size: 11px; font-weight: 500; }
.dot { width: 6px; height: 6px; border-radius: 50%; }
.badge--online    .dot { background: #22c55e; }
.badge--online    { background: rgba(34, 197, 94, 0.1); color: #16a34a; }
.badge--offline   .dot { background: #f59e0b; }
.badge--offline   { background: rgba(245, 158, 11, 0.1); color: #d97706; }
.badge--no-binary .dot { background: #ef4444; }
.badge--no-binary { background: rgba(239, 68, 68, 0.1); color: #dc2626; }
.badge--ready     .dot { background: #3b82f6; }
.badge--ready     { background: rgba(59, 130, 246, 0.1); color: #2563eb; }
</style>
```

**挂载到 `src/renderer/components/chat/ChatHeader.vue`**：
```vue
<!-- ChatHeader.vue 右侧 IconButton 旁加 -->
<RuntimeStatusBadge />
```
import 行加 `import RuntimeStatusBadge from '../runtime/RuntimeStatusBadge.vue';`

**i18n key** 加进 `src/renderer/services/i18n.ts`：
```ts
'runtime.badge.online': 'Runtime: ready',
'runtime.badge.offline': 'Runtime: offline',
'runtime.badge.no_binary': 'Runtime: no binary',
'runtime.badge.ready': 'Runtime: ready',
```

---

### FR-7：`package.json` 依赖

```jsonc
{
  "scripts": {
    "prestart": "npm run build:agent",  // 🆕
    "start": "electron-forge start",
    "build:agent": "node scripts/build-go.js",
    "premake": "npm run build:agent",
    // ...
  },
  "dependencies": {
    "electron-squirrel-startup": "^1.0.1",
    "ws": "^8.18"  // 🆕
  }
}
```

**`ws` 选型理由**：Node 22+ 内置全局 `WebSocket`，Electron 43 跑在 Node 20（embedded）。为 v0 可移植性 + 类型明确，加 `ws ^8.18`；v8 协议版本与 gorilla/websocket 上游一致。

---

### FR-8：`mock-agent.ts` 删除

删除 `src/renderer/services/mock-agent.ts`。仅 `preload/index.ts` 引用，§FR-2 不再 import。`mock-data.ts` 保留，S6 才动。

---

## 4. 实现方案

### 4.1 目录结构（diff 视角）

```
src/
├── main/
│   ├── index.ts                          # 🆕 改：bootstrap + ipc handlers + before-quit
│   ├── menu.ts                           # 不改
│   └── runtime/
│       ├── manager.ts                    # 🆕 改：实装 RuntimeMgr class
│       └── client.ts                     # 🆕 改：实装 AgentClient class
├── preload/
│   └── index.ts                          # 🆕 改：contextBridge 真接 ipcRenderer
├── renderer/
│   ├── components/
│   │   ├── chat/ChatHeader.vue           # 🆕 改：加 <RuntimeStatusBadge />
│   │   └── runtime/RuntimeStatusBadge.vue # 🆕 FR-6
│   ├── services/
│   │   ├── mock-agent.ts                 # ❌ 删除
│   │   ├── mock-data.ts                  # 不改(S6 才动)
│   │   └── i18n.ts                       # 🆕 加 4 个 runtime.badge.* key
│   ├── App.vue                           # 不改(已退化为 <AppShell />)
│   └── layout/AppShell.vue               # 不改(已装配 ChatHeader)
├── shared/
│   └── darvin-api.ts                     # 🆕 改：status() 改 async
└── package.json                          # 🆕 改：prestart + ws dep
```

### 4.2 关键决策

#### 4.2.1 `status()` 改 async 的影响半径

- TS 契约：`DarvinApi.status(): Promise<DarvinRuntimeStatus>` —— 改 1 行
- preload：返 `Promise<...>`（FR-2）
- main：`ipcMain.handle('darvin:status', () => '...')`（sync 返字符串，IPC 自动 wrap 成 Promise）
- 旧调用方：renderer 的 ChatHeader polling —— v2 已配套改 async 版

#### 4.2.2 `listSessions / getMessages` v0 返空

- 避免返 mock-data 假数据（否则 UI 显示假 session list 误导）
- AppShell.vue 当前 `import { mockMessages }` 直接 `messages.list.value.push(...)`——v2 把 AppShell 的初始化逻辑改成 onMounted 调 `window.darvin.listSessions()` + `getMessages(sessionId)`，**不**走 mock-data（**v2 副作用**：AppShell 也需要改）

**v2 决策**：AppShell 改动属本 spec 范围。spec §6 涉及文件加 `src/renderer/layout/AppShell.vue`。

```vue
<!-- AppShell.vue onMounted 段 -->
<script setup lang="ts">
import { onMounted } from 'vue';
import { useSession } from '../composables/useSession';
import { useMessages } from '../composables/useMessages';
const session = useSession();
const messages = useMessages();

onMounted(async () => {
  window.darvin.onEvent((e) => messages.appendEvent(e));
  // 加载 sessions 列表（v0 返空 → useSession 保持空 list，符合 S6 契约）
  const r = await window.darvin.listSessions();
  session.sessions.value = r.sessions;
  // 加载当前 session messages（v0 返空 → messages.reset 并加 user/assistant 占位占位略）
  const m = await window.darvin.getMessages(session.currentSessionId.value);
  messages.reset();
  for (const msg of m.messages) {
    messages.list.value.push(msg);
  }
});
</script>
```

#### 4.2.3 `parseDarvinEvent` 收口位置

- 选 main 端（AgentClient.handleIncoming）而非 preload —— 因为 notification 已脱离 WS 层；主进程是 boundary
- 不放独立文件——单函数内联在 `client.ts` 底部（避免为 1 个 if-else 新开文件）

#### 4.2.4 启动时序图

```
npm start
  └─ prestart: build-go.js            # 🆕
  └─ electron-forge spawn main
       └─ src/main/index.ts
            ├─ app.whenReady()
            ├─ mgr.start()          ── spawn darvin-agent ──┐
            │      └─ stdout parse <port>N</port>           │
            ├─ client.connect(port) ── WS upgrade ──────────┤
            │                                              │
            │         darvin-agent (Go)                     │
            │         ├─ cmd/app/main.go ──────────────────┤
            │         ├─ gateway.Start ───────────────────┤
            │         │   └─ bind :0                       │
            │         │   └─ stdout <port>...              │
            │         ├─ acp.Loop                           │
            │         ├─ agent.runtime                      │
            │         └─ sessions.db (S2)                   │
            │                                              │
            ├─ ipcMain.handle('darvin:prompt/abort/status') │
            ├─ client.onEvent → webContents.send            │
            ├─ mgr.on('exit') 日志                          │
            ├─ createWindow()                               │
            └─ DevTools open                                │
```

#### 4.2.5 关闭时序图

```
user 关主窗口 (Linux/Windows) / Cmd+Q (macOS 视实现)
  └─ app.on('before-quit')
       ├─ shuttingDown = true
       ├─ client.disconnect()  ── WS close
       ├─ mgr.stop()          ── SIGTERM → darvin-agent
       │                        ├─ signal.NotifyContext cancel
       │                        ├─ Agent.Abort
       │                        ├─ WS server Shutdown
       │                        ├─ DB close
       │                        └─ os.Exit(0)
       └─ app.quit()           ── main exit
```

#### 4.2.6 不要 supervisor

- 子进程死了不自动重启——v0 简化
- 远期 spec 加 supervisor（pm2-like）

#### 4.2.7 `DARVIN_DEV: '1'` 环境变量

- spawn 时注入；Go 端 `main.go:73+` 检测用于决定 config 路径
- v0 简单，dev = 环境变量；prod = packaged resources

### 4.3 涉及 S5 的 S4 契约边界

- S4 v2 spec §9.5 把 `done.usage` 列为 S5 范围。v2 **不做** `done.usage` 扩（precedent §0 P0-2 自洽：验收不引用 usage，但 §0 列了「最小扩契约」——v2 S5 仅文档化**预留**，不实施 TS 字段）
- S4 v2 spec §9.4 `error notification` 字段：`{type:"error", messageId, message}` —— v2 TS DarvinEvent union 已经匹配（line 43）

---

## 5. 边界情况

| 场景 | 处理 |
|------|------|
| `bin/darvin-agent-*` 不存在 | `mgr.start()` 立即 reject；`ipcMain.handle('darvin:status')` 返 `'no-binary'`；badge 红；UI 提示 |
| 子进程 5s 内没 `<port>` | `mgr.start()` reject「启动超时」；mgr SIGKILL 兜底；status → `no-binary` |
| WS connect 失败（端口被占 / Go panic） | `client.connect()` reject；status → `'offline'`；当前 in-flight request reject |
| 子进程 exit code != 0 | `mgr.on('exit')` stderr 日志；`client` 自然 `onclose` → 推到 `'offline'` |
| WS close（子进程死） | `client.onclose` 清 pending + emit `'offline'`；后续 `request()` 立即 reject `'agent offline'` |
| stdout chunk 跨 buffer（`"abc<port>12"` + `"34</port>def"`） | `stdoutBuf` 累积 + 每次 .match() |
| 同 WS 多 messageId 推送 | `pending` Map + 严格 `+1` |
| `prompt` 在 Agent busy 期间 | Go 返 `{code:-32603}` → IPC reject；UI banner |
| `prompt` 用未知 sessionId | Go 返 `{code:-32602 "session not active"}` → IPC reject；UI 提示切到 default |
| `subscribe_events` 用未知 sessionId | Go 返 `{code:-32602}`；v2 AppShell 内置单点强制 sessionId=default |
| `onEvent` listener 抛错 | try/catch 吞掉；防止 cascade |
| `before-quit` 二次触发 | `shuttingDown` 标志位幂等 |
| 子进程在 listen 阶段收 SIGTERM | S4 graceful shutdown 走完 ≤3s；mgr.stop() 4s SIGKILL 兜底 |
| `darvin:status` 跟 ws 状态不一致（race） | `mgr.isResolved()` + `client.isConnected()` 实时读；不缓存到 main |
| `listSessions` / `getMessages` 在 S5 返空 | UI 显示空 sessions；user 加 user 消息后 listMessages 仍返空（v0 UI 不缓存）——S6 替换 |
| Electron 主进程 `darvin:event` 在 window 还没 ready 时到达 | `BrowserWindow.getAllWindows()` 检查 `!win.isDestroyed()`；丢弃 |
| `parseDarvinEvent` 遇到 `null`/非对象 msg.params | `warn` + 跳过；不 push 给 renderer |
| AppShell `onMounted` 跑 `listSessions/getMessages` 时 agent 未在线 | `ipcRenderer.invoke` reject → UI catch 静默（status badge 早已显 `offline`） |
| User 在 S5 完成前用旧 `npm start` | 旧版无 status badge，window OK；新 `prestart` 钩可能误删旧 bin —— `build-go.js` 是 idempotent，无影响 |
| `npm start` 后 `npm start` 再起（重合） | `prestart` 重新 build；二启时 mgr 子进程跟 first-run 独立（Electron 重启了） |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/main/runtime/manager.ts` | 🆕 改：实装 RuntimeMgr class + 保留 resolveAgentBinaryPath |
| `src/main/runtime/client.ts` | 🆕 改：实装 AgentClient class（WS + JSON-RPC + parseDarvinEvent）|
| `src/preload/index.ts` | 🆕 改：真接 ipcRenderer.invoke / on；listSessions/getMessages 改返空 |
| `src/main/index.ts` | 🆕 改：bootstrap 时序 + ipcMain.handle + before-quit |
| `src/renderer/components/chat/ChatHeader.vue` | 🆕 改：加 `<RuntimeStatusBadge />` |
| `src/renderer/components/runtime/RuntimeStatusBadge.vue` | 🆕 新组件（独立文件） |
| `src/renderer/services/mock-agent.ts` | ❌ 删除 |
| `src/renderer/services/i18n.ts` | 🆕 加 `runtime.badge.*` 4 key |
| `src/renderer/layout/AppShell.vue` | 🆕 改：onMounted 改走 `window.darvin.listSessions/getMessages`，**不**直接 import mock-data |
| `src/shared/darvin-api.ts` | 🆕 改：`DarvinApi.status(): Promise<DarvinRuntimeStatus>` |
| `src/darvin-agent/.gitignore` | 不改（已含 `bin/darvin-agent-*` + `!bin/.gitkeep`） |
| `package.json` | 🆕 加 `"prestart": "npm run build:agent"` + `"ws": "^8.18"` |

**不修改**：
- `src/main/menu.ts`（已 OK）
- `src/renderer/App.vue`（已退化为 `<AppShell />`，v2 不动）
- `src/renderer/components/chat/{Composer,ChatPane,MessageItem,MessageList,StreamingText}.vue`（v0 不消费真 flow）
- `src/renderer/services/mock-data.ts`（S6 才动）
- `src/renderer/composables/{useMessages,useSession}.ts`（已 OK）
- `scripts/build-go.js`（S3 已 OK）
- `config.yaml` / `darvin-agent` 全部 Go 代码（S4 已 OK）

---

## 7. 验收标准

> 勾选状态见 §9.10。`[x]` = 本轮实测通过；`[~]` = 以 smoke 脚本等价验证（未走 DevTools）；`[ ]` = 未覆盖，留 S6。

### 7.1 构建 / 静态

- [x] `npm run lint` 通过（`tsc --noEmit` + ESLint）
- [x] `npm run build:agent` exit 0
- [~] Electron 启动无 DevTools 报错（主进程侧无报错；DevTools console 未逐条看）

### 7.2 端到端 smoke

- [x] `npm start` 后 prestart 自动 build（agent 改动后第一次会触发）
- [x] Electron 主进程 stderr 含 `agent initialized` + `gateway listening` + `application started successfully`（从子进程 stderr `[darvin-agent] ...` 前缀转发可见）
- [~] `await window.darvin.status()` 返 `'online'`（主进程 connect 成功即 online；未走 DevTools）
- [ ] Badge 显示 `Runtime: ready`（绿色 dot）
- [x] `agent.prompt {content:'ping', sessionId:'default'}` 返 `{sessionId:'default', messageId:'<21>'}`（实测 `mdFQGjSYtWrr1o6uqjoKU`）
- [~] 后续 ≤ 3s 收到 notification 流（实测 6 条：`run_start` / `prompt_received` / `turn_start` / `llm_start` / `error` / `agent_end`；无凭据故未见 `text_delta` / `done`，见 §9.9）：
  - `{type:'thinking_delta'?, messageId, delta}`（如果有）
  - `{type:'text_delta', messageId, delta}` ×N
  - `{type:'done', messageId}`
  - `{type:'agent_end'}` ← 实测收到
- [ ] ChatView 输入 "ping" → send → assistant 消息逐字符追加；`done` 出现后定型
- [~] DevTools Network → WS → Messages 可见原 frame（smoke 脚本已在 socket 层核对 request / response / notification 三类帧）：

  - Request：`{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"ping","sessionId":"default"}}`
  - Response：`{"jsonrpc":"2.0","id":"1","result":{"sessionId":"default","messageId":"<21>"}}`
  - Notification：`{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","messageId":"<21>","delta":"..."}}`
  - Final：`{"jsonrpc":"2.0","method":"agent.event","params":{"type":"done","messageId":"<21>"}}`（**不**要求 `usage` 字段——见 §0 P0-2）

### 7.3 优雅关闭

- [x] 关 Electron（`app.before-quit`）→ client disconnect + SIGTERM → 子进程 stderr 末条 `graceful shutdown complete` → 子进程 `exited code=0` → Electron 退出
- [x] `pgrep darvin-agent` 无本轮残留（另有 6 个 15:46–15:48 的 S4 期孤儿进程，与 S5 无关）
- [x] 整体 ≤ 3s（smoke 实测 SIGTERM → exit 4ms）

### 7.4 错误路径

- [ ] `rm bin/darvin-agent-*` + `npm start`：prestart rebuild 成功；如 revert build 成功 → 子进程起 → badge 绿
- [ ] 故意破坏 `bin/darvin-agent-*`（写空文件 + `chmod -x`）：prestart build 再次覆盖；测成功后破坏保留测试：
  - `mv bin bin.bak` → `npm start`：主进程 stderr 报错 + badge 红 `no-binary` + window 仍打开
- [ ] 子进程 `kill -9`：主进程 stderr「exited code=null signal=KILL」+ ≤ 2s badge 转 amber `offline`
- [ ] `window.darvin.prompt()` 在 offline 态：catch `'agent offline'`

### 7.5 防御

- [ ] `window.darvin.onEvent` cb 在 assistant message 流式完成时**不**抛错（即使 backend 发奇怪事件）
- [~] 非 union 事件 → 不崩：实测 4 类生命周期事件走 null 分支被丢弃，UI / 主进程均正常（真未知 type 仍 warn，见 §9.9）
- [ ] `agent.prompt` 第二次（在 Agent busy 期间）：catch `-32603 loop prompt` + UI banner
- [x] `agent.prompt {sessionId:"unknown"}`：catch `-32602 session not active`（smoke 实测）


---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S6** agent-e2e-integration | 多 Agent 多 session + `store.Save` 落 messages + 重启可见 + `agent.list_sessions` / `agent.get_messages` RPC（替换 S5 的 inline-空返） |
| WS reconnect / ping-pong | v0 简化版（badge polling）；远期自动重连 |
| Runtime supervisor | pm2-like: 子进程死了自动重启 |
| WSS / TLS | 远期 |
| 多 window 多用户隔离 | 每 window 独立 subscription；远期 |
| Production `extraResources` | 打包期把 darvin-agent 二进制塞到 `resources/bin/`（已留 hook） |
| Code signing / sandbox | 远期 |
| IPC schema 强校验（zod / protobuf） | 远期（当前 `parseDarvinEvent` if-else 够用） |
| `done.usage` 真正落地 | S5 仅留 spec 占位未实施；S8+ 视需求打开 |

---

## 9. 实现偏差（落地后追加）

实现过程中发现 spec 有 10 处需修订（§9.1–§9.4 为落地期发现，§9.5–§9.10 为构建 / 实测期发现），代码按「能真正跑通」的读法落地，spec 已同步修正。

### 9.1 启动时 `chat/ChatHeader.vue` 路径找不到组件（已修）

**问题**：spec §6 描述 `src/renderer/components/ChatHeader.vue`，实际路径是 `src/renderer/components/chat/ChatHeader.vue`。两 import 路径都不通。

**落地**：所有引用改 `components/chat/ChatHeader.vue`；新组件 `components/runtime/RuntimeStatusBadge.vue` 同级目录。

### 9.2 `prestart` 钩导致 dev 期 Go 改动必重新 link（acceptable）

**现象**：dev 改 `.go` 后 `npm start` 会自动 rebuild（~3-5s），跟改 TS 即热重载不一致。

**原因**：`prestart` 强制 rebuild。

**判断**：可接受；改 `.go` 是少频路径，热重载为此优化会让 TS 体验复杂化。如果未来成为瓶颈，再加 `--watch` 模式 + `nodemon` / `entr` 触发局部 rebuild。

### 9.3 旧 v0 preload `pump()` 用 `EventTarget` 实现的 callback model 不能再用

**现象**：旧 `preload/index.ts` 用 `EventTarget.dispatchEvent` + `addEventListener` 实现 mock 流式。

**落地**：全切到 `ipcRenderer.invoke / on`；删除整个 `eventTarget / SUBS / pump` 子结构。

### 9.4 spec §1.3 「不改 S1 契约」细化

**实施结果**：v2 实际**改**了 1 行契约：`status()` 从 sync 改 async。理由是 `sendSync` 是反模式（§0 P0-4）。其余 5 项签名 / union 全部未动。在 diff summary 标注。

### 9.5 `vite.main.config.ts` 没 external node 内置模块（潜在 bug，已修）

**现象**：改完 `manager.ts` / `client.ts` 后构建直接失败：

```
"EventEmitter" is not exported by "__vite-browser-external"
```

**根因**：vite lib 模式默认把 `node:fs` / `node:path` / `node:child_process` / `node:events` 当浏览器环境处理，替换成 `__vite-browser-external` shim。S5 之前 main 只用 `import path from 'node:path'` 这类 **default import**，shim 会静默产出一个「调用即抛」的代理对象——构建过得去，运行时才炸；S5 引入 `import { EventEmitter } from 'node:events'` 这种 **named import**，rollup 立刻报错，把这个潜在 bug 提前暴露了。

**落地**：

```ts
import { builtinModules } from 'node:module';
const nodeBuiltins = [...builtinModules, ...builtinModules.map((m) => `node:${m}`)];
// rollupOptions.external: ['electron', 'electron-squirrel-startup', 'ws', ...nodeBuiltins]
// build.target: 'node20'
```

`ws` 一并 external：它对 `bufferutil` / `utf-8-validate` 做可选 require，打进 bundle 会让 rollup 解析失败；作为 `dependencies` 随 asar 分发。

**验证**：产物里可见真 `require("node:child_process")` / `require("node:events")` / `require("ws")`。

### 9.6 dev 期必须注入 `DARVIN_CONFIG`（spec 未提，已补）

**现象**：`mgr.start()` 一律超时失败，子进程 stderr：

```
failed to load config: open config.yaml: no such file or directory
```

**根因**：Go `configPath()` 查找顺序是 `DARVIN_CONFIG` → `<exe-dir>/config.yaml` → `./config.yaml`。dev 期二进制在 `bin/`、cwd 是仓库根，两处都没有配置文件。

**落地**：`manager.ts` 增 `resolveAgentConfigPath()`，dev 期指向 `src/darvin-agent/config.yaml` 并在 spawn 时注入 `DARVIN_CONFIG`；`app.isPackaged` 返回 `undefined` 不干预（打包态由 `<exe-dir>/config.yaml` 兜住）。

### 9.7 `AgentClient.connect()` 必须补 `agent.subscribe_events`（spec FR-1.2 漏了）

**问题**：spec 的 `AgentClient` 只描述了 open socket + `prompt` / `abort`。但 S4 的 `EventLedger` 按 sessionId 维护订阅集合，**没订阅的连接一条 notification 都收不到**——照 spec 落地会得到一个「prompt 成功但 UI 永远不动」的静默故障。

**落地**：`connect()` = openSocket + `request('agent.subscribe_events', { sessionId: 'default' })`，且 subscribe 失败即视为连接失败（没有事件流的连接对 renderer 无用）。smoke 实测返回 `{"subscribed":true}`。

### 9.8 Badge 复用既有 i18n key，不新增 `runtime.badge.*`

**问题**：spec §FR-6 要求新增 `runtime.badge.{online,offline,no_binary}`，但 `services/i18n.ts` 里 `app.runtime.{ready,offline,no_binary}` 已存在且语义一致。

**落地**：复用既有 key，不新增。避免同义 key 双份。

### 9.9 生命周期事件需静默丢弃，不能 warn（实测后修）

**现象**：smoke 实测一轮 prompt 收到 6 条 notification：

```
run_start, prompt_received, turn_start, llm_start, error, agent_end
```

其中前 4 条不在 `DarvinEvent` union 里——Go `mapEventToTS` 只把 7 类事件映射成 renderer 形状（`text_delta` / `thinking_delta` / `done` / `agent_end` / `tool_start` / `tool_end` / `error`），其余按 `{type}` 裸壳发出。按 spec「未知 type → warn + 丢弃」的读法，**每轮 prompt 都会在主进程 stderr 刷 4 条 warn**。

**落地**：`client.ts` 增 `LIFECYCLE_EVENT_TYPES` 集合（`prompt_received` / `run_start` / `run_end` / `turn_start` / `turn_end` / `llm_start` / `compaction`），命中则静默 return；真未知 type 仍 warn。§7.5 的防御语义不变。

### 9.10 验收口径修正：本仓库没有 `npm test` / `npm run check`

**问题**：§7 引用了 `npm run check` / `npm test`，但本仓库尚未配置测试运行器（见 `AGENTS.md`，CI 入口就是 `npm run lint`）。

**落地**：S5 验收实际口径 =

| 手段 | 结果 |
|------|------|
| `npm run lint`（eslint） | exit 0 |
| `npx oxlint src` | exit 0 |
| `npm run build:agent` | exit 0 |
| `vite build --config vite.main.config.ts` | exit 0，产物 6.52 kB，require 真实 |
| 一次性 smoke 脚本（Node WS 直打真 Go 二进制） | port 解析 / WS / subscribe / `-32602` 拒未知 session / prompt result / notification 流 / SIGTERM 4ms exit 0 全通 |
| `npm start`（`ELECTRON_DISABLE_SANDBOX=1`） | prestart rebuild → 子进程起 → `gateway listening` → 无 `[runtime]` 报错（说明 connect 成功）→ 退出时 `graceful shutdown complete` + `exited code=0` |

**未覆盖**（留 S6）：

- `text_delta` / `done` happy path——本机无 Anthropic 凭据，prompt 走到 `error` 事件即终止。
- DevTools 内的手工项（§7.2 后半、§7.4、§7.5 部分）：需要交互式 DevTools，本轮以 smoke 脚本等价验证协议层，UI 层未逐条勾。
- `prettier --check` 全仓 63 文件不合规（含大量未触碰文件），本仓库未把 prettier 纳入 CI，本轮不做全仓 reformat（属 broad refactor）。


> **v1 spec 状态**：作废，仅历史参考。差异详见 §0。
>
> **完成说明**：v2 已落地（保留完成说明章节以便后续 reviewer 看上下文）。
