# Artifact Sandbox iframe 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

`AGENTS.md` 已规定 AI 产物（HTML / SVG / Mermaid / React）必须 sandbox 渲染。`specs/features/artifact-panel/v1` 已确立 10 种 artifact 渲染器形态。本 spec 是商业化迭代：固化 sandbox 安全属性、CSP、postMessage schema、URI 限制、XSS corpus。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `sandbox="allow-scripts"` 默认；**不**启用 `allow-same-origin` | iframe attribute |
| G2 | CSP `default-src 'none'` 起跳 | 显式 header |
| G3 | URI 限制：仅 `data:` / `blob:` / `srcdoc` + 紫白名单 `https://api.darvin-cowork.local` | CSP `frame-src` |
| G4 | postMessage schema & origin 校验 | handlers |
| G5 | XSS corpus 单元测试（≥ 20 例） | corpus |
| G6 | iframe 生命周期与主页面解耦 | lifecycle |
| G7 | ≥ 10 主测试场景 | tests |

### 1.3 非目标

- 不实现 office 文档（docx/xlsx/pptx）渲染。
- 不做 HTML 分享到公网（仅本地预览）。
- 不支持 file:// 协议（仅 `data:` / `blob:` / `srcdoc` / API 白名单）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/artifact-panel/2026-08-01-...` | 渲染器形态 |
| `specs/features/artifact-panel-ux/2026-08-02-...` | UX |
| `src/renderer/services/artifact-renderer/` | 占位目录（推断） |
| `src/renderer/services/artifact-renderer/sandbox.html` | 推断可存在 |

## 3. 用户/系统场景

### 场景 1：HTML 渲染

**Given** AI 返回 `<script>alert(1)</script>`
**When** sandbox iframe 加载
**Then** script 因 CSP `script-src 'self'` 不执行 → UI 仅渲染文本

### 场景 2：Mermaid

**Given** AI 返回 mermaid graph `%%script\n...`
**When** 渲染
**Then** Mermaid `securityLevel: 'strict'` 起跳，inline script 拒绝

### 场景 3：postMessage

**Given** artifact 想跟主页面交互（如下载）
**When** artifact 触发 `window.parent.postMessage({ type: 'download', url: '...' }, '*')`
**Then** 主页面校验 `event.origin` 与 schema；schema 不符丢弃

### 场景 4：跨域脚本

**Given** artifact HTML 含 `<script src="https://evil.com/x.js">`
**When** 渲染
**Then** CSP `script-src 'self'` 阻止

## 4. 功能需求

### FR-1 sandbox attribute

```html
<iframe sandbox="allow-scripts" srcdoc="..."></iframe>
```

> `allow-same-origin` 默认 false。仅 DOM API 必需时（如 `document.title`）按需开 `allow-same-origin` + 同源检测。

### FR-2 CSP

```http
Content-Security-Policy:
  default-src 'none';
  script-src 'unsafe-inline' 'self';
  style-src 'unsafe-inline' 'self';
  img-src data: blob: https://api.darvin-cowork.local;
  font-src data:;
  connect-src 'none';
  frame-ancestors 'self';
  base-uri 'none';
  form-action 'none';
```

### FR-3 URI 白名单

`frame-src` / `img-src` / `media-src` 允许：

- `data:`
- `blob:`
- `srcdoc`（隐含）
- `https://api.darvin-cowork.local`（自定义 API）

### FR-4 postMessage schema

```ts
interface ArtifactPostMessage {
  type: 'ready' | 'resize' | 'download' | 'navigate' | 'tool-call';
  artifactId: string;
  payload: unknown;
  nonce: string; // sha256(sessionId + artifactId + ts) 由主页面校验
}
```

主页面 handler 校验：

- `event.origin === 'null'`（iframe sandbox 是 null origin，**但仍只接受 null**）
- `event.source === iframe.contentWindow`
- `nonce` 命中
- schema 验证

### FR-5 XSS corpus

`src/renderer/services/artifact-renderer/__xss__/` 含固定 corpus：

```js
const CORPUS = [
  '<script>alert(1)</script>',
  '<img src=x onerror=alert(1)>',
  '<svg onload=alert(1)>',
  'javascript:alert(1)',
  '<iframe srcdoc="...">',
  // ... ≥ 20 例
];
```

每个用例：

1. render 到 sandbox
2. 等待 1s
3. 检查未触发 `alert`
4. 检查 sandbox DOM 不含 `unsafe-eval` 调用痕迹

### FR-6 iframe 生命周期

| event | 行为 |
|---|---|
| artifact create | 新建 sandboxed iframe，srcdoc 注入 |
| artifact update | `srcdoc` 替换；前状态保存可回滚 |
| artifact destroy | `iframe.remove()`；postMessage handler 解绑 |

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | sandbox attribute 不含 allow-same-origin |
| T2 | CSP header 含 default-src 'none' |
| T3 | script-src 'unsafe-inline' 缺省被拒 |
| T4 | postMessage schema 校验失败丢弃 |
| T5 | postMessage origin = null 通过 |
| T6 | XSS corpus 全通过 |
| T7 | 数据 URL 加载 svg 通过 |
| T8 | 跨域脚本不加载 |
| T9 | iframe resize 更新主页面容器 |
| T10 | download 请求 schema 校验 |
| T11 | 销毁清理 handler |

## 5. 安全与隐私

- iframe 不共享 localStorage / sessionStorage。
- iframe `name` 不含敏感信息。
- srcdoc 渲染前 XSS filter 一遍（粗筛）。
- download 走主进程 `darvin.artifact.persist`，不直接 `<a href>` 触发浏览器下载。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| iframe 拒绝执行某 API | render 失败并显示错误占位 |
| postMessage flood | main process 限速 |
| sandbox HTML 极大 | srcdoc size > 1MB 拒绝 |
| CSP 不生效（renderer 旧版） | 单元测试显式检查 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/renderer/services/artifact-renderer/sandbox.html` | iframe 模板 |
| `src/renderer/services/artifact-renderer/csp.ts` | CSP 注入 |
| `src/renderer/services/artifact-renderer/postmessage.ts` | handlers |
| `src/renderer/services/artifact-renderer/__xss__/corpus.ts` | XSS corpus |
| `src/renderer/services/artifact-renderer/xss.test.ts` | ≥ 20 corpus 测 |
| `src/renderer/services/artifact-renderer/sandbox.test.ts` | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. `sandbox.html` + `csp.ts`
2. `postmessage.ts` + 校验
3. `__xss__/corpus.ts` + `xss.test.ts`
4. `sandbox.test.ts`

> 前置：`artifact-panel/v1` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | TS 单测 ≥ 10 + XSS corpus ≥ 20 |
| V3 | `npm run smoke -- artifact-sandbox-iframe` |
| V4 | dev 手工：恶意 HTML 注入无害 |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 完整的 Artifact 视觉 / 渲染组件（沿用 v1）。
- 跨窗口共享 artifact（v2）。
