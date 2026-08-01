# Artifact Panel 重做

> 编号 **05**。把当前空态 3 tab 改成真实 artifact 面板：状态机 + 10 种渲染器 + iframe sandbox。**依赖 00-darvin-api-extension**。

## 1. 背景

`src/renderer/components/side-panel/SidePanelContent.vue` 的 tools / thinking / artifact 三个 tab 全部是空态占位。LobsterAI 的 `ArtifactPanel.tsx` + `artifactSlice.ts` 是一套完整状态机：artifactsBySession / previewTabsBySession / panelOpenBySession / panelWidth，10 种 artifact 类型各有专门渲染器。

注：darvin-cowork 的 AGENTS.md 提到 `services/artifact-renderer/` 目录，但目前是空目录（已被本调研验证）。

## 2. 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 协议：`artifact` 事件 + `DarvinArtifact` 类型 | 00 spec 落地 |
| G2 | `artifactSlice`（或 darvin 风格的 composable `useArtifacts`）状态机 | artifactsBySession / previewTabsBySession / activePreviewTabId / panelWidth |
| G3 | 3 个特殊 tab：`fileList` / `browser` / `subagents` | 替代现有空 tab |
| G4 | 10 种 artifact 渲染器：html / svg / image / video / mermaid / code / markdown / text / document / local-service | 全部 sandbox 隔离 |
| G5 | Inline HTML：`sandbox="allow-scripts"`（不加 allow-same-origin） | `HtmlRenderer` 复用 AGENTS.md 规则 |
| G6 | File-based HTML：走 `darvin.artifact.createPreviewSession` 本地服务（main 端启动） | 主进程开端口 |
| G7 | Mermaid：`securityLevel: 'strict'` | 严防 XSS |
| G8 | Code：基于 Shiki 渲染，**不引入 CodeMirror** | 与 01 spec 复用 Shiki 集成 |
| G9 | 面板宽度可拖拽（180-1000px） | `panelWidth` state + 拖拽 handle |

## 3. 非目标

- 不做 HTML 分享到公网（仅有本地预览）
- 不做 artifact 持久化（仅 session 内）
- 不做 artifact 版本管理
- 不实现 office 文档（docx/xlsx/pptx）渲染（darvin-cowork 范围外）

## 4. 设计要点

### 4.1 状态机（Vue composable 版本）

```ts
// src/renderer/composables/useArtifacts.ts
interface ArtifactState {
  artifactsBySession: Record<string, Artifact[]>;
  previewTabsBySession: Record<string, ArtifactPreviewTab[]>;
  activePreviewTabIdBySession: Record<string, string | null>;
  isPanelOpenBySession: Record<string, boolean>;
  panelWidth: number;  // 180-1000
}

interface ArtifactPreviewTab {
  id: string;
  artifactId: string;
  contentView: 'preview' | 'code';
}
```

### 4.2 渲染器路由

```vue
<!-- ArtifactRenderer.vue -->
<template>
  <component :is="rendererFor(kind)" v-bind="rendererProps" />
</template>

<script setup>
import HtmlRenderer from './renderers/HtmlRenderer.vue';
import SvgRenderer from './renderers/SvgRenderer.vue';
import ImageRenderer from './renderers/ImageRenderer.vue';
import VideoRenderer from './renderers/VideoRenderer.vue';
import MermaidRenderer from './renderers/MermaidRenderer.vue';
import CodeRenderer from './renderers/CodeRenderer.vue';
import MarkdownRenderer from './renderers/MarkdownRenderer.vue';
import TextRenderer from './renderers/TextRenderer.vue';
import DocumentRenderer from './renderers/DocumentRenderer.vue';
import LocalServiceRenderer from './renderers/LocalServiceRenderer.vue';

const props = defineProps<{ artifact: Artifact }>();

const rendererFor = (kind: ArtifactKind) => {
  switch (kind) {
    case 'html': return HtmlRenderer;
    case 'svg': return SvgRenderer;
    // ...
  }
};
</script>
```

### 4.3 iframe sandbox 策略

| artifact | sandbox | src | 备注 |
|---|---|---|---|
| `html` (inline) | `allow-scripts` | `srcdoc={content}` | 不加 same-origin |
| `html` (file) | `allow-scripts` | `previewUrl` (本地服务) | 走 `createPreviewSession` |
| `svg` | `allow-scripts` | `srcdoc={sanitizedContent}` | DOMPurify 净化 |
| `mermaid` | n/a（offscreen DOM） | — | 提取 SVG 后渲染 |

### 4.4 Artifact 事件处理

```ts
// useMessages.appendEvent
case 'artifact': {
  const { sessionId, artifactId, kind, name, content } = ev;
  const artifact: Artifact = { id: artifactId, kind, name, content, createdAt: ev.createdAt };
  artifacts.addArtifact(sessionId, artifact);
  artifacts.openPreviewTab(sessionId, artifactId);
  // 关联到最近的 turn
  break;
}
```

## 5. 用户场景

### 场景 1：agent 产出 HTML artifact

**Given** LLM 输出 `<html>...</html>` 触发 `artifact { kind: 'html', content: '...' }` 事件

**When** renderer 收到

**Then** artifact tab 自动激活；`HtmlRenderer` 渲染到 sandboxed iframe；面板自动展开

### 场景 2：Mermaid 图表

**Given** LLM 输出 ` ```mermaid\ngraph TD; A-->B\n``` `

**When** agent 解析为 `artifact { kind: 'mermaid' }` 事件

**Then** `MermaidRenderer` 渲染 SVG；`securityLevel: 'strict'` 防 XSS

### 场景 3：本地服务 artifact

**Given** LLM 写一个静态网站到 workspace

**When** agent 推 `artifact { kind: 'local-service', content: 'http://localhost:3000' }`

**Then** 面板显示 URL + 「在新窗口打开」按钮；iframe 内嵌预览

## 6. 验收

- [ ] 10 种 artifact 类型全部有渲染器
- [ ] iframe sandbox 配置正确（无 allow-same-origin 给 inline）
- [ ] Mermaid 启用 strict 模式
- [ ] 面板宽度 180-1000px 拖拽流畅
- [ ] artifact 与 session 绑定，切换 session 时 tab 隔离
- [ ] Mermaid 渲染时 DOMPurify 净化
- [ ] 事件流：artifact 事件 → tab 激活 → 渲染，1s 内可见

## 7. 依赖

- **前置**：00-darvin-api-extension
- **可并行**：01 / 02 / 03 / 04 / 06 / 07 / 08

## 8. 参考

### darvin-cowork
- `src/renderer/components/side-panel/SidePanelContent.vue` — 现状（要被替换）
- `src/renderer/components/side-panel/SidePanel.vue` — 容器
- `src/renderer/composables/useSidePanel.ts` — `open` ref
- AGENTS.md § 「Artifact 渲染器」约束

### LobsterAI（借鉴）
- `src/renderer/components/artifacts/ArtifactPanel.tsx`（258KB，看 tab 切分即可）
- `src/renderer/store/slices/artifactSlice.ts:46-54` — 状态机
- `src/renderer/services/artifactParser.ts` — 解析
- `src/renderer/components/artifacts/ArtifactRenderer.tsx:22-54` — 路由
- `src/renderer/components/artifacts/renderers/HtmlRenderer.tsx:112-131` — sandbox
- `src/renderer/components/artifacts/renderers/MermaidRenderer.tsx` — strict
- `src/renderer/components/artifacts/renderers/CodeRenderer.tsx` — 语言映射
- `src/shared/artifactPreview/constants.ts` — IPC channel 常量

## 9. 关联调研

`specs/features/agent-output-ux-research/2026-08-01-cowork-vs-lobsterai-comparison.md` § 2.6「右侧工具面板 / Artifact Panel」
