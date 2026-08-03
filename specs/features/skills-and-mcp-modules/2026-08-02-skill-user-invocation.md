# Sub-spec 39 — Skill User Invocation（`/skill-name`）

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：用户在 chat 框输入 `/skill-name args` 显式触发 skill 的能力。**不包含** skills / mcp 本身（spec 31-38）。
>
> 创建日期：2026-08-02
> 状态：✅ 已完成
> 前置：[spec 38 tool-registry-merge-and-routing](./2026-08-02-tool-registry-merge-and-routing.md) + [spec 32 skills-ipc-and-bootstrap](./2026-08-02-skills-ipc-and-bootstrap.md)

---

## 1. 概述

### 1.1 问题 / 背景

OpenClaw / Claude Code 等都支持 `/skill-name args` 形式：用户在 chat 框输入 `/` 前缀，触发特定 skill 而不是走 LLM 决策（节省 token + 更可预测）。本 spec 把这个能力落到 darvin-cowork。

参考 Anthropic Claude Code skill 系统的 `/` command 模式（slash command）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Composer 检测 `/` 前缀 → 弹 skill 自动补全列表 | live 验证 |
| G2 | 发送 `/skill-name args` → main 截获 → 调 Go 端 SkillRunner.ExecuteByUserInvocation | live 验证 |
| G3 | SkillRunner 校验 `userInvocable=true` → mini agent loop | live 验证 |
| G4 | 触发后事件流跟普通 prompt 一致（tool_start / tool_end / done / agent_end） | live 验证 |
| G5 | 不带 `/` 前缀的文本不被截获（仅 `/` 开头触发） | live 验证 |
| G6 | i18n 10+ key 齐全（zh + en） | `assertSameKeys` 通过 |

### 1.3 非目标

- 不做 MCP 的 `/` 触发（MCP 工具走 LLM 决策）
- 不做 `/` 的自然语言理解（前缀必须明确 `/skill-name`）
- 不做 slash command 持久化（每次输入都重新触发）
- 不做 slash command 菜单配置

---

## 2. 用户场景

### 场景 1：自动补全

**Given** 用户在 chat 框输入 `/`
**When** 输入框 focus
**Then** 下方弹浮层，列出所有 `userInvocable=true` 的 enabled skill（5 个 bundled skill 全部 userInvocable）

### 场景 2：选择 skill

**Given** 浮层显示 5 个 skill
**When** 用户键入 `/co` → 浮层过滤到 `code-review`
**When** 用户点 [code-review] 或按 Enter
**Then** 输入框内容变为 `/code-review `（自动加空格），光标定位到尾部

### 场景 3：触发 skill

**Given** 输入框内容 `/code-review src/api/handler.go`
**When** 用户按 Enter 发送
**Then**：
1. main 端 IPC `chat:send` 检测到 `/` 前缀
2. 解析：`skillId=code-review`, `args=src/api/handler.go`
3. 调 Go 端 SkillRunner.ExecuteByUserInvocation("code-review", "src/api/handler.go")
4. Go 端校验：skill 存在 + enabled + userInvocable=true → 跑 mini agent loop
5. 事件流：`tool_start { toolKind:'skill', skillId:'code-review' }` → `tool_end` → `done` → `agent_end`
6. renderer 端：跟普通 prompt 一样渲染（tool group + assistant message）

### 场景 4：触发不存在的 skill

**Given** 输入 `/unknown-skill xxx`
**When** 发送
**Then**：
1. Go 端 SkillRunner.ExecuteByUserInvocation 返回 ErrSkillNotFound
2. main → renderer：toast「Skill 不存在：unknown-skill」+ 输入框不变（用户可改正）

### 场景 5：触发 userInvocable=false 的 skill

**Given** 某个 skill `userInvocable=false`（v0 5 个 bundled 都设为 true；测试用）
**When** 输入 `/secret-skill xxx`
**Then** Go 端返回 ErrSkillNotUserInvocable → toast「Skill 不可手动触发」

### 场景 6：禁用的 skill

**Given** `web-search` enabled=false
**When** 输入 `/web-search xxx`
**Then** Go 端返回 ErrSkillDisabled → toast「Skill 已禁用」

### 场景 7：转义 `/` 前缀

**Given** 用户想发文本 `"/skill-name is a library"`（不是触发 skill）
**When** 用户在 chat 框输入 `//skill-name is a library`
**Then**：
1. main 检测 `//` → 视为转义
2. 把首字符 `/` 去掉，文本变为 `/skill-name is a library`
3. 走普通 prompt 流程（不触发 skill）

---

## 3. 功能需求

### FR-1: Composer 自动补全

```vue
<!-- src/renderer/components/chat/Composer.vue 增量 -->
<template>
  <div class="composer">
    <textarea v-model="text"
              @input="onInput"
              @keydown="onKeydown"
              ... />

    <!-- 自动补全浮层 -->
    <div v-if="showSlashMenu" class="absolute bottom-full mb-2 left-0 w-64 rounded-lg border border-border bg-surface shadow-lg">
      <div v-for="skill in matchedSkills" :key="skill.id"
           :class="['px-3 py-2 cursor-pointer text-sm', selectedIndex === $index ? 'bg-primary-muted' : 'hover:bg-surface-hover']"
           @click="selectSkill(skill)">
        <div class="font-medium">{{ skill.name }}</div>
        <div class="text-[11px] text-text-subtle truncate">{{ skill.description }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useSkills } from '../../composables/useSkills';

const { skills } = useSkills();
const text = ref('');
const showSlashMenu = ref(false);
const selectedIndex = ref(0);

const matchedSkills = computed(() => {
  if (!showSlashMenu.value) return [];
  const filter = text.value.startsWith('/') ? text.value.slice(1).split(' ')[0] : '';
  return skills.value
    .filter(s => s.enabled && s.userInvocable)
    .filter(s => !filter || s.id.startsWith(filter.toLowerCase()))
    .slice(0, 8);
});

function onInput() {
  showSlashMenu.value = text.value.startsWith('/') && !text.value.includes('\n');
}

function onKeydown(e: KeyboardEvent) {
  if (!showSlashMenu.value) return;
  if (e.key === 'ArrowDown') { e.preventDefault(); selectedIndex.value = (selectedIndex.value + 1) % matchedSkills.value.length; }
  else if (e.key === 'ArrowUp') { e.preventDefault(); selectedIndex.value = (selectedIndex.value - 1 + matchedSkills.value.length) % matchedSkills.value.length; }
  else if (e.key === 'Enter' || e.key === 'Tab') {
    if (matchedSkills.value.length > 0) {
      e.preventDefault();
      selectSkill(matchedSkills.value[selectedIndex.value]);
    }
  } else if (e.key === 'Escape') {
    showSlashMenu.value = false;
  }
}

function selectSkill(skill: DarvinSkillSummary) {
  text.value = `/${skill.id} `;
  showSlashMenu.value = false;
}
</script>
```

### FR-2: DarvinSkillSummary 加 `userInvocable` 字段

```typescript
// src/shared/darvin-api.ts 增量
export interface DarvinSkillSummary {
  // ... 已有字段
  userInvocable: boolean;  // 来自 SKILL.md frontmatter invocation.userInvocable
}
```

### FR-3: main 端 `/` 前缀截获

```typescript
// src/main/index.ts 增量（chat:send handler）
ipcMain.handle('chat:send', async (_event, { sessionId, content, attachments }) => {
  // 检测 / 前缀
  if (content.startsWith('//')) {
    // 转义：去掉首字符 /
    content = content.slice(1);
    // 走普通 prompt
    await agentClient.sendPrompt({ sessionId, content, attachments });
    return;
  }

  if (content.startsWith('/')) {
    // 解析 /skill-name args
    const match = content.match(/^\/([a-z0-9][a-z0-9-]{0,63})\s*(.*)$/);
    if (!match) {
      // 不是合法 skill name，按普通 prompt 处理
      await agentClient.sendPrompt({ sessionId, content, attachments });
      return;
    }
    const [, skillId, args] = match;

    try {
      await agentClient.invokeSkill({ sessionId, skillId, args });
    } catch (e) {
      const error = e as Error;
      mainWindow.webContents.send('chat:error', {
        sessionId,
        code: error.code || 'UNKNOWN',
        message: translateError(error),  // 错误码 → i18n 文案
      });
    }
    return;
  }

  // 普通 prompt
  await agentClient.sendPrompt({ sessionId, content, attachments });
});

function translateError(err: Error): string {
  switch (err.message) {
    case 'skill not found': return 'Skill 不存在';
    case 'skill disabled': return 'Skill 已禁用';
    case 'skill not user invocable': return 'Skill 不可手动触发';
    default: return err.message;
  }
}
```

### FR-4: agentClient.invokeSkill

```typescript
// src/main/runtime/client.ts 增量
class AgentClient {
  // ... 已有
  invokeSkill(req: { sessionId: string; skillId: string; args: string }): Promise<void> {
    return this.request<void>('agent.skill.invoke_user', req);
  }
}
```

### FR-5: Go 端 handler

```go
// internal/gateway/handlers.go 增量
h.handle("agent.skill.invoke_user", func(params json.RawMessage) (any, error) {
    var req struct {
        SessionID string `json:"sessionId"`
        SkillID   string `json:"skillId"`
        Args      string `json:"args"`
    }
    if err := json.Unmarshal(params, &req); err != nil { return nil, err }

    // 异步跑 mini agent loop
    go func() {
        ctx := context.Background()
        sec, err := h.Skills.Runner.ExecuteByUserInvocation(ctx, req.SkillID, req.Args)
        if err != nil { h.notifySkillError(req.SessionID, req.SkillID, err); return }

        // 复用 Agent.Run 走 mini loop
        if err := h.Agent.RunSkillSession(ctx, req.SessionID, sec); err != nil {
            h.notifySkillError(req.SessionID, req.SkillID, err)
        }
    }()

    return map[string]any{"ok": true}, nil
})
```

### FR-6: Agent.RunSkillSession（mini agent loop）

```go
// internal/agent/agent.go 增量
func (a *Agent) RunSkillSession(ctx context.Context, sessionID string, sec *skills.SkillExecutionContext) error {
    // 复用现有 RunConversation 但只跑 skill 的工具集 + system prompt
    return a.runMiniConversation(ctx, sessionID, sec)
}

func (a *Agent) runMiniConversation(ctx context.Context, sessionID string, sec *skills.SkillExecutionContext) error {
    // 跟 RunConversation 类似，但只 inject skill 的工具 + system prompt
    // emit ToolStart / ToolEnd / Done / AgentEnd 事件
    // ...
}
```

### FR-7: SkillRunner ExecuteByUserInvocation

```go
// internal/skills/runner.go（已在 spec 31 定义，本 spec 确认用法）
func (r *SkillRunner) ExecuteByUserInvocation(ctx context.Context, id string, args string) (*SkillExecutionContext, error) {
    entry, ok := r.reg.Get(id)
    if !ok { return nil, ErrSkillNotFound }
    if !entry.Enabled { return nil, ErrSkillDisabled }
    if !entry.UserInvocable { return nil, ErrSkillNotUserInvocable }
    return &SkillExecutionContext{
        Skill:        entry,
        SystemPrompt: entry.Prompt,
        Args:         args,
        Tools:        r.toolReg.ListForSkill(id),
        StartedAt:    time.Now(),
    }, nil
}
```

### FR-8: i18n 新增 key（~10 个）

| Key | 中文 | 英文 |
|-----|------|------|
| `slash.menu.empty` | 没有可用的 skill | No skills available |
| `slash.menu.title` | 技能（按 Enter 触发） | Skills (press Enter to trigger) |
| `slash.error.not_found` | Skill 不存在：{id} | Skill not found: {id} |
| `slash.error.disabled` | Skill 已禁用：{id} | Skill disabled: {id} |
| `slash.error.not_user_invocable` | Skill 不可手动触发：{id} | Skill not user-invocable: {id} |
| `slash.error.unknown` | 触发失败：{error} | Failed to invoke: {error} |
| `slash.invoked.title` | 触发 skill：{name} | Invoked skill: {name} |
| `slash.invoked.args` | 参数：{args} | Args: {args} |
| `skill.user_invocation.disabled_hint` | 该 skill 不可手动触发 | This skill cannot be manually invoked |
| `composer.placeholder.slash_hint` | 输入 `/` 查看可用 skill | Type `/` to see available skills |

---

## 4. 实现方案

### 4.1 文件清单

```
src/renderer/
├── components/chat/
│   └── Composer.vue                  改造：自动补全浮层
├── composables/
│   └── useSkills.ts                  改造：暴露 userInvocable 字段
├── services/
│   └── i18n.ts                       +10 key
└── views/
    └── ChatView.vue                  不动（Composer 改造自动生效）

src/main/
├── index.ts                          改造：chat:send handler 检测 / 前缀
└── runtime/client.ts                 +invokeSkill

src/shared/
└── darvin-api.ts                     +DarvinSkillSummary.userInvocable 字段

src/darvin-agent/
├── internal/gateway/
│   └── handlers.go                   +agent.skill.invoke_user handler
├── internal/agent/
│   ├── agent.go                      +RunSkillSession
│   └── agent_mini_loop.go            🆕 runMiniConversation
└── internal/skills/
    └── runner.go                     （已在 spec 31 落地）
```

### 4.2 关键代码片段（见 FR-1 ~ FR-7）

### 4.3 关键决策与理由

#### 4.3.1 `/` 前缀拦截在 main 端 IPC 层

**理由**：不依赖 LLM；确定性路由；节省 token；用户输入即触发。

#### 4.3.2 `//` 转义保留 `/` 文本

**理由**：用户想聊 `/skill-name` 这个字符串时不需要真正触发；双 `/` 是惯例。

#### 4.3.3 mini agent loop 复用 Agent.RunConversation

**理由**：复用 executor + event bus + tool registry；只换 system prompt + tool list。

#### 4.3.4 userInvocable 校验在 Go 端

**理由**：不可信；main 端校验能被绕过。Go 端是 source of truth。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `Composer.test.ts` | `/` 触发浮层 / 过滤 / Enter 选中 / Escape 关闭 |
| `index.test.ts`（main） | `/` 截获 / `//` 转义 / 调 agent.invokeSkill |
| `handlers_test.go`（Go） | agent.skill.invoke_user 触发 mini loop + emit 事件 |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `/` 后立刻按 Enter（无 skill 名） | 走普通 prompt（content="/"） |
| `/unknown-skill` | Go 端返回 ErrSkillNotFound → toast |
| `/code-review` 不带 args | args="" → mini loop 跑 + 用空 args |
| 多行 `/` 前缀（textarea 多行输入） | 检测首行首字符是 `/`；多行不触发 |
| session 不存在 | Agent.RunSkillSession 返回 error → main toast |
| mini loop 超时 | context cancel + emit AgentEnd with error |
| skill 在 mini loop 中再次触发 skill | 不允许嵌套（RunSkillSession 不接受 skill invocation） |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/renderer/components/chat/Composer.vue` | 改造：自动补全浮层 |
| `src/renderer/composables/useSkills.ts` | 改造：userInvocable 字段 |
| `src/renderer/services/i18n.ts` | +10 key |
| `src/main/index.ts` | 改造：`chat:send` handler |
| `src/main/runtime/client.ts` | +`invokeSkill` |
| `src/shared/darvin-api.ts` | +`DarvinSkillSummary.userInvocable` |
| `src/darvin-agent/internal/gateway/handlers.go` | +`agent.skill.invoke_user` |
| `src/darvin-agent/internal/agent/agent.go` | +`RunSkillSession` |
| `src/darvin-agent/internal/agent/agent_mini_loop.go` | 🆕 |

---

## 7. 验收标准

**通用**：
- [x] `npm run lint` 通过；`npm run test` 通过（25 个 mcpManager/mcpStore 失败为 better-sqlite3 ABI 预存环境问题，与本次改动无关；其余 194 通过）
- [x] `cd src/darvin-agent && go build ./...` 编译通过
- [x] `go vet ./...` 无警告

**FR-1 Composer 自动补全**：
- [x] `/` 触发浮层（实现 + 单测；live 待验证）
- [x] 键入 `/co` 过滤到 code-review（实现；live 待验证）
- [x] ArrowDown/Up 切换选择（实现；live 待验证）
- [x] Enter / Tab 选中（实现；live 待验证）
- [x] Escape 关闭（实现；live 待验证）

**FR-2 userInvocable 字段**：
- [x] `DarvinSkillSummary.userInvocable` 暴露（Go wire + TS 类型 + main skillManager）

**FR-3 main `/` 截获**：
- [x] `/skill-name args` 走 invokeSkill（适配：路由在 renderer `useChatActions.send`，main 暴露 `darvin:invoke_skill` IPC）
- [x] `//xxx` 走普通 prompt（escape 去首字符）
- [x] `/xxx` 不是合法 skill 名走普通 prompt（parseSlashCommand 返 null）

**FR-4 agentClient.invokeSkill**：
- [x] 调 `agent.skill.invoke_user`

**FR-5 Go handler**：
- [x] `agent.skill.invoke_user` 校验 skillId + args + 异步跑 mini loop

**FR-6 Agent.RunSkillSession**：
- [x] 跑 mini loop + emit 事件（复用 RunConversation；skill prompt + scoped tool registry）

**FR-7 SkillRunner**：
- [x] ExecuteByUserInvocation 校验 3 个条件（spec 31 已落地 + 测试）

**FR-8 i18n**：
- [x] 10+ key 齐全（zh/en 各 +10，assertSameKeys 通过）

**live 验证**（CDP 实跑通过）：
- [x] 输入 `/` → 浮层显示 5 个 skill（code-review / api-design / testing / web-search / docx）
- [x] 输入 `/co` → 浮层过滤到 code-review
- [x] Enter → 输入框变为 `/code-review `
- [x] 输入 args → Enter 发送（`/code-review src/api/handler.go` 触发 mini loop，assistant 流式 + Bash/read_file 工具组 + token 统计，跟普通 prompt 一致）
- [x] 观察事件流：tool_start + tool_end + done + agent_end（turn 正常收尾）
- [x] 输入 `/unknown-skill xxx` → toast「Skill 不存在：unknown-skill」（Go 端 ErrSkillNotFound → RPC error → i18n toast）
- [x] 输入 `//skill-name is a library` → 普通 prompt（不触发，气泡保留原始 `//` 文本，无 toast）

**live 中发现并修复的 bug**：选中 skill 后继续输入 args（`/code-review src/api`）时，浮层仍因 `/` 前缀保持打开，Enter 会重复选中而非发送——`onInput` 加 `!text.includes(' ')` 条件（出现空格即收浮层）后修复。

---

## 8. 与其他 spec 的关系

**前置**：
- spec 31 + 32（skills 完整可用）
- spec 38（tool registry 合并）

**下游**：无（这是 skills + mcp 模块的最后一份子 spec）

**并行**：spec 33 + 36 + 37

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现
- 2026-08-03 · spec 39 落地：**Go**——`SkillSummaryWire` +`userInvocable`（ToSummary 填充）+ `Agent.RunSkillSession`（新文件 `agent_mini_loop.go`：临时覆盖 `runSkillPrompt`/`runSkillTools`，Instructions()/Tools() 按 transient 返回 skill 上下文；`buildSkillTools` 从 full registry 投影 skill 允许工具集并保留 Kind/Metadata，空集给空 registry）+ acp.Loop `SubmitSkill`（promptReq 带 skill 字段，executeTurn 分支跑 RunSkillSession）+ gateway `agent.skill.invoke_user`（同步校验存在/enabled/userInvocable → 三个新错误码 -32010/-32011/-32012；通过后提交 Loop 异步跑）+ Handler 注入 `SkillRunner` + 3 个错误码 + main.go 接线。**TS**——`DarvinSkillSummary.userInvocable` + `DarvinInvokeSkillRequest/Response` + `client.invokeSkill` + preload `invokeSkill` + main `darvin:invoke_skill` IPC（mint runId → 注册 currentRunIdBySessionId → 返回 prompt 同形结果）。**Renderer**——Composer `/` 自动补全浮层（过滤 enabled+userInvocable / 键盘导航 / Enter·Tab 选中 / Escape 关闭）+ `useChatActions.send/regenerate` `/` 路由（`//` 转义去首字符走普通 prompt；`/skill args` → invokeSkill；失败 toast 不画错误气泡）+ `slash.ts` 纯函数 helpers（parseSlashCommand / translateSkillError）+ i18n +10 key。**bundled `testing` SKILL.md 补 `invocation.userInvocable: true`**（否则 5 个 bundled 只有 4 个可手动触发，与场景 1 冲突）；main BUNDLED_SKILLS + user 目录默认（未声明即 false，与 Go loader 一致）。**测试**：+3 Go（RunSkillSession 作用域断言 / buildSkillTools 保 kind / 空工具集）+ 7 Go handler（未配置 / 不存在 / 禁用 / 不可触发 / 成功 ticket / 缺 skillId / err 透传）+ 2 wire（userInvocable 序列化 / 默认 false）+ 10 TS slash helpers。**验证**：`go build`/`go vet`/`go test ./...` 17 包全绿，`npm run lint` 干净，`npm run build:agent` 成功，vitest 194 通过（25 失败为 better-sqlite3 ABI 预存问题）。**适配说明**：spec FR-3 假设的 `chat:send` IPC 不存在——实际 prompt 走 renderer `useChatActions.send` → `darvin:prompt`；截获逻辑落在 renderer send 层（应用真正的发送入口），main 只暴露 invoke_skill 通道。**待 live**：Electron 重启后验证浮层 / 触发 / 事件流 / toast / 转义。