# Agent 包当前状态

> 范围：`src/darvin-agent/`。本包作为 Electron 主进程 spawn 的子进程二进制运行。
> 配套文档：`agent-package-roadmap.md`（前瞻路线图）。

---

## 一、已完成（2026-07 截止）

### ContextEngine 规范 7 个里程碑全链路打通

| Milestone | 内容 | 关键文件 |
|-----------|------|---------|
| **M1** pkg 骨架 + 接口契约 | `ContextEngine` 10 方法接口 + `*DefaultAssembler` compile-time assertion + 全 stub | `ctxengine/ctxengine.go`, `params.go` |
| **M2** token 估算 + tool 截断 | `EstimateCharsOver4`（rune/4） + Assemble step 1 超大 tool 输出截断 | `ctxengine/tokens.go`, `assemble.go` |
| **M3** DefaultSummarizer + Compact LLM | 7 步管线：snapshot → no-op → tail/span 切分 → 摘要 → retry-half → 失败安全 | `ctxengine/compact.go` |
| **M4** executor seam + Deps 扩展 + API token 适配 | `Usage.PromptTokens` 通过 `LastUsage` 喂回 assembler/compact 优先于估算 | `executor/executor.go`, `agent/agent.go`, `ctxengine/params.go` |
| **M5** Agent 集成 + NewAgentConfig | Config 增 7 ctxengine 字段；`New` 自动构造 DefaultAssembler | `agent/agent.go` |
| **M6** Ingest/AfterTurn/Bootstrap/Maintain/Dispose + Projection | 4 个 stub no-op + ProjectionCreate/Get/List/Delete 内存注册 | `ctxengine/{ingest,after_turn,lifecycle,projection}.go` |
| **M7** 配置 + cmd 文档同步 | `config.AgentConfig` 增 7 ctxengine 字段 + `config.yaml` 默认值 + `cmd/app/main.go` 装配 agent | `internal/config/config.go`, `config.yaml`, `cmd/app/main.go` |

### 其他已完成模块

| 模块 | 内容 | 关键文件 |
|------|------|---------|
| **LLM 抽象** | `ModelProvider` 接口 + `NewProvider` 工厂 + ProviderFactory 注册表 + Anthropic 完整实现（HTTP client + 重试 + 指数退避） | `llm/{provider,registry,events,types,errors,httpclient}.go`, `llm/anthropic/` |
| **流式事件协议** | 7 类 `StreamEvent`：Start / TextDelta / ToolCallStart·Delta·End / Done / Error | `llm/events.go` |
| **Executor 单 turn 编排** | `RunConversation` 循环 + `drainStream` 捕获 Usage + 工具并行执行 + panic 隔离 | `executor/executor.go` |
| **Agent 主入口** | `Run/Prompt/Steer/FollowUp/Abort/Subscribe` 公共 API + 状态机 + 总 usage 累加 | `agent/agent.go`, `dispatcher.go` |
| **3 通道优先级队列** | steer > prompt > followup，非阻塞 Enqueue，ctx-cancel Dequeue | `queue/queue.go` |
| **Event Bus** | 14 种 `Event` 类型，订阅-发布，channel 满丢 oldest | `event/event.go` |
| **Session** | 内存消息历史 + 深拷贝 + Append/ReplaceAll/Meta | `session/session.go` |
| **SessionStore (内存)** | `MemoryStore` 占位 + `SQLiteStore` TODO | `store/{store,memory}.go` |
| **5 个内置工具** | read_file / write_file / edit_file / list_dir / shell + 工作目录沙箱 + shell 命令 allowlist | `tool/{tool,registry,builtins,fs,shell,sandbox,params}.go` |
| **配置** | viper 加载 + `Config{App, Database, Log, LLM, Agent}` + AgentConfig 13 字段 | `internal/config/config.go` |
| **测试** | 12 个包，race 干净，gofmt 干净 | 各包 `_test.go` |

---

## 二、实体关系

### 包结构依赖图

```mermaid
graph TB
    subgraph cmd[cmd 层]
        main[cmd/app/main.go]
    end

    subgraph config[配置层]
        cfg[internal/config]
    end

    subgraph agent_pkg[agent/ 根包]
        Agent[Agent]
        Dispatcher[dispatcher.go<br/>Prompt/Steer/Run]
    end

    subgraph ctxengine[ctxengine/]
        CE[ContextEngine 接口]
        DA[DefaultAssembler]
        Sum[DefaultSummarizer]
    end

    subgraph executor[executor/]
        Ex[Executor]
        Deps1[Deps 接口]
    end

    subgraph infra[支撑包]
        Session[session.Session]
        Store[store.SessionStore]
        Queue[queue.Queue]
        Event[event.Bus]
        Tool[tool.Registry]
    end

    subgraph llm_pkg[llm/]
        Provider[ModelProvider]
        Anthropic[anthropic.Provider]
    end

    main --> cfg
    main --> Agent
    main --> Provider

    Agent --> Session
    Agent --> Store
    Agent --> Queue
    Agent --> Event
    Agent --> Tool
    Agent --> Ex
    Agent --> DA
    Agent --> Deps1

    DA -.implements.-> CE
    DA --> Sum
    Sum --> Provider
    Ex --> Deps1
    Ex --> Provider

    Anthropic -.registers.-> Provider
```

### 核心实体 class 图

```mermaid
classDiagram
    class Agent {
        +session: *Session
        +store: SessionStore
        +queue: *Queue
        +bus: *Bus
        +tools: *Registry
        +provider: ModelProvider
        +exec: Executor
        +assembler: ContextEngine
        +lastUsage: llm.Usage
        +New(cfg NewAgentConfig) *Agent
        +Prompt(ctx, content) error
        +Steer(ctx, content) error
        +FollowUp(ctx, content) error
        +Abort(ctx) error
        +Run(ctx) error
        +Subscribe(buf) *Subscription
    }

    class ContextEngine {
        <<interface>>
        +Info() Info
        +Bootstrap(ctx, p) error
        +Maintain(ctx, p) error
        +Dispose(ctx) error
        +Ingest(ctx, p) IngestResult
        +IngestBatch(ctx, p) IngestResult
        +AfterTurn(ctx, p) error
        +Assemble(ctx, p) AssembleResult
        +Compact(ctx, p) CompactResult
        +PrepareSubagentSpawn(ctx, p) *SubagentSpawnPreparation, error
        +OnSubagentEnded(ctx, p) error
    }

    class DefaultAssembler {
        -mu: sync.RWMutex
        -cfg: Config
        -deps: Deps
        -estimator: TokenEstimator
        -summarizer: Summarizer
        -sections: []SystemSection
        -lastIngestAt: map[string]time.Time
        -projectionsMu: sync.RWMutex
        -projections: map[string]ContextProjection
        +Assemble(ctx, p) AssembleResult
        +Compact(ctx, p) CompactResult
        +ProjectionCreate/Get/List/Delete
    }

    class Executor {
        <<interface>>
        +RunConversation(ctx, d Deps) error
    }

    class Deps {
        <<interface - executor>>
        +Session() *Session
        +Tools() *Registry
        +Provider() ModelProvider
        +ModelName() string
        +Instructions() string
        +Emit(Event)
        +Config() executor.Config
        +Assembler() ContextEngine
        +SystemSections() []SystemSection
        +AssemblerEnabled() bool
        +RecordUsage(llm.Usage)
        +LastUsage() llm.Usage
    }

    class ModelProvider {
        <<interface>>
        +Name() string
        +Complete(ctx, req) *CompletionResponse, error
        +Stream(ctx, req) *StreamingResponse, error
    }

    class DefaultSummarizer {
        -provider: ModelProvider
        +Summarize(ctx, req) string, error
    }

    class Session {
        +ID: string
        +Append(Message)
        +Messages() []Message
        +Len() int
        +ReplaceAll([]Message)
    }

    class Queue {
        -promptCh: chan Message
        -steerCh: chan Message
        -followupCh: chan Message
        +Enqueue(mode, msg) error
        +Dequeue(ctx) Message, Mode, bool
        +Len() int
    }

    class Bus {
        -subs: []*Subscription
        +Subscribe(buf) *Subscription
        +Emit(Event)
        +SubscriberCount() int
    }

    Agent ..|> Deps : 实现 executor.Deps
    Agent ..|> Deps_ctxengine : 实现 ctxengine.Deps<br/>(Provider/ModelName/Logger)
    DefaultAssembler ..|> ContextEngine
    DefaultAssembler --> DefaultSummarizer
    DefaultSummarizer --> ModelProvider
    Executor --> Deps
    Executor --> ModelProvider
    Agent --> Executor
    Agent --> DefaultAssembler
    Agent --> Session
    Agent --> Queue
    Agent --> Bus
    Agent --> ModelProvider
```

---

## 三、运行时数据流

### 3.1 用户消息 → 助手响应（一次完整 turn）

```mermaid
sequenceDiagram
    autonumber
    participant U as User/Electron
    participant A as Agent
    participant Q as Queue
    participant E as Executor
    participant CE as ContextEngine
    participant P as ModelProvider
    participant S as Session
    participant T as Tool.Registry
    participant B as EventBus

    U->>A: Prompt(content)
    A->>Q: Enqueue(ModePrompt, content)
    Note over A,Q: Abort/Steer/FollowUp 走另两条 channel

    U->>A: Run(ctx)
    A->>Q: Dequeue → msg
    A->>B: Emit(PromptReceivedEvent)
    A->>S: Append(user_msg)

    loop for turn = 1..MaxTurns
        A->>E: RunConversation(ctx, self as Deps)
        E->>B: Emit(TurnStartEvent)

        Note over E: assemblerEnabled?
        alt Assembler 启用
            E->>CE: Assemble(params {Messages, ToolBudget, LastUsage, SystemSections})
            CE-->>E: AssembleResult.Messages
        else 走 fallback
            E->>S: Messages() 直接拿
        end

        E->>P: Stream(CompletionRequest)
        P-->>E: <-chan StreamEvent
        E->>B: Emit(LLMStartEvent)

        loop 消费事件
            P-->>E: TextDeltaEvent
            E->>B: Emit(TextDeltaEvent)
            P-->>E: ToolCallEndEvent
            E-->>E: 累积 ToolCalls
            P-->>E: DoneEvent
            Note over E: drainStream 捕获 Usage
        end

        E->>A: RecordUsage(turnUsage)<br/>(→ Agent.lastUsage)
        E->>S: Append(assistant_msg)
        E->>B: Emit(LLMEndEvent{Assistant, Usage})

        alt 无 ToolCall → 终止
            E->>B: Emit(TurnEndEvent{StopReason: stop})
            E-->>A: return nil
        else 有 ToolCall
            par 并行执行
                E->>T: Execute(tool_args)
                T-->>E: Result
            end
            E->>S: Append(tool_result × N)
            E->>B: Emit(ToolStart/End × N)
            E->>B: Emit(TurnEndEvent{StopReason: tool_calls})
        end
    end

    A->>B: Emit(AgentEndEvent{SessionID, TotalTurns, TotalUsage})
    A-->>U: return nil
```

### 3.2 Assemble 超预算 → Compact 全链路

```mermaid
sequenceDiagram
    autonumber
    participant E as Executor
    participant CE as DefaultAssembler.Assemble
    participant Sum as DefaultSummarizer
    participant P as ModelProvider

    E->>CE: Assemble({Messages, Budget, LastUsage})
    CE->>CE: step 1 tool 截断（ToolResultMaxBytes）
    CE->>CE: step 2 tokensBefore = LastUsage.PromptTokens ?? estimate
    alt tokensBefore > Budget
        CE->>CE: step 3 budget 超 → 触发 Compact
        CE->>CE: Compact({Messages, Budget, LastUsage})

        Note over CE: Compact 7 步
        CE->>CE: step 1 snapshot → CheckPoint{randID}
        CE->>CE: step 1.5 tokensBefore = LastUsage ?? estimate
        alt !Force && tokensBefore <= Budget → no-op
            CE-->>CE: Success=true, 不动 messages
        else 需要压缩
            CE->>CE: step 3 tail=CompactTailKeep, span=msgs[:len-tail]
            CE->>Sum: Summarize({span, Hint, MaxTokens})
            Sum->>P: Complete({System: "summarizer", Messages: span, Stream:false})
            P-->>Sum: CompletionResponse.Content
            Sum-->>CE: summaryText

            CE->>CE: step 4 拼 [summary_msg] + tail
            CE->>CE: step 5 tokensAfter = estimate(newMessages)

            loop tokensAfter > Budget && retries < MaxRetries
                CE->>CE: span 砍半, tail 起点左移
                CE->>Sum: Summarize({span/2, Hint: "compress further"})
                Sum->>P: Complete(...)
                P-->>Sum: text
                Sum-->>CE: newSummary
                CE->>CE: 重拼 + 重估
            end

            alt 仍超 budget
                CE-->>CE: Success=false, 保留原 messages
            else 达标
                CE-->>CE: Success=true, RetainedMessages=newMessages
            end
        end
    end
    CE->>CE: step 4 composeSystemAddition
    CE-->>E: AssembleResult{Messages, EstimatedTokens, Stats{CompactionTriggered: true}}
```

### 3.3 一次 LLM 流式调用的事件时序

```mermaid
sequenceDiagram
    autonumber
    participant E as Executor
    participant P as ModelProvider
    participant SR as StreamingResponse
    participant B as EventBus

    E->>P: Stream(CompletionRequest)
    P->>SR: new channel + goroutine
    P-->>E: *StreamingResponse{Events, Err, Body}
    E->>B: Emit(LLMStartEvent{Model})

    loop for ev := range sr.Events
        alt StartEvent
            Note over E: 已经在 LLMStartEvent 发了，跳过
        else TextDeltaEvent
            E->>E: text += delta
            E->>B: Emit(TextDeltaEvent{Delta})
        else ToolCallStartEvent
            Note over E: 占位，End 时才落
        else ToolCallDeltaEvent
            Note over E: provider 在 End 给完整 JSON
        else ToolCallEndEvent
            E->>E: toolCalls += {ID, Name, Arguments}
        else DoneEvent
            E->>E: drainStream returns (e.Response.Usage, nil)
            Note over E: 退出循环
        else ErrorEvent
            E->>E: 检查 stream.Err() / ctx.Err()
        end
    end

    E->>B: Emit(LLMEndEvent{Assistant, Usage: totalUsage})
```

---

## 四、IPC 边界（网关接入点）

当前 `cmd/app/main.go` 在 init 完 logger/db/agent 后直接 `log.Info("application started successfully")` 然后退出。

**接入路径已调整**：Electron 主进程**不直连** agent 子进程，通信统一经 `internal/gateway/` 承接。
因此 IPC 协议是"网关 ↔ agent 子进程"的私有契约，与网关合并设计（见 `agent-package-roadmap.md` P0 + P8，已延后）。

```mermaid
graph LR
    subgraph Electron主进程
        UI[Vue UI]
        Bridge[HTTP/WS 客户端]
    end
    subgraph Go网关[internal/gateway 待实现]
        GW[HTTP/WS server<br/>鉴权 / 会话注册表]
    end
    subgraph Go子进程[internal/agent]
        Stdio[stdin/stdout]
        IPC_GO[IPC server<br/>待实现]
        A[Agent]
    end

    UI <-->|invoke/on| Bridge
    Bridge <-->|HTTP / WS| GW
    GW <-->|spawn + stdio JSON| Stdio
    Stdio --> IPC_GO
    IPC_GO --> A
    A -.event.EventBus.-> IPC_GO
    IPC_GO -.stream.-> Stdio
```

---

## 五、当前能力 vs 最终目标对照

| 能力 | 当前 | OpenClaw 目标 | 差距 |
|------|------|---------------|------|
| LLM provider | anthropic 1/9 | 9 个 | 8 缺（P6） |
| 流式事件 | 7 类 | 11 类（缺 thinking_*3） | 4 缺（P7） |
| ContextEngine | 10 方法全实现 | 同 | 一致 |
| Memory | stub | 三层 + dreaming | 全缺（P2） |
| Skills | 0 | 完整系统 | 全缺（P3） |
| MCP | 0 | 完整 | 全缺（P4） |
| SubAgent | ErrSubAgentUnsupported | 完整 | 全缺（P5） |
| IPC | 无 | stdio JSON-RPC | 缺（P0，与 P8 网关合并，已延后） |
| 会话持久化 | 内存 | SQLite | 缺（P1） |
| 工具 | 5 个内置 | 内置 + MCP + skill | 部分 |
| Usage | 基础 3 字段 | 含 cache + cost | 缺（P7） |