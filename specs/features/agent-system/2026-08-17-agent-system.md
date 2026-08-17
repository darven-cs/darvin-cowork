# Agent 系统（持久化 + 编辑 UI + 工作区绑定）迁移文档

> 续 `2026-08-15-system-prompt-design.md`：把 darvin-cowork 从"session 级 systemPrompt / identity"升级为"agent 一等公民"，追平 LobsterAI 的 agent 设计（`src/main/libs/openclawConfigSync.ts` + `src/renderer/components/agent/*`）。

## 1. 概述

### 1.1 问题 / 背景

刚完成的 spec 让会话级 `systemPrompt` / `identity` 通了链路，但它本质上是"硬塞进 session 的两个字段"，还没把 agent 概念立起来。对照 LobsterAI：

| 维度 | LobsterAI | darvin-cowork 现状（spec 实施后） |
|------|-----------|----------------------------------|
| Agent DB 持久化 | `agents` 表（id/name/system_prompt/identity/icon/skill_ids/is_default/source/preset_id/sort_order/enabled） | ❌ 无；9 个预设硬编码 `mock-data.ts` |
| 编辑 UI | `AgentCreateModal` / `AgentSettingsPanel` / `AgentList` | ❌ 无 |
| 默认 Agent（"Main Agent"） | 必属一个 agent；workspace 内可换默认 | ❌ 无；新建会话无 agent 归属 |
| Session ← Agent 绑定 | `sessions.agent_id` FK，会话由 agent 派生 | ⚠️ 部分：`session.Session.AgentID` 与 GORM `sessions.AgentID` 字段已存在（上一 spec 留下），但仍空串；handler 不派生 systemPrompt/identity |
| Workspace ← 默认 Agent | `workspaces.default_agent_id` | ❌ 无 |
| AGENTS.md / IDENTITY.md / SOUL.md 同步 | `openclawConfigSync.syncAgentsMd` 写文件 | ❌ 无；ctxengine 现读 IDENTITY.md/SOUL.md/USER.md 但没"agent → workspace 文件"同步层 |
| MANAGED policy 段（web/browser/exec/...） | AGENTS.md 托管段固定拼 5-7 段 | ❌ 无；空 baseline 时 LLM 只看到 ctxengine 的 `<available_*>` 块 |

源码证据：
- `src/main/presetAgents.ts`（LobsterAI）：9 个 `PresetAgent`（id/nameEn/identity/systemPrompt/skillIds），UI 用 `Choose Preset` flow 加入 DB。
- `src/main/coworkStore.ts:2871`（LobsterAI）：`INSERT INTO agents (...) VALUES (?, ..., request.identity || '', ...)`——preset 创建时把 identity 字符串持久化。
- `src/main/libs/openclawConfigSync.ts:3371-3377`：非 main agent 的 `systemPrompt` → `SOUL.md`、`identity` → `IDENTITY.md`。
- `src/main/coworkStore.ts:3067`：`isDefault: Boolean(row.is_default)`；`AgentId.Main` 不能删。
- `src/darvin-agent/internal/agents/store/models.go:31-37` `Workspace`：当前无 `default_agent_id`。
- `src/darvin-agent/internal/agents/session/session.go:18` `Session`：**已有 `AgentID string` 字段**（上一 spec 留的占位，本轮把它真接上）；GORM 行 `models.go:15` 同名 `AgentID string \`gorm:"index"\`` 已落库；`sqlite_store.go:45` 已写 `row.AgentID = sess.AgentID`，`:108` 通过 `ReplaceAllMeta` 已恢复。本轮不重新加这个字段，但需要补 `SetAgentID/AgentID()` 公开访问器。

刚完成的 spec 留下的可复用机制：
- `session.SystemPrompt` / `session.Identity`（`session.go:18-26`）— 仍由 agent 派生，写入 session 固化，session-level 行为保持不变。
- `ctxengine.IdentitySection` + priority 31 注入 `<IDENTITY>` 块——复用。
- `mock-data.ts.ExpertAgent` 字段形态（`nameEn/descriptionEn/identity(En)/systemPrompt(En)/skillIds`）— 迁到 DB 时保持同构；`category` / `price` 是 renderer 端过滤字段，不进 DB。
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

### 场景 5：首次启动无 workspace

**Given** 用户首次启动 darvin-cowork（fresh install），DB 里 `workspaces` 表为空
**When** 应用启动
**Then**
1. `loadDatabase` 阶段 AutoMigrate 注册 `Agent` 表
2. workspace 为空 → 建一条 `Workspace(ID="default", RootPath=cfg.Agent.Workdir)`
3. seed 9 条专家 preset + Main Agent（`is_default=true`）到 "default" workspace
4. 用户后续主动 `handleCreateWorkspace` 新 workspace → 收尾再 seed 一份 9 条专家（不同 workspace 独立），并 `EnsureDefaultForWorkspace` 绑定 Main Agent
5. 设置页 / WorkspacesView 可正常看到 agent 列表

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

`Session`（`models.go:12`）字段已就位，**无需改动**：

```go
AgentID string `gorm:"index;default:''"` // 上一 spec 留的占位；本轮真接 agent
```

`Workspace`（`models.go:31-37`）加字段：

```go
DefaultAgentID string `gorm:"default:''"` // 新会话无 agentId 时用此 agent
```

`runtime/database.go:33` 的 `AutoMigrate` 注册新增 `&store.Agent{}`。

### FR-2: 9 个预设数据迁移到 DB

新建 `src/darvin-agent/internal/agents/store/preset_seed.go`：

```go
// PresetSeed 返回 9 条专家 preset。结构与 LobsterAI `presetAgents.ts` 同构
//（nameEn/descriptionEn/identity(En)/systemPrompt(En)/skillIds/icon/color），
// 内容来自 mock-data.ts 第 64 行起的 expertSuiteAgents，verbatim 搬入。
// Source="preset"、IsDefault=false、PresetID 同 id、WorkspaceID 由调用方注入。
func PresetSeed() []Agent
```

调用时机：seed 分两步：

1. **空 workspace 兜底**：`runtime/database.go` 在 `AutoMigrate` 之后，遍历 `WorkspaceStore.List`——若返回空（首次启动），**先建一条 `Workspace` 行（ID = "default"，RootPath 走 cfg.Agent.Workdir）**，作为 seed 容器。
2. **幂等 seed**：对**当前 workspace 列表**每个 workspace 调一次 `agentStore.SeedPresets(ctx, workspaceID)`。`SeedPresets` 内部以 `(workspace_id, preset_id)` 联合唯一键去重——已存在的 `(workspace_id, preset_id)` 行跳过；新 workspace 创建时（`handleCreateWorkspace` 返回前调 `SeedPresets`）再次 seed 9 条。

注意：跨 workspace 的同 `preset_id` 允许多条行（每个 workspace 一份），seed 幂等键是 `(workspace_id, preset_id)`，不是单 `preset_id`——否则 workspace A seed 后会把 workspace B 的 seed 当作"已存在"跳过。

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
	// EnsureDefaultForWorkspace 若该 workspace 还没有 default agent 则创建 Main Agent；
	// 已存在则 no-op。handleCreateWorkspace 收尾时调一次，保证每个 workspace 有 default。
	EnsureDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error)
	// SeedPresets 把 9 条专家 preset 落到目标 workspace；幂等键 (workspace_id, preset_id)。
	SeedPresets(ctx context.Context, workspaceID string) error
}
```

实现 `SQLiteAgentStore`（新文件 `internal/agents/store/sqlite_agent_store.go`），复用 `glebarez/sqlite` 同一 `*gorm.DB`。`Delete` 拒绝 `is_default = true` 或 `source = 'preset'`（由 handler 层先查再删，不在 store 层抛错以保持接口简单）。

### FR-4: `workspace.DefaultAgentID` + Main Agent 自动绑定

**Hook 点**：`handleCreateWorkspace`（`gateway/handler_workspace_crud.go:99-103`）在 `h.WorkspaceStore.Create(ctx, w)` 成功之后、`successResp` 之前依次调：

```go
// 1) 落 9 条专家 preset（幂等）
if err := h.AgentStore.SeedPresets(ctx, w.ID); err != nil {
    return errorResp(id, CodeInternalError, "seed presets", err)
}
// 2) 绑定 Main Agent 为 default
mainAgent, err := h.AgentStore.EnsureDefaultForWorkspace(ctx, w.ID)
if err != nil {
    return errorResp(id, CodeInternalError, "ensure default agent", err)
}
// 3) 写 workspace.DefaultAgentID（可选；handler 直接写以减少一次往返）
if err := h.WorkspaceStore.UpdateDefaultAgent(ctx, w.ID, mainAgent.ID); err != nil {
    return errorResp(id, CodeInternalError, "set workspace default agent", err)
}
```

`EnsureDefaultForWorkspace`（AgentStore 方法）逻辑：

```go
func (s *SQLiteAgentStore) EnsureDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error) {
    var count int64
    s.db.Model(&Agent{}).
        Where("workspace_id = ? AND is_default = ?", workspaceID, true).
        Count(&count)
    if count > 0 {
        // 已存在：返回该 default agent（不重写）
        return s.GetDefaultForWorkspace(ctx, workspaceID)
    }
    main := MainAgentSeed()
    main.WorkspaceID = workspaceID
    main.IsDefault = true
    main.Source = "preset"
    main.PresetID = "main"
    if err := s.db.Create(&main).Error; err != nil {
        return Agent{}, err
    }
    return main, nil
}
```

`MainAgentSeed()` 是 `preset_seed.go` 里的兄弟函数，提供一份最小中文 systemPrompt + identity。文案内容**由本轮 PR 的内容作者新起草**（不在 mock-data.ts 里——9 个 expert 是产品文案，Main Agent 是 placeholder，需要简短通用办公助手风格，systemPrompt 100-300 字 + identity 一两句话）。`color='blue'` / `icon='sparkles'` / `id = 'main'` / `name = '主 Agent'` / `nameEn = 'Main Agent'` / `description = '通用办公助手'`。

`WorkspaceStore` 新增 `UpdateDefaultAgent(ctx, id, defaultAgentID string) error`——实现按 `workspace_store.go` 现有 `UpdateName` / `UpdateRoot` 同形态（`Model(&Workspace{}).Where(...).Update("default_agent_id", ...)`）。

### FR-5: Session ← Agent 绑定（补访问器）

`session.Session.AgentID` 字段已存在（`session.go:18`），`ReplaceAllMeta`（`session.go:142-156`）也已在 `s.mu.Lock()` 下写它。本轮补两个公开访问器，让 handler 层不必绕过锁：

```go
// SetAgentID 在 mu.Lock 下写 session 的 AgentID 字段。
// handleCreateSession 在 agent 派生完成后调一次，把 agent.ID 落上。
func (s *Session) SetAgentID(id string)

// AgentID 在 mu.RLock 下返回当前 AgentID；空字符串表示未绑定。
// hydrate / 读路径用此取代裸字段读取。
func (s *Session) AgentID() string
```

`SQLiteStore.Save`（`sqlite_store.go:37-60`）继续走 `sess.AgentID` 裸字段读——保持现实现状。**不再**把 AgentID 写入逻辑改到 `SetAgentID` 路径：Save 已经处理好了，handler 调完 `SetAgentID` 后 Save 自动落库（已是当前行为）。

`SQLiteStore.toSession`（`sqlite_store.go:106-111`）继续走 `ReplaceAllMeta(r.Key, r.AgentID, ...)`——`AgentID` 恢复链路无需改动。

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

`handleCreateSession` 完整派生逻辑（替代 `handler_session.go:359-402` 现有逻辑；handler 签名是 `(ctx, id, params, c *client, h *Handler)`，第 4 参 `c *client` 不可省略）：

```go
func handleCreateSession(ctx context.Context, id json.RawMessage, params json.RawMessage, c *client, h *Handler) *Response {
    var p CreateSessionParams
    if len(params) > 0 {
        if err := json.Unmarshal(params, &p); err != nil {
            return errorResp(id, CodeInvalidParams, "params must be an object", nil)
        }
    }
    workspaceID := strings.TrimSpace(p.WorkspaceID)
    if workspaceID == "" && h.AppState != nil {
        workspaceID, _ = h.AppState.GetActiveWorkspace(ctx)
    }
    if workspaceID == "" && h.WorkspaceStore != nil {
        return errorResp(id, CodeWorkspaceRequired, "workspace is required", nil)
    }

    // ---- agent 派生（AgentStore 可选；nil 时降级到 params 兜底） ----
    resolvedAgentID := strings.TrimSpace(p.AgentID)
    var agentSystemPrompt, agentIdentity string
    if h.AgentStore != nil {
        if resolvedAgentID == "" && workspaceID != "" {
            if def, err := h.AgentStore.GetDefaultForWorkspace(ctx, workspaceID); err == nil && def.ID != "" {
                resolvedAgentID = def.ID
                agentSystemPrompt = def.SystemPrompt
                agentIdentity = def.Identity
            }
        }
        if resolvedAgentID != "" {
            agent, err := h.AgentStore.GetByID(ctx, resolvedAgentID)
            if err != nil {
                resolvedAgentID = "" // agent 找不到 → 降级到 params 直传
            } else if workspaceID != "" && agent.WorkspaceID != workspaceID {
                return errorResp(id, CodeInvalidParams, "agent does not belong to this workspace", nil)
            } else {
                agentSystemPrompt = agent.SystemPrompt
                agentIdentity = agent.Identity
            }
        }
    }
    // ---- 派生完成 ----

    sessionID := c.sessions.MintSessionID()
    entry, err := c.sessions.GetOrCreateEntry(sessionID)
    if err != nil {
        return errorResp(id, CodeAgentInitFailed, "create session", err)
    }
    sys := agentSystemPrompt
    if sys == "" { sys = p.SystemPrompt }
    ident := agentIdentity
    if ident == "" { ident = p.Identity }
    entry.Session.SetPrompt(sys, ident)
    if resolvedAgentID != "" {
        entry.Session.SetAgentID(resolvedAgentID)
    }
    if h.SessionStore != nil {
        if err := h.SessionStore.Save(ctx, entry.Session); err != nil {
            return errorResp(id, CodeInternalError, "session save", err)
        }
        if err := h.SessionStore.BindWorkspace(ctx, sessionID, workspaceID); err != nil {
            return errorResp(id, CodeInternalError, "session workspace bind", err)
        }
        if strings.TrimSpace(p.Title) != "" {
            if err := h.SessionStore.UpdateTitle(ctx, sessionID, p.Title); err != nil {
                return errorResp(id, CodeInternalError, "session title", err)
            }
        }
    }
    if h.AppState != nil {
        if err := h.AppState.SetActiveSession(ctx, sessionID); err != nil {
            return errorResp(id, CodeInternalError, "set active session", err)
        }
    }
    return successResp(id, CreateSessionResult{Session: wireForSession(ctx, h, sessionID, entry)})
}
```

派生优先级：显式 `agentId` > workspace 默认 agent > `params.SystemPrompt/Identity`（兜底）。`IdentityEn` / `SystemPromptEn` 不参与派生——agent 表存双语文案，但 session 写入时按当前语言已在 Go handler 上层（main / preload）选过语种再下发，所以 `AgentID` 命中后直接拿主字段即可。

> 向后兼容：`h.AgentStore == nil`（handler-test stub）整个派生块跳过，等价于上一 spec 的 "params 直传" 行为，不输出 `agentId` 字段。

`SessionWire`（`handler_session.go:24-34`）加 `AgentID string \`json:"agentId,omitempty"\``，`toSessionWire` 把 `r.AgentID` 拷过去；`wireForSession` 的 fallback 分支填 `entry.Session.AgentID()`。

### FR-7: Agent CRUD handler

新建 `src/darvin-agent/internal/gateway/handler_agent.go`，5 个 RPC + 1 个 workspace-level update_default_agent：

| Method | Params | Result |
|--------|--------|--------|
| `agent.list_agents` | `{workspaceId}` | `{agents: AgentWire[]}` |
| `agent.get_agent` | `{agentId}` | `{agent: AgentWire}` |
| `agent.create_agent` | `{workspaceId, name, ...fields, fromPresetId?}` | `{agent: AgentWire}` |
| `agent.update_agent` | `{agentId, ...patch}` | `{agent: AgentWire}` |
| `agent.delete_agent` | `{agentId}` | `{deleted: bool}` |
| `agent.update_default_agent` | `{workspaceId, defaultAgentId}` | `{workspace: WorkspaceWire}` |

**dispatch 接入**（`gateway/handlers.go:143-253` 的 `dispatchRequest` switch 表）：新增 6 条 case——`agent.list_agents` / `agent.get_agent` / `agent.create_agent` / `agent.update_agent` / `agent.delete_agent` / `agent.update_default_agent`。`update_default_agent` handler 落在 `handler_workspace_crud.go`（紧挨 `rename_workspace` / `update_workspace_root`），复用同一 `WorkspaceStore`；其余 5 条落在 `handler_agent.go`。`update_default_agent` 跨 workspace 校验：`defaultAgentID` 非空时查 `AgentStore.GetByID`，拒绝 `agent.WorkspaceID != req.WorkspaceID`。

**Handler 注入**：`HandlerOptions`（`handlers.go:21-55`）加 `AgentStore *store.SQLiteAgentStore` 字段；`Handler` struct（`handlers.go:57-104`）加 `AgentStore store.AgentStore` 字段；`NewHandler`（`handlers.go:109-139`）把 `o.AgentStore` 注入；`Stores` struct（`runtime/runtime.go:81-90`）加 `Agents *store.SQLiteAgentStore`；`loadDatabase`（`runtime/database.go:49-57`）实例化。

`create_agent` 在 `fromPresetId` 非空时从 `PresetSeed` 拷（`nameEn/descriptionEn/identity(En)/systemPrompt(En)/icon/color/skillIds` 全部带过来，`workspaceId` 写到目标 workspace，`source='user'`，`preset_id='{fromPresetId}'`）。

`update_agent` 不允许改 `id`/`workspace_id`/`source`/`is_default`/`preset_id`——保留字段免覆盖。

`delete_agent` handler 层先 `GetByID`，拒绝 `source='preset'` 或 `is_default=true` 的删除请求。

`create_agent` / `update_agent` / `delete_agent` 完成后通过 `agent.event` 推 `agents:changed` 事件，main 端转发为 `darvin:push:agents-changed` push 事件（renderer useAgents 订阅刷新）。

### FR-8: IPC 类型 + preload + main 透传

`src/shared/darvin-api.ts` 新增 / 扩展类型：

```ts
/** Agent 分类（ExpertSuite filter tabs 用；不进 DB，由 renderer 端按 preset_id 映射）。 */
export type ExpertCategory = 'creative' | 'productivity' | 'technical' | 'business';

/** Agent 价格档（ExpertSuite 'free' filter tab 用；不进 DB，preset 全 Free，user 自建也 Free）。 */
export type ExpertPrice = 'Free' | '50 credits/次' | '100 credits/次' | '200 credits/次' | '300 credits/次';

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
  /** 仅 source='preset' 时由 preset_id → 静态映射得到；user 自建 agent 两字段均为 undefined。 */
  category?: ExpertCategory;
  price?: ExpertPrice;
}
```

`category` / `price` **不进 DB**——它们是 ExpertSuiteView 卡片网格的过滤维度（filter tabs：all / free / creative / productivity / technical / business）。在 renderer 端通过 `darvinAgentToExpert()` adapter 按 `presetId` 静态映射（preset_id='translator' → category='productivity', price='Free'；user 自建 agent → category=undefined, price='Free'）。ExpertSuiteView 的 `onUse` 路径不读这两个字段，仅过滤分支用。

`DarvinWorkspace` 类型加 `defaultAgentId?: string`（`src/shared/darvin-api.ts:597-605`）；`DarvinSession` 加 `agentId?: string`（`src/shared/darvin-api.ts:41-54`）。

`DarvinApi` 加 6 个方法：

```ts
interface DarvinApi {
  // ... existing
  listAgents(workspaceId?: string): Promise<{ agents: DarvinAgent[] }>;
  getAgent(agentId: string): Promise<{ agent: DarvinAgent }>;
  createAgent(req: { workspaceId: string; name: string; fromPresetId?: string; description?: string }): Promise<{ agent: DarvinAgent }>;
  updateAgent(agentId: string, patch: Partial<DarvinAgent>): Promise<{ agent: DarvinAgent }>;
  deleteAgent(agentId: string): Promise<{ deleted: boolean }>;
  updateDefaultAgent(req: { workspaceId: string; defaultAgentId: string }): Promise<{ workspace: DarvinWorkspace }>;
  onAgentsChanged(handler: (agents: DarvinAgent[]) => void): () => void;
}
```

`createSession` 签名加 `agentId?: string` 字段（FR-6）；对应 `DarvinCreateSessionResponse.session` 自动带 `agentId`（已通过 `SessionWire.AgentID` 透传）。

`DarvinPushEvent` 加 `AgentsChanged: 'darvin:push:agents-changed'`（`darvin-api.ts:998-1008`）。

`preload/index.ts`（line 83 的 `createSession` 上下文）透传 6 个 agent 方法 + `createSession` 新字段 + 1 个 `onAgentsChanged` 订阅。

`main/index.ts` 加 6 个 IPC handler（`darvin:list_agents` / `get_agent` / `create_agent` / `update_agent` / `delete_agent` / `update_default_agent`）+ `create_session` 透传 agentId + `agents:changed` push 转发（main 端 `EventRouter` 在 `AgentClient.onEvent` 监听 `agent.event` 名空间里的 `agents:changed` → `webContents.send('darvin:push:agents-changed', agents)`）。

### FR-9: renderer 编辑 UI

`src/renderer/composables/useAgents.ts` 新建：

```ts
export function useAgents() {
  // 暴露 agents / activeAgent / listAgents(workspaceId) / createAgent / updateAgent / deleteAgent
  // 内部：agent.list_agents / agent.create_agent / agent.update_agent / agent.delete_agent
  // main 侧 push 'agents:changed' 事件时刷新
}
```

`src/renderer/services/mock-data.ts`：删除 9 条 `expertSuiteAgents` 硬编码；保留 `ExpertAgent` 类型定义 + `AgentColor` 枚举。新增 `darvinAgentToExpert(agent: DarvinAgent): ExpertAgent` adapter：双语文案按当前 `getLang()` 选；`category` / `price` 按 `agent.presetId` 静态映射（保留旧 mock-data 9 条的映射表：'translator' → category='productivity', price='Free' 等；user 自建 agent（presetId 空）→ category=undefined, price='Free'）。

`src/renderer/views/ExpertSuiteView.vue`：把 `expertSuiteAgents` 改为 `useAgents()` 拉到的列表，按当前 workspace 过滤。**过滤 Main Agent**：`useAgents()` 列表里 `isDefault=true` 的 agent 不进卡片网格（Main Agent 只通过设置页切默认 agent，不进专家套件）。`onUse` 改为调 `session.createSession(name, undefined, '', '', agentId)`（systemPrompt/identity 不传，由 agent 派生）。

`src/renderer/views/SettingsView.vue`：在 `settings-sections.ts:5-13` 的 `SettingsSections` 常量加 `'agents'` 入口（图标 + 文案）；`SettingsView.vue:14-22` 渲染分支加 `<SettingsPanelAgents v-else-if="active === 'agents'" />`。

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

`SettingsSubNav.vue`：自动渲染 `SettingsSections` 新增的 `'agents'` 入口（已有循环逻辑，无需改）。

### FR-10: workspace 默认 agent 选择器

**位置**：`src/renderer/views/WorkspacesView.vue`（顶层 `/workspaces` 路由，已有 workspace name + rootPath 编辑）—— 在现有 workspace 卡片下方加 `默认 Agent` 选择器。**不**另开 `SettingsPanelWorkspace.vue`（与 `WorkspacesView` 重复定位，且 `SettingsSections` 列表已 7 个，再加 workspace section 会让设置导航过长）。

```vue
<!-- WorkspacesView.vue，单个 workspace 卡片底部 -->
<div>
  <label>{{ t('workspaces.default_agent') }}</label>
  <select :value="ws.defaultAgentId" @change="onDefaultAgentChange(ws.id, $event.target.value)">
    <option v-for="a in agentsForWorkspace(ws.id)" :key="a.id" :value="a.id">
      {{ getLang() === 'en' ? a.nameEn : a.name }}
    </option>
  </select>
</div>
```

`agentsForWorkspace(ws.id)` 从 `useAgents()` 取列表过滤 `workspaceId === ws.id && !a.isDefault`——Main Agent（`isDefault=true`）作为 disabled 提示项加入（"主 Agent (默认)"），但默认选项位置不一定是它，因为用户可以改成别的。

`onDefaultAgentChange` 调 `useWorkspaces().updateDefaultAgent({ workspaceId: ws.id, defaultAgentId: agentId })`（`useWorkspaces` 加此方法，走 `darvinApi.updateDefaultAgent`）。

`src/shared/darvin-api.ts` 已扩展 `DarvinWorkspace.defaultAgentId?`（FR-8）；handler 路径已在 FR-7 落到 `handler_workspace_crud.go`。

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
| `internal/agents/store/sqlite_store.go` | **不动**——`Save` 已写 `AgentID`、`toSession` 已通过 `ReplaceAllMeta` 恢复（FR-5） |
| `internal/agents/store/workspace_store.go` | 新增 `UpdateDefaultAgent` |
| `internal/database/sqlite.go` | 无（已用 AutoMigrate） |
| `internal/runtime/database.go` | `AutoMigrate` 注册 `&store.Agent{}`；`Stores.Agents` 实例化；首次启动 workspace 为空时建 `Workspace(ID="default")` 后 seed |
| `internal/runtime/runtime.go` | `Stores` struct 加 `Agents` 字段（`runtime.go:81-90`）；`Build` 阶段把 `Stores.Agents` 注入 `HandlerOptions` |
| `internal/gateway/handlers.go` | `HandlerOptions` 加 `AgentStore` 字段（`:21-55`）；`Handler` struct 加 `AgentStore`（`:57-104`）；`NewHandler` 注入（`:109-139`）；`dispatchRequest` switch 加 6 条 case（FR-7） |
| `internal/gateway/handler_session.go` | `CreateSessionParams` 加 `AgentID`；`SessionWire` 加 `AgentID`；`handleCreateSession` 完整派生逻辑（FR-6） |
| `internal/gateway/handler_agent.go` | 新文件：5 个 RPC handler（list/get/create/update/delete） |
| `internal/gateway/handler_workspace_crud.go` | 新增 `update_default_agent` handler；`handleCreateWorkspace` 收尾调 `SeedPresets` + `EnsureDefaultForWorkspace` + `UpdateDefaultAgent`（FR-4） |
| `internal/agents/session/session.go` | 新增 `SetAgentID(id string)` + `AgentID() string` 访问器（FR-5；字段已存在） |
| `internal/agents/agent.go` | 无改动 |

### 4.3 IPC / 主进程改动点

| 文件 | 改动 |
|------|------|
| `src/shared/darvin-api.ts` | `DarvinAgent` / `ExpertCategory` / `ExpertPrice` 类型；`DarvinWorkspace.defaultAgentId?`；`DarvinSession.agentId?`；`DarvinApi` 6 个 agent 方法 + `onAgentsChanged` + `createSession.agentId?`；`DarvinPushEvent.AgentsChanged` |
| `src/preload/index.ts` | 6 个 agent 方法透传 + `onAgentsChanged` 订阅 + `createSession` 新字段 |
| `src/main/index.ts` | 6 个 IPC handler（`darvin:list_agents` / `get_agent` / `create_agent` / `update_agent` / `delete_agent` / `update_default_agent`）+ `create_session` 透传 `agentId` + `darvin:push:agents-changed` push 转发（`EventRouter` 监听 `agent.event` 的 `agents:changed`） |

### 4.4 renderer 改动点

| 文件 | 改动 |
|------|------|
| `src/renderer/services/mock-data.ts` | 删除 9 条硬编码；保留 `ExpertAgent` 类型 + `AgentColor` 枚举 + 新增 `darvinAgentToExpert()` adapter（`category`/`price` 按 `presetId` 静态映射） |
| `src/renderer/composables/useAgents.ts` | 新文件：列表 + CRUD + active agent + `onAgentsChanged` 订阅 |
| `src/renderer/composables/useWorkspaces.ts` | 加 `updateDefaultAgent({ workspaceId, defaultAgentId })` 方法 |
| `src/renderer/composables/useSession.ts` | `createSession` 签名加 `agentId?` 参数 |
| `src/renderer/views/ExpertSuiteView.vue` | 从 `useAgents()` 取列表；过滤 `isDefault=true`；`onUse` 传 `agentId` |
| `src/renderer/views/WorkspacesView.vue` | 加 `默认 Agent` 选择器（FR-10） |
| `src/renderer/components/settings/settings-sections.ts` | `SettingsSections` 加 `'agents'` 入口 |
| `src/renderer/views/SettingsView.vue` | 加 `<SettingsPanelAgents v-else-if="active === 'agents'" />` 渲染分支 |
| `src/renderer/components/settings/SettingsPanelAgents.vue` | 新文件：列表 + 操作 |
| `src/renderer/components/agent/AgentCreateModal.vue` | 新文件 |
| `src/renderer/components/agent/AgentSettingsPanel.vue` | 新文件 |
| `src/renderer/services/i18n.ts` | `settings.agents.*` / `workspaces.default_agent` 等 i18n key（zh + en 双语，`assertSameKeys` 校验） |

### 4.5 关键决策记录

1. **agent 绑定仍走 session-level 快照，不直接读 agent**——保持上一 spec 的 `Instructions()` / `SystemSections()` 行为不变；agent 改名/改 prompt 后已存在的会话不受影响。新会话拿当前 agent 内容。
2. **agent 表独立于 workspace 表，但通过 `workspace_id` 索引归属**——不引入"workspace 全局 agent"概念，与 LobsterAI `agentWorkspace = stateDir/workspace-{id}/` 对齐。
3. **Main Agent 作为第 10 个 preset seed**——保证每个新 workspace 有默认 agent；用户可在 workspace 设置里换成别的。`MainAgentSeed()` 文案由内容作者新起草（mock-data.ts 里没有 Main Agent，9 个 expert 是产品文案）。
4. **preset agent 可编辑内容但 source/presetId 锁定**——避免破坏预设的"溯源"语义；用户想脱离 preset 控制应"复制为自定义"。
5. **session.agent_id 允许为空**——向后兼容上一 spec 留下的 session 行；为空时 LLM 仍能正常工作（仅缺 agent 上下文）。
6. **AGENTS.md / IDENTITY.md 文件同步延后**——darvin 现有 `WorkspaceBootstrap` 读 IDENTITY.md/SOUL.md/USER.md 是另一条路径（用户手写文件），与本轮 agent DB 同步**不冲突**（方向相反：DB → 文件 vs 文件 → 读）；合并时机放到后续 spec。
7. **跨 workspace 引用 agent 拒绝**——`createSession(workspaceId=A, agentId=agentInB)` 在 handler 层校验拒绝，避免越权。`update_default_agent(workspaceId, defaultAgentId)` 同样校验 `defaultAgentID` 所属 workspace。
8. **Main Agent 不进专家套件列表**——它走设置页 / WorkspacesView 切换默认 agent 路径。ExpertSuiteView 拉列表时过滤 `isDefault=true`，但 preset 的 9 个专家（`isDefault=false`）照常展示。
9. **seed 幂等键是 `(workspace_id, preset_id)` 联合，不是单 `preset_id`**——跨 workspace 的同 preset 允许多条行；workspace A 已 seed 后不会把 workspace B 的 seed 当作"已存在"跳过。
10. **首次启动 workspace 为空时建 `Workspace(ID="default")` 后 seed**——避免 seed 到不存在的 workspace 失败。后续用户主动建 workspace 时 `handleCreateWorkspace` 再次调 `SeedPresets` 给新 workspace 落 9 条。
11. **default agent 选择器放在 `WorkspacesView` 而不是 `SettingsPanelWorkspace`**——与已有 workspace 编辑去重；`SettingsSections` 保持 7 个，不为 workspace 设置再开 section。
12. **`category` / `price` 不进 DB**——是 ExpertSuiteView 卡片网格的过滤维度，由 renderer 端按 `presetId` 静态映射（user 自建 agent 两字段都 undefined）。
13. **`SetAgentID` / `AgentID()` 是新增访问器，字段已存在**——`session.Session.AgentID` 上一 spec 就留了占位；本轮补 lock-protected 访问器让 handler 不必裸读字段。
14. **handler-test stub 走 `h.AgentStore == nil` 降级**——整个派生块跳过，等价于上一 spec 的"params 直传"行为，不输出 `agentId` 字段；不强依赖 AgentStore。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 老 session 无 agent_id | session-level snapshot 仍生效；`Agent.SystemSections()` 行为与上一 spec 一致 |
| 老 session 有 systemPrompt 无 agent_id | wire 输出 `agentId: ""` 但 `systemPrompt` 非空；UI 显示当前 prompt 来源"未知（agent_id 为空）" |
| agent 改名 / 改 prompt | 只影响新建 session；已存在的 session 行不变 |
| 预设 agent 被删除 | handler 拒绝 `source='preset'` 删除；CRUD 测试覆盖 |
| 跨 workspace 引用 agentId | handler 校验 `agent.WorkspaceID == req.WorkspaceID`，不匹配返回 `CodeInvalidParams` |
| workspace 创建但 Main Agent seed 失败 | seed 失败 → workspace 仍可用但 `DefaultAgentID` 空；新会话降级到"无 agent" 路径（空 prompt）；后台一次性 retry seed |
| agent store nil（handler-test 快路径） | `h.AgentStore == nil` 时整个派生块跳过，等价于上一 spec 的"params 直传"行为；handler-test 不强依赖 |
| 9 个 preset seed 重复执行（多次启动） | 以 `(workspace_id, preset_id)` 联合唯一键幂等跳过；运行期不重复 seed |
| `create_agent` fromPresetId 非法 | handler 校验 presetId 存在；不存在返回 `CodeInvalidParams` |
| `update_agent` 改 source / isDefault / workspaceId | handler 静默忽略这些字段（patch 黑名单） |
| 删除 workspace 默认 agent 之前 | handler 拒绝 `is_default=true` 删除；UI 隐藏删除按钮 |
| agentId 找不到（用户从老链接复制粘贴） | handler 降级：把 `SystemPrompt` / `Identity` 设为 params 传入值（兜底上一 spec 的行为） |
| 首次启动 workspace 表为空 | `loadDatabase` 阶段建一条 `Workspace(ID="default", RootPath=cfg.Agent.Workdir)`，再 seed 9 条 preset + Main Agent；后续用户建 workspace 时 `handleCreateWorkspace` 收尾再调 seed |
| 跨 workspace 重复 seed 同 preset | `(workspace_id, preset_id)` 联合唯一键允许同 preset 在不同 workspace 各一份；不会跨 workspace 去重掉 |
| `update_default_agent` 引用跨 workspace agent | handler 校验 `agent.WorkspaceID == req.WorkspaceID`，不匹配返回 `CodeInvalidParams` |

## 6. 涉及文件

### 6.1 Go 后端

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agents/store/models.go` | 新增 `Agent`；`Session` 加 `AgentID`；`Workspace` 加 `DefaultAgentID` |
| `src/darvin-agent/internal/agents/store/preset_seed.go` | 新文件：10 条 preset 数据（9 专家 + Main Agent） |
| `src/darvin-agent/internal/agents/store/store.go` | 新增 `AgentStore` 接口 |
| `src/darvin-agent/internal/agents/store/sqlite_agent_store.go` | 新文件：`SQLiteAgentStore` 实现 |
| `src/darvin-agent/internal/agents/store/sqlite_store.go` | **不动**——`Save` 已写 `AgentID`、`toSession` 已通过 `ReplaceAllMeta` 恢复 |
| `src/darvin-agent/internal/agents/store/workspace_store.go` | 新增 `UpdateDefaultAgent` |
| `src/darvin-agent/internal/agents/session/session.go` | 新增 `SetAgentID(id string)` + `AgentID() string` 访问器（字段已就位） |
| `src/darvin-agent/internal/runtime/database.go` | AutoMigrate 注册 `&store.Agent{}`；`Stores.Agents` 实例化；首次启动 workspace 为空时建 `Workspace(ID="default")` 后 seed |
| `src/darvin-agent/internal/runtime/runtime.go` | `Stores` struct 加 `Agents` 字段；`Build` 阶段注入 `HandlerOptions` |
| `src/darvin-agent/internal/gateway/handlers.go` | `HandlerOptions` / `Handler` 加 `AgentStore` 字段；`NewHandler` 注入；`dispatchRequest` switch 加 6 条 case |
| `src/darvin-agent/internal/gateway/handler_session.go` | `CreateSessionParams` / `SessionWire` 加 `AgentID`；`handleCreateSession` 完整派生逻辑 |
| `src/darvin-agent/internal/gateway/handler_agent.go` | 新文件：5 个 RPC handler |
| `src/darvin-agent/internal/gateway/handler_workspace_crud.go` | `update_default_agent` handler；`handleCreateWorkspace` 收尾调 seed + ensureDefault + updateDefault |

### 6.2 IPC / 主进程 / preload

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | `DarvinAgent` / `ExpertCategory` / `ExpertPrice` 类型；`DarvinWorkspace.defaultAgentId?`；`DarvinSession.agentId?`；`DarvinApi` 6 个 agent 方法 + `onAgentsChanged` + `createSession.agentId?`；`DarvinPushEvent.AgentsChanged` |
| `src/preload/index.ts` | 6 个 agent 方法透传 + `onAgentsChanged` 订阅 + `createSession` 新字段 |
| `src/main/index.ts` | 6 个 IPC handler + `create_session` 透传 `agentId` + `darvin:push:agents-changed` push 转发 |

### 6.3 renderer

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/services/mock-data.ts` | 删除 9 条硬编码；保留 `ExpertAgent` 类型 + `AgentColor` 枚举 + 新增 `darvinAgentToExpert()` adapter |
| `src/renderer/composables/useAgents.ts` | 新文件：列表 + CRUD + active agent + `onAgentsChanged` 订阅 |
| `src/renderer/composables/useWorkspaces.ts` | 加 `updateDefaultAgent({ workspaceId, defaultAgentId })` 方法 |
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
  - `PresetSeed()` 内容正确（9 条专家 + `MainAgentSeed()` = 10 条预设）；icon/color 字段保留
  - `SeedPresets(ctx, workspaceID)` 幂等：重复调用不产生重复行；以 `(workspace_id, preset_id)` 为联合唯一键
  - `EnsureDefaultForWorkspace`：首次创建 Main Agent（`is_default=true`、`source='preset'`、`preset_id='main'`）；已存在则 no-op 返回原 agent
  - `SQLiteAgentStore` CRUD roundtrip（含 `workspace_id` 过滤）
  - `handleCreateSession` 派生逻辑：传 agentId → 派生 systemPrompt/identity/AgentID；agentId 空 → 查 workspace 默认 agent；agent 找不到 → 降级到 params 兜底；agent.WorkspaceID 不匹配 → `CodeInvalidParams`
  - 跨 workspace 引用 agent 拒绝（`createSession` 与 `update_default_agent` 两路径）
  - 预设 agent 删除拒绝（`source='preset'` / `is_default=true`）
  - `Session.SetAgentID(id)` / `AgentID()` 锁正确性
- [ ] `Session.toSession` 恢复 `AgentID` 字段（已是现状，不需新测试但要保留旧测试不退化）
- [ ] `handlers.go dispatchRequest` switch 含 6 条 agent case

### 7.2 链路 / 协议

- [ ] `createSession({ agentId })` 全链路透传（preload → main → gateway → handler → SessionWire.agentId）
- [ ] `listAgents` / `getAgent` / `createAgent` / `updateAgent` / `deleteAgent` 5 个方法可调通
- [ ] `updateDefaultAgent({ workspaceId, defaultAgentId })` handler 可调，wire 输出 `DarvinWorkspace.defaultAgentId`
- [ ] `listSessions` / `get_messages` wire 含 `agentId` 字段（向后兼容：老 session 空串）
- [ ] `agents:changed` push 事件 main 端正确转发 `darvin:push:agents-changed`
- [ ] `createSession.agentId` 派生在 `h.AgentStore == nil` 时安全降级（handler-test stub 路径）

### 7.3 renderer

- [ ] 首次启动后 DB 已 seed 10 个 agent（9 专家 + Main Agent），专家页从 DB 取列表
- [ ] 专家页 "使用" 按钮：以该 agent 创建新会话；systemPrompt/identity 从 agent 派生，不需手动传
- [ ] 专家页 `isDefault=true` 的 Main Agent 不进卡片网格
- [ ] 设置 → Agents：列表展示当前 workspace 的全部 agent（preset 排前、user 排后）；preset 行带"来源"标识；新建 / 编辑 / 删除按钮符合权限
- [ ] `AgentCreateModal`：空白新建 + 从预设复制两个 tab 都能跑通
- [ ] `AgentSettingsPanel`：编辑 name / description / 双语 prompt 后保存成功，列表立即刷新（`onAgentsChanged` 触发）
- [ ] `WorkspacesView`：每个 workspace 卡片底部 `默认 Agent` 选择器；切换后新会话无 agentId 时用该 default
- [ ] 跨语言切换：DB 里的 agent 双语字段保留，下次 `useAgents()` 列表更新按当前语言显示（双语文案在 DarvinAgent 原样下发，renderer 端按 `getLang()` 选）

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