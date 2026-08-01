/**
 * Token 数字格式化 + 上下文状态派生（spec 03）。
 *
 * 纯函数，无 Vue 依赖，方便单测：TurnMeta 的 token 三元组、
 * ContextUsageIndicator 的圆环/tooltip 都从这里取值。
 */

import type { DarvinContextUsageStatus } from '../../shared/darvin-api';

/**
 * 把 token 数压成短标签：1234 → "1.2k"，1500000 → "1.5M"，999 → "999"。
 * 与 LobsterAI tokenFormat 对齐。
 */
export function formatTokenCount(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens < 0) return '0';
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1).replace(/\.0$/, '')}k`;
  return String(tokens);
}

/**
 * 5 态上下文状态：事件显式给的 status 优先（如 compacting / normal），
 * 只有 unknown/缺失时按 percent 阈值派生 —— normal <60%，warning 60-85%，
 * danger >85%（spec §4.3）。
 */
export function deriveContextStatus(
  status: DarvinContextUsageStatus | undefined,
  percent: number | undefined,
): DarvinContextUsageStatus {
  if (status && status !== 'unknown') return status;
  if (typeof percent !== 'number') return 'unknown';
  if (percent < 60) return 'normal';
  if (percent <= 85) return 'warning';
  return 'danger';
}
