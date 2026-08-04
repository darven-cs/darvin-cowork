# Web Browser Tool 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化 agent 需要「自己访问网页」能力：抓取 / 截图 / 填表。darvin-cowork 当前没有 browser tool。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Playwright/Chromium 进程隔离（独立 profile） | isolation |
| G2 | 下载文件落 workspace | download |
| G3 | SSRF / 本地文件访问边界 | boundaries |
| G4 | 截图 / PDF 导出 | render |
| G5 | 用户可视化 take-over | hand-off |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做完全浏览器渲染（仅 agent 自动化）。
- 不支持 WebGL 3D（Chromium 自带）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/artifact-sandbox-iframe/` | iframe 沙箱（不同域） |
| `specs/features/runtime-supervision/` | 进程托管 |
| `src/main/runtime/` | 占位 |

## 3. 用户/系统场景

### 场景 1：抓取

**Given** model 调用 `browser.fetch(url)`
**When** Playwright 启动 Chromium
**Then** 渲染 → 提取 markdown

### 场景 2：截图

**Given** model 调用 `browser.screenshot(url)`
**When** Playwright 截图
**Then** 存 workspace，发送引用

### 场景 3：take-over

**Given** 遇到验证码或登录页
**When** model 调用 `browser.handoff(url)`
**Then** UI 弹窗让用户接管

### 场景 4：本地文件禁止

**Given** 模型想访问 `file:///etc/passwd`
**When** browser tool
**Then** `ErrAccessDenied`

## 4. 功能需求

### FR-1 进程隔离

```go
type Browser struct {
    cmd *exec.Cmd  // playwright-chromium
    profileDir string
    wsURL string
}
```

profile 落在 user workspace；不共享主 OS 浏览。

### FR-2 工具接口

```go
type BrowserTool interface {
    Fetch(ctx context.Context, url string) (*BrowserContent, error)
    Screenshot(ctx context.Context, url string) ([]byte, error)
    Download(ctx context.Context, url string, dst string) error
    Handoff(ctx context.Context, url string) error
}
```

### FR-3 URL 过滤

```go
var blockedHosts = []string{
    "127.0.0.1", "localhost",
    "169.254.169.254", // metadata
    // ...
}
```

`file://` / `chrome://` / `devtools://` 拒绝。

### FR-4 SSRF 防护

```go
func resolveAndCheck(host string) error
```

禁止私网（10.0.0.0/8、192.168.0.0/16、127.0.0.0/8、169.254.0.0/16）；

### FR-5 下载

```go
dst := filepath.Join(workspaceDir, "downloads", uuid)
```

not allow executable（.exe / .sh / .bat）落地。

### FR-6 take-over

```ts
useBrowserHandoff()  // UI 弹窗
```

参数：URL / 截图 / reason。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | Fetch URL |
| T2 | Fetch 私网 |
| T3 | Fetch file:// |
| T4 | Screenshot |
| T5 | Download .txt |
| T6 | Download .exe 拒绝 |
| T7 | Handoff UI |
| T8 | Profile 隔离 |
| T9 | 并发 fetch |
| T10 | Timeout |
| T11 | 内存超限 |

## 5. 安全与隐私

- 浏览历史不进任何持久化。
- Profile dir 0600。
- take-over UI 显示全过程警告。
- 拿 token / cookie 拒绝入 mcp 工具集。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Chromium 启动失败 | fallback CDP |
| 私网 / metadata | 拒绝 |
| profile 损坏 | rebuild |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/browser/playwright.go`（新） | 主 client |
| `policy.go`（新） | URL filter |
| `download.go`（新） | 下载 |
| `tools.go`（新） | tool 注册 |
| `src/shared/darvin-api.ts` | mcp/browser tool type |
| `src/renderer/composables/useBrowserHandoff.ts`（新） | UI |

## 8. 实施顺序与依赖

1. `policy.go` + 单测
2. `playwright.go`
3. `download.go` + `tools.go`
4. UI

> 前置：`runtime-supervision`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- web-browser-tool` |
| V5 | dev 手工：mock fetch |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 完整浏览器 UI（v2）。
- WebRTC / WebTransport（v2）。
