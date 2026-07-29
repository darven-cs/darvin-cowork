# Agent Sessions Store 设计文档（S2）

> **Phase 2 / 6 — Go 阶段 spec #1**。把 sessions.db + 4 张 GORM 表 + SQLite SessionStore 落地。**不**接网络层（那是 S3）、**不**接 ACP/Agent（那是 S4）。
> 前置：S1 已锁 UI 契约；本 spec 自身可独立验收（用 Go 测试驱动，不依赖 Electron）。

---

## 1. 概述

### 1.1 问题 / 背景

按 `docs/系统架构.md` §"数据库归属"，agent 运行时数据应落 `sessions.db`（GORM / SQLite），含 4 张表：`Session` / `Message` / `CompactionCheckpoint` / `SkillSnapshot`。

源码现状：
- `internal/database/sqlite.go` 仅打开单一 `data.db`，无 model 注册
- `internal/agent/store/store.go` 仅定义 `SessionStore` interface
- `internal/agent/store/memory.go` 是唯一实现（注释明说 "SQLite-backed implementation is planned for a future spec"）
- `internal/config/config.go` `DatabaseConfig` 只有 `DSN` 一个字段

S3 会在 Gateway 层做 SessionManager（in-memory session_id → 实例映射），但持久化靠本 spec 的 `SessionStore` SQLite 实现。S4 ACP Loop 会调 `store.Load` / `store.Save`。

### 1.2 目标

- 4 个 GORM 模型按 `docs/系统架构.md` §"SessionStore 表设计" 字段定义
- `internal/store/sqlite_store.go` 实现 `SessionStore` interface，**替换** MemoryStore 作为默认
- `config.yaml` 加 `database.sessions_dsn`（默认 `./sessions.db`）；保留 `database.dsn` 向后兼容（标 deprecated）
- `database.Init` 后立即 AutoMigrate 4 张表
- `cmd/app/main.go` 接入新 SessionStore（Agent 构造时传入）

### 1.3 非目标

- **不**实现 sessions.db 的事件表（架构文档未列；EventLedger 见 S3，本 spec 不动）
- **不**实现 lobsterai.db（Electron 主进程侧 DB，留到后续 spec；本 spec 只动 Go 侧）
- **不**实现 EventLedger / SessionManager（S3）
- **不**实现 SkillSnapshot 表的数据填充（Skills 系统未实装；表结构先建好）
- **不**实现 Schema migration / 降级（v0 阶段，AutoMigrate 即可）
- **不**实现 SessionStore.List 之外的查询（不预写复杂 SQL）
- **不**改 `data.db` 路径语义（保留兼容）

---

## 2. 用户场景

### 场景 1：启动后 sessions.db 被创建

**Given** Go agent 首次启动，`config.yaml` 含 `database.sessions_dsn: ./sessions.db`
**When** `cmd/app/main.go` 跑 `database.Init(cfg)` + `database.AutoMigrate(&Session{}, &Message{}, &CompactionCheckpoint{}, &SkillSnapshot{})`
**Then** 仓库根目录 `./sessions.db` 文件被创建，含 4 张表

### 场景 2：SessionStore CRUD

**Given** 已启动且 AutoMigrate 已跑
**When** 测试代码 `Save` 一个 `*session.Session{ID: "s1", Key: "k1", AgentID: "a1"}` → `Load("s1")` → `List()` → `Delete("s1")`
**Then** 全部通过；`Load` 返回深拷贝；`List` 按 `UpdatedAt desc` 排序；`Delete` 幂等

### 场景 3：SessionStore 替换 MemoryStore

**Given** `internal/agent.New(NewAgentConfig{Store: nil})`
**When** `New` 内部看到 Store 为 nil
**Then** 默认构造 `*store.SQLiteStore`（不再是 `*store.MemoryStore`），从 `config.Get().Database.SessionsDSN` 取 DSN

### 场景 4：Message 表存储 LLM 消息

**Given** Agent 跑一轮 turn
**When** Executor 调用 `store.SaveMessages(sessionID, []*llm.Message{...})`（**注意：本 spec 不实现 SaveMessages，列出仅为说明 Message 表将来用途**）
**Then** （S4 实现）`messages` 表新增对应行，`session_id` 外键关联 `sessions.id`

---

## 3. 功能需求

### FR-1：4 个 GORM 模型

`internal/store/models.go`：

```go
package store

import "time"

// Session 会话记录。
type Session struct {
    ID        string    `gorm:"primaryKey"`           // 唯一标识
    Key       string    `gorm:"index"`                // 会话密钥
    AgentID   string    `gorm:"index"`                // 关联 Agent ID
    Status    string    `gorm:"default:active"`       // active / archived / suspended
    CreatedAt time.Time `gorm:"autoCreateTime"`
    UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (Session) TableName() string { return "sessions" }

// Message 消息记录。
type Message struct {
    ID         string    `gorm:"primaryKey"`
    SessionID  string    `gorm:"index"`               // 关联 Session.ID（外键；不强制以免删 session 麻烦）
    Role       string    `gorm:"index"`                // user / assistant / tool
    Content    string    `gorm:"type:text"`           // 消息内容
    ToolCalls  string    `gorm:"type:text"`            // tool_calls JSON
    Timestamp  int64     `gorm:"index"`               // Unix ms
    StopReason string    `gorm:"default:stop"`        // stop / aborted / max_turns
    ParentID   string    `gorm:"index"`               // DAG 父消息 ID
}

func (Message) TableName() string { return "messages" }

// CompactionCheckpoint 压缩检查点。
type CompactionCheckpoint struct {
    ID            string    `gorm:"primaryKey"`
    SessionID     string    `gorm:"index"`
    Summary       string    `gorm:"type:text"`
    TokensBefore  int       `gorm:"not null"`
    TokensAfter   int       `gorm:"not null"`
    FirstKeptID   string    `gorm:"not null"`
    CreatedAt     time.Time `gorm:"autoCreateTime"`
}

func (CompactionCheckpoint) TableName() string { return "compaction_checkpoints" }

// SkillSnapshot Skill 快照。
type SkillSnapshot struct {
    ID        string    `gorm:"primaryKey"`
    SessionID string    `gorm:"index"`
    SkillName string    `gorm:"not null"`
    LoadedAt  time.Time `gorm:"autoCreateTime"`
    Source    string    `gorm:"not null"`           // workspace / bundled / session / plugin
}

func (SkillSnapshot) TableName() string { return "skill_snapshots" }
```

**字段与架构文档 §"SessionStore 表设计" 对照**：
- ✅ Session：ID/Key/AgentID/Status/CreatedAt/UpdatedAt 全匹配
- ✅ Message：ID/SessionID/Role/Content/ToolCalls/Timestamp/StopReason/ParentID 全匹配（Content + ToolCalls 用 `type:text` 因为可能含大 JSON）
- ✅ CompactionCheckpoint：ID/SessionID/Summary/TokensBefore/TokensAfter/FirstKeptID/CreatedAt 全匹配
- ✅ SkillSnapshot：ID/SessionID/SkillName/LoadedAt/Source 全匹配

### FR-2：config 加 sessions_dsn

`internal/config/config.go`：

```go
type DatabaseConfig struct {
    DSN          string `mapstructure:"dsn"`           // 旧：单一 DB（向后兼容，标 deprecated）
    SessionsDSN  string `mapstructure:"sessions_dsn"`   // 🆕 新：sessions.db
}
```

`config.yaml`：

```yaml
database:
  dsn: ./data.db                       # 旧：保留兼容
  sessions_dsn: ./sessions.db          # 🆕 新
```

### FR-3：database.Init 支持按 DSN 打开

`internal/database/sqlite.go` 加 helper（不动现有 `Init(cfg)` 行为，避免破坏 `cmd/app/main.go` 现有调用）：

```go
// OpenByDSN opens a separate *gorm.DB (does NOT replace globalDB).
// Used by store.SQLiteStore which wants its own connection pool.
func OpenByDSN(dsn string) (*gorm.DB, error) {
    return gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Warn),
    })
}

// Init 不变，仍打开 globalDB（单一连接池），用于 AutoMigrate
// 但 dsn 改为取 cfg.SessionsDSN（若为空回退 cfg.DSN）
func Init(cfg *Config) error {
    dsn := cfg.SessionsDSN
    if dsn == "" { dsn = cfg.DSN }
    db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil { return err }
    globalDB = db
    return nil
}
```

**关键点**：sessions.db 是默认 DSN（向后兼容：旧 config 无 sessions_dsn 时仍走 data.db 路径）。

### FR-4：AutoMigrate 调用点

`cmd/app/main.go` 在 `database.Init` 成功后立即：

```go
if err := database.AutoMigrate(
    &store.Session{},
    &store.Message{},
    &store.CompactionCheckpoint{},
    &store.SkillSnapshot{},
); err != nil {
    log.Error("auto migrate failed", zap.Error(err))
    os.Exit(1)
}
```

### FR-5：store.SessionStore SQLite 实现

`internal/store/sqlite_store.go`：

```go
package store

import (
    "context"
    "errors"

    "gorm.io/gorm"

    "darvin-cowork/backend/internal/agent/session"
)

// SQLiteStore is the GORM-backed SessionStore.
type SQLiteStore struct {
    db *gorm.DB
}

func NewSQLiteStore(db *gorm.DB) *SQLiteStore {
    return &SQLiteStore{db: db}
}

// Save persists s. If a session with s.ID exists it is overwritten.
func (s *SQLiteStore) Save(ctx context.Context, sess *session.Session) error {
    if sess == nil { return errors.New("store: nil session") }
    row := Session{
        ID:        sess.ID,
        Key:       sess.Key,
        AgentID:   sess.AgentID,
        Status:    string(sess.Status),
        CreatedAt: sess.CreatedAt,
        UpdatedAt: sess.UpdatedAt,
    }
    return s.db.WithContext(ctx).Save(&row).Error
}

// Load returns deep-copied *session.Session or ErrNotFound.
func (s *SQLiteStore) Load(ctx context.Context, id string) (*session.Session, error) {
    var row Session
    if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, ErrNotFound
        }
        return nil, err
    }
    return row.toSession(), nil
}

// List returns all sessions sorted by UpdatedAt desc.
func (s *SQLiteStore) List(ctx context.Context) ([]session.SessionMeta, error) {
    var rows []Session
    if err := s.db.WithContext(ctx).Order("updated_at desc").Find(&rows).Error; err != nil {
        return nil, err
    }
    metas := make([]session.SessionMeta, 0, len(rows))
    for _, r := range rows {
        metas = append(metas, session.SessionMeta{
            ID:        r.ID,
            Key:       r.Key,
            AgentID:     r.AgentID,
            Status:    session.Status(r.Status),
            CreatedAt: r.CreatedAt,
            UpdatedAt: r.UpdatedAt,
        })
    }
    return metas, nil
}

// Delete removes by id. Idempotent.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
    return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Session{}).Error
}

func (r *Session) toSession() *session.Session {
    return &session.Session{
        ID:        r.ID,
        Key:       r.Key,
        AgentID:   r.AgentID,
        Status:    session.Status(r.Status),
        CreatedAt: r.CreatedAt,
        UpdatedAt: r.UpdatedAt,
    }
}
```

### FR-6：替换默认 SessionStore

`internal/agent/agent.go` 中 `New(NewAgentConfig{...})`：

```go
// 当 Store 为 nil 时，默认构造 SQLiteStore
if cfg.Store == nil {
    db, err := database.OpenByDSN(database.GetDSN())  // 🆕 helper
    if err != nil { return nil, fmt.Errorf("agent: open store: %w", err) }
    cfg.Store = store.NewSQLiteStore(db)
}
```

`internal/database/sqlite.go` 加 `func GetDSN() string { return globalDSN }` helper（在 Init 中保存 cfg.SessionsDSN）。

### FR-7：session.Session 加 AgentID + Status 字段

`internal/agent/session/session.go` 当前可能没这两个字段。本 spec 落地需补：

```go
type Session struct {
    ID        string
    Key       string
    AgentID   string
    Status    Status  // "active" / "archived" / "suspended"
    CreatedAt time.Time
    UpdatedAt time.Time
    mu        sync.RWMutex
    messages  []llm.Message
    meta      map[string]any
}

type Status string
const (
    StatusActive    Status = "active"
    StatusArchived  Status = "archived"
    StatusSuspended Status = "suspended"
)

type SessionMeta struct {
    ID, Key, AgentID string
    Status           Status
    CreatedAt, UpdatedAt time.Time
}
```

`session.NewSession(id)` 默认 `Status: StatusActive`、`AgentID: ""`、`CreatedAt/UpdatedAt: time.Now()`。

---

## 4. 实现方案

### 4.1 目录结构（v1 增量）

```
src/darvin-agent/internal/
├── store/
│   ├── store.go        # v0 不动（interface）
│   ├── memory.go       # v0 不动（保留作为可选 fallback）
│   ├── models.go       # 🆕 4 GORM 模型
│   ├── sqlite_store.go # 🆕 SQLiteStore 实现
│   ├── memory_test.go  # v0 不动
│   └── sqlite_test.go  # 🆕 SQLite CRUD 测试
├── agent/
│   ├── agent.go        # 改：默认 SQLiteStore
│   └── session/
│       └── session.go  # 改：加 AgentID / Status / SessionMeta
├── database/
│   └── sqlite.go       # 改：Init 走 cfg.SessionsDSN；加 OpenByDSN/GetDSN
└── config/
    └── config.go       # 改：DatabaseConfig 加 SessionsDSN
```

### 4.2 关键决策

#### 4.2.1 sessions.db vs data.db 选哪条路径

`database.Init(cfg)` 改为打开 `cfg.SessionsDSN`（默认 `./sessions.db`）。旧 `cfg.DSN` 留作 fallback 当 SessionsDSN 为空时。

**datadb 会被取代**：原 `data.db` 没有任何表（仅 init 过），所以即使切了 SessionsDSN 也无回归。后续 lobsterai.db（Electron 主进程）由其他 spec 实装。

#### 4.2.2 不加外键约束

`Message.SessionID` 不加 GORM 外键约束（避免删 session 时的级联复杂）。`SessionID` 仅是 indexed 字段。

#### 4.2.3 SkillSnapshot 表暂不写数据

架构文档列出，但 Skills 系统未实装。表结构先建好，将来 Skills 实装时由 Skills 系统填充（不在本 spec 范围）。

#### 4.2.4 Message 表的写入路径不在本 spec

S2 只建表 + SessionStore CRUD。`messages` 表的具体写入由 S4（ACP Loop 接 Agent.Run 后）通过 `executor.RunConversation` 调 store 写入。

#### 4.2.5 Save 行为

`SessionStore.Save` 用 GORM `Save`（upsert by primary key）。同一 ID 二次 Save 覆盖整行。**不**做字段级 merge。

### 4.3 关键代码骨架

```go
// internal/database/sqlite.go (v1)
var (
    globalDB   *gorm.DB
    globalDSN  string
)

func Init(cfg *Config) error {
    dsn := cfg.SessionsDSN
    if dsn == "" { dsn = cfg.DSN }
    globalDSN = dsn

    db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil { return err }
    globalDB = db
    return nil
}

func OpenByDSN(dsn string) (*gorm.DB, error) {
    return gorm.Open(sqlite.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Warn),
    })
}

func GetDSN() string { return globalDSN }

func Get() *gorm.DB {
    if globalDB == nil { panic("database not initialized") }
    return globalDB
}

func AutoMigrate(dst ...interface{}) error {
    return Get().AutoMigrate(dst...)
}
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| sessions.db 文件已存在但 schema 旧 | AutoMigrate 增量加列/索引，不破坏现有数据 |
| Save 空 Session（`sess == nil`） | 返回 `errors.New("store: nil session")`，不写库 |
| Load 不存在的 id | 返回 `ErrNotFound`（与 v0 MemoryStore 行为一致） |
| Delete 不存在的 id | GORM 返回 `RowsAffected=0` 但无 error；本 spec 当成功（幂等） |
| DB 文件被外部进程锁定 | GORM 返回 "database is locked" 错误，向上抛 |
| AutoMigrate 重复调用（第二次启动） | GORM 幂等，不创建重复表 |
| 多个 SQLiteStore 实例共享同一 db 文件 | OK（GORM 池化）；但避免并发 AutoMigrate |
| 极长 Session.Key（>255） | 不限制；如 SQLite 报错向上抛 |
| 表已存在但列缺失（开发期手动改表） | AutoMigrate 会补列；不删列不删数据 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/store/models.go` | 🆕 4 个 GORM 模型 + TableName |
| `src/darvin-agent/internal/store/sqlite_store.go` | 🆕 SQLiteStore |
| `src/darvin-agent/internal/store/sqlite_test.go` | 🆕 CRUD 测试（用 `:memory:` 或 t.TempDir()） |
| `src/darvin-agent/internal/agent/session/session.go` | 改：加 AgentID / Status / SessionMeta |
| `src/darvin-agent/internal/agent/session/session_test.go` | 改：覆盖新字段 |
| `src/darvin-agent/internal/agent/agent.go` | 改：Store nil 时默认构造 SQLiteStore |
| `src/darvin-agent/internal/database/sqlite.go` | 改：Init 走 cfg.SessionsDSN；加 OpenByDSN / GetDSN |
| `src/darvin-agent/internal/config/config.go` | 改：DatabaseConfig 加 SessionsDSN |
| `src/darvin-agent/cmd/app/main.go` | 改：database.Init 后调 AutoMigrate 4 个 model |
| `src/darvin-agent/config.yaml` | 改：加 `database.sessions_dsn: ./sessions.db` |

**不修改**：
- `internal/agent/store/memory.go`（保留为可选 fallback）
- `internal/agent/store/store.go`（interface 不变）
- `internal/agent/store/memory_test.go`（不回归）
- lobsterai.db 相关（Electron 主进程侧，本 spec 不动）

---

## 7. 验收标准

- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] `go test ./internal/store/... -race` 全绿
- [ ] 启动 Go agent 后 `./sessions.db` 文件被创建
- [ ] `sqlite3 sessions.db ".tables"` 输出 `compaction_checkpoints  messages  sessions  skill_snapshots`
- [ ] `sqlite3 sessions.db ".schema sessions"` 含 6 列（id/key/agent_id/status/created_at/updated_at）
- [ ] `SQLiteStore.Save → Load` round-trip 后字段值完全一致（CreatedAt/UpdatedAt 时间戳精度容忍 ≤1ms）
- [ ] `SQLiteStore.List` 返回按 updated_at desc 排序
- [ ] `SQLiteStore.Delete("nonexistent")` 不报错（幂等）
- [ ] `agent.New(NewAgentConfig{Store: nil})` 不再 panic，且 Store 字段被自动设为 `*SQLiteStore`
- [ ] 旧 `cfg.DSN` 不存在 + `cfg.SessionsDSN` 存在 → Init 走 sessions.db 路径
- [ ] 旧 `cfg.DSN` 存在 + `cfg.SessionsDSN` 不存在 → Init 仍走旧 dsn 路径（向后兼容）
- [ ] AutoMigrate 重复调用不报错（GORM 幂等）
- [ ] `session.NewSession(id)` 返回的 Session 含 `Status: StatusActive`、`AgentID: ""`、`CreatedAt/UpdatedAt: time.Now()`（精度内）

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S3** agent-gateway-server | SessionManager 内存映射 + EventLedger（写 sessions.db） |
| **S4** agent-acp-loop | executor.RunConversation 中通过 store.Save 写 messages 表 |
| lobsterai.db | Electron 主进程侧 SQLite（用户设置 / IM 配置） |
| Schema migration 工具 | v0 阶段 AutoMigrate 够用；正式 release 前需引入 goose / atlas |
| SessionStore 二级索引 | 按 AgentID / Status / 时间范围查询（List 已实现，全表扫描） |
| Soft delete | 当前 Delete 硬删；可加 `deleted_at` 字段做软删 |