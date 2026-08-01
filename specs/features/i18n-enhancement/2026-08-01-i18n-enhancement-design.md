# i18n 增强

> 编号 **08**。在现有平铺字典 + zh/en 双语基础上，加插值 + 响应式切换 + 补齐新组件 key。

## 1. 背景

`src/renderer/services/i18n.ts` 现状：
- `dictZh` + `dictEn` 平铺，约 140 key
- `setLang()` 改 `currentLang` ref，**不响应式**（已渲染的不会重渲）
- 无插值
- 散落 hardcoded 字符串（shortcut 标签、expert 分类等）
- 缺本轮 7 个新 spec 涉及的所有 key

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `t(key, params?)` 支持 `{name}` 插值 | 简单 `replace('{key}', value)` |
| G2 | `setLang()` 改 ref 后已渲染的组件自动重渲 | `currentLang` 改为 `ref` + `triggerRef` |
| G3 | 补齐 01-07 spec 涉及的所有 i18n key | zh/en 各 60+ 新 key |
| G4 | 净化 hardcoded 字符串（shortcut 标签 / expert 分类 / 错误兜底） | 全文搜 `'.*[\u4e00-\u9fff].*'` 找漏网 |
| G5 | 引入 `Intl.NumberFormat` / `Intl.DateTimeFormat` 工具方法 | `formatNumber` / `formatDate` / `formatRelativeTime` |
| G6 | `en` 字典 key 集合与 `zh` 完全一致（`assertSameKeys` 强约束） | 已有 dev-mode 校验 |
| G7 | 缺失 key 在 dev 环境 `console.warn`，不静默回退 | 防止翻译漂移 |

## 3. 非目标

- 不引入 vue-i18n / i18next（保持「YAGNI」原则；AGENTS.md 明文要求）
- 不做 3+ 语言
- 不做按 namespace 懒加载
- 不做复数形式（`{count, plural, ...}`）

## 4. 设计要点

### 4.1 插值

```ts
export function t(key: string, params?: Record<string, string | number>): string {
  const value = dict[currentLang.value][key] ?? dict.zh[key] ?? key;
  if (!params) return value;
  return Object.entries(params).reduce(
    (acc, [k, v]) => acc.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v)),
    value,
  );
}
```

### 4.2 响应式

```ts
const currentLang = ref<Lang>('zh');
// 改 ref 时触发 ref 依赖重渲
export function setLang(lang: Lang) {
  currentLang.value = lang;
  localStorage.setItem('darvin.lang', lang);
}
```

注意：AGENTS.md § i18n 提到当前「不响应式」，要升级为响应式时，确保所有 `t()` 调用都在 `<template>` 渲染函数中，**不要**在 `<script setup>` 顶层缓存（缓存会断响应式链）。

### 4.3 格式化工具

```ts
export function formatNumber(n: number, opts?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(currentLang.value, opts).format(n);
}

export function formatDate(ts: number, opts?: Intl.DateTimeFormatOptions): string {
  return new Intl.DateTimeFormat(currentLang.value, opts).format(new Date(ts));
}

export function formatRelativeTime(ts: number): string {
  const diff = Date.now() - ts;
  if (diff < 60_000) return t('time.justNow');
  if (diff < 3_600_000) return t('time.minutesAgo', { n: Math.floor(diff / 60_000) });
  // ...
}
```

### 4.4 缺 key 警告

```ts
const SEEN_KEYS = new Set<string>();
export function t(key: string, params?): string {
  if (process.env.NODE_ENV !== 'production' && !SEEN_KEYS.has(key)) {
    SEEN_KEYS.add(key);
    if (!(key in dict.zh) && !(key in dict.en)) {
      console.warn(`[i18n] missing key: ${key}`);
    }
  }
  // ...
}
```

## 5. 用户场景

### 场景 1：插值

**Given** zh 字典 `'time.minutesAgo': '{n} 分钟前'`

**When** 调 `t('time.minutesAgo', { n: 5 })`

**Then** 返回「5 分钟前」

### 场景 2：响应式切换

**Given** 用户在 settings 切到 en

**When** `setLang('en')` 触发

**Then** 已渲染的所有 `{{ t('xxx') }}` 立即更新为英文

### 场景 3：缺 key 告警

**Given** 组件调 `t('nonexistent.key')`

**When** 渲染

**Then** dev 环境 `console.warn`；生产静默回退到 zh 字典（如果存在）或原 key

## 6. 验收

- [ ] `t(key, params)` 插值单元测试覆盖 3 种情况
- [ ] `setLang('en')` 触发已渲染组件 re-render（手动验证）
- [ ] 01-07 spec 涉及的 60+ 新 key 在 zh + en 双语中齐全
- [ ] AGENTS.md 散落 hardcoded 字符串全部走 `t()`
- [ ] 缺 key dev warn 生效
- [ ] `assertSameKeys(dictZh, dictEn)` 通过

## 7. 依赖

- **前置**：无
- **可并行**：01-07 / 09
- **后置**：所有新组件的 i18n 落地

## 8. 参考

### darvin-cowork
- `src/renderer/services/i18n.ts` — 现有平铺字典
- AGENTS.md § 「国际化」全节约束（特别是「不引入 vue-i18n」）

### LobsterAI（借鉴）
- `src/renderer/services/i18n.ts`（314KB，仅看头部结构 + `replace('{placeholder}', value)` 模式）

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.9「i18n」
