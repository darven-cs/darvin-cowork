# 会话列表按创建时间排序设计文档

## 1. 概述

### 1.1 问题 / 背景

会话列表当前有两个与用户预期不符的行为：

1. **点击会话会触发重新排序**：`switchSession` 调 Go `handleSetActiveSession` → `Touch` 刷新 `updated_at`，而 Go `list_sessions` 按 `updated_at desc` 返回 → 每点一次会话，该会话就被顶到列表头，列表顺序随使用「漂移」。
2. **基础排序不是创建时间**：渲染层 `SessionList.vue` 的 `orderedSessions` 是「置顶 + 主进程返回顺序」（即 updated_at desc）。

用户期望：列表按创建时间稳定排序（最新在上），点击会话不应改变列表顺序。

### 1.2 目标

- 会话列表按 `createdAt` 倒序（最新在上）排列。
- 置顶（pin）会话仍然排在最前；置顶组内部同样按创建时间倒序。
- 点击 / 切换会话不再导致列表重排。

### 1.3 非目标

- 不改动 `relTime` 展示（仍显示 `updatedAt` 相对时间，即「最后活跃」；点击后显示「刚刚」符合直觉）。
- 不删除 Go 端 `Touch` 行为（点击仍刷新 updated_at 供展示，但不影响排序）。
- 不改 Go `list_sessions` 的 ORDER BY（渲染层持有最终展示顺序，避免扩大 Go 改动面）。
- 不动置顶功能本身（菜单入口 / 图标 / localStorage 状态保留）。

## 2. 用户场景

### 场景 1: 会话按创建时间排序
**Given** 会话 A 创建于 3 天前，B 创建于 1 分钟前
**When** 用户打开侧栏会话列表
**Then** B 显示在 A 上方（创建时间倒序）；无论期间点击过多少次 A，A 的位置都不变

### 场景 2: 置顶会话在最前
**Given** 会话 C 被置顶，D / E 未置顶，D 创建时间比 E 新
**When** 用户查看列表
**Then** C 始终在最上方；D / E 按创建时间倒序（D 在 E 上方）

## 3. 功能需求

### FR-1: wire 层暴露 createdAt
- `DarvinSession`（shared）增加 `createdAt: number`（unix ms）。
- Go `SessionWire` 增加 `CreatedAt int64`（json tag `createdAt`），`toSessionWire` 映射 `r.CreatedAt.UnixMilli()`。

### FR-2: 渲染层按创建时间倒序
- `SessionList.vue` `orderedSessions`：先整体按 `createdAt` 倒序，再稳定分区把置顶项放到最前。

## 4. 实现方案

### 4.1 shared 类型

`src/shared/darvin-api.ts`：

```ts
export interface DarvinSession {
  id: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  status?: DarvinSessionStatus;
  claudeSessionId?: string | null;
}
```

### 4.2 Go wire

`src/darvin-agent/internal/gateway/handlers.go`：

```go
type SessionWire struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
	Status          string  `json:"status"`
	ClaudeSessionID *string `json:"claudeSessionId"`
}
```

`toSessionWire` 增加 `CreatedAt: r.CreatedAt.UnixMilli()`。

### 4.3 渲染层排序

`src/renderer/components/sidebar/SessionList.vue`：

```ts
const orderedSessions = computed(() => {
  const sorted = [...props.sessions].sort((a, b) => b.createdAt - a.createdAt);
  const pinned = sorted.filter((s) => pinnedIds.value.has(s.id));
  const rest = sorted.filter((s) => !pinnedIds.value.has(s.id));
  return [...pinned, ...rest];
});
```

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 两个会话 createdAt 相同 | 依赖 `Array.prototype.sort` 稳定排序，保持原始（主进程返回）相对顺序 |
| 老数据 / 测试数据缺 createdAt | `createdAt` 设为必填；Go 始终下发；TS 侧所有 `DarvinSession` 字面量补齐该字段 |
| 置顶会话内部的顺序 | 先整体排序再分区，置顶组内仍按 createdAt 倒序 |
| 点击会话后 updatedAt 被 Touch 刷新 | 不影响排序（排序只看 createdAt）；仅 relTime 标签更新为「刚刚」 |
| 新建会话 | `createSession` 后前置插入 `sessions.value`；`orderedSessions` 仍按 createdAt 排序兜底，新会话天然在顶 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/shared/darvin-api.ts` | `DarvinSession` 增加 `createdAt` |
| `src/darvin-agent/internal/gateway/handlers.go` | `SessionWire` + `toSessionWire` 增加 `CreatedAt` |
| `src/renderer/components/sidebar/SessionList.vue` | `orderedSessions` 改为 createdAt 倒序 + 置顶前置 |
| `src/darvin-agent/internal/gateway/handlers_test.go` | （如有 wire 形状断言）补 `CreatedAt` 字段 |

## 7. 验收标准

- [ ] 场景 1：侧栏会话按创建时间倒序；多次点击任意会话后列表顺序不变
- [ ] 场景 2：置顶会话恒在最前，置顶组内按创建时间倒序
- [ ] 新建会话出现在列表顶部
- [ ] `go build` / `go vet` 通过（Go wire 变更）
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过
- [ ] 手动 `npm start`：DevTools 无 console error；列表顺序稳定
