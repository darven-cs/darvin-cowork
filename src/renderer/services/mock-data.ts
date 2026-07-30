/**
 * Mock 数据：会话列表 / 历史消息 / 模型 / 专家套件 Agent。
 *
 * 消费者应通过 `await window.darvin.listSessions()` /
 * `getMessages(sessionId)` 拿数据，本文件仅在 mock 模式下生效。
 */

import type { DarvinSession, DarvinMessage, DarvinModelId } from '../../shared/darvin-api';

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
    {
      id: 'm-001-2', sessionId: 's-001', role: 'assistant',
      content: '好的，下面是一个用 HTML + JavaScript 实现的贪吃蛇小游戏...',
      done: true, createdAt: now - 59_000,
      toolLabel: 'frontend-design',
    },
  ],
  's-002': [],
  's-003': [],
  's-004': [],
  's-005': [],
  's-006': [],
  's-007': [],
};

export interface MockModel {
  id: DarvinModelId;
  label: string;
  vendor: 'anthropic' | 'openai';
  description: string;
}

export const mockModels: MockModel[] = [
  {
    id: 'claude-sonnet-4-5',
    label: 'Claude Sonnet 4.5',
    vendor: 'anthropic',
    description: '速度与质量的平衡，适合日常任务',
  },
  {
    id: 'claude-opus-4-5',
    label: 'Claude Opus 4.5',
    vendor: 'anthropic',
    description: '最强推理能力，适合复杂任务',
  },
  {
    id: 'gpt-4o',
    label: 'GPT-4o',
    vendor: 'openai',
    description: 'OpenAI 多模态旗舰',
  },
];

/** ExpertSuite agent 分类（spec v6 FR-17 filter tabs）。 */
export type ExpertCategory = 'creative' | 'productivity' | 'technical' | 'business';

/** Agent avatar 配色 token 名称（对应 theme.css 的 --color-agent-*）。 */
export type AgentColor =
  | 'amber' | 'violet' | 'blue' | 'green'
  | 'red' | 'cyan' | 'pink' | 'orange' | 'purple';

export interface ExpertAgent {
  id: string;
  name: string;
  category: ExpertCategory;
  description: string;
  color: AgentColor;     // 头像底色 token 名
  icon: string;
  price: 'Free' | '50 credits/次' | '100 credits/次' | '200 credits/次' | '300 credits/次';
}

export const expertSuiteAgents: ExpertAgent[] = [
  { id: 'a-01', name: '会议助手',   category: 'productivity', description: '整理会议纪要、提取待办', color: 'amber',  icon: 'qa-slide', price: 'Free' },
  { id: 'a-02', name: 'PPT 设计师', category: 'creative',     description: '一键生成演示文稿',      color: 'violet', icon: 'qa-slide', price: '100 credits/次' },
  { id: 'a-03', name: '数据分析师', category: 'productivity', description: '上传文件，智能分析',     color: 'blue',   icon: 'qa-data',  price: '100 credits/次' },
  { id: 'a-04', name: '代码审查',   category: 'technical',    description: 'PR diff 审查、安全检测', color: 'green',  icon: 'qa-web',   price: '200 credits/次' },
  { id: 'a-05', name: '研究助理',   category: 'technical',    description: '联网检索、资料汇总',     color: 'red',    icon: 'qa-web',   price: 'Free' },
  { id: 'a-06', name: '翻译官',     category: 'productivity', description: '多语种互译、润色',       color: 'cyan',   icon: 'qa-doc',   price: '50 credits/次' },
  { id: 'a-07', name: '销售助理',   category: 'business',     description: '客户画像、商机跟进',     color: 'pink',   icon: 'qa-data',  price: '300 credits/次' },
  { id: 'a-08', name: '产品经理',   category: 'business',     description: 'PRD 撰写、需求拆解',     color: 'orange', icon: 'qa-doc',   price: 'Free' },
  { id: 'a-09', name: '设计师',     category: 'creative',     description: '品牌 / 海报 / UI 设计',  color: 'purple', icon: 'qa-slide', price: '200 credits/次' },
];