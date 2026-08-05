import { ref, watch } from 'vue';

const KEY_COLLAPSED = 'darvin.sidebar.collapsed';
const KEY_WIDTH = 'darvin.sidebar.width';

/** 侧栏宽度边界与紧凑态宽度。 */
export const MIN_SIDEBAR_WIDTH = 220;
export const MAX_SIDEBAR_WIDTH = 420;
export const DEFAULT_SIDEBAR_WIDTH = 244;
export const COMPACT_SIDEBAR_WIDTH = 56;

function readStoredCollapsed(): boolean {
  if (typeof localStorage === 'undefined') return false;
  return localStorage.getItem(KEY_COLLAPSED) === 'true';
}

function readStoredWidth(): number {
  if (typeof localStorage === 'undefined') return DEFAULT_SIDEBAR_WIDTH;
  const raw = localStorage.getItem(KEY_WIDTH);
  if (raw === null) return DEFAULT_SIDEBAR_WIDTH;
  const n = Number(raw);
  return clampWidth(Number.isFinite(n) ? n : DEFAULT_SIDEBAR_WIDTH);
}

function clampWidth(w: number): number {
  return Math.max(MIN_SIDEBAR_WIDTH, Math.min(MAX_SIDEBAR_WIDTH, w));
}

const collapsed = ref<boolean>(readStoredCollapsed());
const width = ref<number>(readStoredWidth());
const dragging = ref(false);

watch(collapsed, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY_COLLAPSED, v ? 'true' : 'false');
  }
});

watch(
  width,
  (v) => {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(KEY_WIDTH, String(v));
    }
    if (typeof document !== 'undefined') {
      document.documentElement.style.setProperty('--sidebar-width', `${v}px`);
    }
  },
  { immediate: true },
);

export function useSidebar() {
  function toggle() {
    collapsed.value = !collapsed.value;
  }
  function set(v: boolean) {
    collapsed.value = v;
  }
  function setWidth(w: number) {
    width.value = clampWidth(w);
  }
  function setDragging(v: boolean) {
    dragging.value = v;
  }
  return { collapsed, width, dragging, toggle, set, setWidth, setDragging };
}
