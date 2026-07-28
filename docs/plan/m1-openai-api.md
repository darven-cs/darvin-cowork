# OpenAI GPT API 完整定义

## 概述

OpenAI API 提供 Chat Completions 作为主要接口，支持流式响应和函数调用。

**Base URL**: `https://api.openai.com/v1`

---

## 认证方式

### 1. API Key（最常用）

通过 `Authorization` Header 的 Bearer Token 传递：

```
Authorization: Bearer $OPENAI_API_KEY
```

### 2. Azure OpenAI Service

Azure OpenAI 支持两种认证方式：

#### API Key 方式

```python
client = openai.AzureOpenAI(
    azure_endpoint=os.environ["AZURE_OPENAI_ENDPOINT"],
    api_key=os.environ["AZURE_OPENAI_API_KEY"],
    api_version="2023-09-01-preview"
)
```

#### Azure Active Directory (AAD) 方式

```python
from azure.identity import DefaultAzureCredential, get_bearer_token_provider

client = openai.AzureOpenAI(
    azure_endpoint=os.environ["AZURE_OPENAI_ENDPOINT"],
    azure_ad_token_provider=get_bearer_token_provider(
        DefaultAzureCredential(),
        "https://cognitiveservices.azure.com/.default"
    ),
    api_version="2023-09-01-preview"
)
```

```typescript
import { AzureOpenAI } from 'openai';
import { getBearerTokenProvider, DefaultAzureCredential } from '@azure/identity';

const client = new AzureOpenAI({
    azureADTokenProvider: getBearerTokenProvider(new DefaultAzureCredential(), scope),
    apiVersion: '2024-10-01-preview',
});
```

### 3. 多组织/多项目认证

如果属于多个组织或使用旧版用户 API Key，可通过 Header 指定目标：

```
OpenAI-Organization: org-...
OpenAI-Project: proj-...
```

### 4. Admin API Key（组织级管理）

用于管理目的，需要 admin API key：

```python
client = OpenAI(admin_api_key=os.environ["OPENAI_ADMIN_KEY"])
```

### 5. 短期访问令牌（Workload Identity）

OpenAI 支持通过 workload identity federation 获取短期访问令牌。

---

## 核心端点

### POST /chat/completions

创建聊天补全。

```
POST https://api.openai.com/v1/chat/completions
```

### GET /models

列出可用模型。

```
GET https://api.openai.com/v1/models
```

---

## 请求格式

### 请求体参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型 ID，如 `gpt-4o`, `gpt-4o-mini`, `o3` |
| `messages` | array | 是 | 消息数组 |
| `temperature` | float | 否 | 随机性 0.0-2.0，默认 1.0 |
| `top_p` | float | 否 | 核采样 |
| `stream` | boolean | 否 | 是否启用流式，默认 false |
| `stop` | string/array | 否 | 停止序列 |
| `max_tokens` | integer | 否 | 最大生成 token |
| `presence_penalty` | float | 否 | 存在惩罚 |
| `frequency_penalty` | float | 否 | 频率惩罚 |
| `tools` | array | 否 | 工具定义数组 |
| `tool_choice` | string/object | 否 | 工具选择策略 |
| `response_format` | object | 否 | 响应格式约束 |
| `seed` | integer | 否 | 随机种子 |
| `user` | string | 否 | 用户标识 |

---

## Messages 格式

### 基础消息

```json
{
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hello! How can I help you?"}
  ]
}
```

### 支持的 Role

| Role | 说明 |
|------|------|
| `system` | 系统指令 |
| `user` | 用户消息 |
| `assistant` | 助手回复 |
| `tool` | 工具执行结果（用于函数调用） |

### 消息内容类型

```json
{"role": "user", "content": "Hello"}

{"role": "user", "content": [
  {"type": "text", "text": "Hello"},
  {"type": "image_url", "image_url": {"url": "https://...", "detail": "high"}}
]}

{"role": "user", "content": [
  {"type": "input_audio", "input_audio": {"data": "base64...", "format": "wav"}}
]}
```

---

## 工具定义（Tools）

### 格式

```json
{
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the current weather",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {
              "type": "string",
              "description": "City name"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"]
            }
          },
          "required": ["location"]
        }
      }
    }
  ]
}
```

### 工具选择

```json
"tool_choice": "auto"

"tool_choice": {"type": "function", "function": {"name": "get_weather"}}
```

---

## 流式响应

### 请求

设置 `stream: true`

### 响应事件格式

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":123,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

### Python 流式示例

```python
stream = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True
)

for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### JavaScript 流式示例

```javascript
const stream = await openai.chat.completions.create({
    model: "gpt-4o-mini",
    messages: [{"role": "user", "content": "Hello"}],
    stream: true
});

for await (const chunk of stream) {
    if (chunk.choices[0].delta.content) {
        process.stdout.write(chunk.choices[0].delta.content);
    }
}
```

---

## 函数调用（Function Calling）

### 特点

OpenAI 通过 `tool_calls` 字段返回函数调用请求：
- 消息的 `content` 可能为 `null`
- 函数调用在 `tool_calls` 数组中
- 工具结果通过 `tool` role 的消息提交

### 响应格式

```json
{
  "id": "chatcmpl-xxx",
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "I'll check the weather for you.",
      "tool_calls": [
        {
          "id": "call_88O3ElkW2RrSdRTNeeP1PZkm",
          "type": "function",
          "function": {
            "name": "get_weather",
            "arguments": "{\"location\":\"New York, NY\"}"
          }
        }
      ]
    },
    "finish_reason": "tool_calls"
  }]
}
```

### 执行函数并提交结果

```python
for tool_call in completion.choices[0].message.tool_calls:
    name = tool_call.function.name
    args = json.loads(tool_call.function.arguments)

    result = call_function(name, args)

    messages.append({
        "role": "tool",
        "tool_call_id": tool_call.id,
        "content": str(result)
    })

# 继续对话
response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=messages
)
```

### JavaScript 版本

```javascript
for (const toolCall of completion.choices[0].message.tool_calls ?? []) {
    const name = toolCall.function.name;
    const args = JSON.parse(toolCall.function.arguments);
    const result = await callFunction(name, args);

    messages.push({
        role: "tool",
        tool_call_id: toolCall.id,
        content: result.toString()
    });
}
```

---

## 响应格式

### 非流式响应

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1699896916,
  "model": "gpt-4o-mini",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 15,
    "total_tokens": 25
  }
}
```

### 流式响应最后一块（含 usage）

```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion.chunk",
  "created": 1694268190,
  "model": "gpt-4o-mini",
  "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 15,
    "total_tokens": 25
  }
}
```

### Finish Reasons

- `stop` - 正常完成
- `length` - 达到 max_tokens
- `tool_calls` - 需要执行工具
- `content_filter` - 内容过滤

---

## 可用模型

### GPT-4o 系列

| 模型 ID | 说明 |
|---------|------|
| `gpt-4o` | 最强全面能力 |
| `gpt-4o-mini` | 轻量快速 |
| `gpt-4o-2024-08-06` | 带版本号 |

### GPT-4 系列

| 模型 ID | 说明 |
|---------|------|
| `gpt-4-turbo` | Turbo 加速版 |
| `gpt-4` | 标准版 |

### Reasoning 模型

| 模型 ID | 说明 |
|---------|------|
| `o3` | 高级推理 |
| `o4-mini` | 轻量推理 |
| `o1` | 早期推理模型 |

### Embeddings

| 模型 ID | 说明 |
|---------|------|
| `text-embedding-4` | 最新 Embedding |

---

## 错误响应

```json
{
  "error": {
    "message": "Rate limit reached",
    "type": "rate_limit_error",
    "code": "rate_limit_exceeded",
    "param": null,
    "line": null
  }
}
```

### 错误类型

- `invalid_request_error` - 请求参数错误
- `authentication_error` - API Key 无效
- `rate_limit_error` - 速率限制
- `internal_error` - 服务器内部错误

---

## 代码示例

### Python

```python
from openai import OpenAI

client = OpenAI()

response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[
        {"role": "system", "content": "You are helpful."},
        {"role": "user", "content": "Hello"}
    ],
    temperature=0.7,
    max_tokens=1024
)
```

### JavaScript

```javascript
import OpenAI from 'openai';

const openai = new OpenAI();

const response = await openai.chat.completions.create({
    model: 'gpt-4o-mini',
    messages: [
        {role: 'system', content: 'You are helpful.'},
        {role: 'user', content: 'Hello'}
    ]
});
```

### cURL

```bash
curl https://api.openai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### Go

```go
client := openai.NewClient()

resp, err := client.Chat.Completions.New(context.Background(),
    openai.ChatCompletionArguments{
        Model: "gpt-4o-mini",
        Messages: []openai.ChatCompletionMessage{
            {Role: "user", Content: "Hello"},
        },
    },
)
```
