# 定时任务（Scheduled Tasks）设计文档

> 本 spec 是 `specs/features/lobster-comparison/README.md` § 2 列表第 9 项 **Scheduling** 维度、以及 `CHECKLIST.md` 第 20 行（评分表）与第 101-105 行（Tier 3 当前状态与提案）的实现级落地文档。路线图归路线图，本 spec 负责把"定时任务"拆到可实现的工程粒度。
>
> 参考实现：`~/桌面/github-project/LobsterAI/src/scheduledTask/`（OpenClaw gateway 内置 cron 引擎 + 主进程 15s/3s 自适应 `setTimeout` 状态镜像轮询；详见 `cronJobService.ts:509-514` 的 `POLL_INTERVAL_MS=15_000` / `ACTIVE_POLL_INTERVAL_MS=3_000`）。**澄清**：LobsterAI 主进程轮询只把 OpenClaw gateway 状态镜像到本地 cache（`jobNameCache` / `jobDeliveryCache` / `runningJobIds` / `lastKnownStates`），**不**真正评估或触发任务——cron 表达式求值与执行都在 OpenClaw gateway 子进程内，main 进程只通过 `cron.list / cron.add / cron.update / cron.remove / cron.run / cron.runs` 六个 JSON-RPC 与 gateway 通信。这一点直接影响本 spec 的架构决策（见 § 4.1：darvin 没有独立 gateway 做 cron，所以选择 main 端直跑）。darvin-cowork 现状见 § 2。

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 当前**没有**任何定时触发能力：

- 前端已经埋好入口占位：`src/renderer/composables/useViewMode.ts:11-20` 视图模式包含 `'scheduled'`；`src/renderer/layout/AppShell.vue:78-97` PLACEHOLDERS 表里 scheduled 走 `PlaceholderView`（clock 图标，"定时任务能力尚未接入，敬请期待"）；`src/renderer/components/sidebar/SidebarNav.vue:41` 有侧栏入口；`SettingsPanelShortcuts.vue:37` + `useShortcuts.ts:14` 绑了 `Cmd/Ctrl+3` 快捷键；i18n 双语键 `sidebar.nav.scheduled` + `sidebar.placeholder.scheduled.desc` 已就位。
- Go 侧 `src/darvin-agent/internal/agents/ctxengine/lifecycle.go:17-18` 注释 "Reserved for periodic housekeeping on the context engine's internal state (Dreaming / Cron)" 留过 cron 占位但未实现。
- npm 依赖里**没有**任何 cron 库（`package.json` dependencies 仅有 better-sqlite3 / chokidar / ws / mermaid / pdfjs-dist / jszip / docx-preview / xlsx / pptx-preview / electron-squirrel-startup；**没有** `docx` / `pptxgenjs` ——这两个名字是错的，实际是 `docx-preview` / `pptx-preview`）；Go `go.mod`（模块名 `darvin-cowork/backend`，Go 1.22）也没有 `robfig/cron` 之类。
- 主进程唯一的时间触发是 `time.NewTicker` 在 `src/darvin-agent/internal/gateway/client.go:154` 的 WS 心跳 30s，**没有用户侧调度**。
- **触发链路天然存在**：Go 侧 `src/darvin-agent/internal/sessionruntime/loop.go:134` `Loop.Submit(req PromptRequest) (RunTicket, error)` 是 turn 入队点，可被同进程内的 scheduler goroutine 直接调用（无需跨进程）；`src/main/runtime/client.ts:263` 的 `client.prompt()` 仍是 renderer → main → Go 的人工触发入口。也就是说**调度器所缺的只是"到点了谁来调 Loop.Submit"这个 cron 引擎**。

### 1.2 目标

落地一个最小可用的"定时任务"能力，使用户能够：

1. 在前端创建一个调度任务（一次性 / 固定间隔 / cron 表达式三种），绑定到一个 workspace + agent + 启动 prompt；
2. 后端按计划自动调 Go 侧 `Loop.Submit` 跑 headless turn（**所有产出落在该任务的固定 session**（同一 schedule 复用 sessionId `schedule-<taskId>`，与手动对话统一入口，sidebar 自然可见；agent 上下文连续，知道上次跑了什么）；
3. 用户能在前端看任务列表 + 启停 + 手动触发 + 运行历史；
4. 运行结束后通过 OS 通知 + sidebar 红点提示用户。

### 1.3 非目标（v1 不做）

- **不做 IM 入口**：LobsterAI 第二个入口是 IM 聊天里"提醒我 X 点开会"（正则 + LLM 抽 scheduleAt），darvin 暂不支持 IM，搁置。
- **不做 agent 自己创建 schedule**：LobsterAI 第三个入口是往 agent system prompt 注入 `## Scheduled Tasks` + native tool `cron.add`，让 agent 在 Cowork 会话里帮用户创建。darvin v1 仅 UI 创建。
- **不做 3 种 schedule.kind 之外的复杂触发**（如 IM 消息到达、文件 mtime 变化等）：超出本 spec 范围。
- **不做多实例并发调度**：单桌面应用，本机单 main 进程，scheduler 唯一。LobsterAI 也只在主进程做 15s 轮询，不做分布式。
- **不做 app 关闭后补跑过期任务**（miss-fire policy）：明确告诉用户"应用未启动期间 schedule 不会跑"，与 LobsterAI 行为一致（autoLaunch 是用户偏好，不在本 spec）。
- **不做投递到 IM/邮件/Webhook**：v1 只跑 agent，产出落 session。

## 2. 用户场景

### 场景 1: 用户创建一个"每天 9 点生成工作日报"的定时任务

**Given** 用户当前在 workspace `work` 下，有一个名为 `daily-report` 的 agent（system prompt 设定了"用 Markdown 输出昨天完成的任务清单"）；
**When** 用户打开「定时任务」视图 → 点新建 → schedule.kind 选 `cron`，表达式填 `0 9 * * *`，绑 `work` workspace + `daily-report` agent + 启动 prompt `请生成本日工作日报`，保存并启用；
**Then** 任务出现在列表中，状态为 enabled；每天 09:00（本地时区）自动跑一次 turn，跑出的对话落在该任务的固定 session `schedule-<taskId>` 里（每次复用同一 session，agent 上下文连续），session sidebar 可见；用户能在任务详情页的"运行历史"看到每次跑的 startedAt / endedAt / status / messageId。

### 场景 2: 用户临时手动触发一次任务

**Given** 任务已存在但还没到点；
**When** 用户点任务卡片上的「立即运行」按钮；
**Then** 调度器立即触发一次 turn（不等下次 tick），UI 乐观标记为 running；run 历史里多一条 `manual` 触发的记录。

### 场景 3: 用户暂停/启用/删除任务

**Given** 任务存在；
**When** 用户点 toggle 关闭 / 开启，或点删除二次确认；
**Then** 关闭的任务不再触发，但 run 历史保留；删除的任务列表消失，历史随之外键 cascade 清除（或保留 N 天可配置，见 § 5）。

### 场景 4: 任务触发时 Go agent 未就绪

**Given** 调度器 tick 到点，但 Go agent 子进程还在启动 / 重启中（`runtime/manager.ts:start()` 解析 `<port>` 阶段）；
**When** 调度器尝试 `client.prompt()`；
**Then** `client.isConnected()` 返回 false；调度器标记本次 run 为 `pending_retry`，在下一个 tick（默认 30s 后）重试，最多重试 3 次（30s / 1m / 5m），超过则标记 `failed` 并停用任务 + OS 通知用户。

### 场景 5: 任务触发后 LLM 跑到需要 permission 审批

**Given** 任务 prompt 触发了 shell / fs 工具，工具需要用户审批（`src/darvin-agent/internal/agents/perm/permission_gate.go:65` 有 Timer）；
**When** 调度器无人值守，permission gate 等不到响应；
**Then** 走现有 deny 路径（timeout → deny）；run 状态记为 `failed`，error 字段填"permission timeout"。

## 3. 功能需求

### FR-1: 三种 schedule.kind

| Kind | 字段 | 例子 | 行为 |
|------|------|------|------|
| `at` | `at: ISO8601 string` | `2026-08-20T09:00:00+08:00` | 一次性到点跑一次后自动 `enabled=false`（不删除，留作历史） |
| `every` | `everyMs: number; anchorMs?: number` | `everyMs: 3600000` | 每 N 毫秒跑；可选 anchorMs 对齐起点 |
| `cron` | `expr: string; tz?: string` | `0 9 * * *` + `tz: 'Asia/Shanghai'` | 标准 5 段 cron；tz 缺省 `local`。LobsterAI `ScheduleCron` 同步还带 `staggerMs?: number` 防重入 jitter（`types.ts:14-19`），v1 简化不引入 |

对齐 LobsterAI `src/scheduledTask/types.ts:3-21` 的 `ScheduleAt / ScheduleEvery / ScheduleCron` 形状。

### FR-2: 任务定义字段

```
Schedule {
  id            string (uuid)
  workspaceId   string       // FK
  agentId       string?      // 可选，绑 agent 后自动用其 systemPrompt/identity
  name          string       // 用户起的名字
  enabled       boolean
  kind          'at' | 'every' | 'cron'
  // discriminated union per kind
  schedule      JSON         // {at? | everyMs,anchorMs? | expr,tz?}
  prompt        string       // 启动消息
  sessionTitle  string?      // 自动生成的 session 标题模板，支持 {date} 占位
  // 审计
  createdAt     number (ms)
  updatedAt     number (ms)
  lastFiredAt   number? (ms)
  nextFireAt    number? (ms)  // 调度器维护，查询时按这个排序
  // 失败统计
  consecutiveErrors int       // 触发 retry policy 用
}
```

### FR-3: 运行记录字段

```
ScheduleRun {
  id            string (uuid)
  scheduleId    string       // FK
  triggeredAt   number (ms)  // 调度器实际触发时间
  trigger       'scheduled' | 'manual'
  sessionId     string?      // 该次跑创建/复用的 session id
  runId         string?      // agent.prompt 返回的 runId，对应 message 流
  startedAt     number?
  endedAt       number?
  status        'pending' | 'running' | 'done' | 'failed' | 'aborted'
  error         string?
  attempts      int          // 重试计数
}
```

### FR-4: IPC 通道（在 main 端注册，对外暴露）

| Channel | 方向 | 说明 |
|---------|------|------|
| `schedule:list` | renderer → main | 列出当前 workspace 下所有 schedule（含 disabled） |
| `schedule:get` | renderer → main | 按 id 拿单个 |
| `schedule:create` | renderer → main | 校验 + 落库 + 计算 nextFireAt |
| `schedule:update` | renderer → main | patch 模式更新 |
| `schedule:delete` | renderer → main | 删除 schedule + 级联 schedule_runs |
| `schedule:toggle` | renderer → main | 单独切换 enabled |
| `schedule:run_now` | renderer → main | 立即触发（不等 tick），乐观写一条 manual run |
| `schedule:abort` | renderer → main | 立即停止正在跑的 schedule run（复用现有 `darvin:abort` 协议，见 `src/main/index.ts:1422`） |
| `schedule:list_runs` | renderer → main | 按 scheduleId 拿 run 历史（分页） |
| `schedule:list_all_runs` | renderer → main | 当前 workspace 全部 run（全局历史） |

### FR-5: 推送事件

新增三个 push event（在 `src/shared/darvin-api.ts` 的 `DarvinPushEvent` 加 union 成员）：

| Event | Payload | 触发时机 |
|-------|---------|----------|
| `SchedulesChanged` | `{ workspaceId }` | schedule 列表变化（create/update/delete/toggle） |
| `ScheduleRunsChanged` | `{ scheduleId, runId }` | 单条 run 状态变化 |
| `ScheduleFired` | `{ scheduleId, runId, triggeredAt }` | 调度器真正触发 agent.prompt 的时刻，前端 toast |

main 端 `EventRouter` 新增这三个 case → `webContents.send`。

### FR-6: 重试策略（完整指数退避，对齐 OpenClaw）

调度器触发时若失败（agent 未就绪 / prompt RPC 异常），采用 **OpenClaw 完整指数退避方案**（用户决策；`~/桌面/github-project/LobsterAI/src/scheduledTask/cronJobService.ts` 的 retry/backoff 逻辑）：

| Attempt | 等待时长 | 累计时间 |
|---------|----------|----------|
| 1 | （立即触发） | 0 |
| 2 | 30s | 30s |
| 3 | 1m | 1m30s |
| 4 | 5m | 6m30s |
| 5 | 15m | 21m30s |
| 6 | 60m | 1h21m30s |

**行为规则**：

- attempts 1-6 按上表递增等待；attempts == 6 仍失败 → `status=failed`，`consecutiveErrors++`，任务自动 `enabled=false`，OS 通知用户。
- `consecutiveErrors` 累计到 5 → UI 在任务卡片显示「连续失败」徽章（与 LobsterAI `cronJobService.ts` 同行为）。
- **指数退避在 schedule 自身内部独立计算**（不依赖 cron tick）：失败后 `UPDATE schedules SET next_fire_at = now + backoffMs, consecutive_errors = consecutive_errors + 1`，让下次 tick 自然捡起来。
- 等待期间 schedule 仍处于 enabled 状态（用户可手动 disable / 删除 / 触发 abort）；UI 显示"下次触发：<倒计时>"。
- 退避期间 `next_fire_at` 已写入 DB，重启 Go agent 后退避时长不会丢失（因为是 DB 持久化的，下次 tick 继续按 `next_fire_at <= now` 判定）。

## 4. 实现方案

### 4.1 为什么调度器放在 darvin-agent gateway 内（与 LobsterAI 架构对齐的关键决策）

darvin-cowork Go agent 侧（`src/darvin-agent/`）已经是所有"agent 运行时逻辑"的所在地——session runtime / agent loop / tools / permission gate / ctxengine / GORM `globalDB` 都跑在 darvin-agent 进程内。LobsterAI 的实战证据（`~/桌面/github-project/LobsterAI/src/scheduledTask/cronJobService.ts`）也表明：cron 引擎应该和 session 持久化、agent loop 在同一进程内。

最初本 spec 论证过"v1 放 main 端"的简化路径（少一个 db schema / 少一层 IPC）。用户决策反转，理由如下：

- **sessions.db 复用**：schedule 表与 sessions / messages / app_state 共用 Go 侧 GORM `globalDB`（`DARVIN_SESSIONS_DSN` = `<userData>/darvin-agent/sessions.db`）。**不**需要在 main 端再开 better-sqlite3 文件。schedule 行可直接 `JOIN sessions` / `messages` 查历史，不必跨进程。
- **触发链路零延迟**：scheduler 触发时**直接**调 Go 侧 `Loop.Submit(req PromptRequest) (RunTicket, error)`（`sessionruntime/loop.go:134`），不再走 main → WS → Go 的 JSON-RPC 往返。失败判定（`client.isConnected()` 那种）天然不存在——Go 侧自己就在。
- **重启一致性**：scheduler 与 sessions.db 同生死；Go agent 重启时 scheduler goroutine 重新拉起，从 `schedules` 表重建内存索引，零对账。
- **与现有内部能力对齐**：ctxengine 摘要/压缩 / permission gate 超时 / subagent manager 等都是 Go 侧内部 API，scheduler 在 Go 内可直接调用；放 main 端要走 IPC 才能用这些能力。
- **LobsterAI 实战验证**：OpenClaw gateway 内做 cron 是 2026-06 大重构的结果（`migrate.ts` kv flag `scheduled_tasks_migrated_to_openclaw_v1` 守护从老 SQLite 一键迁到 gateway）；架构稳定后再无人回退。

**结论**：v1 调度器放 darvin-agent gateway（`internal/scheduledtask/` 独立包），与 LobsterAI OpenClaw gateway 对齐。main 端**只做 IPC 转发**（10 个 `schedule:*` handler 内调 `client.request('agent.schedule.<op>', payload)`），不持有 schedule 状态。

### 4.2 架构对比

```
              LobsterAI (参考)                          darvin-cowork (本 spec)
              ────────────────                          ────────────────────
Renderer      React + Redux slice                       Vue3 + composables
   │          ScheduledTasksView / TaskForm             ScheduledView / ScheduleForm
   │          window.electron.scheduledTasks.*          window.darvin.schedule.*
   ▼                                                     ▼
Preload       scheduledTasks: { list/create/... }       darvin: { schedule.list/create/... }
   ▼                                                     ▼
Main          ipcMain.handle →                          ipcMain.handle →
              CronJobService.addJob                     ScheduleProxy (薄转发层)
              (15s/3s 自适应轮询镜像)                     │ (client.request 转发)
   │                                                     │
   ▼                                                     ▼
RPC           client.request('cron.add', ...)           darvin-agent (Go)
              ────────────────────                       │
   ▼                                                     │ internal/scheduledtask/
Subprocess    OpenClaw Gateway 子进程                     │   Engine (cron tick goroutine, 30s)
              ~/.openclaw/cron/jobs.json                 │   Store (GORM DAO)
              (cron engine 跑在这里)                     │   Cron (5 段解析手写)
              │                                         │ schedules / schedule_runs 表
              │                                         │ (复用 sessions.db globalDB)
              ▼                                         ▼
              OpenClaw 内部 cron tick                    SessionRuntime.Loop.Submit
              ↓                                          ↓
              agent run (headless)                       agent run (headless)
```

**关键对齐**：LobsterAI 与 darvin v1 架构一致——cron 引擎都在 gateway 子进程内，main 端仅做 IPC 转发（不持有 schedule 状态）。

### 4.3 三层架构变更总览

| 层 | 改什么 | 在哪 |
|----|--------|------|
| Renderer | 新增 ScheduledView / ScheduleForm / ScheduleList / ScheduleDetail / ScheduleRunHistory；新增 composable `useSchedules.ts`；替换 AppShell PLACEHOLDERS；新增 i18n 键 | `src/renderer/views/ScheduledView.vue`、`composables/useSchedules.ts`、`components/scheduled/`、`layout/AppShell.vue:78-97`、`services/i18n.ts` |
| Preload | 在 `window.darvin` 上暴露 `schedule.*` 9 个方法 + 3 个 push event 订阅 | `src/preload/index.ts:83-419` |
| Main | **薄转发层**：新增 10 个 `schedule:*` ipcMain.handle（每个 handler 内 `client.request('agent.schedule.<op>', payload)` 转发到 Go 侧）；新增 `libs/scheduleProxy.ts`（仅暴露 `send(method, payload)` 一行）+ EventRouter.handle() 加 3 个 if 分支把 `agent.event.schedule.*` 转 `DarvinPushEvent.SchedulesChanged` / `ScheduleRunsChanged` / `ScheduleFired`；**不持有 schedule 状态** | `src/main/libs/scheduleProxy.ts`、`src/main/index.ts:490-1799` 新增 10 个 handler（84 → 94）、`src/main/store/EventRouter.ts:59-78` 加 3 个 if 分支 |
| Go | **新增 `internal/scheduledtask/` 包**（4 文件：`engine.go` cron tick goroutine / `store.go` GORM DAO / `cron.go` 手写 5 段解析 / `handlers.go` 10 个 `agent.schedule.*` JSON-RPC handler）；在 `internal/agents/store/` 加 `Schedule` / `ScheduleRun` GORM 模型 + AutoMigrate；在 `internal/gateway/` 注册 router 把 `agent.schedule.*` 派发到 `internal/scheduledtask/handlers.go`；通过现有 `agent.event` 通道推 `DarvinEvent.ScheduleChanged` / `ScheduleRunsChanged` / `ScheduleFired` 三个新 union 成员 | `src/darvin-agent/internal/scheduledtask/{engine,store,cron,handlers}.go`、`src/darvin-agent/internal/agents/store/schedule.go`、`src/darvin-agent/internal/gateway/router.go`、`src/darvin-agent/internal/runtime/runtime.go`（`Build` 阶段 `Engine.Start(ctx)`） |

### 4.4 数据模型

v1 schedule 表放 Go 侧 GORM `globalDB`（**复用** sessions.db，不另开 db），与 sessions / messages / app_state 同库同引擎。GORM 模型在 `src/darvin-agent/internal/agents/store/schedule.go`，在 `runtime.Build()` 阶段随 sessions / messages 一起 `AutoMigrate`。

```go
// src/darvin-agent/internal/agents/store/schedule.go
type Schedule struct {
    ID                string  `gorm:"primaryKey;type:text"`
    WorkspaceID       string  `gorm:"index:idx_sched_workspace;type:text;not null"`
    AgentID           *string `gorm:"type:text"`
    Name              string  `gorm:"type:text;not null"`
    Enabled           bool    `gorm:"not null;default:true"`
    Kind              string  `gorm:"type:text;not null"` // 'at' | 'every' | 'cron'
    ScheduleJSON      string  `gorm:"type:text;not null"`
    Prompt            string  `gorm:"type:text;not null"`
    SessionTitle      *string `gorm:"type:text"`
    CreatedAt         int64   `gorm:"not null"`
    UpdatedAt         int64   `gorm:"not null"`
    LastFiredAt       *int64
    NextFireAt        *int64  `gorm:"index:idx_sched_next_due,priority:2"`
    ConsecutiveErrors int     `gorm:"not null;default:0"`
}
// 复合索引：scheduler tick 的 hot query (enabled=true AND next_fire_at <= now)

type ScheduleRun struct {
    ID          string  `gorm:"primaryKey;type:text"`
    ScheduleID  string  `gorm:"index:idx_run_schedule,priority:1;type:text;not null"`
    TriggeredAt int64   `gorm:"not null"`
    TriggerKind string  `gorm:"type:text;not null"` // 'scheduled' | 'manual'
    SessionID   *string `gorm:"type:text"`
    RunID       *string `gorm:"type:text"`
    StartedAt   *int64
    EndedAt     *int64
    Status      string  `gorm:"type:text;not null;default:'pending'"` // 'pending'|'running'|'done'|'failed'|'aborted'
    Error       *string `gorm:"type:text"`
    Attempts    int     `gorm:"not null;default:0"`
}
// schedule_runs 在 DeleteSchedule 时硬删除（不软删）；保留期默认永久
```

DB 文件：复用 `<userData>/darvin-agent/sessions.db`（由 `DARVIN_SESSIONS_DSN` 注入；`src/main/runtime/manager.ts:113` 已设置）。**不**新开 db 文件——避免跨进程共享写，参考 `merge-databases` refactor 教训。

### 4.5 调度器实现要点（Go 侧 gateway 内）

- **tick 频率**：30s（`POLL_INTERVAL_MS = 30_000`）。LobsterAI 主进程 15s/3s 自适应轮询只镜像状态，**不**做触发（详见 § 1 参考实现澄清）；darvin 把 tick 放在 Go 侧 cron goroutine 内，30s 足够（每分钟跑的最坏 30s 误差可接受）。
- **到点判定**：`SELECT * FROM schedules WHERE enabled = true AND (next_fire_at IS NULL OR next_fire_at <= now)` → 拉一批 → 逐条触发 → 触发完 `UPDATE schedules SET next_fire_at = ?, last_fired_at = ?`。
- **nextFireAt 计算**（`internal/scheduledtask/cron.go` 手写，零外部依赖）：
  - `at` kind：`at` 时间 < now → 标记 `Enabled=false`（不删）；`at >= now` → `next_fire_at = at`
  - `every` kind：`(now - anchorMs) / everyMs` 向上取整 * everyMs + anchorMs
  - `cron` kind：`cron.go` 手写 5 段解析（< 150 行），支持 IANA tz（`time.LoadLocation` 即可，stdlib 包含 zoneinfo 编译进去——**不**引入 `robfig/cron`）。LobsterAI 同步带 `staggerMs` 防重入 jitter，v1 简化不引入。
- **触发流程**（Go 侧单进程内，零 IPC）：
  ```
  1. Engine.tick goroutine: SELECT due rows
  2. for each row:
     a. 拼稳定 sessionId = `schedule-<scheduleId>`；首次触发 sessions.create_session 拿到 sessionId；之后 Loop.Submit(PromptRequest{RunID: uuid, Content: schedule.Prompt, Provider, Model, SessionID: stableSessionId}) 直传
     b. UPDATE schedules SET last_fired_at = now, next_fire_at = nextTick, consecutive_errors = ...
     c. INSERT schedule_runs (status=running, started_at=now, run_id=...)
     d. 监听 sessionruntime 推过来的 agent.event done (runId 匹配) → UPDATE schedule_runs SET status='done', ended_at=now
     e. 监听 agent.event error → UPDATE status='failed', error=?
     f. 通过 agent.event 推 DarvinEvent.ScheduleFired / ScheduleRunsChanged
  ```
- **手动触发**：`agent.schedule.run_now` JSON-RPC → 走 `Engine.triggerNow(scheduleId)`，跳过 tick 等待；`trigger_kind='manual'`，无视 `enabled`。
- **生命周期**：`runtime.Build()` 阶段 `scheduledtask.NewEngine(store, ...)` + `Engine.Start(ctx)` 起 cron goroutine；`runtime.Shutdown(ctx)` 优雅退出（停 tick、关闭进行中的 run 标记 `aborted`、把 goroutine `wg.Wait()`）。
- **abort 路径**：`agent.schedule.abort` JSON-RPC → 复用现有 `Loop.Abort(ctx, sessionId, runId)`（参考 `sessionruntime/loop.go`），同时 UPDATE schedule_runs status='aborted'。
- **与 EventRouter 协作**（main 端）：Go 侧通过现有 `agent.event` 通道（`src/shared/darvin-api.ts:1033+` 的 `DarvinEvent` union 新增 3 个成员 `ScheduleChanged` / `ScheduleRunsChanged` / `ScheduleFired`）推送。main 端 `EventRouter.handle()` 增加 3 个 if 分支把这三个事件类型转为 `webContents.send(DarvinPushEvent.SchedulesChanged / ScheduleRunsChanged / ScheduleFired, payload)`。这与 EventRouter 现有"消费 `DarvinEvent` 流"职责一致（事件名都是 DarvinEvent union 成员；推送名是 DarvinPushEvent 常量；映射在 EventRouter 一处集中做）。

### 4.6 IPC + Preload 接入（main 端薄转发）

main 端 **不持有 schedule 状态**，仅做 IPC 转发（10 个 `schedule:*` handler 内调 `client.request('agent.schedule.<op>', payload)` 把活交给 Go 侧）：

```ts
// 新增 import
import { getScheduleProxy } from './libs/scheduleProxy'

// 一行转发 helper（在 scheduleProxy.ts 内）：
// const send = (op: string) => (payload: unknown) => client.request(`agent.schedule.${op}`, payload)

// 在 setupIpcHandlers() 内、现有 84 个 handle 之后追加：
const proxy = getScheduleProxy(client)
ipcMain.handle('schedule:list',         wrapWorkspace(proxy.list))
ipcMain.handle('schedule:get',          wrapWorkspace(proxy.get))
ipcMain.handle('schedule:create',       wrapWorkspace(proxy.create))
ipcMain.handle('schedule:update',       wrapWorkspace(proxy.update))
ipcMain.handle('schedule:delete',       wrapWorkspace(proxy.delete))
ipcMain.handle('schedule:toggle',       wrapWorkspace(proxy.toggle))
ipcMain.handle('schedule:run_now',      wrapWorkspace(proxy.runNow))
ipcMain.handle('schedule:abort',        wrapWorkspace(proxy.abort))
ipcMain.handle('schedule:list_runs',    wrapWorkspace(proxy.listRuns))
ipcMain.handle('schedule:list_all_runs',wrapWorkspace(proxy.listAllRuns))
```

Go 侧对应：10 个 `agent.schedule.list / get / create / update / delete / toggle / run_now / abort / list_runs / list_all_runs` JSON-RPC method 注册在 `src/darvin-agent/internal/gateway/router.go`，handler 实现在 `src/darvin-agent/internal/scheduledtask/handlers.go`，全部委托给 `internal/scheduledtask` 包的 Engine / Store。

`src/preload/index.ts` 暴露：

```ts
schedule: {
  list, get, create, update, delete, toggle, runNow, abort,
  listRuns, listAllRuns,
  onSchedulesChanged(cb),   // 订阅 DarvinPushEvent.SchedulesChanged
  onScheduleRunsChanged(cb),
  onScheduleFired(cb),
}
```

### 4.7 Renderer 接入

`src/renderer/composables/useSchedules.ts`（参考 `useSkills.ts` / `useAgents.ts` 模式）：

```ts
export function useSchedules() {
  const schedules = ref<Schedule[]>([])
  const runs = ref<Record<string, ScheduleRun[]>>({})

  const loadAll = async () => { ... }
  const subscribe = () => {
    off1 = window.darvin.schedule.onSchedulesChanged(loadAll)
    off2 = window.darvin.schedule.onScheduleRunsChanged(({ scheduleId, runId }) => refreshRuns(scheduleId))
    off3 = window.darvin.schedule.onScheduleFired((payload) => toast(t('schedule.toast.fired', { name: ... })))
    return () => { off1(); off2(); off3() }
  }

  return { schedules, runs, loadAll, subscribe, ... }
}
```

`src/renderer/views/ScheduledView.vue`（替换 PlaceholderView）：

- 顶部 tab：列表 / 历史
- 列表：ScheduleList（卡片：name + kind 描述 + 下次触发 + toggle + 立即运行 + 删除）
- 创建：ScheduleForm（kind 选择 + 字段编辑 + workspace 选择 + agent 选择 + prompt）
- 详情：ScheduleDetail（基本信息 + 启停 + 最近 runs ScheduleRunHistory）

`src/renderer/layout/AppShell.vue:78-97`：

```ts
const PLACEHOLDERS = { /* scheduled 删掉 */ }
const currentView = computed(() => {
  switch (viewMode.mode.value) {
    // ...
    case 'scheduled':
      return ScheduledView   // 新增
  }
})
```

### 4.8 共享常量与类型

`src/shared/darvin-api.ts` 新增：

```ts
// 类型
export interface Schedule { id, workspaceId, agentId?, name, enabled, kind: 'at'|'every'|'cron', schedule: ScheduleBody, prompt, sessionTitle?, createdAt, updatedAt, lastFiredAt?, nextFireAt?, consecutiveErrors }
export type ScheduleBody = { kind: 'at'; at: string } | { kind: 'every'; everyMs: number; anchorMs?: number } | { kind: 'cron'; expr: string; tz?: string }
export interface ScheduleRun { id, scheduleId, triggeredAt, trigger: 'scheduled'|'manual', sessionId?, runId?, startedAt?, endedAt?, status, error?, attempts }

// DarvinApi 加 9 个方法签名
schedule: { list, get, create, update, delete, toggle, runNow, listRuns, listAllRuns }

// DarvinPushEvent 加 3 个
type DarvinPushEvent = ...
  | { type: 'SchedulesChanged'; payload: { workspaceId: string } }
  | { type: 'ScheduleRunsChanged'; payload: { scheduleId: string; runId: string } }
  | { type: 'ScheduleFired'; payload: { scheduleId: string; runId: string; triggeredAt: number } }
```

### 4.9 i18n

`src/renderer/services/i18n.ts` 双语键新增（assertSameKeys 强制对齐）：

```ts
// zh
'schedule.nav.title': '定时任务',
'schedule.list.empty': '暂无定时任务，点右下角新建',
'schedule.form.kind.at': '指定时间',
'schedule.form.kind.every': '固定间隔',
'schedule.form.kind.cron': 'Cron 表达式',
'schedule.form.field.prompt': '启动提示词',
'schedule.form.field.workspace': '工作区',
'schedule.form.field.agent': 'Agent（可选）',
'schedule.form.field.cron.expr': 'Cron 表达式（5 段）',
'schedule.form.field.cron.tz': '时区（默认系统）',
'schedule.form.field.every.ms': '间隔（毫秒）',
'schedule.form.field.at': '触发时间',
'schedule.card.next': '下次：{time}',
'schedule.card.last': '上次：{time} 或 未触发',
'schedule.card.actions.runNow': '立即运行',
'schedule.card.actions.toggle': '启停',
'schedule.card.actions.edit': '编辑',
'schedule.card.actions.delete': '删除',
'schedule.history.title': '运行历史',
'schedule.history.empty': '尚无运行记录',
'schedule.history.col.triggeredAt': '触发时间',
'schedule.history.col.trigger': '触发方式',
'schedule.history.col.status': '状态',
'schedule.history.col.duration': '耗时',
'schedule.history.col.error': '错误',
'schedule.toast.fired': '{name} 已触发运行',
'schedule.toast.disabled_after_failure': '{name} 连续失败已自动暂停',
'schedule.confirm.delete': '确认删除 {name}？运行历史将一并清除。',
```

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 应用未启动期间 schedule 错过触发 | 不补跑；`consecutiveErrors` 不增；下次启动后按 next_fire_at 重算（LobsterAI 同行为）。在 UI 上把过期次数展示出来，作为"需开启 autoLaunch"的提示。 |
| Go 子进程未就绪（RuntimeMgr 启动中 / 重启） | `client.isConnected()` false → 写 run（status=pending, attempts++）→ 下一 tick 重试；3 次失败 → disable + OS 通知 |
| `agent.prompt` 抛错（WS 断） | 同上 retry；不计入 `consecutiveErrors`（区分"agent 不通"与"agent 跑失败"） |
| permission gate 无人响应 | Go 侧 `permission_gate.go:65` 现有 timeout 路径 → run 记 failed，error 字段填"permission timeout" |
| 单 schedule 多次 tick 期间重叠触发 | 用 `next_fire_at` 作为乐观锁：UPDATE WHERE next_fire_at = <原值> 失败则跳过 |
| 用户在前端删了 schedule，但调度器内存里还持有它 | 每 tick 重新从 DB 读（无内存态），自然一致 |
| schedule 触发的 session 是固定的（每次复用同一 session） | 单 session 长对话交给 `internal/agents/ctxengine` 现有摘要/压缩路径处理；v1 不额外做事。`gateway/sessionmgr.go:34-36` 的 `reapIdleSessions`（DefaultIdleTTL=24h）仍兜底空闲回收 |
| 改了 system clock | v1 不防；提示用户在 UI 上看到 next_fire_at 异常时手动 toggle |
| workspace 被删了，schedule 还指向它 | `ON DELETE CASCADE`；或软删（workspace 有 status 字段）+ schedule 列出时过滤 |
| 创建 schedule 时 prompt 为空 | 表单校验拦截，不落库 |
| agentId 指向不存在的 agent | 创建时校验 reject；运行时若 agent 已删，让 prompt 走 fallback（不用 agent 的 systemPrompt） |
| cron 表达式非法 | 客户端 cron-parser 预校验 + 服务端校验双层；UI 红字提示 |
| 时区不存在 | tz 字段落 `local` 默认值；UI 选择器只给 IANA 时区白名单 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/views/ScheduledView.vue` | 新建；替换 PlaceholderView。tabs = 列表 / 历史 |
| `src/renderer/components/scheduled/ScheduleList.vue` | 新建；任务卡片 + 启停 + 立即运行 + 编辑 + 删除 |
| `src/renderer/components/scheduled/ScheduleForm.vue` | 新建；kind 选择 + schedule 字段编辑 + workspace/agent/prompt |
| `src/renderer/components/scheduled/ScheduleDetail.vue` | 新建；基本信息 + ScheduleRunHistory 子组件 |
| `src/renderer/components/scheduled/ScheduleRunHistory.vue` | 新建；table + 分页 |
| `src/renderer/composables/useSchedules.ts` | 新建；单例 state + IPC 调用 + push 事件订阅 |
| `src/renderer/layout/AppShell.vue:78-97` | 改 PLACEHOLDERS + currentView 加 `case 'scheduled': return ScheduledView` |
| `src/renderer/services/i18n.ts` | 新增 zh/en `schedule.*` 键，assertSameKeys 兜底 |
| `src/shared/darvin-api.ts:1033-1043` | DarvinPushEvent 加 3 个 union 成员 |
| `src/shared/darvin-api.ts:1090-1292` | DarvinApi 加 `schedule.*` 9 个方法签名 + Schedule / ScheduleRun / ScheduleBody 类型 |
| `src/preload/index.ts:83-419` | `api.schedule` 暴露 9 个 invoke + 3 个 on 订阅（与 `api.skills` / `api.mcp` 同款模式） |
| `src/main/libs/scheduleProxy.ts` | 新建；薄转发层，仅暴露 `proxy.<op>(payload) => client.request('agent.schedule.<op>', payload)`，不持有 schedule 状态 |
| `src/main/index.ts:490-1799` | 新增 10 个 ipcMain.handle（84 → 94），全部走 `proxy.X()` 转发到 Go；`app.whenReady` 不再启 runner（runner 在 Go 侧） |
| `src/main/store/EventRouter.ts:59-78` | `handle()` 方法内新增 3 个 if 分支，把 Go 端推过来的 `DarvinEvent.ScheduleChanged` / `ScheduleRunsChanged` / `ScheduleFired` 转为 `webContents.send(DarvinPushEvent.SchedulesChanged / ScheduleRunsChanged / ScheduleFired, payload)` |
| `src/darvin-agent/internal/scheduledtask/engine.go` | 新建；cron tick goroutine（30s POLL_INTERVAL_MS）+ 触发逻辑 + 指数退避计算 |
| `src/darvin-agent/internal/scheduledtask/store.go` | 新建；GORM DAO（Schedule / ScheduleRun CRUD + SelectDue + IncrementConsecutiveErrors） |
| `src/darvin-agent/internal/scheduledtask/cron.go` | 新建；手写 5 段 cron 解析（< 150 行）+ IANA tz 支持（time.LoadLocation） |
| `src/darvin-agent/internal/scheduledtask/handlers.go` | 新建；10 个 `handleSchedule*` JSON-RPC handler（list / get / create / update / delete / toggle / run_now / abort / list_runs / list_all_runs） |
| `src/darvin-agent/internal/agents/store/schedule.go` | 新建；Schedule / ScheduleRun GORM 模型 + AutoMigrate |
| `src/darvin-agent/internal/gateway/router.go` | 注册 `agent.schedule.*` 10 个 method 名到 `scheduledtask.Handlers` 的派发表 |
| `src/darvin-agent/internal/runtime/runtime.go` | `Build()` 阶段 `scheduledtask.NewEngine(store, llm, sessionMgr, eventBus) + Engine.Start(ctx)`；`Shutdown()` 阶段 `Engine.Stop(ctx)` 优雅退出 |
| `src/darvin-agent/internal/sessionruntime/event_bus.go`（或现有等价物） | 新增 `ScheduleFired` / `ScheduleRunsChanged` / `ScheduleChanged` 三个 `DarvinEvent` 事件类型；scheduler 通过 event bus 推送 |

## 7. 落地分阶段

### v1 MVP（本次 spec）

- 10 个 IPC 方法（list / get / create / update / delete / toggle / run_now / abort / list_runs / list_all_runs）+ 3 个 push event（SchedulesChanged / ScheduleRunsChanged / ScheduleFired）完整落地
- 三种 schedule.kind：at / every / cron
- **Go 侧** `internal/scheduledtask/{engine,store,cron,handlers}.go`（4 文件）：cron tick goroutine 30s + GORM DAO + 手写 5 段解析 + 10 个 handler
- **main 端** `libs/scheduleProxy.ts` 薄转发层 + 10 个 ipcMain.handle（84 → 94）
- 触发走 Go 侧 `Loop.Submit(PromptRequest)` headless turn，复用现有 SessionRuntime / Loop
- 失败走**完整指数退避**（30s → 1m → 5m → 15m → 60m，6 次失败自动 disable）+ OS 通知
- 复用 session 策略：每个 schedule 复用稳定 sessionId `schedule-<scheduleId>`（agent 上下文连续）
- ScheduledView + ScheduleForm + ScheduleList + ScheduleDetail + ScheduleRunHistory
- i18n zh/en 双语完整

### v2（不在本 spec）

- IM 入口：复用 LobsterAI `imScheduledTaskHandler.ts` 正则 + LLM 抽 scheduleAt 模式
- 投递到 IM / 邮件 / Webhook（schedule.run 完成后向 IM bot 发消息）
- cron 表达式人可读化（cronstrue 库）
- schedule_runs 表 TTL（90 天清理）

### v3（不在本 spec）

- 注入 `## Scheduled Tasks` + native tool 到 agent system prompt，让 agent 在 Cowork 会话里帮用户创建 schedule
- 把 cron 引擎从单进程 Go gateway 拆成独立 sidecar（如果未来出现多 darvin-cowork 客户端共享一份 schedule 的需求）

## 8. 验收标准

### 8.1 用户场景

- [ ] 场景 1（cron）：创建 cron `0 9 * * *` 任务 → 等到 9 点（或手动调 `nextFireAt` 模拟）→ Go 侧 cron goroutine tick 触发 → sessions.db 内 session 跑 turn → sidebar 出现该 session → 详情页 history 多一条 `done`
- [ ] 场景 2（手动）：点立即运行 → 1s 内出现新 run（status=running）→ 等 turn 完成 → status=done
- [ ] 场景 3（启停/删除）：toggle off → 下次 tick 不触发；删除 → 列表与 history 清空（ON DELETE CASCADE）
- [ ] 场景 4（指数退避）：让 Go 侧 mock 一个临时故障 → run status=failed → 30s 后自动重试 → 仍失败 → 1m 后再试 → … → 6 次失败后自动 disable + OS 通知（验证 30s → 1m → 5m → 15m → 60m 表）
- [ ] 场景 5（abort）：点运行中的 schedule 卡片「停止」按钮 → 1s 内 run status='aborted'，Loop.Abort 复用现有协议
- [ ] 场景 6（session 复用）：同一 schedule 第 2 次触发时 sessionId 与第 1 次相同（agent 上下文连续，能引用上次输出）

### 8.2 工程指标

- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过（vitest 跑通；Go 侧 `internal/scheduledtask/{cron,store,handlers}` 关键函数有 `*_test.go`：5 段 cron 解析 / nextFireAt 计算 / 指数退避表 / kind 校验）
- [ ] `cd src/darvin-agent && go test ./... && make check` 通过（golangci-lint + gofmt + goimports + staticcheck ST10xx + readability）
- [ ] `npm start` 启动后 `electron-cdp` 驱动连真实窗口，手动走完创建 → 触发 → 详情查看全流程
- [ ] DarvinApi 接口与 preload 暴露的字段一一对应；DarvinPushEvent 三个新事件被 EventRouter 正确转发（DevTools 网络面板可见）
- [ ] i18n zh/en 双语 key 严格一致（assertSameKeys 不抛）
- [ ] 无 `Co-Authored-By` / 无 stage 注释 / 无 broad refactor

### 8.3 边界场景

- [ ] 删除 schedule 后级联清 runs（ON DELETE CASCADE）
- [ ] Go agent 重启期间不丢失的 pending run 重试正常
- [ ] 跨工作区 schedule 互不干扰
- [ ] 同 schedule 30s tick 期间不会重复触发（乐观锁或查询条件保证）

### 8.4 关键文件 file:line 自检（实现完成后核对）

| file:line | 期望内容 |
|-----------|----------|
| `src/main/index.ts`（在某行）| `ipcMain.handle('schedule:create', ...)` |
| `src/main/libs/scheduleProxy.ts`（在某几行） | 10 个 proxy 方法各自 `client.request('agent.schedule.<op>', payload)` 一行转发 |
| `src/darvin-agent/internal/scheduledtask/cron.go:1-150` | 手写 5 段解析（支持 `*` / `,` / `-` / `/` / 数字） |
| `src/darvin-agent/internal/scheduledtask/engine.go:ticker()` | 30s tick goroutine + SelectDue + 触发 + 监听 done/error |
| `src/darvin-agent/internal/agents/store/schedule.go` | Schedule / ScheduleRun GORM 模型 + AutoMigrate |
| `src/darvin-agent/internal/gateway/router.go` | 10 个 `agent.schedule.<op>` method 注册 |
| `src/main/store/EventRouter.ts:59-78` | handle() 新增 3 个 if 分支把 `DarvinEvent.ScheduleChanged/ScheduleRunsChanged/ScheduleFired` 转 `webContents.send` |
| `src/preload/index.ts:83-419` | `api.schedule` 暴露 10 方法 + 3 个 on 订阅 |
| `src/shared/darvin-api.ts` | DarvinPushEvent + DarvinApi 多 12 项 |
| `src/renderer/layout/AppShell.vue:78-97` | PLACEHOLDERS 删 scheduled；currentView 加 case |
| `src/renderer/composables/useSchedules.ts` | onSchedulesChanged / onScheduleFired 订阅 |
| `src/renderer/services/i18n.ts` | 至少 25 个新 `schedule.*` 键 |

## 9. 自检问题清单（实现前请用户过目）

实现开始前请回答以下问题，任一为"否"或"不确定"则回头改 spec：

1. **调度器位置**：✅ **darvin-agent gateway 内**（用户决策；与 LobsterAI OpenClaw gateway 架构对齐；详见 § 4.1 论证：复用 sessions.db / 触发零 IPC 延迟 / 重启一致性 / 与 ctxengine / permission_gate 同进程）
2. **失败重试**：✅ **完整指数退避**（用户决策；30s → 1m → 5m → 15m → 60m，6 次失败自动 disable + 通知；与 OpenClaw `cronJobService.ts` 重试逻辑对齐；详见 FR-6 表）
3. **miss-fire 行为**：✅ 接受"应用未启动期间 schedule 不补跑"（与 LobsterAI `migrate.ts` 同行为；不引入 OS 级 cron / autoLaunch 联动）
4. **数据存储**：✅ 接受 schedule 表放 main 端 SQLite（`schedule.db`，与 `mcp.db` / `skill-state.db` 同款；不与 Go 侧 `sessions.db` 共享，参考 `merge-databases` refactor 教训——避免跨进程共享写）
5. **三种 schedule.kind**：✅ **v1 全做 at + every + cron**（用户决策；前端表单 3 个 tab、IPC 校验 3 套字段、cron 解析 + 锚点/every 计算）
6. **session 复用策略**：✅ **复用同一个 session**（用户决策；sessionId 稳定为 `schedule-<taskId>`，首次触发 `create_session`，之后 `client.prompt({sessionId})` 直传同一 id 复用；agent 上下文连续，长对话走现有 ctxengine 摘要/压缩）
7. ~~缺失——编号跳过~~
8. **运行历史保留期**：默认永久，v1 不引入 TTL（90 天清理留 v2 评估）
9. **OS 通知触发条件**：✅ 仅"3 次失败自动 disable"时通知（用户决策；成功完成 silent，参考 `agentTaskNotifier.ts:32` 现有克制风格）
10. **立即停止正在跑的任务按钮**：✅ **v1 实现 `schedule:abort`**（用户决策；复用现有 `darvin:abort` 协议 `src/main/index.ts:1422`，UI 在 ScheduleDetail 卡片上加按钮；仅在 run 状态为 `running` 时显示）

确认上述决策已纳入正文，进入实现阶段。