# 上下文压缩持久化设计文档（对齐 Reasonix / LobsterAI）

## 1. 概述

### 1.1 问题 / 背景

当前 darvin-cowork 的上下文压缩（`ctxengine.DefaultAssembler.Compact`）**产物只存在于内存**：

- 自动压缩（`assemble.go:57-78`）：`Compact` 返回的 `RetainedMessages` 只用于**当次 LLM 请求**，不写回 Session、不落库。
- 手动压缩（`handlers.go:527-547` `runManualCompact`）：`a.Session().ReplaceAll(res.RetainedMessages)` 只改内存，summary 消息**不在任何 `persistAssistantMessages` 路径上**。
- `CompactionCheckpoint` 表（`models.go:53-61`）字段齐全（Summary / TokensBefore / TokensAfter / FirstKeptID / CreatedAt），但全仓**无任何 Save/Get 调用**——是死表。

结果：压缩后若重启 / 切换会话重建（我们的 `hydrate.go` 从 `MessageStore.List` 拉回**全部原始消息**），agent 拿回的是**压缩前的完整历史**，压缩白做。DB 永远是完整历史，压缩只是每次请求的临时优化。

### 1.2 参考项目做法

调研两个参考项目（详见上一条结论）：

| 项目 | 核心机制 |
|---|---|
| **Reasonix**（`internal/agent/compact.go`） | 摘要作为 `Role=User` + `<compaction-summary>` 标签写回 Session → `session.Rewrite` → `SaveRewrite` 全量落盘 JSONL；重启 `LoadSession` 原样读回，**不重新压缩**；被压消息归档 `archive/*.jsonl`；尾部按 **token 预算**（16384，封顶窗口 50%） |
| **LobsterAI**（`docs/openclaw-main/packages/agent-core/.../compaction.ts` + `agent-session-compaction.ts`） | 压缩产物存成会话 DAG 的 `CompactionEntry { summary, firstKeptEntryId }` 写进 JSONL/SQLite；重启 `buildSessionContext` 重放 `summary` + `firstKeptEntryId` 起尾部；**原始消息不删**（留在 DAG，仅组装模型上下文时跳过）；尾部 `keepRecentTokens`（默认 20000 token）；摘要支持 `previousSummary` **增量 UPDATE** |

两者的共同点：**压缩产物是会话记录的一部分并持久化，重启后直接复用 summary，不重新压缩；保留尾部用 token 预算而非固定条数。**

### 1.3 目标

1. **summary 落库**：压缩产生的摘要持久化到 DB（复用 `CompactionCheckpoint` 表）。
2. **hydrate 优先读 checkpoint**：重启 / 切会话重建时，加载 `[summary] + 保留尾部`，而非完整历史。
3. **保留尾部改 token 预算**：替代固定 `compact_tail_keep: 6`。
4. **迭代摘要**：压缩时把既有 summary 作为前序上下文传入，增量折叠（digest 累积）。
5. **被压原始消息留表**：不删（LobsterAI 模式），UI / 历史记录仍是完整会话；仅组装模型上下文时按 checkpoint 边界截断。

### 1.4 非目标

- 不做会话 DAG 重构（darvin-cowork 的 Session 是扁平 `[]protocol.Message`，不加 DAG / 分支）。
- 不做独立归档目录（被压消息留 messages 表即可；Reasonix 的 archive/ 归档在本设计阶段不引入）。
- 不换摘要模型（沿用 `DefaultSummarizer`，复用对话 provider）。
- 不处理 `assemble.go` 里 `SystemAddition` 未回填 `req.System` 的既有 gap（另一个 spec 范畴）。

---

## 2. 用户场景

### 场景 1: 手动压缩后重启，上下文保持压缩后

**Given** 长会话手动压缩成功（圆环回落，DB 存了 checkpoint）
**When** 重启应用，重新进入该会话提问
**Then** agent 的 LLM 上下文是 `[summary] + 最近尾部`（约 tail token），不是完整历史

### 场景 2: 自动压缩后切会话再切回，上下文不膨胀

**Given** 会话超预算触发自动压缩，summary 落库
**When** 切到别的会话再切回
**Then** hydrate 读到 checkpoint，加载压缩后的上下文（summary + tail）

### 场景 3: 历史记录仍完整

**Given** 会话经过多次压缩
**When** 在 UI 查看消息历史
**Then** 完整对话记录仍可见（被压消息没删，只是不进 LLM 上下文）

---

## 3. 功能需求

### FR-1: `protocol.Message` 增加持久化边界字段

当前 `protocol.Message`（`types.go:20-35`）无 ID / Timestamp，无法标识"保留尾部从哪条开始"。

- `Message` 增加 `ID string`（可选，零值兼容）：持久化 / hydrate 时填 `MessageRecord.ID`，压缩时用它记录 firstKept 边界。
- `Message` 增加 `Timestamp int64`（unix ms，零值兼容）：dispatcher append 时填，供 token 预算尾部边界。

> 零值兼容保证：不填的路径（单测 / 手工构造）行为不变。

### FR-2: `CompactionCheckpoint` 表承载 summary + firstKept 边界

`models.go:53-61` 现有字段已覆盖 `SessionID / Summary / TokensBefore / TokensAfter / FirstKeptID / CreatedAt`。补充：

- `FirstKeptID` 语义明确为**保留尾部第一条消息的 `MessageRecord.ID`**（当前字段名已匹配，无需改名）。
- 新增 `FirstKeptTimestamp int64`（unix ms）：备用边界，供按 timestamp 过滤（当消息无 ID 时）。
- `CompactionCheckpoint` 保持 PK=ID、`SessionID` index。

### FR-3: 新增 `CompactionStore` 接口 + SQLite / Memory 实现

仿 `UsageStore`（`store/usage_store.go`）模式：

```go
// store/compaction_store.go
type CompactionStore interface {
    Save(ctx context.Context, cp *CompactionCheckpoint) error
    Latest(ctx context.Context, sessionID string) (*CompactionCheckpoint, error) // 无记录返 nil
    DeleteBySession(ctx context.Context, sessionID string) error
}
```

- `SQLiteCompactionStore`（`store/sqlite_compaction_store.go`）：GORM 实现，`Latest` 按 `session_id` + `created_at desc` 取最新一条。
- `MemoryCompactionStore`（`store/memory.go`）：内存 map，供单测 / factory 测试。

### FR-4: Compact 支持 token 预算尾部 + 输出 firstKept 边界

`ctxengine/compact.go` 现在 `tail := cfg.CompactTailKeep`（固定条数，`compact.go:68-77`）。

- `ctxengine.Config` 增加 `CompactTailTokens int`（默认 0 = 未配置回退到 `CompactTailKeep` 条数；config.yaml 侧默认给 `20000`，对齐 LobsterAI `keepRecentTokens`）。
- `Compact` 切窗口逻辑改为：从尾部反向累加 `EstimateMessageTokens` 到 `CompactTailTokens`，得到 `tailStart` index；再用 `CompactTailKeep` 作为**下限**（`tailFloor`），保证至少保留 N 条（对齐 Reasonix `tailFloor`/`recentKeep` 语义）。
- `CompactResult` 增加 `FirstKeptID string` / `FirstKeptTimestamp int64`（`params.go:96-104`）：切窗后保留尾部第一条消息的边界。
- 折半重试路径（`compact.go:114-136`）同样更新 `FirstKept*` 边界。

### FR-5: Compact 采用 Reasonix 式 digest 累积（partitionFold 保留）

对齐 Reasonix（`compact.go:424-465` `pinnedPrefixLen` + `partitionFold`），不采用 LobsterAI 的 previousSummary-UPDATE 单条滚动方案：

- 新增 helper `isCompactionSummary(msg)`：`msg.Role == RoleAssistant && strings.HasPrefix(msg.Content, "[Conversation Summary]")`。
- `Compact` 对折叠 span 做 `partitionFold(span) → (kept, fold)`：
  - **kept**：`isCompactionSummary(m)` 的既有 digest 消息 + `pinnableUserTurn(m)` 的小 user turn（用户陈述的事实永不被摘要掉）。
  - **fold**：其余消息，进摘要。
- 摘要**只针对 fold 区**，生成一条**新 digest**（`[Conversation Summary]\n...`）。
- 重组 `[pinnedPrefix] + [kept] + [newDigest] + [tail]`：既有 digest **原样保留、不二次折叠**，digests 累积成多条（对齐 Reasonix「a fact that reached a digest once is never re-summarized away」）。
- `pinnedPrefixLen` 语义：从前往后跳过 system / 首个 user / 全部既有 digest，这部分永不折叠。
- `CompactResult.Summary` = 本次新 digest 文本（非累积拼接——旧 digest 各自独立留在 Session / messages 表）。

### FR-6: 压缩结果落库（digest 消息 + checkpoint 边界，自动 + 手动统一）

压缩产生两类持久化：

1. **digest 消息写 `MessageStore`**：每次压缩把新 digest 作为一条 `Role=Assistant` 消息（Content 以 `[Conversation Summary]\n` 开头，`Done=true`）写入 messages 表，ID 用新 minted id（不与 run 消息冲突）。这样 digest 是**普通消息行**，随会话完整历史一起落库，hydrate 时天然恢复全部累积 digest。
2. **checkpoint 写 `CompactionStore`**：`CompactionCheckpoint{ SessionID, Summary(本次 digest 文本), TokensBefore, TokensAfter, FirstKeptID, FirstKeptTimestamp }` 记录保留尾部起点，供 hydrate 截断。

落库触发：
- **手动**：`runManualCompact`（`handlers.go:527-547`）成功后，除 `ReplaceAll` 外，调用 `a.PersistCompaction(res)`：写 digest 消息行 + 写 checkpoint。
- **自动**：`executor.go` 在 `Assemble` 返回 `Stats.CompactionTriggered` 时（`assemble.go:68`），通过 `Deps` 新增方法回调 agent 落库（见实现方案 4.4）。
- Agent 新增 `CompactionStore` 依赖（`NewAgentConfig` + `Agent` 字段 + `AgentFactory`）。

### FR-7: hydrate 优先读 checkpoint（恢复全部 digest + 尾部）

`agentloop/hydrate.go`（现有）改为：

1. 先 `CompactionStore.Latest(sessionID)`；无记录 → 现状（`MessageStore.List` 全部）。
2. 有记录 → 从 `MessageStore.List` 全部行中重建上下文：
   - **firstKept 边界之前**：只保留 `isCompactionSummary` 的 digest 行（跳过被压掉的原始消息），使所有历史 digest 进入上下文；
   - **firstKept 边界起**：保留全部消息（tail）。
3. 无 MessageStore / 无 CompactionStore → 现状（空 Session）。
4. `FirstKeptID` 在表中找不到时回落 `FirstKeptTimestamp`；两者皆失配 → 加载全部（安全降级）。

### FR-8: 删除 session 级联删 checkpoint

`handleDeleteSession`（`handlers.go:833+`，已有 usage cascade）增加 `CompactionStore.DeleteBySession`。

### FR-9: config 映射

`config.yaml` 的 `agent:` 块新增 `compact_tail_tokens: 0`（默认 0 = 未启用 token 预算尾部，回退条数）；`runtime/agent_config.go` 映射到 `agent.Config.CompactTailTokens`；`agent.go Config` / `ctxengine.Config` 增加同名字段。

---

## 4. 实现方案

### 4.1 持久化边界：`protocol.Message` + `CompactionCheckpoint`

```
protocol.Message{ID, Timestamp}        ← FR-1，dispatcher/hydrate 填充
CompactionCheckpoint{Summary, FirstKeptID, FirstKeptTimestamp, ...}  ← FR-2，复用现有表
CompactionStore.Save/Latest/DeleteBySession  ← FR-3
```

### 4.2 Compact 尾部 token 预算（`ctxengine/compact.go`）

```go
// 现有固定条数 (compact.go:68-77)
tail := cfg.CompactTailKeep

// 改为：优先 token 预算，条数作下限
func tailStartIndex(msgs []protocol.Message, cfg Config, tailFloor int) int {
    if cfg.CompactTailTokens <= 0 {
        // 回退：取 tailFloor 条
        start := len(msgs) - tailFloor
        if start < 0 { start = 0 }
        return start
    }
    used := 0
    start := len(msgs)
    for i := len(msgs) - 1; i >= 0; i-- {
        t := EstimateMessageTokens(msgs[i])
        if used+t > cfg.CompactTailTokens && len(msgs)-i >= tailFloor {
            break
        }
        used += t
        start = i
    }
    return start
}
```

- `CompactTailKeep`（默认 6）作为 `tailFloor` 保底。
- `span = msgs[:tailStart]`，`tail = msgs[tailStart:]`。
- `FirstKeptID = tail[0].ID`（若空则 `FirstKeptTimestamp = tail[0].Timestamp`）。
- 折半重试（`compact.go:114-136`）同步更新 `FirstKept*`。

### 4.3 digest 累积（`ctxengine/compact.go`）

移植 Reasonix 的 `partitionFold` / `pinnedPrefixLen`（`compact.go:424-465`）：

```go
// isCompactionSummary 识别既有 digest 消息
func isCompactionSummary(m protocol.Message) bool {
    return m.Role == protocol.RoleAssistant &&
        strings.HasPrefix(m.Content, "[Conversation Summary]")
}

// partitionFold 把 span 分为 kept（原样保留）/ fold（进摘要）
func partitionFold(span []protocol.Message, cfg Config) (kept, fold []protocol.Message) {
    for _, m := range span {
        if isCompactionSummary(m) || pinnableUserTurn(m, cfg) {
            kept = append(kept, m)   // 既有 digest / 小 user turn 原样保留
        } else {
            fold = append(fold, m)
        }
    }
    return kept, fold
}

// Compact 内：span 先 partitionFold，只摘要 fold 区
kept, fold := partitionFold(span, cfg)
if len(fold) == 0 {
    // 无可折叠内容 → 快速返回（保留 span 原样）
    ...
}
summaryText, err := summarizer.Summarize(ctx, SummarizeRequest{Messages: fold, ...})
newDigest := protocol.Message{Role: RoleAssistant, Content: "[Conversation Summary]\n" + summaryText + "\n\n(Compacted ...)"}
newMessages := append([]protocol.Message{}, pinnedPrefix...)
newMessages = append(newMessages, kept...)       // 既有 digest + 小 user turn
newMessages = append(newMessages, newDigest)     // 新 digest
newMessages = append(newMessages, msgs[tailStart:]...)  // 尾部
```

`pinnedPrefixLen`：从前往后跳过 system / 首个 user（小）/ 全部既有 digest，这部分整体作为 `pinnedPrefix` 永不折叠。

### 4.4 落库触发点

**手动路径**（`handlers.go:527-547`）：
```go
func runManualCompact(a *agent.Agent, sessionID string) {
    ...
    if res.Success {
        a.Session().ReplaceAll(res.RetainedMessages)
        a.PersistCompaction(res)              // 新增：digest 写 MessageStore + checkpoint 写 CompactionStore
        a.Emit(event.CompactionEvent{...})
    }
}
```

`Agent.PersistCompaction(res)` 实现（`agent.go`）：
```go
func (a *Agent) PersistCompaction(res ctxengine.CompactResult) {
    if a.compactionStore != nil && res.Success {
        a.compactionStore.Save(ctx, &store.CompactionCheckpoint{
            SessionID: a.session.ID,
            Summary:   res.Summary,
            TokensBefore: res.TokensBefore,
            TokensAfter:  res.TokensAfter,
            FirstKeptID:  res.FirstKeptID,
            FirstKeptTimestamp: res.FirstKeptTimestamp,
        })
    }
    // digest 消息作为 assistant 行写 messages 表，随完整历史落库
    if a.msgStore != nil {
        a.msgStore.Save(ctx, &store.MessageRecord{
            ID: "digest-" + res.Checkpoint.ID, SessionID: a.session.ID,
            Role: string(protocol.RoleAssistant), Content: res.Summary,
            Done: true, Timestamp: time.Now().UnixMilli(),
        })
    }
}
```

**自动路径**：`executor.go` 的 `Deps` 接口增加 `PersistCompaction(ctx, params)`（实现见 `agent.go`）。在 `assemble` 返回后：

```go
assembled := d.Assembler().Assemble(...)
messages = assembled.Messages
if assembled.Stats.CompactionTriggered {
    d.PersistCompaction(ctx, ctxengine.CompactResult{...})  // 或直接传 assembled 相关字段
}
```

> 这里自动路径的 `CompactResult` 目前不暴露给 executor（`assemble.go:58-77` 内部 Compact 后只取 `RetainedMessages` / `TokensAfter`）。**实现时需让 `AssembleResult` 携带 `CompactSummary` / `FirstKeptID` / `FirstKeptTimestamp`**（`params.go:65-72` 扩展），或直接让 `assemble.go` 在 Compact 成功后通过 `a.deps.Emit` 触发落库事件 + agent 订阅处理。二选一，倾向前者（显式返回值）。

### 4.5 hydrate 优先读 checkpoint（`agentloop/hydrate.go`）

```go
func hydrateSession(ctx context.Context, f *AgentFactory, sess *session.Session) {
    if f.MessageStore == nil { return }
    rows, _ := f.MessageStore.List(ctx, sess.ID, 0, 0)

    msgs := make([]protocol.Message, 0, len(rows))
    var cp *store.CompactionCheckpoint
    if f.CompactionStore != nil {
        cp, _ = f.CompactionStore.Latest(ctx, sess.ID)
    }
    if cp != nil {
        // firstKept 边界前：只保留 digest 行（跳过被压掉的原始消息）
        inTail := false
        for _, r := range rows {
            if !inTail {
                if (cp.FirstKeptID != "" && r.ID == cp.FirstKeptID) ||
                    (cp.FirstKeptTimestamp > 0 && r.Timestamp >= cp.FirstKeptTimestamp) {
                    inTail = true
                }
            }
            converted := recordToMessages(r)
            if !inTail {
                // 边界前只放 digest 行（isCompactionSummary）
                for _, m := range converted {
                    if isCompactionSummary(m) {
                        msgs = append(msgs, m)
                    }
                }
                continue
            }
            msgs = append(msgs, converted...)
        }
    } else {
        for _, r := range rows { msgs = append(msgs, recordToMessages(r)...) }
    }
    sess.ReplaceAll(msgs)
}
```

> `isCompactionSummary` 判定 digest 行：`Role==assistant && strings.HasPrefix(Content, "[Conversation Summary]")`。`ctxengine` 导出该 helper 供 agentloop 复用（或复制一份到 hydrate.go）。

### 4.6 装配链

- `runtime/factory.go` `AgentFactoryDeps` 增加 `CompactionStore store.CompactionStore`。
- `runtime/database.go` AutoMigrate 已含 `&store.CompactionCheckpoint{}`（`database.go:34`），无需加表。
- `runtime/runtime.go`：`stores` 增加 `compactionStore`；注入 `AgentFactory` + `gateway.HandlerOptions`。
- `gateway/HandlerOptions` 增加 `CompactionStore`，`handleDeleteSession` 级联删。

---

## 5. 边界情况

| 场景 | 处理方式 |
| ---- | -------- |
| 无 `CompactionStore`（单测 / 精简路径） | 压缩不落库，hydrate 走现状（`MessageStore.List` 全部） |
| 无 `MessageStore` | hydrate 跳过（现状） |
| 有 checkpoint 但 `FirstKeptID` 在消息表中找不到（已删 / ID 变更） | 回落 `FirstKeptTimestamp`；仍找不到 → 加载全部（安全降级） |
| `Message.ID` / `Timestamp` 为零值（老数据 / 手工构造） | firstKept 边界用另一个字段；两者皆空 → 加载全部 |
| 自动压缩的 `CompactResult` 信息如何透出 | `AssembleResult` 增加 `CompactionSummary / FirstKeptID / FirstKeptTimestamp` 字段（见 4.4） |
| 同一会话连续多次压缩 | 既有 digest 通过 `partitionFold` 原样保留、不二次折叠；`CompactionStore.Latest` 只取最新一条作 firstKept 边界，digest 行全部留在 messages 表（hydrate 恢复全部 digest） |
| 被压原始消息 | **不删**，留在 messages 表（UI 完整历史）；仅 hydrate 组装上下文时跳过 firstKept 之前的非 digest 消息 |
| digest 消息与 run 消息 ID 冲突 | digest 用 `digest-<checkpointID>` 前缀（`4.4`），不与 `msgID-index` 格式冲突 |
| 折叠区全是被保留项（`fold` 为空） | `Compact` 快速返回 Success:false，不写 checkpoint（无可压缩内容） |
| 超长会话 > 1000 条消息 | `MessageStore.List` 默认 limit 1000（`message_store.go:119-121`）；checkpoint 后的 tail 必然 < 1000，可正常截取；若 tail 起点在 1000 之后（极端），回落加载全部 |
| `compact_tail_tokens` 未配置（0） | 回退 `compact_tail_keep` 固定条数（现状行为） |

---

## 6. 涉及文件

### Go agent

| 文件 | 变更说明 |
| ---- | -------- |
| `internal/agents/protocol/types.go` | `Message` 加 `ID string` / `Timestamp int64` |
| `internal/agents/store/models.go` | `CompactionCheckpoint` 加 `FirstKeptTimestamp int64` |
| `internal/agents/store/compaction_store.go` | **新增**：`CompactionStore` 接口 |
| `internal/agents/store/sqlite_compaction_store.go` | **新增**：GORM 实现 |
| `internal/agents/store/memory.go` | `MemoryCompactionStore` |
| `internal/agents/store/compaction_store_test.go` | **新增**：roundtrip / Latest / DeleteBySession |
| `internal/agents/ctxengine/params.go` | `Config.CompactTailTokens`、`CompactResult.FirstKept*`、`AssembleResult` 压缩透出字段 |
| `internal/agents/ctxengine/compact.go` | token 预算尾部 + firstKept 边界 + `partitionFold`/`pinnedPrefixLen`/`isCompactionSummary`（digest 累积） |
| `internal/agents/ctxengine/assemble.go` | 透出 `CompactSummary / FirstKept*` 到 `AssembleResult` |
| `internal/agents/ctxengine/compact_test.go` | 新增 token 预算尾部 / firstKept / partitionFold / digest 累积用例 |
| `internal/agents/agent.go` | `CompactionStore` 字段 + `PersistCompaction` 方法 |
| `internal/agents/dispatcher.go` | append 时填 `Message.ID` / `Timestamp` |
| `internal/agents/executor/executor.go` | `Deps.PersistCompaction` + Assemble 后触发 |
| `internal/agentloop/factory.go` | `AgentFactory` 加 `CompactionStore` + hydrate 透传 |
| `internal/agentloop/hydrate.go` | 优先读 checkpoint + digest 识别（FR-7） |
| `internal/agentloop/hydrate_test.go` | checkpoint 优先 / digest 恢复 / 降级 / 边界用例 |
| `internal/gateway/handlers.go` | `runManualCompact` 落库；`handleDeleteSession` 级联删；`HandlerOptions` 加 `CompactionStore` |
| `internal/gateway/handlers_test.go` | delete 级联 / compact 落库用例 |
| `internal/runtime/factory.go` | `AgentFactoryDeps.CompactionStore` |
| `internal/runtime/database.go` | 装配 `SQLiteCompactionStore` |
| `internal/runtime/runtime.go` | 注入 handler + factory |
| `internal/config/config.go` | `Agent.CompactTailTokens` |
| `internal/runtime/agent_config.go` | 映射新字段 |
| `src/darvin-agent/config.yaml` | `compact_tail_tokens: 20000` |

### Electron / renderer

| 文件 | 变更说明 |
| ---- | -------- |
| 无 | 本设计不新增 IPC / renderer 改动（压缩事件 `CompactionEvent` 已透出；历史显示不变） |

---

## 7. 验收标准

### 用户场景
- [ ] **场景 1**：长会话手动压缩 → 重启 → 提问，agent 上下文是 summary + 尾部（`get_messages` 历史仍完整）
- [ ] **场景 2**：超预算自动压缩 → 切走再切回 → 上下文不膨胀（hydrate 读 checkpoint）
- [ ] **场景 3**：多次压缩后 UI 历史仍完整显示

### 自动化
- [ ] `go test ./...`（`src/darvin-agent/`）通过，含 compaction_store / hydrate / compact 新用例
- [ ] `npm run lint` 通过
- [ ] `npm run test`（vitest）通过

### 手动验证
- [ ] `npm start` 起应用，长会话手动压缩 → 重启 → 提问能引用压缩前的关键事实（summary 生效）
- [ ] 压缩后 `sessions.db` 的 `compaction_checkpoints` 表出现最新一行（Summary / FirstKeptID 非空）
- [ ] 切会话再切回，DevTools 无完整历史重新拉起的迹象（上下文占用回落）
