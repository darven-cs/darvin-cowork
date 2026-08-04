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
- [x] 写本 CHECKLIST.md
- [x] 写 README.md(实施顺序)

## Phase 1 — Harness 骨架(基于 spec 01)

### 文件创建
- [ ] `internal/harness/types.go` — Harness interface + 9 capability 子类型
- [ ] `internal/harness/registry.go` — 进程级 Symbol-backed Map
- [ ] `internal/harness/policy.go` — HarnessPolicy 解析
- [ ] `internal/harness/support.go` — capability matching 评分函数
- [ ] `internal/harness/lifecycle.go` — RunAttemptWithLifecycle
- [ ] `internal/harness/builtin-embedded.go` — NewEmbeddedHarness stub
- [ ] `internal/harness/symbol/symbol.go` — Symbol-backed 全局 var 工具

### 测试
- [ ] `internal/harness/registry_test.go` (6 case)
- [ ] `internal/harness/support_test.go` (4 case)
- [ ] `internal/harness/lifecycle_test.go` (6 case)
- [ ] `internal/harness/builtin-embedded_test.go` (2 case)
- [ ] `internal/harness/harness_test.go` 集成 (1 case)

### 验证
- [ ] `go build ./...` PASS
- [ ] `go vet ./...` PASS
- [ ] `go test -count=1 -short ./internal/harness/...` 全 PASS(≥ 19 case)
- [ ] 现有 70+ 既有 test 0 改动 0 失败

### 提交
- [ ] `git commit -m "feat(harness): add Harness interface + Registry + Lifecycle skeleton"`

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
- [ ] `internal/harness/selection.go` — 5 维评分 + Decision tree
- [ ] `internal/harness/plugin/plugin.go` — Plugin struct
- [ ] `internal/harness/plugin/manager.go` — Manager 单例
- [ ] `internal/harness/selection_test.go` (10 case)
- [ ] `internal/harness/plugin/manager_test.go` (6 case)

### 验证
- [ ] `go build ./...` PASS
- [ ] 既有 test 0 失败
- [ ] Phase 1+2+3 test 总数 ≥ 53
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
- [ ] `internal/harness/builtin-embedded.go:embeddedHarness.Compact` 走 assembler
- [ ] `internal/harness/builtin-embedded_test.go` 加 Compact 测试

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
- [ ] 启动时 `harness.RegisterHarness(harness.NewEmbeddedHarness(...), "")`
- [ ] 构造 `acp.AgentFactory` 时填 ConfigRef = cfg

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
- [ ] `go test -count=1 -short ./...` PASS(预计总 test 数 ≥ 130,既有 70+ + 新增 60+)
- [ ] `make lint-agents-boundaries` PASS(harness 不 import agents 的具体子包)
- [ ] smoke test:`client.prompt → LLM first chunk` 延迟增加 < 5%
- [ ] 内存增量 < 10MB
- [ ] 端到端覆盖:AvailableSkills / AvailableMcp / Auto compact / Manual compact / Steer / Abort
- [ ] 全部 RPC 协议 / EventBus 协议 / 数据库 schema 不变
- [ ] 写 CHANGELOG.md 总结(可选)
