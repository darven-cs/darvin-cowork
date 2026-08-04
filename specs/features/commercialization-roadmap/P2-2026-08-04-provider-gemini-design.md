# Provider: Gemini 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

Google Gemini 提供 `POST /v1beta/models/{model}:generateContent` 与 `:streamGenerateContent`，使用 query string `?key=API_KEY` 或 OAuth `Authorization: Bearer ACCESS_TOKEN`（Vertex AI）。Gemini 协议差异：

- `contents[].parts[].text` 而非 messages
- `systemInstruction.parts[].text`
- `tools[].functionDeclarations`
- `tool.functionCall` 输出
- `usageMetadata`: promptTokenCount / candidatesTokenCount / totalTokenCount / cachedContentTokenCount

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 鉴权：API_KEY (query) 或 Bearer | config |
| G2 | 请求 / 响应映射到 darvin | mapping |
| G3 | SSE 流（`streamGenerateContent`） | sse parser |
| G4 | tool use via `functionDeclarations` | tools |
| G5 | usage 含 cachedContentTokenCount | breakdown |
| G6 | extra_headers | config |
| G7 | ≥ 10 测试场景 | |

### 1.3 非目标

- 不实现 Imagen / Veo（独立 spec / media-generation）。
- 不实现 Gemini File API / 长上下文 cache（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/provider/openai/` | 协议参考 |
| `specs/features/provider-vertex/` | Vertex 复用于本文映射 |

## 3. 用户/系统场景

### 场景 1：generateContent

**Given** GeminiProvider 配置正确
**When** Chat 请求
**Then** 收到 200 JSON；`usageMetadata` 含 token 统计

### 场景 2：stream

**Given** stream=true
**When** 调用 `:streamGenerateContent`
**Then** SSE event 拆分 → onDelta

### 场景 3：tool_use

**Given** req 配置 tools（OpenAI 风格）
**When** 转换为 `functionDeclarations`
**Then** 响应中 `functionCall` 解析回 OpenAI tool_calls 形态

## 4. 功能需求

### FR-1 协议路径

`POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={apiKey}`

或 `streamGenerateContent`。

### FR-2 鉴权

| 模式 | 签名 |
|---|---|
| `api_key` | `?key=<key>` |
| `bearer` | `Authorization: Bearer <token>` |

### FR-3 字段映射

| darvin | gemini |
|---|---|
| `model` | URL path |
| `messages[]` | `contents[]` |
| `system` | `systemInstruction.parts[].text` |
| `temperature` | `generationConfig.temperature` |
| `top_p` | `generationConfig.topP` |
| `max_tokens` | `generationConfig.maxOutputTokens` |
| `stream` | URL 选 streamGenerateContent |
| `tools[]` | `tools[].functionDeclarations` |
| `tool_choice` | `tool_config` |
| `user` | `safetySettings` / `X-Goog-User` header |

### FR-4 Response

```json
{
  "candidates": [
    {
      "content": { "role": "model", "parts": [{ "text": "..." }] },
      "finishReason": "STOP"
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 12,
    "candidatesTokenCount": 5,
    "totalTokenCount": 17,
    "cachedContentTokenCount": 3
  }
}
```

`functionCall` 出现在 `candidates[].content.parts[]` 内。

### FR-5 Usage

```go
type UsageBreakdown struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CachedTokens     int
    Model            string
    Provider         string
    SessionID        string
}
```

`CachedTokens = usageMetadata.cachedContentTokenCount`。

### FR-6 安全设置

`safetySettings[]` 表达 harrasment / hate / sex / dangerous 阈值，默认 `BLOCK_NONE`。

### FR-7 错误归一化

| HTTP | 处理 |
|---|---|
| 400 | `ErrProviderBadRequest` |
| 401 / 403 | `ErrProviderUnauthorized` |
| 404 | `ErrProviderModelNotFound` |
| 429 | `ErrProviderRateLimited` + retry-after |
| 500 / 503 | `ErrProviderServer` / `ErrProviderOverloaded` |
| SAFETY block | `ErrProviderContentRefused` |

### FR-8 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | 普通 generateContent |
| T2 | stream 流事件解析 |
| T3 | API_KEY 鉴权 |
| T4 | Bearer 鉴权 |
| T5 | tools 转换 |
| T6 | tool_choice=any |
| T7 | usage cached_token |
| T8 | safety block 归一 |
| T9 | 401 |
| T10 | 429 |
| T11 | extra_headers |

## 5. 安全与隐私

- `apiKey` 仅出现在 URL query；TLS 必填。
- 多模态 parts（inline_data）可能含用户上传文件，按 PII 脱敏策略。
- `contents[]` history 不存明文敏感对话。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| SAFETY 拦截 | UI 提示用户 |
| 输出截断 (MAX_TOKENS) | `finishReason=MAX_TOKENS` |
| network 中断 | Failover |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/gemini/gemini.go` | 主实现 |
| `mapping.go` | request / response |
| `sse.go` | 流事件 |
| `errors.go` | 错误归一 |
| `safety.go` | safety settings 注入 |

## 8. 实施顺序与依赖

1. mapping + safety
2. errors + sse
3. 主串联

> 前置：`specs/features/provider-registry/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- provider-gemini` |
| V5 | dev 手工验证 mock 路径 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Vertex AI（独立 spec，由 provider-vertex 复用本文 mapping）。
- Imagen / Veo（独立 spec / media-generation）。
- File API（v2）。
