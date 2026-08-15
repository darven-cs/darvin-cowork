/**
 * 模型选择状态：选项来自 Go 模型目录（getLLMModels）+ 共享目录 defaultModels，
 * 过滤到「已配置的 provider」。currentModel 持久化到 localStorage；选中即记，
 * 发消息时按该模型的 provider + model 作为按次覆盖传给 Go。
 */
import { ref, watch } from 'vue';
import type { DarvinModelInfo } from '../../shared/darvin-api';
import { DARVIN_PROVIDERS } from '../../shared/providers';
import { mockModels } from '../services/mock-data';

const KEY = 'darvin.model';
const DEFAULT_MODEL = 'claude-sonnet-4-6';

export interface ModelOption {
  id: string;
  label: string;
  /** provider preset key（anthropic / openai / deepseek / ...），发消息时随 prompt 带上。 */
  provider: string;
  description: string;
  contextWindow?: number;
}

function readStored(): string {
  if (typeof localStorage === 'undefined') return DEFAULT_MODEL;
  return localStorage.getItem(KEY) || DEFAULT_MODEL;
}

const currentModel = ref<string>(readStored());
const options = ref<ModelOption[]>([]);
const loaded = ref(false);

watch(currentModel, (v) => {
  if (typeof localStorage !== 'undefined') localStorage.setItem(KEY, v);
});

/** 无 Go 目录时的兜底：共享目录 defaultModels（含 mock-data 兼容）。 */
function fallbackOptions(configuredKeys: Set<string>): ModelOption[] {
  const out: ModelOption[] = [];
  for (const p of DARVIN_PROVIDERS) {
    if (!configuredKeys.has(p.id)) continue;
    for (const m of p.defaultModels) {
      out.push({
        id: m.id,
        label: m.label,
        provider: p.id,
        description: p.label,
        contextWindow: m.contextWindow,
      });
    }
  }
  for (const m of mockModels) {
    out.push({
      id: m.id,
      label: m.label,
      provider: m.vendor,
      description: m.description,
    });
  }
  return out;
}

/**
 * 拉取 Go 模型目录 + 当前 LLM 配置，构建选项。configuredKeys = 已存凭据的
 * provider + 当前 active；Go 目录优先，缺描述符的 preset 回落共享目录。
 */
async function loadModels(): Promise<void> {
  const [goModels, cfg] = await Promise.all([
    window.darvin.getLLMModels().catch(() => [] as DarvinModelInfo[]),
    window.darvin.getLLMConfig().catch(() => null),
  ]);
  const configured = new Set<string>(
    cfg ? [...Object.keys(cfg.providers ?? {}), cfg.activeProvider] : ['anthropic'],
  );
  const goByProvider = new Map<string, DarvinModelInfo[]>();
  for (const m of goModels) {
    const arr = goByProvider.get(m.provider) ?? [];
    arr.push(m);
    goByProvider.set(m.provider, arr);
  }

  const out: ModelOption[] = [];
  for (const p of DARVIN_PROVIDERS) {
    if (!configured.has(p.id)) continue;
    const go = goByProvider.get(p.id) ?? [];
    const seen = new Set<string>();
    for (const m of go) {
      out.push({
        id: m.id,
        label: m.name,
        provider: m.provider,
        description: p.label,
        contextWindow: m.contextWindow,
      });
      seen.add(m.id);
    }
    for (const m of p.defaultModels) {
      if (seen.has(m.id)) continue;
      out.push({
        id: m.id,
        label: m.label,
        provider: p.id,
        description: p.label,
        contextWindow: m.contextWindow,
      });
    }
  }
  options.value = out.length > 0 ? out : fallbackOptions(configured);
  loaded.value = true;
}

export function useModel() {
  function selectModel(id: string): void {
    currentModel.value = id;
  }
  /** 当前选中模型的 provider（发消息时作按次覆盖）。 */
  function providerOf(id: string): string {
    return options.value.find((m) => m.id === id)?.provider ?? '';
  }
  return { currentModel, options, loaded, selectModel, providerOf, loadModels };
}
