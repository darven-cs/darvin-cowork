/**
 * McpServerFormModal 用到的纯文本解析 helpers。
 *
 * 拆出来便于测试（vitest include 限定为 *.test.ts；Vue 组件本身不挂测试）。
 *
 * 解析约定：
 * - args      按空白 split（简化：带空格的值不在 v0 范围内；走 shell quote 留给后续）
 * - env       一行一个 KEY=val；空行 / `#` 注释 / 缺 `=` 的行跳过
 * - headers   同 env
 */

export function parseArgs(text: string): string[] {
  return text
    .split(/\s+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export function parseKv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eq = trimmed.indexOf('=');
    if (eq <= 0) continue;
    const key = trimmed.slice(0, eq).trim();
    const val = trimmed.slice(eq + 1).trim();
    if (key) out[key] = val;
  }
  return out;
}

export function formatKv(kv: Record<string, string>): string {
  return Object.entries(kv).map(([k, v]) => `${k}=${v}`).join('\n');
}