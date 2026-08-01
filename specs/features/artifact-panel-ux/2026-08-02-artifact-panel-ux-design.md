# Artifact 面板 UX 升级（参考 LobsterAI）

> 编号 **11**。spec 05 落地了面板骨架，用户反馈「不好看、不实用」；本 spec 参照 LobsterAI `ArtifactPanel.tsx`（6810 行）提炼出适用 darvin 的视觉/交互升级，剔除云服务/部署类范围。

## 1. 概述

### 1.1 问题 / 背景

spec 05 已落地：`useArtifacts` 状态机 + 10 渲染器 + 内层 tab（3 特殊 tab + per-artifact 预览 tab）+ 宽度拖拽（180-1000）+ file-based HTML 本地预览服务。live 验证全过，但面板观感与实用性不足：

- **三个特殊 tab（文件 / 浏览器 / 子代理）全是空态占位**「该面板尚未实现」——最「不实用」的点。
- **内层 tab 是纯文本按钮**：无图标、关闭按钮是文本 `×`、无激活浮起态、无 hover 反馈，观感粗糙。
- **无面板 header 动作栏**：单个 artifact 预览时没有「刷新 / 在文件夹中显示 / 用系统应用打开 / 复制」等操作。
- **preview/code 切换是独立一行两个按钮**，与内容区割裂。
- **聊天消息里没有 artifact 呈现**：artifact 只出现在右侧面板，对话流里看不到产出物，缺少「聊天 ↔ 面板」联动入口。

对照 LobsterAI（`CoworkSessionDetail.tsx` tab 栏 + `ArtifactPanel.tsx` header/动作 + `ArtifactPreviewCard.tsx` 聊天卡片 + `FileDirectoryView.tsx` 文件列表 + `autoPreviewPolicy.ts`），本 spec 提炼适用 darvin 的 5 组改进。

### 1.2 目标

把面板从「能跑」提升到「好看 + 实用」：

| # | 目标 | 度量 |
|---|------|------|
| G1 | tab 栏精修：图标 + 关闭两级 hover + 激活浮起 + 溢出渐变 | 观感对齐 LobsterAI tab 样式；tab 多时横向滚动 + 右渐变提示 |
| G2 | 面板 header 动作栏：文件名 + 类型徽标 + 刷新 / 在文件夹中显示 / 系统应用打开 / 复制 / preview-code 切换 | 每个可交互动作 live 可点、无报错 |
| G3 | 文件列表 tab 实用化：列出当前 session workspace 文件，搜索 + 类型分组 + 路径缩写 + 点击打开预览 | 列表非空、搜索过滤、html 文件点击出预览 |
| G4 | 浏览器 tab：地址栏 + iframe 嵌入任意 URL | 输入 URL 回车 → iframe 加载 |
| G5 | 聊天消息内 artifact 卡片组：默认折叠 3 张 + 展开全部 + 点击联动打开面板 | assistant 消息旁出现卡片组，点击打开对应 tab |
| G6 | 子代理 tab 占位文案优化（darvin 无子代理体系，明示待接入） | 不再显示「尚未实现」，显示定向文案 |

### 1.3 非目标

> **落地补充（实现期决议）**：按用户反馈移除右侧面板外层空壳的 `tools` / `thinking` tab（LobsterAI 无此结构，thinking / 工具调用本已内联在聊天流中）——右侧面板现直接渲染 ArtifactPanel；`useSidePanel` 只保留 `open` 状态，删除 `SidePanelTabs.vue` / `SidePanelContent.vue`。另：媒体文件（svg/image/video）经共享 composable `useArtifactPreviewUrl` 复用本地预览会话；预览服务器补 video/更多 image MIME。

- 不做 HTML 分享/部署、内置浏览器（带标注/设备仿真/本地服务发现）、office 文档渲染、子代理体系、tab 拖拽排序、拖拽关闭面板阈值、`autoPreviewPolicy` 自动开面板（darvin 事件流无 turn 完成信号可挂钩，价值低）。

## 2. 用户场景

### 场景 1：tab 观感
**Given** 面板内开了 4+ 个 artifact tab
**When** 用户看 tab 栏
**Then** 每个 tab 有文件类型图标 + 截断名 + 激活浮起态；hover tab 关闭钮出现灰圆、再 hover 变高对比；tab 超出宽度横向滚动并在右侧显示渐变遮罩

### 场景 2：文件列表
**Given** 当前 session workspace 有若干文件（含 html / 图片 / 文本）
**When** 用户点内层「文件」tab
**Then** 显示按类型分组的文件列表（搜索框 + 类型分组头 + 路径缩写 + 类型小标签）；点 html 文件打开预览 tab

### 场景 3：浏览器 tab
**When** 用户点「浏览器」tab，输入 `https://example.com` 回车
**Then** iframe 内嵌加载该页面；地址栏显示当前 URL

### 场景 4：聊天内 artifact 卡片
**Given** assistant 消息关联了 5 个 artifact
**When** 用户在对话流看该消息
**Then** 消息下方显示卡片组（默认 3 张 + 「显示全部 5」展开），点卡片打开右侧面板对应预览 tab

### 场景 5：header 动作
**When** 用户预览一个 filePath 型 html artifact
**Then** header 显示文件名 + `html` 徽标 + 动作按钮；点「在文件夹中显示」打开系统文件管理器定位该文件；点「用系统应用打开」调用系统默认程序

## 3. 功能需求

### FR-1: tab 栏视觉精修
- 特殊 tab 与预览 tab 统一加 SVG 图标（文件列表 / 浏览器 / 子代理 / 按 artifact 文件类型）。
- 关闭按钮两级 hover：默认透明 → hover 整 tab 显示灰圆 → hover 按钮本身高对比前景色（`ArtifactPanel.tsx:971-972` 同款）。
- 激活 tab 浮起态：`bg-surface-raised text-text shadow-sm`；非激活 `text-text-muted hover:bg-surface`。
- 横向滚动：`overflow-x-auto` + 隐藏滚动条；超出时右侧 `w-12` 渐变遮罩（`backdrop-blur-sm [mask-image:...]`）。

### FR-2: 面板 header 动作栏
- 内容区顶部 40px header：左 `fileName truncate` + 类型小徽标（uppercase muted）；右动作按钮组。
- 动作与显隐（按 artifact 属性布尔推导）：
  - 刷新（所有可重渲染 kind）：给 ArtifactRenderer 加 key 强制重挂载。
  - 复制代码（code / markdown / html / svg / text / document）：复用 `useChatActions.copy`。
  - 在文件夹中显示（`filePath` 存在）：新 IPC `reveal_workspace_file`。
  - 用系统应用打开（`filePath` 存在）：新 IPC `open_workspace_file`。
  - preview / code 切换：并入 header 右侧为分段控件（`preview` / `code`，仅非 `NON_CODE_TYPES` 显示 code 项）。

### FR-3: 文件列表 tab
- 新 IPC `list_workspace_files()`：递归列 workspace 目录（深度 ≤3、文件数 ≤500），返回 `{ files: [{ relativePath, name, kind, size, modifiedAt }] }`；kind 按扩展名映射（复用 `DarvinArtifactKind` 集合）。
- 新 IPC `read_workspace_file(relativePath)`：读文本文件（≤256KB），返回 `{ success, content?, error? }`；非文本类型返回 `unsupported`。
- `FileListView.vue`：搜索框（`focus:border-primary`）+ 按类型分组（html/svg/image/video/mermaid/markdown/text/code/document/local-service）+ 行 = 图标 + 文件名 + 路径缩写（`.../末两段`）+ type 小标签；选中 `bg-primary/10 text-primary`。
- 点击文件 → 打开预览 tab：
  - html/svg/image/video → 合成 `{ id: 'file:<relPath>', kind, name, content:'', filePath: relPath }` → 走既有本地预览服务（HtmlRenderer 文件分支 / 新增的媒体文件分支）。
  - markdown/text/code → `read_workspace_file` 读内容 → 合成 inline artifact 打开。

### FR-4: 浏览器 tab
- `BrowserTab.vue`：地址栏（input + 回车跳转）+ iframe 嵌入 URL；初始 `https://example.com` 占位；返回/前进/刷新按钮可选（v0 至少地址栏 + iframe）。
- 复用 `local-service` 渲染器的 iframe 模式（不加 sandbox，用户自己输入的 URL）。

### FR-5: 子代理 tab
- 占位文案改为 `artifact.subagents.placeholder`：「子代理能力尚未接入」/「Subagents not wired up yet」。

### FR-6: 聊天消息内 artifact 卡片组
- 协议：`artifact` 事件加 `messageId?: string`（产出该 artifact 的 assistant 消息 id，向后兼容）。
- `Message` 接口加 `artifacts?: Artifact[]`；`useMessages.appendEventFor` 收到带 `messageId` 的 artifact 事件时挂到对应 assistant 消息，同时照常进 `useArtifacts` store。
- `AssistantTurnBlock` 在 content 段后渲染 `ArtifactCardGroup.vue`：`rounded-lg border divide-y` 卡片组，默认 3 张 + 「显示全部 N」；每行 32px 图标块 + 标题 + 副标题（hover 切动作提示）+ 右侧「打开」；点击 `useArtifacts.openPreviewTab(sessionId, artifactId)` + 弹开面板。

### FR-7: 特殊 tab 空态收敛
- 文件 / 浏览器 tab 改为真实内容（FR-3 / FR-4）；子代理走 FR-5；无 artifact 时面板空态文案保留。

## 4. 实现方案

### 4.1 tab 栏精修（`ArtifactPanel.vue` + 新图标）

- 图标：新增 `file-list.svg`、`browser.svg`（地球）、`subagents.svg`、`external-open.svg`；预览 tab 图标按 kind 映射（html→`file-text`，code→`terminal`，markdown→`file-text`，text→`file-text`，image→`file-text` 兜底，等等；v0 用统一 `file-text` + 类型色块即可，不引入文件类型图标库）。
- 关闭按钮结构：
  ```
  <span role="button" class="mr-1 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full text-transparent transition-colors group-hover:bg-surface-hover group-hover:text-text hover:!bg-text hover:!text-bg">
    <Icon name="x" :size="10" />
  </span>
  ```
- 激活态 `bg-surface-raised text-text shadow-sm`；tab 容器加 `group`。
- 溢出渐变：tab 栏容器 `overflow-x-auto`，外层 relative；`scrollWidth > clientWidth` 时渲染右侧 `pointer-events-none absolute right-0 w-12 bg-gradient-to-l from-bg to-transparent`；用 `resize`/`scroll` 事件维护 `canScrollRight` ref。

### 4.2 header 动作栏（`ArtifactPanel.vue` 重构内容区顶部）

```
[📄 name truncate] [html]  ────flex-1────  [preview|code 分段] [刷新] [复制] [文件夹] [系统打开]
```

- 分段控件：`preview` / `code` 两段，激活段 `bg-surface-hover text-text`；`NON_CODE_TYPES = {document, image, video, text, local-service}` 时只渲染 `preview` 段并禁用切换。
- 刷新：`reloadKey` ref，ArtifactRenderer `:key="reloadKey"`，点击 `reloadKey++`。
- 动作布尔：`hasFilePath = !!activeArtifact.filePath`；`canCopy = kind ∈ {html, svg, code, markdown, text, document}`。
- `reveal_workspace_file` / `open_workspace_file` IPC（见 4.5）。

### 4.3 文件列表 tab（`FileListView.vue` + main IPC）

main 新增两个 IPC（沿 `remove_imported_file` 的 realpath 防越界模式）：
```ts
// darvin:list_workspace_files → { files: WorkspaceFileInfo[] }
async function walkWorkspace(root: string, depth: number, cap: number): Promise<WorkspaceFileInfo[]>
// kind 映射：.html→html .svg→svg .png/.jpg→image .mp4→video .md→markdown
// .txt→text .js/.ts/.py→code 其余→document
```
```ts
// darvin:read_workspace_file(relPath) → { success, content?, size?, error? }
// realpath 校验在 workspace 根内；>256KB 返回 { success:false, error:'too_large' }
```

`FileListView.vue`：props `{ sessionId }`；`onMounted` 拉 `list_workspace_files`；搜索过滤（文件名/路径 includes）；按 `kind` 分组排序；行点击按 4.3 规则合成 artifact 并 `openPreviewTab`。空态 / 无结果文案走 i18n。

### 4.4 浏览器 tab（`BrowserTab.vue`）

```
[🔍 url input] [go]
[iframe :src="url" class="min-h-0 flex-1 border-0"]
```
- 初始 `url = 'https://example.com'`；回车提交更新；ifreame 不加 sandbox（用户输入的可信 URL）。
- 放置于 `ArtifactPanel` 特殊 tab 内容区。

### 4.5 main / shared / preload 契约

`src/shared/darvin-api.ts`：
```ts
export interface DarvinWorkspaceFileInfo {
  relativePath: string; name: string; kind: DarvinArtifactKind; size: number; modifiedAt: number;
}
export interface DarvinListWorkspaceFilesResponse { files: DarvinWorkspaceFileInfo[]; }
export interface DarvinReadWorkspaceFileResponse {
  success: boolean; content?: string; size?: number; error?: string;
}
// DarvinApi:
listWorkspaceFiles(): Promise<DarvinListWorkspaceFilesResponse>;
readWorkspaceFile(relativePath: string): Promise<DarvinReadWorkspaceFileResponse>;
revealWorkspaceFile(relativePath: string): Promise<void>;
openWorkspaceFile(relativePath: string): Promise<{ success: boolean; error?: string }>;
// artifact 事件加可选字段：
filePath?: string; messageId?: string;
```
`src/main/index.ts` 注册 4 个 handler；`src/preload/index.ts` 转发。

### 4.6 聊天内 artifact 卡片（协议 + 渲染）

- `useMessages.appendEventFor` artifact 分支：若 `ev.messageId` 命中 bucket 中 assistant 消息 → `msg.artifacts = [...(msg.artifacts ?? []), artifact]`；仍调 `artifacts.addArtifact`。
- `Message` 加 `artifacts?: Artifact[]`（`Artifact` 从 `useArtifacts` 导入）。
- `AssistantTurnBlock.vue` content 段后：`<ArtifactCardGroup v-if="msg.artifacts?.length" :session-id="sid" :artifacts="msg.artifacts" />`。
- `ArtifactCardGroup.vue`：`divide-y` 卡片组；`visibleCount = ref(3)`；「显示全部 N」/「收起」按钮；每行 = 图标块 + 标题 + hover 副标题 + 打开按钮；点击 `useArtifacts.openPreviewTab(sessionId, id)`（`setPanelOpen` 已负责弹开面板 + 切外层 tab）。

### 4.7 i18n 新增 key（zh/en 对齐）

`artifact.special.fileList/browser/subagents`（已有）、`artifact.subagents.placeholder`、`artifact.fileList.search`、`artifact.fileList.empty`、`artifact.fileList.noResult`、`artifact.browser.placeholder`、`artifact.browser.go`、`artifact.actions.refresh`、`artifact.actions.copy`、`artifact.actions.reveal`、`artifact.actions.openExternal`、`artifact.chat.showAll`、`artifact.chat.collapse`、`artifact.chat.open`。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| workspace 目录很大 / 深 | `list_workspace_files` 深度 ≤3、文件数 ≤500，超出静默截断（返回已收集部分） |
| 读大文本文件 | `read_workspace_file` >256KB 返回 `too_large`，列表点击时提示 |
| `list_workspace_files` 时 Go 离线 | main 直接走 Node fs 读目录（不依赖 agent），恒可用 |
| filePath 越界 | realpath 校验抛错 → IPC 返回 error，renderer 不展示绝对路径 |
| 浏览器 tab 输入不可达 URL | iframe 显示浏览器默认错误页，不额外处理 |
| 聊天卡片 artifact 缺 messageId（老事件 / Go 不产） | 只进 useArtifacts store，不挂消息（兼容） |
| 关闭面板时刷新任务 | 无状态泄露（reloadKey / 预览会话随组件卸载清理） |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | 加 `WorkspaceFileInfo` / list / read / reveal / open 类型 + 2 个 IPC 方法；`artifact` 事件加 `messageId?` |
| `src/preload/index.ts` | 转发 4 个 IPC |
| `src/main/index.ts` | 注册 `list_workspace_files` / `read_workspace_file` / `reveal_workspace_file` / `open_workspace_file` |
| `src/renderer/composables/useArtifacts.ts` | `Artifact` 加 `messageId?`（可选） |
| `src/renderer/composables/useMessages.ts` | artifact 事件按 `messageId` 挂到 assistant 消息；`Message.artifacts?` |
| `src/renderer/components/side-panel/ArtifactPanel.vue` | tab 精修 + header 动作栏 + 分段控件 + 文件/浏览器 tab 挂载 |
| `src/renderer/components/side-panel/FileListView.vue` | 新增：文件列表 tab |
| `src/renderer/components/side-panel/BrowserTab.vue` | 新增：地址栏 iframe |
| `src/renderer/components/side-panel/ArtifactCardGroup.vue` | 新增：聊天卡片组 |
| `src/renderer/components/chat/AssistantTurnBlock.vue` | content 段后渲染 ArtifactCardGroup |
| `src/renderer/services/i18n.ts` | 新增 ~14 key（zh/en） |
| `src/renderer/assets/icons/` | 新增 `browser.svg` / `subagents.svg` / `external-open.svg` / `file-list.svg` |
| 测试 | `useMessages` artifact 挂消息单测；main workspace walk 单测（越界/截断） |

## 7. 验收标准

- [ ] FR-1：tab 有图标 + 关闭两级 hover + 激活浮起；4+ tab 时横向滚动 + 右渐变（live 验证）
- [ ] FR-2：header 动作栏各动作 live 可点；`reveal_workspace_file` 打开文件管理器定位、`open_workspace_file` 调系统程序、刷新重渲染、复制进剪贴板
- [ ] FR-3：文件列表列出 workspace 文件 + 搜索过滤 + 类型分组；点 html 文件开预览（走本地服务）；点 md/txt 读内容开预览
- [ ] FR-4：浏览器 tab 输入 URL 回车 iframe 加载
- [ ] FR-6：注入带 `messageId` 的 artifact 事件 → 消息下方出现卡片组，点卡片打开面板对应 tab
- [ ] 越界 / 大文件 / Go 离线边界按 §5 处理，无绝对路径泄露
- [ ] `npm run lint` + `npm run test`（新增用例）+ renderer + main vite build 通过
