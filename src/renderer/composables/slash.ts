/**
 * spec 39 — `/skill-name args` 斜杠命令的纯函数 helpers（renderer 侧路由）。
 * 放在独立模块便于单测，useChatActions 只负责把解析结果接到 IPC。
 */

import { t } from '../services/i18n';

export interface SlashCommand {
  skillId: string;
  args: string;
}

/**
 * 解析 `/skill-name args`。只取首行（多行输入 / 附件行不参与匹配）；
 * `/` 后不是合法 skill 名（小写字母开头 + 中划线，≤64 字符）返回 null，
 * 例如 `/`、`//x`、`not a command`。空 args 返回空串。
 */
export function parseSlashCommand(content: string): SlashCommand | null {
  const firstLine = content.split('\n')[0].trim();
  const m = /^\/([a-z0-9][a-z0-9-]{0,63})(?:\s+([\s\S]*))?$/.exec(firstLine);
  if (!m) return null;
  return { skillId: m[1], args: (m[2] ?? '').trim() };
}

/**
 * 把 Go 端 RPC error 翻译成 i18n toast 文案。Go 的错误消息形如
 * "skill: not found" / "skill: disabled" / "skill: not user invocable"。
 */
export function translateSkillError(err: Error, skillId: string): string {
  const msg = err.message ?? '';
  if (msg.includes('not found')) return t('slash.error.not_found', { id: skillId });
  if (msg.includes('disabled')) return t('slash.error.disabled', { id: skillId });
  if (msg.includes('not user invocable')) return t('slash.error.not_user_invocable', { id: skillId });
  return t('slash.error.unknown', { error: msg });
}
