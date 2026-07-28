# Google Gemini API 完整定义

## 概述

Google Gemini API 提供多种接口风格，包括原生 API 和 OpenAI 兼容接口。

**Base URLs**:
- 原生: `https://generativelanguage.googleapis.com/v1beta`
- OpenAI 兼容: `https://generativelanguage.googleapis.com/v1beta/openai/`

---

## 认证方式

### 1. API Key（标准 Key）

通过 `x-goog-api-key` Header 传递：

```
x-goog-api-key: AI...
```

### 2. Authorization Key（授权 Key）

绑定到 Google Cloud 服务账号，支持：
- 细粒度 IAM 权限控制
- 默认限制为 Generative Language API
- 泄漏检测和快速强制执行

```
x-goog-api-key: YOUR_AUTH_KEY
```

### 3. 服务账号认证（gcloud CLI / 外部环境）

适用于访问 Google Cloud Storage（GCS）等需要特定权限的场景：

```bash
# 使用服务账号 JSON Key 文件登录
gcloud auth application-default login \
  --client-id-file=service-account.json \
  --scopes='https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/devstorage.read_only'
```

### 4. OAuth 2.0（企业级）

通过 Google OAuth 2.0 获取访问令牌，适用于需要用户授权的场景。

### 5. OpenAI 兼容接口认证

Gemini 的 OpenAI 兼容端点支持 OpenAI 的认证方式：

```bash
# 直接使用 API Key
-H "Authorization: Bearer $GEMINI_API_KEY"

# 或通过 x-goog-api-key
-H "x-goog-api-key: $GEMINI_API_KEY"
```

---

## 核心端点

### 原生 API

#### POST /models/{model}:generateContent

生成内容。

```
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-3.6-flash:generateContent
```

#### POST /interactions

交互接口（支持多轮对话和工具调用）。

```
POST https://generativelanguage.googleapis.com/v1beta/interactions
```

### OpenAI 兼容接口

```
POST https://generativelanguage.googleapis.com/v1beta/openai/chat/completions
```

---

## 请求格式（原生 API）

### generateContent 请求体

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `contents` | array | 是 | 内容数组 |
| `tools` | array | 否 | 工具定义 |
| `safetySettings` | array | 否 | 安全设置 |
| `generationConfig` | object | 否 | 生成配置 |
| `systemInstruction` | object | 否 | 系统指令 |

### Contents 格式

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [
        {"text": "Hello"}
      ]
    }
  ]
}
```

**支持的 Role**: `user`, `model`

### Parts 类型

```json
{"text": "Hello"}
{"inlineData": {"mimeType": "image/png", "data": "base64..."}}
{"fileData": {"mimeType": "text/plain", "fileUri": "gs://..."}}
```

---

## Generation Config

```json
{
  "temperature": 0.9,
  "topP": 0.95,
  "topK": 40,
  "candidateCount": 1,
  "maxOutputTokens": 8192,
  "stopSequences": ["END"]
}
```

---

## 工具定义（Tools）

### 函数声明格式

```json
{
  "tools": [
    {
      "functionDeclarations": [
        {
          "name": "get_weather",
          "description": "Get the current weather",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "City name"
              }
            },
            "required": ["location"]
          }
        }
      ]
    }
  ]
}
```

### 内置工具

```json
{
  "tools": [
    {"google_search": {}}
  ]
}
```

---

## 安全设置（Safety Settings）

### Harm Categories

- `HARM_CATEGORY_HATE_SPEECH`
- `HARM_CATEGORY_SEXUALLY_EXPLICIT`
- `HARM_CATEGORY_HARASSMENT`
- `HARM_CATEGORY_DANGEROUS_CONTENT`

### Thresholds

- `BLOCK_NONE`
- `BLOCK_ONLY_HIGH`
- `BLOCK_MEDIUM_AND_ABOVE`
- `BLOCK_LOW_AND_ABOVE`

---

## 流式响应

### 请求

在 `interactions.create` 中设置 `stream: true`

### 事件类型

```
event: interaction.created
event: interaction.in_progress
event: step.start
event: step.delta
event: step.stop
event: interaction.requires_action
event: interaction.completed
```

### SSE 事件示例

```text
event: interaction.created
data: {"type": "interaction.created", "interaction": {"id": "int_xyz", "status": "created"}}

event: step.start
data: {"type": "step.start", "index": 0, "step": {"type": "thought"}}

event: step.delta
data: {"type": "step.delta", "index": 0, "delta": {"type": "thought", "text": "Let me think..."}}

event: step.start
data: {"type": "step.start", "index": 1, "step": {"type": "function_call", "id": "fc_1", "name": "get_weather"}}

event: step.delta
data: {"type": "step.delta", "index": 1, "delta": {"type": "arguments", "partial_arguments": "{\"location\": \"Boston\"}"}}

event: interaction.requires_action
data: {"type": "interaction.requires_action", "interaction": {"id": "int_xyz", "status": "requires_action"}}
```

---

## 工具调用（Function Calling）

### 特点

Gemini 通过 `interactions` API 支持工具调用，采用两步流程：
1. 模型返回函数调用请求
2. 客户端执行后通过 `function_result` 提交结果

### 函数调用事件

```javascript
for await (const event of stream) {
  if (event.event_type === "step.start") {
    if (event.step.type === "function_call") {
      console.log(event.step.id);      // 函数调用 ID
      console.log(event.step.name);     // 函数名
      console.log(event.step.arguments); // 参数对象
    }
  }
}
```

### 提交函数结果

```javascript
const stream2 = await client.interactions.create({
  model: "gemini-3.6-flash",
  previous_interaction_id: firstInteractionId,
  input: [{
    type: "function_result",
    name: funcCallName,
    call_id: funcCallId,
    result: { content: [{ type: "text", text: '{"weather": "Sunny"}' }] }
  }],
  stream: true,
});
```

---

## 响应格式

### generateContent 响应

```json
{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [{"text": "Hello! How can I help?"}]
      },
      "finishReason": "STOP",
      "safetyRatings": [
        {"category": "HARM_CATEGORY", "probability": "NEGLIGIBLE"}
      ]
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 10,
    "candidatesTokenCount": 20,
    "totalTokenCount": 30
  }
}
```

### interactions 响应

```json
{
  "interaction": {
    "id": "int_xyz",
    "status": "completed",
    "usage": {
      "promptTokens": 256,
      "completionTokens": 128,
      "totalTokens": 384
    }
  }
}
```

### Finish Reasons

- `STOP` - 正常完成
- `MAX_TOKENS` - 达到最大 token
- `SAFETY` - 安全过滤
- `RECITATION` - 内容引用限制

---

## OpenAI 兼容接口

Gemini 提供 OpenAI 兼容的 `/v1beta/openai/chat/completions` 端点：

```bash
curl https://generativelanguage.googleapis.com/v1beta/openai/chat/completions \
  -H "Authorization: Bearer $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.6-flash",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### 支持的 OpenAI API 特性

- `chat/completions` 端点
- `models.list` 端点
- 流式响应
- 函数调用

---

## 可用模型

| 模型 ID | 说明 |
|---------|------|
| `gemini-3.6-flash` | 快速响应，多模态 |
| `gemini-3.5-flash` | 平衡性能 |
| `gemini-3.5-pro` | 强推理能力 |
| `gemini-2.0-flash` | 标准版本 |

---

## 代码示例

### Python（原生 SDK）

```python
from google import genai

client = genai.Client()

response = client.models.generate_content(
    model='gemini-3.6-flash',
    contents='Hello',
    config=types.GenerateContentConfig(
        temperature=0.9,
        max_output_tokens=1024
    )
)
```

### Python（流式 + 函数调用）

```python
stream = client.interactions.create(
    model='gemini-3.6-flash',
    input='What is the weather in Paris?',
    tools=[weather_tool],
    stream=True
)

for event in stream:
    if event.event_type == 'step.start':
        if event.step.type == 'function_call':
            print(f"Function: {event.step.name}")
    elif event.event_type == 'step.delta':
        if event.delta.type == 'text':
            print(event.delta.text, end='', flush=True)
```

### JavaScript

```javascript
import { GoogleGenAI } from '@google/genai';

const client = new GoogleGenAI({});

const response = await client.models.generateContent({
    model: 'gemini-3.6-flash',
    contents: 'Hello'
});
```

### cURL

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.6-flash:generateContent" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "contents": [{"parts": [{"text": "Hello"}]}]
  }'
```
