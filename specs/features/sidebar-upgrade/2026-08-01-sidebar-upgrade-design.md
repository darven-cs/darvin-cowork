# 侧栏升级

> 编号 **06**。把侧栏从「会话列表 + 静态 Agent 卡」升级为「树形 Agent + 拖拽改宽 + 多 tab 真实入口 + 快捷键」。

## 1. 背景

`src/renderer/components/sidebar/*` 现状：
- 6 个 nav 按钮（新建任务/搜索/定时任务/专家套件/技能/MCP），其中 scheduled / skill / mcp 仅 `console.warn`
- `SidebarAgentCard.vue` 静态卡，无切换
- 220px 固定宽，折叠时整列 `0px`（`AppShell.vue` 布局会跳）
- 6 个快捷键仅 `SettingsPanelShortcuts` 展示，无绑定

LobsterAI 侧栏：树形 Agent + 会话 + 子会话 + 拖拽排序 + 220-420px 可调 + `Cmd+1-5` 快捷键。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 侧栏宽度 220-420px 拖拽可调 | MIN/MAX 边界 + `transition: width 180ms` |
| G2 | nav 后 3 个 tab（scheduled / skill / mcp）至少落地为「未实现」占位面板 | 空态友好 |
| G3 | `Cmd+1-5` 快捷键绑定 nav 切换 | `useShortcut` composable 统一注册 |
| G4 | 会话项显示 status（idle / running / completed / error）+ pin + 拖拽排序 | `SessionItem` 升级 |
| G5 | Agent 区支持多 Agent 树形展示（主 agent + 子 agent） | `MyAgentSidebarTree` 等价组件 |
| G6 | 折叠态：图标模式 + tooltip | `isCollapsed` 派生 |

## 3. 非目标

- 不做多 Agent 创建/编辑（属 settings 07 spec）
- 不做跨 device session 同步
- 不做侧栏拖拽到主区（分离窗口是后续 spec）

## 4. 设计要点

### 4.1 布局改造

`AppShell.vue` 把 `gridTemplateColumns: '220px 1fr 300px'` 改为 `gridTemplateColumns: 'var(--sidebar-width) 1fr var(--sidepanel-width)'`，其中两个 width 由 CSS variable 驱动。

```css
:root {
  --sidebar-width: 244px;
  --sidebar-min: 220px;
  --sidebar-max: 420px;
}
```

### 4.2 拖拽 handle

```vue
<!-- Sidebar.vue -->
<aside :style="{ width: 'var(--sidebar-width)' }" class="relative">
  <SidebarBrand />
  <SidebarNav />
  <SessionList />
  <SidebarBottom />
  <div class="resize-handle" @mousedown="startDrag" />
</aside>
```

```ts
function startDrag(e: MouseEvent) {
  const startX = e.clientX;
  const startW = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width'));
  function onMove(ev: MouseEvent) {
    const w = clamp(startW + (ev.clientX - startX), 220, 420);
    document.documentElement.style.setProperty('--sidebar-width', `${w}px`);
  }
  function onUp() {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    // 持久化
    localStorage.setItem('darvin.sidebar.width', String(...));
  }
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
}
```

### 4.3 快捷键注册

`src/renderer/composables/useShortcuts.ts`：

```ts
export function useShortcuts() {
  const view = useViewMode();
  const onKey = (e: KeyboardEvent) => {
    if (!(e.metaKey || e.ctrlKey)) return;
    const map: Record<string, ViewMode> = {
      '1': 'chat', '2': 'search', '3': 'scheduled', '4': 'expert', '5': 'skills',
    };
    if (map[e.key]) {
      e.preventDefault();
      view.setView(map[e.key]);
    }
  };
  onMounted(() => document.addEventListener('keydown', onKey));
  onUnmounted(() => document.removeEventListener('keydown', onKey));
}
```

### 4.4 会话项 status 多态

| status | UI |
|---|---|
| `idle` | 默认 |
| `running` | 绿色脉冲点（`bg-accent animate-pulse`） |
| `completed` | 灰色勾 |
| `error` | 红色感叹号 |
| `pinned` | 顶部 pin 图标 |

## 5. 用户场景

### 场景 1：拖拽改宽

**Given** 侧栏 220px

**When** 鼠标按住右侧 handle 拖到 320px

**Then** 宽度平滑过渡；下次启动保持 320px

### 场景 2：快捷键切 tab

**Given** 当前在 chat view

**When** 按 `Cmd+3`

**Then** 切到 scheduled view（即使是空态也展示空态面板，不 warn）

### 场景 3：会话项 running 状态

**Given** session 跑 bash 工具中

**When** 渲染

**Then** `SessionItem` 显示绿色脉冲点；切回该 session 时清除

## 6. 验收

- [x] 拖拽 handle 流畅，220-420px 边界正确
- [x] 宽度持久化（localStorage）
- [x] 6 个 nav tab 全部可点（即使内容是空态面板，不是 warn）
- [x] `Cmd+1-5` / `Ctrl+1-5` 快捷键生效
- [x] 会话项 5 种 status 正确显示
- [x] 折叠态：220px → 56px 紧凑模式
- [x] `npm run lint` 通过

### 落地补充（实现期决议）

- **宽度实现**：不依赖 CSS `:root` 变量声明，`useSidebar` 持 `width` ref（220-420 clamp）并把 `--sidebar-width` 写到 `documentElement.style`，`AppShell` grid 左列直接引用 `var(--sidebar-width)`；拖拽期间 `dragging` 置真关闭 grid 过渡。
- **紧凑态**：`collapsed` 不再从 DOM 摘除 Sidebar，而是 220px → 56px 图标 rail；Brand/Nav/AgentCard/Bottom 各收 `collapsed` prop 切换 icon-only + `title` tooltip，session 列表折叠时隐藏。
- **nav 全可点**：`scheduled` / `skill` / `mcp` 三个 nav 接入 `useViewMode` 新增的 `scheduled` / `skills` / `mcp` mode，AppShell 路由到统一 `PlaceholderView`（icon + 标题 + desc），不再 `console.warn`。
- **快捷键**：新建 `useShortcuts.ts`，`Cmd/Ctrl+1-5` 映射 home/search/scheduled/suite/skills；可编辑元素聚焦时跳过；`settings` 不占 1-5。
- **会话状态**：`useMessages` 新增 `sessionStatusBySessionId`（流式→running / done→completed / error→error / agent_end→completed），历史加载走 `deriveSessionStatusFromMessages` 纯函数（error > completed > idle）；`SessionItem` 用 `status` prop 替换原 `running`。
- **pin**：`useSession` 新增 `pinnedSessionIds`（localStorage `darvin.sidebar.pinned`）+ `togglePin`；SessionList 置顶排序；SessionItem 状态图标旁加 pin 徽标，下拉菜单含置顶/取消置顶；deleteSession 同步清理。
- **G5 多 Agent 树**：darvin 无子代理体系（spec 11 已确认），本轮保留 `SidebarAgentCard` 单主 Agent 卡，未造树；后续接子代理体系时再补 `MyAgentSidebarTree` 等价组件。

## 7. 依赖

- **可并行**：01-08 全部
- **后置**：07-settings-expansion 中 shortcuts tab 复用本 spec 绑定的快捷键

## 8. 参考

### darvin-cowork
- `src/renderer/components/sidebar/Sidebar.vue` — 容器
- `src/renderer/components/sidebar/SidebarAgentCard.vue` — 静态卡
- `src/renderer/components/sidebar/SidebarNav.vue` — 5 nav（3 个 warn）
- `src/renderer/components/sidebar/SessionItem.vue` — 会话项
- `src/renderer/layout/AppShell.vue` — `gridTemplateColumns`
- `src/renderer/composables/useSidebar.ts` / `useSession.ts` / `useViewMode.ts`
- `src/renderer/components/settings/SettingsPanelShortcuts.vue` — 6 个绑定面板

### LobsterAI（借鉴）

> 参考项目根目录：`~/桌面/github-project/LobsterAI`（下述路径均相对该项目根）。组件实现遇阻时直接查该项目源码。

- `src/renderer/components/Sidebar.tsx` — 拖拽 + 宽度范围
- `src/renderer/components/agentSidebar/MyAgentSidebarTree.tsx` — 树形
- `src/renderer/components/agentSidebar/AgentTreeNode.tsx`
- `src/renderer/components/agentSidebar/AgentTaskRow.tsx`

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.4「侧栏」
