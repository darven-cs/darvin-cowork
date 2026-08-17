# Agent 系统（持久化 + 编辑 UI + 工作区绑定）迁移文档

> 续 `2026-08-15-system-prompt-design.md`：把 darvin-cowork 从"session 级 systemPrompt / identity"升级为"agent 一等公民"，追平 LobsterAI 的 agent 设计（`src/main/libs/openclawConfigSync.ts` + `src/renderer/components/agent/*`）。

## 1. 概述

### 1.1 问题 / 背景

刚完成的 `system-prompt-design` spec 让会话级 `systemPrompt` / `identity` 通了链路，但它本质上是"硬塞进 session 的两个字段"，还没把 agent 概念立起来。对照 LobsterAI：

| 维度 | LobsterAI | darvin-cowork 现状（spec 实施后） |
|------|-----------|----------------------------------|
| Agent DB 持久化 | `agents` 表（id/name/system_prompt/identity/icon/skill_ids/is_default/source/preset_id/sort_order/enabled） | ❌ 无；9 个预设硬编码 `mock-data.ts` |
| 编辑 UI | `AgentCreateModal` / `AgentSettingsPanel` / `AgentList` | ❌ 无 |
| 默认 Agent（"Main Agent"） | 必属一个 agent；workspace 内可换默认 | ❌ 无；新建会话无 agent 归属 |
| Session ← Agent 绑定 | `sessions.agent_id` FK，会话由 agent 派生 | ❌ 无；只有 session.SystemPrompt/Identity 副本 |
| Workspace ← 默认 Agent | `workspaces.default_agent_id` | ❌ 无 |
| AGENTS.md / IDENTITY.md / SOUL.md 同步 | `openclawConfigSync.syncAgentsMd` 写文件 | ❌ 无；ctxengine 现读 IDENTITY.md/SOUL.md/USER.md 但没"agent → workspace 文件"同步层 |
| MANAGED policy 段（web/browser/exec/...） | AGENTS.md 托管段固定拼 5-7 段 | ❌ 无；空 baseline 时 LLM 只看到 ctxengine 的 `<available_*>` 块 |

源码证据：
- `src/main/presetAgents.ts`（LobsterAI）：9 个 `PresetAgent`（id/nameEn/identity/systemPrompt/skillIds），UI 用 `Choose Preset` flow 加入 DB。
- `src/main/coworkStore.ts:2871`（LobsterAI）：`INSERT INTO agents (...) VALUES (?, ..., request.identity || '', ...)`——preset 创建时把 identity 字符串持久化。
- `src/main/libs/openclawConfigSync.ts:3371-3377`：非 main agent 的 `systemPrompt` → `SOUL.md`、`identity` → `IDENTITY.md`。
- `src/main/coworkStore.ts:3067`：`isDefault: Boolean(row.is_default)`；`AgentId.Main` 不能删。
- `src/darvin-agent/internal/agents/store/models.go:29` `Workspace`：当前无 `default_agent_id`。
- `src/darvin-agent/internal/agents/session/session.go`：当前无 `AgentID` 关联概念（只有最初遗留的 `AgentID string` 空字段）。

刚完成的 spec 留下的可复用机制：
- `session.SystemPrompt` / `session.Identity`（`session.go:18-22`）— 仍由 agent 派生，写入 session 固化，session-level 行为保持不变。
- `ctxengine.IdentitySection` + priority 31 注入 `<IDENTITY>` 块——复用。
- `mock-data.ts.ExpertAgent` 字段形态（`nameEn/descriptionEn/identity(En)/systemPrompt(En)/skillIds`）— 迁到 DB 时保持同构。
- Workspace-first 会话模型（commit a7a7755）— agent 绑定走 workspace 路径。

### 1.2 目标

1. **agents 表持久化**：9 个预设从 `mock-data.ts` 迁到 DB migration seed；新建/编辑/删除/复制 agent 走 store + handler。
2. **Agent 编辑 UI**：设置页新增 `Agents` 区块；`AgentCreateModal`（基于预设新建 + 空白新建）；`AgentSettingsPanel`（编辑系统提示词/身份/描述）。
3. **Workspace 默认 agent**：`workspaces.default_agent_id` 字段；新建 workspace 自动绑定 `Main Agent`。
4. **Session ← Agent 绑定**：`sessions.agent_id` FK；`createSession` 接受 `agentId`，agent 的 `systemPrompt`/`identity` 复制到 session（保持 session-level 固化行为）。
5. **预设使用流程**：专家页 "使用" 改为"以该 agent 为 workspace 默认 agent + 新建会话绑定 agentId"，而不是临时创建临时 session。

### 1.3 非目标

- **AGENTS.md / IDENTITY.md / SOUL.md 文件同步**——LobsterAI 把 agent 写文件，OpenClaw runtime 原生读。本轮只做 DB + UI + 绑定；文件同步层留给后续 spec（与 darvin 现有 `WorkspaceBootstrap` 读 IDENTITY.md/SOUL.md/USER.md 不冲突，方向相反）。
- **MANAGED policy 段**——空 baseline 兜底（web search / browser / exec safety / memory / heartbeat）单独 spec。
- **Plan Mode prompt 覆盖**——LobsterAI 有 `PLAN_MODE_PROMPT_MARKER`；darvin 暂无 plan mode，不在本轮。
- **`skillIds` 注入/启用**——上一 spec 已明确推迟；继续推迟。
- **跨 workspace agent 共享**——每个 workspace 独立的 agent 列表，agent 不全局共享（与 LobsterAI 同：`agentWorkspace = {STATE_DIR}/workspace-{agentId}/`）。
- **agent 头像自绘**——沿用 `AgentColor` token + 现有图标系统，不引入 LobsterAI 的 `AgentAvatarSvg` 二维码风格。

## 2. 用户场景

### 场景 1：从专家套件使用预设 agent（升级版）

**Given** 用户打开专家页，选 "翻译官"，当前语言 zh
**When** 点 "使用"
**Then**
1. "翻译官" agent 已存在于 DB（preset seed）
2. 调用 `createSession({ agentId: 'preset-translator', title: '翻译官' })`：从 agent 复制 systemPrompt/identity 到 session + session.agentId 落上
3. 新会话成为 active session，AppShell 切到 ChatView
4. 行为与上一 spec 一致，但 prompt 数据来自 DB 而非 mock-data

### 场景 2：设置页管理 agent

**Given** 用户进入 Settings → Agents
**When** 看到当前 workspace 的 agent 列表
**Then**
1. 列出 9 个 preset（标记 `来源：预设`，不可删除）+ 用户自定义 agent（可编辑/删除/复制为预设）
2. 点 "新建" → `AgentCreateModal`：选预设或空白创建
3. 点 agent 行 → `AgentSettingsPanel`：编辑 name/description/systemPrompt(双语)/identity(双语)
4. "删除" 仅对非 preset、非 default 开放

### 场景 3：workspace 切换默认 agent

**Given** 用户创建新 workspace
**When** 进入该 workspace 的设置
**Then**
1. workspace 自动绑定 `Main Agent` 作为 default agent
2. 用户可在 workspace 设置里把 default agent 改为"翻译官"等自定义 agent
3. 后续该 workspace 新建会话若不指定 agentId，自动用 default agent

### 场景 4：旧 session 向后兼容

**Given** 用户有上一 spec 留下的带 systemPrompt/identity 的 session（无 agent_id）
**When** 重启应用、恢复会话
**Then**
1. hydrate 阶段从 store 读到旧 session 行：agent_id 为空、systemPrompt/identity 仍按行字段恢复
2. `Agent.SystemSections()` / `Instructions()` 行为不变
3. 旧 session 不会因为新 spec 自动绑定到某个 agent（保持 session-level 语义）

## 3. 功能需求

### FR-1: `agents` GORM 表 + 模型

`src/darvin-agent/internal/agents/store/models.go` 新增：

```go
// Agent is a persisted agent (preset or user-defined). Sessions reference
// one via AgentID; at session creation the agent's systemPrompt / identity
// are snapshotted onto the session so changing an agent later does not
// retroactively rewrite existing conversations.
type Agent struct {
	ID              string    `gorm:"primaryKey"`
	Name            string    `gorm:"index"`
	Description     string    `gorm:"default:''"`
	NameEn          string    `gorm:"default:''"`
	DescriptionEn   string    `gorm:"default:''"`
	Identity        string    `gorm:"type:text;default:''"`
	IdentityEn      string    `gorm:"type:text;default:''"`
	SystemPrompt    string    `gorm:"type:text;default:''"`
	SystemPromptEn  string    `gorm:"type:text;default:''"`
	Icon            string    `gorm:"default:''"`
	Color           string    `gorm:"default:'blue'"` // AgentColor token 名
	SkillIDs        string    `gorm:"type:text;default:''"` // JSON 序列化的 []string
	Source          string    `gorm:"default:'user'"` // 'preset' | 'user'
	PresetID        string    `gorm:"default:''"`
	IsDefault       bool      `gorm:"default:false"`
	SortOrder       int       `gorm:"default:0"`
	Enabled         bool      `gorm:"default:true"`
	WorkspaceID     string    `gorm:"index;default:''"` // 每个 workspace 独立 agent 列表
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}
```

`Session`（`models.go:12`）加字段：

```go
AgentID string `gorm:"index;default:''"` // 创建时绑定的 agent；空 = 老 session
```

`Workspace`（`models.go:29`）加字段：

```go
DefaultAgentID string `gorm:"default:''"` // 新会话无 agentId 时用此 agent
```

`runtime/database.go:33` 的 `AutoMigrate` 注册新增 `&store.Agent{}`。

### FR-2: 9 个预设数据迁移到 DB

新建 `src/darvin-agent/internal/agents/store/preset_seed.go`：

```go
// PresetSeed returns the 9 hardcoded preset agents, identical in shape to
// the LobsterAI presetAgents.ts entries (nameEn/descriptionEn/identity(En)/
// systemPrompt(En)/skillIds) — content authored once in mock-data.ts is
// moved here verbatim. Source is "preset"; IsDefault false; WorkspaceID
// empty (seeded per workspace at first run).
func PresetSeed() []Agent
```

调用时机：`runtime/database.go` 在 `AutoMigrate` 之后、`Stores` 装配之前，遍历当前 workspace 列表（如尚未创建 workspace 则 seed 到 `"default"` workspace，待 `WorkspaceStore` 准备好后调用一次）。具体：

```go
// runtime/database.go，AutoMigrate 后
seeded, err := store.SeedPresets(db, existingWorkspaces)
```

seed 逻辑幂等：以 `preset_id` 为唯一键，已存在则跳过。

### FR-3: AgentStore 接口

`src/darvin-agent/internal/agents/store/store.go` 新增 `AgentStore`：

```go
type AgentStore interface {
	Create(ctx context.Context, agent Agent) (Agent, error)
	Update(ctx context.Context, agent Agent) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (Agent, error)
	ListByWorkspace(ctx context.Context, workspaceID string) ([]Agent, error)
	GetDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error)
}
```

实现 `SQLiteAgentStore`（新文件 `internal/agents/store/sqlite_agent_store.go`），复用 `glebarez/sqlite` 同一 `*gorm.DB`。`Delete` 拒绝 `is_default = true` 或 `source = 'preset'`（由 handler 层先查再删，不在 store 层抛错以保持接口简单）。

### FR-4: `workspace.DefaultAgentID` + Main Agent 自动绑定

`runtime/runtime.go` 在新 workspace 创建后立即调：

```go
// ensureDefaultAgent 为 workspace 绑定 Main Agent（"主 Agent"）作为默认 agent。
// Main Agent 是 source="preset" 且 preset_id="main" 的种子行；首次绑定时
// 从 PresetSeed 拷一份到该 workspace（per-workspace 独立）。
func ensureDefaultAgent(ctx context.Context, db *gorm.DB, workspaceID string) error {
    var count int64
    db.Model(&store.Agent{}).
        Where("workspace_id = ? AND is_default = ?", workspaceID, true).
        Count(&count)
    if count > 0 { return nil }
    main := store.MainAgentSeed() // 单独的"主 Agent"preset（除 9 个专家外的第 10 个）
    main.WorkspaceID = workspaceID
    main.IsDefault = true
    return db.Create(&main).Error
}
```

`MainAgentSeed` 是 PresetSeed 的兄弟函数，提供一份最小的中文 systemPrompt（"全场景办公助手" 等）+ identity（"你是一位通用办公助手..."），数据来源从 `mock-data.ts` 的"主 Agent"文案搬。

`WorkspaceStore` 新增 `UpdateDefaultAgent(ctx, id, defaultAgentID string) error`。

### FR-5: Session ← Agent 绑定

`src/darvin-agent/internal/agents/session/session.go`（已有 `AgentID string` 字段）：

- 加方法 `SetAgentID(id string)`（带 mu.Lock）
- 加方法 `AgentID() string`（RLock 返回）—— 用于 hydrate 读
- **不再自动从 AgentID 派生 SystemPrompt/Identity**：派生发生在 createSession 时（一次性写入 session），后续 agent 改动不影响 session

`sqlite_store.go Save` 不变（仍写 `sess.SystemPrompt` / `sess.Identity`，与上一 spec 一致）。

`sqlite_store.go toSession` 新增：

```go
out.AgentID = r.AgentID
```

### FR-6: createSession 接受 agentId

`src/darvin-agent/internal/gateway/handler_session.go`：

```go
type CreateSessionParams struct {
	Title        string `json:"title,omitempty"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	SystemPrompt string `json:"systemPrompt,omitempty"`
	Identity     string `json:"identity,omitempty"`
	AgentID      string `json:"agentId,omitempty"` // 新增：优先于 systemPrompt/identity
}
```

`handleCreateSession` 逻辑顺序：

1. 解析 `AgentID`：`agentId` 非空 → `agentStore.GetByID(ctx, agentId)` 拿到 agent
2. 若 agentId 为空 → 查 workspace 的 `DefaultAgentID` → 若非空用之；否则空
3. 派生写入：
   - `entry.Session.SystemPrompt` ← `agent.SystemPrompt || agent.SystemPromptEn || params.SystemPrompt`
   - `entry.Session.Identity` ← `agent.Identity || agent.IdentityEn || params.Identity`
   - `entry.Session.AgentID` ← `agent.ID`（agentId 实际来源，可能为空）
4. 既有 `entry.Session.SetPrompt(p.SystemPrompt, p.Identity)` 调用保留为兜底（当 agentId 解析失败 / agent 不存在时降级）
5. `SessionStore.Save` → `sess.AgentID` 字段写入（store schema 配合加字段后）

`SessionWire`（`:24`）加 `AgentID string \`json:"agentId,omitempty"\``。

> 向后兼容：上一 spec 留下的 `systemPrompt`/`identity` 字段仍可独立传（手动注入路径），agentId 缺省时降级到 params 直传。

### FR-7: Agent CRUD handler

新建 `src/darvin-agent/internal/gateway/handler_agent.go`：

| Method | Params | Result |
|--------|--------|--------|
| `agent.list_agents` | `{workspaceId}` | `{agents: AgentWire[]}` |
| `agent.get_agent` | `{agentId}` | `{agent: AgentWire}` |
| `agent.create_agent` | `{workspaceId, name, ...fields, fromPresetId?}` | `{agent: AgentWire}` |
| `agent.update_agent` | `{agentId, ...patch}` | `{agent: AgentWire}` |
| `agent.delete_agent` | `{agentId}` | `{deleted: bool}` |

`create_agent` 在 `fromPresetId` 非空时从 PresetSeed 拷（`nameEn/descriptionEn/identity(En)/systemPrompt(En)/icon/color/skillIds` 全部带过来，`workspaceId` 写到目标 workspace，`source='user'`，`preset_id='{fromPresetId}'`）。

`update_agent` 不允许改 `id`/`workspace_id`/`source`/`is_default`/`preset_id`——保留字段免覆盖。

`delete_agent` handler 层先 `GetByID`，拒绝 `source='preset'` 或 `is_default=true` 的删除请求。

### FR-8: IPC 类型 + preload + main 透传

`src/shared/darvin-api.ts`：

```ts
export interface DarvinAgent {
  id: string;
  name: string;
  description: string;
  nameEn: string;
  descriptionEn: string;
  identity: string;
  identityEn: string;
  systemPrompt: string;
  systemPromptEn: string;
  icon: string;
  color: string;
  skillIds: string[];
  source: 'preset' | 'user';
  presetId: string;
  isDefault: boolean;
  sortOrder: number;
  enabled: boolean;
  workspaceId: string;
}

interface DarvinApi {
  // ... existing
  listAgents(workspaceId?: string): Promise<{ agents: DarvinAgent[] }>;
  getAgent(agentId: string): Promise<{ agent: DarvinAgent }>;
  createAgent(req: { workspaceId: string; name: string; fromPresetId?: string; description?: string }): Promise<{ agent: DarvinAgent }>;
  updateAgent(agentId: string, patch: Partial<DarvinAgent>): Promise<{ agent: DarvinAgent }>;
  deleteAgent(agentId: string): Promise<{ deleted: boolean }>;
}
```

`createSession` 签名加 `agentId?: string` 字段（FR-6）。

`preload/index.ts` 与 `main/index.ts` 透传 5 个 agent 方法 + `createSession` 新字段。

### FR-9: renderer 编辑 UI

`src/renderer/composables/useAgents.ts` 新建：

```ts
export function useAgents() {
  // 暴露 agents / activeAgent / listAgents(workspaceId) / createAgent / updateAgent / deleteAgent
  // 内部：agent.list_agents / agent.create_agent / agent.update_agent / agent.delete_agent
  // main 侧 push 'agents:changed' 事件时刷新
}
```

`src/renderer/services/mock-data.ts`：删除 9 条 `expertSuiteAgents` 硬编码，保留 `ExpertAgent` 类型定义给 ExpertSuiteView 内部 adapter 使用（adapter 把 `DarvinAgent` → `ExpertAgent` 形态）。

`src/renderer/views/ExpertSuiteView.vue`：把 `expertSuiteAgents` 改为 `useAgents()` 拉到的列表，按当前 workspace 过滤；`onUse` 改为调 `session.createSession(name, undefined, '', '', agentId)`（systemPrompt/identity 不传，由 agent 派生）。

`src/renderer/views/SettingsView.vue`：在左侧导航加 `Agent` 区块入口（图标 + 文案）。设置区根据当前 workspace 渲染 agent 列表。

`src/renderer/components/settings/SettingsPanelAgents.vue` 新建：
- 列表：所有 agent（preset 排前，user 排后；preset 行带"来源：预设"标记，禁删禁改 source 字段）
- 行操作：编辑（→ `AgentSettingsPanel`）、删除（仅 user 且非 default）、复制为自定义（→ `AgentCreateModal` with `fromPresetId`）
- 顶部 "+ 新建" 按钮 → `AgentCreateModal`

`src/renderer/components/agent/AgentCreateModal.vue` 新建：
- 两个 tab：空白新建 / 从预设复制（选预设下拉）
- 表单：name / nameEn / description / descriptionEn / identity(En 双语 textarea) / systemPrompt(En 双语 textarea) / icon / color
- 提交 → `useAgents().createAgent({ workspaceId, name, fromPresetId })`

`src/renderer/components/agent/AgentSettingsPanel.vue` 新建：
- 渲染现有 agent 字段（同上表单 + SkillIDs 列表编辑）
- "保存" → `useAgents().updateAgent(id, patch)`
- preset agent 进入此面板允许编辑 name / description / systemPrompt / identity / icon / color（双语文本），但 `id`/`source`/`isDefault`/`presetId` 显示为只读说明

### FR-10: workspace 设置页加 defaultAgentId 选择

`src/renderer/components/settings/SettingsPanelWorkspace.vue`（如已有；若没有则新建）加一个 `默认 Agent` 选择器：列出当前 workspace 的非 preset agent + Main Agent，选中后调 `workspace.update_default_agent({ defaultAgentId })`。

`src/shared/darvin-api.ts`：扩展 `DarvinWorkspace` 类型加 `defaultAgentId?: string`；新增 `updateDefaultAgent(workspaceId, defaultAgentId)` handler。

### FR-11: AGENTS.md 同步层 — **推迟到本轮之外**（非目标）

LobsterAI 的 `syncAgentsMd` 把 `agent.systemPrompt` 写进 workspace 根目录的 `AGENTS.md` 托管段。本轮不动文件系统；agent 改动只影响新建 session。如需 workspace 共享 agent，单独 spec。

### FR-12: 边界保护

- 老 session（agent_id 空、systemPrompt 非空）：`Agent.SystemSections()` / `Instructions()` 行为不变（按上一 spec）。
- preset agent 改名/改 prompt 后，已存在的 session 不变（session-level 快照）。
- 跨 workspace 引用 agentId：handler 层校验 agent.WorkspaceID == req.WorkspaceID，不匹配拒绝。

## 4. 实现方案

### 4.1 数据流

```
ExpertSuiteView.onUse(agent)
  → session.createSession(name, undefined, '', '', agent.id)
    → window.darvin.createSession({ title, workspaceId?, agentId })
      → ipcRenderer.invoke('darvin:create_session', req)
        → ipcMain.handle('darvin:create_session')
          → client.request('agent.create_session', params)
            → handleCreateSession (gateway/handler_session.go)
              1. agentId 非空 → agentStore.GetByID → 拿 systemPrompt/identity
              2. agentId 空 → 查 workspace.DefaultAgentID → 拿 agent
              3. entry.Session.SystemPrompt ← agent.SystemPrompt || params.SystemPrompt
                 entry.Session.Identity     ← agent.Identity     || params.Identity
                 entry.Session.AgentID      ← agent.ID || ''
              4. SessionStore.Save → store.Session(SystemPrompt/Identity/AgentID)
              5. wireForSession 输出 SessionWire 含 AgentID
              ↓ (LLM 时，与上一 spec 一致)
        executor.RunConversation
          → system := d.Instructions()      // = global + session.SystemPrompt
          → system += Assembler().Assemble(...) // <IDENTITY>(31) + skills/facts/mcp
```

Settings → Agents → 编辑保存：

```
SettingsPanelAgents.vue → useAgents().updateAgent(id, patch)
  → window.darvin.updateAgent(id, patch)
    → ipcRenderer.invoke('darvin:update_agent', { agentId, ...patch })
      → ipcMain.handle('darvin:update_agent')
        → client.request('agent.update_agent', params)
          → handleUpdateAgent (gateway/handler_agent.go)
            → agentStore.Update(agent)
            → push 'agents:changed' 事件 → renderer useAgents 列表刷新
```

### 4.2 Go 后端改动点

| 文件 | 改动 |
|------|------|
| `internal/agents/store/models.go` | 新增 `Agent` 结构；`Session` 加 `AgentID`；`Workspace` 加 `DefaultAgentID` |
| `internal/agents/store/preset_seed.go` | 新文件：9 个专家 preset + Main Agent preset 共 10 行数据 |
| `internal/agents/store/store.go` | 新增 `AgentStore` 接口 |
| `internal/agents/store/sqlite_agent_store.go` | 新文件：`SQLiteAgentStore` 实现 AgentStore |
| `internal/agents/store/sqlite_store.go` | `Session.toSession` 恢复 `AgentID`；`Save` 写 `AgentID` |
| `internal/agents/store/workspace_store.go` | 新增 `UpdateDefaultAgent` |
| `internal/database/sqlite.go` | 无（已用 AutoMigrate） |
| `internal/runtime/database.go` | `AutoMigrate` 注册 `&store.Agent{}`；`SeedPresets` 调一次 |
| `internal/runtime/runtime.go` | `ensureDefaultAgent(ctx, db, workspaceID)` 在 workspace 创建后调用 |
| `internal/gateway/handler_session.go` | `CreateSessionParams` 加 `AgentID`；`SessionWire` 加 `AgentID`；`handleCreateSession` 派生逻辑（FR-6） |
| `internal/gateway/handler_agent.go` | 新文件：5 个 RPC handler（list/get/create/update/delete） |
| `internal/gateway/handler_workspace.go` | 新增 `update_default_agent` handler |
| `internal/agents/session/session.go` | `SetAgentID(id string)` + `AgentID() string`（mu.Lock/RLock） |
| `internal/agents/agent.go` | 无改动 |

### 4.3 IPC / 主进程改动点

| 文件 | 改动 |
|------|------|
| `src/shared/darvin-api.ts` | `DarvinAgent` 类型；`DarvinWorkspace.defaultAgentId?`；`DarvinApi` 5 个 agent 方法 + `createSession.agentId?` |
| `src/preload/index.ts` | 5 个 agent 方法透传 + `createSession` 新字段 |
| `src/main/index.ts` | 5 个 IPC handler（`darvin:list_agents` / `get_agent` / `create_agent` / `update_agent` / `delete_agent`）+ `darvin:update_default_agent`；`create_session` 透传 agentId |

### 4.4 renderer 改动点

| 文件 | 改动 |
|------|------|
| `src/renderer/services/mock-data.ts` | 删除 9 条硬编码预设；保留 `ExpertAgent` 类型与 `AgentColor` 枚举；新增 `darvinAgentToExpert(agent)` adapter |
| `src/renderer/composables/useAgents.ts` | 新文件：列表 / CRUD / active agent 状态 |
| `src/renderer/views/ExpertSuiteView.vue` | 从 `useAgents()` 取列表；`onUse` 传 `agentId` |
| `src/renderer/views/SettingsView.vue` | 新增 `Agent` 区块入口（左侧导航 + 内容面板） |
| `src/renderer/components/settings/SettingsPanelAgents.vue` | 新文件：列表 + 新建/编辑/删除/复制按钮 |
| `src/renderer/components/settings/SettingsPanelWorkspace.vue` | 新文件或扩展：默认 agent 选择器 |
| `src/renderer/components/agent/AgentCreateModal.vue` | 新文件：新建表单 |
| `src/renderer/components/agent/AgentSettingsPanel.vue` | 新文件：编辑表单 |
| `src/renderer/composables/useSession.ts` | `createSession` 签名加 `agentId?` 参数 |
| `src/renderer/services/i18n.ts` | 新增 i18n key：`settings.agents.*`（标签 / 按钮 / 文案） |

### 4.5 关键决策记录

1. **agent 绑定仍走 session-level 快照，不直接读 agent**——保持上一 spec 的 `Instructions()` / `SystemSections()` 行为不变；agent 改名/改 prompt 后已存在的会话不受影响。新会话拿当前 agent 内容。
2. **agent 表独立于 workspace 表，但通过 `workspace_id` 索引归属**——不引入"workspace 全局 agent"概念，与 LobsterAI `agentWorkspace = stateDir/workspace-{id}/` 对齐。
3. **Main Agent 作为第 10 个 preset seed**——保证每个新 workspace 有默认 agent；用户可在 workspace 设置里换成别的。
4. **preset agent 可编辑内容但 source/presetId 锁定**——避免破坏预设的"溯源"语义；用户想脱离 preset 控制应"复制为自定义"。
5. **session.agent_id 允许为空**——向后兼容上一 spec 留下的 session 行；为空时 LLM 仍能正常工作（仅缺 agent 上下文）。
6. **AGENTS.md / IDENTITY.md 文件同步延后**——darvin 现有 `WorkspaceBootstrap` 读 IDENTITY.md/SOUL.md/USER.md 是另一条路径（用户手写文件），与本轮 agent DB 同步**不冲突**（方向相反：DB → 文件 vs 文件 → 读）；合并时机放到后续 spec。
7. **跨 workspace 引用 agent 拒绝**——`createSession(workspaceId=A, agentId=agentInB)` 在 handler 层校验拒绝，避免越权。
8. **Main Agent 不放专家套件列表**——它走设置页切换默认 agent 路径，不进 ExpertSuiteView 卡片网格。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 老 session 无 agent_id | session-level snapshot 仍生效；`Agent.SystemSections()` 行为与上一 spec 一致 |
| 老 session 有 systemPrompt 无 agent_id | wire 输出 `agentId: ""` 但 `systemPrompt` 非空；UI 显示当前 prompt 来源"未知（agent_id 为空）" |
| agent 改名 / 改 prompt | 只影响新建 session；已存在的 session 行不变 |
| 预设 agent 被删除 | handler 拒绝 `source='preset'` 删除；CRUD 测试覆盖 |
| 跨 workspace 引用 agentId | handler 校验 `agent.WorkspaceID == req.WorkspaceID`，不匹配返回 `CodeInvalidParams` |
| workspace 创建但 Main Agent seed 失败 | seed 失败 → workspace 仍可用但 `DefaultAgentID` 空；新会话降级到"无 agent" 路径（空 prompt）；后台一次性 retry seed |
| agent store nil（handler-test 快路径） | 与上一 spec 一致：fallback 不输出 agentId 字段；handler-test 不强依赖 |
| 9 个 preset seed 重复执行（多次启动） | 以 `preset_id` 唯一键幂等跳过；运行期不重复 seed |
| `create_agent` fromPresetId 非法 | handler 校验 presetId 存在；不存在返回 `CodeInvalidParams` |
| `update_agent` 改 source / isDefault / workspaceId | handler 静默忽略这些字段（patch 黑名单） |
| 删除 workspace 默认 agent 之前 | handler 拒绝 `is_default=true` 删除；UI 隐藏删除按钮 |
| agentId 找不到（用户从老链接复制粘贴） | handler 降级：把 `SystemPrompt` / `Identity` 设为 params 传入值（兜底上一 spec 的行为） |

## 6. 涉及文件

### 6.1 Go 后端

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agents/store/models.go` | 新增 `Agent`；`Session` 加 `AgentID`；`Workspace` 加 `DefaultAgentID` |
| `src/darvin-agent/internal/agents/store/preset_seed.go` | 新文件：10 条 preset 数据（9 专家 + Main Agent） |
| `src/darvin-agent/internal/agents/store/store.go` | 新增 `AgentStore` 接口 |
| `src/darvin-agent/internal/agents/store/sqlite_agent_store.go` | 新文件：`SQLiteAgentStore` 实现 |
| `src/darvin-agent/internal/agents/store/sqlite_store.go` | `Session.toSession` 恢复 `AgentID`；`Save` 写 `AgentID` |
| `src/darvin-agent/internal/agents/store/workspace_store.go` | 新增 `UpdateDefaultAgent` |
| `src/darvin-agent/internal/agents/session/session.go` | `SetAgentID` / `AgentID` 访问器 |
| `src/darvin-agent/internal/runtime/database.go` | AutoMigrate 注册 `Agent`；`SeedPresets` 调用 |
| `src/darvin-agent/internal/runtime/runtime.go` | `ensureDefaultAgent` 在 workspace 创建后调用 |
| `src/darvin-agent/internal/gateway/handler_session.go` | `CreateSessionParams` / `SessionWire` 加 `AgentID`；handleCreateSession 派生逻辑 |
| `src/darvin-agent/internal/gateway/handler_agent.go` | 新文件：5 个 RPC handler |
| `src/darvin-agent/internal/gateway/handler_workspace.go` | `update_default_agent` handler |

### 6.2 IPC / 主进程 / preload

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | `DarvinAgent` 类型；`DarvinWorkspace.defaultAgentId?`；`DarvinApi` 5 个 agent 方法 + `createSession.agentId?` |
| `src/preload/index.ts` | 5 个 agent 方法透传 + `createSession` 新字段 |
| `src/main/index.ts` | 5 个 IPC handler + `update_default_agent`；`create_session` 透传 |

### 6.3 renderer

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/services/mock-data.ts` | 删除 9 条硬编码；保留 `ExpertAgent` 类型 + adapter |
| `src/renderer/composables/useAgents.ts` | 新文件：列表 + CRUD + active agent |
| `src/renderer/composables/useSession.ts` | `createSession` 签名加 `agentId?` |
| `src/renderer/views/ExpertSuiteView.vue` | 从 `useAgents()` 取列表；`onUse` 传 `agentId` |
| `src/renderer/views/SettingsView.vue` | 新增 `Agent` 区块入口 |
| `src/renderer/components/settings/SettingsPanelAgents.vue` | 新文件：列表 + 操作 |
| `src/renderer/components/settings/SettingsPanelWorkspace.vue` | 新文件/扩展：默认 agent 选择器 |
| `src/renderer/components/agent/AgentCreateModal.vue` | 新文件 |
| `src/renderer/components/agent/AgentSettingsPanel.vue` | 新文件 |
| `src/renderer/services/i18n.ts` | `settings.agents.*` 文案 key |

## 7. 验收标准

### 7.1 Go 后端

- [ ] `cd src/darvin-agent && go build ./...` 通过；`go vet ./...` 无警告
- [ ] `go test ./...` 全绿；新增测试覆盖：
  - `PresetSeed` 内容正确（10 条：9 专家 + Main Agent）
  - `SeedPresets` 幂等（重复调用不产生重复行）
  - `SQLiteAgentStore` CRUD roundtrip
  - `handleCreateSession` 派生逻辑：传 agentId → session.SystemPrompt/Identity/AgentID 落上；agentId 空 → params 兜底
  - 跨 workspace 引用 agent 拒绝
  - 预设 agent 删除拒绝
  - `Session.SetAgentID` / `AgentID()` 锁正确性
- [ ] `Session.toSession` 恢复 `AgentID` 字段

### 7.2 链路 / 协议

- [ ] `createSession({ agentId })` 全链路透传（preload → main → gateway → handler）
- [ ] `listAgents` / `getAgent` / `createAgent` / `updateAgent` / `deleteAgent` 5 个方法可调通
- [ ] `updateDefaultAgent(workspaceId, agentId)` handler 可调
- [ ] `listSessions` / `get_messages` wire 含 `agentId` 字段

### 7.3 renderer

- [ ] 首次启动后 DB 已 seed 10 个 agent（9 专家 + Main Agent），专家页从 DB 取列表
- [ ] 专家页 "使用" 按钮：以该 agent 为 workspace 默认行为；新会话 LLM system prompt 注入 agent 内容
- [ ] 设置 → Agent：列表展示 10 个；preset 行带"来源"标识；新建 / 编辑 / 删除按钮符合权限
- [ ] AgentCreateModal：空白新建 + 从预设复制两个 tab 都能跑通
- [ ] AgentSettingsPanel：编辑 name / description / 双语 prompt 后保存成功，列表立即刷新
- [ ] workspace 默认 agent 选择器：把 workspace 默认改成"翻译官"后，新会话无 agentId 时用翻译官内容
- [ ] 跨语言切换：DB 里的 agent 双语字段保留，下次 `useAgents()` 列表更新按当前语言显示

### 7.4 手动验证（Electron）

- [ ] `npm run lint` 通过
- [ ] `npm start` 起窗口：
  - 首次启动后设置页能看见 10 个 agent（9 preset + Main Agent）
  - 专家页选"翻译官"→ 新会话 → zap 日志 `Instructions` 含翻译官 systemPrompt，`SystemAddition` 含翻译官 `<IDENTITY>` 块
  - 设置 → Agents → 编辑翻译官 systemPrompt → 关掉会话再开新的 → 新会话拿到更新后的 prompt（已存在的旧会话不变）
  - 跨 workspace：新 workspace 默认绑 Main Agent；切到原 workspace 默认绑翻译官（验证多 workspace 隔离）
- [ ] 普通空会话（无 agentId 无 systemPrompt）行为与上一 spec 一致

### 7.5 不在验收范围

- AGENTS.md / IDENTITY.md / SOUL.md 文件同步层
- MANAGED policy 段（web/browser/exec/memory/heartbeat）
- Plan Mode prompt 覆盖
- skillIds 自动启用
- 跨 workspace agent 共享
- agent 头像自绘（沿用 `AgentColor` token）

## 8. 后续 spec 排队（不在本轮）

1. **AGENTS.md 同步层**：把 `agent.SystemPrompt` 写进 workspace 根目录的 `AGENTS.md` 托管段，配合 darvin 现有 `WorkspaceBootstrap` 形成"DB ↔ 文件"双向流。
2. **MANAGED policy 段**：空 baseline 兜底（web search / browser / exec safety / memory / heartbeat / skill creation），来源 LobsterAI `openclawConfigSync.MANAGED_*_PROMPT`。
3. **skillIds 注入/启用**：把 `agent.skillIds` 接到 skill 注册流程，会话创建时按 agent 启用 skill。
4. **Plan Mode prompt 覆盖**：darvin 引入 plan mode 时实现 `buildPlanModeSystemPrompt` + `PLAN_MODE_PROMPT_MARKER`。
5. **Agent 头像自绘**：LobsterAI 风格的 `AgentAvatarSvg` 二维码头像（30 个，darvin 已有 `assets/agent-avatars/*.svg` 占位，可改造）。