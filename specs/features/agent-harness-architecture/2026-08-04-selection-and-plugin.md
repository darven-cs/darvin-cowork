# 03 — Selection + Runtime Plugin

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 前置: `01-harness-core-interface.md`
> 输出: `internal/harness/selection.go` (~450) + `internal/harness/plugin/` 子包 (~180) + 测试

## 1. 目标

引入 5 维 harness 自动选择机制(平移 OpenClaw selection.ts 847 行) + 动态 plugin 加载机制(平移 OpenClaw runtime-plugin.ts 310 行)。让 darvin-cowork 在以下场景自动选 harness:

1. 显式指定 (`config.agents.defaults.harness = "cli"`) → 选 cli
2. 隐式 fallback (`requestedRuntime = "codex"`,但无 codex harness) → 退回 embedded
3. Auto 模式 (没指定) → 按 5 维评分选最优

## 2. 5 维评分模型

### 2.1 输入:SupportContext

```go
// internal/harness/types.go (扩展)
package harness

// SupportContext 是 Selection 评分时给 harness 的输入。
type SupportContext struct {
    Provider           string                 // "anthropic" / "openai" / ...
    ModelID            string                 // "claude-sonnet-4-5" / ...
    ModelProvider      *ModelProviderFacts    // api / baseUrl / auth / transport / runtimePolicy
    RequestedRuntime   string                 // "auto" / "embedded" / "codex" / "claude-acp" / ...
    Config             *config.OpenClawConfig
    AgentID            string
    SessionKey         string
    ProviderOwnership  *ProviderOwnership     // "unowned" / "owned" / "ambiguous" + plugin ids
    PreparedModelProvider bool
}

// ModelProviderFacts 来自 config 的 provider/model 解析结果
type ModelProviderFacts struct {
    API                     string  // "openai" / "anthropic" / "openai-responses" / ...
    BaseURL                 string
    AzureAPIVersion         string
    Request                 any     // 来自 config.models.{provider}.request
    PreparedAuth            *PreparedAuthSupport
    RequestTransportOverrides RequestTransportOverrides  // "none" / "present"
    RuntimePolicy           *RuntimePolicy
}

// ProviderOwnership 描述某个 provider 的 plugin owner 关系
type ProviderOwnership struct {
    Status   string   // "unowned" | "owned" | "ambiguous"
    PluginIDs []string
}
```

### 2.2 输出:SupportResult

```go
// SupportResult 是 harness.Supports(ctx) 的返回值
type SupportResult struct {
    Supported bool
    Reason    string   // 不支持时填写
    Priority  int      // 支持时填写;数值越大越优先
}
```

### 2.3 5 维评分权重(Selection 排序)

| 维度 | 含义 | 来源 |
|---|---|---|
| **1. requestedRuntime 匹配** | 如果 `requestedRuntime = "codex"` 且 harness.id = "codex",直接胜出 | `SupportContext.RequestedRuntime` |
| **2. provider 静态白名单** | harness.AutoSelection().ProviderIDs 包含当前 provider | `Harness.AutoSelection()` |
| **3. supports() 评分** | harness.Supports(ctx) 主动评估 | `Harness.Supports(ctx)` |
| **4. context engine host capabilities** | ctx engine 要求的 capability,harness 必须声明 | `Harness.Capabilities().ContextEngineHost` |
| **5. deliveryDefaults** | "automatic" / "message_tool" 模式匹配 | `Harness.Capabilities().DeliveryDefaults` |

**排序规则**(OpenClaw `compareHarnessSupport`):

```go
// 数值越大越优先;同分按 harness.id 字典序(稳定)
func compareSupport(a, b ScoredHarness) int {
    if a.Priority != b.Priority { return b.Priority - a.Priority }
    return strings.Compare(a.Harness.ID(), b.Harness.ID())
}
```

## 3. Selection 完整算法

### 3.1 输入:SelectionParams

```go
// internal/harness/selection.go
package harness

type SelectionParams struct {
    Provider    string
    ModelID     string
    AgentID     string
    SessionKey  string
    Config      *config.OpenClawConfig

    // 显式指定(最高优先级)
    ExplicitHarnessID string

    // 来自 config.agents.defaults.harness
    DefaultHarnessID string
}

type Decision struct {
    Harness         Harness
    SelectedID      string
    SelectedReason  SelectionReason
    Candidates      []Candidate
}

type SelectionReason string
const (
    ReasonForcedOpenClaw         SelectionReason = "forced_openclaw"
    ReasonForcedPlugin           SelectionReason = "forced_plugin"
    ReasonImplicitPluginUnavailable SelectionReason = "implicit_plugin_unavailable_openclaw"
    ReasonImplicitPluginUnsupported SelectionReason = "implicit_plugin_unsupported_openclaw"
    ReasonCliRuntimePassthrough  SelectionReason = "cli_runtime_passthrough_openclaw"
    ReasonAutoPlugin             SelectionReason = "auto_plugin"
    ReasonAutoOpenClaw           SelectionReason = "auto_openclaw"
)

type Candidate struct {
    ID       string
    Label    string
    PluginID string
    Supported bool
    Priority  int
    Reason    string
}
```

### 3.2 决策树

```go
func SelectHarness(params SelectionParams) (*Decision, error) {
    candidates := collectCandidates()  // 遍历 Registry
    ctx := buildSupportContext(params)

    // 1. 显式指定(最高优先级)
    if params.ExplicitHarnessID != "" {
        h := GetHarness(params.ExplicitHarnessID)
        if h == nil {
            return nil, fmt.Errorf("harness: explicit %q not registered", params.ExplicitHarnessID)
        }
        return &Decision{Harness: h, SelectedID: h.ID(), SelectedReason: ReasonForcedPlugin, ...}, nil
    }

    // 2. policy / 默认
    policy := resolveHarnessPolicy(params)  // 查 config

    // 3. implicit codex 但无 codex harness → 退回 embedded
    if policy.Runtime == "codex" && policy.RuntimeSource == "implicit" {
        codex := GetHarness("codex")
        if codex == nil {
            embedded := GetHarness("embedded")
            return &Decision{Harness: embedded, SelectedReason: ReasonImplicitPluginUnavailable, ...}, nil
        }
        // 调 codex.Supports 进一步判断
        if !codex.Supports(ctx).Supported {
            embedded := GetHarness("embedded")
            return &Decision{Harness: embedded, SelectedReason: ReasonImplicitPluginUnsupported, ...}, nil
        }
    }

    // 4. Auto 模式评分
    scored := scoreAllCandidates(ctx, candidates)
    sorted := sortByPriorityAndID(scored)
    if len(sorted) == 0 {
        // 没有 supported 的 → fallback embedded
        embedded := GetHarness("embedded")
        return &Decision{Harness: embedded, SelectedReason: ReasonAutoOpenClaw, ...}, nil
    }
    top := sorted[0]
    if top.Harness.ID() == "embedded" {
        return &Decision{Harness: top.Harness, SelectedReason: ReasonAutoOpenClaw, ...}, nil
    }
    return &Decision{Harness: top.Harness, SelectedReason: ReasonAutoPlugin, ...}, nil
}
```

### 3.3 scoreAllCandidates 实现

```go
type ScoredHarness struct {
    Harness Harness
    Support SupportResult
    Score   int    // 综合得分
}

func scoreAllCandidates(ctx SupportContext, candidates []Harness) []ScoredHarness {
    out := make([]ScoredHarness, 0, len(candidates))
    for _, h := range candidates {
        s := h.Supports(ctx)
        if !s.Supported { continue }
        score := s.Priority

        // 加分维度
        if matchesAutoSelection(h, ctx)   { score += 100 }  // provider 白名单
        if matchesRequestedRuntime(h, ctx) { score += 1000 } // 强匹配

        out = append(out, ScoredHarness{Harness: h, Support: s, Score: score})
    }
    return out
}
```

## 4. Support 详细逻辑

### 4.1 `buildSupportContext`

```go
// internal/harness/support.go
package harness

// BuildSupportContext 把 SelectionParams 展开成完整的 SupportContext。
// 关键是:从 config 读 provider 解析 → ModelProviderFacts,从 harness
// Registry 收集 ProviderOwnership。
func BuildSupportContext(params SelectionParams) SupportContext {
    cfg := params.Config

    // 1. provider config
    providerConfig := resolveMergedProviderConfig(cfg, params.Provider)
    modelConfig := resolveModelConfig(cfg, params.Provider, params.ModelID)
    modelProvider := buildModelProviderFacts(providerConfig, modelConfig)

    // 2. request transport override
    hasOverride := resolveRequestTransportOverride(cfg, params.Provider, params.ModelID)
    if hasOverride { modelProvider.RequestTransportOverrides = "present" }

    // 3. ownership: 哪个 plugin 拥有这个 provider
    ownership := resolveProviderOwnership(cfg, params.Provider)

    return SupportContext{
        Provider: params.Provider,
        ModelID: params.ModelID,
        ModelProvider: modelProvider,
        RequestedRuntime: resolveRequestedRuntime(cfg, params.AgentID),
        Config: cfg,
        AgentID: params.AgentID,
        SessionKey: params.SessionKey,
        ProviderOwnership: ownership,
        PreparedModelProvider: params.ExplicitHarnessID != "",
    }
}
```

### 4.2 `resolveProviderOwnership`

```go
// 查 config.plugins 列表,看哪个 plugin 声明拥有这个 provider。
// darvin-cowork 当前没有 plugin 系统(Phase 3 才加),Phase 3 之前
// 一律返回 unowned。
func resolveProviderOwnership(cfg *config.OpenClawConfig, provider string) *ProviderOwnership {
    // Phase 3 实现:
    // for plugin in cfg.Plugins {
    //     if plugin.HasProvider(provider) { ... }
    // }
    return &ProviderOwnership{Status: "unowned"}
}
```

## 5. Runtime Plugin 动态加载

### 5.1 Plugin 概念

```go
// internal/harness/plugin/plugin.go
package plugin

// Plugin 是 runtime 可动态加载的扩展单位,跟 harness factory 一一对应。
// 一个 plugin 可以注册 0..N 个 Harness(目前只支持 1)。
type Plugin struct {
    ID          string                       // "anthropic-extra" / "cli-backend" / ...
    Version     string                       // "1.0.0"
    HarnessFactory func() (harness.Harness, error)   // 构造 Harness 实例
    Hooks       *Hooks                       // 生命周期钩子(可选)
    Config      PluginConfig                 // 解析自 yaml
}

type Hooks struct {
    OnLoad    func(ctx context.Context) error
    OnUnload  func(ctx context.Context) error
}

type PluginConfig struct {
    Enabled bool
    Path    string    // 未来:动态加载路径(so / dll);当前:static
    Settings map[string]any
}

// Manager 进程级单例,管理所有加载的 plugin
type Manager struct {
    mu     sync.RWMutex
    loaded map[string]*loadedPlugin
}

type loadedPlugin struct {
    Plugin  *Plugin
    Harness harness.Harness
    Hooks   *Hooks
    LoadedAt time.Time
}
```

### 5.2 Manager API

```go
// internal/harness/plugin/manager.go
package plugin

var defaultManager = newManager()

// LoadPlugin 注册并启动一个 plugin。
// 通常在 main.go 启动时遍历 config.plugins 调,或在 hot-reload 时调。
func LoadPlugin(p *Plugin) error {
    return defaultManager.load(p)
}

// UnloadPlugin 卸载 + 调 OnUnload + 从 harness registry 反注册
func UnloadPlugin(id string) error { ... }

// ListLoaded 返回当前所有已加载的 plugin
func ListLoaded() []*Plugin { ... }

// Get 通过 plugin id 拿(主要给 selection / status 用)
func Get(id string) (*Plugin, bool) { ... }
```

### 5.3 加载流程

```
LoadPlugin(p)
  ↓
  1. validate p (id / version / factory)
  2. run p.Hooks.OnLoad(ctx)  (若提供)
  3. h, err := p.HarnessFactory()
  4. harness.RegisterHarness(h, p.ID)
  5. stored := &loadedPlugin{Plugin: p, Harness: h, Hooks: p.Hooks, ...}
  6. defaultManager.mu.Lock(); defaultManager.loaded[p.ID] = stored
  7. emit event.PluginLoadedEvent{PluginID, Version, HarnessID}

UnloadPlugin(id)
  ↓
  1. 从 defaultManager.loaded 删除
  2. harness.UnregisterHarness(h.ID())
  3. run p.Hooks.OnUnload(ctx)
  4. emit event.PluginUnloadedEvent{PluginID}
```

### 5.4 配置集成(由 main.go 在启动时读 yaml 调)

```yaml
# config.yaml (示意)
plugins:
  - id: "builtin-extra"
    enabled: true
    harness:
      id: "embedded-extra"
      # 暂不通过 yaml 构造 plugin;主进程注册
  - id: "cli-codex"
    enabled: false   # 未来启用
    path: "./plugins/cli-codex.so"
```

## 6. 与 OpenClaw 的差异

| OpenClaw | darvin-cowork | 原因 |
|---|---|---|
| `selection.ts` 847 行 | ~450 行 | 砍掉 OpenClaw 的"subagent runtime" / "session fork" / "provider ownership full lookup" / "config agentId 提取"(darvin-cowork 暂没这些) |
| `runtime-plugin.ts` 310 行 | ~180 行 | 砍掉 dynamic .so load(Go 暂不用),只留 static factory + lifecycle hook |
| 5 维评分全支持 | **全支持** | 完全平移 |
| `compareHarnessSupport` 排序算法 | 同 | 平移 |
| `autoSelection` provider 白名单 | 同 | 平移 |

## 7. 测试要求

### 7.1 selection_test.go

| Test | 覆盖 |
|---|---|
| `TestExplicitHarness` | params.ExplicitHarnessID 不为空 → 选它 |
| `TestExplicitHarnessNotRegistered` | 显式指定但没注册 → error |
| `TestImplicitCodexFallback` | policy.Runtime="codex" 但无 codex → 选 embedded, Reason=ImplicitPluginUnavailable |
| `TestImplicitCodexUnsupported` | codex.Supports=false → 选 embedded, Reason=ImplicitPluginUnsupported |
| `TestAutoSelectByPriority` | 3 个 harness 不同 priority → 选 priority 最高 |
| `TestAutoSelectByProviderWhitelist` | harness.AutoSelection() 命中 → 加分 |
| `TestAutoFallbackToEmbedded` | 没有 supported → 选 embedded, Reason=AutoOpenClaw |
| `TestStableSortByID` | 同 priority 按字典序 |
| `TestEmptyRegistry` | 没有任何 harness → 选 embedded |
| `TestPreparedModelProviderFlag` | ExplicitHarness 触发 PreparedModelProvider=true |

### 7.2 plugin_test.go

| Test | 覆盖 |
|---|---|
| `TestLoadPlugin` | 注册 + 进 Registry + emit event |
| `TestUnloadPlugin` | 移除 + 反注册 + 调 OnUnload |
| `TestLoadPluginFactoryError` | factory 返回 err → 不进 Registry |
| `TestLoadPluginDuplicateID` | 重复 ID → 覆盖(幂等) |
| `TestOnLoadFailure` | OnLoad 返回 err → 整体 Load 失败 |
| `TestListLoaded` | 顺序稳定 |

总测试数 ≥ 16。

## 8. Phase 3 提交清单

```bash
$ git add internal/harness/selection.go internal/harness/support.go internal/harness/policy.go internal/harness/plugin/
$ go test -count=1 -short ./internal/harness/...   # 全 PASS
$ git commit -m "feat(harness): add Selection (5-dim scoring) + Runtime Plugin

平移 OpenClaw src/agents/harness/selection.ts + runtime-plugin.ts:

Selection 评分:
  - requestedRuntime 匹配 (强匹配 +1000)
  - provider 静态白名单 (+100)
  - supports() 评分 (harness 自报)
  - context engine host capability
  - deliveryDefaults

Runtime Plugin:
  - Plugin struct (ID / Version / HarnessFactory / Hooks)
  - Manager 进程级单例
  - Load/Unload + emit event
  - 配置集成(由 main.go 读 yaml 调)

不动现有代码:agent / acp / gateway 保持原样

Spec: specs/features/agent-harness-architecture/03-selection-and-plugin.md"
```

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Selection 评分太复杂,出现 tie-break 抖动 | 中 | 中 | compareHarnessSupport 强制按 id 字典序(稳定),不允许 runtime 随机 |
| Plugin 动态加载破坏 main.go 启动时间 | 低 | 中 | Phase 3 不实现 .so load,只 static factory;启动 0 额外开销 |
| Provider ownership 查 plugin 元数据涉及 IO | 中 | 低 | Phase 3 实现:只查 in-memory config,不查磁盘 |
| Test 不覆盖 Negative case(没注册 / priority 都 0) | 中 | 中 | selection_test.go 强制覆盖 negative |
| `BuildSupportContext` 在 cfg=nil 时崩溃 | 低 | 中 | 加 cfg nil check,缺省值 |
