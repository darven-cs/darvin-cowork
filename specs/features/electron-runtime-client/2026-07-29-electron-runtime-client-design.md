# Electron Runtime Client 设计文档（S5）

> **Phase 3 / 6 — Electron 阶段**。把 Electron 主进程和 Go Agent 子进程连起来：`runtime/manager.ts` 真 spawn 子进程并读 stdout 拿端口；`runtime/client.ts` 真 WS 客户端 + JSON-RPC 2.0 envelope；`preload/index.ts` 把 S1 的 mock 换成真 IPC；`main/index.ts` 串 lifecycle（启动 → spawn → connect → 暴露 → 关闭 → SIGTERM）。
> 前置：S1 已锁 `src/shared/darvin-api.ts` 契约 + S1 mock 走过发/收流；S2/S3/S4 已在 Go 侧把 Gateway / Agent.Run / sessions.db 立起来。
> 本 spec 是 Electron 端的"接通"工作 — 完成后用户能在 DevTools console 用 `await window.darvin.prompt('ping')` 真实调用 Go Agent。

---

## 1. 概述

### 1.1 问题 / 背景

S1 落完之后，UI 走的是 `mock-agent.ts` 假流，没接真后端。S2/S3/S4 已经在 Go 侧把 Gateway + Agent.Run + sessions.db 串起来，端到端只有 Electron 这边还缺一段连法。

当前 Electron 端 3 个文件状态：

| 文件 | 现状 | S5 落点 |
|------|------|---------|
| `src/main/runtime/manager.ts` | `resolveAgentBinaryPath()` 实装；`startAgentRuntime()` 是 console.log 占位 | 真 spawn、把 Go 进程 stdout 累积到 `<port>NNNNN</port>` 解析，存到 Resolved Runtime 句柄 |
| `src/main/runtime/client.ts` | `throw new Error('createAgentClient 未实现...')` | 真 WS 客户端：connect / disconnect / request / onNotification |
| `src/preload/index.ts` | 仅一段注释 | contextBridge 暴露 `window.darvin.prompt / abort / onEvent`；走 `ipcMain.handle` / `webContents.send` |
| `src/main/index.ts` | Hello World + DevTools；没 startAgentRuntime | 启动后 startAgentRuntime → createAgentClient → connect；所有 window 关闭前 graceful shutdown |

Electron 端的核心难点是 **3 段 protocol 串联**：

1. **subprocess spawn**：`child_process.spawn(bin, [], { stdio: ['ignore', 'pipe', 'pipe'] })`；累积 stdout 直到匹配 `<port>(\d+)</port>`，记下 port
2. **WS connect**：`ws://localhost:{port}/ws`（gorilla/websocket 上游，遵循 S3 spec）
3. **JSON-RPC 2.0 envelope**：strict parse / build / match id → promise resolve

### 1.2 目标

- `src/main/runtime/manager.ts` 实装：spawn + stdout port parsing + SIGTERM forwarding + 进程退出码 / stderr 日志
- `src/main/runtime/client.ts` 实装：WS Connection + JSON-RPC envelope + request/notification + promise-id-mux
- `src/preload/index.ts` 实装：contextBridge 暴露 `darvin.prompt / abort / onEvent`（同 S1 签名，但实现走 ipcRenderer.invoke / ipcRenderer.on）
- `src/main/index.ts` 接线：`app.whenReady` → `RuntimeMgr.start()` → `AgentClient.connect()` → window.create → 退出前 `client.disconnect()` + `RuntimeMgr.stop()`
- 跨进程生命周期：Electron 退出 → `darwin-agent` 子进程 SIGTERM；Go 端 S4 已处理 SIGTERM 流程（abort → flush → close）

### 1.3 非目标

- **不**加 WS reconnect / ping-pong 容错（v0；subprocess 死了 UI 报错提示用户重启）
- **不**做 spawn 超时（v0 信任 local 二进制 ms 级启动）
- **不**做 privileged-bundle / sandbox / code signing（远期）
- **不**做 supervisor（subprocess 死掉自动重启；scope 之外）
- **不**做 Event 流的多窗口订阅（v0 单 window 全局订阅）
- **不**改 S1 锁死的 `DarvinEvent` union / `prompt/abort` 签名（仅替换实现）
- **不**改 S3 / S4 的 Go 侧协议（仅 consumer）
- **不**做生产环境 `extraResources` 打包（dev 路径即可）

---

## 2. 用户场景

### 场景 1：npm start 启动后端到端通

**Given** S1-S4 全部完成，仓库状态齐
**When** `npm start`（electron-forge 启动主进程）
**Then** 行为序列：
1. 主进程 `app.whenReady` 后调 `RuntimeMgr.start()`,spawn `darvin-agent-...` 子进程
2. 子进程 stdout 累积直到 `<port>NNNNN</port>` 被解析（最长 5s）
3. `AgentClient` 用 `ws://localhost:{port}/ws` 建立连接
4. BrowserWindow 创建 + DevTools 打开
5. 用户在 DevTools console 输入 `await window.darvin.prompt({ content: 'ping' })`
6. 看到 `{ sessionId: '...', messageId: '...' }` 返回
7. （后台）S4 Agent.Run → LLM 流 → WS notification → `window.darvin.onEvent` 回调被调用

### 场景 2：UI 流式追加

**Given** 场景 1 已通
**When** 用户在 UI 输入框输入 "ping" 并 send
**Then** 消息列表显示 user 消息（右侧），assistant 消息逐字符从左到右出现（每条 `text_delta` 推一次），最后一条 `done` 出现后定型
**And** duration（用户敲 send 到 assistant done）< 10s（Anthropic 流式）

### 场景 3：Electron 退出触发 graceful shutdown

**Given** 场景 1 已通，子进程在跑
**When** 用户关 Electron 主窗口（macOS 不会退；Win/Linux 全窗口关闭 → app.quit）
**Then** 主进程：
1. `app.on('before-quit')` 触发 `RuntimeMgr.stop()`
2. `Manager` 转发 SIGTERM 给子进程
3. 子进程 S4 graceful shutdown 路径在 ≤3s 内完成，stderr 输出 "graceful shutdown complete"
4. 主进程退出码 0
5. 没有 dangling subprocess（`pgrep darvin-agent` 无结果）

### 场景 4：Go 子进程启动失败

**Given** `bin/darvin-agent-...` 不存在（开发态忘 build）
**When** `npm start`
**Then** 主进程 stderr 打印 `[runtime] darvin-agent 二进制不存在，已跳过启动。运行 \`npm run build:agent\` 编译。`
**And** BrowserWindow 仍然打开，UI 顶部 header 显示 "Runtime: offline (run \`npm run build:agent\`)"

### 场景 5：Go 子进程 crash（启动后退出非 0）

**Given** 子进程已启动 + AgentClient 已连接
**When** 子进程 panic 退出（exit code != 0）
**Then** 主进程 stderr 打印 `[runtime] darvin-agent exited code=...`
**And** `AgentClient` 触发 `disconnect()`，把状态切到 offline
**And** UI 顶部 header 改为 "Runtime: offline"

### 场景 6：fe ↔ be 协议 mismatch 防御

**Given** S1 已锁 `darvin-api.ts` union 类型
**When** Go 端 emit 一条不符合 TS union 的事件（如 `type: "unknown_event"`）
**Then** TS 端 `parseDarvinEvent(raw)` 走 `default` 分支输出 `console.warn('[darvin] 未知 event type: unknown_event')` 且**不**抛错
**And** UI 仍能继续接收后续合法 event

---

## 3. 功能需求

### FR-1：进程模型

#### FR-1.1 RuntimeMgr

`src/main/runtime/manager.ts` 整体重写：

```ts
import { app } from 'electron';
import { spawn, ChildProcess } from 'node:child_process';
import path from 'node:path';
import fs from 'node:fs';
import { EventEmitter } from 'node:events';

export interface ResolvedAgent {
  port: number;
  pid: number;
}

export class RuntimeMgr extends EventEmitter {
  private proc: ChildProcess | undefined;
  private resolvedPort: number | undefined;
  private resolved: (r: ResolvedAgent) => void = () => {};
  private rejected: (e: Error) => void = () => {};
  private stdoutBuf = '';

  /**
   * Spawn darvin-agent subprocess; resolves once <port>NNNNN</port> on stdout.
   * Emit 'exit' on child exit; emit 'offline' on resolve failure / exit.
   */
  start(): Promise<ResolvedAgent> {
    const bin = resolveAgentBinaryPath();
    if (!bin) {
      return Promise.reject(
        new Error('darvin-agent 二进制不存在；运行 npm run build:agent'),
      );
    }

    return new Promise<ResolvedAgent>((resolve, reject) => {
      this.resolved = resolve;
      this.rejected = reject;

      this.proc = spawn(bin, [], {
        stdio: ['ignore', 'pipe', 'pipe'],
        env: { ...process.env, DARVIN_DEV: '1' },
      });

      this.proc.on('exit', (code, signal) => {
        this.emit('exit', { code, signal });
        this.resolvedPort = undefined;
      });

      this.proc.stdout!.on('data', (chunk: Buffer) => {
        const text = this.stdoutBuf + chunk.toString('utf8');
        const m = text.match(/<port>(\d+)<\/port>/);
        if (m) {
          this.stdoutBuf = '';
          this.resolvedPort = Number(m[1]);
          this.resolved({ port: this.resolvedPort, pid: this.proc!.pid! });
        } else {
          this.stdoutBuf = text;
        }
      });

      this.proc.stderr!.on('data', (chunk: Buffer) => {
        process.stderr.write(`[darvin-agent] ${chunk}`);
      });

      // 5s 启动超时
      setTimeout(() => {
        if (this.resolvedPort === undefined) {
          this.rejected(new Error('启动超时：5s 内未读到 <port>...</port>'));
        }
      }, 5000);
    });
  }

  /** 转发 SIGTERM；S4 graceful shutdown 在 ≤3s 内完成。 */
  stop(): Promise<void> {
    if (!this.proc) return Promise.resolve();
    return new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        this.proc?.kill('SIGKILL');
        resolve();
      }, 4000);
      this.proc!.on('exit', () => {
        clearTimeout(t);
        resolve();
      });
      this.proc!.kill('SIGTERM');
    });
  }

  pid(): number | undefined { return this.proc?.pid; }
}

export function resolveAgentBinaryPath(): string | undefined {
  const { platform, arch } = process;
  const exeSuffix = platform === 'win32' ? '.exe' : '';
  const name = `darvin-agent-${platform}-${arch}${exeSuffix}`;
  let p: string;
  if (app.isPackaged) {
    p = path.join(process.resourcesPath, 'bin', name);
  } else {
    p = path.join(__dirname, '..', '..', '..', 'bin', name);
  }
  return fs.existsSync(p) ? p : undefined;
}
```

要点：
- 监听 `stdout` 累积 → 正则匹配 `<port>(\d+)</port>`（单行；多 chunk 跨缓冲）
- 5s 启动超时（防 Go 端 hang）
- stderr 透传（用户 dev 时能看到 Go 日志）
- `stop()` 主动 SIGTERM，超过 4s SIGKILL 兜底
- 复用 `resolveAgentBinaryPath()`（不重写）

#### FR-1.2 AgentClient

`src/main/runtime/client.ts` 整体重写：

```ts
import { WebSocket } from 'ws';
import { EventEmitter } from 'node:events';
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortRequest,
  DarvinAbortResponse,
  DarvinEvent,
} from '../../shared/darvin-api';

interface PendingRequest {
  resolve: (v: any) => void;
  reject: (e: Error) => void;
}

export class AgentClient extends EventEmitter {
  private ws: WebSocket | undefined;
  private nextId = 1;
  private pending = new Map<string, PendingRequest>();
  private notifListeners = new Set<(e: DarvinEvent) => void>();

  async connect(port: number): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(`ws://localhost:${port}/ws`);
      ws.on('open', () => {
        this.ws = ws;
        resolve();
      });
      ws.on('error', (e) => reject(e));
      ws.on('close', () => {
        this.emit('offline');
        // reject 所有 pending
        for (const p of this.pending.values()) {
          p.reject(new Error('ws closed'));
        }
        this.pending.clear();
      });
      ws.on('message', (data: Buffer) => {
        const msg = JSON.parse(data.toString('utf8'));
        this.handleIncoming(msg);
      });
    });
  }

  private handleIncoming(msg: any) {
    if (msg.id !== undefined && msg.id !== null) {
      // response
      const p = this.pending.get(String(msg.id));
      if (!p) return;
      this.pending.delete(String(msg.id));
      if (msg.error) {
        p.reject(new Error(`rpc ${msg.error.code}: ${msg.error.message}`));
      } else {
        p.resolve(msg.result);
      }
    } else if (msg.method === 'agent.event' && msg.params) {
      // notification
      for (const cb of this.notifListeners) {
        try { cb(msg.params as DarvinEvent); } catch (e) { /* swallow */ }
      }
    }
  }

  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return this.request('agent.prompt', req) as Promise<DarvinPromptResponse>;
  }

  async abort(req: DarvinAbortRequest): Promise<DarvinAbortResponse> {
    return this.request('agent.abort', req) as Promise<DarvinAbortResponse>;
  }

  private request(method: string, params: any): Promise<any> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('agent offline'));
    }
    const id = String(this.nextId++);
    const payload = JSON.stringify({
      jsonrpc: '2.0', id, method, params,
    });
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws!.send(payload);
    });
  }

  onEvent(cb: (e: DarvinEvent) => void): () => void {
    this.notifListeners.add(cb);
    return () => this.notifListeners.delete(cb);
  }

  async disconnect(): Promise<void> {
    if (!this.ws) return;
    this.ws.close();
    this.ws = undefined;
  }
}
```

要点：
- 依赖：`ws`（Node 22 内置 `WebSocket` 全局，但 electron 22+ 主进程可能需要 `npm i ws`）；保守起见 S5 加 `ws ^8.0` 到 deps
- id 每次 `+1`（单 connection）
- `id` 是 string / number / null 三态，存到 Map 用 `String(id)`
- notification `method === 'agent.event'` 是 S3 约定的推送通道
- WS close → reject 所有 pending，避免 leak

### FR-2：preload contextBridge 真接

`src/preload/index.ts` 整体重写（替换 S1 mock）：

```ts
import { contextBridge, ipcRenderer } from 'electron';
import type {
  DarvinPromptRequest,
  DarvinPromptResponse,
  DarvinAbortRequest,
  DarvinAbortResponse,
  DarvinEvent,
} from '../shared/darvin-api';

const darvin = {
  async prompt(req: DarvinPromptRequest): Promise<DarvinPromptResponse> {
    return ipcRenderer.invoke('darvin:prompt', req);
  },
  async abort(req: DarvinAbortRequest): Promise<DarvinAbortResponse> {
    return ipcRenderer.invoke('darvin:abort', req);
  },
  onEvent(cb: (e: DarvinEvent) => void): () => void {
    const wrap = (_: unknown, ev: DarvinEvent) => cb(ev);
    ipcRenderer.on('darvin:event', wrap);
    return () => ipcRenderer.off('darvin:event', wrap);
  },
  // 状态：'connecting' | 'online' | 'offline' | 'no-binary'
  status(): 'online' | 'offline' | 'no-binary' {
    // 同 ipcRenderer.invoke('darvin:status') 让主进程查
    return ipcRenderer.sendSync('darvin:status') as any;
  },
};

contextBridge.exposeInMainWorld('darvin', darvin);
```

签名完全等同 S1 §FR-5，保证 renderer 不感知 mock vs real。

### FR-3：主进程 IPC handler

`src/main/index.ts` 增加 `ipcMain.handle` 把 `ipcRenderer` 调转发到 `AgentClient`：

```ts
import { ipcMain } from 'electron';
import { RuntimeMgr, resolveAgentBinaryPath } from './runtime/manager';
import { AgentClient } from './runtime/client';

const mgr = new RuntimeMgr();
const client = new AgentClient();

ipcMain.handle('darvin:prompt', async (_e, req) => client.prompt(req));
ipcMain.handle('darvin:abort', async (_e, req) => client.abort(req));
ipcMain.on('darvin:event', (_e, ev) => { /* renderer → main, 本 spec 不用 */ });
ipcMain.on('darvin:status', (e) => {
  // sync 转发状态
  e.returnValue = mgr.pid() && clientConnected ? 'online' : 'offline';
});

// client → renderer 事件转发
client.onEvent((ev) => {
  for (const win of BrowserWindow.getAllWindows()) {
    win.webContents.send('darvin:event', ev);
  }
});

mgr.on('exit', ({ code }) => {
  console.error(`[runtime] darvin-agent exited code=${code}`);
  // 标记 offline
});
```

要点：
- `darvin:prompt` / `darvin:abort` 转发到 AgentClient
- AgentClient 的 `onEvent` 推回所有 window（v0 全局订阅）
- `darvin:status` 用 sync 简单返回（renderer header 用一次）

### FR-4：main 启动时序

`src/main/index.ts` 在 `app.whenReady` 后：

```ts
app.whenReady().then(async () => {
  // 1. 启动子进程
  const port = await mgr.start().catch((e) => {
    console.error(`[runtime] ${e.message}`);
    return null;
  });
  if (port) {
    // 2. 连 WS
    await client.connect(port.port).catch((e) => {
      console.error(`[runtime] connect: ${e.message}`);
    });
  }
  // 3. 创建窗口
  createWindow();
});
```

要点：
- 子进程启动失败**不**block 窗口创建（场景 4：UI 仍能起，header 显示 offline）
- 创建窗口**先**于 ws connect（避免 ws 事件触发时 window 还没 ready）
- DevTools 仍开（dev 期）

### FR-5：graceful shutdown

`src/main/index.ts`：

```ts
app.on('before-quit', async (e) => {
  if (!shuttingDown) {
    e.preventDefault();
    shuttingDown = true;
    await client.disconnect();
    await mgr.stop();
    app.quit();
  }
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});
```

要点：
- `before-quit` 是唯一保证 graceful shutdown 的钩子（比 `will-quit` 早）
- 关 disconnect 子进程，再关 WS，最后 quit
- macOS 保留 `window-all-closed` 不退（标准行为）

### FR-6：renderer 状态指示

`src/renderer/App.vue`（S1 已有）顶部 `ChatHeader.vue` 加一个 runtime status badge：

```vue
<template>
  <header class="flex items-center justify-between border-b px-4 py-2">
    <span class="font-medium">Darvin Cowork · dev</span>
    <span :class="badge" class="text-xs">{{ label }}</span>
  </header>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue';
const status = ref<'online' | 'offline' | 'no-binary'>('offline');
onMounted(() => {
  try { status.value = window.darvin.status(); } catch {}
  setInterval(() => {
    try { status.value = window.darvin.status(); } catch {
      status.value = 'offline';
    }
  }, 2000);
});
const badge = computed(() => ({
  online: 'text-green-600',
  offline: 'text-amber-600',
  'no-binary': 'text-red-600',
}[status.value]));
const label = computed(() => status.value === 'online' ? 'Runtime: ready' : 'Runtime: offline');
</script>
```

要点：
- 2s 轮询（v0 简单实现；后续可走 push）
- `no-binary` 是 S5 新状态（S1 没暴露）；IPC 层 fallback `'offline'` 让 renderer 不感知

### FR-7：依赖 / 工具链

- `package.json` `dependencies` 加 `ws ^8.18`（或 `^8.0`）
- `package.json` `devDependencies` 已含 `electron` / `electron-forge` / `@electron-forge/plugin-vite` / `vite` / `vue` / `tailwindcss`（S1 已加），S5 不动
- 不引入 zod / io-ts（DarvinEvent 走 S1 类型 + `parseDarvinEvent()` 收口）

---

## 4. 实现方案

### 4.1 目录结构

```
src/
├── main/
│   ├── index.ts               # 🆕 改：接线 mgr + client + window + ipc
│   └── runtime/
│       ├── manager.ts         # 🆕 改：spawn + stdout port parse
│       └── client.ts          # 🆕 改：WS + JSON-RPC
├── preload/
│   └── index.ts               # 🆕 改：contextBridge 真接 ipcRenderer
├── renderer/
│   ├── App.vue                # 🆕 改：ChatHeader status badge
│   ├── components/
│   │   └── ChatHeader.vue     # 🆕 改：runtime status
│   └── services/
│       └── mock-agent.ts      # 保留但 S5 启动时已不再被加载（preload 不再 import）
└── shared/
    └── darvin-api.ts          # S1 已锁；S5 不改
```

### 4.2 关键流程时序

#### 启动

```
npm start
  └─ electron-forge spawn main
       └─ src/main/index.ts
            ├─ app.whenReady()
            ├─ mgr.start()  ── spawn darvin-agent ──┐
            │      └─ stdout parse <port>N</port>   │
            ├─ client.connect(port) ── WS upgrade ──┤
            │                                        │
            │              darvin-agent (Go)         │
            │              ├─ cmd/app/main.go ───────┤
            │              ├─ gateway.Start ─────────┤
            │              │   └─ bind :0            │
            │              │   └─ stdout <port>...   │
            │              ├─ acp.Loop                │
            │              ├─ agent.runtime          │
            │              └─ sessions.db (S2)        │
            │                                        │
            ├─ createWindow() ← window ready ←──────┘
            └─ DevTools open
```

#### 一次 prompt

```
User 输入 ping, click send
  └─ renderer: await window.darvin.prompt({ content: 'ping' })
       └─ preload: ipcRenderer.invoke('darvin:prompt', req)
            └─ main: ipcMain.handle → client.prompt(req)
                 └─ AgentClient.request('agent.prompt', req)
                      └─ WS send {"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{...}}
                           └─ darvin-agent gateway.handlePrompt
                                └─ acp.Loop.Prompt → Agent.Prompt → Run (async)
                                     └─ agent.run → event.Bus emit TextDeltaEvent
                                          └─ EventLedger.AttachBus → WS notification
                                               └─ AgentClient.handleIncoming(onEvent)
                                                    └─ client.onEvent listeners
                                                         └─ main: webContents.send('darvin:event', ev)
                                                              └─ preload: ipcRenderer.on → cb(ev)
                                                                   └─ renderer: MessageList reactive update
```

#### 关闭

```
User 关闭窗口 / Cmd+Q
  └─ app.on('before-quit')
       ├─ client.disconnect()  ── WS close
       ├─ mgr.stop()  ── SIGTERM → darvin-agent
       │                       ├─ signal.NotifyContext cancel
       │                       ├─ Agent.Abort
       │                       ├─ WS server Shutdown
       │                       ├─ DB close
       │                       └─ os.Exit(0)
       └─ app.quit()  ── main process exit
```

### 4.3 关键决策

#### 4.3.1 不要 zod / io-ts

依赖 `ws` 即可。`DarvinEvent` 类型仍是 TS 接口（编译期校验），运行时校验只做 `kind` 字段判别（`event.type`）；不匹配打 warn 不抛错。

#### 4.3.2 stdout 单行约定

延续 S3 §4.3.1 决策：`<port>NNNNN</port>` 唯一一行。S5 的 `data` chunk 累积逻辑确保多 chunk 跨 buffer 也能匹配。

#### 4.3.3 不做 reconnect

S3 §1.3 明确 v0 不做 ping-pong / reconnect。S5 子进程死了，UI 报 offline；用户手动 restart Electron。

#### 4.3.4 `darvin:status` 走 sync IPC

`onEvent` 是单向推（preload 不调），`prompt/abort` 是 async invoke，状态查询走 sync（renderer 启动 / 轮询用 1 次）。sync IPC 在本地同进程极快，< 1ms。

#### 4.3.5 复用 S1 签名

`window.darvin` 暴露的 3 个方法签名（`prompt / abort / onEvent`）S1 已锁死；S5 仅替换实现（mock → 真 IPC）。Renderer 端代码（S1 写完）**不**需要改业务逻辑。

#### 4.3.6 SIGTERM 转发

`mgr.stop()` 主动 SIGTERM（不是 SIGKILL），给 S4 graceful shutdown 3s 窗口。超时 4s 后 SIGKILL 兜底。

### 4.4 关键代码骨架

```ts
// src/main/index.ts (新增段落示意)
import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import { RuntimeMgr } from './runtime/manager';
import { AgentClient } from './runtime/client';

declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

if (require('electron-squirrel-startup')) { app.quit(); }

const mgr = new RuntimeMgr();
const client = new AgentClient();
let shuttingDown = false;

async function bootstrap() {
  const port = await mgr.start().catch((e) => {
    console.error(`[runtime] ${e.message}`);
    return null;
  });
  if (port) {
    await client.connect(port.port).catch((e) => {
      console.error(`[runtime] connect: ${e.message}`);
    });
  }
  createWindow();
}

ipcMain.handle('darvin:prompt', (_e, req) => client.prompt(req));
ipcMain.handle('darvin:abort', (_e, req) => client.abort(req));
ipcMain.on('darvin:status', (e) => {
  e.returnValue = mgr.pid() && client.isConnected() ? 'online'
                : mgr.pid() === undefined ? 'no-binary'
                : 'offline';
});

client.onEvent((ev) => {
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) win.webContents.send('darvin:event', ev);
  }
});

mgr.on('exit', ({ code }) => {
  console.error(`[runtime] darvin-agent exited code=${code}`);
});

const createWindow = () => { /* 同 S1 现有 */ };

app.whenReady().then(bootstrap);

app.on('before-quit', async (e) => {
  if (shuttingDown) return;
  e.preventDefault();
  shuttingDown = true;
  await client.disconnect();
  await mgr.stop();
  app.quit();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) createWindow();
});
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `bin/darvin-agent-*` 不存在 | `mgr.start()` reject; main 仍 `createWindow()`; UI header 显示 offline |
| 子进程启动但 5s 内没输出 `<port>` | `mgr.start()` 5s 超时 reject; 同上 |
| WS 连接失败（端口被占用 / Go 端 panic） | `client.connect()` reject; stderr 报错; UI offline |
| WS 断连（子进程死掉） | `client` emit `'offline'`; main 推 `darvin:event` 给所有 window? **不**推（offline 状态走 `status()` 轮询）|
| 子进程 exit code != 0 | `mgr.on('exit')` 打印 stderr; UI 通过 `status()` 感知 |
| stdout chunk 跨 buffer（"abc<port>12" + "34</port>def"） | `stdoutBuf` 累积 + repeat regex |
| port parse 成功后 stdout 继续有数据 | 忽略（不重新 resolve）; S9+ 可加 stderr 后续处理 |
| 同 WS 上多 client 推送 | `client.onEvent` 内部 Set 防重复; main 端 `for win in getAllWindows()` 全推 |
| `agent.prompt` 中途 RPC id 冲突 | `AgentClient.nextId` 单 connection +1; 不会冲突 |
| `prompt` 返回后 RPC 还在飞 | pending Map 保留; WS close 时 reject |
| `onEvent` listener 抛错 | `try/catch` 吞掉; 防止一个 listener 崩其他 |
| Electron 主进程 `before-quit` 多次触发 | `shuttingDown` 标志位幂等 |
| SIGKILL 兜底 | 子进程 4s 内没退 → SIGKILL |
| 子进程在 listen 阶段收到 SIGTERM | S4 graceful shutdown 路径走完（≤3s）; main 在 4s 内看到 exit |
| Node 22+ Electron 主进程全局 WebSocket | 优先用 `ws` 包（避免 Node 22 之前的兼容问题）|
| `darvin:status` sync 在 renderer 阻塞 | 1ms 内同步返回; 可忽略 |
| 用户在子进程 offline 时发 prompt | `client.prompt()` reject `agent offline`; UI catch 后显示 toast "Runtime offline" |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/main/runtime/manager.ts` | 🆕 改：实装 `RuntimeMgr` class（spawn + port parse + stop）; 保留 `resolveAgentBinaryPath()` |
| `src/main/runtime/client.ts` | 🆕 改：实装 `AgentClient` class（WS + JSON-RPC + request/onEvent） |
| `src/preload/index.ts` | 🆕 改：contextBridge 真接 `ipcRenderer.invoke / on`，签名同 S1 |
| `src/main/index.ts` | 🆕 改：bootstrap 时序 + IPC handler + before-quit graceful shutdown |
| `src/renderer/components/ChatHeader.vue` | 🆕 改：runtime status badge (online / offline / no-binary) |
| `src/renderer/App.vue` | 改：组件挂 ChatHeader |
| `src/renderer/services/mock-agent.ts` | 移除（S5 后不再被 import） |
| `package.json` | 改：`dependencies` 加 `ws ^8.18` |

**不修改**：
- S1 锁的 `src/shared/darvin-api.ts`（签名不变）
- S3 / S4 的 Go 侧（只 consumer）
- `electron-forge` / `vite.config` 配置
- TS / Tailwind / Vue 工程设置

---

## 7. 验收标准

- [ ] `npm run lint` 通过（`tsc --noEmit` + ESLint）
- [ ] `npm start` 启动 Electron，主进程在 `~1s` 内成功 spawn darvin-agent 子进程
- [ ] 子进程 stdout 解析到 `<port>NNNNN</port>` 且 `client.connect(port)` 成功
- [ ] `npm start` 启动后 Electron DevTools console 中：
  - 输入 `await window.darvin.prompt({ content: 'ping' })` 返回 `{ sessionId: '...', messageId: '...' }`
  - 输入 `window.darvin.status()` 返回 `'online'`
- [ ] UI ChatHeader 顶部 badge 显示 "Runtime: ready"（绿色）
- [ ] UI 输入框输入 "ping" → send → 消息列表出现 user + 流式 assistant 消息（每条 `text_delta` 推一次），最后 `done`
- [ ] DevTools Network 标签看到 `ws://localhost:NNNNN/ws` 帧流入（WS 帧可展开看到 JSON-RPC envelope）
- [ ] 关 Electron 主窗口 → `app.before-quit` 触发 → 子进程 stdout flush → stderr 输出 "graceful shutdown complete" → 子进程退出 → Electron 退出
- [ ] `pgrep darvin-agent` 关闭后无残留
- [ ] 故意 `rm bin/darvin-agent-*` 后 `npm start`：主进程 stderr 报错，UI 仍打开，header 显示 "Runtime: offline"
- [ ] 子进程被 `kill -9` 模拟 crash：主进程 stderr "exited code=null"，UI 切换到 offline
- [ ] `window.darvin.onEvent` 回调在 assistant message 流式完成时**不**抛错
- [ ] WS 帧序列 raw 可读（DevTools Network → WS → Messages 标签）：
  - Request: `{"jsonrpc":"2.0","id":"1","method":"agent.prompt","params":{"content":"ping"}}`
  - Response: `{"jsonrpc":"2.0","id":"1","result":{"sessionId":"...","messageId":"..."}}`
  - Notification: `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"text_delta","delta":"..."}}`
  - Final: `{"jsonrpc":"2.0","method":"agent.event","params":{"type":"done","usage":{...}}}`

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S6** agent-e2e-integration | 端到端联调（Electron → Go → Anthropic → UI 流），session 持久化跨重启，graceful shutdown 链路验证，README "first run" |
| WS reconnect / ping-pong | v0 简化版; 子进程死了 UI 报错; 后续可加 auto-restart |
| Runtime supervisor | 子进程死掉自动重启（pm2-like） |
| WSS / TLS | 远期 |
| 多 window 多用户隔离 | 远期 |
| Production `extraResources` | 打包期把 darvin-agent 二进制塞到 `resources/bin/` |
| Code signing / sandbox | 远期 |
| IPC schema 强校验（zod / protobuf） | 远期 |
