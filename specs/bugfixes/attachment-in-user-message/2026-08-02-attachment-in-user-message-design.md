# 附件在用户消息中的展示与路径注入 — 设计文档

## 1. 概述

### 1.1 问题 / 背景

spec 12 实现了「附加即授权」的路径引用附件：渲染层把绝对路径随 prompt 发给 Go，
Go 把路径加入授权读集（`SetGrantedReads`），`read_file` 可免审批读取。但遗留两个
UI 缺口（用户在 spec 12 落地后反馈）：

1. 发送后用户气泡只显示纯文本，看不到上传的文件（没有卡片 / chip / 缩略图展示）。
2. 发给模型的 `content` 里不含文件路径。路径只通过 Go 的 transient 系统注记
   （`formatImportedNote`，注入 Instructions、不进用户消息）间接告知模型；
   因此用户可见的气泡内容与持久化 transcript 都不含路径，聊天里「引用」不了文件。

用户追加要求：**图片要按 base64 走**（LobsterAI 式）——图片读取为 base64 后作为
image content block 随 prompt 发给模型，模型才能真正看到图像（只给路径、`read_file`
返回二进制文本是看不到的）。darvin 的 LLM 层目前 `llm.Message.Content` 是纯字符串、
Anthropic converter 只发 text block，**完全没有图片 content block 支持**，需要补齐。

LobsterAI 做法（`coworkPromptPayload.ts` / `UserMessageItem.tsx`）：

- 非图片：路径以 `文件: <absPath>` 行嵌入 prompt 文本，`finalPrompt` 即展示即发送。
- 图片：`readFileAsDataUrl` 读成 `data:image/png;base64,…`，作为 `imageAttachments`
  发后端 → 模型 image block；展示层在气泡内容上方渲染缩略图。

### 1.2 目标

发送带附件的消息后：

- **非图片**：气泡上方显示文件 chip（文件名 + 大小）；消息内容含 `文件: <绝对路径>` 行。
- **图片**：渲染层读为 base64 → Go 转 image content block → 模型可见；气泡上方渲染
  缩略图；消息内容含 `图片: <绝对路径>` 行（transcript 持久化引用）。
- 两类路径行都随 `content` 持久化，刷新 / 重进会话后仍可见（chip / 缩略图为内存态）。

### 1.3 非目标

- **不做图片缩略压缩 / 降采样**：本迭代直接发原图 base64（设 10MB 上限，超限报错跳过）；
  LobsterAI 的 canvas 预览降采样留作后续。
- **不在 DB 持久化 base64**：图片只在 live session 的 LLM context 里（会话内多轮仍可见），
  重启后模型失去图像、transcript 只剩 `图片: <path>` 行。图片路径**不加入授权读集**
  （图像已由 base64 交付，避免模型 `read_file` 读到二进制垃圾）。
- **不引入模型能力开关**：darvin 唯一 provider 是 anthropic，所有已注册 Claude 模型
  `Input` 都含 `InputImage`，图片 block 无条件发送。
- **不修 HomeView 未显示暂存 chip**：暂存状态跨视图共享，Home 发送后立即跳转聊天。

## 2. 用户场景

### 场景 1: 上传文件后发送

**Given** 用户在 Composer 上传文件（暂存 chip 显示在输入框上方）
**When** 用户输入「帮我分析这个文件」并发送
**Then** 用户气泡上方显示该文件 chip；气泡内容为：

```
帮我分析这个文件

文件: /home/user/data.csv
```

模型能通过 `read_file` 读取该路径（授权读集已生效，免审批）。

### 场景 2: 上传图片后发送，模型能看到图

**Given** 用户上传一张 `logo.png`
**When** 用户输入「这个 logo 的主色调是什么？」并发送
**Then** 气泡上方显示 `logo.png` 缩略图；内容含 `图片: /home/user/logo.png`；
Go 把该图作为 image content block 发给模型，模型能正确描述图像内容。

### 场景 3: 刷新后历史消息仍引用文件 / 图片

**Given** 场景 1、2 已发送并持久化
**When** 用户重启应用 / 切换会话再切回
**Then** 消息内容仍含 `文件: /home/user/data.csv` 与 `图片: /home/user/logo.png` 行
（随 content 持久化）。chip / 缩略图为内存态，不显示（Go 不存附件元数据）。

### 场景 4: 无附件发送

**Given** 用户只输入文字
**When** 发送
**Then** 内容为原文本，不含 `文件:` / `图片:` 行，无 chip / 缩略图。

## 3. 功能需求

### FR-1: 发送时把附件路径注入消息内容

`useChatActions.send()` 按类型构造 `finalContent`：

```
finalContent = content
  + '\n\n'
  + '文件: /a/b.csv\n'          // 非图片，fileLabel 前缀
  + '图片: /a/logo.png'          // 图片，imageLabel 前缀
```

两类前缀走 i18n（`attachment.fileLabel` / `attachment.imageLabel`）。`finalContent` 作为
发给 `window.darvin.prompt` 的 `content`；同时传：

- `attachments: string[]` — 仅非图片路径（授权读集）。
- `images: DarvinImageRef[]` — 图片（含 base64 dataUrl），Go 转 image content block。

### FR-2: 图片读取为 base64

- main 新增 IPC `darvin:read_file_data_url(path)` → `{ dataUrl: 'data:<mime>;base64,…' }`
  （LobsterAI `dialog:readFileAsDataUrl` 同款；>10MB 返回错误）。
- 渲染层按扩展名识别图片（png/jpg/jpeg/gif/webp/bmp），对图片附件调该 IPC 拿 dataUrl。

### FR-3: Go LLM 层支持 image content block

- `llm.Message` 增加 `Images []llm.ImageBlock{MediaType, Data}`。
- Anthropic `convertMessages`：user 消息带图片时输出
  `[{type:"image",source:{type:"base64",media_type,data}}, {type:"text",text}]`。
- `gateway.PromptParams` / `acp.PromptRequest` / `promptReq` / `queue.Message` /
  `agent.Prompt` / dispatcher `session.Append` 全链路透传 `Images`。

### FR-4: 用户气泡上方展示 chip 与缩略图

- `appendUserMessage` 记录 `attachmentRefs`（文件）与 `imageRefs`（图片）。
- `UserMessage.vue` 在气泡内容上方渲染：图片缩略图行（`<img :src="dataUrl">`）+
  文件 chip 行（复用 `ImportedFilesBar` 风格，无移除按钮）。

## 4. 实现方案

### 4.1 类型（shared / main / Go）

**shared `darvin-api.ts`：**
```ts
export interface DarvinImageRef { path: string; name: string; size: number; dataUrl: string }
export interface DarvinReadFileDataUrlResponse { success: boolean; dataUrl?: string; error?: string }
// DarvinPromptRequest 增加：images?: DarvinImageRef[]
// DarvinApi 增加：readFileAsDataUrl(path: string): Promise<DarvinReadFileDataUrlResponse>
```

**Go `queue.ImageRef` / `llm.ImageBlock`：**
```go
// queue
type ImageRef struct { Path, Name string; Size int64; DataURL string }
// llm.Message 增加 Images []ImageBlock
type ImageBlock struct { MediaType, Data string } // Data 为去 data: 前缀的 base64
```

### 4.2 渲染层

- `useImportedFiles.ts`：`attachments` 保持 `DarvinAttachmentRef[]`（chip 展示），
  图片 dataUrl 单独存 `imageDataByPath`（path → dataUrl）map；
  `pickAttachments()` 后按扩展名把图片读成 dataUrl（`readFileAsDataUrl`），失败
  toast 并剔除该图；提供 `splitForSend()` 返回
  `{ files: DarvinAttachmentRef[], images: DarvinImageRef[] }`；
  导出 `formatBytes`、`isImagePath`。
- `useChatActions.send()`：`splitForSend()` → 构造 `finalContent`（`文件:`/`图片:` 行）→
  `appendUserMessage(sessId, finalContent, undefined, files, images)` →
  `window.darvin.prompt({ content: finalContent, attachments: files.map(p), images })`。
- `useMessages.ts`：`Message` 增加 `attachmentRefs?: DarvinAttachmentRef[]` 与
  `imageRefs?: DarvinImageRef[]`；`appendUserMessage` 增两个存储参数。
- `UserMessage.vue`：内容上方渲染图片缩略图行 + 文件 chip 行。
- `i18n.ts`：`attachment.fileLabel`、`attachment.imageLabel`、`attachment.imageTooLarge`。

### 4.3 main / preload

- `main/index.ts`：新增 `darvin:read_file_data_url` handler（fs 读文件 → base64 dataUrl，
  10MB 上限）；`darvin:prompt` 把 `req.images` 转发给 Go `client.prompt`。
- `preload/index.ts`：转发 `readFileAsDataUrl`；`prompt` 透传 images。

### 4.4 Go 链路

- `gateway/handlers.go`：`PromptParams` 增加 `Images []queue.ImageRef json:"images,omitempty"`；
  `handlePrompt` 传给 `Loop.Submit`。
- `acp/loop.go`：`PromptRequest` / `promptReq` 增加 `Images`；`admit` 透传；
  run 循环 `l.agent.Prompt(runCtx, req.content, req.attachments, req.images)`。
- `agent/dispatcher.go`：`Prompt(ctx, content string, attachments ...[]string)` 增加
  `images ...[]ImageRef`；`enqueue` 透传；run 循环
  `a.session.Append(llm.Message{Role: RoleUser, Content: msg.Content, Images: toLLMImages(msg.Images)})`，
  `toLLMImages` 把 dataUrl 拆成 `{MediaType, Data}`（解析 `data:<mime>;base64,<data>`）。
- `agent/llm/types.go`：`Message.Images []ImageBlock`。
- `agent/llm/anthropic/convert.go`：`convertMessages` user 分支带图片时输出 content 数组。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 无附件发送 | `finalContent = content`，不追加行 |
| 图片 >10MB | `readFileAsDataUrl` 返回错误，渲染层 toast「图片过大」并从待发列表剔除该图 |
| 图片读取失败（损坏 / 权限） | toast 错误，剔除该图，其余附件正常发送 |
| 重新生成带附件的消息 | 内容含路径行但无授权读集；越界读取触发权限审批（安全降级） |
| HomeView 发送 | 共用 `useChatActions.send`，行为一致 |
| 刷新后 chip / 缩略图缺失 | 可接受：路径行随 content 持久化，Go 不存附件元数据（见场景 3） |
| 同一条消息图 + 文件混发 | `文件:` 行 + `图片:` 行并存；attachments 只含非图片路径 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | `DarvinImageRef` / `DarvinReadFileDataUrlResponse`；`PromptRequest.images`；`DarvinApi.readFileAsDataUrl` |
| `src/main/index.ts` | `darvin:read_file_data_url` IPC；`darvin:prompt` 转发 images |
| `src/preload/index.ts` | 转发 `readFileAsDataUrl`；prompt 透传 images |
| `src/renderer/composables/useImportedFiles.ts` | 图片识别 + dataUrl 读取；`splitForSend`；导出 `formatBytes` / `isImagePath` |
| `src/renderer/composables/useChatActions.ts` | `send()` 注入 `文件:`/`图片:` 行 + 传 images |
| `src/renderer/composables/useMessages.ts` | `Message` 加 `attachmentRefs` / `imageRefs`；`appendUserMessage` 存储 |
| `src/renderer/components/chat/UserMessage.vue` | 气泡上方渲染缩略图 + 文件 chip |
| `src/renderer/services/i18n.ts` | 新增 `attachment.fileLabel/imageLabel/imageTooLarge` |
| `src/darvin-agent/internal/gateway/handlers.go` | `PromptParams.Images` + 透传 |
| `src/darvin-agent/internal/acp/loop.go` | `PromptRequest`/`promptReq` 加 Images + 透传 |
| `src/darvin-agent/internal/agent/queue/queue.go` | `Message.Images []ImageRef` |
| `src/darvin-agent/internal/agent/dispatcher.go` | `Prompt` 收 images；`toLLMImages`；`session.Append` 带图 |
| `src/darvin-agent/internal/agent/llm/types.go` | `Message.Images []ImageBlock` |
| `src/darvin-agent/internal/agent/llm/anthropic/convert.go` | user 带图 → content 数组（image + text block） |

## 7. 验收标准

- [ ] 场景 1：发送带文件消息后，气泡上方有 chip，内容含 `文件: <path>` 行
- [ ] 场景 2：发送图片后，气泡上方有缩略图，内容含 `图片: <path>` 行，模型能描述图
- [ ] 场景 3：刷新 / 重进会话后，内容仍含 `文件:` / `图片:` 行
- [ ] 场景 4：无附件时内容不含 `文件:` / `图片:` 行、无 chip / 缩略图
- [ ] 图片 >10MB 时报错并剔除，其余附件正常发送
- [ ] 通过 oxlint + prettier
- [ ] 通过 `npm test`
- [ ] 通过 `npm run test:go`（新增 convert 图片 block 测试）
