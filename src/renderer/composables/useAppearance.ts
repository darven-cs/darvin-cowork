/**
 * useAppearance — 外观偏好：UI 字号 / 代码字号 / 主题色。
 *
 * 与 useTheme 同模式，localStorage 持久化；运行期把值写到 theme.css 的
 * @theme token 上（LobsterAI applyTypographyPreferences 同款比例缩放）：
 *   - UI 字号：uiFontSize/14 比例整体缩放 --text-xs..2xl（body 与 markdown 消费）
 *   - 代码字号：单独写 --text-code（CodeBlock / 代码块消费）
 *   - 主题色：html[data-accent] 属性驱动 --color-primary 家族覆盖
 */
import { ref, watch } from 'vue';

export type AccentColor = 'orange' | 'blue' | 'green';

export const UI_FONT_MIN = 11;
export const UI_FONT_MAX = 16;
export const UI_FONT_DEFAULT = 14;
export const CODE_FONT_MIN = 8;
export const CODE_FONT_MAX = 24;
export const CODE_FONT_DEFAULT = 13;

const KEY_UI = 'darvin.ui-font-size';
const KEY_CODE = 'darvin.code-font-size';
const KEY_ACCENT = 'darvin.accent';

/** theme.css @theme 里声明的基础字号表；按 UI 字号比例整体缩放。 */
const UI_TEXT_BASE: Record<string, number> = {
  xs: 11,
  sm: 13,
  base: 14,
  md: 15,
  lg: 18,
  xl: 24,
  '2xl': 32,
};

export function clampNumber(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

export function readStoredNumber(key: string, fallback: number, min: number, max: number): number {
  if (typeof localStorage === 'undefined') return fallback;
  const raw = localStorage.getItem(key);
  if (raw === null) return fallback;
  const num = Number(raw);
  if (!Number.isFinite(num)) return fallback;
  return clampNumber(num, min, max);
}

export function readStoredAccent(): AccentColor {
  if (typeof localStorage === 'undefined') return 'orange';
  const raw = localStorage.getItem(KEY_ACCENT);
  return raw === 'blue' || raw === 'green' ? raw : 'orange';
}

/** 按 uiFontSize / 默认值 比例缩放基础字号表。 */
export function scaleUiText(uiFontSize: number): Record<string, number> {
  const scale = uiFontSize / UI_FONT_DEFAULT;
  const out: Record<string, number> = {};
  for (const [k, v] of Object.entries(UI_TEXT_BASE)) {
    out[k] = Math.round(v * scale);
  }
  return out;
}

const uiFontSize = ref<number>(readStoredNumber(KEY_UI, UI_FONT_DEFAULT, UI_FONT_MIN, UI_FONT_MAX));
const codeFontSize = ref<number>(readStoredNumber(KEY_CODE, CODE_FONT_DEFAULT, CODE_FONT_MIN, CODE_FONT_MAX));
const accentColor = ref<AccentColor>(readStoredAccent());

function applyAppearance(): void {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  const scaled = scaleUiText(uiFontSize.value);
  for (const [k, v] of Object.entries(scaled)) {
    root.style.setProperty(`--text-${k}`, `${v}px`);
  }
  root.style.setProperty('--text-code', `${codeFontSize.value}px`);
  root.dataset.accent = accentColor.value;
}

if (typeof document !== 'undefined') {
  applyAppearance();
}

// watch 在模块级只注册一次（仓库 composable singleton 惯例），避免多处调用重复建 watcher
watch([uiFontSize, codeFontSize, accentColor], applyAppearance);
watch(uiFontSize, (v) => {
  if (typeof localStorage !== 'undefined') localStorage.setItem(KEY_UI, String(v));
});
watch(codeFontSize, (v) => {
  if (typeof localStorage !== 'undefined') localStorage.setItem(KEY_CODE, String(v));
});
watch(accentColor, (v) => {
  if (typeof localStorage !== 'undefined') localStorage.setItem(KEY_ACCENT, v);
});

export function useAppearance() {
  function setUiFontSize(v: number): void {
    uiFontSize.value = clampNumber(Math.round(v), UI_FONT_MIN, UI_FONT_MAX);
  }
  function setCodeFontSize(v: number): void {
    codeFontSize.value = clampNumber(Math.round(v), CODE_FONT_MIN, CODE_FONT_MAX);
  }
  function setAccent(v: AccentColor): void {
    accentColor.value = v;
  }

  return { uiFontSize, codeFontSize, accentColor, setUiFontSize, setCodeFontSize, setAccent };
}
