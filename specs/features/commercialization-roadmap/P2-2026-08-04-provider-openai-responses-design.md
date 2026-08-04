# Provider: OpenAI Responses 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

OpenAI Responses 是 2025+ 推出的新一代 stateful 对话 API：

- 端点 `POST /v1/responses`
- 引入 `previous_response_id`，服务端保存对话状态
- `tools` / `tool_choice` 同 Chat Completions
- 支持 `instructions`（system prompt）
- 流事件为 `response.created / response.in_progress / response.output_text.delta / response.completed`

darvin-cowork 必须在 Chat Completions 之外支持 Responses，作为 advanced user option。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 鉴权同 Chat：Bearer | header |
| G2 | stateful session：`previous_response_id` | 缓存 + 投递 |
| G3 | 流事件 → onDelta 拼装 | SSE parser |
| G4 | tool_use 通过 `tools` | Tool registry |
| G5 | usage：含 cached_tokens | breakdown |
| G6 | 错误归一化 | errors.go |
| G7 | extra_headers 透传 | config |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不实现 server-side file_search / code interpreter（v2）。
- 不实现 ChatKit 等上层封装。
- 不实现 web_search / image_in 工具（独立 provider spec）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-openai/...` | 鉴权与 SSE 工程可借鉴 |
| `src/darvin-agent/internal/provider/registry.go` | Registry 接口 |

## 3. 用户/系统场景

### 场景 1：stateful 续轮

**Given** 首轮响应返回 `response_id = "resp_abc"`
**When** 第二轮 `previous_response_id = "resp_abc"` 携带
**Then** 模型自动续上文，无需客户端重发

### 场景 2：stream 增量

**Given** 设置 `stream=true`
**When** 收到 `response.output_text.delta`
**Then** 拼装到 onDelta

### 场景 3：tool_use

**Given** Tools 列表 + tool_choice
**When** 收到 `response.completed`
**Then** 含 `output[]` 含 `function_call` 项

## 4. 功能需求

### FR-1 请求字段

| darvin | Responses 字段 |
|---|---|
| `model` | `model` |
| `instructions` | `instructions`（system 内容） |
| `input` | `input`（user/assistant/tool 消息列表） |
| `tools` | `tools` |
| `tool_choice` | `tool_choice` |
| `previous_response_id` | `previous_response_id` |
| `stream` | `stream` |
| `temperature` | `temperature` |
| `max_tokens` | `max_output_tokens` |
| `user` | `user`（headers `OpenAI-User`） |
| `extra_headers` | 透传 |

### FR-2 响应

```json
{
  "id": "resp_abc",
  "object": "response",
  "status": "completed",
  "model": "gpt-4o-2024-08-06",
  "output": [
    { "type": "message", "role": "assistant", "content": [{ "type": "output_text", "text": "..." }] },
    { "type": "function_call", "name": "foo", "arguments": "..." }
  ],
  "usage": { "input_tokens": 12, "output_tokens": 5, "total_tokens": 17, "output_tokens_details": {...}, "input_tokens_details": { "cached_tokens": 3 } }
}
```

### FR-3 流事件

按 `event: <type>\ndata: {...}` 格式。事件类型：

| event | 含义 |
|---|---|
| `response.created` | 流开始 |
| `response.in_progress` | 模型处理 |
| `response.output_text.delta` | 增量文本 |
| `response.function_call_arguments.delta` | 工具参数增量 |
| `response.completed` | 流结束 |

### FR-4 stateful cache

Go runtime 持有 `responsesState`：

```go
type responsesState struct {
    bySession map[string]string
    mu sync.Mutex
}
```

切换 provider / fail 时清空。

### FR-5 Errors

与 Chat 同 schema：401/403/404/429/5xx 等。

### FR-6 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | 普通 200 解析 |
| T2 | SSE 流事件拼接 |
| T3 | previous_response_id 注入 |
| T4 | instructions 拼接 system |
| T5 | tool_choice=auto |
| T6 | function_call.arguments 增量 |
| T7 | 401 |
| T8 | 429 + Retry-After |
| T9 | usage.cached_tokens |
| T10 | 切换 provider 清空 state |
| T11 | 超长 conversation_id 安全处理 |

## 5. 安全与隐私

- `previous_response_id` 不暴露给用户。
- `instructions` 含敏感 system 内容时，UI 不展示。
- `output` 中可能含 tool_calls 的 PII，按审计脱敏策略。
- 流事件断流时不打印截断内容。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| stateful 续轮 400 | 回退到无 previous_response_id 重新发 |
| 503 | Failover 接管 |
| tools schema 失败 | 调用前 fail-fast |
| response_id 失效 | 当新会话处理 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/openai_responses/openai_responses.go` | 主实现 |
| `mapping.go` | request / response |
| `sse.go` | 流事件解析 |
| `errors.go` | 错误归一 |
| `state.go` | previous_response_id 状态 |

## 8. 实施顺序与依赖

1. mapping.go
2. sse.go
3. state.go
4. errors.go
5. 主串联

> 前置：`specs/features/provider-openai/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- provider-openai-responses` |
| V5 | dev 手工验证 stateful 续轮 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- server-side tools（file_search / web_search / code_interpreter）— v2。
- 多模态 image_in / audio_in 工具 — v2。
