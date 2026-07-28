# MCP 集成详解

## 概述

MCP (Model Context Protocol) 是 OpenClaw 连接外部工具和数据源的协议标准。

**核心文件**:
- `src/node-host/mcp.ts` - MCP 管理器
- `src/mcp/` - MCP 相关模块

---

## MCP 架构

```
OpenClaw (MCP Client) ←→ MCP Server (外部工具)
         ↓
    Agent Runtime
```

---

## MCP 客户端

### NodeHostMcpClient

使用 `@modelcontextprotocol/sdk` 实现：

```typescript
interface NodeHostMcpClient {
  // 连接关闭回调
  onclose?: () => void;

  // 建立连接
  connect(transport: Transport): Promise<void>;

  // 列出工具
  listTools(params?: {
    cursor?: string;
  }, options?: {
    timeout?: number;
  }): Promise<{
    tools: Tool[];
    nextCursor?: string;
  }>;

  // 调用工具
  callTool(params: {
    name: string;
    arguments?: Record<string, unknown>;
  }, options?: {
    timeout?: number;
  }): Promise<CallToolResult>;

  // 关闭连接
  close(): Promise<void>;
}
```

---

## MCP 管理器

### NodeHostMcpManager

```typescript
interface NodeHostMcpManager {
  // 已配置的服务器数量
  configuredServerCount: number;

  // 工具描述符
  descriptors: NodePluginToolDescriptor[];

  // 调用 MCP 工具
  callMcpTool(params: {
    server: string;           // 服务器名称
    tool: string;             // 工具名称
    arguments?: Record<string, unknown>;
    timeoutMs?: number;
  }): Promise<CallToolResult>;

  // 关闭所有连接
  close(): Promise<void>;
}
```

---

## MCP 配置

### McpServerConfig

```typescript
interface McpServerConfig {
  // 服务器 ID
  id?: string;

  // 连接类型
  type: "stdio" | "http";

  // STDIO 连接配置
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  cwd?: string;

  // HTTP 连接配置
  url?: string;
  headers?: Record<string, string>;

  // OAuth 配置
  auth?: {
    type: "oauth" | "api_key";
    clientId?: string;
    clientSecret?: string;
    authEndpoint?: string;
    tokenEndpoint?: string;
  };

  // 工具过滤
  toolFilter?: {
    include?: string[];  // 只暴露匹配的工具
    exclude?: string[];  // 排除匹配的工具
  };

  // 超时配置
  timeout?: {
    connect?: number;   // 连接超时 (ms)
    call?: number;      // 调用超时 (ms)
  };

  // 重试配置
  retry?: {
    maxAttempts?: number;
    initialDelay?: number;
    maxDelay?: number;
  };
}
```

### 配置示例

```yaml
mcp:
  servers:
    # 文件系统服务器 (STDIO)
    filesystem:
      type: "stdio"
      command: "npx"
      args:
        - "-y"
        - "@modelcontextprotocol/server-filesystem"
        - "/path/to/workspace"
      env:
        DEBUG: "false"

    # HTTP 服务器
    web-search:
      type: "http"
      url: "http://localhost:8080/mcp"
      headers:
        Authorization: "Bearer ${API_KEY}"

    # 带 OAuth 的服务器
    github:
      type: "http"
      url: "https://api.github.com/mcp"
      auth:
        type: "oauth"
        clientId: "${GITHUB_CLIENT_ID}"
        clientSecret: "${GITHUB_CLIENT_SECRET}"
        authEndpoint: "https://github.com/login/oauth/authorize"
        tokenEndpoint: "https://github.com/login/oauth/access_token"
```

---

## 工具定义

### MCP Tool

```typescript
interface Tool {
  // 工具名称
  name: string;

  // 工具描述
  description: string;

  // 输入参数 schema
  inputSchema: {
    type: "object";
    properties?: Record<string, {
      type: string;
      description?: string;
      default?: unknown;
      enum?: unknown[];
    }>;
    required?: string[];
  };
}
```

### 工具过滤

```typescript
function shouldExposeTool(config: McpServerConfig, toolName: string): boolean {
  // 如果有 include，只暴露匹配的工具
  if (config.toolFilter?.include) {
    return config.toolFilter.include.some(pattern =>
      matchPattern(toolName, pattern)
    );
  }

  // 如果有 exclude，排除匹配的工具
  if (config.toolFilter?.exclude) {
    return !config.toolFilter.exclude.some(pattern =>
      matchPattern(toolName, pattern)
    );
  }

  // 默认暴露所有工具
  return true;
}

function matchPattern(name: string, pattern: string): boolean {
  // 支持通配符: "read_*", "*_file", etc.
  const regex = new RegExp(
    "^" + pattern.replace(/\*/g, ".*").replace(/\?/g, ".") + "$"
  );
  return regex.test(name);
}
```

---

## 工具调用流程

### 调用流程

```
1. Agent 生成 tool_call
         ↓
2. ToolDispatcher 路由到 MCP
         ↓
3. NodeHostMcpManager.callMcpTool()
         ↓
4. MCP Client 发送请求到服务器
         ↓
5. 服务器执行工具
         ↓
6. 返回结果
         ↓
7. 结果转换为 ToolResult
         ↓
8. 返回给 Agent
```

### 调用参数

```typescript
interface CallMcpToolParams {
  server: string;           // MCP 服务器名称
  tool: string;            // 工具名称
  arguments?: Record<string, unknown>;  // 工具参数
  timeoutMs?: number;      // 超时时间
}
```

### 返回结果

```typescript
interface CallToolResult {
  // 内容
  content: Array<{
    type: "text" | "image" | "resource";
    text?: string;
    data?: string;
    mimeType?: string;
  }>;

  // 是否错误
  isError?: boolean;
}
```

---

## 传输层

### Transport 接口

```typescript
interface Transport {
  // 连接
  connect(): Promise<void>;

  // 发送消息
  send(message: McpMessage): Promise<void>;

  // 接收消息
  receive(): Promise<McpMessage>;

  // 关闭
  close(): Promise<void>;
}
```

### STDIO 传输

```typescript
interface StdioTransport extends Transport {
  // 子进程
  process: ChildProcess;

  // 标准输入输出
  stdin: WriteStream;
  stdout: ReadStream;
}
```

### HTTP 传输

```typescript
interface HttpTransport extends Transport {
  // 服务器 URL
  url: string;

  // HTTP 客户端
  client: HttpClient;

  // 请求头
  headers: Record<string, string>;
}
```

---

## MCP 协议消息

### 请求

```typescript
interface McpRequest {
  jsonrpc: "2.0";
  id: string | number;
  method: string;
  params?: Record<string, unknown>;
}
```

### 响应

```typescript
interface McpResponse {
  jsonrpc: "2.0";
  id: string | number;
  result?: unknown;
  error?: {
    code: number;
    message: string;
    data?: unknown;
  };
}
```

### 通知

```typescript
interface McpNotification {
  jsonrpc: "2.0";
  method: string;
  params?: Record<string, unknown>;
}
```

---

## 生命周期管理

### 服务器生命周期

```typescript
interface McpServerLifecycle {
  // 启动服务器
  start(): Promise<void>;

  // 停止服务器
  stop(): Promise<void>;

  // 重启服务器
  restart(): Promise<void>;

  // 健康检查
  healthCheck(): Promise<boolean>;
}
```

### 连接管理

```typescript
class McpConnectionPool {
  // 最大连接数
  maxConnections: number;

  // 获取连接
  async getConnection(serverId: string): Promise<McpClient>;

  // 释放连接
  releaseConnection(serverId: string, client: McpClient): void;

  // 关闭所有连接
  async closeAll(): Promise<void>;
}
```

---

## 最佳实践

### 安全建议

1. **工具过滤**: 使用 `toolFilter` 只暴露必要的工具
2. **超时控制**: 设置合理的超时时间
3. **环境变量**: 敏感信息使用环境变量，不要硬编码
4. **沙箱**: 考虑使用沙箱限制工具能力

### 性能建议

1. **连接复用**: 使用连接池避免频繁建立连接
2. **超时重试**: 配置合理的重试策略
3. **并发控制**: 限制并发调用数量

---

## 文档导航

- [Agent 框架概述](./00_OVERVIEW.md)
- [LLM 接口详解](./01_LLM_INTERFACE.md)
- [上下文管理详解](./02_CONTEXT_MANAGEMENT.md)
- [记忆系统详解](./03_MEMORY_SYSTEM.md)
- [Skills 系统详解](./04_SKILLS_SYSTEM.md)
- [MCP 集成详解](./05_MCP_INTEGRATION.md) - 本文档
