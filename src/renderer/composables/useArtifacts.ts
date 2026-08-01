/**
 * useArtifacts — renderer 端 artifact 面板状态机，按 session 隔离。
 *
 * 数据来自 Go 的 `artifact` 事件（useMessages 转发），本地维护：
 * - artifactsBySession：每个 session 的 artifact 列表（id 去重，重发时更新内容）
 * - previewTabsBySession：每个 artifact 一个预览 tab（带 preview / code 内容视图）
 * - activeTabIdBySession：当前激活的内层 tab（特殊 tab 或 artifact 预览 tab）
 * - isPanelOpenBySession / panelWidth：面板开关与宽度（180-1000px 可拖拽）
 */

import { computed, ref } from 'vue';
import type { DarvinArtifactKind } from '../../shared/darvin-api';
import { useSession } from './useSession';
import { useSidePanel } from './useSidePanel';

/** 单 artifact 的内容视图（照 LobsterAI ArtifactContentView）。 */
export const ArtifactContentView = {
  Preview: 'preview',
  Code: 'code',
} as const;
export type ArtifactContentView = typeof ArtifactContentView[keyof typeof ArtifactContentView];

/** 面板内层固定特殊 tab（照 LobsterAI ArtifactSpecialTab）。 */
export const ArtifactSpecialTab = {
  FileList: 'fileList',
  Browser: 'browser',
  Subagents: 'subagents',
} as const;
export type ArtifactSpecialTab = typeof ArtifactSpecialTab[keyof typeof ArtifactSpecialTab];

export interface Artifact {
  id: string;
  kind: DarvinArtifactKind;
  name?: string;
  content: string;
  /** html 引用 workspace 内文件时携带（相对 workspace 根），走本地预览服务。 */
  filePath?: string;
  /** 产出该 artifact 的 assistant 消息 id；聊天消息内卡片组按它挂载。 */
  messageId?: string;
  createdAt: number;
}

export interface ArtifactPreviewTab {
  id: string;
  artifactId: string;
  contentView: ArtifactContentView;
  openedAt: number;
}

/** 内层 tab = 特殊 tab 或某个 artifact 的预览 tab。 */
export type ArtifactInnerTab =
  | { id: ArtifactSpecialTab; special: true; openedAt: number }
  | ArtifactPreviewTab;

export const MIN_PANEL_WIDTH = 180;
export const MAX_PANEL_WIDTH = 1000;
export const DEFAULT_PANEL_WIDTH = 560;

const artifactsBySession = ref<Record<string, Artifact[]>>({});
const previewTabsBySession = ref<Record<string, ArtifactPreviewTab[]>>({});
const activeTabIdBySession = ref<Record<string, string | null>>({});
const isPanelOpenBySession = ref<Record<string, boolean>>({});
const panelWidth = ref<number>(DEFAULT_PANEL_WIDTH);
/** 拖拽面板宽度期间置 true，AppShell 用它关掉 grid 过渡避免拖拽卡顿。 */
const dragging = ref(false);

const session = useSession();
const sidePanel = useSidePanel();

export function previewTabId(artifactId: string): string {
  return `artifact:${artifactId}`;
}

export function isSpecialTabId(id: string | null): id is ArtifactSpecialTab {
  return id === 'fileList' || id === 'browser' || id === 'subagents';
}

export function useArtifacts() {
  const currentArtifacts = computed<Artifact[]>(
    () => artifactsBySession.value[session.activeSessionId.value ?? ''] ?? [],
  );
  const currentPreviewTabs = computed<ArtifactPreviewTab[]>(
    () => previewTabsBySession.value[session.activeSessionId.value ?? ''] ?? [],
  );
  const currentActiveTabId = computed<string | null>(
    () => activeTabIdBySession.value[session.activeSessionId.value ?? ''] ?? null,
  );

  /** 当前激活的 artifact；激活特殊 tab 或面板未打开时返回 null。 */
  function activeArtifact(): Artifact | null {
    const sid = session.activeSessionId.value;
    if (sid === null) return null;
    const tabId = activeTabIdBySession.value[sid];
    if (!tabId || isSpecialTabId(tabId)) return null;
    const tab = previewTabsBySession.value[sid]?.find((t) => t.id === tabId);
    if (!tab) return null;
    return artifactsBySession.value[sid]?.find((a) => a.id === tab.artifactId) ?? null;
  }

  function setPanelOpen(sid: string, open: boolean): void {
    isPanelOpenBySession.value = { ...isPanelOpenBySession.value, [sid]: open };
    // 只对 active session 的 artifact 自动弹开侧栏，后台 session 静默记录
    if (open && session.activeSessionId.value === sid) sidePanel.set(true);
  }

  function openPreviewTab(sid: string, artifactId: string): void {
    const tabs = previewTabsBySession.value[sid] ?? [];
    const tabId = previewTabId(artifactId);
    if (!tabs.some((t) => t.id === tabId)) {
      previewTabsBySession.value = {
        ...previewTabsBySession.value,
        [sid]: [...tabs, { id: tabId, artifactId, contentView: ArtifactContentView.Preview, openedAt: Date.now() }],
      };
    }
    activeTabIdBySession.value = { ...activeTabIdBySession.value, [sid]: tabId };
    setPanelOpen(sid, true);
  }

  function addArtifact(sid: string, artifact: Artifact): void {
    const list = artifactsBySession.value[sid] ?? [];
    const existing = list.find((a) => a.id === artifact.id);
    if (existing) {
      if (existing.content === artifact.content) return;
      artifactsBySession.value = {
        ...artifactsBySession.value,
        [sid]: list.map((a) => (a.id === artifact.id ? { ...a, ...artifact } : a)),
      };
    } else {
      artifactsBySession.value = { ...artifactsBySession.value, [sid]: [...list, artifact] };
    }
    openPreviewTab(sid, artifact.id);
  }

  function activateTab(sid: string, tabId: string): void {
    activeTabIdBySession.value = { ...activeTabIdBySession.value, [sid]: tabId };
    setPanelOpen(sid, true);
  }

  function closePreviewTab(sid: string, tabId: string): void {
    const tabs = previewTabsBySession.value[sid] ?? [];
    const idx = tabs.findIndex((t) => t.id === tabId);
    if (idx < 0) return;
    const remaining = tabs.filter((t) => t.id !== tabId);
    previewTabsBySession.value = { ...previewTabsBySession.value, [sid]: remaining };
    if (activeTabIdBySession.value[sid] === tabId) {
      const next = remaining[Math.min(idx, remaining.length - 1)] ?? null;
      activeTabIdBySession.value = { ...activeTabIdBySession.value, [sid]: next ? next.id : ArtifactSpecialTab.FileList };
    }
  }

  function setContentView(sid: string, tabId: string, view: ArtifactContentView): void {
    previewTabsBySession.value = {
      ...previewTabsBySession.value,
      [sid]: (previewTabsBySession.value[sid] ?? []).map((t) =>
        t.id === tabId ? { ...t, contentView: view } : t,
      ),
    };
  }

  function setPanelWidth(w: number): void {
    panelWidth.value = Math.max(MIN_PANEL_WIDTH, Math.min(MAX_PANEL_WIDTH, w));
  }

  function setDragging(v: boolean): void {
    dragging.value = v;
  }

  function clearSessionArtifacts(sid: string): void {
    const nextArtifacts = { ...artifactsBySession.value };
    delete nextArtifacts[sid];
    artifactsBySession.value = nextArtifacts;
    const nextTabs = { ...previewTabsBySession.value };
    delete nextTabs[sid];
    previewTabsBySession.value = nextTabs;
    const nextActive = { ...activeTabIdBySession.value };
    delete nextActive[sid];
    activeTabIdBySession.value = nextActive;
    const nextOpen = { ...isPanelOpenBySession.value };
    delete nextOpen[sid];
    isPanelOpenBySession.value = nextOpen;
  }

  function reset(): void {
    artifactsBySession.value = {};
    previewTabsBySession.value = {};
    activeTabIdBySession.value = {};
    isPanelOpenBySession.value = {};
  }

  return {
    artifactsBySession,
    previewTabsBySession,
    activeTabIdBySession,
    isPanelOpenBySession,
    panelWidth,
    dragging,
    currentArtifacts,
    currentPreviewTabs,
    currentActiveTabId,
    activeArtifact,
    addArtifact,
    openPreviewTab,
    activateTab,
    closePreviewTab,
    setContentView,
    setPanelOpen,
    setPanelWidth,
    setDragging,
    clearSessionArtifacts,
    reset,
  };
}
