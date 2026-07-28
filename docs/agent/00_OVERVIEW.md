# OpenClaw Agent 框架详解

## 整体架构

OpenClaw 的 Agent 框架是一个**分层架构**，从底层 LLM 抽象到上层的 Agent 调度，形成完整的 Agent 运行时。

```
┌─────────────────────────────────────────────────────────────┐
│                      Agent 调度层                            │
│  (Agent.prompt() → runAgentLoop())                          │
├─────────────────────────────────────────────────────────────┤
│                   Context Engine 层                          │
│  (上下文组装、压缩、记忆管理)                                   │
├─────────────────────────────────────────────────────────────┤
│               Skills / MCP / Tools 层                        │
│  (技能调度、工具调用、MCP 集成)                                 │
├─────────────────────────────────────────────────────────────┤
│                      LLM Core 层                            │
│  (模型抽象、流式处理、提供商适配)                                │
└─────────────────────────────────────────────────────────────┘
```

---

## 核心组件概览

| 组件 | 位置 | 职责 |
|------|------|------|
| **Agent** | `packages/agent-core/src/` | Agent 调度核心 |
| **ContextEngine** | `src/context-engine/` | 上下文组装与管理 |
| **LLM Core** | `packages/llm-core/src/` | LLM 接口抽象 |
| **Memory** | `src/memory-host-sdk/` | 记忆系统 |
| **Skills** | `src/skills/` | 技能系统 |
| **MCP** | `src/node-host/mcp.ts` | MCP 客户端集成 |

---

## 组件交互流程

### 用户消息处理流程

```
用户输入
    ↓
Agent.prompt(message)
    ↓
runAgentLoop()
    ↓
ContextEngine.assemble()  ← 组装上下文
    ↓
convertToLlm()  ← 转换为 LLM 格式
    ↓
streamFn()  ← 调用 LLM
    ↓
流式事件 (text_delta, toolcall_delta...)
    ↓
工具执行 (Skills/MCP/内置工具)
    ↓
afterTurn()  ← 收尾处理
    ↓
agent_end 事件
```

---

## 一、LLM 封装

### 1.1 模型抽象

LLM Core 包 (`packages/llm-core/`) 定义了统一的模型接口。

**支持的 API 类型**：
- `openai-completions` - OpenAI 兼容的补全 API
- `anthropic-messages` - Anthropic 的 Messages API
- `openai-responses` - OpenAI Responses API
- `google-generative-ai` - Google Gemini
- `bedrock-converse-stream` - AWS Bedrock

**Model 对象包含**：
- `id` - 模型标识符
- `provider` - 提供商引用
- `baseUrl` - API 端点
- `contextWindow` - 上下文窗口大小
- `maxTokens` - 最大输出 token 数
- `cost` - 输入/输出/缓存读写价格
- `reasoning` - 是否支持推理
- `thinkingLevelMap` - 思考级别映射

### 1.2 消息类型

统一的消息格式：
```typescript
type Message = UserMessage | AssistantMessage | ToolResultMessage
```

- **UserMessage** - 用户输入
- **AssistantMessage** - AI 响应（含 tool_calls）
- **ToolResultMessage** - 工具执行结果

### 1.3 流式处理

使用**事件协议**处理流式响应：

| 事件类型 | 说明 |
|---------|------|
| `start` | 开始生成响应 |
| `text_delta` | 文本增量输出 |
| `thinking_delta` | 思考过程增量 |
| `toolcall_delta` | 工具调用参数增量 |
| `toolcall_end` | 工具调用完成 |
| `done` | 生成结束 |
| `error` | 发生错误 |

### 1.4 Claude 特性支持

针对 Claude 模型的特殊处理：
- `supportsClaudeAdaptiveThinking()` - 检查是否支持自适应思考
- `requiresClaudeMandatoryAdaptiveThinking()` - 是否强制要求思考
- `resolveClaudeModelIdentity()` - 规范化模型 ID

---

## 二、上下文管理

### 2.1 Context Engine 接口

上下文引擎 (`src/context-engine/`) 是**可插拔**的上下文管理契约。

**核心职责**：
- **Bootstrap** - 初始化会话上下文
- **Ingest** - 消化处理消息
- **Assemble** - 组装最终的 prompt 上下文
- **Compact** - 上下文压缩
- **Maintain** - 定期维护

### 2.2 Assemble（上下文组装）

`assemble()` 方法是关键入口，返回：

- `messages` - 排序后的消息列表，用于送入 LLM
- `estimatedTokens` - 估算的 token 数量
- `promptAuthority` - token 预算检查权限
- `systemPromptAddition` - 额外的系统提示注入
- `contextProjection` - 持久化后端线程的生命周期

### 2.3 上下文压缩（Compaction）

当上下文超过预算时：
- 触发 compaction 流程
- 保留关键信息（最后消息、重要转折点）
- 生成摘要替换历史消息
- 支持 DAG 结构的分支管理

### 2.4 子 Agent 支持

Context Engine 还支持子 Agent：
- `prepareSubagentSpawn()` - 准备子 Agent 诞生
- `onSubagentEnded()` - 子 Agent 结束回调

---

## 三、记忆系统

### 3.1 记忆架构

记忆系统采用**分层存储**：

| 层级 | 说明 |
|------|------|
| **短期记忆** | 当前会话消息 |
| **中期记忆** | 每日笔记、会话摘要 |
| **长期记忆** | 持久化的事实库 |

### 3.2 记忆配置

```yaml
memory:
  backend: "builtin"  # 或 "qmd"
  search:
    enabled: true
    rememberAcrossConversations: true  # 跨会话记忆
    sources: ["memory", "sessions"]    # 记忆来源
    provider: "..."                   # 搜索provider
    model: "..."                      # 嵌入模型
```

### 3.3 Dreaming（记忆巩固）

OpenClaw 实现了**模拟做梦的记忆巩固**机制，分为三个阶段：

1. **Light Dreaming**
   - 快速、低思考，成本低
   - 处理每日笔记和会话摘要

2. **Deep Dreaming**
   - 平衡速度和质量
   - 将短期记忆晋升为长期记忆

3. **REM Dreaming**
   - 慢速、高思考、高成本
   - 跨记忆的模式识别

**触发方式**：Cron 定时调度（默认凌晨 3 点）

### 3.4 记忆事件存储

- 使用插件状态存储记忆事件
- 10,000 条事件预算
- 支持游标分页
- 事件类型：`memory.recall.recorded`、`memory.promotion.applied`、`memory.dream.completed`

---

## 四、Skills 系统

### 4.1 Skill 定义

Skill 是**可复用的技能单元**，结构如下：

```yaml
# SKILL.md
---
name: skill-name
description: 技能描述
---
# 技能内容...
```

### 4.2 Skill 来源

Skills 从多个来源加载：

| 来源 | 说明 |
|------|------|
| **Workspace Skills** | 工作区 `skills/` 目录下的 `.md` 文件 |
| **Session Skills** | 会话级别的技能 |
| **Plugin Skills** | 插件提供的技能 |
| **Bundled Skills** | 内置技能 |

### 4.3 Skill 调用策略

```typescript
SkillInvocationPolicy {
  userInvocable: boolean      # 用户是否可调用
  disableModelInvocation: boolean  # 是否禁止模型调用
}
```

### 4.4 Skill 格式化

Skill 在送入 prompt 前会被格式化为 XML：

```xml
<available_skills>
  <skill>
    <name>...</name>
    <description>...</description>
    <location>...</location>
  </skill>
</available_skills>
```

### 4.5 Skill 调度

工具调用通过 `tool-dispatch.ts` 路由：
- 解析生效的工具策略
- 应用允许/拒绝列表
- 处理沙箱策略
- 支持群组和发送者策略

---

## 五、MCP 集成

### 5.1 MCP 客户端

使用 `@modelcontextprotocol/sdk` 实现：

**核心功能**：
- `connect(transport)` - 建立连接
- `listTools()` - 列出可用工具
- `callTool()` - 调用工具
- `close()` - 关闭连接

### 5.2 MCP 管理器

`NodeHostMcpManager` 管理所有 MCP 连接：

```typescript
NodeHostMcpManager {
  configuredServerCount  # 已配置服务器数量
  descriptors           # 工具描述符列表
  callMcpTool()        # 调用 MCP 工具
  close()              # 关闭所有连接
}
```

### 5.3 MCP 配置

```yaml
mcp:
  servers:
    my-server:
      type: "stdio"  # 或 "http"
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "./data"]
      env:
        API_KEY: "xxx"
```

### 5.4 工具过滤

MCP 工具可配置过滤规则：

```yaml
mcp:
  servers:
    my-server:
      toolFilter:
        include: ["read_*", "write_*"]   # 只暴露这些工具
        exclude: ["dangerous_*"]          # 排除这些工具
```

---

## 六、Agent 核心

### 6.1 Agent 类

主入口类 (`packages/agent-core/src/agent.ts`)：

```typescript
Agent {
  // 核心方法
  prompt(message)     # 发送用户消息
  continue()          # 继续上轮对话
  steer(message)      # 队列消息到下一轮
  followUp(message)   # 队列消息在 Agent 停止后
  abort(reason)       # 中止当前运行
  waitForIdle()       # 等待空闲
  reset()             # 重置状态

  // 订阅
  subscribe(listener)  # 订阅 Agent 事件
}
```

### 6.2 Agent 循环

`runAgentLoop()` 编排完整执行周期：

1. 接收消息（prompt/steer/followUp）
2. 调用 `convertToLlm()` 转换消息格式
3. 调用 `transformContext()` 转换上下文
4. 执行 `streamFn()` 调用 LLM
5. 处理流式事件（text_delta、toolcall_delta）
6. 执行工具调用
7. 调用 `afterTurn()` 钩子
8. 发送 `agent_end` 事件

### 6.3 工具执行模式

- **Sequential** - 顺序执行工具
- **Parallel** - 并行执行工具

### 6.4 队列模式

- **all** - 所有排队的消息一起处理
- **one-at-a-time** - 每次只处理一条消息

---

## 七、关键设计理念

### 7.1 可插拔架构

- **ContextEngine** 是接口，可以有多种实现
- **LLM Provider** 可替换
- **Memory Backend** 可切换（builtin / qmd）

### 7.2 分层解耦

- Agent 调度 ↔ 上下文管理 ↔ LLM 抽象
- 每层只关注自己的职责

### 7.3 预算感知

- Token 预算检查在 assemble 阶段
- 压缩在上下文超出预算时触发

### 7.4 事件驱动

- 使用事件协议处理流式输出
- Agent 生命周期通过事件暴露

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md) - 本文档
- [LLM 接口详解](./01_LLM_INTERFACE.md)
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md)
- [记忆系统详解](./03_MEMORY_SYSTEM.md)
- [Skills 系统详解](./04_SKILLS_SYSTEM.md)
- [MCP 集成详解](./05_MCP_INTEGRATION.md)
