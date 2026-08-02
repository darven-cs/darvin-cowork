# Sub-spec 35 — MCP Registry & Launcher（Go 端）

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：Go 端 `McpRegistry` + `LaunchResolver`（npx 优化）。**不包含** main 端 store（spec 36）、renderer UI（spec 37）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 34 mcp-transport-and-client](./2026-08-02-mcp-transport-and-client.md)

---

## 1. 概述

### 1.1 问题 / 背景

spec 34 落地了 MCP transport + client，但缺一个「统一管理多个 MCP server 连接 + 启动优化（npx 前置安装避免运行时解析延迟）」的 registry / launcher。

参考 LobsterAI 的 mcpLaunchResolverManager.ts（480 行）+ mcpStore.ts（332 行），本 spec 复用其设计（4 类 resolverKind + 5 类状态 + 指纹 hash 检测配置改动），但用 Go 重写。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `McpRegistry` 进程级按 serverId 索引 client + tools | `reg.GetTools(serverID)` O(1) |
| G2 | 4 类 `ResolverKind`：`npx` / `uvx` / `go` / `raw` | 单测覆盖各类 |
| G3 | npx 优化：`npm install --prefix` 前置安装 + 读 `package.json` bin | 单测用 mock npm 验证 |
| G4 | 源指纹 hash：命令+参数+env+platform+arch → sha256 | 用户改配置自动 invalidate |
| G5 | 状态机：`pending → installing → ready | failed | unsupported` | 单元 + 集成测试 |
| G6 | 陈旧 installing 自动重试（启动扫一遍） | 单测模拟中断后启动 |
| G7 | 与 spec 34 Client 集成：registry 自动 connect / disconnect | live 验证 |

### 1.3 非目标

- 不做 main 端 SQLite 持久化（spec 36）
- 不做 renderer UI（spec 37）
- 不做 OAuth / auth
- 不做 uvx / go 类的完整 resolver（v0 stub；只 npx 完整实现）
- 不做 marketplace 拉取

---

## 2. 用户场景

### 场景 1：注册 npx MCP server

**Given** 用户配置 `command=npx, args=[-y, @modelcontextprotocol/server-github]`
**When** `registry.Register(serverSpec)`
**Then**：
1. 检测 command 是 npx → resolverKind = npx
2. 创建 `LaunchResolution{status: pending}`
3. 异步启动 resolver（`go resolverManager.Resolve(serverID)`）
4. resolver 调 `npm view @modelcontextprotocol/server-github version --json` 拿 latest 版本
5. resolver 调 `npm install --prefix userData/mcp-packages/<serverID>/...` 装包
6. 读 `node_modules/@modelcontextprotocol/server-github/package.json` 的 bin
7. 更新 `LaunchResolution{status: ready, command: 'node', args: [abs-bin-path, ...extra]}` + emit McpResolutionReady
8. registry 用优化后的 command/args 启动 client + Connect + Initialize + ListTools
9. emit McpConnected + McpToolsListed

### 场景 2：用户改 command 后重新解析

**Given** server `github` 已 ready，`sourceFingerprint = hash(command|args|env|platform|arch)`
**When** 用户把 args 改为 `[-y, @modelcontextprotocol/server-github, --extra]`
**Then**：
1. 用户改 args → 触发新 bootstrap（spec 36 推送）
2. registry 算新指纹 hash，**不等于** 旧 hash
3. 旧 ready 结果 invalidate，status 回到 pending
4. 重新跑 resolver 流程（场景 1）

### 场景 3：npm install 失败

**Given** 网络不通 / npm registry down
**When** resolver 跑 `npm install`
**Then**：
1. spawn 失败 → status = failed，error = "npm install: ..."
2. **不破坏**原始 command；registry 仍用原始 command + args 启动 client（fallback）
3. UI 端显示「安装失败」+ error 详情 + [重试] 按钮

### 场景 4：陈旧 installing 自动重试

**Given** 上次 App 退出时，server `github` 状态是 installing（resolver goroutine 中断）
**When** App 重启 + registry.Load(persistedResolutions)
**Then**：
1. 扫描所有 resolution，若 status=installing 且 updatedAt < now-30min，标记 stale
2. 自动重新触发 resolver
3. 若 retry 仍失败 → status = failed

### 场景 5：禁用 server 后断开连接

**Given** server `filesystem` enabled + connected + 暴露 4 个 tools
**When** 用户 setEnabled(false)
**Then**：
1. registry 调 client.Close() 断开 transport
2. 从 tools list 中移除 4 个 tools
3. emit McpDisconnected
4. agent tool.Registry.Get("mcp:filesystem:read_file") 返回 nil（spec 38 联动）

### 场景 6：连接失败重试

**Given** server `filesystem` 启动成功但 Initialize 失败（如 server bug）
**When** client.Connect + Initialize 失败
**Then**：
1. 触发 Client.CallWithRetry 最多 3 次（指数退避 1s / 2s / 4s）
2. 3 次都失败 → status = error（connectionStatus），保留在 registry
3. emit McpConnectionFailed + error 消息

---

## 3. 功能需求

### FR-1: 数据类型

```go
// internal/mcp/types.go 增量

type ServerSpec struct {
    ID            string
    Name          string
    Description   string
    Enabled       bool
    Transport     TransportType  // stdio / sse / http
    Command       string         // stdio
    Args          []string       // stdio
    Env           map[string]string  // stdio
    URL           string         // sse/http
    Headers       map[string]string  // sse/http
    IsBuiltIn     bool
    GitHubURL     string
    RegistryID    string
}

type TransportType string
const (
    TransportStdio TransportType = "stdio"
    TransportSSE   TransportType = "sse"
    TransportHTTP  TransportType = "http"
)

type ResolverKind string
const (
    ResolverNpx ResolverKind = "npx"
    ResolverUvx ResolverKind = "uvx"
    ResolverGo  ResolverKind = "go"
    ResolverRaw ResolverKind = "raw"
)

type ResolutionStatus string
const (
    StatusPending     ResolutionStatus = "pending"
    StatusInstalling  ResolutionStatus = "installing"
    StatusReady       ResolutionStatus = "ready"
    StatusFailed      ResolutionStatus = "failed"
    StatusUnsupported ResolutionStatus = "unsupported"
)

type LaunchResolution struct {
    ServerID          string
    ResolverKind      ResolverKind
    SourceFingerprint string
    Status            ResolutionStatus
    PackageName       string
    RequestedVersion  string
    ResolvedVersion   string
    InstallDir        string
    Command           string         // 优化后绝对路径
    Args              []string
    Env               map[string]string
    Error             string
    InstalledAt       time.Time
    ResolvedAt        time.Time
    UpdatedAt         time.Time
}

type ServerStatus struct {
    ServerID         string
    Enabled          bool
    Resolving        bool           // resolver 正在跑
    Resolution       *LaunchResolution
    Connected        bool
    ConnectionError  string
    Tools            []ToolDescriptor
}

type ToolDescriptor = mcp.ToolDescriptor  // alias
```

### FR-2: McpRegistry

```go
// internal/mcp/registry.go
package mcp

import (
    "context"
    "fmt"
    "sync"
)

type Registry struct {
    mu          sync.RWMutex
    servers     map[string]*serverEntry     // serverID → entry
    resolver    *ResolverManager
    persistence ResolutionPersistence  // 由 main 端通过 SQLite 提供（spec 36）；v0 stub
}

type serverEntry struct {
    spec         ServerSpec
    status       ServerStatus
    client       *Client
    cancelFunc   context.CancelFunc
}

func NewRegistry(resolver *ResolverManager, persistence ResolutionPersistence) *Registry
func (r *Registry) Register(ctx context.Context, spec ServerSpec) error
func (r *Registry) Unregister(ctx context.Context, serverID string) error
func (r *Registry) SetEnabled(ctx context.Context, serverID string, enabled bool) error
func (r *Registry) List() []ServerStatus
func (r *Registry) Get(serverID string) (*ServerStatus, bool)
func (r *Registry) GetTools(serverID string) []ToolDescriptor
func (r *Registry) GetToolsByName(name string) (serverID string, tool *ToolDescriptor, found bool)

// 内部：
func (r *Registry) connectServer(ctx context.Context, serverID string) error  // 异步触发
func (r *Registry) disconnectServer(ctx context.Context, serverID string) error
func (r *Registry) persistResolution(res LaunchResolution) error  // 调 persistence.Save
```

**并发安全**：
- 所有 mutation 走 `mu.Lock()`
- 读走 `mu.RLock()`
- client.Close() 在 disconnect 时调用；连接建立后 client 不再修改

### FR-3: ResolverManager

```go
// internal/mcp/launcher.go
package mcp

type ResolverManager struct {
    rootDir    string  // userData/mcp-packages
    inFlight   sync.Map  // serverID → *resolveTask
    timeout    time.Duration  // 默认 60s
}

type resolveTask struct {
    cancel context.CancelFunc
}

func NewResolverManager(rootDir string) *ResolverManager

// Resolve 异步解析；返回 channel 通知结果
func (r *ResolverManager) Resolve(ctx context.Context, spec ServerSpec, fingerprint string) <-chan LaunchResolution

// 内部：根据 command 选 resolver
func (r *ResolverManager) pickResolver(spec ServerSpec) Resolver
type Resolver interface {
    Kind() ResolverKind
    Resolve(ctx context.Context, spec ServerSpec) (LaunchResolution, error)
}

type npxResolver struct {
    rootDir string
}
type uvxResolver struct{}  // v0 stub: 总是返回 unsupported
type goResolver struct{}   // v0 stub: 总是返回 unsupported
type rawResolver struct{}  // v0 总是返回 unsupported（command 直接执行，无需优化）
```

### FR-4: npx Resolver 详细流程

```go
// internal/mcp/launcher.go
func (r *npxResolver) Resolve(ctx context.Context, spec ServerSpec) (LaunchResolution, error) {
    // 1. 解析 args，识别 `npx -y <pkg>@<version>` 形式
    pkgSpec, extraArgs, err := parseNpxArgs(spec.Args)
    if err != nil {
        return LaunchResolution{
            ResolverKind: ResolverNpx,
            Status:       StatusUnsupported,
            Error:        err.Error(),
        }, nil
    }

    // 2. 调 npm view 拿版本
    cmd := exec.CommandContext(ctx, "npm", "view", pkgSpec.Name+"@"+pkgSpec.Version, "version", "--json")
    out, err := cmd.Output()
    if err != nil {
        return LaunchResolution{
            ResolverKind: ResolverNpx,
            Status:       StatusFailed,
            Error:        fmt.Sprintf("npm view: %v", err),
        }, nil
    }
    var resolvedVersion string
    json.Unmarshal(out, &resolvedVersion)

    // 3. 安装到 r.rootDir/<serverID>/<pkgName>/
    installDir := filepath.Join(r.rootDir, spec.ID, pkgSpec.Name)
    cmd = exec.CommandContext(ctx, "npm", "install",
        "--prefix", installDir,
        "--omit=dev",
        "--no-audit",
        "--no-fund",
        pkgSpec.Name+"@"+resolvedVersion,
    )
    if err := cmd.Run(); err != nil {
        return LaunchResolution{
            ResolverKind: ResolverNpx,
            Status:       StatusFailed,
            Error:        fmt.Sprintf("npm install: %v", err),
        }, nil
    }

    // 4. 读 package.json 拿 bin
    pkgJSONPath := filepath.Join(installDir, "node_modules", pkgSpec.Name, "package.json")
    pkgJSON, err := os.ReadFile(pkgJSONPath)
    if err != nil {
        return LaunchResolution{Status: StatusFailed, Error: "package.json not found"}, nil
    }
    var pkg struct {
        Bin map[string]string `json:"bin"`
    }
    json.Unmarshal(pkgJSON, &pkg)
    binName := filepath.Base(pkgSpec.Name)  // @scope/name → name
    binRel, ok := pkg.Bin[binName]
    if !ok {
        // 退化：取第一个 bin
        for _, v := range pkg.Bin {
            binRel = v
            break
        }
    }
    if binRel == "" {
        return LaunchResolution{Status: StatusFailed, Error: "no bin in package.json"}, nil
    }

    // 5. 生成优化后的 command
    binPath := filepath.Join(installDir, "node_modules", pkgSpec.Name, binRel)
    optimizedArgs := append([]string{binPath}, extraArgs...)

    return LaunchResolution{
        ResolverKind:      ResolverNpx,
        Status:            StatusReady,
        PackageName:       pkgSpec.Name,
        RequestedVersion:  pkgSpec.Version,
        ResolvedVersion:   resolvedVersion,
        InstallDir:        installDir,
        Command:           "node",
        Args:              optimizedArgs,
        ResolvedAt:        time.Now(),
    }, nil
}

func parseNpxArgs(args []string) (pkgSpec npxPackage, extraArgs []string, err error) {
    // 期望：[-y, <pkg>@<version>, ...extra]
    // 简化：扫描 args 找第一个 @ 开头的元素
    var pkgStr string
    for i, a := range args {
        if strings.HasPrefix(a, "-") { continue }
        pkgStr = a
        extraArgs = args[i+1:]
        break
    }
    if pkgStr == "" {
        return npxPackage{}, nil, errors.New("no package spec in args")
    }
    parts := strings.SplitN(pkgStr, "@", 3)
    if len(parts) < 2 {
        return npxPackage{}, nil, errors.New("invalid package spec")
    }
    name := "@" + parts[1]
    version := "latest"
    if len(parts) >= 3 && parts[2] != "" {
        name = "@" + parts[1] + "/" + parts[2]
        version = parts[3]  // 注意：scoped 包的特殊处理
    }
    return npxPackage{Name: name, Version: version}, extraArgs, nil
}
```

**注意**：scoped 包（`@scope/name`）路径按 `node_modules/@scope/name` 解析，name 字段不能去掉 `@scope` 前缀。

### FR-5: 源指纹

```go
// internal/mcp/resolver_fingerprint.go
package mcp

import (
    "crypto/sha256"
    "encoding/hex"
    "runtime"
)

func ComputeFingerprint(spec ServerSpec) string {
    payload := struct {
        TransportType string            `json:"transportType"`
        Command       string            `json:"command"`
        Args          []string          `json:"args"`
        Env           map[string]string `json:"env"`
        URL           string            `json:"url"`
        Headers       map[string]string `json:"headers"`
        Platform      string            `json:"platform"`
        Arch          string            `json:"arch"`
    }{
        TransportType: string(spec.Transport),
        Command:       spec.Command,
        Args:          spec.Args,
        Env:           spec.Env,
        URL:           spec.URL,
        Headers:       spec.Headers,
        Platform:      runtime.GOOS,
        Arch:          runtime.GOARCH,
    }
    raw, _ := json.Marshal(payload)
    sum := sha256.Sum256(raw)
    return hex.EncodeToString(sum[:])
}
```

### FR-6: 陈旧 installing 检测 + 自动重试

```go
// internal/mcp/registry.go
const staleResolutionTimeout = 30 * time.Minute

func (r *Registry) LoadStaleResolutions(ctx context.Context) {
    for _, serverID := range r.allServerIDs() {
        res, ok := r.getResolution(serverID)
        if !ok { continue }
        if res.Status != StatusInstalling { continue }
        if time.Since(res.UpdatedAt) < staleResolutionTimeout { continue }
        if r.resolver.IsInFlight(serverID) { continue }

        log.Warn("stale installing resolution, retry", "server", serverID)
        r.retryResolve(ctx, serverID)
    }
}

func (r *ResolverManager) IsInFlight(serverID string) bool {
    _, ok := r.inFlight.Load(serverID)
    return ok
}
```

`LoadStaleResolutions` 在 `cmd/app/main.go` 启动时调一次。

### FR-7: Registry 与 Client 集成

```go
// internal/mcp/registry.go
func (r *Registry) connectServer(ctx context.Context, serverID string) error {
    spec, status := r.getSpecAndStatus(serverID)
    if !spec.Enabled { return nil }

    // 1. 触发 resolver（如果是 npx 且还没 ready）
    res, ok := r.getResolution(serverID)
    if !ok || res.Status != StatusReady {
        // 触发异步 resolver
        ch := r.resolver.Resolve(ctx, spec, ComputeFingerprint(spec))
        // v0 简化：同步等待 resolver（不阻塞 UI，由 main 端通过 IPC 异步触发）
        select {
        case res = <-ch:
            r.persistResolution(res)
        case <-ctx.Done():
            return ctx.Err()
        }
    }

    // 2. 用优化后的 command/args 构造 transport
    var transport transport.Transport
    if spec.Transport == TransportStdio {
        cmd := res.Command
        if cmd == "" { cmd = spec.Command }  // fallback
        args := res.Args
        if args == nil { args = spec.Args }   // fallback
        env := mergeEnv(spec.Env, res.Env)
        transport = &transport.StdioTransport{
            Command: cmd,
            Args:    args,
            Env:     env,
        }
    } else {  // http
        transport = &transport.HTTPTransport{
            URL:     spec.URL,
            Headers: spec.Headers,
        }
    }

    // 3. 创建 client + Connect + Initialize + ListTools
    client := NewClient(transport)
    if err := client.Connect(ctx); err != nil {
        return fmt.Errorf("connect: %w", err)
    }
    if _, err := client.Initialize(ctx); err != nil {
        _ = client.Close()
        return fmt.Errorf("initialize: %w", err)
    }
    tools, err := client.ListTools(ctx)
    if err != nil {
        _ = client.Close()
        return fmt.Errorf("list tools: %w", err)
    }

    // 4. 更新 status
    r.updateStatus(serverID, ServerStatus{
        Enabled:   true,
        Connected: true,
        Tools:     tools,
    })

    return nil
}
```

### FR-8: 持久化接口（v0 stub，spec 36 实现）

```go
// internal/mcp/persistence.go
type ResolutionPersistence interface {
    SaveResolution(ctx context.Context, res LaunchResolution) error
    LoadAllResolutions(ctx context.Context) ([]LaunchResolution, error)
    DeleteResolution(ctx context.Context, serverID string) error
}

// v0 stub（内存版）：
type InMemoryResolutionPersistence struct {
    mu    sync.RWMutex
    store map[string]LaunchResolution
}
func (p *InMemoryResolutionPersistence) SaveResolution(...) error { /* ... */ }
// ... 其他方法
```

### FR-9: cmd 接入（占位）

```go
// cmd/app/main.go 增量
resolver := mcp.NewResolverManager(filepath.Join(cfg.UserDataDir, "mcp-packages"))
registry := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence())
```

---

## 4. 实现方案

### 4.1 文件清单

```
src/darvin-agent/internal/mcp/
├── types.go                       🆕 +ServerSpec / +TransportType / +ResolverKind / +ResolutionStatus / +LaunchResolution / +ServerStatus
├── registry.go                    🆕 McpRegistry ~350 行
├── launcher.go                    🆕 ResolverManager + 4 类 Resolver ~400 行
├── resolver_fingerprint.go        🆕 ComputeFingerprint ~50 行
├── persistence.go                 🆕 ResolutionPersistence interface + InMemory impl ~80 行
├── registry_test.go               🆕 ~250 行
├── launcher_test.go               🆕 ~300 行（mock npm）
├── resolver_fingerprint_test.go   🆕 ~60 行
└── persistence_test.go            🆕 ~80 行
```

### 4.2 关键代码片段（见 FR-2 / FR-3 / FR-4 / FR-5 / FR-6 / FR-7）

### 4.3 关键决策与理由

#### 4.3.1 复用 LobsterAI 的 4 类 resolverKind

**理由**：业界惯例（Anthropic / MCP / OpenClaw 都按此分类）；darvin-cowork 扩展时无需新增类型。

#### 4.3.2 v0 只实现 npx 完整逻辑，uvx / go / raw 是 stub

**理由**：npx 是最常见 MCP 安装方式（占 MCP marketplace 90%+）；uvx / go 是 v1 扩展位。

#### 4.3.3 陈旧 installing timeout 30 分钟

**理由**：npm install 一般 ≤ 60s；超过 30 分钟的 installing 一定是历史异常。30 分钟是兜底，不会误伤正在进行的 install。

#### 4.3.4 resolver 失败不阻塞启动

**理由**：用户即使 resolver 失败，仍可用原始 command 启动 MCP（fallback）；UI 端通过 error message + [重试] 按钮告知。

#### 4.3.5 持久化接口抽象（v0 in-memory，v1 SQLite）

**理由**：spec 36 main 端 SQLite 实现 `ResolutionPersistence` 接口；v0 阶段用 in-memory 跑通流程即可。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `registry_test.go` | Register / Unregister / SetEnabled / List / GetTools / 并发安全 |
| `launcher_test.go` | npx resolver 成功 / 失败 / scoped 包 / fallback to raw / 旧 installing 自动重试 |
| `resolver_fingerprint_test.go` | 同 spec 同 fingerprint；改 command 不同 fingerprint；改 platform 不同 fingerprint |
| `persistence_test.go` | Save / Load / Delete in-memory |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| npx 命令不存在 | `exec.LookPath("npx")` 失败 → resolver 返回 unsupported + error |
| npm registry 不可达 | `npm view` 失败 → status=failed |
| npm install 超时（>60s） | context cancel → status=failed |
| package.json 无 bin 字段 | status=failed + error="no bin" |
| scoped 包路径 | 按 `node_modules/@scope/name` 解析（保留 @scope） |
| 多 bin 字段（包导出多个命令） | 优先选与包 basename 同名 bin；退化取第一个 |
| 同一 server 多次 Register | 后者覆盖前者；in-flight 任务取消 |
| 陈旧 installing + 无 in-flight | 启动时 `LoadStaleResolutions` 自动重试 |
| 陈旧 installing + 有 in-flight（理论不该发生） | 不重试；log warn |
| 用户重复 enable / disable | 最新一次生效 |
| SetEnabled(false) 时正在 in-flight | 取消 resolver goroutine（cancelFunc） |
| userData/mcp-packages 不存在 | 启动时 `os.MkdirAll(rootDir, 0755)` |
| 磁盘空间不足（npm install） | spawn 返回 error → status=failed |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/mcp/types.go` | 🆕 +ServerSpec / TransportType / ResolverKind / ResolutionStatus / LaunchResolution / ServerStatus |
| `src/darvin-agent/internal/mcp/registry.go` | 🆕 |
| `src/darvin-agent/internal/mcp/launcher.go` | 🆕 |
| `src/darvin-agent/internal/mcp/resolver_fingerprint.go` | 🆕 |
| `src/darvin-agent/internal/mcp/persistence.go` | 🆕 |
| `src/darvin-agent/internal/mcp/registry_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/launcher_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/resolver_fingerprint_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/persistence_test.go` | 🆕 |
| `src/darvin-agent/cmd/app/main.go` | +bootstrap ResolverManager + Registry |

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告

**FR-1 类型**：
- [ ] ServerSpec / TransportType / ResolverKind / ResolutionStatus / LaunchResolution / ServerStatus 字段完整
- [ ] 5 个 status 字符串常量化

**FR-2 Registry**：
- [ ] Register / Unregister / SetEnabled 并发安全
- [ ] Get / GetTools O(1)
- [ ] List 返回所有 server 的 status

**FR-3 ResolverManager**：
- [ ] 4 类 Resolver 接口实现
- [ ] pickResolver 根据 command 路由（npx → npxResolver）

**FR-4 npx resolver**：
- [ ] `npx -y @scope/name@version` 解析成功
- [ ] `npm view` 失败返回 status=failed
- [ ] `npm install` 失败返回 status=failed
- [ ] 读 package.json bin 成功
- [ ] 生成 `node <abs-bin-path>` 启动命令

**FR-5 fingerprint**：
- [ ] 同 spec → 同 hash
- [ ] 改 command → 不同 hash
- [ ] 改 env → 不同 hash
- [ ] 改 platform → 不同 hash（用 build tag 跑多平台）

**FR-6 stale installing**：
- [ ] LoadStaleResolutions 30min 前的 installing 自动 retry
- [ ] in-flight 的 installing 不重试

**FR-7 connectServer**：
- [ ] 用优化后的 command/args 启动 transport
- [ ] fallback 到原始 command（resolver failed 时）
- [ ] Initialize 失败断开 + 报错
- [ ] ListTools 成功后更新 status

**FR-8 persistence**：
- [ ] InMemory 增删改查通过

**FR-9 cmd 接入**：
- [ ] 启动时 bootstrap Registry + ResolverManager
- [ ] userData/mcp-packages 目录自动创建

**集成手测**：

```bash
cd src/darvin-agent
cat > /tmp/mcp_registry_check.go <<'EOF'
package main
import (
    "context"
    "fmt"
    "darvin-cowork/internal/mcp"
)
func main() {
    resolver := mcp.NewResolverManager("/tmp/mcp-packages")
    reg := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence())
    spec := mcp.ServerSpec{
        ID: "filesystem", Name: "filesystem",
        Transport: mcp.TransportStdio,
        Command: "npx",
        Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
        Enabled: true,
    }
    reg.Register(context.Background(), spec)
    // 等 resolver 跑完
    for i := 0; i < 60; i++ {
        status, _ := reg.Get("filesystem")
        if status.Connected {
            fmt.Println("connected, tools:", len(status.Tools))
            for _, t := range status.Tools {
                fmt.Printf("  - %s\n", t.Name)
            }
            return
        }
        time.Sleep(1 * time.Second)
    }
    fmt.Println("connect timeout")
}
EOF
go run /tmp/mcp_registry_check.go
# 期望：filesystem server 连接 + 4 tools
```

---

## 8. 与其他 spec 的关系

**前置**：spec 34

**下游依赖**：
- spec 36（mcp-main-store-and-ipc）调 Registry.Register / SetEnabled
- spec 37（mcp-renderer-view）显示 ServerStatus
- spec 38（tool-registry-merge-and-routing）从 Registry.GetToolsByName 拿 tool

**并行**：spec 31 / 32 / 33（skills）不依赖本 spec

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现