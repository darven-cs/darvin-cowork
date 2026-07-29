# agent-ui-shell 设计文档 (v5)

> 本文档在 v4 基础上扩范围至「全套 LobsterAI 复刻」（用户决策 C 档）。
> v4 文件保留作历史：`2026-07-29-agent-ui-shell-design-v4.md`。
> 视觉参考原型：`lobsterai-home-prototype.html`。

## 0. 与 v4 的差异（导读）

| 维度 | v4 | v5 |
|---|---|---|
| 范围 | sidebar 4 段 + token 切换 + 主区微调 | 全套复刻：sidebar 5 段 + Home 视图 + Chat 视图 |
| Sidebar nav 项 | 2 项（对话 / 定时任务） | 7 项（对话 / 定时 / 技能 / 套件 / MCP / IM / 梦境），见 §3 FR-3 |
| Sidebar agent 卡 | 1 个 | 3 个（main / 研究员 / 前端工程师），见 §3 FR-3 |
| 圆角 token | sm=4 / md=6 / lg=8 / xl=12 | sm=4 / md=6 / lg=8 / xl=12 / 2xl=16 / pill=999 + 局部 7/9，见 §3 FR-2 |
| 主背景 | 实色 | aurora 3 层 + grid mask，见 §3 FR-8 |
| Sidebar 背景 | 实色 | 半透明 + backdrop-filter blur(20px)，见 §3 FR-3 |
| 字体加载 | Google Fonts CDN | 本地 woff2 + CDN 兜底，见 §3 FR-1 |
| 视图切换 | 单一聊天视图 | 空会话 → HomeView；有消息 → MessageList，见 §3 FR-16 |
| 新增组件数 | +5（sidebar 子组件） | +13（sidebar + home + prompt-dock 子组件），见 §6 |
| PR 拆分 | 1 PR | 3 PR，见 §8 |
| 验收项 | 11 项 | 25+ 项，见 §7 |

---

## 1. 概述

### 1.1 问题 / 背景

v4 spec 的范围是「换 sidebar 结构 + 换视觉 token + 微调主区」，明确排除了原型 `lobsterai-home-prototype.html` 的核心元素（Home 视图、mascot、quick-action pills、mode chips、prompt-toolbar、attach cards、ctx meter、voice wave）。

用户审核 v4 spec 后反馈：「要做和 LobsterAI 一样的 UI」，并选择 C 档范围（全套复刻）。本 v5 即为 C 档落地版。

### 1.2 目标

1. 视觉 1:1 复刻 `lobsterai-home-prototype.html`：blue primary `#60A5FA` + Geist 字体栈 + aurora/grid 背景 + 玻璃 sidebar。
2. 引入 Home 视图（空会话时显示），含 mascot + greeting + 7 个 quick-action pills + prompt-dock（mode chips + attach + voice + model-picker + ctx meter）。
3. Sidebar 改为 5 段：顶部 primary → 主导航（7 项）→ 当前 Agent（3 卡）→ 会话列表 → 底部 dock（5 主题 picker + 用户卡）。
4. Chat 视图保留 v3 的「无气泡 + YOU/DARVIN label」结构，但视觉 token 全部跟新。
5. 视觉 token 扩展：圆角到 2xl=16/pill=999，新增 qa-color 系列，新增 aurora/grid 变量。
6. 严格遵守 AGENTS.md 编码规范（utility-only、无 `<style>`、无内联 style 颜色）。

### 1.3 非目标

- **不做** edge actions（原型右侧浮动卡片）、toast 通知、float-pill（mascot 周围浮动小卡）——这些是装饰性非核心，留给后续。
- **不做** 多 Agent 真实切换 / 多 nav 真实路由（保留视觉但点击只切 active，不切换业务视图）。
- **不做** 5 主题 picker 的真实切换（picker 渲染但只有 dark/light 真生效，ocean/emerald/sakura 为视觉占位）。
- **不做** voice wave 的真实录音（按钮可点切换 recording class，但不接麦克风权限）。
- **不做** attach 的真实文件选择（× 按钮可移除单条，+ 不接文件系统）。
- **不做** IPC 协议、Go agent、主进程改动。
- **不做** quick-action 真实业务（点击只把 prompt 文本填到 textarea）。

---

## 2. 用户场景

### 场景 1: 首次启动 → Home 视图
**Given** 用户打开应用，mock 数据里 s-001 / s-002 / s-003 都是「有消息」状态
**When** 用户点击 sidebar 顶部「新建任务」按钮
**Then**
- main agent 创建新 session（id 形如 `s-004`），消息列表为空
- 主区从 MessageList 切换为 HomeView（mascot + greeting + 7 个 quick-action + prompt-dock）
- sidebar「近期任务」顶部出现新会话项（active），时间为「现在」
- localStorage 记录 currentSessionId

### 场景 2: 从 Home 视图回到 Chat 视图
**Given** 当前在新会话 s-004 的 Home 视图
**When** 用户点击 sidebar「近期任务」中的 s-001
**Then**
- 主区从 HomeView 切换为 MessageList
- 渲染 s-001 的 mock 消息（2 条：ping / Pong）
- active 状态从 s-004 移到 s-001

### 场景 3: 选择 quick-action
**Given** 当前在 Home 视图
**When** 用户点击「生成 PPT」pill
**Then**
- prompt-dock 的 textarea 自动填入：「生成 PPT」预定义模板文本（如「帮我生成一份关于 [主题] 的 PPT，目标受众是 [人群]，需要 [页数] 页」）
- textarea 获得焦点，光标在第一个 `[主题]` 处
- 不自动发送

### 场景 4: 切换 mode chip
**Given** prompt-dock 显示
**When** 用户点击「Plan 模式」chip（当前 active）→ 点击「技能 · xlsx」chip
**Then**
- Plan 模式 chip 失去 active 状态（左侧 ✓ 消失）
- 技能 chip 获得 active 状态
- 仅视觉切换，不影响 mock 数据

### 场景 5: 切换主题
**Given** 当前 dark
**When** 用户点击 sidebar 底部 5 主题 picker 中的「Ocean」圆点
**Then**
- 由于 Ocean 是占位主题，不实际切换（保持 dark）
- picker 内的 active 高亮跳到 Ocean 圆点（视觉反馈）
- console 打印 warning：「theme 'ocean' is reserved, not implemented yet」
- 仅 classic-dark / classic-light 圆点点击会真实切换 html.class

### 场景 6: 切换暗/亮（真实）
**Given** 当前 dark
**When** 用户点击 classic-light 圆点
**Then**
- html 根 class 从 `dark` 翻转到 `light`
- 所有面板同步翻转，过渡 200ms
- localStorage 记录偏好

---

## 3. 功能需求

### FR-1: 字体栈切换 + 本地化

**字体族**：
- `--font-sans` → `Geist` (300/400/500/600/700)
- `--font-mono` → `Geist Mono` (400/500)
- `--font-display` → `Instrument Serif`（仅 italic，用于 hero greeting）

**加载策略**（双轨）：
1. 本地 woff2：下载 Geist / Geist Mono / Instrument Serif 的 woff2 到 `src/renderer/assets/fonts/`，通过 `@font-face` 在 `theme.css` 注册。
2. CDN 兜底：`index.html` 保留 Google Fonts `<link>` 作为离线 fallback，但在 `@font-face` `src` 列表里**本地 woff2 优先**，CDN 兜底。

**移除**：Fraunces / Inter Tight / JetBrains Mono 的引用（含 `index.html` link 和 theme.css `@font-face`）。

**fallback 链**：`-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei UI', sans-serif`。

### FR-2: 主题 token 重写 + 圆角扩展

**颜色**（dark default → light 覆盖）：
- `--color-primary` `#d4a574` (v3 amber) → `#60A5FA` (blue)；light `#3B82F6`
- `--color-primary-hover`：`#93C5FD` / `#2563EB`
- `--color-primary-muted`：`rgba(96,165,250,0.12)` / `rgba(59,130,246,0.08)`
- `--color-primary-soft`：`rgba(96,165,250,0.06)` / `rgba(59,130,246,0.04)`
- `--color-accent`（success / 在线点）保留 `#34D399`
- `--color-bg`：`#0F1117` / `#FAFBFC`
- `--color-surface`：`#181A23` / `#FFFFFF`
- `--color-surface-raised`：`#1E212B` / `#F4F5F7`
- `--color-surface-hover`：`#252830` / `#EBEDF0`
- `--color-border`：`rgba(255,255,255,0.06)` / `rgba(0,0,0,0.06)`
- `--color-border-strong`：`rgba(255,255,255,0.12)` / `rgba(0,0,0,0.12)`
- `--color-text-subtle`：`#5C6370` / `#9CA3AF`
- `--color-user-msg`：`#60A5FA` / `#3B82F6`
- `--color-warm`（mascot / recording）：`#FF5F56` / `#EF4444`
- `--color-warm-soft`：`rgba(255,95,86,0.14)` / `rgba(239,68,68,0.10)`

**quick-action 颜色 token**（每个 pill 一种，避免内联 `style="--qa-color: ..."`）：
- `--color-qa-ppt` `#F59E0B`
- `--color-qa-data` `#8B5CF6`
- `--color-qa-teach` `#10B981`
- `--color-qa-web` `#3B82F6`
- `--color-qa-design` `#EC4899`
- `--color-qa-write` `#F472B6`
- `--color-qa-translate` `#06B6D4`

**agent avatar 渐变**（避免内联渐变）：
- `--color-avatar-main`：linear-gradient `#60A5FA → #A855F7`
- `--color-avatar-research`：linear-gradient `#F472B6 → #EC4899`
- `--color-avatar-frontend`：linear-gradient `#34D399 → #10B981`

**背景氛围**：
- `--color-aurora-1`：`rgba(96,165,250,0.18)` / `rgba(59,130,246,0.10)`
- `--color-aurora-2`：`rgba(244,114,182,0.10)` / `rgba(244,114,182,0.06)`
- `--color-aurora-3`：`rgba(52,211,153,0.10)` / `rgba(16,185,129,0.06)`
- `--color-grid-line`：`rgba(255,255,255,0.025)` / `rgba(0,0,0,0.03)`

**圆角 token 扩展**：
- `--radius-sm` 4
- `--radius-md` 6
- `--radius-lg` 8
- `--radius-xl` 12
- `--radius-2xl` 16（**新增**，prompt-box 用）
- `--radius-pill` 999（**新增**，pill / 圆形 avatar 用）
- 局部硬编码（不经 token）：`search-input` 7 / `agent-pill` 7 / `new-chat-btn` 9 / `send-btn` 9 / `mode-chip` 6
  - 用 Tailwind arbitrary value 实现：`rounded-[7px]` / `rounded-[9px]`

**阴影**：
- `--shadow-sm`：`0 1px 2px rgba(0,0,0,0.2)` / `0 1px 2px rgba(0,0,0,0.04)`
- `--shadow-md`：`0 4px 12px rgba(0,0,0,0.3)` / `0 4px 12px rgba(0,0,0,0.08)`
- `--shadow-lg`：`0 12px 40px rgba(0,0,0,0.4)` / `0 12px 40px rgba(0,0,0,0.12)`

**间距**：保留 `--spacing-app-padding` 12 / `--spacing-section-gap` 16。

**过渡**：`* { transition: background-color 200ms ease, color 200ms ease, border-color 200ms ease }`。

### FR-3: Sidebar 结构重构（5 段）

Sidebar 宽度 248px，整体背景 `bg-surface/60 + backdrop-blur-20px`（玻璃效果），右边 1px border-border。

**顶部 primary action 区（≈ 84px）**：
- 「新建任务」按钮：`bg-primary` + 白字 + 14×14 plus icon + `⌘N` kbd hint + `rounded-[9px]` + `shadow-md` + hover translateY(-1px)
- 搜索框：`bg-surface-raised` + 12×12 search icon + placeholder「搜索任务...」+ `rounded-[7px]` + 1px border-border + hover border-strong

**主导航（7 项）**：
- nav-item 列表（顺序、icon、label、count）：

  | 顺序 | label | icon | 右侧 |
  |---|---|---|---|
  | 1 | 对话 | message-square | count=12 |
  | 2 | 定时任务 | clock | count=3 |
  | 3 | 技能 | star | count=8 |
  | 4 | 套件 | layout | **badge NEW**（粉紫渐变） |
  | 5 | MCP | link | count=5 |
  | 6 | IM 通道 | message-circle | （无 count） |
  | 7 | 梦境 | sun | （无 count） |

- active 状态：`bg-primary-muted` + 文字 `text-primary` + 左侧 2px primary 指示条（`::before`，但因为 AGENTS.md 禁组件 `<style>`，改为子元素 `<span class="absolute left-0 top-2 bottom-2 w-0.5 rounded-full bg-primary">`）
- hover：`bg-surface-hover` + `text-text`
- 图标 14×14，count 文字 `font-mono text-[10.5px]`
- badge "NEW"：`text-[9px] font-semibold px-1.5 py-px rounded-pill bg-gradient-to-r from-[#F472B6] to-[#A855F7] text-white tracking-[0.04em]`

**当前 Agent 区（3 个 agent pill，垂直堆叠）**：
- nav-label：「当前 Agent · main」（main 用 `text-primary`）
- 3 个 agent-pill（22×22 渐变 avatar + name 12px + sub 10.5px muted + 6×6 绿色 dot 仅 main agent）：

  | agent | avatar 渐变 | name | sub | dot |
  |---|---|---|---|---|
  | main | `bg-avatar-main`（token 渐变） | LobsterAI | 全场景办公助手 | ✓ |
  | 研究员 | `bg-avatar-research` | 研究员 | 深度调研 · 长报告 | ✗ |
  | 前端工程师 | `bg-avatar-frontend` | 前端工程师 | React · UI · Tailwind | ✗ |

**会话列表（flex 1，滚动）**：
- nav-label：「近期任务」
- 7 个 session-item（mock 数据：见 §3 FR-16），结构：12×12 icon + truncate 标题 + 10px mono 时间
- active：`bg-surface-raised + text-text`
- hover：`bg-surface-hover + text-text`
- 圆角 `rounded-md`，间距 8px

**底部 dock**：
- 5 主题 picker：5 个 18×18 圆点（dark / light / ocean / emerald / sakura），active 圆点有 2px primary 描边；点击仅 dark/light 生效，其他 3 个 console.warn 占位
- 用户行：26×26 渐变 avatar + name + plan（PRO badge + 4,280 credits） + 右侧 chevron-right

### FR-4: ChatHeader 视觉更新

- 保持 `h-14`，padding 改为 `px-[28px]`（与原型一致）。
- 左侧：hamburger IconButton + 面包屑（`LobsterAI /` crumb + 当前页 h1）。
- 右侧：2 个 ghost-btn（导入任务 / 设置）+ 模型 chip + side-panel toggle。
- 模型 chip：14×14 渐变 glyph + 模型名 + chevron-down，`rounded-md` + 1px border-border + hover bg-surface-hover。
- 面包屑 / crumb 颜色 `text-text-muted`，分隔符 `/` opacity 0.5。

### FR-5: MessageList / MessageItem / StreamingText 视觉更新

- 保持「无气泡 + YOU / DARVIN label」结构。
- 字号 14 → 14.5（与原型对齐）。
- label 字号 10.5 → 11。
- 用户消息文字色：dark 用 `text-user-msg`（blue `#60A5FA`）。
- assistant 消息文字色：`text-assistant-msg`。
- messages-inner 最大宽度 760px，居中，间距 18px。

### FR-6: Composer 视觉更新（仅 Chat 视图）

> Home 视图用 PromptDock（FR-14），Chat 视图保留 Composer。

- 圆角 `rounded-2xl`（16px）。
- placeholder：Geist 13.5px normal「描述你要完成的任务...」。
- 发送按钮：32×32 蓝色方块 + arrow-up-right icon 14×14 + `rounded-[9px]` + hover translateY(-1px)。
- 顶部 `rounded-2xl` 一致，整体宽度跟随 messages-inner 760px 居中。

### FR-7: SidePanel 视觉更新

- 3 tab 不变（TOOLS / THINKING / ARTIFACT）。
- tab 文字 mono uppercase + 1px primary 底色下划线。
- empty state 文字 Geist 13px。
- 右栏宽度 324 → **300**。

### FR-8: AppShell grid + aurora + grid 背景

**grid**：`gridTemplateColumns: 248px 1fr 300px`，过渡 180ms。

**装饰层**（关键，v4 遗漏）：
- AppShell.vue `<template>` 增加结构：
  ```
  <div class="grid h-screen ...">
    <div class="pointer-events-none absolute inset-0 -z-10 aurora-bg" />
    <div class="pointer-events-none absolute inset-0 -z-10 grid-bg" />
    <Sidebar />
    <ChatPane />
    <SidePanel />
  </div>
  ```
- aurora-bg：`bg-[radial-gradient(900px_600px_at_12%_8%,theme(colors.aurora-1),transparent_60%),radial-gradient(...)]`（Tailwind arbitrary）
- grid-bg：`bg-[linear-gradient(theme(colors.grid-line)_1px,transparent_1px),linear-gradient(90deg,...)] bg-[length:56px_56px] [mask-image:radial-gradient(ellipse_at_center,black_30%,transparent_78%)]`
- `AppShell.vue` 根容器需 `relative isolate` 让装饰层正确堆叠

### FR-9: icons 扩展

**新增 SVG**（viewBox 0 0 24 24, stroke-width 1.7~2.2, stroke="currentColor"）：
- `search.svg` — 放大镜
- `calendar-clock.svg` — 定时任务
- `star.svg` — 技能
- `layout.svg` — 套件
- `link.svg` — MCP
- `message-circle.svg` — IM 通道
- `sun.svg`（已存在，复用为「梦境」icon）
- `chevron-right.svg` — 用户行箭头
- `chevron-down.svg`（已存在）
- `arrow-up-right.svg` — 发送 icon（替换 v3 `send.svg`）
- `bot.svg` — 对话 nav（消息气泡 + 尾巴）
- `paperclip.svg` — 附件
- `mic.svg` — 录音
- `at-sign.svg` — @提及
- `globe.svg` — 翻译
- `sliders.svg` — 设置
- `bell.svg` — 通知（titlebar）
- `more.svg` — 更多（titlebar 垂直三点）
- `x.svg` — 移除附件
- `check.svg`（已存在）
- `plus.svg`（已存在）

quick-action 7 个 svg（每个 pill 一个，配色由 token 控制，stroke=currentColor）：
- `qa-ppt.svg` / `qa-data.svg` / `qa-teach.svg` / `qa-web.svg` / `qa-design.svg` / `qa-write.svg` / `qa-translate.svg`

mode chip icons：
- `mode-plan.svg`（active 时配合 check）
- `mode-skill.svg`（技能 · xlsx）

**自动 glob 加载**，丢到 `src/renderer/assets/icons/<name>.svg` 即可。

### FR-10: HomeView 视图（新增）

整体布局：
- 容器：`flex flex-col items-center px-8 pb-8 flex-1 min-h-0`
- hero 区：`flex flex-col items-center justify-center w-full max-w-[760px] text-center gap-[14px] flex-1`
- 进入动画：`animate-[heroIn_0.7s_cubic-bezier(0.2,0.7,0.2,1)_both]`（keyframes 加到 theme.css `@theme` 的 `--animate-*`，**或**作为 utility 写到 theme.css 全局——AGENTS.md 允许 `reset.css` 极少量全局，keyframes 算装饰可放 theme.css）

**子组件树**：
```
HomeView.vue
├── Mascot.vue                    （76×76 红色头像 + 眼/嘴/Z 动画）
├── HeroGreeting.vue              （56px serif italic + tagline + live-dot meta）
├── QuickActions.vue              （7 个彩色 pill）
└── PromptDock.vue                （attachments + prompt-box + hint）
    ├── AttachCards.vue           （附件卡列表，mock 2 个）
    ├── PromptBox.vue             （mode-chips + textarea + toolbar）
    │   ├── ModeChips.vue
    │   ├── Textarea              （原生）
    │   └── PromptToolbar.vue
    │       ├── ToolGroup (attach + mode + skill)
    │       ├── ModelPicker.vue
    │       ├── VoiceWave.vue
    │       ├── CtxMeter.vue
    │       └── SendButton
    └── PromptHint.vue            （kbd hint 行）
```

### FR-11: Mascot 组件

- 76×76 红色渐变球（`radial-gradient(circle at 32% 28%, #FF8A7A 0%, #E63946 55%, #B91C1C 100%)`），用 `bg-[radial-gradient(...)]` arbitrary value
- `rounded-[22px]` + 多层 box-shadow（`shadow-[inset_0_-8px_12px_rgba(0,0,0,0.18),inset_0_1px_0_rgba(255,255,255,0.18),0_14px_28px_rgba(255,77,77,0.25)]`）
- 外层 glow：absolute inset negative + radial blur + animate glowPulse 3.5s
- 眼睛：2 个 6×8 黑色椭圆 + white 2px halo + blink 4.5s animation
- 嘴：12×6 border-bottom + rounded-bottom
- Z 字浮：2 个 `font-mono` Z，floatZ 3.5s animation，错开 1.2s delay

**动画 keyframes** 在 theme.css 注册：`--animate-mascot-breathe` / `--animate-mascot-blink` / `--animate-mascot-glow` / `--animate-mascot-floatz`。

### FR-12: HeroGreeting

- `<h2>` `font-display italic text-[56px] leading-[1.05] tracking-[-0.01em]`
- 文本「下午好<span class="ampersand text-primary">,</span> Darven」（ampersand 用 `,` 字符 + primary 色，避免真的 ampersand `&` 字符）
- tagline：14.5px muted，max-width 520px，accent 词（`LobsterAI` / `一句话就能开始`）用 `text-text font-medium`
- live-dot meta：`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-pill bg-surface border border-border text-[11.5px] text-text-muted`，含 6×6 绿色脉冲 dot + 「所有引擎就绪 · 4 个技能已加载 · 8 个工具可用」

**greeting 时段逻辑**：
- `<6` 「凌晨好」/ `6-12` 「早上好」/ `12-14` 「中午好」/ `14-18` 「下午好」/ `18-24` 「晚上好」
- name 从 mock user 服务取（Darven）

### FR-13: QuickActions

7 个 pill，flex-wrap，gap 2，居中：

| order | label | qa-color token | icon | placeholder template |
|---|---|---|---|---|
| 1 | 生成 PPT | `qa-ppt` `#F59E0B` | qa-ppt.svg | 「帮我生成一份关于 [主题] 的 PPT...」 |
| 2 | 数据分析 | `qa-data` `#8B5CF6` | qa-data.svg | 「分析这份数据：[粘贴/附件]...」 |
| 3 | 教学辅导 | `qa-teach` `#10B981` | qa-teach.svg | 「请讲解 [概念]...」 |
| 4 | 做网站 | `qa-web` `#3B82F6` | qa-web.svg | 「帮我做一个 [类型] 网站...」 |
| 5 | 设计组件 | `qa-design` `#EC4899` | qa-design.svg | 「设计一个 [类型] 组件...」 |
| 6 | 长文写作 | `qa-write` `#F472B6` | qa-write.svg | 「写一篇关于 [主题] 的长文...」 |
| 7 | 翻译润色 | `qa-translate` `#06B6D4` | qa-translate.svg | 「翻译润色：[粘贴文本]」 |

pill 结构（合规处理：颜色走 token，不用内联 `style="--qa-color: ..."`）：
- 容器：`inline-flex items-center gap-2 px-3.5 py-1.5 rounded-pill bg-surface border border-border text-text-muted text-[12.5px] cursor-pointer transition-all`
- icon-wrap：18×18 `rounded-[5px] grid place-items-center`，颜色由父 pill 的 qa-color class 注入到子选择器
  - 实现：QuickActions.vue 接收 `:color="qa-ppt"`，渲染 `<button :class="['qa-pill', \`qa-pill--\${color}\`]">`，theme.css 全局定义 `.qa-pill--qa-ppt .icon-wrap { background: ...; color: ... }`（**全局 CSS，不算组件级 `<style>`**）
- hover：`hover:-translate-y-px hover:text-text hover:border-border-strong`，加 qa-color 阴影（同样通过 `.qa-pill--xxx:hover` 全局选择器）
- 右侧 arrow：默认 opacity 0 + translateX(-4px)，hover 显示

### FR-14: PromptDock + 子组件

**PromptDock 容器**：`w-full max-w-[760px] mx-auto shrink-0`

**AttachCards**（mock 2 张）：
- flex gap-2 mb-2 flex-wrap
- 单张卡片：`inline-flex items-center gap-2 p-1.5 pr-2 bg-surface border border-border rounded-lg text-[11.5px] text-text`
- thumb：24×24 rounded-[5px] grid place-items-center
- name + size（mono 10px muted）+ x button（16×16，hover bg-surface-hover）
- mock 2 张：`Q3-数据汇总.xlsx` / `活动主视觉.png`，点 × 移除单张

**PromptBox**：
- 容器：`relative bg-surface border border-border rounded-2xl transition-all`
- focus-within：border 改为 `color-mix 50% primary` + box-shadow 4px primary-soft + 16px primary glow（用 arbitrary value + `focus-within:` 变体）
- prompt-mode-row：`flex items-center gap-1.5 px-3.5 pt-2.5 flex-wrap`

**ModeChips**（mock 2 个，第 1 个 active）：
- active：「Plan 模式」chip，11×11 primary 方块 + 白色 check icon + 文字，背景 primary-muted + 边框 30% primary + 文字 primary
- inactive：「技能 · xlsx」chip，10×10 outline icon + 文字，default 状态
- chip 容器：`inline-flex items-center gap-1.5 px-2 py-[3px] rounded-md border border-border text-[11px] text-text-muted cursor-pointer transition-all`

**Textarea**：
- `block w-full px-4 pt-3 pb-1 bg-transparent border-0 resize-none text-text text-[14.5px] leading-[1.55] min-h-[64px] max-h-[220px]`
- placeholder Geist 14.5px muted：「描述你要完成的任务...」

**PromptToolbar**：
- `flex items-center gap-1 px-2 pb-2`
- tool-btn：`inline-flex items-center gap-1.5 px-2 py-1.5 border-0 bg-transparent text-text-muted text-xs rounded-md cursor-pointer transition-all hover:bg-surface-hover hover:text-text`
- 4 组：
  - 左：paperclip（附件）+ mode-plan/skill（模式选择）+ at-sign（提及）
  - 中：`flex-1`
  - 右：ModelPicker + VoiceWave + CtxMeter + SendButton

**ModelPicker**：
- `inline-flex items-center gap-1.5 px-2 py-1 rounded-[7px] border border-border text-text text-xs cursor-pointer transition-all hover:bg-surface-hover hover:border-border-strong`
- 14×14 渐变 glyph + 模型名 + chevron-down
- mock 3 选项（点击不展开，仅视觉占位，避免和 ChatHeader 的模型 chip 重复实现）

**VoiceWave**：
- 默认状态：tool-btn 样式 + mic icon
- recording 状态：`text-warm bg-warm-soft`，mic icon 旁加 4 根波形条（2px 宽，animate scaleY 0.4 → 1，错开 delay 0.15s）

**CtxMeter**：
- 70×3 进度条，`rounded-[2px] bg-surface-raised overflow-hidden relative`
- 内部 22% 渐变条 `bg-gradient-to-r from-accent to-primary`
- 左侧文字「Context」`font-mono text-[10.5px] text-text-muted`

**SendButton**：
- 32×32 `bg-primary text-white rounded-[9px] grid place-items-center shadow-[0_4px_12px_rgba(96,165,250,0.32)]`
- hover `translateY(-1px) bg-primary-hover`
- active `translateY(0)`
- disabled `bg-surface-raised text-text-muted shadow-none cursor-not-allowed`
- icon：arrow-up-right 14×14

**PromptHint**：
- `flex items-center justify-between mt-2 text-[10.5px] text-text-muted font-mono`
- 左：「Enter 发送 · Shift+Enter 换行」（kbd 包裹）
- 右：「今天 0 次任务」（mock）

### FR-15: 视图切换逻辑

**useViewMode composable**（新增 `src/renderer/composables/useViewMode.ts`）：
- state：`view: Ref<'home' | 'chat'>`
- 切换规则：当 `messages.list.value.length === 0` → `view = 'home'`；否则 `view = 'chat'`
- watch `messages.list` 自动派生 view（不直接 set）

**ChatPane.vue 改造**：
```vue
<template>
  <section class="flex flex-col min-w-0 min-h-0">
    <ChatHeader />
    <HomeView v-if="view === 'home'" @select-quick-action="..." />
    <MessageList v-else :messages="messages.list" />
    <Composer v-if="view === 'chat'" />  <!-- Home 视图的 prompt-dock 在 HomeView 内部 -->
  </section>
</template>
```

### FR-16: Mock 数据扩展

**sessions（7 条，替代 v3 的 3 条）**：
| id | title | time | icon |
|---|---|---|---|
| s-001 | 为 Q3 营销总结做一份 PPT | 现在 | message-square |
| s-002 | 分析 Q2 销售数据并生成图表 | 1h | message-square |
| s-003 | 批量重命名 4K 张产品图 | 今 | file |
| s-004 | 中翻英：产品发布会讲稿 | 昨 | message-square |
| s-005 | 整理飞书周会纪要 | 2d | check-circle |
| s-006 | React 表单组件重构 | 3d | file |
| s-007 | 设计个人作品集首页 | 5d | message-square |

**messages**：保留 v3 的 mockMessages（s-001 的 ping/Pong），其他 session mock 1 条欢迎消息。

**新建会话**：`createSession()` 返回 s-008，messages 为空 → 触发 HomeView。

---

## 4. 实现方案

### 4.1 theme.css 重写

`@theme` 块覆盖：
- 字体 3 个变量替换（Geist / Geist Mono / Instrument Serif）
- 颜色全量重写（含 qa-color 7 个 + avatar 渐变 3 个 + aurora 3 个 + grid 1 个 + warm/warm-soft 2 个）
- 圆角扩到 2xl=16 / pill=999
- 阴影 3 档
- 动画 keyframes 7 个：cursor-blink（保留）+ hero-in + mascot-breathe/blink/glow/floatz + voice-bar + live-pulse + float-y

`@layer base` 下：
- `html.light` 覆盖所有颜色 token
- `html.dark` 显式声明 default（不依赖 `:root`）
- body 字体 + bg + 过渡
- `*:focus-visible` outline 2px primary

**全局选择器**（用于 quick-action 颜色映射，避免组件内 `<style>`）：
```css
/* 在 theme.css 末尾，仍是全局 CSS（非组件级） */
.qa-pill--qa-ppt .icon-wrap { background: color-mix(in srgb, var(--color-qa-ppt) 14%, transparent); color: var(--color-qa-ppt); }
.qa-pill--qa-ppt:hover { box-shadow: 0 6px 14px -8px var(--color-qa-ppt); }
/* 其余 6 个 qa-color 同样模式 */
```

### 4.2 字体本地化

**目录**：`src/renderer/assets/fonts/`

**woff2 来源**：
- Geist：https://github.com/vercel/geist-font/releases
- Geist Mono：同上
- Instrument Serif：https://github.com/googlefonts/instrument-serif/raw/main/fonts/ttf/InstrumentSerif-Italic.ttf（需转 woff2）

**注册**（theme.css 顶部，`@font-face` 不算组件级 CSS）：
```css
@font-face {
  font-family: 'Geist';
  src: url('./assets/fonts/Geist-Variable.woff2') format('woff2');
  font-weight: 100 900;
  font-display: swap;
}
/* Geist Mono / Instrument Serif 同样 */
```

**index.html**：保留 Google Fonts link 作为兜底（用户网络好时仍能拿到字体），但 `@font-face` 已用本地 woff2，CDN 仅在本地字体加载失败时生效（同族 fallback）。

### 4.3 Sidebar 5 段重构

**新增组件**（替代 v4 的 5 个）：
- `SidebarTopAction.vue`（新建任务 + 搜索）
- `SidebarNav.vue`（7 项主导航，接收 items 数组 props）
- `SidebarAgentList.vue`（3 个 agent pill）
- `SidebarSessionItem.vue`（单个 session item）
- `SidebarThemePicker.vue`（5 主题 picker）
- `SidebarUser.vue`（底部用户行）
- `Sidebar.vue`（容器，组装 5 段）

**删除**：
- `SidebarHeader.vue`（逻辑并入 SidebarTopAction）
- `SidebarFooter.vue`（逻辑拆并入 SidebarThemePicker + SidebarUser，theme.toggle 迁移到 SidebarThemePicker）
- `SessionList.vue`（内部用 SidebarSessionItem；可保留作 list wrapper 或直接并入 Sidebar.vue）

**useTheme 迁移**：
- `useTheme` composable 不动
- `SidebarThemePicker.vue` 调 `useTheme()` + emit `change(themeId)`
- 5 主题里仅 dark/light 调 `theme.set('dark'|'light')`，其他 3 个 `console.warn` 不切换

### 4.4 HomeView 新组件树

见 §3 FR-10 子组件树。所有新组件放 `src/renderer/components/home/`。

**QuickActions 填充逻辑**：
- emit `select(template: string)`
- HomeView 接收，把 template 写入 useMessages 的 draft（新增 `useDraft` composable 或复用 PromptBox 内部 ref）
- 当前实现：直接通过 ref 设置 PromptBox 的 textarea 值 + focus

### 4.5 ChatPane 视图切换

见 §3 FR-15。ChatPane.vue 不再固定渲染 MessageList + Composer，改条件渲染。

**useViewMode** composable 派生 view 模式，避免组件内 `if` 散落。

### 4.6 AppShell 装饰层

见 §3 FR-8。aurora-bg 和 grid-bg 是两个 absolute div，不写组件级 CSS，全部用 Tailwind arbitrary value。

**`relative isolate` 关键**：根 grid 容器加 `isolate`，装饰层 `-z-10`，业务内容默认 z-auto。

### 4.7 主进程 / preload / Go agent

不动。

### 4.8 i18n

新增 key（zh 默认 + en 兜底）：
- `sidebar.new_chat`：新建任务 / New task
- `sidebar.search_placeholder`：搜索任务... / Search tasks...
- `sidebar.nav.chat` / `.scheduled` / `.skill` / `.suite` / `.mcp` / `.im` / `.dream`
- `sidebar.nav.suite_badge`：NEW
- `sidebar.current_agent_label`：当前 Agent · main
- `sidebar.recent_label`：近期任务
- `sidebar.user_plan_pro`：PRO
- `sidebar.user_credits`：4,280 credits
- `chat.header.import`：导入任务
- `chat.header.settings`：设置
- `home.greeting.morning/noon/afternoon/evening/midnight`：5 个时段
- `home.tagline`：我是 LobsterAI...
- `home.meta`：所有引擎就绪 · 4 个技能已加载 · 8 个工具可用
- `home.quick.ppt/data/teach/web/design/write/translate` + 7 个 template
- `home.prompt.placeholder`：描述你要完成的任务...
- `home.mode.plan` / `.skill`
- `home.attach.q3_data.name` / `.size` / `.visual.name` / `.size`
- `home.hint.send`：Enter 发送
- `home.hint.newline`：Shift+Enter 换行
- `home.hint.today_count`：今天 0 次任务
- `home.ctx_label`：Context
- `theme.reserved_warning`：theme '{}' is reserved, not implemented yet

### 4.9 AGENTS.md 合规性处理

**冲突点**：原型大量用内联 `style="..."` 注入颜色 / 渐变 / 位置，AGENTS.md 第 210/213/216 行明文禁止。

**处理策略**（按场景）：

| 原型写法 | 合规转换 |
|---|---|
| `style="--qa-color: #F59E0B"` | 走 `@theme` token `--color-qa-ppt` + theme.css 全局 `.qa-pill--qa-ppt` 选择器 |
| `style="background: linear-gradient(...)"` (avatar) | 走 `@theme` token + theme.css 全局 `.bg-avatar-main` 等 utility class |
| `style="background: radial-gradient(...)"` (mascot) | 用 Tailwind arbitrary value `bg-[radial-gradient(...)]`（不算 magic value，因 mascot 视觉独特、不需要 token 化） |
| `style="margin-top: 2px"` | Tailwind utility `mt-0.5` |
| `style="opacity: 0.5"` | Tailwind utility `opacity-50` |
| `style="top: 24px; right: 30px"` (float-pill) | **不做**（float-pill 是非目标） |
| `style="width: 22%"` (ctx meter) | Tailwind `w-[22%]` |
| `style="color: var(--lobster-primary)"` (label · main) | Tailwind `text-primary` |

**规则**：组件模板内严禁 `style="..."`；颜色一律走 token；位置 / 透明度 / 百分比宽高用 Tailwind arbitrary。

### 4.10 路由 / 状态管理

不引入 vue-router。`useViewMode` 单例 composable 即可。

不引入 Pinia / Vuex。沿用 v3 的 composables 模式。

---

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| 字体加载失败（断网） | `@font-face` `src` 列表里本地 woff2 在前，CDN link 在 `index.html` 兜底；都失败时 fallback 链到 system sans-serif，不阻塞渲染 |
| localStorage 不可用 | useSession 已有 fallback；useTheme 同样 |
| Sidebar 折叠（collapsed=true） | `useSidebar` 行为不变，折叠后整个 aside 消失，grid 左列 0px |
| 极小窗口（< 800px） | 不做响应式，固定布局 |
| SidePanel 触发但右栏总宽超容器 | grid `1fr` 自动收缩，min-w-0 已在 ChatPane 配 |
| Icon 缺失 | Icon 组件 fallback 占位 |
| 主题切换瞬间闪烁 | theme.css 全局 `*` 过渡 200ms |
| 新建会话后立刻发消息 | 文本进 useMessages.push，view 自动从 home 切到 chat |
| mascot 动画在低性能机器卡顿 | keyframes 用 `transform` / `opacity`（不触发 layout），`will-change: transform` |
| 5 主题 picker 点 ocean/emerald/sakura | console.warn，不实际切换，仅 picker 视觉反馈 |
| quick-action 模板含 `[主题]` 占位符 | textarea focus 后用 `setSelectionRange` 选中第一个 `[xxx]`，方便用户直接替换 |
| voice wave 点 mic 后再点 | toggle recording class，不调浏览器 `getUserMedia` |
| 用户在 Home 视图发送消息 | 文本进 messages.list，view 切到 chat；prompt-dock 内容保留还是清空？ → 清空 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| **token / 全局** | |
| `src/renderer/styles/theme.css` | 重写 `@theme` + `@layer base`，加 7 个 keyframes，加 qa-color / avatar 全局 utility 选择器 |
| `src/renderer/index.html` | Google Fonts link 替换（Geist / Geist Mono / Instrument Serif） |
| `src/renderer/assets/fonts/*.woff2` | **新增** 本地字体（Geist-Variable / GeistMono-Variable / InstrumentSerif-Italic） |
| **icons**（新增到 `src/renderer/assets/icons/`） | |
| search / calendar-clock / star / layout / link / message-circle / chevron-right / arrow-up-right / bot / paperclip / mic / at-sign / globe / sliders / bell / more / x / mode-plan / mode-skill / qa-* (7) | **新增 19+7+2 = 28 个 SVG** |
| **sidebar 重构** | |
| `src/renderer/components/sidebar/Sidebar.vue` | 重写为 5 段 layout |
| `src/renderer/components/sidebar/SidebarTopAction.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarNav.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarAgentList.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarSessionItem.vue` | **新增**（拆自 v3 SessionItem） |
| `src/renderer/components/sidebar/SidebarThemePicker.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarUser.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarHeader.vue` | **删除** |
| `src/renderer/components/sidebar/SidebarFooter.vue` | **删除** |
| `src/renderer/components/sidebar/SessionList.vue` | 保留作 list wrapper，内部用 SidebarSessionItem |
| **chat 视图调整** | |
| `src/renderer/components/chat/ChatPane.vue` | 加视图切换（HomeView / MessageList 条件渲染） |
| `src/renderer/components/chat/ChatHeader.vue` | padding / 字号 / 面包屑调整 |
| `src/renderer/components/chat/MessageList.vue` | 字号 14.5 / 颜色 token |
| `src/renderer/components/chat/MessageItem.vue` | 字号 / 颜色 token |
| `src/renderer/components/chat/StreamingText.vue` | 不动 |
| `src/renderer/components/chat/Composer.vue` | rounded-2xl + arrow-up-right + 中文 placeholder |
| **side panel** | |
| `src/renderer/components/side-panel/SidePanel.vue` | 宽度 324 → 300 |
| `src/renderer/components/side-panel/SidePanelTabs.vue` | 微调 |
| `src/renderer/components/side-panel/SidePanelContent.vue` | 微调 |
| **home 新视图**（新增 `src/renderer/components/home/`） | |
| `HomeView.vue` | **新增** 容器 + hero 区组装 |
| `Mascot.vue` | **新增** |
| `HeroGreeting.vue` | **新增** |
| `QuickActions.vue` | **新增** |
| `PromptDock.vue` | **新增** |
| `AttachCards.vue` | **新增** |
| `PromptBox.vue` | **新增** |
| `ModeChips.vue` | **新增** |
| `PromptToolbar.vue` | **新增** |
| `ModelPicker.vue` | **新增** |
| `VoiceWave.vue` | **新增** |
| `CtxMeter.vue` | **新增** |
| `PromptHint.vue` | **新增** |
| **layout / composables / services** | |
| `src/renderer/layout/AppShell.vue` | grid 248/300 + aurora-bg + grid-bg 装饰层 + relative isolate |
| `src/renderer/composables/useViewMode.ts` | **新增** home/chat 视图派生 |
| `src/renderer/composables/useSidebar.ts` | 不动 |
| `src/renderer/composables/useSidePanel.ts` | 不动 |
| `src/renderer/composables/useTheme.ts` | 加 `set(themeId)` 方法 + 5 主题支持（占位 3 个 warn） |
| `src/renderer/composables/useSession.ts` | createSession 返回新 session + 触发空消息 |
| `src/renderer/services/mock-data.ts` | sessions 扩到 7 条 + messages 补 + quick-action templates |
| `src/renderer/services/i18n.ts` | 新增 30+ key（见 §4.8） |
| **不动** | |
| `src/main/*` / `src/preload/*` / `src/darvin-agent/*` / `src/shared/*` | 全部不动 |

---

## 7. 验收标准

### 通用门槛
- [ ] `npm run lint` clean
- [ ] `npx vite build` 成功，模块数 100+
- [ ] `npm start` 启动后 DevTools console 0 errors / 0 warnings
- [ ] AGENTS.md 合规：组件内 0 个 `<style>` 块、0 个 `style="..."` 内联颜色、0 个 `bg-gray-*` / `text-red-*` 默认调色板

### 视觉一致性
- [ ] 整体视觉与 `lobsterai-home-prototype.html` 一致：blue primary + Geist + aurora + grid + 玻璃 sidebar
- [ ] aurora 3 层 radial-gradient 可见（左上蓝、右上粉、底部绿）
- [ ] grid 网格 mask 可见（中心实、边缘隐）
- [ ] sidebar `backdrop-filter: blur(20px)` 生效（DevTools Computed 看 backdrop-filter）
- [ ] Geist 字体实际加载（DevTools Network 看 woff2 200，或 Google Fonts 200）

### 布局精度
- [ ] Sidebar 宽度精确 248px（AppShell grid 左列）
- [ ] SidePanel 宽度精确 300px
- [ ] Home hero 最大宽度 760px 居中
- [ ] Composer / PromptBox 圆角 16px
- [ ] send-btn 圆角 9px
- [ ] new-chat-btn 圆角 9px

### Sidebar 功能
- [ ] 5 段顺序：top-action → nav → agent → sessions → bottom
- [ ] 主导航 7 项渲染（含「套件」badge NEW）
- [ ] 当前 Agent 3 个 pill（main + 研究员 + 前端工程师），仅 main 有绿点
- [ ] 近期任务 7 个 session-item 渲染，s-001 active
- [ ] 底部 5 主题 picker 渲染
- [ ] 用户行：avatar DA + name Darven + PRO badge + 4,280 credits + chevron

### Home 视图
- [ ] 点击「新建任务」→ 主区切换到 HomeView
- [ ] mascot 渲染 + 3 个动画播放（breathe / blink / glow / floatZ）
- [ ] greeting 时段正确（按当前系统时间）
- [ ] 7 个 quick-action pill 渲染，颜色分别为 amber/purple/green/blue/pink/pink/cyan
- [ ] hover quick-action pill：translateY(-1px) + 阴影 glow
- [ ] 点击 quick-action：textarea 填模板 + focus
- [ ] PromptBox focus-within：border + 4px shadow halo
- [ ] 2 个 attach card 渲染，点 × 移除
- [ ] mode chip 「Plan 模式」active，点 「技能 · xlsx」切换 active
- [ ] CtxMeter 渲染 70×3 + 22% 渐变条
- [ ] voice wave 点 mic → recording class + 4 根波形动画

### Chat 视图
- [ ] 点击 s-001 → MessageList 渲染 2 条（ping / Pong）
- [ ] 用户消息文字色 blue (`text-user-msg`)
- [ ] ChatHeader 面包屑 `LobsterAI / Ping 测试`
- [ ] Composer 圆角 16px + arrow-up-right icon + 中文 placeholder
- [ ] 点击 ChatHeader 模型 chip：3 个选项，选中带蓝色圆点

### 主题切换
- [ ] 点 classic-light：html 加 light class，所有面板翻转，200ms 过渡
- [ ] 点 ocean：console.warn，html class 不变，picker active 视觉跳到 ocean
- [ ] localStorage 记录偏好，重启应用保留

### 视图切换
- [ ] 新建会话 → view=home
- [ ] 在 Home 视图发送消息 → view=chat
- [ ] 切换到其他 session → view 按 messages.length 派生
- [ ] HomeView unmount 时 mascot 动画停止（不漏 timer）

---

## 8. PR 拆分计划

### PR-1: token + 背景 + sidebar 重构

**范围**：
- theme.css 完整重写（`@theme` + `@layer base` + qa-color 全局选择器 + keyframes）
- index.html 字体 link 替换（暂用 CDN，本地化放 PR-2 或独立 PR）
- AppShell.vue aurora-bg + grid-bg 装饰层 + grid 248/300
- Sidebar 7 个新组件 + 删 Header/Footer
- SidebarThemePicker + useTheme.set
- 新增 icons（search / calendar-clock / star / layout / link / message-circle / chevron-right / arrow-up-right / bot）
- ChatHeader / MessageList / MessageItem / Composer / SidePanel 视觉微调
- mock sessions 扩到 7 条
- i18n sidebar/chat key

**验收**：
- sidebar 视觉与原型一致
- aurora + grid + 玻璃 sidebar 全部可见
- 切换主题正常
- npm run lint / build / start 全通

**预估文件**：~25 个

### PR-2: HomeView 骨架

**范围**：
- HomeView.vue + ChatPane.vue 视图切换
- useViewMode composable
- Mascot.vue + HeroGreeting.vue
- PromptDock.vue + PromptBox.vue + Textarea + PromptHint.vue
- ModelPicker.vue + SendButton（拆自 PromptToolbar 或 inline）
- Composer 仍保留（Chat 视图用）
- 新增 icons：mode-plan / mode-skill
- i18n home key（greeting / tagline / meta / prompt / hint）
- keyframes 注册（hero-in / mascot-* / live-pulse）

**验收**：
- 新建会话 → HomeView 渲染
- mascot 3 动画播放
- greeting 时段正确
- 输入文本 + Enter → view 切到 chat

**预估文件**：~15 个

### PR-3: HomeView 装饰

**范围**：
- QuickActions.vue + 7 个 qa-color token + 7 个 qa-* svg icon
- AttachCards.vue + x icon
- ModeChips.vue
- PromptToolbar.vue + ToolGroup
- VoiceWave.vue + mic icon
- CtxMeter.vue
- SidebarThemePicker 完整 5 主题 picker
- 字体本地化（woff2 下载 + @font-face 注册）
- i18n quick-action + attach key
- icons：paperclip / mic / at-sign / globe / sliders / x / qa-* (7)

**验收**：
- 7 个 quick-action pill 颜色 + hover glow 正确
- mode chip 切换
- attach × 移除
- voice wave recording 状态
- ctx meter 渲染
- 字体本地化生效（断网仍能加载）

**预估文件**：~20 个

---

## 9. 视觉原型引用

完整视觉参考：`specs/features/agent-ui-shell/lobsterai-home-prototype.html`（浏览器直接打开，含 dark/light 切换、所有交互态的视觉原型）。

**原型与 v5 实现的对应关系**：

| 原型 CSS class | v5 Vue 组件 | 备注 |
|---|---|---|
| `.app::before` / `.app::after` | AppShell.vue 装饰层 | §3 FR-8 |
| `.titlebar` | （v5 不做，titlebar 由 Electron frame 管） | 非目标 |
| `.sidebar` | Sidebar.vue | §3 FR-3 |
| `.new-chat-btn` / `.search-input` | SidebarTopAction.vue | |
| `.nav-item` / `.nav-label` | SidebarNav.vue | |
| `.agent-pill` | SidebarAgentList.vue | |
| `.session-item` | SidebarSessionItem.vue | |
| `.theme-picker` / `.theme-dot` | SidebarThemePicker.vue | |
| `.user-row` | SidebarUser.vue | |
| `.content-header` | ChatHeader.vue | |
| `.messages` / `.messages-inner` | MessageList.vue | |
| `.msg` / `.label` / `.text` | MessageItem.vue | |
| `.composer` / `.composer-wrap` | Composer.vue（仅 Chat 视图） | |
| `.home` / `.home-hero` | HomeView.vue | |
| `.mascot` / `.mascot-body` / `.mascot-eyes` | Mascot.vue | |
| `.hero-greeting` / `.hero-tagline` / `.hero-meta` | HeroGreeting.vue | |
| `.quick-actions` / `.qa-pill` | QuickActions.vue | |
| `.prompt-dock` | PromptDock.vue | |
| `.prompt-attachments` / `.attach-card` | AttachCards.vue | |
| `.prompt-box` | PromptBox.vue | |
| `.prompt-mode-row` / `.mode-chip` | ModeChips.vue | |
| `.prompt-textarea` | PromptBox.vue 内 textarea | |
| `.prompt-toolbar` / `.tool-btn` | PromptToolbar.vue | |
| `.model-picker` | ModelPicker.vue | |
| `.send-btn` | PromptBox.vue 内 SendButton | |
| `.voice-wave` | VoiceWave.vue | |
| `.ctx-bar` / `.ctx-meter` | CtxMeter.vue | |
| `.prompt-hint` | PromptHint.vue | |
| `.edge-actions` / `.edge-card` | **不做**（非目标） | |
| `.toast` | **不做**（非目标） | |
| `.float-pill` | **不做**（非目标） | |

---

## 附录 A: 风险与权衡

| 风险 | 影响 | 缓解 |
|---|---|---|
| AGENTS.md 严禁组件级 CSS | quick-action 颜色映射无法用内联 style | 颜色全部走 `@theme` token + theme.css 全局 `.qa-pill--xxx` 选择器 |
| 字体本地化增加仓库体积 | woff2 ~300KB × 3 | 接受（桌面应用，离线优先）；如不可接受，PR-3 可降级为 CDN-only |
| 7 个 keyframes 加到 theme.css | theme.css 膨胀 | 接受（keyframes 是全局资源）；如担心，可拆 `theme-animations.css` 但要权衡文件数 |
| 5 主题 picker 仅 2 个生效 | UX 困惑（用户点 ocean 没反应） | console.warn + picker 视觉反馈 + 文档说明（§3 FR-3） |
| Home 视图与 Chat 视图共用 textarea 逻辑 | 切换时文本可能丢失 | useDraft composable 持久化草稿（按 sessionId） |
| mascot 动画 CPU 占用 | 低端机风扇响 | transform/opacity only + will-change + 隐藏时 unmount 自动停 animation |
| quick-action 模板的 `[主题]` 占位符替换 | 用户需手动选 | setSelectionRange 选中第一个占位符；不自动替换 |

## 附录 B: 不做的事（明确否决）

- ❌ titlebar / traffic lights（Electron frame 管）
- ❌ edge actions（右侧浮动卡片）
- ❌ toast 通知
- ❌ float-pill（mascot 周围浮动小卡）
- ❌ 5 主题真实切换（仅 dark/light）
- ❌ voice 真实录音
- ❌ attach 真实文件系统
- ❌ quick-action 真实业务
- ❌ 多 Agent 真实切换
- ❌ nav 真实路由（仅 active 视觉）
- ❌ IPC 协议 / Go agent / 主进程改动
