# Sub-spec 36 — MCP Main Store & IPC

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：main 端 `mcpManager` + SQLite store + IPC `mcp:*` + bundled filesystem MCP。**不包含** Go 端 transport / client（spec 34）、registry / launcher（spec 35）、renderer UI（spec 37）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 34 mcp-transport-and-client](./2026-08-02-mcp-transport-and-client.md) + [spec 35 mcp-registry-and-launcher](./2026-08-02-mcp-registry-and-launcher.md)

---

## 1. 概述

### 1.1 问题 / 背景

Go 端有完整的 transport + client + registry + launcher，但 main 端不知道有哪些 MCP server，renderer 也看不到。本 spec 把 main 端的 SQLite 持久化 + IPC 通道 + bundled MCP 落地。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | main 端 SQLite 表 `mcp_servers` + `mcp_launch_resolutions` 创建成功 | 启动日志「mcp tables created」 |
| G2 | main 端 `mcpManager` 启动时从 SQLite 读 servers → 推给 Go | App 启动 ≤ 2s 内 Go 收到 bootstrap |
| G3 | 增 / 删 / 改 / 启停 MCP server 走 IPC → main → Go | live 验证 |
| G4 | Go 端 connection 状态变化 → notification → main → push renderer | live 验证 |
| G5 | bundled filesystem MCP 随 App 安装 | 启动后 renderer 看到 filesystem 已连接 |
| G6 | 复用 `merge-databases` 的 SQLite store 抽象 | `sqliteStore.run` / `sqliteStore.get` / `sqliteStore.all` |

### 1.3 非目标

- 不做 renderer UI（spec 37）
- 不做 OAuth / auth
- 不做 marketplace 拉取

---

## 2. 用户场景

### 场景 1：App 启动时 MCP bootstrap

**Given** SQLite `mcp_servers` 表有 1 个 bundled filesystem server（enabled=true）
**When** App 启动
**Then**：
1. main 端 `mcpManager.bootstrap()` 启动
2. 从 SQLite 读所有 server
3. 调 `agent.mcp.bootstrap({ servers: [...] })` 推给 Go
4. Go 端 registry.Register + connectServer 触发 resolver（npx 优化）
5. filesystem server 连接成功 + 4 tools，emit `agent.mcp.connection_changed` notification
6. main 端收到 notification → push `onMcpConnectionChanged` 给 renderer

### 场景 2：用户新增 stdio MCP server

**Given** renderer 端填写：name=`github`，transport=`stdio`，command=`npx`，args=`-y @modelcontextprotocol/server-github`
**When** renderer 调 `window.darvin.mcp.create({ ... })`
**Then**：
1. main IPC handler `mcp:create` → mcpManager.createServer
2. 写 SQLite
3. 调 Go 端 `agent.mcp.register`
4. Go 端触发 resolver → 异步连接
5. UI 端收到 connection_changed → 卡片状态 connecting → connected

### 场景 3：用户新增 http MCP server

**Given** URL=`http://localhost:3001/mcp`，无 auth
**When** renderer 调 create
**Then** 走 http transport 流程（同场景 2 但 transport 不同）

### 场景 4：禁用 server

**Given** `filesystem` server enabled + connected + 4 tools
**When** renderer 调 `setEnabled({ id: 'filesystem', enabled: false })`
**Then**：
1. main 写 SQLite（enabled=0）
2. 调 Go 端 `agent.mcp.set_enabled({ id: 'filesystem', enabled: false })`
3. Go 端 client.Close() + 从 registry 移除
4. emit `agent.mcp.connection_changed { id, status: 'disconnected' }`
5. main → renderer：filesystem 卡片 toggle 已关，状态 disconnected

### 场景 5：删除 server

**Given** `github` server（已 disconnected / 任意状态）
**When** renderer 调 `delete({ id: 'github' })`
**Then**：
1. main 写 SQLite DELETE
2. 调 Go 端 `agent.mcp.unregister({ id: 'github' })`
3. Go 端从 registry 移除 + delete resolution
4. main → renderer：github 卡片消失

### 场景 6：连接失败重试

**Given** 上一个 filesystem server 连接失败（status=`error`，error=`ECONNREFUSED`）
**When** 用户点 [重试] 按钮
**Then** main 调 `retryLaunchResolution({ id: 'filesystem' })` → Go 端重新触发 resolver + connect

---

## 3. 功能需求

### FR-1: 共享类型（`src/shared/darvin-api.ts`）

```typescript
export type McpTransportType = 'stdio' | 'sse' | 'http';

export type McpLaunchStatus = 'pending' | 'installing' | 'ready' | 'failed' | 'unsupported';
export type McpConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'error';

export interface DarvinMcpServerExposedTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
}

export interface DarvinMcpServer {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  transportType: McpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  isBuiltIn: boolean;
  githubUrl?: string;
  registryId?: string;
  createdAt: number;
  updatedAt: number;
  launchStatus?: McpLaunchStatus;
  launchError?: string;
  connectionStatus?: McpConnectionStatus;
  connectionError?: string;
  exposedTools?: DarvinMcpServerExposedTool[];
}

export interface DarvinMcpServerCreate {
  name: string;
  description?: string;
  enabled?: boolean;
  transportType: McpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
}

export interface DarvinMcpServerPatch {
  name?: string;
  description?: string;
  enabled?: boolean;
  transportType?: McpTransportType;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
}

export interface DarvinApi {
  // mcp
  listMcpServers(): Promise<{ servers: DarvinMcpServer[] }>;
  createMcpServer(req: DarvinMcpServerCreate): Promise<{ server: DarvinMcpServer }>;
  updateMcpServer(req: { id: string; patch: DarvinMcpServerPatch }): Promise<{ server: DarvinMcpServer }>;
  deleteMcpServer(req: { id: string }): Promise<{ ok: boolean }>;
  setMcpServerEnabled(req: { id: string; enabled: boolean }): Promise<{ ok: boolean }>;
  testMcpConnection(req: { id: string }): Promise<{ ok: boolean; error?: string; tools?: DarvinMcpServerExposedTool[] }>;
  retryLaunchResolution(req: { id: string }): Promise<{ ok: boolean }>;
  onMcpServersChanged(handler: (servers: DarvinMcpServer[]) => void): () => void;
  onMcpConnectionChanged(handler: (e: { id: string; status: McpConnectionStatus; error?: string }) => void): () => void;
}
```

### FR-2: SQLite 表

```sql
-- 合并后 db schema（spec merge-databases 已落位）
CREATE TABLE IF NOT EXISTS mcp_servers (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  enabled         INTEGER NOT NULL DEFAULT 1,
  is_built_in     INTEGER NOT NULL DEFAULT 0,
  transport_type  TEXT NOT NULL,            -- stdio / sse / http
  config_json     TEXT NOT NULL,            -- JSON: {command, args, env, url, headers, githubUrl, registryId}
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);

CREATE TABLE IF NOT EXISTS mcp_launch_resolutions (
  server_id          TEXT PRIMARY KEY REFERENCES mcp_servers(id) ON DELETE CASCADE,
  resolver_kind      TEXT NOT NULL,         -- npx / uvx / go / raw
  source_fingerprint TEXT NOT NULL,
  status             TEXT NOT NULL,         -- pending / installing / ready / failed / unsupported
  package_name       TEXT,
  requested_version  TEXT,
  resolved_version   TEXT,
  install_dir        TEXT,
  command            TEXT,                  -- 优化后
  args_json          TEXT,                  -- JSON array
  env_json           TEXT,                  -- JSON object
  error              TEXT,
  installed_at       INTEGER,
  resolved_at        INTEGER,
  updated_at         INTEGER NOT NULL
);
```

### FR-3: Main 端 mcpStore

```typescript
// src/main/libs/mcpStore.ts
export class McpStore {
  constructor(private sqlite: SqliteStore) {}

  async createServer(server: Omit<DarvinMcpServer, 'createdAt' | 'updatedAt'>): Promise<DarvinMcpServer> {
    const now = Date.now();
    const config = {
      command: server.command,
      args: server.args,
      env: server.env,
      url: server.url,
      headers: server.headers,
      githubUrl: server.githubUrl,
      registryId: server.registryId,
    };
    this.sqlite.run(
      `INSERT INTO mcp_servers (id, name, description, enabled, is_built_in, transport_type, config_json, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      [server.id, server.name, server.description, server.enabled ? 1 : 0, server.isBuiltIn ? 1 : 0,
       server.transportType, JSON.stringify(config), now, now]
    );
    return { ...server, createdAt: now, updatedAt: now };
  }

  async getServer(id: string): Promise<DarvinMcpServer | null> { /* ... */ }
  async listServers(): Promise<DarvinMcpServer[]> { /* ... */ }
  async updateServer(id: string, patch: DarvinMcpServerPatch): Promise<DarvinMcpServer> { /* ... */ }
  async deleteServer(id: string): Promise<void> { /* cascade delete resolution */ }

  async saveResolution(serverId: string, res: { /* LaunchResolution */ }): Promise<void> { /* ... */ }
  async loadResolutions(): Promise<Map<string, any>> { /* ... */ }
  async deleteResolution(serverId: string): Promise<void> { /* ... */ }
}
```

### FR-4: Main 端 mcpManager

```typescript
// src/main/libs/mcpManager.ts
export class McpManager {
  private servers: Map<string, DarvinMcpServer> = new Map();

  constructor(
    private store: McpStore,
    private agent: AgentClient,
    private eventBus: EventBus,
  ) {}

  async bootstrap(): Promise<void> {
    // 1. 从 SQLite 读 servers
    const servers = await this.store.listServers();
    this.servers = new Map(servers.map(s => [s.id, s]));

    // 2. 推给 Go
    await this.agent.mcp.bootstrap({ servers: Array.from(this.servers.values()) });

    // 3. 订阅 Go connection_changed
    this.agent.mcp.onConnectionChanged(({ id, status, error }) => {
      const server = this.servers.get(id);
      if (!server) return;
      server.connectionStatus = status;
      server.connectionError = error;
      this.broadcastConnection({ id, status, error });
    });

    // 4. 订阅 Go resolution_changed
    this.agent.mcp.onResolutionChanged(({ serverId, resolution }) => {
      // 写 SQLite
      this.store.saveResolution(serverId, resolution);
      const server = this.servers.get(serverId);
      if (server) {
        server.launchStatus = resolution.status;
        server.launchError = resolution.error;
        this.broadcastServers();
      }
    });
  }

  async listServers(): Promise<DarvinMcpServer[]> {
    return Array.from(this.servers.values());
  }

  async createServer(req: DarvinMcpServerCreate): Promise<DarvinMcpServer> {
    const id = `mcp_${crypto.randomUUID()}`;
    const server = await this.store.createServer({
      id,
      name: req.name,
      description: req.description ?? '',
      enabled: req.enabled ?? true,
      isBuiltIn: false,
      transportType: req.transportType,
      command: req.command,
      args: req.args,
      env: req.env,
      url: req.url,
      headers: req.headers,
      githubUrl: '',
      registryId: '',
      createdAt: Date.now(),
      updatedAt: Date.now(),
    });
    this.servers.set(id, server);
    await this.agent.mcp.register({ server });
    this.broadcastServers();
    return server;
  }

  async updateServer(id: string, patch: DarvinMcpServerPatch): Promise<DarvinMcpServer> {
    const server = await this.store.updateServer(id, patch);
    this.servers.set(id, server);
    await this.agent.mcp.update({ id, patch });
    this.broadcastServers();
    return server;
  }

  async deleteServer(id: string): Promise<void> {
    await this.store.deleteServer(id);
    this.servers.delete(id);
    await this.agent.mcp.unregister({ id });
    this.broadcastServers();
  }

  async setEnabled(id: string, enabled: boolean): Promise<void> {
    await this.store.updateServer(id, { enabled });
    const server = this.servers.get(id);
    if (server) server.enabled = enabled;
    await this.agent.mcp.setEnabled({ id, enabled });
    this.broadcastServers();
  }

  async testConnection(id: string): Promise<{ ok: boolean; error?: string; tools?: any[] }> {
    return this.agent.mcp.test({ id });
  }

  async retryLaunchResolution(id: string): Promise<void> {
    await this.agent.mcp.retryResolution({ id });
  }

  private broadcastServers(): void {
    this.eventBus.emit('mcp:servers_changed', Array.from(this.servers.values()));
  }

  private broadcastConnection(e: { id: string; status: McpConnectionStatus; error?: string }): void {
    this.eventBus.emit('mcp:connection_changed', e);
  }

  async shutdown(): Promise<void> { /* ... */ }
}
```

### FR-5: IPC Channels（`src/main/index.ts`）

```typescript
ipcMain.handle('mcp:list', async () => ({ servers: await mcpManager.listServers() }));
ipcMain.handle('mcp:create', async (_e, req) => ({ server: await mcpManager.createServer(req) }));
ipcMain.handle('mcp:update', async (_e, { id, patch }) => ({ server: await mcpManager.updateServer(id, patch) }));
ipcMain.handle('mcp:delete', async (_e, { id }) => ({ ok: await mcpManager.deleteServer(id) }));
ipcMain.handle('mcp:set_enabled', async (_e, { id, enabled }) => ({ ok: await mcpManager.setEnabled(id, enabled) }));
ipcMain.handle('mcp:test', async (_e, { id }) => mcpManager.testConnection(id));
ipcMain.handle('mcp:retry_resolution', async (_e, { id }) => ({ ok: await mcpManager.retryLaunchResolution(id) }));

// Push to renderer
mainWindow.webContents.send('mcp:servers_changed', servers);   // 由 mcpManager.broadcastServers() 触发
mainWindow.webContents.send('mcp:connection_changed', event);  // 由 mcpManager.broadcastConnection() 触发
```

### FR-6: Preload 暴露（`src/preload/index.ts`）

```typescript
contextBridge.exposeInMainWorld('darvin', {
  mcp: {
    list: () => ipcRenderer.invoke('mcp:list'),
    create: (req) => ipcRenderer.invoke('mcp:create', req),
    update: ({ id, patch }) => ipcRenderer.invoke('mcp:update', { id, patch }),
    delete: ({ id }) => ipcRenderer.invoke('mcp:delete', { id }),
    setEnabled: ({ id, enabled }) => ipcRenderer.invoke('mcp:set_enabled', { id, enabled }),
    test: ({ id }) => ipcRenderer.invoke('mcp:test', { id }),
    retryResolution: ({ id }) => ipcRenderer.invoke('mcp:retry_resolution', { id }),
    onServersChanged: (handler) => { /* ... */ },
    onConnectionChanged: (handler) => { /* ... */ },
  },
});
```

### FR-7: Agent Client 扩展（`src/main/runtime/client.ts`）

```typescript
class AgentClient {
  mcp = {
    list: () => this.request<{ servers: any[] }>('agent.mcp.list'),
    register: (req: { server: any }) => this.request('agent.mcp.register', req),
    update: (req: { id: string; patch: any }) => this.request('agent.mcp.update', req),
    unregister: (req: { id: string }) => this.request('agent.mcp.unregister', req),
    setEnabled: (req: { id: string; enabled: boolean }) => this.request('agent.mcp.set_enabled', req),
    test: (req: { id: string }) => this.request('agent.mcp.test', req),
    retryResolution: (req: { id: string }) => this.request('agent.mcp.retry_resolution', req),
    bootstrap: (req: { servers: any[] }) => this.request('agent.mcp.bootstrap', req),
    onConnectionChanged: (handler: (e: any) => void) => { this.on('mcp.connection_changed', handler); },
    onResolutionChanged: (handler: (e: any) => void) => { this.on('mcp.resolution_changed', handler); },
  };
}
```

### FR-8: Go 端 IPC handler

```go
// internal/gateway/handlers.go 增量
h.handle("agent.mcp.list", func(params json.RawMessage) (any, error) {
    servers := h.McpRegistry.List()
    return map[string]any{"servers": convertToDarvinMcp(servers)}, nil
})

h.handle("agent.mcp.register", func(params json.RawMessage) (any, error) {
    var req struct {
        Server mcp.ServerSpec `json:"server"`
    }
    if err := json.Unmarshal(params, &req); err != nil { return nil, err }
    if err := h.McpRegistry.Register(ctx, req.Server); err != nil { return nil, err }
    return map[string]any{"ok": true}, nil
})

// update / unregister / set_enabled / test / retry_resolution 类似
h.handle("agent.mcp.bootstrap", func(params json.RawMessage) (any, error) {
    var req struct {
        Servers []mcp.ServerSpec `json:"servers"`
    }
    if err := json.Unmarshal(params, &req); err != nil { return nil, err }
    for _, s := range req.Servers {
        if err := h.McpRegistry.Register(ctx, s); err != nil {
            log.Warn("mcp bootstrap register", "id", s.ID, "err", err)
        }
    }
    return map[string]any{"ok": true}, nil
})

// Connection notification (registry emit)
func (h *Handlers) OnMcpConnectionChanged(serverID string, status mcp.ConnectionStatus, errMsg string) {
    h.notify("mcp.connection_changed", map[string]any{
        "id":     serverID,
        "status": string(status),
        "error":  errMsg,
    })
}

// Resolution notification
func (h *Handlers) OnMcpResolutionChanged(serverID string, res mcp.LaunchResolution) {
    h.notify("mcp.resolution_changed", map[string]any{
        "serverId":   serverID,
        "resolution": convertToDarvinResolution(res),
    })
}
```

### FR-9: bundled filesystem MCP

**bundled 形式**：filesystem MCP server 写为 darvin-agent 内置 Go 二进制（不是 npx 包）。

```go
// src/darvin-agent/resources/mcp-bundled/filesystem/server.go
// 独立的 Go 程序，作为 cmd subcommand 注册：darvin-agent mcp-filesystem
package main

import (
    "encoding/json"
    "fmt"
    "os"
)

func main() {
    // 极简实现：暴露 list_directory / read_file / write_file 三个工具
    // 走 stdio + JSON-RPC 2.0（用标准库）
    scanner := bufio.NewScanner(os.Stdin)
    for scanner.Scan() {
        // 解析 JSON-RPC request
        // 调对应 handler
        // 输出 JSON-RPC response
    }
}
```

**注册方式**：在 darvin-agent `cmd/app/main.go` 启动时，bundled filesystem server 注册为一个 `ServerSpec`：
```go
spec := mcp.ServerSpec{
    ID:          "filesystem",
    Name:        "Filesystem",
    Description: "本地文件系统读写（bundled）",
    Enabled:     true,
    IsBuiltIn:   true,
    Transport:   mcp.TransportStdio,
    Command:     os.Args[0],  // darvin-agent 二进制自己
    Args:        []string{"mcp-filesystem"},
    Env:         map[string]string{},
}
h.McpRegistry.Register(ctx, spec)
```

**第一次安装**：main 端 `mcpManager.bootstrap()` 时检测 SQLite 没有 filesystem → 写 SQLite → bootstrap 给 Go。

---

## 4. 实现方案

### 4.1 文件清单

```
src/main/
├── libs/
│   ├── mcpManager.ts              🆕 ~250 行
│   ├── mcpManager.test.ts         🆕 ~80 行
│   ├── mcpStore.ts                🆕 ~250 行
│   └── mcpStore.test.ts           🆕 ~100 行
├── index.ts                       +8 个 ipcMain.handle
├── runtime/client.ts              +mcp.* 方法
└── libs/user-paths.ts             +getMcpStoreDir() / +getMcpPackagesDir()

src/preload/
└── index.ts                       +window.darvin.mcp.*

src/shared/
└── darvin-api.ts                  +DarvinMcpServer 等

src/darvin-agent/
├── internal/gateway/
│   └── handlers.go                +6 个 mcp handler
├── resources/mcp-bundled/
│   └── filesystem/
│       └── server.go              🆕 内置 filesystem MCP server
└── cmd/app/
    ├── main.go                    +register bundled filesystem + bootstrap mcp
    └── mcp_filesystem.go          🆕 bundled filesystem command
```

### 4.2 关键代码片段（见 FR-3 / FR-4 / FR-8）

### 4.3 关键决策与理由

#### 4.3.1 bundled filesystem 用 Go 写（不依赖 npx）

**理由**：darvin-agent 是 Go 二进制，可以自己注册一个 MCP server；bundled 不需要外部依赖。其它 MCP marketplace server 仍走 npx。

#### 4.3.2 main 端 SQLite 持久化 launchResolution（不只 Go 端 in-memory）

**理由**：App 重启时需要加载上次的状态，避免重复 resolve。main 端 SQLite 是 source of truth，Go 端 `LoadStaleResolutions`（spec 35）从 main 端读。

#### 4.3.3 connection_changed 是 notification（不是 request/response）

**理由**：连接状态变化是异步事件；Go → main push 模式。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `mcpStore.test.ts` | create / get / list / update / delete / saveResolution / cascade delete |
| `mcpManager.test.ts` | bootstrap / create / update / delete / setEnabled / test / retryResolution |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| SQLite 已有 server 但 Go 端没启动 | bootstrap 阶段重试注册 3 次 |
| 用户新建 server 时 Go agent 已死 | main 写 SQLite + 缓存；下次启动重试 |
| Go agent connection_changed 频率高（重连循环） | main 节流：相同 status ≤ 1s 内只 push 一次 |
| bundled filesystem SQLite 不存在 | bootstrap 时 insert（幂等 upsert） |
| 用户删 server 时 connection 是 connecting | 先 cancel 连接 → 再 delete SQLite |
| 测试连接（testConnection）时 server 正在重连 | 复用现有连接；不重新 Connect |
| 重试 launch resolution 但 Go 端 in-flight | Go 端返回 error "already in flight" |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/main/libs/mcpManager.ts` | 🆕 |
| `src/main/libs/mcpManager.test.ts` | 🆕 |
| `src/main/libs/mcpStore.ts` | 🆕 |
| `src/main/libs/mcpStore.test.ts` | 🆕 |
| `src/main/libs/user-paths.ts` | +`getMcpStoreDir()` / `getMcpPackagesDir()` |
| `src/main/index.ts` | +8 个 ipc handler + mainWindow push |
| `src/main/runtime/client.ts` | +`mcp.*` |
| `src/preload/index.ts` | +`window.darvin.mcp.*` |
| `src/shared/darvin-api.ts` | +`DarvinMcpServer` 等 |
| `src/darvin-agent/internal/gateway/handlers.go` | +6 个 mcp handler + 2 个 notification callback |
| `src/darvin-agent/resources/mcp-bundled/filesystem/server.go` | 🆕 |
| `src/darvin-agent/cmd/app/main.go` | +bundled filesystem 注册 + bootstrap mcp |
| `src/darvin-agent/cmd/app/mcp_filesystem.go` | 🆕 subcommand |

---

## 7. 验收标准

**通用**：
- [ ] `npm run lint` + `npm run test` 通过
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告

**FR-1 类型**：
- [ ] `DarvinMcpServer` + `DarvinMcpServerCreate` + `DarvinMcpServerPatch` 字段完整

**FR-2 SQLite**：
- [ ] `mcp_servers` + `mcp_launch_resolutions` 表创建成功
- [ ] cascade delete 工作

**FR-3 mcpStore**：
- [ ] createServer / listServers / updateServer / deleteServer 通过单测
- [ ] saveResolution / loadResolutions 通过单测

**FR-4 mcpManager**：
- [ ] bootstrap 从 SQLite 读 + 推 Go
- [ ] 订阅 connection_changed / resolution_changed → 更新 SQLite + push renderer
- [ ] createServer 写 SQLite + 调 Go register + broadcast

**FR-5 IPC**：
- [ ] 8 个 ipcMain.handle 注册
- [ ] push 给 renderer 走 mainWindow.webContents.send

**FR-6 preload**：
- [ ] `window.darvin.mcp.*` 9 个方法

**FR-7 AgentClient**：
- [ ] `client.mcp.*` 9 个方法

**FR-8 Go handler**：
- [ ] 6 个 handler（list / register / update / unregister / set_enabled / test / retry_resolution / bootstrap）

**FR-9 bundled filesystem**：
- [ ] darvin-agent binary `mcp-filesystem` subcommand 可执行
- [ ] 启动时 bundled filesystem 自动注册
- [ ] SQLite insert（幂等）

**live 验证**：

```bash
# 启动 App
npm start

# 启动日志包含：
# [mcp] bundled filesystem registered
# [mcp] bootstrap 1 server(s)

# renderer console:
await window.darvin.mcp.list()
# 期望：{ servers: [{id:'filesystem', enabled:true, connectionStatus:'connected', exposedTools:[...]}] }

await window.darvin.mcp.test({ id: 'filesystem' })
# 期望：{ ok: true, tools: [{name:'list_directory', ...}, ...] }
```

---

## 8. 与其他 spec 的关系

**前置**：spec 34 + 35

**下游依赖**：
- spec 37（mcp-renderer-view）消费本 spec 的 `window.darvin.mcp.*`
- spec 38（tool-registry-merge-and-routing）通过 Go 端 registry 拿 mcp tools

**并行**：spec 31 / 32 / 33（skills）

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现