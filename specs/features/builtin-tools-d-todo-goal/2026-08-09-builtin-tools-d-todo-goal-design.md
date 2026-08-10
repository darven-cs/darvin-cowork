# 内置工具补齐 D 组:todo / goal(todo_write + complete_step,update_goal 挂起)设计文档

## 1. 概述

### 1.1 问题 / 动机

对比参考项目 DeepSeek-Reasonix,当前模型缺少结构化任务跟踪能力。参考实现有 3 个相关工具:

- `todo_write` — 记录 / 更新当前工作的结构化任务清单。
- `complete_step` — 带证据地签收(正式 sign-off)计划中的某个已完成步骤。
- `update_goal` — 每轮回报目标 turn 的处置(continue / complete / blocked),由 host FSM 校验并决定是否自动续跑。

其中 `update_goal` 依赖参考项目的 goal-turn FSM(接受标准、自动续跑、Delivery 就绪检查),当前项目没有这套 host 机制(sessionruntime 是单 prompt 驱动,无 autonomous-continue 循环),**本 spec 不实现 update_goal**,只落地 `todo_write` 与 `complete_step`,并给出 `update_goal` 落地所需的前置条件(见 §7)。

### 1.2 目标

1. 新增 `todo_write`:`args 即状态`——模型每次调用携带完整清单,工具只做 schema 校验 + 数量确认,无 host 副作用;清单保存在对话历史(工具调用消息)里,渲染层可从工具参数渲染清单。
2. 新增 `complete_step`:带证据的步骤签收;无证据的完成声明被拒绝(防止模型空口把步骤标成 done)。
3. 两个工具走现有 `RegisterBuiltinFactory` / `BuiltinConfig` / `validateArgs` 机制,零架构改动。

### 1.3 非目标

- 不做 `update_goal`(缺 host goal-turn FSM,见 §7)。
- 不做 host 端 todo 持久化(清单是对话上下文的一部分,不落库)。
- 不做渲染层 todo 面板(Go 侧工具先行,渲染面板作为后续项,§7)。
- 不做跨 session 的任务清单共享。

## 2. 现状分析

| 现状 | 说明 |
|---|---|
| `Tool.Execute(ctx, args)` | protocol.Tool,返回 `Result{Content, IsError}`;工具调用消息进对话历史,模型跨轮可见 |
| `validateArgs` | params.go,arg 校验(required / enum / 数组 items / unknown 硬拒绝) |
| dispatcher | 工具调用以 `tool_start` / `tool_end` 事件经 gateway 推到 renderer,`c.Arguments` 可被前端消费 |
| 权限门 | `ClassifyPermission` / `pathEscape`:`shell` 走命令危险判定,文件工具走路径判定;todo 工具不触碰文件,分类 safe |

参考实现:
- `todo_write`(217 行):无 host 副作用,完整清单活在调用 args 里,前端渲染为 checklist;工具只校验 shape 并以 count 回执。两级结构:`level` 0 是 PHASE(里程碑),`level` 1 是子步骤;每项 `content`(命令式)/ `status`(pending|in_progress|completed)/ `activeForm`(进行时态)/ `level`。
- `complete_step`(328 行):`stepEvidence`(kind + 描述)签名式签收;无证据的完成被 `Execute` 拒绝。同样无 host 副作用,签名状态在 args 里,前端渲染为 sign-off 步骤。

## 3. 方案设计

### 3.0 文件组织(F2 按业务域)

新增 `internal/tools/todo.go`,域:任务清单 / 步骤签收,内含 `todo_write` + `complete_step` 两个工具及共享的清单校验 helper。

### 3.1 todo_write

参数(schema):
- `todos`(array,必填,0..50 项):每项
  - `content`(string,必填):命令式描述("Add the parser")。
  - `status`(string,必填,enum `pending|in_progress|completed`)。
  - `activeForm`(string,可选):进行时态,仅 `in_progress` 项填写("Adding the parser")。
  - `level`(integer,可选,0|1):0 = PHASE,1 = 子步骤;缺省按平铺。

实现(严格对齐参考"无 host 副作用"):
- `validateArgs` 过 schema 只覆盖**顶层形状**(`todos` 必填且为 array);`protocol.ParameterProperty` 不支持嵌套 object 的 `properties`,所以数组项字段(content 必填 / `status` enum / `level`∈{0,1})与跨项不变量(in_progress ≤1、completed 禁 activeForm、level=1 在 level=0 之后)全部在 `Execute` 手动校验。
- 通过则返回 `Result{Content: fmt.Sprintf("todo list updated: %d items (%d completed)", n, done)}`;不通过返回 `IsError` 文案说明违规项(如 "at most one item may be in_progress")。
- **不存储**:清单以工具调用消息形式留在对话历史,模型下一轮天然可见;工具每次收到完整清单,覆盖前一份。

权限:`ClassifyPermission` 返回 safe(不触碰文件/网络),无 `pathEscape` 分支。

### 3.2 complete_step

参数(schema):
- `step_id`(integer,必填):计划中步骤的序号(对应 todo 列表顺序)。
- `content`(string,必填):该步骤的标题(回显,便于前端对账)。
- `evidence`(array,必填,1..5 项):每项
  - `kind`(string,enum `verification|diff|test|file|manual`):证据类型。
  - `description`(string,必填):证据描述(跑了什么验证、改了哪些文件、手检了什么)。

实现(证据强制,对齐参考):
- `validateArgs` 只校验顶层(`step_id` / `content` / `evidence` 必填且类型正确);`evidence` 非空、长度上限、逐项 `kind` enum 与 `description` 非空在 `Execute` 手动校验(validateArgs 不深入数组项,也不支持 minItems / maxItems)。
- `evidence` 为空 → 拒绝:`"complete_step requires at least one evidence item"`(防止模型无依据标 done)。
- 每项 `kind` 必须命中 enum;`description` 非空。
- 通过则回执 `"step N \"<content>\" signed off with M evidence item(s)"`。
- 无 host 副作用,同 `todo_write`:签名状态在 args 里,前端据此渲染 sign-off 徽标。

权限:同 `todo_write`,safe。

### 3.3 与对话历史的关系(关键设计)

两个工具都是 **stateless(状态在 args)**,这是刻意选择:
- 模型每次调用都重发完整清单 / 完整证据,工具只做校验 + 回执。
- 清单随对话历史滚动压缩(ctxengine compact / archive 时会随消息一起折叠),不额外落库,不引入新存储模型。
- 前端若要做 todo 面板,从 `tool_start` 事件的 `c.Arguments.todos` 渲染即可(§7 后续项)。

### 3.4 配置

不新增配置。两个工具无条件注册(属模型自跟踪能力,不触碰任何外部资源)。

## 4. 实施步骤

1. 新建 `internal/tools/todo.go`:定义 `todoItem` / `stepEvidence` 结构 + `todoWriteTool` + `completeStepTool`。
2. `todo_write`:`Parameters()` schema(顶层)+ `Execute` 校验(数组项字段 + in_progress ≤1、completed 禁 activeForm、level 层级)+ count 回执。
3. `complete_step`:`Parameters()` schema(顶层)+ `Execute` 证据强制校验(非空 / 上限 / kind enum / description 非空)+ 回执。
4. 两个工具各 `init()` 注册 `RegisterBuiltinFactory`。
5. 补 `todo_test.go`,跑 §5 验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/tools/todo.go` | 新增,`todo_write` + `complete_step` |
| `internal/tools/todo_test.go` | 新增,清单校验 / 证据强制单测 |

## 6. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告;`staticcheck -checks 'ST10*' ./...` 零告警。
3. `golangci-lint run ./...` 零告警(相对 baseline 不新增)。
4. `go test ./...` 全绿。`todo_test.go` 覆盖:todo_write 合法清单回执、双 in_progress 拒绝、completed+activeForm 拒绝、level 层级违规、complete_step 空证据拒绝、合法证据回执、enum 外 kind 拒绝。
5. `bash scripts/check-agent-readability.sh` 不新增超密度 / 违规文件。

## 7. update_goal 落地前置(本 spec 明确不实现)

`update_goal`(continue / complete / blocked 处置回报)必须有 host FSM 才有意义,当前项目缺三件事,缺任一件就只能是空壳:

1. **goal 接受标准 / Delivery 就绪校验**:host 要能判断"complete"声明是否满足请求的输出格式与约束。
2. **autonomous-continue 循环**:sessionruntime 目前是单 prompt → 自然停止;`update_goal(status=continue)` 需要 loop 支持在回合结束后依据 disposition 自动发起下一轮。
3. **goal 状态机**:blocked / complete 的状态转移与 UI 呈现。

建议:等未来的"goal mode"功能(host 级目标驱动循环)立项时,再与 `update_goal` 一并设计,把 disposition 校验接进 FSM。当前不为了"凑齐工具面"而发布一个不校验、不驱动任何流程的占位工具。

## 8. 渲染层后续(不在本 spec)

- 前端 `TodoPanel`:订阅 `tool_start` 事件的 `todos` 参数,渲染两阶段 checklist 与 `complete_step` 的 sign-off 徽标。
- 清单随会话切换 / 历史回放可见(从消息历史读取最后一次 `todo_write` 调用)。

## 9. 更新记录

- 2026-08-10:对照源码修正三处 —
  1. `agentloop` 包已改名为 `sessionruntime`(见 `specs/refactors/session-runtime-package/`),§1.1 / §7 措辞同步更新。
  2. 补充 `validateArgs` 的边界:`protocol.ParameterProperty`(agents/protocol/types.go:58)不支持嵌套 object 的 `properties`,数组项字段与 minItems / maxItems 校验都落在 `Execute` 手动做(§3.1 / §3.2)。
  3. §4 实施步骤措辞同步(区分顶层 schema 校验与项级 / 跨项业务校验)。
