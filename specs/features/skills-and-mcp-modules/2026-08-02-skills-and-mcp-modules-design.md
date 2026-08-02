# Skills 与 MCP 模块设计文档（主 spec）

> **本 spec 是 skills + mcp 整套能力的**总入口**。调研资料、实施 checklist、已拆分的子 spec 索引都在这里。**
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动（依赖前置条件见 §6）
> 子 spec 落地顺序：见 §7

---

## 0. 速查（TL;DR）

- **目标**：把 darvin-cowork 侧栏 `技能` / `MCP` 两个 nav 从 `PlaceholderView` 空态升级为可用的 skills + MCP 管理面，参考 LobsterAI 的成熟实现，但适配 darvin-cowork 的 Electron + Vue3 + Go 双进程架构。
- **范围**：
  - **Skills**：用户级 SKILL.md 目录加载、启用/禁用、bundled 内置技能、安全扫描、后台服务（web-search 之类）。
  - **MCP**：stdio / http 两种 transport 的 MCP server 注册、连接池、launch 优化、启停状态、配置同步给 Go agent。
- **非范围**：
  - 不实现 skills / MCP 远端 marketplace（v0 仅本地 / GitHub / npm spec 安装）
  - 不引入 `@modelcontextprotocol/sdk`（darvin-cowork 用纯 Go 实现的 MCP client，跟 skills 走同一个 `internal/agent/tool/` 入口；见 §4.5）
  - 不引入第三方图标 / 组件库（按 AGENTS.md §Vue 3 组件化规范）
  - 不动 darvin-agent 的 LLM / ctxengine / session / gateway 等已落地的模块（仅在 `internal/agent/tool/` 加新驱动 + 新增 1 个 plugin loader）

---

## 1. 调研资料

### 1.1 darvin-cowork 现状（基线）

#### 1.1.1 UI 层（renderer）

| 项 | 现状 | 证据 |
|---|------|------|
| 侧栏 nav | 已加 `skill` / `mcp` 两项（spec 06 sidebar-upgrade 已落地） | `src/renderer/components/sidebar/SidebarNav.vue` 第 43-44 行 |
| View mode | `skills` / `mcp` 已在 `useViewMode` 注册 | `src/renderer/composables/useViewMode.ts` 第 33-34 行 |
| AppShell 路由 | skills / mcp 走 `PlaceholderView` 空态 | `src/renderer/layout/AppShell.vue` 第 84-88 行 |
| 国际化 key | `sidebar.nav.skill` / `sidebar.nav.mcp` + `sidebar.placeholder.skills.desc` / `sidebar.placeholder.mcp.desc` 已在 i18n | `src/renderer/services/i18n.ts` |
| 实际功能 | **0 行** — 没有 SkillsView / McpView 组件、没有 services | `src/renderer/components/`、`src/renderer/services/` 全无 skill/mcp 命名文件 |

#### 1.1.2 主进程（main）

- `src/main/index.ts`：Electron 生命周期 + IPC 入口；`ipcMain.handle` 列表里**无** `skills:*` / `mcp:*` 通道
- `src/main/libs/`：8 个 util（agentTaskNotifier / importFiles / user-paths / user-settings / workspaceFiles / workspace-map）+ 两个 `.test.ts`，无 skill/mcp
- `src/main/runtime/`：Go agent 子进程管理（`manager.ts` + `client.ts`）；不感知 skill/mcp
- `src/main/store/`：仅有 `EventRouter.ts`
- 共享类型 `src/shared/darvin-api.ts`：已有 `DarvinToolKind`（bash / read / write / edit / todowrite / web_search / web_fetch / image_gen / video_gen + 兜底 string），但**不区分** skill / mcp 工具

#### 1.1.3 Go agent（`src/darvin-agent/`）

```
src/darvin-agent/
├── cmd/app/main.go
├── internal/
│   ├── acp/                     # ACP loop + queue + steer (S4 已落地)
│   ├── agent/
│   │   ├── agent.go             # Agent 主循环
│   │   ├── dispatcher.go        # RunStart/End/AgentEnd
│   │   ├── executor/            # RunConversation 主调度
│   │   ├── ctxengine/           # 上下文工程 (assemble/compact/lifecycle)
│   │   ├── event/               # Event + Subscription
│   │   ├── llm/                 # anthropic provider + 模型注册表
│   │   ├── queue/               # 消息队列
│   │   ├── session/             # 会话状态
│   │   ├── store/               # SQLite 持久化 (messages / imported_files / memory / app_state)
│   │   └── tool/                # 内置工具
│   │       ├── builtins.go      # 工具注册 (bash/read/write/edit/todowrite/web_search/web_fetch/image_gen/video_gen)
│   │       ├── fs.go            # 文件读写 (read/write/edit)
│   │       ├── shell.go         # shell 执行 + allowlist
│   │       ├── sandbox.go       # fs 沙箱
│   │       ├── permission.go    # 权限审批
│   │       ├── registry.go      # 全局工具注册表（不含 sessionKey）
│   │       ├── exclusions.go    # glob 排除
│   │       └── tool.go          # Tool interface
│   ├── config/                  # viper 配置
│   ├── database/                # SQLite 连接
│   ├── gateway/                 # JSON-RPC gateway
│   └── logger/                  # zap
└── config.yaml
```

**关键 gap**：

1. `tool.Registry.Get(name)` 是**全局**查表，不感知 session（spec 25 tool-architecture-rework §1.1 已点名）
2. **无 plugin loader**——加一个 tool 必须改 `darvin-agent` 主仓库
3. **无 skills**：`agent/ctxengine/sections.go` 已有 `SkillSummary` 占位类型但 0 loader
4. **无 MCP client**：完全没有 `internal/mcp/` 目录
5. **无 transport 抽象**——stdio / http 都没有 JSON-RPC 客户端

#### 1.1.4 已有相关 spec（前置 / 并行 / 冲突）

| spec | 关系 | 关键摘录 |
|------|------|---------|
| `darvin-api-extension` (00) | **前置** | `DarvinToolKind` 已为内置工具预留 union，可扩展 `skill` / `mcp` 子类型 |
| `tool-architecture-rework` (Tier 2) | **并行** | 已规划 plugin loader + session-aware Registry + workspace coordinator；skills/MCP 是其"consumer"之一 |
| `sidebar-upgrade` (06) | **已落地** | 已加 `skill` / `mcp` nav + `PlaceholderView` 路由，**留空态占位等本 spec 落地** |
| `agent-output-rendering` / `tool-result-rendering` (01/02) | **已落地** | 渲染层已能识别 6 种内置 tool；本 spec 落地的 skill/mcp 工具复用同套 `ToolCallGroup` 渲染（kind 字段驱动） |
| `merge-databases` (refactor) | **可并行** | 把主进程 SQLite + Go agent SQLite 合并；本 spec 的 skill/mcp store 设计应直接落合并后的 schema，避免再迁 |
| `i18n-enhancement` (08) | **已落地** | 复用现有 t()/插值/响应式机制 |

#### 1.1.5 `docs/agent/` 里 skills / MCP 的设计草案

- `04_SKILLS_SYSTEM.md`（参考 OpenClaw 风格）：SKILL.md frontmatter（`name` / `description` / `invocation` 块）+ 4 类 Source（workspace / plugin / bundled / session）
- `05_MCP_INTEGRATION.md`：用 `@modelcontextprotocol/sdk` 实现 NodeHostMcpClient；本 spec **不引入**该 SDK（darvin-cowork 走 Go 实现，详见 §4.5）

---

### 1.2 LobsterAI 实现参考（对手代码库）

> LobsterAI 的 skills / mcp 模块已经稳定运行，作为 darvin-cowork 的功能对齐基准。**只借鉴设计 + 数据模型 + 安全策略**，具体实现走 darvin-cowork 的 Electron + Vue3 + Go 双进程架构。

#### 1.2.1 Skills

**文件**：`src/main/skills/`
```
skillManager.ts           3248 行 — 核心管理器（安装 / 卸载 / 升级 / 启停 / 文件监听）
skillServices.ts          512 行 — 后台服务管理（web-search server 等长驻进程）
openClawSync.ts           43 行 — 与 OpenClaw 插件同步 plugin-provided skill id
index.ts                  2 行 — 导出
skillManager.test.ts      345 行 — 单元测试
skillServices.test.ts     49 行 — 单测
```

**核心数据模型**（`skillManager.ts:321-332`）：
```typescript
type SkillRecord = {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  isOfficial: boolean;
  isBuiltIn: boolean;
  updatedAt: number;
  prompt: string;          // SKILL.md 正文（YAML frontmatter 已剥离）
  skillPath: string;       // 绝对路径
  version?: string;
};
```

**存储布局**：
- **磁盘**：`userData/SKILLs/{name}/SKILL.md`（frontmatter + markdown 正文）
- **磁盘**：`userData/SKILLs/skills.config.json`（每个 skill 默认 order / enabled）
- **SQLite**：`skills_state` key（全局启用 / 禁用状态，按 id 索引）
- **bundled**：源码 `SKILLs/` 目录（27 个：article-writer / docx / pdf / pptx / xlsx / web-search / stock-analyzer / skill-creator / ...）

**SKILL.md 文件格式**：
```markdown
---
name: code-review
description: 执行代码审查，检查潜在问题
version: 1.2.0
invocation:
  userInvocable: true
  disableModelInvocation: false
---

# Code Review Skill

工具：grep / read_file
工作流：...
```

**安装源**（5 种）：
1. 本地 `SKILL.md` 文件 / 文件夹
2. 本地 `.zip` 包
3. GitHub 仓库 URL（`owner/repo` / `git@github.com:...` / `https://...zip`）
4. `clawhub.ai` URL（`/skills/{owner}/{name}`）
5. npm spec（`@scope/name@version`）

**关键工具函数**（借鉴但简化）：
- `parseFrontmatter(raw)`：YAML 解析（`js-yaml`）→ `{ frontmatter, content }`
- `compareVersions(a, b)`：semver 比较
- `resolveUserShellPath()`：macOS/Linux 用 `bash -lc` 拿 PATH（darvin-cowork Go 侧不需要走此路径，直接 spawn 时用 os.Environ）
- `parseGithubRepoSource(repoUrl)`：SSH / HTTPS 都支持
- `downloadGithubArchive()`：fallback 三种 archive URL（refs/heads / refs/tags / archive）
- `parseClawhubUrl(source)`：clawhub.ai 路径解析
- `isNpmPackageSpec(source)`：区分 npm spec vs git spec vs path

**安全扫描**（`src/main/libs/skillSecurity/skillSecurityScanner.ts`，约 400 行）：
- 基于 `js-x-ray`（AST 静态分析）+ 自定义规则
- 5 维度：network / file_access / process / dangerous_command / 其它
- 4 等级严重度：info / warning / danger / critical
- 风险分数 = Σ `SEVERITY_SCORES` (0/5/20/50)，cap 100
- 等级映射：0=safe / ≤10=low / ≤30=medium / ≤70=high / >70=critical
- 扫描预算：`MAX_FILES=500` / `MAX_FILE_SIZE_BYTES=512KB` / `MAX_FINDINGS=100` / `SCAN_TIMEOUT_MS=5000`
- 跳过 `node_modules` / `.git` / `__pycache__` / `.svn` / `.hg`
- 高风险技能安装时弹安全报告 modal，由用户确认

**后台服务**（`skillServices.ts`）：
- 部分技能自带 server（如 web-search 本地服务），需要后台常驻
- `SkillServiceManager.webSearchPid` 等字段跟踪子进程
- 启动时 health check：检查 `dist/server/index.js` + `iconv-lite/encodings/index.js` 是否齐全
- 输出 outdated 时自动 rebuild

**升级流程**（3 状态 + 中断恢复）：
- `not_installed` / `installed` / `update_available`（基于 semver 比较）
- 升级时把旧目录原子重命名为 `.upgrading`，拷新版本后还原 `.env` + `_meta.json`
- App 启动扫描 `.upgrading` 后缀目录做回滚

**IPC 通道**（`skills:*` 命名空间）：
- `skills:list` / `skills:install` / `skills:uninstall` / `skills:enable` / `skills:disable` / `skills:getDetails` / `skills:upgrade` / `skills:listMarketplace` / `skills:fetchMarketplace` / `skills:changed`

#### 1.2.2 MCP

**文件**：`src/main/mcp/`
```
mcpStore.ts                       332 行 — SQLite 存储
mcpRuntime.ts                     335 行 — 运行时生命周期 + 桥接服务
mcpLaunchResolution.ts            68 行 — 启动解析数据类型
mcpLaunchResolverManager.ts       480 行 — npx/uvx 前置安装优化
mcpLaunchResolverManager.test.ts  81 行 — 单测
mcpStore.test.ts                  121 行 — 单测
qichachaMcpAuth.ts                235 行 — OAuth 集成示例
```

**核心数据模型**（`mcpStore.ts:6-23`）：
```typescript
type McpServerRecord = {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  transportType: 'stdio' | 'sse' | 'http';
  command?: string;            // stdio 用
  args?: string[];
  env?: Record<string, string>;
  url?: string;                // sse/http 用
  headers?: Record<string, string>;
  isBuiltIn: boolean;
  githubUrl?: string;          // 来源溯源
  registryId?: string;         // marketplace id
  launchResolution?: McpLaunchResolution;  // 启动优化状态
  createdAt: number;
  updatedAt: number;
};
```

**启动解析状态**（`mcpLaunchResolution.ts:13-19`）：
```typescript
type McpLaunchResolution = {
  serverId: string;
  resolverKind: 'npx' | 'uvx' | 'python' | 'raw';
  sourceFingerprint: string;   // 命令+参数+env+platform+arch hash
  status: 'pending' | 'installing' | 'ready' | 'failed' | 'unsupported';
  packageName?: string;
  requestedVersion?: string;
  resolvedVersion?: string;
  installDir?: string;
  command?: string;            // 优化后的最终启动命令（绝对路径）
  args?: string[];
  env?: Record<string, string>;
  error?: string;
  installedAt?: number;
  resolvedAt?: number;
  lastProbeAt?: number;
  lastProbeStatus?: string;
  updatedAt: number;
};
```

**存储布局**：
- **SQLite**：`mcp_servers` 表 + `mcp_launch_resolutions` 表
- `mcp_servers.config_json` 字段：command / args / env / url / headers / isBuiltIn / githubUrl / registryId（normalizeTransportConfig 按 transportType 互斥）
- delete server 时同步 delete resolution（外键语义）

**Launch 优化（重点参考）**：
- 启用一个 `npx -y <pkg>@latest` MCP 时
- 1. `npm view <pkg>@<version> version --json`
- 2. `npm install --prefix <managed-dir> --omit=dev --no-audit --no-fund <pkg>@<version>`
- 3. 读 `node_modules/<pkg>/package.json` 的 `bin`（scoped 包必须按 `node_modules/@scope/name` 解析）
- 4. 把 `node <absolute-bin-path> ...args` 写入 OpenClaw 配置
- 指纹 hash 检测用户改动（命令/参数/env）→ 旧 ready 结果失效重做

**OpenClaw 集成**：
- LobsterAI 不直接执行 MCP，让 OpenClaw 执行
- LobsterAI 只负责：MCP 注册 / 配置持久化 / 启动解析 / OAuth / 写 OpenClaw 配置
- OpenClaw 通过 JSON-RPC 调 MCP（OpenClaw 是 client，外部 MCP server 是 server）
- `openclawConfigSync.ts` 触发 OpenClaw 配置热更新（OpenClaw 自带 file watch）

**Bridge 服务**（`mcpBridgeServer.ts`）：
- `127.0.0.1` 监听随机端口的 HTTP 回调
- `POST /askuser` 给 AskUserQuestion 工具用（用户在 UI 选答案）
- `POST /media-generation/tool` 给媒体生成工具用
- 共享 secret token（`x-mcp-bridge-secret` header）
- `Promise<AskUserResponse>` pending map + timeout

**IPC 通道**（`mcp:*` 命名空间）：
- `mcp:list` / `mcp:create` / `mcp:update` / `mcp:delete` / `mcp:setEnabled` / `mcp:retryLaunchResolution` / `mcp:fetchMarketplace` / `mcp:changed`

**OAuth / Auth 示例**（`qichachaMcpAuth.ts`）：
- MCP server 需要 token 时走 OAuth 回调
- 流程：浏览器跳 auth URL → 用户登录 → 回调到本地 HTTP server → 拿 token → 写 SQLite
- 复用 `authLocalCallbackServer` 工具

#### 1.2.3 关键设计决策借鉴

| LobsterAI | darvin-cowork 对应方案 | 备注 |
|-----------|----------------------|------|
| OpenClaw 子进程执行 MCP | **Go agent** 内置 MCP client + tool 驱动 | darvin-cowork 把 MCP 也下沉到 Go，与 LLM 决策在同一进程 |
| OpenClaw 监听 file watch 同步配置 | **WS JSON-RPC push 配置** | 已有 `AgentClient.request('agent.mcp.sync')` 通道，扩展即可 |
| npx / uvx 前置安装 | **同思路**：Go 侧 `pkg.go.dev` 或 `go install` 预拉取 MCP server（用 Go module 写 MCP） | v0 暂不实现，npx 优化足够；远期支持自定义二进制 |
| HTTP bridge（askUser / mediaGen） | darvin-cowork **不需要**——renderer ↔ main 走 IPC 即可 | LobsterAI 的 bridge 是给 OpenClaw 子进程用的，我们没这层 |
| js-x-ray 安全扫描 | **复用 Go 静态扫描**：用 `go/ast` 解析 `.go` 文件 + 正则扫 shell 脚本 | 不引入 npm 依赖；扫描 Go / Python / Bash 三类即可 |

---

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 侧栏 `技能` nav 跳到 `SkillsView`，列出 bundled + 已安装 + marketplace 三类，支持启用/禁用/安装/卸载/升级 | live 演示：禁用/启用 skill 后 `useTools` composable 立刻反映 |
| G2 | 侧栏 `MCP` nav 跳到 `McpView`，列出已注册 MCP server，支持新增/编辑/删除/启停/连接状态查看 | live 演示：stdin / http MCP 各启一个，状态显示 `connected` / `error` / `connecting` |
| G3 | 启用 skill 后，agent prompt 自动注入该 skill 的 prompt；用户可在 chat 中 `/skill-name args` 显式触发 | live 演示：禁用 web-search 后再发"搜索 X" → 模型看不到 web_search 工具 |
| G4 | 启用 MCP 后，agent tool registry 自动包含该 MCP 暴露的工具；tool 调用的 prompt / output 走统一 `ToolCallGroup` 渲染 | live 演示：启用 filesystem MCP → 工具列表多出 `mcp_filesystem_read_file` 等 |
| G5 | skill / MCP 都走 Go agent 子进程模型；不允许 renderer 直接 fork / exec 外部进程 | 检查：`src/main/` 下不出现新的 `spawn` / `exec` 调用 |
| G6 | 不引入 `@modelcontextprotocol/sdk` / `js-x-ray` / `clawhub` 等 npm 依赖；安全扫描在 Go 侧实现 | `package.json` 依赖列表不变（除单元测试工具） |
| G7 | 全部用户可见文案走 i18n；新增 60+ key 在 zh / en 双语字典齐全 | `assertSameKeys(dictZh, dictEn)` 通过 |
| G8 | `npm run lint` + `npm run test` + `npm run build:agent` 全绿 | CI 通过 |

---

## 3. 非目标（明确不做）

1. **不做远端 marketplace 站点**——darvin-cowork 没有 clawhub / npm registry 直连；v0 仅本地文件 + GitHub archive + `go install` 三种安装源
2. **不做 OAuth / 第三方授权登录**——v0 不接入任何 OAuth MCP server；OAuth 流程作为 `mcp-auth-callback.ts` 占位
3. **不做 skill 服务市场评分 / 评论**——纯本地 / 文件级管理
4. **不做 MCP server 多租户 / 跨 session 共享配置隔离**——v0 单 user 配置
5. **不做 hot reload**——skill / MCP 改动后下次 prompt 自动拉新配置；运行中修改需重启 Go agent
6. **不做 Go agent 侧的 SubAgent 复用 skill / MCP**——单 session 单 agent 视角
7. **不动 darvin-cowork 已落地的 `01-10` 9 份 spec + `darwin-api-extension` + `tool-architecture-rework` 的设计结论**——只在它们预留的扩展点上加内容
8. **不做 skills / MCP 的版本控制 UI**——版本号只在文件 frontmatter 里有，UI 不暴露 `compareVersions` 三态（v0 仅标 `isOfficial` / `isBuiltIn`）

---

## 4. 实现方案（总体架构）

### 4.1 分层

```
┌─────────────────────────────────────────────────────────┐
│  Renderer (Vue3 + Tailwind)                              │
│  ┌──────────────────────┐  ┌────────────────────────┐    │
│  │ SkillsView.vue       │  │ McpView.vue            │    │
│  │ (composables:        │  │ (composables:          │    │
│  │  useSkills)          │  │  useMcpServers)        │    │
│  └──────────────────────┘  └────────────────────────┘    │
│             ↓                                  ↓         │
│  preload bridge (window.darvin.skills.* / mcp.*)         │
└──────────────────────┬──────────────────────────────────┘
                       │ IPC (ipcMain.handle)
┌──────────────────────▼──────────────────────────────────┐
│  Main (Electron)                                          │
│  ┌────────────────┐  ┌────────────────────┐              │
│  │ skillsManager  │  │ mcpManager         │              │
│  │ (注册/启停/    │  │ (注册/启停/连接    │              │
│  │  状态广播)     │  │  池/健康检查)      │              │
│  └────────┬───────┘  └─────────┬──────────┘              │
│           │ via RuntimeMgr  │                          │
│           ▼ JSON-RPC request ▼                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │ AgentClient (WS / IPC) — 已有                       │ │
│  └────────────────────────────────────────────────────┘ │
└──────────────────────┬──────────────────────────────────┘
                       │ WebSocket JSON-RPC 2.0
┌──────────────────────▼──────────────────────────────────┐
│  Go agent (darvin-agent)                                 │
│  ┌──────────────────────────────────────────────┐        │
│  │ internal/skills/        ← NEW               │        │
│  │   loader.go   (扫 SKILL.md 目录)             │        │
│  │   registry.go (SkillRegistry)                │        │
│  │   runner.go   (skill 命令执行)               │        │
│  │   scanner.go  (安全扫描 — go/ast + 正则)     │        │
│  ├──────────────────────────────────────────────┤        │
│  │ internal/mcp/           ← NEW               │        │
│  │   client.go   (JSON-RPC client)              │        │
│  │   transport/  (stdio + sse + http)           │        │
│  │   registry.go (McpRegistry — 暴露 tool 描述)  │        │
│  │   launcher.go (启动解析 + npx 优化)          │        │
│  │   manager.go  (连接池 + 健康检查)            │        │
│  ├──────────────────────────────────────────────┤        │
│  │ internal/agent/tool/   (已落地, 新增驱动)    │        │
│  │   plugin.go    ← NEW (skill + mcp 统一入口)  │        │
│  ├──────────────────────────────────────────────┤        │
│  │ internal/gateway/                            │        │
│  │   新增 RPC:                                   │        │
│  │     agent.skills.list                        │        │
│  │     agent.skills.set_enabled                 │        │
│  │     agent.mcp.list                           │        │
│  │     agent.mcp.set_enabled                    │        │
│  │     agent.mcp.test                           │        │
│  │     agent.tools.list    ← 拉取合并后的       │        │
│  │                              (built-in +     │        │
│  │                               skill + mcp)   │        │
│  └──────────────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────┘
```

### 4.2 数据流

#### 4.2.1 Skills 加载流程

```
App 启动
   │
   ▼
main process: skillsManager.bootstrap()
   │
   ├─ 读 userData/SKILLs/skills.config.json
   ├─ 读 userData/SKILLs/*/SKILL.md（每个文件夹一个 skill）
   ├─ 读 darvin-cowork/resources/skills-bundled/（打包内嵌）
   └─ 通过 WS RPC `agent.skills.bootstrap` 推给 Go agent
         │
         ▼
Go agent: internal/skills/loader.ScanRoots([bundled, user])
   ├─ 解析每个 SKILL.md frontmatter (YAML)
   ├─ 走 scanner.ScanFile(path) 做安全扫描
   ├─ 写 SkillRegistry: map[skillID]*SkillEntry
   └─ emit SkillChangedEvent
         │
         ▼
agent.tool.Registry.Get("skill:code-review") ← 统一入口
   │
   ▼
Executor.RunConversation → tool_use 事件带 kind="skill"
   │
   ▼
Renderer ToolCallGroup 渲染（同内置工具一套）
```

#### 4.2.2 MCP 加载流程

```
App 启动
   │
   ▼
main process: mcpManager.bootstrap()
   │
   ├─ 读 SQLite mcp_servers 表
   ├─ 对 enabled=true 的 server 触发连接（异步）
   └─ 通过 WS RPC `agent.mcp.bootstrap` 推给 Go agent
         │
         ▼
Go agent: internal/mcp/manager.Connect(serverSpec)
   ├─ spawn stdio 子进程（按 launchResolution.command/args）
   ├─ 建立 JSON-RPC 2.0 client over stdio
   ├─ 调 `initialize` + `tools/list`
   ├─ 把 tool 描述符注入 McpRegistry
   └─ emit McpConnectedEvent + McpToolsListedEvent
         │
         ▼
agent.tool.Registry.Get("mcp:filesystem:read_file") ← 统一入口
   │
   ▼
Executor 调度：分发到对应 MCP server 的 JSON-RPC tools/call
   │
   ▼
Renderer ToolCallGroup 渲染（kind="mcp"，展示 server 来源）
```

### 4.3 数据模型

#### 4.3.1 共享类型（`src/shared/darvin-api.ts` 增量）

```typescript
/** spec 25 — tool kind 扩展（darvin-api-extension 已落地） */
export type DarvinToolKind =
  | 'bash' | 'read' | 'write' | 'edit' | 'todowrite'
  | 'web_search' | 'web_fetch' | 'image_gen' | 'video_gen'
  | 'skill'           // ← NEW: 来自 SKILL.md 的 skill 工具
  | 'mcp'             // ← NEW: 来自 MCP server 的工具
  | (string & { __brand?: never });

/** spec 31 — skill 描述符（renderer 视图） */
export interface DarvinSkillSummary {
  id: string;                 // 来自 frontmatter name
  name: string;               // 显示名
  description: string;
  version?: string;
  enabled: boolean;
  isOfficial: boolean;        // bundled + 已知清单
  isBuiltIn: boolean;
  path: string;               // 绝对路径（仅 main 端使用）
  updatedAt: number;
  /** 安全扫描等级 */
  riskLevel?: 'safe' | 'low' | 'medium' | 'high' | 'critical';
  /** 风险详情（仅 high/critical 显示） */
  riskFindings?: Array<{
    dimension: string;
    severity: 'info' | 'warning' | 'danger' | 'critical';
    message: string;
    file: string;
    line: number;
  }>;
}

/** spec 31 — MCP server 描述符（renderer 视图） */
export interface DarvinMcpServer {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  transportType: 'stdio' | 'sse' | 'http';
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  isBuiltIn: boolean;
  githubUrl?: string;
  registryId?: string;
  createdAt: number;
  updatedAt: number;
  /** 启动解析状态（运行期，null 表示尚未触发） */
  launchStatus?: 'pending' | 'installing' | 'ready' | 'failed' | 'unsupported';
  launchError?: string;
  /** 实测的连接状态 */
  connectionStatus?: 'disconnected' | 'connecting' | 'connected' | 'error';
  connectionError?: string;
  /** MCP server 暴露的工具列表（连接成功后回填） */
  exposedTools?: Array<{
    name: string;
    description: string;
    inputSchema: Record<string, unknown>;
  }>;
}
```

#### 4.3.2 DarvinApi 增量（`src/shared/darvin-api.ts`）

```typescript
export interface DarvinApi {
  // ── skills ──
  listSkills(): Promise<{ skills: DarvinSkillSummary[] }>;
  setSkillEnabled(req: { skillId: string; enabled: boolean }): Promise<{ ok: boolean }>;
  installSkill(req: { source: string }): Promise<{ skill: DarvinSkillSummary; riskLevel: string }>;
  uninstallSkill(req: { skillId: string }): Promise<{ ok: boolean }>;
  upgradeSkill(req: { skillId: string }): Promise<{ skill: DarvinSkillSummary }>;
  onSkillsChanged(handler: (skills: DarvinSkillSummary[]) => void): () => void;

  // ── mcp ──
  listMcpServers(): Promise<{ servers: DarvinMcpServer[] }>;
  createMcpServer(req: DarvinMcpServerCreate): Promise<{ server: DarvinMcpServer }>;
  updateMcpServer(req: { id: string; patch: DarvinMcpServerPatch }): Promise<{ server: DarvinMcpServer }>;
  deleteMcpServer(req: { id: string }): Promise<{ ok: boolean }>;
  setMcpServerEnabled(req: { id: string; enabled: boolean }): Promise<{ ok: boolean }>;
  testMcpConnection(req: { id: string }): Promise<{ ok: boolean; error?: string; tools?: DarvinMcpServer['exposedTools'] }>;
  retryLaunchResolution(req: { id: string }): Promise<{ ok: boolean }>;
  onMcpServersChanged(handler: (servers: DarvinMcpServer[]) => void): () => void;
  onMcpConnectionChanged(handler: (e: { id: string; status: DarvinMcpServer['connectionStatus']; error?: string }) => void): () => void;
}
```

#### 4.3.3 Go agent 内部类型

```go
// internal/skills/types.go
package skills

type SkillEntry struct {
    ID          string
    Name        string
    Description string
    Version     string
    Path        string         // SKILL.md 绝对路径
    Prompt      string         // frontmatter 之后的正文
    Source      SkillSource    // bundled / user / github / npm
    Enabled     bool
    IsOfficial  bool
    IsBuiltIn   bool
    UpdatedAt   time.Time
    RiskLevel   SecurityRiskLevel
    Findings    []SecurityFinding
}

type SkillSource string
const (
    SkillSourceBundled SkillSource = "bundled"
    SkillSourceUser    SkillSource = "user"
    SkillSourceGitHub  SkillSource = "github"
    SkillSourceNPM     SkillSource = "npm"
)

// internal/mcp/types.go
package mcp

type ServerSpec struct {
    ID            string
    Name          string
    Description   string
    Enabled       bool
    Transport     TransportType  // stdio / sse / http
    Command       string        // stdio
    Args          []string      // stdio
    Env           map[string]string  // stdio
    URL           string        // sse/http
    Headers       map[string]string  // sse/http
    IsBuiltIn     bool
    GitHubURL     string
    RegistryID    string
}

type TransportType string
const (
    TransportStdio TransportType = "stdio"
    TransportSSE   TransportType = "sse"
    TransportHTTP  TransportType = "http"
)

type ResolverKind string
const (
    ResolverNpx   ResolverKind = "npx"
    ResolverUvx   ResolverKind = "uvx"
    ResolverGo    ResolverKind = "go"     // go install <pkg>@<ver>
    ResolverRaw   ResolverKind = "raw"    // command 直接 exec
)

type LaunchResolution struct {
    ServerID          string
    ResolverKind      ResolverKind
    SourceFingerprint string
    Status            ResolutionStatus
    PackageName       string
    RequestedVersion  string
    ResolvedVersion   string
    InstallDir        string
    Command           string         // 优化后绝对路径
    Args              []string
    Env               map[string]string
    Error             string
    InstalledAt       time.Time
    ResolvedAt        time.Time
    UpdatedAt         time.Time
}

type ResolutionStatus string
const (
    StatusPending      ResolutionStatus = "pending"
    StatusInstalling   ResolutionStatus = "installing"
    StatusReady        ResolutionStatus = "ready"
    StatusFailed       ResolutionStatus = "failed"
    StatusUnsupported  ResolutionStatus = "unsupported"
)
```

### 4.4 IPC 协议增量

**复用现有 WS JSON-RPC**（`AgentClient.request('agent.*', params)`），新增 8 个 RPC：

```
agent.skills.list            → { skills: [...] }
agent.skills.set_enabled     → { skillId, enabled } → { ok }
agent.skills.bootstrap       → { skills: [...] }   ← main → Go，App 启动时推
agent.skills.changed         → notification       ← Go → main，skill 变化时

agent.mcp.list               → { servers: [...] }
agent.mcp.set_enabled        → { id, enabled } → { ok }
agent.mcp.bootstrap          → { servers: [...] } ← main → Go
agent.mcp.test               → { id } → { ok, error?, tools? }
agent.mcp.retry_resolution   → { id } → { ok }
agent.mcp.connection_changed → notification       ← Go → main，连接状态变化
agent.mcp.tools_list         → { id } → { tools: [...] }   ← 主动查询
agent.tools.list             → { tools: [...] }   ← 合并内置 + skill + mcp
```

### 4.5 关键决策与理由

#### 4.5.1 MCP client 在 Go 实现（不引入 SDK）

**理由**：
- `@modelcontextprotocol/sdk` 是 npm 包，darvin-cowork 主进程已不打算再扩 Node 依赖面
- darvin-agent (Go) 已有 `net/http` / `encoding/json` / `bufio`，自实现 JSON-RPC 客户端成本低
- 好处：MCP 跟 skill 走同一个 `tool.Registry` 入口，统一权限审批、统一 sandbox
- 边界：v0 只实现 stdio + http（spec 26 / spec 27 都先 stdio）；sse 放 v1

#### 4.5.2 skills 走 SKILL.md frontmatter（与 LobsterAI / OpenClaw 兼容）

**理由**：
- 业界惯例（Anthropic / OpenClaw / LobsterAI 都用同一格式）
- frontmatter 用 YAML（Go 标准库 `gopkg.in/yaml.v3` 已是 darvin-agent 依赖）
- 复用 LobsterAI 的 `parseFrontmatter` 语义，但 Go 重写

#### 4.5.3 安全扫描在 Go 侧做

**理由**：
- LobsterAI 的 `js-x-ray` 是 npm 包（AST 静态分析），不能复用
- darvin-agent 已有 `go/parser` + `go/ast` 标准库
- 扫描目标：`.go` / `.py` / `.sh` / `.js` 四类
  - `.go`：`go/parser` AST → 检查 `os/exec` / `net/http` / `ioutil.ReadFile` / `os.Remove` 等危险 import + call
  - `.py`：正则扫 `subprocess` / `os.system` / `urllib` / `requests` / `eval` / `exec`
  - `.sh`：正则扫 `curl` / `wget` / `rm -rf` / `chmod 777` / `eval` / `nc`
  - `.js`：正则扫 `require('child_process')` / `eval` / `Function(` / `fetch(` / `XMLHttpRequest`

#### 4.5.4 不做 OAuth / 远端 marketplace

**理由**（已在 §3 列出）：保持 v0 单 user + 本地优先的极简安全模型。

#### 4.5.5 skill / MCP 工具走统一的 `kind: 'skill' | 'mcp'` 渲染

**理由**：spec 01/02 已落地的 `ToolCallGroup` 按 `DarvinToolKind` 分发渲染，新增 kind 不改渲染层结构，只在 `toolDisplay.ts` 加归一化函数。

#### 4.5.6 SQLite 复用 `merge-databases` 整合后的 schema

**理由**：spec refactor `merge-databases` 正在落地；本 spec 的 skill / MCP store 表直接落合并后的位置，避免后续再迁。

#### 4.5.7 bundled skill 用 Go `embed.FS` 打包

**理由**：
- darvin-cowork 已用 `forge.config.ts` + `extraResources` 打包 Go 二进制
- bundled skill 放 `src/darvin-agent/resources/skills-bundled/`，Go 用 `//go:embed` 嵌入二进制
- 升级 darvin-cowork 版本时 bundled 自然更新（用户不可改）

---

## 5. 用户场景

### 场景 1：用户打开侧栏 `技能` nav

**Given** App 已启动，bundled 5 个 skill（code-review / api-design / testing / web-search / docx），用户从未装过 skill
**When** 用户点侧栏 `技能` 图标
**Then** 跳到 `SkillsView`，顶部三个 tab：`已安装` / `市场` / `设置`；`已安装` 列出 5 个 bundled（默认全部 enabled），每个卡片显示名称 / 描述 / 版本 / 启用开关 / [详情] 按钮

### 场景 2：禁用 skill 后 agent 看不到对应工具

**Given** `web-search` skill 已启用
**When** 用户切到 `已安装` tab，关掉 `web-search` 开关
**Then** toast「已禁用 web-search」；下次 prompt，agent tool registry 不再含 `web_search` 工具；DarvinApi `listTools` 不返回

### 场景 3：安装本地 SKILL.md

**Given** 用户从 GitHub 下载了 `code-review.zip`，解压到本地 `~/Downloads/code-review/`
**When** 用户在 `市场` tab 点「本地安装」选 `~/Downloads/code-review/SKILL.md`
**Then** 触发 `installSkill`，main 调 Go 端解析 + 安全扫描（5s timeout）；扫描结果：
- `safe` / `low`：直接装，提示「已安装 code-review v1.2.0」
- `medium`：弹安全报告 modal，列出 findings，用户点「仍然安装」才落地
- `high` / `critical`：强制阻断，提示「风险过高，禁止安装」

### 场景 4：安装 GitHub skill

**Given** 用户在市场看到一个 GitHub 仓库 `owner/repo`（或带 ref / subpath）
**When** 点 [安装]
**Then** main 调 Go 端 `npm`-free GitHub archive 下载（`https://codeload.github.com/.../zip/refs/heads/main`），解压 → 走场景 3 的安全扫描 → 落地

### 场景 5：升级 skill（v0 简化）

**Given** 用户装了一个 GitHub skill v1.0.0；仓库后来推到 v1.1.0
**When** 用户在 skill 卡片上点 [升级]（v0 显式触发，不做 3 态自动检测）
**Then** main 走安全扫描 → 「旧目录 → .upgrading → 新版本解压 → 还原 .env / .meta.json → 删除 .upgrading」；中途中断下次启动扫描 `.upgrading` 自动回滚

### 场景 6：用户打开侧栏 `MCP` nav

**Given** 用户从未配过 MCP
**When** 点 `MCP` 图标
**Then** 跳到 `McpView`，列出 [filesystem (内置, 已启用)]；按钮 [+ 新增 MCP server]

### 场景 7：新增 stdio MCP server

**Given** 用户填了：name=`github`，command=`npx`，args=`-y @modelcontextprotocol/server-github`，env=`{GITHUB_TOKEN: ghp_xxx}`
**When** 点 [保存]
**Then** main 调 Go 端：
1. 检测 command 是 npx → 触发 launch resolution（async，不阻塞保存）
2. 把 server 注册到 McpRegistry
3. 异步 spawn `node <resolved-bin>` + 建立 JSON-RPC client
4. `initialize` + `tools/list` → 回填 `exposedTools`
5. 连接状态 `connecting` → `connected`（或 `error` + error 消息）

UI 端：卡片显示 `connecting` 状态徽章；3s 内变 `connected`，展示 4 个工具（create_issue / search_repos / ...）

### 场景 8：新增 http MCP server

**Given** URL=`http://localhost:3001/mcp`，无 auth
**When** 点 [保存]
**Then** 走 http transport 的 JSON-RPC client；状态变化同上

### 场景 9：连接失败重试

**Given** 上一个 MCP server 连接失败（status=`error`，error=`ECONNREFUSED`）
**When** 用户点 [重试] 按钮
**Then** main 调 `retryLaunchResolution` + 重新连接；状态 `connecting` → `connected` 或仍 `error`

### 场景 10：agent 实际使用 skill / MCP 工具

**Given** `web-search` skill 启用；`filesystem` MCP 启用且 connected
**When** 用户在 chat 发「搜索一下 Go embedding 最新版本」
**Then** agent 收到 tool 列表（内置 + skill + MCP 合并），决定调 `web_search`；emit `tool_start { kind: 'skill', name: 'web_search', input: {...} }`；返回结果后 emit `tool_end`；renderer `ToolCallGroup` 按 kind='skill' 渲染（同内置工具一套）

### 场景 11：用户在 chat 显式触发 skill（`/skill-name`）

**Given** `code-review` skill 启用且 `userInvocable: true`
**When** 用户在 chat 框输入 `/code-review src/api/handler.go`
**Then** main 检测到 `/` 前缀 → 解析为 skill 触发；不走 LLM 决策；直接把 skill 的 prompt + args 喂给 LLM；tool_use 事件带 `kind='skill'`、`source='user-invocation'`

### 场景 12：禁用 MCP 后 agent 看不到对应工具

**Given** `filesystem` MCP 启用且有 3 个工具
**When** 用户在 MCP 卡片关掉开关
**Then** main 通知 Go 断开连接；下次 prompt agent tool registry 不含这 3 个工具

---

## 6. 前置 / 依赖

| 依赖 | 类型 | 状态 | 备注 |
|------|------|------|------|
| `darvin-api-extension` (00) | **前置** | ✅ 已落地 | 仅消费其 `DarvinToolKind` union + `DarvinEvent` 扩展 |
| `tool-architecture-rework` (Tier 2) | **前置** | 🚧 设计阶段 | 必须先把 plugin loader + session-aware Registry 落地，本 spec 才能接入 skill/mcp 作为 plugin |
| `merge-databases` (refactor) | **并行** | 🚧 设计阶段 | 本 spec 的 `mcp_servers` / `skill_state` 表直接落合并后的 schema |
| `i18n-enhancement` (08) | **前置** | ✅ 已落地 | 复用 t() / 插值 / 响应式 |
| `sidebar-upgrade` (06) | **前置** | ✅ 已落地 | 复用 nav 路由 + PlaceholderView 占位 |
| `tool-result-rendering` (02) | **前置** | ✅ 已落地 | 复用 `ToolCallGroup` 渲染 |
| Go 依赖 `gopkg.in/yaml.v3` | Go module | ✅ 已有 | 用于 SKILL.md frontmatter 解析 |
| Go 依赖 `golang.org/x/text` | Go module | ✅ 已有 | 安全扫描字符串处理 |

---

## 7. 子 spec 拆分（核心交付物）

> 每份子 spec **独立**落地、独立 review、独立合并。每份 spec 都不应一次性吞掉所有内容。

### 7.1 子 spec 索引

| # | 文件 | 范围 | 前置 | 落地顺序 |
|---|------|------|------|---------|
| **31** | `2026-08-02-skills-loader-and-registry.md` | Go 端 skills loader + registry + 扫描器（无 UI） | 26 tool-architecture-rework | ① |
| **32** | `2026-08-02-skills-ipc-and-bootstrap.md` | Go ↔ main IPC `agent.skills.*` + main 端 `skillsManager` + bundled 5 个示例 skill | 31 | ② |
| **33** | `2026-08-02-skills-renderer-view.md` | `SkillsView.vue` + `useSkills` composable + i18n key | 32 | ③ |
| **34** | `2026-08-02-mcp-transport-and-client.md` | Go 端 stdio + http transport + JSON-RPC client（无 UI） | 26 tool-architecture-rework | ② (并行 32) |
| **35** | `2026-08-02-mcp-registry-and-launcher.md` | Go 端 `McpRegistry` + `launchResolution` (npx 优化) + connection lifecycle | 34 | ③ |
| **36** | `2026-08-02-mcp-main-store-and-ipc.md` | main 端 `mcpManager` + SQLite store + IPC `mcp:*` + bundled filesystem MCP | 34 | ③ |
| **37** | `2026-08-02-mcp-renderer-view.md` | `McpView.vue` + `useMcpServers` composable + i18n key + `McpServerFormModal` | 36 | ④ |
| **38** | `2026-08-02-tool-registry-merge-and-routing.md` | 把 skill / MCP 工具合并进 `tool.Registry`；改 `tool_start` / `tool_end` 事件加 `kind` + `serverId?` | 31 + 34 | ④ |
| **39** | `2026-08-02-skill-user-invocation.md` | chat `/skill-name args` 解析 + 显式触发 | 31 + 38 | ⑤ |

### 7.2 子 spec 之间的依赖图

```
[26 tool-architecture-rework] (前提：plugin loader 已落地)
        │
        ├─→ [31 skills-loader-and-registry]
        │       │
        │       └─→ [32 skills-ipc-and-bootstrap]
        │               │
        │               └─→ [33 skills-renderer-view]
        │
        └─→ [34 mcp-transport-and-client]
                │
                ├─→ [35 mcp-registry-and-launcher]
                │       │
                │       └─→ [38 tool-registry-merge-and-routing]
                │               │
                │               └─→ [37 mcp-renderer-view]
                │                       │
                │                       └─→ [39 skill-user-invocation]
                │
                └─→ [36 mcp-main-store-and-ipc]
                        │
                        └─→ [37 mcp-renderer-view]
```

### 7.3 每份子 spec 的核心要点（占位，详细设计在各自 spec）

#### 7.3.1 spec 31 — skills-loader-and-registry（Go 端）

**范围**：
- `src/darvin-agent/internal/skills/` 新建
- `loader.go`：扫 SKILL.md + 解析 frontmatter
- `registry.go`：进程级 `SkillRegistry{byID map[string]*SkillEntry}`
- `scanner.go`：基于 `go/parser` + 正则的安全扫描
- `types.go` + `runner.go`（skill 命令执行入口）
- 单测 3 个

**FR 要点**：
- 4 类 Source（bundled / user / github / npm）的统一接口 `SkillSource`
- bundled 用 `//go:embed skills-bundled` 嵌入（v0 含 2 个：code-review / api-design 作为 PoC）
- 安全扫描 5 维度评分（同 LobsterAI 阈值）；high/critical 直接拒
- `enabled=false` 的 skill 不进 AgentToolList

**非目标**：
- 不做 install / uninstall（spec 32 落地）
- 不做 IPC（spec 32 落地）
- 不做 renderer UI

---

#### 7.3.2 spec 32 — skills-ipc-and-bootstrap（Go ↔ main IPC + main 端 manager）

**范围**：
- Go 端：`internal/gateway/handlers.go` 新增 RPC `agent.skills.list` / `agent.skills.set_enabled` / `agent.skills.bootstrap` / notification `agent.skills.changed`
- main 端：`src/main/libs/skillManager.ts` 新建（~800 行；远小于 LobsterAI 的 3248，因为不做 marketplace）
- bundled：实际打包 5 个 skill（code-review / api-design / testing / web-search / docx）
- IPC handler 注册到 `src/main/index.ts`

**FR 要点**：
- `bootstrap()` 时把 `userData/SKILLs/*` 扫描一遍 + 通过 `agent.skills.bootstrap` 推给 Go
- 用户改 enabled → `agent.skills.set_enabled` → Go 更新 registry + emit `agent.skills.changed` → main 转发 renderer
- `onSkillsChanged` push 模式（与现有 `onSessionsChanged` 一致）

**非目标**：
- 不做 install / uninstall / upgrade（spec 33 落地）
- 不做 marketplace 拉取

---

#### 7.3.3 spec 33 — skills-renderer-view（renderer UI）

**范围**：
- `src/renderer/views/SkillsView.vue` 新建
- `src/renderer/composables/useSkills.ts` 新建
- `src/renderer/services/skillService.ts` 新建
- `src/renderer/components/skills/` 新建 4 个组件：
  - `SkillCard.vue`（单个 skill 卡片）
  - `SkillMarketplace.vue`（本地安装 / GitHub 仓库 / npm spec 输入）
  - `SkillSecurityReportModal.vue`（medium 风险时弹窗）
  - `SkillSettingsPanel.vue`（bundled skill 的禁用 / 启用）
- `src/renderer/layout/AppShell.vue` 移除 `skills` 的 `PlaceholderView` 路由
- i18n 新增 ~30 key（zh + en）

**FR 要点**：
- 三个 tab：`已安装` / `市场` / `设置`
- 卡片显示：name / description / version / enabled 开关 / [详情]
- 安装流程：本地文件 / GitHub URL / npm spec
- 升级流程：v0 显式按钮触发
- 卸载：bundled 不允许；用户安装的可卸

**非目标**：
- 不做 chat 内 `/skill-name` 触发（spec 39）
- 不做 MCP

---

#### 7.3.4 spec 34 — mcp-transport-and-client（Go 端 transport + JSON-RPC）

**范围**：
- `src/darvin-agent/internal/mcp/` 新建
- `transport/stdio.go`：spawn 子进程 + 双向 pipe JSON-RPC frame
- `transport/http.go`：HTTP POST + optional SSE 长连接
- `client.go`：JSON-RPC 2.0 client（`initialize` / `tools/list` / `tools/call`）
- 单测 2 个

**FR 要点**：
- 帧格式：`Content-Length: N\r\n\r\n<body>`（LSP / JSON-RPC over stdio 标准）
- `initialize` 握手：客户端 `protocolVersion="2024-11-05"`，server `capabilities` 记录
- `tools/list` 返回 tool 描述符 → 缓存到 `McpClient.tools`
- `tools/call` 转发 + 收 response 或 error
- transport 断开自动重连（最多 3 次，指数退避）

**非目标**：
- 不做 SSE transport（v1）
- 不做 OAuth / auth（v1）
- 不做 launcher / registry（spec 35）

---

#### 7.3.5 spec 35 — mcp-registry-and-launcher（Go 端 registry + launch 优化）

**范围**：
- `src/darvin-agent/internal/mcp/registry.go`：进程级 `McpRegistry`，按 serverId 索引 client + tools
- `src/darvin-agent/internal/mcp/launcher.go`：4 类 resolver（npx / uvx / go / raw），npx 优化前置安装
- `src/darvin-agent/internal/mcp/resolver_fingerprint.go`：源指纹 hash
- 单测 3 个

**FR 要点**：
- 复用 LobsterAI 的 4 类 `resolverKind`：`npx` / `uvx` / `go` / `raw`
- npx 优化：`npm view <pkg>@<ver> version --json` → `npm install --prefix <dir> --omit=dev --no-audit --no-fund` → 读 `node_modules/<pkg>/package.json` 的 bin → 生成 `node <abs-bin>` 启动命令
- 指纹 hash：`sha256(command|args|env|platform|arch)`，用户改配置后旧结果失效
- 状态机：`pending → installing → ready | failed | unsupported`
- 重试 API + 陈旧 installing 自动重试（启动时扫一遍）

**非目标**：
- 不做 OAuth
- 不做 main 端 store（spec 36）

---

#### 7.3.6 spec 36 — mcp-main-store-and-ipc（main 端 manager + SQLite + IPC）

**范围**：
- `src/main/libs/mcpManager.ts` 新建（~600 行）
- `src/main/libs/mcpStore.ts` 新建（SQLite 封装，~300 行）
- `src/shared/darvin-api.ts` 增量（`DarvinMcpServer` 等）
- `src/main/index.ts` 注册 IPC handler
- `src/preload/index.ts` 暴露 `window.darvin.mcp.*`
- bundled 1 个 MCP server：filesystem（内置，filesystem tools）
- 数据库迁移：加 `mcp_servers` + `mcp_launch_resolutions` 表

**FR 要点**：
- `listMcpServers` / `createMcpServer` / `updateMcpServer` / `deleteMcpServer` / `setMcpServerEnabled` / `testMcpConnection` / `retryLaunchResolution`
- `onMcpServersChanged` / `onMcpConnectionChanged` push
- 启用 server 后：通知 Go 端 `agent.mcp.bootstrap` + Go 异步连接
- Go 端发 connection changed → main 更新 SQLite + push renderer

**非目标**：
- 不做 renderer UI（spec 37）
- 不做 OAuth

---

#### 7.3.7 spec 37 — mcp-renderer-view（renderer UI）

**范围**：
- `src/renderer/views/McpView.vue` 新建
- `src/renderer/composables/useMcpServers.ts` 新建
- `src/renderer/services/mcpService.ts` 新建
- `src/renderer/components/mcp/` 新建 3 个组件：
  - `McpServerCard.vue`
  - `McpServerFormModal.vue`（新增 / 编辑）
  - `McpConnectionStatus.vue`（状态徽章）
- 移除 `mcp` 的 `PlaceholderView` 路由
- i18n 新增 ~35 key（zh + en）

**FR 要点**：
- 列表：name / description / enabled 开关 / transport 类型 / 连接状态徽章
- 新增 modal：按 transportType 切 form（stdio：command + args + env；http：url + headers）
- 测试连接：触发 `testMcpConnection`，展示 tools 列表
- 删除 / 编辑 / 启停

**非目标**：
- 不做 marketplace 拉取
- 不做 OAuth 流程 UI

---

#### 7.3.8 spec 38 — tool-registry-merge-and-routing（tool 层统一入口）

**范围**：
- Go 端：`internal/agent/tool/registry.go` 改造——加 `kind` 字段 + session-aware
- Go 端：`internal/agent/tool/skill.go` 新建（skill 工具调用入口）
- Go 端：`internal/agent/tool/mcp.go` 新建（MCP 工具调用入口）
- Go 端：`internal/agent/tool/plugin.go` 新建（统一插件注册接口）
- `internal/gateway/handlers.go` 新增 RPC `agent.tools.list`（合并内置 + skill + mcp）
- `DarvinEvent.tool_start` / `tool_end` 加 `toolKind: 'skill' | 'mcp' | ...` + `serverId?` + `skillPath?`
- Renderer `useMessages` 已能消费 kind 字段（spec 02 落地）

**FR 要点**：
- `ToolRegistry.Get(name string, sessionID string)` 合并查表（built-in + skill + mcp）
- tool_use 触发：内置走 `executor.runXxx`；skill 走 `skills.Runner.Execute(skillID, args)`；mcp 走 `mcp.Client.Call(serverID, toolName, args)`
- `tool_start` event 带 `kind` + 来源标识（让 renderer 决定渲染样式）

**非目标**：
- 不改 renderer 渲染逻辑（kind 字段加好，渲染层已支持）
- 不做 chat `/skill-name` 触发（spec 39）

---

#### 7.3.9 spec 39 — skill-user-invocation（chat 内 `/skill-name` 显式触发）

**范围**：
- Renderer `Composer` 检测 `/` 前缀 → 弹 skill 自动补全列表
- main 端：截获以 `/` 开头的 prompt → 解析为 skill 触发
- Go 端：`internal/skills/runner.go` 加 `ExecuteByUserInvocation(skillID, args string)` 入口
- DarvinApi 新增 `invokeSkill(req: { skillId, args })`（不走 prompt 直接走 skill runner）
- i18n 新增 ~10 key（zh + en）

**FR 要点**：
- `/skill-name <args>` 模式：main 拦截 → 校验 skill 存在 + `userInvocable=true` → 调 Go 端 ExecuteByUserInvocation → 跑一遍 mini-agent（只喂 skill prompt + tools）→ emit 跟普通 prompt 一样的事件流
- 取消 `/` 触发只把 `/skill-name` 当普通文本前缀（前缀被 `/` 转义）

**非目标**：
- 不做 `/` 的自然语言理解（前缀必须明确 `/skill-name`）
- 不做 MCP 的 `/` 触发（MCP 工具走 LLM 决策）

---

## 8. 边界情况

| 场景 | 处理方式 |
|------|---------|
| App 启动时 Go agent 未就绪 | main 端 manager 缓存 skill/mcp 操作 + 等 Go 起来再 flush |
| 用户安装 skill 时 Go agent 已死 | main 端写 SQLite + 本地文件，下次启动 Go bootstrap 时拉取 |
| SKILL.md frontmatter 解析失败 | 整个 skill 拒绝加载，记 warn 日志 |
| SKILL.md 文件 > 256KB | 拒绝加载，提示「skill 文件过大」 |
| 安全扫描超时（>5s） | 视为 medium 风险，弹安全报告 modal 让用户决定 |
| 安全扫描评分 high / critical | 强制阻断安装，提示「风险过高，禁止安装」 |
| MCP 连接失败（stdio 子进程崩溃） | 自动重试 3 次（指数退避），最终 `connectionStatus=error` + error 消息 |
| MCP 工具调用超时（>30s） | Go agent emit `tool_end` with `isError=true` + `output=timeout` |
| MCP transport 断开（stdin EOF / http 500） | 断开 + 尝试重连；UI 状态从 `connected` → `connecting` → `connected` 或 `error` |
| 用户安装 / 卸载 skill 时 Go agent 正在调用该 skill | 当前调用走完；新调用不再路由；skill_runner 引用计数清零后回收资源 |
| bundled skill 文件被用户手动改 | 不阻止（frontmatter 改了会被检测到，下次 prompt 走新内容）但 i18n 提示「bundled skill 修改可能被升级覆盖」 |
| 用户在 darvin-cowork 装同 id 的 skill 两次（不同来源） | 第二次安装拒绝，提示「skill id 已存在」 |
| Go agent 给出的 tools 列表 > 100 个 | 提示「工具过多，agent 可能调用效率下降」；不动（不强行截断） |
| SQLite busy / locked | main 端 retry 3 次；失败提示用户重试 |
| npm install 失败（npx resolver） | launchStatus = `failed`，保留原始 command 不替换，UI 提示用户重试 |
| Go install 失败（go resolver） | 同上 |

---

## 9. 涉及文件（汇总，按子 spec 归并）

### 9.1 renderer（src/renderer/）

| 文件 | 变更 | 来自 spec |
|------|------|-----------|
| `views/SkillsView.vue` | 🆕 | 33 |
| `views/McpView.vue` | 🆕 | 37 |
| `composables/useSkills.ts` | 🆕 | 33 |
| `composables/useMcpServers.ts` | 🆕 | 37 |
| `services/skillService.ts` | 🆕 | 33 |
| `services/mcpService.ts` | 🆕 | 37 |
| `components/skills/SkillCard.vue` | 🆕 | 33 |
| `components/skills/SkillMarketplace.vue` | 🆕 | 33 |
| `components/skills/SkillSecurityReportModal.vue` | 🆕 | 33 |
| `components/skills/SkillSettingsPanel.vue` | 🆕 | 33 |
| `components/mcp/McpServerCard.vue` | 🆕 | 37 |
| `components/mcp/McpServerFormModal.vue` | 🆕 | 37 |
| `components/mcp/McpConnectionStatus.vue` | 🆕 | 37 |
| `services/i18n.ts` | +~75 key | 33 + 37 + 39 |
| `layout/AppShell.vue` | 移除 `skill` / `mcp` 的 PlaceholderView 路由 | 33 + 37 |
| `views/PlaceholderView.vue` | 不动（仍被 `scheduled` 用） | — |
| `assets/icons/` | +`plugin.svg` `plug.svg` `refresh.svg` `shield.svg` `terminal.svg` `cube.svg` `trash.svg` | 33 + 37 |

### 9.2 preload（src/preload/）

| 文件 | 变更 | 来自 spec |
|------|------|-----------|
| `index.ts` | +`window.darvin.skills.*` + `window.darvin.mcp.*` | 32 + 36 |

### 9.3 main（src/main/）

| 文件 | 变更 | 来自 spec |
|------|------|-----------|
| `index.ts` | +`skills:*` + `mcp:*` IPC handler 注册 | 32 + 36 |
| `libs/skillManager.ts` | 🆕 | 32 |
| `libs/skillSecurityScanner.ts` | 🆕（也可由 Go agent 做，看 4.5.3） | 32 |
| `libs/mcpManager.ts` | 🆕 | 36 |
| `libs/mcpStore.ts` | 🆕（SQLite） | 36 |
| `libs/user-paths.ts` | +`getSkillsRoot()` + `getMcpStoreDir()` | 32 + 36 |
| `runtime/client.ts` | +`listSkills` / `setSkillEnabled` / `listMcpServers` / `setMcpServerEnabled` / `testMcpConnection` / `retryLaunchResolution` | 32 + 36 + 38 |

### 9.4 shared（src/shared/）

| 文件 | 变更 | 来自 spec |
|------|------|-----------|
| `darvin-api.ts` | +`DarvinToolKind` 增 `'skill' | 'mcp'`；+`DarvinSkillSummary` + `DarvinMcpServer` + `DarvinApi.skills.*` + `DarvinApi.mcp.*` | 31 + 34 + 36 |

### 9.5 Go agent（src/darvin-agent/）

| 文件 | 变更 | 来自 spec |
|------|------|-----------|
| `internal/skills/loader.go` | 🆕 | 31 |
| `internal/skills/registry.go` | 🆕 | 31 |
| `internal/skills/scanner.go` | 🆕 | 31 |
| `internal/skills/types.go` | 🆕 | 31 |
| `internal/skills/runner.go` | 🆕 | 31 + 39 |
| `internal/skills/loader_test.go` | 🆕 | 31 |
| `internal/skills/registry_test.go` | 🆕 | 31 |
| `internal/skills/scanner_test.go` | 🆕 | 31 |
| `internal/mcp/transport/stdio.go` | 🆕 | 34 |
| `internal/mcp/transport/http.go` | 🆕 | 34 |
| `internal/mcp/transport/transport.go` | 🆕（interface） | 34 |
| `internal/mcp/client.go` | 🆕 | 34 |
| `internal/mcp/registry.go` | 🆕 | 35 |
| `internal/mcp/launcher.go` | 🆕 | 35 |
| `internal/mcp/resolver_fingerprint.go` | 🆕 | 35 |
| `internal/mcp/types.go` | 🆕 | 35 |
| `internal/mcp/transport/stdio_test.go` | 🆕 | 34 |
| `internal/mcp/client_test.go` | 🆕 | 34 |
| `internal/mcp/launcher_test.go` | 🆕 | 35 |
| `internal/agent/tool/skill.go` | 🆕 | 38 |
| `internal/agent/tool/mcp.go` | 🆕 | 38 |
| `internal/agent/tool/plugin.go` | 🆕 | 38 |
| `internal/agent/tool/registry.go` | 改造：加 kind 字段 + session-aware | 38 |
| `internal/gateway/handlers.go` | +`agent.skills.*` + `agent.mcp.*` + `agent.tools.list` | 32 + 36 + 38 |
| `resources/skills-bundled/code-review/SKILL.md` | 🆕（bundled skill 示例 1） | 32 |
| `resources/skills-bundled/api-design/SKILL.md` | 🆕（bundled skill 示例 2） | 32 |
| `resources/skills-bundled/testing/SKILL.md` | 🆕（bundled skill 示例 3） | 32 |
| `resources/skills-bundled/web-search/SKILL.md` | 🆕（bundled skill 示例 4，含脚本） | 32 |
| `resources/skills-bundled/docx/SKILL.md` | 🆕（bundled skill 示例 5，含脚本） | 32 |
| `resources/mcp-bundled/filesystem/server.go` | 🆕（内置 MCP server，filesystem 工具） | 36 |
| `cmd/app/main.go` | +bootstrap skills + bootstrap mcp + skills-bundled embed | 31 + 32 + 36 |

### 9.6 spec / docs（不涉及代码 review 但要同步）

| 文件 | 变更 |
|------|------|
| `specs/features/skills-and-mcp-modules/`（本目录） | 🆕 主 spec + 9 份子 spec |
| `docs/agent/04_SKILLS_SYSTEM.md` | 同步：从 OpenClaw 参考改写为 darvin-cowork 实现说明 |
| `docs/agent/05_MCP_INTEGRATION.md` | 同步：去掉 `@modelcontextprotocol/sdk` 引用，改写为 Go 自实现 |
| `docs/plan/agent-package-roadmap.md` | P3 / P4 标记为「已拆分到子 spec 31-39」 |

---

## 10. 验收标准

### 10.1 子 spec 各自的验收标准（占位）

每份子 spec 自带 `## 7. 验收标准` 节，包含：

- [ ] 单元测试通过（具体数字随子 spec）
- [ ] `npm run lint` + `npm run test` 通过
- [ ] `cd src/darvin-agent && go build ./... && go vet ./...` 通过
- [ ] live 手动验证（playwright 驱动 Electron，截图 + console 无 error）

### 10.2 主 spec 验收（全部 9 份子 spec 落地后）

- [ ] 侧栏 `技能` 跳 `SkillsView`，看到 5 个 bundled skill（code-review / api-design / testing / web-search / docx）
- [ ] 关掉 `web-search` 后发 prompt「搜索 Go embedding」，agent 不调用 web_search
- [ ] 从本地装一个 SKILL.md，安全扫描 safe 直接装；medium 弹安全报告 modal
- [ ] 从 GitHub 装一个 skill，下载 + 扫描 + 装上
- [ ] 升级一个 skill，.env 保留
- [ ] 卸载一个用户安装的 skill
- [ ] 侧栏 `MCP` 跳 `McpView`，看到 bundled `filesystem` MCP（已启用 + 已连接）
- [ ] 新增一个 stdio MCP（如 github），走 npx 优化，状态 connecting → connected，列出 4 个工具
- [ ] 新增一个 http MCP，状态同上
- [ ] 失败重试：把 MCP URL 改错，看 error 状态 + error 消息；改回正确 URL 后 retry 成功
- [ ] agent 实际调用 MCP 工具，renderer `ToolCallGroup` 按 `kind: 'mcp'` 渲染（显示 server 来源）
- [ ] agent 实际调用 skill 工具，renderer `ToolCallGroup` 按 `kind: 'skill'` 渲染
- [ ] `/code-review src/api/handler.go` 触发 skill（不走 LLM 决策）
- [ ] SQLite 表 `mcp_servers` + `mcp_launch_resolutions` 创建成功
- [ ] `npm run lint` + `npm run test` + `npm run build:agent` + `go test ./...` 全绿
- [ ] i18n 新增 75+ key，zh / en 双语齐全，`assertSameKeys` 通过

### 10.3 非功能验收

- [ ] App 启动时间增加 ≤ 500ms（bundled 5 skill + 1 MCP）
- [ ] 安全扫描 5s timeout 严格执行（手工构造 100MB 文件验证）
- [ ] MCP 连接失败不阻塞 UI（async 触发，UI 显示 connecting → error）
- [ ] 不引入 npm 依赖（除单测工具）
- [ ] Go agent 二进制增量 ≤ 5MB（5 bundled skill + 1 MCP server）
- [ ] 安全扫描通过率 0 误报（已用 code-review / api-design 验证）

---

## 11. 状态变更日志

> 每完成一份子 spec，在此处加一行。

- 2026-08-02 · 00（主 spec） · 完成调研 + checklist + 子 spec 拆分；待用户确认后启动 spec 31