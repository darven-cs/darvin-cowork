/**
 * 浮层互斥状态：同时只允许 1 个浮层（plus / model / quick-action 等）打开。
 *
 * 用法：
 *   const fp = useFloatingPanel();
 *   // 打开 plus
 *   fp.toggle('plus');
 *   // 关闭
 *   fp.close();
 *   // 是否当前浮层（用于 v-if）
 *   fp.isActive('plus')
 */

import { onBeforeUnmount, onMounted, ref } from 'vue';

const activePanel = ref<string | null>(null);

export function useFloatingPanel() {
  function open(name: string): void {
    activePanel.value = name;
  }
  function close(): void {
    activePanel.value = null;
  }
  function toggle(name: string): void {
    activePanel.value = activePanel.value === name ? null : name;
  }
  function isActive(name: string): boolean {
    return activePanel.value === name;
  }

  // 全局 Escape 关闭（仅注册一次）
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && activePanel.value !== null) {
      activePanel.value = null;
    }
  }

  onMounted(() => {
    document.addEventListener('keydown', onKeydown);
  });
  onBeforeUnmount(() => {
    document.removeEventListener('keydown', onKeydown);
  });

  return { activePanel, open, close, toggle, isActive };
}