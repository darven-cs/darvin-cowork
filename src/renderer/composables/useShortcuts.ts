/**
 * useShortcuts — Cmd/Ctrl+1-5 切换侧栏导航。
 *
 * 统一在 AppShell 挂载时注册一次；键盘映射与 SidebarNav 的 6 个 nav 项对齐，
 * settings 用 ⌘, 不占 1-5。可编辑元素聚焦时跳过，避免打断输入。
 */
import { onMounted, onUnmounted } from 'vue';
import { useViewMode, type ViewMode } from './useViewMode';
import { useSession } from './useSession';

const NAV_KEYS: Record<string, ViewMode> = {
  '1': 'home',
  '2': 'search',
  '3': 'scheduled',
  '4': 'suite',
  '5': 'skills',
};

function isEditableTarget(e: KeyboardEvent): boolean {
  const el = e.target as HTMLElement | null;
  if (!el) return false;
  if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT') return true;
  return el.isContentEditable;
}

export function useShortcuts() {
  const view = useViewMode();
  const session = useSession();

  function onKey(e: KeyboardEvent): void {
    if (!(e.metaKey || e.ctrlKey)) return;
    if (isEditableTarget(e)) return;
    const target = NAV_KEYS[e.key];
    if (!target) return;
    e.preventDefault();
    if (target === 'home') {
      session.startNewTask();
    }
    view.navigate(target);
  }

  onMounted(() => document.addEventListener('keydown', onKey));
  onUnmounted(() => document.removeEventListener('keydown', onKey));

  return {};
}
