# 记忆系统详解

## 概述

OpenClaw 的记忆系统采用**分层架构**，支持短期、中期、长期记忆的存储和检索。

**核心文件**:
- `src/memory-host-sdk/engine-foundation.ts` - 记忆引擎基础
- `src/memory-host-sdk/dreaming.ts` - 记忆巩固机制
- `src/memory-host-sdk/event-store.ts` - 记忆事件存储

---

## 记忆架构

### 分层存储

```
┌─────────────────────────────────────────────┐
│              长期记忆 (Long-term)            │
│         持久化的事实库、模式识别结果           │
├─────────────────────────────────────────────┤
│              中期记忆 (Medium-term)          │
│         每日笔记、会话摘要                     │
├─────────────────────────────────────────────┤
│              短期记忆 (Short-term)            │
│            当前会话消息                        │
└─────────────────────────────────────────────┘
```

### 记忆流转

1. **摄入**: 当前会话消息 → 短期记忆
2. **巩固**: 定时触发 → 短期 → 中期/长期
3. **检索**: 组装上下文时 → 相关记忆注入 prompt

---

## 记忆配置

### MemoryConfig

```typescript
interface MemoryConfig {
  // 记忆后端
  backend?: "builtin" | "qmd";

  // 引用模式
  citations?: "auto" | "on" | "off";

  // 搜索配置
  search?: MemorySearchConfig;

  // QMD 配置（可选）
  qmd?: MemoryQmdConfig;
}
```

### MemorySearchConfig

```typescript
interface MemorySearchConfig {
  // 是否启用
  enabled?: boolean;

  // 是否跨会话记忆
  rememberAcrossConversations?: boolean;

  // 记忆来源
  sources?: Array<
    | "memory"      // 记忆库
    | "sessions"    // 会话历史
    | "daily"       // 每日笔记
    | "logs"        // 日志
  >;

  // 额外搜索路径
  extraPaths?: string[];

  // 搜索 provider
  provider?: string;
  model?: string;

  // 存储配置
  store?: {
    // 全文搜索
    fts?: {
      tokenizer?: "unicode61" | "trigram";
    };

    // 向量搜索
    vector?: {
      enabled?: boolean;
      extensionPath?: string;
    };

    // 缓存
    cache?: {
      enabled?: boolean;
      maxEntries?: number;
    };
  };

  // 查询配置
  query?: {
    maxResults?: number;   // 最大返回数
    minScore?: number;      // 最小相关度
  };
}
```

---

## Dreaming（记忆巩固）

### 概述

Dreaming 是 OpenClaw 模拟"做梦"的记忆巩固机制，在后台定时运行，将短期记忆晋升为长期记忆。

### 三阶段模型

#### 1. Light Dreaming

| 属性 | 值 |
|------|-----|
| 速度 | 快 |
| 思考深度 | 低 |
| 成本 | 便宜 |
| 适用 | 每日笔记整理、会话摘要 |

```typescript
interface LightDreamingConfig {
  enabled: boolean;
  frequency: string;  // cron 表达式
  lookbackDays: number;
  sources: ("daily" | "sessions")[];
}
```

#### 2. Deep Dreaming

| 属性 | 值 |
|------|-----|
| 速度 | 平衡 |
| 思考深度 | 中等 |
| 成本 | 适中 |
| 适用 | 短期 → 中期记忆晋升 |

```typescript
interface DeepDreamingConfig {
  enabled: boolean;
  frequency: string;
  lookbackDays: number;
  sources: ("daily" | "sessions" | "memory")[];
  minConfidence: number;  // 晋升最小置信度
}
```

#### 3. REM Dreaming

| 属性 | 值 |
|------|-----|
| 速度 | 慢 |
| 思考深度 | 高 |
| 成本 | 昂贵 |
| 适用 | 跨记忆模式识别 |

```typescript
interface RemDreamingConfig {
  enabled: boolean;
  frequency: string;
  lookbackDays: number;
  sources: ("memory" | "logs")[];
  patternTypes: ("routine" | "preference" | "relationship")[];
}
```

### Dreaming 事件

```typescript
interface DreamCompletedEvent {
  type: "memory.dream.completed";
  phase: "light" | "deep" | "rem";
  processedCount: number;
  promotedCount: number;
  durationMs: number;
}
```

---

## 记忆事件存储

### Event Store

记忆事件存储在插件状态中：

```typescript
interface MemoryEventStore {
  // 命名空间预算
  namespaceBudget: 10000;

  // 记录事件
  record(event: MemoryEvent): Promise<void>;

  // 查询事件
  query(params: {
    cursor?: string;
    limit?: number;
    type?: string[];
  }): Promise<{
    events: MemoryEvent[];
    nextCursor?: string;
  }>;

  // 删除旧事件
  prune(olderThan: number): Promise<number>;
}
```

### 事件类型

```typescript
type MemoryEvent =
  | { type: "memory.recall.recorded"; memory: Fact }
  | { type: "memory.promotion.applied"; from: MemoryTier; to: MemoryTier; memory: Fact }
  | { type: "memory.dream.completed"; phase: string; stats: DreamStats };
```

---

## 记忆检索

### 检索流程

1. 用户消息进入 `assemble()`
2. ContextEngine 调用记忆检索
3. 检索相关记忆
4. 注入到 `systemPromptAddition`

### 检索参数

```typescript
interface MemoryRecallParams {
  // 查询内容
  query: string;

  // 最大返回数
  maxResults?: number;

  // 最小相关度
  minScore?: number;

  // 记忆层级
  tiers?: MemoryTier[];

  // 排除的会话
  excludeSessionIds?: string[];
}
```

### 检索结果

```typescript
interface MemoryRecallResult {
  memories: RetrievedMemory[];
  stats: {
    totalChecked: number;
    latencyMs: number;
  };
}

interface RetrievedMemory {
  fact: Fact;
  score: number;
  source: "memory" | "session" | "daily";
  sessionId?: string;
  citation?: string;
}
```

---

## 每日笔记

### 用途

每日笔记是短期记忆的一种形式，记录：
- 日常任务和想法
- 会话摘要
- 临时备忘

### 笔记格式

```markdown
# Daily Note: 2024-01-15

## Sessions
- [session:xxx] 讨论了项目架构

## Facts
- 用户偏好使用 TypeScript

## Tasks
- [ ] 完成 API 设计
```

---

## 事实（Facts）

### Fact 结构

```typescript
interface Fact {
  // 唯一标识
  id: string;

  // 内容
  content: string;

  // 元数据
  metadata: {
    // 来源
    source: "user" | "session" | "dream" | "manual";
    sessionId?: string;

    // 置信度
    confidence: number;  // 0-1

    // 层级
    tier: MemoryTier;

    // 标签
    tags: string[];

    // 创建时间
    createdAt: number;

    // 更新时间
    updatedAt: number;
  };
}

type MemoryTier = "short" | "medium" | "long";
```

---

## 与 Agent 的集成

### 集成点

```
用户消息
    ↓
ContextEngine.assemble()
    ↓
MemoryRecall() ← 检索相关记忆
    ↓
systemPromptAddition ← 注入到 prompt
    ↓
LLM 调用
```

### 引用生成

当记忆被使用时，可以生成引用：

```typescript
interface MemoryCitation {
  memoryId: string;
  fact: string;
  citation: string;  // "根据记忆 [1]"
}
```

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md)
- [LLM 接口详解](./01_LLM_INTERFACE.md)
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md)
- [记忆系统详解](./03_MEMORY_SYSTEM.md) - 本文档
- [Skills 系统详解](./04_SKILLS_SYSTEM.md)
- [MCP 集成详解](./05_MCP_INTEGRATION.md)
