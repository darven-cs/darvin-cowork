# Provider: Bedrock 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

AWS Bedrock 通过统一的 `Converse` / `ConverseStream` API 暴露多家底层模型（Anthropic Claude / Mistral / Llama / Cohere / AI21）：

- 端点：`POST https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse`
- 鉴权：SigV4（不直接走 Bearer）
- 请求 / 响应归一为通用 `messages[] / system[] / content blocks / toolConfig`

darvin-cowork 必须支持 Bedrock 作为统一后台，让企业客户把多个第三方模型打包在同一 IAM 角色下。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | SigV4 签算（不依赖 AWS SDK） | signer |
| G2 | region / credentials 配置 | config |
| G3 | Converse / ConverseStream 协议归一 | mapping |
| G4 | toolConfig → darvin tool_calls | tools |
| G5 | usage (`inputTokens / outputTokens / totalTokens`) | breakdown |
| G6 | extra_headers / 透传 modelId | config |
| G7 | ≥ 10 测试场景 | |

### 1.3 非目标

- 不实现模型微调 / Provisioned Throughput 管理。
- 不接入 Bedrock Agents / Knowledge Bases（独立 spec / v2）。
- 不支持 `InvokeModel` 原生端点（仅 Converse）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/darvin-agent/internal/provider/openai/` | mapping 参考 |
| `specs/features/provider-mistral/` | Mistral 模型在 Bedrock 上的映射可借鉴 |

## 3. 用户/系统场景

### 场景 1：Anthropic Claude via Bedrock

**Given** 配置 `modelId=anthropic.claude-sonnet-4-5-20250929-v1:0`
**When** Chat 请求
**Then** Bedrock Converse 调用；usage 含 tokens

### 场景 2：凭证缺失

**Given** AWS_ACCESS_KEY_ID 缺 / secret 缺
**When** runtime 启动
**Then** fail-fast；不进入 ready

### 场景 3：模型不支持 tool

**Given** modelId = `cohere.command-light-text-v14` 不支持 toolConfig
**When** 调用
**Then** Capability check fail-fast：`ErrProviderCapabilityUnsupported`

### 场景 4：region 不可达

**Given** region=us-gov-east-1
**When** 调用
**Then** 网络错误归一为 `ErrProviderNetwork`，Failover 接管

## 4. 功能需求

### FR-1 配置

```go
type BedrockConfig struct {
    Region          string
    AccessKeyID     string
    SecretAccessKey string
    SessionToken    string // optional
    ModelID         string
    BaseURL         string // override
}
```

### FR-2 SigV4 signer

```go
type Signer interface {
    Sign(req *http.Request, body []byte, now time.Time) error
}
```

含 4 步骤：

1. Canonical Request
2. String to Sign
3. Signing Key
4. Signature

不依赖 `aws-sdk-go`。

### FR-3 Converse 协议

`POST /model/{modelId}/converse`：

```json
{
  "messages": [
    { "role": "user", "content": [{ "text": "..." }] }
  ],
  "system": [{ "text": "..." }],
  "inferenceConfig": {
    "maxTokens": 1024,
    "temperature": 0.7,
    "topP": 0.9
  },
  "toolConfig": {
    "tools": [
      { "toolSpec": { "name": "foo", "description": "...", "inputSchema": { "json": {...} } } }
    ]
  }
}
```

### FR-4 ConverseStream

`POST /model/{modelId}/converse-stream` 返回 `chunked` 流：

每块 `{"contentBlockDelta": {"delta": {"text": "..."}}}` 等事件。

### FR-5 响应

```json
{
  "output": {
    "message": {
      "role": "assistant",
      "content": [
        { "text": "..." },
        { "toolUse": { "toolUseId": "...", "name": "foo", "input": {...} } }
      ]
    }
  },
  "stopReason": "end_turn",
  "usage": {
    "inputTokens": 12,
    "outputTokens": 5,
    "totalTokens": 17
  }
}
```

### FR-6 Usage

```go
type UsageBreakdown struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CachedTokens     int // 0
    Model            string
    Provider         string // "bedrock"
    SessionID        string
}
```

### FR-7 错误归一化

| HTTP | 处理 |
|---|---|
| 400 | `ErrProviderBadRequest` |
| 401 / 403 | `ErrProviderUnauthorized` |
| 404 | `ErrProviderModelNotFound` |
| 429 | `ErrProviderThrottled` + Retry-After |
| 5xx | `ErrProviderServer` |
| ValidationException | `ErrProviderCapabilityUnsupported` |

### FR-8 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | SigV4 签算单测（固定时间） |
| T2 | 普通 Converse 解析 |
| T3 | ConverseStream 增量 |
| T4 | tools 转换 |
| T5 | tool_use 解析为 darvin tool_calls |
| T6 | 401 |
| T7 | 403 |
| T8 | 404 ModelNotFound |
| T9 | 429 + Retry-After |
| T10 | 5xx 服务错误 |
| T11 | ValidationException（不支持 tool） |

## 5. 安全与隐私

- AWS access key 不进日志；`***redacted***`。
- SigV4 时间依赖：runtime 必须使用稳定时间源，避免时钟漂移导致签名错误。
- STS temporary credentials 额外缓存 + refresh。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| SigV4 clock skew | 失败重试 1 次（用更新后的 now） |
| modelId 不支持 | Capability check fail-fast |
| region 不可达 | Failover |
| ConverseStream 截断 | 重试 1 次 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/bedrock/bedrock.go` | 主实现 |
| `signer.go` | SigV4 |
| `mapping.go` | request / response |
| `stream.go` | ConverseStream 解析 |
| `errors.go` | 错误归一 |
| tests | ≥ 10 场景 |

## 8. 实施顺序与依赖

1. Signer + 单测（含固定时间）
2. mapping
3. errors
4. stream
5. 主串联

> 前置：`specs/features/provider-registry/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- provider-bedrock` |
| V5 | dev 手工验证（mock） |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Knowledge Bases / RAG（独立 spec）。
- Models-from-other-foundation 自定义输出 schema（v2）。
