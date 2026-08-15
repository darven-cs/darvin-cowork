# 思维链标签归一化（`<think>` → thinking_delta 流）设计文档

## 1. 概述

### 1.1 问题 / 背景

部分 reasoning 模型（DeepSeek-R1 / 部分 Qwen-reasoning 网关）把思考过程**直接作为正文 `<think>...</think>` 文本**流式输出到 `content` 字段，而不是放进 API 的专门字段（OpenAI `reasoning_content` / Anthropic `thinking` 块）。当前 LLM 接入层没有识别这类正文里的 `<think>`，导致：

- `content` 里的 `<think>...</think>` 走 `TextDeltaEvent` → 渲染层按普通 markdown 显示 → 用户看到一坨 `<think>...` 源码。
- 现有 `thinking_delta` 通道（→ `msg.thinking` → `ThinkingBlock.vue` 可折叠"思考中"块）没被触发。

已核对三个 provider 的现状：

| Provider | `reasoning_content` / 原生 thinking | 正文 `<think>` 文本 |
|----------|-----------------------------------|---------------------|
| OpenAI（`openai/stream.go:261-263`） | ✅ `delta.reasoning_content` → `ThinkingDeltaEvent` | ❌ 未识别，当正文流出 |
| Anthropic（`anthropic/stream.go:319-323`） | ✅ `thinking_delta` 内容块 → `ThinkingDeltaEvent` | ❌ 未识别 |
| Gemini（`gemini/stream.go`） | ❌ 无 thought 处理 | ❌ 未识别 |

链路（`llm.StreamEvent` → `executor.go:318` → `event.ThinkingDeltaEvent` → `eventledger.go:182` → wire `thinking_delta` → renderer `msg.thinking`）**已经完整**，只差 provider 流层把正文 `<think>` 归一成 `ThinkingDeltaEvent`。executor 的 `drainStream` 只把 `TextDeltaEvent` 累积进 `textOut`（持久化内容），`ThinkingDeltaEvent` 只转发不入文本——所以在流层归一后，持久化内容自然不含 think 块。

对齐参考：LobsterAI 的 `openclawAssistantText.ts`（结构化 thinking 抽取）+ `imReplyGuard.ts`（`<think>[\s\S]*?</think>` 剥离兜底）。本方案把"正文 `<think>` → thinking"提升到 provider 层统一归一，走重量级路线。

### 1.2 目标

- 在 LLM 流层新增流式 `<think>` 切分器：把正文里的 `<think>...</think>` / `<thinking>...</thinking>` 内容转成 `ThinkingDeltaEvent`，标签从可见文本剥离。
- 三个 provider（OpenAI / Anthropic / Gemini）的流都过同一切分器，`reasoning_content` / 原生 thinking 块的处理保持现状不动。
- 用户可见效果：模型思考过程进可折叠"思考中"块，正文只剩答案，`<think>` 源码不再泄漏。

### 1.3 非目标

- 不改 Gemini 原生 `thought` part 的解析（Gemini 思维模型走独立 part，需单独的 provider 改造，列为后续）。
- 不改非流式 `Complete` 路径（`Complete` 用于 subagent 结果 / 压缩摘要等，正文 think 泄漏影响小；后续可复用同一切分函数做字符串级处理）。
- 不改 renderer / gateway / executor 层——链路已通，纯 llm 层改动。
- 不做 per-model 开关（切分器仅在遇到真实 `<think>` 标签时激活，正常文本透传）。
- 不持久化 thinking（与现有 `thinking_delta` 行为一致：思考块仅 live 展示，reload 后不恢复）。

## 2. 用户场景

### 场景 1: reasoning 模型回复正常展示
**Given** 模型流式输出 `content = "<think>…推理…</think>答案正文"`
**When** 回答开始
**Then** 聊天里出现可折叠"思考中"块（内容为推理，流式中自动展开），正文 markdown 渲染"答案正文"；`<think>` 源码不可见

### 场景 2: 无 think 块的回复不受影响
**Given** 模型输出普通文本 / `reasoning_content` / 原生 thinking 块
**When** 回答
**Then** 行为与现状一致：普通文本原样；`reasoning_content` 与原生 thinking 块继续走既有 thinking 通道

### 场景 3: thinking 跨多个流式 delta
**Given** `<think>` 开标签、内容、闭标签被拆到多个 `text_delta` chunk
**When** 流式进行
**Then** 切分器跨 delta 正确累积，推理内容完整进 thinking 块，正文不含残留标签

## 3. 功能需求

### FR-1: `llm.ThinkingSplitter` 流式切分器
- 新增 `src/darvin-agent/internal/llm/thinking_split.go`，导出 `ThinkingSplitter` 状态机：
  - `Feed(delta string) []StreamEvent`：处理一个文本 delta，返回需发射的 `TextDeltaEvent` / `ThinkingDeltaEvent` 序列（保持顺序）。
  - `Flush() []StreamEvent`：流结束时返回残留事件（未闭合尾部 `<think>` 内容 → Thinking；normal 态残留文本 → Text）。
- 匹配 `think` 与 `thinking` 两种标签（区分大小写，仅小写）。
- 跨 delta 处理：开/闭标签可被任意拆分，用 lookahead 缓冲保证不误判。
- 未闭合尾部 `<think>` 按 thinking 处理（避免正文残留 `<think>` 半截）。

### FR-2: 三个 provider 接入切分器
- OpenAI `openai/stream.go`：`dispatchState` 增加 `splitter *llm.ThinkingSplitter`（runStream 层持久化，随 `partialContent` 同生命周期）；`ch.Delta.Content != ""` 分支改为 `for _, ev := range st.splitter.Feed(d.Delta.Content) { ... }`，其中 `TextDeltaEvent` 才写 `st.partial`，其余原样发射。
- Anthropic `anthropic/stream.go`：`case "text_delta"` 分支同样改造。
- Gemini `gemini/stream.go`：`p.Text != ""` 分支同样改造。
- 三个 provider 的 `finishStream` / `runStream` 结尾、`DoneEvent` 之前调用 `splitter.Flush()`，先写 `partial` 再构造 `DoneEvent.Content`。

### FR-3: 可见文本累积保持干净
- `st.partial`（`DoneEvent.Content`）只累积切分器返回的 `TextDeltaEvent` 内容，think 内容不进正文。
- executor 的 `textOut` 从 `TextDeltaEvent` 累积，天然只含正文 → 持久化内容无 think 块。

## 4. 实现方案

### 4.1 `ThinkingSplitter` 状态机（`llm/thinking_split.go`）

```go
// ThinkingSplitter is a streaming state machine that extracts <think>/
// <thinking> blocks from text deltas, routing inner content to
// ThinkingDeltaEvent and stripping tags from visible text.
type ThinkingSplitter struct {
    inThink bool
    pending strings.Builder // normal-mode lookahead（可能是不完整开标签的后缀）
    think   strings.Builder // think 内容累积
}

func NewThinkingSplitter() *ThinkingSplitter

// Feed 处理一个文本 delta，返回需按序发射的事件。
func (s *ThinkingSplitter) Feed(delta string) []llm.StreamEvent

// Flush 在流结束时返回残留事件（未闭合 think / 残留文本）。
func (s *ThinkingSplitter) Flush() []llm.StreamEvent
```

normal 态逻辑（`Feed` 每段文本）：
1. `pending` 追加 delta。
2. 在 `pending` 里找最早出现的开标签 `<think>` / `<thinking>`：
   - 找到 → 标签前文本作为 `TextDeltaEvent` 发射；标签后余下部分进入 think 累积；进入 `inThink`。
   - 未找到 → 若 `pending` 尾部是某开标签的真前缀（`<thi` / `<thin` / `<think` 等），仅把"安全前缀"（去掉可疑后缀的部分）作为文本发射，可疑后缀留在 `pending` 继续等；否则整段作为文本发射。
3. `inThink` 态：`think` 追加文本；找最早出现的闭标签 `</think>` / `</thinking>`：
   - 找到 → 闭标签前内容作为 `ThinkingDeltaEvent` 发射（多个 think 块各自独立事件）；闭标签后余下部分回到 normal 态重新处理（可能含下一个 `<think>`）。
   - 未找到 → 若尾部是闭标签真前缀，保留可疑后缀；否则整段进 `think` 累积。

`Flush`：`inThink` 态时把 `think` 残留作为 `ThinkingDeltaEvent`；normal 态时把 `pending` 残留作为 `TextDeltaEvent`。

实现要点：开/闭标签集合（`<think>` / `<thinking>` / `</think>` / `</thinking>`）与"真前缀"判断用统一的最小标签长度缓冲；事件按序返回，Provider 端原样转发。

### 4.2 Provider 接入示例（OpenAI）

`openai/stream.go` runStream 持久变量区：

```go
splitter := llm.NewThinkingSplitter() // 与 partialContent 同级，跨 flush 持久
```

`dispatchState` 增加字段 `splitter *llm.ThinkingSplitter`，runStream 构造时传入指针。

`dispatch` 的 content 分支：

```go
if ch.Delta.Content != "" {
    for _, ev := range st.splitter.Feed(ch.Delta.Content) {
        if te, ok := ev.(llm.TextDeltaEvent); ok {
            st.partial.WriteString(te.Delta)
        }
        st.out <- ev
    }
}
```

`finishStream` 在构造 `DoneEvent` 之前：

```go
for _, ev := range st.splitter.Flush() {
    if te, ok := ev.(llm.TextDeltaEvent); ok {
        partial.WriteString(te.Delta)
    }
    out <- ev
}
```

Anthropic / Gemini 同构替换（对应 `case "text_delta"` 与 `p.Text != ""` 分支）。

### 4.3 测试

- `llm/thinking_split_test.go`（纯状态机，直接调 `Feed` / `Flush` 断言事件序列）：
  - 单块 `"<think>推理</think>正文"` → `[ThinkingDeltaEvent("推理"), TextDeltaEvent("正文")]`
  - 标签跨 delta（`"<thi"` + `"nk>推理</thi"` + `"nk>正文"`）
  - 多块、空块、未闭合尾部、无 think 普通文本、`<thinking>` 变体
  - 混合 `reasoning_content`（`ThinkingDeltaEvent` 直通）与正文 `<think>` 共存
- 每个 provider 的 `stream_test.go` 补一条"content 含 `<think>` → 产出 ThinkingDeltaEvent 且 text_delta 不含标签"的用例（可选，纯状态机测试已覆盖核心）。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| `<think>` 标签跨多个 delta | lookahead 缓冲 + 真前缀判定，跨 delta 正确切分 |
| 未闭合尾部 `<think>`（流式未结束） | 残留内容按 thinking 发射，正文不残留半截标签 |
| 多个 think 块 | 每块独立 `ThinkingDeltaEvent` |
| `<think>` 变体 `<thinking>` | 两种标签都支持 |
| 普通文本 / 代码块里出现字面 `<think>` | 会被误判为思考块（与 LobsterAI `imReplyGuard` 同一取舍）；概率低、可接受；模型正文里解释 `<think>` 的场景可用 `//` 转义等规避（后续如需要可加 per-model 开关） |
| 模型走 `reasoning_content` / 原生 thinking 块 | 切分器不介入（没有 `<think>` 文本），既有通道不变 |
| 修复前已持久化的旧消息 | 旧消息的 `<think>` 已写入 DB，属历史数据、不重写；仅新消息生效（如需要可后续加一次性迁移或渲染层兜底，本 spec 不包含） |
| `DoneEvent.Content` | 只累积可见文本，不含 think 块（executor 本就只用 Usage，这里双保险） |
| 流异常 / 上下文取消 | `Feed`/`Flush` 无额外错误路径，事件流关闭即止，行为与现状一致 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/llm/thinking_split.go` | 新增 `ThinkingSplitter` 状态机（Feed / Flush） |
| `src/darvin-agent/internal/llm/thinking_split_test.go` | 新增状态机单测 |
| `src/darvin-agent/internal/llm/openai/stream.go` | `dispatchState` 加 `splitter`；content 分支过切分器；`finishStream` 结尾 `Flush` |
| `src/darvin-agent/internal/llm/anthropic/stream.go` | `text_delta` 分支过切分器；runStream 结尾 `Flush` |
| `src/darvin-agent/internal/llm/gemini/stream.go` | `p.Text` 分支过切分器；runStream 结尾 `Flush` |

## 7. 验收标准

- [ ] 场景 1：reasoning 模型回答时，思考进"思考中"折叠块（流式中展开），正文只有答案，`<think>` 源码不出现
- [ ] 场景 2：`reasoning_content` / 原生 thinking 块模型行为不变；普通文本不变
- [ ] 场景 3：think 块跨多个流式 delta 仍完整切分
- [ ] 持久化消息内容不含 `<think>` 标签（`getMessages` 返回的 assistant content 干净）
- [ ] `cd src/darvin-agent && go build ./... && go vet ./...` 通过
- [ ] `cd src/darvin-agent && go test ./internal/llm/...` 通过（含新增 thinking_split 用例）
- [ ] `npm run lint` + `npm run test` 通过（renderer 未改动，仅确认无回归）
- [ ] 手动 `npm start`：用当前 reasoning 模型发消息，DevTools 观察 `thinking_delta` 事件流入、气泡渲染思考块、正文无标签
