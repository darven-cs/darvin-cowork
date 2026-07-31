/**
 * 视图路由状态：home / chat / suite / settings
 *
 * AppShell 监听 mode 切换 component 渲染；
 * Sidebar / HomeView / ChatView 等通过 navigate() 驱动。
 */

import { ref } from 'vue';

export type ViewMode = 'home' | 'chat' | 'suite' | 'settings' | 'search';

const mode = ref<ViewMode>('home');

export function useViewMode() {
  function navigate(target: ViewMode): void {
    mode.value = target;
  }
  function goHome(): void { mode.value = 'home'; }
  function goChat(): void { mode.value = 'chat'; }
  function goSuite(): void { mode.value = 'suite'; }
  function goSettings(): void { mode.value = 'settings'; }
  function goSearch(): void { mode.value = 'search'; }
  return { mode, navigate, goHome, goChat, goSuite, goSettings, goSearch };
}
