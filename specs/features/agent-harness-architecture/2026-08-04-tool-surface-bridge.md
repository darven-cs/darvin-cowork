# 05 — Tool Surface Bridge + Result Middleware

> 状态: 草案 v1 · 2026-08-04
> 父 spec: `00-harness-architecture-design.md`
> 前置: `01-harness-core-interface.md`, `02-agent-refactor.md`
> 输出: `internal/harness/tooldridge/bridge.go` (~150) + `internal/harness/tooldridge/middleware.go` (~280) + 测试

## 1. 目标

在 harness 层加 2 个独立工具:

1. **Tool Surface Bridge** (`bridge.go`, ~150 行):把 darvin-cowork 现有的 `protocol.ToolRegistry` 适配成 harness 视角的"声明式 tool surface"。harness 通过它查"这个 backend 能跑哪些 tool",而不直接 import `internal/agents/protocol`。
2. **Tool Result Middleware** (`middleware.go`, ~280 行):对 `protocol.Result` 标准化。统一限制大小、统一 metadata schema、标准化 IsError 语义。让所有 backend (embedded / cli / 未来 plugin) 产出的 tool result 都符合 harness 期望的格式。

## 2. 设计原则

- **bridge 不持有状态**:它只是 `*protocol.ToolRegistry` 的 thin adapter,所有查询转发
- **middleware 是纯函数链**:`type Middleware func(next Result) Result`,可以链式组合
- **不修改 tool 本身**:tool 实现 (`internal/tools/`) 一行不改
- **不修改 executor**:executor 拿到的还是 `protocol.ToolRegistry`,只是 harness 在初始化时包一层 middleware

## 3. Tool Surface Bridge

### 3.1 接口定义

```go
// internal/harness/tooldridge/bridge.go
package tooldridge

import "darvin-cowork/backend/internal/agents/protocol"

// Surface 是 harness 视角的 tool 声明接口。它把具体 tool 实现的细节
// (internal/tools/fs.go, internal/skills/...) 隐藏在 Bridge 后面。
type Surface interface {
    // Specs 返回 LLM 看到的 tool 列表(给 CompletionRequest.Tools 用)。
    // 中间件链应用前/后都可以调,看哪一阶段需要标准化。
    Specs() []protocol.ToolSpec

    // Names 返回所有已注册 tool 的名字(给 status / debug 用)。
    Names() []string

    // GetEntry 返回单个 tool 的元数据(给 routing / permission gate 用)。
    // 第二返回值=false 表示该 tool 不存在。
    GetEntry(name string) (protocol.Entry, bool)

    // WithMiddleware 包一层 middleware(返回新的 Surface,不动原 registry)。
    // 多次调用 = 多个 middleware 累积(顺序:外层先调,内层后调)。
    WithMiddleware(mw ...ResultMiddleware) Surface
}

// ResultMiddleware 是 tool result 的纯函数链 middleware,见 §4。
type ResultMiddleware func(next protocol.Result) protocol.Result
```

### 3.2 Bridge 默认实现

```go
// bridge.go (续)
type bridge struct {
    reg   protocol.ToolRegistry
    mws   []ResultMiddleware
}

func New(reg protocol.ToolRegistry) Surface {
    return &bridge{reg: reg}
}

func (b *bridge) Specs() []protocol.ToolSpec {
    specs := b.reg.Specs()
    // Phase 5 暂不修改 specs(由 executor 标准化,见 §5)
    return specs
}

func (b *bridge) Names() []string {
    return b.reg.Names()
}

func (b *bridge) GetEntry(name string) (protocol.Entry, bool) {
    return b.reg.GetEntry(name)
}

func (b *bridge) WithMiddleware(mw ...ResultMiddleware) Surface {
    return &bridge{reg: b.reg, mws: append(append([]ResultMiddleware{}, b.mws...), mw...)}
}

// ApplyMiddleware 对一个 tool result 跑当前所有 middleware。
// 内置 Bridge 不直接调,留给 harness.RunAttempt 收尾时用。
func (b *bridge) ApplyMiddleware(r protocol.Result) protocol.Result {
    cur := r
    for i := len(b.mws) - 1; i >= 0; i-- {
        cur = b.mws[i](cur)
    }
    return cur
}
```

### 3.3 不做的事

- **不接管 permission gate**:permission 是 `perm.Gate` 的事(02 spec),bridge 只查 GetEntry,不改 EvaluatePermission
- **不接管 scoping**:`ScopedForSkill` 是 `protocol.ToolRegistry` 的方法,bridge 直接转
- **不接管 Plugin 注册**:plugin (`tool.Plugin`) 还是直接调 `protocol.ToolRegistrar`,bridge 不介入

## 4. Tool Result Middleware

### 4.1 6 个标准 middleware

```go
// internal/harness/tooldridge/middleware.go
package tooldridge

import "darvin-cowork/backend/internal/agents/protocol"

// MaxResultBytes 限制 result Content 字节数(默认 50KB,LLM context 不能爆)。
// 超限时:截断 + 加 "[truncated N bytes]" 后缀 + Metadata["truncated"]=true。
func MaxResultBytes(maxBytes int) ResultMiddleware { ... }

// MaxResultLines 限制 result 行数(默认 2000 行)。
// 超限时:截断 + 加 "[truncated N lines]" 后缀 + Metadata["line_truncated"]=true。
func MaxResultLines(maxLines int) ResultMiddleware { ... }

// NormalizeError 把各种 error 表示统一为 IsError=true + Content="[error] {msg}"。
// 部分 backend(尤其 cli subprocess)会用 Content 里塞 error text,而不是
// IsError=true;这个 middleware 统一修正。
func NormalizeError() ResultMiddleware { ... }

// SanitizeControlChars 把 Content 里的 NUL 等控制字符(0x00-0x08, 0x0B,
// 0x0C, 0x0E-0x1F)替换成空格,避免 LLM 解析问题。
func SanitizeControlChars() ResultMiddleware { ... }

// WithToolMetadata 给 Result.Metadata 注入 tool name + kind。
// 当前由 executor 注入,这里提供一个兜底(给绕过 executor 的 path 用)。
func WithToolMetadata(toolName string, kind protocol.Kind) ResultMiddleware { ... }

// TimeLimit(暂时不做,留作未来扩展) - 工具执行超时
// PermissionRelay(暂时不做) - 权限请求转发
```

### 4.2 默认 middleware 链

```go
// tooldridge.DefaultMiddleware() 返回 OpenClaw 等价的标准链。
// main.go 在创建 embedded harness 时用这个包一下。
func DefaultMiddleware() []ResultMiddleware {
    return []ResultMiddleware{
        SanitizeControlChars(),
        NormalizeError(),
        MaxResultBytes(50 * 1024),
        MaxResultLines(2000),
    }
}
```

### 4.3 中间件组合(可选)

```go
// ChainHelper: 一行加多个 middleware
func Chain(mws ...ResultMiddleware) ResultMiddleware {
    return func(next protocol.Result) protocol.Result {
        for i := len(mws) - 1; i >= 0; i-- {
            next = mws[i](next)
        }
        return next
    }
}

// 使用:
//   bridge := tooldridge.New(reg).WithMiddleware(
//       tooldridge.Chain(tooldridge.MaxResultBytes(...), tooldridge.SanitizeControlChars()),
//   )
```

## 5. 接入 harness

### 5.1 5.1 harness 初始化时挂 middleware

```go
// internal/harness/builtin-embedded.go
func NewEmbeddedHarness(agentRegistry *agent.Registry) Harness {
    reg := agentRegistry.Tools()                  // 拿 tool registry
    surface := tooldridge.New(reg).WithMiddleware(tooldridge.DefaultMiddleware()...)

    return &embeddedHarness{
        surface: surface,                          // 注入
    }
}

type embeddedHarness struct {
    surface tooldridge.Surface
    // ...
}

func (h *embeddedHarness) RunAttempt(ctx, params) (*AttemptResult, error) {
    // ...
    // 内部 Agent.Run 时:
    //   1. executor.RunConversation → 调 tool.Execute → 拿 protocol.Result
    //   2. 通过 surface.ApplyMiddleware 标准化 → emit event
    // (实际 executor 集成见 §5.2)
}
```

### 5.2 executor 集成(轻改动)

```go
// internal/agents/executor/executor.go
// 现有:drainStream + runToolsParallel 拿到的 Result 直接给 LLM
// 改造后:在 Result 返回 LLM 前过 middleware

// 不改 executor 接口,改为:executor.Deps 接收一个 optional ResultTransformer
type Deps struct {
    // ... 现有字段
    ResultTransformer func(protocol.Result) protocol.Result  // 新增
}

// executor.runToolsParallel:
//   for each tool call:
//     res := tool.Execute(...)
//     if d.ResultTransformer != nil { res = d.ResultTransformer(res) }
//     append to results
```

具体实现走 `internal/agents/agent.go` 包装:

```go
// agent.go 的 exec.RunConversation 调用处
func (a *Agent) buildExecutorDeps() executor.Deps {
    base := executor.Deps{ /* 现有所有字段 */ }
    if a.toolMiddleware != nil {
        base.ResultTransformer = a.toolMiddleware
    }
    return base
}
```

## 6. 与 OpenClaw 差异

| OpenClaw | darvin-cowork | 原因 |
|---|---|---|
| `tool-surface-bridge.ts` 234 行 | ~150 行 | 砍掉 OpenClaw 的"deliverToAgentDelivery" / "transcriptVisibility" 等与 darvin-cowork 不重合的概念 |
| `tool-result-middleware.ts` 556 行 | ~280 行 | 砍掉 OpenClaw 的"image embedding" / "audio attachment" / "diff visualization",这些 darvin-cowork 不做 |
| 6 个 middleware | 5 个 + 1 留空 | TimeLimit/PermissionRelay 留作未来 spec |

## 7. 不动的东西

- `internal/tools/` (fs / shell / list_dir 等) **完全不动**
- `internal/skills/` (skill plugin) **完全不动**
- `internal/mcp/` (mcp transport) **完全不动**
- `executor.RunConversation` 算法 **完全不动** (只多 1 个 ResultTransformer 钩子)
- `protocol.Tool` / `protocol.ToolRegistry` / `protocol.Entry` / `protocol.Kind` **完全不动**

## 8. 测试要求

### 8.1 bridge_test.go

| Test | 覆盖 |
|---|---|
| `TestNewBridgeSpecs` | Specs() 转发正确 |
| `TestNewBridgeNames` | Names() 转发 |
| `TestNewBridgeGetEntry` | 存在/不存在 |
| `TestBridgeWithMiddleware` | WithMiddleware 不影响原 bridge,返回新实例 |
| `TestBridgeApplyMiddleware` | ApplyMiddleware 顺序:后加的先跑 |

### 8.2 middleware_test.go

| Test | 覆盖 |
|---|---|
| `TestMaxResultBytes` | 超过 → 截断 + Metadata["truncated"]=true |
| `TestMaxResultBytesUnderLimit` | 不超过 → 原样返回 |
| `TestMaxResultLines` | 超过行数 → 截断 |
| `TestNormalizeErrorPlainText` | Content 含 "error:" 前缀 → IsError=true |
| `TestNormalizeErrorAlreadyError` | IsError=true → 不动 |
| `TestSanitizeControlChars` | NUL 等替换 |
| `TestWithToolMetadata` | 注入 name + kind |
| `TestChain` | 多个 middleware 链式跑 |

总测试数 ≥ 13。

## 9. Phase 4 提交清单

```bash
$ git add internal/harness/tooldridge/
$ go test -count=1 -short ./internal/harness/tooldridge/...
$ go test -count=1 -short ./...   # 0 破坏
$ git commit -m "feat(harness): add Tool Surface Bridge + Result Middleware

平移 OpenClaw src/agents/harness/tool-surface-bridge.ts +
tool-result-middleware.ts:

- internal/harness/tooldridge/bridge.go:
  * Surface interface (Specs/Names/GetEntry/WithMiddleware)
  * bridge 包装 protocol.ToolRegistry
  * ApplyMiddleware 跑链

- internal/harness/tooldridge/middleware.go:
  * MaxResultBytes(50KB 默认)
  * MaxResultLines(2000 默认)
  * NormalizeError(统一 IsError 语义)
  * SanitizeControlChars(控制字符)
  * WithToolMetadata(注入 tool name/kind)
  * DefaultMiddleware() 链

- internal/agents/executor: 加 ResultTransformer 钩子
  * 1 个字段,0 业务逻辑改动
  * executor_test.go 0 改动

不影响任何下游:internal/tools/ internal/skills/ internal/mcp/

Spec: specs/features/agent-harness-architecture/05-tool-surface-bridge.md"
```

## 10. 风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| MaxResultBytes 50KB 太小,某些 tool (比如 cat 大文件) 丢失信息 | 中 | 低 | Metadata["truncated"] 标志 + "[truncated N bytes]" 后缀,LLM 知道被截断;未来可按 tool kind 配置不同上限 |
| NormalizeError 把正常文本误判为 error | 低 | 中 | 只在 Content 前 1KB 扫描 "error:" / "Error:" / "ERROR:",不全文搜 |
| SanitizeControlChars 把 JSON 里的转义符搞坏 | 低 | 高 | 只动 0x00-0x08/0x0B/0x0C/0x0E-0x1F,**不动** 0x09 (tab) / 0x0A (LF) / 0x0D (CR) |
| executor 加 ResultTransformer 钩子破坏现有 13 个 test | 中 | 中 | 钩子是 optional,nil 时跟现在完全一样;test 走原路径 |
| WithMiddleware 每次返回新 Surface,但 embeddedHarness 持的是常量 | 低 | 低 | 中间件注册发生在 harness 构造时,运行时只 ApplyMiddleware,无 alloc |

## 11. 与其它 spec 的接口

- **01 spec**: Harness interface 通过 `Capabilities().ToolSurface` 字段(本 spec 加)声明自己用哪个 Surface
- **02 spec**: Agent 工具调用路径上,executor.Deps.ResultTransformer 由 Agent 在构造时填入 surface.ApplyMiddleware
- **03 spec**: Selection 不直接用 bridge,本 spec 不影响 selection
- **04 spec**: Gateway 改造时,`harness.RunAttempt` 内部用 surface
- **06 spec**: ctx engine 启用时,中间件链保证 message 不被 tool result 撑爆
