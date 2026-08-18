<script setup lang="ts">
import { onUnmounted, reactive, ref } from 'vue';
import { showToast } from '../../services/toast';
import { t } from '../../services/i18n';
import { useIm } from '../../composables/useIm';
import type { DarvinIMInstance, DarvinIMLoginResult } from '../../../shared/darvin-api';
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
const saving = ref(false);

const adding = reactive<Record<string, string>>({});

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

onUnmounted(stopQrPoll);

function statusLabel(s: string): string {
  return t(STATUS_KEY[s] ?? 'im.status.stopped');
}

async function onToggle(inst: DarvinIMInstance): Promise<void> {
  await toggle(inst.id, !inst.enabled);
}

async function onDelete(inst: DarvinIMInstance): Promise<void> {
  await remove(inst.id);
}

function startEdit(inst: DarvinIMInstance): void {
  editingId.value = inst.id;
  Object.assign(editing, {
    name: inst.name ?? '',
    appId: (inst.config?.appId as string) ?? '',
    appSecret: (inst.config?.appSecret as string) ?? '',
    botId: (inst.config?.botId as string) ?? '',
    secret: (inst.config?.secret as string) ?? '',
    accessMode: inst.accessMode ?? 'open',
    allowFrom: (inst.allowFrom ?? []).join(','),
  });
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
    editingId.value = null;
  } finally {
    saving.value = false;
  }
}

async function onTest(): Promise<void> {
  const config: Record<string, unknown> = {};
  if (props.channel === 'qq') {
    config.appId = editing.appId;
    config.appSecret = editing.appSecret;
  } else if (props.channel === 'wecom') {
    config.botId = editing.botId;
    config.secret = editing.secret;
  }
  const res = await test(props.channel, config);
  if (res.ok) {
    showToast(t('im.toast.test_ok'), 'success');
  } else {
    showToast(t('im.toast.test_ko', { error: res.error ?? 'unknown' }), 'error');
  }
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
            <input v-model="adding.appSecret" type="password" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
          </label>
          <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.wecom.botId') }}
            <input v-model="adding.botId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
          </label>
          <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
            {{ t('im.form.wecom.secret') }}
            <input v-model="adding.secret" type="password" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
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
          <span class="text-sm font-medium text-text">{{ inst.name || inst.channel }}</span>
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
          <button class="text-xs text-danger" @click="onDelete(inst)">
            {{ t('im.action.delete') }}
          </button>
        </div>
      </div>

      <div v-if="editingId === inst.id" class="mt-3 flex flex-col gap-3 border-t border-border pt-3">
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.name') }}
          <input v-model="editing.name" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.qq.appId') }}
          <input v-model="editing.appId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <label v-if="channel === 'qq'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.qq.appSecret') }}
          <input v-model="editing.appSecret" type="password" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.wecom.botId') }}
          <input v-model="editing.botId" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <label v-if="channel === 'wecom'" class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.wecom.secret') }}
          <input v-model="editing.secret" type="password" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.access') }}
          <select v-model="editing.accessMode" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text">
            <option value="open">{{ t('im.form.access.open') }}</option>
            <option value="allowlist">{{ t('im.form.access.allowlist') }}</option>
            <option value="disabled">{{ t('im.form.access.disabled') }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('im.form.allowFrom') }}
          <input v-model="editing.allowFrom" class="rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-text" />
        </label>
        <div class="flex gap-2">
          <button class="rounded-md bg-primary px-3 py-1.5 text-sm text-white" :disabled="saving" @click="onSave(inst)">
            {{ t('im.action.save') }}
          </button>
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="onTest">
            {{ t('im.action.test') }}
          </button>
          <button class="rounded-md border border-border px-3 py-1.5 text-sm text-text-muted" @click="editingId = null">
            {{ t('im.action.cancel') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
