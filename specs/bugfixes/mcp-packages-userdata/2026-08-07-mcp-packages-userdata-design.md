# MCP packages 移至 user-data 设计文档

## 1. 概述

### 1.1 问题

`mcp-packages/` 目录泄漏到项目根,污染源码树。当前 Go 端 `bootstrapMCP`(`internal/runtime/mcp.go:13-19`) 把根路径硬编码成 `<workspace>/mcp-packages`,而 `workspace` 在某些执行路径下会回退到 `cfg.Agent.Workdir = "."`(`src/darvin-agent/config.yaml:39`),cwd 恰好是项目根,直接 `os.MkdirAll` 把目录写到 `/home/darven/桌面/dev_app/darvin-cowork/mcp-packages/`,后续 `npm install` 在里头铺 `node_modules/`,污染 git status。

直接观察:`git status` 出现 `?? mcp-packages/`,目录里已有 `mcp_fa4db17d-fd54-42b8-84a6-772c9b702e4e/{node_modules,package.json,package-lock.json}`,装的是 `@modelcontextprotocol/server-github`,说明某个真实的 MCP server install 已经在这条坏路径上跑过了。

### 1.2 根因

三处互相耦合:

1. **`config.yaml:39 workdir: "."`** — dev 默认把工作目录钉到 cwd,看似无害,实际是兜底链路的起点。
2. **`resolveWorkspace(cfg, opts.WorkspaceRoot)`(`internal/runtime/agent_config.go:34`)** — `override` 缺失就静默回退到 `cfg.Agent.Workdir`,没有任何"必须是绝对路径"的硬约束。
3. **`bootstrapMCP(root = workspace + "/mcp-packages")`** — 把 MCP 根目录绑定到 workspace,workspace 是 fsSandbox / 工具执行用的根,把"用户安装的 npm 包"这个**应用层数据**和"工具沙箱"耦合在了一起,任何上游路径错了都会污染下游。

参考 DeepSeek-Reasonix desktop 与 LobsterAI:两者 MCP packages 都直接放在 `app.getPath('userData')/<brand>/mcp-packages/`,跟 workspace 完全解耦。

### 1.3 目标

`mcp-packages/` 永远落在 Electron 用户数据目录,与 workspace / cwd / session 完全无关;同时把子目录命名从 `<server.id>` 升级到 LobsterAI 的 `<server.id>-<packageName>` 模式,让"同一个 server 改包名"有显式目录隔离,不再靠 npm 隐式 detect。

- production(`npm start`):`<userData>/darvin-agent/mcp-packages/<server.id>-<packageName>/`
- dev(`go run` / 直接 `cd src/darvin-agent && go test`):`$XDG_CACHE_HOME/darvin-cowork/mcp-packages/`(或 OS 等价物)兜底
- 任何回退路径必须是**绝对路径**,相对路径 → 启动失败并报错
- 一次性:仓库根现存 `mcp-packages/` 清掉,加进 `.gitignore` 防复发

### 1.4 非目标

- 不动 `cfg.Agent.Workdir` 字段本身(其他用途还在用,例如 fsSandbox 的 `Workdir`);只把 MCP 路径从 workspace 派生链上摘下来
- 不动 `getMcpPackagesDir()`(`src/main/libs/user-paths.ts:74`)— 它已经返回正确路径,只是没人注入
- 不改 MCP server 注册 / resolve / registry 接口
- 不做"老 `<server.id>/` 目录 → 新 `<server.id>-<packageName>/`"自动 rename 迁移 — 用户已有的旧目录变孤儿,下次 install 装到新位置即可(orphan 自动过期;不在 hot path 上,代价一次)

## 2. 用户场景

### 场景 1: production `npm start` 装 MCP server

**Given** 用户首次启动 app,Electron `userData` 为 `~/.config/darvin-cowork/`
**When** 通过 settings 装一个 npx 类 MCP server
**Then** package 落到 `~/.config/darvin-cowork/darvin-agent/mcp-packages/mcp_<sid>/`,`git status` 干净

### 场景 2: 仓库根残留清理

**Given** 之前误生成的 `mcp-packages/` 已在仓库根(带 `node_modules`)
**When** 应用本 spec
**Then** `rm -rf mcp-packages/` + `.gitignore` 加 `mcp-packages/`,后续 dev run 不会再写到这里

### 场景 3: dev / 裸 `go run`

**Given** 开发者 `cd src/darvin-agent && go run ./cmd/app`,没有 Electron 包装,`opts.WorkspaceRoot` 为空
**When** agent 启动
**Then** MCP 路径走 dev fallback = `$XDG_CACHE_HOME/darvin-cowork/mcp-packages`(macOS: `~/Library/Caches/darvin-cowork/mcp-packages`,Windows: `%LocalAppData%\darvin-cowork\mcp-packages`),绝不写到 cwd

### 场景 4: 启动期路径校验

**Given** Go 端拿到 `DARVIN_MCP_PACKAGES_DIR` 但路径是相对路径或为空
**When** `bootstrapMCP` 跑
**Then** 立即返回 error,runtime.Build 失败,stderr 报清晰错信息("mcp-packages root must be absolute: <path>"),不静默写到 cwd

### 场景 5: 共享依赖 — 跨 session 复用 node_modules

**Given** session A 注册了 server `github-mcp`,使用 `@modelcontextprotocol/server-github`
**When** session B 也用同一个 server config 注册同一 package
**Then** 都落到 `<mcpRoot>/github-mcp-@modelcontextprotocol-server-github/`,共享同一份 `node_modules/`,无副本

### 场景 6: 同 server 改包名 — 显式目录隔离

**Given** `server-github` 当前用 `@modelcontextprotocol/server-github@1.0.0`,目录是 `…/server-github-@modelcontextprotocol-server-github/`
**When** 用户把 package 升级/换成 `@tangibly/server-github`
**Then** 落到新目录 `…/server-github-@tangibly-server-github/`,老目录变孤儿(下次 install 不重用),不污染依赖树

### 场景 7: 路径安全 — sanitize

**Given** server id 或 package name 含 `/`(例如 `@scope/name`)或非 ASCII 字符
**When** 计算 installDir
**Then** `/`、空白、控制字符归 `-`,首尾 `-` 去掉,空串 fallback `mcp`;最终路径段稳定可作为目录名

## 3. 功能需求

### FR-1: main 端注入 `DARVIN_MCP_PACKAGES_DIR`

`src/main/runtime/manager.ts:start()` 在已有 `env.DARVIN_SESSIONS_DSN = agentSessionsDsnPath()` 的旁边追加:

```ts
env.DARVIN_MCP_PACKAGES_DIR = getMcpPackagesDir();
```

`getMcpPackagesDir()`(`src/main/libs/user-paths.ts:74`)已经存在并返回 `<userData>/darvin-agent/mcp-packages`,无需新增 helper。

### FR-2: Go 端读取 `DARVIN_MCP_PACKAGES_DIR`

新增 `internal/runtime/paths.go`(或并入 `agent_config.go`),实现:

```go
func resolveMCPPackagesDir() (string, error) {
    if p := strings.TrimSpace(os.Getenv("DARVIN_MCP_PACKAGES_DIR")); p != "" {
        if !filepath.IsAbs(p) {
            return "", fmt.Errorf("DARVIN_MCP_PACKAGES_DIR must be absolute, got %q", p)
        }
        return p, nil
    }
    // dev fallback: $XDG_CACHE_HOME/darvin-cowork/mcp-packages,etc.
    if c, err := os.UserCacheDir(); err == nil && c != "" {
        return filepath.Join(c, "darvin-cowork", "mcp-packages"), nil
    }
    return "", fmt.Errorf("no DARVIN_MCP_PACKAGES_DIR set and UserCacheDir unavailable")
}
```

`bootstrapMCP(ctx, log, mcpRoot)` 接收这个值,不再接 workspace 派生路径。

### FR-3: bootstrapMCP 路径校验

```go
func bootstrapMCP(ctx context.Context, log *zap.Logger, mcpRoot string) (*mcp.Registry, error) {
    if !filepath.IsAbs(mcpRoot) {
        return nil, fmt.Errorf("bootstrapMCP: mcp-packages root must be absolute: %q", mcpRoot)
    }
    if err := os.MkdirAll(mcpRoot, 0o755); err != nil {
        log.Warn("mcp packages dir create failed", zap.Error(err))
    }
    resolver := mcp.NewResolverManager(mcpRoot).WithLogger(log)
    registry := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence()).WithLogger(log)
    if err := registry.LoadStaleResolutions(ctx); err != nil {
        log.Warn("mcp stale resolution scan failed", zap.Error(err))
    }
    return registry, nil
}
```

绝对路径校验是防御性的:FR-2 已经把校验做了,这里再保一道,即便 FR-2 未来被改也不会再退回到 cwd。

### FR-4: 启动期硬失败而非静默

`Build(opts)` 链路里 `resolveMCPPackagesDir()` 失败 → `Build` 直接 return error。错误信息要清晰包含:**变量名 / 当前值 / 期望(必须绝对路径或留空走 UserCacheDir fallback)**。

`internal/runtime/runtime.go:Build` 拿到错误用 `fmt.Errorf("build: %w", err)` 包一层,与现有错误传播风格对齐。

### FR-5: MCP 子目录对齐 LobsterAI 命名 `<server.id>-<packageName>`

`internal/mcp/launcher.go:372` 当前 `installDir := filepath.Join(n.rootDir, spec.ID)`,改成 `<server.id>-<packageName>` 形式:

```go
installDir := filepath.Join(n.rootDir,
    sanitizeForPath(spec.ID)+"-"+sanitizeForPath(pkg.Name))
```

新增 `sanitizeForPath` helper(沿用 LobsterAI 的语义,放在 `internal/mcp/sanitize.go`):

```go
// sanitizeForPath collapses any character outside [a-zA-Z0-9._-] to a
// single dash, strips leading/trailing dashes, and falls back to "mcp"
// when the result would be empty — so the segment is always a stable,
// non-empty path component compatible with all three target OSes.
// Mirrors LobsterAI's mcpLaunchResolverManager.sanitizeForPath (TypeScript).
func sanitizeForPath(value string) string {
    if value == "" {
        return "mcp"
    }
    var b strings.Builder
    lastDash := false
    for _, r := range value {
        if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
            (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
            b.WriteRune(r)
            lastDash = false
            continue
        }
        if !lastDash {
            b.WriteRune('-')
            lastDash = true
        }
    }
    s := strings.Trim(b.String(), "-")
    if s == "" {
        return "mcp"
    }
    return s
}
```

行为细节:

- `@scope/name` → `-scope-name`(去 `@` 和 `/`,连续分隔符合并成一个 `-`,首尾去掉)
- `..` / `---` → 空后 fallback `mcp`
- 中文 / 控制字符 → 全部归 `-` 后 fallback(避免跨平台编码问题)
- 与 LobsterAI 的 TS 版语义等价(规则一致;实现语言差异)

### FR-6: 残留清理 + 防复发

- 一次性:`rm -rf mcp-packages/`(仓库根目录)
- 防御性:`.gitignore` 加 `mcp-packages/` 一行(注:同 `bin/darvin-agent-*`、`*.db` 等运行时产物一并忽略 — 现有 `.gitignore` 末尾的 `.claude`、`.bak` 区域附近加)

## 4. 实现方案

### 4.1 main 端 manager.ts 改动

`src/main/runtime/manager.ts:104-112` 当前代码:

```ts
const env: NodeJS.ProcessEnv = {
  ...process.env,
  DARVIN_DEV: app.isPackaged ? '0' : '1',
};
const cfg = resolveAgentConfigPath();
if (cfg) env.DARVIN_CONFIG = cfg;
env.DARVIN_SESSIONS_DSN = agentSessionsDsnPath();
if (workspaceRoot) env.DARVIN_AGENT_WORKSPACE = workspaceRoot;
```

改为(增量加一行):

```ts
env.DARVIN_SESSIONS_DSN = agentSessionsDsnPath();
env.DARVIN_MCP_PACKAGES_DIR = getMcpPackagesDir();
if (workspaceRoot) env.DARVIN_AGENT_WORKSPACE = workspaceRoot;
```

`getMcpPackagesDir` 已在 `user-paths.ts:74`,从 `user-paths` 模块 import 即可。

### 4.2 Go 端新文件 `internal/runtime/paths.go`

新增文件,与 `agent_config.go` 同 package,职责单一:

```go
package runtime

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

// resolveMCPPackagesDir picks the absolute path for MCP npm install
// landing. Production wiring injects DARVIN_MCP_PACKAGES_DIR via the
// Electron main process (which gets it from app.getPath('userData')).
// When the env var is missing (go run / tests), it falls back to the
// OS user cache dir so dev never writes into cwd.
//
// Returns an error when the resolved path is relative (the env var
// was set to a relative path) or when neither source is available.
func resolveMCPPackagesDir() (string, error) {
    if p := strings.TrimSpace(os.Getenv("DARVIN_MCP_PACKAGES_DIR")); p != "" {
        if !filepath.IsAbs(p) {
            return "", fmt.Errorf("DARVIN_MCP_PACKAGES_DIR must be absolute, got %q", p)
        }
        return p, nil
    }
    if c, err := os.UserCacheDir(); err == nil && c != "" {
        return filepath.Join(c, "darvin-cowork", "mcp-packages"), nil
    }
    return "", fmt.Errorf("DARVIN_MCP_PACKAGES_DIR not set and os.UserCacheDir unavailable")
}
```

### 4.3 Go 端 `internal/runtime/mcp.go` 改动

`bootstrapMCP` 签名从 `(ctx, log, workspace)` 改成 `(ctx, log, mcpRoot)`:

```go
func bootstrapMCP(ctx context.Context, log *zap.Logger, mcpRoot string) (*mcp.Registry, error) {
    if !filepath.IsAbs(mcpRoot) {
        return nil, fmt.Errorf("bootstrapMCP: mcp-packages root must be absolute: %q", mcpRoot)
    }
    if err := os.MkdirAll(mcpRoot, 0o755); err != nil {
        log.Warn("mcp packages dir create failed", zap.Error(err))
    }
    resolver := mcp.NewResolverManager(mcpRoot).WithLogger(log)
    registry := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence()).WithLogger(log)
    if err := registry.LoadStaleResolutions(ctx); err != nil {
        log.Warn("mcp stale resolution scan failed", zap.Error(err))
    }
    return registry, nil
}
```

注释从 `<workspace>/mcp-packages` 改成 `MCP packages 根目录(DARVIN_MCP_PACKAGES_DIR 或 dev fallback)`。

### 4.4 Go 端 `internal/mcp/sanitize.go` 改动(新增文件)

新增文件,内容见 FR-5 helper 实现。文件名遵守仓库现有 `mcp/launcher.go` / `mcp/registry.go` 同级命名风格。

### 4.5 Go 端 `internal/mcp/launcher.go` 改动

`launcher.go:370-372` 当前:

```go
// Step 2: install into rootDir/<serverID>/ so each server has its
// own isolated dependency tree.
installDir := filepath.Join(n.rootDir, spec.ID)
```

改成:

```go
// Step 2: install into rootDir/<server.id>-<packageName>/ so every
// (server, package) pair shares a single node_modules across
// sessions, while a package swap lands in a fresh dir instead of
// overwriting the prior install. Mirrors LobsterAI's
// mcpLaunchResolverManager.<server-id>-<packageName> convention.
installDir := filepath.Join(n.rootDir,
    sanitizeForPath(spec.ID)+"-"+sanitizeForPath(pkg.Name))
```

注释里"isolated dependency tree"换成"shared across sessions per (server, package) pair",反映实际意图。

### 4.6 `internal/runtime/runtime.go` 改动

`Build(ctx, opts)` 里 `bootstrapMCP` 调用点改成:

```go
mcpRoot, err := resolveMCPPackagesDir()
if err != nil {
    return nil, fmt.Errorf("resolve mcp packages dir: %w", err)
}
mcpReg, err := bootstrapMCP(ctx, log, mcpRoot)
if err != nil {
    return nil, err
}
```

把 `mcpRoot` 错误冒泡到 `Build` 返回值,启动期硬失败(FR-4)。

### 4.7 文档同步

`src/main/libs/user-paths.ts:1-21` 顶部的注释里有目录布局 ASCII 图,补一行:

```text
└── darvin-agent/
    ├── config.yaml
    ├── sessions.db
    ├── skill-state.db
    ├── mcp.db
    ├── mcp-packages/                ← npx install 落点(从 main 注入 env 给 Go)
    │   └── <server.id>-<packageName>/  ← per (server, package),跨 session 共享
    └── skills/                      ← 用户装 skill 的根
```

`docs/系统架构.md`(若有)同步更新;若没有 spec 已 self-contained,不强制外溢。

## 5. 边界情况

| 场景 | 处理方式 |
|---|---|
| `DARVIN_MCP_PACKAGES_DIR` 未设置 | dev fallback 到 `os.UserCacheDir()/darvin-cowork/mcp-packages` |
| `DARVIN_MCP_PACKAGES_DIR` 是相对路径 | FR-2 + FR-3 双重校验,启动失败并报清晰错误 |
| `os.UserCacheDir` 不可用(Linux 上 `HOME` 未设) | FR-4 硬失败,Build 报错("…UserCacheDir unavailable"),不静默走 cwd |
| 仓库根现有 `mcp-packages/` 残留 | FR-5 一次性 `rm -rf`;`.gitignore` 兜底 |
| macOS sandbox / SIP 影响 user-data 写入 | 同 sessions.db 的现状:依赖 `app.getPath('userData')` 返回的可写路径,已有测试覆盖 |
| Windows 路径大小写敏感 | `filepath.IsAbs` 跨平台正确判断(`%LocalAppData%\darvin-cowork\mcp-packages`) |
| MCP resolver 在 dev fallback 路径下跑 | 与 production 路径同形态,resolver 接口不变,无须改动 |

## 6. 涉及文件

| 文件 | 变更说明 |
|---|---|
| `src/main/runtime/manager.ts` | 新增一行 `env.DARVIN_MCP_PACKAGES_DIR = getMcpPackagesDir()`;新增 `getMcpPackagesDir` import |
| `src/darvin-agent/internal/runtime/paths.go` | 新增 `resolveMCPPackagesDir()` |
| `src/darvin-agent/internal/runtime/mcp.go` | `bootstrapMCP` 签名改 `(ctx, log, mcpRoot)`;删 workspace 派生;加绝对路径校验 |
| `src/darvin-agent/internal/runtime/runtime.go` | `Build` 里调 `resolveMCPPackagesDir` + 错误传播到 `bootstrapMCP` |
| `src/darvin-agent/internal/mcp/sanitize.go` | 新增文件,`sanitizeForPath` helper(对齐 LobsterAI 语义) |
| `src/darvin-agent/internal/mcp/launcher.go` | `installDir` 计算从 `rootDir/<spec.ID>` 改成 `rootDir/<sanitize(spec.ID)>-<sanitize(pkg.Name)>`;注释更新 |
| `src/darvin-agent/internal/mcp/sanitize_test.go` | 新增测试覆盖各种字符(空串 / `@scope/name` / 非 ASCII / `..` / `--` / 普通名) |
| `src/darvin-agent/internal/mcp/launcher_test.go` | 新增或扩展一条断言:`installDir` 等于期望的 `<server.id>-<packageName>` 路径 |
| `src/main/libs/user-paths.ts` | 顶部 ASCII 图补 `mcp-packages` + 子目录结构(纯注释) |
| `.gitignore` | 新增 `mcp-packages/`(防御性) |
| `specs/bugfixes/mcp-packages-userdata/2026-08-07-mcp-packages-userdata-design.md` | 本 spec |
| `src/darvin-agent/internal/runtime/paths_test.go` | 新增测试覆盖 env / 相对路径 / 未设置走 fallback / fallback 不可用报硬错 |

不涉及:

- `src/main/libs/user-paths.ts:74 getMcpPackagesDir`(已存在,不动)
- `src/darvin-agent/internal/runtime/agent_config.go`(workspace 解析不变)
- `src/darvin-agent/config.yaml:39 workdir: "."`(其他用途还在用)
- `internal/mcp/` 的 resolver / launcher 公开接口 / registry(子目录命名是实现细节)

## 7. 验收标准

- [ ] `cd src/darvin-agent && go test ./internal/runtime/...` 通过;新增 `paths_test.go` 覆盖:`getenv` 设置 / 相对路径 / 未设置走 fallback / fallback 不可用报硬错
- [ ] `cd src/darvin-agent && go test ./internal/mcp/...` 通过;新增 `sanitize_test.go` 覆盖空串 / `@scope/name` / 非 ASCII / `..` / `--` / 普通名 / 数字,以及 launcher installDir 命名断言
- [ ] `cd src/darvin-agent && go test ./...` 全过
- [ ] `go build ./...` 通过,`go vet ./...` 干净
- [ ] `npm run lint` 通过
- [ ] `npm run test` 通过
- [ ] 手动:删除仓库根现存 `mcp-packages/`,`git status` 不再标 untracked
- [ ] 手动:`npm start` 装一个 npx 类 MCP server,确认 package 落到 `<userData>/darvin-agent/mcp-packages/<server.id>-<packageName>/`,仓库根不出现新目录
- [ ] 手动:`cd src/darvin-agent && go run ./cmd/app`,启动日志里 `mcp packages dir` 对应路径是 `$XDG_CACHE_HOME/darvin-cowork/mcp-packages`(或 OS 等价),绝不在 cwd 下
- [ ] 手动:把 `DARVIN_MCP_PACKAGES_DIR=relative/path` 注入后启动,启动失败并报"must be absolute",不静默走 cwd
- [ ] 手动:session A 注册 server `github-mcp` 装 `@modelcontextprotocol/server-github` 后,session B 用同一 server config 启动,日志里 installDir 与 A 命中同一目录(`<mcpRoot>/github-mcp-@modelcontextprotocol-server-github/`),无新 `npm install`