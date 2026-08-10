# agentloop 包命名与职责评估

> 探索性评估：`internal/agentloop` 的包名是否贴合实际职责？三方案（保持现状 / 改名 / 拆包）的成本、收益、风险对比。**本 spec 只产出决策建议，不直接落地代码改动。**

## 1. 概述

### 1.1 问题 / 背景

`internal/agentloop`（2030 行，含测试）实际承担 4 类职责，但包名 `agentloop` 字面只覆盖「串行 turn 队列（Loop）」一个子件：

| 职责 | 文件 | 说明 |
|---|---|---|
| **Loop 驱动** | `loop.go` (~400 行) | 串行 turn 队列：`Submit` / `Steer` / `SubmitSkill` / `Stop` / `Abort` / `Close`，单 goroutine 消费，msgID/runID 生成 |
| **Agent 装配** | `factory.go` (~300 行) | `AgentFactory.Build` 构造 `*agent.Agent` + plugins；`NewAgentLoopSession` 组装 Agent+Harness+Loop+DeltaHook+Subagents；`buildSubagentRunner` 构造 sub-agent runner；`resolveHarnessFor` 选 harness |
| **运行时恢复** | `hydrate.go` (~160 行) | 从 MessageStore + DigestStore 恢复会话历史（digests + tail 拼接） |
| **session 容器** | `session.go` (~60 行) | `AgentLoopSession` 生命周期 + Close 链（Subagents → DeltaHook → Loop） |

从**消费方视角**看更明显：gateway 的 `SessionManager` 不依赖 Loop，而是调用 `AgentFactory.NewAgentLoopSession(sessionID)` 一次性拿到「一个 session 的可运行 agent 运行时」（Agent + Harness + Loop + DeltaHook + Subagents + 历史恢复）。Loop 只是这个运行时的一个组件。

CLAUDE.md 第 200 行对 `internal/agentloop/` 的描述也只写了 Loop 驱动职责，没提 factory/session/hydrate —— **文档比代码还窄**。

### 1.2 目标

产出一份命名/职责评估：三个候选方案的**成本 - 收益 - 风险**对比，给出推荐方向。评审通过后，按采纳的方案落地（或维持现状）。

### 1.3 非目标

- 本 spec 不直接落地改名 / 拆包 —— 先评审决策。
- 不改任何行为逻辑、不改任何运行时路径。

## 2. 现状分析

### 2.1 执行链（为什么包名与职责错位）

```
gateway prompt → agentloop.Loop.Submit/Steer → Loop.executeTurn
  → harness.RunAttemptWithLifecycle  (harness/lifecycle.go:104)
  → embedded harness cfg.Run 闭包     (runtime/harness.go:20)
  → a.Prompt + a.Run                 (agents/agent.go)
  → executor.RunConversation         (agents/executor)
```

`runtime/harness.go:16` 注释即此链路。包名 `agentloop` 强化了「Loop 是核心」，但 Loop 只是链路的**调度入口**；装配、恢复、生命周期容器占了包内容近一半，且消费方入口是 `AgentFactory`。

### 2.2 引用面

`internal/agentloop` 共 **24 个引用点**：

- **9 个 Go 文件**：`internal/runtime/{runtime,factory,harness}.go`、`internal/gateway/{sessionmgr,handler_prompt,handler_skill}.go` + 4 个测试
- **CLAUDE.md**：第 200 行
- **specs/**：`subagent` / `bg-jobs` / `context-compaction-persistence` / `main-thin-runtime-build` / `agent-harness-architecture` 等多份既有 spec 引用 `internal/agentloop/factory.go`、`session.go` 等路径

### 2.3 命名规范对照

CLAUDE.md Go 代码规范 N1：包名小写、单数、短。`agentloop` 符合格式，但语义窄 —— 描述的是包内最大单一类型（Loop），而非包的职责边界。Go 社区惯例「包名取内部主类型」可为之辩护（对标标准库 `net/http` 的 `http.Server`），但当包的主入口类型（`AgentFactory`）与包名主类型（`Loop`）不一致时，语义偏差是真实的。

## 3. 候选方案

### 方案 A：保持包名 + 补文档描述

- **做法**：只更新 CLAUDE.md 第 200 行，补全 4 类职责（Loop 驱动 + AgentFactory 装配 + hydrate 恢复 + AgentLoopSession 生命周期）。
- **成本**：~5 行文档，零代码风险。
- **收益**：文档贴合实际，新读者不被误导。
- **风险**：无。但包名语义偏差长期存在——读者仍可能先入为主认为「这只是 turn 循环」；未来再往 AgentFactory 加装配（jobs / scheduler）会更偏离。

### 方案 B：改名 `sessionruntime`

- **做法**：`internal/agentloop` → `internal/sessionruntime`，全量替换 package 声明 + import + 文档 + 既有 spec 路径引用。
- **成本**：一次 commit，~24 个引用点机械替换；specs 里过期的 `internal/agentloop/xxx.go` 路径需同步或容忍过期。
- **收益**：语义最贴切 ——「per-session agent 运行时：装配 + 调度 + 生命周期」正是消费方拿到的产物。
- **风险**：**broad refactor**（CLAUDE.md 明确「不主动 broad refactor」，需用户显式授权）；git blame / 历史 spec 的路径引用全部失效；改名本身不增加能力。

### 方案 C：拆包

- **做法**：装配（`AgentFactory` / `hydrate` / `AgentLoopSession`）挪到 `agents/` 下新包，`loop.go` 独立成 `internal/loop`。
- **成本**：最高。`agents/` 禁引 capability 包（llm/tools/skills/mcp），装配逻辑需接口化（`subagent.Runner` 已示范）；`buildSubagentRunner` 依赖 `AgentFactory` 持有的 Provider/Tools，拆包要重排装配链路；改 import 环风险。
- **收益**：单一职责最清晰，`loop` 回归字面。
- **风险**：P1 子包阈值（≥300 行 / 独立依赖边界 / 独立测试）当前不满足；与「不主动 broad refactor」冲突最大；对运行时无任何行为收益，纯粹是组织重构。

## 4. 权衡对比

| 维度 | A 补文档 | B 改名 | C 拆包 |
|---|---|---|---|
| 语义贴合度 | 文档贴合，包名仍偏窄 | 全贴合 | 最贴合 |
| 改动成本 | ~5 行 | ~24 文件机械替换 | 最高（装配重排 + 接口化） |
| 行为风险 | 零 | 低（纯重命名，build/test 可兜底） | 中（import 环 / 装配链路） |
| 违背「不 broad refactor」 | 否 | 是 | 是 |
| 对未来演进友好度 | 一般（包名继续偏窄） | 好 | 最好 |

## 5. 未来趋势（影响决策）

- **subagent 已落地**：`AgentFactory.buildSubagentRunner` 让该包同时装配主 agent + sub-agent runner，已明确是「agent 运行时工厂 + 调度器」综合体。
- **bg-jobs spec 待落地**：`specs/features/builtin-tools-c-bg-jobs/` 会在 `AgentFactory` 附近加 jobs 装配，包将进一步偏离「loop」语义。
- 结论：包内容只会越来越「运行时」，不会越来越「loop」。**语义偏差随时间增长**，但改名的最佳时机应随「下次动这个包的结构」一起，而不是单独为改名开一次 broad refactor。

## 6. 推荐

**首选方案 A**：补 CLAUDE.md 描述（零成本让文档贴合实际），维持包名。

**改名（B）作为随附动作**：下次对该包做结构性改动（如 bg-jobs 装配、session 生命周期大改）时，随那次重构一并改名 `sessionruntime`，避免单独承担一次纯改名 commit 的 broad-refactor 成本。

**不推荐 C**：当前拆包收益仅是「组织美感」，成本最高，且 P1 阈值不满足。

## 7. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `CLAUDE.md` | 第 200 行补全 `internal/agentloop/` 的 4 类职责（方案 A）；若采纳 B，同步更新 import 路径描述 |

若采纳方案 B（改名），追加：

| 文件 | 变更说明 |
|---|---|
| `src/darvin-agent/internal/agentloop/*.go` | package 声明 `agentloop` → `sessionruntime` |
| `src/darvin-agent/internal/runtime/{runtime,factory,harness}.go` | import 路径 |
| `src/darvin-agent/internal/gateway/{sessionmgr,handler_prompt,handler_skill}.go` | import 路径 |
| 上述 + agentloop 内部 5 个 `*_test.go` | import 路径 |
| `CLAUDE.md` + 受影响的 specs | 路径引用同步 |

## 8. 验收标准

- [ ] 评估覆盖三方案，给出成本 / 收益 / 风险对比（§ 3-4）
- [ ] 明确推荐方向（§ 6）
- [ ] 若采纳 A：CLAUDE.md 第 200 行补全 4 类职责
- [ ] 若采纳 B：24 个引用点全量替换，`go build ./...` + `go vet ./...` + `go test ./...` 全绿
- [ ] 本 spec 作为决策记录保留（后续不满足「为什么叫 agentloop」时回查）
