# Artifact 面板默认收起设计文档

## 1. 概述

### 1.1 问题 / 背景

当前每次收到 Go 的 `artifact` 事件，渲染层都会自动弹开右侧 Artifact 面板：

`useMessages.appendEventFor` → `useArtifacts.addArtifact()` → `openPreviewTab()` → `setPanelOpen(sid, true)` → `sidePanel.set(true)`。

在多 artifact 会话中，面板会反复「抢焦点」自动弹出，打断用户阅读聊天流。用户希望 artifact 默认收起：新 artifact 到达时不弹面板，用户手动点击卡片 / tab / 文件时才展开。

### 1.2 目标

- 后台 artifact 事件（来自 Go 事件流）不再自动弹开右侧面板。
- 面板开关保持用户当前状态（收起就保持收起）。
- 用户显式交互（点聊天卡片 / 点 tab / 点文件列表项）仍然打开面板。

### 1.3 非目标

- 不改 `ArtifactCardGroup` 的聊天内卡片折叠行为（已是默认折叠，显示前 3 张 + 展开按钮）。
- 不改首次启动时 `useSidePanel` 的默认开/关（无本地记录时维持现状 `true`）。
- 不引入未读 badge / 面板图标提示（后续迭代再说）。

## 2. 用户场景

### 场景 1: artifact 到达不弹面板
**Given** 用户正在看聊天流，右侧面板处于收起状态
**When** agent 产出一个 artifact 事件
**Then** 面板保持收起；artifact 登记进面板 tab（用户随后打开面板可看到）

### 场景 2: 手动点击卡片打开面板
**Given** 聊天流某 assistant 消息下方有 artifact 卡片组
**When** 用户点击某张卡片
**Then** 右侧面板打开并激活对应 artifact tab

### 场景 3: 文件列表点击打开面板
**Given** 用户已手动打开面板并在「文件」tab 里看文件列表
**When** 点击一个 html 文件
**Then** 面板保持打开并激活该文件的预览 tab

## 3. 功能需求

### FR-1: addArtifact 支持不弹面板
- `useArtifacts.addArtifact(sid, artifact, opts?: { openPanel?: boolean })`。
- 默认 `openPanel: true`（保持既有语义，用户显式触发的路径不变）。
- `openPanel: false` 时只登记 preview tab + 设激活，不调 `setPanelOpen(sid, true)`。

### FR-2: 后台 artifact 事件关闭自动弹出
- `useMessages.appendEventFor` 的 artifact 分支改为 `artifacts.addArtifact(sid, artifact, { openPanel: false })`。

### FR-3: 用户显式路径保持打开
- `ArtifactCardGroup.open` / `ArtifactPanel.activate` / `FileListView.openFile` 不变，仍打开面板。

## 4. 实现方案

### 4.1 useArtifacts.ts

`openPreviewTab` 增加 `openPanel` 参数：

```ts
function openPreviewTab(sid: string, artifactId: string, openPanel = true): void {
  const tabs = previewTabsBySession.value[sid] ?? [];
  const tabId = previewTabId(artifactId);
  if (!tabs.some((t) => t.id === tabId)) {
    previewTabsBySession.value = {
      ...previewTabsBySession.value,
      [sid]: [...tabs, { id: tabId, artifactId, contentView: ArtifactContentView.Preview, openedAt: Date.now() }],
    };
  }
  activeTabIdBySession.value = { ...activeTabIdBySession.value, [sid]: tabId };
  if (openPanel) setPanelOpen(sid, true);
}
```

`addArtifact` 增加 `opts` 并透传：

```ts
function addArtifact(sid: string, artifact: Artifact, opts?: { openPanel?: boolean }): void {
  // ...（原有 add / update 逻辑不变）
  openPreviewTab(sid, artifact.id, opts?.openPanel ?? true);
}
```

### 4.2 useMessages.ts

`appendEventFor` artifact 分支：

```ts
artifacts.addArtifact(sid, artifact, { openPanel: false });
```

### 4.3 测试

`useArtifacts.test.ts` 新增用例：

```ts
it('addArtifact with openPanel:false does not open the panel', () => {
  artifacts.addArtifact('s1', { id: 'a1', kind: 'text', content: 'x', createdAt: 1 }, { openPanel: false });
  expect(artifacts.previewTabsBySession.value.s1).toHaveLength(1);
  expect(artifacts.activeTabIdBySession.value.s1).toBe(previewTabId('a1'));
  expect(artifacts.isPanelOpenBySession.value.s1).toBeUndefined();
});
```

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 面板已打开时收到新 artifact | `openPanel:false` 仍切换激活 tab 到新 artifact（live 预览），面板状态不变 |
| 面板收起时收到新 artifact | 面板保持收起；tab 已登记 + 激活（不可见）；用户手动开面板看到最新 artifact |
| 用户在文件列表点文件 | 走默认 `openPanel:true`，照常打开面板 |
| 老事件缺 messageId | 仍走 `addArtifact` 进 store（兼容），同样不弹面板 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/composables/useArtifacts.ts` | `openPreviewTab` 加 `openPanel` 参数；`addArtifact` 加 `opts` 透传 |
| `src/renderer/composables/useMessages.ts` | artifact 分支传 `{ openPanel: false }` |
| `src/renderer/composables/useArtifacts.test.ts` | 新增「openPanel:false 不弹面板」用例 |

## 7. 验收标准

- [ ] 场景 1：agent 产 artifact 后面板保持收起（CDP 实测）
- [ ] 场景 2：点聊天卡片打开面板并激活对应 tab
- [ ] 场景 3：文件列表点击照常打开预览
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过（新增用例：`addArtifact({openPanel:false})` 不改 `isPanelOpenBySession`）
- [ ] 手动 `npm start`：artifact 会话不再自动弹面板
