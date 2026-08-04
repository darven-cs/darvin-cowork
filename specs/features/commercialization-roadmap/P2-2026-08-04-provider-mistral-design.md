# Provider: Mistral 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

Mistral 提供 OpenAI 兼容 Chat Completions 端点 `https://api.mistral.ai/v1/chat/completions`，但有以下差异：

- 鉴权：`Authorization: Bearer <key>` + 可选 `User-Agent`
- 部分模型支持 `safe_prompt`
- 不支持 `cached_tokens`
- 部分模型（`codestral-*`）支持 `tool_choice="any"`

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 同 OpenAI Chat 协议，但映射 Mistral 字段差异 | mapping |
| G2 | `safe_prompt` 默认 false，可开关 | config |
| G3 | `tool_choice` 支持 `any` | tests |
| G4 | 错误归一化（含 Mistral 429 / 503 特殊语义） | errors.go |
| G5 | 至少 10 测试场景 | |

### 1.3 非目标

- 不实现 Mistral Embeddings（独立 spec / memory）。
- 不实现 Mistral Agents / Functions 等尚未稳定的 API。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-openai/...` | 已规范 OpenAI Chat 端点，可借鉴 |
| `src/darvin-agent/internal/provider/registry.go` | 占位接口 |

## 3. 用户/系统场景

### 场景 1：默认 Chat

**Given** 配置 Mistral apiKey + model=`mistral-large-latest`
**When** `Chat(req)`
**Then** 收到 200；usage 含 prompt/completion/total

### 场景 2：safe_prompt

**Given** 设置页勾选 `safe_prompt=true`
**When** Chat Completions 请求附 `safe_prompt: true`
**Then** 模型按 Mistral 系统提示调整回答

### 场景 3：tool_choice any

**Given** req 配置 `tool_choice="any"`
**When** Chat 请求
**Then** 验证请求 JSON 含此字段；响应解析 OK

## 4. 功能需求

### FR-1 协议路径

`POST {baseUrl}/v1/chat/completions`（默认 `https://api.mistral.ai`）

### FR-2 字段差异

| darvin | mistral | 备注 |
|---|---|---|
| `temperature` | `temperature` | 同 OpenAI |
| `top_p` | `top_p` | 同 OpenAI |
| `max_tokens` | `max_tokens` | 同 OpenAI |
| `tools` | `tools` | 同 OpenAI |
| `tool_choice` | `tool_choice` | 支持 `none/auto/any/{type:function,name}` |
| `safe_prompt` | `safe_prompt` | Mistral 专用 |
| `stream` | `stream` | 同 OpenAI |
| `random_seed` | `random_seed` | Mistral 专用（可选） |

### FR-3 鉴权

`Authorization: Bearer <key>` + 默认 `User-Agent: darvin-cowork/<version>`。

### FR-4 Response

与 OpenAI 同 schema；缺失字段：

- 无 `cached_tokens`、`prompt_tokens_details`
- `choices[].finish_reason` 仅 `stop / length / tool_calls / model_error`

### FR-5 Usage

```go
type UsageBreakdown struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CachedTokens     int // 始终 0
    Model            string
    Provider         string // "mistral"
    SessionID        string
}
```

### FR-6 错误归一化

| HTTP | 处理 |
|---|---|
| 400 | `ErrProviderBadRequest` |
| 401 | `ErrProviderUnauthorized` |
| 403 | `ErrProviderForbidden` |
| 404 | `ErrProviderModelNotFound` |
| 429 | `ErrProviderRateLimited` + `RetryAfter` |
| 503 | `ErrProviderOverloaded`（与一般 5xx 区分，触发 Failover） |

### FR-7 至少 10 测试场景

| # | 场景 |
|---|---|
| T1 | 普通 Chat 解析 |
| T2 | SSE stream |
| T3 | safe_prompt=true 注入 |
| T4 | safe_prompt=false 不出现 |
| T5 | tool_choice=any |
| T6 | tool_choice=auto |
| T7 | tool_choice={type:function,name:foo} |
| T8 | 401 |
| T9 | 429 + Retry-After |
| T10 | 503 model_error |
| T11 | usage 无 cached_tokens 走 0 |

## 5. 安全与隐私

- `apiKey` 与 OpenAI 一致。
- Mistral 在 EU 区域部署，跨境请求应在 settings.json 显式 `baseUrl` 防回美。
- `safe_prompt` 开关变更需要用户明确点击，不自动设置。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 503 | Failover 接管 |
| 401 | 提示用户重新输入 key |
| 复杂 tool_calls schema | 与 OpenAI 统一校验 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/mistral/mistral.go` | 主实现 |
| `src/darvin-agent/internal/provider/mistral/mapping.go` | 字段差异 |
| `src/darvin-agent/internal/provider/mistral/errors.go` | 错误归一 |
| `src/darvin-agent/internal/provider/mistral/sse.go` | stream parser（可复用 OpenAI 版） |
| tests | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. 复用 OpenAI 的 SSE parser 与 mapping（继承抽象）。
2. 增量添加 Mistral 字段。
3. 错误码映射。

> 前置：`specs/features/provider-openai/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` 通过 |
| V4 | `npm run smoke -- provider-mistral` |
| V5 | 手工 dev：mock key 验证协议路径正确 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Mistral Embeddings（独立 spec）。
- Mistral Agents API（v2 候补）。
