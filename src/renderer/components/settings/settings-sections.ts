/**
 * 设置面板 tab 注册表：7 个 tab + query string 深链校验。
 */

export const SettingsSections = [
  'general',
  'appearance',
  'shortcuts',
  'models',
  'memory',
  'runtime',
  'about',
] as const;

export type SettingsSectionId = typeof SettingsSections[number];

export function isSettingsSectionId(v: string | null): v is SettingsSectionId {
  return v !== null && (SettingsSections as readonly string[]).includes(v);
}
