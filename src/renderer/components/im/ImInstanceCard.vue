<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue';
import { showToast } from '../../services/toast';
import { t } from '../../services/i18n';
import { useIm } from '../../composables/useIm';
import type { DarvinIMCheck, DarvinIMInstance, DarvinIMLoginResult } from '../../../shared/darvin-api';
import { toDataURL } from 'qrcode';
import Icon from '../common/Icon.vue';

const props = defineProps<{
  instances: DarvinIMInstance[];
  channel: string;
  needsQr: boolean;
  creating: boolean;
}>();
const emit = defineEmits<{
  'cancel-create': [];
  'submit-create': [config: Record<string, unknown>];
  'created': [];
}>();

const { update, remove, toggle, test, loginStart, loginPoll, create, loadAll } = useIm();

const editingId = ref<string | null>(null);
const editing = reactive<Record<string, string>>({});
const originalEditing = ref<Record<string, string>>({});
const saving = ref(false);

const adding = reactive<Record<string, string>>({});
const showSecretEdit = ref(false);
const showSecretAdd = ref(false);

const dirty = ref<Record<string, boolean>>({});

const testResult = ref<{ ok: boolean; error?: string; checks?: DarvinIMCheck[] } | null>(null);
const testSource = ref<DarvinIMInstance | null>(null);

const confirmDelete = ref<DarvinIMInstance | null>(null);

const renamingId = ref<string | null>(null);
const nameDraft = ref('');

const STATUS_KEY: Record<string, string> = {
  connected: 'im.status.connected',
  disconnected: 'im.status.disconnected',
  error: 'im.status.error',
  stopped: 'im.status.stopped',
  login_expired: 'im.status.login_expired',
};
const STATUS_CLASS: Record<string, string> = {
  connected: 'bg-success/15 text-success',
  disconnected: 'bg-warning/15 text-warning',
  error: 'bg-danger/15 text-danger',
  stopped: 'bg-surface-hover text-text-muted',
  login_expired: 'bg-danger/15 text-danger',
};

// weixin QR (LobsterAI-style status machine: idle/loading/showing/success/error)
const qrStatus = ref<'idle' | 'loading' | 'showing' | 'success' | 'error'>('idle');
const qrState = ref<DarvinIMLoginResult | null>(null);
const qrBusy = ref(false);
const qrDataUrl = ref('');
const qrExpiresIn = ref(0);
// 防重入：confirmed 只 autoCreate 一次 + 一次只跑一个长轮询
let confirming = false;
let qrTimer: ReturnType<typeof setInterval> | null = null;
let countdownTimer: ReturnType<typeof setTimeout> | null = null;

function stopQrPoll(): void {
  if (qrTimer) {
    clearTimeout(qrTimer);
    qrTimer = null;
  }
  if (countdownTimer) {
    clearTimeout(countdownTimer);
    countdownTimer = null;
  }
}

async function autoCreateWeixin(): Promise<void> {
  if (!qrState.value?.token) {
    qrStatus.value = 'error';
    return;
  }
  const inst = await create({
    channel: 'weixin',
    name: '',
    config: {
      botToken: qrState.value.token,
      botId: qrState.value.botId ?? '',
    },
    enabled: true,
  });
  if (inst) {
    void loadAll();
    emit('created');
  }
}

async function pollQr(): Promise<void> {
  if (!qrState.value?.sessionId) return;
  try {
    const res = await loginPoll({ sessionId: qrState.value.sessionId });
    qrState.value = { ...qrState.value, ...res };
    if (res.state === 'confirmed') {
      // 长轮询可能并发返回多个 confirmed；一次只 autoCreate 一次
      if (confirming) return;
      confirming = true;
      stopQrPoll();
      await autoCreateWeixin();
      qrStatus.value = 'success';
    } else if (res.state === 'expired' || res.state === 'cancelled') {
      stopQrPoll();
      confirming = false;
      qrStatus.value = 'error';
    } else {
      // 自调度：上一次长轮询结束（未确认）后再排下一个，避免 setInterval 堆叠并发
      qrTimer = setTimeout(() => void pollQr(), 2000);
    }
  } catch (e) {
    stopQrPoll();
    confirming = false;
    qrStatus.value = 'error';
    showToast(t('im.toast.test_failed', { error: (e as Error).message }), 'error');
  }
}

onUnmounted(() => {
  stopQrPoll();
  warnDiscard();
});

// 判定编辑面板相对已落库值的改动；切换平台/关闭时据此提示未保存
watch(editing, () => {
  if (editingId.value) {
    dirty.value[editingId.value] = editingDirty();
  }
}, { deep: true });

// 组件在平台 tab 切换时保持挂载（ImView 只换 channel prop），需在此提示丢弃
watch(() => props.channel, () => {
  warnDiscard();
  editingId.value = null;
});

function editingDirty(): boolean {
  const o = originalEditing.value;
  return Object.keys(o).some((k) => (editing[k] ?? '') !== (o[k] ?? ''));
}

function warnDiscard(): void {
  if (editingId.value && dirty.value[editingId.value]) {
    showToast(t('im.dirty.discard'), 'info');
    delete dirty.value[editingId.value];
  }
}

function statusLabel(s: string): string {
  return t(STATUS_KEY[s] ?? 'im.status.stopped');
}

async function onToggle(inst: DarvinIMInstance): Promise<void> {
  await toggle(inst.id, !inst.enabled);
}

function requestDelete(inst: DarvinIMInstance): void {
  confirmDelete.value = inst;
}

async function confirmDeleteInstance(): Promise<void> {
  const inst = confirmDelete.value;
  confirmDelete.value = null;
  if (!inst) return;
  await remove(inst.id);
}

function startEdit(inst: DarvinIMInstance): void {
  warnDiscard();
  editingId.value = inst.id;
  const snapshot = {
    name: inst.name ?? '',
    appId: (inst.config?.appId as string) ?? '',
    appSecret: (inst.config?.appSecret as string) ?? '',
    botId: (inst.config?.botId as string) ?? '',
    secret: (inst.config?.secret as string) ?? '',
    accessMode: inst.accessMode ?? 'open',
    allowFrom: (inst.allowFrom ?? []).join(','),
  };
  originalEditing.value = { ...snapshot };
  Object.assign(editing, snapshot);
  showSecretEdit.value = false;
}

function cancelEdit(): void {
  warnDiscard();
  editingId.value = null;
}

async function onSave(inst: DarvinIMInstance): Promise<void> {
  saving.value = true;
  try {
    const config: Record<string, unknown> = {};
    if (props.channel === 'qq') {
      config.appId = editing.appId;
      config.appSecret = editing.appSecret;
    } else if (props.channel === 'wecom') {
      config.botId = editing.botId;
      config.secret = editing.secret;
    }
    await update(inst.id, {
      name: editing.name,
      config_json: JSON.stringify(config),
      access_mode: editing.accessMode,
      allow_from: editing.allowFrom,
    });
    originalEditing.value = { ...editing };
    dirty.value[inst.id] = false;
    editingId.value = null;
  } finally {
    saving.value = false;
  }
}

function markDirty(): void {
  if (editingId.value) dirty.value[editingId.value] = true;
}

async function onTest(inst: DarvinIMInstance): Promise<void> {
  const config: Record<string, unknown> = {};
  if (props.channel === 'qq') {
    config.appId = editing.appId;
    config.appSecret = editing.appSecret;
  } else if (props.channel === 'wecom') {
    config.botId = editing.botId;
    config.secret = editing.secret;
  }
  testSource.value = inst;
  const res = await test(props.channel, config);
  testResult.value = res;
}

function testVerdict(): 'pass' | 'warn' | 'fail' {
  const checks = testResult.value?.checks ?? [];
  if (checks.some((c) => c.level === 'fail')) return 'fail';
  if (checks.some((c) => c.level === 'warn')) return 'warn';
  return checks.length > 0 ? 'pass' : (testResult.value?.ok ? 'pass' : 'fail');
}

async function confirmEnableFromTest(): Promise<void> {
  const inst = testSource.value;
  testResult.value = null;
  testSource.value = null;
  if (inst && !inst.enabled) {
    await toggle(inst.id, true);
  }
}

function closeTest(): void {
  testResult.value = null;
  testSource.value = null;
}

function beginRename(inst: DarvinIMInstance): void {
  renamingId.value = inst.id;
  nameDraft.value = inst.name || inst.channel;
}

async function commitRename(inst: DarvinIMInstance): Promise<void> {
  renamingId.value = null;
  const name = nameDraft.value.trim() || inst.channel;
  await update(inst.id, { name });
}

async function beginQr(): Promise<void> {
  qrBusy.value = true;
  qrStatus.value = 'loading';
  qrState.value = null;
  qrDataUrl.value = '';
  confirming = false;
  stopQrPoll();
  try {
    const res = await loginStart({
      channel: props.channel,
      instanceId: '',
    });
    qrState.value = res;
    if (res.qrUrl) {
      // iLink 的 qrcode_img_content 是个 HTML 页面（X-Frame-Options 禁 iframe），
      // 不是图片；本地用 qrcode 库把它的 URL 渲染成可扫二维码。
      try {
        qrDataUrl.value = await toDataURL(res.qrUrl, { width: 320, margin: 1 });
        qrStatus.value = 'showing';
      } catch {
        qrStatus.value = 'error';
      }
    } else {
      qrStatus.value = 'error';
    }
    qrExpiresIn.value = res.expiresIn ? Math.max(0, Math.round(res.expiresIn / 1000)) : 120;
    if (res.sessionId) {
      // 自调度长轮询（一次只发一个 loginPoll），避免 setInterval 堆叠并发 confirmed
      void pollQr();
      countdownTimer = setTimeout(function tick() {
        qrExpiresIn.value -= 1;
        if (qrExpiresIn.value <= 0) {
          stopQrPoll();
          qrStatus.value = 'error';
          return;
        }
        countdownTimer = setTimeout(tick, 1000);
      }, 1000);
    }
  } catch (e) {
    qrStatus.value = 'error';
    showToast(t('im.toast.test_failed', { error: (e as Error).message }), 'error');
  } finally {
    qrBusy.value = false;
  }
}

async function submitCreate(): Promise<void> {
  const config: Record<string, unknown> = {};
  if (props.channel === 'qq') {
    config.appId = adding.appId;
    config.appSecret = adding.appSecret;
  } else if (props.channel === 'wecom') {
    config.botId = adding.botId;
    config.secret = adding.secret;
  } else if (props.channel === 'weixin') {
    // 扫码确认后把 iLink token 写进实例 config，否则空 config 的连接器
    // 因缺 botToken 起不来，WeChat 端看不到在线连接。
    if (qrState.value?.token) config.botToken = qrState.value.token;
    if (qrState.value?.botId) config.botId = qrState.value.botId;
  }
  emit('submit-create', config);
}

const testVerdictClass: Record<'pass' | 'warn' | 'fail', string> = {
  pass: 'text-success',
  warn: 'text-warning',
  fail: 'text-danger',
};

const testVerdictKey = computed(() => `im.test.verdict.${testVerdict()}`);

const showEnableFromTest = computed(
  () =>
    testVerdict() === 'pass' &&
    !!testSource.value &&
    !testSource.value.enabled &&
    !!testResult.value,
);
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- create form -->
    <div v-if="creating" class="rounded-lg border border-border bg-surface p-4">
      <div class="mb-3 text-sm font-medium text-text">{{ t('im.add') }}</div>

      <!-- weixin: LobsterAI-style auto-save QR panel -->
      <div v-if="channel === 'weixin'" class="rounded-lg border border-dashed border-border p-4 text-center space-y-3">
        <template v-if="qrStatus === 'idle' || qrStatus === 'error'">
          <button
            type="button"
            class="rounded-lg bg-primary px-4 py-2.5 text-sm font-medium text-white disabled:opacity-50"
            :disabled="qrBusy"
            @click="beginQr"
          >
            {{ t('im.form.weixin.scan') }}
          </button>
          <p class="text-xs text-text-muted">{{ t('im.qr.hint') }}</p>
          <p class="text-xs text-text-muted">{{ t('im.qr.auto_save') }}</p>
          <div v-if="qrStatus === 'error'" class="mx-auto flex items-center gap-1.5 rounded-lg bg-danger/10 px-3 py-2 text-xs text-danger">
            <Icon name="alert-circle" :size="14" />
            <span>{{ t('im.qr.expired') }}</span>
          </div>
          <button
            v-if="qrStatus === 'error'"
            type="button"
            class="rounded-lg bg-surface px-3 py-1.5 text-xs text-text"
            @click="beginQr"
          >
            {{ t('im.qr.regenerate') }}
          </button>
        </template>

        <div v-if="qrStatus === 'loading'" class="flex flex-col items-center gap-2 py-2">
          <Icon name="refresh" :size="22" class="animate-spin text-primary" />
          <span class="text-xs text-text-muted">{{ t('im.qr.generating') }}</span>
        </div>

        <div v-if="qrStatus === 'showing' && qrDataUrl" class="flex flex-col items-center gap-2">
          <div class="inline-block rounded-lg bg-white p-2">
            <img :src="qrDataUrl" class="h-40 w-40 object-contain" alt="weixin qr" />
          </div>
          <p class="max-w-[240px] text-xs text-text-muted">{{ t('im.qr.scan_prompt') }}</p>
          <p class="text-xs text-text-muted">{{ t('im.qr.expires_in', { seconds: String(qrExpiresIn) }) }}</p>
          <div class="flex items-center gap-2 pt-1">
            <button
              type="button"
              class="rounded-lg bg-surface px-3 py-1.5 text-xs text-text"
              @click="beginQr"
            >
              {{ t('im.qr.regenerate') }}
            </button>
            <button
              type="button"
              class="rounded-lg bg-surface px-3 py-1.5 text-xs text-text-muted"
              @click="qrStatus = 'idle'; stopQrPoll()"
            >
              {{ t('im.action.cancel') }}
            </button>
          </div>
        </div>

        <div v-if="qrStatus === 'success'" class="mx-auto flex items-center justify-center gap-1.5 rounded-lg bg-success/10 px-3 py-2 text-xs text-success">
          <Icon name="check" :size="14" />
          <span>{{ t('im.qr.success') }}</span>
        </div>
      </div>

      <!-- qq / wecom: manual credential form + save -->
      <template v-if="channel === 'qq' || channel === 'wecom'">
        <div class="flex flex-col gap-3">
          <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.qq.appId') }}
            <input v-model="adding.appId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
          </label>
          <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.qq.appSecret') }}
            <div class="relative">
              <input
                v-model="adding.appSecret"
                :type="showSecretAdd ? 'text' : 'password'"
                class="w-full rounded-md border border-border bg-bg px-2 py-1.5 pr-16 text-sm text-text"
              />
              <div class="absolute inset-y-0 right-0 flex items-center pr-1">
                <button
                  type="button"
                  class="p-1 text-text-muted hover:text-text"
                  :aria-label="showSecretAdd ? t('im.action.hide_secret') : t('im.action.show_secret')"
                  @click="showSecretAdd = !showSecretAdd"
                >
                  <Icon :name="showSecretAdd ? 'eye-off' : 'eye'" :size="16" />
                </button>
                <button
                  type="button"
                  class="p-1 text-text-muted hover:text-text"
                  :aria-label="t('im.action.clear')"
                  @click="adding.appSecret = ''"
                >
                  <Icon name="x" :size="15" />
                </button>
              </div>
            </div>
          </label>
          <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.wecom.botId') }}
            <input v-model="adding.botId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
          </label>
          <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.wecom.secret') }}
            <div class="relative">
              <input
                v-model="adding.secret"
                :type="showSecretAdd ? 'text' : 'password'"
                class="w-full rounded-md border border-border bg-bg px-2 py-1.5 pr-16 text-sm text-text"
              />
              <div class="absolute inset-y-0 right-0 flex items-center pr-1">
                <button
                  type="button"
                  class="p-1 text-text-muted hover:text-text"
                  :aria-label="showSecretAdd ? t('im.action.hide_secret') : t('im.action.show_secret')"
                  @click="showSecretAdd = !showSecretAdd"
                >
                  <Icon :name="showSecretAdd ? 'eye-off' : 'eye'" :size="16" />
                </button>
                <button
                  type="button"
                  class="p-1 text-text-muted hover:text-text"
                  :aria-label="t('im.action.clear')"
                  @click="adding.secret = ''"
                >
                  <Icon name="x" :size="15" />
                </button>
              </div>
            </div>
          </label>
        </div>
        <div class="mt-4 flex gap-2">
          <button class="rounded-md bg-primary px-3 py-1.5 text-sm text-white" @click="submitCreate">
            {{ t('im.form.enable') }}
          </button>
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="emit('cancel-create')">
            {{ t('im.action.cancel') }}
          </button>
        </div>
      </template>
    </div>

    <!-- instance cards -->
    <div v-if="instances.length === 0 && !creating" class="text-sm text-text-muted">
      {{ t('im.list.empty') }}
    </div>

    <div
      v-for="inst in instances"
      :key="inst.id"
      class="rounded-lg border border-border bg-surface p-4"
    >
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Icon name="message-square" :size="16" class="text-text-muted" />
          <template v-if="renamingId === inst.id">
            <input
              v-model="nameDraft"
              class="rounded-md border border-border bg-bg px-2 py-1 text-sm text-text"
              :placeholder="t('im.rename.placeholder')"
              @keydown.enter="commitRename(inst)"
              @keydown.esc="renamingId = null"
              @blur="commitRename(inst)"
            />
          </template>
          <template v-else>
            <span
              class="cursor-pointer text-sm font-medium text-text hover:underline"
              :title="t('im.rename.placeholder')"
              @click="beginRename(inst)"
            >{{ inst.name || inst.channel }}</span>
          </template>
          <span v-if="dirty[inst.id]" class="rounded-full bg-warning/15 px-2 py-0.5 text-xs text-warning">
            {{ t('im.dirty.unsaved') }}
          </span>
          <span class="rounded-full px-2 py-0.5 text-xs" :class="STATUS_CLASS[inst.status.state] ?? STATUS_CLASS.stopped">
            {{ statusLabel(inst.status.state) }}
          </span>
        </div>
        <div class="flex items-center gap-2">
          <label class="flex cursor-pointer items-center gap-1 text-xs text-text-muted">
            <input type="checkbox" class="accent-primary" :checked="inst.enabled" @change="onToggle(inst)" />
            {{ t('im.form.enable') }}
          </label>
          <button class="text-xs text-text-muted hover:text-text" @click="startEdit(inst)">
            {{ t('im.action.save') }}
          </button>
          <button class="text-xs text-danger" @click="requestDelete(inst)">
            {{ t('im.action.delete') }}
          </button>
        </div>
      </div>

      <!-- lastError 红条 -->
      <div
        v-if="inst.status?.lastError"
        class="mt-2 flex items-start gap-1.5 rounded-md bg-danger/10 px-3 py-2 text-xs text-danger"
      >
        <Icon name="alert-circle" :size="14" class="mt-0.5 shrink-0" />
        <span class="break-all">{{ inst.status.lastError }}</span>
      </div>

      <div v-if="editingId === inst.id" class="mt-3 flex flex-col gap-3 border-t border-border pt-3">
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.name') }}
          <input v-model="editing.name" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" @input="markDirty" />
        </label>
        <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.qq.appId') }}
          <input v-model="editing.appId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" @input="markDirty" />
        </label>
        <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.qq.appSecret') }}
          <div class="relative">
            <input
              v-model="editing.appSecret"
              :type="showSecretEdit ? 'text' : 'password'"
              class="w-full rounded-md border border-border bg-bg px-2 py-1.5 pr-16 text-sm text-text"
              @input="markDirty"
            />
            <div class="absolute inset-y-0 right-0 flex items-center pr-1">
              <button
                type="button"
                class="p-1 text-text-muted hover:text-text"
                :aria-label="showSecretEdit ? t('im.action.hide_secret') : t('im.action.show_secret')"
                @click="showSecretEdit = !showSecretEdit"
              >
                <Icon :name="showSecretEdit ? 'eye-off' : 'eye'" :size="16" />
              </button>
              <button
                type="button"
                class="p-1 text-text-muted hover:text-text"
                :aria-label="t('im.action.clear')"
                @click="editing.appSecret = ''; markDirty()"
              >
                <Icon name="x" :size="15" />
              </button>
            </div>
          </div>
        </label>
        <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.wecom.botId') }}
          <input v-model="editing.botId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" @input="markDirty" />
        </label>
        <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.wecom.secret') }}
          <div class="relative">
            <input
              v-model="editing.secret"
              :type="showSecretEdit ? 'text' : 'password'"
              class="w-full rounded-md border border-border bg-bg px-2 py-1.5 pr-16 text-sm text-text"
              @input="markDirty"
            />
            <div class="absolute inset-y-0 right-0 flex items-center pr-1">
              <button
                type="button"
                class="p-1 text-text-muted hover:text-text"
                :aria-label="showSecretEdit ? t('im.action.hide_secret') : t('im.action.show_secret')"
                @click="showSecretEdit = !showSecretEdit"
              >
                <Icon :name="showSecretEdit ? 'eye-off' : 'eye'" :size="16" />
              </button>
              <button
                type="button"
                class="p-1 text-text-muted hover:text-text"
                :aria-label="t('im.action.clear')"
                @click="editing.secret = ''; markDirty()"
              >
                <Icon name="x" :size="15" />
              </button>
            </div>
          </div>
        </label>
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.access') }}
          <select v-model="editing.accessMode" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" @change="markDirty">
            <option value="open">{{ t('im.form.access.open') }}</option>
            <option value="allowlist">{{ t('im.form.access.allowlist') }}</option>
            <option value="disabled">{{ t('im.form.access.disabled') }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.allowFrom') }}
          <input v-model="editing.allowFrom" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" @input="markDirty" />
        </label>
        <div class="flex gap-2">
          <button class="rounded-md bg-primary px-3 py-1.5 text-sm text-white" :disabled="saving" @click="onSave(inst)">
            {{ t('im.action.save') }}
          </button>
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="onTest(inst)">
            {{ t('im.action.test') }}
          </button>
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="cancelEdit">
            {{ t('im.action.cancel') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 测试报告弹窗 -->
    <div v-if="testResult" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" @click.self="closeTest">
      <div class="w-full max-w-md rounded-xl bg-surface p-5 shadow-lg">
        <div class="mb-3 flex items-center justify-between">
          <h3 class="text-base font-semibold text-text">{{ t('im.test.title') }}</h3>
          <button class="text-text-muted hover:text-text" :aria-label="t('im.test.close')" @click="closeTest">
            <Icon name="x" :size="18" />
          </button>
        </div>

        <div class="mb-4 flex items-center gap-2 text-sm font-medium" :class="testVerdictClass[testVerdict()]">
          <Icon
            :name="testVerdict() === 'pass' ? 'check' : (testVerdict() === 'warn' ? 'alert-circle' : 'x')"
            :size="18"
          />
          <span>{{ t(testVerdictKey) }}</span>
        </div>

        <div class="flex flex-col gap-2">
          <div
            v-for="c in testResult.checks"
            :key="c.code"
            class="flex items-start gap-2 rounded-md border border-border p-2.5"
          >
            <Icon
              :name="c.level === 'pass' ? 'check' : (c.level === 'warn' ? 'alert-circle' : 'x')"
              :size="15"
              class="mt-0.5 shrink-0"
              :class="c.level === 'pass' ? 'text-success' : (c.level === 'warn' ? 'text-warning' : 'text-danger')"
            />
            <div class="min-w-0">
              <div class="text-sm text-text">{{ c.title }}</div>
              <div v-if="c.detail" class="break-all text-xs text-text-muted">{{ c.detail }}</div>
            </div>
          </div>
          <div v-if="testResult.checks?.length === 0 && testResult.error" class="text-xs text-text-muted">
            {{ testResult.error }}
          </div>
        </div>

        <div v-if="testVerdict() !== 'pass'" class="mt-4 text-xs text-text-muted">
          {{ t('im.test.retry_hint') }}
        </div>

        <div class="mt-5 flex gap-2">
          <button
            v-if="showEnableFromTest"
            class="rounded-md bg-primary px-3 py-1.5 text-sm text-white"
            @click="confirmEnableFromTest"
          >
            {{ t('im.test.enable') }}
          </button>
          <button class="ml-auto rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="closeTest">
            {{ t('im.test.close') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 删除二次确认弹窗 -->
    <div v-if="confirmDelete" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" @click.self="confirmDelete = null">
      <div class="w-full max-w-sm rounded-xl bg-surface p-5 shadow-lg">
        <h3 class="mb-3 text-base font-semibold text-text">{{ t('im.confirm.delete.title') }}</h3>
        <p class="text-sm text-text-muted">{{ t('im.confirm.delete.body', { name: confirmDelete.name || confirmDelete.channel }) }}</p>
        <div class="mt-5 flex justify-end gap-2">
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="confirmDelete = null">
            {{ t('im.confirm.no') }}
          </button>
          <button class="rounded-md bg-danger px-3 py-1.5 text-sm text-white" @click="confirmDeleteInstance">
            {{ t('im.confirm.yes') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
