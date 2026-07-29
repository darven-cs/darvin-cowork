import { ref, watch } from 'vue';
import type { DarvinModelId } from '../../shared/darvin-api';
import { t } from '../services/i18n';

const KEY = 'darvin.model';

const VALID: DarvinModelId[] = ['claude-sonnet-4-5', 'claude-opus-4-5', 'gpt-4o'];

function readStored(): DarvinModelId {
  if (typeof localStorage === 'undefined') return 'claude-sonnet-4-5';
  const raw = localStorage.getItem(KEY) as DarvinModelId | null;
  return raw && VALID.includes(raw) ? raw : 'claude-sonnet-4-5';
}

const currentModel = ref<DarvinModelId>(readStored());

watch(currentModel, (v) => {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(KEY, v);
  }
});

export interface ModelOption {
  id: DarvinModelId;
  label: string;
}

const options: ModelOption[] = [
  { id: 'claude-sonnet-4-5', label: t('model.sonnet') },
  { id: 'claude-opus-4-5',   label: t('model.opus') },
  { id: 'gpt-4o',            label: t('model.gpt4o') },
];

export function useModel() {
  function selectModel(id: DarvinModelId) {
    if (VALID.includes(id)) {
      currentModel.value = id;
    }
  }
  return { currentModel, options, selectModel };
}
