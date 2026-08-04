# Runtime Supervision 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 问题

`src/main/runtime/manager.ts` 当前的 `startAgentRuntime()` 仅打印日志而未实际 spawn Go 子进程。一旦 main-go 业务完全下放到 `darvin-agent`，Electron 主进程必须为 Go runtime 提供稳定可靠的进程托管，否则：

- 一次 Go runtime 崩溃（OOM、panic、卡死）会让整个桌面应用失去 AI 能力。
- WS+JSON-RPC 会话无 watchdog，超过 30s 心跳缺失无法识别。
- 重启窗口期间既有的 session 状态、订阅关系、增量事件流可能丢失。
- 与 LobsterAI 的 RuntimeManager 在 `crash` / `graceful-shutdown` 两条路径上区分度不足。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | RuntimeManager 主动 spawn / supervise `darvin-agent` 子进程 | `runtime/manager.ts` `startAgentRuntime` 真正 spawn |
| G2 | 端口 + IPC 双向探活，心跳缺失 30s 触发恢复 | watch 协程 + ping/pong |
| G3 | 最多 3 次指数退避（1s / 4s / 16s），3 次后进 `fatal` | 退避表 + `state-machine.ts` |
| G4 | 主动退出与异常崩溃区别处理 | exit code 分类：`graceful` / `crash` / `unknown` |
| G5 | 重启期间 RPC 行为可定义：拒绝新订阅，保留旧订阅 buffer | `agent:*` IPC 切到 `degraded` channel |
| G6 | Session 恢复：从 SQLite 读最近事件序号，重连后补齐 | `manager.recoverSession` + replay buffer |
| G7 | `smoke:recovery` 自动化用例 | `npm run smoke -- recovery` |

### 1.3 非目标

- 不动 Go agent 内部业务逻辑。
- 不引入第三方进程守护库（`pm2` / `systemd` 等）；由 Electron 主进程自治。
- 不实现集群 / 多副本（单实例单子进程）。
- 不修改 `forge.config.ts` 之外的构建脚本。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/main/runtime/manager.ts` | `resolveAgentBinaryPath()` 已实现；`startAgentRuntime()` 仅打印日志 |
| `src/main/runtime/client.ts` | `AgentClient` 接口占位；`createAgentClient()` 抛 `Not Implemented` |
| `src/shared/darvin-api.ts` | `DarvinApi` / `DarvinEvent` 定义；channel 名待常量集中 |
| `src/main/index.ts` | 主进程骨架；`window-all-closed` 已存在 |
| `scripts/build-go.js` | Go 二进制构建脚本 |

## 3. 用户/系统场景

### 场景 1：冷启动 spawn

**Given** `darvin-agent-<platform>-<arch>` 二进制存在且 `resolveAgentBinaryPath()` 返回有效路径
**When** 主进程调用 `startAgentRuntime()`
**Then** 子进程在 5s 内 listen `127.0.0.1:<port>`；ping/pong 通；`AgentClient` 状态切换为 `ready`

### 场景 2：进程崩溃恢复

**Given** runtime 已 `ready`，用户订阅某 session 的 event stream
**When** 子进程因 panic 退出（exit code ≠ 0）
**Then** 主进程 1s 内 spawn 新进程；3 次失败后推送 `runtime:fatal` 事件到 renderer；session 重新订阅并从 SQLite 恢复增量事件

### 场景 3：主动退出

**Given** 用户在设置页触发「重启 Runtime」
**When** UI 调用 `darvin.runtime.restart()`
**Then** 主进程先发 SIGTERM，等待 5s 优雅退出；超时 SIGKILL；新进程启动不计入 retry counter

### 场景 4：网络抖动

**Given** WS 连接断开（无 exit code）
**When** 5s 内未恢复
**Then** 进入 `reconnecting` 状态；保留旧订阅 buffer；30s 后转 `crash-recovery`（计入 retry counter）

## 4. 功能需求

### FR-1 spawn & supervise

RuntimeManager 持有 4 个状态：`idle` / `starting` / `ready` / `reconnecting` / `fatal`。

每次状态切换发 `runtime.state` 事件，renderer 通过 IPC 监听。

### FR-2 端口探活

主进程在 spawn 后启动 watcher：每 200ms TCP `127.0.0.1:<port>` 试探，最多 50 次（≈10s）；命中后才发 `ready`。

### FR-3 重启退避表

```ts
const BACKOFF_MS = [1_000, 4_000, 16_000];
```

第 3 次仍失败进 `fatal`，状态保留，需要用户手动重启。

### FR-4 异常分类

| exit code | 分类 | 行为 |
|---|---|---|
| 0 | graceful | 状态机 → `idle` |
| SIGTERM(15) / SIGINT(2) | graceful | 同上 |
| 其他 | crash | 进入 retry 流程 |
| 无法获取 exit code（信号丢失） | unknown | 同 crash |

### FR-5 重启期 RPC 行为

| 通道 | 重启期 |
|---|---|
| `agent:request` | 返回 `deferred` 错误，UI 提示「Runtime 正在恢复」 |
| `agent:event` 入站 | 缓冲到本地 channel 队列，恢复后回放 |
| `agent:event` 出站 | 按 session id 缓存最近 200 条，恢复后一次性投递 |

### FR-6 session 恢复

`recoverSession(sessionId)`：

1. 读 `sessions.events` 表最近 N 条（默认 200）
2. 与 renderer 端 last-event-id 对齐
3. 通过 `agent:event` 补齐差量

### FR-7 验收信号

- 自动化用例：`npm run smoke -- recovery` 必须 30s 内通过。
- 24h soak 不出现 `fatal`。

## 5. 安全与隐私

- 子进程 spec `stdio: ['ignore', 'pipe', 'pipe']`：`stdout` / `stderr` 重定向到日志文件，禁止 explorer 父进程 stdout。
- `port` 不得来自用户输入（仅由 Go runtime 启动时打 `DARVIN_RUNTIME_PORT` 给主进程读；从环境变量提取时校验范围 `[1024, 65535]`）。
- `runtime.state` 事件不含任何用户数据。
- 父进程崩溃时不重启 Explorer（避免递归故障）：仅在前进程 PID 未变化时尝试 spawn。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| spawn 后子进程退出 | watcher 触发 backoff |
| watcher 错过 exit code | 通过 `process.on('exit')` 同时注册兜底 |
| Go runtime 死锁但端口存活 | 30s 主动发 `agent.ping`，未应答判为 hung |
| 双 Explorer 实例 | 第二个实例探测端口占用则 `fatal` + 提示「已有实例在运行」 |
| macOS app nap | `app.setName('Darvin')` 避免被节流 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/main/runtime/manager.ts` | 真正 spawn + state machine + watcher |
| `src/main/runtime/client.ts` | 实现 `AgentClient` 全部方法 |
| `src/main/runtime/watcher.ts`（新） | 端口 + ping 探活 |
| `src/main/runtime/state-machine.ts`（新） | 5 状态切换 + 退避表 |
| `src/darvin-agent/internal/runtime/runtime.go`（新） | `DARVIN_RUNTIME_PORT` env + WS listen |
| `src/shared/darvin-api.ts` | 新增 `RuntimeState` 类型与 `agent.runtime.*` channel |

## 8. 实施顺序与依赖

1. 先实现 `state-machine.ts` 与 `watcher.ts`，单元测试覆盖状态切换。
2. `manager.ts` 接入 state machine + watcher。
3. Go runtime 端实现 runtime.go 与 `runtime.ping` JSON-RPC method。
4. `client.ts` 与 `darvin-api.ts` 同步。
5. `smoke:recovery` 用 `child.kill('SIGKILL')` 模拟崩溃 3 次，验证退出 `fatal`。
6. 24h soak 跑 dev 模式，监控崩溃恢复次数 ≤ 0。

> 前置：`specs/refactors/per-session-acp-agent/` 已 `已确认`。
> 并行：`specs/bugfixes/db-consistency-fixes/` 可同步推进（仅共享 SQLite 路径）。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | 单元测试覆盖 state machine 全部 5 状态切换 + 退避表 |
| V3 | `npm run smoke -- recovery` 自动化用例通过 |
| V4 | `npm start` 手动验证：手动 kill -9 子进程后 1s 内恢复 |
| V5 | Go 端 `go test ./...` + `go vet` 通过 |
| V6 | 24h soak 跑 `npm run dev` 模式不出现 `fatal` |
| V7 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V8 | 验收后同步更新 `CHECKLIST.md` 状态 |

## 10. 不在范围

- 进程间 IPC 协议字段扩展由 `specs/features/darvin-api-extension/` 主理；本文不重复 channel 设计。
- Go runtime 业务逻辑（agent loop / memory / tools）。
