# darvin-cowork Memory Subsystem — Bootstrap Migration & Per-Agent Workspaces

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：沿用既有内容；文件名为本次从 `2026-MM-DD-*` 规范化为 `2026-08-04-*` 的新文件名，正文未重写。
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## Scope

Covers CHECKLIST sections C5-C6 (AGENTS.md / TOOLS.md / BOOTSTRAP.md migration), I9 (per-agent bootstrap IPC), J1-J5 (startup migrations), K1-K3 (workspace / cwd separation).

Reference: `LobsterAI/src/main/libs/openclawWorkspaceMigration.ts`, `openclawMemoryIndexMigration.ts`, `openclawMemoryFile.ts:643-689` (syncMemoryFileOnWorkspaceChange).

## 1. 概述

darvin-cowork 启动期需要一次性的迁移逻辑：

1. **旧 SQLite user_memories → 新 MEMORY.md**：LobsterAI 早期版本用 SQLite 存 memory，迁移到 MEMORY.md 后用文件做 source of truth。darvin-cowork 直接起 MEMORY.md，但万一有历史 SQLite 数据要导入（v1 不存在，所以本 spec 是 **stub**：保留接口 + 测试，不写真实 SQLite→MD 转换）
2. **workspace 路径迁移**：从 `DARVIN_AGENT_WORKSPACE/MEMORY.md`（旧默认）迁移到 `state/workspace-main/MEMORY.md`（新默认）
3. **AGENTS.md 用户区迁移**：旧路径下的 AGENTS.md 用户内容（managed marker 之前的部分）合入新路径
4. **TOOLS.md / BOOTSTRAP.md 复制**：bootstrap 文件按"空目标才复制，冲突则备份"策略
5. **FTS index 升级**：FTS5 tokenizer 从 unicode61 → trigram 需要全量 rebuild

外加 per-agent workspace：

- darvin-cowork v1 只支持 main agent，但要把 IPC 形状先扩展到 `{agentId?}` 参数，方便 v2 加多 agent 不破坏 wire 协议

## 2. 路径语义

darvin-cowork 沿用 LobsterAI 三路径模型：

| 概念 | 路径 | 用途 |
|---|---|---|
| **LobsterAI task cwd**（即 workspaceRoot） | `os.Getenv("DARVIN_AGENT_WORKSPACE")` 或 `cfg.Agent.Workdir` | 工具 read/write/edit/exec 的 cwd；用户项目目录 |
| **OpenClaw main agent workspace**（即 MEMORY.md 目录） | `{workspaceRoot}/state/workspace-main/` | IDENTITY.md / USER.md / SOUL.md / AGENTS.md / MEMORY.md / memories.sqlite |
| **per-agent workspace**（v2 预留） | `{stateDir}/workspace-{agentId}/` | 非主 agent 用自己的 IDENTITY/SOUL/USER |

`{stateDir}` = `os.Getenv("DARVIN_AGENT_STATE")` 或 `{userData}/openclaw/state/`（main 端）。

本 spec v1 只用前两个概念；per-agent workspace 把 hook 留好。

## 3. MEMORY.md 路径解析

```go
// internal/memory/paths.go
const (
    // StateDir is appended to the task cwd so MEMORY.md lives next to
    // IDENTITY.md / USER.md / SOUL.md, not scattered across user project dirs.
    StateDir = "state/workspace-main"
)

// ResolveMemoryFilePath returns the absolute MEMORY.md path under task cwd.
func ResolveMemoryFilePath(workspaceRoot string) string {
    return filepath.Join(workspaceRoot, StateDir, MemoryFilename)
}

// ResolveBootstrapFilePath returns the absolute bootstrap file path; the
// caller MUST pass a whitelisted filename (ValidateBootstrapFilename).
func ResolveBootstrapFilePath(workspaceRoot, name string) (string, error) {
    if err := ValidateBootstrapFilename(name); err != nil { return "", err }
    return filepath.Join(workspaceRoot, StateDir, name), nil
}
```

**为什么不放 `{userData}/openclaw/state/workspace-main/`**：darvin-cowork 当前 task cwd 已经在 `userData/workspaces/<sessionId>/`（main 端管的），把 memory 放在更深的子目录，方便单 session 数据打包（含 memory）给用户导出/调试。

## 4. SQLite → MEMORY.md 一次性迁移

### 4.1 触发条件

启动期检测：
- `memories.sqlite` 已存在 + `user_memories` 表行数 > 0
- `state/workspace-main/MEMORY.md` 不存在或为空

满足 → 触发迁移。

### 4.2 实现

```go
// internal/memory/migrate.go
type MigrationDataSource interface {
    IsMigrationDone() bool
    MarkMigrationDone()
    GetActiveMemoryTexts() []string
}

func MigrateSqliteToMemoryMd(filePath string, source MigrationDataSource) int {
    if source.IsMigrationDone() { return 0 }
    texts := source.GetActiveMemoryTexts()
    if len(texts) == 0 {
        source.MarkMigrationDone()
        return 0
    }
    added := appendMemoryTexts(filePath, texts)
    source.MarkMigrationDone()
    return added
}

// appendMemoryTexts 与 LobsterAI `openclawMemoryFile.ts:494-518` 一致：
// 先 parse 现有 file 拿 fingerprint 集合，对新 texts 去重后 append。
func appendMemoryTexts(filePath string, texts []string) int {
    original := readFileOrEmpty(filePath)
    parsed := ParseBytes([]byte(original))
    existing := collectExistingFingerprints(parsed)

    var blocks []string
    for _, raw := range texts {
        lines := serializeEntryLines(raw)
        if lines == nil { continue }
        display := blockDisplayText(lines)
        id := Fingerprint(display)
        if existing[id] { continue }
        existing[id] = true
        blocks = append(blocks, strings.Join(lines, "\n"))
    }
    if len(blocks) == 0 { return 0 }
    WriteAtomic(filePath, appendBlocksToContent(original, blocks))
    return len(blocks)
}

func collectExistingFingerprints(parsed []Block) map[string]bool {
    out := make(map[string]bool)
    for _, b := range parsed {
        if b.Kind != BlockEntry || b.Entry == nil { continue }
        out[b.Entry.ID] = true
        for _, line := range b.Lines {
            m := bulletLineRe.FindStringSubmatch(strings.TrimSpace(line))
            text := line
            if m != nil { text = m[1] }
            text = strings.TrimSpace(text)
            if text != "" { out[Fingerprint(text)] = true }
        }
    }
    return out
}
```

### 4.3 启动期 wiring

```go
// cmd/app/main.go
func getMemMgr() *memory.Manager {
    if memMgr != nil { return memMgr }
    stateDir := filepath.Join(effectiveWorkdir, memory.StateDir)
    mgr, err := memory.New(stateDir, database.Get(), cfg.Memory, log.Logger)
    if err != nil { return nil }
    if err := mgr.Migrate(rootCtx); err != nil { return nil }

    // 一次性 SQLite → MEMORY.md
    if !migratedFlag() {
        memFilePath := memory.ResolveMemoryFilePath(effectiveWorkdir)
        n := memory.MigrateSqliteToMemoryMd(memFilePath, &sqliteMemorySource{})
        if n > 0 { log.Info("memory migration: SQLite → MEMORY.md", zap.Int("entries", n)) }
    }
    return mgr
}
```

`v1 scope`：实际没有旧 SQLite 数据，所以这个 wiring 走空 path。`MigrateSqliteToMemoryMd` + `appendMemoryTexts` + `collectExistingFingerprints` 实现 + 测试都到位；migration flag（kv 存在 SQLite）也建好。

### 4.4 migration flag

```go
// internal/memory/paths.go
const (
    MigrationFlagKey = "memory_migration.sqlite_to_md.v1"
)

// MigrationFlagStore 抽象（main 端用 SqliteStore 实现）
type MigrationFlagStore interface {
    Get(key string) string
    Set(key, value string)
}
```

## 5. workspace 路径迁移（v1 stub）

### 5.1 触发条件

启动期检测：
- `DARVIN_AGENT_WORKSPACE` env 不为空（说明用户用旧路径）
- 旧路径下存在 `MEMORY.md` 且非空
- 新路径下 `MEMORY.md` 不存在或为空

满足 → 触发迁移。

### 5.2 实现

```go
// internal/memory/workspace_migrate.go
func SyncMemoryFileOnWorkspaceChange(oldCwd, newCwd string) (Synced bool, errorMsg string) {
    oldPath := ResolveMemoryFilePath(oldCwd)
    newPath := ResolveMemoryFilePath(newCwd)
    if oldPath == newPath { return false, "" }
    oldContent := readFileOrEmpty(oldPath)
    if oldContent == "" { return false, "" }
    parsed := ParseBytes([]byte(oldContent))
    entries := Entries(parsed)
    if len(entries) == 0 { return false, "" }
    texts := make([]string, 0, len(entries))
    for _, e := range entries { texts = append(texts, e.Text) }
    added := appendMemoryTexts(newPath, texts)
    return added > 0, ""
}
```

### 5.3 bootstrap 文件迁移

```go
// internal/memory/bootstrap_migrate.go
func MigrateBootstrapFiles(oldCwd, newCwd string) MigrateResult {
    for _, name := range []string{"IDENTITY.md", "USER.md", "SOUL.md", "TOOLS.md", "BOOTSTRAP.md"} {
        src := filepath.Join(oldCwd, name)
        dst := filepath.Join(newCwd, name)
        copyIfNeeded(src, dst)
    }
    // AGENTS.md 用户内容（managed marker 之前）
    src := filepath.Join(oldCwd, "AGENTS.md")
    dst := filepath.Join(newCwd, "AGENTS.md")
    mergeAgentsMdUserContent(src, dst)
}
```

```go
func copyIfNeeded(src, dst string) {
    srcContent := readNonEmptyText(src)
    if srcContent == "" { return }  // 源空 → 不动
    if isNonEmptyFile(dst) { return }  // 目标非空 → 保留用户内容
    os.MkdirAll(filepath.Dir(dst), 0o755)
    os.WriteFile(dst, []byte(srcContent), 0o600)
}

func readNonEmptyText(p string) string {
    info, err := os.Stat(p)
    if err != nil || !info.Mode().IsRegular() { return "" }
    data, err := os.ReadFile(p)
    if err != nil { return "" }
    return strings.TrimSpace(string(data))
}

func isNonEmptyFile(p string) bool {
    info, err := os.Stat(p)
    if err != nil || !info.Mode().IsRegular() { return false }
    return info.Size() > 0
}
```

### 5.4 AGENTS.md 用户区合并

照搬 LobsterAI `openclawWorkspaceMigration.ts:141-188`：

```go
const agentsMarker = "<!-- darvin-cowork managed: do not edit below this line -->"

func mergeAgentsMdUserContent(src, dst string) {
    srcContent := readNonEmptyText(src)
    if srcContent == "" { return }
    srcUser := extractAgentsUserContent(srcContent)
    if srcUser == "" { return }

    dstContent := readNonEmptyText(dst)
    if dstContent == "" {
        // 新文件直接写 srcUser
        os.MkdirAll(filepath.Dir(dst), 0o755)
        os.WriteFile(dst, []byte(srcUser+"\n"), 0o600)
        return
    }
    if strings.Contains(dstContent, srcUser) { return }  // 已包含

    markerIdx := strings.Index(dstContent, agentsMarker)
    var merged string
    if markerIdx >= 0 {
        // 把 srcUser 插到 marker 之前
        dstUser := strings.TrimSpace(dstContent[:markerIdx])
        managed := strings.TrimSpace(dstContent[markerIdx:])
        if dstUser != "" {
            merged = dstUser + "\n\n" + srcUser + "\n\n" + managed + "\n"
        } else {
            merged = srcUser + "\n\n" + managed + "\n"
        }
    } else {
        // dst 没有 marker；append 到末尾
        merged = strings.TrimRight(dstContent, "\n") + "\n\n" + srcUser + "\n"
    }
    os.WriteFile(dst, []byte(merged), 0o600)
}

func extractAgentsUserContent(content string) string {
    idx := strings.Index(content, agentsMarker)
    if idx < 0 { return strings.TrimSpace(content) }
    return strings.TrimSpace(content[:idx])
}
```

### 5.5 启动期 wiring

```go
// cmd/app/main.go
func runStartupMigrations(mgr *memory.Manager) {
    oldCwd := os.Getenv("DARVIN_AGENT_WORKSPACE_OLD")
    newCwd := effectiveWorkdir  // 当前的
    if oldCwd == "" || oldCwd == newCwd { return }

    if synced, err := memory.SyncMemoryFileOnWorkspaceChange(oldCwd, newCwd); err != nil {
        log.Warn("memory workspace sync failed", zap.Error(err))
    } else if synced {
        log.Info("memory workspace synced", zap.String("from", oldCwd), zap.String("to", newCwd))
    }
    memory.MigrateBootstrapFiles(oldCwd, newCwd)
}
```

## 6. FTS5 索引迁移

### 6.1 检测条件

启动期：
- `memory_fts` virtual table 存在
- `memory_index_meta_v1` 表中 `schema` row 缺失 / `tokenizer != "trigram"` / `model != "fts-only"`

→ 触发全量 rebuild。

### 6.2 实现

```go
// internal/memory/index.go
type MemoryIndexMeta struct {
    Model       string `json:"model"`        // "fts-only"
    Provider    string `json:"provider"`     // "none"
    Tokenizer   string `json:"tokenizer"`    // "trigram"
    FTSOnly     bool   `json:"fts_only"`     // v1 恒为 true
    BuiltAt     int64  `json:"built_at"`
    EntryCount  int    `json:"entry_count"`
}

func (m *Manager) ensureMemoryIndex(ctx context.Context) (rebuilt bool, err error) {
    dbMeta := readDBMemoryMeta(m.DB)
    fileMeta := loadMemoryMeta(m.StateDir)

    needsRebuild := dbMeta == nil ||
        dbMeta.Tokenizer != "trigram" ||
        dbMeta.FTSOnly != true ||
        fileMeta == nil ||
        fileMeta.SchemaVersion != 1 ||
        fileMeta.Tokenizer != "trigram"

    if !needsRebuild {
        // 一致性检查
        var ftsCount, rowCount int64
        m.DB.Raw(`SELECT COUNT(*) FROM memory_fts`).Scan(&ftsCount)
        m.DB.Model(&UserMemory{}).Where("status = ?", "created").Count(&rowCount)
        if ftsCount != rowCount { needsRebuild = true }
    }

    if !needsRebuild { return false, nil }

    if err := m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        if err := tx.Exec(`DELETE FROM memory_fts`).Error; err != nil { return err }
        var rows []UserMemory
        if err := tx.Where("status = ?", "created").Find(&rows).Error; err != nil { return err }
        for _, r := range rows {
            if err := tx.Exec(`INSERT INTO memory_fts(memory_id, text) VALUES (?, ?)`,
                r.ID, r.Text).Error; err != nil { return err }
        }
        now := time.Now().UnixMilli()
        body, _ := json.Marshal(MemoryIndexMeta{
            Model: "fts-only", Provider: "none", Tokenizer: "trigram",
            FTSOnly: true, BuiltAt: now, EntryCount: len(rows),
        })
        return upsertDBMemoryMeta(tx, "schema", string(body))
    }); err != nil { return false, err }

    // 文件 meta 同步写
    writeMemoryMeta(m.StateDir, &memoryMeta{
        SchemaVersion: 1, Tokenizer: "trigram",
        BuiltAt: time.Now().UnixMilli(), EntryCount: -1,
    })

    return true, nil
}
```

### 6.3 启动期触发

```go
// cmd/app/main.go
go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    rebuilt, err := memMgr.ensureMemoryIndex(ctx)
    if err != nil { log.Warn("memory index ensure failed", zap.Error(err)) }
    if rebuilt { log.Info("memory index rebuilt (tokenizer upgrade)") }
}()
```

## 7. per-agent bootstrap workspace（v1 钩子）

### 7.1 现状

darvin-cowork v1 只支持 main agent。但 renderer 已经有 `agents` 概念（`CoworkUserMemory` schema 里也有 `agentId`），main 端 `agentManager`（plan 中）也要走 per-agent 路由。

本 spec 提前把 IPC 形状扩展到 `{agentId?}`：

```ts
// src/shared/darvin-api.ts
export interface DarvinBootstrapReadRequest {
  filename: DarvinBootstrapFilename;
  agentId?: string;  // 缺省 = main
}
export interface DarvinBootstrapWriteRequest {
  filename: DarvinBootstrapFilename;
  content: string;
  agentId?: string;
}
```

### 7.2 实现

```go
// internal/memory/paths.go
func ResolveAgentWorkspacePath(stateRoot, agentID string) string {
    if agentID == "" || agentID == "main" {
        return filepath.Join(stateRoot, "workspace-main")
    }
    return filepath.Join(stateRoot, fmt.Sprintf("workspace-%s", agentID))
}
```

```go
// internal/gateway/handlers_memory.go
type BootstrapReadParams struct {
    Filename string `json:"filename"`
    AgentID  string `json:"agentId,omitempty"`
}

func handleBootstrapRead(_ context.Context, id json.RawMessage, params json.RawMessage, h *Handler) *Response {
    if h.Memory == nil {
        return errorResp(id, CodeInvalidParams, "memory disabled", nil)
    }
    var p BootstrapReadParams
    if err := json.Unmarshal(params, &p); err != nil { ... }
    if err := memory.ValidateBootstrapFilename(p.Filename); err != nil { ... }
    stateDir := memory.ResolveAgentWorkspacePath(h.Memory.StateDir, p.AgentID)
    content, err := memory.ReadBootstrap(stateDir, p.Filename)
    ...
}
```

### 7.3 v1 scope

handler 接受 `agentId` 参数但**只对 `main`（缺省）有效**；非 main agent 返回 `CodeInvalidParams "agent not supported in v1"`。v2 解锁。

```go
if p.AgentID != "" && p.AgentID != "main" {
    return errorResp(id, CodeInvalidParams, "agent not supported in v1", nil)
}
```

## 8. workspace vs cwd 分离（system prompt 注入）

### 8.1 policy section 改写

`internal/memory/policy.go` 的 PolicySection 内容增加一段：

```
## Workspace Roles

Your task working directory (where tools read/write/exec) is the user's project directory.
Your MEMORY.md and bootstrap files (IDENTITY/USER/SOUL) live in a separate agent workspace
under `state/workspace-main/`. Don't write deliverables or scratch files into the agent
workspace — use the task cwd. Don't write long-term memory into the project directory —
use memory_write (which goes to MEMORY.md in the agent workspace).
```

确保模型不会把交付物（PPT / 图片）写到 `{userData}/darvin-agent/state/workspace-main/`。

### 8.2 main.go policy section 注入顺序

```go
// cmd/app/main.go
da.SetSections([]ctxengine.SystemSection{
    memory.PolicySection(),  // priority 50
    // 用户在 cfg.Agent.SystemPromptAddition 里加的自定义内容 → priority 1000
})
```

## 9. 涉及文件

| 文件 | 操作 |
|---|---|
| `src/darvin-agent/internal/memory/paths.go` | 新建（含 `ResolveMemoryFilePath` / `ResolveAgentWorkspacePath`） |
| `src/darvin-agent/internal/memory/paths_test.go` | 新建 |
| `src/darvin-agent/internal/memory/migrate.go` | 新建（SQLite → MEMORY.md） |
| `src/darvin-agent/internal/memory/migrate_test.go` | 新建 |
| `src/darvin-agent/internal/memory/workspace_migrate.go` | 新建（path sync） |
| `src/darvin-agent/internal/memory/workspace_migrate_test.go` | 新建 |
| `src/darvin-agent/internal/memory/bootstrap_migrate.go` | 新建（5 文件 + AGENTS.md 用户区） |
| `src/darvin-agent/internal/memory/bootstrap_migrate_test.go` | 新建 |
| `src/darvin-agent/internal/memory/index.go` | 新建（Reindex + memory_index_meta_v1） |
| `src/darvin-agent/internal/memory/index_test.go` | 新建 |
| `src/darvin-agent/internal/memory/policy.go` | 修改（policy 加 workspace roles 段） |
| `src/darvin-agent/internal/memory/manager.go` | 修改（ReadBootstrap / WriteBootstrap 接受 agentID） |
| `src/darvin-agent/internal/gateway/handlers_memory.go` | 修改（10 个 handler 加 agentID 参数） |
| `src/darvin-agent/cmd/app/main.go` | 修改（启动期 5 类 migration wiring） |
| `src/shared/darvin-api.ts` | 修改（DarvinBootstrapReadRequest / WriteRequest 加 agentId） |
| `src/main/index.ts` | 修改（10 个 IPC handler 透传 agentId） |
| `src/main/runtime/client.ts` | 修改 |
| `src/preload/index.ts` | 修改 |

## 10. 验收标准

### Go 单测

- `ResolveMemoryFilePath` 总是返回 `{workspaceRoot}/state/workspace-main/MEMORY.md`
- `ResolveAgentWorkspacePath("main")` 返回 `workspace-main`，`("foo")` 返回 `workspace-foo`
- `MigrateSqliteToMemoryMd` 幂等（第二次不重复）
- `SyncMemoryFileOnWorkspaceChange`：
  - 旧空 → 不复制
  - 旧有 5 条 + 新空 → 新增 5 条
  - 旧有 5 条 + 新已有 3 条 dedup → 新增 2 条
- `MigrateBootstrapFiles`：5 个文件 + AGENTS.md 用户区
- `AGENTS.md` 用户区合并：目标有 marker vs 没 marker；冲突备份
- `copyIfNeeded`：源空不复制；目标非空不覆盖；目标空复制
- `ensureMemoryIndex`：meta mismatch → rebuild；meta 匹配 + 一致 → skip
- `memory_fts` MATCH `'燕麦'` 返回 CJK 命中（trigram tokenize）

### 手工 smoke

1. 在 `~/.config/darvin-cowork/darvin-agent/old/MEMORY.md` 放 3 条 entry
2. 设置 `DARVIN_AGENT_WORKSPACE_OLD=/old/path`
3. 启动 → 日志显示 "memory workspace synced, added=3"
4. 新路径 `state/workspace-main/MEMORY.md` 含 3 条
5. 旧路径文件保留（不删）
6. 第二次启动 → 日志 "memory migration skipped"（幂等）
7. 在 `old/AGENTS.md` 写 `<!-- user content -->`
8. 启动 → 新路径 `state/workspace-main/AGENTS.md` 含该用户内容（managed marker 之前）

### FTS migration smoke

1. 故意把 `memory_fts` 表 drop
2. 启动 → `ensureMemoryIndex` 自动 rebuild
3. `SELECT * FROM memory_fts WHERE memory_fts MATCH '燕麦'` 仍命中

## 11. 边界 / 非目标

| 场景 | 处理 |
|---|---|
| 旧路径 = 新路径 | 不动 |
| 旧路径不存在 | 不报错，直接返 noop |
| 旧路径 AGENTS.md 同时有 managed marker 之后的内容 | 取 marker 之前部分；marker 之后由 `syncAgentsMd()` 重生 |
| TOOLS.md / BOOTSTRAP.md 同名冲突且内容不同 | 保留新目标，写 `<name>.migrated-<timestamp>` 备份 |
| migration 失败 | 不写 flag，下次启动继续重试 |
| per-agent workspace (v2) | handler 已支持 `agentId` 透传，但 v1 只接受 `main` |
| `DARVIN_AGENT_WORKSPACE_OLD` env 在 v2+ 移除 | v1 兼容期保留 |
| `memory_index_meta_v1` 缺 + `memory_fts` 空 | 不重建（0 行 = 0 行） |