/**
 * Mock 数据：模型与专家套件 Agent 的**类型定义**与渲染端 adapter。
 *
 * 9 条 expert preset 的实际文案已迁到 Go 端
 * `src/darvin-agent/internal/agents/store/preset_seed.go` 的 PresetSeed()，
 * ExpertSuiteView / SettingsView 改走 `useAgents()` 拉 DB 数据。
 * 本文件仅保留：
 * - `ExpertCategory` / `ExpertPrice` / `AgentColor` / `ExpertAgent` 类型
 * - `darvinAgentToExpert()` adapter（双语文案 + category/price 映射）
 */

import type { DarvinModelId, DarvinAgent, ExpertCategory, ExpertPrice } from '../../shared/darvin-api';

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

/** ExpertSuite agent 分类。preset_id 决定；user 自建 agent 为 undefined。 */
export type { ExpertCategory } from '../../shared/darvin-api';
/** Agent avatar 配色 token 名称（对应 theme.css 的 --color-agent-*）。 */
export type AgentColor =
  | 'amber' | 'violet' | 'blue' | 'green'
  | 'red' | 'cyan' | 'pink' | 'orange' | 'purple';

/** preset_id → 静态元数据映射（category + price）；保留与历史 mock 一致。 */
const PRESET_META: Record<string, { category: ExpertCategory; price: ExpertPrice }> = {
  'preset-meeting': { category: 'productivity', price: 'Free' },
  'preset-slide': { category: 'creative', price: '100 credits/次' },
  'preset-analyst': { category: 'productivity', price: '100 credits/次' },
  'preset-codereview': { category: 'technical', price: '200 credits/次' },
  'preset-research': { category: 'technical', price: 'Free' },
  'preset-translator': { category: 'productivity', price: '50 credits/次' },
  'preset-sales': { category: 'business', price: '300 credits/次' },
  'preset-pm': { category: 'business', price: 'Free' },
  'preset-designer': { category: 'creative', price: '200 credits/次' },
  'preset-main': { category: 'productivity', price: 'Free' },
};

export interface ExpertAgent {
  id: string;
  name: string;
  category: ExpertCategory;
  description: string;
  color: AgentColor;
  icon: string;
  price: ExpertPrice;
  nameEn: string;
  descriptionEn: string;
  identity: string;
  identityEn: string;
  systemPrompt: string;
  systemPromptEn: string;
  skillIds: string[];
}

import { getLang } from './i18n';

/** Go Agent 行 → 专家卡片渲染形态。双语按 getLang() 选；category/price 按 presetId 映射。 */
export function darvinAgentToExpert(agent: DarvinAgent): ExpertAgent {
  const en = getLang() === 'en';
  const meta = PRESET_META[agent.presetId] ?? { category: 'productivity', price: 'Free' };
  return {
    id: agent.id,
    name: en ? agent.nameEn : agent.name,
    category: meta.category,
    description: en ? agent.descriptionEn : agent.description,
    color: agent.color as AgentColor,
    icon: agent.icon,
    price: meta.price,
    nameEn: agent.nameEn,
    descriptionEn: agent.descriptionEn,
    identity: agent.identity,
    identityEn: agent.identityEn,
    systemPrompt: agent.systemPrompt,
    systemPromptEn: agent.systemPromptEn,
    skillIds: agent.skillIds,
  };
}