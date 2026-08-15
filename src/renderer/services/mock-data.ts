/**
 * Mock 数据：模型 / 专家套件 Agent。会话列表 / 历史消息走 Go agent
 * 持久化，本文件不再提供 session / message mock 种子。
 */

import type { DarvinModelId } from '../../shared/darvin-api';

export interface MockModel {
  id: DarvinModelId;
  label: string;
  vendor: 'anthropic' | 'openai';
  description: string;
}

export const mockModels: MockModel[] = [
  {
    id: 'claude-sonnet-4-6',
    label: 'Claude Sonnet 4.6',
    vendor: 'anthropic',
    description: '速度与质量的平衡，适合日常任务',
  },
  {
    id: 'claude-opus-4-7',
    label: 'Claude Opus 4.7',
    vendor: 'anthropic',
    description: '最强推理能力，适合复杂任务',
  },
  {
    id: 'gpt-5.4',
    label: 'GPT-5.4',
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