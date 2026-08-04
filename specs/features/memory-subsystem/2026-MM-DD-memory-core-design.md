# darvin-cowork Memory Subsystem — Core Design

## Scope

This spec covers storage, file format, FTS5 index, bootstrap files, configuration, and tool integration (sections A, B, C, D, F of CHECKLIST).

Out of scope (separate specs): auto-extract pipeline, renderer UI, bootstrap migration, dreaming.

Reference: `~/桌面/github-project/LobsterAI/src/main/libs/openclawMemoryFile.ts`, `openclawConfigSync.ts`, `coworkStore.ts` memory section.

## 1. 概述

### 1.1 背景

darvin-cowork 当前没有用户记忆系统。LobsterAI 已经把 OpenClaw 的 `MEMORY.md` 文件 + FTS5 SQLite 索引 + per-user provenance 全部跑通；本次要把对等能力移植到 darvin-cowork，**沿用 LobsterAI 的两层存储**：

1. **Source of truth = MEMORY.md**（人类可读，git-friendly，OpenClaw 直接读）
2. **Index / provenance = SQLite + FTS5**（机器可搜索，能 per-session 标记）

renderer 通过 IPC 拿视图与发命令；model 通过 `memory_search / memory_get / memory_write` 工具读写。

### 1.2 关键设计取舍

| 决策 | 选择 | 理由 |
|---|---|---|
| MEMORY.md 解析策略 | **block-aware**（top-bullet + indented children + code fence） | LobsterAI 已验证；上版简化版在多行 block 上失败 |
| Fingerprint 算法 | `sha1(toLowerCase + [^\p{L}\p{N}\s]→space + \s+→single-space + trim)` | LobsterAI 用 `\p{L}\p{N}` Unicode property，**必须**覆盖 CJK |
| FTS5 tokenizer | **trigram**（不是 unicode61） | LobsterAI `openclawConfigSync.ts:1880-1883` 显式注释：`unicode61 cannot tokenize CJK characters` |
| Bootstrap 文件名 | 严格白名单 `{IDENTITY.md, USER.md, SOUL.md}` + `path.Base === name` | LobsterAI 已用 |
| 默认 IDENTITY.md | 跟随 `app.getLocale()` 切 zh / en | LobsterAI 已用 |
| Capacity 触顶 | 最老 `created` 标 `stale`（保留行，仅 FTS 删除） | 与 LobsterAI 一致 |
| Path 锁 | `state/workspace-main/` 固定，不跟随 `DARVIN_AGENT_WORKSPACE` | 与 LobsterAI `getMainAgentWorkspacePath` 一致 |

### 1.3 非目标

- 不接 Embedding 向量检索（schema 字段保留，UI 加 tab，代码不实接 — 与 LobsterAI 现状一致）
- 不接 LLM Judge auto-extract（`LLMJudgeEnabled` 字段保留，默认 false）
- 不接 Dreaming 后台压缩（DREAMS.md / 三阶段 Light/Deep/REM — 独立 spec）
- 不做多 agent workspace 路由（v1 单 main agent）
- 不接 `memory/YYYY-MM-DD.md` daily notes（v2）
- 不动 OpenClaw 上游（darvin-cowork 用 Go agent；不走 OpenClaw runtime）

## 2. 路径与目录

```
{workspaceRoot}/
├── state/workspace-main/
│   ├── MEMORY.md                  # 人类可读的源数据
│   ├── IDENTITY.md                # 默认有；空 / 缺时由 EnsureDefaultIdentity 补
│   ├── USER.md
│   ├── SOUL.md
│   └── memories.sqlite            # FTS5 + user_memories + user_memory_sources + memory_index_meta_v1
```

常量：

```go
// internal/memory/paths.go
package memory

const (
    StateDir          = "state/workspace-main"
    MemoryFilename    = "MEMORY.md"
    MemoriesDBName    = "memories.sqlite"          // 放在 StateDir 下而非 sessions.db 旁边
    MemoryMetaFile    = "memories.meta.json"       // 保留旧版文件版 meta 作 fallback
    BootstrapAllowlist = "IDENTITY.md|USER.md|SOUL.md"
)
```

**理由**：memories.sqlite 跟 MEMORY.md 同目录，备份 / 迁移时一次 `cp -r state/workspace-main/` 就够；不与 sessions.db 混。

## 3. 文件解析器

### 3.1 Block 类型

```go
// internal/memory/file.go
type BlockKind int

const (
    BlockEntry    BlockKind = iota  // 一个 entry（top-bullet + indented children）
    BlockVerbatim                    // 任何保留字节段的行
)

type Entry struct {
    ID      string  // sha1(normalize(section + "\x00" + display_text))
    Section string
    Text    string  // display text（首行去掉 bullet marker，续行 verbatim）
}

type Block struct {
    Kind    BlockKind
    Section string  // 离该 entry 最近的 ## 标题；verbatim 段为空
    Text    string  // 原始行文本
    Entry   *Entry
}
```

### 3.2 解析规则

照搬 LobsterAI `openclawMemoryFile.ts:131-232`，关键差异：

| LobsterAI 行为 | 本次是否沿用 |
|---|---|
| top-level bullet (`/^-\s+\S/`) 起新 block | ✅ |
| indented children / lazy continuations 进同一 block | ✅ 必须 |
| `##+` heading 设置 section context | ✅ |
| ` ``` ` / `~~~` 围栏代码块 → 进 block 或 verbatim | ✅ 必须 |
| column-0 `<!--` HTML comment → verbatim（避免被当 entry） | ✅ 必须 |
| blank line 关闭当前 block | ✅ |
| orphan content line → prose block（首行不是 bullet 也开 block） | ✅ |

### 3.3 Fingerprint

```go
func normalize(s string) string {
    var b strings.Builder
    b.Grow(len(s))
    prevSpace := true
    for _, r := range strings.ToLower(s) {
        if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r) {
            // 标点 / emoji / 其它 → 折叠为空格
            if !prevSpace { b.WriteByte(' '); prevSpace = true }
            continue
        }
        b.WriteRune(r)
        prevSpace = false
    }
    return strings.TrimSpace(b.String())
}
func Fingerprint(text string) string {
    sum := sha1.Sum([]byte(normalize(text)))
    return hex.EncodeToString(sum[:])
}
```

**测试**：CJK + 英文混合文本 `我每天早上喝燕麦奶` 与 `我每天早上 喝 燕麦奶！` 应当 hash 相同。

### 3.4 serializeEntryLines（新增，上版缺）

```go
// internal/memory/file.go
func serializeEntryLines(text string) []string {
    lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
    // 去尾空白 + 跳空行
    var out []string
    for _, l := range lines {
        l = strings.TrimRight(l, " \t")
        if strings.TrimSpace(l) == "" { continue }
        out = append(out, l)
    }
    if len(out) == 0 {
        return nil  // caller 视为 error
    }
    first := out[0]
    // 首行已经是 bullet 就保留；否则补 `- `
    if !bulletLineRe.MatchString(first) {
        first = "- " + first
    }
    rest := out[1:]
    for i, l := range rest {
        if !startsWithWhitespace(l) {
            rest[i] = "  " + l  // 缩进 2 空格以保证留在同一个 block
        }
    }
    return append([]string{first}, rest...)
}
```

### 3.5 序列化输出

```go
func Render(blocks []Block) string {
    var sb strings.Builder
    for _, b := range blocks {
        switch b.Kind {
        case BlockEntry:
            sb.WriteString("- "); sb.WriteString(b.Text); sb.WriteByte('\n')
        case BlockVerbatim:
            sb.WriteString(b.Text); sb.WriteByte('\n')
        }
    }
    return sb.String()  // 不补尾随换行（round-trip 测试稳定）
}
```

### 3.6 CRUD（对齐 LobsterAI `openclawMemoryFile.ts:342-468`）

| 方法 | 行为 |
|---|---|
| `Parse(path) ([]Block, error)` | 读盘 → ParseBytes |
| `ParseBytes(data) []Block` | 见 §3.2 |
| `Entries(blocks) []Entry` | 过滤 BlockEntry，按文件顺序 |
| `Render(blocks) string` | 见 §3.5 |
| `ReadFile(stateDir) ([]Block, error)` | 缺 / 空 → header-only block list |
| `WriteAtomic(path, blocks) error` | 写 tmp → rename；首次旋转 `.bak`（见 §3.7） |
| `Add(blocks, section, text) ([]Block, *Entry, error)` | 末尾追加；若 section 不存在则先创建 heading |
| `Update(blocks, id, newText, newSection) ([]Block, *Entry, error)` | 替换；id 因 fingerprint 变而变 |
| `Delete(blocks, id) ([]Block, error)` | 移除 entry block；合并相邻 verbatim 段 |
| `SerializeMemoryMd(entries) string` | 完整重新生成（standalone） |

### 3.7 备份与恢复

照搬 LobsterAI `ensureBackup`：

```go
func WriteAtomic(path string, blocks []Block) error {
    body := Render(blocks)
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil { return err }
    bak := path + ".bak"
    if _, err := os.Stat(path); err == nil {
        if _, err := os.Stat(bak); errors.Is(err, fs.ErrNotExist) {
            _ = os.Rename(path, bak)  // best-effort：失败也不阻塞新内容
        }
    }
    return os.Rename(tmp, path)
}
```

**规则**：`.bak` 只写一次，后续再不覆盖（用户手动恢复入口）。

### 3.8 raw view 写入回写 SQLite

`Manager.WriteRaw(content)` 不仅写 MEMORY.md，还要把新内容 parse 后与 SQLite 对齐（见 §6）。

## 4. Bootstrap 文件

### 4.1 白名单 + 路径防护

```go
var allowedBootstrapFilenames = map[string]struct{}{
    "IDENTITY.md": {}, "USER.md": {}, "SOUL.md": {},
}

func ValidateBootstrapFilename(name string) error {
    if name == "" { return errors.New("memory: bootstrap filename required") }
    if filepath.Base(name) != name { return fmt.Errorf("memory: bootstrap filename invalid: %q", name) }
    if _, ok := allowedBootstrapFilenames[name]; !ok {
        return fmt.Errorf("memory: bootstrap filename not allowed: %q", name)
    }
    return nil
}
```

### 4.2 默认 IDENTITY.md（**新增 locale 切换**，对齐 LobsterAI）

```go
const defaultIdentityZH = `... 你叫 Darvin，是 ...`
const defaultIdentityEN = `... You are Darvin, ...`

func defaultIdentity(locale string) string {
    if strings.HasPrefix(strings.ToLower(locale), "zh") { return defaultIdentityZH }
    return defaultIdentityEN
}

func EnsureDefaultIdentity(stateDir string) error {
    path := filepath.Join(stateDir, "IDENTITY.md")
    info, err := os.Stat(path)
    if err == nil && info.Size() > 0 { return nil }  // 用户已编辑，绝不覆盖
    if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
    return os.WriteFile(path, []byte(defaultIdentity(localeFromEnv())), 0o600)
}

func localeFromEnv() string {
    // 1. main 端写进 env（DARVIN_LOCALE）
    // 2. 回退 LC_ALL / LANG
    // 3. 回退 "en"
    if v := os.Getenv("DARVIN_LOCALE"); v != "" { return v }
    if v := os.Getenv("LC_ALL"); v != "" { return v }
    if v := os.Getenv("LANG"); v != "" { return v }
    return "en"
}
```

main 端在 IPC `darvin:get_locale` / `darvin:set_locale` 时同步写 `DARVIN_LOCALE` env（renderer 已经做了 i18n 切换）。

### 4.3 读写原子化

`ReadBootstrap(stateDir, name)`：缺文件不报错，返空串。
`WriteBootstrap(stateDir, name, content)`：tmp + rename，不带备份（用户主动保存，损坏概率低）。

## 5. SQLite + FTS5

### 5.1 Schema

```sql
-- GORM AutoMigrate 跑
CREATE TABLE user_memories (
    id           TEXT PRIMARY KEY,
    text         TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,                  -- 新增：冗余存 sha1，便于 dedup 查询走索引
    confidence   REAL DEFAULT 0.75,
    is_explicit  INTEGER DEFAULT 0,
    status       TEXT DEFAULT 'created',         -- created / stale / deleted
    section      TEXT DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    last_used_at INTEGER
);
CREATE INDEX user_memories_status_idx ON user_memories(status);
CREATE INDEX user_memories_fingerprint_idx ON user_memories(fingerprint);

CREATE TABLE user_memory_sources (
    id          TEXT PRIMARY KEY,
    memory_id   TEXT NOT NULL,
    session_id  TEXT,
    message_id  TEXT,
    role        TEXT DEFAULT 'system',
    is_active   INTEGER DEFAULT 1,
    created_at  INTEGER NOT NULL
);
CREATE INDEX user_memory_sources_memory_idx ON user_memory_sources(memory_id);

-- 手工 raw Exec（不能用 GORM 管理 virtual table）
CREATE VIRTUAL TABLE memory_fts USING fts5(
    memory_id UNINDEXED,
    text,
    tokenize='trigram'
);

-- FTS index schema meta
CREATE TABLE memory_index_meta_v1 (
    key   TEXT PRIMARY KEY,
    value TEXT
);
-- 写一行: ('schema', '{ "model":"fts-only", "tokenizer":"trigram", "built_at":..., "entry_count":N }')
```

### 5.2 写入一致性（**FTS5 virtual table 不支持 UPSERT**）

```go
func writeEntryAndFTS(tx *gorm.DB, m *UserMemory) error {
    if err := tx.Save(m).Error; err != nil { return err }
    if err := tx.Exec(`DELETE FROM memory_fts WHERE memory_id = ?`, m.ID).Error; err != nil { return err }
    if err := tx.Exec(`INSERT INTO memory_fts(memory_id, text) VALUES (?, ?)`, m.ID, m.Text).Error; err != nil { return err }
    return nil
}
```

**关键**：`ON CONFLICT(memory_id) DO UPDATE` 不支持（FTS5 没有 unique index 可以冲突），必须先 DELETE 再 INSERT。LobsterAI 不需要写自己 FTS（OpenClaw 做），但 Go 端走 `internal/memory/db.go` 时已经踩过这个坑。

### 5.3 状态语义

| status | user_memories 行 | memory_fts 行 | UI 可见？ |
|---|---|---|---|
| `created` | 保留 | 保留 | ✅ |
| `stale` | 保留（容量触顶最老的） | 删除 | ✅ （"已陈旧"分组） |
| `deleted` | 保留（id 不重用） | 删除 | ❌（includeDeleted 时返） |

### 5.4 Near-dup 合并（**新增**，对齐 LobsterAI `createOrReviveUserMemory`）

`Manager.Create(text, section, isExplicit, confidence)` 流程：

1. `normalizedText := truncate(replaceWhitespace(text), 360)`
2. `fingerprint := Fingerprint(section + "\x00" + normalizedText)` — 精确查重
3. 若精确命中且 status != 'deleted' → 走 near-dup 分支：
   - `semKey := normalizeMemorySemanticKey(normalizedText)`
   - 拉 status != 'deleted' 的最近 200 条
   - `scoreMemorySimilarity(candidate.semKey, semKey)` 算 bigram-dice
   - 若 `score >= 0.82` → 合并：保留 preferred text（按 `scoreMemoryTextQuality` 比） + max confidence + OR is_explicit
4. 若没命中 → 全新 row

```go
const MEMORY_NEAR_DUPLICATE_MIN_SCORE = 0.82

func scoreMemorySimilarity(left, right string) float64 {
    // 1. 完全相等 → 1.0
    // 2. 折叠空白后包含关系 → 子串覆盖率
    // 3. token overlap (lowercase bag-of-words Jaccard)
    // 4. 字符 bigram Dice
    // 取 max
}
func scoreMemoryTextQuality(s string) float64 {
    // 长度加分；前缀 "我 / my / i" 加成；"该用户 / the user" 减分
}
func choosePreferredMemoryText(current, incoming string) string {
    // 截 360 字符 → scoreMemoryTextQuality → 高的胜出
    // 平局 → 长者胜出
}
```

### 5.5 Capacity pruning

```go
func (m *Manager) enforceCapacity(ctx context.Context) error {
    cap := m.Config.UserMemoriesMaxItems  // [1,60] clamp
    var active int64
    m.DB.Model(&UserMemory{}).Where("status = ?", "created").Count(&active)
    if active <= int64(cap) { return nil }
    overflow := active - int64(cap)
    var ids []string
    m.DB.Model(&UserMemory{}).
        Where("status = ?", "created").
        Order("created_at ASC").
        Limit(int(overflow)).
        Pluck("id", &ids)
    return m.DB.Transaction(func(tx *gorm.DB) error {
        for _, id := range ids {
            if err := tx.Exec(`DELETE FROM memory_fts WHERE memory_id = ?`, id).Error; err != nil { return err }
            if err := tx.Model(&UserMemory{}).Where("id = ?", id).
                Update("status", "stale").Error; err != nil { return err }
        }
        return nil
    })
}
```

### 5.6 Search（FTS5 MATCH + bm25）

```go
type SearchHit struct {
    Entry UserMemory
    Score float64
}

func (m *Manager) Search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
    q = strings.TrimSpace(q)
    if q == "" { return nil, nil }
    if limit <= 0 { limit = 5 }
    var rows []struct {
        ID, Text, Confidence, IsExplicit, Status, Section string
        CreatedAt, UpdatedAt                              int64
        LastUsedAt                                        *int64
        Rank                                              float64
    }
    if err := m.DB.Raw(`
        SELECT m.id, m.text, m.confidence, m.is_explicit, m.status, m.section,
               m.created_at, m.updated_at, m.last_used_at,
               bm25(memory_fts) AS rank
        FROM memory_fts
        JOIN user_memories m ON m.id = memory_fts.memory_id
        WHERE memory_fts MATCH ? AND m.status = 'created'
        ORDER BY rank ASC
        LIMIT ?
    `, q, limit).Scan(&rows).Error; err != nil {
        return nil, err
    }
    // 顺便更新 last_used_at = now()
    // ...
    return hits, nil
}
```

## 6. Manager facade

```go
package memory

type Manager struct {
    StateDir string
    DB       *gorm.DB
    Config   config.MemoryConfig
    Log      *zap.Logger
}

func New(stateDir string, db *gorm.DB, cfg config.MemoryConfig, log *zap.Logger) (*Manager, error)
func (m *Manager) Migrate(ctx context.Context) error         // AutoMigrate + EnsureFTS + EnsureMeta
func (m *Manager) Reindex(ctx context.Context) error         // 见 §7
func (m *Manager) List(ctx, ListQuery) ([]UserMemory, int64, error)
func (m *Manager) Create(ctx, text, section string, isExplicit bool, confidence float64) (UserMemory, error)
func (m *Manager) Update(ctx, id string, patch UpdatePatch) (UserMemory, error)
func (m *Manager) Delete(ctx, id string) error
func (m *Manager) Get(ctx, id string) (UserMemory, error)
func (m *Manager) Search(ctx, query string, limit int) ([]SearchHit, error)
func (m *Manager) ReadRaw(ctx) (string, string, error)         // content, path
func (m *Manager) WriteRaw(ctx, content string) error           // 见 §3.8
func (m *Manager) Stats(ctx) (Stats, error)
func (m *Manager) ReadBootstrap(name string) (string, error)
func (m *Manager) WriteBootstrap(name, content string) error
```

写入 MEMORY.md 的方法都走 `m.mu sync.Mutex` 串行化（renderer raw edit vs auto-extract vs tool call 抢占）。

### 6.1 ListQuery

```go
type ListQuery struct {
    Section string  // 空 = 全部
    Status  string  // 空 = "created"
    Limit   int     // <=0 = 100
    Offset  int
}

type Stats struct {
    Total     int64
    ByStatus  map[string]int64      // created / stale / deleted
    ByExplicit map[string]int64     // explicit / implicit
    FTSStatus string               // fresh / stale / rebuilding
}
```

## 7. Reindex 与 meta 同步

```go
type memoryMeta struct {
    SchemaVersion int    `json:"schema_version"`
    Tokenizer     string `json:"tokenizer"`      // "trigram"
    BuiltAt       int64  `json:"built_at"`
    EntryCount    int    `json:"entry_count"`
}

func (m *Manager) Reindex(ctx context.Context) error {
    var meta *memoryMeta
    if v, _ := loadMemoryMeta(m.StateDir); v != nil {
        meta = v
    }

    // 一致性检查：DB meta 表 + 文件 meta 双向
    dbMeta := readDBMemoryMeta(m.DB)
    rebuildRequired := meta == nil ||
        meta.SchemaVersion != 1 ||
        meta.Tokenizer != "trigram" ||
        dbMeta == nil ||
        dbMeta.Tokenizer != "trigram"

    if !rebuildRequired {
        // FTS 行数 == status=created 行数？
        var ftsCount, rowCount int64
        m.DB.Raw(`SELECT COUNT(*) FROM memory_fts`).Scan(&ftsCount)
        m.DB.Model(&UserMemory{}).Where("status = ?", "created").Count(&rowCount)
        if ftsCount != rowCount { rebuildRequired = true }
    }

    if !rebuildRequired { return nil }

    if err := m.DB.Transaction(func(tx *gorm.DB) error {
        if err := tx.Exec(`DELETE FROM memory_fts`).Error; err != nil { return err }
        var rows []UserMemory
        if err := tx.Where("status = ?", "created").Find(&rows).Error; err != nil { return err }
        for _, r := range rows {
            if err := tx.Exec(`INSERT INTO memory_fts(memory_id, text) VALUES (?, ?)`,
                r.ID, r.Text).Error; err != nil { return err }
        }
        return writeDBMemoryMeta(tx, memoryMeta{SchemaVersion: 1, Tokenizer: "trigram",
            BuiltAt: time.Now().UnixMilli(), EntryCount: int(len(rows))})
    }); err != nil { return err }

    return writeMemoryMeta(m.StateDir, &memoryMeta{
        SchemaVersion: 1, Tokenizer: "trigram",
        BuiltAt: time.Now().UnixMilli(), EntryCount: -1,
    })
}
```

**启动期调用**：`main.go` 在 `memMgr.Migrate` 后 `go memMgr.Reindex(ctx)`，30s 超时，失败 warn 不阻塞。

## 8. Settings / Config

### 8.1 MemoryConfig（**字段扩充**，对齐 LobsterAI）

```go
// internal/config/config.go
type MemoryConfig struct {
    Enabled               bool   `mapstructure:"enabled"`                  // true
    ImplicitUpdateEnabled bool   `mapstructure:"implicit_update_enabled"` // true
    LLMJudgeEnabled       bool   `mapstructure:"llm_judge_enabled"`       // false
    GuardLevel            string `mapstructure:"guard_level"`             // "strict" | "standard" | "relaxed"
    UserMemoriesMaxItems  int    `mapstructure:"user_memories_max_items"` // 12, clamp [1,60]

    EmbeddingEnabled      bool   `mapstructure:"embedding_enabled"`        // false (v1)
    EmbeddingProvider     string `mapstructure:"embedding_provider"`       // "openai" | "local" | "gemini" | "voyage" | "mistral" | "ollama"
    EmbeddingModel        string `mapstructure:"embedding_model"`
    EmbeddingLocalModelPath string `mapstructure:"embedding_local_model_path"`
    EmbeddingVectorWeight float64 `mapstructure:"embedding_vector_weight"` // 0.7
    EmbeddingRemoteBaseURL string `mapstructure:"embedding_remote_base_url"`
    EmbeddingRemoteAPIKey  string `mapstructure:"embedding_remote_api_key"`
}
```

`applyDefaults` 在 `Load()` 末尾集中补：

```go
func applyDefaults(cfg *Config) {
    if cfg.Memory.GuardLevel == "" { cfg.Memory.GuardLevel = "strict" }
    if cfg.Memory.UserMemoriesMaxItems <= 0 { cfg.Memory.UserMemoriesMaxItems = 12 }
    else if cfg.Memory.UserMemoriesMaxItems > 60 { cfg.Memory.UserMemoriesMaxItems = 60 }
    if cfg.Memory.EmbeddingProvider == "" { cfg.Memory.EmbeddingProvider = "openai" }
    if cfg.Memory.EmbeddingVectorWeight == 0 { cfg.Memory.EmbeddingVectorWeight = 0.7 }
}
```

### 8.2 Settings UI 持久化修复

现状：`SettingsPanelMemory.vue` 只调 `setAppPreferences({ memory: { enabled, embeddingProvider, apiKey }})`。main 端 `setAppPreferences` 写 yaml 时 **也只关心这 3 个字段**。

修复：renderer 把所有 13 个字段打包 `setAppPreferences({ memory: {...} })`，main 端 `setAppPreferences` patch 透传到 `writeUserSettingsYAML({ memory: {...} })`（透传所有字段，不再 hardcode whitelist）。

```ts
// renderer
const allPrefs = {
  enabled, implicitUpdateEnabled, llmJudgeEnabled, guardLevel,
  userMemoriesMaxItems,
  embeddingEnabled, embeddingProvider, embeddingModel,
  embeddingLocalModelPath, embeddingVectorWeight,
  embeddingRemoteBaseUrl, embeddingRemoteApiKey,
};
await window.darvin.setAppPreferences({ memory: allPrefs });
```

```ts
// main/index.ts 现有 setAppPreferences handler 改：
async (_e, patch: DarvinAppPreferencesPatch): Promise<void> => {
  if (patch.autoLaunch !== undefined) { app.setLoginItemSettings({ openAtLogin: patch.autoLaunch }); }
  await writeUserSettingsYAML({
    app: { notifications: patch.notifications, proxy: patch.proxy },
    memory: patch.memory,  // 透传整个对象（已含全部字段）
  });
}
```

yaml 落盘格式：
```yaml
memory:
  enabled: true
  implicit_update_enabled: true
  llm_judge_enabled: false
  guard_level: strict
  user_memories_max_items: 12
  embedding_enabled: false
  embedding_provider: openai
  embedding_model: ''
  embedding_local_model_path: ''
  embedding_vector_weight: 0.7
  embedding_remote_base_url: ''
  embedding_remote_api_key: ''
```

### 8.3 配置生效路径

- 启动期：Go 端 `config.Load` → `cfg.Memory` → `memory.New(stateDir, db, cfg.Memory, log)`
- 改配置后：renderer → main → yaml → **Go 端必须重启才能重新读 yaml**（现状）

要让 UI 改完立即生效，需要：
- 新增 `darvin:reload_memory_config` IPC
- main 端把 `cfg.Memory` 引用换成可热替换的 `*config.MemoryConfig`，handler 收到 IPC 重新 `viper.Unmarshal` + 通知 Go 端 reload

**v1 scope**：只做到改完需要重启 Go agent，提示文案"重启后生效"。v2 再做热 reload。

## 9. Tool Integration

### 9.1 三个 memory_* 工具

```go
// internal/tools/memory.go
func RegisterMemoryTools(reg *Registry, mgr *memory.Manager) {
    if reg == nil || mgr == nil { return }
    reg.MustRegister(&memorySearchTool{mgr})
    reg.MustRegister(&memoryGetTool{mgr})
    reg.MustRegister(&memoryWriteTool{mgr})
}

type memorySearchTool struct{ mgr *memory.Manager }
func (t *memorySearchTool) Name() string { return "memory_search" }
func (t *memorySearchTool) Parameters() llm.ParameterSchema {
    return llm.ParameterSchema{
        Type: "object",
        Properties: map[string]llm.ParameterProperty{
            "query": {Type: "string", Description: "Free-form search query."},
            "limit": {Type: "integer", Minimum: ptrFloat64(1), Maximum: ptrFloat64(20), Default: 5},
        },
        Required: []string{"query"},
    }
}
func (t *memorySearchTool) Execute(ctx context.Context, args map[string]any) Result {
    if t.mgr == nil { return Result{IsError: true, Content: "memory subsystem disabled"} }
    q := args["query"].(string)
    limit := 5
    if v, ok := args["limit"].(float64); ok && v > 0 { limit = int(v) }
    hits, err := t.mgr.Search(ctx, q, limit)
    if err != nil { return Result{IsError: true, Content: err.Error()} }
    if len(hits) == 0 { return Result{Content: "no matching memories"} }
    body, _ := json.MarshalIndent(hits, "", "  ")
    return Result{Content: string(body)}
}

// memory_get / memory_write 类似
```

### 9.2 system prompt policy section（**新增 marker 触发词**，对齐 LobsterAI `MANAGED_MEMORY_POLICY_PROMPT`）

```go
// internal/memory/policy.go
func PolicySection() ctxengine.SystemSection {
    return ctxengine.SystemSection{
        Name:     "memory-policy",
        Priority: 50,
        Content: `## Memory

You have access to a personal MEMORY.md file and three memory tools: memory_search, memory_get, memory_write.

**Write before you confirm.** When the user expresses any intent to persist information — including phrases like "记住", "以后", "下次要", "remember this", "keep this in mind", "from now on", or similar — you MUST call the memory_write tool BEFORE replying that you have remembered it.

- Save to MEMORY.md (durable facts), grouped under '## {section}' (e.g. "preferences", "project:foo", "people").
- Each entry is a single self-contained sentence.
- Only say "记住了" / "I'll remember that" AFTER the memory_write call succeeds.
- Never give a verbal acknowledgment of remembering without a corresponding tool call.
- Mental notes do not survive session restarts. Files do.

Search first. Before answering from memory, run memory_search. Don't invent or repeat what's already stored.

Bootstrap files IDENTITY.md / USER.md / SOUL.md are read at session start and reflect standing instructions; treat their content as higher-priority than MEMORY.md entries.

Do not store: secrets, ephemeral task state, or anything obvious from the current conversation.`,
    }
}
```

main.go：
```go
if memMgr != nil && cfg.Agent.AssemblerEnabled {
    policySection := memory.PolicySection()
    if da, ok := steerAgent.Assembler().(*ctxengine.DefaultAssembler); ok {
        da.SetSections([]ctxengine.SystemSection{policySection})
        da.SetOnAfterTurn(func(ctx context.Context, p ctxengine.AfterTurnParams) error {
            autoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
            defer cancel()
            rows, err := msgStore.List(autoCtx, p.SessionID, 1000, 0)
            if err != nil { return err }
            msgs := make([]protocol.Message, 0, len(rows))
            for _, r := range rows {
                msgs = append(msgs, protocol.Message{Role: protocol.Role(r.Role), Content: r.Content})
            }
            return memMgr.AutoExtract(autoCtx, p.SessionID, msgs)
        })
    }
}
```

## 10. IPC 协议

`agent.*` 是 Go → renderer 的 RPC method 命名空间。本 spec 加 10 个新 method：

| method | 行为 |
|---|---|
| `memory.list_entries` | `{section?, status?, limit?, offset?} → {entries: [], total}` |
| `memory.create_entry` | `{text, section?, isExplicit?, confidence?, source?} → {entry, created, updated}` |
| `memory.update_entry` | `{id, text?, section?, confidence?, isExplicit?, status?} → {entry}` |
| `memory.delete_entry` | `{id} → {deleted}` |
| `memory.get_stats` | `{} → {stats}` |
| `memory.read_raw` | `{} → {content, path}` |
| `memory.write_raw` | `{content} → {written, entryCount}` |
| `memory.reindex` | `{} → {reindexed, entryCount}` |
| `bootstrap.read` | `{filename, agentId?} → {content}` |
| `bootstrap.write` | `{filename, content, agentId?} → {written, triggeredConfigSync}` |

参数 source 字段（新增）：`{sessionId?, messageId?, role?}`，Create 时附加到 `user_memory_sources`。

renderer IPC channel 命名 `darvin:list_memory_entries` 等 10 个，main 端直转对应 Go RPC。

## 11. 错误码

| 错误 | 返回 |
|---|---|
| Manager nil（子系统关闭） | `CodeInvalidParams` "memory disabled" |
| 路径非法 / 文件名不在白名单 | `CodeInvalidParams` "bootstrap filename invalid: …" |
| id 不存在 / 删除 0 行 | `CodeInternalError` "memory not found" |
| raw 内容 parse 后零 entry 且 renderer 没保存操作 | `CodeInvalidParams` "raw content has no entries"（可关） |

## 12. 涉及文件

| 文件 | 操作 |
|---|---|
| `src/darvin-agent/internal/memory/file.go` | 新建（block parser + CRUD） |
| `src/darvin-agent/internal/memory/file_test.go` | 新建 |
| `src/darvin-agent/internal/memory/bootstrap.go` | 新建（含 locale-aware IDENTITY.md） |
| `src/darvin-agent/internal/memory/bootstrap_test.go` | 新建 |
| `src/darvin-agent/internal/memory/db.go` | 新建（GORM models + FTS5 virtual table + 一致性写入） |
| `src/darvin-agent/internal/memory/db_test.go` | 新建 |
| `src/darvin-agent/internal/memory/manager.go` | 新建（facade + near-dup 合并） |
| `src/darvin-agent/internal/memory/manager_test.go` | 新建 |
| `src/darvin-agent/internal/memory/paths.go` | 新建（常量） |
| `src/darvin-agent/internal/memory/policy.go` | 新建（含 marker 触发词） |
| `src/darvin-agent/internal/tools/memory.go` | 新建 |
| `src/darvin-agent/internal/tools/memory_test.go` | 新建 |
| `src/darvin-agent/internal/config/config.go` | 修改（MemoryConfig 字段扩充 + applyDefaults） |
| `src/darvin-agent/config.yaml` | 修改（13 字段） |
| `src/darvin-agent/cmd/app/main.go` | 修改（memory.New + RegisterMemoryTools + SetSections + SetOnAfterTurn + HandlerOptions.Memory） |
| `src/darvin-agent/internal/gateway/handlers.go` | 修改（HandlerOptions.Memory + Handler.Memory） |
| `src/darvin-agent/internal/gateway/handlers_memory.go` | 新建（10 个 handler） |
| `src/darvin-agent/internal/gateway/handlers_memory_test.go` | 新建 |
| `src/shared/darvin-api.ts` | 修改（DarvinMemoryEntry/Stats + 10 个 DarvinApi method） |
| `src/main/index.ts` | 修改（10 个 ipcMain.handle + setAppPreferences 透传全部字段） |
| `src/main/runtime/client.ts` | 修改（client.memory.* / client.bootstrap.* namespace） |
| `src/preload/index.ts` | 修改（contextBridge 10 个方法） |
| `src/renderer/composables/useMemory.ts` | 新建（singleton composable） |
| `src/renderer/services/i18n.ts` | 修改（增 ~15 个 memory.* key） |

## 13. 验收标准

### Go 单测

- `parseMemoryMd` round-trip 字节级一致；top-bullet + indented children 合并到同一 entry
- `serializeEntryLines` 多行文本拆边界正确（空行 / 缩进补齐）
- `Fingerprint` CJK / 英文 / 混合文本稳定性
- `MEMORY.md` 代码块 / HTML comment 不被识别为 entry
- `choosePreferredMemoryText` 三种情况（current 好 / incoming 好 / 平局）
- `shouldAutoDeleteMemoryText` 5 类 negative case
- `MEMORY_PROCEDURAL_TEXT_RE` 8 个命令正 / 负样本
- FTS5 trigram 命中中文 / 英文（`memory_fts MATCH '燕麦'` 返回命中）
- `createOrReviveUserMemory` near-dup 合并（同 fingerprint / 0.82 bigram-dice / 不合并）
- `enforceCapacity` 触顶后 oldest → stale，FTS 同步删
- `migrateSqliteToMemoryMd` 幂等（第二次不重复）
- `migrateMainAgentWorkspace` 5 类 file + AGENTS.md 用户区
- `Reindex` meta mismatch → 全量 rebuild → 写 meta
- `WriteAtomic` 首次备份 `.bak`，后续不覆盖
- gateway `memory.*` 10 handler + nil safety
- `WriteRaw` → parse → SQLite 对齐（删除消失的、新增未在 DB 的）

### 手工 smoke

1. `npm run build:agent && npm start`
2. Settings → Memory → toggle `enabled = true`
3. chat 输入"请记住：我喝燕麦奶" → 模型答完后 Settings → Memory → entries 列表新增一条 `## auto`
4. `cat ~/.config/darvin-cowork/darvin-agent/state/workspace-main/MEMORY.md` 含该 entry
5. kill 重启，entry 仍在
6. Settings → Memory → Raw view 切换 → 编辑一行 → Save → 重开 Raw 验证，`.bak` 文件存在
7. Settings → Memory → Bootstrap → IDENTITY.md → 编辑 → Save → 文件落盘
8. FTS smoke：chat "我喝什么奶？" → 模型调 `memory_search` 返回"燕麦奶"
9. `sqlite3 memories.sqlite "SELECT * FROM memory_fts WHERE memory_fts MATCH '燕麦'"` 返回命中
10. `INSERT INTO memory_memories(text)` → `reindex` → FTS 行数 = row 数

### Playwright UI

- Open Settings → Memory (`data-testid="settings-memory"`)
- 切换 entries / embedding / dreaming 三个 tab
- 增 / 删 / 改 entry，断言列表更新
- raw view save 后重启 IPC 验证持久化