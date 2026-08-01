<template>
  <div class="flex h-full flex-col" data-testid="artifact-panel">
    <!-- ── 内层 tab 栏：图标 + 关闭两级 hover + 激活浮起 + 溢出渐变 ── -->
    <div class="relative shrink-0 border-b border-border bg-surface-2">
      <div
        ref="tabBarEl"
        class="flex h-9 items-stretch overflow-x-auto"
        data-testid="artifact-tab-bar"
        @scroll="updateScroll"
      >
        <button
          v-for="sp in specialTabs"
          :key="sp.id"
          type="button"
          class="group flex shrink-0 items-center gap-1.5 px-3 text-xs transition-colors"
          :class="tabClass(sp.id)"
          :data-testid="'artifact-special-tab-' + sp.id"
          @click="activate(sp.id)"
        >
          <Icon :name="sp.icon" :size="13" class="shrink-0" />
          {{ sp.label }}
        </button>
        <button
          v-for="tab in previewTabs"
          :key="tab.id"
          type="button"
          class="group flex shrink-0 items-center gap-1.5 px-3 text-xs transition-colors"
          :class="tabClass(tab.id)"
          data-testid="artifact-preview-tab"
          @click="activate(tab.id)"
        >
          <Icon :name="iconForKind(tabArtifact(tab)?.kind)" :size="13" class="shrink-0 text-text-subtle" />
          <span class="max-w-[96px] truncate">{{ tabName(tab) }}</span>
          <span
            role="button"
            class="mr-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full text-transparent transition-colors group-hover:bg-surface-hover group-hover:text-text hover:!bg-text hover:!text-bg"
            :aria-label="t('artifact.tab.close')"
            data-testid="artifact-tab-close"
            @click.stop="closeTab(tab.id)"
          >
            <Icon name="x" :size="10" />
          </span>
        </button>
      </div>
      <div
        v-if="canScrollRight"
        class="pointer-events-none absolute inset-y-0 right-0 w-12 bg-gradient-to-l from-bg to-transparent"
        data-testid="artifact-tab-fade"
      />
    </div>

    <!-- ── 面板 header 动作栏（仅 artifact 预览时）── -->
    <div
      v-if="activePreviewTab"
      class="flex h-10 shrink-0 items-center gap-1.5 border-b border-border px-3"
      data-testid="artifact-header"
    >
      <span class="min-w-0 flex-1">
        <span class="block truncate text-sm font-medium text-text" data-testid="artifact-header-name">
          {{ headerName }}
        </span>
        <span class="block text-[10px] font-medium uppercase tracking-wide text-text-subtle">
          {{ activeArtifactItem?.kind }}
        </span>
      </span>

      <div class="flex shrink-0 items-center rounded-md border border-border bg-surface-2 p-0.5" data-testid="artifact-view-toggle">
        <button
          type="button"
          class="rounded px-2 py-0.5 text-[11px] transition-colors"
          :class="showCode ? 'text-text-muted hover:text-text' : 'bg-surface-hover text-text'"
          @click="setView(ArtifactContentView.Preview)"
        >
          {{ t('artifact.view.preview') }}
        </button>
        <button
          v-if="!isNonCodeKind"
          type="button"
          class="rounded px-2 py-0.5 text-[11px] transition-colors"
          :class="showCode ? 'bg-surface-hover text-text' : 'text-text-muted hover:text-text'"
          @click="setView(ArtifactContentView.Code)"
        >
          {{ t('artifact.view.code') }}
        </button>
      </div>

      <IconButton icon="refresh" :label="t('artifact.actions.refresh')" data-testid="artifact-action-refresh" @click="reloadKey++" />
      <IconButton v-if="canCopy" icon="copy" :label="t('artifact.actions.copy')" data-testid="artifact-action-copy" @click="copyContent" />
      <IconButton v-if="hasFilePath" icon="folder" :label="t('artifact.actions.reveal')" data-testid="artifact-action-reveal" @click="revealFile" />
      <IconButton v-if="hasFilePath" icon="external-open" :label="t('artifact.actions.openExternal')" data-testid="artifact-action-open" @click="openExternal" />
    </div>

    <!-- ── 内容区 ── -->
    <div class="min-h-0 flex-1 overflow-hidden">
      <FileListView v-if="activeTabId === ArtifactSpecialTab.FileList" :session-id="props.sessionId" />
      <BrowserTab v-else-if="activeTabId === ArtifactSpecialTab.Browser" />
      <div
        v-else-if="activeTabId === ArtifactSpecialTab.Subagents"
        class="flex h-full flex-col items-center justify-center px-6 text-center"
        data-testid="artifact-subagents-placeholder"
      >
        <p class="font-display text-base italic text-text-muted">{{ t('artifact.subagents.placeholder') }}</p>
      </div>
      <template v-else-if="activeArtifactItem">
        <ArtifactRenderer v-if="!showCode" :key="reloadKey" :artifact="activeArtifactItem" />
        <pre v-else class="h-full w-full overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs text-text">{{ activeArtifactItem.content }}</pre>
      </template>
      <div v-else class="flex h-full flex-col items-center justify-center px-6 text-center" data-testid="artifact-empty">
        <p class="font-display text-base italic text-text-muted">{{ t('artifact.empty') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ArtifactContentView, ArtifactSpecialTab, useArtifacts } from '../../composables/useArtifacts';
import type { Artifact, ArtifactPreviewTab } from '../../composables/useArtifacts';
import { useChatActions } from '../../composables/useChatActions';
import ArtifactRenderer from './ArtifactRenderer.vue';
import FileListView from './FileListView.vue';
import BrowserTab from './BrowserTab.vue';
import IconButton from '../common/IconButton.vue';
import { t } from '../../services/i18n';
import { showToast } from '../../services/toast';

const props = defineProps<{ sessionId: string }>();

const artifacts = useArtifacts();
const chatActions = useChatActions();

const specialTabs: { id: ArtifactSpecialTab; label: string; icon: string }[] = [
  { id: ArtifactSpecialTab.FileList, label: t('artifact.special.fileList'), icon: 'file-list' },
  { id: ArtifactSpecialTab.Browser, label: t('artifact.special.browser'), icon: 'browser' },
  { id: ArtifactSpecialTab.Subagents, label: t('artifact.special.subagents'), icon: 'subagents' },
];

/** 不可切 code 视图的 kind（分段控件只留 preview 段）。 */
const NON_CODE_TYPES = new Set<Artifact['kind']>(['document', 'image', 'video', 'text', 'local-service']);
/** 支持复制内容的 kind。 */
const COPY_KINDS = new Set<Artifact['kind']>(['html', 'svg', 'code', 'markdown', 'text', 'document']);

const previewTabs = computed<ArtifactPreviewTab[]>(
  () => artifacts.previewTabsBySession.value[props.sessionId] ?? [],
);
const activeTabId = computed<string | null>(
  () => artifacts.activeTabIdBySession.value[props.sessionId] ?? null,
);
const sessionArtifacts = computed<Artifact[]>(
  () => artifacts.artifactsBySession.value[props.sessionId] ?? [],
);
const artifactById = computed(() => new Map(sessionArtifacts.value.map((a) => [a.id, a])));
const activePreviewTab = computed<ArtifactPreviewTab | null>(
  () => previewTabs.value.find((tab) => tab.id === activeTabId.value) ?? null,
);
const activeArtifactItem = computed<Artifact | null>(
  () => (activePreviewTab.value ? artifactById.value.get(activePreviewTab.value.artifactId) ?? null : null),
);
const isNonCodeKind = computed(
  () => !!activeArtifactItem.value && NON_CODE_TYPES.has(activeArtifactItem.value.kind),
);
const showCode = computed(
  () => !!activePreviewTab.value && !isNonCodeKind.value && activePreviewTab.value.contentView === ArtifactContentView.Code,
);
const canCopy = computed(
  () => !!activeArtifactItem.value && COPY_KINDS.has(activeArtifactItem.value.kind),
);
const hasFilePath = computed(() => !!activeArtifactItem.value?.filePath);
const headerName = computed(
  () => activeArtifactItem.value?.name ?? activePreviewTab.value?.artifactId ?? '',
);

/** 刷新：递增 key 强制 ArtifactRenderer 重挂载（iframe / 预览会话重建）。 */
const reloadKey = ref(0);

/** 溢出渐变：tab 栏可向右滚动时显示右侧遮罩。 */
const tabBarEl = ref<HTMLElement | null>(null);
const canScrollRight = ref(false);

function updateScroll(): void {
  const el = tabBarEl.value;
  if (!el) return;
  canScrollRight.value =
    el.scrollWidth > el.clientWidth + 4 && el.scrollLeft < el.scrollWidth - el.clientWidth - 4;
}

onMounted(() => {
  updateScroll();
  window.addEventListener('resize', updateScroll);
});
onBeforeUnmount(() => {
  window.removeEventListener('resize', updateScroll);
});
watch([previewTabs, activeTabId], async () => {
  await nextTick();
  updateScroll();
});

function tabClass(id: string): string {
  return id === activeTabId.value
    ? 'bg-surface-raised text-text shadow-sm'
    : 'text-text-muted hover:bg-surface';
}

function tabArtifact(tab: ArtifactPreviewTab): Artifact | null {
  return artifactById.value.get(tab.artifactId) ?? null;
}

function tabName(tab: ArtifactPreviewTab): string {
  return tabArtifact(tab)?.name ?? tab.artifactId;
}

function iconForKind(kind?: string): string {
  switch (kind) {
    case 'code':           return 'terminal';
    case 'local-service':  return 'link';
    default:               return 'file-text';
  }
}

function activate(id: string): void {
  artifacts.activateTab(props.sessionId, id);
}

function closeTab(tabId: string): void {
  artifacts.closePreviewTab(props.sessionId, tabId);
}

function setView(view: ArtifactContentView): void {
  if (!activePreviewTab.value) return;
  artifacts.setContentView(props.sessionId, activePreviewTab.value.id, view);
}

async function copyContent(): Promise<void> {
  if (!activeArtifactItem.value) return;
  const ok = await chatActions.copy(activeArtifactItem.value.content);
  showToast(ok ? t('chat.markdown.copied') : t('artifact.actions.copy'), ok ? 'success' : 'error');
}

function revealFile(): void {
  const fp = activeArtifactItem.value?.filePath;
  if (!fp) return;
  void window.darvin.revealWorkspaceFile(fp);
}

async function openExternal(): Promise<void> {
  const fp = activeArtifactItem.value?.filePath;
  if (!fp) return;
  const r = await window.darvin.openWorkspaceFile(fp);
  if (!r.success) showToast(r.error ?? t('artifact.actions.openExternal'), 'error');
}
</script>
