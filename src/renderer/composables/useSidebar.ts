import { ref, watch } from 'vue';

const KEY = 'darvin.sidebar.collapsed';

function readStored(): boolean {
  if (typeof localStorage === 'undefined') return false;
  return localStorage.getItem(KEY) === 'true';
}

const collapsed = ref<boolean>(readStored());

watch(collapsed, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY, v ? 'true' : 'false');
  }
});

export function useSidebar() {
  function toggle() {
    collapsed.value = !collapsed.value;
  }
  function set(v: boolean) {
    collapsed.value = v;
  }
  return { collapsed, toggle, set };
}
