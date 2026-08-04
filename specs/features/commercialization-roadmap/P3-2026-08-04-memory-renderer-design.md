# darvin-cowork Memory Subsystem — Renderer UI

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：沿用既有内容；文件名为本次从 `2026-MM-DD-*` 规范化为 `2026-08-04-*` 的新文件名，正文未重写。
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## Scope

Covers CHECKLIST section H (renderer UI). Section L "out of scope" items (Dreaming / DREAMS.md) go to a separate spec.

Reference: `LobsterAI/src/renderer/components/Settings.tsx:4899-5135`, `LobsterAI/src/renderer/components/cowork/EmbeddingSettingsSection.tsx`, `LobsterAI/src/renderer/components/cowork/DreamingSettingsSection.tsx`, `LobsterAI/specs/features/dreaming/2026-05-11-dreaming-design.md`.

## 1. 概述

darvin-cowork Settings → Memory tab 现在只有一个 SettingsPanelMemory.vue 平铺组件。本 spec 把它改成 **3 个子 tab**（对齐 LobsterAI）：

1. **Entries**：列表 + 增 / 删 / 改 / Raw view / Stats
2. **Embedding**：provider / model / vector weight / local model path / remote base URL / API key
3. **Dreaming**：独立 spec，本 spec 只占位（按钮 disabled + hint）

保留旧的 `enabled` / `embeddingProvider` / `apiKey` 三个 toggle（不破坏现有持久化路径）。

## 2. UI 结构

```vue
<!-- SettingsPanelMemory.vue -->
<template>
  <section class="flex flex-col gap-4" data-testid="settings-memory">
    <h3>{{ t('settings.memory.title') }}</h3>
    <p>{{ t('settings.memory.desc') }}</p>

    <!-- 三 tab 切换 -->
    <div role="tablist" class="flex gap-6 border-b border-border">
      <button role="tab" :aria-selected="tab === 'entries'" @click="tab = 'entries'">
        {{ t('settings.memory.tab.entries') }}
      </button>
      <button role="tab" :aria-selected="tab === 'embedding'" @click="tab = 'embedding'">
        {{ t('settings.memory.tab.embedding') }}
      </button>
      <button role="tab" :aria-selected="tab === 'dreaming'" @click="tab = 'dreaming'" disabled>
        {{ t('settings.memory.tab.dreaming') }}
      </button>
    </div>

    <div v-if="tab === 'entries'"><EntriesTab /></div>
    <div v-else-if="tab === 'embedding'"><EmbeddingTab /></div>
    <div v-else><DreamingTabPlaceholder /></div>
  </section>
</template>
```

把现有 `enabled` / `embeddingProvider` / `apiKey` 三个 toggle 整合进对应 tab。

## 3. Entries tab

### 3.1 顶栏：开关 + 重新索引按钮

```vue
<div class="flex items-center justify-between">
  <div>
    <label class="flex items-center justify-between">
      <span>{{ t('settings.memory.enabled') }}</span>
      <input type="checkbox" v-model="enabled" @change="persist({ enabled })"
             data-testid="settings-memory-enabled" />
    </label>
    <p class="text-text-muted">{{ t('settings.memory.enabled_desc') }}</p>
  </div>
  <button @click="onReindex" :disabled="reindexing">
    {{ reindexing ? t('settings.memory.reindex_running') : t('settings.memory.reindex') }}
  </button>
</div>
```

### 3.2 Stats summary

```vue
<div v-if="stats" class="text-xs text-text-muted">
  <span>{{ t('settings.memory.stats_total', { count: stats.total }) }}</span>
  <span v-for="s in stats.bySection" :key="s.section" class="ml-2">
    {{ s.section }}: {{ s.count }}
  </span>
</div>
```

### 3.3 搜索框

```vue
<input type="text" v-model="query" :placeholder="t('settings.memory.search_placeholder')"
       class="w-full ..." />
```

绑定 `query.value` → `useMemory.refresh({ query })` debounced 300ms。

### 3.4 分组列表（按 section）

对齐 LobsterAI `Settings.tsx:4899-5058`：

```vue
<div v-if="loading" class="text-text-muted">{{ t('common.loading') }}</div>
<div v-else-if="!entries.length" class="text-text-muted">{{ t('settings.memory.entries_empty') }}</div>
<div v-for="group in groupedEntries" :key="group.section || 'ungrouped'"
     class="rounded-lg border border-border">
  <div v-if="group.section" class="px-3 pb-1.5 pt-3 text-[11px] font-medium text-secondary">
    {{ group.section }} <span class="opacity-70">{{ group.entries.length }}</span>
  </div>
  <div class="divide-y divide-border">
    <div v-for="entry in group.entries" :key="entry.id"
         class="group px-3 py-3 text-xs transition-colors hover:bg-surface-raised/60">
      <div class="flex items-start justify-between gap-3">
        <div class="flex-1 min-w-0">
          <div :class="['text-foreground break-words whitespace-pre-wrap leading-relaxed',
                        isLong(entry) && !expanded.has(entry.id) ? 'line-clamp-3' : '']">
            {{ entry.text }}
          </div>
          <button v-if="isLong(entry)" @click="toggleExpanded(entry.id)"
                  class="mt-1.5 text-[11px] text-primary hover:underline">
            {{ expanded.has(entry.id) ? t('settings.memory.collapse') : t('settings.memory.expand') }}
          </button>
        </div>
        <div class="flex items-center gap-0.5 flex-shrink-0 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
          <button @click="onEdit(entry)" :aria-label="t('common.edit')"
                  class="rounded-md p-1.5 text-secondary hover:text-foreground hover:bg-surface-raised">
            <EditIcon class="h-4 w-4" />
          </button>
          <button @click="onDelete(entry)" :aria-label="t('common.delete')" :disabled="loading"
                  class="rounded-md p-1.5 text-secondary hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 disabled:opacity-60">
            <TrashIcon class="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  </div>
</div>
```

`isLong(entry)`：`entry.text.split('\n').length > 3 || entry.text.length > 240`。

### 3.5 创建 / 编辑 modal

沿用 LobsterAI 的 CoworkMemoryModal 模式：

```vue
<Modal :is-open="editing !== null" @close="editing = null">
  <h2>{{ editing.isNew ? t('settings.memory.crud_create_title') : t('settings.memory.crud_edit_title') }}</h2>
  <textarea v-model="form.text" rows="6" />
  <input v-model="form.section" :placeholder="t('settings.memory.section_placeholder')" />
  <button @click="onSave" :disabled="!form.text.trim()">
    {{ t('common.save') }}
  </button>
</Modal>
```

`onSave`：
- isNew → `await window.darvin.createMemoryEntry({ text, section, isExplicit: true })`
- editing → `await window.darvin.updateMemoryEntry({ id, text, section })`

### 3.6 Raw view modal

```vue
<Modal :is-open="rawMode" @close="rawMode = false" overlay-class-name="..." class-name="...">
  <h2>{{ t('settings.memory.raw_button') }}</h2>
  <p>{{ t('settings.memory.raw_hint') }}</p>
  <button @click="rawMode = false"><XIcon /></button>
  <textarea v-model="rawText" spellcheck="false" autofocus
            class="min-h-0 w-full flex-1 resize-none bg-transparent font-mono" />
  <div class="flex justify-end gap-2">
    <button @click="rawMode = false">{{ t('common.cancel') }}</button>
    <button @click="onSaveRaw" :disabled="rawSaving">{{ t('common.save') }}</button>
  </div>
</Modal>
```

`onSaveRaw`：`await window.darvin.writeMemoryRaw({ content: rawText })` → 刷新列表 → toast 成功。

### 3.7 切换 workspace 提示

提示文案：

> 「MEMORY.md 路径：`<workspaceRoot>/state/workspace-main/MEMORY.md`」 + `在文件管理器中打开` 按钮

## 4. Embedding tab

### 4.1 现有三 toggle 不动

```vue
<label>{{ t('settings.memory.embedding_enabled') }}</label>
<input type="checkbox" v-model="embeddingEnabled" @change="persist({ embeddingEnabled })" />
```

### 4.2 新增字段（对齐 LobsterAI `EmbeddingSettingsSection.tsx`）

```vue
<label>
  <span>{{ t('settings.memory.embedding_provider') }}</span>
  <select v-model="embeddingProvider" @change="persist({ embeddingProvider })">
    <option value="openai">OpenAI</option>
    <option value="gemini">Gemini</option>
    <option value="voyage">Voyage</option>
    <option value="mistral">Mistral</option>
    <option value="ollama">Ollama (local)</option>
    <option value="local">Local path</option>
  </select>
</label>

<label>
  <span>{{ t('settings.memory.embedding_model') }}</span>
  <input type="text" v-model="embeddingModel"
         :placeholder="t('settings.memory.embedding_model_placeholder')"
         @change="persist({ embeddingModel })" />
</label>

<label v-if="embeddingProvider === 'local'">
  <span>{{ t('settings.memory.embedding_local_model_path') }}</span>
  <input type="text" v-model="embeddingLocalModelPath" @change="persist({ embeddingLocalModelPath })" />
</label>

<label>
  <span>{{ t('settings.memory.embedding_vector_weight') }}</span>
  <input type="number" min="0" max="1" step="0.05"
         v-model.number="embeddingVectorWeight" @change="persist({ embeddingVectorWeight })" />
</label>

<label v-if="embeddingProvider !== 'local' && embeddingProvider !== 'ollama'">
  <span>{{ t('settings.memory.embedding_remote_base_url') }}</span>
  <input type="text" v-model="embeddingRemoteBaseUrl" @change="persist({ embeddingRemoteBaseUrl })" />
</label>

<label v-if="embeddingProvider !== 'local' && embeddingProvider !== 'ollama'">
  <span>{{ t('settings.memory.embedding_api_key') }}</span>
  <input type="password" v-model="embeddingRemoteApiKey" @change="persist({ embeddingRemoteApiKey })" />
</label>
```

### 4.3 hint 文案

> 「Embedding 向量检索 v1 不接，schema 字段已保留。重启 Go agent 后生效。」

## 5. Dreaming tab 占位

```vue
<div>
  <h4>{{ t('settings.memory.dreaming_title') }}</h4>
  <p class="text-text-muted">{{ t('settings.memory.dreaming_placeholder_hint') }}</p>
  <button disabled class="opacity-50 cursor-not-allowed">
    {{ t('settings.memory.dreaming_configure_button') }}
  </button>
</div>
```

占位：未来 spec 落地后这个 tab 渲染 `<DreamingSettingsSection>`。

## 6. State management

### 6.1 useMemory composable（已有，需扩）

```ts
// src/renderer/composables/useMemory.ts
import { ref, computed, watch } from 'vue';
import type { DarvinMemoryEntry, DarvinMemoryStats, DarvinMemorySectionCount } from '../../shared/darvin-api';

const entries = ref<DarvinMemoryEntry[]>([]);
const stats = ref<DarvinMemoryStats | null>(null);
const query = ref('');
const loading = ref(false);
const rawText = ref('');
const rawPath = ref('');
const rawMode = ref(false);
const tab = ref<'entries' | 'embedding' | 'dreaming'>('entries');

const groupedEntries = computed(() => {
  const groups = new Map<string, DarvinMemoryEntry[]>();
  for (const e of entries.value) {
    const key = e.section || '';
    const list = groups.get(key) ?? [];
    list.push(e);
    groups.set(key, list);
  }
  return [...groups.entries()].map(([section, items]) => ({ section, items }));
});

let debounceTimer: number | null = null;
async function refresh() {
  loading.value = true;
  try {
    const [listR, statsR] = await Promise.all([
      window.darvin.listMemoryEntries({ query: query.value.trim(), limit: 200 }),
      window.darvin.getMemoryStats(),
    ]);
    entries.value = listR.entries;
    stats.value = statsR.stats;
  } catch { showToast(t('settings.memory.list_failed'), 'error'); }
  finally { loading.value = false; }
}

watch(query, () => {
  if (debounceTimer) clearTimeout(debounceTimer);
  debounceTimer = window.setTimeout(refresh, 300);
});

async function onDelete(id: string) {
  if (!confirm(t('settings.memory.entries_delete_confirm'))) return;
  try { await window.darvin.deleteMemoryEntry({ id }); await refresh(); }
  catch { showToast(t('settings.memory.entry_delete_failed'), 'error'); }
}

async function onSave(form: { text: string; section: string }, id?: string) {
  try {
    if (id) await window.darvin.updateMemoryEntry({ id, text: form.text, section: form.section });
    else    await window.darvin.createMemoryEntry({ text: form.text, section: form.section, isExplicit: true });
    await refresh();
  } catch { /* toast */ }
}

async function onSaveRaw() {
  try {
    await window.darvin.writeMemoryRaw({ content: rawText.value });
    rawMode.value = false; await refresh();
    showToast(t('settings.memory.raw_save_done'), 'success');
  } catch { showToast(t('settings.memory.raw_save_failed'), 'error'); }
}

async function onReindex() {
  try { await window.darvin.reindexMemory(); await refresh();
    showToast(t('settings.memory.reindex_done'), 'success'); }
  catch { /* toast */ }
}

async function loadRaw() {
  const r = await window.darvin.readMemoryRaw();
  rawText.value = r.content; rawPath.value = r.path;
}

async function revealRaw() {
  if (rawPath.value) shell.showItemInFolder(rawPath.value);  // 通过 IPC
}

return {
  entries, stats, query, loading, rawText, rawPath, rawMode, tab,
  groupedEntries,
  refresh, onDelete, onSave, onSaveRaw, onReindex, loadRaw, revealRaw,
};
```

### 6.2 useMemorySettings composable（新增）

```ts
// src/renderer/composables/useMemorySettings.ts
import { ref, watch } from 'vue';
import { showToast } from '../services/toast';
import { t } from '../services/i18n';

const fields = {
  enabled: ref(false),
  implicitUpdateEnabled: ref(true),
  llmJudgeEnabled: ref(false),
  guardLevel: ref<'strict' | 'standard' | 'relaxed'>('strict'),
  userMemoriesMaxItems: ref(12),
  embeddingEnabled: ref(false),
  embeddingProvider: ref<'openai' | 'gemini' | 'voyage' | 'mistral' | 'ollama' | 'local'>('openai'),
  embeddingModel: ref(''),
  embeddingLocalModelPath: ref(''),
  embeddingVectorWeight: ref(0.7),
  embeddingRemoteBaseUrl: ref(''),
  embeddingRemoteApiKey: ref(''),
};

async function loadFromPrefs() {
  const p = await window.darvin.getAppPreferences();
  fields.enabled.value = p.memory.enabled ?? false;
  // ... 13 个字段映射
}

async function persist(patch: Partial<typeof fields>) {
  // 透传所有字段（renderer 全权持有，main 不做 whitelist）
  const memPatch = {};
  for (const k of Object.keys(fields)) {
    if (patch[k] !== undefined) memPatch[k] = patch[k];
  }
  await window.darvin.setAppPreferences({ memory: memPatch });
}

export function useMemorySettings() {
  return { fields, loadFromPrefs, persist };
}
```

## 7. IPC channel 增量

### 7.1 新增 IPC（main 端）

| channel | 行为 |
|---|---|
| `darvin:reveal_memory_raw` | `shell.showItemInFolder(rawPath)` |

### 7.2 修改现有 IPC

`darvin:set_app_preferences`：把 `memory: {...}` 整体透传给 yaml，不再 hardcode whitelist。

```ts
// src/main/index.ts
ipcMain.handle('darvin:set_app_preferences', async (_e, patch: DarvinAppPreferencesPatch) => {
  if (patch.autoLaunch !== undefined) app.setLoginItemSettings({ openAtLogin: patch.autoLaunch });
  await writeUserSettingsYAML({
    app: { notifications: patch.notifications, proxy: patch.proxy },
    memory: patch.memory,  // 透传
  });
});
```

## 8. i18n keys (~30 个新增)

按 `settings.memory.*` 加在 `src/renderer/services/i18n.ts`：

```ts
// tab labels
'settings.memory.tab.entries': '记忆条目',
'settings.memory.tab.embedding': 'Embedding 语义搜索',
'settings.memory.tab.dreaming': 'Dreaming 记忆整理',

// entries tab
'settings.memory.entries_empty': '还没有任何记忆条目',
'settings.memory.entries_search_placeholder': '搜索条目文本',
'settings.memory.entries_long_text_expand': '展开',
'settings.memory.entries_long_text_collapse': '折叠',
'settings.memory.entries_section_other': '（未分组）',
'settings.memory.entries_create_button': '+ 添加记忆',
'settings.memory.entries_create_title': '添加记忆条目',
'settings.memory.entries_edit_title': '编辑记忆条目',
'settings.memory.entries_delete_confirm': '删除这条记忆？此操作不可撤销。',
'settings.memory.entries_text_label': '事实',
'settings.memory.entries_section_label': '分组',
'settings.memory.entries_section_placeholder': '例如 preferences / project:foo',
'settings.memory.entries_path_label': '存储路径',
'settings.memory.entries_reveal_button': '在文件管理器中打开',
'settings.memory.entries_reindex_button': '重建索引',
'settings.memory.entries_reindex_running': '重建中…',
'settings.memory.entries_reindex_done': '索引已重建',
'settings.memory.entries_raw_button': '编辑原始 MEMORY.md',
'settings.memory.entries_raw_hint': '直接编辑文件内容，保存后会自动重新索引。',
'settings.memory.entries_raw_save_done': '已保存到 MEMORY.md',
'settings.memory.entries_raw_save_failed': '保存失败',
'settings.memory.entries_raw_load_failed': '加载失败',

// embedding tab
'settings.memory.embedding_section_title': '语义检索设置',
'settings.memory.embedding_model_label': '模型 ID',
'settings.memory.embedding_model_placeholder': 'text-embedding-3-small',
'settings.memory.embedding_local_model_path_label': '本地模型路径',
'settings.memory.embedding_local_model_path_placeholder': '/path/to/model.onnx',
'settings.memory.embedding_vector_weight_label': '向量权重（0 = 仅关键词，1 = 仅向量）',
'settings.memory.embedding_remote_base_url_label': '远程 API Base URL',
'settings.memory.embedding_remote_base_url_placeholder': 'https://api.openai.com/v1',
'settings.memory.embedding_api_key_label': 'API Key',
'settings.memory.embedding_hint_v1': 'Embedding 向量检索 v1 不接，schema 字段已保留。重启 Go agent 后生效。',

// dreaming tab (占位)
'settings.memory.dreaming_section_title': 'Dreaming 记忆整理',
'settings.memory.dreaming_placeholder_hint': 'Dreaming 功能将单独 spec 落地，本 tab 占位。',
'settings.memory.dreaming_configure_button': '配置 Dreaming（即将开放）',

// top-level
'settings.memory.stat_total_label': '共 {count} 条',
'settings.memory.stat_by_section_label': '按分组：',
```

zh + en 双语字典各自增 ~30 个 key，**强制 `assertSameKeys` 通过**。

## 9. 涉及文件

| 文件 | 操作 |
|---|---|
| `src/renderer/components/settings/SettingsPanelMemory.vue` | 重写（3 tab 结构） |
| `src/renderer/composables/useMemory.ts` | 扩充（query / tab / groupedEntries / modal state） |
| `src/renderer/composables/useMemorySettings.ts` | 新建（13 字段统一持久化） |
| `src/renderer/services/i18n.ts` | 修改（增 ~30 key） |
| `src/renderer/components/settings/SettingsSubNav.vue` | 不动 |
| `src/renderer/components/settings/settings-sections.ts` | 不动（memory tab 已在） |
| `src/shared/darvin-api.ts` | 修改（DarvinAppPreferences.memory 增字段；新 IPC 类型） |
| `src/main/index.ts` | 修改（`set_app_preferences` 透传 memory 字段；新增 `reveal_memory_raw`） |
| `src/main/runtime/client.ts` | 修改（client.memory.revealRaw / client.appPreferences.set 透传） |
| `src/preload/index.ts` | 修改 |

## 10. 验收标准

### Vitest / 组件测试

- 3 tab 切换正常
- entries 按 section 分组渲染
- 长文本 line-clamp-3 + expand 切换
- delete / edit modal 状态正确
- raw view modal 打开 → 加载内容 → 编辑 → save → 列表更新
- reindex 按钮 disabled 状态切换
- embedding tab 6 个字段全绑 + 透传

### 手工 smoke

1. Settings → Memory → entries tab 默认显示
2. 添加 entry：text="我喝燕麦奶" + section="preferences" → 列表立即出现
3. 编辑同一条 → 文本更新
4. 删一条 → 列表移除 + toast 成功
5. 搜索 "燕麦" → 列表过滤
6. Raw view → 编辑一行 → Save → reload → 内容保留
7. Embedding tab → 改 provider / model / vectorWeight → Save → 重启 → 持久化
8. Dreaming tab → 占位文案 + 按钮 disabled

### Playwright UI

- 三 tab 切换 → 断言 `aria-selected` 切换
- entries 列表增删改 → 断言 DOM 更新
- raw view save 后 IPC 验证后端落盘

## 11. 边界 / 非目标

| 场景 | 处理 |
|---|---|
| 用户在 embedding tab 切换 provider 时其他字段被清空 | 保留所有字段，只更新 provider；`embeddingLocalModelPath` 仅 provider=local 时显示 |
| raw view 编辑后忘了 reload | Save 后自动 `loadRaw()` 同步 UI |
| 删最后一条 entry | 列表显示 `entries_empty` 占位 |
| search query 与 section filter 冲突 | query 优先（全局搜索） |
| Modal 中 ESC | @close 关闭 |
| settings.memory.* 旧 key 保留 | 不删除，避免破坏现有翻译 |