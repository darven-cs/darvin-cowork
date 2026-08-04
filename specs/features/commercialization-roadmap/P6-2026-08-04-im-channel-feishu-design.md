# IM Channel: Feishu 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

飞书（Feishu/Lark）IM 通过 Event Subscription 回调 HTTP + 卡片消息更新。darvin-cowork 用户希望从飞书收到消息并回复。本 spec 在 `im-channel-abstraction` 基础上落地飞书平台 spec。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 回调验签（Encrypt key + Verification token） | sign |
| G2 | 事件映射：`message / message_read / recall / reaction` | map |
| G3 | 卡片流式更新（card.message.update） | card |
| G4 | 限流 5 msg/s 自动退避 | backoff |
| G5 | 撤回 / 重复回调幂等 | idempotent |
| G6 | 凭证加密 | encrypt |
| G7 | SaaS / 自部署 / ngrok 部署边界 | deployment |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不实现 larksuite 私有 SDK（仅自实现）。
- 不做飞书文档 / 多维表格 / 视频会议。
- 不支持飞书 Lite。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/im-channel-abstraction/` | 抽象层 |
| `src/darvin-agent/internal/im/` | 占位 |

## 3. 用户/系统场景

### 场景 1：用户消息

**Given** 飞书群中用户发消息
**When** 回调到 darvin Gateway
**Then** 验签通过；转 `im:feishu:chat_id:sender_id` session；agent 启动

### 场景 2：卡片回复

**Given** agent 在跑
**When** 持续输出
**Then** 飞书卡片每 30ms 更新（更新 message_id）

### 场景 3：撤回

**Given** 用户撤回原始消息
**When** 飞书回调 `message.recalled`
**Then** Gateway 取消 agent turn；audit log

### 场景 4：限流

**Given** 飞书限 5 msg/s
**When** Gateway burst
**Then** 退避 1s，重试

## 4. 功能需求

### FR-1 回调验签

```go
func verifyFeishuSignature(encrypt string, ts string, nonce string, key string) bool
```

匹配飞书官方 Encrypt key 算法。

### FR-2 事件类型

| type | map |
|---|---|
| `im.message.receive_v1` | user message |
| `im.message.recalled_v1` | recall |
| `im.message.message_read_v1` | read receipt（忽略） |
| `im.chat.member_change_v1` | 加入/离开（可选） |

### FR-3 Send 接口

`POST /open-apis/im/v1/messages`

```json
{
  "receive_id": "oc_xxx",
  "msg_type": "interactive",
  "card": { "elements": [...] }
}
```

或 `msg_type=text` 简单文本。

### FR-4 卡片流式

飞书提供 `PATCH /open-apis/im/v1/messages/{message_id}`：

```json
{
  "content": { "body": { "elements": [...] } }
}
```

Gateway 通过 ReplyStream → 调 PATCH。

### FR-5 限流

```go
type Limiter struct {
    Burst     int  // 5
    Interval  time.Duration  // 1s
}
```

backoff：1s / 2s / 4s + jitter。

### FR-6 撤回幂等

撤回事件到达后，记录 `event_id`；后续 cancel agent turn；audit log。

### FR-7 凭证

```json
{
  "channels.feishu.app_id": "cli_xxx",
  "channels.feishu.app_secret": "***redacted***",
  "channels.feishu.encrypt_key": "***redacted***",
  "channels.feishu.verification_token": "***redacted***"
}
```

### FR-8 部署边界

| mode | 边界 |
|---|---|
| SaaS | `https://open.feishu.cn` |
| 自部署 | 用户在 settings 配置 baseUrl |
| ngrok | 通过 ngrok 暴露 Gateway |

### FR-9 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 签名校验 |
| T2 | 用户消息映射 |
| T3 | send text |
| T4 | send card |
| T5 | 卡片流式更新 |
| T6 | 撤回 |
| T7 | 限流 5 msg/s |
| T8 | backoff |
| T9 | event id 幂等 |
| T10 | 自部署 baseUrl |
| T11 | ngrok webhook |

## 5. 安全与隐私

- credentials AES-GCM 加密；密钥 derived from keychain。
- verification token 入参校验防 replay。
- 飞书 webhook URL 不进日志。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 签名校验失败 | 401 |
| 限流 | backoff + audit |
| 重试收件人变更 | cancel + new session |
| 网络断流 | reconnect |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/im/feishu/feishu.go`（新） | Channel 实现 |
| `verify.go`（新） | 验签 |
| `card.go`（新） | 卡片构建与更新 |
| `send.go`（新） | send / patch |
| `limiter.go`（新） | 限流 + backoff |
| `recall.go`（新） | 撤回 |
| `credentials.go` | 加密 |

## 8. 实施顺序与依赖

1. `verify.go` + 单测
2. `send.go` + `card.go`
3. `limiter.go`
4. `recall.go`
5. 主串联

> 前置：`im-channel-abstraction`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- im-channel-feishu` |
| V5 | dev 手工 mock 平台验证 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 飞书文档 / 多维表格 / 视频会议（独立 spec）。
