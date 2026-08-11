# 链接点击跳转 Artifact 面板设计文档

## 1. 概述

### 1.1 问题 / 背景

用户反馈两个问题：

1. **TS 编译错误**：`src/renderer/components/side-panel/ArtifactPanel.vue:87-90` 给 `IconButton` 传了 `icon="refresh"`，但 `IconButton.vue:20` 声明的必填 prop 是 `name`，导致类型不匹配。
2. **点击 URL 链接会把整个应用变成浏览器**：在聊天 markdown 里点 `<a href="https://...">`，Electron 默认行为是让主窗口整个导航到该 URL，应用 UI 全部消失。期望是「跳转到 Artifact 面板」，用面板内已存在的沙箱 `BrowserTab` 打开该 URL。

根因：
- 主进程 `createWindow()` **没有** `setWindowOpenHandler` / `will-navigate` 拦截（对比 `LobsterAI/src/main/main.ts:10130` 有完整双保险：`setWindowOpenHandler → shell.openExternal + deny`，子窗口 `will-navigate` 白名单）。
- `MarkdownContent.vue` 渲染的 `<a href>` 无点击拦截、无 `target` 处理；`LocalServiceRenderer.vue` 还有 `<a target="_blank">`，点它同样触发窗口跳转。
- 现有 `BrowserTab.vue`（Artifact 面板特殊 tab，沙箱 iframe）已存在，但 `url` 是组件内本地 `ref`，外部无法注入 URL。

### 1.2 目标

- 修复 `IconButton` prop 类型错误。
- 聊天 / artifact markdown 预览中的 http(s) 链接点击 → 在 Artifact 面板的 Browser tab（沙箱 iframe）打开，面板自动展开。
- 任何情况下主窗口都不会被导航离开应用：未拦截的 `window.open` / `target="_blank"` / 链接跳转，由主进程守卫转投系统浏览器（`shell.openExternal`），绝不导航主窗口。
- 手动在 Browser tab 地址栏输入导航仍然可用。

### 1.3 非目标

- 不引入 Electron `<webview>`（darvin 当前渲染器全部走 iframe，保持统一）。
- 不做「每个 URL 一个预览 tab」的产物级 Web artifact——直接复用现有 Browser 特殊 tab，最小侵入。
- 不改 Go agent / IPC 协议（纯 renderer + main 侧导航守卫）。

## 2. 用户场景

### 场景 1: 聊天里点击一个 http 链接
**Given** 当前会话的聊天消息里有一个 `<a href="https://example.com/docs">`（LLM 生成的 markdown）
**When** 用户点击该链接
**Then** 主窗口不导航、不跳出；右侧 Artifact 面板自动展开，激活 Browser tab，iframe 加载 `https://example.com/docs`；地址栏回显该 URL。

### 场景 2: artifact markdown 预览里点击链接
**Given** 用户在 Artifact 面板查看一个 `markdown` 类型的 artifact，内容含外链
**When** 用户点击外链
**Then** 同样切到 Browser tab 打开该 URL（与场景 1 一致的语义）。

### 场景 3: `target="_blank"` / `window.open` 兜底
**Given** 主窗口 DOM 里某处触发了 `window.open`（如 `LocalServiceRenderer` 的「在新窗口打开」链接）
**When** 用户点击
**Then** 不新建 Electron 窗口、不导航主窗口；URL 交给系统默认浏览器打开。

### 场景 4: 未拦截的主窗口导航兜底
**Given** 恶意 / 意外触发 `location.href = 'https://evil.example'` 这类主帧导航
**When** 执行
**Then** `will-navigate` 阻止导航，并转系统浏览器打开；应用界面保持不变。

### 场景 5: 手动在 Browser tab 输入 URL
**Given** Browser tab 已激活
**When** 用户在地址栏输入 `example.com` 并回车 / 点 Go
**Then** 补全为 `https://example.com` 并加载；切换会话后该 session 的 browser URL 独立保留。

## 3. 功能需求

### FR-1: `IconButton` prop 修正
`ArtifactPanel.vue` 4 处 `icon="..."` 改为 `name="..."`，消除 TS 2345。

### FR-2: 每会话 browser URL 状态
`useArtifacts` 增加 `browserUrlBySession`（`Record<sessionId, url>`）+ `setBrowserUrl(sid, url)` + `openBrowser(sid, url)`（`setBrowserUrl` + `activateTab(Browser)`，自动展开面板）。`clearSessionArtifacts` / `reset` 同步清理。

### FR-3: BrowserTab 外部可控
`BrowserTab.vue` 接受 `session-id` prop，iframe `src` 与地址栏回显以 `browserUrlBySession[sid]` 为唯一数据源；无值时回退 `https://example.com`。地址栏输入走 `setBrowserUrl`。

### FR-4: markdown 链接点击拦截
`MarkdownContent.vue` 根节点加 `@click.capture`：命中 `<a>` 且 href 非空、非 `#` 锚点时 `preventDefault`——
- http(s) → `openBrowser(activeSessionId, href)`
- 其他协议（mailto/tel/相对路径）→ `window.darvin.openExternal(href)`，失败弹 toast。

### FR-5: 主进程导航守卫（安全网）
`createWindow()` 内：
- `setWindowOpenHandler`：`shell.openExternal(url)` + `{ action: 'deny' }`。
- `will-navigate`：应用自身 origin（dev server / `file:`）放行，其余 `preventDefault` + `shell.openExternal`。
- 协议白名单 `https:|http:|mailto:|tel:` 之外的 URL 静默拒绝（不调 openExternal）。

### FR-6: `openExternal` IPC
`DarvinApi` + preload + main `ipcMain.handle('darvin:open_external')`：`shell.openExternal(url)`，返回 `{ success: boolean }`。

## 4. 实现方案

### 4.1 `useArtifacts.ts` 扩展

```ts
const browserUrlBySession = ref<Record<string, string>>({});

function setBrowserUrl(sid: string, url: string): void {
  browserUrlBySession.value = { ...browserUrlBySession.value, [sid]: url };
}

function openBrowser(sid: string, url: string): void {
  setBrowserUrl(sid, url);
  activateTab(sid, ArtifactSpecialTab.Browser); // activateTab 内部已 setPanelOpen(sid, true)
}
```

导出新增三项；`clearSessionArtifacts` / `reset` 里 `delete nextBrowser[sid]` / 清空。

### 4.2 `BrowserTab.vue` 改造

- props：`defineProps<{ sessionId: string }>()`；`ArtifactPanel.vue:96` 改为 `<BrowserTab :session-id="props.sessionId" />`。
- `const externalUrl = computed(() => browserUrlBySession[props.sessionId] ?? FALLBACK_URL)`（`FALLBACK_URL = 'https://example.com'`）。
- 地址栏输入保留本地 draft ref；`watch(externalUrl)` 把 draft 同步为当前值（外部链接跳进来时回显）。
- iframe `:src="externalUrl"`；`go()` → `setBrowserUrl(sid, normalize(input))`（复用现有 `https?://` 补全逻辑）。

### 4.3 `MarkdownContent.vue` 链接拦截

```ts
const artifacts = useArtifacts();
const session = useSession();

function onLinkClick(e: MouseEvent): void {
  const target = e.target as Element | null;
  const anchor = target?.closest?.('a');
  if (!anchor) return;
  const href = anchor.getAttribute('href');
  if (!href || href.trim() === '' || href.startsWith('#')) return;
  e.preventDefault();
  const sid = session.activeSessionId.value;
  if (/^https?:\/\//i.test(href)) {
    if (sid) artifacts.openBrowser(sid, href);
    else void window.darvin.openExternal(href);
    return;
  }
  void window.darvin.openExternal(href).then((r) => {
    if (!r.success) showToast(t('artifact.link.openFailed'), 'error');
  });
}
```

模板根节点：`<div class="markdown-content min-w-0" @click.capture="onLinkClick">`。

覆盖两个消费方：chat（`AssistantTurnBlock.vue:30`）与 artifact markdown 预览（`MarkdownRenderer.vue` 复用同组件）。HtmlRenderer 产物走沙箱 iframe，内部链接只导航 iframe，天然安全，无需拦截。

### 4.4 主进程守卫（`src/main/index.ts` `createWindow()`）

```ts
const SAFE_EXTERNAL_PROTOCOLS = /^(https?|mailto|tel):/i;
const isSafeExternalUrl = (url: string): boolean => {
  try { return SAFE_EXTERNAL_PROTOCOLS.test(new URL(url).protocol); } catch { return false; }
};
const isAllowedAppNavigation = (url: string): boolean => {
  if (url === 'about:blank') return true;
  if (url.startsWith('file:')) return true;              // 生产加载自身 index.html
  if (MAIN_WINDOW_VITE_DEV_SERVER_URL) {
    try { if (url.startsWith(new URL(MAIN_WINDOW_VITE_DEV_SERVER_URL).origin)) return true; } catch { /* noop */ }
  }
  return false;
};

mainWindow.webContents.on('will-navigate', (event, url) => {
  if (isAllowedAppNavigation(url)) return;
  event.preventDefault();
  if (isSafeExternalUrl(url)) void shell.openExternal(url);
});
mainWindow.webContents.setWindowOpenHandler(({ url }) => {
  if (isSafeExternalUrl(url)) void shell.openExternal(url);
  return { action: 'deny' };
});
```

`shell` 已在 `main/index.ts:14` 导入。放 `createWindow()` 创建 `mainWindow` 后、`loadURL/loadFile` 前。

### 4.5 `openExternal` IPC

- `darvin-api.ts` `DarvinApi`：`openExternal(url: string): Promise<{ success: boolean }>;`
- preload：`ipcRenderer.invoke('darvin:open_external', url)`。
- main：`ipcMain.handle('darvin:open_external', async (_e, url: string) => { if (!isSafeExternalUrl(url)) return { success: false }; try { await shell.openExternal(url); return { success: true }; } catch { return { success: false }; } });`

### 4.6 i18n

新增 1 个 key（zh + en 同步）：`artifact.link.openFailed` = 「无法打开该链接」 / `'Failed to open link'`。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 无激活 session（HomeView）点链接 | `sid` 为 null → 走 `openExternal` 兜底 |
| 相对路径链接 `./foo` | 非 http(s) → `openExternal`，失败 toast |
| 恶意协议 `javascript:` / `file:///etc/passwd` | `isSafeExternalUrl` 协议白名单拒绝，不开外部、不导航 |
| 同一 URL 反复点击 | `openBrowser` 幂等：Browser tab 已激活则仅更新 URL |
| Vite dev server 重载 / HMR | 属于应用自身 origin，`will-navigate` 放行 |
| Browser tab 内 iframe 导航 | iframe 子帧导航不触发 `will-navigate`（主帧守卫） |
| 切换会话 | browser URL 按 session 隔离，互不覆盖 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/components/side-panel/ArtifactPanel.vue` | `icon=` → `name=`（4 处）；`BrowserTab` 传 `:session-id` |
| `src/renderer/composables/useArtifacts.ts` | 新增 `browserUrlBySession` / `setBrowserUrl` / `openBrowser`；清理逻辑扩展 |
| `src/renderer/components/side-panel/BrowserTab.vue` | 改为外部受控 URL + `session-id` prop |
| `src/renderer/components/chat/MarkdownContent.vue` | `@click.capture` 链接拦截 |
| `src/renderer/composables/useArtifacts.ts` 旁 `*.test.ts` | 新增 openBrowser / setBrowserUrl 单测 |
| `src/shared/darvin-api.ts` | `DarvinApi.openExternal` |
| `src/preload/index.ts` | `openExternal` 桥接 |
| `src/main/index.ts` | `setWindowOpenHandler` + `will-navigate` 守卫 + `darvin:open_external` handler |
| `src/renderer/services/i18n.ts` | `artifact.link.openFailed`（zh + en） |

## 7. 验收标准

- [ ] 场景 1：聊天 markdown 点击 http 链接 → 面板展开 + Browser tab 加载该 URL，主窗口不导航
- [ ] 场景 2：artifact markdown 预览点击外链 → 切 Browser tab 打开
- [ ] 场景 3：`target="_blank"`（LocalServiceRenderer）→ 系统浏览器打开，主窗口不变
- [ ] 场景 4：devtools 里 `location.href='https://example.com'` → 被拦截，应用不消失
- [ ] 场景 5：Browser tab 地址栏手动输入导航正常；切换 session URL 独立
- [ ] `npm run lint` 通过
- [ ] `npm test`（vitest）通过（含新增 useArtifacts 用例）
- [ ] 手动验证：`npm start` + electron-cdp 驱动确认上述场景
