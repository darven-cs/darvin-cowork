# 聊天框组合（Composer 重组）

> 编号 **补充 09**。把 darvin 碎片化的聊天底部（Composer + PromptToolbar 两行、圆环在顶栏、import 独立按钮、无工作目录展示）重组为 LobsterAI 式的**单一 composer 卡片**。**纯渲染层重组，不改 Go / IPC 契约**（工作目录展示除外，见 §4.5）。

## 1. 背景

darvin 当前聊天底部是三个割裂的 UI 块，控制项散落：

- `ChatHeader`（顶栏）塞了上下文圆环 / Runtime 状态 / 主题 / 侧栏开关
- `Composer.vue`：仅 `ImportButton`（回形针）+ textarea + 发送
- `PromptToolbar.vue`（home 组件被 ChatView 复用）**挂在 Composer 下方单独一行**：+ / 专家套件 / 模型 / 语音

LobsterAI 的做法是**一个完整 composer 卡片**承载全部输入控制，圆环也放进 composer 工具栏，工作目录 + Agent 选择器作为卡片下方一条弱化 context 行。本 spec 把 darvin 向这个形态收敛。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 单一 composer 卡片承载 textarea + 全部工具栏控制 | PromptToolbar 不再单独成行，控件全部并入 Composer 内 |
| G2 | 上下文圆环移入 composer 底部工具栏（发送键左侧） | ChatHeader 不再挂 `ContextUsageIndicator` |
| G3 | `+` 菜单整合文件导入入口 | PlusMenu 保留上传/目标/待办/设置，上传项直接触发 import |
| G4 | 模型选择器进 composer 工具栏 | ModelPicker 在 composer 内渲染，home/chat 复用同一控件 |
| G5 | composer context 行展示当前工作目录 + 目录选择 | `[📁 path ▾]` 可展开 FolderSelectorPopover（添加/最近目录） |
| G6 | 底部工具栏右侧：圆环 · 模型 · 语音 · 发送（对齐 LobsterAI 顺序） | 视觉布局与 LobsterAI `CoworkPromptInput` 一致 |
| G7 | 专家/Agent 入口进 composer context 行 | Agent 选择器（当前为主 Agent，扩展留给 Agent 体系） |

## 3. 非目标

- 不实现 Agent 切换的真实逻辑（当前只有「主 Agent」，选择器先只展示）
- 不实现技能/Kit 选择（darvin 尚无 skills 体系，+ 菜单暂不含技能项）
- 不改 Go / IPC 契约（除 §4.5 新增工作目录展示所需的最小 IPC）
- 不重排顶栏主题/侧栏/运行时（只把圆环移走，其余保持）

## 4. 设计要点

### 4.1 目标布局（对齐 LobsterAI CoworkPromptInput）

```
┌────────────────────────────────────────────────────┐
│ [导入文件 chips 行]（ImportedFilesBar，保持现状）    │
│ ┌──────────────────────────────────────────────┐  │
│ │ [附件/上下文徽章行，v0 可空]                    │  │
│ │ [textarea 自适应高度]                          │  │
│ │ ┌─────────────── 底部工具栏 ────────────────┐ │  │
│ │ │ 左: [+][专家/Agent]                       │ │  │
│ │ │ 右: [⭘圆环][模型▾][🎤][↑发送]            │ │  │
│ │ └───────────────────────────────────────────┘ │  │
│ ├──────────────────────────────────────────────┤  │
│ │ context 行(弱化底): [📁 工作目录 ▾][Agent]    │  │
│ └──────────────────────────────────────────────┘  │
│ [免责声明行]                                       │
└────────────────────────────────────────────────────┘
```

### 4.2 组件归属

| 控件 | 现状 | 目标 | 说明 |
|------|------|------|------|
| 上下文圆环 `ContextUsageIndicator` | ChatHeader | Composer 工具栏右侧（发送键左） | 复用现有组件；点击压缩逻辑不变 |
| `+` 菜单（PlusMenu） | PromptToolbar | Composer 工具栏左侧 | 上传项改接 import（见 4.4） |
| 专家套件入口 | PromptToolbar grid 按钮 | Composer 工具栏左侧（`+` 右侧） | 触发 navigate('suite') |
| 模型 `ModelPicker` | PromptToolbar | Composer 工具栏右侧 | 复用组件，home/chat 同一实例 |
| 语音 `MicButton` | PromptToolbar | Composer 工具栏右侧 | 保持占位 |
| 发送按钮 | Composer | Composer 工具栏最右 | 复用现有 send 按钮 |
| 文件导入 `ImportButton` | Composer（独立回形针） | **并入 + 菜单**，工具栏不再单放 | 对齐 LobsterAI「+ → 添加文件」 |
| 工作目录 | 无 | Composer context 行左侧 | 见 4.5 |
| Agent 选择器 | 无 | Composer context 行右侧 | v0 只展示「主 Agent」+ 禁点 |

### 4.3 Composer 组件结构

新建 `ComposerToolbar.vue`（工具栏）与 `ComposerContextRow.vue`（context 行），`Composer.vue` 组装：

```vue
<!-- Composer.vue（重组后） -->
<ImportedFilesBar class="mb-1.5" />
<div class="mx-auto max-w-[760px] rounded-xl border bg-surface-2 focus-within:border-border-strong">
  <textarea v-model="text" ... @keydown="onKeydown" @input="autoGrow" />
  <ComposerToolbar @plus="..." @suite="onSuite" @compact="handleCompact" />
  <ComposerContextRow :working-dir="wd" @pick-dir="onPickDir" />
</div>
```

- 复用 `useFloatingPanel` 管理 `+` / 模型 / 目录 popover 开合（现有多面板开关机制）
- `ChatHeader.vue` 删除 `<ContextUsageIndicator :session-id="..." @compact="handleCompact" />` 与 `handleCompact`；压缩逻辑整体搬进 `Composer.vue`（读 `session.activeSessionId` + `window.darvin.compactContext` + `useMessages.begin/end/failCompact`，与现状完全一致）

### 4.4 `+` 菜单与文件导入（合并两个入口）

现状是**两条割裂的导入路径**，必须合并成一条：

| 入口 | 现状 | 问题 |
|------|------|------|
| `ImportButton.vue`（Composer 回形针） | 调 `useImportedFiles().importFiles()` → `window.darvin.importFiles()` → main 弹系统对话框 + `runImport` + `onWorkspaceChanged` | 是唯一真实可用的入口 |
| `PlusMenu` 的「上传文件」项 | `PromptToolbar.onPick('upload')` → 仅 `console.warn` | **死占位，不触发任何导入** |

**合并方案**（对齐 LobsterAI「+ → 添加文件」）：
1. `PlusMenu` 的 `upload` 项改调 `useImportedFiles().importFiles()`——与现有回形针按钮**同一实现**，无第二条导入代码路径
2. `importFiles()` 执行期间 `+` 菜单项禁用（`useImportedFiles().busy`）；跳过的文件沿用 `useImportedFiles().notice` 汇总展示
3. **删除** Composer 里独立的 `ImportButton.vue`（回形针），导入唯一入口收敛到 `+` 菜单
4. `ImportedFilesBar.vue` 保留（composer 上方 chips + workspace 用量 + 移除），它已是合并后的唯一「导入结果」展示
5. home（PromptDock）与 chat（Composer）两处的 `+` 菜单**共用同一个 `useImportedFiles` 单例**，导入行为一致

> 目标/待办/设置项保持占位（现状 `onPick` 仅 `console.warn`；v0 可继续占位，或目标项切 draftMode）。

### 4.4.1 放宽文件类型到任意类型

现状 `runImport` 有**扩展名白名单**（`src/main/libs/importFiles.ts` 的 `TEXT_FILE_EXTS`）且 main dialog 加了 text-only `filters`。用户决策放宽到任意类型：

1. `main/index.ts` 的 `dialog.showOpenDialog` **去掉 filters**（不再只选文本文件）
2. `importFiles.ts` `runImport` **删掉扩展名白名单检查**（`TEXT_FILE_EXTS.includes(ext)` 那段），保留两条硬校验：
   - `lst.isFile() && !lst.isSymbolicLink()`（拒绝 symlink / 非普通文件）
   - `lst.size <= MAX_IMPORT_BYTES`（大小上限兜底，`user-paths.MAX_IMPORT_BYTES`）
3. **二进制识别**（轻量，可选）：导入时对前 N 字节做 content sniff（含 `\0` / 高比例非 UTF-8 即判二进制），在 `DarvinImportedFile.mimeType` 记录（当前 Go 侧不填，保持 `null` 也接受）；二进制文件照常入 workspace，agent 的 `read_file` 读二进制失败属 agent 侧行为，不阻断导入
4. `TEXT_FILE_EXTS` 常量可保留（供未来「仅文本」场景复用）或删除；本 spec 内标记为不再作导入过滤

> 大小上限（`MAX_IMPORT_BYTES`）已在 `runImport` 生效，是「任意类型」放开的兜底闸门，不变更。

### 4.5 工作目录展示（最小 IPC 扩展）

darvin 的 workspace 根在 main（`user-paths.ts` 的 `WorkspaceLocation.rootPath`），为安全不透传绝对路径。展示「当前工作目录」需最小扩展：

1. `shared/darvin-api.ts`：`DarvinWorkspaceInfoResponse` 增 `label?: string`（目录 basename 或相对显示名；绝对路径不下发）
2. main `darvin:get_workspace_info`：从 `workspaceLoc.rootPath` 取 basename 下发
3. `ComposerContextRow` 左侧：`[📁 <label> ▾]`，点击弹 `FolderSelectorPopover`（新增，参考 LobsterAI）：「选择目录」调 `dialog.showOpenDialog({ properties: ['openDirectory'] })` → 更新 workspace 根 + 重建 Go 子进程（复用 `restartGoSubprocess`）；「最近目录」v0 不实现（无历史存储）

> 若「工作目录选择」v0 判定过重，可先只**展示** label（只读），选择功能列到后续 spec。展示本身零 IPC 变更成本之外的改动。

### 4.6 顶栏瘦身

`ChatHeader` 移除圆环后剩：菜单 / 标题 / Runtime 状态 / 主题 / 侧栏开关。圆环「接近上限提示」入口转移到 composer，交互不变。

## 5. 用户场景

### 场景 1：在聊天里换模型 + 看上下文
**Given** 会话上下文 78%（warning）
**When** 用户看 composer 底部工具栏右侧
**Then** 看到橙色圆环（hover 显示「78% 已用 78k / 上下文 100k 接近上限，可手动压缩」）；点模型选择器切换模型；点圆环走压缩确认流程

### 场景 2：导入文件
**When** 用户点 composer 工具栏左侧 `+` → 选「上传文件」
**Then** 系统文件选择框打开，导入成功后 composer 上方 ImportedFilesBar 出现 chips + toast「已导入文件」

### 场景 3：查看工作目录
**When** 用户看 composer context 行
**Then** 看到 `[📁 workspace-label ▾]`，hover 显示完整路径 tooltip（若只读展示）或点击打开目录选择器

## 6. 验收

- [ ] Composer 单一卡片包含 textarea + 底部工具栏 + context 行，PromptToolbar 不再单独成行
- [ ] 圆环从 ChatHeader 移入 Composer 工具栏右侧（发送键左），点击压缩流程不变
- [ ] `+` 菜单「上传文件」触发真实导入（`useImportedFiles().importFiles()`），Composer 内独立回形针 ImportButton 删除；导入结果统一由 ImportedFilesBar 展示
- [ ] 文件类型放宽：main dialog 无 filters，`runImport` 无扩展名白名单；大小上限（`MAX_IMPORT_BYTES`）与 symlink 拒绝仍生效；二进制文件照常入 workspace
- [ ] 模型 / 语音 / 专家套件入口全部并入 Composer 工具栏
- [ ] context 行展示当前工作目录（label + hover tooltip 或目录选择器）
- [ ] `npm run lint` + `npm run test` 通过；home / chat 两处 composer 均正常

## 7. 依赖

- **前置**：04（圆环组件已就绪，本 spec 只改挂载点）
- **可并行**：05 / 06 / 07 / 08
- **涉及组件**：`Composer.vue` / `ChatHeader.vue` / `PromptToolbar.vue` / `PlusMenu.vue` / `ImportButton.vue` / `ImportedFilesBar.vue` / `ModelPicker.vue` / `MicButton.vue` / `SendButton.vue` / `ChatView.vue` / `HomeView.vue` / `PromptDock.vue` / `useImportedFiles.ts`
- **涉及 main / shared**：`src/main/index.ts`（dialog filters / `darvin:get_workspace_info` 补 label）、`src/main/libs/importFiles.ts`（去白名单）、`src/shared/darvin-api.ts`（`DarvinWorkspaceInfoResponse.label`）

## 8. 参考

### LobsterAI（参考项目根：`~/桌面/github-project/LobsterAI`）

- `src/renderer/components/cowork/CoworkPromptInput.tsx`（3843 行）— 核心 composer：底部工具栏 `flex items-center justify-between px-4 pb-2 pt-1`；左 `[+][Kits][MediaModelPicker]`，右 `[⭘ contextUsageControl][ModelSelector][🎤][↑send]`；下方 `-mt-2 ... bg-black/[0.035]` context 行放 `FolderSelectorPopover` + Agent 选择器
- `src/renderer/components/cowork/CoworkSessionDetail.tsx:5219-5251` — 圆环经 `contextUsageControl` prop 注入 composer 工具栏，点击出 cancel/confirm 确认层
- `src/renderer/components/cowork/FolderSelectorPopover.tsx` — 「添加目录」+「最近目录」子菜单 + PathTooltip
- `src/renderer/components/cowork/ContextUsageIndicator.tsx` — 圆环组件（darvin 已有等价实现）

### darvin-cowork 现状文件

- `src/renderer/components/chat/Composer.vue` / `ChatHeader.vue` / `ImportButton.vue` / `ImportedFilesBar.vue`
- `src/renderer/components/home/PromptToolbar.vue` / `PlusMenu.vue` / `ModelPicker.vue` / `MicButton.vue` / `SendButton.vue` / `PromptDock.vue`
- `src/renderer/views/ChatView.vue`（Composer + PromptToolbar 两行）
- `src/main/libs/user-paths.ts`（`WorkspaceLocation.rootPath`）
