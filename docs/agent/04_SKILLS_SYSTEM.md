# Skills 系统详解

## 概述

Skills 是 OpenClaw 中可复用的技能单元，允许 Agent 调用预定义的工具和工作流程。

**核心文件**:
- `src/skills/types.ts` - 类型定义
- `src/skills/loading/skill-contract.ts` - 技能合约
- `src/skills/runtime/tool-dispatch.ts` - 技能调度

---

## Skill 结构

### Skill 接口

```typescript
interface Skill {
  // 基本信息
  name: string;           // "code-review"
  description: string;    // "执行代码审查"

  // 位置信息
  locationNote?: string;  // "workspace:skills/code-review"
  filePath: string;        // "/path/to/SKILL.md"
  baseDir: string;         // "/path/to"

  // 运行时内容（非文件系统定位器）
  readContent?: string;

  // 版本标记
  promptVersion?: string; // 内容版本，用于缓存

  // 来源信息
  sourceInfo: SourceInfo;

  // 调用策略
  disableModelInvocation: boolean;
  source: string;          // 来源标识
}

interface SourceInfo {
  type: "workspace" | "plugin" | "bundled" | "session";
  id?: string;
  path?: string;
}
```

---

## Skill 来源

### 加载来源

| 来源 | 说明 | 位置 |
|------|------|------|
| **Workspace Skills** | 工作区 `skills/` 目录下的 `.md` 文件 | `skills/*.md` |
| **Session Skills** | 会话级别的技能 | 临时创建 |
| **Plugin Skills** | 插件提供的技能 | 插件目录 |
| **Bundled Skills** | 内置技能 | 预装技能 |

### Workspace Skills

工作区技能存储在工作区的 `skills/` 目录中：

```
project/
├── skills/
│   ├── code-review.md
│   ├── api-design.md
│   └── testing.md
└── src/
```

### 技能文件格式

```markdown
---
name: code-review
description: 执行代码审查，检查潜在问题和最佳实践
invocation:
  userInvocable: true
  disableModelInvocation: false
---

# Code Review Skill

## 工具

此技能可以使用以下工具：

- `grep` - 搜索代码模式
- `read_file` - 读取文件内容

## 工作流程

1. 分析代码结构
2. 检查常见问题
3. 生成审查报告
```

---

## Skill Frontmatter

### 解析结构

```typescript
interface ParsedSkillFrontmatter {
  name: string;
  description: string;
  invocation?: SkillInvocationPolicy;
  exposure?: SkillExposure;
  metadata?: Record<string, unknown>;
}

interface SkillInvocationPolicy {
  userInvocable: boolean;      // 用户是否可调用
  disableModelInvocation: boolean;  // 是否禁止模型调用
}

interface SkillExposure {
  mode: "always" | "auto" | "manual";
  trigger?: string;
}
```

---

## SkillEntry

SkillEntry 是加载后的完整技能条目：

```typescript
interface SkillEntry {
  // 技能定义
  skill: Skill;

  // 解析后的 frontmatter
  frontmatter: ParsedSkillFrontmatter;

  // 元数据
  metadata?: OpenClawSkillMetadata;

  // 调用策略
  invocation?: SkillInvocationPolicy;

  // 暴露模式
  exposure?: SkillExposure;

  // 同步源目录（用于工作区技能）
  syncSourceDir?: string;
  syncDirName?: string;

  // 是否禁用命令调度
  disableCommandDispatch?: boolean;
}
```

---

## Skill 格式化

### Prompt 格式化

技能在送入 prompt 前会被格式化为 XML：

```typescript
function formatSkillsForPrompt(skills: Skill[]): string {
  const skillXml = skills.map(skill => `
    <skill>
      <name>${escapeXml(skill.name)}</name>
      <description>${escapeXml(skill.description)}</description>
      <location>${escapeXml(skill.locationNote || skill.filePath)}</location>
      ${skill.promptVersion ? `<version>${skill.promptVersion}</version>` : ""}
    </skill>
  `).join("\n");

  return `
<available_skills>
${skillXml}
</available_skills>
`.trim();
}
```

### 输出示例

```xml
<available_skills>
  <skill>
    <name>code-review</name>
    <description>执行代码审查，检查潜在问题和最佳实践</description>
    <location>workspace:skills/code-review</location>
  </skill>
  <skill>
    <name>api-design</name>
    <description>设计和文档化 API</description>
    <location>workspace:skills/api-design</location>
  </skill>
</available_skills>
```

---

## Skill 调度

### ToolDispatch 流程

```typescript
// src/skills/runtime/tool-dispatch.ts

interface ToolDispatchSpec {
  // 工具名称
  name: string;

  // 参数
  arguments: Record<string, unknown>;

  // 调用来源
  source: "user" | "agent" | "skill";

  // 关联的 skill（如果是 skill 调用）
  skillName?: string;
}

function dispatchTool(spec: ToolDispatchSpec): Promise<ToolResult> {
  // 1. 解析生效的工具策略
  const policy = resolveEffectivePolicy(spec);

  // 2. 应用允许/拒绝列表
  if (!policyCheck(policy, spec)) {
    throw new PolicyDeniedError(spec.name);
  }

  // 3. 处理沙箱策略
  if (policy.sandbox) {
    return dispatchSandboxed(spec, policy.sandbox);
  }

  // 4. 执行调度
  return executeTool(spec);
}
```

### 策略解析

```typescript
interface ToolPolicy {
  // 允许列表
  allow?: string[];

  // 拒绝列表
  deny?: string[];

  // 沙箱配置
  sandbox?: SandboxConfig;

  // 群组策略
  groupPolicy?: Record<string, GroupPolicy>;

  // 发送者策略
  senderPolicy?: Record<string, SenderPolicy>;
}

function resolveEffectivePolicy(spec: ToolDispatchSpec): ToolPolicy {
  // 合并多个层级的策略
  // 优先级: skill > agent > global
}
```

---

## Skill 命令

### SkillCommandDispatchSpec

```typescript
interface SkillCommandDispatchSpec {
  // 命令名称
  command: string;

  // 参数
  args?: string[];

  // 来源
  source: "user" | "agent";

  // 关联的 skill
  skillName?: string;
}
```

### 命令路由

```typescript
function dispatchSkillCommand(spec: SkillCommandDispatchSpec): Promise<CommandResult> {
  // 1. 查找 skill
  const skill = findSkillByCommand(spec.command);
  if (!skill) {
    throw new SkillNotFoundError(spec.command);
  }

  // 2. 检查调用权限
  if (!canInvoke(skill, spec.source)) {
    throw new SkillInvocationDeniedError(skill.name);
  }

  // 3. 执行命令
  return executeSkillCommand(skill, spec.args);
}
```

---

## Skill 生命周期

### 加载

```
1. 发现技能
   - 扫描工作区 skills/ 目录
   - 扫描插件技能
   - 加载内置技能

2. 解析技能
   - 解析 frontmatter
   - 验证格式
   - 提取元数据

3. 注册技能
   - 添加到技能注册表
   - 应用调用策略
```

### 调用

```
1. 用户/Agent 请求
2. 查找技能
3. 检查策略
4. 执行技能
5. 返回结果
```

### 更新

```
1. 文件变化检测
2. 重新解析
3. 更新注册表
4. 通知 Agent
```

---

## Skill 缓存

### 版本标记

```typescript
interface Skill {
  // 内容版本（基于内容的 hash）
  promptVersion?: string;
}

// 版本检测
function detectSkillUpdate(skill: Skill): boolean {
  const currentVersion = computeContentHash(skill.readContent || fs.readFileSync(skill.filePath));
  return skill.promptVersion !== currentVersion;
}
```

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md)
- [LLM 接口详解](./01_LLM_INTERFACE.md)
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md)
- [记忆系统详解](./03_MEMORY_SYSTEM.md)
- [Skills 系统详解](./04_SKILLS_SYSTEM.md) - 本文档
- [MCP 集成详解](./05_MCP_INTEGRATION.md)
