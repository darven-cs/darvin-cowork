/**
 * 视图路由状态：home / chat / suite / settings / search / scheduled / skills / mcp
 *
 * AppShell 监听 mode 切换 component 渲染；
 * Sidebar / HomeView / ChatView 等通过 navigate() 驱动。
 * scheduled / skills / mcp 也是真实导航入口（内容为占位面板）。
 */

import { ref } from 'vue';

export type ViewMode =
  | 'home'
  | 'workspaces'
  | 'chat'
  | 'suite'
  | 'settings'
  | 'search'
  | 'scheduled'
  | 'skills'
  | 'mcp';

const mode = ref<ViewMode>('home');

export function useViewMode() {
  function navigate(target: ViewMode): void {
    mode.value = target;
  }
  function goHome(): void { mode.value = 'home'; }
  function goChat(): void { mode.value = 'chat'; }
  function goWorkspaces(): void { mode.value = 'workspaces'; }
  function goSuite(): void { mode.value = 'suite'; }
  function goSettings(): void { mode.value = 'settings'; }
  function goSearch(): void { mode.value = 'search'; }
  function goScheduled(): void { mode.value = 'scheduled'; }
  function goSkills(): void { mode.value = 'skills'; }
  function goMcp(): void { mode.value = 'mcp'; }
  return {
    mode, navigate,
    goHome, goChat, goWorkspaces, goSuite, goSettings, goSearch, goScheduled, goSkills, goMcp,
  };
}
