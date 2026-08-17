<template>
  <nav class="flex-1 overflow-y-auto px-2 pb-2">
    <ul v-if="groups.length > 0" class="flex flex-col gap-0.5">
      <li v-for="group in groups" :key="group.key" class="flex flex-col gap-0.5">
        <!-- 重命名态：标题变 input，回车提交 -->
        <div
          v-if="renamingKey === group.key"
          class="flex items-center gap-1.5 rounded-md px-2 py-1.5"
        >
          <Icon name="chevron-down" :size="12" class="shrink-0" />
          <Icon name="folder" :size="13" class="shrink-0 text-text-muted" />
          <input
            ref="renameInput"
            v-model.trim="renameDraft"
            class="min-w-0 flex-1 rounded-md border border-border bg-surface-2 px-2 py-0.5 font-sans text-xs text-text outline-none focus:border-border-strong"
            :placeholder="t('sidebar.session.rename_placeholder')"
            @keydown.enter.prevent="commitRename(group)"
            @keydown.esc="cancelRename"
            @blur="cancelRename"
          />
        </div>

        <!-- workspace 分组头 -->
        <div
          v-else
          class="group flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 font-sans text-xs transition-colors hover:bg-surface-2"
          :class="group.active ? 'text-text' : 'text-text-muted'"
        >
          <button
            type="button"
            class="flex min-w-0 flex-1 items-center gap-1.5 text-left"
            :aria-expanded="group.expanded"
            @click="toggle(group.key)"
          >
            <Icon
              name="chevron-down"
              :size="12"
              class="shrink-0 transition-transform"
              :class="group.expanded ? '' : '-rotate-90'"
            />
            <Icon name="folder" :size="13" class="shrink-0" />
            <span class="min-w-0 flex-1 truncate text-left" :title="group.label">
              {{ group.label }}
            </span>
            <span
              v-if="group.sessions.length > 0"
              class="shrink-0 text-[10px] text-text-subtle"
            >
              {{ formatNumber(group.sessions.length) }}
            </span>
          </button>

          <!-- kebab 菜单：在 workspace 上做「新建会话 / 重命名 / 改目录 / 删除」 -->
          <Dropdown @update:open="(open: boolean) => onMenuOpenChange(group.key, open)">
            <template #trigger>
              <span
                role="button"
                class="grid h-6 w-6 shrink-0 place-items-center rounded-md text-text-subtle transition-colors hover:bg-surface-hover hover:text-text"
                :class="group.active ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'"
                :aria-label="t('sidebar.workspace.more_aria')"
              >
                <Icon name="more" :size="14" />
              </span>
            </template>
            <template #menu="{ close }">
              <div
                v-if="confirmingKey === group.key"
                class="min-w-[200px] rounded-lg border border-border bg-surface p-2 shadow-md"
              >
                <p class="px-1 pb-2 text-xs text-text-muted">
                  {{ t('workspace.delete.empty') }}
                </p>
                <p
                  v-if="group.sessions.length > 0"
                  class="px-1 pb-2 text-xs text-danger"
                >
                  {{ t('workspace.delete.cascade', { n: group.sessions.length }) }}
                </p>
                <div class="flex justify-end gap-1">
                  <button
                    type="button"
                    class="rounded-md px-2 py-1 text-xs text-text-muted transition-colors hover:bg-surface-hover"
                    @click="cancelConfirm"
                  >
                    {{ t('sidebar.session.cancel') }}
                  </button>
                  <button
                    type="button"
                    class="rounded-md bg-danger px-2 py-1 text-xs text-white transition-colors hover:opacity-90"
                    :disabled="deletingKey === group.key"
                    @click="confirmDelete(group, close)"
                  >
                    {{ t('sidebar.session.delete_confirm_btn') }}
                  </button>
                </div>
              </div>
              <div v-else class="min-w-[160px] rounded-lg border border-border bg-surface p-1 shadow-md">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-text transition-colors hover:bg-surface-hover"
                  @click="emit('create', group.workspaceId); close()"
                >
                  <Icon name="plus" :size="13" class="text-text-subtle" />
                  {{ t('sidebar.workspace.newSession') }}
                </button>
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-text transition-colors hover:bg-surface-hover"
                  @click="startRename(group, close)"
                >
                  <Icon name="edit" :size="13" class="text-text-subtle" />
                  {{ t('sidebar.workspace.rename') }}
                </button>
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-text transition-colors hover:bg-surface-hover"
                  :disabled="movingKey === group.key"
                  @click="startMove(group, close)"
                >
                  <Icon name="folder-open" :size="13" class="text-text-subtle" />
                  {{ t('sidebar.workspace.moveRoot') }}
                </button>
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] text-danger transition-colors hover:bg-surface-hover"
                  @click="confirmingKey = group.key"
                >
                  <Icon name="trash" :size="13" />
                  {{ t('sidebar.workspace.delete') }}
                </button>
              </div>
            </template>
          </Dropdown>
        </div>

        <!-- 组内会话 -->
        <ul v-if="group.expanded" class="ml-4 flex flex-col gap-0.5">
          <SessionItem
            v-for="s in group.sessions"
            :key="s.id"
            :session="s"
            :active="s.id === currentId"
            :status="statusOf(s.id)"
            :pinned="pinnedIds.has(s.id)"
            :unread="unreadSessionIds.has(s.id)"
            @select="emit('select', $event)"
            @rename="(id, title) => emit('rename', id, title)"
            @delete="emit('delete', $event)"
            @pin="session.togglePin"
          />
          <li v-if="group.sessions.length === 0" class="px-2 py-1 text-[11px] text-text-subtle">
            {{ t('sidebar.session.empty') }}
          </li>
        </ul>
      </li>
    </ul>
    <p v-else class="px-3 py-4 text-xs text-text-muted">{{ t('sidebar.session.empty') }}</p>
  </nav>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import type { DarvinSession, DarvinWorkspace } from '../../../shared/darvin-api';
import SessionItem from './SessionItem.vue';
import Icon from '../common/Icon.vue';
import Dropdown from '../common/Dropdown.vue';
import { useMessages, type SessionActivityStatus } from '../../composables/useMessages';
import { useSession } from '../../composables/useSession';
import { useWorkspaces } from '../../composables/useWorkspaces';
import { showToast } from '../../services/toast';
import { t, formatNumber } from '../../services/i18n';

const props = defineProps<{
  sessions: DarvinSession[];
  workspaces: DarvinWorkspace[];
  currentId: string;
}>();
const emit = defineEmits<{
  select: [id: string];
  rename: [id: string, title: string];
  delete: [id: string];
  create: [workspaceId: string];
}>();

const messages = useMessages();
const session = useSession();
const workspaceState = useWorkspaces();
const runningSessionIds = computed(() => messages.streamingSessionIds.value);
const unreadSessionIds = computed(() => messages.unreadSessionIds.value);
const pinnedIds = computed(() => session.pinnedSessionIds.value);
const statusBySession = computed(() => messages.sessionStatusBySessionId.value);

const expandedKeys = ref<Set<string>>(new Set());
const renamingKey = ref<string | null>(null);
const renameDraft = ref('');
const renameInput = ref<HTMLInputElement | null>(null);
const confirmingKey = ref<string | null>(null);
const deletingKey = ref<string | null>(null);
const movingKey = ref<string | null>(null);

interface Group {
  key: string;
  workspaceId: string;
  label: string;
  expanded: boolean;
  active: boolean;
  sessions: DarvinSession[];
}

const UNGROUPED_KEY = '__ungrouped__';

/** 每个 workspace 一个分组；未归属会话（理论：迁移后不存在）落到末尾分组。 */
const groups = computed<Group[]>(() => {
  const byWorkspace = new Map<string, DarvinSession[]>();
  const ungrouped: DarvinSession[] = [];
  for (const s of props.sessions) {
    const wid = s.workspaceId;
    if (!wid) {
      ungrouped.push(s);
    } else {
      const list = byWorkspace.get(wid) ?? [];
      list.push(s);
      byWorkspace.set(wid, list);
    }
  }
  const out: Group[] = props.workspaces.map((w) => {
    const list = byWorkspace.get(w.id) ?? [];
    byWorkspace.delete(w.id);
    return makeGroup(w.id, w.label, list);
  });
  if (ungrouped.length > 0) out.push(makeGroup(UNGROUPED_KEY, t('workspace.ungrouped'), ungrouped));
  return out;
});

function makeGroup(key: string, label: string, list: DarvinSession[]): Group {
  const sorted = [...list].sort((a, b) => b.createdAt - a.createdAt);
  const pinned = sorted.filter((s) => pinnedIds.value.has(s.id));
  const rest = sorted.filter((s) => !pinnedIds.value.has(s.id));
  const expanded = expandedKeys.value.has(key);
  return {
    key,
    workspaceId: key === UNGROUPED_KEY ? '' : key,
    label,
    expanded,
    active: key !== UNGROUPED_KEY && key === workspaceState.activeWorkspaceId.value,
    sessions: [...pinned, ...rest],
  };
}

function toggle(key: string): void {
  const next = new Set(expandedKeys.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expandedKeys.value = next;
}

// active workspace 变化时自动展开其分组（dsh 同款：当前组默认展开）。
watch(
  () => workspaceState.activeWorkspaceId.value,
  (id) => {
    if (!id) return;
    const next = new Set(expandedKeys.value);
    next.add(id);
    expandedKeys.value = next;
  },
);

function statusOf(id: string): SessionActivityStatus {
  if (runningSessionIds.value.has(id)) return 'running';
  return statusBySession.value[id] ?? 'idle';
}

function onMenuOpenChange(key: string, open: boolean): void {
  if (!open) return;
  // 菜单打开时清掉其它组的遗留中态，避免两个组同时处于确认/重命名。
  if (confirmingKey.value && confirmingKey.value !== key) cancelConfirm();
  if (renamingKey.value && renamingKey.value !== key) cancelRename();
}

function startRename(group: Group, close: () => void): void {
  if (!group.workspaceId) return;
  close();
  renameDraft.value = group.label;
  renamingKey.value = group.key;
  void nextTick(() => renameInput.value?.select());
}

async function commitRename(group: Group): Promise<void> {
  if (renamingKey.value !== group.key) return;
  renamingKey.value = null;
  const trimmed = renameDraft.value.trim();
  if (!trimmed || trimmed === group.label) return;
  const dup = workspaceState.workspaces.value.find(
    (w) => w.id !== group.workspaceId && w.name.trim() === trimmed,
  );
  if (dup) {
    showToast(t('workspace.edit.duplicate'), 'error');
    return;
  }
  try {
    await workspaceState.renameWorkspace(group.workspaceId, trimmed);
  } catch (e) {
    showToast((e as Error).message || 'Rename', 'error');
  }
}

function cancelRename(): void {
  renamingKey.value = null;
  renameDraft.value = '';
}

function cancelConfirm(): void {
  confirmingKey.value = null;
  deletingKey.value = null;
}

async function confirmDelete(group: Group, close: () => void): Promise<void> {
  if (!group.workspaceId) return;
  close();
  deletingKey.value = group.key;
  try {
    await workspaceState.deleteWorkspace(group.workspaceId, {
      force: group.sessions.length > 0,
    });
  } catch (e) {
    showToast((e as Error).message || 'Delete', 'error');
  }
}

async function startMove(group: Group, close: () => void): Promise<void> {
  if (!group.workspaceId) return;
  close();
  movingKey.value = group.key;
  try {
    const r = await window.darvin.setWorkspaceRoot();
    if (r.canceled || !r.rootPath) return;
    const owner = workspaceState.workspaces.value.find(
      (w) => w.id !== group.workspaceId && w.rootPath === r.rootPath,
    );
    if (owner) {
      showToast(t('workspace.errors.conflict'), 'error');
      return;
    }
    await workspaceState.updateWorkspaceRoot(group.workspaceId, r.rootPath);
  } catch (e) {
    showToast((e as Error).message || 'Move', 'error');
  } finally {
    movingKey.value = null;
  }
}
</script>