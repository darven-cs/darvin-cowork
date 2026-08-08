# darvin-agent Go 代码全量合规修订 设计文档

## 1. 概述

### 1.1 问题 / 背景

`src/darvin-agent/` 当前规模:非测试 Go 代码 ~23000 行,分布在 14 个 internal 包(含 30 个子包)。功能正确、测试覆盖率高、命名风格统一,但读者会觉得难啃。量化指标:

| 维度 | 现状 | 工程项目典型值(参考 Reasonix) |
|---|---|---|
| 最大文件行数 | `gateway/handlers.go` 2030 行 | 6000+(Reasonix `controller.go`),但有兄弟文件按职责切分 |
| 注释/代码比 | 0.36–0.49(agent/dispatcher/loop) | 0.15–0.35 |
| `Phase N` 注释 | 2 处(`agent.go`) | 0 |
| `FR-N` 注释 | 22 处 / 10 个文件 | 0 |
| `D-N` 注释 | 8 处 / 4 个文件 | 0 |
| `Reasonix` 镜像注释 | **54 处 / 21 个文件**(ctxengine 包占 30+) | 0 |
| 过小子包(<120 行) | `agents/runtime`(78)、`agents/msgid`(85) | — |

具体痛点:

1. **god file**:`gateway/handlers.go` 2030 行塞了 59 个 IPC handler,按业务域明显可拆。
2. **注释当作散文写**:每个字段 3–8 行注释,反复解释"为什么这么设计",塞满 `Phase 5 default until the skill plugin ships` 这类违反 CLAUDE.md 注释规范的迭代规划内容。
3. **外部代号满地跑**:`FR-1` / `D10` / `Reasonix summaryTimeout` 等代码里没链接到任何 spec 文档的代号,读者根本不知道指什么。
4. **子包过度细分**:`agents/` 下 11 个子包,其中 `msgid`(85 行)、`runtime`(78 行)等极小包只放一两个类型,但 `agent.go` 要 import 8 个,读者读 `Agent` 结构时要在十几个目录间跳。
5. **格式 / 命名局部不一致**:部分文件用中文行内注释,部分纯英文;部分文件函数命名带前缀(`handleXxx` vs `Xxx`),无统一约定。
6. **缺 CI 硬门**:仅 `Makefile lint-agents-boundaries` 一个自定义 target,没有 `gofmt` / `go vet` / `golangci-lint` 阻塞门。

### 1.2 目标

1. **制定一份 darvin-agent Go 代码规范**(落 `CLAUDE.md` → `### darvin-agent Go 代码规范`,作为后续所有 Go 改动的硬约束)。本 spec §3 仅承担规则摘要 + 变更追踪,正文不重复。
2. **按规范全量修订现有代码**:分阶段、可回滚、零行为变化。
3. **可量化验收**:修订完成后,§7 验收标准的所有数字指标可被脚本一键校验。

### 1.3 非目标

- **不改外部行为**:本次属于 refactor bucket,任何 IPC / DB schema / 配置项 / 用户可见输出都保持字节一致。
- **不重写架构**:不引入 transport-agnostic core / DI 容器 / 依赖反转等结构性改动(那是后续 spec 的事)。
- **不动测试逻辑**:测试文件**只**跟随生产代码的拆分/重命名做机械调整,不重写测试用例。
- **不动 TS / Vue / Electron 主进程**:本 spec 仅限 `src/darvin-agent/`。
- **不删功能**:`steerAgent` 之类的"死代码"由其他 spec 处理,本 spec 不顺手清。

---

## 2. 现状量化(全包扫描,2026-08-08)

### 2.1 注释违规模式(精确扫描结果)

| 模式 | 出现次数 | 涉及文件数 | 重灾区 |
|---|---|---|---|
| `Phase [0-9]` | 2 | 1 | `agents/agent.go` |
| `FR-[0-9]` | 22 | 10 | `ctxengine/assembler.go`(7)、`ctxengine/assemble.go`(3)、`ctxengine/compact.go`(2) |
| `D[0-9]+` | 8 | 4 | `ctxengine/compact.go`(3)、`ctxengine/assemble.go`(2) |
| `Reasonix` | 54 | 21 | `ctxengine/compact.go`(10)、`ctxengine/reasonix_style_test.go`(11)、`mcp/registry.go`(5) |

### 2.2 中文注释分布(违反 C4 全英文)

**20 个文件,共 303 行中文**:

| 文件 | 中文行数 |
|---|---|
| `gateway/handlers.go` | 123 |
| `gateway/sessionmgr.go` | 85 |
| `agentloop/factory.go` | 23 |
| `agentloop/session.go` | 12 |
| `gateway/jsonrpc.go` | 12 |
| `agents/executor/executor.go` | 8 |
| `agents/text_delta_hook.go` | 7 |
| `agents/dispatcher.go` | 4 |
| `agents/store/store.go` | 4 |
| `agents/store/models.go` | 4 |
| `runtime/runtime.go` | 4 |
| `gateway/eventledger.go` | 4 |
| `agentloop/loop.go` | 3 |
| `agents/perm/permission_gate.go` | 2 |
| `tools/shell.go` | 2 |
| `llm/anthropic/convert.go` | 2 |
| `tools/sandbox.go` | 1 |
| `skills/runner.go` | 1 |
| `agents/store/sqlite_store.go` | 1 |
| `mcp/types.go` | 1 |

### 2.3 注释密度 > 0.30 的文件(违反 C3,共 44 个)

**重灾区(>0.50)**:
| 文件 | 密度 |
|---|---|
| `agents/protocol/protocol.go` | 4.50(纯接口包,可豁免) |
| `agents/errors.go` | 1.20(13 行小文件,可豁免) |
| `agents/store/digest_store.go` | 1.25(32 行小文件,可豁免) |
| `agents/protocol/provider.go` | 1.32(82 行接口包,可豁免) |
| `agents/ctxengine/ctxengine.go` | 0.74(47 行包 doc) |
| `config/config.go` | 0.74(106/143) |
| `agents/ctxengine/errors.go` | 0.70(7/9) |
| `agents/protocol/events.go` | 0.70(31/43) |
| `llm/events.go` | 0.92(12/12) |
| `llm/types.go` | 0.63(33/51) |
| `agents/protocol/types.go` | 0.58(58/99) |
| `mcp/notifier.go` | 0.58(11/18) |
| `agents/usage/tracker.go` | 0.52(39/74) |
| `harness/types.go` | 0.50(167/330) |
| `agents/agent.go` | 0.49(205/420) |
| `agents/ctxengine/assembler.go` | 0.49(102/206) |

**中度(0.30–0.50)**(共 28 个,清单略,见 `scripts/check-agent-readability.sh` 跑出)

**总评**:`protocol/` / `errors.go` / `events.go` / 极小文件(<30 行)的密度高是因为分母小且承担包 doc,**可豁免**;真正需要清理的是 `agent.go` / `dispatcher.go` / `assembler.go` / `compact.go` / `loop.go` 等核心业务文件。

### 2.4 god file 清单(>500 行非测试文件,7 个)

| 文件 | 行数 | 拆分策略(详见 §4.2) |
|---|---|---|
| `gateway/handlers.go` | 2030 | 按业务域拆成 8 个 `handler_<domain>.go` |
| `mcp/registry.go` | 716 | 拆 `registry.go` / `registry_resolve.go` / `registry_notify.go` |
| `agents/ctxengine/compact.go` | 698 | 把嵌入的 system prompt 字面量抽到 `prompts.go`,主体保持 |
| `agents/agent.go` | 694 | 拆 `agent.go`(类型+构造) / `agent_run.go`(Run循环) / `agent_run_skill.go` |
| `harness/types.go` | 575 | 按 F5 拆:文件按职责命名(`capabilities.go` / `execution.go` 等),不建 `types.go`;若本质是跨包契约可挪到 `harness/protocol/` 子包(对标 `agents/protocol/`),实施时决定 |
| `mcp/transport/stdio.go` | 527 | 拆 `stdio.go`(协议) / `stdio_io.go`(读写循环) |
| `agents/executor/executor.go` | 512 | 拆 `executor.go`(主循环) / `executor_permission.go` / `executor_truncate.go` |

### 2.5 过碎子包(待合并)

合并判定依据:子包行数 < 300 且不承担独立依赖边界。

| 子包 | 行数 | 处理策略(详见 §4 Phase D) |
|---|---|---|
| `agents/runtime` | 78 | 合并回 `agents/`,改名为 `runtime_state.go` |
| `agents/msgid` | 85 | 合并回 `agents/`,改名为 `msgid_bridge.go` |
| `agents/queue` | 121 | 合并回 `agents/`,改名为 `queue.go` |
| `agents/usage` | 124 | 合并回 `agents/`,改名为 `usage_tracker.go` |
| `harness/plugin` | 229 | 合并回 `harness/`,改名为 `plugin.go`(子包反向依赖父包,典型该合的信号) |
| `harness/tooldridge` | 311 | 合并回 `harness/`,拆为 `bridge.go` + `middleware.go`(顺便消灭 typo 命名) |

**保留独立**的子包(承担独立依赖边界或被多包反向引用):

| 子包 | 行数 | 保留理由 |
|---|---|---|
| `agents/session`(179) | import `llm`,合进 `agents/` 父包会违反 `Makefile lint-agents-boundaries`(agents/ 不准 import 能力包) |
| `agents/event`(394) | 被 `ctxengine` / `executor` / `perm` / `queue` 等多子包反向依赖,合并会循环 |
| `agents/perm`(249) | 反向被父包用,且依赖 `executor` |
| `agents/protocol`(614) | F6 跨包契约层 |
| `agents/executor`(512) | import `tools`,违反父包边界 |
| `agents/store`(1499) | import `llm`,违反父包边界;且已超 300 行 |
| `agents/ctxengine`(2130) | 行数过大,承担独立职责 |

合并后 `agents/` 子包数从 11 → 7,`harness/` 子包数从 2 → 0。

### 2.6 gofmt 未跑文件(违反 G1,共 30 个)

`gofmt -l .` 报告以下文件未格式化(测试文件 13 个、生产文件 17 个)。Phase B-0 一次性 `gofmt -s -w .` 解决。

生产文件清单(`internal/`):
- `agentloop/{factory,hydrate}.go`
- `agents/agent.go` / `agents/ctxengine/{archive,assemble,compact,params}.go`
- `agents/store/{digest_store,memory_digest_store,models,sqlite_digest_store,usage_store}.go`
- `agents/usage/tracker.go`
- `gateway/handlers.go`
- `mcp/{sanitize,registry_enhance_test}.go`(后者是 test)
- `mcp/transport/{sse,stdio}.go`

### 2.7 全包合规体检结论

| 检查项 | 状态 | 数量 |
|---|---|---|
| `go vet ./...` | ✅ 通过 | 0 警告 |
| `gofmt -l .` | ❌ 未跑 | 30 文件 |
| `goimports -l .`(违反 I1/I2) | ❌ 未跑 | 24 文件(import 分组顺序混乱) |
| 文件级 package comment(违反 F3) | ❌ 缺失 | 393 文件 |
| exported godoc 缺失(违反 ST1020+ / C2) | ❌ 缺失 | 262 处 |
| ST1003 initialism 命名(`Id` / `Http` 等) | ✅ 假阳性 | 0 实际(扫到的 2 处是字符串字面量) |
| ST1005 错误字符串大写开头 / 尾标点 | ⏳ 待 lint | 工具未装,实施时跑 |
| ST1006 receiver 名不一致 | ⏳ 待 lint | 工具未装,实施时跑 |
| `Phase [0-9]` / `FR-[0-9]` / `D-N` / `Reasonix` 注释 | ❌ 违规 | 86 处 / 21 文件 |
| 中文注释(违反 C4) | ❌ 违规 | 303 行 / 20 文件 |
| 注释密度 > 0.30(违反 C3) | ❌ 违规 | 44 文件(含 ~12 个小文件豁免) |
| god file > 800 行(违反 F1) | ❌ 违规 | 4 个(`handlers.go` 2030 / `mcp/registry` 716 / `compact.go` 698 / `agent.go` 694) |
| 过碎子包(违反 P1) | ❌ 违规 | 6 个 |
| register 模式缺失(违反 F7) | ❌ 违规 | `tools/` 包 |

### 2.8 格式统一 + 全量注释 lint 基线

**工具可用性**(2026-08-08 扫描时):

| 工具 | 状态 | 用途 |
|---|---|---|
| `gofmt` | ✅ Go 工具链自带 | 基础格式(tab / 空格 / 缩进) |
| `goimports` | ❌ 未装 | import 分组 + 别名缺失补全(`go install golang.org/x/tools/cmd/goimports@latest`) |
| `staticcheck` | ❌ 未装 | ST10xx 注释 / 命名规则(`go install honnef.co/go/tools/cmd/staticcheck@latest`) |
| `golangci-lint` | ❌ 未装 | Phase E 聚合门(`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`) |

Phase B 开始前必须先 `go install` 这三个工具,否则后续阶段无法跑。

**import 分组顺序违规清单**(24 文件,违反 I1 三段顺序):

- `internal/agentloop/` × 3:`loop.go` / `factory.go` / `hydrate.go`
- `internal/agents/` × 4:`agent.go` / `dispatcher.go` / `text_delta_hook.go` / `perm/permission_gate.go` / `ctxengine/assembler.go` / `store/sqlite_store.go` / `store/usage_store.go`
- `internal/gateway/` × 3:`handlers.go` / `sessionmgr.go` / `eventledger.go`
- `internal/mcp/registry.go` × 1
- `internal/runtime/` × 10:`runtime.go` / `factory.go` / `database.go` / `gateway.go` / `config.go` / `provider.go` / `skills.go` / `mcp.go` / `workspace_bootstrap.go`
- `internal/skills/bootstrap.go` × 1

**F3 文件级 package comment 缺失**:393 文件(占非测试文件总数 ~80%)。这是历史欠债——绝大多数 `.go` 顶部直接 `package xxx`,无注释。Phase B-5 一次性补齐。

**ST1020+ exported godoc 缺失**:262 处(导出 func / type / var / const 无 doc 注释)。Phase B-6 一次性补齐。

---

## 3. darvin-agent Go 代码规范(本 spec 的核心产出)

> **规范正文已迁移到 `CLAUDE.md` → `## 编码风格` 下的 `### darvin-agent Go 代码规范` 子章节**。本 spec 不再重复规则正文,只保留**执行入口与验收口径**。
>
> 参照规则时按 ID 索引(如 F1 / N3 / C4 / G3.1 等),实际正文以 CLAUDE.md 为准。本 spec §3 仅承担:
> 1. 规则变更通知路径(规则变更需同步本 spec §3.2 摘要 + 修改 CLAUDE.md)
> 2. 阶段执行时的规则引用清单(Phase B/C/D 都按 F1-F7 / N1-N6 / C1-C5 / I1-I4 / E1-E4 / P1-P3 / G1-G4 判定)

### 3.1 规则部署位置

| 载体 | 用途 | 状态 |
|---|---|---|
| `CLAUDE.md` → `### darvin-agent Go 代码规范`(`## 编码风格` 子章节) | **规范正文唯一来源**(rule ID + 完整反例 / 例外 / 理由) | Phase A 落地 |
| `src/darvin-agent/AGENTS.md` | **不创建**(避免重复维护;CLAUDE.md 已是仓库顶层指引) | 取消 |
| `docs/darvin-agent-go-style.md` | **不创建**(CLAUDE.md 已含完整正文;不在 docs/ 复制) | 取消 |
| 本 spec §3.2 / §3.3 | **不重复规则正文**;仅放规则摘要表 + 变更追踪 | 当前 |

### 3.2 规则摘要表(spec 仅维护此表,正文在 CLAUDE.md)

| 章节 | 规则 ID | 一句话 |
|---|---|---|
| 文件结构 | F1 | 单文件软上限 800 行 |
| | F2 | god file 按业务域拆,不按语法元素 |
| | F3 | 每个 `.go` 顶部必须有 package / file-level comment |
| | F4 | 文件名 `snake_case` 小写 |
| | F5 | 文件按"类型 + 操作"组织,禁建 `types.go` / `utils.go` 垃圾抽屉 |
| | F6 | 接口在调用方包定义,实现在被调方包 |
| | F7 | 能力接口 + `init()` 自注册(对标 Reasonix `tool.RegisterBuiltin`) |
| 命名 | N1 | 包名小写、单数、短 |
| | N2 | 导出 `PascalCase`,包内 `camelCase`,导出必须有 doc |
| | N3 | 接口名用职责动词,不带 `I` 前缀 |
| | N3.1 | 接口位置遵守 F6 |
| | N4 | JSON-RPC handler `handle<Domain>` 前缀 |
| | N5 | wire 投影类型 `<Domain>Wire` 后缀 |
| | N6 | 常量值禁止 magic value 散落 |
| 注释 | C1 | 禁阶段/版本/FR-N/Reasonix/代码复述/思考过程注释 |
| | C2 | 仅保留 doc / 非常规写法意图 / 架构边界 / 兜底注释 |
| | C3 | 注释密度 ≤ 0.30(核心文件可放宽 0.35) |
| | C4 | 注释语言统一英文 |
| | C5 | godoc 精简,无 `@example` |
| import | I1 | 三段(stdlib / 第三方 / 内部),空行分隔,组内字母序 |
| | I2 | `gofmt -s` + `goimports` 自动维护 |
| | I3 | 禁 `.` import,别名 import 须说明 |
| | I4 | import 分组错误由 `goimports -w` 自动归一 |
| 错误 | E1 | 错误变量 `Err<Entity>` / `err<Entity>` |
| | E2 | `fmt.Errorf` 用 `%w` |
| | E3 | 错误字符串小写开头、无尾标点 |
| | E4 | `if err != nil` 不加注释 |
| 子包 | P1 | 新子包阈值(≥200 行 / 独立依赖边界 / 独立测试) |
| | P2 | 不满足 P1 的合并回父包 |
| | P3 | `agents/` 下子包名避免与父包同义 |
| 格式 | G1 | `gofmt -s` 强制 |
| | G1.1 | `goimports -l .` 强制 |
| | G2 | `go vet ./...` 零警告 |
| | G3 | `golangci-lint`(`errcheck` + `govet` + `staticcheck` + `unused` + `ineffassign`) |
| | G3.1 | `staticcheck` ST10xx(ST1000/1003/1005/1006/1019/1020-1023)强制 |
| | G4 | Go 文件用 tab 缩进 |

### 3.3 规则变更追踪

| 日期 | 变更 | 原因 |
|---|---|---|
| 2026-08-08 | 初版:从本 spec 迁出至 `CLAUDE.md` | 避免 spec 与项目指南双源 |

新增 / 修订规则:**改 `CLAUDE.md` 同步在本节追加一行**;不另起 spec 记录规则正文(spec 只追踪变更,不重复内容)。

---

## 4. 实现方案(分阶段)

> 每个 phase 是**独立可回滚的 commit 单元**。任何一个 phase 卡住,后续 phase 不动。

### Phase A:规范正文落 `CLAUDE.md`(零代码改动)

**动作**:
1. 在 `CLAUDE.md` 下"## 编码风格"章节新增 `### darvin-agent Go 代码规范` 子章节,内容是 darvin-agent Go 代码规范**完整正文**(rule ID + 完整反例 / 例外 / 理由),与 §3.2 规则摘要表 1:1 对应。这是 darvin-agent Go 代码规范的**唯一权威来源**。
2. 不创建 `src/darvin-agent/AGENTS.md`(避免双源维护;读者已通过仓库根 `CLAUDE.md` 进入)。
3. 不创建 `docs/darvin-agent-go-style.md`(规范正文已在 `CLAUDE.md`,不在 docs/ 复制)。
4. 在 `CLAUDE.md` "先读"段补一行:"3. `### darvin-agent Go 代码规范`(若改动 Go agent 代码)"。

**产出**:`CLAUDE.md` 新增一个 `###` 子章节。

**风险**:无。

### Phase B:全包格式化 + 注释合规 + 全量 lint(零行为变化)

**目标**:让 darvin-agent 每个 `.go` 文件都通过 `CLAUDE.md` → `### darvin-agent Go 代码规范` 全部规则(规则 ID 见本 spec §3.2)。拆 7 个子阶段(B-0 ~ B-7),**每个子阶段独立 commit**。

**前置**(B-0 之前一次性做):

```bash
go install golang.org/x/tools/cmd/goimports@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

把 `$(go env GOPATH)/bin` 加入 PATH 验证三个工具可用。

#### B-0:全包基础格式化(零风险)

```bash
cd src/darvin-agent
gofmt -s -w .
```

一次解决 §2.6 列出的 30 个文件(测试 + 生产)。`gofmt -l .` 输出必须为空。

#### B-1:删违规注释模式(机械替换)

按 §2.1 清单逐文件处理:

1. 删除所有 `Phase N` / `FR-N` / `D-N` / `Reasonix` 镜像式注释(共 86 处,21 个文件)
2. 删除首尾解说、装饰分隔线、模型思考过程注释
3. 外部 spec 代号(如 `FR-4`)**改写成自然语言**或**链接到 spec 文件**(如 `// 详见 specs/features/context-compaction-reasonix-style/`)

**逐文件改动量预估**:

| 文件 | 预计改动 |
|---|---|
| `agents/agent.go` | -50 行(Phase N / 散文 / FR-N) |
| `agents/ctxengine/assembler.go` | -40 行 |
| `agents/ctxengine/compact.go` | -30 行 + system prompt 抽出 |
| `agents/ctxengine/assemble.go` | -25 行 |
| `agents/executor/executor.go` | -20 行 |
| `agents/dispatcher.go` | -25 行 |
| `agentloop/loop.go` | -30 行 |
| `mcp/registry.go` | -20 行 |
| 其他 ~13 个文件 | -2 ~ -10 行/文件 |

#### B-2:中文注释翻译为英文(逐文件人工)

按 §2.2 清单(20 文件,303 行)逐文件翻译。**翻译原则**:
- 不直译,按 invariant / 边界约束重写
- 长中文段落优先**精简**或**删除**,不堆英文
- 测试文件名中文不动(只是路径)

**重点文件优先级**(按中文行数):
1. `gateway/handlers.go`(123)→ `gateway/sessionmgr.go`(85)→ `agentloop/factory.go`(23)
2. 中度:`agentloop/session.go`(12)/ `gateway/jsonrpc.go`(12)/ `executor/executor.go`(8)/ `text_delta_hook.go`(7)
3. 零散:剩 13 个文件,每个 ≤ 4 行

#### B-3:注释密度精简(针对 >0.30 的核心文件)

§2.3 列出 44 个文件密度 >0.30,但**只对核心业务文件强制降到 0.30 以下**:
- 必降:`agent.go`(0.49) / `dispatcher.go`(0.36) / `assembler.go`(0.49) / `compact.go`(0.32) / `loop.go`(0.39) / `factory.go`(0.35) / `helpers.go`(0.38) / `archive.go`(0.45) / `params.go`(0.47) / `projection.go`(0.36) / `agent_mini_loop.go`(0.33) / `queue.go`(0.39) / `tracker.go`(0.52) / `types.go`(harness,0.50) / `models.go`(store,0.55) / `message_store.go`(0.42) / `imported_file_store.go`(0.34) / `store.go`(0.88) / `usage_store.go`(0.32) / `event.go`(agents,0.33) / `model_registry.go`(0.37) / `tools.go`(protocol,0.60) / `types.go`(protocol,0.58) / `bridge.go`(msgid,0.48) / `client.go`(gateway,0.39) / `eventledger.go`(0.38) / `jsonrpc.go`(0.45) / `policy.go`(0.31) / `builtin_cli.go`(0.47) / `builtin_embedded.go`(0.37) / `bridge.go`(tooldridge,0.61) / `errors.go`(llm,0.36) / `registry.go`(llm,0.61) / `types.go`(llm,0.63) / `provider.go`(llm,0.36) / `model_registry.go`(llm,0.88) / `events.go`(llm,0.92) / `notifier.go`(0.58) / `database/sqlite.go`(0.34) / `session.go`(agentloop,0.58) / `testharness.go`(0.44)

**豁免清单**(<30 行小文件或纯接口/包 doc,Phase B-3 不动):
- `agents/protocol/protocol.go`(4.50,纯包 doc)/ `provider.go`(1.32)
- `agents/errors.go`(1.20,13 行) / `ctxengine/errors.go`(0.70,21 行) / `ctxengine/ctxengine.go`(0.74,47 行)
- `agents/store/digest_store.go`(1.25,32 行)
- `lifecycle.go`(ctxengine,0.33) 等极小文件

**精简方法**:
- 散文字段注释 → 1 行说 invariant
- 重复/复述注释 → 删
- "为什么这么写" → 保留(规则 C2 允许);"做了什么" → 删

**B-3 风险**:误删上下文。**缓解**:每个文件单独 commit,PR review 时对照前后版本逐行确认;豁免清单明确列出。

#### B-4:import 分组顺序归一(机械)

```bash
cd src/darvin-agent
goimports -w -local darvin-cowork/ .
goimports -l . | grep -q . && echo "still unformatted" || echo "clean"
```

一次性解决 §2.8 列出的 24 文件 import 顺序违规(违反 I1/I2/I4)。**完全机械**——`goimports` 按 `stdlib → 第三方 → 内部(darvin-cowork/)`三段重排,无歧义。

**注意**:`-local darvin-cowork/` 把内部包单独成组,避免被并入第三方组。

**风险**:零(纯格式)。`go build ./... && go test ./...` 必须 100% 通过,失败立即 `git checkout` 还原。

#### B-5:文件级 package comment 补全(违反 F3,393 文件)

**目标**:每个 `.go` 顶部都有一段以 `Package <name>` 开头的注释,或者(file-level)说明该文件在包内承担的职责。

**实施策略**(批量,低风险):

1. 包内主文件(每个 internal 包选一个主文件)写 `// Package <name> does X.` 形式的 package doc;主文件选择规则:
   - 已有 `doc.go` 的包 → 不动
   - 包名同名文件存在(`<pkg>.go`)→ 在该文件顶部加
   - 否则在最小文件或最"通用"文件(如 `types.go` / `registry.go`)加
2. 同包其他文件在 `package <name>` 行之前加 file-level 注释(规则 F3):
   ```go
   // File-level: HTTP-handler family for the MCP JSON-RPC surface.
   // Splits from handlers.go for readability; same package, same Handler type.

   package gateway
   ```
3. **批量执行**:`scripts/check-agent-readability.sh` 输出 F3 缺失清单,逐文件人工补一行(可借助模板)。

**模板生成建议**:写一段 `scripts/gen-pkg-comments.sh` 脚本扫描所有缺失文件,基于文件名(`handler_<domain>.go` → "Handler family for <domain>")生成候选注释,人工 review 后批量 commit。

**风险**:低。注释内容只影响 godoc 渲染,无运行时行为;批量补的注释措辞需要 review,但每个文件 1 行,review 量可控。

#### B-6:全量注释 lint(违反 ST1020+,262 处)

**目标**:让 `staticcheck` ST10xx 规则集(本 spec §3.2 G3.1 行的 9 条 ST 规则)在 darvin-agent 全包零告警。

**实施步骤**:

1. **跑 lint 拿精确清单**:
   ```bash
   cd src/darvin-agent
   staticcheck -checks 'ST10*' ./... > /tmp/st-violations.txt
   wc -l /tmp/st-violations.txt  # baseline
   ```

2. **按违规类型分批 fix**:
   - **ST1020/ST1021/ST1022/ST1023(导出标识缺 godoc)**:262 处。**机械补 doc**:
     - `func` → `// FuncName does X.`
     - `type` → `// TypeName represents ...`
     - `const` / `var` → `// Name is the ...`
     - 不知所云的(如 helper 函数)→ 重命名 + 加 doc,或加 `//nolint:staticcheck` 暂时豁免
   - **ST1003(initialism)**:实际 0 处(§2.7 已确认)。lint 跑出再处理
   - **ST1005(错误字符串)**:与规则 E3 重合,lint 报数后逐个 fix
   - **ST1006(receiver 名)**:同类型所有方法 receiver 名必须一致;`agents/agent.go` 的 `a *Agent` 与某处 `ag *Agent` 不一致 → 统一为 `a`
   - **ST1019(import 重复)**:删除冗余 import 分组

3. **再次跑 lint 确认**:`staticcheck -checks 'ST10*' ./...` 输出空 → B-6 完成。

**风险**:
- 给 helper 函数硬塞 doc 会显得机械 → **缓解**:doc 措辞遵循 godoc 风格(动词开头,一句话讲清"做什么"),不知如何描述的 helper 优先考虑是否应该重命名
- `//nolint:staticcheck` 滥用 → 严格限制:每个 nolint 必须配一行 reason(`//nolint:staticcheck // internal helper, not part of API`)
- receiver 名统一可能与外部 import 点冲突 → 影响面为 0(receiver 名不在调用点出现),纯内部命名,放心改

#### B-7:全量 lint 收尾(golangci-lint 聚合)

```bash
cd src/darvin-agent
golangci-lint run ./... > /tmp/lint-baseline.txt
```

把 Phase E 的 `.golangci.yml` 落地后跑一次聚合 lint。**预期**:B-0 ~ B-6 完成后,本步骤应该 0 告警。剩余告警记入 baseline 文件(`.golangci-baseline.txt`),后续 PR 不允许新增同类告警。

**风险**:首次跑可能爆出 `unused` / `ineffassign` 等代码质量告警(非格式 / 注释类)。**处理**:本 spec 范围只 fix 注释 / 命名 / 格式类;代码质量类告警(如未使用变量)记入 baseline,留给后续 spec。

### Phase C:god file 拆分

**动作**:按 §2.2 清单拆分,每个文件一个 commit。拆分顺序(从最低风险到最高):

1. `harness/types.go` → 按类型分组拆(纯类型移动,零逻辑改动)
2. `agents/executor/executor.go` → 抽 `executor_permission.go` / `executor_truncate.go`
3. `agents/ctxengine/compact.go` → 抽 `prompts.go`(system prompt 字面量)+ `compact_archive.go`(归档逻辑)
4. `mcp/registry.go` → 抽 `registry_resolve.go` / `registry_notify.go`
5. `mcp/transport/stdio.go` → 抽 `stdio_io.go`(读写循环)
6. `agents/agent.go` → 抽 `agent_run.go`(Run 循环 + dispatcher hook)/ `agent_skill.go`(skill mini loop)
7. **`gateway/handlers.go`** → 按业务域拆 8 个 `handler_<domain>.go`(最大动作,放最后)

**拆分原则**:
- 同包不同文件,**不改包名**,不改函数签名
- 文件顶部加规则 F3 规定的 file-level 注释
- 测试文件跟随生产代码拆分(如 `handlers_test.go` 跟着拆成 `handler_session_test.go` 等)
- **按 F5:每个拆分文件聚焦一个领域概念**(类型 + 方法 + helper 同文件),**禁止**顺手建 `types.go` / `interfaces.go` / `utils.go` 这类语法元素命名的集中文件;struct / const / interface 跟它对应的领域逻辑一起放在领域文件里
- **按 F6:接口和它的实现不在同一个包**——拆分时若发现某个接口和它的唯一实现挤在同包同文件,把接口挪到调用方包(或独立 protocol 子包);若接口和实现在不同包但实现包 import 接口包正常,不动

**风险**:
- 大文件 git history 会断(用 `git log --follow` 缓解)
- 拆分时漏移 helper → 编译失败,**缓解**:每个拆分 commit 单独跑 `go build ./... && go test ./...`

### Phase D:子包合并

**动作**:合并 §2.3 列出的 6 个过碎子包回父包。

**D-1:`agents/` 下合并 4 个子包**(顺序:无外部依赖先合 → 有依赖的后合)

| 子包 | 合到 | 文件名 | 关键改动 |
|---|---|---|---|
| `agents/runtime/`(78) | `agents/` | `runtime_state.go` | 改包名 `runtime` → `agent`;`runtime.Controller` 重命名为 `RunController`(避免与 `agents/runtime` 子包名混淆) |
| `agents/msgid/`(85) | `agents/` | `msgid_bridge.go` | 改包名 `msgid` → `agent`;`msgid.Bridge` 保持类型名 `Bridge` |
| `agents/queue/`(121) | `agents/` | `queue.go` | 改包名 `queue` → `agent`;`queue.Queue` 保持类型名 `Queue` |
| `agents/usage/`(124) | `agents/` | `usage_tracker.go` | 改包名 `usage` → `agent`;`usage.Tracker` 保持类型名 `Tracker` |

D-1 全包仅父包自用,合并后改 `agents/agent.go` 等文件的 import,删 `package queue` / `package msgid` 等声明。

**D-2:`harness/` 下合并 2 个子包**

| 子包 | 合到 | 文件名 | 关键改动 |
|---|---|---|---|
| `harness/plugin/`(229) | `harness/` | `plugin.go` | 改包名 `plugin` → `harness`;`plugin.Manager` 保持类型名 `PluginManager` 或 `Manager`(后者有命名冲突风险,实施时定) |
| `harness/tooldridge/`(311) | `harness/` | `bridge.go` + `middleware.go` | 改包名 `tooldridge` → `harness`;`Surface` / `Bridge` / `ResultMiddleware` 类型保持;**顺手消灭 typo 命名**(tooldridge 不是词) |

D-2 合并后,所有 import `harness/plugin` 或 `harness/tooldridge` 的地方改为 `harness`。

**反向 import 清单**(实施时一并改):
```
grep -rln "agents/queue\|agents/msgid\|agents/runtime\|agents/usage" --include="*.go"
grep -rln "harness/plugin\|harness/tooldridge" --include="*.go"
```

**风险**:
- `agents/queue.go` 文件名与 `agents/queue/` 子包目录同名,删除子包时易残留空目录 → **缓解**:子包目录 `rmdir` 前确认无文件
- `harness/plugin` 反向 import 父包,合并后父子关系消失,代码无变化但**包名变更**影响所有 import 点 → 每个 import 修改单独 commit
- `harness/tooldridge` 类型 `Surface` 重名风险:合并后若 `harness` 包已有同名类型需重命名,实施时检查
- 任一合并引发 `go build` 失败 → **缓解**:每个子包合并一个 commit,单独跑 `go build ./... && go test ./...`

### Phase E:本地工具链(CI 暂不接入)

**前提**:本项目目前 CI 仅手动跑,无 `.github/workflows/` Go 流程。Phase E **只产出本地工具**,CI 接入留给未来 spec。

**动作**:

1. 新增 `src/darvin-agent/.golangci.yml`(基于 Reasonix `.golangci.yml`,见下方模板)。
2. `Makefile` 增加 target:
   - `fmt`:跑 `gofmt -s -w . && goimports -w -local darvin-cowork/ .`
   - `fmt-check`:跑 `gofmt -l . && goimports -l .`(输出空为通过)
   - `vet`:跑 `go vet ./...`
   - `lint`:跑 `golangci-lint run ./...`
   - `lint-comments`:跑 `staticcheck -checks 'ST10*' ./...`(单独跑注释/命名规则,便于本地迭代)
   - `check`:聚合 `fmt-check + vet + lint-comments + lint + check-readability`(一行命令跑全部)
3. 新增 `scripts/check-agent-readability.sh`(§7.1 验收脚本),人工或 IDE 保存时跑
4. **不接入 CI**:开发者自行 `make check`;未来 CI 流程落地后,本 spec 的工具直接复用

**`.golangci.yml` 模板**(放 `src/darvin-agent/.golangci.yml`):

```yaml
version: "2"
linters:
  default: none
  enable:
    - errcheck
    - gofmt    # 与 G1 强制对齐
    - goimports # 与 G1.1 / I1 / I2 / I4 强制对齐
    - govet
    - ineffassign
    - staticcheck
    - unused
  settings:
    staticcheck:
      checks:
        - all
        - -ST1005  # error 字符串结尾标点(初期宽松)
    errcheck:
      check-type-assertions: false
      exclude-functions:
        - (*os.File).Close
        - (io.Closer).Close
issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

**风险**:首次跑 lint 可能爆出大量既有告警,**缓解**:首次接入时用 `--exclude` 文件标记 baseline,新增告警才阻塞。

### Phase F:能力点接入 register 模式(可选,优先级低于 A–E)

**背景**:`internal/llm/` 已用模式 B(`llm.RegisterProvider("anthropic", ...)` + `llm.NewProvider(ctx, name, cfg)`,`llm/anthropic/provider.go:52` 有 `func init()`)。**只有 `internal/tools/` 缺失该模式**:`builtins.go` 平铺构造 5 个 tool(`readFileTool{sb}` / `writeFileTool{sb}` / ... / `newShellTool(sb, allowlist)`),加新 tool 要改 main + 改 builtins.go。

**前提约束**:5 个 tool 都依赖 `workdir + allowlist` 运行时配置,因此**必须用模式 B(工厂注册)**,不能用模式 A(单例注册)。

**动作**(只针对 `tools` 包):

1. `tools/registry.go` 新增 process-global 注册部分:
   ```go
   type BuiltinConfig struct {
       Sandbox   *fsSandbox  // 当前 fsSandbox 类型不导出;Phase F 顺手导出为 Sandbox
       Allowlist []string
   }
   type BuiltinFactory func(cfg BuiltinConfig) (Tool, error)

   var (
       builtinMu        sync.RWMutex
       builtinFactories = map[string]BuiltinFactory{}
   )

   func RegisterBuiltinFactory(name string, factory BuiltinFactory) {
       // panic on empty name / nil factory / dup name(对齐 llm.RegisterProvider 行为)
   }

   func RegisteredBuiltinFactories() []string {
       // RLock + sorted keys(对齐 llm.RegisteredProviders)
   }
   ```

2. `tools/builtins.go` 重写 `NewBuiltins` 内部实现(**签名不变**,向后兼容):
   ```go
   func NewBuiltins(workdir string, allowlist []string) (*Registry, error) {
       sb, err := newFsSandbox(workdir, DefaultPathExclusions()...)
       if err != nil { return nil, err }
       reg := NewRegistry()
       reg.sb = sb
       cfg := BuiltinConfig{Sandbox: sb, Allowlist: allowlist}
       for _, name := range RegisteredBuiltinFactories() {
           t, err := builtinFactories[name](cfg)
           if err != nil { return nil, fmt.Errorf("tool: builtin %s: %w", name, err) }
           reg.MustRegister(t)
       }
       return reg, nil
   }
   ```

3. 5 个 tool 各自在原文件加 `func init()`(不动业务代码):
   - `fs.go`:`readFileTool` / `writeFileTool` / `editFileTool` / `listDirTool` 各加一段 init
   - `shell.go`:`shellTool` 加一段 init

   每段形如:
   ```go
   func init() {
       RegisterBuiltinFactory("read_file", func(cfg BuiltinConfig) (Tool, error) {
           return &readFileTool{sb: cfg.Sandbox}, nil
       })
   }
   ```

4. `fsSandbox` 类型导出为 `Sandbox`(因为 `BuiltinConfig.Sandbox` 字段类型暴露给 factory;不导出会无法被 factory 实现 引用)。所有内部引用同步改。

**main 端**:**不动**。`runtime/runtime.go` 调用 `tools.NewBuiltins(workdir, allowlist)` 的签名零变化,加新 tool 时只要在 builtin 子目录加文件 + `init()` 即可。

**可选(本 spec 范围外,留给下一 spec)**:把 5 个 tool 从 `tools/fs.go` / `tools/shell.go` 拆到 `tools/builtin/read_file.go` / `write_file.go` / ...,并新建 `tools/builtin/` 子包,main 用 `_ "darvin-cowork/backend/internal/tools/builtin"` blank import 控制。**本 spec 不做**(避免改动面失控,先把注册机制接通)。

**风险**:
- `fsSandbox` 导出为 `Sandbox` 后,任何外部 import 都能直接调 `Sandbox.Resolve` —— 需 grep 确认无意外暴露
- 5 个 init() 在 `tools` 包内执行顺序无法保证(按文件名字母序)—— `RegisteredBuiltinFactories` 返回已排序,注册顺序由 map 决定但取用时 sorted,行为确定

**回滚**:整个 Phase F 独立 commit。失败 `git revert`,回到平铺构造的 `NewBuiltins`。

---

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| 拆分 god file 时,helper 同时被多个域调用 | 放在 `handler_core.go` / `registry_common.go` 等"共享"文件,文件顶部注释声明"跨域共享" |
| 注释清理后某段代码确实需要长注释才能讲清 | 保留,但 PR review 必须明确说明"为何此段例外" |
| 拆分时发现 handler 之间有隐式耦合(共享状态) | 暂停拆分,先记录到 TODO,本 spec 不解决,留给后续 spec |
| 子包合并引发 import cycle | 退回保留独立子包,在子包 doc 里说明"虽小但有独立依赖边界" |
| CI 接入 golangci-lint 首次跑出 100+ 告警 | 用 baseline 文件标记存量,本 spec 只要求"新增告警阻塞",存量分批清 |
| 测试文件因为生产代码拆分需要跟着拆 | 强制跟随拆分,保持"被测文件 ↔ 测试文件" 1:1 关系 |

---

## 6. 涉及文件

### 6.1 新增文件

| 文件 | 用途 |
|---|---|
| `src/darvin-agent/AGENTS.md` | **不创建**(规则正文已在 `CLAUDE.md`) |
| `docs/darvin-agent-go-style.md` | **不创建**(规则正文已在 `CLAUDE.md`) |
| `src/darvin-agent/.golangci.yml` | Phase E lint 配置 |
| `src/darvin-agent/.golangci-baseline.txt` | Phase B-7 产出的 lint baseline(后续 PR 不允许新增同类告警) |
| `scripts/check-agent-readability.sh` | §7.1 验收脚本(gofmt / goimports / vet / F3 / ST10xx / baseline) |
| `scripts/gen-pkg-comments.sh` | Phase B-5 辅助:扫 F3 缺失文件,基于文件名生成候选 file-level comment 供 review |
| `specs/refactors/agent-code-readability/2026-08-08-agent-code-readability-design.md` | 本 spec |
| `CLAUDE.md` → `### darvin-agent Go 代码规范` | Phase A 落地的规范正文(本 spec §3.2 的 rule ID + 完整反例 / 例外) |

### 6.2 改动文件(按 phase 分组)

**Phase A**(1 文件,新增):见上。

**Phase B**(格式化 + 注释合规 + 全量 lint,~393 文件含全包覆盖):
- B-0(30 文件):`gofmt -s -w .` 落地
- B-1(21 文件):删 `Phase N` / `FR-N` / `D-N` / `Reasonix` 注释 + 散文复述
- B-2(20 文件,303 行):中文注释翻译为英文
- B-3(~40 核心文件):注释密度降到 0.30 以下(豁免清单见 §4 Phase B-3)
- B-4(24 文件):`goimports -w -local darvin-cowork/ .` 修正 import 分组
- B-5(393 文件):补 file-level package comment(F3)
- B-6(~262 处):补 exported godoc(ST1020+)
- B-7(聚合收尾):`golangci-lint run ./...` 产出 baseline
- 涉及范围:全包(非测试 ~390 文件 + 测试 ~80 文件)

**Phase C**(god file 拆分,7 个源文件 + 对应测试):
- `internal/gateway/handlers.go` → 8+ 个 `handler_<domain>.go`
- `internal/mcp/registry.go` → 3 个文件
- `internal/agents/ctxengine/compact.go` → 2 个文件 + `prompts.go`
- `internal/agents/agent.go` → 3 个文件
- `internal/harness/types.go` → **按 F5 拆,文件命名按职责**(如 `capabilities.go` / `execution.go` / `support.go` / `usage.go`),禁止新建 `types.go` 这种语法元素命名的文件;若该包内容本质是跨包共享契约,可考虑挪到 `harness/protocol/` 子包(对标 `agents/protocol/`),实施时决定
- `internal/mcp/transport/stdio.go` → 2 个文件
- `internal/agents/executor/executor.go` → 3 个文件

**Phase D**(子包合并,删 6 个子包 + 父包新增 7 文件 + 改反向 import):

D-1:`agents/` 下合并 4 子包
- 删 `internal/agents/runtime/` → 新增 `internal/agents/runtime_state.go`
- 删 `internal/agents/msgid/` → 新增 `internal/agents/msgid_bridge.go`
- 删 `internal/agents/queue/` → 新增 `internal/agents/queue.go`
- 删 `internal/agents/usage/` → 新增 `internal/agents/usage_tracker.go`

D-2:`harness/` 下合并 2 子包
- 删 `internal/harness/plugin/` → 新增 `internal/harness/plugin.go`
- 删 `internal/harness/tooldridge/` → 新增 `internal/harness/bridge.go` + `internal/harness/middleware.go`

D-3:改反向 import(`agents/agent.go` / `agents/dispatcher.go` / `gateway/handlers.go` / `agentloop/loop.go` / `runtime/runtime.go` 等所有引用点)

**Phase E**(本地工具链,不接入 CI):
- 新增 `src/darvin-agent/.golangci.yml`
- 改 `Makefile`(加 `lint` / `fmt` / `vet` / `check` target)
- 新增 `scripts/check-agent-readability.sh`(§7.1 验收脚本)

**Phase F**(可选,能力点 register 化,只针对 `tools` 包):
- 改 `internal/tools/registry.go`(加 `BuiltinConfig` / `BuiltinFactory` / `RegisterBuiltinFactory` / `RegisteredBuiltinFactories`)
- 改 `internal/tools/builtins.go`(`NewBuiltins` 内部改为遍历 factory,签名不变)
- 改 `internal/tools/sandbox.go`(`fsSandbox` → `Sandbox` 导出)
- 改 `internal/tools/fs.go`(4 个 tool 各加 `func init()`)
- 改 `internal/tools/shell.go`(shell tool 加 `func init()`)

### 6.3 不改动文件

- 所有 `*_test.go` 的**测试逻辑**(只跟随生产代码做机械拆分)
- `cmd/app/main.go`(由 `main-thin-runtime-build` spec 处理)
- TS / Vue / Electron 主进程代码
- `docs/` / `*.md`(本 spec 不新增 docs/ 文档)

---

## 7. 验收标准

### 7.1 量化指标(脚本可校验)

新增脚本 `scripts/check-agent-readability.sh`(本 spec 一并交付):

```bash
#!/usr/bin/env bash
# 用法: scripts/check-agent-readability.sh
# 失败即非零退出
set -euo pipefail
cd src/darvin-agent

fail=0

# 1. 单文件软上限 800 行
over=$(find internal -name "*.go" ! -name "*_test.go" -exec wc -l {} \; \
       | awk '$1 > 800 { print }')
[ -z "$over" ] || { echo "Files over 800 lines:"; echo "$over"; fail=1; }

# 2. 注释/代码比 < 0.30(白名单文件单独放过)
for f in $(find internal -name "*.go" ! -name "*_test.go"); do
  total=$(wc -l < "$f")
  comments=$(grep -c "^[[:space:]]*//" "$f" || true)
  blank=$(grep -c "^$" "$f" || true)
  code=$((total - comments - blank))
  ratio=$(awk "BEGIN{printf \"%.2f\", $comments/($code+1)}")
  if (( $(awk "BEGIN{print ($ratio > 0.30)}") )); then
    echo "$f ratio=$ratio (code=$code comments=$comments)"
  fi
done

# 3. 违规注释模式
for pat in 'Phase [0-9]' 'FR-[0-9]' 'Reasonix' 'D[0-9]+ '; do
  hits=$(grep -rnE "$pat" --include="*.go" . | wc -l)
  [ "$hits" -eq 0 ] || { echo "Pattern '$pat' still appears $hits times"; fail=1; }
done

# 4. 格式硬门
gofmt -l . | grep -q . && { echo "gofmt diff detected"; fail=1; } || true
goimports -l . | grep -q . && { echo "goimports diff detected"; fail=1; } || true
go vet ./... || fail=1

# 5. F3 文件级 package comment(F3 缺失文件数应为 0)
missing_pkg_comment=$(find internal cmd -name "*.go" ! -name "*_test.go" -exec awk '
  /^[[:space:]]*\/\// {
    line=$0
    if (line !~ /\+build/ && line !~ /\/go:/ && line !~ /^\/\/ Code generated/) next
  }
  /^package / { print FILENAME; nextfile }
  /^[[:space:]]*$/ { next }
' {} \; | wc -l)
[ "$missing_pkg_comment" -eq 0 ] || { echo "F3 missing in $missing_pkg_comment files"; fail=1; }

# 6. ST10xx 注释 / 命名硬约束(全包 0 告警)
staticcheck -checks 'ST10*' ./... 2>&1 | grep -q . && {
  echo "staticcheck ST10* violations:"
  staticcheck -checks 'ST10*' ./...
  fail=1
} || true

# 7. 聚合 lint(baseline 比对:不允许新增告警)
if [ -f .golangci-baseline.txt ]; then
  golangci-lint run ./... --out-format line-number > /tmp/current-lint.txt || true
  new_violations=$(comm -23 <(sort /tmp/current-lint.txt) <(sort .golangci-baseline.txt) | wc -l)
  [ "$new_violations" -eq 0 ] || { echo "New golangci-lint violations: $new_violations"; fail=1; }
fi

exit $fail
```

### 7.2 checklist

- [ ] Phase A 完成:`CLAUDE.md` → `### darvin-agent Go 代码规范` 子章节落地,`CLAUDE.md` "先读"段更新;**未创建** `src/darvin-agent/AGENTS.md` 与 `docs/darvin-agent-go-style.md`
- [ ] **Phase B-0** 完成:`gofmt -l .` 输出空
- [ ] **Phase B-1** 完成:`grep -rnE 'Phase [0-9]|FR-[0-9]|Reasonix' --include="*.go"` 全部归零
- [ ] **Phase B-2** 完成:20 文件 303 行中文注释全部英文化(或精简删除)
- [ ] **Phase B-3** 完成:核心业务文件注释密度 ≤ 0.30(豁免清单已 review)
- [ ] **Phase B-4** 完成:`goimports -l .` 输出空(24 文件 import 顺序归一)
- [ ] **Phase B-5** 完成:F3 缺失文件数从 393 降到 0(file-level package comment 补全)
- [ ] **Phase B-6** 完成:`staticcheck -checks 'ST10*' ./...` 输出空(262 处 exported godoc 补齐)
- [ ] **Phase B-7** 完成:`golangci-lint run ./...` 跑出 baseline 文件,新 PR 不允许新增同类告警
- [ ] Phase C 完成:无 >800 行非测试文件(豁免清单 PR 中说明)
- [ ] Phase D-1 完成:`agents/{runtime,msgid,queue,usage}` 4 个子包删除,父包新增 4 个 `.go` 文件
- [ ] Phase D-2 完成:`harness/{plugin,tooldridge}` 2 个子包删除,父包新增 3 个 `.go` 文件
- [ ] Phase D-3 完成:所有反向 import(`agents/queue` / `harness/plugin` 等)修改完毕
- [ ] Phase E 完成:`make check`(`gofmt` + `goimports` + `go vet` + `staticcheck ST10*` + `golangci-lint` + `check-readability.sh`)全部通过
- [ ] Phase F(可选)完成:`tools.NewBuiltins` 内部走 factory 注册;`grep -rn '^func init()' internal/tools/` 显示 5+ 个注册点;`RegisteredBuiltinFactories()` 返回 5 个名字
- [ ] 全程 `cd src/darvin-agent && go test ./...` 全绿
- [ ] 全程 `npm run build:agent` 成功(平台二进制正常产出)
- [ ] `npm start` 起 Electron,Go agent 启动抓 `<port>` 行成功,主流程(打开会话、发消息、收事件)跑通
- [ ] 注释/代码比 0.30 以上的文件清单已 review 通过(或调整阈值)

---

## 8. 回滚策略

每个 Phase 是独立 commit。任一 Phase 失败:

1. `git revert <commit-sha>` 回滚该 Phase 的所有 commit。
2. 不影响前一 Phase 的成果。
3. 在本 spec 同目录新建 `2026-MM-DD-agent-code-readability-retry.md` 记录失败原因 + 调整方案。

特别地:
- Phase C god file 拆分**如果 git history 断得不严重可保留**,严重时整体 `git revert`。
- Phase D 子包合并**如果引发 import cycle 或大规模 import 改动**,立即回滚,保留独立子包并在 doc 里说明。

---

## 9. 风险与开放问题

### 9.1 已识别风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| god file 拆分引发 import cycle | 中 | 每个 phase 单独 commit,`go build ./...` 验证 |
| 测试覆盖在拆分中漏移 | 中 | 测试文件强制 1:1 跟随拆分 |
| 注释清理误删上下文 | 中 | 每个文件单独 commit + review |
| 子包合并改 import 影响 main / gateway / runtime wiring | 高 | Phase D 单独做,先 dry-run import 改动 |
| golangci-lint 首次跑出大量既有告警 | 中 | baseline 文件标记存量,本 spec 只 fix 注释/命名/格式类,代码质量类(未使用 / ineffassign)留后续 spec |
| B-5 批量补 file-level comment 措辞机械 | 中 | `gen-pkg-comments.sh` 生成候选,人工 review;允许部分文件用通用模板("Implementation of X for Y package") |
| B-6 给 helper 硬塞 godoc 显冗余 | 中 | godoc 动词开头一句话讲清"做什么";不知描述的 helper 优先考虑重命名或加 `//nolint:staticcheck // reason` |
| B-4 `goimports -w` 可能误改 vendor / generated 文件 | 低 | 跑前 `.gitignore` / `// Code generated` 守卫;若 vendor 目录存在,跑前 `cd internal && goimports -w .` 限定范围 |

### 9.2 决议(已确认)

| # | 议题 | 决议 |
|---|---|---|
| 1 | 注释语言策略 | **统一英文**(C4) |
| 2 | god file 软上限 | **800 行**(F1) |
| 3 | 子包合并范围 | **能合的就合**(§2.3 已扩到 6 个:agents/ 4 个 + harness/ 2 个) |
| 4 | `harness/plugin` / `harness/tooldridge` 合并 | **合并**(§4 Phase D-2 已含) |
| 5 | Phase 执行顺序 | **A → B(0~7)→ C → D → E(→ F 可选)** |
| 6 | CI 接入 | **暂不接入 GitHub Actions**,Phase E 仅产出本地工具(`.golangci.yml` + `Makefile` + `scripts/`)。CI 流程留给未来 spec |
| 7 | `interface + register` 模式 | **引入**(F7)。Phase F 只针对 `tools` 包(`llm` 已合规),用模式 B 工厂注册;`mcp.Transport` / `harness` 暂不动 |
| 8 | 格式统一 + 全量注释 lint | **纳入 Phase B**(B-4 ~ B-7)。B-4 跑 `goimports` 修 24 文件 import 分组;B-5 补 393 文件 F3 file-level package comment;B-6 跑 `staticcheck ST10*` 修 262 处 exported godoc + 命名硬约束;B-7 用 `golangci-lint` 聚合产 baseline |
| 9 | **规范正文落点** | **直接写入 `CLAUDE.md` → `### darvin-agent Go 代码规范` 子章节**(Phase A);**不创建** `src/darvin-agent/AGENTS.md` 与 `docs/darvin-agent-go-style.md`(避免双源维护);本 spec §3.2 / §3.3 改为摘要表 + 变更追踪,不重复规则正文 |

### 9.3 仍待实施时决定的细节

1. `harness/plugin` 合并后 `Manager` 类型是否需要重命名为 `PluginManager` 避免 `harness.Manager` 与可能存在的同包 `Manager` 冲突(实施时检查)
2. `harness/tooldridge.Surface` 类型合并后若与 `harness` 包内已有 `Surface` 重名,重命名实施时定
3. `agents/runtime.Controller` 合并后改名 `RunController`,具体改 `runtime_state.go` 内 + 所有引用点
4. god file 拆分时若发现 helper 跨多个业务域共用,放 `<pkg>_shared.go` 文件,顶部注释声明跨域共享
