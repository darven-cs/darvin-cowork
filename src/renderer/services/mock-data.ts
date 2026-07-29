/**
 * Mock 数据：会话列表 + 历史消息。
 *
 * 消费者应通过 `await window.darvin.listSessions()` /
 * `getMessages(sessionId)` 拿数据，本文件仅在 mock 模式下生效。
 */

import type { DarvinSession, DarvinMessage } from '../../shared/darvin-api';

const now = Date.now();

export const mockSessions: DarvinSession[] = [
  { id: 's-001', title: '给我写一个贪吃蛇',         updatedAt: now - 60_000 },
  { id: 's-002', title: '整理本周飞书周会纪要',     updatedAt: now - 3_600_000 },
  { id: 's-003', title: '设计个人作品集首页',       updatedAt: now - 3 * 3_600_000 },
  { id: 's-004', title: '中翻英：产品发布会讲稿',   updatedAt: now - 86_400_000 },
  { id: 's-005', title: 'React 表单组件重构',       updatedAt: now - 2 * 86_400_000 },
  { id: 's-006', title: '批量重命名 4K 张产品图',   updatedAt: now - 4 * 86_400_000 },
  { id: 's-007', title: '数据分析：Q2 销售报表',    updatedAt: now - 7 * 86_400_000 },
];

export const mockMessages: Record<string, DarvinMessage[]> = {
  's-001': [
    {
      id: 'm-001-1', sessionId: 's-001', role: 'user',
      content: '给我写一个贪吃蛇', done: true, createdAt: now - 60_000,
    },
  ],
  's-002': [],
  's-003': [],
  's-004': [],
  's-005': [],
  's-006': [],
  's-007': [],
};
