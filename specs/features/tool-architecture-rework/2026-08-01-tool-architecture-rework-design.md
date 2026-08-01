# Tool Architecture Rework 设计文档

> Tier 2：把工具层从「单 main module 平铺代码」升级为「plugin 隔离 + session 可见性 + workspace 状态机 + reconcile」的可演进架构。覆盖 plugin 隔离、tool factory / sessionKey、workspace 状态机与操作 coordinator、workspace reconcile + sync。

## 1. 概述

### 1.1 问题 / 背景

Tier 1 把工具层「正确性」拉到 OpenClaw 同级，但**架构层**仍有结构性短板：

1. **没有 plugin 边界**。
   - `internal/agent/tool/builtins.go` 与 `fs.go` / `shell.go` / `registry.go` 全在同一 Go module（`src/darvin-agent`），加一个 tool（如 `git`、`docker`、`web_fetch`）必须改 `darvin-agent` 仓库——违反 OpenClaw / Claude Code 的「plugin 可独立分发」惯例。
   - 当前没有 `internal/agent/tool/plugin/` 目录、没有 driver 接口、没有 entry-point 协议。
2. **Registry 是全局单例，无 session 上下文**。
   - `registry.go:50-54` `Get(name)` 返回全局 tool，忽略 `sessionID`。
   - 现实需求：某些 tool 是「per-session enabled」（如 `bash` 只在用户开 `dangerousMode` 时挂载）；某些是「per-session 配置」（如 `git` tool 需要绑到当前 session 的 working dir）。
   - 当前架构下要让 tool 感知 session，要么走全局可变状态（反模式），要么在每个 tool 内部 `sync.Map` 维护 session → config（绕过 Registry）。
3. **Workspace 没有状态机 + 没有 coordinator**。
   - 当前 workspace 只是一个 fs sandbox（`fsSandbox`）+ session 的 `workdir` 字段；多个 tool 并发对同一文件 `write_file` / `edit_file` / `shell > rm` 时，工具层没有任何串行化与冲突检测。
   - 没有「事务边界」（一组文件改要么全成功要么全回滚），也没有「finalize 验证」（改完之后跑 `gofmt` / `tsc --noEmit` 之类 sanity check）。
4. **Workspace 没有 reconcile / sync**。
   - 当前 workspace 状态只活在内存（sandbox root + session.workdir 字段）；用户重启应用、打开另一个 device、或外部 git pull 后，workspace 视图与「实际」会有偏差。
   - 同步目标至少两类：（a）主进程 SQLite（`src/main/store/SessionStore.ts`，已在 S7 落地）；（b）git / 本地 fs 真实状态。当前双向同步逻辑完全空缺。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 第三方 tool 可作独立 Go module 分发并被 `darvin-agent` 加载 | 一个示例 `example-plugin/` 走 `internal/agent/tool/plugin/loader.go` 跑通；plugin 注册 + 调用闭环 |
| G2 | Tool 可见性按 session 维度过滤，tool 工厂模式注入 context | `Registry.Get(name, sessionKey)` 返 `Tool` 或 `nil`；tool 实现可选地实现 `SessionAware` 接口拿 `SessionContext` |
| G3 | Workspace 引入状态机（idle → planning → mutating → verifying → finalized）与 coordinator | 多 tool 并发改同一文件时序列化；plan/commit 两阶段；verify 失败可回滚 |
| G4 | Workspace 与 main 进程 SQLite 双向 reconcile（changelog + 反查 fs） | 启动时比对 memory vs fs，差异生成 reconciliation events；运行期每次 commit 推 changelog 进 SQLite |

### 1.3 非目标

- **不做 plugin marketplace / 远端下载**：v0 plugin 通过 Go workspace / 本地文件加载；远端分发留作后续。
- **不做 plugin 多语言**（WASM / Lua）：v0 仅 Go plugin（用 `yaegi` 解释执行或 `go plugin` 编译加载，二选一）。
- **不做 git operation primitives**（`git_tool` 实现）：Tier 2 只搭架构；具体 `git_tool` 留作 Tier 3。
- **不做 CRDT / OT 类协同编辑算法**：并发冲突用 last-write-wins + 显式 reconcile；CRDT 留作更远期。
- **不做网络共享 workspace**：v0 workspace 是 per-process local；跨进程 / 跨 device 留作远期。

## 2. 用户场景

### 场景 1: 第三方加 tool 不动 `darvin-agent` 主仓库

**Given** 某开发者 fork 出 `darvin-plugin-git`，含 `go.mod` + `main.go` 实现 `git_tool`。
**When** 该开发者把 `darvin-plugin-git` clone 到 `~/.darvin-cowork/plugins/git/`，并 `go build -o ~/.darvin-cowork/plugins/git/darvin-plugin-git .so`。
**Then** `darvin-agent` 启动时 `plugin.Loader` 扫 `~/.darvin-cowork/plugins/`，加载该 .so，调其 `Register(reg PluginRegistry)` 入口，把 `git_tool` 挂进全局 registry；用户不需要 PR `darvin-agent` 主仓库。

### 场景 2: 不同 session 看到不同 tool 集合

**Given** session A 启用 `dangerousMode`（开了 `bash` / `shell` 全功能），session B 是只读模式（仅 `read_file` / `list_dir`）。
**When** agent loop 在 session A 调 `tool_registry.Get("shell", "session-A")` 返 tool 实例；在 session B 调同一个名字返 `nil`。
**Then** LLM 在 session B 尝试调 `shell` 收到「tool not available in this session」错误，工具层在 schema 层就不暴露该 tool。

### 场景 3: 多 tool 并发改同一文件不丢改

**Given** session 内两个并发 `edit_file` 调用，分别改 `main.go` 第 10 行与第 50 行。
**When** 两个调用同时进。
**Then** coordinator 串行化（mutex per file），第二个等第一个完成后读最新内容，再做 replace；不会发生「read-modify-write」丢失。

### 场景 4: 一组文件改动要么全成要么回滚

**Given** LLM 决定「重构：rename Foo → Bar 涉及 5 个文件」。
**When** LLM 调 `workspace.applyPlan` 提交 plan（5 个 file edit）。
**Then** coordinator 进入 `planning` 状态 → 校验 plan（路径全在 sandbox 内、文件存在、edit 不冲突） → 进入 `mutating` 顺序执行 → 每个文件改前备份 → 任一失败 → 回滚已成功的文件 → 状态机回到 `idle`，返错误给 LLM。

### 场景 5: 应用重启后 workspace 视图与磁盘一致

**Given** session 内 memory 记录了 3 个 file change；用户强杀应用。
**When** 重启应用，恢复 session。
**Then** Workspace reconciler 比对 memory changelog 与 fs 实际状态：若 fs 与 memory 一致 → 进入 `finalized`；若 fs 被外部改动 → 反查（mtime / size / hash）→ 生成 `ReconcileDiff` event，标记 session 为 `dirty`，LLM 看到 dirty 提示后可用。

### 场景 6: workspace 与 main 进程 SQLite 双向同步

**Given** main 进程 S7 已落地 `SessionStore`（`darvin-cowork.sqlite`），存 session + message + active。
**When** Go agent 的 workspace coordinator 完成一次 commit（mutating → verifying → finalized）。
**Then** 通过 `bridge.WorkspaceBridge` 把 `WorkspaceChangelog{commitID, ops[]}` 推到 main；main 写入 `workspace_changelog` 表（schema 新增）；下次启动 main 主动反查时把未应用的 changelog 喂回 Go agent。

## 3. 功能需求

### FR-5: Plugin 隔离机制

**目录布局**（新增）：

```
src/darvin-agent/
├── internal/agent/tool/plugin/      # 新增
│   ├── loader.go                    # 扫 ~/.darvin-cowork/plugins/*/*.so
│   ├── driver.go                    # PluginDriver interface
│   ├── host.go                      # 暴露给 plugin 的 host API（受限）
│   ├── manifest.go                  # PluginManifest{Name, Version, Tools, Permissions}
│   ├── loader_test.go
│   └── host_test.go
├── plugins/                         # 新增（in-tree examples）
│   └── example/
│       ├── plugin.go                # 最小可加载 plugin
│       └── plugin_test.go
```

**PluginDriver 接口**：

```go
type PluginDriver interface {
    Name() string                                    // "git" / "docker" / ...
    Version() string                                 // semver
    Manifest() PluginManifest
    Register(api PluginAPI) error                    // plugin 把自己要注册的 tool + hook 报到 host
}

type PluginAPI interface {
    RegisterTool(t Tool) error                       // 受限：Name 必须以 "<plugin>/" 前缀（如 "git/commit"）
    On(event string, cb func(ctx PluginContext, payload any)) error  // 订阅 host 事件
    Log() *zap.Logger                                // 受限 zap（结构化）
    Workspace() WorkspaceAccessor                    // 受限的 workspace 访问
}
```

**Loader 行为**：

1. 启动时扫 `~/.darvin-cowork/plugins/`（config 可改；v0 默认路径）：
   - Linux：`$XDG_DATA_HOME/darvin-cowork/plugins` 或 `~/.local/share/darvin-cowork/plugins`
   - macOS：`~/Library/Application Support/darvin-cowork/plugins`
   - Windows：`%APPDATA%\darvin-cowork\plugins`
2. 对每个 `*.so` 用 `plugin.Open`（Go 标准库 `plugin` package）。
3. 调约定符号 `NewPluginDriver` 拿到 `PluginDriver`。
4. 调 `driver.Register(api)`；api 内部调 `RegisterTool` 把 tool 加进全局 registry（**工具名前缀 `<plugin-name>/`**，避免与 built-in 冲突）。
5. 加载失败 → `logger.Warn("plugin load failed", "name", ..., "err", ...)`，不阻断 darvin-agent 启动。

**Plugin Tool 命名空间**：

- Built-in tool 走原名（`read_file` / `shell` 等）。
- Plugin tool 走 `git/commit` / `git/log` / `docker/run` 等 `<plugin>/<action>` 形式。
- Registry.Names() 返回全名（带前缀），LLM 看到的 schema name 也带前缀。

**受限 host API**（plugin 不能直接 `os/exec` / 任意读写）：

- 走 `WorkspaceAccessor`，所有 fs 操作经 sandbox。
- shell 执行受限于 built-in `shell` tool 同款 allowlist（plugin 不能自带 allowlist 绕过；除非 manifest 声明 `permissions: ["shell:custom-allowlist"]` 且用户首次启用时确认）。
- 日志走 `host.Log()`（plugin 不能 `fmt.Println` 到 stdout，stdout 是 gateway `<port>` 单行契约）。

**v0 加载策略**：`plugin.Open` 仅 Linux / macOS 支持（Go plugin package 限制）；Windows 走**编译期 embed**（v0 不支持，等 Tier 3 用 wasmtime 替代）。在 Windows 上 plugin loader 退化为「无 plugin 加载」+ log warn。

### FR-6: Tool factory + sessionKey 上下文

**Registry 扩展**（`registry.go`）：

```go
// SessionVisibility 描述一个 tool 在哪些 session 下可见。
type SessionVisibility int

const (
    VisibilityGlobal  SessionVisibility = iota // 所有 session 都能见
    VisibilitySessionOnly                      // 仅特定 session（factory 按 sessionKey 创建实例）
    VisibilityDisabled                         // 全不可见（用于 hot-off）
)

// SessionKey 是 session 的稳定标识。当前 v0 直接用 session.ID()。
type SessionKey string

// Factory 是「按 session 创建 tool 实例」的工厂。
// 当一个 tool 内部需要 session-scoped 状态（如 git tool 绑 working dir），实现它。
type Factory func(sk SessionKey) (Tool, error)

type registration struct {
    name        string
    factory     Factory                  // 必有
    visibility  SessionVisibility
    schema      llm.ParameterSchema      // 来自 factory 第一次创建时的 tool
}

type Registry struct {
    mu    sync.RWMutex
    regs  map[string]*registration
    cache sync.Map                     // sk SessionKey → map[string]Tool（实例缓存）
}

func (r *Registry) Register(name string, factory Factory, vis SessionVisibility) error
func (r *Registry) Get(name string, sk SessionKey) Tool   // 改了签名
func (r *Registry) VisibleTools(sk SessionKey) []llm.Tool // 按 session 过滤的 schema
```

**Tool 接口不变**（`tool.go`）：

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() llm.ParameterSchema
    Execute(ctx context.Context, args map[string]any) Result
}

// SessionAware 是可选接口；factory 创建的实例若实现它，会被 host 在 Execute 前调 SetSession。
type SessionAware interface {
    SetSession(sk SessionKey, sess SessionContext) error
}
```

**SessionContext 注入**（`internal/agent/session/` 加）：

```go
type SessionContext interface {
    Workdir() string                   // sandbox root
    Config() SessionConfig             // 用户级配置快照（language / dangerousMode / ...）
    Memory() SessionMemory             // 记忆读写接口
    Logger() *zap.Logger
    Cancel() context.Context
}
```

**Built-in tool 适配**：

- `readFileTool` / `writeFileTool` / `editFileTool` / `listDirTool` 是 `VisibilityGlobal`：factory 直接返同一个 sandbox-backed 实例（无 per-session 状态）。
- `shellTool` 视配置：默认 `VisibilityGlobal`；若 session.Config().DangerousMode 为 true 才挂载（由 main 在构造 Registry 时根据 session metadata 决定）。
- 后续 plugin tool（如 `git_tool`）实现 `SessionAware`：`SetSession` 时把 `sess.Workdir()` 存住，每次 `Execute` 都基于该 workdir。

**兼容性**：

- 旧 `Registry.Get(name)` 调用点改成 `Registry.Get(name, sessionKey)`；一处一处迁移（agent loop / dispatcher / executor 各一处）。
- 旧 `MustRegister(t Tool)` 改成 `MustRegister(name string, factory Factory, vis SessionVisibility)`，提供适配器：`MustRegisterTool(t Tool)` 等价于 `MustRegister(t.Name(), func(SessionKey) (Tool, error) { return t, nil }, VisibilityGlobal)`。
- `builtins.go` 改造：

```go
func NewBuiltins(workdir string, allowlist []string) (*Registry, error) {
    sb, err := newFsSandbox(workdir, nil)
    if err != nil { return nil, err }
    reg := NewRegistry()
    reg.MustRegisterTool(&readFileTool{sb: sb})    // 适配器
    reg.MustRegisterTool(&writeFileTool{sb: sb})
    reg.MustRegisterTool(&editFileTool{sb: sb})
    reg.MustRegisterTool(&listDirTool{sb: sb})
    reg.MustRegisterTool(newShellTool(sb, allowlist))
    return reg, nil
}
```

### FR-7: Workspace 状态机 + 操作 coordinator

**状态机**（新增 `internal/agent/workspace/state.go`）：

```go
type State int

const (
    StateIdle      State = iota // 无 in-flight 操作；可启动 plan
    StatePlanning              // 收到 plan；正在校验
    StateMutating              // 校验通过；正在执行 file ops
    StateVerifying             // 改完；正在跑 verify hooks
    StateFinalized             // 成功；可读
    StateDirty                 // 与 fs 不一致（reconcile diff）
    StateRolledBack            // 出错回滚后；与 StateIdle 等价但保留 diagnostic
)

func (s State) String() string { /* "idle" / "planning" / ... */ }
```

合法转移（任何不合法转移返 `ErrInvalidStateTransition`）：

```
idle ──StartPlan()──▶ planning
planning ──ValidateOK()──▶ mutating
planning ──ValidateFail()──▶ idle (with RolledBack state set)
mutating ──AllOpsOK()──▶ verifying
mutating ──OpFail()──▶ rollback ──▶ rolledback
verifying ──VerifyOK()──▶ finalized
verifying ──VerifyFail()──▶ rollback ──▶ rolledback
finalized ──NewExternalChange()──▶ dirty
dirty ──ReconcileOK()──▶ idle
```

**Coordinator**（新增 `internal/agent/workspace/coordinator.go`）：

```go
type Op struct {
    Kind    OpKind              // OpWrite / OpEdit / OpDelete / OpShell
    Path    string              // sandbox 内路径
    Payload []byte              // write 内容 / edit old→new 已预计算
    // edit 用：Payload = oldText, Extra = newText；由 coordinator 读最新内容再 replace
    Extra   []byte
}

type Plan struct {
    ID    string                // nanoid 21 字符
    Ops   []Op
    Verify []VerifyHook         // 可选：plan-level verify 函数集合
}

type VerifyHook func(ctx context.Context, ws Workspace) error

type Workspace struct {
    sandbox *fsSandbox
    state   *atomicState        // State + 转移锁
    lock    KeyedLocker         // per-path mutex（"github.com/vmihailenco/keyed-locker" 或自实现）
    changelog *Changelog         // 见 FR-8
}

func (w *Workspace) ApplyPlan(ctx context.Context, p Plan) (Result, error)
func (w *Workspace) State() State
```

**关键不变量**（测试断言）：

1. ApplyPlan 启动时必须 State == Idle，否则返 ErrInvalidStateTransition（不允许 nested plan）。
2. Plan 校验阶段（planning）必须验证：
   - 所有 Op 路径在 sandbox 内（走 `sandbox.Resolve`）。
   - Op.Kind == OpEdit 时必须存在 oldText（不是「找不到 oldText 就当 noop」）。
   - 同 plan 内不允许对同一 path 同时 OpWrite + OpEdit（冲突）。
3. Mutating 阶段对每个 Op 按 plan 内顺序串行执行；对每个 path 在执行前 acquire `lock.Lock(path)`，执行后释放。
4. Verify 阶段按 plan.Verify 顺序跑；任一 verify 失败 → 整体回滚（用 changelog 的 inverse op）。
5. finalize 时把整个 plan 的 net effect 写进 `Changelog`（commitID + ops），状态机进入 finalized。

**Built-in tool 走 Workspace**（adapter 模式）：

- `readFileTool` / `listDirTool` 仍直接走 `sandbox`（read-only，不入 plan）。
- `writeFileTool` / `editFileTool` / `shellTool` 改造：调用方改为调 `Workspace.ApplyPlan(Plan{ Ops: [...] })`，而不是直接 sandbox.openRootFile / exec.CommandContext。
  - 内部仍然用 sandbox 做 path containment。
  - 改动涉及 `fs.go` 与 `shell.go` 的 Execute 函数体（业务逻辑几乎不变，只是入口改了）。

**并发模型**：

- 同一 session 内的 plan 串行（State 状态机保证）。
- 跨 session 的 plan **不阻塞**：不同 session 的 workspace 是独立实例（每个 session 自己的 fsSandbox + State），互不干扰。
- 同 session 内「非 plan 操作」（read_file / list_dir）走 `lock.RLock(path)` 短锁，不阻塞其它 read。

### FR-8: Workspace reconcile + sync

**Changelog**（新增 `internal/agent/workspace/changelog.go`）：

```go
type ChangeOp struct {
    Path     string    // sandbox 内相对路径
    Kind     string    // "write" / "edit" / "delete"
    Before   []byte    // 旧内容（删文件时为空）
    After    []byte    // 新内容（删文件时为空）
    HashAlgo string    // "sha256"
    HashBefore string  // 旧内容 sha256
    HashAfter  string  // 新内容 sha256
    Timestamp time.Time
}

type ChangeEntry struct {
    CommitID  string       // nanoid 21 字符
    SessionID SessionKey
    PlanID    string
    Ops       []ChangeOp
    Verified  bool
}

type Changelog struct {
    mu     sync.RWMutex
    entries []ChangeEntry     // in-memory ring buffer；持久化走 bridge
}
```

**Reconciler**（新增 `internal/agent/workspace/reconciler.go`）：

```go
type Reconciler struct {
    workspace *Workspace
    changelog *Changelog
}

type Diff struct {
    Path        string
    Expected    *ChangeOp  // changelog 记录
    Actual      FileStat   // fs 实际：mtime / size / sha256
    Mismatch    string     // "hash" / "missing" / "extra"
}

func (r *Reconciler) Reconcile(ctx context.Context) ([]Diff, error)
```

- Reconcile 启动时机：
  - 应用启动 + session 加载后（一次性）。
  - 收到 fs watcher 事件（inotify / FSEvents / ReadDirectoryChangesW；v0 用 `fsnotify` 跨平台）。
  - LLM / UI 显式 `reconcile_now` 调用。
- Reconcile 算法：
  1. 遍历 changelog 最新 N 条（v0 N = 100，cap 可配）。
  2. 对每条 op 的 Path 走 `os.Lstat` + sha256，与 changelog 记录比对。
  3. 不匹配 → 生成 Diff，累积。
  4. workspace.State 若是 finalized 且 diff 非空 → 转 dirty，emit `WorkspaceReconcileEvent`。
  5. dirty 状态允许 LLM 显式 `accept_changes`（再触发 reconcile 转回 idle）或 `revert_to_changelog`（按 changelog 反向操作回滚）。

**Bridge 到 main 进程 SQLite**（新增 `internal/agent/workspace/bridge.go`）：

```go
type WorkspaceBridge interface {
    PublishChangelog(ctx context.Context, entry ChangeEntry) error
    FetchPending(ctx context.Context) ([]ChangeEntry, error)
}

type WSBridge struct {
    // 通过现有 gateway WS 客户端调 main 端的 'workspace.publish' / 'workspace.fetch'
    client *gateway.Client
}
```

- Go agent → main：plan finalize 后调 `bridge.PublishChangelog(entry)`；main 把 entry 写进 `workspace_changelog` 表。
- main → Go agent：启动时 main 把上次未消费的 entry 喂回（`bridge.FetchPending`），agent 把它们 merge 进 in-memory changelog。
- **冲突解决**：last-write-wins（changelog 按 CommitID 字典序最大者赢）；main 端 `INSERT OR REPLACE`。
- **错误处理**：bridge 调失败不阻断 workspace 本地 finalize；改为本地 finalized + 异步 retry 队列（v0：简单 retry 3 次，失败 log warn 后等下次 reconcile 兜底）。

**SQLite schema（main 端新增）**：

```sql
CREATE TABLE workspace_changelog (
    commit_id    TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    plan_id      TEXT NOT NULL,
    ops_json     TEXT NOT NULL,    -- JSON 序列化 []ChangeOp
    verified     INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX idx_workspace_changelog_session ON workspace_changelog(session_id, created_at DESC);
```

main 端 `SessionStore.ts` 加 `saveWorkspaceChangelog` / `fetchPendingWorkspaceChangelog`（与现有 `saveMessage` / `listSessions` 同 pattern）。

## 4. 实现方案

### 4.1 实施顺序（4 个 FR 之间有依赖）

```
FR-6 (Registry 扩展)         ← 独立，无前置
       │
       ▼
FR-7 (Workspace + Coordinator) ← 依赖 FR-6 的 sessionKey 概念
       │
       ▼
FR-8 (Changelog + Reconciler + Bridge) ← 依赖 FR-7 的 Workspace

FR-5 (Plugin Loader)          ← 独立；但依赖 FR-6 的 Registry 新签名（plugin tool 走 new factory）
```

实施期序：FR-6 → FR-7 → FR-8 → FR-5（每完成一个 FR，全套测试 + smoke 跑一遍后再开下一个）。

### 4.2 文件布局总览（增量）

```
src/darvin-agent/
├── internal/agent/tool/
│   ├── plugin/                 # FR-5
│   │   ├── loader.go
│   │   ├── driver.go
│   │   ├── host.go
│   │   ├── manifest.go
│   │   └── *_test.go
│   ├── registry.go             # FR-6 改
│   ├── tool.go                 # 不变（SessionAware 加进同文件）
│   └── builtins.go             # FR-6 改（用适配器）
├── internal/agent/workspace/   # FR-7 + FR-8 新增
│   ├── state.go                # State 枚举 + 状态机
│   ├── coordinator.go          # Workspace + ApplyPlan
│   ├── changelog.go            # ChangeEntry / Changelog
│   ├── reconciler.go           # Reconciler + Diff
│   ├── bridge.go               # WorkspaceBridge + WSBridge
│   ├── lock.go                 # KeyedLocker 自实现
│   └── *_test.go
├── internal/agent/session/
│   ├── session.go              # 改：加 SessionContext 方法
│   └── ...
├── plugins/                    # FR-5 新增 in-tree examples
│   └── example/
│       ├── plugin.go
│       └── plugin_test.go
├── internal/agent/agent.go     # FR-6 改：tool 注册 + 调用走 sessionKey
├── internal/agent/executor/    # FR-6 改：tool 调用传 sessionKey
├── cmd/app/main.go             # FR-5 + FR-6 + FR-8 改：plugin loader + bridge 注入
└── ...
```

```
src/main/
├── store/
│   └── SessionStore.ts         # FR-8 改：加 workspace_changelog 表 + CRUD
├── runtime/
│   └── client.ts               # FR-8 改：加 workspace.publish / fetch RPC
├── ipc/                        # FR-8 改：加 darvin:workspace_publish 等
└── ...
```

```
src/shared/
└── darvin-api.ts               # FR-8 改：加 WorkspaceChangelog 类型 + RPC 类型
```

### 4.3 FR-5 Plugin Loader 关键代码

```go
// internal/agent/tool/plugin/loader.go
package plugin

type Loader struct {
    dirs []string
    log  *zap.Logger
    api  PluginAPI
}

func (l *Loader) LoadAll(ctx context.Context) (loaded []PluginDriver, errs []error) {
    for _, dir := range l.dirs {
        entries, err := os.ReadDir(dir)
        if err != nil {
            if errors.Is(err, os.ErrNotExist) { continue }
            errs = append(errs, fmt.Errorf("read %s: %w", dir, err))
            continue
        }
        for _, e := range entries {
            if e.IsDir() || !strings.HasSuffix(e.Name(), ".so") { continue }
            drv, err := l.loadOne(ctx, filepath.Join(dir, e.Name()))
            if err != nil { errs = append(errs, err); continue }
            loaded = append(loaded, drv)
        }
    }
    return loaded, errs
}

func (l *Loader) loadOne(ctx context.Context, path string) (PluginDriver, error) {
    p, err := plugin.Open(path)
    if err != nil { return nil, fmt.Errorf("plugin.Open %s: %w", path, err) }
    sym, err := p.Lookup("NewPluginDriver")
    if err != nil { return nil, fmt.Errorf("lookup NewPluginDriver: %w", err) }
    factory, ok := sym.(func() PluginDriver)
    if !ok { return nil, fmt.Errorf("NewPluginDriver signature mismatch") }
    drv := factory()
    if err := drv.Register(l.api); err != nil {
        return nil, fmt.Errorf("Register: %w", err)
    }
    return drv, nil
}
```

### 4.4 FR-6 Registry 新签名细节

```go
// internal/agent/tool/registry.go（FR-6 改造后）
type Factory func(sk SessionKey) (Tool, error)

type registration struct {
    factory    Factory
    visibility SessionVisibility
    schema     llm.ParameterSchema  // factory 首次返的 tool 的 schema；用作 cache
}

type Registry struct {
    mu     sync.RWMutex
    regs   map[string]*registration
    cache  sync.Map                // key = SessionKey, value = map[string]Tool
}

func (r *Registry) Register(name string, factory Factory, vis SessionVisibility) error {
    // 调一次 factory(SessionKey("__probe__")) 拿 schema 缓存；probe 失败返错
    probe, err := factory("__probe__")
    if err != nil { return fmt.Errorf("factory probe failed: %w", err) }
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, ok := r.regs[name]; ok { return ErrAlreadyRegistered }
    r.regs[name] = &registration{
        factory: factory, visibility: vis,
        schema: probe.Parameters(),
    }
    return nil
}

func (r *Registry) Get(name string, sk SessionKey) Tool {
    r.mu.RLock()
    reg, ok := r.regs[name]
    r.mu.RUnlock()
    if !ok || reg.visibility == VisibilityDisabled { return nil }
    // cache 拿 / 建
    if cached, ok := r.cache.Load(sk); ok {
        if t, ok := cached.(map[string]Tool)[name]; ok { return t }
    }
    t, err := reg.factory(sk)
    if err != nil { return nil }
    if sa, ok := t.(SessionAware); ok {
        sess := lookupSession(sk)  // 全局 session registry
        if err := sa.SetSession(sk, sess); err != nil { return nil }
    }
    // double-check 后写入 cache
    r.cache.LoadOrStore(sk, map[string]Tool{name: t})
    return t
}

func (r *Registry) VisibleTools(sk SessionKey) []llm.Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := []llm.Tool{}
    for name, reg := range r.regs {
        if reg.visibility == VisibilityDisabled { continue }
        // session-scoped visibility check：调 reg.factory(sk) 探测 ok？
        // v0 简化：visibility == Global 直接放；SessionOnly 探测 factory。
        if reg.visibility == VisibilitySessionOnly {
            if _, err := reg.factory(sk); err != nil { continue }
        }
        out = append(out, llm.Tool{
            Type: "function", Name: name,
            Description: "", // 注：Description 走 Get(name,sk).Description()，schema cache 已含
            Parameters: reg.schema,
        })
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out
}
```

`lookupSession` 通过 `internal/agent/session` 全局 session registry（v0：`sync.Map`）查；session 创建时 `Register(sk, sess)`，销毁时 `Unregister(sk)`。

### 4.5 FR-7 Workspace Coordinator 关键代码

```go
// internal/agent/workspace/coordinator.go
type Workspace struct {
    sandbox   *tool.FsSandbox
    state     *atomicState
    lock      KeyedLocker
    changelog *Changelog
    verify    []VerifyHook         // session-level verify hook 集合
}

func New(sb *tool.FsSandbox, cl *Changelog, verify []VerifyHook) *Workspace {
    return &Workspace{
        sandbox: sb, changelog: cl, verify: verify,
        lock: NewKeyedLocker(),
        state: &atomicState{s: StateIdle},
    }
}

func (w *Workspace) ApplyPlan(ctx context.Context, p Plan) (Result, error) {
    // 1) 抢状态机
    if !w.state.transition(StateIdle, StatePlanning) {
        return Result{IsError: true, Content: "workspace busy"}, ErrInvalidStateTransition
    }
    // 2) planning 校验
    if err := w.validatePlan(ctx, p); err != nil {
        w.state.set(StateIdle)
        return Result{IsError: true, Content: err.Error()}, err
    }
    if !w.state.transition(StatePlanning, StateMutating) {
        return Result{IsError: true}, ErrInvalidStateTransition
    }
    // 3) 备份 + 顺序执行
    backups := make([]ChangeOp, 0, len(p.Ops))
    for _, op := range p.Ops {
        w.lock.Lock(op.Path)
        before, backup, err := w.executeOp(ctx, op)
        w.lock.Unlock(op.Path)
        if err != nil {
            w.rollback(backups)
            w.state.set(StateRolledBack)
            return Result{IsError: true, Content: err.Error()}, err
        }
        backups = append(backups, backup)
        _ = before  // 留给 hook 用
    }
    // 4) verifying
    if !w.state.transition(StateMutating, StateVerifying) { return Result{}, ErrInvalidStateTransition }
    for _, hook := range append(w.verify, p.Verify...) {
        if err := hook(ctx, w); err != nil {
            w.rollback(backups)
            w.state.set(StateRolledBack)
            return Result{IsError: true, Content: "verify failed: " + err.Error()}, err
        }
    }
    // 5) finalize + changelog
    entry := ChangeEntry{
        CommitID: nanoid(), SessionID: w.sessionKey, PlanID: p.ID,
        Ops: backups, Verified: true, Timestamp: time.Now(),
    }
    w.changelog.Append(entry)
    w.state.set(StateFinalized)
    // 异步推 bridge（不阻塞）
    go w.bridge.PublishChangelog(context.Background(), entry)
    return Result{Content: fmt.Sprintf("plan %s applied: %d ops", p.ID, len(p.Ops))}, nil
}

func (w *Workspace) rollback(backups []ChangeOp) {
    // 倒序按 backups 反向执行；用 inverse op（write→原内容，delete→空）
    for i := len(backups) - 1; i >= 0; i-- {
        op := backups[i]
        w.lock.Lock(op.Path)
        switch op.Kind {
        case "write":
            os.WriteFile(filepath.Join(w.sandbox.Root(), op.Path), op.Before, 0o644)
        case "edit":
            os.WriteFile(filepath.Join(w.sandbox.Root(), op.Path), op.Before, 0o644)
        case "delete":
            os.WriteFile(filepath.Join(w.sandbox.Root(), op.Path), op.Before, 0o644)
        }
        w.lock.Unlock(op.Path)
    }
}
```

### 4.6 FR-8 Bridge 协议

WebSocket JSON-RPC 新增 2 个 method（`internal/gateway/handlers.go` 加）：

```go
// request
{"jsonrpc":"2.0","id":N,"method":"workspace.publish","params":{
    "commitId": "abc...", "sessionId": "default", "planId": "p1",
    "ops": [{"path":"...","kind":"write","before":"<base64>","after":"<base64>","hashBefore":"...","hashAfter":"...","timestamp":...}],
    "verified": true
}}

// response
{"jsonrpc":"2.0","id":N,"result":{"ok":true}}
```

`workspace.fetch` 类似；返回 `{entries: [...]}[]`。

`PublishChangelog` 内部序列化 ChangeOp 时 `Before` / `After` 走 base64（防 JSON 注入换行 / NUL）。

### 4.7 Built-in tool 迁移到 Workspace

**最小侵入路径**：

- `readFileTool` / `listDirTool` 保持直接走 `sandbox`（read-only，无 workspace 状态变化）。
- `writeFileTool` / `editFileTool` 改造为「对单 op Plan」：调 `ws.ApplyPlan(ctx, Plan{ID: nanoid(), Ops: [op]})`，Verify hook 列表为空。
- `shellTool` 改造：若 args 涉及文件写（`mkdir` / `cp` / `mv` / `rm` 等白名单子集），把命令解析为「1 个 shell op + pre/post fs backup」，调 ApplyPlan。否则（纯只读 `ls` / `cat` / `grep`）不走 workspace，直接 exec。

v0 简化：shell 工具若 args 命中 `defaultShellAllowlist` 中的「写操作」子集（`mkdir` / `cp` / `mv` / `rm`），走 workspace；否则直接 exec（与原行为一致）。**完全解析 shell 命令的语义不现实**，v0 仅做 best-effort。SPEC §5 边界表写清楚「shell 工具只保证命中的子集走 workspace；未命中的命令仍直接 exec，但写操作最终也会被 workspace reconciler 在下次 reconcile 时捕获并报 dirty」。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| Plugin .so 加载失败（编译版本不匹配） | log warn，不阻断；该 plugin tool 在该 session 不存在 |
| Plugin `Register` 重复注册同名 tool | plugin.RegisterTool 内部返 `ErrPluginToolConflict`；plugin 自身决定降级或 panic |
| Plugin tool `Factory(sk)` 返错 | `Get` 返 nil；VisibleTools 也不列 |
| Plan 内同一 path 重复出现 | ValidatePlan 返 `ErrPlanOpConflict` |
| Plan 校验通过后某 Op 执行失败（写盘 ENOSPC） | 状态机进入 rolledback；已成功的 op 走 inverse 恢复 |
| Verify hook panic | state machine 走 rollback；plugin / verify hook 作者责任，**v0 不捕获 panic**（避免 swallowed panic 难诊断） |
| Verify hook 超时（默认 30s） | ctx cancel → verify 返 `context.DeadlineExceeded` → rollback |
| Reconcile 时文件被外部改（git pull） | 走 fsnotify event 触发 reconcile；diff 生成；state → dirty |
| Reconcile 时 hash 计算超大文件（> 16 MiB） | 跳过 + log warn（不卡启动）；仅 mtime / size 比对 |
| Bridge PublishChangelog 失败 | finalize 不阻断；retry 3 次仍失败 log warn；下次 reconcile 时会重新尝试（pending 队列） |
| Bridge FetchPending 在启动时 main 端没启动 | 5s 超时后 log warn，不阻断 Go agent 启动；本地 changelog 仍 in-memory |
| 同 session 内并发 ApplyPlan | 第二个进 `StateIdle → StatePlanning` 转移失败，返 `ErrWorkspaceBusy` |
| 跨 session ApplyPlan | 不同 workspace 实例，互不阻塞 |
| Session 销毁时 workspace 是否关闭 | 加 `Workspace.Close()`：释放 lock、changelog 持久化到 bridge（best-effort） |
| Windows 上 plugin .so 加载 | Go `plugin.Open` 不支持 Windows；log warn + 跳过全部 plugin；不影响 built-in |
| Plugin host API 调用 panic | host API 在 `plugin` package 内 panic-recover；panic 转 error 上抛（v0 用 `defer recover()`） |
| changelog 内存无限增长 | ring buffer cap 100（config 可改）；超过即截断最旧，log warn |
| `git pull` 后 workspace dirty | fsnotify 触发 reconcile；LLM 收到 `WorkspaceReconcileEvent`；UI 弹「外部改动」提示 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `internal/agent/tool/registry.go` | FR-6 改：新签名 `Register(name, factory, vis)` / `Get(name, sk)`；`VisibleTools(sk)`；保留 `MustRegisterTool` 适配器 |
| `internal/agent/tool/tool.go` | FR-6 改：加 `SessionAware` 接口；Tool 接口签名不变 |
| `internal/agent/tool/builtins.go` | FR-6 改：用 `MustRegisterTool` 适配器，签名兼容 |
| `internal/agent/tool/plugin/{loader,driver,host,manifest}.go` | FR-5 新增 |
| `plugins/example/plugin.go` | FR-5 新增 in-tree 示例 |
| `internal/agent/workspace/{state,coordinator,changelog,reconciler,bridge,lock}.go` | FR-7 + FR-8 新增 |
| `internal/agent/workspace/*_test.go` | 新增：状态机 + plan 应用 + reconcile + bridge mock 测试 |
| `internal/agent/session/session.go` | FR-6 改：加 `SessionContext` 接口方法；session registry 全局表 |
| `internal/agent/agent.go` | FR-6 改：tool 调用传 sessionKey |
| `internal/agent/executor/executor.go` | FR-6 改：调 `Registry.Get(name, sk)` |
| `internal/agent/agent.go` | FR-7 改：tool 入口分发（write / edit / shell）改走 Workspace.ApplyPlan |
| `internal/gateway/handlers.go` | FR-8 改：加 `workspace.publish` / `workspace.fetch` method |
| `cmd/app/main.go` | FR-5 改：构造 plugin.Loader + 启动时 LoadAll；FR-8 改：构造 WSBridge 并注入 workspace |
| `src/main/store/SessionStore.ts` | FR-8 改：加 `workspace_changelog` 表 + `saveWorkspaceChangelog` / `fetchPendingWorkspaceChangelog` |
| `src/main/runtime/client.ts` | FR-8 改：加 `publishWorkspaceChangelog` / `fetchWorkspaceChangelog` RPC |
| `src/main/index.ts` | FR-8 改：IPC handler + 启动时把 pending changelog 喂给 Go agent |
| `src/preload/index.ts` | FR-8 改：暴露 IPC（renderer 暂不直接用；留 API 给 future 调试面板） |
| `src/shared/darvin-api.ts` | FR-8 改：`WorkspaceChangelog` / `WorkspaceChangeOp` 类型 + `publishWorkspaceChangelog` / `fetchWorkspaceChangelog` 类型 |
| `package.json` | FR-8 改：dep `fsnotify` 已被 Electron 自带？不带入；改 dep `keyed-locker`？v0 自实现，不引入 |
| `src/darvin-agent/go.mod` | FR-8 改：dep `github.com/vmihailenco/keyed-locker`？v0 自实现（≈ 50 行）不引入 |

## 7. 验收标准

### 7.1 FR-5 Plugin

- [ ] `go build ./plugins/example/` 产出 `darvin-plugin-example.so`。
- [ ] `internal/agent/tool/plugin/loader_test.go::TestLoaderLoadAll` 找到 `plugins/example/`，调 `Register`，把示例 tool `example/echo` 挂进 registry。
- [ ] `TestPluginToolNamespace`：`Get("example/echo", sk)` 返 tool；`Get("echo", sk)` 返 nil。
- [ ] `TestPluginRegisterConflict`：plugin 试图注册 `read_file`（built-in 名），返 `ErrPluginToolConflict`，不污染主 registry。
- [ ] `TestPluginLoadFailure`：故意放一个损坏的 `.so`，启动 log warn，darvin-agent 不退出。
- [ ] `TestPluginHostAPIRestriction`：plugin 试图调 `os.Exit` / `fmt.Println`（绕过 host.Log）会被 go vet / 编译期挡住（plugin 代码在独立 package，导入路径检查）。
- [ ] Windows 上 plugin loader 走 noop（log warn），不影响 built-in 跑通。

### 7.2 FR-6 Registry

- [ ] `TestRegistryFactory`：factory 调 1 次，第二次 `Get` 返缓存实例。
- [ ] `TestRegistryVisibilityDisabled`：`Get` 返 nil；`VisibleTools` 不列。
- [ ] `TestRegistryVisibilitySessionOnly`：factory 在某些 sk 上返 err，VisibleTools 不列那些 sk。
- [ ] `TestSessionAware`：`SetSession` 注入 SessionContext 后，Execute 能拿到 workdir。
- [ ] **回归**：旧 `TestRegistryRegisterAndGet` / `TestRegistryDuplicate` / `TestRegistrySpecsSorted` / `TestRegistryNilTool` 改为 `MustRegisterTool` 适配器调用，全绿。
- [ ] **回归**：`internal/agent/executor/executor_test.go` 与 `internal/agent/agent_test.go` 全部传 `sessionKey` 参数后仍绿。

### 7.3 FR-7 Workspace

- [ ] `TestStateTransitions`：表驱动覆盖所有合法转移 + 至少 5 个非法转移返 `ErrInvalidStateTransition`。
- [ ] `TestWorkspaceApplyPlanHappy`：5-op plan（write / edit / delete 混合）跑通，state 最终 `finalized`，changelog 5 条。
- [ ] `TestWorkspaceApplyPlanRollback`：plan 第 3 op 故意 fail（文件不存在），state 进入 `rolledback`，前 2 op 改回原状。
- [ ] `TestWorkspaceConcurrentApply`：两个 goroutine 同 session 并发 ApplyPlan，第二个进 → `ErrWorkspaceBusy`。
- [ ] `TestWorkspaceCrossSession`：不同 session 各跑一个 ApplyPlan，互不阻塞。
- [ ] `TestWorkspaceVerifyFail`：plan 加一个 verify hook 故意 fail，回滚成功。
- [ ] `TestWorkspaceKeyedLock`：两个 goroutine 并发改同一文件不同行，串行化执行，**无 read-modify-write 丢失**（diff 终态文件同时含两处修改）。
- [ ] **回归**：`internal/agent/tool/fs_test.go` / `shell_test.go` 旧 case（走 workspace 入口）全绿；FR-1 sandbox 行为不退步。

### 7.4 FR-8 Reconcile + Bridge

- [ ] `TestReconcileNoDiff`：changelog 与 fs 一致 → 返空 diff，state 保持 finalized。
- [ ] `TestReconcileHashMismatch`：外部改了某个文件 mtime + content → 生成 diff，state → dirty。
- [ ] `TestReconcileMissingFile`：外部 `rm` 了一个 changelog 记录的路径 → diff 含 `mismatch: missing`。
- [ ] `TestReconcileExtraFile`：workspace 里多了一个 changelog 没记录的文件 → diff 含 `mismatch: extra`。
- [ ] `TestReconcileDirtyAccept`：dirty 状态调 accept_changes → state → idle。
- [ ] `TestBridgePublishChangelog`：mock gateway client；ApplyPlan finalize 后 ≤ 1s 收到 `workspace.publish` 调；main mock 写表成功。
- [ ] `TestBridgeFetchPending`：启动时 mock gateway client 返 3 条 pending；agent 把它们 merge 进 changelog。
- [ ] `TestBridgePublishRetry`：mock 第一次失败、第二次成功；最终表里有 1 条；不退 finalize。
- [ ] **SQLite schema 校验**：`sqlite3 darvin-cowork.sqlite ".schema workspace_changelog"` 4 列 + 1 索引；`saveWorkspaceChangelog` / `fetchPendingWorkspaceChangelog` 在 `SessionStore.ts` 单测覆盖。
- [ ] **e2e smoke**：`scripts/smoke.sh` 加一段：调 ApplyPlan（含 write + edit + delete）→ 等 finalize → 调 reconcile → 期望 0 diff；调 `touch workspace/foo.txt`（伪造外部改动）→ reconcile → 期望 1 diff（dirty）。

### 7.5 整体回归

- [ ] `go test -race -count=1 ./...` 全绿（含 FR-5/6/7/8 新加测试 + 旧 case）。
- [ ] `go vet ./...` / `gofmt -l .` 干净。
- [ ] `npm run lint` 通过（TS 端 main / preload / shared 改动）。
- [ ] `npm start` 启动后 smoke：`darvin-plugin-example.so` 加载成功 log 一行；session 启动后 `VisibleTools` 含 `example/echo`；调 `echo` 走 plugin tool 返预期结果。
- [ ] `npm run e2e --project=core` 通过（session-persistence / graceful-shutdown / sessions），FR-8 不破坏 S6/S7 e2e。

### 7.6 范围外（不做）

- 不引入 Go `plugin` 之外的加载机制（wasmtime / yaegi）；Windows plugin 留 Tier 3。
- 不实现 plugin marketplace / 远端下载；本地文件 + go build 为主。
- 不实现具体 `git_tool` / `docker_tool` plugin；FR-5 仅搭骨架。
- 不实现 CRDT / OT 协同编辑；冲突用 last-write-wins。
- 不动 renderer 现有 UI；FR-8 暂不暴露给 UI（仅 main 内部用），UI 上「workspace dirty 提示」留 Tier 3。
- 不动 `docs/系统架构.md` 的 mermaid 架构图（已知的「Gateway 层位置」自相矛盾问题已留作 S8 follow-up，不在 FR-5/6/7/8 范围）。