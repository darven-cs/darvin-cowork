# darvin-cowork Memory Subsystem — Auto-Extract & Cleanup

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：沿用既有内容；文件名为本次从 `2026-MM-DD-*` 规范化为 `2026-08-04-*` 的新文件名，正文未重写。
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## Scope

Covers CHECKLIST sections E (Auto-Extract / Cleanup Pipeline) + G (Pre-compaction Memory Flush Hook).

Reference: `LobsterAI/src/main/coworkStore.ts:60-101, 271-362, 2271-2387`, `LobsterAI/src/main/libs/openclawHistory.ts:268-284`.

## 1. 概述

darvin-cowork 的 memory 系统有两条写入路径：

1. **显式**：`memory_write` 工具（model 主动调）+ Settings UI 手填 — confidence 0.95、isExplicit=true
2. **隐式**：每轮 turn 结束后，agent 在 `AfterTurn` 钩子里从最近 20 条 user-role 消息里筛值得长期保留的事实 — confidence 0.75、isExplicit=false、section="auto"

隐式路径的难点：

- 不能把对话里的所有问题 / 命令 / 短噪都写成 fact
- 不能写重复（同一 fact 在多轮里出现）
- 命中容量上限时不能直接删（保留 stale 状态作为历史）
- 启动期要扫一遍已存在的 row，把质量差的（问题 / 命令）标 deleted
- OpenClaw 的 `pre-compaction memory flush` 内部 user prompt 不应被当成普通 user message 喂给 auto-extract

LobsterAI 把这套放在了 `coworkStore.ts:60-362` + `shouldAutoDeleteMemoryText` + `createOrReviveUserMemory`。本 spec 把它移植到 Go 端 `internal/memory/` 包。

## 2. 过滤器

### 2.1 程序化命令识别

```go
// internal/memory/filter.go
const memoryProceduralPattern = `(?i)(执行以下命令|run\s+(?:the\s+)?following\s+command|\b(?:cd|npm|pnpm|yarn|node|python|python3|bash|sh|git|curl|wget|brew|apt)\b|\$[A-Z_][A-Z0-9_]*|&&|--[a-z0-9-]+|/tmp/|\.sh\b|\.bat\b|\.ps1\b)`
```

**正样本**（应被 auto-delete）：
- `npm install foo`
- `$ ls -la`
- `git push origin main`
- `cd /tmp && rm -rf foo`
- `curl https://...`
- `python3 script.py`

**负样本**（应保留）：
- `I prefer Python over Node.js`（不是命令）
- `我每天跑步 5 公里`（不是命令）

### 2.2 Skill-meta 识别

```go
const memoryAssistantStylePattern = `(?i)^(?:使用|use)\s+[A-Za-z0-9._-]+\s*(?:技能|skill)`
```

匹配：`使用 pptx 技能`、`use foo skill` → 跳过。

### 2.3 Question 识别

照搬 LobsterAI `isQuestionLikeMemoryText`：

```go
var (
    chineseQuestionPrefixRe = regexp.MustCompile(`^(?:请问|问下|问一下|是否|能否|可否|为什么|为何|怎么|如何|谁|什么|哪(?:里|儿|个)?|几|多少|要不要|会不会|是不是|能不能|可不可以|行不行|对不对|好不好)`)
    englishQuestionPrefixRe = regexp.MustCompile(`^(?:what|who|why|how|when|where|which|is|are|am|do|does|did|can|could|would|will|should)\b`)
    questionInlineRe        = regexp.MustCompile(`(?:是不是|能不能|可不可以|要不要|会不会|有没有|对不对|好不好)`)
    questionSuffixRe        = regexp.MustCompile(`(?:吗|么|呢|嘛)\s*$`)
)

func isQuestionLikeMemoryText(text string) bool {
    normalized := strings.TrimSpace(strings.ReplaceAll(text, `\\s+`, " "))
    // 去末尾感叹 / 句号
    normalized = strings.TrimRight(normalized, ".！!?")
    if normalized == "" { return false }
    if strings.HasSuffix(normalized, "?") || strings.HasSuffix(normalized, "？") { return true }
    if chineseQuestionPrefixRe.MatchString(normalized) { return true }
    if englishQuestionPrefixRe.MatchString(normalized) { return true }
    if questionInlineRe.MatchString(normalized) { return true }
    if questionSuffixRe.MatchString(normalized) { return true }
    return false
}
```

**正样本**（应跳过）：
- `请问今天天气怎么样？`
- `怎么做烤鸡？`
- `how do I deploy this?`
- `要不要继续？`

**负样本**（应保留）：
- `我每天都问自己今天做什么`（虽然含"什么"，但不是疑问句）

### 2.4 短噪识别

```go
func isShortStylized(text string) bool {
    runes := []rune(strings.TrimSpace(text))
    if len(runes) >= 16 { return false }
    cjkCount := 0
    for _, r := range runes {
        if unicode.Is(unicode.Han, r) { cjkCount++ }
    }
    // CJK 1-2 字（如 "嗯嗯"、"好的"）几乎都是 ack
    if cjkCount > 0 && cjkCount <= 2 { return true }
    // 英文短句末尾是标点
    if len(runes) <= 4 { return true }
    if unicode.IsPunct(runes[len(runes)-1]) { return true }
    return false
}
```

### 2.5 shouldAutoExtract 决策

```go
func ShouldAutoExtract(text string) bool {
    t := strings.TrimSpace(text)
    if t == "" { return false }
    if strings.HasPrefix(t, "$") { return false }
    if regexp.MustCompile(memoryProceduralPattern).MatchString(t) { return false }
    if regexp.MustCompile(memoryAssistantStylePattern).MatchString(t) { return false }
    if isQuestionLikeMemoryText(t) { return false }
    if isShortStylized(t) { return false }
    return true
}
```

## 3. AutoExtract

### 3.1 触发位置

`ctxengine/after_turn.go` 已存在 `AfterTurn(ctx, AfterTurnParams)` 入口，main.go 在构造 DefaultAssembler 后调 `SetOnAfterTurn(...)` 注入闭包。本 spec 把闭包 body 从 stub 换成：

```go
// cmd/app/main.go
onAfter := func(ctx context.Context, p ctxengine.AfterTurnParams) error {
    autoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    // 拉当前 session 的全部消息（已持久化的）
    rows, err := msgStore.List(autoCtx, p.SessionID, 1000, 0)
    if err != nil { return err }
    msgs := make([]protocol.Message, 0, len(rows))
    for _, r := range rows {
        msgs = append(msgs, protocol.Message{
            Role: protocol.Role(r.Role),
            Content: r.Content,
        })
    }
    return memMgr.AutoExtract(autoCtx, p.SessionID, msgs)
}
```

### 3.2 AutoExtract 实现

```go
// internal/memory/extract.go
const autoExtractRecentLimit = 20

func (m *Manager) AutoExtract(ctx context.Context, sessionID string, msgs []protocol.Message) error {
    if m == nil || !m.Config.Enabled || !m.Config.ImplicitUpdateEnabled || m.DB == nil {
        return nil
    }
    // 取最近 N 条
    start := 0
    if len(msgs) > autoExtractRecentLimit {
        start = len(msgs) - autoExtractRecentLimit
    }
    for i := start; i < len(msgs); i++ {
        msg := msgs[i]
        if msg.Role != protocol.RoleUser { continue }
        if !ShouldAutoExtract(msg.Content) { continue }
        entry, err := m.Create(ctx, msg.Content, "auto", false, 0.75)
        if err != nil {
            m.Log.Warn("memory auto-extract failed", zap.Error(err))
            continue
        }
        if sessionID != "" {
            if err := m.recordSource(ctx, entry.ID, sessionID, "", "user"); err != nil {
                m.Log.Debug("memory source record failed", zap.Error(err))
            }
        }
    }
    return m.enforceCapacity(ctx)
}
```

### 3.3 二次跑幂等

同一批 messages 第二次跑 `AutoExtract`，`Create` 走精确 fingerprint 命中 + near-dup 合并 → 不重复创建。测试覆盖（`extract_test.go::TestAutoExtractSecondRunZeroOp`）。

## 4. Near-dup 合并

放在 `Manager.Create` 内部，对齐 LobsterAI `createOrReviveUserMemory`。

### 4.1 精确指纹查重

```go
func (m *Manager) Create(ctx, text, section string, isExplicit bool, confidence float64) (UserMemory, error) {
    normalized := truncate(replaceWhitespace(text), 360)
    fingerprint := Fingerprint(section + "\x00" + normalized)
    // 查精确
    var existing UserMemory
    err := m.DB.Where("fingerprint = ? AND status != 'deleted'", fingerprint).First(&existing).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        // 走 near-dup 分支
        return m.createOrMerge(ctx, normalized, section, fingerprint, isExplicit, confidence)
    }
    if err != nil { return UserMemory{}, err }
    // 命中精确：no-op 或复活（status == stale/deleted → revive）
    switch existing.Status {
    case "created":
        return existing, nil
    case "stale", "deleted":
        return m.reviveExisting(ctx, existing, normalized, section, isExplicit, confidence)
    }
    return existing, nil
}
```

### 4.2 Near-dup 评分

```go
const memoryNearDuplicateMinScore = 0.82

func scoreMemorySimilarity(left, right string) float64 {
    if left == "" || right == "" { return 0 }
    if left == right { return 1.0 }
    // 折叠空白
    compactLeft := strings.ReplaceAll(left, " ", "")
    compactRight := strings.ReplaceAll(right, " ", "")
    if compactLeft == compactRight { return 1.0 }
    // 子串覆盖率
    phrase := 0.0
    if strings.Contains(compactLeft, compactRight) || strings.Contains(compactRight, compactLeft) {
        minLen := float64(min(len(compactLeft), len(compactRight)))
        maxLen := float64(max(len(compactLeft), len(compactRight)))
        if maxLen > 0 { phrase = minLen / maxLen }
    }
    return max(phrase, scoreTokenOverlap(left, right), scoreCharacterBigramDice(left, right))
}

func scoreTokenOverlap(left, right string) float64 {
    bag := func(s string) map[string]int {
        m := map[string]int{}
        for _, t := range strings.Fields(strings.ToLower(s)) { m[t]++ }
        return m
    }
    l, r := bag(left), bag(right)
    inter, sumL, sumR := 0, 0, 0
    for k, v := range l {
        sumL += v
        if rv, ok := r[k]; ok && rv < v { inter += rv; if !ok { continue } }
        if rv, ok := r[k]; ok && rv >= v { inter += v }
    }
    for _, v := range r { sumR += v }
    denom := sumL + sumR
    if denom == 0 { return 0 }
    return float64(2*inter) / float64(denom)
}

func scoreCharacterBigramDice(left, right string) float64 {
    bigrams := func(s string) map[string]int {
        rs := []rune(s)
        if len(rs) < 2 { return nil }
        m := map[string]int{}
        for i := 0; i < len(rs)-1; i++ {
            m[string(rs[i:i+2])]++
        }
        return m
    }
    l, r := bigrams(left), bigrams(right)
    if len(l) == 0 || len(r) == 0 { return 0 }
    inter, sumL, sumR := 0, 0, 0
    for k, v := range l {
        sumL += v
        if rv, ok := r[k]; ok { inter += min(v, rv) }
    }
    for _, v := range r { sumR += v }
    return float64(2*inter) / float64(sumL+sumR)
}
```

### 4.3 Preferred text 选择

```go
func scoreMemoryTextQuality(s string) float64 {
    if s == "" { return 0 }
    score := float64(len([]rune(s)))
    if regexp.MustCompile(`^(?:该用户|这个用户|用户)\s*`).MatchString(s) { score -= 12 }
    if regexp.MustCompile(`^(?:the user|user)\b`).MatchString(s) { score -= 12 }
    if regexp.MustCompile(`^(?:我|我的|我是|我有|我会|我喜欢|我偏好)`).MatchString(s) { score += 4 }
    if regexp.MustCompile(`^(?:i|i am|i'm|my)\b`).MatchString(s) { score += 4 }
    return score
}

func choosePreferredMemoryText(current, incoming string) string {
    cur := truncate(replaceWhitespace(current), 360)
    inc := truncate(replaceWhitespace(incoming), 360)
    if cur == "" { return inc }
    if inc == "" { return cur }
    cs := scoreMemoryTextQuality(cur)
    is := scoreMemoryTextQuality(inc)
    if is > cs+1 { return inc }
    if cs > is+1 { return cur }
    return longerOf(cur, inc)
}
```

### 4.4 合并行为

```go
func (m *Manager) createOrMerge(...) {
    // 没精确命中 → 走 near-dup
    semKey := normalizeMemorySemanticKey(normalized)
    var candidates []UserMemory
    m.DB.Where("status != ?", "deleted").Order("updated_at DESC").Limit(200).Find(&candidates)
    var best UserMemory
    bestScore := 0.0
    for _, c := range candidates {
        cKey := normalizeMemorySemanticKey(c.Text)
        if cKey == "" { continue }
        s := scoreMemorySimilarity(cKey, semKey)
        if s > bestScore { bestScore = s; best = c }
    }
    if bestScore >= memoryNearDuplicateMinScore {
        mergedText := choosePreferredMemoryText(best.Text, normalized)
        mergedConfidence := max(best.Confidence, confidence)
        mergedExplicit := best.IsExplicit || isExplicit
        // update best row in place
        return m.updateExistingWithMerge(ctx, best.ID, mergedText, mergedConfidence, mergedExplicit)
    }
    // 真没有重复 → 新建
    return m.insertNewRow(ctx, normalized, section, fingerprint, isExplicit, confidence)
}
```

`normalizeMemorySemanticKey`：

```go
func normalizeMemorySemanticKey(s string) string {
    return replaceWhitespace(strings.ToLower(s))
}
```

## 5. 启动期清扫

对齐 LobsterAI `main.ts:1755-1765`：

```go
// cmd/app/main.go
func getCoworkStore() *CoworkStore {
    if coworkStore == nil {
        coworkStore = NewCoworkStore(database.Get())
        n := coworkStore.AutoDeleteNonPersonalMemories()
        if n > 0 { log.Info("cowork-memory: auto-deleted bad memories", zap.Int("count", n)) }
    }
    return coworkStore
}
```

`AutoDeleteNonPersonalMemories()`：

```go
func (s *CoworkStore) AutoDeleteNonPersonalMemories() int {
    var rows []UserMemory
    s.DB.Where("status = ?", "created").Find(&rows)
    now := time.Now().UnixMilli()
    deleted := 0
    for _, r := range rows {
        if !shouldAutoDeleteMemoryText(r.Text) { continue }
        s.DB.Transaction(func(tx *gorm.DB) error {
            if err := tx.Exec(`DELETE FROM memory_fts WHERE memory_id = ?`, r.ID).Error; err != nil { return err }
            return tx.Model(&UserMemory{}).Where("id = ?", r.ID).
                Updates(map[string]any{"status": "deleted", "updated_at": now}).Error
        })
        deleted++
    }
    return deleted
}

func shouldAutoDeleteMemoryText(text string) bool {
    norm := replaceWhitespace(text)
    if norm == "" { return false }
    matched, _ := regexp.MatchString(memoryProceduralPattern, norm)
    if matched { return true }
    matched, _ = regexp.MatchString(memoryAssistantStylePattern, norm)
    if matched { return true }
    return isQuestionLikeMemoryText(norm)
}
```

**为什么只在启动期跑一次**：用户主动写的 fact 也可能形式上像 question（"我以前问过这个吗？"），运行时 `AutoExtract` 跳过 question 是合理的，但启动期清扫不能误杀用户显式写的内容 — 故**只清扫 status='created' 且 is_explicit=0 的行**。

修正：

```go
func (s *CoworkStore) AutoDeleteNonPersonalMemories() int {
    var rows []UserMemory
    s.DB.Where("status = ? AND is_explicit = ?", "created", false).Find(&rows)
    // ...
}
```

## 6. Capacity pruning

```go
func (m *Manager) enforceCapacity(ctx context.Context) error {
    if m.Config.UserMemoriesMaxItems <= 0 { return nil }
    cap := m.Config.UserMemoriesMaxItems
    var active int64
    m.DB.WithContext(ctx).Model(&UserMemory{}).Where("status = ?", "created").Count(&active)
    if active <= int64(cap) { return nil }
    overflow := active - int64(cap)
    var ids []string
    m.DB.WithContext(ctx).Model(&UserMemory{}).
        Where("status = ?", "created").
        Order("created_at ASC").
        Limit(int(overflow)).
        Pluck("id", &ids)
    return m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        for _, id := range ids {
            if err := tx.Exec(`DELETE FROM memory_fts WHERE memory_id = ?`, id).Error; err != nil { return err }
            if err := tx.Model(&UserMemory{}).Where("id = ?", id).
                Update("status", "stale").Error; err != nil { return err }
        }
        return nil
    })
}
```

触发点：
- `AutoExtract` 末尾（每轮 turn 后自动 trim）
- `WriteRaw` 末尾（renderer 一次性保存大段后也 trim）

## 7. Pre-compaction Memory Flush 防护

### 7.1 Marker 检测

OpenClaw 在 context 接近窗口时会插入内部 user turn "Pre-compaction memory flush..."，这条不应被 auto-extract 当成 user fact。

```go
// internal/ctxengine/history.go （新文件）
var preCompactionMemoryFlushMarkers = []string{
    "pre-compaction memory flush",
    "store durable memories only in memory/",
    "reply with no_reply",
}

func IsPreCompactionMemoryFlushPromptText(text string) bool {
    norm := strings.ToLower(strings.TrimSpace(text))
    if norm == "" { return false }
    for _, m := range preCompactionMemoryFlushMarkers {
        if !strings.Contains(norm, m) { return false }
    }
    return true
}
```

### 7.2 dispatcher 层过滤

`internal/agents/dispatcher.go` 的 `Run()` 在调 `AfterTurn` 前判断 — 如果 session 进入 pre-compaction 模式，跳过 AutoExtract：

```go
// Run() loop 内：
// ... RunConversation 之后：
if a.assembler != nil {
    // 抓最后一条 user message
    last := lastUserMessage(a.session.Messages())
    if !ctxengine.IsPreCompactionMemoryFlushPromptText(last.Content) {
        _ = a.assembler.AfterTurn(runCtx, ctxengine.AfterTurnParams{
            SessionID: a.session.ID, TurnIndex: totalTurns,
        })
    }
}
```

主循环不动，event 流照常发。

### 7.3 shouldSuppressHeartbeatText

renderer 端 dispatcher 已经做的事（`LobsterAI/src/main/libs/openclawHistory.ts:276-284`）。本 spec 在 Go 端 `internal/agents/event/event.go` 提供 helper，renderer 经 IPC 拉 history 时调用过滤：

```go
// internal/agents/event/filter.go
func ShouldSuppressHistoryText(role, text string) bool {
    norm := strings.ToLower(strings.TrimSpace(text))
    if norm == "" { return false }
    if role == "user" {
        if isHeartbeatPromptText(norm) || IsPreCompactionMemoryFlushPromptText(norm) {
            return true
        }
    }
    if role == "assistant" || role == "system" {
        if isHeartbeatAckText(norm) || isSilentReplyText(norm) { return true }
    }
    return false
}
```

renderer 拉 session history 时，按这个 helper 过滤后展示。

## 8. provenance 记录

```go
func (m *Manager) recordSource(ctx context.Context, memoryID, sessionID, messageID, role string) error {
    if memoryID == "" { return nil }
    row := &UserMemorySource{
        ID:        newID(),
        MemoryID:  memoryID,
        SessionID: sessionID,
        MessageID: messageID,
        Role:      role,
        IsActive:  true,
        CreatedAt: time.Now().UnixMilli(),
    }
    return m.DB.WithContext(ctx).Create(row).Error
}
```

调用点：
- `Create()` 成功时（v1: source 为 nil，由 callers 按需补；v2: `memory_write` tool 强制带 source）
- `AutoExtract()` 每条新 entry 写入时（source = {sessionID, "", "user"}）

`user_memory_sources` 表让"哪条记忆从哪里来"可以追溯；以后做"Dreaming 整合"时这些 row 是 grounding source。

## 9. lastUsedAt 更新

Search / Get 时更新 — 让"最近被调用的记忆"优先复活：

```go
func (m *Manager) Search(ctx, q, limit) {
    // ...hits...
    if len(hits) > 0 {
        now := time.Now().UnixMilli()
        var ids []string
        for _, h := range hits { ids = append(ids, h.Entry.ID) }
        m.DB.Model(&UserMemory{}).Where("id IN ?", ids).
            Update("last_used_at", now)  // 不影响 updated_at
    }
    return hits, nil
}
```

## 10. 涉及文件

| 文件 | 操作 |
|---|---|
| `src/darvin-agent/internal/memory/filter.go` | 新建（5 个 regex + 4 个 helper） |
| `src/darvin-agent/internal/memory/filter_test.go` | 新建 |
| `src/darvin-agent/internal/memory/extract.go` | 新建（AutoExtract + recordSource + lastUsedAt） |
| `src/darvin-agent/internal/memory/extract_test.go` | 新建 |
| `src/darvin-agent/internal/memory/similarity.go` | 新建（bigram-dice + token overlap + quality + preferred） |
| `src/darvin-agent/internal/memory/similarity_test.go` | 新建 |
| `src/darvin-agent/internal/agents/ctxengine/history.go` | 新建（isHeartbeat / isPreCompactionFlush / isSilentReply） |
| `src/darvin-agent/internal/agents/ctxengine/history_test.go` | 新建 |
| `src/darvin-agent/internal/agents/event/filter.go` | 新建（ShouldSuppressHistoryText） |
| `src/darvin-agent/internal/agents/event/filter_test.go` | 新建 |
| `src/darvin-agent/internal/agents/dispatcher.go` | 修改（Run() 调 AfterTurn 前判断 pre-compaction flush） |
| `src/darvin-agent/cmd/app/main.go` | 修改（AutoDeleteNonPersonalMemories 启动期调） |
| `src/main/index.ts` | 修改（renderer 拉 history 时调 ShouldSuppress — 通过 IPC） |
| `src/main/runtime/client.ts` | 修改（client.shouldSuppressMessage 等） |
| `src/preload/index.ts` | 修改 |
| `src/renderer/composables/useMessages.ts` | 修改（filter pre-compaction / heartbeat 文本） |
| `src/renderer/components/chat/MessageItem.vue` | 修改（不展示 suppress 文本） |

## 11. 验收标准

### Go 单测

- `ShouldAutoExtract`：3 个 positive + 12 个 negative case（命令 / 问句 / skill-meta / 短噪）
- `IsPreCompactionMemoryFlushPromptText`：4 个 marker 全命中 + 5 个 negative
- `shouldAutoDeleteMemoryText`：5 个 negative case（用户 question 形式但内容是 fact）
- `scoreMemorySimilarity`：`I drink oat milk` vs `I drink oat milk every morning` ≥ 0.82；`I drink oat milk` vs `My favourite colour is green` < 0.3
- `choosePreferredMemoryText` 3 种情况
- `enforceCapacity` 触顶时把最老标 stale，FTS 删除
- `AutoExtract` 第二次跑 0-op
- `AutoDeleteNonPersonalMemories` 启动期跑一次
- `recordSource` 写入 `user_memory_sources`
- dispatcher `Run()` 在 pre-compaction marker 命中时不调 `AfterTurn.AutoExtract`

### 手工 smoke

1. chat 输入 `请问今天天气怎么样？` → 不写新 entry
2. chat 输入 `请记住：我每天跑步 5 公里` → 写 `## auto` entry
3. 同一句话第二次输入 → 不重复创建（near-dup 合并）
4. 连续 20+ 条 fact 触发容量触顶 → 最老的标 `stale`（不删除 row，只从 FTS 移除）
5. chat 进入 OpenClaw pre-compaction flush 模式 → 不写新 entry
6. Settings → Memory → Bootstrap → IDENTITY.md → 编辑 → Save → 文件落盘
7. `sqlite3 memories.sqlite "SELECT count(*) FROM user_memories WHERE status='created' AND is_explicit=0"` 应该反映当前活跃自动提取数

### Playwright UI

- Settings → Memory → entries 列表显示按 section 分组
- "已陈旧" 分组显示 status=stale 的项
- 删一条 entry → 列表更新 + toast
- raw view save 后 reload 验证持久化

## 12. 边界 / 非目标

| 场景 | 处理 |
|---|---|
| 用户手动存的事实包含 question keyword | `is_explicit=true` 跳过启动期清扫；运行期不触发 AutoExtract（只走显式 `memory_write`） |
| AutoExtract 跑超过 2s 超时 | `AfterTurn` 闭包 `context.WithTimeout(ctx, 2s)`，超时返回错误但不阻塞 agent 主循环 |
| 内存库未启动（nil DB） | `AutoExtract` 早返 nil；不 panic |
| near-dup 候选 ≥200 条但匹配分低 | 直接 insert new（不卡循环） |
| pre-compaction flush 后立刻有用户问题 | dispatcher 重新打开 AutoExtract（marker 只挡一次） |