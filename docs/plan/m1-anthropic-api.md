# Anthropic Claude API 完整定义

## 概述

Anthropic Claude API 采用 **Messages API** 架构，所有请求都通过 `POST /v1/messages` 端点。

**Base URL**: `https://api.anthropic.com/v1`

---

## 认证方式

Anthropic API 支持多种认证方式：

### 1. API Key（最常用）

通过 `x-api-key` Header 传递：

```
x-api-key: sk-ant-api03-...
```

### 2. Bearer Token（短期访问令牌）

通过 `Authorization` Header 传递从 OAuth 端点获取的短期访问令牌：

```
Authorization: Bearer $ACCESS_TOKEN
```

### 3. Workload Identity Federation（企业级/云平台）

支持通过标准 OIDC/JWT 交换获取短期访问令牌，适用于：
- **AWS** (Amazon EKS, ECS)
- **GCP** (Google Cloud Run, GKE, Compute Engine)
- **Azure** (Azure Kubernetes Service)
- **Okta**（及其他 OIDC 兼容 IdP）

#### WIF 认证流程

1. 从云平台获取 OIDC JWT 令牌
2. 调用 `/v1/oauth/token` 交换为 Anthropic 访问令牌
3. 使用访问令牌调用 API

#### POST /v1/oauth/token

交换 JWT 为短期访问令牌：

```bash
curl -X POST https://api.anthropic.com/v1/oauth/token \
  -H "content-type: application/json" \
  -d '{
    "grant_type": "urn:ietf:params:oauth:grant-type:jwt-bearer",
    "assertion": "$JWT_FROM_IDP",
    "federation_rule_id": "$ANTHROPIC_FEDERATION_RULE_ID",
    "organization_id": "$ANTHROPIC_ORGANIZATION_ID",
    "service_account_id": "$ANTHROPIC_SERVICE_ACCOUNT_ID",
    "workspace_id": "$ANTHROPIC_WORKSPACE_ID"
  }'
```

响应：
```json
{
  "access_token": "...",
  "expires_in": 3600
}
```

#### AWS WIF 示例

```python
import anthropic
from anthropic import IdentityTokenFile, WorkloadIdentityCredentials

client = anthropic.Anthropic(
    credentials=WorkloadIdentityCredentials(
        identity_token_provider=IdentityTokenFile(
            os.environ["ANTHROPIC_IDENTITY_TOKEN_FILE"]
        ),
        federation_rule_id=os.environ["ANTHROPIC_FEDERATION_RULE_ID"],
        organization_id=os.environ["ANTHROPIC_ORGANIZATION_ID"],
        service_account_id=os.environ["ANTHROPIC_SERVICE_ACCOUNT_ID"],
    ),
)
```

#### GCP WIF 示例

```python
def fetch_google_identity_token() -> str:
    import google.auth.transport.requests
    return google.oauth2.id_token.fetch_id_token(
        google.auth.transport.requests.Request(),
        audience="https://api.anthropic.com"
    )

client = anthropic.Anthropic(
    credentials=WorkloadIdentityCredentials(
        identity_token_provider=fetch_google_identity_token,
        federation_rule_id=os.environ["ANTHROPIC_FEDERATION_RULE_ID"],
        organization_id=os.environ["ANTHROPIC_ORGANIZATION_ID"],
        service_account_id=os.environ["ANTHROPIC_SERVICE_ACCOUNT_ID"],
    ),
)
```

#### Azure WIF 示例

```go
client := anthropic.New(
    option.WorkloadIdentityCredentials(
        option.IdentityTokenProvider(func(ctx context.Context) (string, error) {
            // Azure Foley token provider
            return fetchAzureIdentityToken()
        }),
        option.FederationRuleID(os.Getenv("ANTHROPIC_FEDERATION_RULE_ID")),
        option.OrganizationID(os.Getenv("ANTHROPIC_ORGANIZATION_ID")),
        option.ServiceAccountID(os.Getenv("ANTHROPIC_SERVICE_ACCOUNT_ID")),
    ),
)
```

---

## 核心端点

### POST /v1/messages

创建一条新消息交互。

#### 请求头

| Header | 必填 | 说明 |
|--------|------|------|
| `x-api-key` 或 `Authorization: Bearer` | 是 | 认证凭证（二选一） |
| `anthropic-version` | 是 | 固定值 `2023-06-01` |
| `content-type` | 是 | `application/json` |

#### 请求体参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型 ID，如 `claude-opus-4-8`, `claude-sonnet-4-7`, `claude-haiku-4-7` |
| `messages` | array | 是 | 消息数组 |
| `max_tokens` | integer | 是 | 最大生成 token 数（建议 8192+） |
| `system` | string/array | 否 | 系统提示 |
| `temperature` | float | 否 | 随机性 0.0-1.0，默认 1.0 |
| `top_p` | float | 否 | 核采样阈值 |
| `top_k` | integer | 否 | top-k 采样 |
| `tools` | array | 否 | 工具定义数组 |
| `tool_choice` | object | 否 | 工具选择策略 |
| `stop_sequences` | array | 否 | 停止序列 |
| `stream` | boolean | 否 | 是否启用流式响应 |
| `metadata` | object | 否 | 元数据（可含 `user_id`） |
| `thinking` | object | 否 | 扩展思考配置 |

#### Messages 格式

```json
{
  "messages": [
    {
      "role": "user",
      "content": "Hello, Claude"
    },
    {
      "role": "assistant",
      "content": "Hello! How can I help you today?"
    }
  ]
}
```

**支持的 Role**:
- `user` - 用户消息
- `assistant` - 助手回复

---

## 工具定义（Tools）

### 工具参数结构

```json
{
  "name": "get_weather",
  "description": "Get the current weather in a given location",
  "input_schema": {
    "type": "object",
    "properties": {
      "location": {
        "type": "string",
        "description": "The city and state, e.g. San Francisco, CA"
      }
    },
    "required": ["location"]
  }
}
```

### 工具选择策略

```json
{
  "type": "any",
  "name": "get_weather"
}
```

- `type: "auto"` - 模型自动选择
- `type: "any"` - 强制使用任一工具
- `type: "tool"` - 指定具体工具

---

## 流式响应（Streaming）

### 请求

设置 `stream: true`

### 响应事件类型

```
text event: message_start
text event: message_delta
text event: content_block_start
text event: content_block_delta
text event: content_block_stop
text event: message_stop
```

### 流式事件示例

```json
event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "!"}}
```

---

## 工具调用（Tool Use）

### 特点

Claude API 将工具调用**集成在标准消息结构中**：
- 工具请求在 `assistant` 消息的 `content` 数组中
- 工具结果通过 `user` 消息的 `tool_result` 类型传递

### Assistant 消息中的工具调用

```json
{
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "I'll check the weather for you."
    },
    {
      "type": "tool_use",
      "id": "toolu_123",
      "name": "get_weather",
      "input": {"location": "San Francisco, CA"}
    }
  ]
}
```

### 工具结果消息

```json
{
  "role": "user",
  "content": [
    {
      "type": "tool_result",
      "tool_use_id": "toolu_123",
      "content": "72°F, sunny"
    }
  ]
}
```

---

## 响应格式

### 非流式响应

```json
{
  "id": "msg_abc123",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "Hello!"
    }
  ],
  "model": "claude-opus-4-8",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 20
  }
}
```

### Stop Reasons

- `end_turn` - 正常完成
- `max_tokens` - 达到最大 token 限制
- `stop_sequence` - 遇到停止序列
- `tool_use` - 需要执行工具

---

## 错误响应

```json
{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "Rate limit exceeded"
  }
}
```

### 错误类型

- `authentication_error` - API Key 无效
- `rate_limit_error` - 速率限制
- `invalid_request_error` - 请求参数错误
- `model_error` - 模型执行错误

---

## 可用模型

| 模型 ID | 说明 |
|---------|------|
| `claude-opus-4-8` | 最强推理能力 |
| `claude-sonnet-4-7` | 平衡性能 |
| `claude-haiku-4-7` | 快速响应 |

---

## 代码示例

### cURL

```bash
curl -X POST https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{
    "model": "claude-sonnet-4-7",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "Hello"}
    ]
  }'
```

### Go

```go
client := anthropic.NewClient()

resp, err := client.Messages.NewMessage(
    context.Background(),
    anthropic.MessageCreateParams{
        Model:     "claude-sonnet-4-7",
        MaxTokens: 1024,
        Messages: []anthropic.MessageParam{
            anthropic.NewUserMessageParam("Hello"),
        },
    },
)
```

### Python

```python
client = anthropic.Anthropic()

message = client.messages.create(
    model="claude-sonnet-4-7",
    max_tokens=1024,
    messages=[
        {"role": "user", "content": "Hello"}
    ]
)
```
