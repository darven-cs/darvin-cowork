# 上下文拼接规范 — Reasonix 式 digest 链 + OpenClaw 分层记忆 + messages 表做 UI 展示

> 依赖既有 spec:
> - [`specs/features/memory-subsystem/README.md`](../memory-subsystem/README.md) + [`P3-2026-08-04-memory-core-design.md`](../commercialization-roadmap/P3-2026-08-04-memory-core-design.md)(MEMORY.md / IDENTITY.md / USER.md / SOUL.md 文件结构、FTS5、Manager facade、PolicySection)
> - [`specs/features/agent-context-engine/2026-07-29-agent-context-engine-design.md`](../agent-context-engine/2026-07-29-agent-context-engine-design.md)(Sections / Priority / Assemble pipeline)

---

## 1. 概述

### 1.1 问题 / 背景

完整的「LLM 上下文是什么」在当前实现里由两块拼起来:

```
LLM 上下文 = 系统层 + 工具层 + 消息层
```

但当前实现只拼了**消息层**(基本落库 + 全量回放)和**系统层基线**(`Instructions()` 字符串 + 临时 imported note),**临近层(block-wise 分层记忆 + 持久化压缩产物)全缺**。具体三处真 gap:

1. **`<IDENTITY> / <SOUL> / <USER> / <MEMORY>` 块从来没进到 system 层**。`memory.PolicySection()` 把「如何用 memory」摆在 priority=50,但真正的事实块(SOUL/USER/MEMORY 的内容)从 v1 至今没注入。`ctxengine.AvailableFacts` 永远是 nil。

2. **压缩产物没有持久化,自动压缩基本失效**。当前 `compact.go` 触发自动压缩后,
   - `assemble.go:66` 把 `compactRes.RetainedMessages` 替换了 `assembled.Messages`(只活在 in-memory)
   - `executor.go:200` 用 `assembled.Messages` 发起 LLM 调用 — 这一 turn 看到了压缩版
   - `executor.go:244` 紧接着 `d.Session().Append(assistant)` 把 LLM 输出 **append** 进来
   - `assembled.Messages` 的 `RetainedMessages` **从未**写回 `Session.Messages`,也**从未**落到任何持久化表
   - 下次 turn `d.Session().Messages()` 仍返回全量历史,`assemble.go:57` 重新判 budget 超 → 又全量过 summarizer → 每个 turn 跑一次 LLM 摘要(慢、贵)但每次的摘要立刻被丢
   - `compaction_checkpoints` 表存在,但**没人写**它(grep 0 命中)
   - 唯一持久化路径是 `gateway/handlers.go:540` 的手动压缩 `a.Session().ReplaceAll(res.RetainedMessages)` — 也只写内存,Session 销毁 / 重启就丢

3. **`req.System` 漏接 `assembled.SystemAddition`**。`assemble.go:92` 已经计算并填 `AssembleResult.SystemAddition`(`composeSystemAddition` 产物: builtin sections + caller sections + cfg.SystemPromptAddition,3 个单测都验证正确),但 `executor.go:200` `System: d.Instructions()` **完全无视**这个字段。本该发给 LLM 的 `<available_skills> / <available_mcp>` 块 + `SystemPromptAddition` 全部丢失。

**两表分离的语义问题**:历史上 digest 会被写到 `messages` 表里 `Role=assistant, Content="[Conversation Summary]\n..."` 的 row,造成三个问题:
1. UI `get_messages` 把摘要当普通助手气泡渲染,污染对话视图
2. `protocol.Message.Role` 类型被滥用:压缩产物 ≠ 助手回复
3. hydrate 必须靠 `Content` 前缀 `strings.HasPrefix("[Conversation Summary]")` 反查 digest,DB 索引 / grep 易踩坑

本 spec 把 digest **完全从 `messages` 表剥离**,落到独立表 `session_digests`,两表分离后语义 / UI / 检索全部干净。**`messages` 表只存 UI 真实交互消息(`user / assistant / tool / system`),`session_digests` 表只存压缩摘要。** 详细设计见 §3 FR-2 / FR-5 / FR-6。

**bootstrap 穿透设计意图**:bootstrap 缓存必须由 `runtime.WorkspaceBootstrap`(workspace 级单例)持有,`ctxengine.Deps.MemoryBootstrap(name)` 仅作穿透转发。**实现风险**:Agent.MemoryBootstrap 名义上是"穿透",但若误实现为 `a.memory.ReadBootstrap(name)` 直接读盘就绕过 WorkspaceBootstrap,失效机制完全作废,写时缓存永远不刷新。代码片段与文档表层一致、行为层背离,review 容易漏。完整约束见 §3 FR-9 / §4.3.3 / §8 决策 #14。

与此同时,`specs/features/memory-subsystem/` 已经画好三层记忆的 source of truth:
- `IDENTITY.md` / `SOUL.md` / `USER.md` — bootstrap 文件,白名单(`internal/memory/bootstrap.go`)
- `MEMORY.md` — block-aware 解析 + FTS5 索引(`internal/memory/file.go` + `db.go`)
- `memories.sqlite` — `user_memories` / `memory_fts`(trigram)/ `memory_index_meta_v1`

memory-core spec 已经规划 `Manager.ReadBootstrap(name)` 和 `Manager.Search(ctx, q, limit)`,但**还没有「什么时候塞进 ctxengine」的契约**。

### 1.2 参考项目做法

| 项目 | 上下文如何分层 |
|---|---|
| **Reasonix**(`internal/agent/compact.go`) | `[pinnedPrefix + 旧 digests 原样 + 新 digest + tail]`,**没有任何 system 层记忆**(digest 链即历史) |
| **LobsterAI**(`docs/openclaw-main/packages/agent-core/.../compaction.ts` + `agent-session-compaction.ts`) | `[系统 prompt + OpenClaw 段落(SOUL/USER/MEMORY 检索)+ 压缩 DAG 重建的 session]` |
| **OpenClaw**(`packages/cowork/src/lib/openclawMemoryFile.ts` 等) | **system 层**有显式分层:`<IDENTITY>` / `<SOUL>` / `<USER>` / `<MEMORY>` / `<available_skills>` / `<available_mcp>`,每层独立 `<tag>…</tag>` 包裹,层与层之间 `\n\n` |

本 spec = **OpenClaw 的 system 层分层 + Reasonix 的消息层 digest 链**:
- system 层负责「持久化的『我是谁 / 用户是谁 / 我该知道什么 / 我能调什么』」,refresh 频率 = 每次 Assemble(必要时)/ session 启动。
- 消息层负责「本会话历史」,refresh 频率 = 每次 Compact。

### 1.3 目标

1. **system 层完整化**:LLM 看到的 system prompt 由明确的 8 块拼成:`Instructions()` + `<IDENTITY>`(p30) + `<SOUL>`(p40) + `<memory-policy>`(p50) + `<USER>`(p60) + `<MEMORY>`(p110) + `<available_skills>`(p100) + `<available_mcp>`(p120) + `addition`(p1000),按 priority 升序排。
2. **消息层持久化**:Reasonix digest 累积 + `session_digests` 表(合并 `compaction_checkpoints`)+ hydrate 优先读 cp(两表分离版)+ messages 表只做 UI。
3. **Executor 修复 `req.System` 接回 `assembled.SystemAddition`**(这条 gap 当前实现里有,源码已确认)。
4. **自动压缩跨 turn 持久化**:`a.Session().ReplaceAll(assembled.Messages)` + `a.PersistCompaction` 落库,否则自动压缩形同虚设。
5. **边界清晰**:system 层 ≠ 消息层。system 层记忆走 `memory.Manager`;消息层历史走 `MessageStore` + `DigestStore`,互相不串。
6. **保持零新外部依赖**:复用 `memory.Manager` / `memory_fts`(已在 memory-core spec 落地)。
7. **可降级**:memory subsystem 未启用 / bootstrap 文件不存在 → system 层降级为只剩 `Instructions()` + 已有 skills/mcp/policy,不报错。

### 1.4 非目标

- 不改 `memory.PolicySection()`(memory-core §9.2 已定 priority=50)。
- 不改 `ctxengine.SystemSection` 类型 / Priority 数字基础机制(agent-context-engine §FR-5 已落地)。
- 不做 Embedding 检索(沿用 FTS5 trigram,memory-core §1.2 决策)。
- 不动 DAG / SubAgent(沿用 ErrSubAgentUnsupported)。
- 不改 IDENTITY.md / SOUL.md / USER.md 文件格式(沿用 memory-core §3)。
- 不重写 session.Session 状态机(继续 append-only)。
- 不动 `memory-core` 已经规划但本 spec 不复用的部分(Dreaming / embedding / auto-extract)。

---

## 2. 用户场景

### 场景 1:长会话手动压缩后重启,系统层记忆仍在

**Given** 长会话,用户曾输入「我司数据库用 Postgres 16,我喝燕麦奶」,MEMORY.md 已落库多条 entry;手动压成功
**When** 重启应用,切回该会话提问「我喝什么奶?」
**Then**
- agent 的 system prompt 包含:
  - `<USER>` 块(bootstrap.USER.md 用户编辑过)
  - `<MEMORY>` 块(MEMORY.md FTS 命中「燕麦奶」那条)
- agent 的 messages 数组是 `[旧 digest1 + 旧 digest2 + 新 digest + tail]`,不是完整历史
- UI `get_messages` 仍返回完整历史(被压原始消息没删)
- 模型答「燕麦奶」

### 场景 2:自动压缩后切会话再切回,系统层 + 消息层都不膨胀

**Given** 会话超预算触发自动压缩,新 digest + `session_digests` 行落库;`Session.Messages` 被替换为 `[digests + tail]`
**When** 切到别的会话再切回
**Then**
- hydrate 读到 checkpoint,加载压缩后的 messages(同本 spec 描述)
- 重新组装 system 层时,IDENTITY/SOUL/USER bootstrap 仍然命中(MEMORY FTS 仍命中最近关键词)
- 上下文占用不膨胀(消息层压了;system 层受 cap 上限)

### 场景 3:历史记录仍完整

**Given** 会话经过多次压缩
**When** UI 拉 `get_messages`
**Then** 完整对话记录仍可见(被压消息没删,只不进 LLM 上下文)

### 场景 4:全新会话首条 prompt,系统层全部到位

**Given** 全新 session,无历史
**When** 用户首条 prompt
**Then** LLM 的 system prompt 块齐全(`Instructions()` + `<IDENTITY>` + `<SOUL>` + `<memory-policy>` + `<USER>` + `<available_skills>` + `<available_mcp>` + `addition`),`<MEMORY>` 是否挂由 FTS hits 决定

### 场景 5:memory subsystem 关闭 / 不可用

**Given** `cfg.Memory.Enabled = false` 或 bootstrap 文件缺失
**When** 任何 prompt
**Then**
- IDENTITY/SOUL/USER 段为空字符串(不输出「<none registered>」stub,因为本来就不该误导用户)
- `<MEMORY>` 块直接不挂(`BuiltInSections` 在 `len(facts) == 0` 时省略)
- 不报错,不影响其他段

---

## 3. 功能需求

### FR-1:`protocol.Message` 增加持久化边界字段

`protocol.Message{ID string, Timestamp int64}`,dispatcher `Append` 时填,hydrate / 压缩切窗用。

### FR-2:`session_digests` 表承载累积 digest(合并 `compaction_checkpoints`)

`session_digests` 承担所有压缩产物:每一条 digest 是「一次压缩的摘要 + 保留尾部起点 + 累积序列号」。latest sequence 那一行的 `first_kept_id` / `first_kept_ts` 就是当前会话的保留尾部起点。**`compaction_checkpoints` 表(现有)合并到 `session_digests` 移除,latest sequence 即当前 checkpoint**。

```sql
CREATE TABLE session_digests (
    id                TEXT    PRIMARY KEY,         -- "digest-<checkpointID>"
    session_id        TEXT    NOT NULL,
    sequence          INTEGER NOT NULL,             -- 累积序号 1,2,3, ... (同 session_id 单调递增)
    summary           TEXT    NOT NULL,             -- LLM 生成的摘要原文
    tokens_before     INTEGER NOT NULL,
    tokens_after      INTEGER NOT NULL,
    first_kept_id     TEXT,                         -- 本次压的保留尾部起点(MessageRecord.ID)
    first_kept_ts     INTEGER,                      -- unix ms;first_kept_id 失配时的 fallback 边界
    compact_reason    TEXT    NOT NULL,             -- "budget_exceeded" | "manual" | "steer_triggered"
    source_compact_id TEXT    NOT NULL,             -- 关联 ctxengine.CheckPoint.ID
    created_at        INTEGER NOT NULL,             -- unix ms
    UNIQUE(session_id, sequence)
);
CREATE INDEX session_digests_session_idx ON session_digests(session_id, sequence);
```

`DigestStore` 接口(同 session_id 的 sequence 分配由 store 内部串行化,见 FR-2.1):

```go
// internal/agents/store/digest_store.go
type DigestStore interface {
    // Save 持久化一条新 digest。d.Sequence 若为 0,store 内部用 nextSequence
    // 串行分配(same-session per-session mutex + DB max(seq)+1 缓存);d.Sequence
    // 非 0 时直接落库(供测试 / 修复场景使用)。同 (session_id, sequence) 冲突由
    // SQLite UNIQUE 约束兜底,内部 retry 一次;仍冲突则清 cache + 返 error。
    Save(ctx context.Context, d *SessionDigest) error

    // List 返该 session 全部累积 digest,按 sequence asc。供 hydrate / 审计 / 调试用。
    // 空 slice 表示该 session 从未压缩过。
    List(ctx context.Context, sessionID string) ([]SessionDigest, error)

    // Latest 返 sequence 最大那一行(即当前 checkpoint);无返 nil。
    Latest(ctx context.Context, sessionID string) (*SessionDigest, error)

    // DeleteBySession 删除该 session 全部 digest;同时清空 store 内部
    // 的 sequenceAlloc cache。handleDeleteSession 级联调。
    DeleteBySession(ctx context.Context, sessionID string) error
}
```

### FR-2.1:sequence 分配并发安全

**问题**:`Agent.PersistCompaction` 调 `Latest` 拿 max + 1 → 调 `Save` 的两步非原子路径,极端场景:
- 手动压与自动压同时触发(用户在自动压进行时点了手动压按钮)
- 同 session 多个并发 Assemble 触发 `budget_exceeded`(理论不该发生,但同一 session 出现并发 Assemble 是 ctxengine 设计上的可能)
- 进程崩溃重启后 first-call vs 在 flight goroutine 都调 Save

三个并发 goroutine 各自读 `Latest → N`、各自返回 `N+1`、各自 `Save(N+1)` → 2 个被 UNIQUE 约束挡掉 → 不优雅。

**修复**:进程内 per-session mutex 串行化 sequence 分配 + DB cache + UNIQUE 兜底 + retry 一次:

```go
// internal/agents/store/sqlite_digest_store.go
type SQLiteDigestStore struct {
    db *gorm.DB

    // seqAllocs 保护 map 自身的并发写。
    seqAllocs sync.Mutex
    // sessionID → 该 session 的 seqMu + cache;lazy 创建,DeleteBySession 移除。
    seqAlloc map[string]*seqAlloc
}

type seqAlloc struct {
    mu    sync.Mutex                  // per-session 串行化分配
    value int                          // 已确认的最大 sequence
}

func (s *SQLiteDigestStore) nextSequence(ctx context.Context, sessionID string) (int, error) {
    a := s.getOrCreateAlloc(sessionID)
    a.mu.Lock()
    defer a.mu.Unlock()

    if a.value > 0 {
        n := a.value + 1
        a.value = n
        return n, nil
    }

    // cache miss:从 DB 读 max(seq),初始化 cache
    var maxSeq int
    if err := s.db.WithContext(ctx).
        Model(&SessionDigest{}).
        Where("session_id = ?", sessionID).
        Select("COALESCE(MAX(sequence), 0)").
        Scan(&maxSeq).Error; err != nil {
        return 0, err
    }
    a.value = maxSeq
    return maxSeq + 1, nil
}

func (s *SQLiteDigestStore) Save(ctx context.Context, d *SessionDigest) error {
    if d == nil { return errors.New("store: nil SessionDigest") }
    if d.SessionID == "" { return errors.New("store: SessionID required") }
    if d.ID == "" { return errors.New("store: ID required") }

    if d.Sequence <= 0 {
        seq, err := s.nextSequence(ctx, d.SessionID)
        if err != nil { return err }
        d.Sequence = seq
    }

    err := s.db.WithContext(ctx).Save(d).Error
    if err != nil && isUniqueConstraintErr(err) {
        // UNIQUE 冲突:cache 与 DB 不一致,清 cache 后 retry 一次
        s.invalidateAlloc(d.SessionID)
        d.Sequence = 0   // 让 nextSequence 重新分配
        seq, seqErr := s.nextSequence(ctx, d.SessionID)
        if seqErr != nil { return seqErr }
        d.Sequence = seq
        err = s.db.WithContext(ctx).Save(d).Error
    }
    if err != nil {
        return err
    }
    return nil
}

func (s *SQLiteDigestStore) DeleteBySession(ctx context.Context, sessionID string) error {
    if sessionID == "" { return errors.New("store: sessionID required") }
    if err := s.db.WithContext(ctx).
        Where("session_id = ?", sessionID).
        Delete(&SessionDigest{}).Error; err != nil {
        return err
    }
    s.invalidateAlloc(sessionID)
    return nil
}
```

**保证**:
1. 同 session 并发 `Save` 走 per-session mutex 串行,sequence 不会重复(常规路径)
2. 跨 session 并发 `Save` 不阻塞(不同 sessionID 各持独立 mutex)
3. UNIQUE 约束二次兜底:cache 与 DB 短暂不一致时(可能由 retry 内部 race 或外部修复工具写库)Save 失败 → 内部 retry 一次;仍冲突 → 清 cache + 返 error
4. `DeleteBySession` 清 cache,下次 `Save` 重新读 DB

**关于 `seqAlloc` 内存累积**:只能 `DeleteBySession` 删除,长寿命进程下 `seqAlloc` 入口会随 session 数线性增长。**添加 size cap**:超过 `seqAllocMaxEntries` (默认 1000) 时,清空整张 map(下次访问重新读 DB);这是 acceptance window — 磁盘 DB 永远是真值,清空仅丢内存 cache,后续 session save 重新 lazy 加载,不丢数据。

**为什么不用 SQLite `AUTOINCREMENT`**:业务 sequence 是 per-session 累加(同 session 从 1 开始),不是全局;`AUTOINCREMENT` 是全局分配,不满足业务语义。

**为什么不用 SQLite 事务 + `MAX(sequence) FOR UPDATE`**:SQLite 没有真正的行锁(只是 reserved lock);多个写事务并发 `INSERT` 仍可能拿到相同的 max+1。事务 + per-session mutex + UNIQUE 兜底才是稳态。

### FR-3:Compact 切窗改 token 预算 + 输出 firstKept 边界

`CompactTailTokens` 默认 20000,`CompactTailKeep` 作下限;`CompactResult.FirstKeptID / FirstKeptTimestamp` 透出。

### FR-4:Compact 采用 Reasonix digest 累积

`partitionFold / pinnedPrefixLen / isCompactionSummary`:
- `kept = isCompactionSummary(m) + pinnableUserTurn(m)` 保留
- `fold = 其余消息` 摘要
- 重组 `[pinnedPrefix] + [kept] + [newDigest] + [tail]`,旧 digest 不二次折叠

**关键修正**:当前 `compact.go` 直接 `summarizer.Summarize(span)` 把所有 tail 之外消息(含旧 digest)送进 LLM 摘要,导致反复压缩下旧 digest 文本被反复重写 — 原始信息可能丢失。本 spec 强制 `partitionFold` 识别旧 digest 不进 fold,保留为 `kept`。

**tool_use / tool_result 原子约束(Reasonix 默认语义)**:`Compact` 必须保证 `assistant{ToolCalls}` 与紧随其后的 `tool{ToolCallID ∈ ToolCalls.ID}` 不被切到不同的桶 / 不同的边界 — 即 assistant 的 tool_use 块和对应的 tool_result 块在最终进 LLM 的 wire format 里**始终成对出现**。

- **为什么**:Anthropic wire format 要求每个 `tool_result.tool_use_id` 在紧邻上一条 assistant 消息的 `tool_use` 块里能找到(`llm/anthropic/convert.go:convertMessages` 注释已记录该硬约束)。一旦切对,LLM API 返 `400 invalid_request_error: unexpected tool_use_id found in tool_result blocks`,整个 turn 废掉。OpenAI 同样要求 tool_call.id 与后续 tool message 对齐,虽然报错措辞不同。
- **历史 bug**:2026-08 用户报告长会话自动压后 API 400,根因 `partitionFold` 逐条消息独立判断 fold/kept,tail 切窗(`p.Messages[len-tail:]`)从一对 `[assistant.tool_use, tool_result]` 中间切过 — tool_use 被 LLM 摘要吞掉、tool_result 留在 tail,wire format 失败。Reasonix 主仓(`internal/agent/compact.go`)默认保持 pair 原子;本 spec 抄结构时漏了这条不变量。
- **两层防御**:
  1. **`partitionFold` pair-aware**:以 `pairAwareGroupSize(msgs, i)` 为步长迭代 — 遇到 assistant.ToolCalls 时,把紧随其后的全部 `tool{ToolCallID ∈ ToolCalls.ID}` 归到同一组(同进 fold 或同进 kept)。保证 fold/kept 内部 pair 不被切。
  2. **`alignTailBoundary(msgs, requestedTail)`**:tail 切窗可能从 pair 中间切过(`msgs[len-tail]` 落在 tool 消息或 assistant 上),在算出 requestedTail 后调一次,做两端对齐:
     - **start-cut**(tail[0] 是 tool):向前回溯找到配对的 assistant.tool_use,把 start 拉到 assistant 位置,tail 变长;
     - **end-cut**(tail[-1] 是带未配对 ToolCalls 的 assistant):若该 assistant 的 tool_use_id 集合不在 `[start, end)` 内都有对应 tool 消息,end 向后收缩。
  3. **不变量**:`RetainedMessages` 中每条 `tool` 消息的 `tool_use_id` 都在紧邻前一条 assistant 的 `ToolCalls[].ID` 里;反之每条 assistant 的 `ToolCalls[].ID` 在紧邻后若干条 tool 消息里有完整配对。
- **retry 路径边界**:`Compact` 的 retry 循环里 `fold = fold[:half]` 也会切 group — 当前由 pair-aware group 保证 fold 内部的 pair 不被切(group 不会跨 group 边界被 `[:half]` 劈开,因 fold 本身就是 group 列表);fold 之外被 `[half, len)` 切掉的部分不进 LLM,丢失信息但不破坏 wire format。本 spec 范围内不补这一条,作为后续 refine。

### FR-5:压缩产物只写 `session_digests`(不污染 messages 表)

`messages` 表**只存 UI 真实交互消息**(`Role=user / assistant / tool / system`),digest **彻底不写**到这里。压缩产物全部落 `session_digests`:

```
Agent.PersistCompaction(res):
  newDigest := &store.SessionDigest{
    ID:                "digest-" + res.Checkpoint.ID,
    SessionID:         a.session.ID,
    // Sequence = 0 → DigestStore.Save 内部用 FR-2.1 的 nextSequence 串行分配
    Summary:           res.Summary,
    TokensBefore:      res.TokensBefore,
    TokensAfter:       res.TokensAfter,
    FirstKeptID:       res.FirstKeptID,
    FirstKeptTimestamp: res.FirstKeptTimestamp,
    CompactReason:     res.Reason,
    SourceCompactID:   res.Checkpoint.ID,
    CreatedAt:         time.Now().UnixMilli(),
  }
  digestStore.Save(ctx, newDigest)        ← 单一事务;sequence 在 Save 内原子分配
  // 不再调 msgStore.Save
```

**sequence 由 store 分配,不在 caller 算**:`Agent` 不再 `Latest().Sequence + 1` — 把 sequence 留给 `DigestStore.nextSequence` 串行处理(FR-2.1)。这避免:
1. caller 算 seq 与 store 实际分配的 race(`Latest` 读 DB → `Save` 写 DB 之间窗口期被并发抢占)
2. caller 与 store 双重事实源,易混

`Sequence > 0` 时(store 仍允许):直接落库,跳过 `nextSequence` —— 供测试 / 数据修复场景使用;**生产路径不调用**。

**触发路径(语义沿用,落库目标改成只写 `session_digests`)**:
- 手动:`runManualCompact` 成功后 → `a.PersistCompaction(res)`
- 自动:`executor.RunConversation` 检查 `assembled.Stats.CompactionTriggered` → `a.Session().ReplaceAll(assembled.Messages)`(内存替换)+ `a.PersistCompaction(ctx, ctxengine.CompactResult{...})` → `Agent.PersistCompaction` 同上

### FR-6:hydrate 拼 `[digests...] + [firstKept 之后的 messages]`

`MessageStore.List` 与 `DigestStore.List` 各拉各的,职责清晰:

```
1. MessageStore.List(sessionID, 0, 0) → messages (纯 UI 历史,不含 digest;仍是事实源)
2. DigestStore.List(sessionID) → digests (sequence asc;可能 N 条;空表示没压过)
3. 若 digests 非空,latest 一行给 firstKept 边界:
       boundaryID = latest.FirstKeptID
       boundaryTS = latest.FirstKeptTimestamp
4. tail = messages 按 (boundaryID / boundaryTS) 切:
       for i, m in messages:
           if (boundaryID != "" && m.ID == boundaryID) ||
              (boundaryID == "" && boundaryTS > 0 && m.Timestamp >= boundaryTS):
               tail = messages[i:]
               break
       失配 → tail = messages(全部) ← 安全降级
5. Session.Messages = [digest1, digest2, ..., digestN] + tail
       每条 digest 在 Session 里渲染为 protocol.Message{
           Role: RoleAssistant,
           Content: "[Conversation Summary]\n" + d.Summary + "\n\n(Compacted at ...)",
           Timestamp: d.CreatedAt,
       }
       ——digest 不进 messages 表,但进 Session 内存(LLM 上下文)
```

**方案对比**:旧版本 hydrate 从 `messages` 行里 `strings.HasPrefix("[Conversation Summary]")` 反查 digest;本 spec 直接从独立表读,无字符串嗅探,DB 索引 / grep 都干净。

**UI `get_messages` 永不返 digest**:handler 只查 `MessageStore.List`(纯 messages),渲染器拿到的就是纯历史。digest 信息需要审计 / 调试时另走 `digest.list_by_session` 内部 IPC(本 spec 不实现,留作后续)。

### FR-7:system 层分段注入(`system prompt + SOUL.md + USER.md + MEMORY.md + …`)

LLM 看到的 system prompt 由以下块按 priority 升序拼出:

| Priority | 块名 | 来源 | 加载时机 |
|---|---|---|---|
| 30 | `<IDENTITY>` | `WorkspaceBootstrap.Get("IDENTITY.md")` | **workspace / 进程级一次性加载,所有 session 共享同一缓存**(见 FR-9) |
| 40 | `<SOUL>` | `WorkspaceBootstrap.Get("SOUL.md")` | 同上 |
| 50 | `<memory-policy>` | `memory.PolicySection()`(memory-core §9.2 已定) | 注册时一次性 |
| 60 | `<USER>` | `WorkspaceBootstrap.Get("USER.md")` | 同 IDENTITY/SOUL |
| 100 | `<available_skills>` | `agent.SkillSummaries()` | 每次 Assemble 实时查 `SkillRegistry.ListEnabled()` |
| 110 | `<MEMORY>` | `memory.Search(ctx, query, limit)` | 每次 Assemble 实时查 FTS(per `(sessionID, query)` TTL 缓存,见 FR-8) |
| 120 | `<available_mcp>` | `agent.McpServers()` | 每次 Assemble 实时查 `mcp.Registry.ListServers()` |
| 1000 | `addition` | `cfg.SystemPromptAddition` | 启动一次性 |

> IDENTITY/SOUL/USER 是 **workspace 级全局文件**(一份,所有 session 共用),不是会话级数据 — 不该 per-session bootstrap 各读一份。`MEMORY` 块 (`<MEMORY>`) 走 FTS 是 per-session 的 query(从最近 user msg 触发),但命中的 facts 本身来自 workspace 级 MEMORY.md / memories.sqlite。**`available_skills` / `available_mcp` 沿用 `BuiltInSections`;IDENTITY/SOUL/USER/MEMORY 在新组件 `runtime.WorkspaceBootstrap` + `ctxengine.memory_layers.go` 里挂。**

### FR-8:`<MEMORY>` 块按最近用户消息触发 FTS 检索

每次 `Assemble` 时:
- `query` 取最近 `N=3` 条 `RoleUser` 消息拼接(降级:全为空则 `query = ""`)
- `memMgr.Search(ctx, query, limit)` 取 top-N(`cfg.MemoryFactsLimit`,默认 `5`,clamp `[1,20]`)
- 渲染为 `<MEMORY>\n- (preferences) 用户喝燕麦奶\n- (project:foo) DB 是 Postgres 16\n…\n</MEMORY>`(沿用 `renderAvailableFactsSection`)
- 空结果:沿用现有 `BuiltInSections` 的 `len(facts) == 0` 短路,**不**挂 `<MEMORY>` 块

**TTL cache key 必须含 query**:见 §4.2 `memory_layers.go` 描述,不能只看 sessionID。query 变了要 invalidate 该 entry。

> 复用 `ctxengine.sections.go` 的 `renderAvailableFactsSection`,**不**新建 `<memory>` 私有 renderer。

### FR-9:`bootstrap` 段(IDENTITY/SOUL/USER)在 workspace 级一次性加载,所有 session 共享

IDENTITY/SOUL/USER 是 workspace 级全局文件(与 workspace root 一一对应),**不是**会话级数据 — 所有 session 看到的内容应当一致。设计如下:

**新组件 `runtime.WorkspaceBootstrap`**(workspace 级单例,挂在 runtime 上,不属于任何 session):

```go
// internal/runtime/workspace_bootstrap.go
type WorkspaceBootstrap struct {
    mu      sync.RWMutex
    content map[string]string                  // name → 内容;缺 / 读失败留 ""
    memMgr  *memory.Manager                     // 提供底层 ReadBootstrap
    log     *zap.Logger
}

func NewWorkspaceBootstrap(memMgr *memory.Manager, log *zap.Logger) *WorkspaceBootstrap {
    wb := &WorkspaceBootstrap{
        content: map[string]string{},
        memMgr:  memMgr,
        log:     log,
    }
    wb.RefreshAll(context.Background())          // 启动期一次性拉三个文件
    return wb
}

func (wb *WorkspaceBootstrap) Get(name string) string {
    wb.mu.RLock()
    defer wb.mu.RUnlock()
    return wb.content[name]                      // 缺返 ""
}

func (wb *WorkspaceBootstrap) Invalidate(name string) { wb.mu.Lock(); delete(wb.content, name); wb.mu.Unlock() }
func (wb *WorkspaceBootstrap) RefreshAll(ctx context.Context) {
    if wb.memMgr == nil { return }
    next := map[string]string{}
    for _, name := range []string{"IDENTITY.md", "SOUL.md", "USER.md"} {
        if s, err := wb.memMgr.ReadBootstrap(ctx, name); err == nil {
            next[name] = s
        } else {
            next[name] = ""                    // 缺失 / 错误 = 空字符串 → 不挂段
        }
    }
    wb.mu.Lock(); wb.content = next; wb.mu.Unlock()
}
```

**与 session 的关系**:
- `runtime.Build` 时构造一次 `WorkspaceBootstrap`(`RefreshAll` 同步拉三文件),挂到 runtime
- `AgentFactory.NewAgentLoopSession` 不再触发 ReadBootstrap
- `ctxengine.Deps.MemoryBootstrap(name)` 代理到 `WorkspaceBootstrap.Get(name)`(所有 session 共用同一份内容)

**bootstrap 文件缺失 / 读错误**:`content[name] = ""` → `BuildSystemSections` 跳过该段(等同 FR-7 降级)。

### FR-10:bootstrap 文件 write 触发 workspace 级缓存失效

新增信号路径 `memMgr → WorkspaceBootstrap`(进程级,影响所有 session):

```
memory.Manager.WriteBootstrap(name, content)
  → 写盘  → memMgr.onBootstrapChanged(name) callback (slice 注册)
  → runtime.WorkspaceBootstrap.Invalidate(name)   ← 进程级单例
  → 下次任何 session 的 Assemble 走 WorkspaceBootstrap.Get(name) → cache miss
  → ctxengine.Deps.MemoryBootstrap(name) 内部 lazy-reload:见 ctxengine 端实现
```

**ctxengine 端简化**:`ctxengine.Deps.MemoryBootstrap(name)` 不再带内部 cache(per-session 缓存是反模式 — IDENTITY/SOUL/USER 是 workspace 文件,所有 session 必须看到同一份内容);改成纯转发到 `WorkspaceBootstrap.Get(name)`,由 `WorkspaceBootstrap` 单一来源负责缓存与失效。`InvalidateBootstrapCache(name)` 在 ctxengine 侧**删除**。

> **★ 穿透实现的严格定义(防"穿透"变"重读"bug)**:`Agent.MemoryBootstrap` 的**唯一正确实现**是 `return a.workspaceBootstrap.Get(name)` 一行,**禁止**:
> - ❌ `return a.memory.ReadBootstrap(name)` —— 直接读盘绕过 WorkspaceBootstrap,失效机制作废
> - ❌ `if cache := a.bootstrapCache[name]; cache != "" {...}` —— per-session 缓存,与 workspace 单例矛盾
> - ❌ `a.workspaceBootstrap.Refresh(name)` —— Refresh 是 RefreshAll 的子操作,在 Invalidate 后第一次 Get 时 lazy 触发,不需 caller 显式调
>
> 任何"为了简洁 / 性能 / 测试"的"先 ReadBootstrap 再 Get"半透半绕实现都是错的。穿透 = `a.workspaceBootstrap.Get(name)` 一行,**不多不少**。

**回调生命周期纪律(防泄漏)**:`memory.Manager` 的 `onBootstrapChanged` 必须是带显式 id 的注册 API,而不是匿名 func 注册就完:

```go
// internal/memory/manager.go (新增)
type Manager struct {
    // ...
    mu             sync.RWMutex
    bootstrapHooks map[string]func(name string)  // id → hook
}

func (m *Manager) RegisterBootstrapChanged(id string, hook func(name string)) {
    m.mu.Lock()
    if m.bootstrapHooks == nil { m.bootstrapHooks = map[string]func(name string){} }
    m.bootstrapHooks[id] = hook
    m.mu.Unlock()
}

func (m *Manager) UnregisterBootstrapChanged(id string) {
    m.mu.Lock()
    delete(m.bootstrapHooks, id)
    m.mu.Unlock()
}

func (m *Manager) WriteBootstrap(name string, content []byte) error {
    // ... 写盘 ...
    m.mu.RLock()
    hooks := make([]func(string), 0, len(m.bootstrapHooks))
    for _, h := range m.bootstrapHooks { hooks = append(hooks, h) }
    m.mu.RUnlock()
    for _, h := range hooks { h(name) }
    return nil
}
```

调用方(`WorkspaceBootstrap`)拿稳定 id(例如 `"workspace-bootstrap"`)注册,生命周期 = 进程级,无需主动注销。**未来**如果 `set_workspace` 触发 workspace 切换,新 `WorkspaceBootstrap` 注册新 id,旧的在 `Dispose()` 里注销旧的 id — 不留任何泄漏路径。

**对照:被这个 API 救下的现状**

| 订阅方 | 生命周期 | 注册 API | 注销 API | 状态 |
|---|---|---|---|---|
| `WorkspaceBootstrap` | 进程级 | `RegisterBootstrapChanged("workspace-bootstrap", wb.invalidate)` | `runtime.Dispose` 时调 `UnregisterBootstrapChanged` | 防泄漏 |
| `TextDeltaHook`(per-session,已存在) | per-session | `deltaHook.Attach(a)`(`agents/text_delta_hook.go:34`) | `deltaHook.Close()` 在 `AgentLoopSession.Close()`(`agentloop/session.go:35`) | 已有配对 |

> **关键设计**:bootstrap 缓存必须在 workspace 单例持有,`ctxengine.Deps.MemoryBootstrap` 仅穿透转发。早期设计曾把 bootstrap 缓存放到 ctxengine per-session,所有 session 各自缓存各自的副本,违反 workspace 文件语义,已删除。

### FR-10.1:回调生命周期通用纪律

任何"事件订阅 / 回调注册"必须满足以下两条(否则不许合并):

1. **配对的 Register / Unregister API**:注册时给一个稳定 id,注销时按 id 删(不是按匿名函数);注册 API 必须返回 id 或接收 id 参数。
2. **owner 明确**:subscribing 一方负责在自己被销毁时调 Unregister。owner 的生命周期决定注册生命周期:
   - per-session 资源(`TextDeltaHook` / 任何 `*AgentLoopSession` 持有的订阅):在 `AgentLoopSession.Close()` 链里注销
   - workspace / 进程级资源(`WorkspaceBootstrap`):在 `runtime.Dispose()` 里注销
   - 临时订阅(单次 RPC handler 内):在 handler return 前注销

### FR-11:`MemFactsProvider` 接口 — `ctxengine` 不直连 `memory` 包

`ctxengine` 不该 import `internal/memory`(避免污染);通过 `ctxengine.Deps` 扩展:

```go
// ctxengine.Deps 新增(在 assembler.go Deps interface 里)
type Deps interface {
    // ... 已有 Provider() / ModelName() / Logger() ...
    SkillSummaries() []SkillSummary                   // 已有 (skill sections 实时查)
    McpServers() []MCPServerInfo                      // 已有
    MemoryFacts(ctx context.Context) []Fact           // 新增 — memory 子系统注入;Agent 内部使用 a.session.ID
    MemoryBootstrap(name string) string               // 新增 — 穿透到 WorkspaceBootstrap
}
```

**`MemoryFacts` 签名简化**:Agent 是 per-session 单例,`a.session.ID` 就是当前 session。函数签名不接 sessionID,避免 caller 传错。caller 不应当传别的 session ID(语义上"这个 agent 拿这个 session 的 facts"是天然对应)。

**`Fact` 跨包映射**:`memory.Manager.Search` 返回 `[]memory.SearchResult`(含 `Text / Section / Score` 字段),`Agent.MemoryFacts` 内部转 `ctxengine.Fact{Content: h.Text, Source: h.Section}`。`ctxengine.Fact` 是 ctxengine 自有 type,不让 memory 包 import 进来。

`Agent` 隐式满足(指向 `memory.Manager`)。当 `memMgr == nil` 时 `MemoryFacts` 返回 nil / `MemoryBootstrap` 返回 `""` → BuiltInSections 自然降级。

> 与既有 `AvailableSkills / MCPServers` 字段对称;**不**改 `AssembleParams.AvailableFacts` 的位置(那是 caller 提供的 caller-side override),而是走 `Deps` 实时查。

### FR-12:memory 子系统未启用时的可降级

```
memMgr == nil 或 cfg.Memory.Enabled == false:
  MemoryFacts() → nil
  MemoryBootstrap(name) → ""
  BuiltInSections 判定 len==0 → 不挂段
  PolicySection 也不注册(沿用 memory-core §9.2:memMgr==nil 时不调 SetSections)
  → 结果:system 层 = Instructions() + 现有 skills/mcp + addition,与现状一致
```

---

## 4. 实现方案

### 4.1 数据流总览

```
┌────────────────────────────────────────────────────────────────────────────┐
│ workspace / 进程启动(一次性)                                                  │
│  ↓                                                                                       │
│ runtime.Build                                                                            │
│   ├─ memMgr := memory.New(...)                              // 已有                       │
│   ├─ workspaceBootstrap := runtime.NewWorkspaceBootstrap(memMgr, log)  // FR-9 新增 │
│   │    └─ RefreshAll 一次性读 IDENTITY.md / SOUL.md / USER.md → workspace cache      │
│   ├─ workspaceBootstrap 注册到 memMgr.RegisterBootstrapChanged("workspace-bootstrap", wb.invalidate) │
│   ├─ digestStore := store.NewSQLiteDigestStore(db)                                       │
│   └─ 把 workspaceBootstrap / digestStore 注入 AgentFactoryDeps                          │
│                                                                                          │
│ session 启动 / NewAgentLoopSession(per session,频繁发生)                                  │
│  ↓                                                                                       │
│ factory.NewAgentLoopSession                                                              │
│   ├─ agent.New(... Skills / Mcp / MemoryFacts / WorkspaceBootstrap 等)                  │
│   │    └─ Agent 隐式满足 ctxengine.Deps:MemoryBootstrap → WorkspaceBootstrap.Get(name) │
│   └─ ctxengine.NewDefaultAssembler(deps)                                                │
│        └─ da.SetSections(PolicySection()) // 注册一次                                   │
│   ↓                                                                                      │
│ hydrateSession(ctx, f, sess)  ← 两表分离                                              │
│   messages := MessageStore.List(sid) (纯 UI 历史)                                        │
│   digests  := DigestStore.List(sid) (累积 N 条,空表示没压过)                             │
│   latest   := digests[N-1]                                                                │
│   tail     := 按 latest.FirstKeptID / FirstKeptTs 切 messages                          │
│   sess.ReplaceAll([digest1, ..., digestN] + tail)  ← digest 进 Session 不进 messages 表 │
│   ↓                                                                                      │
│ user prompt → executor.RunConversation 每 turn: │
│   (1) 调 BuildSystemSections(ctx, sess.ID, ...) 拿 sections                              │
│   (2) messages = d.Assembler().Assemble(ctx, AssembleParams{                              │
│           Messages:        session.Messages(),                                          │
│           AvailableSkills: d.SkillSummaries(),                                          │
│           AvailableFacts:  <caller override;nil 默认>,                                  │
│           MCPServers:      d.McpServers(),                                              │
│       })                                                                                  │
│   (3) 若 assembled.Stats.CompactionTriggered:                                            │
│         a.Session().ReplaceAll(assembled.Messages)   ← ★ 写回 Session                     │
│         a.PersistCompaction(ctx, CompactResult{...})    ← 落库 session_digests         │
│   ↓                                                                                      │
│ assembler.Assemble pipeline (agent-context-engine §FR-1 7-step)            │
│   step 1: tool result 截断                                                               │
│   step 2: tokensBefore 估算                                                              │
│   step 3: tokensBefore > budget → Compact (partitionFold + token)                        │
│   step 4: BuildSystemSections(ctx, sid) 返回 8 块 sorted                                  │
│           sections = [IDENTITY p30, SOUL p40, policy p50, USER p60,                     │
│                        available_skills p100, MEMORY p110, available_mcp p120]          │
│           builtin = BuiltInSections(skills, nil /* facts 已挂 */, mcp)                   │
│           merged = sections ++ builtin                                                  │
│           sysAddition = sort by priority asc + skip empty + "\n\n" 拼                     │
│   step 5: 透出 CompactResult(Summary/FirstKept*) → AssembleResult                         │
│   step 6: emit CompactionEvent                                                           │
│   step 7: return AssembleResult{Messages, EstimatedTokens, SystemAddition}              │
│   ↓                                                                                      │
│ executor.RunConversation step 2(★ 修复点):                                                │
│   req := CompletionRequest{                                                              │
│       System:   d.Instructions() + "\n\n" + assembled.SystemAddition,  ← v1 漏接,v2 接回 │
│       Messages: assembled.Messages,                                                      │
│       Tools:    d.Tools().Specs(),                                                       │
│   }                                                                                      │
│   ↓                                                                                      │
│ provider.Stream(req) → LLM 看到的上下文:                                                 │
│                                                                                          │
│   <Instructions()>                                                                       │
│   <IDENTITY>...</IDENTITY>                                                               │
│   <SOUL>...</SOUL>                                                                       │
│   <memory-policy>...</memory-policy>                                                     │
│   <USER>...</USER>                                                                       │
│   <available_skills>...</available_skills>                                               │
│   <MEMORY>...</MEMORY>                                                                   │
│   <available_mcp>...</available_mcp>                                                     │
│   <addition>...</addition>                                                               │
│                                                                                          │
│   [pinnedPrefix] [旧 digest1] [旧 digest2] [新 digest] [tail]                            │
│                                                                                          │
└────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 代码改动 — Go agent

新增 / 修改文件:

| 文件 | 改动说明 |
|---|---|
| `internal/agents/protocol/types.go` | `+Message.ID / +Message.Timestamp` (FR-1) |
| `internal/agents/ctxengine/sections.go` | **新增** `renderIdentitySection(name, content)` / `renderSoulSection` / `renderUserSection`(空 → 不挂);priority 常量 `PriorityIdentity=30 / PrioritySoul=40 / PriorityUser=60 / PriorityMemory=110`;`BuiltInSections` 保持不变(只挂 skill/facts/mcp) |
| `internal/agents/ctxengine/memory_layers.go` | **新增**(轻量):仅保留 MEMORY 块 TTL 缓存(`cfg.MemoryFactsCacheTTL` 默认 60s,key = `(sessionID, query)`);**不再负责 bootstrap 缓存**(已迁出到 `runtime.WorkspaceBootstrap`) |
| `internal/agents/ctxengine/assemble.go` | step 4 改:用 `d.BuildSystemSections(ctx, p.SessionID, p.AvailableSkills, p.AvailableFacts, p.MCPServers)` 替换 `BuiltInSections + SystemSections` 简单拼接;**关键修复**:函数体必须保留 caller 传入的 `facts` 作 override,deps 实时查仅作 fallback(只有 `facts == nil && a.deps != nil` 时才走 deps);FR-11 降级;FR-12 兼容 |
| `internal/agents/ctxengine/assembler.go` | `Deps` 接口 + `MemoryFacts(ctx) / MemoryBootstrap(name)`;**`MemoryBootstrap` 改成纯转发到 `WorkspaceBootstrap.Get(name)`**(`ctxengine` 不再自己 cache bootstrap);`BuildSystemSections(ctx, sessionID, skills, facts, mcp)` 内部 helper(返回 `[]SystemSection`) |
| `internal/agents/ctxengine/compact.go` | `partitionFold` pair-aware(FR-4 不切 tool_use/tool_result) + `pinnedPrefixLen / isCompactionSummary` 识别旧 digest 不进 fold;`alignTailBoundary` 防止 tail 切 pair;token 预算切窗 + firstKept 边界透出 |
| `internal/agents/ctxengine/params.go` | `AssembleResult` 增加 `CompactSummary / FirstKeptID / FirstKeptTimestamp`(透出自动压缩产物);`Config` 增加 `CompactTailTokens / MemoryFactsLimit / MemoryFactsCacheTTL` |
| `internal/agents/ctxengine/ctxengine.go` | 不变 |
| `internal/agents/agent.go` | `+Agent.Memory *memory.Manager`(供 MemoryFacts 用);`+Agent.WorkspaceBootstrap *runtime.WorkspaceBootstrap`(供 MemoryBootstrap 用 — 必须,见 §8 决策 #14);`+Agent.MemoryFacts(ctx) []Fact` 实现 `ctxengine.Deps` 扩展(内部 `a.recentUserQuery(a.session.ID, 3)`);`+Agent.MemoryBootstrap(name)` 代理到 `workspaceBootstrap.Get`;`+Agent.PersistCompaction(ctx, ctxengine.CompactResult) error` 落库 |
| `internal/agents/dispatcher.go` | `Append` 时填 `Message.ID = msgID + "-"+index`,`Timestamp = time.Now().UnixMilli()` |
| `internal/agents/executor/executor.go` | **★ 关键修复 #1**:`req.System = d.Instructions() + "\n\n" + assembled.SystemAddition`(当前实现只填 `d.Instructions()`,漏接);`+Deps.PersistCompaction`;**★ 关键修复 #2**:自动压缩触发路径 — `assembled.Stats.CompactionTriggered == true` 时:① `a.Session().ReplaceAll(assembled.Messages)`(内存替换,跨 turn 持久);② `a.PersistCompaction(ctx, ctxengine.CompactResult{...})`(落库 session_digests);`AssembleParams` 补 `AvailableFacts` 字段(供 caller override;运行时 caller 一般传 nil,让 deps 实时查) |
| `internal/agents/store/digest_store.go` | **新增** `DigestStore` 接口 + `SessionDigest` struct |
| `internal/agents/store/sqlite_digest_store.go` | **新增** `SQLiteDigestStore`(GORM,AutoMigrate `session_digests` 表);**FR-2.1**: per-session `sync.Mutex` + `seqAlloc map[string]*seqAlloc` 缓存 session→max sequence;`nextSequence(ctx, sessionID)` 内部串行分配;UNIQUE 冲突时清 cache + retry 一次 + size cap (`seqAllocMaxEntries = 1000`) |
| `internal/agents/store/memory.go` | **新增** `MemoryDigestStore`(单测 / factory test 用) |
| `internal/agents/store/models.go` | **改造** `CompactionCheckpoint` struct → `SessionDigest` struct(schema 迁移见 FR-2) |
| `internal/agents/store/digest_store_test.go` | **新增** roundtrip / List / Latest / DeleteBySession 用例 |
| `internal/agents/store/memory_store.go` | `List` 已有;无需改 |
| `internal/agentloop/factory.go` | `+AgentFactory.DigestStore` + `+AgentFactory.MemoryManager` + `+AgentFactory.WorkspaceBootstrap`;`NewAgentLoopSession` 透传到 `agent.New` |
| `internal/agentloop/hydrate.go` | **关键修复**:两表分离版本 — `MessageStore.List` 拉纯 messages,`DigestStore.List` 拉累积 digest,按 latest digest 的 `FirstKeptID / FirstKeptTs` 切 tail,`sess.ReplaceAll(digests + tail)`;**不再注册 bootstrap 失效回调**(由 runtime.WorkspaceBootstrap 统一负责,per-session 不需要) |
| `internal/agentloop/loop.go` | 不变 |
| `internal/gateway/handlers.go` | `runManualCompact` 成功后调 `agent.PersistCompaction(res)`(**只写 `session_digests`,不再写 messages**);`handleDeleteSession` 增加 `DigestStore.DeleteBySession` 级联;`HandlerOptions.DigestStore`;`agent.set_workspace` 触发 memMgr 重扫(同 `set_workspace` spec 已 wiring skills,可扩展) |
| `internal/runtime/factory.go` | `+AgentFactoryDeps.MemoryManager / DigestStore / WorkspaceBootstrap` |
| `internal/runtime/runtime.go` | `memMgr` 已 wire(memory-core §9.2 `cmd/app/main.go`);**新增 `WorkspaceBootstrap` 构造 + 注入到 AgentFactoryDeps**;`digestStore` 注入 factory + handler |
| `internal/runtime/database.go` | AutoMigrate:改造 `CompactionCheckpoint` 表 → `SessionDigest` |
| `internal/runtime/workspace_bootstrap_test.go` | **新增**:RefreshAll / Get / Invalidate / 并发安全 / 文件缺失降级 |
| `internal/memory/manager.go` | 新增 `Manager` struct + `RegisterBootstrapChanged(id, hook)` / `UnregisterBootstrapChanged(id)` / `WriteBootstrap` 内 fanout 调用(FR-10);`bootstrapHooks map[string]func(string)` 字段 + `sync.RWMutex` 保护 |
| `internal/config/config.go` | `+cfg.Agent.CompactTailTokens / MemoryFactsLimit / MemoryFactsCacheTTL` |
| `src/darvin-agent/config.yaml` | 新增字段默认 |
| `internal/agents/store/compaction_store.go` | **废弃**(旧 `compaction_checkpoints` store 实现,本 spec 替换为 `digest_store.go`) |
| `internal/agents/store/sqlite_compaction_store.go` | **废弃**(同上) |

**不改**:
- `internal/agentloop/loop.go`
- `internal/agents/protocol/provider.go`
- `internal/agents/session/session.go`
- `internal/llm/*`
- `internal/skills / internal/mcp`
- spec 内已落地,本 spec 不重复(memory-core)

### 4.3 关键调用点代码片段(伪代码,仅示意结构)

#### 4.3.1 ctxengine.Assemble step 4 — 拼 system 层

```go
// agents/ctxengine/assemble.go (新增/改造)
func (a *DefaultAssembler) Assemble(ctx context.Context, p AssembleParams) AssembleResult {
    // ... step 1-3 同现状:tool result 截断 / tokensBefore / Compact ...

    // step 4(本 spec):BuildSystemSections 接管 system 拼装
    sections, err := a.BuildSystemSections(ctx, p.SessionID, p.AvailableSkills, p.AvailableFacts, p.MCPServers)
    if err != nil {
        // 降级:返原 sections,仅含 builtin + caller-supplied
        sections = a.sections
    }
    sysAddition := a.composeSystemAddition(sections)

    return AssembleResult{ Messages: msgs, SystemAddition: sysAddition, ... }
}

func (a *DefaultAssembler) BuildSystemSections(ctx context.Context, sessionID string,
    skills []SkillSummary, facts []Fact, mcp []MCPServerInfo) ([]SystemSection, error) {

    out := append([]SystemSection{}, a.sections...) // 已注册 (Policy + addition)

    // bootstrap 三段(IDENTITY/SOUL/USER)走 workspace 级单例
    // ctxengine.Deps.MemoryBootstrap 内部代理到 runtime.WorkspaceBootstrap.Get,
    // 因此本 assembler **不持有** bootstrap 缓存(已迁出,见 FR-9 / FR-10)
    if a.deps != nil {
        if identity := a.deps.MemoryBootstrap("IDENTITY.md"); identity != "" {
            out = append(out, SystemSection{Name: "identity", Priority: PriorityIdentity, Content: renderIdentitySection(identity)})
        }
        if soul := a.deps.MemoryBootstrap("SOUL.md"); soul != "" {
            out = append(out, SystemSection{Name: "soul", Priority: PrioritySoul, Content: renderSoulSection(soul)})
        }
        if user := a.deps.MemoryBootstrap("USER.md"); user != "" {
            out = append(out, SystemSection{Name: "user", Priority: PriorityUser, Content: renderUserSection(user)})
        }
    }

    // MEMORY 块:FTS top-N(per-(sessionID,query) TTL 缓存,60s)
    // ★ caller override 优先:facts 非空走 caller,空才走 deps 实时查
    if len(facts) == 0 && a.deps != nil && a.cfg.MemoryFactsLimit > 0 {
        var err error
        facts, err = a.fetchMemoryFacts(ctx, sessionID)
        if err != nil {
            facts = nil // warn-and-continue
        }
    }
    if len(facts) > 0 {
        out = append(out, SystemSection{Name: "memory", Priority: PriorityMemory, Content: renderAvailableFactsSection(facts)})
    }

    // builtIns (skills / mcp) — 沿用 BuiltInSections,但 facts 传 nil(已挂)
    out = append(out, BuiltInSections(skills, nil /* facts 已挂 */, mcp)...)
    return out, nil
}
```

**`fetchMemoryFacts` 内部**:从 `a.deps.MemoryFacts(ctx)` 拿 (Agent 内部用 `a.session.ID` 触发),并按 `(sessionID, query)` 走 TTL cache(见 FR-8)。cache key 包含 query,query 变了 invalidate 该 entry。

#### 4.3.2 executor 拼 `req.System`(本 spec 关键修复)

```go
// agents/executor/executor.go (本 spec 修复)
req := &protocol.CompletionRequest{
    Model:      d.ModelName(),
    Messages:   messages,
    Tools:      d.Tools().Specs(),
    ToolChoice: protocol.ToolChoice{Type: "auto"},
    System:     d.Instructions() + "\n\n" + assembled.SystemAddition, // ★ 修复:接回 absent SystemAddition
    Stream:     true,
    MaxTokens:  4096,
}
```

> **当前实现** `System: d.Instructions()` 漏接 `assembled.SystemAddition`,builtin sections + `SystemPromptAddition` 全部丢失。本 spec 修复这条 gap。

#### 4.3.3 ctxengine.Deps 扩展(Agent 隐式满足)

```go
// agents/ctxengine/assembler.go
type Deps interface {
    Provider() protocol.ModelProvider
    ModelName() string
    Logger() *zap.Logger
    Emit(ev event.Event)

    SkillSummaries() []SkillSummary  // 已有(实时查)
    McpServers() []MCPServerInfo     // 已有(实时查)

    MemoryFacts(ctx context.Context) []Fact      // 新增 — Agent 内部用 a.session.ID
    MemoryBootstrap(name string) string          // 新增 — 穿透到 WorkspaceBootstrap
}
```

`Agent` 实现 — **★ MemoryBootstrap 必须穿透 WorkspaceBootstrap(不要直接 ReadBootstrap)**:

```go
// agents/agent.go
//
// Agent 同时持有 *memory.Manager (供 MemoryFacts 用) 与 *runtime.WorkspaceBootstrap
// (供 MemoryBootstrap 用)。两者职责严格分离:
//   - memory.Manager.Search      → per-session FTS 检索 (MemoryFacts 用)
//   - WorkspaceBootstrap.Get     → workspace 级 bootstrap 缓存读取 (MemoryBootstrap 用)
//   - WorkspaceBootstrap.Invalidate → bootstrap.write 触发 (通过 onBootstrapChanged 回调)

func (a *Agent) MemoryFacts(ctx context.Context) []ctxengine.Fact {
    if a.memory == nil { return nil }
    q := a.recentUserQuery(a.session.ID, 3) // 取最近 3 条 RoleUser 拼接
    if q == "" { return nil }
    hits, err := a.memory.Search(ctx, q, a.cfg.MemoryFactsLimit)
    if err != nil || len(hits) == 0 { return nil }
    out := make([]ctxengine.Fact, 0, len(hits))
    for _, h := range hits {
        out = append(out, ctxengine.Fact{Content: h.Text, Source: h.Section})
    }
    return out
}

// ★ 必须代理到 WorkspaceBootstrap;若直接调 a.memory.ReadBootstrap 会绕过缓存
//   + 失效机制,WorkspaceBootstrap.Invalidate 后仍读到老值。
func (a *Agent) MemoryBootstrap(name string) string {
    if a.workspaceBootstrap == nil { return "" }
    return a.workspaceBootstrap.Get(name)
}
```

#### 4.3.4 hydrate 两表分离版本

```go
// agentloop/hydrate.go (本 spec)
func hydrateSession(ctx context.Context, f *AgentFactory, sess *session.Session) {
    if f.MessageStore == nil || sess == nil { return }

    // 1) 纯 UI 历史(来自 messages 表)
    rows, err := f.MessageStore.List(ctx, sess.ID, 0, 0)
    if err != nil { /* warn-and-continue */ }
    history := make([]protocol.Message, 0, len(rows))
    for _, r := range rows {
        converted, _ := recordToMessages(r)
        history = append(history, converted...)
    }

    // 2) 累积 digest(来自 session_digests 表;空表示没压过)
    var digests []store.SessionDigest
    if f.DigestStore != nil {
        if d, derr := f.DigestStore.List(ctx, sess.ID); derr == nil && len(d) > 0 {
            digests = d
        }
    }

    // 3) 切 boundary:用 latest digest 的 FirstKeptID / FirstKeptTimestamp
    var boundaryID string
    var boundaryTS int64
    if len(digests) > 0 {
        latest := digests[len(digests)-1]
        boundaryID = latest.FirstKeptID
        boundaryTS = latest.FirstKeptTimestamp
    }

    tail := history
    if boundaryID != "" || boundaryTS > 0 {
        tail = splitAtBoundary(history, boundaryID, boundaryTS)
    }

    // 4) 拼装 Session.Messages:digest 在前 + tail 在后
    msgs := make([]protocol.Message, 0, len(digests)+len(tail))
    for _, d := range digests {
        msgs = append(msgs, protocol.Message{
            Role: protocol.RoleAssistant,
            Content: "[Conversation Summary]\n" + d.Summary +
                     fmt.Sprintf("\n\n(Compacted at %s; sequence #%d)",
                         time.UnixMilli(d.CreatedAt).Format(time.RFC3339), d.Sequence),
            Timestamp: d.CreatedAt,
        })
    }
    msgs = append(msgs, tail...)

    // 5) 不需要注册 bootstrap 失效回调:
    //    IDENTITY/SOUL/USER 是 workspace 级文件,由 runtime.WorkspaceBootstrap 单例持有缓存;
    //    写入时 memMgr.onBootstrapChanged 已直接调 WorkspaceBootstrap.Invalidate,
    //    hydrate 路径不参与 bootstrap 刷新。

    sess.ReplaceAll(msgs)
}

// splitAtBoundary 找 FirstKeptID 命中的行;失配退 FirstKeptTimestamp;两者皆失配返原 msgs
func splitAtBoundary(msgs []protocol.Message, id string, ts int64) []protocol.Message {
    for i, m := range msgs {
        if id != "" && m.ID == id { return msgs[i:] }
        if ts > 0 && m.Timestamp >= ts { return msgs[i:] }
    }
    return msgs // 安全降级:不切
}
```

### 4.4 落库触发(两表分离 — digest 只写 session_digests)

`Agent.PersistCompaction(res)` 只做一件事:写一条新 digest 到 `session_digests` 表。**不再调 `msgStore.Save`;sequence 留给 store 内部串行分配**(FR-2.1):

```go
// agents/agent.go
func (a *Agent) PersistCompaction(ctx context.Context, res ctxengine.CompactResult) error {
    if a.digestStore == nil || !res.Success {
        return nil
    }
    rec := &store.SessionDigest{
        ID:                "digest-" + res.Checkpoint.ID,
        SessionID:         a.session.ID,
        // Sequence = 0 → 由 digestStore.nextSequence 内部分配
        Summary:           res.Summary,
        TokensBefore:      res.TokensBefore,
        TokensAfter:       res.TokensAfter,
        FirstKeptID:       res.FirstKeptID,
        FirstKeptTimestamp: res.FirstKeptTimestamp,
        CompactReason:     res.Reason,
        SourceCompactID:   res.Checkpoint.ID,
        CreatedAt:         time.Now().UnixMilli(),
    }
    if err := a.digestStore.Save(ctx, rec); err != nil {
        return fmt.Errorf("digest save: %w", err)
    }
    return nil
}
```

**触发路径**(语义沿用,落库目标改成只写 `session_digests`):

```
手动:runManualCompact 成功后 → a.PersistCompaction(res) → digestStore.Save(seq 自动分配)
自动:executor.RunConversation: if assembled.Stats.CompactionTriggered
                             → (i)  a.Session().ReplaceAll(assembled.Messages)  ← 内存替换
                             → (ii) a.PersistCompaction(ctx, ctxengine.CompactResult{...})
                                  → Agent.PersistCompaction → digestStore.Save
```

**关键修复**:步骤 (i) 是当前实现缺失的 — 没有 `a.Session().ReplaceAll(assembled.Messages)`,自动压缩结果只在当前 turn 内存生效,下次 turn Session.Messages 仍然全量。本 spec 强制补这一步,automatic compaction 才有跨 turn 价值。

---

## 5. 边界情况

| 场景 | 处理 |
|---|---|
| memory 子系统未启用 (`memMgr==nil` 或 `cfg.Memory.Enabled==false`) | `MemoryFacts → nil` / `MemoryBootstrap → ""` → `BuiltInSections` 短路;`PolicySection` 也不注册 → system 层 = `Instructions()` + skills/mcp + addition,与现状一致 |
| bootstrap 文件缺失 | `WorkspaceBootstrap.content[name] = ""` → `<IDENTITY>` / `<SOUL>` / `<USER>` 不挂 |
| bootstrap 文件 write 时正在 Assemble | `WorkspaceBootstrap.Invalidate(name)` 删该 key;**下次任何 session** 的 Assemble 通过 `Deps.MemoryBootstrap(name)` → `WorkspaceBootstrap.Get(name)` 拿到空 → 触发 lazy reload → 下次 `RefreshAll` 或下一次访问 ReadBootstrap 重新读盘。**进程级失效**,所有 session 同步看到新值 |
| 多 session 共用同一 workspace | **设计本意**:`WorkspaceBootstrap` 是 workspace 单例,所有 session 看到同一份 IDENTITY/SOUL/USER;切换 session 不再各自重读,刷新时机一致 |
| MEMORY.md FTS 检索失败 / 无 hits | `len(facts)==0` → `<MEMORY>` 不挂;warn-and-continue |
| 自动压缩的 `CompactResult` 透出 | `AssembleResult` 增加 `CompactSummary / FirstKeptID / FirstKeptTimestamp` |
| 同一会话连续多次压缩 | `partitionFold` 保留旧 digest 原样(不进 fold);每次压写一条新 `SessionDigest`,`sequence` 单调递增;`DigestStore.Latest` 只取最大 sequence 那行作 current boundary |
| 被压原始消息 | 不删;`messages` 表保留全部原始交互(UI 完整);hydrate 时按 latest digest 的 `FirstKeptID / FirstKeptTs` 切 tail,被压消息不进 LLM 上下文 |
| **两表分离边界** | `messages` 表只存 `Role=user/assistant/tool` 真实交互;`session_digests` 表只存摘要。**digest 不写 messages 表**;`recordToMessages` 不再嗅探 `Content` 前缀;UI `get_messages` 永不返 digest |
| digest ID 与 run 消息 ID 冲突 | **不存在**(已分离);digest ID 仅在 `session_digests` 内部唯一,前缀 `digest-<checkpointID>` |
| 折叠区全是被保留项 (`fold` 为空) | `Compact` 快速返回 `Success=false`,不写新 digest |
| 超长会话 > 1000 条 | `MessageStore.List` 默认 1000;tail 起点在 1000 之后 → 降级加载全部 messages(`splitAtBoundary` 找不到 boundary 即返原 msgs) |
| `compact_tail_tokens` 未配置 (0) | 回退 `compact_tail_keep` 固定条数 |
| `MemoryFactsLimit` ≤ 0 | 不查 FTS;`<MEMORY>` 块不挂 |
| `MemoryFactsCacheTTL` ≤ 0 | 关闭缓存;每次 Assemble 都查 FTS |
| MEMORY FTS TTL cache key 错位 | key = `(sessionID, query)` 二元组,query 变 invalid 该 entry;不能只看 sessionID(query 是动态的,基于最近 3 条 user msg) |
| concurrent Assemble × N | `WorkspaceBootstrap` 用 `sync.RWMutex`(内部);`BuildSystemSections` 不再持有自己的 bootstrap 锁,改为单次 `Deps.MemoryBootstrap(name)` 调用穿透;`MEMORY` FTS TTL cache 走 ctxengine 内部 `sync.RWMutex`(per `(sessionID,query)` 互不影响) |
| Exec.Abort mid-Compact | ctx cancel → `Summarizer` 立即返回 err → `Compact` 返 `Success=false`;executor 仍用原 `Messages` 跑 turn,不在 system 层产生副作用 |
| 用户编辑 bootstrap 文件后立即提问 | `bootstrap.write` IPC → `memMgr.WriteBootstrap` → 推 `onBootstrapChanged` → `WorkspaceBootstrap.Invalidate(name)` → **任何** session 下次 Assemble 重新读到新值;延迟 ≤ 1 turn |
| `set_workspace` 触发 workspace 切换 | 当前 spec 范围内不实现;若未来加,需新 `WorkspaceBootstrap` 重新 `RefreshAll`,旧的 `Dispose()` 调 `UnregisterBootstrapChanged("workspace-bootstrap-<old-id>")`,避免回调泄漏 |
| 回调泄漏(per-session register / 进程级数据) | 早期设计在 `hydrateSession` 调 `memMgr.RegisterBootstrapChanged(fn)` 是反模式 — per-session 注册进程级事件的回调,session 销毁没注销就泄漏;已删除,`WorkspaceBootstrap` 只在 `runtime.Build` 注册一次,进程级,无需注销 |
| `TextDeltaHook` 已有配对 | per-session:`AgentLoopSession.Close()` 调 `deltaHook.Close()`(已存在,不在本 spec 改动范围);对比做正例 |
| `WorkspaceBootstrap.Dispose()`(未来) | 进程级:注销 `UnregisterBootstrapChanged("workspace-bootstrap")` + 清空 `content` map;若未来 `set_workspace` 加,要替换时**先注销旧再注册新**,不能漏 |
| **并发 Save 同一 session**(FR-2.1 重点) | 手动压 + 自动压同时触发 / 多 goroutine 并发 Assemble / 进程重启后 first-call vs in-flight goroutine → SQLite UNIQUE 约束会挡掉重复 sequence,`SQLiteDigestStore.Save` 内部 retry 一次:清 cache + 重新 nextSequence;**常态下不冲突**(per-session mutex 串行);仍冲突 → 返 error(调用方按 warn-and-continue 处理,不阻塞 turn) |
| **Save 后 DB 失败但 cache 已更新** | 第一次 `nextSequence` 先 + 1 → 落库失败 → 清 cache;retry 重新读 DB max → 不会断号,只会缺号(比如期望 1,2,4,缺 3);缺号不影响业务(Latest 仍能取 max,List 仍能按 sequence asc 排) |
| **`seqAlloc` 内存累积** | 只有 `DeleteBySession` 清 cache;长寿命进程下入口数线性增长。**size cap**:`seqAllocMaxEntries = 1000`,超过时清空整张 map(下次访问重新读 DB);磁盘 DB 永远是真值,清空仅丢内存 cache,不丢数据 |
| **★ MemoryBootstrap 绕过 WorkspaceBootstrap 实现风险** | 反模式:`Agent.MemoryBootstrap(name)` 内部 `return a.memory.ReadBootstrap(name)` —— 直接读 `memMgr` 绕过 `WorkspaceBootstrap`。**后果**:`memMgr.WriteBootstrap` 触发 `onBootstrapChanged → WorkspaceBootstrap.Invalidate` 失效后,`MemoryBootstrap` 仍读 `memMgr` 拿到**老值**;WorkspaceBootstrap 的缓存和失效机制**完全作废**,所有 session 持续读旧 bootstrap 内容。**正模式**:`return a.workspaceBootstrap.Get(name)` 一字不改穿透;**禁止任何"先 ReadBootstrap 再 Get"或"per-Agent cache"的实现** |
| **代码片段与文档表层一致 / 行为层背离**(本 bug 的特征) | 这种 bug 在 review 时极难发现:函数签名 / 类型 / 表面对得上 §3 设计,只有跑 bootstrap.write RPC + 各 session 触发 Assemble 才能观察到。**防御**:写 `MemoryBootstrap` 时强制用一行穿透实现;review 看到任何 `if a.X { ... else { return ... }}` 形态立刻 flag;单测覆盖"写 → 读"路径(见 §7) |
| `protocol.Message.ID / Timestamp` 零值(老数据 / 手工构造) | `recordToMessages` 跳过旧行不抛错;`firstKept` 边界匹配降级为 timestamp |
| memory-core spec 后续做 LLM judge auto-extract | 与本 spec 正交:`MemoryFacts` 仍走 FTS,embedding 检索后续独立 spec |
| Dreaming / 三阶段压缩 | 本 spec 不引入;`Maintain` 仍是 stub;后续独立 spec |
| `AssembleResult.SystemAddition` 仍为空字符串(memMgr 全 nil) | executor `req.System = Instructions() + "\n\n" + ""` → 等价只有 `Instructions()`(graceful 降级) |
| **tail start 落在 tool 消息上** | `alignTailBoundary` 向前回溯找配对 assistant.tool_use 并扩展 tail;assistant + tool 一起进 tail。多花几条 verbatim 消息,但保住 wire format;retry 路径会重新算 budget |
| **tail end 落在未配对 assistant.tool_use 上** | `alignTailBoundary` 向后收缩到不含该 assistant;LLM 看不到孤儿 tool_use |
| **malformed tool 消息(无对应 assistant.tool_use)** | `partitionFold` 单独归 fold(进 LLM summary);`alignTailBoundary` 无法配对,start-cut 早退,orphan tool 留在 fold → summary 文本中描述该 tool(可能仍触发 wire 错误,但属于数据问题不是 compact bug) |
| **assistant 后紧跟多个并行 tool 结果(并行 tool_use)** | `pairAwareGroupSize` 把全部匹配 `ToolCalls[].ID` 的 tool 消息并入同一 group(同进 fold/kept),符合 wire format「同一 assistant 的 tool_results 必须聚合在紧邻的 user 消息里」的规则 |

---

## 6. 涉及文件

### Go agent

```
新增:
  internal/runtime/workspace_bootstrap.go              (FR-9:workspace 级单例,Get/Invalidate/RefreshAll)
  internal/runtime/workspace_bootstrap_test.go         (并发安全 / 文件缺失降级 / RefreshAll 单调调用)
  internal/agents/ctxengine/memory_layers.go           (FR-7/8/11:FTS TTL cache key=(sessionID,query) + Deps 扩展;**不再持 bootstrap 缓存**)
  internal/agents/ctxengine/memory_layers_test.go
  internal/agents/store/digest_store.go                (FR-2:DigestStore 接口 + SessionDigest struct)
  internal/agents/store/digest_store_test.go           (roundtrip / List / Latest / DeleteBySession + 并发 100 个 Save)
  internal/agents/store/sqlite_digest_store.go         (FR-2:SQLiteDigestStore + session_digests 表 migration;per-session mutex + seqAlloc + size cap + UNIQUE retry)
  internal/agents/store/memory.go                      (MemoryDigestStub 单测用)
  internal/memory/manager.go                           (新增 Manager struct + RegisterBootstrapChanged/UnregisterBootstrapChanged + WriteBootstrap fanout)

修改:
  internal/agents/protocol/types.go                    (+Message.ID/Timestamp)
  internal/agents/ctxengine/sections.go                (+Priority 常量 + renderIdentity/Soul/User + PriorityMemory)
  internal/agents/ctxengine/assemble.go                (step4 改用 BuildSystemSections;**caller override 优先**)
  internal/agents/ctxengine/assembler.go               (Deps + MemoryFacts(ctx)/MemoryBootstrap(name) + BuildSystemSections helper)
  internal/agents/ctxengine/compact.go                 (pair-aware partitionFold + alignTailBoundary + pinnedPrefixLen / isCompactionSummary + token 预算切窗 + firstKept 边界;FR-4 tool_use/tool_result 原子)
  internal/agents/ctxengine/params.go                  (Config / CompactResult / AssembleResult 字段扩充)
  internal/agents/agent.go                             (+Memory + +WorkspaceBootstrap + +DigestStore + MemoryFacts/MemoryBootstrap + PersistCompaction)
  internal/agents/dispatcher.go                        (Append 填 ID/Timestamp)
  internal/agents/executor/executor.go                 (★ req.System = Instructions() + "\n\n" + assembled.SystemAddition;+Deps.PersistCompaction;★ 自动压缩触发 a.Session().ReplaceAll + a.PersistCompaction;AssembleParams +AvailableFacts)
  internal/agents/store/models.go                      (★ 改造 CompactionCheckpoint struct → SessionDigest struct)
  internal/agentloop/factory.go                        (+AgentFactory.DigestStore / MemoryManager / WorkspaceBootstrap; 透传到 hydrate)
  internal/agentloop/hydrate.go                        (★ 两表分离版本 + splitAtBoundary + bootstrap 失效信号)
  internal/gateway/handlers.go                         (runManualCompact 落库到 session_digests; handleDeleteSession 级联 DigestStore.DeleteBySession; HandlerOptions.DigestStore)
  internal/runtime/factory.go                          (AgentFactoryDeps 扩字段)
  internal/runtime/runtime.go                          (注入 WorkspaceBootstrap + DigestStore)
  internal/runtime/database.go                         (AutoMigrate 改造 CompactionCheckpoint → SessionDigest)
  internal/config/config.go                            (Agent.CompactTailTokens / MemoryFactsLimit / MemoryFactsCacheTTL)
  src/darvin-agent/config.yaml                         (默认)

废弃(不再调用,文件保留以便过渡期迁):
  internal/agents/store/compaction_store.go            (旧 CompactionStore 实现,本 spec 替换为 digest_store.go)
  internal/agents/store/sqlite_compaction_store.go     (同上)

不改:
  internal/agentloop/loop.go
  internal/agents/protocol/provider.go
  internal/agents/session/session.go
  internal/llm/*
  internal/skills / internal/mcp
  spec 内已落地,本 spec 不重复(memory-core)
```

### Electron / renderer

```
不变:本 spec 不新增 IPC / renderer 改动。
- 压缩事件 CompactionEvent 已透出
- 历史显示不变(messages 表只做 UI 展示)
- memory 渲染走 memory-renderer spec(memory-subsystem/P3-2026-08-04-memory-renderer-design.md)
- bootstrap.write IPC 已由 memory-core §10 落地,本 spec 复用其信号通路
```

---

## 7. 验收标准

### 用户场景

- [ ] 场景 1:长会话手动压 → 重启 → 提问,LLM system prompt 含 `<USER>` + `<MEMORY>` 块,引用未失效的事实
- [ ] 场景 2:超预算自动压 → 切走再切回,system 层 IDENTITY/SOUL/USER/MEMORY 仍命中(bootstrap 文件不变 / MEMORY FTS 仍命中)
- [ ] 场景 3:多次压缩后 UI `get_messages` 仍返回完整对话
- [ ] 场景 4:全新会话首条 prompt,system 层 8 块(Instructions / IDENTITY / SOUL / policy / USER / skills / MEMORY / MCP / addition)齐全(MEMORY 是否挂由 hits 决定)
- [ ] 场景 5:`cfg.memory.enabled=false` → system 层只含 Instructions + skills/mcp + addition,无错误
- [ ] 场景 6(本 spec 新增):**自动压缩跨 turn 持久** — 长会话 / 显存超预算 → 自动压触发 → **关闭 Electron 重启** → 切回该会话 → LLM 看到 `[digests + tail]` 而不是全量历史(`Session.Messages` 跨 turn 持久 + session_digests 行落库)

### 自动化

- [ ] `cd src/darvin-agent && go test -count=1 ./...` 全绿
- [ ] `go test -race ./internal/agents/...` 全绿
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 无输出
- [ ] `go.mod / go.sum` 不引入新外部依赖
- [ ] `npm run lint` 通过
- [ ] `npm run test`(vitest)通过
- [ ] 新增单测覆盖:
  - `digest_store_test.go` — roundtrip / List 排序 / Latest 返 max sequence / DeleteBySession 级联;**新增**:同 session 并发 100 个 Save → sequence 必须严格 1..100 单调无重复 / 跨 session 并发 100 个 Save 不阻塞(per-session mutex 独立) / UNIQUE 冲突时 Save 内部 retry 一次 / DeleteBySession 清 cache 下次 Save 重新读 DB / `seqAlloc` 超过 `seqAllocMaxEntries` 清空整张 map
  - **`workspace_bootstrap_test.go` — workspace 单例断言**:RefreshAll 拉三文件 / Get 命中 / Invalidate 后再 Get 触发重读 / 文件缺失返 `""` / 并发 Get+Invalidate 安全 / **多 session 共享同一 WorkspaceBootstrap 实例**(用计数器 + sync.Once 验证)
  - **`memory_manager_bootstrap_hook_test.go`(新增)— 回调生命周期**:Register 成功 / Unregister 后 hook 不再被调用 / 重复 Unregister 同一 id 幂等 / 多次 Register 同 id 后者覆盖前者 / WriteBootstrap fan-out 调用所有当前 hook
  - `memory_layers_test.go` — FTS TTL 缓存命中 / 失效(同一个 sessionID 不同 query 必须 invalidate)/ **不再测 bootstrap 缓存**(已迁出)
  - `hydrate_test.go` — 增量:**两表分离断言**(messages 表无 digest row + session_digests 拉 N 条 + tail 切边界 + 降级全部);**回调泄漏断言** — 100 个 session 各自 hydrate + close 后 `memMgr.bootstrapHooks` 长度仍 ≤ 1(`WorkspaceBootstrap` 那一条)
  - `compact_test.go` — 增量:`partitionFold` / `pinnedPrefixLen` / `isCompactionSummary` 识别旧 digest 不进 fold / token 预算切窗 / digest 累积;**FR-4 tool pair 原子**:`partitionFold` pair-aware(`toolPair` helper + `TestPartitionFoldKeepsToolPairsAtomic` + digest 紧邻 pair 边界 case) / `alignTailBoundary` start-cut 扩展(`TestAlignTailBoundaryExtendsOverOrphanTool`) / end-cut 收缩(`TestAlignTailBoundaryShrinksOverOrphanToolUse`) / 已对齐 no-op(`TestAlignTailBoundaryNoOpWhenAligned`) / 端到端 wire format(`TestCompactRetainsToolPairInTail` 走 retained slice 验证每条 tool.tool_use_id 都在紧邻 assistant.ToolCalls.ID 里)
  - `executor_test.go` — 增量:`req.System` 含 `SystemAddition`(用 spy 验证 `Instructions() + "\n\n" + assembled.SystemAddition`);**自动压缩路径**:`assembled.Stats.CompactionTriggered == true` 时 `a.Session().ReplaceAll(assembled.Messages)` 被调 + `a.PersistCompaction` 被调
  - `agent_test.go` — 增量:`MemoryFacts(ctx)` 内部用 `a.session.ID`(`MockSession` 验证取的是本 agent 的 session);`MemoryBootstrap` 必须代理到 `WorkspaceBootstrap.Get`(用 spy / mock 验证:fake `WorkspaceBootstrap` 的 `Get` 被调一次,`memMgr.ReadBootstrap` 一次都没调过);`PersistCompaction` 只调 `digestStore.Save` 不调 `msgStore.Save`(`Sequence = 0` 由 store 分配)
  - **`agent_memory_bootstrap_penetration_test.go`(新增)— 穿透强制断言**:fake `WorkspaceBootstrap` + fake `memory.Manager` → 调 `agent.MemoryBootstrap("USER.md")` → 断言 `WorkspaceBootstrap.Get` 被调且 `memMgr.ReadBootstrap` **未**被调(任何 ReadBootstrap 调用即 fail);并发 N 个 `MemoryBootstrap` 调用 → `WorkspaceBootstrap.Get` 被调 N 次,`memMgr.ReadBootstrap` 仍 0 次
  - **`assemble_caller_override_test.go`(新增)— caller override 优先级**:传入非空 `AvailableFacts` → 函数体不调 `a.deps.MemoryFacts`;传入 nil `AvailableFacts` 才走 deps 实时查(spy 验证 deps.MemoryFacts 被调一次)

### 手动验证

- [ ] `npm run build:agent && npm start` 起应用
- [ ] 编辑 `state/workspace-main/USER.md` 加一段「用户每天早上喝燕麦奶」
- [ ] chat 提问「我早上喝什么?」 → 模型调 `memory_search` / 直接命中 → 答「燕麦奶」
- [ ] DevTools 看 `agent.event` 的 `prompt` payload,确认 `System` 含 `<USER>...燕麦奶...</USER>`(本 spec 修复 `req.System` 漏接 SystemAddition 后才能看到 builtin sections)
- [ ] **多 session 共享 workspace bootstrap**:开 2 个 session(A、B),分别发任意 prompt → 两个 session 的 `System` 都含 `<USER>...燕麦奶...</USER>`(证明 workspace 单例而非 per-session 缓存)
- [ ] **写时即时刷新**:在 Settings 改 USER.md → kill 当前 turn 后下一 turn,**任一 session** 的 `System` 都看到新值(`WorkspaceBootstrap.Invalidate` 跨 session 生效)
- [ ] 长会话 → 自动压触发(显存超 budget) → `sqlite3 sessions.db "SELECT * FROM session_digests"` 看到最新 `sequence=N` 行,`messages` 表**没有** `digest-` 前缀的 row(两表分离的关键断言)
- [ ] **自动压缩跨 turn 持久**:自动压触发 → kill 重启 → 切回该会话 → LLM 看到 `[digests + tail]` 而不是全量(`Session.Messages` 跨 turn 持久 + `session_digests` 落库)
- [ ] 长会话 → 手动压 → `sqlite3 sessions.db "SELECT * FROM session_digests"` 看到最新 `sequence=N` 行,`messages` 表**没有** `digest-` 前缀的 row
- [ ] kill 重启 → 切回该会话 → system prompt 仍有 `<USER>...燕麦奶...</USER>` + `<MEMORY>` 命中,`Messages` 仅 `[digests + tail]`(digest 在 Session 内存,不在 messages 表)
- [ ] `agent.get_messages` 返回**纯历史**,不含 digest 摘要(grep 返回的 Content 不应以 `[Conversation Summary]` 开头)
- [ ] `cat state/workspace-main/MEMORY.md` 验证 entry 在文件(非 digest 落 messages 表)
- [ ] 关掉 `cfg.memory.enabled=false` 重启 → 验证 system 层无 `<MEMORY>` / `<USER>` 块,模型行为退化到无 memory 模式
- [ ] **tool pair 不被切(FR-4 wire format)**:长会话触发大量 tool_use/tool_result 后自动压 → DevTools 看 LLM request payload 的 `messages[].content[]` 数组 — 紧邻 assistant 消息的下一条 user 消息里所有 `tool_result.tool_use_id` 都必须能在前一条 assistant 的 `tool_use.id` 里找到(任何 orphan → Anthropic 400);`Session.Messages` 跨重启 hydrate 后同样验证

### 不在验收范围

- DAG / SubAgent 实现(沿用 `ErrSubAgentUnsupported`)
- Embedding 检索 / 向量库(memory-core §1.3 非目标)
- Dreaming / 后台整合
- `memory/YYYY-MM-DD.md` daily notes
- 改 IDENTITY.md / SOUL.md / USER.md 文件格式(沿用 memory-core §3)
- 重写 Session 状态机
- 改 `ctxengine.SystemSection` 类型基础机制

---

## 8. 关键设计决策(摘要)

1. **system 层 ≠ 消息层**:system 层走 `memory.Manager`(bootstrap + facts);消息层走 `MessageStore` + `DigestStore`(digest + tail)。互不串。

2. **8 块 system 层顺序固定**(priority 30/40/50/60/100/110/120/1000),保证跨会话一致。

3. **bootstrap(IDENTITY/SOUL/USER)是 workspace 级全局文件,不是 session 级数据 — 必须 workspace 单例缓存**:由 `runtime.WorkspaceBootstrap`(进程级单例)持有缓存,所有 session 共享;`ctxengine.Deps.MemoryBootstrap(name)` 仅作穿透转发,不持有自己的 bootstrap 缓存;`runtime.Build` 时一次性 `RefreshAll` 三文件,`bootstrap.write` RPC 通过 `onBootstrapChanged` 回调触发 `WorkspaceBootstrap.Invalidate(name)`。

4. **MEMORY FTS 缓存 = per-(sessionID, query) 短 TTL**:避免每次 Assemble 都打 FTS,但仍能反映最近 hits 变化。**cache key 必须包含 query**:query 动态变(基于最近 3 条 user msg),只看 sessionID 会返 stale data。

5. **`BuiltInSections` 不挂 IDENTITY/SOUL/USER** — 这些走 `BuildSystemSections` 内部 helper,因为它们需要 `Deps.MemoryBootstrap` 实时取(不是 caller 提供的)。

6. **`executor.System = Instructions() + "\n\n" + assembled.SystemAddition`**:本 spec 修复当前实现 **漏接 `assembled.SystemAddition`** 的 gap(源码 `executor.go:200` 当前 `System: d.Instructions()` 完全丢弃 `SystemAddition` 字段)。`Assemble()` 已经计算 `SystemAddition`(`composeSystemAddition` 产物),executor 必须接回。

7. **★ 自动压缩跨 turn 持久**:自动压缩路径必须 `a.Session().ReplaceAll(assembled.Messages)` 把压缩后的 messages 写回 Session 内存 + `a.PersistCompaction` 落库。当前实现缺第一步,自动压缩只在当前 turn 内存生效,下次 turn 全量,等价压缩形同虚设。

8. **降级优先于报错**:`memMgr==nil` / 缓存失败 / FTS 失败都走「不挂段」,不影响 turn 进行。

9. **不动 `memory.PolicySection()`** priority=50:与 memory-core §9.2 对齐,避免冲突。

10. **不动 `ctxengine.SystemSection` 类型**:扩展在 priority 常量 + `BuiltInSections` 调用方,不破坏 OpenClaw 对齐的 10-method 接口。

11. **不破坏 `assembler_enabled: false` 回退路径**:executor 那个 if-cascade 仍在,关掉 assembler 仍走 `session.Messages()` 直发。

12. **★ 两表分离(`messages` vs `session_digests`)**:历史 digest 写成 `messages` 表 `Role=assistant` row 污染 UI。`messages` 表只存 UI 真实交互消息(`user / assistant / tool`),`session_digests` 表只存压缩摘要。UI `get_messages` 只查 messages,绝不返 digest;hydrate 拼 Session.Messages 时从两表各拉各的,按 latest digest 的 `FirstKeptID` 切 tail。**`compaction_checkpoints` 表迁移到 `session_digests`**。latest sequence 即 current checkpoint。**不再用 `Content` 前缀嗅探 digest**(该路径在新设计中不存在)。

13. **★ 回调生命周期纪律**:任何事件订阅必须满足 (1) 配对 Register/Unregister API(按 id 注册 / 按 id 注销,非匿名 func);(2) owner 明确 — subscribing 一方负责在自己的销毁路径里注销。本 spec 范围内:`WorkspaceBootstrap` 用 `RegisterBootstrapChanged("workspace-bootstrap", ...)` 注册,生命周期 = 进程级,无需注销,但 owner 标识仍带 id 方便未来 `set_workspace` 切换时按 id 注销旧的。

14. **★ sequence 分配原子化(per-session mutex + UNIQUE 兜底 retry)**:早期设计 `caller 算 Latest().Sequence + 1` + `Save` 两步非原子,极端并发(手动压与自动压同时触发 / 进程重启后 first-call vs in-flight goroutine / 同 session 多 goroutine 并发 Assemble)会出现 sequence 重复,UNIQUE 约束报错兜底。修复:`SQLiteDigestStore` 内部用 per-session `sync.Mutex`(`sync.Map` 风格 `seqAlloc map[string]*seqAlloc`)串行分配 sequence;UNIQUE 冲突时清 cache + retry 一次 + 仍冲突则返 error。caller(`Agent.PersistCompaction`)**不再算 sequence**,只填 `Sequence = 0`,由 store 原子分配。`DeleteBySession` 清 cache。`seqAlloc` 超过 `seqAllocMaxEntries` (1000) 时清空整张 map(下次访问重新读 DB)。

15. **★ MemoryBootstrap 穿透实现严格性(防"穿透"变"重读"bug)**:实现 `Agent.MemoryBootstrap` 时极易诱发反模式 `return a.memory.ReadBootstrap(name)` —— 直接读 `memMgr`,**与 §3 FR-9 / §8 决策 #3 的 workspace 单例设计直接矛盾**,但函数签名 / 类型 / 表层都符合文档,只有运行时观察到"bootstrap.write 失效不生效"才能发现。**★ 强制规则**:`Agent.MemoryBootstrap(name)` 的**唯一正确实现**是 `return a.workspaceBootstrap.Get(name)` 一行,**禁止** `ReadBootstrap` / per-Agent cache / `Refresh` 显式触发。Agent 必须持 `*runtime.WorkspaceBootstrap` 字段(per-session 持有单例引用,共享 workspace 级缓存)。`agent_memory_bootstrap_penetration_test.go` 用 spy 强制断言 `memMgr.ReadBootstrap` 调用次数 = 0,任何"为了简洁"的绕过实现都立刻 fail。

16. **★ `BuildSystemSections` caller override 优先**:函数签名接收 `facts []Fact` 作为 caller override(由 `AssembleParams.AvailableFacts` 传入),函数体实现必须先检查 `len(facts) > 0` → 用 caller;否则 fallback 到 `a.deps.MemoryFacts(ctx, sessionID)` 实时查。**不能让 caller override 失效**(`BuildSystemSections` 内部默默 fetch deps 覆盖 caller 是反模式,所有 `assemble_caller_override_test.go` 单测都会 fail)。

17. **★ tool_use/tool_result 原子(FR-4 wire format 不变量)**:Anthropic wire format 要求 `tool_result.tool_use_id` 必须在紧邻上一条 assistant 消息的 `tool_use` 块里能找到(`llm/anthropic/convert.go:convertMessages` 注释已记录),否则 400。**两层强制**:`partitionFold` 必须 pair-aware — assistant.ToolCalls 与紧随其后的 tool{ToolCallID ∈ ToolCalls.ID} 一起进 fold 或一起进 kept,**禁止**逐条消息独立判断;Compact 在算出 tail 长度后必须调 `alignTailBoundary` — start-cut 向前扩展、end-cut 向后收缩,**禁止** raw `msgs[len-tail:]`。实现 review 看到「partitionFold 逐条判断」或「Compact 直接用 requestedTail 切片」立刻 flag;`TestCompactRetainsToolPairInTail` 端到端断言每条 tool 消息的 tool_use_id 都在紧邻前一条 assistant.ToolCalls.ID 里(任何违反 → 400,turn 废)。

17. **★ `Agent.MemoryFacts` 签名无 sessionID**:Agent 是 per-session 单例,`a.session.ID` 就是当前 session 上下文。函数签名 `MemoryFacts(ctx)` 不接 sessionID,避免 caller 误传别的 session ID(`memory.Fact` 跨包映射规则见 FR-11:`memory.SearchResult` → `ctxengine.Fact{Content, Source}`)。

18. **★ MEMORY FTS TTL cache key = `(sessionID, query)`**:query 动态变(基于最近 3 条 user msg),key 只看 sessionID 会返 stale data(最近 60s 内 query 变了但 cache 仍返上一次的 FTS hits)。cache 命中率因此降低,但正确性优先。
