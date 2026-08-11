<template>
  <div class="flex h-full flex-col" data-testid="browser-tab">
    <!-- 地址栏 + 能力按钮 -->
    <div class="flex shrink-0 items-center gap-1 border-b border-border px-2 py-1.5">
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!canGoBack"
        :aria-label="t('artifact.browser.back')"
        data-testid="browser-back"
        @click="webviewRef?.goBack?.()"
      >
        ←
      </button>
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-text disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!canGoForward"
        :aria-label="t('artifact.browser.forward')"
        data-testid="browser-forward"
        @click="webviewRef?.goForward?.()"
      >
        →
      </button>
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('artifact.browser.reload')"
        data-testid="browser-reload"
        @click="webviewRef?.reload?.()"
      >
        <Icon name="refresh" :size="14" />
      </button>
      <input
        v-model="inputUrl"
        type="text"
        :placeholder="t('artifact.browser.placeholder')"
        class="h-6 min-w-0 flex-1 rounded border border-border bg-surface px-2 text-xs text-text outline-none placeholder:text-text-subtle focus:border-primary"
        data-testid="browser-url-input"
        @keydown.enter="go"
      />
      <button
        type="button"
        class="shrink-0 rounded border border-border px-2 py-0.5 text-xs text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        data-testid="browser-go"
        @click="go"
      >
        {{ t('artifact.browser.go') }}
      </button>
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('artifact.browser.zoomOut')"
        data-testid="browser-zoom-out"
        @click="zoomOut"
      >
        −
      </button>
      <span class="w-9 shrink-0 text-center text-[10px] text-text-subtle" data-testid="browser-zoom">{{ zoomPercent }}%</span>
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('artifact.browser.zoomIn')"
        data-testid="browser-zoom-in"
        @click="zoomIn"
      >
        +
      </button>
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-text-muted transition-colors hover:bg-surface-hover hover:text-text"
        :aria-label="t('artifact.browser.openExternal')"
        :title="t('artifact.browser.openExternal')"
        data-testid="browser-open-external"
        @click="openExternal"
      >
        <Icon name="external-open" :size="14" />
      </button>
    </div>

    <!-- webview 内容 -->
    <webview
      v-if="currentUrl"
      ref="webviewRef"
      src="about:blank"
      class="min-h-0 flex-1 border-0 bg-surface"
      partition="persist:artifact-browser"
      allowpopups="false"
      data-testid="browser-webview"
      @dom-ready="onDomReady"
      @did-navigate="onNavigate"
      @did-navigate-in-page="onNavigateInPage"
      @did-stop-loading="onDidStopLoading"
      @did-finish-load="onFinishLoad"
      @did-fail-load="onFailLoad"
      @page-title-updated="onTitleUpdate"
    />

    <!-- 空态：本地服务列表 -->
    <div v-else class="min-h-0 flex-1 overflow-auto p-3" data-testid="browser-empty">
      <p class="mb-2 text-xs text-text-muted">{{ t('artifact.browser.empty') }}</p>
      <p v-if="localServices.length === 0" class="text-xs text-text-subtle">{{ t('artifact.browser.services') }}</p>
      <div class="flex flex-col gap-1">
        <button
          v-for="svc in localServices"
          :key="svc.url ?? svc.id"
          type="button"
          class="flex items-center gap-2 rounded-md border border-border bg-surface px-2.5 py-1.5 text-left transition-colors hover:bg-surface-2"
          :data-testid="'browser-service-' + (svc.url ?? svc.id)"
          @click="openService(svc)"
        >
          <span
            class="h-1.5 w-1.5 shrink-0 rounded-full"
            :class="(svc.url ? serviceStatus[svc.url]?.online : false) ? 'bg-success' : 'bg-text-subtle'"
          />
          <span class="min-w-0 flex-1">
            <span class="block truncate text-xs text-text">
              {{ svc.url ? (serviceStatus[svc.url]?.title || svc.name || svc.url) : (svc.name || svc.id) }}
            </span>
            <span v-if="svc.url" class="block truncate font-mono text-[10px] text-text-subtle">{{ svc.url }}</span>
          </span>
          <span v-if="svc.url" class="shrink-0 text-[10px] text-text-subtle">
            {{ serviceStatus[svc.url]?.online ? t('artifact.browser.online') : t('artifact.browser.offline') }}
          </span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import Icon from '../common/Icon.vue';
import { useArtifacts, type Artifact } from '../../composables/useArtifacts';
import type { WebviewElement } from '../../types/webview';
import { t } from '../../services/i18n';

const props = defineProps<{ sessionId: string }>();

const artifacts = useArtifacts();

/** webview 实际加载的 URL（受控）。 */
const currentUrl = computed(() => artifacts.browserUrlBySession.value[props.sessionId] ?? '');
/** 地址栏显示值（本地 html 预览时为 filePath）。输入即写回受控状态。 */
const inputUrl = computed({
  get: () => artifacts.browserAddressBySession.value[props.sessionId] ?? '',
  set: (v: string) => artifacts.setBrowserAddress(props.sessionId, v),
});

const webviewRef = ref<WebviewElement | null>(null);
/** dom-ready 是否已触发；loadURL 在 dom-ready 前调用会同步抛错。 */
const webviewReady = ref(false);
const canGoBack = ref(false);
const canGoForward = ref(false);
const userZoom = ref(1);
/** 内容超宽时自动缩放到视口宽度的系数（did-finish-load 后计算）。 */
const fitScale = ref(1);
const zoomPercent = computed(() => Math.round(fitScale.value * userZoom.value * 100));

const ZOOM_MIN = 0.25;
const ZOOM_MAX = 3;
const ZOOM_STEP = 0.1;

function go(): void {
  let value = inputUrl.value.trim();
  if (!value) return;
  if (!/^https?:\/\//i.test(value)) value = `https://${value}`;
  artifacts.setBrowserUrl(props.sessionId, value);
  artifacts.setBrowserAddress(props.sessionId, value);
}

function load(url: string): void {
  if (!url) return;
  const wv = webviewRef.value;
  if (!wv) return;
  if (typeof wv.getURL === 'function' && wv.getURL() === url) return;
  try {
    void wv.loadURL(url).catch(() => {});
  } catch (err) {
    // loadURL 在 webview 未 attach / dom-ready 前会同步 throw；等下一次 dom-ready 重试。
    const msg = err instanceof Error ? err.message : String(err);
    if (/attached to the DOM|dom-ready/i.test(msg)) {
      webviewReady.value = false;
    }
  }
}

function refreshNavState(): void {
  const wv = webviewRef.value;
  if (!wv) return;
  canGoBack.value = wv.canGoBack();
  canGoForward.value = wv.canGoForward();
}

function onDomReady(): void {
  webviewReady.value = true;
  refreshNavState();
  load(currentUrl.value);
}

function onNavigate(e: { url: string }): void {
  // 初始 about:blank 不应污染地址栏（LobsterAI 同款守卫）。
  if (e.url === 'about:blank') {
    refreshNavState();
    return;
  }
  artifacts.setBrowserUrl(props.sessionId, e.url);
  artifacts.setBrowserAddress(props.sessionId, e.url);
  refreshNavState();
}

function onNavigateInPage(e: { url: string }): void {
  if (e.url === 'about:blank') {
    refreshNavState();
    return;
  }
  artifacts.setBrowserUrl(props.sessionId, e.url);
  artifacts.setBrowserAddress(props.sessionId, e.url);
  refreshNavState();
}

function onDidStopLoading(): void {
  // 导航成功由 did-navigate / did-navigate-in-page 回写；这里只刷前进/后退状态。
  // 加载失败时 getURL() 会返回旧地址，若在此回写会覆盖用户刚输入的 URL。
  refreshNavState();
}

function onFailLoad(e: { errorCode: number }): void {
  // -3 = ERR_ABORTED（用户主动取消 / 被重定向打断），忽略。
  if (e.errorCode === -3) return;
  refreshNavState();
}

function onTitleUpdate(): void {
  // 标题暂不进入状态机，预留
}

function applyZoom(): void {
  const wv = webviewRef.value;
  if (!wv) return;
  wv.setZoomFactor(fitScale.value * userZoom.value);
}
function zoomIn(): void {
  userZoom.value = Math.min(ZOOM_MAX, userZoom.value + ZOOM_STEP);
  applyZoom();
}
function zoomOut(): void {
  userZoom.value = Math.max(ZOOM_MIN, userZoom.value - ZOOM_STEP);
  applyZoom();
}

/** 页面加载完成后自动缩放适配视口：内容超宽则整体缩小，避免被裁切。 */
async function fitToViewport(): Promise<void> {
  const wv = webviewRef.value;
  if (!wv) return;
  const vw = wv.clientWidth;
  if (!vw) return;
  fitScale.value = 1;
  try {
    const raw = await wv.executeJavaScript('document.documentElement.scrollWidth');
    const num = Number(raw);
    if (Number.isFinite(num) && num > vw) {
      fitScale.value = vw / num;
    }
  } catch {
    // 页面禁止脚本注入时跳过自适应，保持原样
  }
  applyZoom();
}

function onFinishLoad(): void {
  void fitToViewport();
}

function openExternal(): void {
  const url = currentUrl.value;
  if (url) void window.darvin.openExternal(url);
}

// 空态：本 session 的 local-service artifacts + HTTP 在线探测。
const localServices = computed<Artifact[]>(() =>
  (artifacts.artifactsBySession.value[props.sessionId] ?? []).filter(
    (a) => a.kind === 'local-service' && !!a.url,
  ),
);
const serviceStatus = ref<Record<string, { online: boolean; title: string }>>({});

async function probeServices(): Promise<void> {
  const urls = localServices.value.map((a) => a.url as string);
  if (urls.length === 0) {
    serviceStatus.value = {};
    return;
  }
  try {
    const res = await window.darvin.listLocalServices(urls);
    const map: Record<string, { online: boolean; title: string }> = {};
    for (const s of res.services) {
      map[s.url] = { online: s.online, title: s.title };
    }
    serviceStatus.value = map;
  } catch {
    serviceStatus.value = {};
  }
}

function openService(a: Artifact): void {
  if (a.url) artifacts.openBrowser(props.sessionId, a.url);
}

watch(currentUrl, (url) => {
  if (!url) return;
  if (webviewReady.value) load(url);
  // 未 ready 时 webview 挂载后 onDomReady 会加载当前值
});
watch(localServices, () => {
  void probeServices();
});

onMounted(() => {
  void probeServices();
});
</script>
