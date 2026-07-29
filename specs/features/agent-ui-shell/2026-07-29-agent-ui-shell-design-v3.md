# Agent UI Shell 设计文档 v3（S1，重设计）

> **Phase 1 / 6 — UI 阶段**。v3 相对 v2 的全部改动：UI 视觉重做（Editorial Console 方向）+ Electron 默认菜单清理。**未启动实现**，等用户审核 spec 后再动源码。
>
> 前置：v1（单栏）→ v2（3 栏 + 组件化规范）→ **v3（视觉重做 + 菜单清理）**。v2 的架构、目录、composable、contextBridge 契约**完全保留**；v3 改的是：① §1.2 视觉方向 ② §3 FR-7 token ③ §3 FR-9 视觉规约 ④ §4.3 组件骨架的视觉细节 ⑤ §6 新增 src/main/menu.ts（Electron 菜单清理）。
>
> **实现状态**（标注格式：✅ 已完成 / ⏳ 待用户审核 spec / ❓ 待用户决策 / 🚧 实现中）
>
> | 段落 | 状态 |
> |------|------|
> | 调研当前 UI / main 菜单 | ✅ |
> | 选型 Editorial Console 方向 + token | ⏳ 待审核 |
> | Typography（Google Fonts CDN 接入方式） | ⏳ 待审核 |
> | 组件视觉重写（Sidebar / ChatHeader / Composer / MessageItem / SidePanel / Dropdown） | ⏳ 待审核 |
> | Sidebar / ChatHeader / Composer / MessageItem / SidePanel 源码实现 | ⏳ 等 spec 通过后做 |
> | theme.css token 重写 | ⏳ |
> | Electron 菜单清理（`src/main/menu.ts`） | ⏳ |
> | main/index.ts 接入 menu 模块 | ⏳ |
> | `npm run lint` + `vite build` + `go vet` 验证 | ⏳ |
>
> **已发现的问题 / 待用户决策**（列在文末 §9，不阻塞 spec 评审）。

---

## 1. 概述

### 1.1 问题 / 背景

v2 spec 落地后第一版 UI（`bg-bg #0e0e10` / 系统字体 / 蓝色 accent `#6a8cff` / 圆角消息气泡）跑起来了，但**视觉上极通用、缺乏记忆点**：

- 颜色取自 Tailwind 默认调色板（蓝紫 accent + 标准灰阶），与「这是一个个人专属 AI 桌面」不匹配
- 字体走系统 ui-sans-serif，无品牌特征
- 消息气泡样式与 ChatGPT / Cursor / Claude.ai 等高度同质化
- 组件间距 / 留白「够用但平庸」，没有刻意营造阅读节奏
- Electron 默认菜单（File / Edit / View / Window / Help）全保留，对个人 AI 工具是冗余 chrome

### 1.2 目标

- **建立一套强烈可识别的视觉方向**——不是「又一个 chat UI」，而是「Darvin 的 workspace」
- 保留 v2 的全部架构决策（3 栏、Vue 3 组件化、contextBridge 契约、@theme token 系统）不动
- 重写 **design tokens**（颜色、字体、字号、间距、动效）以支撑新美学
- 重写 **5 个核心组件的视觉骨架**（Sidebar / ChatHeader / Composer / MessageItem / SidePanel）
- **清理 Electron 默认菜单**——只保留 macOS 必须的 app name + quit；其他平台全部隐藏
- 保留 v2 的所有 acceptance criteria；视觉部分用新的「是否符合 Editorial Console 方向」补一条

### 1.3 非目标（v3 不动）

- 仍不接 Go agent 子进程（mock 模式不变；S5 才接）
- 仍不做 Pinia / Vue Router / artifact 渲染 / markdown 解析 / 拖拽排序 / 移动端适配
- v2 目录结构、composable API、contextBridge 6 方法全部不动
- 不动 AGENTS.md 的组件化规范（10 条）+ 注释规范 + 图标系统规则

---

## 2. 用户场景

v3 复用 v2 §2 的全部 10 个场景（启动 / 发 mock 流式 / 切主题 / 折叠 sidebar / 折叠 side panel / 切换会话 / 新建会话 / 模型 dropdown / contextBridge 契约 / error 事件），视觉改动不影响交互流程。

**新增场景 11：菜单清爽**

**Given** Electron 启动
**When** 用户查看窗口顶部（macOS 顶部菜单栏）/ 窗口内左上角（Windows / Linux 应用的菜单栏）
**Then** **看不到** File / Edit / View / Window / Help 等默认菜单项
**And** macOS 顶部菜单栏仅剩 `Darvin / Quit Darvin` 两项
**And** DevTools 仍可通过 `F12` / `Ctrl+Shift+I` 直接打开（不走菜单）

---

## 3. 功能需求

### FR-1 ~ FR-6（架构、布局、子树、组件、side panel）

与 v2 完全一致。**v3 不重写**。

### FR-7：design token 重写

#### FR-7.1 颜色（dark default + light override）

**核心转向**：从「冷色蓝紫 + 中性灰」切到「暖色琥珀 + 纸感中性灰」。

```css
/* dark default */
--color-bg:              #0c0c0e;   /* 暖底近黑，非纯黑 */
--color-surface:         #161618;   /* 侧栏底 */
--color-surface-2:       #1d1d20;   /* 输入框 / 卡片 */
--color-border:          #28282d;   /* 1px hairline */
--color-border-strong:   #3a3a42;   /* 强调用：active 边、focus ring */
--color-text:            #ededee;   /* 主文字 */
--color-text-muted:      #a4a4ad;
--color-text-subtle:     #6b6b75;   /* 时间戳 / ID / hint */
--color-accent:          #d4a574;   /* 暖琥珀，主交互色 */
--color-accent-hover:    #e0b889;
--color-accent-soft:     #2a221b;   /* accent 弱化背景，hover/active */
--color-user-msg:        #f3eee4;   /* 用户文字色，浅奶白（与 dark bg 形成高对比） */
--color-assistant-msg:   #ededee;   /* 助手文字色 = 主文字色 */
--color-danger:          #e57373;
--color-success:         #7eb98e;
--color-warning:         #d4a259;

/* light override（在 @layer base 下，html.light 触发） */
--color-bg:              #f8f6f1;   /* 纸感米白 */
--color-surface:         #fdfcf8;
--color-surface-2:       #f3f0e9;
--color-border:          #e3ddd0;
--color-border-strong:   #c9c2b1;
--color-text:            #1a1a1c;
--color-text-muted:      #5a5a62;
--color-text-subtle:     #8a8478;
--color-accent:          #a67a3d;   /* 深琥珀，浅底要压暗 */
--color-accent-hover:    #8e6629;
--color-accent-soft:     #f0e9d8;
--color-user-msg:        #1a1a1c;   /* 浅底下用户文字要更深 */
--color-assistant-msg:   #1a1a1c;
```

**消费规则**：所有组件继续走 `bg-bg` / `text-text-muted` / `border-border` / `bg-accent` 等 utility，token 名字不变，只是值换了。

#### FR-7.2 字体（Google Fonts CDN）

不走 Vite 字体插件，直接 `<link>` 引入。3 套字体定调：

```css
--font-display: "Fraunces", ui-serif, Georgia, serif;             /* 品牌 / 标题 / session 标题 */
--font-sans:    "Inter Tight", -apple-system, "Segoe UI", sans-serif; /* 主体 UI 文字 */
--font-mono:    "JetBrains Mono", ui-monospace, monospace;        /* ID / 时间戳 / char count */
```

**接入方式**：在 `src/renderer/index.html` 的 `<head>` 加：
```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet"
  href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;0,9..144,600;1,9..144,400&family=Inter+Tight:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap">
```

**fallback**：本地断网时降级到 `ui-serif` / `system-ui` / `ui-monospace`，不闪退。

**注意**：AGENTS.md 之前没禁止网络字体；本次显式引入需在 AGENTS.md 追加一行「允许从 Google Fonts 加载 3 套字体，禁止引入其他 CDN 资源」。详见 §6 涉及文件。

#### FR-7.3 字号 scale

```css
--text-xs:   11px;   /* mono: 时间戳 / ID / hint */
--text-sm:   13px;   /* 副信息 / 按钮 */
--text-base: 14.5px; /* 默认正文（比 v2 的 14 略大，可读性优先） */
--text-md:   15.5px; /* 消息正文 */
--text-lg:   18px;   /* session 标题 / 段标题（Fraunces） */
--text-xl:   24px;   /* 品牌名 / 大数字（Fraunces） */
--text-2xl:  32px;   /* 空态大标题（Fraunces italic） */
```

#### FR-7.4 间距 / 圆角 / 动效

- 间距：`app-padding: 14px`（v2 的 12 略大，编辑感更强）/ `section-gap: 24px`（v2 的 16 翻倍，节奏更舒展）
- 圆角：`sm 4px / md 6px / lg 10px / xl 14px / 2xl 20px`（增加 2xl 给大卡片）
- 阴影：**v3 全部不引入 box-shadow**——只用 1px hairline border + bg shift 制造层次
- 动效：
  - 主题切换：`color` / `background-color` / `border-color` 全属性 200ms ease
  - 侧栏 / 侧面板折叠：`width` 180ms cubic-bezier(0.4, 0, 0.2, 1)
  - hover bg shift：120ms ease
  - 发送按钮 hover：`scale(1.04)` 120ms ease

### FR-8：主题切换

与 v2 §3 FR-8 一致（`useTheme` composable + localStorage + HTML `.light` 类）。**额外**：v3 的 token 切换通过 CSS `transition` 平滑过渡（v2 是瞬切，v3 切主题时 200ms 渐变，更克制）。

### FR-9：图标系统

A 组 11 个 chat UI icon 沿用 v2。**v3 调整**：

- 颜色规约不变（`stroke="currentColor"` 强制）
- viewBox 不变（`0 0 34 34`）
- stroke-width 从 v2 的 2.4 改为 **1.6**（v2 偏粗，v3 配 serif 字体需要更细的线条以保持比例）
- 线条末端：保持 round
- 缺失 icon 行为不变（warn + 占位）

**新增 2 个 icon**（v3 渲染需要）：

| name | 用途 | viewBox |
|------|------|---------|
| `arrow-up` | Composer 发送按钮 | 0 0 34 34 |
| `circle-dot` | sidebar runtime 状态点 | 0 0 34 34 |

A 组总 13 个，全部在 `src/renderer/assets/icons/`。

### FR-10 ~ FR-15（composable / 数据流 / 持久化 / 依赖 / contextBridge 契约）

与 v2 完全一致。**v3 不动**。

### FR-16：Electron 菜单清理

**新增**。在 `src/main/menu.ts` 暴露 `installAppMenu()`，由 `src/main/index.ts` 在 `app.on('ready', ...)` 里调用。

```ts
// src/main/menu.ts 骨架
import { app, Menu, type MenuItemConstructorOptions } from 'electron';

export function installAppMenu(): void {
  const isMac = process.platform === 'darwin';

  if (isMac) {
    // macOS 必须保留 app name（系统要求显示在菜单栏）+ Quit
    const macTemplate: MenuItemConstructorOptions[] = [
      {
        label: app.name,
        submenu: [
          { role: 'about' },
          { type: 'separator' },
          { role: 'services' },
          { type: 'separator' },
          { role: 'hide' },
          { role: 'hideOthers' },
          { role: 'unhide' },
          { type: 'separator' },
          { role: 'quit' },
        ],
      },
    ];
    Menu.setApplicationMenu(Menu.buildFromTemplate(macTemplate));
    return;
  }

  // 其他平台：完全隐藏菜单栏
  Menu.setApplicationMenu(null);
}
```

**DevTools 入口**：菜单里不挂，但需要在主进程注册全局快捷键，让用户即使没有菜单也能开 DevTools：

```ts
// 在 createWindow() 之后
mainWindow.webContents.on('before-input-event', (_event, input) => {
  // F12 / Ctrl+Shift+I / Cmd+Opt+I
  if (
    input.key === 'F12' ||
    ((input.control || input.meta) && input.shift && input.key.toLowerCase() === 'i')
  ) {
    mainWindow.webContents.toggleDevTools();
  }
});
```

**spec 验收**：v2 §7.1 启动 6 条不变；新增「窗口菜单栏 / 顶部菜单栏仅 macOS 保留 app name + Quit；其他平台无可见菜单；F12 仍能开 DevTools」。

---

## 4. 实现方案

### 4.1 目录结构（相对 v2 增量）

```
src/
├── main/
│   ├── index.ts                   # 改：app.on('ready') 后调 installAppMenu
│   └── menu.ts                    # 🆕：Electron 菜单清理
├── renderer/
│   ├── index.html                 # 改：<head> 加 Google Fonts <link>
│   ├── styles/
│   │   └── theme.css              # 改：token 全部重写（颜色 / 字体 / 字号 / 间距 / 动效）
│   └── assets/icons/
│       ├── arrow-up.svg           # 🆕
│       └── circle-dot.svg         # 🆕
└── AGENTS.md                      # 改：加一行「renderer 允许从 Google Fonts 加载 3 套字体」
```

其他文件（`src/renderer/App.vue` / `layout/AppShell.vue` / 所有 components / composables / services）**视觉骨架重写**，但 API / props / emits / 路径不变。

### 4.2 关键设计决策

#### 4.2.1 视觉方向：Editorial Console

**为什么是「Editorial Console」**：

- **Editorial**（编辑式）：serif 字体（Fraunces）承担品牌 / session 标题 / 空态大标题，让 UI 摆脱「又一个 chat」的同质化
- **Console**（控制台式）：极简 hairline border + 无 shadow + 紧凑密度，让信息密度与编辑感平衡
- **暖琥珀 accent**（`#d4a574`）：与「冷色 AI」形成差异化，呼应「个人工作台」温度

**对比 v2**：
- v2 accent `#6a8cff`（冷蓝紫）→ v3 accent `#d4a574`（暖琥珀）
- v2 系统字体 → v3 Fraunces（serif）+ Inter Tight（sans）+ JetBrains Mono（mono）三件套
- v2 蓝色消息气泡 → v3 暖色边框 + accent 弱化背景的卡片
- v2 圆角 `lg=8px` → v3 圆角 `lg=10px xl=14px 2xl=20px`（更柔的圆角让 serif 字体不显生硬）

#### 4.2.2 消息视觉：无气泡

v3 改用「无气泡」方案：

- **user message**：右对齐，无背景，仅一段文字 + 上方小标签「YOU」（Fraunces italic 11px uppercase letter-spaced）
- **assistant message**：左对齐，无背景，仅一段文字 + 上方小标签「DARVIN」（Fraunces italic 11px uppercase letter-spaced）
- 整列居中 `max-w-[720px]`
- 消息之间 `section-gap` (24px) 间距
- 流式光标：v2 的 `▍` 改成在文字末尾渲染一个 1px 宽、accent 色、blink 1s 的 div
- 错误态：左边 2px 边框 `border-l-2 border-danger` + 文字 `text-danger`
- hover 消息：下方淡入时间戳（mono 11px text-subtle）

**为什么去气泡**：
- 编辑式 / 文档式阅读体验，更像 Notion 文档而不是 iMessage
- 与 serif 字体搭配时气泡显得啰嗦
- 通过「YOU / DARVIN」标签 + 对齐方向就能区分，无需额外容器

#### 4.2.3 SidebarHeader：serif 品牌

```html
<header>
  <div class="logo">     <!-- 12px 圆形，bg accent，文字 Fraunces italic 'D' -->
  <span class="brand">   <!-- "Darvin"  Fraunces 18px medium，文字色 text -->
  <IconButton name="plus" />  <!-- 24x24 圆形描边按钮，hover bg accent-soft -->
</header>
```

**为什么这样**：
- 「D」在 Fraunces italic 下是曲线优美的大写字母，比 v2 的方块「D」更编辑
- 「Darvin」是英文品牌名，配 Fraunces 而不是 Inter — 让品牌与功能 UI 拉开视觉层级
- 整体宽度 268px 比 v2 的 240px 多 28px，给「Darvin」这个长一点的品牌名留呼吸

#### 4.2.4 ChatHeader：serif 标题 + mono 模型徽章

- 标题：「{sessionTitle}」用 Fraunces 18px medium（v2 是 system 14px medium）
- 模型徽章：mono 11px uppercase letter-spaced，1px border-border 边框，padding 4px 8px，bg surface-2
  - 内容形如 `CLAUDE · SONNET 4.5`，分隔点用 `·` 增强编辑感
- 折叠按钮：v2 改用 24x24 圆形描边按钮（不是 IconButton 默认的 p-1.5），与 SidebarHeader 的 plus 按钮统一规格

#### 4.2.5 Composer：编辑感

- 容器：`max-w-[720px] mx-auto`，1px border-border，圆角 `rounded-2xl` (20px)
- 内部 padding 16px
- textarea placeholder：`Fraunces italic 14.5px`，颜色 `text-text-subtle`，内容「Write a message...」
- 右下角：char count（仅 `text.length > 50` 时显示）`JetBrains Mono 11px text-text-subtle`
- 发送按钮：36x36 圆形，`bg-accent` 实色，内部白色 `<Icon name="arrow-up" :size="16">`
  - hover：`bg-accent-hover scale-1.04`
  - disabled：`bg-border cursor-not-allowed`（**不**用 v2 的 `disabled:opacity-40` 那种灰雾感）
- busy 态：textarea 显示「Darvin is thinking...」（Fraunces italic subtle）

#### 4.2.6 SidePanel：uppercase 标签 + 极简内容

- Tab 栏：36px 高，3 个 tab 文字 `JetBrains Mono 11px uppercase letter-spaced 0.08em`，颜色 `text-text-muted`；active 文字色 `text-text` + 底部 1px 横线（inset-x-0 1px `bg-accent`）
- 内容区：空态改为「
  - 小图标（用 mono 字符或现有 icon 凑一下，比如 `cog` for tools）
  - 大标题（Fraunces italic 18px text-text-muted）例如「Tools」
  - 副标题（13px text-text-subtle）例如「Tool traces will appear here.」
  - 整体 `flex-1 flex flex-col items-center justify-center`

#### 4.2.7 Dropdown：微调

- 菜单卡片：`bg-surface border border-border rounded-lg`，padding 4px
- item 高度 28px，padding 0 10px
- hover bg：`bg-accent-soft text-text`
- 选中态：右侧 8x8 accent 圆点（不是 ✓，避免跟 check icon 重复）

#### 4.2.8 ChatHeader 折叠按钮与 SidebarHeader 按钮统一

为保持视觉一致，**所有 IconButton 在 v3 改成同一规格**：
- 默认尺寸 28x28
- 描边：默认无；hover 时 `border border-border`（v2 是单纯 bg shift）
- 圆角：`rounded-md` (6px)
- 图标尺寸 16px

`IconButton` 组件 prop 新增 `variant: 'ghost' | 'outline' | 'solid'`：
- `ghost`（默认）：无边框，hover bg surface-2
- `outline`：1px border-border 始终
- `solid`：bg-accent text-white，hover bg-accent-hover

发送按钮用 `solid`，hamburger / 折叠用 `ghost`，模型徽章触发器用 `outline`。

### 4.3 关键代码骨架（视觉改动部分）

> 只列视觉相关；props / emits / 逻辑与 v2 一致的不再重复。

#### 4.3.1 theme.css 关键片段

```css
@import "tailwindcss";

@theme {
  /* 字体 */
  --font-display: "Fraunces", ui-serif, Georgia, serif;
  --font-sans:    "Inter Tight", -apple-system, "Segoe UI", sans-serif;
  --font-mono:    "JetBrains Mono", ui-monospace, monospace;

  /* 颜色：dark default */
  --color-bg:              #0c0c0e;
  --color-surface:         #161618;
  --color-surface-2:       #1d1d20;
  --color-border:          #28282d;
  --color-border-strong:   #3a3a42;
  --color-text:            #ededee;
  --color-text-muted:      #a4a4ad;
  --color-text-subtle:     #6b6b75;
  --color-accent:          #d4a574;
  --color-accent-hover:    #e0b889;
  --color-accent-soft:     #2a221b;
  --color-user-msg:        #f3eee4;
  --color-assistant-msg:   #ededee;
  --color-danger:          #e57373;
  --color-success:         #7eb98e;
  --color-warning:         #d4a259;

  /* 圆角 */
  --radius-sm: 4px;
  --radius-md: 6px;
  --radius-lg: 10px;
  --radius-xl: 14px;
  --radius-2xl: 20px;

  /* 间距 */
  --spacing-app-padding:  14px;
  --spacing-section-gap:  24px;

  /* 字号 */
  --text-xs:   11px;
  --text-sm:   13px;
  --text-base: 14.5px;
  --text-md:   15.5px;
  --text-lg:   18px;
  --text-xl:   24px;
  --text-2xl:  32px;

  /* 动效 */
  --animate-cursor-blink: cursor-blink 1s step-end infinite;
  @keyframes cursor-blink { 0%, 50% { opacity: 1; } 51%, 100% { opacity: 0; } }
}

@layer base {
  html.light {
    --color-bg:              #f8f6f1;
    --color-surface:         #fdfcf8;
    --color-surface-2:       #f3f0e9;
    --color-border:          #e3ddd0;
    --color-border-strong:   #c9c2b1;
    --color-text:            #1a1a1c;
    --color-text-muted:      #5a5a62;
    --color-text-subtle:     #8a8478;
    --color-accent:          #a67a3d;
    --color-accent-hover:    #8e6629;
    --color-accent-soft:     #f0e9d8;
    --color-user-msg:        #1a1a1c;
    --color-assistant-msg:   #1a1a1c;
  }

  body {
    background: var(--color-bg);
    color: var(--color-text);
    font-family: var(--font-sans);
    font-size: var(--text-base);
    margin: 0;
    -webkit-font-smoothing: antialiased;
    transition: background-color 200ms ease, color 200ms ease;
  }
  * { transition: background-color 200ms ease, border-color 200ms ease; }
  *:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }

  ::-webkit-scrollbar { width: 8px; height: 8px; }
  ::-webkit-scrollbar-track { background: transparent; }
  ::-webkit-scrollbar-thumb { background: var(--color-border); border-radius: 4px; }
}
```

#### 4.3.2 AppShell 视觉细节变化

```vue
<div
  class="grid h-screen overflow-hidden bg-bg text-text"
  :style="{ gridTemplateColumns }"
  style="transition: grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1);"
>
```

grid template 不变（左 268 / flex / 右 324），只是宽度从 v2 的 240/320 微调。

#### 4.3.3 SidebarHeader 视觉骨架

```vue
<template>
  <header class="flex items-center justify-between px-4 pt-4 pb-3">
    <div class="flex items-baseline gap-2">
      <span
        class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-accent font-display text-[15px] italic text-white"
      >D</span>
      <span class="font-display text-lg font-medium text-text">Darvin</span>
    </div>
    <IconButton variant="ghost" name="plus" :label="t('app.new_chat')" @click="emit('new-chat')" />
  </header>
</template>
```

#### 4.3.4 MessageItem 视觉骨架（无气泡版）

```vue
<template>
  <article
    class="group flex w-full"
    :class="isUser ? 'justify-end' : 'justify-start'"
  >
    <div
      class="max-w-[85%]"
      :class="isError ? 'border-l-2 border-danger pl-3' : ''"
    >
      <p
        class="font-mono text-[11px] uppercase tracking-[0.08em] mb-1.5"
        :class="isError ? 'text-danger' : 'text-text-subtle'"
      >
        {{ isUser ? 'You' : 'Darvin' }}
      </p>
      <div
        class="text-md leading-relaxed whitespace-pre-wrap"
        :class="isError ? 'text-danger' : isUser ? 'text-user-msg' : 'text-assistant-msg'"
      >
        <StreamingText
          v-if="!isError"
          :content="message.content"
          :done="message.done"
        />
        <span v-else>{{ message.error }}</span>
      </div>
      <p
        class="mt-1.5 font-mono text-[11px] text-text-subtle opacity-0 transition-opacity group-hover:opacity-100"
      >
        {{ message.id }}
      </p>
    </div>
  </article>
</template>
```

#### 4.3.5 Composer 视觉骨架

```vue
<template>
  <div class="px-6 pb-5 pt-2">
    <div
      class="mx-auto max-w-[720px] flex items-end gap-2 rounded-2xl border border-border bg-surface-2 px-4 py-3 transition-colors focus-within:border-border-strong"
    >
      <textarea
        ref="textareaRef"
        v-model="text"
        :placeholder="busy ? 'Darvin is thinking…' : 'Write a message…'"
        :disabled="busy"
        rows="1"
        class="flex-1 resize-none bg-transparent font-sans text-[14.5px] leading-relaxed text-text outline-none placeholder:font-display placeholder:italic placeholder:text-text-subtle disabled:opacity-50"
        @input="autoGrow"
        @keydown="onKeydown"
      />
      <button
        type="button"
        :disabled="!canSend"
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-all"
        :class="canSend ? 'bg-accent text-white hover:bg-accent-hover hover:scale-[1.04]' : 'bg-border cursor-not-allowed'"
        :aria-label="t('chat.send')"
        @click="emitSend"
      >
        <Icon name="arrow-up" :size="16" />
      </button>
    </div>
    <p
      v-if="text.length > 50"
      class="mx-auto mt-1.5 max-w-[720px] text-right font-mono text-[11px] text-text-subtle"
    >
      {{ text.length }}
    </p>
  </div>
</template>
```

#### 4.3.6 ChatHeader 视觉骨架

```vue
<template>
  <header class="flex h-14 shrink-0 items-center justify-between border-b border-border bg-bg px-5">
    <div class="flex items-center gap-3 min-w-0">
      <IconButton variant="ghost" name="menu" :label="t('chat.menu.toggle_sidebar')" @click="emit('toggle-sidebar')" />
      <h1 class="truncate font-display text-lg font-medium text-text">{{ title }}</h1>
    </div>
    <div class="flex items-center gap-2">
      <ChatHeaderModel />
      <IconButton
        variant="ghost"
        :name="sidePanelOpen ? 'panel-right-close' : 'panel-right-open'"
        :label="t('chat.menu.toggle_sidepanel')"
        @click="emit('toggle-side-panel')"
      />
    </div>
  </header>
</template>
```

模型徽章（ChatHeaderModel）：

```vue
<template>
  <Dropdown>
    <template #trigger>
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 font-mono text-[11px] uppercase tracking-[0.08em] text-text-muted transition-colors hover:border-border-strong hover:text-text"
      >
        <span>{{ currentLabel }}</span>
        <Icon name="chevron-down" :size="12" />
      </button>
    </template>
    <template #menu>
      <ul class="min-w-[180px] rounded-lg border border-border bg-surface py-1 shadow-none">
        <li
          v-for="opt in options"
          :key="opt.id"
          class="flex h-7 cursor-pointer items-center justify-between px-2.5 font-mono text-[11px] uppercase tracking-[0.06em] text-text-muted hover:bg-accent-soft hover:text-text"
          :class="opt.id === currentModel ? 'text-text' : ''"
          @click="onSelect(opt.id)"
        >
          <span>{{ opt.label }}</span>
          <span
            v-if="opt.id === currentModel"
            class="inline-block h-1.5 w-1.5 rounded-full bg-accent"
          />
        </li>
      </ul>
    </template>
  </Dropdown>
</template>
```

#### 4.3.7 SidePanel 视觉骨架

```vue
<!-- SidePanelTabs.vue -->
<template>
  <div class="flex h-10 shrink-0 items-stretch border-b border-border bg-bg">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      class="relative flex flex-1 items-center justify-center font-mono text-[11px] uppercase tracking-[0.08em] transition-colors"
      :class="item.id === active ? 'text-text' : 'text-text-muted hover:text-text'"
      @click="emit('switch', item.id)"
    >
      {{ item.label }}
      <span
        v-if="item.id === active"
        class="absolute inset-x-3 bottom-0 h-px bg-accent"
      />
    </button>
  </div>
</template>
```

```vue
<!-- SidePanelContent.vue（空态） -->
<template>
  <div class="flex flex-1 flex-col items-center justify-center px-6 text-center">
    <p class="font-display text-lg italic text-text-muted">{{ title }}</p>
    <p class="mt-1 text-sm text-text-subtle">{{ subtitle }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import type { SidePanelTab } from '../../composables/useSidePanel';

const props = defineProps<{ tab: SidePanelTab }>();

const title = computed(() => {
  switch (props.tab) {
    case 'tools':    return 'Tools';
    case 'thinking': return 'Thinking';
    case 'artifact': return 'Artifact';
  }
});
const subtitle = computed(() => {
  switch (props.tab) {
    case 'tools':    return 'Tool traces will appear here.';
    case 'thinking': return 'No thinking trace yet.';
    case 'artifact': return 'No artifacts yet.';
  }
});
</script>
```

#### 4.3.8 main/menu.ts（Electron 菜单清理）

```ts
import { app, Menu, type MenuItemConstructorOptions } from 'electron';

export function installAppMenu(): void {
  const isMac = process.platform === 'darwin';

  if (isMac) {
    const macTemplate: MenuItemConstructorOptions[] = [
      {
        label: app.name,
        submenu: [
          { role: 'about' },
          { type: 'separator' },
          { role: 'services' },
          { type: 'separator' },
          { role: 'hide' },
          { role: 'hideOthers' },
          { role: 'unhide' },
          { type: 'separator' },
          { role: 'quit' },
        ],
      },
    ];
    Menu.setApplicationMenu(Menu.buildFromTemplate(macTemplate));
    return;
  }

  // Windows / Linux：完全隐藏菜单栏
  Menu.setApplicationMenu(null);
}
```

#### 4.3.9 main/index.ts 接入

```ts
// 在 createWindow() 之后追加：
mainWindow.webContents.on('before-input-event', (_event, input) => {
  if (
    input.key === 'F12' ||
    ((input.control || input.meta) && input.shift && input.key.toLowerCase() === 'i')
  ) {
    mainWindow.webContents.toggleDevTools();
  }
});

// app.on('ready', createWindow) 之前新增：
import { installAppMenu } from './menu';
// ...
app.on('ready', () => {
  installAppMenu();
  createWindow();
});
```

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 启动时 `localStorage` 缺失 / 损坏 | composable 初始化 fallback 默认值；try/catch 包住 localStorage 访问 |
| 切主题瞬间组件未挂载 | module-level 加载时立即应用主题（v2 行为） |
| icon 文件缺失 | `<Icon name="missing">` 警告 + 空 16×16 占位 |
| 流式回复中切到其他会话 | `useMessages.reset()` 清空；in-flight event 仍在 EventTarget 推，被 ignore |
| 流式回复中切到 light 主题 | 消息内容颜色变化，无丢失（v3 多了 200ms 渐变，更平滑） |
| 流式回复中折叠 sidebar / side panel | 布局过渡 180ms，无事件丢失 |
| 流式回复中点 "+ New chat" | 暂不弹确认（v0 简化：直接新建） |
| textarea 超过 12 行 | scrollHeight cap 12 行（max-h 200px） |
| 输入框空 + Enter | composer 不 emit |
| 流式期间 Enter | composer 仍 emit 但 send 按钮 disabled（v3 改用 bg-border 表示） |
| 切会话时新会话无 messages | 空态文案 |
| 同一 sessionId 多次 prompt | 复用现有 messageId 列表，追加新 turn |
| mock 报错（preload 抛） | catch → 消息标红边框 + busy=false |
| 浅色主题下 bubble 颜色对比度 | v3 无气泡，颜色对比已校验（user-msg #1a1a1c on bg #f8f6f1 = WCAG AAA） |
| dropdown 打开时 Esc / click outside | Dropdown 组件统一关闭（v2 行为保留） |
| 模型 dropdown 选中态 | 右侧 1.5×1.5 圆点指示（v2 是 ✓ icon） |
| 侧栏窄屏（< 1200px） | v0 不处理；窗口固定 1200×800 |
| 大量会话（> 100） | mock 阶段无此场景 |
| Google Fonts 加载失败 | `<link>` 加 `media="print" onload="this.media='all'"` + 兜底 `font-display: swap`；fallback 字体立即生效 |
| macOS 菜单栏 app name 修改 | 通过 `app.setName('Darvin')` 在 `ready` 之前；当前 `app.name` 来自 `package.json#productName`（已为 `darvin-cowork`）——v3 决定改 `productName: 'Darvin'`，或在 `app.on('ready')` 前调 `app.setName('Darvin')` |
| 跨平台菜单差异 | macOS 走 macTemplate；其他平台 `setApplicationMenu(null)`；DevTools 走全局快捷键，跨平台一致 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/main/menu.ts` | 🆕 Electron 菜单清理模块 |
| `src/main/index.ts` | 改：导入 `installAppMenu`，在 `ready` 时调；DevTools 全局快捷键；`app.setName('Darvin')`（macOS 菜单栏显示用） |
| `src/renderer/styles/theme.css` | 改：token 全部重写（颜色 / 字体 / 字号 / 间距 / 圆角 / 动效） |
| `src/renderer/index.html` | 改：`<head>` 加 Google Fonts `<link>` |
| `src/renderer/assets/icons/arrow-up.svg` | 🆕 发送按钮图标 |
| `src/renderer/assets/icons/circle-dot.svg` | 🆕 状态点（备用，runtime footer 用） |
| `src/renderer/assets/icons/*.svg` (A 组 11 个) | 改：`stroke-width` 2.4 → 1.6 |
| `src/renderer/components/sidebar/Sidebar.vue` | 改：宽度 240→268 |
| `src/renderer/components/sidebar/SidebarHeader.vue` | 改：serif 品牌 + 新 + 按钮 |
| `src/renderer/components/sidebar/SidebarFooter.vue` | 改：mono 状态标签 + 新 IconButton 规格 |
| `src/renderer/components/sidebar/SessionItem.vue` | 改：active 边 3px→2px、bg 改用 accent-soft |
| `src/renderer/components/chat/ChatHeader.vue` | 改：高度 48→56、标题 serif、IconButton 改 variant |
| `src/renderer/components/chat/ChatHeaderModel.vue` | 改：mono uppercase 模型徽章 + 圆点选中态 |
| `src/renderer/components/chat/MessageList.vue` | 改：max-w-[720px] 居中；section-gap 16→24 |
| `src/renderer/components/chat/MessageItem.vue` | 改：**无气泡方案**（YOU/DARVIN 标签 + 对齐 + hover 时间戳） |
| `src/renderer/components/chat/StreamingText.vue` | 改：`▍` 字符 → `<span>` 元素 + accent 颜色 |
| `src/renderer/components/chat/Composer.vue` | 改：圆角 2xl、placeholder Fraunces italic、发送按钮圆形、char count |
| `src/renderer/components/side-panel/SidePanel.vue` | 改：宽度 320→324 |
| `src/renderer/components/side-panel/SidePanelTabs.vue` | 改：mono uppercase + 1px 横线 active |
| `src/renderer/components/side-panel/SidePanelContent.vue` | 改：空态大标题 italic + 副标题 |
| `src/renderer/components/common/IconButton.vue` | 改：新增 `variant` prop（ghost/outline/solid）；28x28 规格 |
| `src/renderer/components/common/Dropdown.vue` | 改：菜单卡片 padding 4、item 高度 28、选中态 1.5×1.5 圆点 |
| `src/renderer/layout/AppShell.vue` | 改：宽度 240/320→268/324；加 grid-template-columns transition |
| `AGENTS.md` | 改：加一行「renderer 允许从 Google Fonts 加载 3 套字体，禁止引入其他 CDN 资源」 |

**不修改**：
- `src/renderer/components/common/Icon.vue`（API 不变）
- `src/renderer/App.vue` / `index.ts` / `index.css` / `darvin.d.ts`
- `src/renderer/composables/*.ts`（6 个，逻辑不变）
- `src/renderer/services/*.ts`（mock 数据 / i18n 不动）
- `src/shared/darvin-api.ts`（契约不变）
- `src/preload/index.ts`（API 形状不变）
- `src/darvin-agent/**`（Go 端 v3 不动）
- `forge.config.ts` / `vite.renderer.config.mts` / `package.json` / `.eslintrc.json`（基础设施不动）
- 所有 ctxengine 注释清理结果（上一轮已经做完）

---

## 7. 验收标准

### 7.1 启动

- [ ] `npm run lint` 通过
- [ ] `npm start` 启动 Electron，主窗口显示 3 栏布局（**268px + flex + 324px**）
- [ ] 窗口内/顶部菜单栏**不可见**；macOS 顶部菜单栏仅剩 `Darvin / Quit Darvin`
- [ ] F12 / Ctrl+Shift+I 仍能打开 DevTools
- [ ] 字体已加载（DevTools Network 应有 fonts.googleapis.com 请求；离线时 fallback 生效）
- [ ] 默认深色主题（背景 `#0c0c0e`）

### 7.2 视觉（Editorial Console 方向）

- [ ] 全局字体：标题用 Fraunces（serif），UI 文字用 Inter Tight（sans），ID/时间戳用 JetBrains Mono
- [ ] accent 颜色是暖琥珀 `#d4a574`（深）/ `#a67a3d`（浅）
- [ ] 消息**无气泡**；「YOU」「DARVIN」标签是 Fraunces italic 11px uppercase letter-spaced
- [ ] 侧栏品牌名「Darvin」是 Fraunces 18px medium，配圆形 accent 底 + 白色 italic 「D」logo
- [ ] 模型徽章是 mono 11px uppercase letter-spaced，1px 边框
- [ ] Composer 是 `rounded-2xl` (20px) 圆角，placeholder 是 Fraunces italic
- [ ] 主题切换有 200ms 渐变（不闪）

### 7.3 交互（v2 全部保留）

- [ ] 发 mock 流式：「ping」→ user 消息右对齐 + assistant 消息左对齐逐字符出现，带 accent 颜色光标
- [ ] 流式带 `▍` 光标改为 accent 颜色块，done 后消失
- [ ] 流式期间 send 按钮 bg-border 不可点 + placeholder 改 "Darvin is thinking..."
- [ ] DevTools console `window.darvin` 看到 6 个方法
- [ ] 主题切换：toggle 按钮 → 整个 UI 浅色 → 按钮 icon 切
- [ ] 折叠 sidebar：hamburger → sidebar 宽度 180ms 过渡收起
- [ ] 模型 dropdown：点击 badge → 下拉 → 选中 → 右侧 1.5×1.5 accent 圆点指示
- [ ] 切换会话：点 SessionItem #2 → chat 标题变 + 消息列表清空 + 加载 mock messages
- [ ] error event：mock 注入 error → 消息左边 2px danger 边框 + 文字 danger

### 7.4 样式合规（v2 + v3 增量）

- [ ] 组件**无** `<style>` 块
- [ ] 组件**无**裸 `style="..."` 属性
- [ ] grep `bg-\[` / `text-\[` / `color: #` 在 `src/renderer/components/` 下 = 0
- [ ] 字体走 `font-display` / `font-sans` / `font-mono` utility（值来自 token）
- [ ] 字号走 `text-xs` / `text-sm` / `text-md` / `text-lg` / `text-xl` / `text-2xl`（token scale）
- [ ] `package.json` 不含 `lucide-vue-next` / `@heroicons/vue` 等图标库
- [ ] 13 个 icon 通过 `<Icon name="..." />` 组件

### 7.5 Electron 菜单

- [ ] `Menu.setApplicationMenu(null)` 在 Windows / Linux 生效，窗口无菜单栏
- [ ] macOS 仅显示 `Darvin / Quit Darvin` 等最小菜单
- [ ] DevTools 仍可通过 F12 / Ctrl+Shift+I / Cmd+Opt+I 打开（不走菜单）

### 7.6 TypeScript / build

- [ ] `tsconfig.json` 不用改
- [ ] `npm run build`（electron-forge）成功
- [ ] `go vet ./...` 仍 clean（Go 端未动）

### 7.7 文档

- [ ] `AGENTS.md` 加一行「renderer 允许从 Google Fonts 加载 3 套字体，禁止引入其他 CDN 资源」
- [ ] `specs/CHECKLIST.md` S1 子任务勾选更新

---

## 8. 后续 spec 候选（v3 不在范围）

| Spec | 内容 |
|------|------|
| S2 agent-sessions-store | Go sessions.db |
| S3 agent-gateway-server | Go 网关 |
| S4 agent-acp-loop | Go agent loop |
| S5 electron-runtime-client | 真 IPC 客户端 |
| S6 agent-e2e-integration | 三层接通 |
| Pinia | 跨组件大量状态时引入 |
| Vue Router | 多视图（设置 / 历史） |
| i18n 切换 UI | v0 中英混用 |
| Artifact 渲染器 | side panel Artifact tab |
| Markdown 渲染 | assistant content 走 markdown |
| Tool calls 渲染 | `<MessageItem>` 折叠 sub-component |
| Thinking 渲染 | `thinking_delta` 折叠 / 详展 |
| 字体本地化打包 | Google Fonts → 自托管 woff2（合规 / 离线优先） |
| macOS Dock 图标 | 自定义 Darvin mark |

---

## 9. 待用户决策 / 已发现的问题（不阻塞 spec 评审）

1. **Google Fonts vs 自托管** — v3 默认从 `fonts.googleapis.com` 加载 3 套字体，优点是 0 构建成本；缺点是依赖外网（v0 fallback 已加）。**等用户决定**：是接受这个依赖，还是要求一开始就走自托管 woff2？
2. **`productName` 改不改** — 当前 `package.json` 的 `productName` 是 `"darvin-cowork"`，macOS 菜单栏默认显示这个。**建议改**为 `"Darvin"` 以匹配品牌（更编辑感）。**等用户拍板**。
3. **空态文案中英混用** — v3 改用更克制的英文（"You"、"Darvin"、"Tools"）。**是否同步把 i18n.ts 字典的中文 key 改成英文**？还是保留中英混用现状（i18n 不动，仅 UI 文字用硬编码英文）？
4. **`__dirname` 三级回溯的注释** — `src/main/runtime/manager.ts` 第 22 行 `// __dirname 在开发态位于 .vite/build/main/，回溯三级到仓库根` 在清理 roadmap 注释时**保留了**（这是功能性描述），但语义有点过时（v3 没动这块）。**保留 vs 删**？
5. **Composer 的 `max-h 200px`** — v3 没改。但 v2 的「~12 行」对应 14px×12=168px，200px 略大。**保留 v2 行为**（不动）。
6. **「Darvin」品牌字体大小** — v3 选 18px（Fraunces medium）。**偏大 vs 偏小**？
7. **消息 `max-w-[85%]`** — v3 比 v2 的 80% 略宽。**保留 85% 还是收回到 80%**？
8. **circle-dot.svg 是否真的需要** — 计划用作 SidebarFooter 的状态点，但目前 SidebarFooter 用 `bg-success` 圆 dot div 实现即可。**加这个 icon 还是删掉**？
9. **Accent 二次色** — v3 引入了 `--color-accent-soft`（accent 弱化背景）。**是否够用**？还是需要再加 `--color-accent-tint`（accent 更弱化的 hover 态背景）？
10. **macOS app name 设置时机** — `app.setName('Darvin')` 必须在 `ready` 之前调；如果 `package.json#productName` 已改 `"Darvin"` 则无需显式 setName。**走哪条**？

---

> **审核要点**（请用户重点确认）：
>
> 1. **Editorial Console 方向本身**（暖琥珀 accent + Fraunces serif + 无气泡消息）是否符合预期？
> 2. **Google Fonts CDN 引入**是否接受？
> 3. **Electron 菜单清理策略**（macOS 保留最小菜单 + 其他平台 setApplicationMenu(null)）是否合理？
> 4. **v2 目录 / 契约 / composable 全部保留**这一条是否同意？
> 5. **§9 10 个待决策项**逐条确认或回退。
