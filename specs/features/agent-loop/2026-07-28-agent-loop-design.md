# Agent 运行时（Agent Loop）设计文档

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 的目标架构（`AGENTS.md`）明确把 Agent 业务逻辑下放到 Go 运行时 `darvin-agent`（`src/darvin-agent/`），由 Electron 主进程以子进程方式拉起。当前状态：

- **已落地（M1）**：`internal/agent/llm` 包完成统一 LLM 客户端抽象——`ModelProvider` 接口、`StreamEvent` 事件协议（`StartEvent` / `TextDeltaEvent` / `ToolCall*` / `DoneEvent` / `ErrorEvent`）、Anthropic provider、流式 HTTP/SSE 解析、Provider 注册表。`docs/plan/m1-model-provider.md` 同步归档。
- **已落地（基础设施）**：`config`（viper）、`logger`（zap）、`database`（gorm + sqlite，已在 `go.mod`）、`cmd/app/main.go` 启动脚手架（仅 init，无业务）。
- **未落地**：Agent 调度循环、会话管理、工具注册表、用户消息队列、中止机制。
- **未在本 spec 范围**：上下文压缩、记忆系统、Skills、MCP、IPC 协议（Electron 主进程 ↔ darvin-agent）、子 Agent、UI。

参考设计来源：`docs/agent/00_OVERVIEW.md` 起的 5 篇文档（OpenClaw 框架，TypeScript 写就），本 spec 把它映射到 Go 的 `darvin-agent` 进程内。

### 1.2 目标

1. 实现一个**进程内**的 Agent 调度循环：接收用户消息 → 调 LLM → 处理工具调用 → 产出 assistant 消息，重复直到自然停止。
2. 实现**可注册的工具系统**与本里程碑约定的 5 个内置工具（`read_file` / `write_file` / `edit_file` / `list_dir` / `shell`），带 cwd 沙箱与 shell 命令白名单。
3. 实现**会话管理**：内存版 `Session` 持有消息历史，配套 `SessionStore` 接口（内存实现 + 后续 SQLite 落地的接缝）。
4. 实现**事件订阅**：上层（包括未来的 IPC 层）通过订阅 channel 拿到 Agent 完整生命周期事件。
5. 实现**消息队列与中止**：`prompt` / `steer` / `followup` 三种入队模式，`abort(ctx)` 通过 `context.Context` 中断正在跑的 LLM 调用和工具执行。
6. **不引入新依赖**：复用 `gorm.io/driver/sqlite`（仅本 spec 留 Store 接口、不立即落 SQLite Store）、`go.uber.org/zap`、现有 `llm` 包。JSON Schema 校验自写最小子集，不引入第三方 schema 库。

### 1.3 非目标

- **IPC 协议**：Electron 主进程 ↔ darvin-agent 的通信形态（stdio JSONL / WS / UDS）由后续独立 spec 决定。本 spec 只暴露进程内 Go API（`Agent` / `Session` / `Subscribe`），不写任何 transport。
- **上下文压缩 / ContextEngine**：不实现 token 预算检查、不实现 compaction、不实现 DAG 分支管理。`assemble()` 留接口位但本里程碑返回原始消息。
- **记忆系统（短期 / 中期 / 长期 / Dreaming）**：完全不实现。
- **Skills（SKILL.md 加载、调度策略）**：完全不实现。
- **MCP 客户端**：完全不实现。
- **子 Agent**：`prepareSubagentSpawn` / `onSubagentEnded` 不实现。
- **持久化层落地**：本 spec 只定义 `SessionStore` 接口并提供 `MemoryStore` 实现；SQLite 实现留 TODO，后续 spec 单独评审。
- **流式 UI 协议**：事件 payload 用 Go 结构体，JSON 序列化由 IPC 层做。
- **多模型并行 / 跨模型思考签名保留**：本里程碑单 Agent 单 Provider 单 Model，不实现跨模型消息转换。
- **Token 精确计算**：本里程碑用「字符数 / 4」粗估占位（仅用于日志/调试，不触发压缩）。

---

## 2. 用户场景

### 场景 1：单轮问答，无工具

**Given** 用户输入「Go 1.22 引入了哪些新特性？」
**When** Agent 收到 prompt
**Then**
1. 调度循环从 Session 取消息历史（首轮为空）+ system 指令
2. 调用 `llm.Stream` → 收到 `TextDeltaEvent` 序列 → 收到 `DoneEvent { FinishReason: stop }`
3. 累积成一条 `assistant` 消息写回 Session
4. emit `AgentEndEvent`，无工具调用

### 场景 2：单轮任务，多次工具调用

**Given** 用户输入「读 `main.go` 然后用一句话总结它在做什么」
**When** Agent 收到 prompt
**Then**
1. 第一轮 LLM 输出 `ToolCallStartEvent {Name: "read_file"}` + delta → `ToolCallEndEvent {Arguments: {path: "main.go"}}`
2. Executor 查工具注册表 → 命中 → `read_file` 在 cwd 沙箱内读文件 → 返回 `Result{Content: "<文件内容>"}`
3. 把 `tool` role 消息（含 `ToolCallID`）append 到 messages
4. 第二轮 LLM 基于工具结果生成自然语言总结 → `DoneEvent { FinishReason: stop }`
5. emit `AgentEndEvent`，Session 里有 2 条 assistant 消息 + 1 条 tool 消息

### 场景 3：用户中止长任务

**Given** Agent 正在跑 shell 工具（grep 大量文件，预计 30s）
**When** 用户调用 `Agent.Abort(ctx)` 传入已 cancel 的 context
**Then**
1. 当前工具执行的 goroutine 收到 `ctx.Done()` → shell 子进程被 kill → 工具返回 `ErrAborted`
2. 当前 turn 的 `AssistantMessage` 标记 `stopReason=aborted` 写回 Session
3. emit `AgentErrorEvent { Err: ErrAborted }`
4. Session 状态回到 idle，下次 `prompt` 可正常开始新轮次

### 场景 4：followup 队列在 agent_end 后自动处理

**Given** Agent 上一轮 `agent_end` 刚刚 emit
**When** 用户在 agent_end 之前/之后调用 `Agent.FollowUp("继续")`
**Then**
1. 若 agent 仍在跑：消息进 followup 队列，等待 `agent_end` 后自动启动下一个 Run
2. 若 agent 已 idle：立即启动下一个 Run

### 场景 5：steer 队列中断当前轮

**Given** Agent 正在跑（LLM 流式输出中）
**When** 用户调用 `Agent.Steer("换个思路，忽略上面的指令")`
**Then**
1. 当前 LLM 调用被 cancel（abort current run）
2. steer 消息进入下一轮的 messages 头部
3. 立即开始下一轮 Run

---

## 3. 功能需求

### FR-1: Agent 结构

- `Agent` 持有：`Name`、`Instructions`（system prompt）、`Model`（provider + model id）、`Tools`（`*tool.Registry`）、`Session`（`*Session`）、`MaxTurns`（默认 25）、`Logger`。
- `Agent` 是 goroutine-safe：通过内部 mutex 保护状态转换、用 channel 协调 prompt/steer/followup 入队。

### FR-2: 调度循环

- `Agent.Run(ctx)` 是核心入口：消费一个或多个入队消息，循环执行 turn，直到 `FinishReason ∈ {stop, length, error, aborted}` 或达到 `MaxTurns`。
- 每个 turn：组装 messages → `provider.Stream()` → 累积 assistant 消息 → 若有 tool calls 则执行工具并把 tool result append 到 messages → 进入下一 turn。
- 单 turn 内若 LLM 输出 N 个 tool call：默认**并行**执行（goroutine + WaitGroup），全部完成后把 N 条 tool result 消息按原顺序 append 到 messages。

### FR-3: Session

- `Session` 持有：会话级元数据（`ID`、`CreatedAt`、`UpdatedAt`）+ 消息历史（`[]llm.Message`，含 system/user/assistant/tool）。
- `Session.Messages()` 返回拷贝（防止外部直接修改内部 slice）。
- `Session.Append(msg llm.Message)` append 并刷新 `UpdatedAt`。
- 消息历史完整、未压缩；后续 ContextEngine spec 会引入组装层。

### FR-4: SessionStore

```go
type SessionStore interface {
    Save(ctx context.Context, s *Session) error
    Load(ctx context.Context, id string) (*Session, error)
    List(ctx context.Context) ([]SessionMeta, error)
    Delete(ctx context.Context, id string) error
}
```

- 本 spec 提供 `MemoryStore`（`map[string]*Session` + `sync.RWMutex`）作为唯一实现。
- SQLite Store 留 `// TODO: implement with gorm` 注释，不在本 spec 落地。

### FR-5: 工具接口与注册表

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() llm.ParameterSchema
    Execute(ctx context.Context, args map[string]any) Result
}

type Result struct {
    Content  string
    IsError  bool
    Metadata map[string]any
}
```

- `tool.Registry` 提供 `Register(Tool)` / `Get(name)` / `Names() []string` / `Specs() []llm.Tool`（把 Tool 转成 LLM 可见的 `llm.Tool` 描述）。
- 参数校验：`Execute` 调用前用自写最小 JSON Schema 校验器检查 `args` 是否满足 `Parameters()` 声明的 `type` / `required` / `properties`。校验失败直接返回 `Result{IsError: true, Content: "invalid arguments: ..."}`，不进入工具实现。
- 工具执行统一走 `ctx`，cancel 时工具应尽快返回（执行者用 `goroutine` 启动工具；如工具是阻塞系统调用，应自行 select on `ctx.Done()`）。

### FR-6: 内置工具（5 个）

所有文件类工具统一在 `fsSandbox` 限制下工作：相对路径 resolve 到 `agent.workdir`（默认 cwd）下，绝对路径必须落在 workdir 内部（用 `filepath.Clean` + `strings.HasPrefix` 检查）。`workdir` 外访问一律 `Result{IsError: true}`。

| Name | 用途 | 参数（JSON Schema） | 行为 |
|------|------|---------------------|------|
| `read_file` | 读文件 | `{path: string, limit?: number, offset?: number}` | 读 UTF-8 文本，按 limit/offset 截断；超 1 MiB 截断并加 marker |
| `write_file` | 写文件 | `{path: string, content: string}` | 覆盖写入；自动创建父目录；与 read_file/edit_file 共用 fsSandbox |
| `edit_file` | 局部替换 | `{path: string, old_text: string, new_text: string, replace_all?: bool}` | 找不到 old_text 返回 IsError；replace_all=true 替换全部 |
| `list_dir` | 列目录 | `{path: string, max_depth?: number}` | 返回条目（name / type / size）；path 必须为目录 |
| `shell` | 跑命令 | `{command: string, args: string[], cwd?: string, timeout_ms?: number}` | 白名单（见下）+ cwd 沙箱 + 超时 + 收集 stdout/stderr |

`shell` 白名单（拒绝对系统的破坏性命令；本里程碑硬编码，后续 spec 走配置）：

```
ls cat head tail wc grep find echo pwd date file which env printenv uname
mkdir cp mv rm stat du df tr cut sort uniq tee xargs basename dirname
sed awk test true false
```

- 任何不在白名单的 command → `Result{IsError: true, Content: "command not allowed: <cmd>"}`。
- `timeout_ms` 默认 30_000，上限 5_分钟；超时则 `exec.CommandContext` 杀进程。
- 工作目录默认 workdir，参数 `cwd` 必须是 workdir 内子目录。

### FR-7: 事件订阅

```go
type Event interface { isAgentEvent() }

type Subscriber interface {
    Events() <-chan Event
}

type Subscription interface {
    Unsubscribe()
}

func (a *Agent) Subscribe(buffer int) Subscription
```

- 事件全集（14 种）：
  - `PromptReceivedEvent {Content string, Mode PromptMode}` — `prompt` / `steer` / `followup` 三种模式
  - `RunStartEvent {SessionID string}`
  - `TurnStartEvent {TurnID string, TurnIndex int}`
  - `LLMStartEvent {Model string}`
  - `TextDeltaEvent {Delta string}` — 透传自 LLM 流
  - `LLMEndEvent {Assistant llm.Message, Usage llm.Usage}`
  - `ToolStartEvent {TurnID string, CallID string, Name string, Arguments map[string]any}`
  - `ToolEndEvent {CallID string, Result Result, DurationMS int64}`
  - `TurnEndEvent {TurnIndex int, StopReason llm.FinishReason}`
  - `RunEndEvent {Turns int}`
  - `AgentErrorEvent {Err error}`
  - `AgentEndEvent {SessionID string, TotalTurns int, TotalUsage llm.Usage}` — 整个 Run 结束
  - `CompactionEvent`（本 spec 不 emit，留接口位）
  - `CustomEvent {Name string, Payload any}` — 给后续 spec 注入自定义事件
- 订阅 channel 满时，策略为 **drop oldest** 并在日志记 warn（避免阻塞 Agent 主循环）。
- `Unsubscribe()` 取消订阅、关闭 channel。

### FR-8: 消息队列与 PromptMode

- `Agent.Prompt(ctx, content string) error` — 立即入队并触发 Run；若当前已有 Run 在跑，返回 `ErrAgentBusy`（调用方应改用 `Steer` 或 `FollowUp`）。
- `Agent.Steer(ctx, content string) error` — 取消当前 Run、把消息注入下一轮 messages 头部、立即重启 Run。
- `Agent.FollowUp(ctx, content string) error` — 把消息入 followup 队列；若 Agent 当前 idle 则立即触发 Run；否则等当前 `AgentEndEvent` 之后自动触发。
- `Agent.Abort(ctx context.Context) error` — 取消当前 Run 的 `context.Context`；不修改队列。

### FR-9: 中止与取消

- `Agent.Run` 接受 `context.Context`；所有下游 LLM 调用与工具执行透传 ctx。
- LLM 层：`provider.Stream(ctx, ...)` 已支持（见 `llm/provider.go:30` 的 ctx 参数）；ctx cancel 时 provider 关闭 HTTP body，事件流 channel 提前关闭。
- 工具层：工具实现必须 `select { case <-ctx.Done(): ... ; case result := <-doWork(): ... }`；`shell` 工具用 `exec.CommandContext` 透传。
- 取消后当前 turn 的 assistant 消息以 `stopReason=aborted`（自定义 FinishReason，见 §4.2 边界）写回 Session；emit `AgentErrorEvent {Err: ErrAborted}`。

### FR-10: 配置

- `config.yaml` 新增 `agent` 段（viper 自动 map）：

  ```yaml
  agent:
    max_turns: 25
    tool_timeout_ms: 30000
    workdir: ""          # 空字符串 = 进程 cwd
    shell_allowlist: []  # 空 = 用内置白名单 defaultShellAllowlist
    event_buffer: 64
    provider_name: anthropic
    model: claude-sonnet-4-5
    instructions: ""
  ```

- `internal/config/config.go` 增加 `AgentConfig` 字段与 `mapstructure` 标签（见 §4.10）。
- 用户未在 yaml 写 `shell_allowlist`（空切片 / nil）时，agent 工具层用 §FR-6 列出的 `var defaultShellAllowlist = []string{...}` 兜底。

---

## 4. 实现方案

### 4.1 目录与包结构

**设计原则**：`internal/agent/` 下采用 **根包 + 平铺子包** 结构。根包 `package agent` 只承担最薄的「装配 + 顶层 API」职责；所有"机制"作为同层子包存在（与 M1 已有的 `llm` 子包平级），便于后续按域独立演进 / 单测 / 替换。

**当前实际状态**（Glob 结果，`src/darvin-agent/`）：

```
src/darvin-agent/
├── cmd/
│   └── app/main.go                ← 仅 init,无业务（M2 不在此处接线 agent,见 §9)
├── config.yaml
├── data.db
├── go.mod                         ← module darvin-cowork/backend
├── go.sum
└── internal/
    ├── agent/
    │   └── llm/                   ← package llm (M1 已建,带 _test.go)
    ├── config/                    ← package config (viper)
    ├── database/                  ← package database (gorm+sqlite)
    └── logger/                    ← package logger (zap)
```

注意：`internal/agent/` 根目录当前**无任何 .go 文件**——本 spec 新加的 `agent.go` / `dispatcher.go` 会**创建** `package agent` 根包（与 `llm` 子包同层）。Module path 是 `darvin-cowork/backend`，所以新包 import path 为 `darvin-cowork/backend/internal/agent/...`。

**目标结构**（`src/darvin-agent/`）：

```
src/darvin-agent/
├── cmd/
│   └── app/main.go                 ← 改造: 构造 Agent + 注册 5 工具 + 订阅日志 + 跑示例 prompt
├── config.yaml                     ← 小改: 追加 agent: 段
├── go.mod                          ← 不变
└── internal/
    ├── agent/                      ← package agent 根包 (新建,跟 llm 同级)
    │   ├── agent.go                ← Agent struct + NewAgentConfig
    │   ├── dispatcher.go           ← Run + Prompt/Steer/FollowUp/Abort + 队列消费
    │   ├── errors.go               ← 根包级错误(ErrAgentBusy 等)
    │   ├── agent_test.go
    │   ├── dispatcher_test.go
    │   │
    │   ├── llm/                    ← package llm (M1 已有,小改: types.go 加 FinishReasonAborted)
    │   │
    │   ├── tool/                   ← package tool
    │   │   ├── tool.go             ← Tool interface + Result
    │   │   ├── registry.go         ← Registry
    │   │   ├── params.go           ← validateArgs 最小 JSON Schema 校验
    │   │   ├── sandbox.go          ← fsSandbox 路径检查
    │   │   ├── fs.go               ← read_file / write_file / edit_file / list_dir
    │   │   ├── shell.go            ← shell + defaultShellAllowlist
    │   │   ├── registry_test.go
    │   │   ├── params_test.go
    │   │   ├── sandbox_test.go
    │   │   ├── fs_test.go
    │   │   └── shell_test.go
    │   │
    │   ├── executor/               ← package executor
    │   │   ├── executor.go         ← RunConversation + runToolsParallel + executeOneTool
    │   │   └── executor_test.go    ← 用 fake ModelProvider 测
    │   │
    │   ├── session/                ← package session
    │   │   ├── session.go          ← Session + SessionMeta
    │   │   └── session_test.go
    │   │
    │   ├── store/                  ← package store
    │   │   ├── store.go            ← SessionStore 接口
    │   │   ├── memory.go           ← MemoryStore;SQLiteStore TODO
    │   │   └── memory_test.go
    │   │
    │   ├── queue/                  ← package queue
    │   │   ├── queue.go            ← 三 channel (prompt/steer/followup) + dequeue 优先级
    │   │   └── queue_test.go
    │   │
    │   └── event/                  ← package event
    │       ├── event.go            ← Event 接口 + 14 种事件类型 + Subscription
    │       └── event_test.go
    │
    ├── config/                     ← package config (小改: 加 AgentConfig 字段)
    ├── database/                   ← package database (不改)
    └── logger/                     ← package logger (不改)
```

**包依赖关系**（单向无环，箭头方向 = import 方向）：

```
agent (root) ──→ executor ──→ tool, llm, session, event
            ──→ tool
            ──→ llm
            ──→ session
            ──→ store
            ──→ queue
            ──→ event
tool ──→ llm          (ParameterSchema)
session ──→ llm       (Message)
store ──→ session, llm
executor ──→ tool, llm, session, event
queue    (无内部依赖)
event    (无内部依赖)
```

**关于"上下文"子包**（ContextEngine）：本 spec **不实现** token 预算 / compression / DAG 分支管理（见 §1.3）。`executor` 步骤 1 直接读 `session.Messages()` 传 LLM，**但留 TODO 接缝**：

```go
// TODO(spec: future-context-engine): replace direct session snapshot with
// ContextEngine.Assemble() once internal/agent/ctxengine/ lands.
// 命名避让 stdlib context: 后续 spec 引入时用 ctxengine 或 contextengine。
messages := a.session.Messages()
```

后续 ContextEngine spec 落地时新建 `internal/agent/ctxengine/` 子包，**本 spec 不预先创建空目录**。

**`cmd/app/main.go` 改造**：
- 构造 `agent.New(agent.NewAgentConfig{...})`（根包 API）
- `NewAgentConfig` 内部自动注册 5 个内置工具到 `tool.Registry`
- 订阅 `agent.Subscribe(128)` 事件 → zap logger
- 跑一个示例 prompt → `Run(ctx)`
- 后续 IPC spec 接手时把"读 stdin / 写 stdout"换成 transport

`internal/config/config.go` 增加 `AgentConfig` 字段（§4.10）。

### 4.2 核心类型（包级 API）

```go
// internal/agent/agent.go
package agent

type Agent struct {
    name         string
    instructions string
    model        ModelRef         // provider name + model id
    provider     llm.ModelProvider
    tools        *tool.Registry
    session      *Session
    store        SessionStore
    logger       *zap.Logger
    cfg          AgentConfig      // 来自 config.AgentConfig 的子集

    // 队列
    promptCh   chan queuedMessage
    steerCh    chan queuedMessage
    followupCh chan queuedMessage

    // 状态
    stateMu    sync.Mutex
    state      agentState        // idle | running
    cancelFn   context.CancelFunc

    // 事件
    subsMu     sync.RWMutex
    subscribers []*subscriber
}

type ModelRef struct {
    Provider string  // "anthropic"
    Model    string  // "claude-sonnet-4-20250514"
}

type AgentConfig struct {
    MaxTurns      int
    ToolTimeout   time.Duration
    Workdir       string
    ShellAllowlist []string
    EventBuffer   int
}

func New(cfg NewAgentConfig) (*Agent, error)   // 构造 + 注册工具
func (a *Agent) Subscribe(buffer int) Subscription
func (a *Agent) Run(ctx context.Context) error // 阻塞直到 agent_end
func (a *Agent) Prompt(ctx context.Context, content string) error
func (a *Agent) Steer(ctx context.Context, content string) error
func (a *Agent) FollowUp(ctx context.Context, content string) error
func (a *Agent) Abort(ctx context.Context) error
func (a *Agent) Session() *Session
```

`Agent` 的事件 channel buffer 来自 `cfg.EventBuffer`（默认 64），订阅者自己的 channel 由 `Subscribe` 内部缓冲后桥接到全局 fan-out。

**包归属**（`agent` 根包 import 哪些子包）：

| 字段 / 返回类型 | 实际包 | Import 路径 |
|-----------------|--------|-------------|
| `*tool.Registry` | `tool` | `darvin-cowork/backend/internal/agent/tool` |
| `*session.Session` | `session` | `darvin-cowork/backend/internal/agent/session` |
| `store.SessionStore` | `store` | `darvin-cowork/backend/internal/agent/store` |
| `chan queue.queuedMessage` | `queue` | `darvin-cowork/backend/internal/agent/queue` |
| `*subscriber`（非导出） | 根包内 | — |
| `event.Subscription` | `event` | `darvin-cowork/backend/internal/agent/event` |
| `llm.ModelProvider` | `llm` | `darvin-cowork/backend/internal/agent/llm` |
| `ModelRef` / `AgentConfig` / `agentState` | 根包内 | — |

`agent` 根包不直接 import `executor`。**耦合方式（采用 A：显式注入）**：`Agent` 持有 `executor.Executor` 接口字段（单方法接口，便于测试时注入 fake 实现），`NewAgentConfig` 提供可选的 `Executor executor.Executor` 字段（未填时 `New` 内部用 `executor.New()` 构造默认实现），`dispatcher.go` 通过 `a.exec.RunConversation(ctx, a)` 显式调用。

```go
// internal/agent/agent.go（增量）
package agent

import (
    "darvin-cowork/backend/internal/agent/executor"
)

type Agent struct {
    // ... 其他字段
    exec executor.Executor   // 显式注入,接口见 executor 包
}

type NewAgentConfig struct {
    // ... 其他字段
    Executor executor.Executor  // 可选;nil = New 内部构造默认实现
}
```

`executor.Executor` 是单方法接口（见 §4.4），便于测试时注入 fake 实现替换默认 `executor.runConversation`。

### 4.3 Agent 调度循环（`dispatcher.go`，根包）

`Run` 主循环伪代码：

```go
func (a *Agent) Run(ctx context.Context) error {
    runCtx, cancel := context.WithCancel(ctx)
    a.stateMu.Lock()
    a.state = stateRunning
    a.cancelFn = cancel
    a.stateMu.Unlock()
    defer func() {
        a.stateMu.Lock()
        a.state = stateIdle
        a.cancelFn = nil
        a.stateMu.Unlock()
    }()

    defer a.emit(AgentEndEvent{...})

    for {
        msg, mode, ok := a.dequeue(runCtx)
        if !ok {
            return nil  // ctx cancel 或队列空且无 in-flight
        }
        // 把消息作为 user message 写入 session
        a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg})

        // 跑单次会话（可多 turn）
        if err := a.runConversation(runCtx); err != nil {
            if errors.Is(err, ErrAborted) { return err }
            a.emit(AgentErrorEvent{Err: err})
            return err
        }
    }
}
```

- `dequeue` 优先级：`steerCh` > `promptCh` > `followupCh`；三者都空则阻塞。
- `runConversation` 调用 `executor.RunConversation`（见 §4.4）。
- `Run` 返回时机：(a) ctx cancel；(b) prompt 入队后跑完且 followup 队列空；(c) 显式 `Abort`。

### 4.4 单轮执行器（`executor/` 子包）

`executor` 子包对外暴露单方法接口 `Executor`，由 `agent` 根包持有并显式调用：

```go
// internal/agent/executor/executor.go
package executor

import (
    "context"
    "darvin-cowork/backend/internal/agent/event"
    "darvin-cowork/backend/internal/agent/llm"
    "darvin-cowork/backend/internal/agent/session"
    "darvin-cowork/backend/internal/agent/tool"
)

// Deps is the surface of agent.Agent the executor consumes. The agent root
// package satisfies this implicitly — keeping executor free of agent
// avoids an agent <-> executor import cycle.
type Deps interface {
    Session() *session.Session
    Tools() *tool.Registry
    Provider() llm.ModelProvider
    ModelName() string
    Instructions() string
    Emit(event.Event)
    Config() Config
}

// Executor 跑一次"用户消息入队 → 多 turn → 自然停止" 的完整会话。
// 实现必须透传 ctx（cancel 时立即返回 ctx.Err()）。
type Executor interface {
    RunConversation(ctx context.Context, d Deps) error
}

// New 构造默认实现。
func New() Executor { return &defaultExecutor{} }

// ErrMaxTurns is returned when the loop hits MaxTurns without a natural
// stop. Lives here (not in the agent root) to avoid the cycle above; the
// agent dispatcher forwards it up via errors.Is.
var ErrMaxTurns = errors.New("executor: max turns exceeded")
```

**与上面伪代码的有意偏离**：spec 草拟的 `RunConversation(ctx, *agent.Agent)` 会让 executor 反向 import agent 根包，触发 `agent → executor → agent` 的循环依赖。实际落地用一个 `Deps` 接口（agent 隐式满足）化解 —— 等价语义、零运行时代价、保留了 fake `Executor` 注入的测试接口（§7.1 列出）。

测试时可注入 fake `Executor` 替换默认实现（不需要 fake 整个 LLM 链路）。

`executor.RunConversation(ctx, a *agent.Agent) error` 默认实现伪代码：

```go
turnIndex := 0
var totalUsage llm.Usage
for turnIndex < a.cfg.MaxTurns {
    turnIndex++
    turnID := newTurnID()
    a.emit(TurnStartEvent{TurnID: turnID, TurnIndex: turnIndex})

    // 1. 组装 messages（占位：直接用 session 全部消息；后续 spec 引入 ContextEngine）
    messages := a.session.Messages()

    // 2. 调 LLM（流式）
    a.emit(LLMStartEvent{Model: a.model.Model})
    req := &llm.CompletionRequest{
        Model:       a.model.Model,
        Messages:    messages,
        Tools:       a.tools.Specs(),
        ToolChoice:  llm.ToolChoice{Type: "auto"},
        System:      a.instructions,
        Stream:      true,
        MaxTokens:   4096,
    }
    stream, err := a.provider.Stream(ctx, req)
    if err != nil { return err }

    // 3. 累积 assistant 消息
    var text strings.Builder
    var toolCalls []llm.ToolCall
    pending := map[string]*partialToolCall{}  // ID -> partial

    for ev := range stream.Events() {
        switch e := ev.(type) {
        case llm.TextDeltaEvent:
            text.WriteString(e.Delta)
            a.emit(TextDeltaEvent{Delta: e.Delta})
        case llm.ToolCallStartEvent:
            pending[e.ID] = &partialToolCall{ID: e.ID, Name: e.Name}
        case llm.ToolCallDeltaEvent:
            p := pending[e.ID]; p.argBuf.WriteString(e.Delta)
        case llm.ToolCallEndEvent:
            p := pending[e.ID]
            p.Arguments = e.Arguments  // provider 已解析好
            delete(pending, e.ID)
            toolCalls = append(toolCalls, llm.ToolCall{ID: p.ID, Name: p.Name, Arguments: p.Arguments})
        case llm.DoneEvent:
            // 累积 usage
            totalUsage.add(e.Response.Usage)
        case llm.ErrorEvent:
            return stream.Err()
        }
    }
    a.emit(LLMEndEvent{Assistant: assistant, Usage: usage})

    // 4. 写 assistant 消息到 session
    assistantMsg := llm.Message{
        Role:      llm.RoleAssistant,
        Content:   text.String(),
        ToolCalls: toolCalls,
    }
    a.session.Append(assistantMsg)

    // 5. 决策下一步
    switch {
    case len(toolCalls) == 0:
        a.emit(TurnEndEvent{TurnIndex: turnIndex, StopReason: llm.FinishReasonStop})
        return nil  // 自然结束
    case errors.Is(stream.Err(), context.Canceled):
        a.emit(TurnEndEvent{TurnIndex: turnIndex, StopReason: FinishReasonAborted})
        return ErrAborted
    }

    // 6. 并行执行所有 tool call
    a.emit(ToolStartEvent{...} for each)
    results := a.runToolsParallel(ctx, toolCalls)
    a.emit(ToolEndEvent{...} for each)

    // 7. 写 tool result 消息（按原顺序）
    for i, tc := range toolCalls {
        a.session.Append(llm.Message{
            Role:       llm.RoleTool,
            Content:    results[i].Content,
            ToolCallID: tc.ID,
        })
    }
    a.emit(TurnEndEvent{TurnIndex: turnIndex, StopReason: llm.FinishReasonToolCalls})
    // 进入下一 turn
}
return fmt.Errorf("agent: max turns %d exceeded", a.cfg.MaxTurns)
```

**FinishReason 扩展**：`llm.FinishReason` 已有 `stop/length/tool_calls/content_filter/error`，本 spec 新增 `FinishReasonAborted = "aborted"`（加在 `internal/agent/llm/types.go`）。在 `llm` 包下扩展常量。

**并行执行**（`runToolsParallel`）：

```go
func (a *Agent) runToolsParallel(ctx context.Context, calls []llm.ToolCall) []tool.Result {
    results := make([]tool.Result, len(calls))
    var wg sync.WaitGroup
    for i, c := range calls {
        wg.Add(1)
        go func(i int, c llm.ToolCall) {
            defer wg.Done()
            tctx, cancel := context.WithTimeout(ctx, a.cfg.ToolTimeout)
            defer cancel()
            start := time.Now()
            results[i] = a.executeOneTool(tctx, c)
            a.emit(ToolEndEvent{
                CallID: c.ID, Result: results[i], DurationMS: time.Since(start).Milliseconds(),
            })
        }(i, c)
    }
    wg.Wait()
    return results
}

func (a *Agent) executeOneTool(ctx context.Context, c llm.ToolCall) tool.Result {
    t, ok := a.tools.Get(c.Name)
    if !ok { return tool.Result{IsError: true, Content: "tool not found: " + c.Name} }
    if err := validateArgs(c.Arguments, t.Parameters()); err != nil {
        return tool.Result{IsError: true, Content: "invalid arguments: " + err.Error()}
    }
    return t.Execute(ctx, c.Arguments)
}
```

`ToolStartEvent` 在 goroutine 启动时 emit；`ToolEndEvent` 在 goroutine 完成时 emit（事件顺序不保证与调用顺序一致——并发工具的并行性是 by design）。

### 4.5 Session 与 Store（`session/` / `store/` 子包）

```go
// session.go
type Session struct {
    ID        string
    CreatedAt time.Time
    UpdatedAt time.Time
    messages  []llm.Message
    mu        sync.RWMutex
}

func NewSession(id string) *Session
func (s *Session) Messages() []llm.Message                  // 拷贝
func (s *Session) Append(m llm.Message)
func (s *Session) ReplaceAll(messages []llm.Message)         // 给 store 反序列化用

type SessionMeta struct {
    ID        string
    CreatedAt time.Time
    UpdatedAt time.Time
    MessageCount int
}

// store.go
type SessionStore interface {
    Save(ctx context.Context, s *Session) error
    Load(ctx context.Context, id string) (*Session, error)
    List(ctx context.Context) ([]SessionMeta, error)
    Delete(ctx context.Context, id string) error
}

type MemoryStore struct {
    mu       sync.RWMutex
    sessions map[string]*Session
}

func NewMemoryStore() *MemoryStore
// Save / Load / List / Delete 实现

// TODO: SQLiteStore
// type SQLiteStore struct { db *gorm.DB }
// 实现 SessionStore 时把 messages 序列化到 messages 表（gorm 已有）
```

`MemoryStore` 用于本里程碑；`Agent` 启动时把 `MemoryStore` 持有的所有 session 加载到内存（或按需 load）。

### 4.6 工具实现（`tool/fs.go`, `tool/shell.go`）

**fsSandbox**（`tool/sandbox.go`）：

```go
type fsSandbox struct {
    root string  // 绝对路径
}

func newFsSandbox(workdir string) (*fsSandbox, error) {
    root, err := filepath.Abs(workdir)
    if err != nil { return nil, err }
    return &fsSandbox{root: root}, nil
}

// resolve 把任意 path 解析为绝对路径并验证在 root 内部。
// 路径不存在不算错（让 read/write 各自报 ENOENT），但必须 resolve 后仍在 root 内。
func (s *fsSandbox) resolve(p string) (string, error) {
    abs, err := filepath.Abs(p)
    if err != nil { return "", err }
    rel, err := filepath.Rel(s.root, abs)
    if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
        return "", fmt.Errorf("path escapes sandbox: %s", p)
    }
    return abs, nil
}
```

**read_file**：

```go
type readFileTool struct{ sb *fsSandbox }

func (t *readFileTool) Execute(ctx context.Context, args map[string]any) tool.Result {
    path, _ := args["path"].(string)
    abs, err := t.sb.resolve(path)
    if err != nil { return tool.Result{IsError: true, Content: err.Error()} }
    f, err := os.Open(abs)
    if err != nil { return tool.Result{IsError: true, Content: err.Error()} }
    defer f.Close()
    // limit / offset 处理
    // 1 MiB 上限
    // 整体实现省略；超 1 MiB 时返回前 1 MiB + "\n[truncated, total N bytes]"
}
```

**write_file** / **edit_file** / **list_dir** 结构同上；`edit_file` 走「读全文 → 找 old_text → 替换 → 写回」。

**shell**（`tool/shell.go`）：

```go
type shellTool struct {
    sb        *fsSandbox
    allowlist map[string]struct{}
    timeout   time.Duration
}

func (t *shellTool) Execute(ctx context.Context, args map[string]any) tool.Result {
    cmd, _ := args["command"].(string)
    if _, ok := t.allowlist[cmd]; !ok {
        return tool.Result{IsError: true, Content: "command not allowed: " + cmd}
    }
    argv, _ := args["args"].([]any)  // []string
    cwdArg, _ := args["cwd"].(string)
    cwd := t.sb.root
    if cwdArg != "" {
        resolved, err := t.sb.resolve(cwdArg)
        if err != nil { return tool.Result{IsError: true, Content: err.Error()} }
        cwd = resolved
    }
    timeout := t.timeout
    if v, ok := args["timeout_ms"].(float64); ok {
        timeout = time.Duration(v) * time.Millisecond
    }
    if timeout > 5*time.Minute { timeout = 5*time.Minute }

    cctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    c := exec.CommandContext(cctx, cmd, toStrSlice(argv)...)
    c.Dir = cwd
    var stdout, stderr bytes.Buffer
    c.Stdout = &stdout
    c.Stderr = &stderr
    err := c.Run()
    out := stdout.String()
    if stderr.Len() > 0 { out += "\n[stderr]\n" + stderr.String() }
    if err != nil {
        return tool.Result{IsError: true, Content: out + "\n[exit] " + err.Error()}
    }
    return tool.Result{Content: out}
}
```

### 4.7 事件订阅（`event/` 子包）

```go
type Event interface { isAgentEvent() }

// 14 个具体类型如 §FR-7 所列；用 sum type pattern + unexported marker

type subscriber struct {
    ch chan Event
}

type Subscription interface {
    Unsubscribe()
    C() <-chan Event
}

type subscription struct {
    agent *Agent
    sub   *subscriber
    once  sync.Once
}

func (s *subscription) Unsubscribe() {
    s.once.Do(func() {
        s.agent.removeSubscriber(s.sub)
    })
}
func (s *subscription) C() <-chan Event { return s.sub.ch }

func (a *Agent) Subscribe(buffer int) Subscription {
    if buffer <= 0 { buffer = 64 }
    sub := &subscriber{ch: make(chan Event, buffer)}
    a.subsMu.Lock()
    a.subscribers = append(a.subscribers, sub)
    a.subsMu.Unlock()
    return &subscription{agent: a, sub: sub}
}

// emit fan-out：每个 subscriber 各自一个 non-blocking send
func (a *Agent) emit(ev Event) {
    a.subsMu.RLock()
    defer a.subsMu.RUnlock()
    for _, s := range a.subscribers {
        select {
        case s.ch <- ev:
        default:
            a.logger.Warn("agent: subscriber channel full, dropping event",
                zap.String("event", eventName(ev)))
        }
    }
}
```

### 4.8 队列（`queue/` 子包）

```go
type queuedMessage struct {
    content string
    enqueuedAt time.Time
}

// Agent 内部持三 channel：
//   promptCh   chan queuedMessage  // buffer 1
//   steerCh    chan queuedMessage  // buffer 1
//   followupCh chan queuedMessage  // buffer 16
//
// dequeue 优先级：steer > prompt > followup；用 select-default 实现非阻塞 peek。
```

`Prompt` / `Steer` / `FollowUp` 都是 `select { case ch <- msg: ...; default: return ErrAgentBusy }` 模式，其中 prompt 在 idle 时必成功，busy 时返回 `ErrAgentBusy` 让调用方改用 `Steer`。

### 4.9 主入口改造（cmd/app/main.go）

```go
// 伪代码
provider, err := llm.NewProvider(ctx, cfg.LLM.Provider, llm.ProviderConfig{
    APIKey:  cfg.LLM.APIKey,
    BaseURL: cfg.LLM.BaseURL,
})
model := agent.ModelRef{Provider: cfg.LLM.Provider, Model: "claude-sonnet-4-20250514"}

sess := agent.NewSession("dev-session-1")
a, err := agent.New(agent.NewAgentConfig{
    Name:         cfg.App.Name,
    Instructions: "你是一个 helpful coding assistant。",
    Model:        model,
    Provider:     provider,
    Session:      sess,
    Workdir:      cfg.Agent.Workdir,  // 空 = os.Getwd()
    MaxTurns:     cfg.Agent.MaxTurns,
    ToolTimeout:  time.Duration(cfg.Agent.ToolTimeoutMS) * time.Millisecond,
    Logger:       log,
})
// NewAgentConfig 内部自动注册 5 个内置工具

sub := a.Subscribe(128)
go func() {
    for ev := range sub.C() {
        log.Info("agent event", zap.String("event", eventName(ev)), zap.Any("payload", ev))
    }
}()

if err := a.Prompt(context.Background(), "你好"); err != nil { log.Fatal(err) }
if err := a.Run(context.Background()); err != nil && !errors.Is(err, agent.ErrAborted) {
    log.Fatal(err)
}
```

### 4.10 配置扩展（internal/config/config.go）

```go
type Config struct {
    // ... 已有字段
    Agent AgentConfig `mapstructure:"agent"`
}

type AgentConfig struct {
    MaxTurns       int      `mapstructure:"max_turns"`
    ToolTimeoutMS  int      `mapstructure:"tool_timeout_ms"`
    Workdir        string   `mapstructure:"workdir"`
    ShellAllowlist []string `mapstructure:"shell_allowlist"`
    EventBuffer    int      `mapstructure:"event_buffer"`
    ProviderName   string   `mapstructure:"provider_name"`
    Model          string   `mapstructure:"model"`
    Instructions   string   `mapstructure:"instructions"`
}
```

`config.yaml` 默认段（追加在现有配置后）：

```yaml
agent:
  max_turns: 25
  tool_timeout_ms: 30000
  workdir: ""
  shell_allowlist: []
  event_buffer: 64
  provider_name: anthropic
  model: claude-sonnet-4-5
  instructions: ""
```

**有意偏离**：第一稿 spec 写的是 `shell_allowlist_path: ""`（指向外部文件路径，由 `agent.New` 在启动时 `os.ReadFile` 加载）。实现期评估发现：v0 没有外部 allowlist 文件交付物，路径加载反而引入"配置文件 vs 文件路径文件"的双层结构、错误处理路径更复杂、以及测试时构造 fixtures 的额外负担。直接以 `[]string` 注入是更简洁的形态，且与"硬编码 `var defaultShellAllowlist` 兜底"的 fallback 语义一致 —— 留 nil/空切片就用 `DefaultShellAllowlist()`。后续如果出现外部维护的多团队共享 allowlist 需求，再单独 spec 走 file/source resolver，不破现有接口。

`ProviderName` / `Model` / `Instructions` 三个字段是 §4.9 main.go 装配时需要的，方便把运行时参数全部从 yaml 注入；不在 §4.2 `agent.Config`（`NewAgentConfig.Config`）里直接出现，是因为前者是 yaml 反序列化目标、后者是 Go API 入参。

### 4.11 错误类型（`agent/errors.go`，根包 + 各子包按需）

```go
// internal/agent/errors.go
var (
    ErrAgentBusy       = errors.New("agent: busy, use Steer or FollowUp")
    ErrAborted         = errors.New("agent: aborted")
    ErrToolNotFound    = errors.New("agent: tool not found")
    ErrInvalidArguments = errors.New("agent: invalid arguments")
)

// internal/agent/executor/executor.go
// (居于此处以避免 agent ↔ executor import 循环)
var ErrMaxTurns = errors.New("executor: max turns exceeded")
```

**有意偏离**：`ErrMaxTurns` 原计划放在 `agent` 根包，但实际放在 `executor` 子包 —— executor 不反向 import agent 根包，二者通过 `Deps` 接口解耦；dispatcher 用 `errors.Is(err, executor.ErrMaxTurns)` 上抛。代码中 `ErrToolNotFound` / `ErrInvalidArguments` 也暂未在 agent 根包引用（工具层走 `tool.Result{IsError: true}`），保留定义供后续 spec 复用。

### 4.12 最小 JSON Schema 校验（`tool/params.go`）

不引第三方库。自写 `validateArgs(args map[string]any, schema llm.ParameterSchema) error`：

- 检查 `schema.Type == "object"`（其他类型报错）。
- 对每个 `Required` 字段：args 必须存在且非 nil。
- 对每个 `Properties` 字段：若 args 存在则检查 `Property.Type ∈ {"string","number","boolean","array","object","integer"}` 的最小子集（用 type switch 即可）。
- 不做 enum/default/format 校验——留给后续 spec。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|----------|
| 工具 panic | executor 用 `defer recover()` 包住 `executeOneTool`，panic 转 `Result{IsError: true, Content: "tool panic: ..."}`，不挂掉 Agent |
| LLM 流中断（HTTP socket 关）| `provider.Stream` 返回 `ErrorEvent` + `Err()` 非 nil；`runConversation` 返回 error；Agent 写 `AgentErrorEvent` 并退出当前 Run |
| 工具返回超长 content | 单条 `tool` 消息 > 100 KiB 时截断到 100 KiB + `[truncated N bytes]` 标记，避免撑爆 context window |
| 用户连续 Steer 多次 | 只有最新 steer 生效（steerCh buffer=1，写入即覆盖） |
| Session 未初始化（nil）| `NewAgentConfig.Session == nil` 时 `New` 返回 error |
| `workdir` 不存在 | `newFsSandbox` 返回 error，agent 启动失败 |
| shell 工具返回非零退出码 | `Result{IsError: true, Content: stdout + "\n[stderr]\n" + stderr + "\n[exit] " + err}`；非零退出 ≠ 工具 panic，让 LLM 自行决定下一步 |
| 并行工具中一个 panic | `runToolsParallel` 各自 goroutine 内 recover，互不影响；panic 工具返回 `IsError=true` |
| `MaxTurns` 触发 | 返回 `ErrMaxTurns`；不 emit `AgentErrorEvent`（视为正常终止路径），emit `TurnEndEvent` + `AgentEndEvent` |
| Subscriber channel 满 | drop oldest，zap warn 记日志（不阻塞 Agent） |
| 用户 Abort 但当前 turn 已经走到 LLM Stream 收尾 | 不强求 abort 一定把消息写回；若已 emit `DoneEvent` 则保留该 turn 的 assistant 消息，abort 视为"已结束本轮"，不返回 `ErrAborted` |

---

## 6. 涉及文件

### 6.1 新增（按包分组）

**根包 `package agent`**（`src/darvin-agent/internal/agent/`）

| 文件 | 内容 |
|------|------|
| `agent.go` | `Agent` struct + `NewAgentConfig` + `New` 构造器 |
| `dispatcher.go` | `Run` / `Prompt` / `Steer` / `FollowUp` / `Abort` + dequeue 消费 |
| `errors.go` | 根包级错误（`ErrAgentBusy` / `ErrAborted`） |
| `agent_test.go` | `New` / `NewAgentConfig` 默认值 + 工具自动注册断言 |
| `dispatcher_test.go` | 三种队列模式 + abort 时序 |

**`tool` 子包**（`src/darvin-agent/internal/agent/tool/`）

| 文件 | 内容 |
|------|------|
| `tool.go` | `Tool` interface + `Result` struct |
| `registry.go` | `Registry` 注册表 + `Specs()` 转 LLM 可见的 `llm.Tool` |
| `params.go` | `validateArgs` 最小 JSON Schema 校验 |
| `sandbox.go` | `fsSandbox` 路径 resolve + 越界检测 |
| `fs.go` | `read_file` / `write_file` / `edit_file` / `list_dir` |
| `shell.go` | `shell` + `defaultShellAllowlist` + `exec.CommandContext` 超时 |
| `*_test.go` | 各自单测（sandbox 逃逸 / 白名单 / 超时 / 越界） |

**`executor` 子包**（`src/darvin-agent/internal/agent/executor/`）

| 文件 | 内容 |
|------|------|
| `executor.go` | `RunConversation` + `runToolsParallel` + `executeOneTool` + ctx 透传 + recover |
| `executor_test.go` | fake `ModelProvider` 驱动：单 turn stop / 多 turn tool use / MaxTurns / ctx cancel |

**`session` 子包**（`src/darvin-agent/internal/agent/session/`）

| 文件 | 内容 |
|------|------|
| `session.go` | `Session` + `SessionMeta`（深拷贝语义） |
| `session_test.go` | Append / Messages / 并发安全 |

**`store` 子包**（`src/darvin-agent/internal/agent/store/`）

| 文件 | 内容 |
|------|------|
| `store.go` | `SessionStore` 接口 |
| `memory.go` | `MemoryStore`（map + RWMutex）；`SQLiteStore` 留 `// TODO(spec: agent-loop)` |
| `memory_test.go` | 增删查 + 并发 |

**`queue` 子包**（`src/darvin-agent/internal/agent/queue/`）

| 文件 | 内容 |
|------|------|
| `queue.go` | `Queue` + `Mode`（`prompt`/`steer`/`followup`）+ 三 channel + 优先级 dequeue |
| `queue_test.go` | 优先级序、busy 拒绝、steer 覆盖 |

**`event` 子包**（`src/darvin-agent/internal/agent/event/`）

| 文件 | 内容 |
|------|------|
| `event.go` | `Event` 接口 + 14 个具体事件类型 + `Subscription` + `Subscriber` |
| `event_test.go` | emit fan-out / drop oldest / Unsubscribe 幂等 |

### 6.2 修改（既有文件）

| 文件 | 变更说明 |
|------|----------|
| `src/darvin-agent/internal/agent/llm/types.go` | **小改** 增加 `FinishReasonAborted = "aborted"` 常量 |
| `src/darvin-agent/internal/config/config.go` | **小改** 增加 `AgentConfig` 字段（`mapstructure` 标签见 §4.10） |
| `src/darvin-agent/config.yaml` | **小改** 追加 `agent:` 段 |
| `src/darvin-agent/cmd/app/main.go` | **改造** 构造 Agent + 注册 5 工具 + 订阅日志 + 跑一个示例 prompt；保留原有 init 流程（config / logger / database） |
| `specs/features/agent-loop/2026-07-28-agent-loop-design.md` | **本文件** |

### 6.3 不改

| 文件 | 理由 |
|------|------|
| `docs/plan/开发计划.md` | M2 模块拆分（runtime/agent/session/executor/tool_registry）与本 spec 对齐，文件级映射留作实现期注释；如下游有出入另起 PR |
| `src/darvin-agent/go.mod` / `go.sum` | 不引入新依赖（`params.go` 自写校验，`shell.go` 用 stdlib `os/exec`） |
| `src/darvin-agent/internal/database/sqlite.go` | 本 spec 不实现 `SQLiteStore`，仅占 TODO |
| `src/darvin-agent/internal/logger/logger.go` / `config/config.go`（根配置） | 已有全局单例，新 Agent 直接复用 |
| `src/main/runtime/{manager,client}.ts` | Electron 主进程桥接层；IPC 协议后续 spec |

---

## 7. 验收标准

### 7.1 单元 / 集成测试

- `internal/agent/session/session_test.go`：`Append` / `Messages` 深拷贝语义。
- `internal/agent/store/memory_test.go`：`MemoryStore` 增删查 + 并发。
- `internal/agent/tool/params_test.go`：JSON Schema 校验的 pass / fail 路径。
- `internal/agent/tool/sandbox_test.go`：sandbox 路径 resolve 越界检测。
- `internal/agent/tool/fs_test.go`：read/write/edit/list 正常路径 + sandbox 逃逸（`../etc/passwd`、`/etc/hosts`）必须返回 `IsError=true`。
- `internal/agent/tool/shell_test.go`：白名单（允许 echo、拒绝 rm）、超时、cwd 逃逸。
- `internal/agent/queue/queue_test.go`：优先级序（steer > prompt > followup）、busy 拒绝、steer 覆盖语义。
- `internal/agent/event/event_test.go`：emit fan-out / drop oldest / `Unsubscribe` 幂等。
- `internal/agent/executor/executor_test.go`：用一个**假** `ModelProvider`（返回预设的 `StreamEvent` 序列）验证：
  - 单 turn stop → emit 完整事件序列且 `session.Session` 长度 = 2（user + assistant）
  - 多 turn tool use → tool 消息正确 append、最终 stop
  - MaxTurns 触发 → 返回 `ErrMaxTurns`
  - ctx cancel during LLM stream → 返回 `ErrAborted`、Session 里有 stopReason=aborted 的 assistant 消息
- `internal/agent/dispatcher_test.go`：`Prompt` 在 busy 时返回 `ErrAgentBusy`；`Steer` 中断当前并重启；`FollowUp` 在 idle 立即触发 / 在 running 排队；`Abort` 取消正在跑的 turn。

### 7.2 `npm run check` & Go test

- 仓库根：`npm run lint` 仍绿（本次不改 TS）。
- `cd src/darvin-agent && go test ./...` 全绿，新测试覆盖 7.1 列出的所有路径。
- `cd src/darvin-agent && go vet ./...` 无警告。
- `go build` 在 `CGO_ENABLED=0` 下成功（与现有 `scripts/build-go.js` 一致）。

### 7.3 手动验证（dev 模式）

- `cd src/darvin-agent && ANTHROPIC_API_KEY=<key> go run ./cmd/app` 跑通示例 prompt（"读 `main.go` 然后一句话总结"），zap logger 看到 `LLMStartEvent` / `TextDeltaEvent` / `ToolStartEvent` / `ToolEndEvent` / `RunEndEvent{Turns}` / `AgentEndEvent` 完整序列。
- 故意构造越权路径（`read_file {path: "/etc/passwd"}`）→ 工具返回 `IsError=true`，Agent 不 panic，LLM 收到错误信息后能继续对话。
- `Ctrl-C` 中断主进程 → Agent 干净退出，zap logger 看到 `agent: aborted` 信息。

> **注意**：当前迭代 `cmd/app/main.go` 仅含原 config / logger / database 初始化，未接入 agent（见 §9）。手动验证脚本与 agent wiring 一并推迟到 bootstrap / IPC spec。

### 7.4 文档 / 一致性

- `internal/agent/agent.go` 顶部 package 注释 + 核心导出符号 godoc 齐全。
- `internal/agent/tool/shell.go` 的 `defaultShellAllowlist` 与 §FR-6 表格一一对应。
- 本 spec 中所有 `// TODO`（仅限 `SQLiteStore`）在实现后用 `// implemented` 标注或删除。
- 任何 spec 中描述但未实现的接口（如 `SessionStore.SQLiteStore`）在对应文件留 `// TODO(spec: agent-loop): implement` 注释。
- 不引入新第三方 Go 依赖（go.mod / go.sum 与现状一致）。

### 7.5 不在验收范围

- IPC 协议、Electron 主进程集成、preload 暴露、UI 渲染：留待后续 spec。
- 上下文压缩、token 预算、记忆系统、Skills、MCP、子 Agent：明确不在本 spec 范围（见 §1.3）。
- 跨模型消息转换、思考签名保留：不在本 spec 范围。

---

## 8. 落地后追加 — `RunStartEvent` / `RunEndEvent` 事件契约

代码实现期在 `dispatcher.go` 落地的事件发射节奏，严格遵循以下约定：

1. 进入 `Run`：先 emit `RunStartEvent{SessionID: a.session.ID}`，再进入 dequeue 循环。
2. 每消费完一条入队消息并跑完 `RunConversation` 后，emit `RunEndEvent{Turns: <本次新增的 assistant 消息数>}`；turn 数通过对比 `session.Len()` 前/后差值粗估。
3. `Run` `defer` 中 emit `AgentEndEvent{SessionID, TotalTurns, TotalUsage}`。
4. 异常路径（`ErrAborted` / `ErrMaxTurns` / 其他 error）依然走 `AgentErrorEvent` + 终止 `Run`，`RunEndEvent` 仍然在错误发生之前的最后一次成功 `RunConversation` 后 emit。

事件顺序全程可见、可被订阅方重放。详细见 `internal/agent/dispatcher.go`。

---

## 9. 实现偏差与说明（Implementation Notes）

落地时对前述章节做了若干有意偏离，逐条记录，便于后续 spec 引用与回溯。

### 9.1 `Deps` 接口化解 agent ↔ executor 循环依赖

§4.4 / §4.2 中 `executor.RunConversation(ctx, *agent.Agent)` 直接拿 `*agent.Agent` 的签名会让 executor 反向 import agent 根包，触发 `agent → executor → agent` 的循环导入。实际落地引入 `executor.Deps` 接口（位于 `internal/agent/executor/executor.go`），agent 根包**隐式满足**该接口（满足的 7 个方法：`Session()` / `Tools()` / `Provider()` / `ModelName()` / `Instructions()` / `Emit(event.Event)` / `Config()`）。等价语义、零运行时代价，并保留了 fake `Executor` 注入的测试便利（`dispatcher_test.go` 当前不依赖此接缝，未来加深并发测试可启用）。

### 9.2 `ShellAllowlist []string` 而非 `ShellAllowlistPath`

§FR-10 / §4.10 原稿写的是 `shell_allowlist_path: ""`（外部 yaml 路径，由 `agent.New` 启动时 `os.ReadFile` 加载）。实现期评估：

- v0 没有外部 allowlist 文件交付物，引入"配置文件 + 文件路径"双层结构反而复杂化错误处理与 fixture 构造。
- 直接注入 `[]string` 与"硬编码 `defaultShellAllowlist` 兜底"语义一致 —— 留 nil/空切片就用 `DefaultShellAllowlist()`。
- 后续如出现多团队共享 allowlist 需求，单独 spec 走 file/source resolver，不破坏现有接口。

故最终 `Config.AgentConfig.ShellAllowlist []string` + `tool.DefaultShellAllowlist()`。

### 9.3 `cmd/app/main.go` 不接入 agent

§4.9 设计了 `cmd/app/main.go` 改造：`agent.New(...)` + `Subscribe(128)` + 跑示例 prompt。**实际实现期未在 `cmd/app/main.go` 接线**——用户希望 main.go 保持空（仅 config / logger / database init），agent 启动由后续 **bootstrap / IPC** spec 接手。所有手动验证（§7.3）也随之推迟。

### 9.4 `ErrMaxTurns` 改放 `executor` 包

§4.11 把 `ErrMaxTurns` 列在 `internal/agent/errors.go`。实际放在 `internal/agent/executor/executor.go` 顶层 —— 原因同 §9.1：避免 agent ↔ executor import 循环。dispatcher 用 `errors.Is(err, executor.ErrMaxTurns)` 上抛，调用方语义不变。`ErrToolNotFound` / `ErrInvalidArguments` 在根包 `errors.go` 保留定义供后续 spec 复用（当前未在 agent 根包引用，工具层走 `tool.Result{IsError: true}`）。

### 9.5 事件发射补全

§3 FR-7 列出 14 类事件，§4.2 伪代码只覆盖 8 类。落地后补齐：

- `RunStartEvent{SessionID}` — `Run` 入口
- `RunEndEvent{Turns}` — 每条入队消息消费完成
- `AgentEndEvent{SessionID, TotalTurns, TotalUsage}` — `Run` defer
- `AgentErrorEvent{Err}` — error 路径

§8 给出完整顺序。

### 9.6 `cmd/llm-smoke/` 与 `internal/agent/e2e/` 被删除

- `cmd/llm-smoke/main.go`（M1 烟测入口）已被用户删除（清理历史调试入口）。后续如需独立烟测入口，单独起 spec。
- `internal/agent/e2e/`（含 `agent_e2e_test.go` 单元式 e2e、`agent_real_e2e_test.go` 集成 build-tag e2e）已被用户删除。本 spec §7.1 列出的所有测试路径当前**只**通过单元测试 + dispatcher / executor 单测覆盖，**真 LLM 集成测试缺位** —— 后续如需真实 provider 烟测，另起 spec 设计（推荐两条路径：(a) 保留 `//go:build integration` 真 LLM 测试 + shell `set -a && source .env && set +a`；(b) 起一个 docker-compose mock provider）。

### 9.7 `agent.Config` 字段分两套

§4.10 `internal/config/config.go.AgentConfig` 是 **yaml 反序列化目标**（含 `ProviderName` / `Model` / `Instructions` 等装配期由 main.go 注入的字段）。`agent.NewAgentConfig.Config` 是 **Go API 入参**（用户在 main.go 手工构造 `agent.Config` 喂给 `New`）。两者字段子集重合但非等价；不要合并。
