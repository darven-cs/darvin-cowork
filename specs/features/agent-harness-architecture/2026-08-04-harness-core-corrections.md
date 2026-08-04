# 07 — Harness 核心修正

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `2026-08-04-harness-architecture-design.md`
> 前置: `2026-08-04-harness-core-interface.md`(已实现)
> 阻塞: spec 03(Selection)、spec 06(ctx engine)
> 输出: `internal/harness/` 既有 6 个文件的修正 + 新增测试

## 1. 由来

spec 01 落地后,对照 OpenClaw `src/agents/harness/`(types.ts / registry.ts / support.ts / auto-selection.ts / result-classification.ts / lifecycle.ts)与 `src/context-engine/host-compat.ts` 做了一次逐行复核。骨架是忠实的,但**选择与能力匹配的语义有若干处与 OpenClaw 相反**:OpenClaw 在这些点上是 fail-closed,当前实现是 fail-open。

其中 C1 是可复现的排序 bug,C2 / C4 是语义反转,C5 是安全闸门缺失。这四项会被 spec 03 的 Selection 和 spec 06 的 ctx engine 直接继承并放大,**必须在 Phase 3 之前收掉**。

`internal/harness/` 目前没有任何 import 方(Phase 1 本来就是纯新增),所有修改零调用方影响,改动成本此刻最低。

## 2. 修正清单

| # | 问题 | 优先级 | 文件 |
|---|---|---|---|
| C1 | AutoSelection 优先级被重复计入排序 | P0 | `support.go` `types.go` `builtin_embedded.go` |
| C2 | `AutoSelectionHint` 空列表语义与 OpenClaw 相反 | P0 | `types.go` |
| C3 | provider id 裸字符串比较,未归一 | P0 | `types.go` |
| C4 | `ContextEngineHost` 建模轴错误 + 缺省 fail-open | P0 | `types.go` |
| C5 | lifecycle 未做 ctx engine host 断言 | P0 | `lifecycle.go` |
| C6 | 陈旧 classification 不被清除 | P1 | `lifecycle.go` |
| C7 | `AttemptResult` 无 harness 归属 | P1 | `types.go` `lifecycle.go` |
| C8 | Registry 无 reset / dispose 扇出 | P1 | `registry.go` |
| C9 | `SettledTurnResult` 丢失真实产物 | P1 | `types.go` |
| C10 | 诊断事件层整体缺失 | P1 | `lifecycle.go` |
| C11 | `Superseded` 注释与行为不符 | P2 | `types.go` `lifecycle.go` |
| C12 | `Register` 未归一 PluginID | P2 | `registry.go` |
| C13 | `Capabilities` 缺 `DeliveryDefaults` | P2 | `types.go` |

---

## 3. P0 修正

### C1 — AutoSelection 优先级重复计入

**现状**:`embedded.Supports` 返回 `Priority: cfg.Priority`,`Rank` 又把 `hint.Priority`(同一个值)当 bonus 加一遍:

```go
// support.go:88-102 现状
bonus := 0
if sel, ok := h.(AutoSelector); ok {
    hint := sel.AutoSelection()
    if !hint.Matches(sc.Provider) { continue }
    if hint != nil { bonus = hint.Priority }
}
res := h.Supports(sc)
if !res.Supported { continue }
res.Priority += bonus     // ← embedded 的 priority 被算两次
```

实测(声明 priority:a=10 / b=6):

```
id=b effectivePriority=12
id=a effectivePriority=10
winner=b        ← 声明 6 的赢了声明 10 的
```

**OpenClaw**:`autoSelection` 只有 `{ providerIds }`,**没有 priority 字段**。优先级唯一来源是 `supports()` 的返回值,`compareHarnessSupport` 直接读 `support.priority`,不存在任何叠加。

**修改**:删掉 `AutoSelectionHint.Priority`,`Rank` 不再叠加。

```go
// types.go
type AutoSelectionHint struct {
    // Providers restricts the harness to these provider ids. A nil slice
    // leaves eligibility to Supports; an empty slice marks the harness
    // explicit-only.
    Providers []string
}
```

```go
// support.go — Rank 中 AutoSelection 只做过滤
if sel, ok := h.(AutoSelector); ok {
    if !sel.AutoSelection().Eligible(sc.Provider) { continue }
}
res := h.Supports(sc)
if !res.Supported { continue }
out = append(out, Candidate{Harness: h, Result: res})
```

`EmbeddedConfig.Priority` 保留,但只经由 `Supports` 生效一次。

`registry.go:autoPriority` 随之失效 —— `List()` 改为纯按 id 升序排,把优先级排序完全交给 `Rank`。`Get("")` 语义相应收紧为「id 最小的健康 harness」,并在注释里写明它只是诊断/兜底入口,真实选择必须走 `Rank` / `Policy.Resolve`。

### C2 — `AutoSelectionHint` 空列表语义反转

**现状**:

```go
// types.go:214 现状
func (h *AutoSelectionHint) Matches(provider string) bool {
    if h == nil || len(h.Providers) == 0 { return true }   // 空 = 匹配所有
    return contains(h.Providers, provider)
}
```

**OpenClaw**(`auto-selection.ts`)三态语义明确:

| `providerIds` | 含义 | 行为 |
|---|---|---|
| 省略(undefined) | 保留动态探测 | 返回 undefined → 继续问 `supports()` |
| 非空且命中 | 白名单命中 | 返回 undefined → 继续问 `supports()` |
| 非空未命中 | 不可自动选择 | 硬拒绝,`reason: "provider is not auto-selectable"` |
| **空列表 `[]`** | **explicit-only harness** | **硬拒绝**,`reason: "harness is explicit-only"` |

当前实现把空列表当成「匹配所有」,与 OpenClaw 完全相反,实测 explicit-only harness 会被自动选中。后果是「只能显式点名」这个语义在 Go 侧无法表达,且想表达的人得到相反行为。

**修改**:`Matches` 改名 `Eligible`,区分 nil 与空切片:

```go
// Eligible reports whether the hint admits provider for auto selection.
// A nil hint (or a nil Providers slice) leaves the decision to Supports.
// A non-nil but empty Providers slice marks the harness explicit-only: it
// is never auto-selected and must be named through RequestedHarnessID.
func (h *AutoSelectionHint) Eligible(provider string) bool {
    if h == nil || h.Providers == nil { return true }
    if len(h.Providers) == 0 { return false }
    return containsProvider(h.Providers, provider)
}
```

`builtin_embedded.go:AutoSelection()` 同步调整:`len(cfg.Providers) == 0` 时返回 `nil`(动态探测),而不是构造一个 `Providers` 为空切片的 hint —— 否则 embedded 自己会变成 explicit-only。

### C3 — provider id 归一化

**现状**:`contains(h.Providers, provider)` 精确比较。实测 provider `"anthropic"` 匹配不上白名单 `["Anthropic"]`。

**OpenClaw**:`auto-selection.ts` 两侧都过 `normalizeProviderId`。

**修改**:新增包内 helper,白名单与入参都归一后比较:

```go
// normalizeProviderID lowercases and trims a provider id so an allowlist
// entry and a request that differ only in case still match.
func normalizeProviderID(id string) string {
    return strings.ToLower(strings.TrimSpace(id))
}

func containsProvider(list []string, want string) bool {
    want = normalizeProviderID(want)
    for _, v := range list {
        if normalizeProviderID(v) == want { return true }
    }
    return false
}
```

仓库暂无 provider alias 表(OpenClaw 的 `normalizeProviderId` 还处理别名),先只做大小写/空白归一;alias 等 provider spec 落地再接。

### C4 — `ContextEngineHost` 建模轴错误

**现状**:`ContextEngineHost []string` 被当作 **context engine id 白名单**,`HostsContextEngine(id)` 在列表为空时返回 `true`(托管任意 engine)。

**OpenClaw**(`context-engine/host-compat.ts`)是**能力动词集合 + 按 operation 的超集检查**:

```
host.capabilities:  bootstrap | assemble-before-prompt | after-turn |
                    maintain | compact | runtime-llm-complete |
                    thread-bootstrap-projection
engine.info.hostRequirements[operation].requiredCapabilities → 必须被 host 覆盖
missing = required \ supported;  missing 非空 → 抛错
```

不同 host 的能力集**真的不一样**,这正是要区分的东西:

| host | 能力 |
|---|---|
| `openclaw-embedded` | bootstrap, assemble-before-prompt, after-turn, maintain, compact, runtime-llm-complete |
| `codex-app-server` | 以上 + thread-bootstrap-projection |
| generic CLI | **仅** bootstrap, after-turn, maintain(无 assemble-before-prompt / 无 compact) |

缺省方向也相反:OpenClaw 不声明能力 → 对任何声明了 requirements 的 engine **不支持**;当前实现空 → 托管一切。

**修改**:

```go
// ContextEngineHostCapability names one host-side facility a context engine
// may require. A harness advertises the set it provides.
type ContextEngineHostCapability string

const (
    HostBootstrap              ContextEngineHostCapability = "bootstrap"
    HostAssembleBeforePrompt   ContextEngineHostCapability = "assemble-before-prompt"
    HostAfterTurn              ContextEngineHostCapability = "after-turn"
    HostMaintain               ContextEngineHostCapability = "maintain"
    HostCompact                ContextEngineHostCapability = "compact"
    HostRuntimeLLMComplete     ContextEngineHostCapability = "runtime-llm-complete"
)

// ContextEngineOperation is the operation whose host requirements are checked.
type ContextEngineOperation string

const (
    OpAgentRun ContextEngineOperation = "agent-run"
    OpCompact  ContextEngineOperation = "compact"
)

type Capabilities struct {
    // ...
    // ContextEngineHost lists the host facilities this harness provides.
    // A harness that advertises none cannot run a context engine that
    // declares requirements for the operation.
    ContextEngineHost []ContextEngineHostCapability
}

// ContextEngineRequirement is what the caller's engine needs for one operation.
type ContextEngineRequirement struct {
    EngineID             string
    Operation            ContextEngineOperation
    RequiredCapabilities []ContextEngineHostCapability
    UnsupportedMessage   string
}

// MissingHostCapabilities returns the required capabilities c does not
// provide, empty when the harness can host req.
func (c Capabilities) MissingHostCapabilities(req ContextEngineRequirement) []ContextEngineHostCapability
```

`SupportContext.ContextEngine string` 换成 `ContextEngine *ContextEngineRequirement`(nil = 无要求,放行),`Rank` 改用 `MissingHostCapabilities`。

错误信息平移 OpenClaw 的形状,把 missing / required / actual 三份都带上,否则排查时只知道"不支持"而不知道差哪个动词:

```
context engine "ctxv2" cannot run operation "agent-run" on harness "cli":
missing host capabilities: assemble-before-prompt, compact;
required: bootstrap, assemble-before-prompt, compact; host provides: bootstrap, after-turn, maintain
```

> spec 01 §11 称本字段对 spec 06「已就位」—— 该结论作废,以本节为准。

### C5 — lifecycle 未做 ctx engine host 断言

**现状**:host 检查只存在于 `Rank` 内(即 auto 选择路径)。`Policy.Resolve` 走 `RequestedHarnessID` 分支时不过 `Rank`;调用方直接调 `RunAttemptWithLifecycle` 也不过。两条路径都能把一个托管不了该 engine 的 harness 跑起来。

**OpenClaw**:`lifecycle.ts:308` 在**每次 attempt 的 prepare 阶段**调 `assertAgentHarnessContextEngineSupport` —— pin 了也要过闸。

**修改**:`RunAttemptParams` 加 `ContextEngine *ContextEngineRequirement`,`RunAttemptWithLifecycle` 在参数校验之后、计时开始之前断言:

```go
if err := assertContextEngineHost(h, params.ContextEngine); err != nil {
    return nil, err
}
```

engine id 为 `"legacy"` 或 requirement 为 nil 时直接放行(对齐 OpenClaw 对 legacy engine 的豁免),保证 spec 06 启用之前行为不变。

---

## 4. P1 修正

### C6 — 陈旧 classification 不被清除

**现状**:

```go
// lifecycle.go:93-97
if c, ok := h.(Classifier); ok && h.Capabilities().Classify {
    if label := c.Classify(ctx, result, &params); label != "" {
        result.Classification = label
    }
}
```

classifier 返回空(「不表态」)时,backend 或 wrapper 先前写在 result 上的分类会原样留存。实测:backend 留下 `"drift"`,classifier 明确不表态,最终结果仍是 `"drift"`。

**OpenClaw**(`result-classification.ts`)先解构掉旧值再分类,注释写明动机:*"so retries or wrappers cannot preserve an obsolete classification from an earlier harness"*。

**修改**:先清零再分类,分类器返回空或 `ok` 一律落到 `ClassificationOK`:

```go
result.Classification = ""
if Implements(h, CapClassify) {
    result.Classification = h.(Classifier).Classify(ctx, result, &params)
}
if result.Classification == "" {
    result.Classification = ClassificationOK
}
```

注意这里顺带把裸 type assertion 换成 `Implements`,跟 spec 01 §3.3 自己定的规矩对齐(当前 lifecycle 是唯一一处违规)。

### C7 — `AttemptResult` 无 harness 归属

**OpenClaw** 无论有没有 classifier 都盖 `agentHarnessId: harness.id`。当前 `AttemptResult` 没有该字段,多 harness 之后无法归因一条结果是谁产出的。

**修改**:`AttemptResult` 加 `HarnessID string`,由 `RunAttemptWithLifecycle` 在 `normalizeResult` 里无条件写入(含 panic / error 路径)。

### C8 — Registry 无 reset / dispose 扇出

**现状**:`Harness.Dispose` 目前**没有任何调用路径**,进程关闭时谁都不会被拆;`Reset` 也只能一个个手点。

**OpenClaw**:`resetRegisteredAgentHarnessSessions` / `disposeRegisteredAgentHarnesses`,对每个 harness 单独 try/catch + `log.warn`,一个失败不阻断其余。

**修改**:

```go
// ResetAll calls Reset on every registered harness. One harness failing does
// not stop the fan-out; all errors are joined into the return value.
func ResetAll(ctx context.Context, params ResetParams) error

// DisposeAll calls Dispose on every registered harness with the same
// fan-out semantics. Intended for process shutdown.
func DisposeAll(ctx context.Context) error
```

用 `errors.Join` 聚合,不吞错(harness 包不 import 仓库 logger,保持零 `internal/` 依赖 —— 见 spec 01 §2.1)。接线由 spec 04 在 gateway 关闭路径上调 `DisposeAll`。

### C9 — `SettledTurnResult` 丢失真实产物

**现状**:`SettledTurnResult{Changed bool}`。

**OpenClaw** 的 `finalizeSettledTurn` 返回 `{ assistant, usage, assistantTranscriptOwned, assistantTranscriptIdempotencyKey, assistantMessageIndex }` —— 这个 capability 的存在意义就是**从已结算的工具 transcript 产出那条最终可见回答**。`Changed bool` 把产物本身丢了,capability 无法履行职责。

**修改**:

```go
type SettledTurnResult struct {
    // AssistantText is the single completed answer produced from the settled
    // tool transcript.
    AssistantText string
    Usage         Usage
    // TranscriptOwned marks that the harness already persisted the assistant
    // message itself, so the caller must not write it again.
    TranscriptOwned bool
    // IdempotencyKey is the exact key of the harness-owned transcript row.
    IdempotencyKey string
    // MessageIndex correlates the final reply with the assistant stream.
    MessageIndex int
}
```

本 spec 只改类型,不实现 —— embedded 目前不声明该 capability。

### C10 — 诊断事件层缺失

spec 01 §6.2 拒绝在 lifecycle 发事件,理由是会与 `Agent.Run` 的 `RunStartEvent` / `RunEndEvent` 重复。**对 embedded 成立,但结论被过度泛化了。**

OpenClaw 的解法是按 harness 过滤而非整体删除:

```ts
// lifecycle.ts:145
function shouldEmitAgentRunDiagnostics(harness: AgentHarness): boolean {
  return harness.id !== "openclaw";     // 内置的关掉,第三方的开着
}
```

而且它发的是独立的 trusted diagnostic 总线(`harness.run.started` / `harness.run.completed` / `harness.run.error`,带 trace span / durationMs / outcome / errorCategory),跟应用层 run 事件不是同一条流。当前一刀切的后果:未来的 CLI / plugin harness 什么都不发,既没有应用事件也没有诊断事件。

**修改**:lifecycle 加一个**可选观察者钩子**,默认 nil(不发任何东西,README §5 不变式 2 继续成立):

```go
// Observer receives harness-level diagnostics. It is not the application
// event bus: the embedded runtime already emits the run/turn event stream,
// so wiring leaves this nil for any harness that emits its own events.
type Observer interface {
    AttemptStarted(ObserverAttempt)
    AttemptCompleted(ObserverAttempt, *AttemptResult)
    AttemptFailed(ObserverAttempt, error)
}

func SetObserver(o Observer)
```

`ObserverAttempt` 携带 HarnessID / PluginID / RunID / SessionID / Provider / Model / DurationMs。是否为某个 harness 启用由 wiring 层决定,harness 包不硬编码 id 判断。

---

## 5. P2 修正

### C11 — `Superseded` 注释与行为不符

`types.go:337` 写「a reset **or a newer attempt** raced this one」,`lifecycle.go:16` 写「an attempt that started under an older generation」。但 `BumpLifecycleGeneration` 全仓库只有 `embedded.Reset` 一个调用点 —— **并发 attempt 之间永远不会互相 supersede**,且 `RunAttemptWithLifecycle` 起头只读不 bump。

另需在注释里点明:generation 机制在 OpenClaw 无对应物(那边靠 `terminal` / `externalAbort` 表达中断),属本仓库自创,免得后来者去 OpenClaw 找不到而以为漏了。

**修改**:两处注释改成只承诺 reset 语义。若确实要覆盖「新 attempt 抢占旧 attempt」,那是行为变更,单独提案,不在本 spec。

### C12 — `Register` 未归一 PluginID

**OpenClaw**:`pluginId: harness.pluginId ?? options.ownerPluginId`,注册时归一到一处。

**现状**:`h.PluginID()` 与 `Registration.OwnerPluginID` 两个真相源,`Rank` 的委派检查只看 `sc.PluginID`,两者不一致时行为未定义。

**修改**:`Register` 校验 —— `h.PluginID()` 非空且与 `ownerPluginID` 不一致时直接报错拒绝(与 `VerifyCapabilities` 一样,启动期炸)。

### C13 — `Capabilities` 缺 `DeliveryDefaults`

spec 03 §2.3 的第 5 维评分直接读 `Harness.Capabilities().DeliveryDefaults`,该字段**不存在**。

**修改**:

```go
// VisibleReplies is how a harness expects visible replies to be produced.
type VisibleReplies string

const (
    VisibleRepliesAutomatic   VisibleReplies = "automatic"
    VisibleRepliesMessageTool VisibleReplies = "message_tool"
)

type DeliveryDefaults struct {
    // VisibleReplies is the default policy when config does not override it.
    VisibleReplies VisibleReplies
}
```

挂到 `Capabilities.DeliveryDefaults *DeliveryDefaults`,nil = 未声明,由调用方取自身缺省。

## 6. 明确不在本 spec 范围

以下 OpenClaw 能力在当前实现中同样缺失,但没有下游 spec 依赖,不在本次修正内(spec 01 §3.2 只列了前两项,此处补全记录):

| 能力 | OpenClaw 位置 | 等待 |
|---|---|---|
| `RuntimeArtifact` | `runtime-artifact.types.ts` | artifact binding spec |
| `AuthBinding` | `types.ts:authBinding` | auth profile spec |
| `authBootstrap: "harness"` | `types.ts:259` | auth profile spec |
| `sessionFork.upstreamKinds` | `types.ts:292` | session fork spec |
| SessionFork 失败码(`steer-message` / `in-progress-turn` / `drift-mismatch` / `upstream-unavailable`) | `types.ts:187` | session fork spec |
| `PrepareAuthSupport` / `PrepareRouteSupport` | `support.ts:32,57` | provider / auth spec |

`SupportContext` 缺 `providerOwnerStatus` / `providerOwnerPluginIds` 一项归 spec 03(它已在自己的 §2.1 里声明了 `ProviderOwnership`)。

## 7. 测试要求

既有 53 个 case 必须继续全过(C1 / C2 / C4 会改动 `support_test.go` 的既有断言,属预期)。新增:

| 文件 | Test | 覆盖 |
|---|---|---|
| `support_test.go` | `TestRankNoPriorityDoubleCount` | 声明 10 的胜过「Supports 6 + hint 6」,C1 回归 |
| `support_test.go` | `TestAutoSelectionNilHintProbes` | nil hint → 交给 Supports |
| `support_test.go` | `TestAutoSelectionEmptyListExplicitOnly` | 空切片 → 自动选择中被排除 |
| `support_test.go` | `TestAutoSelectionProviderCaseInsensitive` | `anthropic` 命中 `["Anthropic"]` |
| `support_test.go` | `TestRankFiltersMissingHostCapability` | 缺 `assemble-before-prompt` → 出局 |
| `support_test.go` | `TestMissingHostCapabilitiesEmptyRequirement` | 无 requirement → 放行 |
| `support_test.go` | `TestCapabilitiesNoHostAdvertisedFailsClosed` | 未声明能力 + engine 有要求 → 拒绝 |
| `lifecycle_test.go` | `TestLifecycleAssertsContextEngineHost` | pin 的 harness 托管不了 → 直接报错,不跑 RunAttempt |
| `lifecycle_test.go` | `TestLifecycleLegacyEngineExempt` | legacy / nil requirement → 放行 |
| `lifecycle_test.go` | `TestClassificationClearsStaleLabel` | backend 留 drift + classifier 不表态 → ok |
| `lifecycle_test.go` | `TestResultCarriesHarnessID` | 成功 / 失败 / panic 三条路径都带 HarnessID |
| `lifecycle_test.go` | `TestObserverReceivesAttemptEvents` | 设了 Observer 才发,默认 nil 不发 |
| `registry_test.go` | `TestResetAllContinuesAfterFailure` | 3 个 harness,中间那个报错,另外两个仍被调用 |
| `TestDisposeAllJoinsErrors` | `errors.Join` 聚合,`errors.Is` 可取回每个 |
| `registry_test.go` | `TestRegisterRejectsPluginIDMismatch` | `h.PluginID()` 与 ownerPluginID 冲突 → 拒绝 |
| `registry_test.go` | `TestListOrdersByIDOnly` | C1 之后 List 不再受 priority 影响 |

新增 ≥ 16 个 case,总数 ≥ 69。覆盖率不低于当前的 92.9%。

## 8. 验收标准

```
go build ./...                                          PASS
go vet ./...                                            PASS
gofmt -l internal/                                      clean
go test -count=1 -race -cover ./internal/harness/...    PASS, coverage ≥ 92.9%
go test -count=1 -short ./...                           既有包 0 改动 0 失败
make lint-agents-boundaries                             PASS
go list -deps ./internal/harness | grep darvin-cowork   只有它自己
```

最后一条是硬约束:C4 引入的 `ContextEngineHostCapability` 常量**不得**从 `internal/agents/ctxengine/` import,必须在 harness 包内自行声明,由 wiring 层做转换(理由同 spec 01 §2.1)。

## 9. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| C4 改 `SupportContext.ContextEngine` 类型,spec 03 草案已引用旧形状 | 高 | 中 | 本 spec 先落地,spec 03 §2.1 同步更新(已在 03 加注) |
| C1 删 `AutoSelectionHint.Priority`,spec 03 §3.3 的 +100 / +1000 加分模型基于它 | 高 | 中 | spec 03 的加分模型本身也偏离 OpenClaw(那边是硬过滤),一并在 03 修正 |
| C2 改语义后 embedded 意外变成 explicit-only | 中 | 高 | `builtin_embedded.go:AutoSelection()` 在无 Providers 时返回 nil 而非空切片;`harness_test.go` 端到端用例守住 |
| C5 断言引入后既有 lifecycle 测试大面积失败 | 低 | 低 | requirement 为 nil 时放行,既有用例不传该字段 → 行为不变 |
| C10 Observer 被误当成应用事件总线,导致事件重复 | 中 | 高 | 默认 nil;doc comment 写明它不是 event bus;spec 04 接线时不给 embedded 设 Observer |
| 修正范围扩散成"顺手重构整个包" | 中 | 中 | 严格限定在 §2 的 13 条;超出的记到 §6 或新 spec |

## 10. 提交清单

```bash
$ git add internal/harness/
$ go test -count=1 -race -cover ./internal/harness/...
$ go test -count=1 -short ./...
$ git commit -m "fix(harness): align selection and capability semantics with reference design

对照 OpenClaw src/agents/harness/ 复核 spec 01 实现后的修正:

P0:
  - Rank 不再重复计入 AutoSelection 优先级(排序 bug)
  - AutoSelectionHint 空列表 = explicit-only(此前为匹配所有,语义相反)
  - provider id 比较前归一化
  - ContextEngineHost 改为能力动词集 + 按 operation 超集检查,缺省 fail-closed
  - lifecycle 每次 attempt 断言 ctx engine host 支持(pin 路径此前绕过)

P1:
  - 分类前清除陈旧 classification
  - AttemptResult 带 HarnessID
  - Registry 增加 ResetAll / DisposeAll 扇出
  - SettledTurnResult 补齐真实产物字段
  - lifecycle 增加可选 Observer(默认 nil,不发事件)

P2:
  - 修正 Superseded 注释与实际行为不符
  - Register 校验 PluginID 一致性
  - Capabilities 增加 DeliveryDefaults(spec 03 第 5 维依赖)

不动现有代码:internal/harness 仍无任何 import 方

Spec: specs/features/agent-harness-architecture/2026-08-04-harness-core-corrections.md"
```

## 11. 与其它 spec 的接口

- **01 spec**: 本 spec 修正它的产出。§11 中「`ContextEngineHost` 已就位」「`SupportContext` 已稳定」两条结论作废。
- **03 spec**: 依赖 C1 / C2(评分模型)、C4(第 4 维)、C13(第 5 维)。spec 03 的 `SupportContext` / `Candidate` 声明需与已实现形状对齐。
- **04 spec**: 关闭路径调 `DisposeAll`;是否给某 harness 设 `Observer` 由它决定。
- **06 spec**: 依赖 C4 / C5。ctx engine 启用前,host capability 必须能表达 assemble-before-prompt 与 compact 的差异。
