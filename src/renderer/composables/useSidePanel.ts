import { ref, watch } from 'vue';

export type SidePanelTab = 'tools' | 'thinking' | 'artifact';

const KEY_OPEN = 'darvin.sidepanel.open';
const KEY_TAB = 'darvin.sidepanel.tab';

function readOpen(): boolean {
  if (typeof localStorage === 'undefined') return true;
  const raw = localStorage.getItem(KEY_OPEN);
  return raw === null ? true : raw === 'true';
}

function readTab(): SidePanelTab {
  if (typeof localStorage === 'undefined') return 'tools';
  const raw = localStorage.getItem(KEY_TAB);
  return raw === 'thinking' || raw === 'artifact' ? raw : 'tools';
}

const open = ref<boolean>(readOpen());
const tab = ref<SidePanelTab>(readTab());

watch(open, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY_OPEN, v ? 'true' : 'false');
  }
});

watch(tab, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY_TAB, v);
  }
});

export function useSidePanel() {
  function toggle() {
    open.value = !open.value;
  }
  function set(v: boolean) {
    open.value = v;
  }
  function switchTab(t: SidePanelTab) {
    tab.value = t;
  }
  return { open, tab, toggle, set, switchTab };
}
