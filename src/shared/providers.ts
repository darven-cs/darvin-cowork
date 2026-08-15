/**
 * LLM provider preset catalog — single source of truth for the settings UI.
 *
 * Aligned 1:1 with LobsterAI's src/shared/providers/constants.ts: the same
 * provider ids, labels, default base URLs, default api formats, switchable
 * anthropic/openai endpoints, and default model lists. Each provider can
 * speak more than one wire format (anthropic / openai / gemini); the active
 * format + base URL are stored in the user config as providers.<id>.
 *
 * The Go agent resolves each preset onto a wire via providers.<id>.api_format:
 *   - "anthropic" → internal/llm/anthropic (Messages API, {base}/v1/messages)
 *   - "openai"    → internal/llm/openai   (chat.completions)
 *   - "gemini"    → internal/llm/gemini   (native generateContent)
 */

export type DarvinApiFormat = 'anthropic' | 'openai' | 'gemini';

export type DarvinProviderRegion = 'china' | 'global';

export interface DarvinProviderModel {
  id: string;
  label: string;
  supportsImage?: boolean;
  supportsThinking?: boolean;
  /** contextWindow in tokens; used when the Go model registry has no descriptor. */
  contextWindow?: number;
}

export interface DarvinProviderPreset {
  /** Config key / llm.provider value. */
  id: string;
  /** Human-readable name shown in the settings dropdown. */
  label: string;
  /** Default wire protocol (matches LobsterAI defaultApiFormat). */
  apiFormat: DarvinApiFormat;
  /** Default endpoint auto-filled when the preset is selected. */
  defaultBaseUrl: string;
  /** Base URL to use when switching apiFormat (LobsterAI switchableBaseUrls). */
  switchableBaseUrls?: Partial<Record<DarvinApiFormat, string>>;
  /** Whether the API key input is mandatory for this preset. */
  apiKeyRequired: boolean;
  /** Whether a base URL is mandatory (custom endpoints). */
  requiresBaseUrl: boolean;
  /** Input placeholder for the API key field. */
  apiKeyPlaceholder: string;
  /** Provider console URL (shown as a hint). */
  website?: string;
  /** Fallback model list when the Go model catalog is unavailable. */
  defaultModels: DarvinProviderModel[];
  /** UI grouping: domestic (china) vs global providers. */
  region: DarvinProviderRegion;
}

export const DARVIN_PROVIDERS: DarvinProviderPreset[] = [
  // ── China ──
  {
    id: 'deepseek',
    label: 'DeepSeek',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.deepseek.com',
    switchableBaseUrls: {
      anthropic: 'https://api.deepseek.com/anthropic',
      openai: 'https://api.deepseek.com',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-...',
    website: 'https://platform.deepseek.com',
    defaultModels: [
      { id: 'deepseek-v4-flash', label: 'DeepSeek V4 Flash', supportsThinking: true, contextWindow: 1000000 },
      { id: 'deepseek-v4-pro', label: 'DeepSeek V4 Pro', supportsThinking: true, contextWindow: 1000000 },
      { id: 'deepseek-reasoner', label: 'DeepSeek Reasoner', supportsThinking: true },
    ],
    region: 'china',
  },
  {
    id: 'moonshot',
    label: 'Moonshot',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.moonshot.cn/v1',
    switchableBaseUrls: {
      anthropic: 'https://api.moonshot.cn/anthropic',
      openai: 'https://api.moonshot.cn/v1',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-...',
    website: 'https://platform.moonshot.cn',
    defaultModels: [
      { id: 'kimi-k2.6', label: 'Kimi K2.6', supportsImage: true, supportsThinking: true, contextWindow: 262144 },
      { id: 'kimi-k2.5', label: 'Kimi K2.5', supportsImage: true, supportsThinking: true, contextWindow: 262144 },
    ],
    region: 'china',
  },
  {
    id: 'qwen',
    label: 'Qwen',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://dashscope.aliyuncs.com/apps/anthropic',
    switchableBaseUrls: {
      anthropic: 'https://dashscope.aliyuncs.com/apps/anthropic',
      openai: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-...',
    website: 'https://dashscope.console.aliyun.com',
    defaultModels: [
      { id: 'qwen3.6-plus', label: 'Qwen3.6 Plus', supportsImage: true, contextWindow: 1000000 },
      { id: 'qwen3.5-plus', label: 'Qwen3.5 Plus', supportsImage: true, contextWindow: 1000000 },
    ],
    region: 'china',
  },
  {
    id: 'zhipu',
    label: 'Zhipu',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://open.bigmodel.cn/api/anthropic',
    switchableBaseUrls: {
      anthropic: 'https://open.bigmodel.cn/api/anthropic',
      openai: 'https://open.bigmodel.cn/api/paas/v4',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://open.bigmodel.cn',
    defaultModels: [
      { id: 'glm-5.1', label: 'GLM 5.1', supportsThinking: true, contextWindow: 202800 },
      { id: 'glm-5', label: 'GLM 5', supportsThinking: true, contextWindow: 202800 },
      { id: 'glm-4.7', label: 'GLM 4.7', supportsThinking: true, contextWindow: 204800 },
    ],
    region: 'china',
  },
  {
    id: 'minimax',
    label: 'MiniMax',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://api.minimaxi.com/anthropic',
    switchableBaseUrls: {
      anthropic: 'https://api.minimaxi.com/anthropic',
      openai: 'https://api.minimaxi.com/v1',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://platform.minimaxi.com',
    defaultModels: [
      { id: 'MiniMax-M3', label: 'MiniMax M3', supportsImage: true, supportsThinking: true, contextWindow: 1000000 },
      { id: 'MiniMax-M2.7', label: 'MiniMax M2.7', contextWindow: 204800 },
      { id: 'MiniMax-M2.5', label: 'MiniMax M2.5', contextWindow: 204800 },
    ],
    region: 'china',
  },
  {
    id: 'volcengine',
    label: 'Volcengine',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://ark.cn-beijing.volces.com/api/compatible',
    switchableBaseUrls: {
      anthropic: 'https://ark.cn-beijing.volces.com/api/compatible',
      openai: 'https://ark.cn-beijing.volces.com/api/v3',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://console.volcengine.com/ark',
    defaultModels: [
      { id: 'doubao-seed-2-0-pro-260215', label: 'Doubao-Seed-2.0-pro', supportsImage: true, supportsThinking: true },
      { id: 'ark-code-latest', label: 'Auto', supportsImage: true, supportsThinking: true },
      { id: 'doubao-seed-2-0-lite-260215', label: 'Doubao-Seed-2.0-lite', supportsImage: true, supportsThinking: true },
    ],
    region: 'china',
  },
  {
    id: 'youdaozhiyun',
    label: 'Youdao',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://openapi.youdao.com/llmgateway/api/v1/chat/completions',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://ai.youdao.com',
    defaultModels: [
      { id: 'deepseek-reasoner', label: 'DeepSeek Reasoner', supportsThinking: true },
      { id: 'deepseek-inhouse-reasoner', label: 'DeepSeek Reasoner (安全)', supportsThinking: true },
    ],
    region: 'china',
  },
  {
    id: 'qianfan',
    label: 'Qianfan',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://qianfan.baidubce.com/v2',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://console.bce.baidu.com/qianfan',
    defaultModels: [
      { id: 'kimi-k2.5', label: 'Kimi K2.5' },
      { id: 'glm-5.1', label: 'GLM 5.1', supportsThinking: true },
      { id: 'minimax-m2.5', label: 'MiniMax M2.5' },
      { id: 'deepseek-v4-flash', label: 'DeepSeek V4 Flash', supportsThinking: true, contextWindow: 1000000 },
      { id: 'ernie-4.5-turbo-20260402', label: 'ERNIE 4.5 Turbo' },
    ],
    region: 'china',
  },
  {
    id: 'stepfun',
    label: 'StepFun',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.stepfun.com/v1',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://platform.stepfun.com',
    defaultModels: [{ id: 'step-3.5-flash', label: 'Step 3.5 Flash' }],
    region: 'china',
  },
  {
    id: 'xiaomi',
    label: 'Xiaomi',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.xiaomimimo.com/v1/chat/completions',
    switchableBaseUrls: {
      anthropic: 'https://api.xiaomimimo.com/anthropic',
      openai: 'https://api.xiaomimimo.com/v1/chat/completions',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '...',
    website: 'https://dev.mi.com/platform',
    defaultModels: [
      { id: 'mimo-v2.5-pro', label: 'MiMo V2.5 Pro', supportsThinking: true, contextWindow: 1000000 },
      { id: 'mimo-v2.5', label: 'MiMo V2.5', supportsImage: true, supportsThinking: true, contextWindow: 1000000 },
    ],
    region: 'china',
  },
  {
    id: 'ollama',
    label: 'Ollama',
    apiFormat: 'openai',
    defaultBaseUrl: 'http://localhost:11434/v1',
    switchableBaseUrls: {
      anthropic: 'http://localhost:11434',
      openai: 'http://localhost:11434/v1',
    },
    apiKeyRequired: false,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '本地免 key',
    website: 'https://ollama.com',
    defaultModels: [
      { id: 'qwen3-coder-next', label: 'Qwen3-Coder-Next' },
      { id: 'glm-4.7-flash', label: 'GLM 4.7 Flash' },
    ],
    region: 'china',
  },
  {
    id: 'lm-studio',
    label: 'LM Studio',
    apiFormat: 'openai',
    defaultBaseUrl: 'http://localhost:1234/v1',
    switchableBaseUrls: {
      anthropic: 'http://localhost:1234',
      openai: 'http://localhost:1234/v1',
    },
    apiKeyRequired: false,
    requiresBaseUrl: false,
    apiKeyPlaceholder: '本地免 key',
    website: 'https://lmstudio.ai',
    defaultModels: [],
    region: 'china',
  },
  // ── Global ──
  {
    id: 'copilot',
    label: 'GitHub Copilot',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.individual.githubcopilot.com',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'OAuth token',
    website: 'https://github.com/settings/copilot',
    defaultModels: [
      { id: 'gpt-5-mini', label: 'GPT-5 mini', supportsImage: true },
      { id: 'claude-haiku-4.5', label: 'Claude Haiku 4.5', supportsImage: true },
      { id: 'gpt-4.1', label: 'GPT-4.1', supportsImage: true },
      { id: 'gpt-4o', label: 'GPT-4o', supportsImage: true },
    ],
    region: 'global',
  },
  {
    id: 'openai',
    label: 'OpenAI',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.openai.com/v1',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-...',
    website: 'https://platform.openai.com',
    defaultModels: [
      { id: 'gpt-5.4', label: 'GPT-5.4', supportsImage: true, supportsThinking: true },
      { id: 'gpt-5.5', label: 'GPT-5.5', supportsImage: true, supportsThinking: true },
    ],
    region: 'global',
  },
  {
    id: 'gemini',
    label: 'Gemini',
    apiFormat: 'gemini',
    defaultBaseUrl: 'https://generativelanguage.googleapis.com/v1beta',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'AIza...',
    website: 'https://aistudio.google.com',
    defaultModels: [
      { id: 'gemini-3.1-pro-preview', label: 'Gemini 3.1 Pro', supportsImage: true, supportsThinking: true },
      { id: 'gemini-3-flash-preview', label: 'Gemini 3 Flash', supportsImage: true, supportsThinking: true },
      { id: 'gemini-3.1-flash-lite', label: 'Gemini 3.1 Flash Lite', supportsImage: true, supportsThinking: true },
    ],
    region: 'global',
  },
  {
    id: 'xai',
    label: 'xAI (Grok)',
    apiFormat: 'openai',
    defaultBaseUrl: 'https://api.x.ai/v1',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'xai-...',
    website: 'https://console.x.ai',
    defaultModels: [
      { id: 'grok-4.3', label: 'Grok 4.3', supportsImage: true, supportsThinking: true, contextWindow: 1000000 },
      { id: 'grok-build-0.1', label: 'Grok Build 0.1', supportsImage: true, supportsThinking: true, contextWindow: 256000 },
    ],
    region: 'global',
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://api.anthropic.com',
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-ant-...',
    website: 'https://console.anthropic.com',
    defaultModels: [
      { id: 'claude-opus-4-7', label: 'Claude Opus 4.7', supportsImage: true, supportsThinking: true, contextWindow: 1048576 },
      { id: 'claude-opus-4-6', label: 'Claude Opus 4.6', supportsImage: true, supportsThinking: true, contextWindow: 1048576 },
      { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6', supportsImage: true, supportsThinking: true, contextWindow: 1048576 },
    ],
    region: 'global',
  },
  {
    id: 'openrouter',
    label: 'OpenRouter',
    apiFormat: 'anthropic',
    defaultBaseUrl: 'https://openrouter.ai/api',
    switchableBaseUrls: {
      anthropic: 'https://openrouter.ai/api',
      openai: 'https://openrouter.ai/api/v1',
    },
    apiKeyRequired: true,
    requiresBaseUrl: false,
    apiKeyPlaceholder: 'sk-or-...',
    website: 'https://openrouter.ai',
    defaultModels: [
      { id: 'anthropic/claude-sonnet-4.6', label: 'Claude Sonnet 4.6', supportsImage: true, supportsThinking: true },
      { id: 'anthropic/claude-opus-4.7', label: 'Claude Opus 4.7', supportsImage: true, supportsThinking: true },
      { id: 'openai/gpt-5.5', label: 'GPT 5.5', supportsImage: true, supportsThinking: true },
      { id: 'google/gemini-3.1-pro-preview', label: 'Gemini 3.1 Pro', supportsImage: true, supportsThinking: true },
    ],
    region: 'global',
  },
  {
    id: 'custom',
    label: 'Custom (OpenAI-compatible)',
    apiFormat: 'openai',
    defaultBaseUrl: '',
    apiKeyRequired: false,
    requiresBaseUrl: true,
    apiKeyPlaceholder: '可选，本地网关可留空',
    defaultModels: [
      { id: 'gpt-4o-mini', label: 'GPT-4o mini' },
      { id: 'deepseek-chat', label: 'DeepSeek V3' },
      { id: 'qwen-max', label: 'Qwen Max' },
    ],
    region: 'global',
  },
];

export function darvinProviderPreset(id: string): DarvinProviderPreset | undefined {
  return DARVIN_PROVIDERS.find((p) => p.id === id);
}

export function darvinProviderModels(id: string): DarvinProviderModel[] {
  return darvinProviderPreset(id)?.defaultModels ?? [];
}
