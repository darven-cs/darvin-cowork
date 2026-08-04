# IM Channel: DingTalk 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

钉钉（DingTalk）IM 提供两种集成方式：

- Stream API（WebSocket 长连接）—— 更省运维
- 旧 HTTP 回调（robot）—— 用户已有机器人

darvin-cowork P6 落地 Stream 模式为主，HTTP 回调为可选项。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Stream 长连接注册 + 心跳 | ws |
| G2 | 加签校验（Secret） | sign |
| G3 | 事件映射：`ChatBotMessage / CardCallback / MessageRecall` | map |
| G4 | 主动发送 / 卡片回调更新 | send |
| G5 | 限流（钉钉 20 req/s 默认） | backoff |
| G6 | 凭证加密 | encrypt |
| G7 | SaaS / 自部署 / ngrok 部署边界 | deployment |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做钉钉 OA / 审批 / 文档。
- 不实现钉钉小程序（独立 spec）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/im-channel-abstraction/` | 抽象层 |
| `src/darvin-agent/internal/im/dingtalk/` | 占位 |

## 3. 用户/系统场景

### 场景 1：Stream 连接

**Given** 配置 AppKey / AppSecret / 机器人 code
**When** runtime 启动
**Then** Stream 长连接建立；心跳 30s

### 场景 2：用户消息

**Given** 用户 @机器人
**When** Stream 收到 ChatBotMessage
**Then** Gateway 转 `im:dingtalk:chat_id:sender_id` session

### 场景 3：卡片回调

**Given** Agent 用 interactive card 回复
**When** 用户点卡片按钮
**Then** Stream 收到 CardCallback；trigger agent 续答

### 场景 4：撤回

**Given** 用户撤回原消息
**When** Stream 收到 MessageRecall
**Then** Cancel agent turn

## 4. 功能需求

### FR-1 Stream 连接

```go
client := dingtalk.NewStreamClient(AppKey, AppSecret, RobotCode)
client.OnMessage(...)
client.OnCardCallback(...)
go client.Run(ctx)
```

### FR-2 加签

```go
func signDingTalk(timestamp int64, secret string) string
```

HMAC-SHA256 + `timestamp\nsecret` → base64。

### FR-3 事件映射

| type | map |
|---|---|
| `ChatBotMessage` | user message |
| `CardCallback` | interactive response |
| `MessageRecall` | recall |

### FR-4 主动发送

`robot/oToMessages/batchSend` 或 Stream 主动 send。

```json
{
  "robotCode": "...",
  "userIds": ["..."],
  "msgKey": "sampleText",
  "msgParam": "..."
}
```

### FR-5 卡片更新

stream 推送 card instance_id，回调 CardCallback 时按 userChoice 拼装后续输出。

### FR-6 限流

默认 20 req/s；用户可在 settings 调整。

### FR-7 凭证

```json
{
  "channels.dingtalk.app_key": "...",
  "channels.dingtalk.app_secret": "***redacted***",
  "channels.dingtalk.robot_code": "..."
}
```

### FR-8 部署边界

Stream 走公网，不需要 ngrok。

### FR-9 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | Stream 连接 |
| T2 | 加签 |
| T3 | 用户消息 |
| T4 | 卡片回调 |
| T5 | 主动 send |
| T6 | 撤回 |
| T7 | 限流 backoff |
| T8 | 凭证加密 |
| T9 | Stream 断线重连 |
| T10 | 心跳 |
| T11 | 多机器人 |

## 5. 安全与隐私

- credentials AES-GCM。
- timestamp 防 replay；服务端校验 ±5min。
- robotCode 不进日志。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Stream 断开 | 指数退避重连 |
| 限流 | backoff |
| 用户 @ 多次 | dedup by event id |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/im/dingtalk/client.go`（新） | Stream 客户端 |
| `signature.go`（新） | 加签 |
| `callback.go`（新） | CardCallback 解析 |
| `send.go`（新） | 主动发送 |
| `recall.go`（新） | 撤回 |
| `limiter.go` | 限流 |

## 8. 实施顺序与依赖

1. `signature.go` + 单测
2. `client.go`
3. `send.go` + `callback.go`
4. `recall.go`
5. 主串联

> 前置：`im-channel-abstraction`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- im-channel-dingtalk` |
| V5 | dev mock 验证 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 钉钉 OA / 审批（独立 spec）。
