# Harness Architecture 实施 CHECKLIST

> 7 个 spec 的实施 checklist,每条 = 1 个 git commit。
> 严格按 Phase 顺序执行,每 Phase 跑 `go test ./...` 必须全 PASS 才能进下一 Phase。

## Phase 0 — 文档(已完成)

- [x] 写 `00-harness-architecture-design.md` 总览
- [x] 写 `01-harness-core-interface.md` Harness interface
- [x] 写 `02-agent-refactor.md` Agent 拆分
- [x] 写 `03-selection-and-plugin.md` Selection + Plugin
- [x] 写 `04-gateway-integration.md` Gateway 接入
- [x] 写 `05-tool-surface-bridge.md` Tool bridge + middleware
- [x] 写 `06-ctx-engine-binding.md` ctx engine 启用
- [x] 写 `07-harness-core-corrections.md` 核心修正(Phase 1 复核后补写)
- [x] 写本 CHECKLIST.md
- [x] 写 README.md(实施顺序)

## Phase 1 — Harness 骨架(基于 spec 01)· 已完成

### 文件创建
- [x] `internal/harness/types.go` — Harness interface + 6 个可选 capability interface + 参数/结果类型
- [x] `internal/harness/registry.go` — 进程级注册表(package var,非 Symbol)
- [x] `internal/harness/policy.go` — Policy 解析 + Resolve
- [x] `internal/harness/support.go` — capability 校验 + Rank 评分
- [x] `internal/harness/lifecycle.go` — RunAttemptWithLifecycle + per-session generation
- [x] `internal/harness/builtin_embedded.go` — NewEmbedded(EmbeddedConfig)
- [x] ~~`internal/harness/symbol/symbol.go`~~ — **取消**:Go 单二进制里 package 级 var 本身就是进程单例,Symbol 是 Node 专有问题

### 测试
- [x] `internal/harness/registry_test.go` (11 case)
- [x] `internal/harness/support_test.go` (14 case)
- [x] `internal/harness/policy_test.go` (8 case)
- [x] `internal/harness/lifecycle_test.go` (12 case)
- [x] `internal/harness/harness_test.go` fixture + embedded 集成 (6 case)

### 验证
- [x] `go build ./...` PASS
- [x] `go vet ./...` PASS
- [x] `go test -count=1 -race -cover ./internal/harness/...` PASS(51 case,覆盖率 92.9%)
- [x] 现有既有 test 0 改动 0 失败(21 个包全 PASS)
- [x] `go list -deps ./internal/harness` 不含任何 `internal/` 包

### 提交
- [x] `git commit -m "feat(harness): add Harness interface + Registry + Lifecycle skeleton"`

## Phase 1.5 — Harness 核心修正(基于 spec 07)· **Phase 3 之前必须完成 P0**

> 对照 OpenClaw 源码复核 Phase 1 产出后发现的 13 处偏移。
> `internal/harness/` 目前零 import 方,改动成本此刻最低。

### P0(阻塞 spec 03 / 06)
- [x] C1 `support.go` / `types.go` / `builtin_embedded.go` — 删 `AutoSelectionHint.Priority`,`Rank` 不再叠加 bonus;`registry.go:autoPriority` 移除,`List()` 改纯 id 升序
- [x] C2 `types.go` — `Matches` → `Eligible`,区分 nil(探测)/ 空切片(explicit-only,硬拒);`builtin_embedded.go` 无 Providers 时返回 nil hint
- [x] C3 `types.go` — 新增 `normalizeProviderID` / `containsProvider`,白名单比较前归一
- [x] C4 `types.go` — `ContextEngineHost` 改能力动词集 + `ContextEngineRequirement` + `MissingHostCapabilities`;`SupportContext.ContextEngine` 换类型
- [x] C5 `lifecycle.go` — `RunAttemptParams` 加 `ContextEngine`,`RunAttemptWithLifecycle` 每次 attempt 断言 host 支持(nil / legacy 放行)

### P1
- [x] C6 `lifecycle.go` — 分类前清除陈旧 classification;裸 type assertion 换 `Implements`
- [x] C7 `types.go` / `lifecycle.go` — `AttemptResult.HarnessID`,三条路径都写
- [x] C8 `registry.go` — `ResetAll` / `DisposeAll` 扇出,`errors.Join` 聚合不吞错
- [x] C9 `types.go` — `SettledTurnResult` 补 AssistantText / Usage / TranscriptOwned / IdempotencyKey / MessageIndex
- [x] C10 `lifecycle.go` — 可选 `Observer` 钩子 + `SetObserver`,默认 nil 不发任何事件

### P2
- [x] C11 `types.go` / `lifecycle.go` — `Superseded` 注释收窄到 reset 语义,注明机制为本仓库自创
- [x] C12 `registry.go` — `Register` 校验 `h.PluginID()` 与 ownerPluginID 一致性
- [x] C13 `types.go` — `Capabilities.DeliveryDefaults`(spec 03 第 5 维依赖)

### 测试
- [x] `support_test.go` 新增 7 case(C1 回归 / nil vs 空切片 / 大小写 / host capability 3 类)
- [x] `lifecycle_test.go` 新增 5 case(host 断言 / legacy 豁免 / 陈旧分类 / HarnessID / Observer)
- [x] `registry_test.go` 新增 4 case(扇出容错 / 错误聚合 / PluginID 冲突 / List 排序)
- [x] 既有 53 case 全过(`support_test.go` 部分断言随 C1/C2/C4 调整,属预期)

### 验证
- [x] `go build ./...` PASS
- [x] `go vet ./...` PASS
- [x] `go test -count=1 -race -cover ./internal/harness/...` PASS(≥ 69 case,覆盖率 ≥ 92.9%)
- [x] 既有 test 0 改动 0 失败
- [x] `go list -deps ./internal/harness` 仍不含任何 `internal/` 包(C4 的常量必须包内自声明)

### 提交
- [x] `git commit -m "fix(harness): align selection and capability semantics with reference design"`

## Phase 2 — Agent 重构(基于 spec 02)

### Phase 2a:加子包
- [ ] `internal/agents/perm/permission_gate.go` (新子包)
- [ ] `internal/agents/msgid/bridge.go` (新子包)
- [ ] `internal/agents/runtime/controller.go` (新子包)
- [ ] `internal/agents/usage/tracker.go` (新子包)
- [ ] 各自 `_test.go`

### Phase 2b:Agent 切换到子包
- [ ] `internal/agents/agent.go` 字段从 30+ 减到 15
- [ ] 9 个 permission method 迁到 `*perm.Gate`
- [ ] 6 个 messageID method 迁到 `*msgid.Bridge`
- [ ] 5 个 lifecycle 状态迁到 `*runtime.Controller`
- [ ] 2 个 usage method 迁到 `*usage.Tracker`
- [ ] `internal/agents/dispatcher.go` 调用方更新
- [ ] `internal/agents/agent_mini_loop.go` 调用方更新

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 13 个 test(agent_test.go / dispatcher_test.go / agent_mini_loop_test.go)0 失败
- [ ] 4 个新子包 18 个 test 全 PASS
- [ ] agent.go 从 532 → ~300 行

### 提交
- [ ] `git commit -m "refactor(agents): decompose Agent into 4 sub-packages (perm/msgid/runtime/usage)"`

## Phase 3 — Selection + Plugin(基于 spec 03)

### 文件创建
- [ ] `internal/harness/selection.go` — 在 Rank 现有 5 类过滤之上扩展评分维度 + Decision tree
- [ ] `internal/harness/plugin/plugin.go` — Plugin struct
- [ ] `internal/harness/plugin/manager.go` — Manager 单例
- [ ] `internal/harness/selection_test.go` (10 case)
- [ ] `internal/harness/plugin/manager_test.go` (6 case)

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 test 0 失败
- [ ] Phase 1.5 的 P0 四项(C1–C5)已完成 —— 否则评分模型建在错误的地基上
- [ ] Phase 1+1.5+2+3 test 总数 ≥ 103(Phase 1 已有 53,Phase 1.5 +16)
- [ ] 0 业务代码改动(纯加 harness/ 子包)

### 提交
- [ ] `git commit -m "feat(harness): add Selection (5-dim scoring) + Runtime Plugin"`

## Phase 4 — Tool Bridge + Middleware(基于 spec 05)

### 文件创建
- [ ] `internal/harness/tooldridge/bridge.go` — Surface interface + bridge
- [ ] `internal/harness/tooldridge/middleware.go` — 5 个 middleware
- [ ] `internal/harness/tooldridge/bridge_test.go` (5 case)
- [ ] `internal/harness/tooldridge/middleware_test.go` (8 case)

### executor 改造(轻)
- [ ] `internal/agents/executor/executor.go:executor.Deps` 加 `ResultTransformer func(protocol.Result) protocol.Result` 字段
- [ ] `internal/agents/executor/executor.go:runToolsParallel` 调 ResultTransformer(若非 nil)
- [ ] `internal/agents/agent.go:buildExecutorDeps` 填 ResultTransformer

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 13 个 executor test 0 失败
- [ ] 13 个新 test 全 PASS
- [ ] 集成 test:`go test ./internal/agents/executor/...` 全过

### 提交
- [ ] `git commit -m "feat(harness): add Tool Surface Bridge + Result Middleware"`

## Phase 5 — ctx engine 启用(基于 spec 06)

### ctx engine 改造
- [ ] `internal/agents/ctxengine/params.go` 加 AvailableMcp / SystemSections 字段
- [ ] `internal/agents/ctxengine/sections.go` 真渲染 skills / mcp section
- [ ] `internal/agents/ctxengine/sections_test.go` 新增
- [ ] `internal/agents/ctxengine/assembler.go` 不动(已有实现)

### Agent 改造
- [ ] `internal/agents/agent.go:NewAgentConfig` 加 Skills / Mcp 字段
- [ ] `internal/agents/agent.go:New` 注入 skills / mcp
- [ ] `internal/agents/agent.go:listSkillSummaries / listMcpSummaries` 实现
- [ ] `internal/agents/agent.go:Agent` 字段加 skills / mcp

### executor 改造
- [ ] `internal/agents/executor/executor.go:RunConversation` 加 token 超 budget 检测
- [ ] 超 budget 时调 `d.CompactHarness()(harness.Compact)`

### harness.Compact 实现
- [ ] `internal/harness/builtin_embedded.go:EmbeddedConfig.Compact` 钩子在 wiring 层传入走 assembler 的闭包
- [ ] 调用方走 `harness.Compact(ctx, h, params)` helper(自带 Implements 校验)
- [ ] `internal/harness/harness_test.go` 加 Compact 端到端测试

### config 改动
- [ ] `config.yaml`:`agents.defaults.assembler_enabled: true`(从 false 改)
- [ ] 文档同步

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 ctx engine 13 个 test 0 失败
- [ ] 8 个新 test 全 PASS
- [ ] 端到端:启动后 system prompt 包含 `<available_skills>` / `<available_mcp>` 块
- [ ] 跑 5 轮长对话,触发至少 1 次 auto compact(log: `auto compact triggered`)

### 提交
- [ ] `git commit -m "feat(ctx-engine): wire up Assembler + AvailableSkills + harness.Compact"`

## Phase 6 — Gateway 集成(基于 spec 04)

### AgentFactory 改造
- [ ] `internal/acp/factory.go:AgentFactory` 加 HarnessID + ConfigRef 字段
- [ ] `internal/acp/factory.go:NewAcpSession` 调 resolveHarness
- [ ] `internal/acp/factory.go:resolveHarness` 实现

### AcpSession 改造
- [ ] `internal/acp/loop.go:AcpSession` 加 Harness 字段
- [ ] `internal/acp/loop.go:Loop` 持 Harness
- [ ] `internal/acp/loop.go:Loop.executeTurn` 调 harness.RunAttemptWithLifecycle
- [ ] `internal/acp/loop.go:Loop.executeTurn` skill 路径仍直接调 agent.RunSkillSession

### SessionEntry 改造
- [ ] `internal/gateway/sessionmgr.go:SessionEntry` 加 Harness 字段
- [ ] `internal/gateway/sessionmgr.go:attachAcpLocked` 复制 Harness 引用

### main.go 改造
- [ ] 启动时 `harness.MustRegister(harness.NewEmbedded(harness.EmbeddedConfig{Run: ...}), "")`
- [ ] 构造 `acp.AgentFactory` 时填 ConfigRef = cfg
- [ ] 关闭路径调 `harness.DisposeAll(ctx)`(spec 07 C8 提供)

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 gateway test (15+ 个) 0 失败
- [ ] 既有 acp test (10+ 个) 0 失败
- [ ] 新增 6 个集成 test 全 PASS
- [ ] 端到端 smoke:`client.prompt → LLM first chunk` 延迟 < 100ms

### 提交
- [ ] `git commit -m "feat(gateway): route prompts through Harness abstraction"`

## Phase 7 — (可选)BuiltinCliHarness demo

- [ ] 写 `internal/harness/builtin-cli.go`(mock 实现,真 CLI backend 后续 spec)
- [ ] 在 main.go 临时注册,验证 selection 工作
- [ ] 写 1 个 demo 集成 test
- [ ] 删除 demo 保留 framework

## 最终验收(全部 Phase 后)

- [ ] `go build ./...` PASS
- [ ] `go vet ./...` PASS
- [ ] `go test -count=1 -short ./...` PASS(预计总 test 数 ≥ 145,既有 70+ + 新增 75+)
- [ ] `make lint-agents-boundaries` PASS(harness 不 import agents 的具体子包)
- [ ] spec 07 的 13 条修正全部落地(P0 四项在 Phase 3 之前,P1/P2 可延后但不得跳过)
- [ ] smoke test:`client.prompt → LLM first chunk` 延迟增加 < 5%
- [ ] 内存增量 < 10MB
- [ ] 端到端覆盖:AvailableSkills / AvailableMcp / Auto compact / Manual compact / Steer / Abort
- [ ] 全部 RPC 协议 / EventBus 协议 / 数据库 schema 不变
- [ ] 写 CHANGELOG.md 总结(可选)
