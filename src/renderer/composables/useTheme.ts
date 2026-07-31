import { ref } from 'vue';

type Theme = 'dark' | 'light';
const KEY = 'darvin.theme';

function readStored(): Theme {
  if (typeof localStorage === 'undefined') return 'light';
  const raw = localStorage.getItem(KEY);
  return raw === 'dark' ? 'dark' : 'light';
}

const theme = ref<Theme>(readStored());

if (typeof document !== 'undefined') {
  document.documentElement.classList.toggle('dark', theme.value === 'dark');
  document.documentElement.classList.remove('light');
}

export function useTheme() {
  function apply(t: Theme) {
    theme.value = t;
    if (typeof document !== 'undefined') {
      document.documentElement.classList.toggle('dark', t === 'dark');
      document.documentElement.classList.remove('light');
    }
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(KEY, t);
    }
  }
  function toggle() {
    apply(theme.value === 'dark' ? 'light' : 'dark');
  }
  return { theme, apply, toggle };
}
