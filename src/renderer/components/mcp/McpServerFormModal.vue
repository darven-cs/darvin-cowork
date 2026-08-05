<script setup lang="ts">
/**
 * MCP server 新增 / 编辑 modal。
 *
 * 按 transportType 切字段：stdio(command + args + env) / http(url + headers)。
 * args / env / headers 用文本框输入（CLI 风格）：
 *   args      空格分隔 → 数组（不带引号 split，按空格 split，简化处理）
 *   env       一行一个 KEY=val
 *   headers   一行一个 KEY=val
 *
 * 编辑既有 server 时把所有字段预填进 form；保存发 emit('save', req)。
 * 父组件决定走 create 还是 update。
 */
import { computed, ref, watch } from 'vue';
import type {
  DarvinMcpServer,
  DarvinMcpServerCreate,
  DarvinMcpServerPatch,
  DarvinMcpTransportType,
} from '../../../shared/darvin-api';
import { t } from '../../services/i18n';
import { parseArgs, parseKv, formatKv } from './mcpForm';

const props = defineProps<{
  open: boolean;
  editing?: DarvinMcpServer | null;
  saving?: boolean;
}>();

const emit = defineEmits<{
  save: [payload: DarvinMcpServerCreate | { id: string; patch: DarvinMcpServerPatch }];
  cancel: [];
}>();

interface FormState {
  name: string;
  description: string;
  enabled: boolean;
  transportType: DarvinMcpTransportType;
  command: string;
  argsStr: string;
  envStr: string;
  url: string;
  headersStr: string;
}

function blankForm(): FormState {
  return {
    name: '',
    description: '',
    enabled: true,
    transportType: 'stdio',
    command: '',
    argsStr: '',
    envStr: '',
    url: '',
    headersStr: '',
  };
}

const form = ref<FormState>(blankForm());

watch(
  () => [props.open, props.editing] as const,
  ([isOpen, editing]) => {
    if (!isOpen) return;
    if (!editing) {
      form.value = blankForm();
      return;
    }
    form.value = {
      name: editing.name,
      description: editing.description ?? '',
      enabled: editing.enabled,
      transportType: editing.transportType,
      command: editing.command ?? '',
      argsStr: (editing.args ?? []).join(' '),
      envStr: formatKv(editing.env ?? {}),
      url: editing.url ?? '',
      headersStr: formatKv(editing.headers ?? {}),
    };
  },
  { immediate: true },
);

const canSave = computed(() => {
  if (!form.value.name.trim()) return false;
  if (form.value.transportType === 'stdio') return form.value.command.trim().length > 0;
  if (form.value.transportType === 'http') return form.value.url.trim().length > 0;
  return false;
});

function onSave(): void {
  if (!canSave.value) return;
  if (form.value.transportType === 'stdio') {
    if (props.editing) {
      emit('save', {
        id: props.editing.id,
        patch: {
          name: form.value.name.trim(),
          description: form.value.description.trim(),
          enabled: form.value.enabled,
          transportType: 'stdio',
          command: form.value.command.trim(),
          args: parseArgs(form.value.argsStr),
          env: parseKv(form.value.envStr),
        },
      });
    } else {
      const req: DarvinMcpServerCreate = {
        name: form.value.name.trim(),
        description: form.value.description.trim() || undefined,
        enabled: form.value.enabled,
        transportType: 'stdio',
        command: form.value.command.trim(),
        args: parseArgs(form.value.argsStr),
        env: parseKv(form.value.envStr),
      };
      emit('save', req);
    }
  } else if (form.value.transportType === 'http') {
    if (props.editing) {
      emit('save', {
        id: props.editing.id,
        patch: {
          name: form.value.name.trim(),
          description: form.value.description.trim(),
          enabled: form.value.enabled,
          transportType: 'http',
          url: form.value.url.trim(),
          headers: parseKv(form.value.headersStr),
        },
      });
    } else {
      const req: DarvinMcpServerCreate = {
        name: form.value.name.trim(),
        description: form.value.description.trim() || undefined,
        enabled: form.value.enabled,
        transportType: 'http',
        url: form.value.url.trim(),
        headers: parseKv(form.value.headersStr),
      };
      emit('save', req);
    }
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="mcp-form-modal"
    >
      <div class="flex max-h-[80vh] w-full max-w-lg flex-col rounded-xl border border-border bg-surface shadow-lg">
        <div class="flex shrink-0 items-center gap-2 border-b border-border p-4">
          <h3 class="font-sans text-sm font-semibold text-text">
            {{ editing ? t('mcp.modal.edit_title') : t('mcp.modal.create_title') }}
          </h3>
        </div>

        <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
          <div class="space-y-1">
            <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.name') }}</label>
            <input
              v-model="form.name"
              type="text"
              class="w-full rounded border border-border bg-bg px-3 py-1.5 font-sans text-sm text-text outline-none transition-colors focus:border-primary"
              data-testid="mcp-form-name"
            />
          </div>

          <div class="space-y-1">
            <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.description') }}</label>
            <textarea
              v-model="form.description"
              rows="2"
              class="w-full resize-none rounded border border-border bg-bg px-3 py-1.5 font-sans text-sm text-text outline-none transition-colors focus:border-primary"
              data-testid="mcp-form-description"
            />
          </div>

          <div class="space-y-1">
            <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.transport_type') }}</label>
            <select
              v-model="form.transportType"
              class="w-full rounded border border-border bg-bg px-3 py-1.5 font-sans text-sm text-text outline-none transition-colors focus:border-primary"
              data-testid="mcp-form-transport"
            >
              <option value="stdio">stdio</option>
              <option value="http">http</option>
              <option value="sse" disabled>sse (v1)</option>
            </select>
          </div>

          <template v-if="form.transportType === 'stdio'">
            <div class="space-y-1">
              <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.command') }}</label>
              <input
                v-model="form.command"
                type="text"
                placeholder="npx"
                class="w-full rounded border border-border bg-bg px-3 py-1.5 font-mono text-sm text-text outline-none transition-colors focus:border-primary"
                data-testid="mcp-form-command"
              />
            </div>
            <div class="space-y-1">
              <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.args') }}</label>
              <input
                v-model="form.argsStr"
                type="text"
                placeholder="-y @scope/pkg@latest"
                class="w-full rounded border border-border bg-bg px-3 py-1.5 font-mono text-sm text-text outline-none transition-colors focus:border-primary"
                data-testid="mcp-form-args"
              />
            </div>
            <div class="space-y-1">
              <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.env') }}</label>
              <textarea
                v-model="form.envStr"
                rows="3"
                placeholder="KEY1=val1&#10;KEY2=val2"
                class="w-full resize-none rounded border border-border bg-bg px-3 py-1.5 font-mono text-sm text-text outline-none transition-colors focus:border-primary"
                data-testid="mcp-form-env"
              />
            </div>
          </template>

          <template v-else-if="form.transportType === 'http'">
            <div class="space-y-1">
              <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.url') }}</label>
              <input
                v-model="form.url"
                type="text"
                placeholder="http://localhost:3001/mcp"
                class="w-full rounded border border-border bg-bg px-3 py-1.5 font-mono text-sm text-text outline-none transition-colors focus:border-primary"
                data-testid="mcp-form-url"
              />
            </div>
            <div class="space-y-1">
              <label class="font-sans text-xs text-text-muted">{{ t('mcp.field.headers') }}</label>
              <textarea
                v-model="form.headersStr"
                rows="3"
                placeholder="Authorization=Bearer xxx"
                class="w-full resize-none rounded border border-border bg-bg px-3 py-1.5 font-mono text-sm text-text outline-none transition-colors focus:border-primary"
                data-testid="mcp-form-headers"
              />
            </div>
          </template>
        </div>

        <div class="flex shrink-0 items-center justify-end gap-2 border-t border-border p-4">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-sans text-xs font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text"
            data-testid="mcp-form-cancel"
            @click="emit('cancel')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="rounded-md bg-primary px-3 py-1.5 font-sans text-xs font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-40"
            :disabled="!canSave || saving"
            data-testid="mcp-form-save"
            @click="onSave"
          >
            {{ t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>