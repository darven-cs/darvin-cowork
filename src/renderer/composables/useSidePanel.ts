import { ref, watch } from 'vue';

/**
 * useSidePanel — 右侧工具面板开关。
 *
 * spec 11 后右侧面板就是纯 Artifact 面板（外层 Tools/Thinking tab 已随
 * LobsterAI 对齐移除），这里只保留 open 状态，没有 tab 概念。
 */

const KEY_OPEN = 'darvin.sidepanel.open';

function readOpen(): boolean {
  if (typeof localStorage === 'undefined') return true;
  const raw = localStorage.getItem(KEY_OPEN);
  return raw === null ? true : raw === 'true';
}

const open = ref<boolean>(readOpen());

watch(open, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY_OPEN, v ? 'true' : 'false');
  }
});

export function useSidePanel() {
  function toggle() {
    open.value = !open.value;
  }
  function set(v: boolean) {
    open.value = v;
  }
  return { open, toggle, set };
}
