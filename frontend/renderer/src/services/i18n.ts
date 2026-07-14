/**
 * 轻量 i18n 工具。
 *
 * 用法：
 *   import { t, setLocale, getLocale } from '@/services/i18n';
 *   t('action.submit');
 *   t('message.greeting', { name: 'darven' });
 *   setLocale('en');
 *
 * 新增 key 必须同时加入 zh.json 和 en.json。
 */
import zhDict from '../locales/zh.json';
import enDict from '../locales/en.json';

export const Locale = {
  Zh: 'zh',
  En: 'en',
} as const;
export type Locale = (typeof Locale)[keyof typeof Locale];

const dict: Record<Locale, Record<string, string>> = {
  [Locale.Zh]: zhDict as Record<string, string>,
  [Locale.En]: enDict as Record<string, string>,
};

const STORAGE_KEY = 'darvin.locale';

function readInitialLocale(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === Locale.Zh || saved === Locale.En) return saved;
  } catch {
    /* localStorage may be unavailable */
  }
  const nav = typeof navigator !== 'undefined' ? navigator.language : '';
  return nav.startsWith('zh') ? Locale.Zh : Locale.En;
}

let current: Locale = readInitialLocale();

export function getLocale(): Locale {
  return current;
}

export function setLocale(l: Locale): void {
  current = l;
  try {
    localStorage.setItem(STORAGE_KEY, l);
  } catch {
    /* ignore */
  }
}

/**
 * 翻译 key。找不到时返回 key 本身（便于发现遗漏）。
 * vars 支持 `{name}` 占位符替换。
 */
export function t(key: string, vars?: Record<string, string | number>): string {
  let s = dict[current]?.[key] ?? dict[Locale.En]?.[key] ?? key;
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      s = s.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v));
    }
  }
  return s;
}
