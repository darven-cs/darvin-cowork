# Provider: OpenAI Chat Completions 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

OpenAI 的 `POST /v1/chat/completions` 是 darvin-cowork 用户最常见的 Provider。区别于：
- Anthropic Messages
- OpenAI Responses（独立 spec）
- Azure OpenAI（独立 spec）

本文锁定 OpenAI 公有云 `api.openai.com` 的 Chat Completions 协议。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 鉴权：`Authorization: Bearer <key>` | header 注入 |
| G2 | request / response 字段映射 | `darvin-api.ts` 与 Go struct 对齐 |
| G3 | SSE 流事件：`data: {...}\n\n` + `[DONE]` | stream parser |
| G4 | tool_use 通过 `tools` array；`tool_choice` | Tool registry 集成 |
| G5 | usage：`prompt_tokens / completion_tokens / total_tokens / cached_tokens` | usage extractor |
| G6 | 错误归一化（401 / 429 / 5xx） | `errors.go` |
| G7 | `extra_headers` 透传（`OpenAI-Organization` 等） | config |
| G8 | 至少 10 个 wire/单元场景 | tests |

### 1.3 非目标

- 不实现 OpenAI Responses（独立 spec）。
- 不实现 Azure OpenAI 部署级 URL（独立 spec）。
- 不实现微调 / file / image edit 等其他 OpenAI 端点。
- 不实现微调 + TTS / DALL-E / whisper（独立 spec）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/provider/registry.go` | 占位接口 |
| `src/shared/darvin-api.ts` | `DarvinApi` 已含 `agent.request(method, params)` |
| `./P2-2026-08-04-provider-registry-design.md` | 给出 `Provider` 接口 |

## 3. 用户/系统场景

### 场景 1：普通对话

**Given** OpenAIProvider 配置正确
**When** `Chat(req)` 发送 `gpt-4o` Chat Completions
**Then** 收到 `chat.completion` 响应；`usage` 完整

### 场景 2：流式

**Given** `stream=true`
**When** `Stream(req)` 异步处理 SSE
**Then** 每个 `data:` 行触发 `onDelta(deltaBytes)`；`[DONE]` 触发 `onFinish`

### 场景 3：tool_use

**Given** `req.Tools` 非空
**When** Chat Completions 返回 `finish_reason="tool_calls"`
**Then** 把 `tool_calls[]` 转 `ToolResponse`，由 Registry dispatch

### 场景 4：限流

**Given** 429 + `Retry-After: 2`
**When** provider 收到错误
**Then** 返回 `ErrProviderRateLimited` 含 retry-after 秒数，由 Failover 接管

## 4. 功能需求

### FR-1 鉴权

```go
req.Header.Set("Authorization", "Bearer "+apiKey)
```

`apiKey` 来自 `ProviderConfig.APIKey`（AES-GCM 解密）。

### FR-2 Chat Request 字段映射

| darvin 字段 | OpenAI 字段 |
|---|---|
| `model` | `model` |
| `messages` | `messages`（role / content / name 等同） |
| `temperature` | `temperature` |
| `top_p` | `top_p` |
| `max_tokens` | `max_tokens` |
| `tools` | `tools`（仅 OpenAI 标准 `function`） |
| `tool_choice` | `tool_choice` |
| `stream` | `stream` |
| `extra_headers` | 透传 |
| `user` | `user`（用于日志归因） |

### FR-3 Chat Response 解析

`/v1/chat/completions` 响应字段：

```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "model": "gpt-4o-2024-08-06",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "...",
      "tool_calls": [...]
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 123,
    "completion_tokens": 45,
    "total_tokens": 168,
    "prompt_tokens_details": { "cached_tokens": 12 }
  }
}
```

→ 映射为 `ChatResponse { ID, Choices[], Usage }`。

### FR-4 SSE Stream

每行 `data: {...}` 是 JSON；末尾 `data: [DONE]`。`tool_calls` 通过 `delta.tool_calls[i]` 增量拼装。

### FR-5 Usage 提取

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

无 `cached_tokens` 字段时记 0。

### FR-6 错误归一化

| HTTP | 处理 |
|---|---|
| 400 | `ErrProviderBadRequest` |
| 401 | `ErrProviderUnauthorized` |
| 403 | `ErrProviderForbidden` |
| 404 | `ErrProviderModelNotFound` |
| 429 | `ErrProviderRateLimited` + `RetryAfter` |
| 5xx | `ErrProviderServer` |
| 网络层 | `ErrProviderNetwork` |

### FR-7 extra_headers

`OpenAI-Organization`、`OpenAI-Project` 等通过 settings.json 配置，注入到 header。

### FR-8 测试场景（≥ 10）

| # | 场景 |
|---|---|
| T1 | 普通 Chat 200 响应正确解析 |
| T2 | 流式 SSE 拼接正确 |
| T3 | 401 -> `ErrProviderUnauthorized` |
| T4 | 429 含 Retry-After -> `ErrProviderRateLimited` |
| T5 | tool_use finish_reason=tool_calls 解析 |
| T6 | tool_choice=auto / required |
| T7 | usage 含 cached_tokens |
| T8 | 空 content + finish_reason=length |
| T9 | extra_headers 注入 |
| T10 | 网络层 timeout |
| T11 | 一万 token 长 prompt 不 OOM |

## 5. 安全与隐私

- `apiKey` 不进任何日志 / 错误响应。
- tool_calls 中 `function.arguments` 可能是敏感 JSON，按 `_audit_only` 模式记录（默认不记）。
- `user` 字段使用 `session_id` hash，不暴露 user email。
- SSE 流断开时不打印截断的 delta 内容（可能含秘密）。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 流中 network error | `ErrProviderStreamTruncated`；Failover 重试 |
| 500 | 同上 |
| model 不存在 | `ErrProviderModelNotFound`，UI 提示 |
| content filter | `ErrProviderContentRefused`，UI 给替代提示 |
| tool schema 校验失败 | fail-fast 在调用前 |
| SSE `[DONE]` 缺失 | 视为 stream truncated |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/openai/openai.go`（新） | 主实现 |
| `src/darvin-agent/internal/provider/openai/mapping.go`（新） | request / response |
| `src/darvin-agent/internal/provider/openai/sse.go`（新） | SSE parser |
| `src/darvin-agent/internal/provider/openai/errors.go`（新） | 错误归一化 |
| `src/darvin-agent/internal/provider/openai/openai_test.go`（新） | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. `mapping.go` + `errors.go` + 5 单元测试。
2. `sse.go` + 5 wire 测试（含 httptest）。
3. 主 `openai.go` 串联。
4. 接入 `factory.go`。

> 前置：`specs/features/provider-registry/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单元测试 ≥ 10 条 |
| V3 | `go vet ./...` 通过 |
| V4 | `npm run smoke -- provider-openai` 通过 |
| V5 | 手工 `npm run dev`：在设置页打开 OpenAI Provider，输入 mock key `darvin-mock-*`，发一条消息不报错 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- OpenAI Responses（独立 spec）。
- Azure OpenAI（独立 spec）。
- OpenAI 自家以外的 OpenAI 兼容端点（如 LM Studio）暂不支持。
