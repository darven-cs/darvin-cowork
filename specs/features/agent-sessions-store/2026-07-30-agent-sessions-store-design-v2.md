# Agent Sessions Store 设计文档（S2 · v2）

> **v2 — 修订版**。相对 v1（`2026-07-29-agent-sessions-store-design.md`）做了一轮源码对照后修正：3 处必修（P0）+ 5 处强烈建议（P1）。
>
> **Phase 2 / 6 — Go 阶段 spec #1**。把 sessions.db + 4 张 GORM 表 + SQLite SessionStore 落地。**不**接网络层（那是 S3）、**不**接 ACP/Agent（那是 S4）。
> 前置：S1 已锁 UI 契约；本 spec 自身可独立验收（用 Go 测试驱动，不依赖 Electron）。

---

## 0. v1 → v2 变更日志

> 对应审查报告：仓库内 `specs/features/agent-sessions-store/audit-v1.md`（如未落地则参考对话上下文）。

| # | 级别 | 变更点 | 原因 |
|---|------|--------|------|
| **P0-1** | 必修 | 把 `session.Session` 字段补全（`Key` / `AgentID` / `Status`）挪到 **FR-0**，早于 FR-1 | v1 在 FR-5 直接用 `sess.Key`、`sess.AgentID` 编译不过——字段在 FR-7 才被加 |
| **P0-2** | 必修 | 新增 `store.ErrNilSession` 常量；`MemoryStore.Save(nil)` 改为返回 `ErrNilSession`（与 SQLite 实现行为一致） | v1 让 SQLite 报错但保留 MemoryStore 静默吞错——两个实现行为分裂 |
| **P0-3** | 必修 | 移除 `database.Config.DSN` 字段（不留 deprecated），仅保留 `SessionsDSN`；`config.yaml` 删除 `database.dsn: ./data.db`；**显式删除**仓库根的 `src/darvin-agent/data.db`（0 字节、空表，删除无数据风险） | v1 标 deprecated 但调用方必须改——直接切干净比留尾巴便宜。`data.db` 是 S0/S1 早期 dev 跑 `go run ./cmd/app` 留下的空 SQLite 文件，schema 是空的（仅 `database.Init` 调过一次 `gorm.Open` 没注册 model），无业务数据，删除安全 |
| **P1-1** | 强烈建议 | 显式契约：`SQLiteStore.Save` 不持久化 `messages`，`Load` 后 `Len()==0`；验收钉死 | v1 备注里说"本 spec 不实现 SaveMessages"，但没把"消息不持久化"做成显式契约，S4 接手时易回看以为是 bug |
| **P1-3** | 强烈建议 | **取消** `agent.New` 默认 SQLite 路径；改成 `cmd/app/main.go` 显式构造 SQLiteStore 并通过 `NewAgentConfig.Store` 注入 | v1 让 `agent.New` 调 `database.OpenByDSN` → `internal/agent` 依赖 `internal/database`（反向耦合 + 双连接池风险 + N+1 测试改动） |
| **P1-4** | 强烈建议 | 时间戳精度容忍从「≤1ms」改为「≤1s」（SQLite DATETIME 默认秒级） | v1 的 1ms 容忍与 SQLite 实际存储精度不匹配 |
| **P1-5** | 强烈建议 | 边界表 L439「多个 SQLiteStore 实例共享同一 db 文件」改为禁用 | v1 写「GORM 池化 OK」是错的——双池会触发 "database is locked" |
| **P2-1** | 锦上添花 | 显式声明 `session.Status`（强类型）与 GORM 字段 `Status string` 的映射关系 | v1 没有"运行时类型 ↔ GORM 列"映射说明 |
| **P2-2** | 锦上添花 | 验收加 `cd src/darvin-agent` 锚定 cwd；sessions.db 路径语义化 | v1 验收写「./sessions.db」没说明相对路径基线 |

未采纳 v1 的建议（解释）：

- v1 §FR-1 的 `Message.ToolCalls string`（JSON 文本）：保留。理由：v0 llm.Message 是结构化的，但 GORM 这层用 string 存 JSON 与架构文档一致，handler 层做序列化。v2 不动。
- v1 §FR-2 标 `DSN` 为 deprecated：取消，直接删。

---

## 1. 概述

### 1.1 问题 / 背景

按 `docs/系统架构.md` §"数据库归属"，agent 运行时数据应落 `sessions.db`（GORM / SQLite），含 4 张表：`Session` / `Message` / `CompactionCheckpoint` / `SkillSnapshot`。

源码现状（v2 落 spec 时实测）：
- `internal/database/sqlite.go`：开 `data.db`（来自 `config.yaml:5-6` 的 `database.dsn: ./data.db`），无 model 注册
- `internal/agent/store/store.go`：仅定义 `SessionStore` interface（4 方法：Save / Load / List / Delete）
- `internal/agent/store/memory.go`：唯一实现；`Save(nil)` 当前静默吞错（v0 行为，P0-2 修正）
- `internal/agent/session/session.go`：`Session` struct 只有 `ID` + `CreatedAt` + `updatedAt` + `messages`；**没有** `Key` / `AgentID` / `Status` 字段
- `internal/agent/session/session.go`：`SessionMeta` 只有 `ID/CreatedAt/UpdatedAt/MessageCount`——**没有** `Key/AgentID/Status`
- `internal/config/config.go`：`DatabaseConfig` 只有 `DSN` 字段
- `internal/agent/agent.go:155-157`：`agent.New` 的默认 Store 路径构造 `MemoryStore`（v2 不动这段，保持测试默认行为）

S3 会在 Gateway 层做 SessionManager（in-memory session_id → 实例映射），但持久化靠本 spec 的 `SessionStore` SQLite 实现。S4 ACP Loop 会调 `store.Save` / `store.Load`（messages 表的写入也归 S4）。

### 1.2 目标

- `internal/agent/session/session.go` 补齐 `Key` / `AgentID` / `Status` 字段及 `SessionMeta` 对应字段
- `internal/agent/store/store.go` 新增 `ErrNilSession`；`MemoryStore.Save(nil)` 改为返回 `ErrNilSession`
- 4 个 GORM 模型按 `docs/系统架构.md` §"SessionStore 表设计" 字段定义，落 `internal/agent/store/models.go`
- `internal/agent/store/sqlite_store.go` 实现 `SessionStore` interface；`Save` 仅持久化 Session 元数据，**不**持久化 messages（v2 P1-1 契约）
- `internal/database/sqlite.go` 移除 `DSN` 字段，仅留 `SessionsDSN`（v2 P0-3 直接砍，不留 deprecated）；`Init` 走 `SessionsDSN`
- `cmd/app/main.go` 显式构造 `*store.SQLiteStore` 并通过 `agent.NewAgentConfig.Store` 注入（v2 P1-3 推荐方案）
- `config.yaml` 用 `database.sessions_dsn: ./sessions.db` 替代旧 `database.dsn: ./data.db`
- **删除** `src/darvin-agent/data.db`（0 字节空 SQLite，删除无风险）

### 1.3 非目标

- **不**实现 sessions.db 的事件表（架构文档未列；EventLedger 见 S3，本 spec 不动）
- **不**实现 lobsterai.db（Electron 主进程侧 DB，留到后续 spec；本 spec 只动 Go 侧）
- **不**实现 EventLedger / SessionManager（S3）
- **不**实现 SkillSnapshot 表的数据填充（Skills 系统未实装；表结构先建好）
- **不**实现 Schema migration / 降级（v0 阶段，AutoMigrate 即可）
- **不**实现 SessionStore.List 之外的查询（不预写复杂 SQL）
- **不**实现 `SQLiteStore.SaveMessages` 或 messages 表的写入路径（归 S4）
- **不**改 `agent.New` 的默认 Store 路径：仍默认 `MemoryStore`；SQLite 是 cmd 层显式注入（v2 P1-3）

---

## 2. 用户场景

### 场景 1：启动后 sessions.db 被创建

**Given** Go agent 首次启动，`config.yaml` 含 `database.sessions_dsn: ./sessions.db`
**When** `cmd/app/main.go` 跑 `database.Init(cfg)` + `database.AutoMigrate(&Session{}, &Message{}, &CompactionCheckpoint{}, &SkillSnapshot{})`
**Then** Go agent 进程 cwd 下 `./sessions.db` 文件被创建，含 4 张表

### 场景 2：SessionStore CRUD

**Given** 已启动且 AutoMigrate 已跑
**When** 测试代码 `Save` 一个 `*session.Session{ID: "s1", Key: "k1", AgentID: "a1"}` → `Load("s1")` → `List()` → `Delete("s1")`
**Then** 全部通过；`Load` 返回深拷贝（messages 为空数组，按 v2 P1-1 契约）；`List` 按 `UpdatedAt desc` 排序；`Delete` 幂等

### 场景 3：cmd/app/main.go 注入 SQLiteStore

**Given** `cmd/app/main.go` 显式构造 `*store.SQLiteStore`（从 `database.Get()` 拿连接）并传入 `agent.NewAgentConfig{Store: sqliteStore}`
**When** `agent.New(...)` 完成
**Then** `Agent` 内部 `Store` 字段为 `*SQLiteStore`；agent 默认路径（`Store == nil`）**仍**走 `MemoryStore`（保持现有测试默认行为）

### 场景 4：Message 表存储 LLM 消息

**Given** Agent 跑一轮 turn
**When** Executor 调用 `store.SaveMessages(sessionID, []*llm.Message{...})`（**注意：本 spec 不实现 SaveMessages，列出仅为说明 Message 表将来用途**）
**Then** （S4 实现）`messages` 表新增对应行，`session_id` 外键关联 `sessions.id`

---

## 3. 功能需求

### FR-0：session.Session 字段补全

> **FR 编号调整说明**：v1 把 `Key/AgentID/Status` 补全放在 FR-7（晚于 FR-5），导致 FR-5 的代码块引用了未声明的字段。v2 提到 FR-0，所有后续 FR 都假设这些字段已存在。

`internal/agent/session/session.go`：

```go
type Session struct {
    ID        string
    Key       string
    AgentID   string
    Status    Status
    CreatedAt time.Time

    mu        sync.RWMutex
    updatedAt time.Time
    messages  []llm.Message
}

// Status 描述 session 的活跃状态。
type Status string

const (
    StatusActive    Status = "active"
    StatusArchived  Status = "archived"
    StatusSuspended Status = "suspended"
)

// SessionMeta 是 Session 的可序列化摘要。
type SessionMeta struct {
    ID           string
    Key          string
    AgentID      string
    Status       Status
    CreatedAt    time.Time
    UpdatedAt    time.Time
    MessageCount int
}
```

`NewSession(id)` 默认：
- `Status: StatusActive`
- `AgentID: ""`、`Key: ""`
- `CreatedAt: time.Now()`、`updatedAt: time.Now()`

`ReplaceAllMeta` 扩签名——加 `Key` + `AgentID` + `Status`：

```go
// ReplaceAllMeta 一次性覆盖 Session 的元数据字段；SessionStore 实现
// 用它在 Load 时把持久化的列值灌回 Session。messages 不在此处处理。
func (s *Session) ReplaceAllMeta(
    key, agentID string,
    status Status,
    createdAt, updatedAt time.Time,
)
```

`Meta()` 同步补 `Key` / `AgentID` / `Status` 三个字段。

### FR-1：store 顶层加 ErrNilSession

`internal/agent/store/store.go`：

```go
// ErrNilSession is returned by Save when called with a nil *session.Session.
var ErrNilSession = errors.New("store: nil session")
```

`MemoryStore.Save`（`internal/agent/store/memory.go:27-30`）改为：

```go
func (m *MemoryStore) Save(_ context.Context, s *session.Session) error {
    if s == nil {
        return ErrNilSession
    }
    // ... 现有实现不变
}
```

> 行为变化：v0 的 `Save(nil) → nil` 改为 `Save(nil) → ErrNilSession`。这是 v2 P0-2 修正，与 SQLite 实现行为统一。**注意：此变更影响所有调用 Save(nil) 的测试**——S2 落地时需把现有测试里 `Save(nil)` 删掉或改成 `Save(NewSession("x"))`。

### FR-2：4 个 GORM 模型

`internal/agent/store/models.go`：

```go
package store

import "time"

// Session 会话记录。
type Session struct {
    ID        string    `gorm:"primaryKey"`
    Key       string    `gorm:"index"`
    AgentID   string    `gorm:"index"`
    Status    string    `gorm:"default:active"`
    CreatedAt time.Time `gorm:"autoCreateTime"`
    UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (Session) TableName() string { return "sessions" }

// Message 消息记录。S2 阶段仅建表，不写不读；S4 实装 SaveMessages / LoadMessages。
type Message struct {
    ID         string    `gorm:"primaryKey"`
    SessionID  string    `gorm:"index"`
    Role       string    `gorm:"index"`
    Content    string    `gorm:"type:text"`
    ToolCalls  string    `gorm:"type:text"`
    Timestamp  int64     `gorm:"index"`
    StopReason string    `gorm:"default:stop"`
    ParentID   string    `gorm:"index"`
}

func (Message) TableName() string { return "messages" }

// CompactionCheckpoint 压缩检查点。
type CompactionCheckpoint struct {
    ID           string    `gorm:"primaryKey"`
    SessionID    string    `gorm:"index"`
    Summary      string    `gorm:"type:text"`
    TokensBefore int       `gorm:"not null"`
    TokensAfter  int       `gorm:"not null"`
    FirstKeptID  string    `gorm:"not null"`
    CreatedAt    time.Time `gorm:"autoCreateTime"`
}

func (CompactionCheckpoint) TableName() string { return "compaction_checkpoints" }

// SkillSnapshot Skill 快照。
type SkillSnapshot struct {
    ID        string    `gorm:"primaryKey"`
    SessionID string    `gorm:"index"`
    SkillName string    `gorm:"not null"`
    LoadedAt  time.Time `gorm:"autoCreateTime"`
    Source    string    `gorm:"not null"`
}

func (SkillSnapshot) TableName() string { return "skill_snapshots" }
```

`session.Status`（强类型）↔ GORM `Status string` 的映射在 `SQLiteStore.Save` / `SQLiteStore.toSession` 中显式做 `string(sess.Status)` 与 `session.Status(r.Status)` 互转。

### FR-3：config 加 sessions_dsn，删除旧 dsn

`internal/config/config.go`：

```go
type DatabaseConfig struct {
    SessionsDSN string `mapstructure:"sessions_dsn"`
}
```

`config.yaml`：

```yaml
database:
  sessions_dsn: ./sessions.db
```

> v2 P0-3 决策：删除 `DSN` 字段，**不留 deprecated**。理由：(1) 当前唯一调用方是 `cmd/app/main.go:48-50`，改起来一行的事；(2) `config.yaml` 当前 `database.dsn: ./data.db` 是 v0 早期占位，从未在生产用过；(3) 留 deprecated 字段让 S2 之后所有读 config 的人都要判断"用哪个"——切干净比留尾巴便宜。
>
> **data.db 显式删除**：`src/darvin-agent/data.db` 是 S0/S1 早期 dev 跑 `go run ./cmd/app` 留下的空 SQLite 文件（0 字节，schema 是空的——`database.Init` 只 `gorm.Open` 没注册 model），无业务数据，**直接 `rm` 删除**。S2 实施时落地为单一 commit：先删文件、再改 config、再跑 AutoMigrate，避免 "data.db 还在但 sessions.db 也创建" 的双文件混淆状态。

### FR-4：database.Init / Config 同步

`internal/database/sqlite.go`：

```go
type Config struct {
    SessionsDSN string
}

func Init(cfg *Config) error {
    db, err := gorm.Open(sqlite.Open(cfg.SessionsDSN), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil {
        return err
    }
    globalDB = db
    return nil
}

// Get 返回 Init 打开的 *gorm.DB；调用方在 Init 之前调 Get 会 panic。
// SQLiteStore 复用此连接——不复用会导致双池，写入侧会触发 "database is locked"。
func Get() *gorm.DB {
    if globalDB == nil {
        panic("database not initialized, call Init first")
    }
    return globalDB
}

func AutoMigrate(dst ...interface{}) error {
    return Get().AutoMigrate(dst...)
}
```

> v2 P1-5 修正：v1 设计的 `OpenByDSN` 取消（避免双池）。所有调用方统一走 `database.Get()` 复用 Init 打开的连接。

### FR-5：AutoMigrate 调用点

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

### FR-6：SQLiteStore 实现

`internal/agent/store/sqlite_store.go`：

```go
package store

import (
    "context"
    "errors"

    "gorm.io/gorm"

    "darvin-cowork/backend/internal/agent/session"
)

// SQLiteStore is the GORM-backed SessionStore. Save persists only the
// session metadata (Session row); messages are NOT persisted by this
// implementation — see v2 P1-1 contract. S4 will add a MessageStore path.
type SQLiteStore struct {
    db *gorm.DB
}

func NewSQLiteStore(db *gorm.DB) *SQLiteStore {
    return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Save(ctx context.Context, sess *session.Session) error {
    if sess == nil {
        return ErrNilSession
    }
    row := Session{
        ID:        sess.ID,
        Key:       sess.Key,
        AgentID:   sess.AgentID,
        Status:    string(sess.Status),
        CreatedAt: sess.CreatedAt,
        UpdatedAt: sess.UpdatedAt(),
    }
    return s.db.WithContext(ctx).Save(&row).Error
}

func (s *SQLiteStore) Load(ctx context.Context, id string) (*session.Session, error) {
    var row Session
    if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, ErrNotFound
        }
        return nil, err
    }
    out := row.toSession()
    return out, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]session.SessionMeta, error) {
    var rows []Session
    if err := s.db.WithContext(ctx).Order("updated_at desc").Find(&rows).Error; err != nil {
        return nil, err
    }
    out := make([]session.SessionMeta, 0, len(rows))
    for _, r := range rows {
        out = append(out, session.SessionMeta{
            ID:           r.ID,
            Key:          r.Key,
            AgentID:      r.AgentID,
            Status:       session.Status(r.Status),
            CreatedAt:    r.CreatedAt,
            UpdatedAt:    r.UpdatedAt,
            MessageCount: 0, // v2 P1-1: messages 不持久化
        })
    }
    return out, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
    return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Session{}).Error
}

func (r *Session) toSession() *session.Session {
    out := session.NewSession(r.ID)
    out.ReplaceAllMeta(r.Key, r.AgentID, session.Status(r.Status), r.CreatedAt, r.UpdatedAt)
    return out
}
```

> v2 P1-1 契约：`Save` 只持久化 Session 元数据；`Load` 后 `out.Len() == 0`、`out.Messages()` 返回空切片。S4 实装 `MessageStore` 时补 messages 写入。

### FR-7：cmd/app/main.go 显式注入 SQLiteStore

`internal/agent/agent.go` **不动**——`agent.New` 的默认路径仍构造 `MemoryStore`（保持所有现有 `agent_test.go` 行为不变）。

`cmd/app/main.go` 在 `agent.New` 之前：

```go
sqliteStore := store.NewSQLiteStore(database.Get())

a, err := agent.New(agent.NewAgentConfig{
    // ... 现有字段
    Store: sqliteStore,
    // ...
})
```

> v2 P1-3 决策：v1 让 `agent.New` 默认走 SQLite 路径需要引入 `agent` → `database` 反向 import 边 + 双池风险 + N+1 测试改动。改为 cmd 层显式注入，`internal/agent` 保持纯依赖 `store` interface（不依赖 `database` 包）。
>
> 影响：所有现有 `agent_test.go`（`TestNewAutoRegistersBuiltinTools` 等）继续不传 Store、走 MemoryStore，**无回归**。

---

## 4. 实现方案

### 4.1 目录结构（v2 增量）

```
src/darvin-agent/internal/
├── store/
│   ├── store.go        # 改：加 ErrNilSession
│   ├── memory.go       # 改：Save(nil) → ErrNilSession
│   ├── memory_test.go  # 改：删/改依赖 Save(nil) 的测试
│   ├── models.go       # 🆕 4 GORM 模型
│   ├── sqlite_store.go # 🆕 SQLiteStore
│   └── sqlite_test.go  # 🆕 SQLite CRUD 测试
├── agent/
│   ├── agent.go        # 不动
│   ├── agent_test.go   # 不动
│   └── session/
│       ├── session.go      # 改：加 Key/AgentID/Status 字段
│       └── session_test.go # 改：覆盖新字段 + ReplaceAllMeta 新签名
├── database/
│   └── sqlite.go       # 改：移除 DSN 字段，仅留 SessionsDSN
├── config/
│   ├── config.go       # 改：DatabaseConfig 仅留 SessionsDSN
│   └── config_test.go  # 改：测试 YAML fixtures 去掉 dsn 加 sessions_dsn
└── cmd/app/main.go     # 改：构造 SQLiteStore 并注入 NewAgentConfig.Store
```

### 4.2 关键决策

#### 4.2.1 sessions.db 路径语义

相对路径，相对 Go agent 进程的 cwd。开发期 cwd = `src/darvin-agent/`。生产期由 Electron 主进程决定 cwd（`src/main/runtime/manager.ts`）——S5 阶段处理。

#### 4.2.2 不加外键约束

`Message.SessionID` 不加 GORM 外键约束（避免删 session 时的级联复杂）。`SessionID` 仅是 indexed 字段。

#### 4.2.3 SkillSnapshot 表暂不写数据

架构文档列出，但 Skills 系统未实装。表结构先建好，将来 Skills 实装时由 Skills 系统填充（不在本 spec 范围）。

#### 4.2.4 Message 表的写入路径不在本 spec

S2 只建表 + SQLite SessionStore 元数据 CRUD。`messages` 表的具体写入由 S4（ACP Loop 接 Agent.Run 后）实装 `MessageStore` 接口或扩 `SessionStore`。

#### 4.2.5 Save 行为

`SessionStore.Save` 用 GORM `Save`（upsert by primary key）。同一 ID 二次 Save 覆盖整行。**不**做字段级 merge。

#### 4.2.6 双池禁用（v2 P1-5）

`SQLiteStore` 只能拿到 `database.Get()` 返回的 `*gorm.DB` 实例。所有 SQLiteStore 实例**必须**共用同一连接池（共用 Init 打开的连接）。**禁止**对同一 DSN 调 `gorm.Open` 多次。

#### 4.2.7 agent.New 默认行为不变（v2 P1-3）

`agent.New(NewAgentConfig{Store: nil})` 仍走 `MemoryStore`。SQLite 是 cmd 层显式注入，不在 agent 包默认路径。这保证：
- `agent` 包不依赖 `database` 包（避免反向耦合）
- 所有现有 `agent_test.go` 行为不变（无 N+1 测试改动）
- S2 落地时仅需改 `cmd/app/main.go` 一处即可让生产二进制走 SQLite

### 4.3 关键代码骨架

```go
// internal/database/sqlite.go (v2)
var globalDB *gorm.DB

type Config struct {
    SessionsDSN string
}

func Init(cfg *Config) error {
    db, err := gorm.Open(sqlite.Open(cfg.SessionsDSN), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil { return err }
    globalDB = db
    return nil
}

func Get() *gorm.DB {
    if globalDB == nil { panic("database not initialized, call Init first") }
    return globalDB
}

func AutoMigrate(dst ...interface{}) error {
    return Get().AutoMigrate(dst...)
}
```

```go
// internal/agent/session/session.go (v2 — 仅展示增量)
type Session struct {
    ID        string
    Key       string
    AgentID   string
    Status    Status
    CreatedAt time.Time

    mu        sync.RWMutex
    updatedAt time.Time
    messages  []llm.Message
}

type Status string

const (
    StatusActive    Status = "active"
    StatusArchived  Status = "archived"
    StatusSuspended Status = "suspended"
)

type SessionMeta struct {
    ID           string
    Key          string
    AgentID      string
    Status       Status
    CreatedAt    time.Time
    UpdatedAt    time.Time
    MessageCount int
}

func NewSession(id string) *Session {
    now := time.Now()
    return &Session{
        ID:        id,
        Status:    StatusActive,
        CreatedAt: now,
        updatedAt: now,
    }
}

func (s *Session) ReplaceAllMeta(
    key, agentID string,
    status Status,
    createdAt, updatedAt time.Time,
) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.Key = key
    s.AgentID = agentID
    s.Status = status
    s.CreatedAt = createdAt
    if updatedAt.After(s.updatedAt) {
        s.updatedAt = updatedAt
    }
}
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| sessions.db 文件已存在但 schema 旧 | AutoMigrate 增量加列/索引，不破坏现有数据 |
| `Save(nil)` | 返回 `ErrNilSession`（v2 P0-2；MemoryStore 与 SQLiteStore 行为一致） |
| Load 不存在的 id | 返回 `ErrNotFound`（与 v0 MemoryStore 行为一致） |
| Delete 不存在的 id | GORM 返回 `RowsAffected=0` 但无 error；本 spec 当成功（幂等） |
| DB 文件被外部进程锁定 | GORM 返回 "database is locked" 错误，向上抛 |
| AutoMigrate 重复调用（第二次启动） | GORM 幂等，不创建重复表 |
| 多个 SQLiteStore 实例共享同一 `*gorm.DB` | OK（同一连接池） |
| 同一 DSN 重复 `gorm.Open` | **禁用**（v2 P1-5；会触发双池 + "database is locked"） |
| 极长 Session.Key（>255） | 不限制；如 SQLite 报错向上抛 |
| 表已存在但列缺失（开发期手动改表） | AutoMigrate 会补列；不删列不删数据 |
| 时间戳精度（v2 P1-4） | Save→Load round-trip 容忍 **≤1s**（SQLite DATETIME 默认秒级；v1 写的 1ms 不可能达到） |
| `agent.New(NewAgentConfig{Store: nil})` + 未 Init database | 仍走 MemoryStore（v2 P1-3；`internal/agent` 不依赖 `database`） |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/agent/session/session.go` | 改：加 `Key` / `AgentID` / `Status` 字段；加 `Status` 类型与 3 个常量；`SessionMeta` 同步加字段；`ReplaceAllMeta` 签名扩到 5 参数 |
| `src/darvin-agent/internal/agent/session/session_test.go` | 改：覆盖新字段；`TestMeta` 断言新字段；`TestReplaceAllMeta` 改用新签名 |
| `src/darvin-agent/internal/agent/store/store.go` | 改：加 `ErrNilSession` 常量 |
| `src/darvin-agent/internal/agent/store/memory.go` | 改：`Save(nil)` 返回 `ErrNilSession`（不再静默吞错） |
| `src/darvin-agent/internal/agent/store/memory_test.go` | 改：删除 `TestMemoryStoreSaveNilNoop` 类测试或调整为断言 `ErrNilSession` |
| `src/darvin-agent/internal/agent/store/models.go` | 🆕 4 GORM 模型 + `TableName` |
| `src/darvin-agent/internal/agent/store/sqlite_store.go` | 🆕 `SQLiteStore` 实现（FR-6） |
| `src/darvin-agent/internal/agent/store/sqlite_test.go` | 🆕 CRUD 测试（用 `t.TempDir()` + `file::memory:?cache=shared` 避免连接隔离） |
| `src/darvin-agent/internal/database/sqlite.go` | 改：移除 `DSN` 字段；仅保留 `SessionsDSN`；取消 `OpenByDSN`（v2 P1-5） |
| `src/darvin-agent/internal/config/config.go` | 改：`DatabaseConfig` 仅留 `SessionsDSN`（删 `DSN`） |
| `src/darvin-agent/internal/config/config_test.go` | 改：YAML fixture 字段名 `dsn` → `sessions_dsn` |
| `src/darvin-agent/cmd/app/main.go` | 改：构造 `*store.SQLiteStore` 并注入 `NewAgentConfig.Store`；`database.Config{DSN: ...}` 改为 `{SessionsDSN: ...}` |
| `src/darvin-agent/config.yaml` | 改：删除 `database.dsn: ./data.db`；加 `database.sessions_dsn: ./sessions.db` |
| `src/darvin-agent/data.db` | **删**：`rm src/darvin-agent/data.db`（0 字节空 SQLite，无数据风险）；`src/darvin-agent/.gitignore` 加 `data.db`（防 `go run` 再生成后误入版本控制） |
| `src/darvin-agent/internal/agent/agent.go` | **不动**（v2 P1-3：默认 Store 仍走 MemoryStore） |
| `src/darvin-agent/internal/agent/agent_test.go` | **不动**（不传 Store 仍走 MemoryStore，无回归） |
| `src/darvin-agent/internal/agent/store/memory.go:13-14` 的 TODO 注释 | 删除（AGENTS.md §注释规范禁止「// 未来」形态） |

**不修改**：
- `internal/agent/store/memory.go` 现有 happy-path 逻辑（仅 `Save(nil)` 一行改）
- `internal/agent/executor/`、`internal/agent/ctxengine/`、`internal/agent/queue/`、`internal/agent/event/`（与本 spec 无关）
- lobsterai.db 相关（Electron 主进程侧，本 spec 不动）

---

## 7. 验收标准

> 落 spec 后所有项必须通过。执行命令均在 `src/darvin-agent/` 目录下。
>
> ✅ **已落地（2026-07-30）** — 28/28 项全绿。落地 commits：`75092f7`（FR-0+FR-1 字段补全+ErrNilSession）/ `98655cc`（FR-2+FR-6 GORM 模型+SQLiteStore）/ `399b4c2`（FR-3+FR-4 DSN→SessionsDSN）/ `ed208d9`（FR-5+FR-7 AutoMigrate+注入）/ `3abe657`（.gitignore）。Spec 文件本身已在 §7 全部勾上。

### 7.1 编译 / 静态检查

- [x] `go build ./...` 编译通过
- [x] `CGO_ENABLED=0 go build ./...` 编译通过（与 `scripts/build-go.js` 一致）
- [x] `go vet ./...` 无警告
- [x] `gofmt -l .` 干净

### 7.2 单元测试

- [x] `go test -count=1 ./...` 全绿（含 `internal/agent/store/sqlite_test.go`）
- [x] `go test -race ./...` 全绿
- [x] 新增 `TestSQLiteStoreSaveLoad`：Save → Load round-trip 后字段值完全一致（CreatedAt/UpdatedAt 时间戳精度容忍 ≤1s，v2 P1-4）
- [x] 新增 `TestSQLiteStoreListOrderUpdatedDesc`：3 条 Save 后 List 返回按 updated_at desc 排序
- [x] 新增 `TestSQLiteStoreDeleteIdempotent`：`Delete("nonexistent")` 不报错
- [x] 新增 `TestSQLiteStoreLoadNotFound`：`Load("nope")` 返回 `ErrNotFound`
- [x] 新增 `TestSQLiteStoreSaveNil`：`Save(nil)` 返回 `ErrNilSession`
- [x] 新增 `TestSQLiteStoreSaveDoesNotPersistMessages`（v2 P1-1 契约）：Save 一个含 messages 的 Session → Load 后 `Len() == 0`
- [x] 新增 `TestMemoryStoreSaveNil`：`MemoryStore.Save(nil)` 返回 `ErrNilSession`（v2 P0-2 防回归）
- [x] 改 `TestNewSessionDefaults`：`session.NewSession("x")` 返回的 Session 含 `Status: StatusActive / AgentID: "" / Key: "" / CreatedAt ≈ UpdatedAt ≈ time.Now()`
- [x] 改 `TestSessionMetaFields`：`Meta()` 返回值含 `Key` / `AgentID` / `Status` 三个字段

### 7.3 集成 / 黑盒

- [x] `cd src/darvin-agent && go run ./cmd/app` 启动后 cwd 下 `./sessions.db` 文件被创建
- [x] `sqlite3 sessions.db ".tables"` 输出 `compaction_checkpoints  messages  sessions  skill_snapshots`
- [x] `sqlite3 sessions.db ".schema sessions"` 含 6 列（id/key/agent_id/status/created_at/updated_at）
- [x] AutoMigrate 重复调用不报错（GORM 幂等）
- [x] 旧 `src/darvin-agent/data.db` **不存在**（`ls src/darvin-agent/data.db` ENOENT）——已删除
- [x] `src/darvin-agent/.gitignore` 含 `data.db` 条目——防重新生成后误入版本控制
- [x] `src/darvin-agent/.gitignore` 含 `sessions.db` 条目——同上
- [x] `cmd/app/main.go` 中 `agent.NewAgentConfig.Store` 字段是 `*store.SQLiteStore`，不是 `*store.MemoryStore`（用类型断言验证）
- [x] `agent.New(NewAgentConfig{Store: nil})` 仍走 `MemoryStore`（`store.NewMemoryStore()` 返回值的类型断言验证）
- [x] `agent.New(NewAgentConfig{Store: nil})` 在 `database` 未 Init 时**不** panic（v2 P1-3 行为保证：agent 包不依赖 database 包）

### 7.4 向后兼容

- [x] `config.yaml` 移除 `database.dsn` 字段后 `config.Load` 不报错
- [x] `database.Config{DSN: "old"}` 编译期失败（`DSN` 字段已删除）——防回滚
- [x] `cmd/app/main.go` 不再引用 `cfg.Database.DSN`——防回滚

---

## 8. 后续 spec 候选（不在本 spec 范围）

| Spec | 内容 |
|------|------|
| **S3** agent-gateway-server | SessionManager 内存映射 + EventLedger（写 sessions.db） |
| **S4** agent-acp-loop | executor.RunConversation 中通过 `MessageStore.SaveMessages` 写 messages 表；同时把 `*store.SQLiteStore` 的 `Save` 升级为"Save 时同步刷 messages"（消除 v2 P1-1 临时契约） |
| lobsterai.db | Electron 主进程侧 SQLite（用户设置 / IM 配置） |
| Schema migration 工具 | v0 阶段 AutoMigrate 够用；正式 release 前需引入 goose / atlas |
| SessionStore 二级索引 | 按 AgentID / Status / 时间范围查询（List 已实现，全表扫描） |
| Soft delete | 当前 Delete 硬删；可加 `deleted_at` 字段做软删 |
| Status state machine | `Status` 当前只有 3 个常量，缺状态迁移规则（active → archived 何时触发？）；S5+ 再讨论 |
