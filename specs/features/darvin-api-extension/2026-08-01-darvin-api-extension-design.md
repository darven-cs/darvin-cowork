# Darvin API 协议扩展

> 编号 **00**，是后续 8 个 UI spec 的前置依赖。先定协议再写组件。

## 1. 背景

`src/shared/darvin-api.ts` 的 `DarvinEvent` union 与 `DarvinMessage` / `DarvinUsage` 已经埋好部分地基（`tool_start` / `tool_end` / `done.usage`），但要承载 LobsterAI 同级别的 UI 体验（Markdown + 工具组 + Token 圆环 + 压缩 + Artifact + 图片附件），需要把协议层补齐。

详细现状与对比见：

- `specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 3「协议层 gap 摘要」

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `DarvinMessage` 扩展为 discriminated union，按 `type` 分发渲染 | `MessageItem` / `ToolCallGroup` / `ArtifactRenderer` 全部走 `switch (msg.type)` |
| G2 | `DarvinUsage` 补 cache 字段；新增 `DarvinContextUsage` 类型 | `useMessages` 维护 `contextUsageBySessionId` |
| G3 | `DarvinEvent` 新增 `compaction` / `context_usage` / `artifact` 正式 union 成员 | TS 编译通过；不再走 `client.ts` 的静默丢弃 |
| G4 | 工具事件携带 `toolUseId` + `isError` + `kind`（Bash / Read / Edit / TodoWrite / ...） | `ToolCallGroup` 能按 `kind` 选专门渲染器 |
| G5 | user 消息支持 `imageAttachments` / `localMediaAttachments` | `UserMessageItem` 能渲染图片缩略图 |

## 3. 非目标

- 不改 Go agent 实现；只动 renderer / preload / main 类型定义 + IPC 契约
- 不引入新的 IPC 通道，复用现有 `darvin:push:session-event`
- 不动现有 `text_delta` / `thinking_delta` / `done` / `error` / `agent_end` 的字段语义

## 4. 字段定义草案

### 4.1 `DarvinMessage` → discriminated union

```ts
export type DarvinMessage =
  | { id; sessionId; type: 'user'; content; createdAt; attachments?: DarvinAttachment[] }
  | { id; sessionId; type: 'assistant'; content; createdAt; isStreaming; isThinking?; usage?; model? }
  | { id; sessionId; type: 'tool_use'; toolUseId; tool; toolKind: DarvinToolKind; input; createdAt }
  | { id; sessionId; type: 'tool_result'; toolUseId; tool; output; isError; createdAt }
  | { id; sessionId; type: 'system'; content; createdAt };
```

### 4.2 `DarvinToolKind`

```ts
export type DarvinToolKind =
  | 'bash' | 'read' | 'write' | 'edit' | 'todowrite'
  | 'web_search' | 'web_fetch' | 'image_gen' | 'video_gen'
  | (string & { __brand?: never });  // 兜底自定义工具
```

### 4.3 `DarvinUsage` + `DarvinContextUsage`

```ts
export interface DarvinUsage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  totalTokens: number;
}

export interface DarvinContextUsage {
  sessionId: string;
  usedTokens?: number;
  contextTokens?: number;
  percent?: number;
  status: 'unknown' | 'normal' | 'warning' | 'danger' | 'compacting';
  compactionCount?: number;
  latestCompactionAt?: number;
  latestCompactionReason?: string;
  model?: string;
  updatedAt: number;
}
```

### 4.4 `DarvinEvent` 新增成员

```ts
| { type: 'compaction'; sessionId; runId; reason: 'auto' | 'manual'; checkpointId: string; createdAt: number }
| { type: 'context_usage'; sessionId; usage: DarvinContextUsage }
| { type: 'artifact'; sessionId; artifactId: string; kind: DarvinArtifactKind; name?: string; content: string; createdAt: number }
```

### 4.5 `DarvinAttachment`（user 消息附件）

```ts
export interface DarvinAttachment {
  id: string;
  kind: 'image' | 'file';
  name: string;
  size: number;
  mimeType: string | null;
  // image 用 base64 / dataURL；file 用相对 workspace 路径
  src: string;
}
```

## 5. 用户场景

### 场景 1：agent 跑通工具调用 → renderer 渲染 tool_use + tool_result 一对

**Given** Go agent 完成 `bash` 调用：`tool_use { toolUseId: 't1', tool: 'Bash', toolKind: 'bash', input: { command: 'ls' } }` → `tool_result { toolUseId: 't1', tool: 'Bash', output: 'README.md\n', isError: false }`

**When** 事件流到达 renderer

**Then** `useMessages.appendEvent` 按 `toolUseId` 把两条消息配对存进 turn 模型；`ToolCallGroup` 按 `toolKind === 'bash'` 选 bash 渲染器；input 折叠 + result 默认折叠

### 场景 2：上下文到 80% → status=warning → 100% 触发 compaction

**Given** session context 占比从 60% 升到 80% 再到 100%

**When** Go agent 推 `context_usage` 事件 + 自动 `compaction` 事件

**Then** `ContextUsageIndicator` 圆环颜色按状态切换：green → yellow → red；compacting 状态旋转；`AssistantTurnBlock` 在 turn 之间插入 `ContextCompactionDivider`

## 6. 验收

- [ ] `darvin-api.ts` 编译通过；现有 `MessageItem` / `useMessages` 行为不变（向后兼容）
- [ ] 新类型可被 `MessageItem.vue` 用 `v-if="msg.type === 'tool_use'"` 分发
- [ ] `LIFECYCLE_EVENT_TYPES` 移除 `'compaction'` 静默丢弃，改为正常 push
- [ ] 在 `src/shared/darvin-api.ts` 顶部加 `assertNever(msg)` 兜底
- [ ] 协议变更后不破坏现有 mock（`mock-data.ts` 不报错）

## 7. 依赖

- **被依赖**：01 / 02 / 03 / 04 / 05 全部需要本 spec 落地后才能开工
- **依赖**：无（可作为首个 spec 启动）
- **可参考的 LobsterAI 协议类型**（参考项目根：`~/桌面/github-project/LobsterAI`，下述路径均相对该项目根）：
  - `src/renderer/types/cowork.ts:65-137` — `CoworkMessageMetadata` + `CoworkContextUsage` + `CoworkMessage` 完整定义
  - `src/shared/cowork/constants.ts` — `CoworkContextUsageSource` / `CoworkForkMode` 等枚举

## 8. 参考

- `specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md`
