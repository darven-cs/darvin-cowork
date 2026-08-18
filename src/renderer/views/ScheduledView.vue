<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import ChatHeader from '../components/chat/ChatHeader.vue';
import ScheduleList from '../components/scheduled/ScheduleList.vue';
import ScheduleForm from '../components/scheduled/ScheduleForm.vue';
import ScheduleDetail from '../components/scheduled/ScheduleDetail.vue';
import ScheduleRunHistory from '../components/scheduled/ScheduleRunHistory.vue';
import { useSchedules } from '../composables/useSchedules';
import { useWorkspaces } from '../composables/useWorkspaces';
import { t } from '../services/i18n';
import type { DarvinSchedule, DarvinScheduleInput } from '../../shared/darvin-api';

defineProps<{ sidePanelOpen: boolean }>();
defineEmits<{ 'toggle-sidebar': []; 'toggle-side-panel': [] }>();

type TabId = 'list' | 'history';

const tabs: Array<{ id: TabId; labelKey: string }> = [
  { id: 'list', labelKey: 'schedule.nav.title' },
  { id: 'history', labelKey: 'schedule.history.title' },
];
const activeTab = ref<TabId>('list');

const { schedules, runs, loadAll, loadAllRuns, create, update, remove, toggle, runNow, abort } = useSchedules();
const workspaces = useWorkspaces();

const workspaceId = computed(() => workspaces.activeWorkspaceId.value);

onMounted(() => {
  if (workspaceId.value) {
    void loadAll(workspaceId.value);
    void loadAllRuns(workspaceId.value);
  }
});

// workspaces.activeWorkspaceId is null at mount (renderer hasn't fetched
// workspaces yet); refetch when it becomes available.
watch(workspaceId, (id) => {
  if (id) {
    void loadAll(id);
    void loadAllRuns(id);
  }
});

const editing = ref<DarvinSchedule | null>(null);
const detailScheduleId = ref<string | null>(null);

function handleCreate(input: DarvinScheduleInput): void {
  if (!workspaceId.value) return;
  void create(workspaceId.value, input).then(() => {
    void loadAll(workspaceId.value!);
  });
}

function handleUpdate(scheduleId: string, patch: Partial<DarvinScheduleInput>): void {
  if (!workspaceId.value) return;
  void update(workspaceId.value, scheduleId, patch).then(() => {
    editing.value = null;
    void loadAll(workspaceId.value!);
  });
}

function handleDelete(scheduleId: string, name: string): void {
  if (!workspaceId.value) return;
  if (!confirm(t('schedule.confirm.delete', { name }))) return;
  void remove(workspaceId.value, scheduleId, name).then((ok) => {
    if (ok) void loadAll(workspaceId.value!);
  });
}

function handleToggle(scheduleId: string, enabled: boolean): void {
  if (!workspaceId.value) return;
  void toggle(workspaceId.value, scheduleId, enabled);
}

function handleRunNow(scheduleId: string, name: string): void {
  if (!workspaceId.value) return;
  void runNow(workspaceId.value, scheduleId, name);
}

function handleAbort(scheduleId: string, runId: string): void {
  if (!workspaceId.value) return;
  void abort(workspaceId.value, scheduleId, runId);
}

function openDetail(scheduleId: string): void {
  detailScheduleId.value = scheduleId;
}

function startEdit(schedule: DarvinSchedule): void {
  editing.value = schedule;
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col bg-bg text-text">
    <ChatHeader
      :title-key="'schedule.nav.title'"
      @toggle-sidebar="$emit('toggle-sidebar')"
      @toggle-side-panel="$emit('toggle-side-panel')"
    />
    <nav class="flex gap-2 border-b border-border px-6 py-2">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        type="button"
        class="rounded-md px-3 py-1 text-sm transition"
        :class="activeTab === tab.id ? 'bg-primary text-text-inverse' : 'text-text-muted hover:bg-surface-hover'"
        @click="activeTab = tab.id"
      >
        {{ t(tab.labelKey) }}
      </button>
    </nav>
    <main class="flex-1 min-h-0 overflow-auto px-6 py-4">
      <section v-if="activeTab === 'list'" class="flex flex-col gap-4">
        <ScheduleList
          :schedules="schedules"
          @run-now="handleRunNow"
          @toggle="handleToggle"
          @delete="handleDelete"
          @edit="startEdit"
          @open-detail="openDetail"
        />
        <ScheduleForm
          v-if="!editing"
          @submit="handleCreate"
        />
        <ScheduleForm
          v-else
          :initial="editing"
          mode="edit"
          @submit="handleUpdate(editing.id, $event)"
          @cancel="editing = null"
        />
        <ScheduleDetail
          v-if="detailScheduleId"
          :schedule-id="detailScheduleId"
          :schedule="schedules.find((s) => s.id === detailScheduleId) ?? null"
          :runs="runs[detailScheduleId] ?? []"
          @abort="handleAbort"
          @close="detailScheduleId = null"
        />
      </section>
      <section v-else-if="activeTab === 'history'" class="flex flex-col gap-4">
        <ScheduleRunHistory
          :workspace-id="workspaceId"
          :schedules="schedules"
          :runs-by-schedule="runs"
        />
      </section>
    </main>
  </div>
</template>