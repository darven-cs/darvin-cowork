# 系统提示词设计（System Prompt）迁移文档

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 目前**没有产品级的系统提示词设计**，只有运行时装配机制。对照 LobsterAI（`docs/agent/*` + `src/main/presetAgents.ts` + `src/renderer/components/cowork/skillSystemPrompt.ts`），差异如下（均以本仓库源码为准核对）：

| 维度 | LobsterAI | darvin-cowork 现状 |
|------|-----------|--------------------|
| Agent 级人设 `identity` | ✅ 双语预设字段 | ❌ 无 |
| Agent 级能力 `systemPrompt` | ✅ 双语预设字段 | ❌ 无 |
| Session 级 `system_prompt` | ✅ DB 列 + 协议字段 | ❌ Session / 协议都无字段 |
| 编辑 UI | ✅ AgentCreateModal / AgentSettingsPanel | ❌ 无 |
| 运行时拼接 | skillPrompt + systemPrompt + PlanMode 覆盖 | `instructions`（全局空）+ ctxengine SystemSections |
| 动态上下文块 | — | ✅ `<IDENTITY>/<SOUL>/<USER>` + skills/facts/mcp |

具体源码证据：

- `src/darvin-agent/config.yaml`：`agent.instructions: ""` —— 唯一 base system prompt，**全局且为空**。
- `src/darvin-agent/internal/runtime/runtime.go:169`：`Instructions: cfg.Agent.Instructions` —— 所有 session 共用同一全局值。
- `src/darvin-agent/internal/agents/session/session.go:15` `Session` struct：只有 ID/Key/AgentID/Status/时间，**无 SystemPrompt / Identity 字段**。
- `src/darvin-agent/internal/agents/store/models.go:12` `Session`（GORM 行）：**无 system_prompt 列**。
- `src/darvin-agent/internal/gateway/handler_session.go:99` `CreateSessionParams`：只有 `Title`；`:24` `SessionWire`：无 systemPrompt。
- `src/renderer/services/mock-data.ts:44` `ExpertAgent`：只有 name/category/description/color/icon/price，**无 prompt/identity**；`src/renderer/views/ExpertSuiteView.vue:76` `onUse` 只是 `console.log`。

已有可复用的机制（不需要重建）：

- ctxengine 已支持 `SystemSection`（`internal/agents/ctxengine/sections.go`）：优先级槽 `identity=30 / soul=40 / user=60 / available_skills=100 / facts=110 / mcp=120 / addition=1000`，`composeSystemAddition` 按优先级升序拼接。
- executor 已接缝：`executor.go:155` 调 `Assembler().Assemble(...)` 传入 `SystemSections: d.SystemSections()`；`:196` `system := d.Instructions()` + `systemAddition`。
- `Agent.SystemSections()`（`agent.go:416`）目前返回 `nil` —— 正是给 identity 预留的出口。

### 1.2 目标

迁移 LobsterAI 的"会话级系统提示词 + agent 人设"设计到 darvin-cowork：

1. **会话级 systemPrompt / identity 全链路打通**：协议（createSession）→ 存储（GORM 列）→ 运行时（session 字段）→ LLM（`Agent.Instructions()` + ctxengine `<IDENTITY>`）。
2. **专家套件真正可用**：给 `ExpertAgent` 补 `identity + systemPrompt + skillIds`（中英双语），"使用"= 以该 agent 的系统提示词新建会话并跳转聊天。
3. **identity 接入现有 ctxengine `<IDENTITY>` 槽**：复用 `PriorityIdentity` 机制，不另造一套。
4. **保持默认行为不变**：未指定 systemPrompt 的普通会话，最终 system prompt 与现状一致。

### 1.3 非目标

- **不**做 agent 级编辑 UI 的完整 CRUD（新建/编辑/删除 agent、存 DB）。本轮只打通"预设数据 → 会话"单向流；编辑界面留后续 spec。
- **不**把 `skillIds` 接到 skill enable 流程（预设关联 skill 的启用/注入）。本轮只把字段放进预设数据，注入行为不落地。
- **不**实现 Plan Mode 覆盖 prompt（LobsterAI 的 `buildPlanModeSystemPrompt`）。本项目暂无 plan mode，属新增能力而非迁移。
- **不**做会话中途切换 systemPrompt（创建时固化）。
- **不**引入第三套 i18n 库；预设的 name/description 属 UI 文案走 i18n key，identity/systemPrompt 属 LLM 数据走双语字段（见 FR-6 决策说明）。

## 2. 用户场景

### 场景 1：从专家套件使用预设 agent

**Given** 用户打开"专家"页（`ExpertSuiteView`），看到"股票助手"卡片，应用当前语言为中文
**When** 用户点"使用"
**Then**
1. 前端用股票助手的中文 `systemPrompt` + `identity` 调用 `createSession`，标题用 agent 名
2. 新会话成为 active session，AppShell 切到 ChatView
3. 本轮及后续轮次的 LLM system prompt 包含：全局 `agent.instructions`（空则忽略）+ 股票助手 systemPrompt + ctxengine `<IDENTITY>` 块（股票助手 identity）+ 动态 skills/facts/mcp 块

### 场景 2：普通会话行为不变

**Given** 用户直接新建空会话（无预设），systemPrompt / identity 为空
**When** 用户发消息
**Then**
1. `Agent.Instructions()` 返回 `agent.instructions`（现状）
2. `Agent.SystemSections()` 返回 nil（现状）
3. 最终 system prompt 与迁移前逐字节一致

### 场景 3：重启后会话系统提示词恢复

**Given** 用户用"股票助手"预设建了会话 A，发了几轮消息，然后退出应用
**When** 重新启动应用，切回会话 A 继续聊天
**Then**
1. `hydrateSession` 从 `store.Session` 行恢复 SystemPrompt / Identity 到内存 session
2. LLM system prompt 仍包含股票助手的人设与能力提示词（不丢失）

## 3. 功能需求

### FR-1: `session.Session` 增加 SystemPrompt / Identity 字段

`src/darvin-agent/internal/agents/session/session.go:15`：

```go
type Session struct {
	ID           string
	Key          string
	AgentID      string
	Status       Status
	CreatedAt    time.Time
	SystemPrompt string // 会话级系统提示词（预设 agent 的 systemPrompt），空 = 不追加
	Identity     string // 会话级人设（预设 agent 的 identity），空 = 不注入 <IDENTITY>
	mu           sync.RWMutex
	updatedAt    time.Time
	messages     []protocol.Message
}
```

新增方法（供 store / hydrate / handler 使用）：

```go
func (s *Session) SetPrompt(systemPrompt, identity string)  // 覆盖两个字段
func (s *Session) Prompt() (systemPrompt, identity string)  // 读两个字段（RLock）
```

### FR-2: `store.Session`（GORM 行）增加列

`src/darvin-agent/internal/agents/store/models.go:12`：

```go
type Session struct {
	// ... 现有字段
	SystemPrompt string `gorm:"type:text;default:''"`
	Identity     string `gorm:"type:text;default:''"`
}
```

- `SQLiteStore.Save`（`sqlite_store.go:35`）写入 `row.SystemPrompt = sess.SystemPrompt; row.Identity = sess.Identity`。两字段以内存 session 为权威来源，直接赋值；不走 Title / ClaudeSessionID / WorkspaceID 那条 preserve-from-existing 路径（那三列是 handler 持有的渲染元数据，内存 session 不携带）。
- `Session.toSession()`（`sqlite_store.go:101`）恢复时带上两字段（可复用 `SetPrompt`）。
- 新列由 GORM AutoMigrate 自动补：`internal/runtime/database.go:33` 的 `database.AutoMigrate(&store.Session{}, ...)` 已注册 Session 模型，struct 加字段即自动补列，无需 DDL。
- 本轮**不**给 `SessionStore` 接口加 `UpdateSystemPrompt`——创建路径走 `Save` 已覆盖落库；无调用方的接口方法留给编辑 UI 的后续 spec（见决策 6）。

### FR-3: 创建会话协议透传

`src/darvin-agent/internal/gateway/handler_session.go`：

```go
// :103 —— WorkspaceID 为现有字段（workspace-first 会话模型），必须保留
type CreateSessionParams struct {
	Title        string `json:"title,omitempty"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	SystemPrompt string `json:"systemPrompt,omitempty"`
	Identity     string `json:"identity,omitempty"`
}

// :24
type SessionWire struct {
	// ... 现有字段
	SystemPrompt string `json:"systemPrompt,omitempty"`
	Identity     string `json:"identity,omitempty"`
}
```

`handleCreateSession`（`:351`）在 `GetOrCreateEntry` 之后、`SessionStore.Save`（`:373`）之前写入内存 session，随后既有的 Save 把两字段落库（`BindWorkspace` / `UpdateTitle` 逻辑不动）：

```go
entry.Session.SetPrompt(p.SystemPrompt, p.Identity)
// 插入点必须在 h.SessionStore.Save 之前，否则两字段不落库
```

`toSessionWire` / `wireForSession` 输出 `row.SystemPrompt` / `row.Identity`。

### FR-4: `Agent.Instructions()` 组合会话级 systemPrompt

`src/darvin-agent/internal/agents/agent.go:394`：

```go
func (a *Agent) Instructions() string {
	if a.runSkillPrompt != "" {
		return a.runSkillPrompt // skill mini-loop 保持现状，不叠加会话 prompt
	}
	base := a.instructions
	if systemPrompt, _ := a.session.Prompt(); systemPrompt != "" {
		base = strings.TrimSpace(base) + "\n\n" + systemPrompt
	}
	if a.runImportedNote != "" {
		base += "\n\n" + a.runImportedNote
	}
	return base
}
```

统一走 `Prompt()` 访问器（RLock）而非裸读 `a.session.SystemPrompt`：`Instructions()` 在 executor goroutine 调用，`SetPrompt` 在 gateway goroutine（创建 / hydrate）调用，裸字段读是数据竞态。

顺序：全局 instructions 在前，会话级 systemPrompt 在后（更晚出现权重更高）。executor 侧 `executor.go:196` 不用改。

### FR-5: `Agent.SystemSections()` 注入 `<IDENTITY>`

`src/darvin-agent/internal/agents/agent.go:416`：

```go
func (a *Agent) SystemSections() []ctxengine.SystemSection {
	_, identity := a.session.Prompt() // 访问器带 RLock，理由同 FR-4
	if identity == "" {
		return nil
	}
	sec, _ := ctxengine.IdentitySection(identity)
	return []ctxengine.SystemSection{sec}
}
```

`internal/agents/ctxengine/sections.go` 新增导出构造器（复用现有 `renderIdentitySection`，它已处理空值）：

```go
// IdentitySection wraps identity content as the session-level <IDENTITY>
// block. ok=false when content is blank (caller skips the section).
func IdentitySection(content string) (SystemSection, bool)
```

**优先级决策**：session identity 用 `Priority: 31`，落在 workspace 的 `IDENTITY.md`（priority 30）之后、`SOUL.md`（priority 40）之前。理由：workspace 身份与 agent 人设都是 `<IDENTITY>`，二者可并存；稳定排序避免 `sort.SliceStable` 依赖插入序。若未来要区分，可再开独立的 `<PERSONA>` 槽。

### FR-6: hydrate 恢复

`src/darvin-agent/internal/sessionruntime/hydrate.go:28` 函数**开头**追加（`f.Store` 即 `store.SessionStore`，`AgentFactory` 已有该字段，`factory.go:36`）。必须放在 `if f.MessageStore == nil || sess == nil { return }`（`:29`）之前——放函数末尾会被该短路及 message list 失败的提前 return（`:40`）跳过：

```go
if f.Store != nil {
	if row, err := f.Store.GetByID(ctx, sess.ID); err == nil {
		sess.SetPrompt(row.SystemPrompt, row.Identity)
	}
}
```

幂等：新建时 `handleCreateSession` 已写入；hydrate 只是从库恢复确认。

### FR-7: renderer 专家预设数据 + 使用流程

`src/renderer/services/mock-data.ts:44` `ExpertAgent` 扩展（双语，沿用现有 `getLang()`）：

```ts
export interface ExpertAgent {
  id: string;
  name: string;
  category: ExpertCategory;
  description: string;
  color: AgentColor;
  icon: string;
  price: 'Free' | '50 credits/次' | '100 credits/次' | '200 credits/次' | '300 credits/次';
  // 新增 —— 双语，identity / systemPrompt 属 LLM 数据（与 LobsterAI presetAgents 同构）
  nameEn: string;
  descriptionEn: string;
  identity: string;
  identityEn: string;
  systemPrompt: string;
  systemPromptEn: string;
  skillIds: string[];
}
```

`expertSuiteAgents` 9 个条目补齐上述字段（identity / systemPrompt 参照 LobsterAI 的"人设 + 能力 + 原则 + 系统环境"三段式写法）。

`src/renderer/views/ExpertSuiteView.vue:76` `onUse` 接线：

```ts
import { useSession } from '../composables/useSession';
import { useViewMode } from '../composables/useViewMode';
import { getLang } from '../services/i18n';

function onUse(agent: ExpertAgent) {
  const en = getLang() === 'en';
  const name = en ? agent.nameEn : agent.name;
  const systemPrompt = en ? agent.systemPromptEn : agent.systemPrompt;
  const identity = en ? agent.identityEn : agent.identity;
  // workspaceId 不传 → main 侧兜底 activeWorkspaceId（现有行为），新会话落在 active workspace
  void session.createSession(name, undefined, systemPrompt, identity).then(() => {
    viewMode.navigate('chat');
  });
}
```

注意 `useSession.createSession` 现签名为 `(title?, workspaceId?)`（`useSession.ts:75`），本 spec 在其后追加两个参数，`systemPrompt` 落在第三位而不是挤占 workspaceId。

`AgentCard.vue` 的 `name`/`description` 改为 `computed` 按 `getLang()` 取 zh/en；`ExpertSuiteView.vue` 的 `filtered` 搜索匹配（用 `a.name`/`a.description` 比对 query）同样按语言取字段。`AgentFilterTabs` 只渲染分类 tab、不输出 name/description，无需改动。

### FR-8: i18n

- **UI 文案**（卡片外的按钮、搜索占位等）已存在 `expert.*` key（`i18n.ts`），不新增。
- **预设 name/description**：走数据内双语字段（`name`/`nameEn`），与现有 `ExpertAgent` 硬编码中文的做法相比是升级；不进扁平 i18n 字典（9 agent × name/description = 18 key，且 identity/systemPrompt 是 LLM 数据不属于 UI 文案）。若后续要求强一致，可再抽 `expert.preset.<id>.*` key。
- identity/systemPrompt 双语由 `getLang()` 在创建会话时固化到 session（切换语言不影响已建会话）。

## 4. 实现方案

### 4.1 数据流

```
ExpertSuiteView.onUse
  → session.createSession(title, undefined /*workspaceId*/, systemPrompt, identity)
    → window.darvin.createSession({ title, systemPrompt, identity })     // preload（workspaceId 缺省）
      → ipcRenderer.invoke('darvin:create_session', req)                 // preload/index.ts:83
        → ipcMain.handle('darvin:create_session')                        // main/index.ts:489
          （workspaceId 兜底 activeWorkspaceId + followActiveWorkspace + subscribeEvents 现有逻辑不动）
          → client.request('agent.create_session', params)
            → handleCreateSession                                        // gateway/handler_session.go:351
              → entry.Session.SetPrompt(...)                             // 内存 session（须在 Save 前）
              → SessionStore.Save → store.Session(SystemPrompt,Identity) // GORM 落库
                                                                           ↓ (LLM 时)
        executor.RunConversation (executor.go:193)
          → system := d.Instructions()            // = global instructions + session.SystemPrompt
          → system += Assembler().Assemble(...)   // SystemSections 含 <IDENTITY>(priority 31)
```

### 4.2 Go 后端改动点

| 文件 | 改动 |
|------|------|
| `internal/agents/session/session.go` | `Session` 加 `SystemPrompt`/`Identity`；`SetPrompt`/`Prompt` 方法 |
| `internal/agents/store/models.go` | `Session` 加 `SystemPrompt`/`Identity` 列 |
| `internal/agents/store/sqlite_store.go` | `Save` 写两字段；`toSession` 恢复 |
| `internal/gateway/handler_session.go` | `CreateSessionParams`/`SessionWire` 加字段（保留 WorkspaceID）；`handleCreateSession` 写内存 + 落库；`toSessionWire` 输出 |
| `internal/agents/ctxengine/sections.go` | 导出 `IdentitySection(content) (SystemSection, bool)` |
| `internal/agents/agent.go` | `Instructions()` 组合会话 prompt；`SystemSections()` 返回 `<IDENTITY>` |
| `internal/sessionruntime/hydrate.go` | hydrate 开头（MessageStore 短路前）从 store 恢复两字段 |

### 4.3 IPC 链路改动点

| 文件 | 改动 |
|------|------|
| `src/shared/darvin-api.ts` | `DarvinSession` 加 `systemPrompt?`/`identity?`；`createSession(req?: { title?, workspaceId?, systemPrompt?, identity? })`（保留现有 workspaceId） |
| `src/preload/index.ts:83` | `createSession` 透传新字段（workspaceId 保留） |
| `src/main/index.ts:489` | `darvin:create_session` handler 透传新字段；workspaceId 兜底 activeWorkspaceId / followActiveWorkspace / subscribeEvents 现有逻辑不动 |

### 4.4 renderer 改动点

| 文件 | 改动 |
|------|------|
| `src/renderer/services/mock-data.ts` | `ExpertAgent` 加双语 identity/systemPrompt/skillIds；9 个预设补齐 |
| `src/renderer/composables/useSession.ts:75` | `createSession(title?, workspaceId?, systemPrompt?, identity?)` 追加两参（workspaceId 为现有参数） |
| `src/renderer/views/ExpertSuiteView.vue` | `onUse` 创建会话 + `viewMode.navigate('chat')`；`filtered` 搜索按语言取 name/description |
| `src/renderer/components/expert/AgentCard.vue` | name/description 按语言取 zh/en |

### 4.5 关键决策记录

1. **systemPrompt 走 `Instructions()`，identity 走 `<IDENTITY>` section** —— 对齐 LobsterAI 的"会话级 system_prompt（能力）+ agent identity（人设）"双字段语义；不把 identity 硬拼进 `Instructions()`（否则与 ctxengine `<IDENTITY>` 机制重复）。
2. **identity priority = 31** —— 复用现有 `PriorityIdentity` 槽，排序稳定。
3. **skill mini-loop 不叠加会话 prompt** —— `runSkillPrompt != ""` 时提前 return，保持 SKILL.md 独占，避免技能执行被会话指令污染。
4. **预设数据放 renderer（mock-data.ts）** —— 与现有 `ExpertAgent` 同构；后续 agent 落 DB + 编辑 UI 时迁走，不阻塞本轮。
5. **name/description 双语走数据字段而非 i18n key** —— 见 FR-8，控制扁平字典膨胀；本轮保留，注明为有意取舍。
6. **本轮不加 `SessionStore.UpdateSystemPrompt` 接口方法** —— 创建路径 `Save` 已覆盖落库；无调用方的接口方法属 YAGNI 扩面，留给编辑 UI 的后续 spec。
7. **读写统一走 `SetPrompt`/`Prompt()` 访问器** —— `session.Session` 有 RWMutex，executor 侧读（`Instructions()`/`SystemSections()`）与 gateway 侧写（创建/hydrate）在不同 goroutine，裸字段读写是数据竞态。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| systemPrompt / identity 为空 | `Instructions()` / `SystemSections()` 按现状返回；行为不变（场景 2） |
| 老会话无 system_prompt 列数据 | GORM AutoMigrate 加列默认空字符串；hydrate 读到空即跳过 |
| skill mini-loop 中创建了带 prompt 的会话 | `Instructions()` 优先返回 skill prompt，会话 prompt 不叠加（决策 3） |
| 全局 `agent.instructions` 非空 + 会话 prompt 非空 | 拼接：全局在前、会话在后 |
| identity 与 workspace IDENTITY.md 同时存在 | 两个 `<IDENTITY>` 块按 priority 30/31 顺序共存 |
| 语言切换（zh/en） | 只影响下次创建会话时固化的 prompt；已建会话不变 |
| `f.Store` 为 nil（无 store 的测试路径） | hydrate 跳过恢复，不报错 |
| `f.MessageStore` 为 nil / message list 失败 | prompt 恢复放在 hydrate 开头执行，不受 `:29` 短路与 `:40` 提前 return 影响 |
| 会话带 systemPrompt 时 spawn subagent | `buildSubagentInstructions`（`factory.go:205`）用 `parent.Instructions()` → 子代理继承会话 systemPrompt；子代理 `AssemblerEnabled: false`（`factory.go:151`）不注入 `<IDENTITY>`。接受此不对称，后续需要再统一 |
| `wireForSession` no-store fallback（handler-test 快路径） | fallback 分支不输出两字段，可接受（仅测试路径触发） |
| `createSession` 主进程侧 agent 离线 | 走现有 `throw new Error('agent offline')` 路径，不新增分支 |

## 6. 涉及文件

### 6.1 Go 后端

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agents/session/session.go` | 加字段 + `SetPrompt`/`Prompt` |
| `src/darvin-agent/internal/agents/store/models.go` | 加 `SystemPrompt`/`Identity` 列 |
| `src/darvin-agent/internal/agents/store/sqlite_store.go` | `Save`/`toSession` 带两字段 |
| `src/darvin-agent/internal/gateway/handler_session.go` | 协议 + handler（保留 WorkspaceID） |
| `src/darvin-agent/internal/agents/ctxengine/sections.go` | 导出 `IdentitySection` |
| `src/darvin-agent/internal/agents/agent.go` | `Instructions()`/`SystemSections()` |
| `src/darvin-agent/internal/sessionruntime/hydrate.go` | 恢复两字段 |

### 6.2 IPC / 主进程 / preload

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | session 类型 + createSession 签名 |
| `src/preload/index.ts` | 透传 |
| `src/main/index.ts` | IPC handler 透传 |

### 6.3 renderer

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/services/mock-data.ts` | `ExpertAgent` 扩展 + 9 预设 |
| `src/renderer/composables/useSession.ts` | `createSession` 签名 |
| `src/renderer/views/ExpertSuiteView.vue` | `onUse` 接线；`filtered` 搜索按语言取字段 |
| `src/renderer/components/expert/AgentCard.vue` | 语言选择 |

## 7. 验收标准

### 7.1 Go 后端

- [ ] `cd src/darvin-agent && go build ./...` 通过；`go vet ./...` 无警告
- [ ] `go test ./...` 全绿（新增 `session.SetPrompt`/`store` 相关单测；`Instructions()`/`SystemSections()` 组合逻辑单测）
- [ ] `session.Session` 空 prompt 时 `Instructions()` 与改造前逐字节一致（回归断言）
- [ ] `SystemSections()` 在 `Identity=""` 时返回 nil
- [ ] `IdentitySection("x")` 返回 `<IDENTITY>\nx\n</IDENTITY>`，priority=31；空内容返回 `ok=false`
- [ ] `handleCreateSession` 带 `systemPrompt`/`identity` 时：内存 session + store 行都落上

### 7.2 链路 / 协议

- [ ] `createSession({ title, workspaceId?, systemPrompt, identity })` 全链路透传（preload → main → gateway → handler），workspaceId 缺省时 main 侧兜底 activeWorkspaceId 行为不变
- [ ] 建会话后 `listSessions` / `get_messages` 相关 wire 能读到 `systemPrompt`/`identity`

### 7.3 renderer

- [ ] 专家页 9 个预设展示正常（zh/en 切换 name/description 跟随）
- [ ] 点"使用"→ 新建会话（标题=agent 名）+ 切到 ChatView
- [ ] 新会话的 LLM system prompt 含 agent systemPrompt + `<IDENTITY>` 块（DevTools / zap 日志验证）

### 7.4 手动验证（Electron）

- [ ] `npm run lint` 通过
- [ ] `npm start` 起窗口：专家页用"股票助手"→ 聊天窗口发消息，观察 system prompt 组装（`Instructions` 含能力提示词、systemAddition 含 `<IDENTITY>`）
- [ ] 重启应用恢复该会话，继续聊天 system prompt 不丢
- [ ] 普通新会话（无预设）行为与改造前一致

### 7.5 不在验收范围

- Agent 编辑 UI（新建/编辑/删除 agent、DB 持久化预设）
- `skillIds` 注入/启用
- Plan Mode 覆盖 prompt
- 会话中途改 systemPrompt
