# agent-ui-shell 设计文档 (v4)

## 1. 概述

### 1.1 问题 / 背景

v3「Editorial Console」设计（warm amber / Fraunces 衬线 / 极简）落地后用户反馈：

- 视觉太「编辑部」/「杂志」，缺乏工具产品的「克制但好用」的工程感。
- 左栏只有 session 列表，缺少「定时任务 / 当前 Agent / 设置 / 用户」这些必备入口。
- 「首页」空状态缺一个明显的「新会话 / 搜索 / 调度」起点。

参考原型 `@specs/features/agent-ui-shell/lobsterai-home-prototype.html`，该原型是一份「Home」视图，结构清晰：
- 左栏 4 段（新建 + 搜索 + 调度 + Agent） + 当前会话 + 底部主题 + 用户。
- 视觉语言：blue primary + Geist sans / Geist Mono / Instrument Serif 斜体 + aurora 渐变 + 细网格 + 软阴影。
- 风格属于「现代工具」，与 v3「Editorial Console」对立。

本次迭代要把 v3 推到 v4：**只换设计语言 + 改 sidebar 结构**，不引入新功能。

### 1.2 目标

1. 视觉语言换到原型系（blue primary `#60A5FA` / Geist 字体栈 / aurora + grid 背景）。
2. Sidebar 改为 4 段式：顶部操作（新建 / 搜索 / 调度）→ Agent 信息 → 会话列表 → 底部（设置 / 用户）。
3. 聊天主区、SidePanel 保持现有功能，但视觉 token 全部跟新（颜色 / 字号 / 圆角 / 间距 / 字体）。
4. 仍然是 S1 UI shell：mock 数据、Electron contextBridge 接口不变。

### 1.3 非目标

- 不做 Home 视图（greeting / mascot / quick-action pills / mode chips / 附件卡 / ctx meter / voice wave）。
- 不做多主题切换（保留当前的暗 / 亮 toggle，5 主题 picker 不引入）。
- 不做右侧 edge action 卡片、不做 toast 通知。
- 不做定时任务的真实业务（只放一个空的「定时任务」入口项 + 计数 badge）。
- 不做 Agent 切换 / 多 Agent 列表的真实交互（保留 1 个 current agent 卡片展示）。
- 不改 IPC 协议、不改 Go agent、不动主进程。

## 2. 用户场景

### 场景 1: 启动 → 默认进 s-001 会话
**Given** 用户首次打开应用
**When** 加载完成
**Then**
- 左栏宽度 248px，显示 4 段：顶部 primary action 区（New Chat 蓝色按钮 + 搜索框）→ 主导航（对话 / 定时任务）→ 当前 Agent 卡（LobsterAI / Darvin 全场景办公助手）→ 会话列表（Ping 测试 / Why Go single-threaded / Refactor gateway）
- 底部显示设置 icon + 用户卡（Darven + PRO badge + chevron）
- 主区 ChatHeader 显示「Ping 测试」标题 + 模型 chip + 侧栏开关
- MessageList 渲染 2 条 mock 消息（YOU + DARVIN）
- SidePanel 默认关闭；右上角切换可显隐

### 场景 2: 切换会话
**Given** 当前在 s-001
**When** 用户点击 s-002
**Then**
- 左栏 active 状态从 s-001 移到 s-002（左侧 2px 蓝色指示条 + bg-primary-muted 背景）
- 主区标题 + 消息内容同步切换
- localStorage 记录 currentSessionId

### 场景 3: 切换暗 / 亮主题
**Given** 当前 dark
**When** 用户点击左栏底部 cog 旁的 sun/moon icon
**Then**
- 整个配色翻转（白底 ↔ 黑底）
- ChatHeader / Sidebar / MessageList / Composer / SidePanel 一并切换
- 过渡 200ms

## 3. 功能需求

### FR-1: 字体栈切换
- index.html 引入 Google Fonts：Geist (300/400/500/600/700) + Geist Mono (400/500) + Instrument Serif (italic)。
- 移除 Fraunces / Inter Tight / JetBrains Mono 的引用。
- theme.css 的 `--font-display` 指向 Instrument Serif（仅 italic 用）；`--font-sans` 指向 Geist；`--font-mono` 指向 Geist Mono。
- fallback 链保留 system fonts。

### FR-2: 主题 token 重写
- primary 由 `#d4a574` (warm amber) → `#60A5FA` (blue)，light 模式 → `#3B82F6`。
- accent (success / 在线点) 保留 `#34D399`（绿色）。
- 新增 `bg-aurora-1/2/3` 三层 radial-gradient 变量给整页背景。
- 新增 `bg-grid` 渐隐网格（mask 径向渐变到边缘）。
- 阴影从 v3 的硬 cutoff 改为更柔和的多层阴影（`shadow-md` / `shadow-lg`）。
- 圆角 token 收紧：`sm=4` / `md=6` / `lg=8` / `xl=12`（与原型一致）；移除 v3 的大圆角（lg=10/xl=14/2xl=20）。
- 间距 token：保留 `app-padding=12` / `section-gap=16`。

### FR-3: Sidebar 结构重构（4 段）
**顶部 primary action 区（≈ 84px 高）**：
- 「新建会话」主按钮：bg-primary + 白色文字 + 14×14 plus icon + ⌘N kbd hint。
- 搜索框：bg-surface-raised + search icon + placeholder「搜索会话...」+ 边框 hover 加深。
**主导航（≈ 200px）**：
- nav-item 列表：对话（count=2）+ 定时任务（count=0）。
- active 状态：bg-primary-muted + 文字 primary 色 + 左侧 2px 蓝色指示条（`::before` pseudo）。
- hover：bg-surface-hover。
- 图标 14×14，count 文字 mono 10.5px。
**当前 Agent 区（≈ 110px）**：
- nav-label「当前 Agent · main」uppercase mono 10.5px。
- Agent pill：22×22 渐变 avatar + name 12px + sub 10.5px muted + 6×6 绿色 dot 脉冲。
**会话列表（flex 1，滚动）**：
- 复用 v3 的 session-item 样式，但改成圆角 6px + 12×12 icon + 10px mono 时间。
- active：bg-surface-raised。
- hover：bg-surface-hover。
**底部 dock（≈ 80px）**：
- 当前 v3 的「RUNTIME: READY」状态行 + theme toggle + cog 三件 → 简化为：
  - 一行：sun/moon IconButton + cog IconButton + user-avatar 26×26 渐变 + name + chevron
  - 移除 v3 的 mono uppercase RUNTIME 状态条（属于非目标外的细节）。

### FR-4: ChatHeader 视觉更新
- 保持 h-14，但 padding 改为 14×28（与原型一致）。
- 左侧：hamburger IconButton + 标题（truncate + font-medium 13.5px）。
- 右侧：移除「导入任务 / 设置」ghost-btn（v3 没有，原型有，但本次不做），保留模型 chip + side-panel toggle。
- 模型 chip 改为 v3 已有样式（mono uppercase 11px + chevron + 圆点）。

### FR-5: MessageList / MessageItem / StreamingText 视觉更新
- 保持「无气泡 + YOU / DARVIN label」结构。
- 字号从 v3 的 14 → 14.5（原型 home hero 字号稍大）。
- label 字号 10.5 → 11，间距微调。
- 用户消息文字色：dark 模式改用 `#60A5FA` 系（与原型 chat-user 对齐）。

### FR-6: Composer 视觉更新
- 移除 v3 的「rounded-2xl (20px)」圆角 → 12px（与原型 prompt-box 一致）。
- 移除 italic Fraunces placeholder → Geist 13.5px normal placeholder「描述你要完成的任务...」。
- 发送按钮：32×32 蓝色方块 + arrow icon（与原型 send-btn 一致），保持 icon 14×14。
- 移除 v3 的「Write a message...」英文，改中文 placeholder。

### FR-7: SidePanel 视觉更新
- 保持 3 tab（TOOLS / THINKING / ARTIFACT）。
- tab 文字 mono uppercase + 1px primary 底色下划线（与 v3 一致）。
- empty state 文字改为 Geist 13px。
- 右栏宽度 324 → 300（与原型紧凑对齐），触发后仍走 grid transition。

### FR-8: AppShell grid 更新
- 左栏宽度 268 → 248。
- 右栏宽度 324 → 300。
- grid transition 180ms 保留。

### FR-9: icons 扩展
新增 SVG（viewBox 0 0 24 24, stroke-width 1.7~2）：
- search.svg（放大镜）
- calendar-clock.svg（定时任务 icon）
- chevron-right.svg（用户行右侧箭头）
- arrow-up-right.svg（发送按钮 icon，替换 v3 的 send.svg）
- bot.svg（对话 nav icon，对话气泡 + 尾巴）
- 保留 v3 已有 13 个 icons（A 组 11 + arrow-up + circle-dot）

新增 icon 注册：丢到 `src/renderer/assets/icons/<name>.svg` 即可（自动 glob）。

## 4. 实现方案

### 4.1 theme.css 重写
按 v4 token 全面覆盖：
- `:root` 块写 dark 默认（blue `#60A5FA` 系 + aurora + grid）。
- `@layer base` 下加 `html.light` 覆盖（blue `#3B82F6` 系）。
- 字体引用变量替换。
- 圆角 / 间距 token 数值调整。
- 200ms transition on `body` + `*` 保留。

### 4.2 index.html 字体更新
删除现有 3 套字体（Fraunces / Inter Tight / JetBrains Mono）的 preconnect + stylesheet link，替换为：
```html
<link href="https://fonts.googleapis.com/css2?family=Geist:wght@300;400;500;600;700&family=Geist+Mono:wght@400;500&family=Instrument+Serif:ital@0;1&display=swap" rel="stylesheet">
```

### 4.3 Sidebar.vue / 子组件重构
- 删除 SidebarHeader / SidebarFooter（v3 风格不再需要）。
- 新结构：
  - `Sidebar.vue` 渲染 4 段：top-action / nav-section / agent-section / session-section / bottom-dock。
  - `SidebarTopAction.vue` 新组件：新建会话按钮 + 搜索框。
  - `SidebarNav.vue` 新组件：主导航列表（接收 items 数组）。
  - `SidebarAgent.vue` 新组件：当前 agent 卡片。
  - `SidebarSessionItem.vue` 新组件：会话列表项。
  - `SidebarUser.vue` 新组件：底部用户行（avatar + name + chevron + settings icon + theme toggle）。
- 复用 v3 的 SessionList 逻辑（`useSession` composable 不变）。

### 4.4 ChatPane / ChatHeader / MessageList / Composer / SidePanel
- 全部按 §3 FR-4~FR-7 调整 utility class。
- 不改组件拆分，不改 props / emits 接口，不改消息数据流。
- 不改 Mock 数据结构（保持 s-001/s-002/s-003 + mockMessages 现状）。

### 4.5 AppShell.vue
- `gridTemplateColumns`：左 248 / 中 1fr / 右 300。
- `useSidebar.collapsed` / `useSidePanel.open` composable 不变。
- 删除/重命名无影响。

### 4.6 主进程 / preload / Go agent
- 不动。
- `src/main/menu.ts` / `src/main/index.ts` 不改。

### 4.7 i18n
- `src/renderer/services/i18n.ts` 新增 key：
  - `sidebar.new_chat`：新建会话
  - `sidebar.search_placeholder`：搜索会话...
  - `sidebar.nav.chat`：对话
  - `sidebar.nav.scheduled`：定时任务
  - `sidebar.current_agent_label`：当前 Agent · main
  - `sidebar.user_credits`：4,280 credits
  - `composer.placeholder`：描述你要完成的任务...
- 现有 key（`chat.menu.*` / `app.*` 等）保留，仅新增。

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| Google Fonts 加载失败（断网） | fallback 到系统 sans-serif（`-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei UI"`），不阻塞渲染 |
| localStorage 不可用 | `useSession.readCurrentId` 已有 SSR / localStorage 缺失的 fallback |
| Sidebar 折叠（collapsed=true） | 当前 `useSidebar` 行为不变，折叠后整个 aside 消失，grid 左列 0px |
| 极小窗口（< 800px） | 不做响应式优化，固定布局 |
| SidePanel 触发但右栏总宽度超过容器 | 当前 grid `1fr` 自动收缩，min-w-0 已在 ChatPane 配 |
| Icon 缺失 | Icon 组件已有 fallback（空 16×16 占位 + 警告），新增 icons 后 fallback 不触发 |
| 主题切换瞬间闪烁 | theme.css 保留 `* { transition: background-color 200ms ease, color 200ms ease, border-color 200ms ease }` |

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `src/renderer/styles/theme.css` | 重写 token 块（blue / aurora / Geist） |
| `src/renderer/index.html` | Google Fonts link 替换 |
| `src/renderer/components/sidebar/Sidebar.vue` | 重写为 4 段 layout |
| `src/renderer/components/sidebar/SidebarTopAction.vue` | **新增** 顶部 primary 区 |
| `src/renderer/components/sidebar/SidebarNav.vue` | **新增** 主导航 |
| `src/renderer/components/sidebar/SidebarAgent.vue` | **新增** Agent 卡 |
| `src/renderer/components/sidebar/SidebarSessionItem.vue` | **新增** 单个 session item（拆自 v3 SessionList） |
| `src/renderer/components/sidebar/SidebarUser.vue` | **新增** 底部 dock |
| `src/renderer/components/sidebar/SidebarHeader.vue` | **删除**（逻辑并入 SidebarTopAction） |
| `src/renderer/components/sidebar/SidebarFooter.vue` | **删除**（逻辑并入 SidebarUser） |
| `src/renderer/components/sidebar/SessionList.vue` | 保留，内部用 SessionItem 改为 SessionSessionItem |
| `src/renderer/components/chat/ChatHeader.vue` | 微调 padding / 字号 |
| `src/renderer/components/chat/MessageList.vue` | 微调字号 / 间距 |
| `src/renderer/components/chat/MessageItem.vue` | 微调字号 / 颜色 token |
| `src/renderer/components/chat/StreamingText.vue` | 不动 |
| `src/renderer/components/chat/Composer.vue` | 圆角 / placeholder / 按钮 |
| `src/renderer/components/side-panel/SidePanel.vue` | 宽度 324 → 300 |
| `src/renderer/components/side-panel/SidePanelTabs.vue` | 微调 |
| `src/renderer/components/side-panel/SidePanelContent.vue` | 微调 |
| `src/renderer/layout/AppShell.vue` | grid 数值 248/300 |
| `src/renderer/assets/icons/search.svg` | **新增** |
| `src/renderer/assets/icons/calendar-clock.svg` | **新增** |
| `src/renderer/assets/icons/chevron-right.svg` | **新增** |
| `src/renderer/assets/icons/arrow-up-right.svg` | **新增**（替换 send.svg 的视觉） |
| `src/renderer/assets/icons/bot.svg` | **新增**（对话气泡） |
| `src/renderer/services/i18n.ts` | 新增 7 个 sidebar/composer key |

## 7. 验收标准

- [ ] playwright-cli 打开 `http://127.0.0.1:5173/`，console 0 errors / 0 warnings
- [ ] 视觉与原型 @specs/features/agent-ui-shell/lobsterai-home-prototype.html 的左栏布局一致：4 段顺序、底部 dock
- [ ] Sidebar 宽度精确 248px（AppShell grid 左列）
- [ ] SidePanel 宽度精确 300px
- [ ] `document.querySelectorAll('article').length === 2`（mock s-001 消息）
- [ ] 点击 s-002 会话：active 状态移动 + 标题切换 + 消息内容切换
- [ ] 点击主题切换按钮：dark ↔ light 平滑过渡（200ms），所有面板同步翻转
- [ ] 点击 ChatHeader 模型 chip：下拉菜单显示 3 个选项（CLAUDE-SONNET-4-5 / CLAUDE-OPUS-4-5 / GPT-4O），选中项带蓝色圆点
- [ ] `npm run lint` clean
- [ ] `npx vite build`（生产构建）成功，72+ modules
- [ ] 中文 placeholder 「描述你要完成的任务...」正确显示
- [ ] 所有新增 icon 在 SVG_SOURCES 注册（运行时无 "Missing icon" 警告）

---

## 8. 视觉原型 (HTML)

下面是一份**自包含的 HTML/CSS 原型**，把 v4 spec 里的视觉 token + 4 段 sidebar + chat area 全部画出来。直接保存成 `.html` 用浏览器打开即可看到目标效果。

```html
<!doctype html>
<html lang="zh-CN" data-theme="dark">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Darvin v4 · UI Shell Prototype</title>
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link
    href="https://fonts.googleapis.com/css2?family=Geist:wght@300;400;500;600;700&family=Geist+Mono:wght@400;500&family=Instrument+Serif:ital@0;1&display=swap"
    rel="stylesheet"
  />
  <style>
    /* ===== design tokens (v4) ===== */
    :root {
      --font-sans: 'Geist', -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei UI', sans-serif;
      --font-mono: 'Geist Mono', 'SF Mono', 'Fira Code', Menlo, monospace;
      --font-display: 'Instrument Serif', 'Songti SC', serif;

      /* dark default */
      --bg: #0F1117;
      --surface: #181A23;
      --surface-2: #1E212B;
      --surface-3: #252830;
      --border: rgba(255,255,255,0.06);
      --border-strong: rgba(255,255,255,0.12);
      --text: #E4E5E9;
      --text-muted: #8B8FA3;
      --text-subtle: #5C6370;
      --primary: #60A5FA;
      --primary-hover: #93C5FD;
      --primary-muted: rgba(96,165,250,0.12);
      --primary-soft: rgba(96,165,250,0.06);
      --accent: #34D399;
      --accent-muted: rgba(52,211,153,0.12);
      --user-msg: #60A5FA;
      --assistant-msg: #E4E5E9;
      --danger: #FF5F56;
      --shadow-md: 0 4px 12px rgba(0,0,0,0.3);
      --shadow-lg: 0 12px 40px rgba(0,0,0,0.4);
      --aurora-1: rgba(96,165,250,0.18);
      --aurora-2: rgba(244,114,182,0.10);
      --aurora-3: rgba(52,211,153,0.10);
      --grid-line: rgba(255,255,255,0.025);

      /* radius / spacing */
      --radius-sm: 4px;
      --radius-md: 6px;
      --radius-lg: 8px;
      --radius-xl: 12px;
      --app-padding: 12px;
      --section-gap: 16px;
    }
    [data-theme="light"] {
      --bg: #FAFBFC;
      --surface: #FFFFFF;
      --surface-2: #F4F5F7;
      --surface-3: #EBEDF0;
      --border: rgba(0,0,0,0.06);
      --border-strong: rgba(0,0,0,0.12);
      --text: #1A1A1A;
      --text-muted: #6B7280;
      --text-subtle: #9CA3AF;
      --primary: #3B82F6;
      --primary-hover: #2563EB;
      --user-msg: #3B82F6;
      --assistant-msg: #1A1A1A;
      --grid-line: rgba(0,0,0,0.03);
    }

    /* ===== base ===== */
    * { box-sizing: border-box; }
    html, body { margin: 0; padding: 0; height: 100%; }
    body {
      font-family: var(--font-sans);
      font-size: 13.5px;
      line-height: 1.55;
      background: var(--bg);
      color: var(--text);
      -webkit-font-smoothing: antialiased;
      overflow: hidden;
      transition: background-color 200ms ease, color 200ms ease;
    }
    *, *::before, *::after {
      transition: background-color 200ms ease, color 200ms ease, border-color 200ms ease;
    }
    button { font-family: inherit; cursor: pointer; }
    .mono { font-family: var(--font-mono); }
    .display { font-family: var(--font-display); font-style: italic; }

    /* ===== app scaffold ===== */
    .app {
      display: grid;
      grid-template-columns: 248px 1fr;
      height: 100vh;
      position: relative;
      isolation: isolate;
    }
    .app::before {
      content: "";
      position: absolute;
      inset: 0;
      pointer-events: none;
      z-index: 0;
      background:
        radial-gradient(900px 600px at 12% 8%, var(--aurora-1), transparent 60%),
        radial-gradient(700px 500px at 92% 18%, var(--aurora-2), transparent 60%),
        radial-gradient(800px 600px at 50% 100%, var(--aurora-3), transparent 65%);
      opacity: 0.9;
    }
    .app::after {
      content: "";
      position: absolute;
      inset: 0;
      pointer-events: none;
      z-index: 0;
      background-image:
        linear-gradient(var(--grid-line) 1px, transparent 1px),
        linear-gradient(90deg, var(--grid-line) 1px, transparent 1px);
      background-size: 56px 56px;
      mask-image: radial-gradient(ellipse at center, black 30%, transparent 78%);
      -webkit-mask-image: radial-gradient(ellipse at center, black 30%, transparent 78%);
    }
    .app > * { position: relative; z-index: 1; }

    /* ===== sidebar ===== */
    .sidebar {
      display: flex;
      flex-direction: column;
      background: color-mix(in srgb, var(--surface) 60%, transparent);
      border-right: 1px solid var(--border);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
    }
    .sb-section { padding: 0 var(--app-padding); }

    /* top action */
    .sb-top { padding: 14px 12px 10px; display: flex; flex-direction: column; gap: 8px; }
    .new-chat-btn {
      display: flex;
      align-items: center;
      gap: 8px;
      width: 100%;
      padding: 9px 12px;
      background: var(--primary);
      color: white;
      border: 0;
      border-radius: var(--radius-md);
      font-size: 13px;
      font-weight: 500;
      box-shadow: 0 1px 0 rgba(255,255,255,0.15) inset, 0 4px 12px rgba(96,165,250,0.25);
    }
    .new-chat-btn:hover { background: var(--primary-hover); }
    .new-chat-btn .kbd {
      margin-left: auto;
      font-family: var(--font-mono);
      font-size: 10px;
      padding: 1px 5px;
      border-radius: 4px;
      background: rgba(255,255,255,0.2);
      color: rgba(255,255,255,0.85);
    }
    .new-chat-btn svg { width: 14px; height: 14px; }
    .search-input {
      display: flex;
      align-items: center;
      gap: 7px;
      padding: 7px 10px;
      border-radius: var(--radius-md);
      background: var(--surface-2);
      border: 1px solid var(--border);
    }
    .search-input:hover { border-color: var(--border-strong); }
    .search-input svg { width: 12px; height: 12px; color: var(--text-muted); }
    .search-input input {
      flex: 1; min-width: 0;
      border: 0; outline: 0; background: transparent;
      color: var(--text);
      font-size: 12px;
    }
    .search-input input::placeholder { color: var(--text-muted); }

    /* nav */
    .sb-nav { padding: 6px 8px; }
    .nav-label {
      display: flex; align-items: center; justify-content: space-between;
      padding: 4px 8px 6px;
      font-size: 10.5px;
      font-weight: 500;
      letter-spacing: 0.04em;
      text-transform: uppercase;
      color: var(--text-muted);
    }
    .nav-item {
      display: flex;
      align-items: center;
      gap: 9px;
      width: 100%;
      padding: 6.5px 8px;
      border: 0;
      background: transparent;
      color: var(--text-muted);
      font-size: 12.5px;
      border-radius: var(--radius-md);
      text-align: left;
      position: relative;
    }
    .nav-item:hover { background: var(--surface-3); color: var(--text); }
    .nav-item.active { background: var(--primary-muted); color: var(--primary); }
    .nav-item.active::before {
      content: "";
      position: absolute;
      left: 0; top: 8px; bottom: 8px;
      width: 2px;
      background: var(--primary);
      border-radius: 2px;
    }
    .nav-item svg { width: 14px; height: 14px; flex-shrink: 0; opacity: 0.9; }
    .nav-item .count {
      margin-left: auto;
      font-size: 10.5px;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }
    .nav-item.active .count { color: var(--primary); }

    /* agent card */
    .sb-agent { padding: 0 8px; }
    .agent-pill {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 6px 8px;
      border-radius: var(--radius-md);
    }
    .agent-pill .avatar {
      width: 22px; height: 22px;
      border-radius: 6px;
      background: linear-gradient(135deg, #60A5FA, #A855F7);
      display: grid; place-items: center;
      font-size: 11px; font-weight: 600;
      color: white;
      flex-shrink: 0;
    }
    .agent-pill .meta { min-width: 0; flex: 1; }
    .agent-pill .name { font-size: 12px; color: var(--text); }
    .agent-pill .sub { font-size: 10.5px; color: var(--text-muted); }
    .agent-pill .dot {
      width: 6px; height: 6px;
      border-radius: 50%;
      background: var(--accent);
      box-shadow: 0 0 8px var(--accent);
    }

    /* session list */
    .sb-sessions { flex: 1; overflow-y: auto; padding: 6px 8px 12px; }
    .session-item {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 6px 8px;
      border-radius: var(--radius-md);
      font-size: 12px;
      color: var(--text-muted);
      cursor: pointer;
    }
    .session-item:hover { background: var(--surface-3); color: var(--text); }
    .session-item.active { background: var(--surface-2); color: var(--text); }
    .session-item .icon { width: 12px; height: 12px; color: var(--text-subtle); flex-shrink: 0; }
    .session-item .title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .session-item .time { font-size: 10px; color: var(--text-muted); font-family: var(--font-mono); }

    /* bottom dock */
    .sb-bottom {
      padding: 10px 12px 12px;
      border-top: 1px solid var(--border);
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .sb-bottom-row {
      display: flex;
      align-items: center;
      gap: 4px;
    }
    .icon-btn {
      width: 28px; height: 28px;
      display: grid; place-items: center;
      border-radius: var(--radius-md);
      background: transparent;
      border: 0;
      color: var(--text-muted);
    }
    .icon-btn:hover { background: var(--surface-3); color: var(--text); }
    .icon-btn svg { width: 14px; height: 14px; }

    .user-row {
      display: flex;
      align-items: center;
      gap: 9px;
      padding: 7px;
      border-radius: var(--radius-md);
      cursor: pointer;
    }
    .user-row:hover { background: var(--surface-3); }
    .user-avatar {
      width: 26px; height: 26px;
      border-radius: 8px;
      background: linear-gradient(135deg, #60A5FA, #A855F7);
      display: grid; place-items: center;
      color: white; font-size: 11px; font-weight: 600;
      flex-shrink: 0;
    }
    .user-info { flex: 1; min-width: 0; }
    .user-name { font-size: 12.5px; font-weight: 500; color: var(--text); }
    .user-plan {
      font-size: 10.5px;
      color: var(--text-muted);
      display: flex; align-items: center; gap: 4px;
    }
    .user-plan .pro {
      background: linear-gradient(135deg, #FBBF24, #F59E0B);
      color: #0F1117;
      font-weight: 600;
      font-size: 9px;
      padding: 1px 4px;
      border-radius: 3px;
    }
    .user-row .chev { width: 12px; height: 12px; color: var(--text-muted); }

    /* ===== main content ===== */
    .content {
      display: flex;
      flex-direction: column;
      min-width: 0;
      min-height: 0;
    }
    .content-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 14px 28px;
      flex-shrink: 0;
    }
    .content-header .left { display: flex; align-items: center; gap: 10px; }
    .content-header h1 {
      font-size: 13.5px;
      font-weight: 500;
      color: var(--text);
      margin: 0;
    }
    .content-header .right { display: flex; align-items: center; gap: 8px; }
    .model-chip {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      padding: 4px 8px;
      border-radius: var(--radius-md);
      background: transparent;
      border: 1px solid var(--border);
      color: var(--text);
      font-size: 12px;
    }
    .model-chip:hover { background: var(--surface-3); border-color: var(--border-strong); }
    .model-chip .glyph {
      width: 14px; height: 14px;
      border-radius: 4px;
      background: linear-gradient(135deg, #60A5FA, #A855F7);
      display: grid; place-items: center;
      color: white; font-size: 8px; font-weight: 700;
    }
    .model-chip .chev { width: 10px; height: 10px; opacity: 0.6; }

    /* messages */
    .messages {
      flex: 1;
      overflow-y: auto;
      padding: 16px 32px;
    }
    .messages-inner {
      max-width: 760px;
      margin: 0 auto;
      display: flex;
      flex-direction: column;
      gap: 18px;
    }
    .msg {
      display: flex;
      width: 100%;
    }
    .msg.user { justify-content: flex-end; }
    .msg.bot { justify-content: flex-start; }
    .msg .body { max-width: 85%; }
    .msg .label {
      font-family: var(--font-mono);
      font-size: 10.5px;
      letter-spacing: 0.04em;
      text-transform: uppercase;
      color: var(--text-subtle);
      margin-bottom: 6px;
    }
    .msg.user .label { text-align: right; }
    .msg .text {
      font-size: 14.5px;
      line-height: 1.65;
      white-space: pre-wrap;
    }
    .msg.user .text { color: var(--user-msg); }
    .msg.bot .text { color: var(--assistant-msg); }

    /* composer */
    .composer-wrap {
      padding: 12px 32px 20px;
      flex-shrink: 0;
    }
    .composer {
      max-width: 760px;
      margin: 0 auto;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-xl);
      padding: 10px 12px;
      display: flex;
      align-items: center;
      gap: 8px;
      transition: border-color 200ms ease, box-shadow 200ms ease;
    }
    .composer:focus-within {
      border-color: color-mix(in srgb, var(--primary) 50%, transparent);
      box-shadow: 0 0 0 4px var(--primary-soft);
    }
    .composer textarea {
      flex: 1;
      border: 0;
      outline: 0;
      background: transparent;
      color: var(--text);
      font-family: inherit;
      font-size: 13.5px;
      line-height: 1.55;
      resize: none;
      min-height: 24px;
      max-height: 180px;
    }
    .composer textarea::placeholder { color: var(--text-muted); }
    .send-btn {
      width: 32px; height: 32px;
      display: grid; place-items: center;
      border: 0;
      border-radius: var(--radius-md);
      background: var(--primary);
      color: white;
      box-shadow: 0 4px 12px rgba(96,165,250,0.32);
    }
    .send-btn:hover { background: var(--primary-hover); }
    .send-btn svg { width: 14px; height: 14px; }
    .composer-hint {
      max-width: 760px;
      margin: 8px auto 0;
      display: flex;
      justify-content: space-between;
      font-size: 10.5px;
      color: var(--text-muted);
      font-family: var(--font-mono);
    }
    .composer-hint kbd {
      font-family: var(--font-mono);
      font-size: 9.5px;
      padding: 1.5px 5px;
      border-radius: 4px;
      background: var(--surface);
      border: 1px solid var(--border);
      color: var(--text-muted);
    }
  </style>
</head>
<body>
  <div class="app">
    <!-- ===== Sidebar (248px) ===== -->
    <aside class="sidebar">
      <!-- 顶部 primary：新建 + 搜索 -->
      <div class="sb-top">
        <button class="new-chat-btn">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          <span>新建会话</span>
          <span class="kbd">⌘N</span>
        </button>
        <div class="search-input">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>
          </svg>
          <input type="text" placeholder="搜索会话..." />
        </div>
      </div>

      <!-- 主导航 -->
      <div class="sb-nav">
        <div class="nav-label">导航</div>
        <button class="nav-item active">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <span>对话</span>
          <span class="count">2</span>
        </button>
        <button class="nav-item">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>
          </svg>
          <span>定时任务</span>
          <span class="count">0</span>
        </button>
      </div>

      <!-- 当前 Agent -->
      <div class="sb-nav">
        <div class="nav-label">当前 Agent <span style="color: var(--primary);">· main</span></div>
        <div class="agent-pill">
          <div class="avatar">D</div>
          <div class="meta">
            <div class="name">Darvin</div>
            <div class="sub">全场景办公助手</div>
          </div>
          <div class="dot" title="在线"></div>
        </div>
      </div>

      <!-- 会话列表 -->
      <div class="sb-sessions">
        <div class="nav-label">近期任务</div>
        <div class="session-item active">
          <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <span class="title">Ping 测试</span>
          <span class="time">1h</span>
        </div>
        <div class="session-item">
          <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <span class="title">Why Go single-threaded</span>
          <span class="time">1d</span>
        </div>
        <div class="session-item">
          <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <span class="title">Refactor gateway</span>
          <span class="time">3d</span>
        </div>
      </div>

      <!-- 底部 dock -->
      <div class="sb-bottom">
        <div class="sb-bottom-row">
          <button class="icon-btn" title="主题">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="4"/>
              <path d="M12 2v2m0 16v2M4.93 4.93l1.41 1.41m11.32 11.32 1.41 1.41M2 12h2m16 0h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>
            </svg>
          </button>
          <button class="icon-btn" title="设置">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="3"/>
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9 1.65 1.65 0 0 0 4.27 7.18l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6 1.65 1.65 0 0 0 10 3.09V3a2 2 0 0 1 4 0v.09A1.65 1.65 0 0 0 15 4.6a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c.36.16.75.27 1.09.27H21a2 2 0 0 1 0 4h-.09c-.34 0-.73.11-1.09.27z"/>
            </svg>
          </button>
        </div>
        <div class="user-row">
          <div class="user-avatar">DA</div>
          <div class="user-info">
            <div class="user-name">Darven</div>
            <div class="user-plan">
              <span class="pro">PRO</span>
              <span>· 4,280 credits</span>
            </div>
          </div>
          <svg class="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="m9 18 6-6-6-6"/>
          </svg>
        </div>
      </div>
    </aside>

    <!-- ===== Main content ===== -->
    <main class="content">
      <header class="content-header">
        <div class="left">
          <button class="icon-btn" title="切换侧栏">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 6h18M3 12h18M3 18h12"/>
            </svg>
          </button>
          <h1>Ping 测试</h1>
        </div>
        <div class="right">
          <button class="model-chip">
            <span class="glyph">C</span>
            <span>Claude Sonnet 4.5</span>
            <svg class="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="m6 9 6 6 6-6"/>
            </svg>
          </button>
          <button class="icon-btn" title="侧栏面板">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="4" width="18" height="16" rx="2"/>
              <path d="M15 4v16"/>
            </svg>
          </button>
        </div>
      </header>

      <div class="messages">
        <div class="messages-inner">
          <div class="msg user">
            <div class="body">
              <div class="label">You · 1h ago</div>
              <div class="text">ping</div>
            </div>
          </div>
          <div class="msg bot">
            <div class="body">
              <div class="label">Darvin · 1h ago</div>
              <div class="text">Pong. Agent runtime is ready.</div>
            </div>
          </div>
        </div>
      </div>

      <div class="composer-wrap">
        <div class="composer">
          <textarea placeholder="描述你要完成的任务..." rows="1"></textarea>
          <button class="send-btn" title="发送 (Enter)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M5 12h14M13 5l7 7-7 7"/>
            </svg>
          </button>
        </div>
        <div class="composer-hint">
          <span><kbd>Enter</kbd> 发送 · <kbd>Shift</kbd>+<kbd>Enter</kbd> 换行</span>
          <span>今天 0 次任务</span>
        </div>
      </div>
    </main>
  </div>
</body>
</html>
```

**怎么看效果**：

1. 把上面的 `<!doctype html>...</html>` 整段复制到本地一个 `.html` 文件（比如 `darvin-v4-prototype.html`）。
2. 双击在 Chrome 打开。
3. 想看浅色主题，把 `<html data-theme="dark">` 改成 `data-theme="light"`。

**和最终 Vue 实现的对应关系**：

| 原型里的 class | 对应 Vue 组件 | 备注 |
|---|---|---|
| `.sidebar` / 4 段 | `Sidebar.vue` + 5 个子组件 | 见 spec §4.3 |
| `.new-chat-btn` / `.search-input` | `SidebarTopAction.vue` | 新增 |
| `.nav-item` / `.nav-label` | `SidebarNav.vue` | 新增 |
| `.agent-pill` | `SidebarAgent.vue` | 新增 |
| `.session-item` | `SidebarSessionItem.vue` | 新增（替代 v3 的 SessionItem） |
| `.sb-bottom` | `SidebarUser.vue` | 新增 |
| `.content` / `.content-header` / `.messages` / `.composer-wrap` | `ChatPane.vue` + 子组件 | 仅调整 utility class |
| `.composer` | `Composer.vue` | 圆角 20→12，placeholder 中文化 |
| `.send-btn` | `Composer.vue` | 内 send icon 换为 arrow-up-right |