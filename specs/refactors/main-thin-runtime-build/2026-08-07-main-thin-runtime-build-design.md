# main.go 精简 + runtime.Build 单点装配 重构设计文档

## 1. 概述

### 1.1 问题 / 动机

`src/darvin-agent/cmd/app/main.go` 现状 320 行,`main()` 函数自身 270 行,混合了 6 个层次的逻辑(config / logger / database / agent factory / skills / mcp / gateway server / shutdown)。具体问题点:

1. **平铺装配**:所有 wiring 在 `main()` 里按时间顺序铺平,新人扫一遍需要 5 分钟才能理清边界。
2. **agent 重复构造**:`steerAgent`(main:165) 和 `agentloop.AgentFactory` 内部的 `Build`(factory.go:120) 都调用 `agent.New(...)`,参数同源但分别维护,字段改动时必须两边同步改。
3. **死代码**:`steerAgent` 注释自己承认"不会被任何 Loop 驱动、也不会订阅事件 —— UI 本期不发 steer message";`harness.MustRegister(...)`(main:186-194) 注册的 entry 永远返回 `ErrNotImplemented`,因为 `AgentFactory.Selector` 已经覆盖了 harness 选择路径。
4. **匿名闭包藏核心**:`Selector` 里嵌套的 `Run` 闭包(main:207-217)是整个进程唯一的 `agent.Run` 入口,被两层匿名闭包塞进 factory 字段,看 main 的人没法一眼定位"agent 到底是怎么跑起来的"。
5. **plugins 事后塞**:`factory.Plugins = []tool.Plugin{...}`(main:285)在 factory 构造完之后赋值,读 main 时得往下翻三段才知道工厂带哪些 plugin。
6. **配置翻译散落**:`agentCfg := agent.Config{...}`(main:139-152)13 个字段扁平映射,`cfg.Agent.ToolTimeoutMS * time.Millisecond` 这类单位转换混在结构体字面量里,以后 agent.Config 加字段不会编译报错。
7. **mcp notifier 跨层绑定**:`mcpRegistry.SetNotifier(mcp.Notifier{ OnConnectionChanged: handler.OnMcpConnectionChanged, ... })`(main:274-277)把 handler 的方法引用塞进 mcp package,虽然 mcp 通过 Notifier 接口反向引用是合理模式,但 wiring 在 main 里发生会让人觉得是 main 在硬塞跨包依赖。

参考项目 DeepSeek-Reasonix(`/home/darven/桌面/github-project/DeepSeek-Reasonix-main-v2/cmd/reasonix/main.go`,36 行)用三层结构解决了同类问题:
- `cmd/reasonix/main.go` 36 行,只 `os.Exit(runCLI(args, version))`
- `internal/cli/cli.go` 负责命令分派
- `internal/boot/boot.go::Build(ctx, opts) (*control.Controller, error)` 是**唯一装配点**,所有 frontend(TUI / HTTP / desktop)共享

### 1.2 目标

完成后世界应变成:

1. `cmd/app/main.go` 行数 **< 30**,只做"加载配置 + 调 `runtime.Run` + `os.Exit`"
2. **唯一装配点**:`internal/runtime/runtime.go::Build(ctx, opts) (*Runtime, error)` 一处把 config 翻译成 ready-to-drive 的 `*Runtime`
3. **`*Runtime` 是 frontend 依赖面**:gateway server / shutdown 全部基于 `Runtime` 操作,不再持有 provider / executor / store
4. **死代码删除**:steerAgent 单例构造 + harness 兜底注册全部删除
5. **核心路径可见**:`agent.Run` 的真正入口(`harness.Run` 闭包)被抽成命名函数,main 之外也能 trace
6. **plugins 注入收敛**:在 `Build` 阶段一并传入 factory,不再事后赋值
7. **配置翻译集中**:`cfg → agent.Config` 翻译放在 `newAgentConfig(cfg, workspace)` 一个函数里

### 1.3 非目标

- **不改外部行为**:refactor 阶段 0 用户可感知变化。`agent.steer` JSON-RPC 接口形态微调(`session_id` 改成必填,响应字段扩展,见 §6.2);renderer 当前不发此 RPC(grep 确认),所以实际无用户影响,但严格说 IPC 协议有字段变更。其它(gateway 行为 / DB schema / 配置项)都不动
- **不重构 harness / agentloop / executor / gateway 内部**:只调整 wiring 入口 + 修 steer 接线路径,各包内部结构不动
- **不引入 DI 容器**(wire / fx):保持纯 stdlib + 手写工厂,YAGNI
- **修对 steer 接线路径**(本 spec 范围内,见 §6.2):删除"孤儿 `steerAgent` + `SteerControl` 包装 + `Agent.Steer` 旁路"这套坏设计;改成"`agent.steer` JSON-RPC → handler → `sessions.EnsureEntry(sessionID).Loop.Steer(...)`"——直接打到目标 session 的 Loop 优先级队列,`Loop.Steer` 已有的"push steerQueue + cancel in-flight + wake goroutine"机制就是正确实现。`Agent.FollowUp` 仍无任何调用方,本 spec 一并删除
- **不拆 gateway.SessionManager / Handler**:这两个仍由 `runtime.Build` 内部构造,不暴露给 frontend

## 2. 现状分析

### 2.1 当前 main.go 的 9 个阶段(main:51-320)

| 行号段 | 阶段 | 行数 | 输出物 |
|---|---|---|---|
| 52-72 | infra-1: 配置 + logger | 21 | `cfg`, `log` |
| 77-78 | infra-2: signal → ctx | 2 | `rootCtx`, `stop` |
| 82-117 | infra-3: database + migrate + stores | 36 | `sqliteStore`, `msgStore`, `appState`, `importedFiles` |
| 118-125 | infra-4: LLM provider | 8 | `provider` |
| 130-152 | config-build: workspace + agentCfg | 23 | `effectiveWorkdir`, `agentCfg` |
| 156-181 | domain-1: toolsReg + steerAgent | 26 | `toolsReg`, `steerAgent` |
| 186-218 | domain-2: harness 注册 + factory 构造 | 33 | `factory` |
| 223-228 | domain-3: skills bootstrap | 6 | `skillsResult` |
| 229-246 | domain-4: ledger + session manager + active session bootstrap | 18 | `sessions` |
| 252-260 | domain-5: mcp resolver + registry | 9 | `mcpRegistry` |
| 262-285 | domain-6: handler + plugins | 24 | `handler`, `factory.Plugins` |
| 287-293 | transport: gateway server start | 7 | `gs` |
| 298-319 | shutdown: 关 server / harness / sqlite | 22 | (无) |

### 2.2 外部依赖被 main 直接持有

main 直接 import 并使用的内部包 / 外部包:

| 包 | 用途 |
|---|---|
| `darvin-cowork/backend/internal/config` | 加载 yaml |
| `darvin-cowork/backend/internal/logger` | zap 初始化 |
| `darvin-cowork/backend/internal/database` | sqlite 连接 + migrate |
| `darvin-cowork/backend/internal/agents/store` | SessionStore / MessageStore / AppStateStore / ImportedFileStore |
| `darvin-cowork/backend/internal/llm` | LLM provider 工厂 |
| `darvin-cowork/backend/internal/agents/session` | session.NewSession 占位 |
| `darvin-cowork/backend/internal/agents` | agent.New(...) |
| `darvin-cowork/backend/internal/harness` | harness.MustRegister / NewEmbedded |
| `darvin-cowork/backend/internal/agentloop` | AgentFactory(本 spec 修对后,NewSteerControl 调用被删)|
| `darvin-cowork/backend/internal/skills` | Bootstrap / NewSkillPlugin |
| `darvin-cowork/backend/internal/gateway` | NewEventLedger / NewSessionManager / NewHandler / NewServer |
| `darvin-cowork/backend/internal/mcp` | NewResolverManager / NewRegistry / Notifier |
| `darvin-cowork/backend/internal/tools` | NewBuiltins / NewMcpPlugin / NewRegistry |

**问题**:frontend (gateway server) 不该直接看到这一长串。重构后 frontend 只看到 `*Runtime`,所有上述 import 应搬进 `internal/runtime/` 下。

### 2.3 重复 / 死代码定位

| 位置 | 类型 | 说明 |
|---|---|---|
| main:165-181 `steerAgent, err := agent.New(...)` | 死代码 + 重复 | 构造一个 session="steer-placeholder" 的 agent,只用于 NewSteerControl,自身不入 Loop、不订阅事件。`steerAgent` 与 factory.Build 内的 agent.New 参数同源但分开构造。本 spec 修对路径后,这条孤儿 wiring 全删。 |
| main:186-194 `harness.MustRegister(...)` | 死代码 | 注册一个 `Run` 永远返回 `ErrNotImplemented` 的 entry;factory.resolveHarnessFor 通过 `Selector` 已覆盖选择路径(`factory.go:102-104`);注释自己说 "fallback target for selection scoring",实际无人 select 走它。 |
| main:207-217 `Selector: func(a *agent.Agent, ...) (harness.Harness, error) { return harness.NewEmbedded(...{ Run: func(...) { a.Prompt + a.Run } }), nil }` | 匿名闭包 | 真正的 agent.Run 入口,被两层匿名函数包裹,不可独立单测。 |
| main:285 `factory.Plugins = []tool.Plugin{skillPlugin, mcpPlugin}` | 事后赋值 | factory 已经构造完,plugins 列表外部追加,main 读起来要回看上下文才知道插件集合。 |

### 2.4 参考实现:DeepSeek-Reasonix 的 boot.Build

文件:`/home/darven/桌面/github-project/DeepSeek-Reasonix-main-v2/internal/boot/boot.go`

签名:`Build(ctx context.Context, opts Options) (*control.Controller, error)`

结构要点:
- `Options` 结构体集中所有"运行时旋钮"(`Model / MaxSteps / Sink / WorkspaceRoot / ApprovalTimeout / EffortOverride / PermissionAllow / AdditionalDirs / Stderr / AutoPricingCurrency / ExtraPlugins / TokenMode / SessionDir / SharedHost / HeadlessApprovalMode / SandboxNetworkOverride ...`)
- `Build` 内部依次:config 加载 → secrets 注入 → model 解析 → provider 构造 → tool registry 构建 → permission gate 接入 → executor 构造 → 返回 `*control.Controller`
- `Controller` 是 frontend 的统一接口(`control.SessionAPI`),前端(serve.TUI/desktop)只持 `*Controller`

借鉴三条核心抽象:
1. **单一 Build 入口**:`Build(ctx, opts) (*X, error)`,所有装配集中
2. **Options 集中旋钮**:少数 per-run 字段进 Options,其余从 config 读
3. **X 作为 frontend 依赖面**:frontend 不持有内部组件,只持有 X

## 3. 方案设计

### 3.1 分层架构

```
┌──────────────────────────────────────────────────────────────┐
│ cmd/app/main.go                       (< 30 行,thin)         │
│   - 加载 config / env / signals                               │
│   - runtime.Run(os.Args[1:]) → exitCode                      │
└────────────────────┬─────────────────────────────────────────┘
                     │
┌────────────────────▼─────────────────────────────────────────┐
│ internal/runtime/runtime.go             (新增)               │
│   func Run(args []string) int                                │
│   func Build(ctx, opts Options) (*Runtime, error)            │
│   type Runtime struct { ... }                                │
│   type Options struct { ... }                                │
└────────────────────┬─────────────────────────────────────────┘
                     │
       ┌─────────────┼─────────────┬───────────────────┐
       │             │             │                   │
┌──────▼─────┐ ┌─────▼──────┐ ┌────▼──────────┐ ┌──────▼────────┐
│ config +   │ │ database + │ │ domain 层     │ │ transport 层  │
│ logger     │ │ stores     │ │ provider +    │ │ gateway       │
│ (loadXxx)  │ │ (loadDb)   │ │ factory +     │ │ server        │
│            │ │            │ │ skills + mcp  │ │ (wireServer)  │
└────────────┘ └────────────┘ └───────────────┘ └───────────────┘
```

### 3.2 Runtime 结构体

```go
// internal/runtime/runtime.go

// Runtime 是 frontend(gateway server / future TUI / debug 入口)
// 唯一依赖的装配产物。runtime.Build 内部完成所有 wiring,frontend
// 只看到这一面。
type Runtime struct {
    Cfg      *config.Config         // 已加载的配置(供 Shutdown 等阶段读取)
    Log      *zap.Logger            // 已初始化的 logger
    Provider llm.ModelProvider      // 已构造的 provider(供 Stop / switch model 用)
    Sessions *gateway.SessionManager
    Ledger   *gateway.EventLedger
    Handler  *gateway.Handler
    Server   *gateway.Server        // 已 Start 的 HTTP/SSE server
    MCP      *mcp.Registry
    Skills   *skills.BootstrapResult
    Factory  *agentloop.AgentFactory
    Stores   Stores                 // 聚合 sqliteStore + msgStore + appState + importedFiles
}

type Stores struct {
    Sessions      store.SessionStore
    Messages      store.MessageStore
    AppState      store.AppStateStore
    ImportedFiles store.ImportedFileStore
}

// Shutdown 按依赖反序释放:Server → Harness 全局 → Stores.Close。
// 返回第一个非 nil 错误,但不短路。
func (r *Runtime) Shutdown(ctx context.Context) error { ... }

// 注:RUNTIME 故意不持有任何 steer 句柄。Steer 相关 plumbing 全部删除
// (§6.2),接口形态不再保留;后续若 UI 真需要 steer,基于 per-session
// steering 重新设计。
```

**关键约束**:
- `Runtime` 字段都是公开的(`zap.Logger` / `gateway.Server` 等),因为 frontend 需要 shutdown 时持有;但 frontend 不应该 `r.Factory.Build(...)` 这样穿透调用 —— 这是**约定**,不强制(Go 没有 friend 关键字)
- 不导出 `Build` 内的中间构造物(避免依赖逃逸)

### 3.3 Options 结构体

```go
// internal/runtime/runtime.go

// Options 集中所有"运行时旋钮"。Config 路径 / workspace 根目录等
// 通过 flags / env 注入;Provider 强制重建等调试旋钮也走这里。
// 绝大多数字段从 config 读,不需要重复在 Options 里出现。
type Options struct {
    // ConfigPath 覆盖 default config.yaml 查找路径($DARVIN_CONFIG > exe-dir > cwd)。
    ConfigPath string

    // WorkspaceRoot 覆盖 cfg.Agent.Workdir($DARVIN_AGENT_WORKSPACE);用于
    // desktop bridge 注入 fsSandbox.root,跟 Electron 主进程对齐。
    WorkspaceRoot string

    // HarnessSelector 注入 harness 选择逻辑;nil 时走 runtime 默认
    // (factory.Selector 内联)。测试用。
    HarnessSelector agentloop.HarnessSelector

    // ExtraPlugins 注入测试用 / ACP session/new 用的临时 plugin;生产场景为 nil。
    // (本期不在范围,留 hook 给后续 spec)
    ExtraPlugins []tool.Plugin
}
```

**只 4 个字段**——比 Reasonix 的 `boot.Options`(20+ 字段)保守,因为大部分字段对当前 frontend 不需要。后续有需求再加,不预测。

### 3.4 Build 函数签名

```go
// internal/runtime/runtime.go

// Build 是 darvin-agent 进程唯一的装配入口:从 cfg / DB / env 出发,构造
// provider → tool registry → factory → skills → mcp → handler → server,
// 并 Start gateway server。返回的 *Runtime 已 Start,frontend 只需等
// ctx.Done() 然后调 Shutdown。
func Build(ctx context.Context, opts Options) (*Runtime, error)

// Run 是 main 的调用入口;返回进程退出码(0 / 1 / 2)。
// 内部 Build 失败 → 返回 1;ctx 取消后正常 Shutdown → 返回 0。
func Run(args []string) int
```

### 3.5 各阶段拆分(替代原 main 各段)

| 新函数 | 替代原 main 行号段 | 行数预期 |
|---|---|---|
| `loadConfig(args) (*config.Config, *zap.Logger, error)` | 52-72 | ~30 |
| `loadDatabase(cfg) (Stores, func() error, error)` | 82-117 | ~35 |
| `loadProvider(ctx, cfg) (llm.ModelProvider, error)` | 118-125 | ~10 |
| `resolveWorkspace(cfg, override) string` | 130-137 | ~10 |
| `newAgentConfig(cfg, workspace) agent.Config` | 139-152 | ~20 |
| `newAgentFactory(cfg, deps...) *agentloop.AgentFactory` | 156-218 + 285 | ~60 |
| `bootstrapSkills(ctx, log, ...) *skills.BootstrapResult` | 223-228 | ~5(已是包装) |
| `bootstrapMCP(ctx, log, workspace, deps...) (*mcp.Registry, error)` | 252-260 | ~25 |
| `wireGateway(rt, cfg) error` | 229-285 | ~40 |
| `serveUntilShutdown(ctx, rt) int` | 287-319 | ~25 |

`Run(args)` 内部流程(伪代码):

```go
func Run(args []string) int {
    cfg, log, err := loadConfig(args)
    if err != nil { return 1 }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    rt, err := Build(ctx, Options{
        ConfigPath:    cfgPath,
        WorkspaceRoot: envOr(cfg.Agent.Workdir),
    })
    if err != nil {
        log.Error("runtime build failed", zap.Error(err))
        return 1
    }
    defer func() {
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        if err := rt.Shutdown(shutdownCtx); err != nil {
            log.Warn("shutdown error", zap.Error(err))
        }
    }()

    <-ctx.Done()
    log.Info("shutdown signal received")
    return 0
}
```

### 3.6 Build 内部顺序(伪代码)

```go
func Build(ctx context.Context, opts Options) (*Runtime, error) {
    // 1. cfg / log (Run 已经加载,这里只接受入参)
    cfg := mustConfig(opts.ConfigPath)
    log := mustLogger(cfg.Log)

    // 2. database + stores
    stores, dbCloser, err := loadDatabase(cfg)
    if err != nil { return nil, err }

    // 3. provider
    provider, err := loadProvider(ctx, cfg)
    if err != nil { return nil, err }

    // 4. workspace + agent config 翻译
    workspace := resolveWorkspace(cfg, opts.WorkspaceRoot)
    agentCfg := newAgentConfig(cfg, workspace)

    // 5. tools (一次构造,factory 引用)
    toolsReg, err := tool.NewBuiltins(workspace, cfg.Agent.ShellAllowlist)
    if err != nil {
        log.Warn("tool registry init failed, using empty", zap.Error(err))
        toolsReg = tool.NewRegistry()
    }

    // 6. factory — 单次构造,plugins 同步传入
    factory := newAgentFactory(agentloop.AgentFactoryDeps{
        Name: cfg.App.Name + "-agent",
        Cfg: agentCfg, Provider: provider, Tools: toolsReg,
        Store: stores.Sessions, MessageStore: stores.Messages, Logger: log.Logger,
        HarnessSelector: opts.HarnessSelector,    // 默认走 embedded
        ExtraPlugins:   opts.ExtraPlugins,
    })

    // 7. skills + mcp
    skillsResult := bootstrapSkills(ctx, log, workspace, toolsReg)
    mcpReg, err := bootstrapMCP(ctx, log, workspace)
    if err != nil { return nil, err }

    // 8. ledger + session manager
    ledger := gateway.NewEventLedger(log.Logger)
    sessions := gateway.NewSessionManager(
        gateway.WithAgentFactory(factory),
        gateway.WithEventLedger(ledger),
    )

    // 9. handler(无 steer 参数,SteerControl plumbing 已删)
    handler := gateway.NewHandler(sessions, ledger,
        stores.Sessions, stores.Messages, stores.AppState,
        gateway.HandlerOptions{
            ImportedFiles: stores.ImportedFiles,
            WorkspaceRoot: workspace,
            Skills:        skillsResult.Registry,
            SkillRunner:   skillsResult.Runner,
            Mcp:           mcpReg,
            Log:           log.Logger,
        })

    // 10. mcp notifier 绑定(留在这里而不是 §3.7 提到的改方向)
    mcpReg.SetNotifier(mcp.Notifier{
        OnConnectionChanged: handler.OnMcpConnectionChanged,
        OnResolutionChanged: handler.OnMcpResolutionChanged,
    })

    // 11. gateway server start
    server := gateway.NewServer(handler, log.Logger)
    if err := server.Start(ctx); err != nil { return nil, err }

    // 12. active session bootstrap (FR-9)
    if activeID, err := stores.AppState.GetActiveSession(ctx); err == nil && activeID != "" {
        if _, err := sessions.EnsureEntry(activeID); err != nil {
            log.Warn("bootstrap active session failed", zap.String("session_id", activeID), zap.Error(err))
        }
    }

    return &Runtime{
        Cfg: cfg, Log: log, Provider: provider,
        Sessions: sessions, Ledger: ledger, Handler: handler,
        Server: server, MCP: mcpReg, Skills: skillsResult,
        Factory: factory, Stores: stores,
    }, nil
}
```

### 3.7 关键抽象:抽出 `newEmbeddedHarness`

原来 main:207-217 的匿名闭包提取为命名函数:

```go
// internal/runtime/harness.go

// newEmbeddedHarness 构造直接驱动 agent.Prompt + agent.Run 的 embedded
// harness。这是 darvin-agent 进程唯一的 prompt 路径:gateway handler
// 收 prompt → Loop.Submit → harness.RunAttemptWithLifecycle → 这里的
// Run → a.Prompt + a.Run → executor.RunConversation。
//
// 抽出为命名函数后,这个核心调用栈不再藏在两层匿名闭包里;测试也可
// 以单独 trace。
func newEmbeddedHarness(a *agent.Agent) harness.Harness {
    return harness.NewEmbedded(harness.EmbeddedConfig{
        Run: func(ctx context.Context, p harness.RunAttemptParams) (*harness.AttemptResult, error) {
            if err := a.Prompt(ctx, p.Prompt, nil, p.Attachments); err != nil {
                return nil, err
            }
            _ = a.Run(ctx)
            return &harness.AttemptResult{Status: harness.AttemptOK}, nil
        },
    })
}

// defaultHarnessSelector 是 factory 默认的 selector:任何 agent 都用
// embedded harness 驱动。
func defaultHarnessSelector(a *agent.Agent, _ *agentloop.AgentFactory) (harness.Harness, error) {
    return newEmbeddedHarness(a), nil
}
```

main / factory 都不再持有这个闭包的源代码。

### 3.8 AgentFactory 构造签名

为了让 plugins 同步传入 factory,扩展 `agentloop.AgentFactory` 字段或者引入 `AgentFactoryDeps`:

**方案选择**:不修改 `AgentFactory` 现有结构体字段(避免破坏 `NewAgentLoopSession` 等调用方);在 `runtime` 包内提供构造器:

```go
// internal/runtime/factory.go

type AgentFactoryDeps struct {
    Name             string
    Instructions     string
    Model            agent.ModelRef
    Provider         llm.ModelProvider
    Store            store.SessionStore
    MessageStore     store.MessageStore
    Logger           *zap.Logger
    Config           agent.Config
    Tools            *tool.Registry
    AssemblerEnabled bool
    HarnessSelector  agentloop.HarnessSelector
    ExtraPlugins     []tool.Plugin       // ← 注入时机统一到构造时
}

func newAgentFactory(d AgentFactoryDeps) *agentloop.AgentFactory {
    selector := d.HarnessSelector
    if selector == nil {
        selector = defaultHarnessSelector
    }
    return &agentloop.AgentFactory{
        Name:             d.Name,
        Instructions:     d.Instructions,
        Model:            d.Model,
        Provider:         d.Provider,
        Store:            d.Store,
        MessageStore:     d.MessageStore,
        Logger:           d.Logger,
        Config:           d.Config,
        Tools:            d.Tools,
        AssemblerEnabled: d.AssemblerEnabled,
        Selector:         selector,
        Plugins:          d.ExtraPlugins,    // ← 构造时设置
    }
}
```

注:`agentloop.AgentFactory` 已有 `Plugins` 字段(`factory.go:37`),只需在构造时填,不要事后赋值。`HarnessSelector` 字段需要新增到 `agentloop.AgentFactory`(见 §6.1)。

### 3.9 配置翻译集中

```go
// internal/runtime/agent_config.go

// newAgentConfig 把 cfg.Agent 翻译成 agent.Config。所有 cfg.Agent.X →
// agent.Config.Y 的映射、字段单位转换、默认值都在这一个函数里。
func newAgentConfig(cfg *config.Config, workspace string) agent.Config {
    return agent.Config{
        MaxTurns:             cfg.Agent.MaxTurns,
        ToolTimeout:          time.Duration(cfg.Agent.ToolTimeoutMS) * time.Millisecond,
        Workdir:              workspace,
        ShellAllowlist:       cfg.Agent.ShellAllowlist,
        EventBuffer:          cfg.Agent.EventBuffer,
        TokenBudget:          cfg.Agent.TokenBudget,
        CompactTailKeep:      cfg.Agent.CompactTailKeep,
        ToolResultMaxBytes:   cfg.Agent.ToolResultMaxBytes,
        CompactMaxRetries:    cfg.Agent.CompactMaxRetries,
        SummarizeMaxTokens:   cfg.Agent.SummarizeMaxTokens,
        SystemPromptAddition: cfg.Agent.SystemPromptAddition,
        AssemblerEnabled:     cfg.Agent.AssemblerEnabled,
    }
}
```

以后 `agent.Config` 加字段时,只在这一处加一行;编译器会因函数返回类型变化强制更新。

## 4. 实施步骤

按"行为不变 → 结构清理 → 死代码删除 → 跨层调整"四阶段,每阶段可独立编译 + 单测。

### 阶段 1:新建 runtime 包骨架(0 行为变化)

新增文件,不删不改任何旧文件:
- `src/darvin-agent/internal/runtime/runtime.go`(Runtime / Options / Build / Run 框架,部分函数可先 stub)
- `src/darvin-agent/internal/runtime/config.go`(loadConfig / mustLogger)
- `src/darvin-agent/internal/runtime/database.go`(loadDatabase)
- `src/darvin-agent/internal/runtime/provider.go`(loadProvider)
- `src/darvin-agent/internal/runtime/harness.go`(newEmbeddedHarness + defaultHarnessSelector)
- `src/darvin-agent/internal/runtime/agent_config.go`(newAgentConfig)
- `src/darvin-agent/internal/runtime/factory.go`(AgentFactoryDeps + newAgentFactory)
- `src/darvin-agent/internal/runtime/gateway.go`(wireGateway)
- `src/darvin-agent/internal/runtime/skills.go`(bootstrapSkills)
- `src/darvin-agent/internal/runtime/mcp.go`(bootstrapMCP)

实现按现状镜像搬运:`runtime.Build` 内各步骤逻辑与 main 一一对应,行为完全不变。

验证:
```bash
cd src/darvin-agent
go build ./...
go vet ./...
```

### 阶段 2:删 main 死代码 + 修对 steer 接线路径

修改 `cmd/app/main.go`:
- 删除 `steerAgent, err := agent.New(...)`(原 main:165-181)
- 删除 `harness.MustRegister(harness.NewEmbedded(...), "")`(原 main:186-194)
- 删除 `steer := agentloop.NewSteerControl(steerAgent)`(原 main:234)
- `gateway.NewHandler(...)` 不再传 steer 参数

同步修改(本阶段一并做,不拆):
- 删除 `internal/agentloop/steer.go` 整文件(`SteerControl` / `steerControl` / `ErrSteerNotImplemented` 是孤儿包装)
- 删除 `internal/agentloop/queue.go` 整文件(`Queue` 包装层无调用方)
- `internal/agents/dispatcher.go`:删除 `Agent.Steer` / `Agent.FollowUp` 方法
- `internal/agents/event/event.go`:删除 `ModeFollowUp` 常量
- `internal/agents/queue/queue.go`:删除 `ModeFollowUp` 常量 + 相关 case 分支
- `internal/agents/agent.go:4` 注释更新
- `internal/agents/errors.go:6-7` `ErrAgentBusy` 注释更新
- `internal/agents/dispatcher.go` 多处 Steer/FollowUp 注释引用清理
- `internal/gateway/server.go:57` 注释更新(移除 SteerControl 提及)

重写 `gateway/handlers.go` 的 steer 相关:
- `SteerParams`:字段调整 `SessionID string` (必填)、`RunID string` (可选)、`Content string` (必填)
- `SteerResult`:字段扩展 `Steered bool` / `RunID string` / `MessageID string` / `Queued bool`
- `Handler.Steer` 字段删除
- `NewHandler(...)` 删第 6 参 `steer`
- `handleSteer` 重写:取 `p.SessionID` → `h.Sessions.EnsureEntry(...)` → 校验 `entry.AgentLoop != nil` → 调 `entry.AgentLoop.Loop.Steer(PromptRequest{RunID, Content})` → 返回 `SteerResult`
- `Loop.Steer` / `Loop.steerQueue` / `popLocked` steer 优先分支 / `Loop.admit` 的 `jumpQueue` 参数 **全部保留**(这是正路)

跑 `go test ./...` 看失败清单,逐个删除/更新引用被删符号的测试。

验证:
```bash
go build ./...
go vet ./...
go test ./...
grep -E "steerAgent|SteerControl\b|Agent\.Steer\b|Agent\.FollowUp\b|ModeFollowUp|harness\.MustRegister" src/darvin-agent/  # 必须无源代码命中
grep -n "\.Loop\.Steer(" src/darvin-agent/   # 必须唯一命中 gateway/handlers.go
```

### 阶段 3:main 切换到 runtime.Run(行为不变)

`cmd/app/main.go` 缩为:

```go
package main

import (
    "os"

    "darvin-cowork/backend/internal/runtime"
)

// runApp = runtime.Run; var 而非 const 便于测试注入。
var runApp = runtime.Run

func main() {
    os.Exit(runApp(os.Args[1:]))
}
```

旧 main 的 320 行全部删除(代码逻辑搬到 runtime/* 下)。如果某个行为边界不确定,**宁可保留 main 的等价行 + 注释 TODO**,也不允许行为漂移。

验证:
```bash
go build ./...
go vet ./...
go test ./...
# 集成测试同阶段 2
```

### 阶段 4:配置翻译 + plugins 时机收敛

- main 已经不持有 agentConfig 字面量,改为 `newAgentConfig(cfg, workspace)` 调用
- factory 构造时一次性传 plugins,删除原 `factory.Plugins = []tool.Plugin{...}` 事后赋值
- main 不再持有 `agentCfg` 局部变量

验证同阶段 3。

### 阶段 5(可选):补单测

- `internal/runtime/agent_config_test.go`:用固定 cfg 验证 13 个字段映射正确
- `internal/runtime/harness_test.go`:用 mock agent 验证 `newEmbeddedHarness.Run` 调用顺序
- 不强求 runtime.Build 整体单测(依赖太多,价值低)

## 5. 涉及文件

### 5.1 新增文件

| 文件 | 用途 |
|---|---|
| `src/darvin-agent/internal/runtime/runtime.go` | Runtime / Options / Build / Run |
| `src/darvin-agent/internal/runtime/config.go` | loadConfig / mustLogger |
| `src/darvin-agent/internal/runtime/database.go` | loadDatabase + Stores 聚合 |
| `src/darvin-agent/internal/runtime/provider.go` | loadProvider |
| `src/darvin-agent/internal/runtime/harness.go` | newEmbeddedHarness + defaultHarnessSelector |
| `src/darvin-agent/internal/runtime/agent_config.go` | newAgentConfig |
| `src/darvin-agent/internal/runtime/factory.go` | AgentFactoryDeps + newAgentFactory |
| `src/darvin-agent/internal/runtime/gateway.go` | wireGateway |
| `src/darvin-agent/internal/runtime/skills.go` | bootstrapSkills |
| `src/darvin-agent/internal/runtime/mcp.go` | bootstrapMCP |
| `src/darvin-agent/internal/runtime/agent_config_test.go`(可选) | 配置翻译单测 |

### 5.2 修改文件

| 文件 | 变更说明 |
|---|---|
| `src/darvin-agent/cmd/app/main.go` | **大幅修改**:320 → < 30 行;仅 `os.Exit(runApp(...))`;删除 `steerAgent` 构造、`harness.MustRegister` fallback、`agentloop.NewSteerControl` 调用 |
| `src/darvin-agent/internal/agentloop/factory.go` | 小改:增加 `HarnessSelector` 字段,默认走 embedded |
| `src/darvin-agent/internal/agentloop/loop.go` | **不动**(保留 `Loop.Steer` / `steerQueue` / `popLocked` 优先分支 / `admit` 的 `jumpQueue` 参数) |
| `src/darvin-agent/internal/gateway/handlers.go` | `SteerParams` 字段调整(`SessionID` 必填、`RunID` 可选);`SteerResult` 扩字段(`RunID`/`MessageID`/`Queued`);**重写** `handleSteer`(调 `entry.AgentLoop.Loop.Steer`);删除 `Handler.Steer` 字段、`NewHandler` 第 6 参 `steer` |
| `src/darvin-agent/internal/gateway/server.go` | 注释更新:移除 `agentloop.SteerControl` 提及 |
| `src/darvin-agent/internal/agents/dispatcher.go` | **删除** `Agent.Steer` / `Agent.FollowUp` 方法;清理 Steer/FollowUp 相关注释 |
| `src/darvin-agent/internal/agents/agent.go` | 注释更新:从 "Run / Prompt / Steer / FollowUp / Abort" 改为 "Run / Prompt / Abort" |
| `src/darvin-agent/internal/agents/errors.go` | `ErrAgentBusy` 注释改为 "use Abort + Prompt"(error 本体保留) |
| `src/darvin-agent/internal/agents/event/event.go` | **删除** `ModeFollowUp` 常量(只 `ModeSteer` / `ModePrompt` 保留);清理相关注释 |
| `src/darvin-agent/internal/agents/queue/queue.go` | **删除** `ModeFollowUp` 常量及其 `Enqueue`/`Dequeue` 中相关 case 分支;queue 收敛为 `ModePrompt` + `ModeSteer` |

### 5.3 删除文件

| 文件 | 原因 |
|---|---|
| `src/darvin-agent/internal/agentloop/steer.go` | `SteerControl` / `steerControl` / `ErrSteerNotImplemented` 是孤儿包装,修对后无下游消费者 |
| `src/darvin-agent/internal/agentloop/queue.go` | `Queue` 包装层无调用方(grep 全仓库确认) |

### 5.4 配套删除 / 更新的测试文件

(具体哪些测试文件引用了被删符号,在实施阶段跑 `go test ./...` 看失败清单后逐个处理;原则上:`*_test.go` 文件如果只测试已删除符号,整文件删;如果只删部分 case,改 test 即可。)

### 5.5 不修改的文件(明确划界)

- `internal/agents/**` 中除 §5.2 列出之外的文件(executor / protocol / ctxengine / store / session / msgid / runtime / text_delta_hook / usage 等)
- `internal/gateway/**` 中除 §5.2 列出之外的文件(server / broadcaster / eventledger / jsonrpc / client / sessions / handlers_mcp / handlers_skills / handlers_im 等)
- `internal/skills/**`、`internal/mcp/**`、`internal/harness/**`、`internal/config/**`、`internal/database/**`、`internal/logger/**`
- `internal/llm/**`(provider / anthropic / openai 等)
- `internal/tools/**`

## 6. 边界情况 / 风险

### 6.1 `AgentFactory.HarnessSelector` 字段新增

`agentloop.AgentFactory` 当前没有 `HarnessSelector` 字段;`resolveHarnessFor` 只看 `HarnessID` 和默认 `SelectHarness`(factory.go:94-113)。为了让 `defaultHarnessSelector` 能注入到 factory,需要:

**方案**:在 `agentloop.AgentFactory` 加 `Selector HarnessSelector` 字段(已存在,factory.go:52),但当前默认 fallback 用 `SelectHarness`(factory.go:105)。要改成"如果传了 Selector 就用,否则走默认"——已经是这个语义(factory.go:102-104),不需要改。

但 default `SelectHarness` 路径会经过 harness 全局 registry,而我们要的 default 是 `defaultHarnessSelector`(embedded)。所以需要在 `AgentFactoryDeps.HarnessSelector` 为 nil 时,`newAgentFactory` 显式设成 `defaultHarnessSelector`,而不是让 factory 内部 fallback。

**实现**:
```go
selector := d.HarnessSelector
if selector == nil {
    selector = defaultHarnessSelector    // 走 embedded harness
}
return &agentloop.AgentFactory{
    ...
    Selector: selector,    // 总是非 nil
    Plugins:  d.ExtraPlugins,
}
```

这样 factory.Selector 永远是显式的,不会有 fallback 隐式行为。

### 6.2 修对 steer 接线路径(不删 steer,只删孤儿实例 + 包装层)

**问题诊断**:

当前 steer 的接线路径:

```
renderer (不发) ─→ gateway.handleSteer ─→ h.Steer.Steer (SteerControl 接口)
                                              ↓
                                          steerControl.Steer
                                              ↓
                                          steerAgent.Steer  (孤儿 agent,自己 private queue)
                                              ↓
                                          steerAgent.Abort + enqueue(ModeSteer) → 没人消费
```

`steerAgent` 是 `cmd/app/main.go` 构造的孤儿:
- `session.NewSession("steer-placeholder")`
- 没有 Loop 绑它 → abort 信号没人接收
- 没有 consumer → enqueue(ModeSteer) 进了死队列
- 但 `JSON-RPC 端点 + handler.Steer 字段 + SteerControl 接口 + steerControl 实现 + ErrSteerNotImplemented` 全套 plumbing 都在

**实际上 `Loop.Steer` 已经是对的**:

```go
// agentloop/loop.go:159
func (l *Loop) Steer(req PromptRequest) (RunTicket, error) {
    return l.admit(req, nil, true)   // jumpQueue=true → steerQueue
}
```

+ `Loop.admit` 在 `jumpQueue=true` 时主动取消 in-flight run(`active.cancelRun()`)
+ `Loop.popLocked` 优先 pop steerQueue
+ `Loop.run` goroutine 处理时 wake channel

完整机制是对的,只是**没人调用 `Loop.Steer`**——`SteerControl` 走了另一条死路。

**修复方案**:

```
renderer ─→ gateway.handleSteer ─→ sessions.EnsureEntry(sessionID)
                                     ↓
                                     entry.AgentLoop.Loop.Steer(req)
                                     ↓
                                     Loop.admit(req, nil, true) → steerQueue + cancelRun + wake
                                     ↓
                                     Loop.run goroutine → executeTurn
```

JSON-RPC 直接打到 session 的 Loop。不再需要中间 `SteerControl` 包装,也不需要孤儿 `steerAgent`。

**改动清单**:

| 项 | 处理 |
|---|---|
| `agent.steer` JSON-RPC endpoint (`gateway/handlers.go:302`) | **保留**,逻辑重写 |
| `handleSteer` 函数 (`gateway/handlers.go:511-526`) | **重写**:不再调 `h.Steer.Steer(...)`,改成查 sessions + 调 `entry.AgentLoop.Loop.Steer(PromptRequest{...})` |
| `SteerParams` (`gateway/handlers.go:75-83`) | **改字段**:`SessionID` 改成**必填**;`RunID` 可选(允许精确指定 in-flight turn);`Content` 保留 |
| `SteerResult` (`gateway/handlers.go:81-83`) | **扩字段**:返回 `RunID` / `MessageID` / `Queued` 三字段,与 `RunTicket` 对齐 |
| `Handler.Steer` 字段 (`gateway/handlers.go:231`) | **删除字段** —— handler 直接用 `h.Sessions` 找 Loop |
| `NewHandler(..., steer, ...)` 第 6 参 (`gateway/handlers.go:264, 277`) | **删除参数 + 删除赋值** |
| `SteerControl` interface (`agentloop/steer.go:14-20`) | **删除** —— 接口无下游消费者 |
| `steerControl` impl (`agentloop/steer.go:28-36`) | **删除** |
| `ErrSteerNotImplemented` (`agentloop/steer.go:13`) | **删除** —— `Redirect` 暂时不暴露,如未来需要,基于 per-session steering 重新设计 |
| `agentloop/steer.go` 整文件 | **删除** |
| `steerAgent` 单例构造 (`cmd/app/main.go:165-181`) | **删除** —— 无下游 |
| `harness.MustRegister(harness.NewEmbedded(...), "")` fallback (`cmd/app/main.go:186-194`) | **删除** —— `factory.Selector` 已覆盖 |
| `steer := agentloop.NewSteerControl(steerAgent)` (`cmd/app/main.go:234`) | **删除** —— `NewSteerControl` 已删 |
| `Agent.Steer` (`agents/dispatcher.go:37-41`) | **删除** —— 孤儿方法,正路走 `Loop.Steer` |
| `queue.ModeSteer` (`agents/queue/queue.go:20`) | **保留** —— `Loop` 自己用 |
| `event.ModeSteer` (`agents/event/event.go:26`) | **保留** —— queue 内部 enum |
| `Loop.Steer` (`agentloop/loop.go:159`) | **保留** —— 这是正路 |
| `Loop.steerQueue` + `popLocked` 优先分支 + `admit` 的 `jumpQueue` 参数 | **保留** |
| `Agent.FollowUp` (`agents/dispatcher.go:47-49`) | **删除** —— 无任何调用方 |
| `queue.ModeFollowUp` (`agents/queue/queue.go:21`) | **删除** —— 仅 `Agent.FollowUp` 用 |
| `event.ModeFollowUp` (`agents/event/event.go:27`) | **删除** —— 仅 queue 引用 |
| `agentloop/queue.go` 包装层 | **删除** —— 全仓库无调用方 |
| `gateway/server.go:57` 注释提到 `agentloop.SteerControl` | **改注释** |
| `agents/errors.go:6-7` `ErrAgentBusy` 注释 | **改注释**:从 "use Steer or FollowUp" 改为 "use Abort + Prompt" |
| `agents/agent.go:4` 注释 | **改注释**:从 "Run / Prompt / Steer / FollowUp / Abort" 改为 "Run / Prompt / Abort" |
| `agents/dispatcher.go:21, 81, 369-370, 198` 等处 Steer/FollowUp 注释引用 | **清理注释** |
| `internal/gateway/server.go:57` 注释 "EventLedger / agentloop.Loop / agentloop.SteerControl dependencies" | **改注释**:移除 SteerControl 提及 |

**修对后的 handleSteer 实现要点**(伪代码):

```go
func handleSteer(ctx context.Context, id json.RawMessage, params json.RawMessage, _ *client, h *Handler) *Response {
    var p SteerParams
    if err := json.Unmarshal(params, &p); err != nil { ... }
    if p.SessionID == "" {
        return errorResp(id, CodeInvalidParams, "session_id is required", nil)
    }
    if strings.TrimSpace(p.Content) == "" {
        return errorResp(id, CodeInvalidParams, "content is required", nil)
    }

    entry, err := h.Sessions.EnsureEntry(p.SessionID)
    if err != nil { return errorResp(id, CodeInternalError, "session lookup", err) }
    if entry.AgentLoop == nil {
        return errorResp(id, CodeNotFound, "session has no agent loop", nil)
    }

    ticket, err := entry.AgentLoop.Loop.Steer(agentloop.PromptRequest{
        RunID:   p.RunID,
        Content: p.Content,
    })
    if err != nil { return errorResp(id, CodeInternalError, "loop steer", err) }

    return successResp(id, SteerResult{
        Steered:   true,
        RunID:     ticket.RunID,
        MessageID: ticket.MessageID,
        Queued:    ticket.Queued,
    })
}
```

**修对后,agent 公开 API 收敛为**(删除 `Steer` / `FollowUp` 后的 `Agent`):

```go
type Agent interface {
    Prompt(ctx, content, images, attachments...) error
    Run(ctx) error
    Abort(ctx) error
}
```

(其它字段/方法如 `Session()` / `Tools()` / `Provider()` / `Config()` / `Emit()` 等不动)

**修对后,Loop 公开 API 保持**(本 spec 不动 `Loop` 内部):

```go
func (l *Loop) Submit(req PromptRequest) (RunTicket, error)
func (l *Loop) SubmitSkill(sec SkillInvocation) (RunTicket, error)
func (l *Loop) Steer(req PromptRequest) (RunTicket, error)   // ← 被 handler 调到
func (l *Loop) Stop(runID string) bool
func (l *Loop) Abort(ctx context.Context) error
func (l *Loop) Close()
func (l *Loop) CurrentMessageID() string
func (l *Loop) CurrentUserMessageID() string
func (l *Loop) CurrentRunID() string
func (l *Loop) ActiveRunID() string
```

**修对后,queue.Mode 收敛为**:

```go
type Mode = event.Mode
const (
    ModePrompt Mode = "prompt"   // queue 内部用
    ModeSteer  Mode = "steer"    // Loop.admit 内部用
)
```

(`ModeFollowUp` 删除;queue.Enqueue/Dequeue 的 case 分支收敛为只 `ModePrompt` + `ModeSteer`。)

**风险评估**:

| 风险点 | 缓解 |
|---|---|
| renderer 旧版本可能还在按旧 `SteerParams` 形态发(无 session_id) | 加兜底:缺 session_id 直接返 `CodeInvalidParams`;旧 renderer 会收到错,新 renderer 行为正确 |
| renderer 旧版本拿到 `{steered: true}` 就以为成功,不看新字段 | 新字段都加,旧字段保留(向后兼容),defer response shape migration |
| `SessionEntry.AgentLoop` 在 prompt 之前是 nil(`handleSteer` 拿到的是空 entry) | 显式判断 `entry.AgentLoop == nil` → 返 `CodeNotFound`,提示 client 先发 prompt |
| in-flight 取消不彻底 | `Loop.admit` 在 jumpQueue 时已调 `active.cancelRun()`,LSP/工具执行的 ctx 都串到这个 cancel,链路清晰 |
| 测试还在引用被删符号 | 跑 `go test ./...` 看失败,逐个修 |
| 旧 specs 还在引用这些 API | 不动历史 spec;本次 spec 之后新建的 spec 不再引用 |
| renderer 端 preload / IPC channel 定义 | grep 确认无 `agent.steer` 调用方 |

#### 6.2.1 实施前必做的额外验证

```bash
# 1. 确认 renderer / preload / shared 不发 agent.steer
grep -rn "agent\.steer\|sendSteer\|invokeSteer" src/

# 2. 确认没有别处调 Agent.Steer / Agent.FollowUp(应该只有 steer.go 一处)
grep -rn "\.Steer(ctx\|Agent\.Steer\|\.FollowUp(ctx\|Agent\.FollowUp" src/

# 3. 确认 Loop.Steer 没有别的外部调用方
grep -rn "Loop\.Steer\|\.Loop\.Steer(" src/darvin-agent/

# 4. 确认 queue.ModeSteer 没有外部调用方(应该只 Loop.admit 内部用)
grep -rn "queue\.ModeSteer\|ModeSteer" src/darvin-agent/
```

(本仓库已 grep 确认:`Loop.Steer` 无外部调用方 → 修对后会变成 handler 调用,grep 应当唯一命中 `gateway/handlers.go` 的 `handleSteer` 函数;`queue.ModeSteer` 应只命中 `agentloop` 包内。)

### 6.3 mcp notifier 跨层绑定

当前 `mcp.Registry.SetNotifier(mcp.Notifier{ OnConnectionChanged: handler.OnMcpConnectionChanged, ... })` 在 main 里硬绑。

**评估**:这是合理的 observer 模式,mcp 不知道 gateway 存在,通过接口反向通知 gateway。**本期不调整方向**,只把 wiring 位置从 main 搬到 runtime.Build 内(见 §3.6 步骤 10)。

如果未来要改方向(让 gateway 主动订阅 mcp),那是单独 spec。

### 6.4 active session bootstrap 顺序

main:240-246 在 sessions 构造之后立刻 bootstrap active session。本期保留此顺序(放在 runtime.Build 第 12 步,见 §3.6)。

风险:`stores.AppState.GetActiveSession` 是 IO,可能在 sessions 构造前调用更早,但当前 main 的顺序是 sessions.EnsureEntry 依赖 sessions 已构造。runtime.Build 必须保持这个顺序。

### 6.5 signal.NotifyContext 生命周期

当前 main:77 创建 rootCtx,`defer stop()`。runtime.Run 内保留同样模式;shutdown 阶段由 `Runtime.Shutdown(ctx)` 接受独立 shutdownCtx(原 main:301 用的是 3s 超时的 shutdownCtx)。

**实现**:runtime.Run 内:
```go
ctx, stop := signal.NotifyContext(...)
defer stop()
rt, err := Build(ctx, opts)
// ...
<-ctx.Done()
shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
return rt.Shutdown(shutdownCtx)
```

### 6.6 shutdown 错误聚合

main:304-313 分别处理 `gs.Shutdown` / `harness.DisposeAll` / `sqliteStore.Close` 的错误,各自 log。`Runtime.Shutdown` 聚合返回第一个非 nil 错误,但每步独立尝试(不短路):

```go
func (r *Runtime) Shutdown(ctx context.Context) error {
    var errs []error
    if r.Server != nil {
        if err := r.Server.Shutdown(ctx); err != nil {
            errs = append(errs, fmt.Errorf("server: %w", err))
        }
    }
    if err := harness.DisposeAll(ctx); err != nil {
        errs = append(errs, fmt.Errorf("harness dispose: %w", err))
    }
    if r.Stores.Sessions != nil {
        if err := r.Stores.Sessions.Close(); err != nil {
            errs = append(errs, fmt.Errorf("sqlite close: %w", err))
        }
    }
    if len(errs) == 0 { return nil }
    return errors.Join(errs...)
}
```

`runtime.Run` 收到 error 后只 warn,不改变退出码(refactor 阶段行为不变)。

### 6.6 注释合规约束(继承自 AGENTS.md §注释规范)

本 spec 新增的所有 `.go` 业务源代码(`internal/runtime/*.go`、以及 §5.2 / §5.3 修改涉及的文件)必须严格遵守 `AGENTS.md` 的注释规则。具体落地清单:

#### 绝对禁止出现的注释(出现即违规,review 拒绝)

| 类别 | 反例(禁止) |
|---|---|
| 阶段/版本/迭代规划类 | `// v0 占位`、`// 后续迭代替换`、`// 未来会接入 X 协议`、`// 本 spec 范围内的临时实现` |
| 代码复述型废话 | 已经在写 `if cfg == nil { return nil, errors.New("cfg is nil") }`,不允许加 `// 如果 cfg 是 nil 则报错` |
| 模型思考 / 改动说明 | `// 按 spec 重构`、`// 适配 §6.2 改动`、`// 合并自原 main:165-181` |
| 展望 / TODO 规划 | `// TODO: 后续把 plugins 也走 Options`(本 spec 已经定下来就这样,不是 TODO) |
| 无关铺垫 / 收尾 | `// 下面实现 runtime 装配`、`// 以上完成 Runtime 构造` |
| 冗余分隔线 | `// --------------------------`、`// ===== Section =====` |
| 标识符塞版本号 | `ErrNotWiredInV0`、`FixForRefactor`、`MockS5`、`SteerPlaceholderAgent` |

#### 仅允许保留的注释(按需少量,能不写就不写)

| 场景 | 示例 |
|---|---|
| 导出公共函数 / 类型 / struct 的 JSDoc | `// Build is the single assembly entry of the agent runtime. It loads config and returns a ready-to-drive Runtime.` |
| 非常规特殊逻辑的单行意图注释 | `// Loop.admit 在 jumpQueue=true 时主动取消 in-flight run,让 steer 信号立刻接上` |
| 硬性架构约束校验 | `// messageID 必须在 emit 时实时取自 Loop,不能缓存,否则 prompt 切换后会读到旧 id` |
| 关键边界兜底逻辑 | `// platform-specific 兜底:Linux 走 syscall.SIGINT,Windows 走 ctrl handler` |

#### 格式要求

- 单行 `//` 注释,**上方一行**(不写行内)
- JSDoc 精简,无 `@example` / `@author` / `@since` 等非必要标签
- 文件头不写"本文件做什么"的解释块——文件 doc comment 只描述**该 package 对外暴露的契约**,不重述代码

#### 自查命令

每个阶段落地后,实施者跑:

```bash
# 1. 禁词扫描(应无输出)
grep -nE "v0 占位|后续迭代|未来会|TODO:|FIXME|// 按 |// 适配 |// 合并 |MockS|Placeholder|S5 阶段|V1 重构" src/darvin-agent/internal/runtime/

# 2. 分隔线扫描(应无输出)
grep -nE "^\s*//\s*[-=]{4,}" src/darvin-agent/internal/runtime/

# 3. 复述型扫描:每条 `// xxx` 注释前对照代码,人工检查是否只是把代码翻译成中文
```

#### 与既有 main.go 的关系

**不在本 spec 范围内**:旧 main.go 现有注释违反 AGENTS.md 规则的部分(阶段标记 / 复述注释 / 收尾总结等),按 §5.2 改造后**整文件被替换**(< 30 行),违规注释天然消失。**不要单独清理其它现有文件的注释**,那属于独立清理项,违反"修 bug 不做机会性重构"原则(AGENTS.md §实践指导)。

## 7. 回滚方案

### 7.1 单文件回滚

每个新文件独立,删掉即可,不动旧 main:
- 阶段 1(新增 runtime/* 9 个文件):直接 `rm -rf src/darvin-agent/internal/runtime/`,旧 main 不受影响
- 阶段 2(删 main 死代码):git revert 即可
- 阶段 3(main 切换到 runtime.Run):revert main.go,恢复旧 main

### 7.2 完整回滚

```bash
git revert <commit-hash-of-each-stage>
```

阶段独立 commit,任何阶段失败都只影响该 commit 的范围。

### 7.3 风险点

- `agentloop.AgentFactory.Selector` 字段如果新增 `HarnessSelector` 类型需要确保兼容现有调用方(grep `agentloop.AgentFactory{` 看调用点)
- 阶段 3 完成后旧 main 已经被覆盖,如果发现行为漂移,只能 revert 阶段 3 commit

## 8. 验证计划

### 8.1 编译 / lint / 单测

每个阶段完成后必须通过:

```bash
cd src/darvin-agent

# 1. 编译
go build ./...

# 2. vet
go vet ./...

# 3. 单测
go test ./...

# 4. 整体 vet 兜底
go vet -all ./...
```

### 8.2 行为不变性 — 关键场景

| 场景 | 验证方法 |
|---|---|
| 启动 | `npm start` 拉起 Electron + Go agent;DevTools 看 console 无 error;`info` 日志包含 "application started successfully" |
| 新 session prompt | WebSocket 发 prompt,收到 StartEvent → TextDeltaEvent → DoneEvent,event bus 推送完整 |
| 工具调用 | prompt 让 agent 调 `read_file`,ToolStartEvent + ToolEndEvent 顺序正确,permission modal 弹窗正常 |
| MCP 连接 | 启动后 5s 内收到 `mcp.connection_changed` 通知 `status=connected` |
| Skill | 加载用户 skill,tool 列表包含 `skill__<id>`,运行时 skill 工具可用 |
| Active session 恢复 | 重启 agent,FR-9 流程触发,active session bootstrap 成功 |
| Shutdown | SIGTERM 发送后,3s 内进程退出,日志 "graceful shutdown complete" |
| 配置加载 | `DARVIN_CONFIG=/path/to/test.yaml darvin-agent` 启动,cfg 来自该文件 |
| Workspace 覆盖 | `DARVIN_AGENT_WORKSPACE=/path/to/ws` 启动,日志中 `effective` 等于该值 |
| Steer | endpoint 已删除;renderer 不再可发 `agent.steer`;若 renderer 旧代码还在发,WS 会返 `Method not found`,不影响其他 RPC |

### 8.3 行数回归

```bash
wc -l src/darvin-agent/cmd/app/main.go
# 期望: < 30
```

### 8.4 grep 验证死代码已删

```bash
grep -n "steerAgent\|harness.MustRegister\|factory.Plugins =" src/darvin-agent/cmd/app/main.go
# 期望: 无输出
```

### 8.5 测试场景不变化的对照

如果项目里有 e2e / smoke 测试:

```bash
bash scripts/smoke.sh
```

期望与重构前输出一致(任何 agent 行为相关输出不变)。

## 9. 验收标准 Checklist

### 9.1 main 精简

- [ ] `cmd/app/main.go` 行数 < 30,只 `os.Exit(runApp(os.Args[1:]))`
- [ ] `internal/runtime/runtime.go` 存在,`Build(ctx, opts) (*Runtime, error)` 和 `Run(args) int` 签名匹配本 spec
- [ ] `*Runtime` 是 frontend 唯一依赖面;main / cmd/app 不再 import `internal/llm` / `internal/skills` / `internal/mcp` / `internal/tools` / `internal/gateway` / `internal/agentloop` / `internal/agents`(除 main 留 runtime 一个)
- [ ] `newEmbeddedHarness` 是命名函数,不在 main / factory 匿名闭包里
- [ ] `defaultHarnessSelector` 注入 factory.Selector;不再有 fallback 隐式行为
- [ ] `newAgentConfig` 函数存在,main 不再持有 `agentCfg := agent.Config{...}` 字面量
- [ ] factory.Plugins 在 `newAgentFactory` 构造时一次性传入,不在 main 事后赋值
- [ ] mcp notifier 绑定从 main 搬到 runtime.Build 内(接口形态不变)

### 9.2 Steer / FollowUp 接线路径修复(§6.2)

#### 删除的孤儿 plumbing

- [ ] `src/darvin-agent/internal/agentloop/steer.go` 文件已删除
- [ ] `src/darvin-agent/internal/agentloop/queue.go` 文件已删除
- [ ] `cmd/app/main.go` 中 `steerAgent` 构造已删除
- [ ] `cmd/app/main.go` 中 `harness.MustRegister(harness.NewEmbedded(...), "")` fallback 已删除
- [ ] `cmd/app/main.go` 中 `agentloop.NewSteerControl(...)` 已删除
- [ ] `internal/gateway/handlers.go` 中 `Handler.Steer` 字段已删除
- [ ] `internal/gateway/handlers.go` 中 `NewHandler` 第 6 参 `steer` 已删除
- [ ] `internal/agents/dispatcher.go` 中 `Agent.Steer` 已删除
- [ ] `internal/agents/dispatcher.go` 中 `Agent.FollowUp` 已删除
- [ ] `internal/agents/event/event.go` 中 `ModeFollowUp` 常量已删除(只保留 `ModePrompt` + `ModeSteer`)
- [ ] `internal/agents/queue/queue.go` 中 `ModeFollowUp` 常量已删除;`Enqueue` / `Dequeue` 中 `ModeFollowUp` 相关 case 分支已删除
- [ ] grep `SteerControl\|steerControl\|steerAgent\|NewSteerControl\|Agent\.Steer\b\|Agent\.FollowUp\b` 全仓库(排除 .git / specs 历史文档) **无 src 代码命中**

#### 保留 + 改对的正路

- [ ] `internal/agentloop/loop.go` 中 `Loop.Steer` 方法**保留**
- [ ] `internal/agentloop/loop.go` 中 `steerQueue` 字段**保留**;`popLocked` steer 优先分支**保留**
- [ ] `internal/agentloop/loop.go` 中 `Loop.admit(req, skill, jumpQueue bool)` 的 `jumpQueue` 参数**保留**
- [ ] `internal/agents/queue/queue.go` 中 `ModeSteer` 常量**保留**(`Loop.admit` 内部用)
- [ ] `internal/agents/event/event.go` 中 `ModeSteer` 常量**保留**
- [ ] `internal/gateway/handlers.go` 中 `case "agent.steer"` **保留**并重写
- [ ] `internal/gateway/handlers.go` 中 `handleSteer` 函数**保留**并重写,逻辑:取 `p.SessionID` → `h.Sessions.EnsureEntry(p.SessionID)` → 校验 `entry.AgentLoop != nil` → 调 `entry.AgentLoop.Loop.Steer(PromptRequest{RunID, Content})`
- [ ] `internal/gateway/handlers.go` 中 `SteerParams` 类型**保留**,字段扩展:`SessionID string` (必填)、`RunID string` (可选)、`Content string` (必填)
- [ ] `internal/gateway/handlers.go` 中 `SteerResult` 类型**保留**,字段扩展:`Steered bool`、`RunID string`、`MessageID string`、`Queued bool`
- [ ] `grep -n "\.Loop\.Steer(" src/darvin-agent/` 应当**唯一**命中 `gateway/handlers.go` 的 `handleSteer` 函数

### 9.3 编译 / 测试

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `go test ./...` 通过(失败的测试如果引用被删符号,按 §5.4 处理)
- [ ] 集成启动验证 §8.2 列出的 10 个场景全部通过(其中 Steer 场景:不发 `agent.steer` 时一切正常;**新增** §9.3.1 的 steer 正向测试)

#### 9.3.1 新增 steer 正向集成测试

- [ ] renderer 发 `agent.steer` {session_id, content:"换个思路"} → 收到 `{steered: true, runID, messageID, queued}`
- [ ] 若对应 session 正在跑 turn X → turn X 在 ≤ 1 帧内被取消(后续 `TurnEndEvent.StopReason=FinishReasonAborted`)
- [ ] 新 content 作为下一个 turn 立即跑(产生 `TurnStartEvent` + `TextDeltaEvent` + `TurnEndEvent`)
- [ ] 若对应 session 是 idle / 未建 AgentLoop → 返 `CodeNotFound`,renderer 应当先发 prompt
- [ ] 缺 `session_id` → 返 `CodeInvalidParams`

### 9.4 行数 / 死代码 grep

- [ ] 行数 `wc -l cmd/app/main.go` < 30
- [ ] `grep -E "steerAgent|harness\.MustRegister|SteerControl\b|Agent\.Steer\b|Agent\.FollowUp\b|ModeFollowUp" src/darvin-agent/` **无源代码命中**(允许 specs/ 历史 spec 提及)
- [ ] `grep -E "\bLoop\.Steer\b" src/darvin-agent/` 命中且**唯一**位于 `gateway/handlers.go`(`gateway/handlers.go` 是新的正路调用方;`agentloop/loop.go` 是定义本身,允许)

### 9.5 注释合规(§6.6)

- [ ] `grep -nE "v0 占位|后续迭代|未来会|TODO:|FIXME|// 按 |// 适配 |// 合并 |MockS|Placeholder|S5 阶段|V1 重构" src/darvin-agent/internal/runtime/` 无输出
- [ ] `grep -nE "^\s*//\s*[-=]{4,}" src/darvin-agent/internal/runtime/` 无输出(无分隔线注释)
- [ ] `internal/runtime/*.go` 每个文件的 package doc comment 只描述对外契约,不重述代码做什么
- [ ] `internal/runtime/*.go` 公共类型(`Runtime` / `Options` / `Stores`)和公共函数(`Build` / `Run` / `Shutdown`)有简短 JSDoc,无 `@example` 等冗余标签
- [ ] §5.2 修改文件中,所有新增 / 重写的注释遵循 §6.6 规则(尤其是 §6.2 重写 `handleSteer` 时,只解释"为什么 session 为空时返 NotFound",不复述代码)
- [ ] 标识符命名无 `MockS5` / `FixForRefactor` / `SteerPlaceholderAgent` / `V0` 等版本号或阶段号