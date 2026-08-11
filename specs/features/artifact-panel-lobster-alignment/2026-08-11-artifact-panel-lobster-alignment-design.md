# Artifact 面板对齐 LobsterAI 设计文档

> 编号 **13**。对齐 LobsterAI `ArtifactPanel.tsx`（6810 行）+ `main.ts`（11194 行）+ `artifactParser.ts`（1033 行）：Browser 载体 / URL 状态 / local-service / 导航守卫 / 文件引用捕获 / 文档预览。上一份 `specs/features/url-open-in-artifact-browser/2026-08-11-...` 的「URL 状态 + 链接拦截 + 导航守卫」是 Phase A 子集，并入本设计。
> 修订历史：2026-08-11 按 peer review 修正 Go 挂点（harness→bus 订阅者）、webviewTag、端口探测方案、pptx 预处理、虚拟滚动选型、测试策略收敛。

## 1. 概述

### 1.1 问题 / 背景

darvin 的 Artifact 面板（spec 05/11 落地）与 LobsterAI 的差距：

| 维度 | LobsterAI | darvin 现状 |
|------|-----------|-------------|
| **Browser 载体** | Electron `<webview>`（`ArtifactPanel.tsx:6456`）+ `will-attach-webview` 加固 + 独立 partition + **`webviewTag: true`**（`main.ts:10106`） | `<iframe>`（`BrowserTab.vue`），无浏览器能力；主窗口 `webPreferences` 缺 `webviewTag` |
| **URL 状态** | `browserAddress`/`browserUrl` 受控 props 覆盖**组件级 local state**（`useState('')`，`ArtifactPanel.tsx:1045-1046`）；本地 html 预览时 address 显示 filePath | 组件内本地 `ref`，默认 `https://example.com`，外部不可控 |
| **local-service** | renderer 解析 `localhost:PORT` + main HTTP 探测；选中切 Browser tab（`openLocalServiceArtifact` 1733-1744）；Browser 空态列 services | Go 侧**从不产生** `local-service` artifact（预留死类型） |
| **文件引用捕获** | renderer 正则扫消息流（`artifactParser.ts`）：`file://` 链接 / 裸路径（含图片视频）/ `MEDIA:` token / write 工具 / tool_result assets | **无捕获机制** |
| **文档预览** | `.md`(react-markdown) / `.docx`(docx-preview) / `.pdf`(pdfjs) / `.xlsx`(SheetJS 自绘表格) / `.pptx`(pptx-preview + JSZip 预处理) | `DocumentRenderer.vue` 仅 `<pre>` 显示文本；md 预览已有 |
| **导航守卫** | `setWindowOpenHandler → shell.openExternal + deny`（`main.ts:10130`）；`will-navigate` 只挂**子窗口**（10174） | **无**——点链接整个主窗口被导航走 |

**关键现实**：
- **darvin 的 Go agent 从未发送过 `artifact` 事件**（`internal/agents/event/event.go` 20 个事件无 ArtifactEvent；`internal/gateway/eventledger.go:164-273` 的 `mapEventToTS` switch 无 artifact case）。`artifact` 事件类型仅定义在 `shared/darvin-api.ts`，renderer 的 `useArtifacts` + 10 渲染器都在等待数据源，从未接入。**任何捕获机制都依赖先建 Go artifact 事件基础设施。**
- 捕获必须走 **bus 订阅者**（仿 `TextDeltaHook.Attach`，`internal/agents/text_delta_hook.go`，在 `factory.go:93-94` attach），因为嵌入式 harness 返回空 `AttemptResult`（`runtime/harness.go:18-28`）且 loop 丢弃返回值。
- 用户决策：**不做 HTML 分享 / NodeDeployment 部署**；**完整纳入文件引用捕获 + office 文档预览**（推翻 spec 11 的「office 渲染不做」）。

### 1.2 目标

- **Phase A**：Browser tab `iframe→webview`，URL 状态受控化（per-session），导航守卫三件套，任何点击不再导航走主窗口。
- **Phase B**：Go artifact 事件基础设施（bus 订阅者）+ 统一捕获（local-service + 文件引用 + 远程图片 + write_file 产物），打通 local-service 切 Browser tab + 空态列 services。
- **Phase C**：文档预览——`DocumentRenderer` 按扩展名分派（md 复用 / docx / pdf / xlsx / pptx），数据走现有 `readFileAsDataUrl` IPC。

### 1.3 非目标

- 不做 LobsterAI 的登录 / 订阅 / 配额 / 云端上传 / 云端 URL。
- **不做 HTML 分享 / NodeDeployment 部署——用户决策，范围外。**
- 不做 webview 的截图 / 网页标注 / 设备仿真 / 清缓存 cookies（留作后续增强）。
- 不做局域网 0.0.0.0 绑定。
- 不做 office **编辑**（只读预览）；不做 xlsx 公式重算 / 打印。

## 2. 用户场景

### 场景 1: 聊天里点链接 → 面板 Browser tab（Phase A）
**Given** 聊天 markdown 里有 `<a href="https://example.com/docs">`
**When** 用户点击
**Then** 主窗口不导航；右侧面板自动展开，激活 Browser tab，webview 加载该 URL，地址栏回显。

### 场景 2: 浏览器 tab 有完整浏览器能力（Phase A）
**Given** Browser tab 已打开某页面
**When** 用户点后退 / 前进 / 刷新 / 放大 / 在系统浏览器打开
**Then** webview 相应操作生效；地址栏回显当前 URL；「在系统浏览器打开」走 `shell.openExternal`。

### 场景 3: LLM 提到本地 node 服务（Phase B）
**Given** assistant 回复文本里出现 `http://localhost:5173`
**When** 消息流式完成
**Then** 生成 `local-service` artifact（带 `url`）；点击它 → 切 Browser tab 加载；Browser 空态列出本 session services（带标题 + 在线标记）。

### 场景 4: agent 输出文档引用 → 预览（Phase B + C）
**Given** assistant 消息含 `[报告](file:///workspace/report.md)`、裸路径 `/workspace/data.csv`、`MEDIA: report.docx`、本地图片 `/workspace/chart.png`，或 agent 用 `write_file` 写了 `script.py`
**When** turn 完成
**Then** 生成对应类型 artifact（markdown/text/code/document/image/video）；点它 → 面板渲染（md → markdown；txt/csv → 文本；py/js → 代码；png → 图片；docx/pdf/xlsx/pptx → office 渲染）。

### 场景 5: 任意 window.open / target=_blank 兜底（Phase A）
**Given** 主窗口 DOM 某处触发 `window.open`（含 `LocalServiceRenderer` 旧「新窗口打开」链接）
**When** 触发
**Then** 不新建 Electron 窗口、不导航；URL 交系统浏览器。

## 3. 功能需求

### FR-A1: Browser 载体升级 webview
`BrowserTab.vue` 用 Electron `<webview>` 替换 iframe：
- **前置条件**：main `createWindow()` 的 `webPreferences` 加 `webviewTag: true`（LobsterAI `main.ts:10106`；无此开关 `<webview>` 不渲染）。
- `webview` 在 Vue 注册 custom element（`isCustomElement`）。
- TS declare `WebviewElement`（`loadURL / goBack / goForward / reload / stop / getURL / getTitle / setZoomFactor`）。
- 独立 partition `persist:artifact-browser`。

### FR-A2: URL 状态受控化
`useArtifacts` 增加 `browserUrlBySession` / `browserAddressBySession` + `setBrowserUrl` / `setBrowserAddress` / `openBrowser(sid, url)`（set 两值 + `activateTab(Browser)`）。`BrowserTab.vue` 读受控值；本地 html 预览时 address 显示 filePath、url 为 `http://127.0.0.1` 会话地址（双值分离，对齐 LobsterAI 1166-1167）。`clearSessionArtifacts` / `reset` 清理。
**地址栏回写**：webview 事件 `did-navigate`（主帧跳转）→ 更新 url+address；`did-navigate-in-page`（hash/SPA 内跳）→ 更新 url；`did-stop-loading` → 用 `getURL()` 兜底回写；`page-title-updated` → 更新标题。

### FR-A3: markdown 链接拦截
`MarkdownContent.vue` 根节点 `@click.capture`：命中 `<a>` 且非 `#` 锚点 → `preventDefault`；http(s) → `openBrowser(activeSessionId, href)`；mailto/tel/相对路径 → `window.darvin.openExternal(href)`，失败 toast。

### FR-A4: 导航守卫三件套（main/index.ts `createWindow`）
- `setWindowOpenHandler({ url })` → `shell.openExternal(url)` + `{ action: 'deny' }`。
- `will-navigate`：应用自身 origin（dev server / `file:`）放行，其余 `preventDefault` + `shell.openExternal`。**注：这是 darvin 增强**——LobsterAI 的 `will-navigate` 只挂子窗口（`main.ts:10174`），主窗口靠 renderer 拦截；darvin 补主窗口守卫属新增安全面（darvin 无 vue-router，纯内存 view 切换，不会误伤 SPA 内部导航）。
- `will-attach-webview`：删 preload、禁 nodeIntegration / plugins、强制 sandbox + contextIsolation + webSecurity、设 partition、`params.allowpopups='false'`、`javascript:` 拦截。
- 协议白名单 `https?|mailto|tel:`。

### FR-A5: `openExternal` IPC
`DarvinApi.openExternal(url) → { success: boolean }`；preload 桥接；main handler 走 `shell.openExternal`（协议白名单）。

### FR-B0: Go artifact 事件基础设施
- `internal/agents/event/event.go` 新增 `ArtifactEvent{ SessionID, ArtifactID, Kind, Name, Content, FilePath, URL, MessageID, CreatedAt }` + `EventName() = "artifact"`。
- `internal/gateway/eventledger.go` `mapEventToTS` switch 新增 `case event.ArtifactEvent:` 映射到 `DarvinEvent['artifact']`。
- renderer `useMessages.appendEvent`（useMessages.ts:631-652）已处理 `artifact` 类型，无需改。

### FR-B1: 捕获订阅者（bus，非 harness）
新增 `internal/agents/artifact_hook.go`，仿 `TextDeltaHook.Attach`（`text_delta_hook.go:30-56`）：
- `Attach(a *Agent)` 里 `a.Subscribe(64)` + goroutine 消费；构造时**注入工作区根**（`factory.go` `NewSessionRuntime` 处从 `f.Config.Workdir` 捕获，因 `RunAttemptParams.WorkspaceDir`（execution.go:63）从未被赋值）。
- **自攒文本**：累积 `TextDeltaEvent.Delta`（先例：factory.go subagent runner 文本累积）；在 **`TurnEndEvent`（工具之后）** 扫描（`LLMEndEvent.Assistant.Content` 在工具执行之前发，不能作为扫描源）。
- 扫描产物（按 `url`/`filePath` 去重），emit `ArtifactEvent`：
  - `http://(localhost|127\.0\.0\.1|\[::1\]):\d+` → `local-service`（`URL=url`）
  - `[text](file:///abs)` 链接 / 裸路径（目录分隔 + 已知扩展名）→ 按扩展名：`png/jpg/gif/webp/bmp/avif/mp4/webm/mov` → `image`/`video`；`docx/xlsx/pptx/pdf` → `document`；`md` → `markdown`；`txt/log/csv/json` → `text`；其余 → `code`（`FilePath=相对工作区根`）
  - `MEDIA: <path>` token → 按扩展名同上
  - 远程图片 URL（`.png/.jpg/...`）→ `image`（`Content=url`）
- 所有产物**必须带正确 `MessageID`**（useMessages.appendEvent 只在 `ev.messageId` 存在时才挂聊天卡片组，useMessages.ts:644）。

### FR-B2: write_file 产物捕获（executor，非 tools/）
- 工具名是 **`write_file`**（`internal/tools/fs.go:85`，无别名；项目无 image_gen/video_gen 工具）。
- 捕获放 **`executor.executeOneTool`**（`executor.go:469-473`，紧跟 `res = t.Execute(...)` 的 `todos.WriteToolName` 分支是现成先例——`c.Arguments` / `res` / `d` 都在作用域）：`c.Name == write_file` 时从 `c.Arguments["path"]` / `["content"]` + `res` 提取，emit 对应类型 artifact（文本/代码带 content，图片/文档仅 path）。
- tools/ 侧**不注入 bus**（bus 是 per-Agent 的，工具在 `loadTools` 已构造无 bus 引用），故不在 tools 层做。

### FR-B3: `Artifact.url` + LocalServiceRenderer 对齐
`Artifact` 类型加 `url?: string`。选中 `local-service` → `openBrowser(sid, url)`（不走预览 tab）；`LocalServiceRenderer` 改为「URL 卡片 + 在浏览器标签打开 / 复制」。

### FR-B4: Browser 空态列 services + HTTP 探测
`BrowserTab.vue` 无 URL 空态列本 session local-service（过滤 kind），点卡片 `openBrowser`。
main `listLocalWebServices` IPC **HTTP GET 探测**（对齐 LobsterAI `probeLocalWebService` `main.ts:917`）：`session.defaultSession.fetch(url)` + **700ms AbortController**（`LOCAL_WEB_SERVICE_PROBE_TIMEOUT_MS=700`，`main.ts:399`），要求 `text/html` 并提取 `<title>`，返回 `{ id, title, url, host, port, online }`。**不用纯 TCP connect**（会把数据库等非 HTTP 端口误报 online，且拿不到标题）。

### FR-C1: DocumentRenderer 扩展名分派
`DocumentRenderer.vue` 从 `<pre>` 升级为按 `artifact.filePath`/`name` 扩展名分派：
- `.md` → 复用 MarkdownRenderer；`.txt/.log/.csv/.json` → 文本；`.py/.js/...` → CodeRenderer
- `.docx` → `DocxSubRenderer`（docx-preview `renderAsync` 直接 DOM，对齐 DocumentRenderer.tsx:129）
- `.pdf` → `PdfSubRenderer`（pdfjs-dist canvas 逐页懒加载；worker 用 `new URL('pdfjs-dist/build/pdf.worker.mjs', import.meta.url)`，对齐 :373）
- `.xlsx/.xls` → `SheetSubRenderer`（xlsx 解析 → 自绘 HTML 表格：列头 + 合并单元格 + 背景色/粗体 + **手写轻量虚拟滚动**，见 4.4）
- `.pptx` → `PptxSubRenderer`（**JSZip 预处理 + pptx-preview 注入 iframe**，见 4.4）

### FR-C2: 文档数据读取
文档内容来自 `Artifact.content`（文本内联）或 `window.darvin.readFileAsDataUrl(filePath)`（二进制 → dataURL → ArrayBuffer）；文件缺失/过大 → 错误态 + 系统应用打开兜底。

## 4. 实现方案

### 4.1 阶段划分

| 阶段 | 内容 | 规模 | 依赖 |
|------|------|------|------|
| **A** | webview 载体（含 webviewTag）+ URL 受控 + 链接拦截 + 导航守卫 + openExternal IPC | 中 | 无 |
| **B** | Go ArtifactEvent + 捕获订阅者 + write_file 捕获 + local-service 交互 + HTTP 探测 | 中 | A（Browser tab 就绪） |
| **C** | DocumentRenderer 分派 + docx/pdf/xlsx/pptx 渲染器 | 中 | B（artifact 数据源就绪） |

### 4.2 Phase A 关键决策

**webview vs iframe**：对齐 LobsterAI 用 webview。跨域 iframe 无法控制导航，webview 有完整 DOM API + 独立 session partition。Electron 官方虽警示 webview，但 LobsterAI 已示范加固范式；「浏览器载体」是例外，AI 产物渲染器仍严格走 sandbox iframe（CLAUDE.md 约束不变）。**前置硬条件**：`webPreferences.webviewTag = true`。

**Vue 适配**：
```ts
// src/renderer/index.ts
app.config.compilerOptions.isCustomElement = (tag: string) => tag === 'webview';
```
`src/renderer/types/webview.d.ts` declare `WebviewElement`。

**will-attach-webview 加固**（main/index.ts）：见 FR-A4 清单。

### 4.3 Phase B 关键决策

**捕获挂点 = bus 订阅者，不是 harness**：嵌入式 harness 返回空 `AttemptResult`（`runtime/harness.go:25`）且 loop 丢弃（`loop.go:371`），harness 层是死路。正确挂点仿 `TextDeltaHook`：`artifact_hook.go` 订阅 bus，在 `factory.go NewSessionRuntime` 里 attach，并从 `f.Config.Workdir` 注入工作区根（`RunAttemptParams.WorkspaceDir` 未赋值）。

**扫描时机 = TurnEndEvent**（工具之后）：`LLMEndEvent.Assistant.Content`（executor.go:245-256）在工具执行前发；`TurnEndEvent`（event.go:158-166）不带文本但发生在工具后。订阅者自攒 `TextDeltaEvent`，TurnEnd 时扫描——保证 write_file 产出的文件也在扫描窗口内。write_file 捕获另在 `executeOneTool` 单独 emit（文件内容/路径最准）。

**去重**：同 run 内按 `url`/`filePath` 去重，避免流式重复；`ArtifactEvent` 的 `MessageID` 必须正确（聊天卡片组依赖它）。

### 4.4 Phase C 关键决策

**依赖**（renderer，新增）：`docx-preview` / `pdfjs-dist` / `xlsx` / `pptx-preview` / `jszip`。

**pptx 需 JSZip 预处理**（对齐 `DocumentRenderer.tsx:690-999`）：渲染前补 content-types 缺省、`createPptxPreviewMediaPath` 媒体路径归一、`[Content_Types].xml` 扩展名补全、非标准媒体复制到 `ppt/media/image*`。不能一行 `pptx-preview` 直接注入 iframe。

**xlsx 虚拟滚动手写**：CLAUDE.md 禁第三方组件库（vue-virtual-scroller 属库，不引）。`SheetSubRenderer` 用 `scrollTop + 行高估算` 计算可见行区间 + 上下 padding 占位，实现轻量虚拟滚动（对齐 tanstack `useVirtualizer` 的效果但零依赖）。大表（>5000 行）才启用，小表全渲染。

**安全**：office 文档来自 agent 生成的 workspace 文件，属本地可信内容。docx-preview / SheetJS 直接 DOM 渲染；pptx-preview 注入 iframe `contentDocument`。**不**执行文档内脚本、不引入云端。

**DocumentRenderer 结构**：`useFileContent` composable 统一读数据（内联 content 优先，缺则 `readFileAsDataUrl`）。

### 4.5 与既有 spec 的关系

- `specs/features/url-open-in-artifact-browser/2026-08-11-...`：Phase A 子集，并入本 spec；文件保留为历史。
- `specs/features/artifact-panel-ux/2026-08-02-...`：曾列「分享/部署、内置浏览器、本地服务发现、office 文档渲染」为非目标。本 spec 对齐「内置浏览器 + 本地服务发现 + office 渲染」，**推翻**这三条；「分享/部署」维持不做。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| webview 不渲染 | `webviewTag: true` 前置条件；`isCustomElement` 注册 |
| webview 内页面跨域 / 沙箱弹窗 | `will-attach-webview` 禁 popups；独立 partition 隔离 |
| 恶意 `javascript:` URL | will-attach-webview 拦截；openExternal 协议白名单拒绝 |
| 无激活 session 点链接 | `openBrowser` sid null → 走 `openExternal` 兜底 |
| 相对路径 / mailto / tel 链接 | 非 http(s) → `openExternal` |
| LLM 文本多个 localhost 端口 | 每端口一个 local-service artifact，按 url 去重 |
| local-service 已下线 / 非 HTTP | HTTP 探测（700ms）判定 offline；探测到非 text/html → 不算 online |
| 裸路径误抓（普通英文词） | 正则要求目录分隔 + 已知扩展名（对齐 `BARE_FILE_PATH_RE` 764 行）；`ls` 类工具输出白名单防误抓 |
| 文件不存在 / 过大 / 二进制 | `readFileAsDataUrl` 失败 → 错误态 + 系统应用打开兜底 |
| docx/xlsx 含宏 / 外部引用 | 只读解析不执行；渲染异常 → fallback 文本 |
| pdf worker 加载失败 | vite `?url` 引入 worker；失败回退错误态 |
| xlsx 大表性能 | 手写虚拟滚动（>5000 行启用）；小表全渲染 |
| 捕获产物无 messageId | 扫描器必须注入当前 assistant messageId，否则只进面板不挂聊天卡片组 |
| 会话切换 / 删除 | browser URL 按 session 隔离；`clearSessionArtifacts` 同步清理 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/main/index.ts` | `webPreferences.webviewTag: true`；导航守卫三件套 + `darvin:open_external` / `local_services:list` handler（HTTP 探测） |
| `src/renderer/index.ts` | `isCustomElement` 注册 `webview` |
| `src/renderer/components/side-panel/BrowserTab.vue` | iframe → webview；受控 URL + 地址栏回写事件；能力按钮；空态列 services |
| `src/renderer/composables/useArtifacts.ts` | `browserUrlBySession` / `browserAddressBySession` / `openBrowser`；`Artifact.url` |
| `src/renderer/components/chat/MarkdownContent.vue` | `@click.capture` 链接拦截 |
| `src/renderer/components/side-panel/renderers/LocalServiceRenderer.vue` | URL 卡片 + 打开到 Browser / 复制 |
| `src/renderer/components/side-panel/renderers/DocumentRenderer.vue` | 扩展名分派 + docx/pdf/xlsx/pptx 子渲染器 |
| `src/renderer/composables/useFileContent.ts` | 新建：内联 content 优先，缺则 readFileAsDataUrl |
| `src/renderer/types/webview.d.ts` | 新建：WebviewElement 类型 |
| `src/shared/darvin-api.ts` | `openExternal` + `local_services:list` IPC 类型 |
| `src/preload/index.ts` | 新 IPC 桥接 |
| `src/darvin-agent/internal/agents/event/event.go` | 新增 `ArtifactEvent` |
| `src/darvin-agent/internal/agents/artifact_hook.go` | 新建：捕获订阅者（仿 TextDeltaHook，TurnEnd 扫描，注入 Workdir） |
| `src/darvin-agent/internal/agents/executor/executor.go` | `executeOneTool` 加 `write_file` 捕获（仿 todo_write 分支） |
| `src/darvin-agent/internal/gateway/eventledger.go` | `mapEventToTS` 新增 `case event.ArtifactEvent` |
| `src/darvin-agent/internal/sessionruntime/factory.go` | attach `artifact_hook`（`f.Config.Workdir` 注入） |
| `package.json` | 新增 `docx-preview` / `pdfjs-dist` / `xlsx` / `pptx-preview` / `jszip` |
| `src/renderer/services/i18n.ts` | 补缺 `artifact.browser.*`（部分已存在，仅补缺）+ `artifact.doc.*` 新 key（zh+en） |

**测试策略（对齐 CLAUDE.md「能不写就不写，优先纯函数」）**：
- **写**：Go 捕获扫描器正则（localhost / file 引用 / MEDIA / 裸路径扩展名）、`write_file` 提取逻辑、端口 HTTP 探测纯函数、DocumentRenderer 扩展名分派映射、useArtifacts `openBrowser`/URL 状态纯逻辑、markdown 链接 href 分流纯函数。
- **不写**：webview 渲染、office 渲染器、导航守卫、IPC 桥的集成测试（靠 `npm start` + electron-cdp 手动验证）。

## 7. 验收标准

**Phase A**
- [ ] 场景 1：聊天点 http 链接 → 面板展开 + Browser tab webview 加载，主窗口不导航
- [ ] 场景 2：后退/前进/刷新/缩放/外部打开可用；地址栏回显当前 URL（did-navigate / did-navigate-in-page / did-stop-loading）
- [ ] 场景 5：`window.open` / `target=_blank` → 系统浏览器打开，主窗口不变
- [ ] devtools `location.href='https://example.com'` → 被 will-navigate 拦截
- [ ] webview 页面 cookie 隔离于应用主 session
- [ ] `npm run lint` + `npm test` 通过

**Phase B**
- [ ] Go 发 `artifact` 事件后，renderer 面板出现对应 artifact；**聊天流内出现卡片组**（messageId 生效）
- [ ] 场景 3：assistant 文本含 `localhost:PORT` → local-service artifact（带 url）→ 切 Browser tab 加载
- [ ] 场景 4：`file://` 链接 / 裸路径（含图片视频）/ `MEDIA:` token → 生成对应类型 artifact；`write_file` 产物捕获
- [ ] Browser 空态列出 services + 标题 + 在线标记（HTTP 探测）
- [ ] Go `go vet ./...` + `go test ./...` 通过

**Phase C**
- [ ] `.md` → markdown；`.py/.js` → 代码高亮；`.txt/.csv` → 文本
- [ ] `.docx` → docx-preview；`.pdf` → pdfjs；`.xlsx` → 表格（列头/合并/背景色/虚拟滚动）；`.pptx` → 幻灯片预览（JSZip 预处理后）
- [ ] 文件缺失/二进制 → 错误态 + 系统应用打开兜底
- [ ] `npm run lint` + `npm test`（仅纯函数用例）通过；手动验证：`npm start` + electron-cdp 全场景
