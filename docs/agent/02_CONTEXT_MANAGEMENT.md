# 上下文管理详解

## 概述

上下文管理是 Agent 框架的核心组件，负责管理对话历史、组装 prompt、控制 token 预算。

**核心文件**: `src/context-engine/types.ts`

---

## ContextEngine 接口

ContextEngine 是一个**可插拔**的接口，定义了上下文管理的完整生命周期：

```typescript
interface ContextEngine {
  readonly info: ContextEngineInfo;

  // 生命周期
  bootstrap?(params: {
    sessionId: string;
    sessionKey?: string;
    cwd?: string;
    config?: OpenClawConfig;
  }): Promise<BootstrapResult>;

  maintain?(params: {
    sessionId: string;
    signal?: AbortSignal;
  }): Promise<ContextEngineMaintenanceResult>;

  // 消息处理
  ingest(params: {
    sessionId: string;
    message: AgentMessage;
    agentManifest?: AgentManifest;
  }): Promise<IngestResult>;

  ingestBatch?(params: {
    sessionId: string;
    messages: AgentMessage[];
    agentManifest?: AgentManifest;
  }): Promise<IngestBatchResult>;

  afterTurn?(params: {
    sessionId: string;
    messages: AgentMessage[];
    turnId: string;
    agentManifest?: AgentManifest;
  }): Promise<void>;

  // 上下文组装
  assemble(params: {
    sessionId: string;
    messages: AgentMessage[];
    tokenBudget?: number;
    availableTools?: Set<string>;
    agentManifest?: AgentManifest;
  }): Promise<AssembleResult>;

  // 压缩
  compact(params: {
    sessionId: string;
    tokenBudget?: number;
    force?: boolean;
    reason?: string;
  }): Promise<CompactResult>;

  // 子 Agent 支持
  prepareSubagentSpawn?(params: {
    parentSessionKey: string;
    childAgentId: string;
    agentManifest?: AgentManifest;
  }): Promise<SubagentSpawnPreparation | undefined>;

  onSubagentEnded?(params: {
    childSessionKey: string;
    reason: SubagentEndReason;
  }): Promise<void>;

  dispose?(): Promise<void>;
}
```

---

## AssembleResult（组装结果）

上下文组装的核心产物：

```typescript
interface AssembleResult {
  // 消息列表（用于 LLM 调用）
  messages: Message[];

  // Token 估算
  estimatedTokens: {
    prompt: number;
    available: number;
    system: number;
  };

  // 权限控制
  promptAuthority: "agent" | "host" | "defer";

  // 额外注入
  systemPromptAddition?: SystemPromptSection[];

  // 上下文投影（持久化支持）
  contextProjection?: ContextProjection;

  // 统计信息
  stats?: {
    compilationTimeMs: number;
    messageCount: number;
    compactionTriggered?: boolean;
  };
}
```

---

## Token 预算管理

### 预算检查

```typescript
interface AssembleResult {
  estimatedTokens: {
    prompt: number;    // 预计使用的 prompt tokens
    available: number; // 可用的 tokens
    system: number;    // 系统部分 tokens
  };
}
```

### 预算耗尽处理

当 `prompt > available` 时：
1. 触发 `compact()` 操作
2. 压缩历史消息
3. 重新组装

---

## 消息消化（Ingest）

### IngestResult

```typescript
interface IngestResult {
  // 是否成功
  success: boolean;

  // 处理的 token 数
  tokensProcessed: number;

  // 是否有新记忆被记录
  memoryRecorded?: {
    facts: Fact[];
    dailyNoteUpdated: boolean;
  };

  // 警告信息
  warnings?: string[];
}
```

### 消化过程

1. 解析消息内容
2. 提取关键信息
3. 更新每日笔记
4. 可能触发记忆记录

---

## 上下文压缩（Compaction）

### CompactResult

```typescript
interface CompactResult {
  // 压缩是否成功
  success: boolean;

  // 压缩前后的 token 数
  tokensBefore: number;
  tokensAfter: number;

  // 保留了哪些消息
  retainedMessages: Message[];

  // 生成的摘要
  summary?: string;

  // 创建的检查点
  checkpoint?: CompactionCheckpoint;
}
```

### 压缩策略

1. **保留原则**：
   - 最后 N 条消息（保留最近上下文）
   - 关键转折点消息
   - 包含工具结果的消息

2. **摘要生成**：
   - 调用 LLM 生成摘要
   - 保留关键决策和信息点

3. **DAG 支持**：
   - 支持分支结构的压缩
   - 保留分支点信息

---

## 子 Agent 支持

### SubagentSpawnPreparation

```typescript
interface SubagentSpawnPreparation {
  // 准备传递给子 Agent 的上下文
  context: {
    messages: Message[];
    systemPrompt: SystemPromptSection[];
    tools: Tool[];
  };

  // 父上下文的引用
  parentContextRef: string;

  // 生命周期管理
  ttlMinutes?: number;
}
```

### SubagentEndReason

```typescript
type SubagentEndReason =
  | "completed"     // 正常完成
  | "aborted"        // 被中止
  | "error"          // 发生错误
  | "orphaned"       // 父会话结束但子会话仍在运行
  | "timeout";       // 超时
```

---

## ContextProjection（上下文投影）

用于持久化后端线程的生命周期管理：

```typescript
interface ContextProjection {
  // 投影 ID
  id: string;

  // 类型
  type: "agent" | "tool" | "memory";

  // 创建时间
  createdAt: number;

  // 过期时间（可选）
  expiresAt?: number;

  // 序列化状态
  serializedState?: Record<string, unknown>;
}
```

---

## 系统提示注入

### SystemPromptSection

```typescript
interface SystemPromptSection {
  // 优先级（数字越小越靠前）
  priority: number;

  // 内容
  content: string;

  // 来源标识
  source: string;

  // 是否可被覆盖
  overridable?: boolean;
}
```

### 注入来源

系统提示可能来自：
- Agent 配置的默认提示
- 当前加载的 Skills 描述
- 记忆系统检索的相关事实
- MCP 服务器描述
- 用户自定义规则

---

## 消息类型

### AgentMessage

```typescript
interface AgentMessage {
  // 唯一标识
  id: string;

  // 角色
  role: "user" | "assistant" | "system" | "tool";

  // 内容
  content: string | ContentBlock[];

  // 时间戳
  timestamp: number;

  // 元数据
  metadata?: {
    // 工具调用信息
    toolCall?: {
      name: string;
      input: Record<string, unknown>;
    };

    // 工具执行结果
    toolResult?: {
      toolCallId: string;
      result: string;
      isError: boolean;
    };

    // Token 统计
    tokenCount?: {
      input: number;
      output: number;
    };

    // 附加数据
    [key: string]: unknown;
  };
}
```

---

## 消息队列模式

### QueueMode

```typescript
type QueueMode =
  | "steer"      // 主动引导模式 - Agent 主动控制
  | "followup"   // 跟进模式 - 等待用户输入后继续
  | "collect"    // 收集模式 - 收集多条消息后统一处理
  | "interrupt"; // 中断模式 - 可中断当前执行
```

### 队列处理

1. **Steer 模式**：
   - Agent 可以主动请求用户输入
   - 用户消息直接加入队列

2. **Followup 模式**：
   - 用户消息加入 followup 队列
   - Agent 停止后自动处理

3. **Collect 模式**：
   - 多条消息累积后一起处理
   - 适用于批量操作

4. **Interrupt 模式**：
   - 可中断正在执行的 Agent
   - 立即处理中断消息

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md)
- [LLM 接口详解](./01_LLM_INTERFACE.md)
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md) - 本文档
- [记忆系统详解](./03_MEMORY_SYSTEM.md)
- [Skills 系统详解](./04_SKILLS_SYSTEM.md)
- [MCP 集成详解](./05_MCP_INTEGRATION.md)
