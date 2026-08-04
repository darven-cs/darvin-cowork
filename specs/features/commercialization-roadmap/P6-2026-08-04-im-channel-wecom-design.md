# IM Channel: WeCom 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

企业微信（WeCom）提供：

- 自建应用 回调 URL（AES-256-CBC 加密）
- 群机器人 Webhook
- 客户联系 / 上下游

darvin-cowork P6 落地自建应用 + 群机器人两种。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 回调 URL：接收加密消息、解密、应答 | callback |
| G2 | 群机器人 Webhook 主动推送 | webhook |
| G3 | access_token 获取 + 缓存 + refresh | token |
| G4 | 撤回 / 重复回调幂等 | idempotent |
| G5 | 限流 | rate |
| G6 | 凭证加密 | encrypt |
| G7 | SaaS / 自部署 / ngrok 部署边界 | deployment |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做企业微信 OA / 审批。
- 不做上下游 / 客户联系（独立 spec）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/im-channel-abstraction/` | 抽象层 |
| `src/darvin-agent/internal/im/wecom/` | 占位 |

## 3. 用户/系统场景

### 场景 1：回调加密

**Given** WeCom 回调加密 payload
**When** darvin 收到
**Then** 用 AES-256-CBC 解密；解析 message

### 场景 2：主动回复

**Given** Agent 输出
**When** 通过 access_token 调 send
**Then** 限速 5 req/s

### 场景 3：群机器人 Webhook

**Given** 配置 Webhook URL + Key
**When** darvin 主动推送
**Then** 文本 / markdown / news / template 卡片

### 场景 4：撤回

**Given** 用户撤回原始消息
**When** WeCom 回调撤回事件
**Then** Cancel agent

## 4. 功能需求

### FR-1 加密消息处理

```go
func decryptWeCom(ciphertextB64, encodingAESKey, receiveID string) ([]byte, error)
```

PKCS#7 padding；AES-256-CBC；IV = AESKey[:16]。

### FR-2 签名校验

URL 含 `msg_signature = sha256(token, timestamp, nonce, encrypt)`；server 端 verify。

### FR-3 send

```go
POST https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token={token}
```

```json
{
  "touser": "UserID",
  "msgtype": "markdown",
  "markdown": { "content": "..." }
}
```

### FR-4 access_token

```go
type TokenCache struct {
    Token     string
    ExpiresIn int
    mu sync.Mutex
}
```

7200s 过期；提前 300s 刷新。

### FR-5 群机器人

Webhook：

```go
POST <webhook-url>
{
  "msgtype": "markdown",
  "markdown": { "content": "..." }
}
```

无需 access_token。

### FR-6 撤回

无原生撤回事件（仅客户端撤回 UI）；通常通过 read API 过滤失效消息。

### FR-7 限流

```go
type WecomLimiter struct {
    Burst     int // 5
    Interval  time.Duration // 1s
}
```

### FR-8 凭证

```json
{
  "channels.wecom.corp_id": "...",
  "channels.wecom.agent_id": "...",
  "channels.wecom.secret": "***redacted***",
  "channels.wecom.encoding_aes_key": "***redacted***",
  "channels.wecom.token": "..."
}
```

### FR-9 部署边界

- SaaS：`https://qyapi.weixin.qq.com`
- 自部署：baseUrl override
- ngrok：回调 URL 必须 https

### FR-10 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 解密 callback |
| T2 | 签名校验 |
| T3 | 主动 send |
| T4 | markdown 卡片 |
| T5 | access_token cache hit/miss |
| T6 | access_token refresh |
| T7 | 限流 |
| T8 | 群机器人 send |
| T9 | 多 corp 切换 |
| T10 | 自部署 baseUrl |
| T11 | 凭证加密 |

## 5. 安全与隐私

- encoding_aes_key AES-GCM 加密 + keychain 派生。
- 回调 URL 强制 https。
- 调试日志白名单（corp_id / agent_id 允许；secret 屏蔽）。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| access_token 过期 | 自动 refresh |
| 限流 | backoff |
| 解密失败 | 401 + audit |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/im/wecom/crypto.go`（新） | 加解密 |
| `client.go`（新） | api 调用 |
| `token.go`（新） | access_token 缓存 |
| `send.go`（新） | 消息发送 |
| `bot.go`（新） | 群机器人 |
| `limiter.go` | 限流 |

## 8. 实施顺序与依赖

1. `crypto.go` + 单测
2. `token.go`
3. `send.go` + `bot.go`
4. 主串联

> 前置：`im-channel-abstraction`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- im-channel-wecom` |
| V5 | dev mock |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 企业微信 OA / 审批（独立 spec）。
- 客户联系 / 上下游（独立 spec）。
