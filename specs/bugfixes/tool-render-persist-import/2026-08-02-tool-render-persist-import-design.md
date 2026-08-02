# 工具渲染顺序 / 工具持久化 / 导入文件感知与生命周期 修复

> 编号 **B1**。修复 4 个 live bug：①工具调用渲染顺序错位 ②工具调用不持久化 ③agent 感知不到导入文件 ④导入文件应只用于单条消息。

## 1. 概述

### 1.1 问题 / 背景

用户实测报告 4 个问题（均经源码 + DB + live 验证确认根因）：

1. **工具调用渲染顺序错位**：agent 用工具时应「答一句 → 工具 → 答一句 → 工具」交错，但当前「所有工具渲染在上、所有回答在下」。
2. **工具调用不持久化**：重新进入会话，工具调用记录消失（DB `messages.tool_calls` 列实测为空）。
3. **agent 感知不到导入文件**：文件导入只进工作区，agent 不知晓导入的文件。
4. **导入文件一直显示**：导入应只用于当前一条用户消息，但现在 ImportedFilesBar 常驻。

### 1.2 根因

| Bug | 根因 | 证据 |
|---|---|---|
| 1 | Go `Loop.admit` 每 run 只铸一个 messageId；`appendToBucket` 把所有 `text_delta` 累积进同一条 assistant 消息，`tool_start`/`tool_end` 条目 append 到桶尾 → 交错序在桶里丢失；`AssistantTurnBlock.renderItems` 再强制「所有 thinking → 所有 tools → 所有 content」 | `loop.go:138` msgID 每次 prompt 一次；`useMessages.ts:344-396` 事件归桶；`AssistantTurnBlock.vue:72-90` 三阶段重排 |
| 2 | `persistAssistantMessages` 把一轮 run 内所有 turn 的 assistant 消息用**同一 run msgID** upsert（`message_store.go` Save 是 PK 冲突替换）→ last-wins 只剩最后一条；tool **results 从不落库**（`llm.ToolCall` 只有 id/name/arguments）；renderer `toMessage` 的 legacy 分支（`role` 存在）**丢弃 toolCalls** | `dispatcher.go:280-319`；`types.go:123`；`useMessages.ts:237` legacy 分支 |
| 3 | main `runImport` 用 `agent.save_message` 把 `workspace_event` system note 写进 **store**，但 agent 组装 LLM context 用的是 **live `session.Messages()`**，store 的新行不注入 session → agent 看不到 | `importFiles.ts:152-162`；`handlers.go:862 handleSaveMessage` 只 `MessageStore.Save`；assembler 读 session |
| 4 | 导入 = workspace 拷贝 + `imported_files` 行（session 级持久），`useImportedFiles.files` 随会话常驻，ImportedFilesBar 一直展示 | `useImportedFiles.ts` 无「发送后清空」逻辑 |

### 1.3 目标

- Bug1：工具调用与回答按事件序交错渲染（答 → 工具 → 答 → 工具）。
- Bug2：重进会话可见完整工具调用（名称 / 参数 / 结果）。
- Bug3：导入文件后 agent 能在 LLM context 里感知到文件（含相对路径，可 read）。
- Bug4：导入 = 单条用户消息的暂存附件，发送后清空（UI 与 workspace 一致）。

### 1.4 非目标

- 不改 Go 的 eventledger / 事件推送协议本体（只加字段不破坏老事件）。
- 不做多会话附件复用 / 收藏。
- 不引入 vue-i18n 之外的任何新依赖。

## 2. 用户场景

### 场景 1：多轮工具交错渲染
**Given** 用户发「帮我写个文档并检查」。
**When** agent 多轮回复（答一句 → write 工具 → 答一句 → read 工具 → 完成）。
**Then** 聊天流按序渲染「文本 → 工具组 → 文本 → 工具组 → 文本」，TurnMeta 跟随各段。

### 场景 2：重进会话工具仍在
**Given** 会话 A 完成过含 write / bash 工具的多轮回复。
**When** 切走再切回（或重启后进入）。
**Then** 工具调用（名称 + 参数 + 结果）在聊天流中原样显示。

### 场景 3：导入文件被 agent 感知
**Given** 用户导入 `data.csv` 并发送「分析这个文件」。
**When** prompt 发出。
**Then** agent 的 LLM context 含导入 note（相对路径），agent 用 read_file 读取并分析。

### 场景 4：导入只服务单条消息
**Given** 用户导入 `a.pdf`。
**When** 发送消息后。
**Then** ImportedFilesBar 清空，workspace 里该文件不再作为「已导入」常驻；下一条消息不含其引用。

## 3. 功能需求

### FR-1（Bug1）交错渲染
- 消息桶保留事件序的文本 / 工具交错结构。
- `AssistantTurnBlock` 按 turn item 顺序渲染，不做全局 tools/content 重排。

### FR-2（Bug2）工具持久化
- Go 端每个 assistant turn 以独立行持久化（含 text + toolCalls + tool results）。
- `get_messages` 返回结构化行；renderer reload 时还原 tool_use / tool_result 条目。

### FR-3（Bug3）导入感知
- 导入文件相对路径注入 agent 的 live LLM context（当前 run 可见）。
- 已有 `formatImportNote` 文案含相对路径 hint，修复「只写 store 不进 session」即可复用。

### FR-4（Bug4）导入生命周期
- 导入文件标记为「待发送附件」；发送时随 prompt 注入引用；**发送后删除 workspace 拷贝 + imported_files 行 + 清空 ImportedFilesBar**（彻底 one-shot）。

## 4. 实现方案

### 4.1 Bug1 — renderer 侧 split-offset（不改 Go 事件协议）

核心思路：工具调用发生在文本的某个位置，把 assistant 消息按工具出现的「内容长度断点」切成段。

- `Message` 增 `toolSplitOffsets?: number[]`（assistant 消息上，记录其 text 各工具插入位置）。
- `appendToBucket` 的 `tool_start` 分支：`const ass = list.find(m => m.id === ev.messageId && m.role === 'assistant')`；命中时 `ass.toolSplitOffsets ??= []; ass.toolSplitOffsets.push(ass.content.length)`。
- `buildConversationTurns`：处理 assistant 消息时，若 `msg.toolSplitOffsets?.length`，按断点把 `msg.content` 切成 N+1 段，逐段 emit `{type:'assistant', message:{...msg, content: seg, thinking: 首段才保留}}`，段间穿插后续的 `tool_group`。断点按 0-index 内容长度切分。
  - 实现要点：tool_group 的生成仍走现有 tool_use/tool_result 配对；分段只影响 assistant 文本的切分位置。
- `AssistantTurnBlock.renderItems`：**移除**三阶段收集，直接 `props.items.map` 顺序渲染；每个 assistant item 内部仍先 thinking 后 content（`v-if` 逻辑保留）。

边界：并行工具同断点 → 空段（不渲染空文本）；无断点 → 原样单条。

### 4.2 Bug2 — Go 持久化修复 + renderer reload 还原

**Go 侧（改动最小化）：**

- `persistAssistantMessages`：不再用 run msgID 当行主键。每轮新 assistant 消息用**独立 ID**（`fmt.Sprintf("%s-%d", msgID, i)`），避免 upsert 覆盖。
- **tool results 落库**：给 `llm.ToolCall` 加 `Result *ToolResult`（`{Content, IsError}`）或让 dispatcher 在 turn 完成后把 `runToolsParallel` 的 results 回填到对应 session 消息再 persist。倾向后者：executor 已拿到 `results`，`assistant.ToolCalls[i].Result` 回填后 persist 序列化 `toolCalls` JSON 时带上 result。
  - `store.MessageRecord.ToolCalls` 已经是 JSON string（`[]ToolCall`），序列化字段加 `result` 即可，wire 兼容（老行无 result 字段）。

**renderer reload 还原：**

- `toMessage`：legacy 分支不再丢弃 `toolCalls`。若 `legacy.toolCalls`（string）非空，解析 JSON 数组，为每条生成 `tool_use` Message（`kind:'tool_use', toolUseId, tool, input: args`）；有 `result` 时追加 `tool_result` Message（`kind:'tool_result', output`）。
- 还原后的顺序：assistant 行在前，其 tool_use/tool_result 紧随 → `buildConversationTurns` 现有逻辑自然配对。

**约束**：老行（tool_calls 为空）不受影响；结果过大（bash 3.9MB）可截断存储（`MAX_TOOL_RESULT_STORE_BYTES`，如 64KB，超长截断 + 尾注）。

### 4.3 Bug3 — 导入 note 注入 live session context

- `runImport` 成功注入 system note 后，除 `agent.save_message`（store）外，调用一个能**写入 live session** 的 RPC。
  - 方案 A：Go 新增 `agent.inject_system_note`（写 store + `session.Append` system 消息），main 改调它。
  - 方案 B：main 端把当前 imported files 相对路径随 `agent.prompt` 的 params 传过去，Go 端 assembler 注入 system 段。
  - 倾向**方案 B**：改动面小、不改 session 状态机；prompt params 加 `importedFiles?: string[]`（相对路径数组），Go `PromptParams` 解析后注入 context。

### 4.4 Bug4 — 导入生命周期（单条消息附件）

LobsterAI 对照结论：附件存 Redux `draftAttachments[draftKey]`（compose 草稿暂存），发送后 `clearDraftAttachments` 清空——纯内存草稿态、无 workspace 拷贝、无导入注册表，文件仅按路径引用。darvin 的 Go agent 工具只在 workspace 沙箱内有效，文件必须复制进沙箱；但 workspace 里是**拷贝**，删除不影响用户原文件。

- renderer `useImportedFiles`：`files` 语义从「会话级持久」改为「待发送附件暂存」。
- 发送路径（`useChatActions.send` / `ChatPane`）：发送时把 `files` 的相对路径随 `prompt({ content, importedFiles })` 传上；发送成功后调 `removeImportedFiles(files)`（新增批量 IPC，或复用逐条 `removeImportedFile`）删除 workspace 拷贝 + imported_files 行，并清 `files` ref。
- ImportedFilesBar：仅当 `files` 非空且尚未发送时展示。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 并行工具调用同断点 | 空文本段不渲染；工具组各自展示 |
| 工具调用后无后续文本 | 末段为空不渲染 |
| 老 DB 行无 toolCalls / 无 result | 解析容错，缺失则跳过 |
| bash 超大输出 | 存储截断（64KB + 尾注），live 流式不受影响 |
| 导入后未发送就切会话 | 附件随会话隔离；发送时才消费 |
| agent 离线时发送 | 附件不消费，保持暂存，等重试 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/composables/useMessages.ts` | `Message.toolSplitOffsets`；`appendToBucket` 断点记录；`buildConversationTurns` 分段；`toMessage` 解析 toolCalls/result |
| `src/renderer/components/chat/AssistantTurnBlock.vue` | 移除三阶段重排，按序渲染 |
| `src/renderer/composables/useImportedFiles.ts` | 附件暂存语义 + 发送后清空 |
| `src/renderer/composables/useChatActions.ts` / `ChatPane.vue` | 发送携带 `importedFiles`，成功后清理 |
| `src/renderer/services/i18n.ts` | 新增 key（截断尾注等） |
| `src/shared/darvin-api.ts` | `DarvinPromptRequest.importedFiles`；可能 `Message.toolCalls` wire |
| `src/darvin-agent/internal/agent/executor/executor.go` | results 回填到 assistant.ToolCalls |
| `src/darvin-agent/internal/agent/dispatcher.go` | persist 独立行 ID + 序列化 result |
| `src/darvin-agent/internal/agent/llm/types.go` | `ToolCall.Result` 字段 |
| `src/darvin-agent/internal/gateway/handlers.go` | prompt params 解析 importedFiles + 注入 context |
| `src/main/index.ts` | prompt 转发带 importedFiles |

## 7. 验收标准

- [x] 场景 1：多轮工具交错渲染顺序正确（答 → 工具 → 答 → 工具）— 代码 + 单测
- [x] 场景 2：重进会话工具调用（名称 / 参数 / 结果）可见 — 代码 + 单测
- [x] 场景 3：导入文件后 agent 能感知并读取（prompt 后 read_file 命中相对路径）— 代码
- [x] 场景 4：发送后 ImportedFilesBar 清空、workspace 不再常驻 — 代码
- [x] 通过 oxlint + prettier
- [x] 通过 `npm run test`（147 例）+ Go `go build` / `go vet` / 全部 Go 单测（13 包）
- [x] live 验证：4 场景逐一实测，console 0 error

### 落地补充（实现期决议）

- **Bug1 交错渲染**：`Message.toolUse` 增 `splitOffset`（tool_start 时记录 assistant 消息文本长度断点）；`buildConversationTurns` 不变，新增纯函数 `interleaveToolSegments`（useMessages.ts）——按去重断点把 assistant 文本切段，段间穿插工具组；并行工具共享断点 → 工具组排在下段文本前；`AssistantTurnBlock` 移除三阶段重排，按 `interleaveToolSegments` 结果渲染，continuation 段只渲染 MarkdownContent（跳过 TurnMeta / thinking / artifacts / 错误框）。reload 场景工具无 splitOffset → 原样保序。
- **Bug2 持久化**：`llm.ToolCall` 增 `Result *ToolResult`（json tag `result`）+ `id/name/arguments` 小写 tag；executor 在 append assistant 前把 `runToolsParallel` 结果回填进 ToolCalls.Result（`truncateForStore` 64KB 截断 + 尾注，live 事件仍推全文）；`persistAssistantMessages` 每 turn 用独立行 ID（`msgID-{index}`）+ 递增时间戳 + `done:true`（修 last-wins 覆盖 + 排序确定）；renderer `toMessages` 解析 legacy 行 `toolCalls` JSON → 还原 `[assistant 文本, tool_use…, tool_result…]` 序列。老行无 toolCalls → 只保留文本。
- **Bug3 导入感知**：`queue.Message`/`agent.Prompt`/`acp.PromptRequest`/`promptReq`/`Loop.executeTurn`/gateway `PromptParams` 全程线程化 `ImportedFiles`；dispatcher Run 设 `runImportedNote = formatImportedNote(msg.ImportedFiles)`，`Agent.Instructions()` 动态追加该 note（system prompt 注入），RunConversation 后清空 → 只对当前消息生效、不落库。main 转发 `req.importedFiles`；`DarvinPromptRequest.importedFiles?: string[]`。
- **Bug4 导入生命周期**：`useImportedFiles` 增 `pendingPaths()`（发送携带）、`armClearAfterSend()`（prompt 被接受后快照）、`flushAfterSend()`（run 结束删除 workspace 拷贝 + imported_files 行 + 清 UI）；`useChatActions.send` / `ChatPane.handleSend` 发送带 importedFiles + arm；`useMessages` 收到 `agent_end` 调 `flushAfterSend`（避免 agent 异步读文件前被删）。**设计决策（用户确认）**：对齐 LobsterAI「发送后清草稿」——彻底删除 workspace 拷贝 + 行；因删的是沙箱拷贝、不影响用户原文件。
- **追加修复（live 验证时发现）**：Go `withCommon` 只注入 `sessionId`/`runId`，而 `ToolStartEvent`/`ToolEndEvent` 序列化 case **漏了 `messageId`**（TextDelta 等都有）→ renderer 的 `ev.messageId` 为 undefined → splitOffset 记成 0 → 交错失效。eventledger.go 补 `messageId` 字段 + eventledger_test 增 tool_start/tool_end case（比较改 `reflect.DeepEqual` 支持 map 值）。
- **live 验证结果**（playwright 驱动重启后的应用）：①Bug1 交错序 `计划文本 → Write → 创建成功文本 → Read → 总结文本` 渲染正确；②Bug2 reload 后会话还原全部 Write/Read 工具组（含 input/result），DB `tool_calls` 列有结果 JSON；③Bug3 会话导入 3 个 csv 后发送「读取第一个 CSV 表头」，agent 回复表头内容（read_file 命中相对路径）；④Bug4 发送后 ImportedFilesBar「从工作区移除」按钮清零、workspace 3 个 csv 删除、imported_files 行清零。console 0 error。
- **已知边界**：reload 渲染为「每 turn 一行」结构与 live 的「单消息切段」略有差异但交错序一致；`agent_end` 清理为模块级单臂，多会话并发边界可接受；工具结果 64KB 截断影响 reload 展示、live 流式不受影响。

## 8. 参考

### darvin-cowork
- `src/renderer/composables/useMessages.ts` — 事件归桶 / turn 构建 / toMessage
- `src/renderer/components/chat/AssistantTurnBlock.vue` — 三阶段重排（待移除）
- `src/darvin-agent/internal/agent/dispatcher.go` — persistAssistantMessages
- `src/darvin-agent/internal/agent/executor/executor.go` — turn 构建 + runToolsParallel
- `src/main/libs/importFiles.ts` — formatImportNote / runImport

### LobsterAI（借鉴）
> 参考项目根目录：`~/桌面/github-project/LobsterAI`

- `src/renderer/components/cowork/messageDisplayUtils.ts` — `buildConversationTurns` 保序（assistant / tool_group 按 items 顺序 push）
- `src/renderer/services/coworkPromptPayload.ts` — 附件 `file: <path>` 行注入 prompt，发送后清除
