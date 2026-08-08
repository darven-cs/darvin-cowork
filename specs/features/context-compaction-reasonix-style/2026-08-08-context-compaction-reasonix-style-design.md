# Context 压缩拉到 Reasonix 风格

## 1. 概述

### 1.1 问题 / 背景

当前 `darvin-cowork` 的 ctxengine 压缩(`internal/agents/ctxengine/compact.go` + `assemble.go`)在 2026-08 用户报告 400 bug 后已经修了 tool_use/tool_result 原子约束(见 `specs/features/context-compaction-persistence/2026-08-07-...-design.md` §3 FR-4),但**整个压缩管线跟 Reasonix 主仓(`internal/agent/compact.go`)对比还有 6 处明显差距**,导致:

| 维度 | 当前实现 | 后果 |
|---|---|---|
| 1. 触发阈值 | `token_budget: 0`(默认值),`tokensBefore > budget` → 有 token 就触发 | 默认配置下**第一个非空 turn 就自动压**,实际跟 config.yaml 注释「0 = no budget check (executor fallback)」完全不符 — 注释承诺禁用,实现实际启用 |
| 2. 触发模型 | 单一阈值(绝对 token 数) | 没有软通知(50%)、没有廉价 snip 路径(60%)、没有 force 阈值(90%) — Reasonix 三层级联 |
| 3. 摘要 prompt | 通用一句话("preserve tool inputs/outputs, decisions, and context") | 没有针对 coding agent 的结构化 briefing;LLM 输出不可预测,后续 hydrate 解析困难 |
| 4. re-compact loop 防御 | **完全没有** | 窗口太小(`token_budget` 设过低)时每 turn 都压,fold/tail 来回切,既慢又费钱且丢信息 |
| 5. 失败处理 | `Compact` 返 `Success=false`,caller 回退原 messages | 摘要 LLM 调用一旦失败,自动压完全作废,会话持续膨胀直到 LLM 上游 400 |
| 6. Archive 留底 | **完全没有** | 被压消息直接消失,出 bug / 模型 hallucinate 时无法回放查证 |

Reasonix 在 `internal/agent/compact.go` 已经把这些都处理掉(2026-08 实地对照),本 spec 把同样能力搬过来,并按 darvin-cowork 现有的「digest 走独立 `session_digests` 表」(spec §3 FR-2)做适配。

### 1.2 目标

1. **修默认配置语义**:`token_budget: 0` 真正等于「关闭 auto-compact」,而不是「任何内容都触发」。
2. **加三层级联触发**:50% 软通知 / 60% 廉价 snip / 80% 调摘要 — 沿用 Reasonix 比例模型,绝对 token 数仍作为降级。
3. **换 Reasonix 7 段结构化 briefing prompt**:针对 coding agent 场景,7 段 heading 强制结构。
4. **加 re-compact loop 防御**:连续 2 turn 都压 → 暂停自动压并发 Notice。
5. **加机械折叠 fallback**:摘要 LLM 失败时不再 `Success=false`,改用 `mechanicalFoldDigest` 占位 digest。
6. **加 archive 留底**:被压原消息写到 `<archiveDir>/<timestamp>.jsonl`,debug 时可回放。

### 1.3 非目标

- 不改 `partitionFold` 的 pair-aware 逻辑(已经修过,见 context-compaction-persistence §FR-4)
- 不改 `alignTailBoundary`(同上)
- 不动 `session_digests` 表结构 / hydrate 流程
- 不引入 LLM judge 评估摘要质量(memory-core 后续独立 spec)
- 不动 `executor` / `agent.Agent` 接口,只动 ctxengine 内部 + 新增 archive 落盘

---

## 2. 用户场景

### 场景 1:默认配置启动新会话,自动压关闭

**Given** `config.yaml` 里 `token_budget: 0`,用户起一个新会话
**When** 用户输入「你好」 → agent 回 5 句话 → 用户继续聊到 200 turn
**Then**
- 整个会话期间**不触发任何自动压**(turn 1 也不触发)
- 调 `Assemble` 时 `budget = 0` → 跳过 compact 路径(`assemble.go` 加 budget==0 直接 return)
- 长会话下如果用户主动 `/compact`,手动压仍正常工作
- DevTools 看 `agent.event` 流里没有 `compaction` 事件

### 场景 2:窗口快满时(50%)用户看到软通知,cache 优先

**Given** `token_budget: 32000`(默认 200k 窗口下占 16%),会话已经积累 ~16k token(promptTokens=10000 占 5%),用户没主动操作
**When** 下一 turn 走完,promptTokens 升到 11000(窗口 55%)
**Then**
- `assemble.go` 检测到 `50% ≤ tokens/window < 60%`,触发**软通知**
- emit `Notice` 事件(`event.NoticeKind`),UI renderer 显示「context is getting large; preserving cache until cleanup is needed」
- **不调 LLM 摘要**,cache 命中 prefix 保持完整
- 同一窗口内只通知一次(用 `softCompactNotified` flag 防刷屏)

### 场景 3:廉价 snip 路径(60%)剪过期 tool result,不调摘要

**Given** 同场景 2 配置,promptTokens 升到 12500(62.5%)
**When** 下一 turn 走完
**Then**
- `assemble.go` 检测到 `60% ≤ tokens/window < 80%`,跑 `SnipStaleToolResults()`
- 该函数扫描 fold 区间,把**超过 60% tool_result_max_bytes** 的 tool result 截短(类似 `toolResultSnipRatio`)
- **不调 LLM 摘要**,只做机械截断
- emit `Notice` 事件「snipped N stale tool results (~X tokens) before compaction」
- 如果 snip 后 tokens 降到 80% 以下 → 这一 turn 跳过摘要,等下一 turn 再看

### 场景 4:80% 触发调 LLM 摘要

**Given** 同配置,promptTokens 升到 16500(82.5%)
**When** 下一 turn 走完
**Then**
- `assemble.go` 检测到 `tokens/window ≥ 80%`,调 `Compact(...)`
- `Compact` 走 pair-aware `partitionFold` → 算 fold/tail → `SummarizeRequest` 走 Reasonix 7 段 prompt
- 摘要返回后 `newMessages = pinned + kept + summary + tail`,走 pair-aware `alignTailBoundary` 校验
- 成功后 `Session().ReplaceAll(newMessages)` + `PersistCompaction`
- emit `CompactionEvent`(已存在,无需新事件)

### 场景 5:窗口太小,re-compact loop 防御触发

**Given** 用户把 `token_budget` 误设成 `800`(200k 窗口里 ~0.4%),system prompt + 一条 turn 就已经超过 budget
**When** 第 1 turn 触发压 → 摘要 + tail 还是 > 800 token → 第 2 turn 仍然 > 800 → 又触发压
**Then**
- `ctxengine` 内部维护 `consecutiveCompacts` 计数器
- 连续 2 turn 都调过 `Compact` → 设 `compactStuck = true`
- `assemble.go` 检测 `compactStuck == true` → **跳过自动压**,直接返回原 messages
- emit `Notice` 事件「automatic context cleanup paused; raise token_budget or shrink tool output」
- 用户手动把 `token_budget` 调到合理值(例如 30000)→ 自动压恢复

### 场景 6:摘要 LLM 失败,机械折叠兜底

**Given** 触发 80% 压,走到 `summarizer.Summarize(ctx, fold, ...)` 这一步
**When** LLM 上游 503 / 429 / 超时 / 流中断,`Summarize` 返回 error
**Then**
- `Compact` **不再**返 `Success=false`,改用 `mechanicalFoldDigest(N)` 生成占位 digest:
  > "N earlier message(s) were folded here to free context, but the automatic summary was unavailable. Ask the user if you need details from before this point."
- 占位 digest 写入 `RetainedMessages`,走完 `ReplaceAll` + `PersistCompaction`
- emit `Notice` 事件「Context was compacted without a generated summary」
- 会话**不会**因为摘要失败而持续膨胀
- fold 的原消息**已经写到 archive**(见场景 7),后续可人工查证

### 场景 7:被压消息 archive 留底,可回放

**Given** 任何 Compact 成功 / 机械折叠成功
**When** Compact 走完
**Then**
- 在 archive 目录(`<sessionStoreDir>/sessions/<sid>/archive/` 默认)写 `<YYYYMMDD-HHMMSS.NNN>.jsonl`
- 每行一个原 `Message`(JSON 序列化),覆盖 fold 区间
- 写盘失败时 emit `Notice`「archive write failed」,**不阻塞** compact 主流程(archive 是 best-effort)
- debug 时可手动 `cat archive/20260808-153045.000.jsonl | jq .` 回放
- 同一 session 的 archive 文件不会被自动清理(留给后续 spec 做 retention policy)

---

## 3. 功能需求

### FR-1:`token_budget=0` 真正关闭自动压

`ctxengine.assemble.go:27-58` 的 budget 回退逻辑改为:

```go
budget := p.ToolBudget
if budget == 0 {
    budget = cfg.TokenBudget
}
if budget <= 0 {
    // 0 = 关闭自动压 — 跳过整段 compact 路径
    return AssembleResult{...no compact stats...}
}
// 否则正常走 tokensBefore > budget 检查
```

`cfg.TokenBudget=0` 且 `p.ToolBudget=0`(默认值情况) → 整段 `Compact` 路径不进入。`config.yaml` 注释「0 = no budget check」与实际行为对齐。

**额外**:如果 budget > 0 但 token 估算用 0(`LastUsage.PromptTokens=0` 且 msgs 空),`Assemble` 返回空 messages + 0 tokens,预算检查自然不触发。无需额外 guard。

### FR-2:三层级联触发(soft → snip → compact)

`ctxengine.assemble.go` 新增 `AssembleStats` 字段 + 三态决策:

```go
type AssembleStats struct {
    TruncatedTools      int
    TruncatedBytes      int64
    CompactionTriggered bool

    // 新增
    SoftNoticeEmitted   bool   // 50%~60% 软通知已发
    SnipTriggered       bool   // 60%~80% snip 路径触发
    PausedReCompactLoop bool   // re-compact loop 暂停
}
```

`Assemble` 主流程改为:

```go
if cfg.ContextWindow > 0 {
    ratio := float64(tokensBefore) / float64(cfg.ContextWindow)
} else {
    // 降级:用绝对 token
    ratio := float64(tokensBefore) / float64(budget)
}

switch {
case ratio >= 0.8:
    // 触发摘要
    compactRes := a.Compact(...)
case ratio >= 0.6 && !a.snippedThisTurn:
    // 廉价 snip
    snipped, err := a.SnipStaleToolResults(ctx, msgs, budget)
    if err == nil && snipped.Count > 0 {
        stats.SnipTriggered = true
        a.deps.Emit(event.Notice{...})
        a.snippedThisTurn = true
    }
case ratio >= 0.5 && !a.softNotified:
    // 软通知
    a.deps.Emit(event.Notice{...})
    stats.SoftNoticeEmitted = true
    a.softNotified = true
}
```

**`ContextWindow` 配置**:在 `ctxengine.Config` 加 `ContextWindow int` 字段,从 `cfg.Agent.ContextWindow`(config.yaml 新增)读。`ContextWindow=0` → 降级用绝对 token + budget,行为跟今天一致。

**新增 `cfg.Agent.SoftCompactRatio / ToolResultSnipRatio / CompactRatio / CompactForceRatio`**(默认 0.5 / 0.6 / 0.8 / 0.9)。`Config` 加对应字段透出。

### FR-3:Reasonix 7 段结构化 briefing prompt

`ctxengine/compact.go:DefaultSummarizer.Summarize` 的 system prompt 替换:

```go
const summarySystemPrompt = `You are compacting the earlier part of a coding agent's conversation to save context.
The agent keeps your summary alongside the user's own turns (kept verbatim) and the recent tail; your job is to fold the assistant/tool work into a briefing it can resume from.
Write under these exact headings, omitting a heading only if it has no content:

## Standing facts & constraints
Everything the user stated that still governs the work — names, paths, IDs, versions, tokens, preferences, and hard "never do X" rules — in their own words. Be exhaustive; this is the durable contract, so prefer over- to under-including.

## Goal
The user's request and intent.

## Decisions & rationale
Key choices made so far and why — so they are not re-litigated or reversed.

## Files & code
Files read or modified, with the specific facts that matter: signatures, line locations, data shapes, and exact edits applied. Be concrete; this is what lets the agent act without re-reading everything.

## Commands & outcomes
Commands run (builds, tests, git) and their relevant results — what passed, what failed, and the error text that matters.

## Errors & fixes
Problems hit and how they were resolved (or not), so the same dead ends are not repeated.

## Pending & next step
What is still in progress or unstarted, and the single most concrete next action to take.

Rules: be terse — bullet points and fragments, not prose. Preserve identifiers, paths, and numbers exactly. Do NOT invent anything not present in the messages; if something is unknown, leave it out rather than guessing.`
```

替换原 `compact.go:225` 的一行通用 prompt。**保留 `req.Hint` 拼接逻辑**,hint 默认值从 `"conversational summary; preserve tool input/output facts and decisions"` 改为 `"preserve identifiers, paths, and numbers exactly"`。

`DefaultSummarizer` 改用 **`Stream` 而非 `Complete`**(对齐 Reasonix `summarize` 用 `prov.Stream`),超时 90s。原因:长 fold 摘要容易撞上 `Complete` 的 token 上限,流式更稳。

### FR-4:re-compact loop 防御(`compactStuck` latch)

`DefaultAssembler` 新增私有字段:

```go
type DefaultAssembler struct {
    // ... 已有 ...
    mu                      sync.RWMutex
    consecutiveCompacts     int      // 连续 Compact 调用次数
    compactStuck            bool     // latch:触发后暂停直到 prompt 降到阈值下
    softNotified            bool     // 50% 通知防刷屏
    snippedThisTurn         bool     // 60% snip 当 turn 只 snip 一次
}
```

`Compact` 入口加 latch:

```go
func (a *DefaultAssembler) Compact(ctx, p) CompactResult {
    a.mu.Lock()
    if a.compactStuck {
        a.mu.Unlock()
        return CompactResult{
            Success:          false,
            RetainedMessages: p.Messages,
            TokensBefore:     estimateMessages(p.Messages),
            TokensAfter:      estimateMessages(p.Messages),
            Reason:           "compact_paused_stuck",
        }
    }
    a.mu.Unlock()

    // ... 原有 Compact 逻辑 ...

    if result.Success {
        a.mu.Lock()
        a.consecutiveCompacts++
        if a.consecutiveCompacts >= 2 {
            a.compactStuck = true
            if a.deps != nil {
                a.deps.Emit(event.Notice{
                    Kind: event.NoticeStuck,
                    Text: "Automatic context cleanup paused because token_budget is too small for compaction to help (the system prompt plus one turn already exceeds the budget); raise token_budget or shrink tool output. Auto-compaction paused until the prompt drops.",
                })
            }
        }
        a.mu.Unlock()
    } else {
        a.mu.Lock()
        a.consecutiveCompacts = 0  // 失败不算连续
        a.mu.Unlock()
    }
}
```

**`Assemble` 加 clear latch**:每次 `Assemble` 入口,如果 `tokensBefore < budget * 0.8`(健康区),清 `consecutiveCompacts=0` + `compactStuck=false`。让"压一次降下来"的下个 turn 能正常触发。

**`NewDefaultAssembler` 初始化**:`consecutiveCompacts=0`、`compactStuck=false`、`softNotified=false`、`snippedThisTurn=false`(进程级默认;每个 session agent 一个 assembler)。

### FR-5:机械折叠 fallback(`mechanicalFoldDigest`)

`compact.go` 加 helper:

```go
// mechanicalFoldDigest is the deterministic stand-in used when the
// summarizer is unreachable: the foldable region is already archived, so
// the digest just notes the gap and points the model at the user for
// anything it needs from before it.
func mechanicalFoldDigest(n int, archive string) string {
    where := "."
    if archive != "" {
        where = " (archived to " + archive + ")."
    }
    return fmt.Sprintf("%d earlier message(s) were folded here to free context, but the automatic summary was unavailable%s Ask the user if you need details from before this point.", n, where)
}
```

`Compact` 现有 `summarizer.Summarize(...)` 失败分支改为:

```go
summaryText, err := summarizer.Summarize(...)
if err != nil {
    a.mu.Lock()
    a.consecutiveCompacts = 0  // 失败不计入连续计数
    a.mu.Unlock()
    if a.deps != nil {
        a.deps.Emit(event.Notice{
            Kind: event.NoticeMechanicalFold,
            Text: "Context was compacted without a generated summary.",
            Detail: "compaction summary unavailable (" + err.Error() + "); folded mechanically",
        })
    }
    summaryText = mechanicalFoldDigest(len(fold), archivedPath)
}

// 后面走原有的 newMessages 构造,summaryText 当成正常摘要继续用
```

**Compact 不再返 `Success=false` 因 summarizer 失败**。其他失败(ctx cancel、partition 空)仍返 `Success=false`。

### FR-6:Archive 留底

**新增模块**:`internal/agents/ctxengine/archive.go` + `archive_test.go`。

```go
package ctxengine

// Archiver writes dropped original messages to a timestamped jsonl under
// dir, returning the file path. Returns "" if dir is empty (archive
// disabled). Best-effort: any error returns "" + emits a Notice via
// emitter (if non-nil); compaction continues regardless.
type Archiver interface {
    // Archive returns the jsonl path written, or "" on disabled / failure.
    Archive(ctx context.Context, msgs []protocol.Message) (path string, err error)
}

type FileArchiver struct {
    mu     sync.Mutex
    dir    string  // empty = disabled
    emit   func(event.Event)  // optional Notice sink for archive failures
}

func NewFileArchiver(dir string, emit func(event.Event)) *FileArchiver {
    return &FileArchiver{dir: dir, emit: emit}
}

func (a *FileArchiver) Archive(ctx context.Context, msgs []protocol.Message) (string, error) {
    if a.dir == "" {
        return "", nil
    }
    if err := os.MkdirAll(a.dir, 0o755); err != nil {
        return "", err
    }
    name := time.Now().Format("20060102-150405.000") + ".jsonl"
    path := filepath.Join(a.dir, name)

    a.mu.Lock()
    defer a.mu.Unlock()

    f, err := os.Create(path)
    if err != nil {
        return "", err
    }
    defer f.Close()

    enc := json.NewEncoder(f)
    for _, m := range msgs {
        if err := enc.Encode(m); err != nil {
            return path, err
        }
    }
    return path, nil
}
```

**`DefaultAssembler` 持 `Archiver` 字段**;`NewDefaultAssembler` 接收 `Archiver` 参数(或后续 setter)。`Compact` 在调用 `Summarize` 前先 `archive.Archive(ctx, fold)`:

```go
archived := ""
if a.archiver != nil {
    if p, err := a.archiver.Archive(ctx, fold); err != nil {
        if a.deps != nil {
            a.deps.Emit(event.Notice{Text: "archive write failed", Detail: err.Error()})
        }
    } else {
        archived = p
    }
}
```

**Archive 路径**:从 `cfg.Agent.ArchiveDir`(新增 config 字段)读,默认空(禁用)。生产配置建议:

```yaml
archive_dir: "<stateRoot>/sessions/<sid>/archive"
```

`runtime.Build` 时根据 `sessionStoreDir` + `sessionID` 拼出路径注入到 assembler。

---

## 4. 实现方案

### 4.1 模块拆分

| 模块 | 改动 |
|---|---|
| `internal/agents/ctxengine/config.go`(已存在 `Config`) | 新增 `ContextWindow / SoftCompactRatio / ToolResultSnipRatio / CompactRatio / CompactForceRatio / ArchiveDir` |
| `internal/agents/ctxengine/assemble.go` | 重写 budget 决策;加 3-tier switch;新增 `stats.SoftNoticeEmitted / SnipTriggered / PausedReCompactLoop`;加 `clearStuckLatch` 逻辑 |
| `internal/agents/ctxengine/compact.go` | 替换 prompt 常量;`DefaultSummarizer` 改用 `Stream` + 90s 超时;加 `mechanicalFoldDigest`;调 `Archive`;改 fail 路径用机械折叠而非 `Success=false`;加 `compactStuck` latch |
| `internal/agents/ctxengine/assembler.go` | `DefaultAssembler` 增 4 个 mutex-protected 字段 + 3 个 setter(`SetArchiver / SetContextWindow / SetRatios`);`NewDefaultAssembler` 签名不变(向后兼容,新字段用 setter) |
| `internal/agents/ctxengine/archive.go` | **新增** `Archiver` 接口 + `FileArchiver` 实现 |
| `internal/agents/ctxengine/archive_test.go` | **新增** roundtrip / 并发写不同名 / 禁写目录 / 失败容错 |
| `internal/agents/agent.go` | `New` 时如果 cfg.ArchiveDir 非空,构造 `FileArchiver` 注入 assembler;透传 `ContextWindow` 等新 config 字段 |
| `internal/runtime/agent_config.go` | 透传新字段到 `agent.Config` / `executor.Config` / `ctxengine.Config` |
| `internal/config/config.go` | `AgentConfig` 加 `ContextWindow / SoftCompactRatio / ToolResultSnipRatio / CompactRatio / CompactForceRatio / ArchiveDir` 字段(mapstructure tag) |
| `src/darvin-agent/config.yaml` | 注释修正「token_budget=0」实际语义;新增 5 个 ratio 默认值 + `context_window: 0`;`archive_dir: ""` |
| `internal/agents/event/event.go` | `NoticeKind` 加 `NoticeSoftCompact / NoticeSnipStaleTools / NoticeMechanicalFold / NoticeStuck`(或一个 `NoticeCompact` 总类,4 个 detail 字段) |
| `internal/agents/event/notice_test.go`(或合并到现有) | 加新 kind 的 emit 路径单测 |

### 4.2 关键设计决策

**D1**:为什么把 `token_budget=0` 当作「关闭」而不是「永远触发」?
- Reasonix 的语义:`compactRatio=0.8` × `contextWindow`。contextWindow=0 时跳过整段(`maybeCompact` 第一行 `if a.contextWindow <= 0` 直接 return)。darvin-cowork 把 budget 当绝对值,类比过来 budget=0 也应该关闭。
- 修复后行为:用户想触发 → 设 `token_budget: 30000`(或类似);不想触发 → 保持 0。
- 跟 spec FR-1 一致。

**D2**:为什么三层级联而不是单阈值?
- 50% 通知是「cache 优先」策略:压一次会破坏 cache prefix,LLM 调用会失去 cache 命中,延迟 + 成本涨。50% 时先通知但不压,等真要压(80%)再压,中间能保多少保多少。
- 60% snip 是「廉价修剪」:`SnipStaleToolResults` 只是字符串截断 + 重写 `tool_result_max_bytes`,**不调 LLM**,成本极低。能在 80% 前先压一波再决定要不要 LLM。
- 80% compact 才是「贵操作」:调 LLM 摘要,有延迟有 token 开销。

**D3**:为什么 7 段 prompt?
- 通用 prompt 让 LLM 输出不可预测的散文,后续 UI 解析 / agent 再次理解都难。
- 7 段 heading 是 Reasonix 验证过的形态,针对 coding agent 场景:facts / goal / decisions / files / commands / errors / pending — 后续 turn 接续时,模型能直接从对应 heading 拿信息。
- "Rules: be terse — bullet points and fragments, not prose" 强制不写散文,降低 token 成本 + 提高信噪比。

**D4**:为什么 `mechanicalFoldDigest` 不再 `Success=false`?
- 旧行为:LLM 摘要失败 → 返 `Success=false` → executor 用原 messages 继续 → 下一 turn 还是超 budget → 又触发 → 又失败 → 死循环(每次都付一次 LLM 调用 + 失败开销)
- 新行为:LLM 摘要失败 → 用占位 digest 顶上 → 至少腾了 fold 占的空间 → 下一 turn 不会立刻再触发 → 后续 LLM 恢复后下一次 Compact 重新生成 digest(累积,FR-2 的 Reasonix 式 digest 链)
- 占位 digest 文本明确告诉模型「前面的细节丢了,问用户」,不让模型 hallucinate。

**D5**:Archive 路径默认禁用(`ArchiveDir=""` 跳过),不强制启用
- 强制启用会污染所有用户的 state 目录;debug 工具属性,显式 opt-in
- archive 写失败 → emit Notice 但**不阻塞** compact(best-effort)

**D6**:`compactStuck` latch 的清空时机
- 触发:`consecutiveCompacts >= 2` → stuck=true
- 清空:`Assemble` 入口如果 `tokensBefore < budget * 0.8` → 清 stuck + consecutiveCompacts=0
- 为什么不每次都清:避免 latch 失效时(刚压完又压)的回弹窗口太小;只在「明显回到健康区」才清

**D7**:`softNotified` / `snippedThisTurn` 是 per-assembler 状态
- 同一 session 的同一 agent 一个 assembler 实例,这两个 flag 是进程级(per-agent)
- 一次窗口增长周期内只通知一次 / 只 snip 一次 → 不刷屏

### 4.3 关键伪代码

`compact.go` 主体:

```go
func (a *DefaultAssembler) Compact(ctx context.Context, p CompactParams) CompactResult {
    // latch
    a.mu.Lock()
    if a.compactStuck {
        a.mu.Unlock()
        return CompactResult{Success: false, RetainedMessages: p.Messages, TokensBefore: estimateMessages(p.Messages), TokensAfter: estimateMessages(p.Messages), Reason: "compact_paused_stuck"}
    }
    a.mu.Unlock()

    if err := ctx.Err(); err != nil {
        return CompactResult{Success: false, RetainedMessages: p.Messages, Reason: p.Reason}
    }

    // ... read cfg / summarizer / modelName ...

    // 预算检查
    if !p.Force && tokensBefore <= p.Budget {
        // no-op success
    }

    if summarizer == nil {
        return CompactResult{Success: false, RetainedMessages: p.Messages, Reason: p.Reason}
    }

    pinned, kept, fold := partitionFold(p.Messages)

    tail := ... // CompactTailKeep / CompactTailTokens 算
    tail = alignTailBoundary(p.Messages, tail)

    if len(fold) == 0 {
        return CompactResult{Success: false, ...}
    }

    // FR-6: archive 留底(在 LLM 调用前)
    archived := ""
    if a.archiver != nil {
        if path, err := a.archiver.Archive(ctx, fold); err == nil {
            archived = path
        } else if a.deps != nil {
            a.deps.Emit(event.Notice{Text: "archive write failed", Detail: err.Error()})
        }
    }

    summaryText, err := summarizer.Summarize(ctx, SummarizeRequest{...})
    if err != nil {
        // FR-5: 机械折叠兜底
        if a.deps != nil {
            a.deps.Emit(event.Notice{Kind: NoticeMechanicalFold, Text: "Context was compacted without a generated summary.", Detail: "compaction summary unavailable (" + err.Error() + "); folded mechanically"})
        }
        summaryText = mechanicalFoldDigest(len(fold), archived)
    }

    summaryMsg := protocol.Message{...}
    newMessages := ...

    // 失败 latch 更新
    if realSummaryErr != nil {
        // 不计入连续
        a.mu.Lock()
        a.consecutiveCompacts = 0
        a.mu.Unlock()
    } else {
        a.mu.Lock()
        a.consecutiveCompacts++
        if a.consecutiveCompacts >= 2 {
            a.compactStuck = true
            a.deps.Emit(event.Notice{...})
        }
        a.mu.Unlock()
    }

    return CompactResult{Success: true, ...}
}
```

### 4.4 涉及事件

`event.NoticeKind`(若已存在)或新加 `NoticeCompaction` 一类,4 个 detail:

```go
const (
    NoticeSoftCompact     NoticeKind = "compact_soft"          // 50% 通知
    NoticeSnipStaleTools  NoticeKind = "compact_snip_stale"    // 60% snip
    NoticeMechanicalFold  NoticeKind = "compact_mechanical"    // 摘要失败兜底
    NoticeStuck           NoticeKind = "compact_stuck"         // re-compact 暂停
)
```

renderer 端把 4 种 Notice 渲染为不同的 info/warn 卡片(后续 UI 改动不在本 spec 范围)。

---

## 5. 边界情况

| 场景 | 处理 |
|---|---|
| `token_budget=0` 且 `ToolBudget=0`(默认配置) | FR-1:直接 return,不进 compact 路径 |
| `ContextWindow=0`(未配置窗口) | FR-2 降级:用 `budget` 当 100% 阈值;soft/snip/compact 三态用 `tokensBefore/budget` 比例 0.5/0.6/0.8 |
| `ContextWindow>0` 且 `budget>0` 都有 | 优先用 `ContextWindow` 的比例(Reasonix 式);`budget` 仅作为绝对值下界(`tokensBefore > budget` 任一即触发) |
| `force=true` 调用 `Compact` | 跳过 latch(Reasonix `if !force && !foldEconomics`),照常压缩 |
| LLM 摘要超时(90s) | FR-5:走机械折叠 |
| Archive 目录无写权限 | FR-6:Notice 但不阻塞 compact |
| Archive 目录为空(默认) | FR-6:`Archiver.Archive` 直接返 "",跳过写盘 |
| 第一条 turn 就触发(用户 budget 设太小) | FR-4:第一次 Compact 成功 → `consecutiveCompacts=1`;第二次还是 Compact → `consecutiveCompacts=2` → stuck |
| `consecutiveCompacts` 已 stuck,用户手动 `/compact` | 手动调用不走 `Assemble` 的 stuck 检查(走 `gateway/handlers.go runManualCompact`),允许 |
| 同 session 多个并发 Assemble | `consecutiveCompacts` / `compactStuck` 走 `a.mu.Lock()`,per-assembler 串行 |
| SnipStaleToolResults 报错 | emit Notice,不阻塞;下一 turn 再试 |
| `DefaultSummarizer` 切到非 Anthropic provider(OpenAI / DeepSeek) | Stream + prompt 都 provider-agnostic,无需特殊处理 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `internal/agents/ctxengine/config.go` | `Config` 加 `ContextWindow / SoftCompactRatio / ToolResultSnipRatio / CompactRatio / CompactForceRatio / ArchiveDir` |
| `internal/agents/ctxengine/assemble.go` | FR-1/FR-2:重写 budget 决策;3-tier switch;clearStuckLatch |
| `internal/agents/ctxengine/assembler.go` | `DefaultAssembler` 加 4 mutex 字段 + Setter;`AssembleStats` 加 3 bool |
| `internal/agents/ctxengine/compact.go` | FR-3:换 `summarySystemPrompt`;`DefaultSummarizer` 改 Stream + 90s;FR-4:`compactStuck` latch;FR-5:`mechanicalFoldDigest`;FR-6:调 `archiver.Archive` |
| `internal/agents/ctxengine/archive.go` | **新增** `Archiver` 接口 + `FileArchiver` |
| `internal/agents/ctxengine/archive_test.go` | **新增** roundtrip / 并发 / 失败 / 禁用 |
| `internal/agents/ctxengine/compact_test.go` | 加 FR-5 机械折叠测试 / FR-4 stuck latch 测试 |
| `internal/agents/ctxengine/assemble_test.go` | 加 FR-1 budget=0 不触发 / FR-2 三层级联测试 |
| `internal/agents/ctxengine/tokens.go` | 加 `SoftCompactRatio / ToolResultSnipRatio / CompactRatio / CompactForceRatio` 常量(默认 0.5/0.6/0.8/0.9) |
| `internal/agents/event/event.go` | `NoticeKind` 加 4 个常量 |
| `internal/agents/agent.go` | `New` 时构造 `FileArchiver` 注入;透传 ContextWindow / 比例 |
| `internal/runtime/agent_config.go` | 透传新字段到各层 Config |
| `internal/config/config.go` | `AgentConfig` 加 mapstructure 字段 |
| `src/darvin-agent/config.yaml` | 修正 `token_budget` 注释;新增 5 个 ratio + `context_window` + `archive_dir` 默认 |
| `internal/agents/ctxengine/params.go` | `CompactResult` 加 `Reason` 枚举("compact_paused_stuck" / "budget_exceeded" / "manual" 等) |

---

## 7. 验收标准

### 用户场景

- [ ] 场景 1:默认 `token_budget: 0` 配置,200 turn 不触发自动压;调 `Assemble` 返 `Budget=0,CompactionTriggered=false`
- [ ] 场景 2:token 升到 50%~60% 区间,emit `NoticeSoftCompact` 一次;同区间内后续 turn 不重复发
- [ ] 场景 3:token 升到 60%~80% 区间,emit `NoticeSnipStaleTools` + tool result 截短;**不调 LLM**
- [ ] 场景 4:token 升到 ≥80%,emit `NoticeMechanicalFold` 不发、调 LLM 摘要、返回 `Success=true`、Session.ReplaceAll + PersistCompaction 走通
- [ ] 场景 5:连续 2 turn 都压 → 第 3 turn 跳过 emit `NoticeStuck`、自动压暂停;budget 调高后恢复
- [ ] 场景 6:LLM 摘要 503/超时 → emit `NoticeMechanicalFold`,走 `mechanicalFoldDigest`,Session 仍替换,Compaction 返 `Success=true`
- [ ] 场景 7:任何 Compact 成功 / 机械折叠 → `<archiveDir>/<timestamp>.jsonl` 出现,内容是 fold 原消息的 JSON Lines;archive 失败不阻塞

### 自动化

- [ ] `cd src/darvin-agent && go test -count=1 ./...` 全绿
- [ ] `go test -race ./internal/agents/ctxengine/...` 全绿
- [ ] `go vet ./...` 无警告
- [ ] `npm run lint` 通过(若有 renderer / TS 改动)
- [ ] 新增单测覆盖:
  - `assemble_test.go` — `TestAssemble_BudgetZero_NoCompact`(FR-1)、`TestAssemble_3Tier_SoftNotice`(FR-2)、`TestAssemble_3Tier_Snip`(FR-2)、`TestAssemble_3Tier_Compact`(FR-2)、`TestAssemble_ClearStuckLatch_AfterBudgetDrop`
  - `compact_test.go` — `TestCompact_StuckLatch_AfterTwoConsecutive`(FR-4)、`TestCompact_MechanicalFold_OnSummarizerError`(FR-5)、`TestCompact_StreamPrompt_Contains7Headings`(FR-3)、`TestCompact_ArchiveWritesJsonl`(FR-6)
  - `archive_test.go` — `TestFileArchiver_WritesJsonl` / `TestFileArchiver_DisabledWhenDirEmpty` / `TestFileArchiver_ConcurrentWritesDistinctFiles` / `TestFileArchiver_FailureDoesNotPanic`

### 手动验证

- [ ] `npm run build:agent && npm start` 起应用
- [ ] DevTools console 看 `agent.event` 流,验证 4 种 `Notice*` 事件的 `kind` 字段对应正确
- [ ] DevTools network 看 LLM 请求:触发 80% compact 时,Summary LLM 请求的 `system` 字段包含 7 段 heading 文本(FR-3)
- [ ] 长会话触发 archive 后,`ls <stateRoot>/sessions/<sid>/archive/` 看到 `<timestamp>.jsonl`;`cat` + `jq` 能看到原始 `Message.Role / Content / ToolCallID`
- [ ] 故意把 `token_budget` 设成 `1` 触发 stuck;DevTools 看到 `NoticeStuck`;改回 `30000` 后下一 turn 看到正常 `CompactionEvent`
- [ ] 手动压(`/compact` IPC)在 stuck latch 触发后仍正常工作(走 `runManualCompact`,不走 `Assembler.Assemble`)

### 不在验收范围

- 摘要质量的 LLM judge 评估(memory-core 后续 spec)
- Archive 文件 retention policy / 自动清理(后续 spec)
- 摘要 prompt 的多语言支持 / 用户自定义(后续 spec)
- DAG / SubAgent 路径(本 spec 不涉及)
- Renderer 端 Notice 卡片的具体 UI(后续 UI 改动不在本 spec)

---

## 8. 注释规范(实现期约束)

> 本节只约束实现期改动的源码文件(`.ts` / `.vue` / `.go`)中的注释写法。本 spec 文档**不在约束范围** — 设计阶段允许阶段、版本、迭代规划内容(对齐仓库根 `CLAUDE.md`「注释规范」首段)。

### 8.1 绝对禁止的注释(出现即违规,必须删除)

- **阶段、版本、迭代规划类注释**:`// S1/S5 阶段实现` / `// v0 占位,v1 重构` / `// 后续迭代替换此处逻辑` / `// 未来会接入 MCP 协议`
- **代码复述型废话注释**:代码已经写了 `if (!path) return undefined`,不许加 `// 如果路径不存在就返回空`
- **模型思考、编写过程、改动说明注释**:`// 按照规范调整写法` / `// 适配项目架构修改逻辑` / `// 根据 CLAUDE.md 约束重构`
- **展望、TODO 大范围规划注释**:禁止大面积罗列后续开发路线、架构演进内容(仅极小范围内部 `TODO(maybe)` 标记可保留)
- **无关铺垫、开场白、收尾总结注释**:代码块前后不要加 `// 下面实现 X 逻辑` / `// 以上完成 X 封装` 这类首尾解说
- **冗余分隔线、空行堆砌注释**:不要用 `// --------------------------` 分割代码区块

### 8.2 仅允许保留的注释场景

- **导出公共函数 / 类型 / 类**:JSDoc 注释,仅标注入参含义、返回值、边界约束、业务不变量;简洁短句,不啰嗦,不写 `@example`。
- **非常规特殊逻辑**:单行意图注释,一句话说明**为什么这么写**(而非做了什么)— 业务硬性约束、违背常规写法、容易被误改时加。
- **硬性架构约束校验**:`// 事件结构必须对齐 DarvinEvent 类型定义` 类硬性同步约束。
- **关键边界兜底逻辑**:异常兜底、平台差异化兼容逻辑可简短标注。

### 8.3 通用格式要求

- 单行注释用 `//`(`// ` 空格分隔),放在代码上方;**不用**行内注释(`x := foo() // initialize`)
- JSDoc 精简撰写,不写 `@example` / `@author` / `@since` 等非必要标签
- Vue `<template>` 内完全禁止写 HTML 注释(模板结构语义自解释即可)
- 标识符命名同样适用:**避免** `ErrNotImplementedInV0` / `FixForV2` / `MockS5` 这类把版本号塞进 API 名字的做法
  - 本 spec 内出现 `FR-1` / `FR-2` 等编号用于 spec 章节引用 OK;但 Go 源码里如果新增错误类型,不要命名 `ErrCompactStuckV2` 之类

### 8.4 本 spec 涉及文件的注释预审

| 文件 | 预期新注释 |
|---|---|
| `internal/agents/ctxengine/archive.go` | `FileArchiver.Archive` 顶部一段 JSDoc 说明「best-effort,失败不阻塞」业务不变量;**不写**「archive 模块」「以下实现 archive 逻辑」开场白 |
| `internal/agents/ctxengine/compact.go` | `summarySystemPrompt` 常量上方一行说明来源(Reasonix);`mechanicalFoldDigest` 函数顶部 JSDoc;`compactStuck` latch 解锁条件加一行「为什么」注释 |
| `internal/agents/ctxengine/assemble.go` | 3-tier switch 顶部一行说明三层级联的目的;**不写**「step 1 / step 2」注释 |
| `internal/agents/ctxengine/config.go` | `Config` 新字段保留已有 godoc 风格(简短说明单位 / 默认 / 来源) |

review 时除功能正确性外,按 8.1–8.3 三条逐项扫一遍注释。
