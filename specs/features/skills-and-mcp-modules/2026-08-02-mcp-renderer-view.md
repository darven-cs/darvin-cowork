# Sub-spec 37 — MCP Renderer View

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：`McpView.vue` + composables + service + 3 个子组件 + i18n。**不包含** Go / main 端逻辑（spec 34 / 35 / 36）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 36 mcp-main-store-and-ipc](./2026-08-02-mcp-main-store-and-ipc.md)

---

## 1. 概述

### 1.1 问题 / 背景

侧栏 `MCP` nav 当前跳 `PlaceholderView` 空态。本 spec 把 `McpView` 实现为可用 UI，参考 LobsterAI 的 `McpManager.tsx` + `McpView.tsx` 结构，但适配 darvin-cowork 的 Vue3 + Tailwind 栈。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 列表展示 server 卡片（name / description / transport / enabled 开关 / 连接状态） | live 验证 |
| G2 | 新增 / 编辑 / 删除 server 通过 modal 表单 | live 验证 |
| G3 | 表单按 transportType 切：stdio（command + args + env）/ http（url + headers） | live 验证 |
| G4 | 测试连接：触发 `testMcpConnection` 显示 tools 列表 | live 验证 |
| G5 | 重试 launch resolution | live 验证 |
| G6 | i18n 35+ key 齐全（zh + en） | `assertSameKeys` 通过 |
| G7 | 移除 `mcp` 的 PlaceholderView 路由 | `AppShell.vue` 不再有 mcp 占位 |

### 1.3 非目标

- 不做 marketplace 拉取
- 不做 OAuth / auth UI
- 不做 connection 日志详情
- 不做 server 详情页（v0 用 modal）

---

## 2. 用户场景

### 场景 1：进入 McpView

**Given** 用户首次打开 MCP nav；bundled filesystem 已注册
**When** 用户点侧栏 `MCP` 图标
**Then**：
- 跳到 `McpView`
- 看到 1 张卡片：filesystem（内置 / 启用 / connected / 4 tools）
- 右上角 [+ 新增] 按钮

### 场景 2：新增 stdio MCP server

**Given** 用户点 [+ 新增]
**When** modal 打开，填：name=`github`，transport=`stdio`，command=`npx`，args=`-y @modelcontextprotocol/server-github`，env=`GITHUB_TOKEN=ghp_xxx`
**Then** 点 [保存] → 调 create → 卡片新增一条，状态 `connecting` → 1-3s 后 `connected` + 4 tools

### 场景 3：新增 http MCP server

**Given** transport 切到 `http`
**When** 填 url=`http://localhost:3001/mcp`，无 headers
**Then** 走 http transport；流程同场景 2

### 场景 4：编辑 server

**Given** 用户点 github 卡片 [编辑]
**When** modal 打开，预填现有数据
**Then** 改 args 加 `--extra` → [保存] → 卡片状态 `connecting`（resolver 重跑）→ `connected`

### 场景 5：测试连接

**Given** 用户点 [测试连接] 按钮
**When** 触发 `testMcpConnection`
**Then**：
- 成功：toast「连接成功，4 tools」+ 展开 tools 列表
- 失败：toast「连接失败：ECONNREFUSED」

### 场景 6：禁用 / 启用

**Given** filesystem enabled
**When** 用户关掉 toggle
**Then** 卡片状态 disconnected，exposedTools 消失
**When** 用户再开
**Then** 状态 connecting → connected + 4 tools

### 场景 7：删除 server

**Given** github server 存在
**When** 用户点 [删除] → 二次确认
**Then** 卡片消失；调 `window.darvin.mcp.delete({ id: 'github' })`

### 场景 8：launch resolution 重试

**Given** filesystem server status=`error`（launch failed）
**When** 用户点 [重试 launch] 按钮
**Then** 调 `retryLaunchResolution` → status 变 pending → installing → ready → connected

### 场景 9：连接失败显示详情

**Given** server 连接失败（status=`error`，error=`ECONNREFUSED`）
**When** 用户 hover 状态徽章
**Then** tooltip 显示完整 error message

---

## 3. 功能需求

### FR-1: composable

```typescript
// src/renderer/composables/useMcpServers.ts
export function useMcpServers() {
  const servers = ref<DarvinMcpServer[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function refresh(): Promise<void> {
    loading.value = true;
    try {
      const r = await window.darvin.mcp.list();
      servers.value = r.servers;
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      loading.value = false;
    }
  }

  async function create(req: DarvinMcpServerCreate): Promise<DarvinMcpServer> {
    const r = await window.darvin.mcp.create(req);
    servers.value = [...servers.value, r.server];
    return r.server;
  }

  async function update(id: string, patch: DarvinMcpServerPatch): Promise<void> {
    const r = await window.darvin.mcp.update({ id, patch });
    const idx = servers.value.findIndex(s => s.id === id);
    if (idx >= 0) servers.value[idx] = r.server;
  }

  async function remove(id: string): Promise<void> {
    await window.darvin.mcp.delete({ id });
    servers.value = servers.value.filter(s => s.id !== id);
  }

  async function setEnabled(id: string, enabled: boolean): Promise<void> {
    // 乐观更新
    const idx = servers.value.findIndex(s => s.id === id);
    if (idx >= 0) servers.value[idx] = { ...servers.value[idx], enabled };

    try {
      await window.darvin.mcp.setEnabled({ id, enabled });
    } catch (e) {
      if (idx >= 0) servers.value[idx] = { ...servers.value[idx], enabled: !enabled };
      throw e;
    }
  }

  async function testConnection(id: string): Promise<{ ok: boolean; error?: string; tools?: any[] }> {
    return window.darvin.mcp.test({ id });
  }

  async function retryResolution(id: string): Promise<void> {
    await window.darvin.mcp.retryResolution({ id });
  }

  onMounted(() => {
    refresh();

    const unsubServers = window.darvin.mcp.onServersChanged((next) => {
      servers.value = next;
    });
    const unsubConnection = window.darvin.mcp.onConnectionChanged(({ id, status, error }) => {
      const idx = servers.value.findIndex(s => s.id === id);
      if (idx < 0) return;
      servers.value[idx] = {
        ...servers.value[idx],
        connectionStatus: status,
        connectionError: error,
      };
    });

    onUnmounted(() => {
      unsubServers();
      unsubConnection();
    });
  });

  return {
    servers,
    loading,
    error,
    refresh,
    create,
    update,
    remove,
    setEnabled,
    testConnection,
    retryResolution,
  };
}
```

### FR-2: McpServerCard

```vue
<!-- src/renderer/components/mcp/McpServerCard.vue -->
<template>
  <div class="rounded-lg border border-border bg-surface p-4 flex flex-col gap-3">
    <div class="flex items-start justify-between">
      <div class="flex items-center gap-2 flex-1 min-w-0">
        <Icon name="link" :size="18" />
        <h3 class="text-sm font-medium truncate">{{ server.name }}</h3>
        <span v-if="server.isBuiltIn"
              class="text-[11px] px-1.5 py-0.5 rounded bg-primary-muted text-primary shrink-0">
          {{ t('mcp.badge.builtin') }}
        </span>
        <McpConnectionStatus :status="server.connectionStatus" :error="server.connectionError" />
        <McpLaunchStatus v-if="server.launchStatus && server.launchStatus !== 'ready'"
                        :status="server.launchStatus" :error="server.launchError" />
      </div>
      <Switch :checked="server.enabled" @change="onToggle" />
    </div>

    <p class="text-xs text-text-muted line-clamp-2">{{ server.description || '—' }}</p>

    <div class="flex items-center gap-3 text-[11px] text-text-subtle">
      <span>{{ t(`mcp.transport.${server.transportType}`) }}</span>
      <span v-if="server.transportType === 'stdio' && server.command">
        <span class="font-mono">{{ server.command }} {{ (server.args || []).join(' ') }}</span>
      </span>
      <span v-else-if="server.url">
        <span class="font-mono">{{ server.url }}</span>
      </span>
    </div>

    <!-- tools 列表（连接成功后展示） -->
    <div v-if="server.exposedTools?.length" class="border-t border-border pt-2">
      <div class="text-[11px] text-text-subtle mb-1">
        {{ t('mcp.tools.count', { count: server.exposedTools.length }) }}
      </div>
      <div class="flex flex-wrap gap-1">
        <span v-for="tool in server.exposedTools" :key="tool.name"
              class="text-[11px] px-1.5 py-0.5 rounded bg-surface-raised text-text-muted font-mono">
          {{ tool.name }}
        </span>
      </div>
    </div>

    <div class="flex items-center justify-end gap-2 pt-1">
      <button class="text-xs text-text-muted hover:text-text"
              :disabled="!server.enabled"
              @click="emit('test', server)">
        {{ t('mcp.action.test') }}
      </button>
      <button v-if="server.launchStatus === 'failed'"
              class="text-xs text-warning hover:text-warning-hover"
              @click="emit('retry', server)">
        {{ t('mcp.action.retry') }}
      </button>
      <button class="text-xs text-text-muted hover:text-text"
              @click="emit('edit', server)">
        {{ t('mcp.action.edit') }}
      </button>
      <button class="text-xs text-danger hover:text-danger-hover"
              :disabled="server.isBuiltIn"
              @click="emit('delete', server)">
        {{ t('mcp.action.delete') }}
      </button>
    </div>
  </div>
</template>
```

### FR-3: McpConnectionStatus

```vue
<!-- src/renderer/components/mcp/McpConnectionStatus.vue -->
<template>
  <span :class="['inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px]', statusClass]"
        :title="props.error">
    <span :class="['w-1.5 h-1.5 rounded-full', dotClass]" />
    {{ t(`mcp.connection.${status}`) }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import type { McpConnectionStatus } from '../../../shared/darvin-api';

const props = defineProps<{
  status?: McpConnectionStatus;
  error?: string;
}>();

const status = computed(() => props.status ?? 'disconnected');

const statusClass = computed(() => {
  switch (status.value) {
    case 'connected':    return 'bg-success-muted text-success';
    case 'connecting':   return 'bg-warning-muted text-warning';
    case 'error':        return 'bg-danger-muted text-danger';
    case 'disconnected':
    default:             return 'bg-surface-raised text-text-subtle';
  }
});

const dotClass = computed(() => {
  switch (status.value) {
    case 'connected':    return 'bg-success';
    case 'connecting':   return 'bg-warning animate-pulse';
    case 'error':        return 'bg-danger';
    default:             return 'bg-text-subtle';
  }
});
</script>
```

### FR-4: McpLaunchStatus

```vue
<!-- src/renderer/components/mcp/McpLaunchStatus.vue -->
<template>
  <span :class="['text-[11px] px-1.5 py-0.5 rounded', statusClass]"
        :title="props.error">
    {{ t(`mcp.launch.${status}`) }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { t } from '../../services/i18n';
import type { McpLaunchStatus } from '../../../shared/darvin-api';

const props = defineProps<{
  status: McpLaunchStatus;
  error?: string;
}>();

const statusClass = computed(() => {
  switch (props.status) {
    case 'pending':      return 'bg-surface-raised text-text-subtle';
    case 'installing':   return 'bg-warning-muted text-warning';
    case 'ready':        return '';  // 不显示，spec 规定 ready 不显示
    case 'failed':       return 'bg-danger-muted text-danger';
    case 'unsupported':  return 'bg-surface-raised text-text-subtle';
    default:             return '';
  }
});
</script>
```

### FR-5: McpServerFormModal

```vue
<!-- src/renderer/components/mcp/McpServerFormModal.vue -->
<template>
  <Modal :open="open"
         :title="editing ? t('mcp.modal.edit_title') : t('mcp.modal.create_title')"
         @close="emit('cancel')">
    <div class="space-y-3 max-w-md">
      <FormField :label="t('mcp.field.name')">
        <input v-model="form.name" class="w-full px-3 py-1.5 text-sm border border-border rounded" />
      </FormField>

      <FormField :label="t('mcp.field.description')">
        <textarea v-model="form.description"
                  class="w-full px-3 py-1.5 text-sm border border-border rounded" rows="2" />
      </FormField>

      <FormField :label="t('mcp.field.transport_type')">
        <select v-model="form.transportType" class="w-full px-3 py-1.5 text-sm border border-border rounded">
          <option value="stdio">stdio</option>
          <option value="http">http</option>
          <option value="sse" disabled>sse (v1)</option>
        </select>
      </FormField>

      <!-- stdio 字段 -->
      <template v-if="form.transportType === 'stdio'">
        <FormField :label="t('mcp.field.command')">
          <input v-model="form.command" placeholder="npx" class="w-full px-3 py-1.5 text-sm font-mono border border-border rounded" />
        </FormField>
        <FormField :label="t('mcp.field.args')">
          <input v-model="argsStr" placeholder="-y @scope/pkg@latest" class="w-full px-3 py-1.5 text-sm font-mono border border-border rounded" />
        </FormField>
        <FormField :label="t('mcp.field.env')">
          <textarea v-model="envStr" placeholder="KEY1=val1&#10;KEY2=val2"
                    class="w-full px-3 py-1.5 text-sm font-mono border border-border rounded" rows="3" />
        </FormField>
      </template>

      <!-- http 字段 -->
      <template v-else-if="form.transportType === 'http'">
        <FormField :label="t('mcp.field.url')">
          <input v-model="form.url" placeholder="http://localhost:3001/mcp" class="w-full px-3 py-1.5 text-sm font-mono border border-border rounded" />
        </FormField>
        <FormField :label="t('mcp.field.headers')">
          <textarea v-model="headersStr" placeholder="Authorization=Bearer xxx"
                    class="w-full px-3 py-1.5 text-sm font-mono border border-border rounded" rows="3" />
        </FormField>
      </template>
    </div>

    <template #footer>
      <button class="px-3 py-1.5 text-sm text-text-muted hover:text-text" @click="emit('cancel')">
        {{ t('common.cancel') }}
      </button>
      <button class="px-3 py-1.5 text-sm bg-primary text-white rounded hover:bg-primary-hover disabled:opacity-50"
              :disabled="!canSave || saving"
              @click="onSave">
        {{ saving ? t('common.saving') : t('common.save') }}
      </button>
    </template>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { t } from '../../services/i18n';
import type { DarvinMcpServer, DarvinMcpServerCreate, DarvinMcpServerPatch } from '../../../shared/darvin-api';
import Modal from '../common/Modal.vue';
import FormField from '../common/FormField.vue';

const props = defineProps<{
  open: boolean;
  editing?: DarvinMcpServer;
  saving?: boolean;
}>();
const emit = defineEmits<{
  save: [req: DarvinMcpServerCreate | { id: string; patch: DarvinMcpServerPatch }];
  cancel: [];
}>();

// ... form 状态管理、解析 argsStr / envStr / headersStr
</script>
```

### FR-6: McpView 主视图

```vue
<!-- src/renderer/views/McpView.vue -->
<template>
  <div class="flex h-full flex-col">
    <ChatHeader :title="t('sidebar.nav.mcp')" @toggle-sidebar="..." />

    <div class="flex-1 overflow-y-auto p-4">
      <div class="max-w-3xl mx-auto space-y-4">
        <div class="flex items-center justify-between">
          <h2 class="text-base font-medium">{{ t('mcp.list.title') }}</h2>
          <button class="text-sm text-primary hover:underline" @click="openCreate">
            {{ t('mcp.list.add') }}
          </button>
        </div>

        <div v-if="loading" class="text-center text-text-muted py-8">
          {{ t('common.loading') }}
        </div>

        <div v-else-if="!servers.length" class="text-center text-text-muted py-8">
          {{ t('mcp.list.empty') }}
        </div>

        <div v-else class="space-y-3">
          <McpServerCard v-for="server in servers" :key="server.id"
                         :server="server"
                         @toggle="onToggle"
                         @test="onTest"
                         @retry="onRetry"
                         @edit="openEdit"
                         @delete="onDelete" />
        </div>
      </div>
    </div>

    <McpServerFormModal v-if="modalOpen"
                        :open="modalOpen"
                        :editing="editingServer"
                        :saving="saving"
                        @save="onSave"
                        @cancel="modalOpen = false" />

    <ConfirmDialog v-if="confirmDelete"
                   :title="t('mcp.delete.confirm_title')"
                   :message="t('mcp.delete.confirm_message', { name: confirmDelete.name })"
                   @confirm="onConfirmDelete"
                   @cancel="confirmDelete = null" />
  </div>
</template>
```

### FR-7: 路由更新

```typescript
// src/renderer/layout/AppShell.vue 增量
// 移除 mcp 的 PLACEHOLDERS entry
```

### FR-8: i18n 新增 key（~35 个）

| Key | 中文 | 英文 |
|-----|------|------|
| `mcp.list.title` | MCP 服务器 | MCP Servers |
| `mcp.list.add` | + 新增 MCP server | + Add MCP server |
| `mcp.list.empty` | 还没有 MCP server；点上方按钮新增 | No MCP servers yet; click above to add |
| `mcp.badge.builtin` | 内置 | Built-in |
| `mcp.transport.stdio` | stdio | stdio |
| `mcp.transport.http` | http | http |
| `mcp.transport.sse` | sse | sse |
| `mcp.connection.disconnected` | 未连接 | Disconnected |
| `mcp.connection.connecting` | 连接中 | Connecting |
| `mcp.connection.connected` | 已连接 | Connected |
| `mcp.connection.error` | 连接失败 | Connection failed |
| `mcp.launch.pending` | 等待解析 | Pending |
| `mcp.launch.installing` | 正在安装 | Installing |
| `mcp.launch.failed` | 安装失败 | Install failed |
| `mcp.launch.unsupported` | 不支持优化 | No optimization |
| `mcp.tools.count` | {count} 个工具 | {count} tools |
| `mcp.action.test` | 测试连接 | Test connection |
| `mcp.action.retry` | 重试安装 | Retry install |
| `mcp.action.edit` | 编辑 | Edit |
| `mcp.action.delete` | 删除 | Delete |
| `mcp.modal.create_title` | 新增 MCP server | Add MCP server |
| `mcp.modal.edit_title` | 编辑 MCP server | Edit MCP server |
| `mcp.field.name` | 名称 | Name |
| `mcp.field.description` | 描述（可选） | Description (optional) |
| `mcp.field.transport_type` | Transport 类型 | Transport type |
| `mcp.field.command` | 命令 | Command |
| `mcp.field.args` | 参数（空格分隔） | Args (space-separated) |
| `mcp.field.env` | 环境变量（KEY=val 一行一个） | Env vars (one KEY=val per line) |
| `mcp.field.url` | URL | URL |
| `mcp.field.headers` | 请求头（KEY=val 一行一个） | Headers (one KEY=val per line) |
| `mcp.test.success` | 连接成功，{count} 个工具 | Connection successful, {count} tools |
| `mcp.test.failed` | 连接失败：{error} | Connection failed: {error} |
| `mcp.delete.confirm_title` | 确认删除 | Confirm delete |
| `mcp.delete.confirm_message` | 删除 MCP server「{name}」？ | Delete MCP server "{name}"? |
| `mcp.create.success` | 已创建 {name} | Created {name} |
| `mcp.create.failed` | 创建失败：{error} | Create failed: {error} |
| `mcp.update.success` | 已更新 {name} | Updated {name} |
| `mcp.update.failed` | 更新失败：{error} | Update failed: {error} |
| `mcp.delete.success` | 已删除 {name} | Deleted {name} |

---

## 4. 实现方案

### 4.1 文件清单

```
src/renderer/
├── views/
│   └── McpView.vue                       🆕
├── composables/
│   ├── useMcpServers.ts                  🆕
│   └── useMcpServers.test.ts             🆕
├── services/
│   └── mcpService.ts                     🆕（thin wrapper，可选）
├── components/mcp/
│   ├── McpServerCard.vue                 🆕
│   ├── McpServerCard.test.ts             🆕
│   ├── McpServerFormModal.vue            🆕
│   ├── McpServerFormModal.test.ts        🆕
│   ├── McpConnectionStatus.vue           🆕
│   ├── McpLaunchStatus.vue               🆕
│   └── index.ts                          🆕
├── layout/
│   └── AppShell.vue                      移除 mcp 的 PlaceholderView 路由
├── services/
│   └── i18n.ts                           +35 key
└── assets/icons/
    └── cube.svg                          🆕 (mcp server icon)
```

### 4.2 关键代码片段（见 FR-1 ~ FR-6）

### 4.3 关键决策与理由

#### 4.3.1 表单按 transportType 切（不显示无关字段）

**理由**：stdio 不需要 URL；http 不需要 command。简洁。

#### 4.3.2 args / env / headers 用字符串输入（不 KeyValue 列表）

**理由**：darvin-cowork 偏好 CLI 风格输入（参考 `~/.bashrc` 编辑方式）；表单更紧凑。

#### 4.3.3 删除 / 启停 / 测试 按钮全部在卡片底部

**理由**：操作频次低但需要可发现性；统一位置减少误操作。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `useMcpServers.test.ts` | refresh / create / update / remove / setEnabled 乐观更新 / 订阅 onServersChanged + onConnectionChanged |
| `McpServerCard.test.ts` | 显示 name / description / transport / status / tools / 按钮 disabled 条件 |
| `McpServerFormModal.test.ts` | 切换 transportType 切字段 / save 事件 payload / cancel 关闭 |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| bundled filesystem 显示但不让删 | delete 按钮 disabled + tooltip「内置不可删」 |
| connection_status 频繁变化（重连循环） | UI 显示「connecting」脉冲；不 throttle（spec 36 main 节流） |
| launch_status=ready 但 connection=error | 两个状态独立显示（launch 已就绪但运行时连接失败） |
| 表单 args 多行（带空格） | argsStr 拆分时按 shell 风格 split（不在前端做，main 端按 JSON 解析） |
| 重复创建同名 server | 允许（id 不同） |
| 修改 args 触发 resolver 重跑 | UI 显示「installing」徽章 |
| 用户在 modal 关闭前 network 失败 | toast「保存失败」+ modal 不关，用户可重试 |
| 用户在 modal 关闭前 App 重启 | 数据未保存（无草稿功能） |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/renderer/views/McpView.vue` | 🆕 |
| `src/renderer/composables/useMcpServers.ts` | 🆕 |
| `src/renderer/composables/useMcpServers.test.ts` | 🆕 |
| `src/renderer/components/mcp/McpServerCard.vue` | 🆕 |
| `src/renderer/components/mcp/McpServerCard.test.ts` | 🆕 |
| `src/renderer/components/mcp/McpServerFormModal.vue` | 🆕 |
| `src/renderer/components/mcp/McpServerFormModal.test.ts` | 🆕 |
| `src/renderer/components/mcp/McpConnectionStatus.vue` | 🆕 |
| `src/renderer/components/mcp/McpLaunchStatus.vue` | 🆕 |
| `src/renderer/components/mcp/index.ts` | 🆕 |
| `src/renderer/layout/AppShell.vue` | 移除 mcp 的 PlaceholderView 路由 |
| `src/renderer/services/i18n.ts` | +35 key |
| `src/renderer/assets/icons/cube.svg` | 🆕 |

---

## 7. 验收标准

**通用**：
- [ ] `npm run lint` + `npm run test` 通过
- [ ] `npm run build` 成功
- [ ] i18n `assertSameKeys(dictZh, dictEn)` 通过

**FR-1 useMcpServers**：
- [ ] refresh / create / update / remove / setEnabled 调 window.darvin.mcp.*
- [ ] 乐观更新 + 失败回滚
- [ ] 订阅 onServersChanged + onConnectionChanged

**FR-2 McpServerCard**：
- [ ] 显示 name / description / transport / enabled / connection / launch 状态
- [ ] tools 列表展开
- [ ] 按钮：test / retry / edit / delete
- [ ] builtIn 禁用 delete

**FR-3 ConnectionStatus**：
- [ ] 4 状态对应不同颜色 + dot 动画
- [ ] hover tooltip 显示 error

**FR-4 LaunchStatus**：
- [ ] 5 状态对应颜色
- [ ] ready 状态不显示

**FR-5 FormModal**：
- [ ] 按 transportType 切字段
- [ ] args / env / headers 字符串解析
- [ ] save 事件 payload 正确

**FR-6 McpView**：
- [ ] 列表 + 添加按钮 + 空态
- [ ] 卡片 + modal + 确认对话框

**FR-7 路由**：
- [ ] `AppShell.vue` 不再有 mcp 占位
- [ ] 侧栏 `MCP` 跳 `McpView`

**FR-8 i18n**：
- [ ] 35+ key 齐全（zh + en）
- [ ] 缺 key dev warn 触发

**live 验证**：
- 侧栏 → MCP → 看到 filesystem 卡片（connected / 4 tools）
- [+ 新增] → modal → 填 stdio / npx / args → 保存 → 卡片新增 + 状态 connecting → connected
- 切换 transportType → 表单字段切换
- 卡片 [测试连接] → toast 显示工具数
- 卡片 [删除] → 二次确认 → 卡片消失
- toggle 开关 → 卡片状态 disconnected / connected

---

## 8. 与其他 spec 的关系

**前置**：spec 36

**下游**：
- spec 38 改造 tool.Registry 合并 mcp tool 后，renderer `ToolCallGroup` 按 `kind: 'mcp'` 渲染（spec 02 落地）

**并行**：spec 31 / 32 / 33（skills）

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现