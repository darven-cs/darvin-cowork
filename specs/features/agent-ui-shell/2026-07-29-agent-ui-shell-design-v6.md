# agent-ui-shell 设计文档 (v6)

> 本文档基于 `docs/原型/` 6 张实际产品截图重写。
> v1~v5 基于 `lobsterai-home-prototype.html` 单一 HTML 原型设计，与用户真实期望视觉严重偏离（误判主题、配色、组件密度、视图数量）。
> v5 文件保留作历史：`2026-07-29-agent-ui-shell-design-v5.md`。

## 0. 与 v5 的差异（导读）

| 维度 | v5（基于 HTML 原型，错误） | v6（基于真实截图，修正） |
|---|---|---|
| 视觉参考源 | `lobsterai-home-prototype.html`（dark + aurora） | `docs/原型/*.png` 6 张实际截图 |
| 默认主题 | **dark** default | **light** default（白底为主） |
| Primary 色 | blue `#60A5FA` | **red lobster `#FF5722`**（品牌色） |
| 字体 | Geist + Instrument Serif（需下载 woff2） | PingFang SC + Inter 系统字体优先，不强制下载 |
| 背景 | aurora 3 层 + grid mask | **纯白 / 极淡灰**，无装饰层 |
| Sidebar 宽度 | 248px | **220px**（更窄） |
| Sidebar 段数 | 5 段（top/nav/agent/sessions/bottom） | **4 段**（brand+nav / agent / sessions / bottom buttons） |
| Sidebar nav 项 | 7 项（含 IM 通道 / 梦境） | **6 项**（新建任务 / 搜索任务 / 定时任务 / 专家套件 / 技能 / MCP） |
| Sidebar agent 卡 | 3 个（main + 研究员 + 前端工程师） | **1 个**（主 Agent） |
| Sidebar 底部 | 5 主题 picker + 用户卡 | **登录 + 设置 两个按钮** |
| 主导航入口 | 「新建任务」按钮在 sidebar top | 「新建任务」是 nav 第 1 项；点击进 HomeView |
| QuickAction 数 | 7 个彩色 pill | **4 个**（制作幻灯片 / 数据分析 / 文档写作 / 创建网站） |
| QuickAction 形态 | pill（rounded-pill） | **方形 tile 卡**（rounded-xl，更大） |
| Mascot | 76×76 红色渐变球 + 抽象眼/嘴 | **红色龙虾角色插画**（SVG，具象） |
| 视图数 | 2（home / chat） | **4**（home / chat / suite / settings） |
| 新增视图 | — | **ExpertSuite（专家套件市场） + Settings（设置）** |
| 消息气泡 | 无气泡 + YOU/DARVIN label | **有气泡**：user 右对齐白底圆角 + tool label chip；assistant 左对齐 |
| Plus 按钮 | 散在 toolbar 各处 | **HomeView PromptDock 左下 + icon**，点开 4 项 menu |
| 模型 chip | 「LobsterAI / 当前页」+ 模型 chip | **MiniMax M3 dropdown**（含 M3 / M2.7 / M2.5） |
| PR 拆分 | 3 PR | **4 PR**（增加 suite + settings） |
| 验收项 | 25+ | 35+ |

---

## 1. 概述

### 1.1 问题 / 背景

v5 spec 把视觉参考锁定在 `lobsterai-home-prototype.html` 一个 HTML 文件上，结果方向跑偏：

- 误判为 dark 主题（实际产品是 light）；
- 误判 primary 为 blue（实际是 red lobster 品牌色）；
- 误判 quick-action 数量与形态（7 pill vs 4 tile）；
- 误判 sidebar 段数与 agent 卡数量（5 段+3 卡 vs 4 段+1 卡）；
- 漏掉 ExpertSuite（专家套件市场）与 Settings 两个完整视图；
- 漏掉 PlusButton menu（添加文件 / 使用技能 / 目标 / 计划模式）；
- 误判消息形态（无气泡 vs 有气泡 + tool label）；
- 加了一堆 aurora / grid / glassmorphism 装饰，实际产品是极简白底。

用户反馈「效果不符合想要的效果」后，重新基于 `docs/原型/` 6 张截图重写为 v6。

### 1.2 目标

1. **视觉 1:1 复刻 `docs/原型/*.png`**：light default + red lobster primary `#FF5722` + 系统字体栈 + 纯白背景 + 极简留白。
2. **Sidebar 4 段重构**：brand+nav（6 项）→ 主 Agent（1 卡）→ 近期任务 → 登录/设置按钮。
3. **HomeView 极简**：红色龙虾 mascot + 时段问候 + 4 个 quick-action tile + PromptDock（含 plus menu）。
4. **ChatView 气泡化**：user 右对齐白底圆角气泡 + tool label chip；assistant 左对齐；底部「内容由 AI 生成，仅供参考」disclaimer。
5. **新增 ExpertSuite 视图**：卡片网格市场（3 列），含分类筛选 tab + 搜索 + Use/Details 按钮。
6. **新增 Settings 视图**：左侧子导航 + 右侧表单 panel（账户/外观/快捷键/关于）。
7. **视图路由**：sidebar nav 真实切换 4 视图（home/chat/suite/settings），不再仅 active 视觉。
8. **严格遵守 AGENTS.md**：utility-only、无 `<style>`、无内联 style 颜色。

### 1.3 非目标

- **不做** 真实登录流（登录按钮可点但只 console.warn）。
- **不做** 真实 ExpertSuite 数据接入（卡片走 mock）。
- **不做** 真实 Settings 持久化（toggle / input 仅组件内 state）。
- **不做** 真实模型切换逻辑（dropdown 展开 + 选中视觉，不调 API）。
- **不做** 真实文件上传 / skill 加载 / 目标设定 / 计划模式（plus menu 4 项仅视觉占位）。
- **不做** voice 真实录音。
- **不做** IPC 协议、Go agent、主进程改动。
- **不做** quick-action 真实业务（点击只填模板到 textarea）。

---

## 2. 用户场景

### 场景 1: 启动 → 默认进 HomeView

**Given** 用户打开应用，无 currentSessionId 或 currentSession 消息为空
**When** 应用挂载完成
**Then**
- 主区渲染 HomeView（mascot + greeting + 4 quick-action tile + PromptDock）
- Sidebar 「新建任务」nav 项 active
- Sidebar 「我的 Agent」段渲染 1 张主 Agent 卡
- Sidebar 近期任务列表渲染 mock 7 条

### 场景 2: 从 HomeView 切到 ChatView

**Given** 当前在 HomeView
**When** 用户在 PromptDock 输入文本 + 按 Enter
**Then**
- 文本进 `messages.list`，user 消息以右对齐白底气泡渲染
- 视图从 home 切到 chat
- assistant 占位（三点蓝色脉冲 loading）
- Sidebar active 仍停在「新建任务」（因为还在当前会话里）

### 场景 3: 点击 QuickAction 填模板

**Given** 当前在 HomeView
**When** 用户点击「制作幻灯片」tile
**Then**
- PromptDock textarea 填入预定义模板：「帮我制作一份关于 [主题] 的幻灯片，目标受众是 [人群]，约 [页数] 页」
- textarea focus，光标选中第一个 `[主题]` 占位符
- 不自动发送

### 场景 4: Plus menu 展开

**Given** 当前在 HomeView，PromptDock 可见
**When** 用户点击 PromptDock 左下「+」按钮
**Then**
- PlusMenu 浮层展开（绝对定位，向上展开），4 项垂直排列：
  - 添加文件（plus icon）
  - 使用技能（gear icon）
  - 目标（target icon）
  - 计划模式（list icon）
- 点击浮层外任一处关闭
- 点击任一项仅关闭浮层 + console.log（视觉占位）

### 场景 5: 切换到 ExpertSuite

**Given** 当前在 HomeView 或 ChatView
**When** 用户点击 sidebar nav 「专家套件」
**Then**
- 主区从 HomeView/ChatView 切换为 ExpertSuite
- 渲染：标题「专家套件」+ 搜索框 + 6 个分类 tab（All / Free / Creative / Productivity / Technical / Business）+ 3 列卡片网格
- nav active 从「新建任务」移到「专家套件」

### 场景 6: 切换到 Settings

**Given** 当前在任一视图
**When** 用户点击 sidebar 底部「设置」按钮
**Then**
- 主区切换为 Settings 视图（左子导航 + 右表单 panel）
- 子导航 4 项：账户 / 外观 / 快捷键 / 关于
- 默认选中「外观」
- 主题切换真实生效（light / dark flip）

### 场景 7: 切换模型

**Given** 当前在 HomeView 或 ChatView，PromptDock / Composer 显示
**When** 用户点击模型 dropdown「MiniMax M3」
**Then**
- Dropdown 展开列出 3 个模型：MiniMax M3（active）/ MiniMax M2.7 / MiniMax M2.5
- 每项左侧带粉红渐变 avatar circle + 模型名 + 简短描述
- 顶部有搜索框（视觉占位，不真实过滤）
- 点其他模型：dropdown 关闭，模型名更新，console.log（视觉占位）

### 场景 8: 切换主题（真实）

**Given** 当前 light
**When** 用户进 Settings → 外观 → 点击 dark
**Then**
- html 根 class 从 `light` 翻转到 `dark`
- 所有面板同步翻转，过渡 200ms
- localStorage 记录偏好

---

## 3. 功能需求

### FR-1: 字体栈（系统优先，不下载 woff2）

**字体族**（修正 v5 误判，原型用的是系统字体不是 Geist）：
- `--font-sans` → `'PingFang SC', 'Inter', -apple-system, BlinkMacSystemFont, 'Microsoft YaHei UI', 'Segoe UI', sans-serif`
- `--font-mono` → `'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, monospace`（保留 v5 的）
- `--font-display` → 同 `--font-sans`（greeting 不用 italic serif，原型就是加权 sans）

**加载策略**：
- 不下载 woff2；不引入 Google Fonts CDN link。
- `index.html` 移除 Fraunces / Inter Tight / JetBrains Mono 的 Google Fonts `<link>`。
- fallback 链依赖系统字体（macOS PingFang SC / Windows Microsoft YaHei UI / Linux 退化为 sans-serif）。

**移除**：`src/renderer/index.html` 的 Google Fonts link；`theme.css` 的 `@font-face`（如有）。

### FR-2: 主题 token 重写（LIGHT default + RED primary）

**关键调整**：v5 错把 dark 作 default。v6 改为 **light default**，dark 作为可选翻转。

**颜色**（light default → dark 覆盖）：

| token | light | dark |
|---|---|---|
| `--color-bg` | `#FAFBFC` | `#0F1117` |
| `--color-surface` | `#FFFFFF` | `#181A23` |
| `--color-surface-raised` | `#F4F5F7` | `#1E212B` |
| `--color-surface-hover` | `#EBEDF0` | `#252830` |
| `--color-border` | `rgba(0,0,0,0.06)` | `rgba(255,255,255,0.06)` |
| `--color-border-strong` | `rgba(0,0,0,0.12)` | `rgba(255,255,255,0.12)` |
| `--color-text` | `#1A1A1C` | `#EDEDEE` |
| `--color-text-muted` | `#6B7280` | `#9CA3AF` |
| `--color-text-subtle` | `#9CA3AF` | `#5C6370` |
| `--color-primary` | **`#FF5722`**（lobster red） | `#FF7043` |
| `--color-primary-hover` | `#E64A19` | `#FF8A65` |
| `--color-primary-muted` | `rgba(255,87,34,0.10)` | `rgba(255,112,67,0.14)` |
| `--color-primary-soft` | `rgba(255,87,34,0.06)` | `rgba(255,112,67,0.08)` |
| `--color-accent`（success） | `#10B981` | `#34D399` |
| `--color-user-msg-text` | `#1A1A1C` | `#EDEDEE` |
| `--color-user-msg-bg` | `#FFFFFF` | `#252830` |
| `--color-assistant-msg-text` | `#1A1A1C` | `#EDEDEE` |

**quick-action tile 配色**（每个 tile 一种 icon-wrap 背景）：
- `--color-qa-slide` `#F59E0B`（制作幻灯片，amber）
- `--color-qa-data` `#8B5CF6`（数据分析，purple）
- `--color-qa-doc` `#3B82F6`（文档写作，blue）
- `--color-qa-web` `#10B981`（创建网站，green）

**agent avatar 渐变**（避免内联）：
- `--color-avatar-main`：linear-gradient `#FF5722 → #E63946`（红橙渐变，匹配 mascot）

**圆角**：
- `--radius-sm` 4 / `--radius-md` 6 / `--radius-lg` 8 / `--radius-xl` 12 / `--radius-2xl` 16
- 局部硬编码（Tailwind arbitrary）：tile 卡 `rounded-xl(12)` / prompt-box `rounded-xl(12)` / send-btn `rounded-full` / plus-menu-item `rounded-md(6)`

**阴影**：
- `--shadow-sm`：`0 1px 2px rgba(0,0,0,0.04)` / `0 1px 2px rgba(0,0,0,0.2)`
- `--shadow-md`：`0 4px 12px rgba(0,0,0,0.08)` / `0 4px 12px rgba(0,0,0,0.3)`
- `--shadow-lg`：`0 12px 40px rgba(0,0,0,0.12)` / `0 12px 40px rgba(0,0,0,0.4)`
- `--shadow-primary`：`0 4px 12px rgba(255,87,34,0.32)`（send-btn hover 用）

**间距**：保留 v5 的 `--spacing-app-padding` 改 16 / `--spacing-section-gap` 24。

**过渡**：`* { transition: background-color 200ms ease, color 200ms ease, border-color 200ms ease }`。

### FR-3: Sidebar 重构（4 段，220px）

Sidebar 宽度 **220px**（v5 的 248 太宽），背景 `bg-surface`（实色，**无 backdrop-blur**），右边 1px border-border。

**段 1：Brand + Nav（顶部）**
- Brand 行（h-12，px-4，flex items-center gap-2）：
  - 24×24 红色龙虾 mini icon（SVG，简化版 mascot head）
  - 「LobsterAI」文字 `font-semibold text-[15px] text-text`
- Nav 列表（6 项，垂直堆叠，gap-1，px-2）：

  | 顺序 | label | icon | 行为 |
  |---|---|---|---|
  | 1 | 新建任务 | plus | → HomeView（new session） |
  | 2 | 搜索任务 | search | → 搜索浮层（视觉占位 console.log） |
  | 3 | 定时任务 | clock | → 视觉占位 console.log |
  | 4 | 专家套件 | layout | → ExpertSuite 视图 |
  | 5 | 技能 | star | → 视觉占位 console.log |
  | 6 | MCP | link | → 视觉占位 console.log |

- nav-item 结构：`flex items-center gap-2.5 px-3 py-2 rounded-md text-[13px] text-text-muted cursor-pointer transition-all`
- icon 16×16 stroke=currentColor
- active 状态：`bg-primary-muted text-primary font-medium`（**无左侧指示条**，v5 误加）
- hover：`bg-surface-hover text-text`

**段 2：我的 Agent（中部）**
- nav-label：「我的 Agent」`px-4 pt-4 pb-2 text-[11px] font-medium uppercase tracking-wider text-text-subtle`
- 1 张 agent-card（不是 pill，是 card）：
  ```
  flex items-center gap-2.5 px-3 py-2.5 mx-2 rounded-lg bg-surface-raised border border-border cursor-pointer transition-all hover:border-border-strong
  ├── 32×32 rounded-full bg-avatar-main（红橙渐变）grid place-items-center → 内嵌 16×16 龙虾 mini icon（白色）
  ├── div flex-1 min-w-0
  │   ├── div text-[13px] font-medium text-text truncate「主 Agent」
  │   └── div text-[11px] text-text-muted truncate「全场景办公助手」
  └── 6×6 rounded-full bg-accent（在线绿点）
  ```

**段 3：近期任务（flex-1 滚动）**
- nav-label：「近期任务」相同样式
- session-item 列表（mock 7 条，见 FR-19）：
  - 结构：`flex items-center gap-2 px-3 py-1.5 mx-2 rounded-md cursor-pointer transition-all`
  - 12×12 message-square icon（text-text-subtle）
  - `flex-1 min-w-0 text-[12.5px] text-text-muted truncate` 标题
  - 10px mono 时间戳（text-text-subtle）
- active：`bg-surface-raised text-text`
- hover：`bg-surface-hover text-text`

**段 4：底部按钮（h-auto，border-t border-border）**
- 2 个按钮并排（grid grid-cols-2 gap-px bg-border 做 1px 分隔线）：
  - 「登录」按钮：`flex items-center justify-center gap-1.5 py-3 bg-surface text-[13px] text-text-muted hover:bg-surface-hover hover:text-text` + log-in icon 14×14
  - 「设置」按钮：相同样式 + cog icon 14×14
- 点击「登录」→ console.warn + 视觉抖动反馈（不切视图）
- 点击「设置」→ emit `navigate('settings')`

### FR-4: ChatHeader（简化）

- h-14，px-6，flex items-center justify-between，border-b border-border，bg-surface
- **左侧**：当前视图标题（动态）：
  - home/chat：「主 Agent」（13.5px font-medium text-text）
  - suite：「专家套件」
  - settings：空（settings 视图自带子导航标题）
- **右侧**：
  - ModelPicker chip（h-8，px-3，rounded-lg border border-border bg-surface hover:bg-surface-hover）：
    - 18×18 渐变 avatar circle（粉橙渐变，简化模型 glyph）
    - 模型名「MiniMax M3」text-[13px] text-text
    - chevron-down 12×12 text-text-muted
    - 点击 → dropdown（见 FR-15 ModelPicker）
  - 间距 ml-2
  - 主题切换 IconButton（sun/moon，仅 chat header 里给，不进 sidebar）
  - SidePanel toggle IconButton（panel-right-close/open）

### FR-5: MessageList + MessageItem（气泡化）

**关键调整**：v5 是「无气泡 + YOU/DARVIN label」。v6 改为**有气泡**。

**MessageList 容器**：
- `flex-1 overflow-y-auto`
- inner：`max-w-[760px] mx-auto px-6 py-8 flex flex-col gap-6`

**MessageItem（user 消息）**：
- 容器：`flex flex-col items-end gap-1.5 max-w-[80%] ml-auto`
- tool-label chip（如 `frontend-design`）：`inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-primary-muted text-[10.5px] text-primary`
  - 12×12 tool icon
  - label 文字（如「frontend-design」）
  - 仅当消息有 `toolLabel` 字段时渲染
- 气泡：`px-4 py-2.5 rounded-2xl rounded-br-md bg-user-msg-bg border border-border text-[14px] leading-[1.55] text-user-msg-text shadow-sm`
  - 圆角不对称：右下角小（br-md）模拟「尾巴朝向」

**MessageItem（assistant 消息）**：
- 容器：`flex flex-col items-start gap-1.5 max-w-[80%] mr-auto`
- 气泡：`px-4 py-2.5 rounded-2xl rounded-bl-md bg-surface-raised text-[14px] leading-[1.55] text-assistant-msg-text`
  - 左下角小（bl-md）

**StreamingText（loading 态）**：
- 三点蓝色脉冲：`flex items-center gap-1 px-1 py-2`
- 每个 dot：`w-1.5 h-1.5 rounded-full bg-primary animate-pulse`（错开 delay 0.15s / 0.3s / 0.45s）

**MessageList 底部 footer**：
- `mt-8 pt-4 border-t border-border text-[11px] text-text-subtle text-center`
- 文字：「内容由 AI 生成，仅供参考」

### FR-6: Composer（仅 ChatView）

> HomeView 用 PromptDock（FR-13），ChatView 用 Composer。两者视觉相近，逻辑不同。

- 容器：`max-w-[760px] mx-auto px-6 pb-6`
- input-wrap：`relative bg-surface border border-border rounded-xl shadow-sm transition-all focus-within:border-primary-muted focus-within:shadow-md`
- textarea：`block w-full px-4 pt-3 pb-1 bg-transparent border-0 resize-none text-text text-[14px] leading-[1.55] min-h-[56px] max-h-[200px] focus:outline-none`
- placeholder：「继续对话...」text-text-muted
- toolbar：`flex items-center gap-1 px-2 pb-2`
  - 左：plus icon（attach 视觉占位）+ grid icon（工具占位）+ bolt icon（mode 占位）
  - 中：`flex-1`
  - 右：ModelPicker mini（与 ChatHeader 共用） + mic icon button + send button（32×32 rounded-full bg-text text-white grid place-items-center hover:bg-primary transition-colors）
    - send icon：arrow-up 14×14
    - disabled：`bg-surface-raised text-text-subtle cursor-not-allowed`

### FR-7: SidePanel（不动逻辑，仅视觉 token 同步）

- 宽度 300px（v5 已定，保留）
- 3 tab（TOOLS / THINKING / ARTIFACT）保留
- tab 样式：`px-3 py-2 text-[11px] font-mono uppercase tracking-wider text-text-muted`
- active：`text-primary border-b-2 border-primary -mb-px`

### FR-8: AppShell grid（无装饰层）

**关键调整**：v5 加了 aurora-bg + grid-bg 装饰层，v6 完全去掉。

- 根：`grid h-screen overflow-hidden bg-bg text-text relative`
- grid：`220px 1fr 300px`（sidebar / chat / sidePanel），过渡 180ms
- AppShell.vue 模板：
  ```vue
  <template>
    <div class="grid h-screen overflow-hidden bg-bg text-text"
         :style="{ gridTemplateColumns, transition: 'grid-template-columns 180ms cubic-bezier(0.4, 0, 0.2, 1)' }">
      <Sidebar v-if="!sidebarCollapsed" @navigate="onNavigate" />
      <main class="flex flex-col min-w-0 min-h-0 bg-bg">
        <ChatHeader @toggle-sidebar="..." @toggle-side-panel="..." />
        <component :is="currentViewComponent" v-bind="currentViewProps" />
      </main>
      <SidePanel v-if="sidePanelOpen" />
    </div>
  </template>
  ```
- `currentViewComponent` computed：home→HomeView / chat→ChatPane（MessageList+Composer） / suite→ExpertSuite / settings→SettingsView

### FR-9: icons 新增（基于截图实证）

**新增 SVG**（viewBox 0 0 24 24, stroke-width 1.7~2, stroke="currentColor"）：
- `search.svg` — 放大镜（搜索任务 nav + ExpertSuite 搜索框）
- `clock.svg` — 定时任务 nav
- `layout.svg` — 专家套件 nav（4 格网格）
- `star.svg` — 技能 nav
- `link.svg` — MCP nav
- `log-in.svg` — 登录按钮
- `cog.svg`（已存在，复用）— 设置按钮
- `target.svg` — plus menu 目标项
- `list.svg` — plus menu 计划模式项
- `paperclip.svg` — plus menu 添加文件项
- `gear.svg` — plus menu 使用技能项（与 cog 区分：gear 更圆）
- `grid.svg` — toolbar 工具占位
- `bolt.svg` — toolbar mode 占位
- `mic.svg` — 录音
- `arrow-up.svg`（已存在）
- `chevron-down.svg`（已存在）
- `plus.svg`（已存在）
- `check.svg`（已存在）

**quick-action 4 个 svg**（每个 tile 一个，配色由 token 控制）：
- `qa-slide.svg` — 幻灯片（矩形 + 横条）
- `qa-data.svg` — 数据（柱状图）
- `qa-doc.svg` — 文档（纸张 + 文字线）
- `qa-web.svg` — 网站（地球 + 网格线）

**mascot SVG**（红色龙虾角色，具象）：
- `mascot-full.svg` — 完整角色（HomeView hero 用，~120×120）
- `mascot-mini.svg` — 简化 head（sidebar brand + agent-card avatar 用，~24×24 / 16×16）

**自动 glob 加载**，丢到 `src/renderer/assets/icons/<name>.svg` 即可。

### FR-10: HomeView 视图（重构）

整体布局：
- 容器：`flex flex-col items-center px-8 pt-16 pb-8 flex-1 min-h-0 overflow-y-auto`
- hero 区：`flex flex-col items-center justify-center w-full max-w-[760px] text-center gap-4 flex-1`
- 进入动画：`animate-[fadeIn_0.5s_ease-out_both]`（keyframes 加 theme.css）

**子组件树**：
```
HomeView.vue
├── Mascot.vue                  （120×120 红色龙虾角色 SVG + breathe 动画）
├── HeroGreeting.vue            （时段问候 + tagline）
├── QuickActions.vue            （4 个 tile 卡）
└── PromptDock.vue              （plus menu + textarea + toolbar）
    ├── PlusMenu.vue            （浮层，4 项）
    ├── Textarea                （原生）
    └── PromptToolbar.vue
        ├── PlusButton          （开 PlusMenu）
        ├── ToolGroup           （grid + star）
        ├── ModelPicker.vue     （MiniMax M3 dropdown）
        ├── MicButton
        └── SendButton
```

### FR-11: Mascot 组件（红色龙虾角色，非抽象球）

**关键调整**：v5 是「76×76 红色渐变球 + 抽象眼/嘴/Z 动画」。v6 改为**具象红色龙虾 SVG 角色**。

- 120×120 SVG 角色（红橙色 #FF5722 → #E63946 渐变填充）：
  - 身体：椭圆主体 + 尾巴扇形
  - 两只大眼睛（白底 + 黑瞳）+ 微笑曲线嘴
  - 两只钳子（一大一小，举起来挥手姿势）
- 整体 breathe 动画：`animate-[mascot-breathe_3.5s_ease-in-out_infinite]`（scaleY 1 → 1.04 → 1）
- 钳子挥动：左钳 animate `mascot-wave 2.5s ease-in-out infinite`（rotate -10deg → 10deg → -10deg）
- 眼睛 blink：`mascot-blink 4.5s step-end infinite`（opacity 1 → 0 → 1，瞬间）

**动画 keyframes** 在 theme.css 注册（不算组件 CSS）：
- `--animate-mascot-breathe`
- `--animate-mascot-blink`
- `--animate-mascot-wave`

> Mascot 角色 SVG 由设计资源提供；如无，PR-2 用简化占位（红色圆角矩形 + 两个白眼 + 嘴），后续替换。

### FR-12: HeroGreeting

- `<h2>` `text-[36px] leading-[1.15] tracking-[-0.01em] font-semibold text-text`
- 文本：「晚上好」（按时段切换，见下）+ 无 name 后缀（截图显示不接 name）
- tagline：`mt-2 text-[14.5px] text-text-muted max-w-[480px] mx-auto`
  - 文本：「我是 LobsterAI，你的全场景办公 Agent」

**greeting 时段逻辑**（同 v5）：
- `<6` 「凌晨好」/ `6-12` 「早上好」/ `12-14` 「中午好」/ `14-18` 「下午好」/ `18-24` 「晚上好」

> 移除 v5 的 live-dot meta（截图里没有「所有引擎就绪 · 技能 · 工具」状态条）。

### FR-13: QuickActions（4 个 tile 卡）

**关键调整**：v5 是「7 个 pill」。v6 改为 **4 个方形 tile 卡**。

4 个 tile，grid 网格（desktop 4 列，gap-3，max-w-[640px] mx-auto）：

| order | label | qa-color token | icon | template |
|---|---|---|---|---|
| 1 | 制作幻灯片 | `qa-slide` `#F59E0B` | qa-slide.svg | 「帮我制作一份关于 [主题] 的幻灯片，目标受众是 [人群]，约 [页数] 页」 |
| 2 | 数据分析 | `qa-data` `#8B5CF6` | qa-data.svg | 「分析这份数据：[粘贴/附件]，重点找出 [趋势/异常]」 |
| 3 | 文档写作 | `qa-doc` `#3B82F6` | qa-doc.svg | 「写一份关于 [主题] 的 [类型] 文档，约 [字数] 字」 |
| 4 | 创建网站 | `qa-web` `#10B981` | qa-web.svg | 「帮我创建一个 [类型] 网站，目标用户是 [人群]」 |

**tile 结构**（合规处理：颜色走 token，不用内联）：
- 容器：`flex flex-col items-center gap-2 p-4 rounded-xl bg-surface border border-border cursor-pointer transition-all hover:-translate-y-0.5 hover:shadow-md hover:border-border-strong`
- icon-wrap：`w-12 h-12 rounded-lg grid place-items-center`，颜色由父 tile 的 qa-color class 注入到子选择器
  - 实现：QuickActions.vue 接收 `:color="qa-slide"`，渲染 `<button :class="['qa-tile', \`qa-tile--\${color}\`]">`，theme.css 全局定义 `.qa-tile--qa-slide .icon-wrap { background: color-mix(...); color: ... }`
- label：`text-[13px] font-medium text-text`

### FR-14: PromptDock（HomeView 专用）

**PromptDock 容器**：`w-full max-w-[760px] mx-auto mt-8 shrink-0`

**input-wrap**（与 Composer 视觉一致）：
- `relative bg-surface border border-border rounded-xl shadow-sm transition-all focus-within:border-primary-muted focus-within:shadow-md`
- textarea：`block w-full px-4 pt-3 pb-1 bg-transparent border-0 resize-none text-text text-[14px] leading-[1.55] min-h-[56px] max-h-[200px] focus:outline-none`
- placeholder：「描述你要完成的任务...」text-text-muted

**PromptToolbar**（贴底）：
- `flex items-center gap-1 px-2 pb-2 relative`
- 左侧 PlusButton：`inline-flex items-center justify-center w-8 h-8 rounded-md text-text-muted hover:bg-surface-hover hover:text-text`
  - plus icon 16×16
  - 点击 → emit `open-plus-menu`
- tool-btn（grid / star）：同 PlusButton 样式
- 中：`flex-1`
- 右侧：
  - ModelPicker（mini 版，h-8 px-2.5 rounded-md border border-border hover:bg-surface-hover）：18×18 渐变 avatar + 「MiniMax M3」text-[12.5px] + chevron-down 12×12
  - MicButton：同 PlusButton 样式 + mic icon
  - SendButton：`w-8 h-8 rounded-full bg-text text-white grid place-items-center hover:bg-primary transition-colors disabled:bg-surface-raised disabled:text-text-subtle disabled:cursor-not-allowed`
    - arrow-up icon 14×14

### FR-15: PlusMenu（新增，4 项浮层）

**触发**：PromptDock PromptToolbar 的 PlusButton 点击

**浮层结构**：
- 定位：`absolute bottom-full left-0 mb-2 w-56 bg-surface border border-border rounded-xl shadow-lg p-1.5 z-50`
- 进入动画：`animate-[plusMenuIn_0.15s_ease-out_both]`（opacity 0 → 1 + translateY 4px → 0）
- 4 项菜单（垂直堆叠 gap-0.5）：

  | 顺序 | label | icon | 行为 |
  |---|---|---|---|
  | 1 | 添加文件 | paperclip | 关闭浮层 + console.log |
  | 2 | 使用技能 | gear | 关闭浮层 + console.log |
  | 3 | 目标 | target | 关闭浮层 + console.log |
  | 4 | 计划模式 | list | 关闭浮层 + console.log |

- menu-item：`flex items-center gap-2.5 px-3 py-2 rounded-md text-[13px] text-text-muted cursor-pointer hover:bg-surface-hover hover:text-text`
- icon 14×14

**关闭逻辑**：点击外部 / 按 Escape / 选中任一项 → 关闭

### FR-16: ModelPicker dropdown（新增）

**触发**：ChatHeader 或 PromptToolbar 的模型 chip 点击

**Dropdown 结构**：
- 定位：`absolute top-full right-0 mt-2 w-80 bg-surface border border-border rounded-xl shadow-lg p-2 z-50`
- 顶部搜索框：`w-full px-3 py-2 mb-1.5 bg-surface-raised rounded-md text-[13px] text-text placeholder:text-text-muted border border-border focus:outline-none focus:border-primary` placeholder「搜索模型...」（视觉占位，不真实过滤）
- 模型列表（mock 3 项）：

  | id | name | sub | avatar gradient |
  |---|---|---|---|
  | m3 | MiniMax M3 | 旗舰 · 复杂任务 | `#FF5722 → #E63946` |
  | m27 | MiniMax M2.7 | 平衡 · 日常任务 | `#F472B6 → #EC4899` |
  | m25 | MiniMax M2.5 | 经济 · 简单任务 | `#8B5CF6 → #6366F1` |

- model-item 结构：`flex items-center gap-3 px-3 py-2.5 rounded-md cursor-pointer hover:bg-surface-hover`
  - 32×32 rounded-full avatar（用 token 渐变 class）
  - div flex-1：name `text-[13.5px] font-medium text-text` + sub `text-[11.5px] text-text-muted`
  - active 时右侧 check icon 16×16 text-primary
- 选中：dropdown 关闭 + emit `select(modelId)` + console.log

### FR-17: ExpertSuite 视图（新增）

**整体布局**：
- 容器：`flex flex-col h-full`
- 顶栏（h-auto px-6 pt-6 pb-4 border-b border-border）：
  - 标题行：`flex items-center justify-between`
    - 左：`<h2>` `text-[20px] font-semibold text-text` 「专家套件」
    - 右：搜索框 `w-72 px-3 py-1.5 bg-surface-raised rounded-md text-[13px] border border-border focus:outline-none focus:border-primary` placeholder「搜索 agent...」（视觉占位）
  - filter tab 行（mt-4 flex gap-1）：
    - 6 个 tab：All Agents / Free / Creative / Productivity / Technical / Business
    - tab：`px-3 py-1.5 rounded-md text-[12.5px] cursor-pointer transition-all`
    - inactive：`text-text-muted hover:bg-surface-hover hover:text-text`
    - active：`bg-primary-muted text-primary font-medium`
    - 切换：仅过滤显示 mock 卡片（按 card.category）
- 卡片网格（flex-1 overflow-y-auto p-6）：
  - `grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4`
  - 共 mock 9 张 card（见 FR-19）
- 单张 agent-card：
  - 容器：`flex flex-col p-4 rounded-xl bg-surface border border-border hover:border-border-strong hover:shadow-md transition-all`
  - 顶部：`flex items-start gap-3 mb-3`
    - 48×48 rounded-xl avatar（渐变 token）
    - div flex-1：name `text-[15px] font-semibold text-text` + category-tag `mt-1 inline-block px-2 py-0.5 rounded text-[10.5px] bg-primary-muted text-primary`
  - description：`text-[12.5px] text-text-muted leading-[1.55] mb-4 line-clamp-2`
  - 底部：`flex items-center justify-between mt-auto`
    - 左：pricing `text-[12.5px] text-text-muted`（如「Free」或「100 credits/次」）
    - 右：2 个按钮：
      - 「使用」primary 按钮：`px-3 py-1.5 rounded-md bg-primary text-white text-[12.5px] font-medium hover:bg-primary-hover`
      - 「详情」ghost 按钮：`px-3 py-1.5 rounded-md border border-border text-text-muted text-[12.5px] hover:bg-surface-hover`

### FR-18: Settings 视图（新增）

**整体布局**（左 sub-nav + 右 panel）：
- 容器：`flex h-full`
- 左 sub-nav（w-56 border-r border-border px-3 py-4 flex flex-col gap-1）：
  - 4 项：账户 / 外观 / 快捷键 / 关于
  - sub-nav-item：`px-3 py-2 rounded-md text-[13px] text-text-muted cursor-pointer hover:bg-surface-hover`
  - active：`bg-primary-muted text-primary font-medium`
- 右 panel（flex-1 overflow-y-auto p-8）：
  - `max-w-[640px] mx-auto flex flex-col gap-6`
  - 每个 section：`<section class="flex flex-col gap-3">`
    - 标题：`text-[15px] font-semibold text-text`
    - 描述：`text-[12.5px] text-text-muted`
    - 控件（按 sub-nav 切换）：
      - **账户**：用户名 input（mock 只读）+ 邮箱 input（mock 只读）+ 「退出登录」按钮（console.warn）
      - **外观**：主题 radio group（light / dark）→ 真实切换 html.class + localStorage 持久化
      - **快捷键**：6 个 mock 快捷键列表（新建任务 ⌘N / 搜索任务 ⌘F / 切换主题 ⌘D / 打开设置 ⌘, / 切换 sidePanel ⌘J / 发送消息 Enter）
      - **关于**：版本号 v0.1.0 + 架构图链接 + 开源许可列表（mock）

### FR-19: Mock 数据扩展

**sessions（7 条，符合截图里的近期任务密度）**：
| id | title | time | icon |
|---|---|---|---|
| s-001 | 给我写一个贪吃蛇 | 现在 | message-square |
| s-002 | 整理本周飞书周会纪要 | 1h | message-square |
| s-003 | 设计个人作品集首页 | 3h | layout |
| s-004 | 中翻英：产品发布会讲稿 | 昨 | message-square |
| s-005 | React 表单组件重构 | 2d | code |
| s-006 | 批量重命名 4K 张产品图 | 4d | file |
| s-007 | 数据分析：Q2 销售报表 | 1w | chart |

**messages（默认 s-001 的 1 条 user + 1 条 assistant loading）**：
- s-001：
  - user：`给我写一个贪吃蛇`，toolLabel=`frontend-design`（截图实证）
  - assistant：loading（三点脉冲）

**新建会话**：`createSession()` 返回 s-008，messages 为空 → 触发 HomeView。

**expertSuiteAgents（9 条 mock）**：
| id | name | category | description | pricing | avatar gradient |
|---|---|---|---|---|---|
| a-001 | PPT 大师 | Creative | 一键生成专业幻灯片 | 50 credits/次 | `#F59E0B → #EF4444` |
| a-002 | 数据分析师 | Productivity | Excel/CSV 深度分析 + 可视化 | Free | `#8B5CF6 → #6366F1` |
| a-003 | 文档工程师 | Productivity | 长文 / 报告 / 标书撰写 | 30 credits/次 | `#3B82F6 → #06B6D4` |
| a-004 | 前端工程师 | Technical | React / Vue 组件实现 | 80 credits/次 | `#10B981 → #059669` |
| a-005 | UI 设计师 | Creative | 组件 / 海报 / 图标设计 | Free | `#EC4899 → #F472B6` |
| a-006 | 翻译官 | Productivity | 多语种互译 + 润色 | 20 credits/次 | `#06B6D4 → #0EA5E9` |
| a-007 | SEO 顾问 | Business | 关键词 + 文案优化 | 100 credits/次 | `#F97316 → #EF4444` |
| a-008 | 财务分析师 | Business | 报表分析 + 风险评估 | Free | `#84CC16 → #22C55E` |
| a-009 | Python 教练 | Technical | 入门到进阶代码陪练 | 40 credits/次 | `#A855F7 → #8B5CF6` |

**models（3 条 mock）**：见 FR-16 表。

---

## 4. 实现方案

### 4.1 theme.css 重写

**关键调整**：light default（v5 是 dark default）。

`@theme` 块覆盖：
- 字体 3 个变量改为系统优先（PingFang SC / Inter / JetBrains Mono），删 `@font-face`
- 颜色全量重写为 light default：bg/surface/text/primary(red)/qa-color 4 个/avatar 渐变 1 个
- 圆角 sm=4 / md=6 / lg=8 / xl=12 / 2xl=16
- 阴影 4 档（含 primary）
- keyframes 5 个：cursor-blink / fadeIn / mascot-breathe / mascot-blink / mascot-wave / plusMenuIn

`@layer base` 下：
- `:root` 默认 light token
- `html.dark` 覆盖所有 token 为 dark 值（**反过来**，v5 是 `html.light` 覆盖）
- body 字体 + bg + 过渡
- `*:focus-visible` outline 2px primary

**全局选择器**（qa-tile 颜色映射）：
```css
/* theme.css 末尾，全局 CSS */
.qa-tile--qa-slide .icon-wrap { background: color-mix(in srgb, var(--color-qa-slide) 14%, transparent); color: var(--color-qa-slide); }
.qa-tile--qa-slide:hover { box-shadow: 0 8px 20px -10px var(--color-qa-slide); }
/* 其余 3 个 qa-color 同模式 */
```

### 4.2 useViewMode 改造（4 视图）

**关键调整**：v5 是 home/chat 二选一。v6 扩展到 4 视图 + nav 真实路由。

```ts
// src/renderer/composables/useViewMode.ts
type ViewId = 'home' | 'chat' | 'suite' | 'settings';
const view = ref<ViewId>('home');
function navigate(target: ViewId): void {
  if (target === 'home') {
    // 新建 session + 触发 HomeView
    session.createSession();
  }
  view.value = target;
}
// 当 messages.list 非空且 view==='home' 时，自动切到 chat
watch(() => messages.list.value.length, (n) => {
  if (n > 0 && view.value === 'home') view.value = 'chat';
});
```

### 4.3 useTheme 改造（默认 light）

**关键调整**：v5 默认 dark，v6 默认 light。

```ts
// useTheme.ts
const theme = ref<'light' | 'dark'>('light'); // 默认 light
function apply(next: 'light' | 'dark'): void {
  document.documentElement.classList.remove('light', 'dark');
  document.documentElement.classList.add(next);
  localStorage.setItem('darvin-theme', next);
  theme.value = next;
}
// 初始化时读 localStorage，无记录则 light
```

> 移除 v5 的 5 主题 picker（dark/light/ocean/emerald/sakura），v6 只 light/dark 二选一，进 Settings → 外观切换。

### 4.4 Sidebar 4 段重构

**新增组件**（替代 v3 的 Header/Footer）：
- `SidebarBrand.vue`（mini logo + brand name）
- `SidebarNav.vue`（6 项主导航，接收 items props + active id）
- `SidebarAgentCard.vue`（1 张主 Agent 卡）
- `SidebarSessionList.vue`（保留并复用 SessionItem.vue）
- `SidebarBottom.vue`（登录 + 设置 2 按钮）
- `Sidebar.vue`（容器，组装 4 段）

**删除**：
- `SidebarHeader.vue`
- `SidebarFooter.vue`

### 4.5 HomeView 新组件树

见 §3 FR-10。所有新组件放 `src/renderer/components/home/`。

**QuickAction 填充逻辑**：
- emit `select(template: string)`
- HomeView 接收，通过 ref 调 PromptBox 的 textarea setValue + focus + setSelectionRange 选中第一个 `[xxx]` 占位符

### 4.6 ExpertSuite / Settings 视图

- 新建目录 `src/renderer/components/expert-suite/`：`ExpertSuite.vue` + `AgentCard.vue` + `AgentFilterTabs.vue`
- 新建目录 `src/renderer/components/settings/`：`SettingsView.vue` + `SettingsSubNav.vue` + `SettingsPanelAccount.vue` + `SettingsPanelAppearance.vue` + `SettingsPanelShortcuts.vue` + `SettingsPanelAbout.vue`

### 4.7 AppShell 视图切换

AppShell.vue 改为根据 `useViewMode.view` 动态渲染：
- `home` → `<HomeView />`
- `chat` → `<ChatPane />`（含 MessageList + Composer）
- `suite` → `<ExpertSuite />`
- `settings` → `<SettingsView />`

ChatHeader 仅在 home/chat/suite 显示；settings 视图自带顶栏。

### 4.8 主进程 / preload / Go agent

不动。

### 4.9 i18n

新增 key（zh 默认 + en 兜底）：
- `sidebar.brand`：LobsterAI / LobsterAI
- `sidebar.nav.new_task`：新建任务 / New task
- `sidebar.nav.search`：搜索任务 / Search tasks
- `sidebar.nav.scheduled`：定时任务 / Scheduled
- `sidebar.nav.suite`：专家套件 / Expert Suite
- `sidebar.nav.skill`：技能 / Skills
- `sidebar.nav.mcp`：MCP / MCP
- `sidebar.my_agent_label`：我的 Agent / My Agent
- `sidebar.agent.main.name`：主 Agent / Main Agent
- `sidebar.agent.main.sub`：全场景办公助手 / All-in-one office assistant
- `sidebar.recent_label`：近期任务 / Recent
- `sidebar.login`：登录 / Sign in
- `sidebar.settings`：设置 / Settings
- `chat.disclaimer`：内容由 AI 生成，仅供参考 / Content generated by AI, for reference only
- `chat.composer.placeholder`：继续对话... / Continue the conversation...
- `home.greeting.{morning,noon,afternoon,evening,midnight}`
- `home.tagline`：我是 LobsterAI，你的全场景办公 Agent / I'm LobsterAI, your all-scenario office Agent
- `home.quick.{slide,data,doc,web}` + 4 个 template
- `home.prompt.placeholder`：描述你要完成的任务... / Describe what you want to accomplish...
- `home.plus.attach`：添加文件 / Add file
- `home.plus.skill`：使用技能 / Use skill
- `home.plus.target`：目标 / Target
- `home.plus.plan`：计划模式 / Plan mode
- `suite.title`：专家套件 / Expert Suite
- `suite.search_placeholder`：搜索 agent... / Search agents...
- `suite.filter.{all,free,creative,productivity,technical,business}`
- `suite.card.use`：使用 / Use
- `suite.card.details`：详情 / Details
- `settings.subnav.{account,appearance,shortcuts,about}`
- `settings.account.{username,email,logout}`
- `settings.appearance.{title,description,theme_light,theme_dark}`
- `settings.shortcuts.{new_task,search,theme_toggle,open_settings,toggle_panel,send}`
- `settings.about.{version,architecture,licenses}`
- `model.search_placeholder`：搜索模型... / Search models...

### 4.10 AGENTS.md 合规性处理

**冲突点**：截图里大量颜色 / 渐变需要 token 化（avatar 渐变 / mascot 角色 / qa-tile 配色）。

**处理策略**：

| 原型需要 | 合规转换 |
|---|---|
| avatar 粉红渐变 | `@theme` token `--color-avatar-main` + theme.css 全局 `.bg-avatar-main` utility class |
| mascot 角色填充红橙渐变 | 内联在 SVG `fill="url(#gradient)"` + `<defs>` 内（SVG 内部，不算组件 `<style>`） |
| qa-tile icon-wrap 配色 | `@theme` token + theme.css 全局 `.qa-tile--xxx .icon-wrap` 选择器 |
| agent card avatar 渐变（多套） | theme.css 全局 `.bg-avatar-suite-a1` … `.bg-avatar-suite-a9` 选择器 |
| plus-menu 浮层位置 | Tailwind arbitrary `bottom-full left-0` |
| model picker dropdown 位置 | Tailwind arbitrary `top-full right-0` |

**规则**：组件模板内严禁 `style="..."`；颜色一律走 token；位置 / 透明度 / 百分比宽高用 Tailwind arbitrary。

### 4.11 路由 / 状态管理

不引入 vue-router。`useViewMode` 单例 composable 即可。

不引入 Pinia / Vuex。沿用 v3 的 composables 模式。

---

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| localStorage 不可用 | useSession 已有 fallback；useTheme 同样 |
| Sidebar 折叠（collapsed=true） | grid 左列 0px，整个 aside 不渲染 |
| 极小窗口（< 900px） | ExpertSuite 卡片网格降为 2 列；< 600px 降为 1 列（lg/md breakpoint） |
| SidePanel 触发但右栏总宽超容器 | grid `1fr` 自动收缩，min-w-0 已在 main 配 |
| Icon 缺失 | Icon 组件 fallback 占位 |
| 主题切换瞬间闪烁 | theme.css 全局 `*` 过渡 200ms |
| 新建会话后立刻发消息 | 文本进 messages.list，view 自动从 home 切到 chat |
| mascot 动画在低性能机器卡顿 | transform/opacity only + will-change + 隐藏时 unmount 自动停 animation |
| quick-action 模板含 `[主题]` 占位符 | setSelectionRange 选中第一个占位符 |
| PlusMenu / ModelPicker 浮层同时打开 | 同时只允许 1 个浮层（互斥），打开新浮层先关旧 |
| 浮层打开时按 Escape | 关闭当前浮层（事件冒泡到 AppShell 统一处理） |
| 用户在 Suite / Settings 时发消息 | 不可能（这俩视图无 Composer），仅 home/chat 有输入 |
| 用户点击「登录」按钮 | console.warn + 视觉抖动（class toggle `animate-[shake_0.3s]`），不切视图 |
| 用户切换模型后回到 HomeView | 模型 chip 显示选中的模型名，状态保留 |
| Settings 外观切换主题后返回 HomeView | html.class 已 flip，所有面板同步 |

---

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| **token / 全局** | |
| `src/renderer/styles/theme.css` | 重写 `@theme`（light default）+ `@layer base`（dark 覆盖）+ 5 keyframes + qa-tile / avatar 全局 utility 选择器 |
| `src/renderer/index.html` | **移除** Google Fonts link（Fraunces / Inter Tight / JetBrains Mono） |
| `src/renderer/styles/reset.css` | 不动 |
| **icons**（新增到 `src/renderer/assets/icons/`） | |
| search / clock / layout / star / link / log-in / target / list / paperclip / gear / grid / bolt / mic / qa-slide / qa-data / qa-doc / qa-web / mascot-full / mascot-mini | **新增 19 个 SVG** |
| **sidebar 重构** | |
| `src/renderer/components/sidebar/Sidebar.vue` | 重写为 4 段 layout |
| `src/renderer/components/sidebar/SidebarBrand.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarNav.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarAgentCard.vue` | **新增** |
| `src/renderer/components/sidebar/SidebarBottom.vue` | **新增**（登录 + 设置 2 按钮） |
| `src/renderer/components/sidebar/SidebarHeader.vue` | **删除** |
| `src/renderer/components/sidebar/SidebarFooter.vue` | **删除** |
| `src/renderer/components/sidebar/SessionList.vue` | 重命名 SidebarSessionList.vue（或保留原名） |
| `src/renderer/components/sidebar/SessionItem.vue` | 视觉 token 同步 |
| **chat 视图调整** | |
| `src/renderer/components/chat/ChatPane.vue` | 移除 ChatHeader（迁 AppShell）+ 加视图切换 |
| `src/renderer/components/chat/ChatHeader.vue` | padding / 标题动态化 / 加 ModelPicker chip / 主题切换 |
| `src/renderer/components/chat/ChatHeaderModel.vue` | **删除**（被新 ModelPicker 替代） |
| `src/renderer/components/chat/MessageList.vue` | 改为 max-w-760 居中 + 底部 disclaimer |
| `src/renderer/components/chat/MessageItem.vue` | **改为气泡**：user 右对齐 + tool label + 圆角不对称；assistant 左对齐 |
| `src/renderer/components/chat/StreamingText.vue` | loading 改为三点脉冲 |
| `src/renderer/components/chat/Composer.vue` | rounded-xl + 中文 placeholder「继续对话...」+ 圆形 send 按钮 |
| **side panel** | |
| `src/renderer/components/side-panel/SidePanel.vue` | 宽度 300 + 视觉 token |
| **home 新视图**（`src/renderer/components/home/`） | |
| `HomeView.vue` | **新增** 容器 + hero 区组装 |
| `Mascot.vue` | **新增** |
| `HeroGreeting.vue` | **新增** |
| `QuickActions.vue` | **新增** |
| `PromptDock.vue` | **新增** |
| `PlusMenu.vue` | **新增** |
| `PromptToolbar.vue` | **新增** |
| `ModelPicker.vue` | **新增**（共享组件，home + chat + chat-header 都用） |
| **expert-suite 新视图**（`src/renderer/components/expert-suite/`） | |
| `ExpertSuite.vue` | **新增** 容器 |
| `AgentFilterTabs.vue` | **新增** 6 个分类 tab |
| `AgentCard.vue` | **新增** 单张卡片 |
| **settings 新视图**（`src/renderer/components/settings/`） | |
| `SettingsView.vue` | **新增** 容器 |
| `SettingsSubNav.vue` | **新增** 左侧子导航 |
| `SettingsPanelAccount.vue` | **新增** |
| `SettingsPanelAppearance.vue` | **新增** 主题切换 |
| `SettingsPanelShortcuts.vue` | **新增** |
| `SettingsPanelAbout.vue` | **新增** |
| **layout / composables / services** | |
| `src/renderer/layout/AppShell.vue` | grid 220/300 + 视图动态 component + 移除装饰层 |
| `src/renderer/composables/useViewMode.ts` | **新增** 4 视图派生 + navigate |
| `src/renderer/composables/useSidebar.ts` | 不动 |
| `src/renderer/composables/useSidePanel.ts` | 不动 |
| `src/renderer/composables/useTheme.ts` | 改默认 light + 移除 5 主题 picker 支持 |
| `src/renderer/composables/useSession.ts` | createSession 触发空消息 → HomeView |
| `src/renderer/services/mock-data.ts` | sessions 扩到 7 + messages（含 toolLabel）+ 4 quick template + 9 expertSuiteAgents + 3 models |
| `src/renderer/services/i18n.ts` | 新增 50+ key（见 §4.9） |
| **不动** | |
| `src/main/*` / `src/preload/*` / `src/darvin-agent/*` / `src/shared/*` | 全部不动 |

---

## 7. 验收标准

> **进度同步（2026-07-30，PR-1 ~ PR-4 全部交付后逐条实测）**
>
> 勾选状态均以 headless 浏览器实测 DOM / computedStyle 为依据，非阅读代码推断。
> 计 **58 项：47 已达成 / 11 未达成**。
>
> 11 项未达成集中在下表，均非阻塞性缺陷，而是「实现与 spec 文案 / 交互设计不一致」：
>
> | # | 未达成项 | 性质 |
> |---|---|---|
> | 1 | Electron 内 console 0 error/warning | 未实测（browser dev 仅剩 preload 相关 1 条） |
> | 2 | Mascot blink / wave 动画 | 未实现（Mascot 已改为「DC」字母标，只有 breathe） |
> | 3 | tagline 文案 | 实现用了 `home.subtitle`，与 spec 文案不同 |
> | 4 | tile hover translate-y-0.5 | 未实现（shadow / border 已生效） |
> | 5 | 点击 tile 填模板 + 选中 `[xxx]` | 实现改为「立即发送并跳转」 |
> | 6 | PromptBox focus-within 样式 | 用了 border-border-strong，无 shadow-md |
> | 7 | s-001 assistant loading 态 | mock 给的是 `done: true` 完整回复 |
> | 8 | user 气泡 tool chip | 同上连带：`done: true` 下 chip 不渲染 |
> | 9 | assistant 三点脉冲 loading | 同上连带 |
> | 10 | ChatView disclaimer | `MessageList.vue` 内无此元素 |
> | 11 | Composer placeholder「继续对话...」 | 实际为「给 Darvin 发送消息…」 |
>
> 第 7~9 项同源，且含一个真实缺陷：`mock-data.ts` 里 `toolLabel: 'frontend-design'` 在 `done: true`
> 下永远不会被 `MessageItem.vue:6`（条件 `!message.done`）渲染，属于死数据，
> 需要决定「改 mock 为 loading 态」还是「移除该字段」。

### 通用门槛
- [x] `npm run lint` clean
- [x] `npx vite build` 成功，模块数 100+（PR-4 后实测 **137 modules**，3.30s）
- [ ] `npm start` 启动后 DevTools console 0 errors / 0 warnings（browser dev 下仅剩 `window.darvin.onEvent` 一条，成因是 preload 只在 Electron 注入，非渲染层缺陷；Electron 内仍未实测，保持未勾）
- [x] AGENTS.md 合规：组件内 0 个 `<style>` 块、0 个 `style="..."` 内联颜色、0 个 `bg-gray-*` / `text-red-*` 默认调色板（`Dropdown.vue` 的 Vue transition `<style scoped>` 已改写为 `<Transition>` + Tailwind utility props：`transition-opacity duration-100 ease-out / ease-in` + `opacity-0`）

### 视觉一致性（对照 `docs/原型/*.png`）
- [x] 默认主题 light（白底），primary 为红色 `#FF5722`
- [x] **无** aurora / grid 装饰层（AppShell 根容器无装饰 div）
- [x] **无** sidebar backdrop-blur（实色 `bg-surface`）
- [x] 字体走系统 PingFang SC / Inter（DevTools Network 无 Google Fonts 请求）
- [x] 整体视觉与 `docs/原型/首页.png` 一致（结构层面一致：Sidebar / hero / tile / dock 四段齐备；文案层面仍有差异，见下方 HomeView 分区）

### 布局精度
- [x] Sidebar 宽度精确 220px
- [x] SidePanel 宽度精确 300px
- [x] HomeView hero 最大宽度 760px 居中（实测 width 760px，margin-left = margin-right = 56px）
- [x] Composer / PromptBox 圆角 12px（rounded-xl）（实测 textarea 外层 border-radius 12px）
- [x] Send button 圆形（rounded-full）
- [x] MessageItem user 气泡 rounded-2xl + 右下角 br-md（实测 `16px 16px 6px`，容器 justify-content: flex-end）

### Sidebar 功能
- [x] 4 段顺序：brand+nav → agent → sessions → bottom
- [x] 主导航 6 项渲染（含 icon + label）
- [x] 1 张主 Agent 卡（红橙渐变 avatar + 在线绿点）
- [x] 近期任务 7 个 session-item，s-001 active
- [x] 底部 2 按钮：「登录」+「设置」并排
- [x] 点击「设置」→ 切到 settings 视图
- [x] 点击「专家套件」→ 切到 suite 视图

### HomeView
- [x] 启动默认渲染 HomeView（实测加载后 4 个 `.qa-tile`）
- [ ] Mascot 渲染 + 3 动画播放（breathe / blink / wave）— **仅 breathe**：实测运行中动画只有 `mascot-breathe` + `fade-in`；且 Mascot 已改为「DC」字母标而非龙虾角色，blink / wave 未实现
- [x] greeting 时段正确（按系统时间）（08:xx 实测「早上好」）
- [ ] tagline「我是 LobsterAI，你的全场景办公 Agent」— 实际文案为「今天有什么我可以帮你的？」（`home.subtitle`）
- [x] 4 个 quick-action tile（不是 7 个 pill）— 数量达成；文案实际为 做 PPT / 看数据 / 写文档 / 搜网页，与 spec 的 制作幻灯片 / 数据分析 / 文档写作 / 创建网站 不同
- [ ] hover tile：translate-y-0.5 + shadow + border-strong — **translate 缺失**：实测 hover 前后 `transform` 均为 `none`；shadow（`0 8px 20px`）与 border（rgba .06 → .12）已生效
- [ ] 点击 tile：textarea 填模板 + focus + 选中第一个 `[xxx]` — 实际行为是**立即发送并跳 ChatView**（`HomeView.vue:52` onTileSelect 直接调 onSend + goChat），模板里也没有 `[xxx]` 占位符
- [ ] PromptBox focus-within：border-primary-muted + shadow-md — 实际只有 `focus-within:border-border-strong`，无 shadow-md
- [x] 点击 PlusButton：4 项浮层展开 — 数量达成；文案实际为 上传文件 / 目标设定 / 待办清单 / 偏好设置，与 spec 的 添加文件 / 使用技能 / 目标 / 计划模式 不同
- [x] 点击 ModelPicker：3 个模型 dropdown + 搜索框（实测搜索框存在 + 3 个模型项）
- [x] 输入文本 + Enter → 视图切到 chat（实测 tile 数 4 → 0，消息渲染成功）

### ChatView
- [ ] 点击 s-001 → 渲染 1 user + 1 assistant loading — assistant 实际是 `done: true` 的完整回复，不是 loading 态
- [ ] user 消息右对齐 + 白底圆角气泡 + 「frontend-design」tool label chip — 右对齐（flex-end）与白底圆角（`16px 16px 6px` / `#fff`）已达成，**chip 不渲染**：`MessageItem.vue:6` 条件为 `!message.done`，而 mock `m-001-2` 是 `done: true`，导致 `toolLabel: 'frontend-design'` 成为死数据
- [ ] assistant 三点脉冲 loading — 同上，`done: true` 下不渲染
- [ ] MessageList 底部「内容由 AI 生成，仅供参考」disclaimer — `MessageList.vue` 内无此元素（disclaimer 目前只在 HomeView）
- [ ] Composer placeholder「继续对话...」— 实际为「给 Darvin 发送消息…」（`chat.placeholder`）
- [x] Composer 圆形 send 按钮 — 圆形达成（`border-radius` 为 rounded-full）；配色与 spec 的「bg-text，hover bg-primary」不同，实际为 空输入 disabled 灰 → 有输入 `#FF5722` → hover `#E64A19`

### ExpertSuite
- [x] 点击 nav 「专家套件」→ 切到 suite 视图
- [x] 标题「专家套件」+ 搜索框 + 6 个 filter tab（实测 tab：全部 / 免费 / 创意 / 效率 / 技术 / 商业）
- [x] 9 张 agent-card 渲染（3 列 desktop）（实测 cards=9，`grid-template-columns` 3 列）
- [x] 点击 filter tab → 卡片过滤（按 card.category）（实测「免费」→ 3 张）
- [x] active filter tab：bg-primary-muted + text-primary（实测 `rgba(255,87,34,0.1)` / `rgb(255,87,34)`）

### Settings
- [x] 点击「设置」→ 切到 settings 视图
- [x] 左 sub-nav 4 项：账户 / 外观 / 快捷键 / 关于
- [x] 默认选中「外观」（`aria-current="page"` 落在「外观」）
- [x] 外观 panel：light / dark radio → 真实切换 html.class + localStorage 持久化
- [x] 重启应用后主题保留（reload 后 `html.dark` 与 `localStorage=dark` 均保持，body 背景 `rgb(15,17,23)`）

### 视图切换
- [x] home → 输入消息 → chat
- [x] home/chat → 点 nav 「专家套件」→ suite
- [x] home/chat/suite → 点「设置」→ settings
- [x] settings → 点 nav 「新建任务」→ home（新 session）
- [x] HomeView unmount 时 mascot 动画停止（不漏 timer）（Mascot 为纯 CSS 动画、组件内无任何 timer；实测页面加载后新增 `setInterval` 数为 0）
- [x] 同时只允许 1 个浮层（PlusMenu / ModelPicker dropdown 互斥）（实测开 ModelPicker 后点 Plus，ModelPicker 关闭）

### 主题切换
- [x] Settings → 外观 → dark：html 加 dark class，所有面板翻转
- [x] localStorage 记录偏好，重启应用保留（`useTheme.ts` KEY=`darvin.theme`）
- [x] 默认无 localStorage 时为 light（`useTheme.ts` readStored 默认 `'light'`）

---

## 8. PR 拆分计划

### PR-1: theme + Sidebar 重构（基础视觉对齐） — ✅ 已交付（`cf6a1c5`）

**范围**：
- theme.css 完整重写（light default + red primary + qa-tile / avatar 全局选择器 + 5 keyframes）
- index.html 移除 Google Fonts link
- AppShell.vue 改 grid 220/300 + 移除装饰层
- Sidebar 5 个新组件（Brand / Nav / AgentCard / SessionList / Bottom）+ 删 Header/Footer
- useTheme 改默认 light + 移除 5 主题 picker 支持
- 新增 icons：search / clock / layout / star / link / log-in / mascot-mini
- ChatHeader 视觉微调（padding / 标题 / 主题切换 IconButton）
- mock sessions 扩到 7 条
- i18n sidebar key

**验收**：
- sidebar 视觉与 `docs/原型/首页.png` 一致
- 默认 light 主题生效
- npm run lint / build / start 全通

**预估文件**：~22 个

### PR-2: HomeView + 视图路由（核心交互） — ✅ 已交付（`575bba4`、`7c0ee52`）

**范围**：
- HomeView.vue + AppShell.vue 视图动态 component（4 视图路由）
- useViewMode composable（4 视图派生 + navigate）
- Mascot.vue + HeroGreeting.vue
- PromptDock.vue + PromptToolbar.vue + Textarea
- ModelPicker.vue（共享）+ SendButton + MicButton
- Composer.vue 视觉调整（rounded-xl + 中文 placeholder + 圆形 send）
- 新增 icons：mascot-full / arrow-up / grid / bolt / mic / chevron-down（已有）
- i18n home / model key
- keyframes 注册（fadeIn / mascot-breathe / mascot-blink / mascot-wave / plusMenuIn）
- MessageList 改为 max-w-760 居中 + 底部 disclaimer
- MessageItem 改为气泡 + tool label chip
- StreamingText 改为三点脉冲

**验收**：
- 启动 → HomeView 渲染
- mascot 3 动画播放
- greeting 时段正确
- 输入文本 + Enter → view 切到 chat
- user 消息右对齐气泡 + tool label

**预估文件**：~18 个

### PR-3: QuickActions + PlusMenu + ModelPicker dropdown（Home 装饰） — ✅ 已交付（`2615e74`、`ac9e736`、`a598f3e`）

**范围**：
- QuickActions.vue + 4 个 qa-color token + 4 个 qa-* svg icon
- PlusMenu.vue + 4 项浮层
- PlusButton（在 PromptToolbar 内）
- ModelPicker dropdown 完整实现（搜索框 + 3 模型 mock）
- icons：paperclip / gear / target / list / qa-slide / qa-data / qa-doc / qa-web
- i18n quick / plus key
- mock messages 加 toolLabel 字段
- mock models（3 条）+ expertSuiteAgents（9 条）写入 mock-data.ts

**验收**：
- 4 个 quick-action tile 颜色 + hover glow 正确
- plus menu 4 项浮层展开 / 关闭
- model picker dropdown 3 选项 + 搜索框
- 同时只 1 个浮层（互斥）
- 浮层 Escape 关闭

**预估文件**：~12 个

### PR-4: ExpertSuite + Settings（2 个新视图） — ✅ 已交付（PR-4a `c8ffdaf`、PR-4b `df92364`）

**范围**：
- ExpertSuite.vue + AgentFilterTabs.vue + AgentCard.vue
- 9 张 mock agent card 渲染（3 列 grid）
- filter tab 切换过滤
- SettingsView.vue + SettingsSubNav.vue
- 4 个 panel：Account / Appearance / Shortcuts / About
- Appearance panel 真实切换主题 + localStorage 持久化
- i18n suite / settings key
- icons：（无新增）

**验收**：
- 点击 nav 「专家套件」→ 渲染 9 张 card + 6 filter tab
- 点击 filter → 过滤生效
- 点击「设置」→ 切到 settings 视图
- 4 个 sub-nav panel 切换正常
- 外观 panel 切主题真实生效
- 重启应用主题保留

**预估文件**：~14 个

---

## 9. 视觉原型引用

完整视觉参考：`docs/原型/` 目录下 6 张实际产品截图：

| 文件 | 描述 | 对应 v6 FR |
|---|---|---|
| `首页.png` | 默认 home 视图 | FR-1 / FR-2 / FR-3 / FR-10 / FR-11 / FR-12 / FR-13 / FR-14 |
| `首页-加号内容.png` | plus menu 展开态 | FR-15 |
| `首页-模型切换.png` | model dropdown 展开态 | FR-16 |
| `首页-问答.png` | chat 视图（user 气泡 + assistant loading） | FR-5 / FR-6 |
| `首页-专家套件.png` | expert suite 市场 | FR-17 |
| `设置页面.png` | settings 视图 | FR-18 |

**截图与 v6 实现的对应关系**：

| 截图元素 | v6 Vue 组件 | 备注 |
|---|---|---|
| 红色龙虾 logo + brand | SidebarBrand.vue | §3 FR-3 |
| nav 6 项 | SidebarNav.vue | §3 FR-3 |
| 主 Agent 卡 | SidebarAgentCard.vue | §3 FR-3 |
| 近期任务列表 | SidebarSessionList.vue | §3 FR-3 |
| 登录 / 设置 按钮 | SidebarBottom.vue | §3 FR-3 |
| 模型 chip（MiniMax M3） | ModelPicker.vue | §3 FR-4 / FR-16 |
| 龙虾 mascot 角色插画 | Mascot.vue | §3 FR-11 |
| 时段问候 + tagline | HeroGreeting.vue | §3 FR-12 |
| 4 个 quick-action tile | QuickActions.vue | §3 FR-13 |
| PromptDock + plus menu | PromptDock.vue + PlusMenu.vue | §3 FR-14 / FR-15 |
| user 气泡 + tool label | MessageItem.vue | §3 FR-5 |
| 三点脉冲 loading | StreamingText.vue | §3 FR-5 |
| 「内容由 AI 生成」footer | MessageList.vue | §3 FR-5 |
| 继续对话 composer | Composer.vue | §3 FR-6 |
| 专家套件卡片网格 | ExpertSuite.vue + AgentCard.vue | §3 FR-17 |
| 6 个 filter tab | AgentFilterTabs.vue | §3 FR-17 |
| 设置子导航 | SettingsSubNav.vue | §3 FR-18 |
| 外观 / 主题切换 | SettingsPanelAppearance.vue | §3 FR-18 |

---

## 附录 A: v5 → v6 关键修正清单

| v5 错误 | v6 修正 | 截图依据 |
|---|---|---|
| dark default | **light default** | `首页.png` 白底 |
| blue primary `#60A5FA` | **red primary `#FF5722`** | `首页.png` 龙虾 logo + 4 tile 配色 |
| Geist + Instrument Serif | **系统字体优先** | `首页.png` 字形（PingFang SC 特征） |
| aurora 3 层 + grid mask | **移除装饰层** | `首页.png` 纯白底 |
| sidebar backdrop-blur 玻璃 | **实色 bg-surface** | `首页.png` sidebar 无模糊 |
| 7 nav 项（含 IM/梦境） | **6 项**（无 IM/梦境，含搜索/专家套件） | `首页.png` sidebar |
| 3 agent pill（main+研究员+前端工程师） | **1 agent card（主 Agent）** | `首页.png` |
| 5 主题 picker + 用户卡 | **登录 + 设置 按钮** | `首页.png` sidebar 底部 |
| 7 quick-action pill | **4 quick-action tile** | `首页.png` 主区 |
| pill 形态（rounded-pill） | **tile 形态（rounded-xl 方卡）** | `首页.png` |
| 抽象红球 mascot | **具象龙虾 SVG 角色** | `首页.png` |
| 「下午好, Darven」+ live-dot meta | **「晚上好」+ tagline（无 meta）** | `首页.png` |
| 2 视图（home/chat） | **4 视图**（home/chat/suite/settings） | 6 张截图 |
| 无 ExpertSuite | **新增 ExpertSuite 市场** | `首页-专家套件.png` |
| 无 Settings | **新增 Settings 视图** | `设置页面.png` |
| 无 plus menu | **新增 plus menu 4 项** | `首页-加号内容.png` |
| 无消息气泡 | **气泡化**（user 右对齐 + tool label） | `首页-问答.png` |
| 无 disclaimer | **底部「内容由 AI 生成」** | `首页-问答.png` |
| 3 PR | **4 PR** | 新增 suite + settings |

## 附录 B: 不做的事（明确否决）

- ❌ titlebar / traffic lights（Electron frame 管）
- ❌ aurora / grid 装饰背景（v5 误判）
- ❌ sidebar backdrop-blur 玻璃效果（v5 误判）
- ❌ 5 主题 picker（v5 误判，v6 只 light/dark）
- ❌ Geist / Instrument Serif 字体下载（v5 误判，用系统字体）
- ❌ 真实登录 / 注册
- ❌ 真实 ExpertSuite 数据接入
- ❌ 真实 Settings 持久化（除主题外）
- ❌ 真实模型切换逻辑
- ❌ 真实文件上传 / skill 加载 / 目标 / 计划模式
- ❌ voice 真实录音
- ❌ attach 真实文件系统
- ❌ quick-action 真实业务
- ❌ nav 真实路由（4 视图切换是真实的，但 nav 项里搜索任务/定时任务/技能/MCP 仍是占位）
- ❌ IPC 协议 / Go agent / 主进程改动

## 附录 C: 风险与权衡

| 风险 | 影响 | 缓解 |
|---|---|---|
| 截图分辨率不足（设置页面.png 分析失败） | Settings 视图细节可能偏离 | 先按通用 settings 布局实现，后续用户审核截图后再修 |
| 龙虾 mascot 角色 SVG 设计资源未提供 | PR-2 卡 mascot 视觉 | 先用简化占位（圆角矩形 + 白眼 + 嘴），后续替换正式 SVG |
| AGENTS.md 严禁组件级 CSS | qa-tile 颜色 / avatar 渐变需要映射 | 全部走 `@theme` token + theme.css 全局选择器 |
| 浮层互斥逻辑（PlusMenu / ModelPicker / 其他 dropdown） | UX 混乱 | AppShell 统一管理 `openDropdown` 单例 state |
| 消息气泡圆角不对称（rounded-2xl + br-md） | Tailwind 不直接支持不对称圆角 | 用 arbitrary value `rounded-[16px] rounded-br-[6px]` |
| expert-suite 卡片响应式 | 极小窗口卡片挤压 | `grid-cols-1 md:grid-cols-2 lg:grid-cols-3` breakpoint |
| 4 个 PR 串行依赖 | 整体周期长 | PR-1 完成后 PR-2/3 可并行（PR-4 依赖 PR-1 + PR-2） |
