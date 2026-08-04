# IM Channel 抽象层 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化要求把 IM 接入 darvin-cowork：飞书 / 钉钉 / 企业微信三家为 P6 验收范围。darvin-cowork 当前无 IM 集成。

LobsterAI 的 `src/main/libs/im-client.ts` 给出参考；darvin-cowork 的目标是：

- 独立 Gateway `:8081/ws`
- 平台无关：`OnEvent / Send / ReplyStream`
- sessionId 规范：`<im>:<platform>:<chatId>:<senderId>`
- 多租户隔离

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 独立 `:8081/ws` Gateway | port |
| G2 | DarvinEvent 子集下发（`agent.*` / `session.*`） | subset |
| G3 | sessionId 规范 | naming |
| G4 | OnEvent / Send / ReplyStream 三方法 | interface |
| G5 | 背压 / 重试 / 去重 | resilience |
| G6 | 流式 delta 聚合（≥ 30 ms flush） | aggregation |
| G7 | 多租户隔离（per-channel-instance） | boundary |
| G8 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做 11 个平台（仅 P6 三家 + im-channel-extension 预留）。
- 不做 IM 内富文本（仅 markdown + code）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/main/runtime/manager.ts` | 进程托管 |
| `src/darvin-agent/internal/gateway/` | 计划承载 |
| `src/shared/darvin-api.ts` | DarvinEvent |

## 3. 用户/系统场景

### 场景 1：飞书 webhook 进来

**Given** Gateway 收到飞书消息回调
**When** 校验签名
**Then** 构造 `im:feishu:chat_id:sender_id` session；调起 agent

### 场景 2：回复流

**Given** agent 正在输出
**When** OnEvent `agent.text.delta`
**Then** IM Gateway 聚合 30ms 后调平台 send 接口

### 场景 3：撤回

**Given** 用户在 IM 端撤回消息
**When** Gateway 收到撤回回调
**Then** 取消 agent 当前 turn；写 audit

### 场景 4：背压

**Given** agent 输出 burst
**When** 平台限速（飞书限 5 msg/s）
**Then** Gateway buffer + 退避重试

## 4. 功能需求

### FR-1 Gateway

```go
// src/darvin-agent/internal/im/gateway.go
type Gateway struct {
    addr   string // :8081
    routes []RouteBinding
}
```

WS 协议走现有 darvin JSON-RPC 2.0；事件用 DarvinEvent 子集。

### FR-2 Channel interface

```go
type Channel interface {
    ID() string // 'feishu' / 'dingtalk' / 'wecom'
    OnEvent(evt DarvinEvent) error
    Send(ctx context.Context, msg *Outbound) error
    ReplyStream(ctx context.Context, sessionID string, onDelta func([]byte) error) error
}
```

### FR-3 sessionId

```
<im>:<platform>:<chatId>:<senderId>
```

例：`im:feishu:oc_abc:user_123`

注入 darvin session manager。

### FR-4 DarvinEvent 子集

```go
var imSubscriptions = []string{
    "agent.session.started",
    "agent.text.delta",
    "agent.tool.call",
    "agent.tool.result",
    "agent.session.completed",
    "agent.error",
}
```

### FR-5 ReplyStream 聚合

```go
type StreamAggregator struct {
    FlushInterval time.Duration // 30ms
    MaxBatchBytes  int           // 4KB
}

func (a *StreamAggregator) OnDelta(delta []byte) error
```

达到 flush interval 或 max batch 即调用平台 send。

### FR-6 背压 / 重试 / 去重

- 平台返回 429 → exponential backoff
- 同 event id 多次收到 → dedup map TTL 60s
- 平台 webhook 重复 → 通过 event id 丢弃

### FR-7 多租户

每个 channel instance 拥有：

- 独立的凭证
- 独立的 webhook 路由
- 独立的 rate limit 配置
- 独立的 audit log

### FR-8 凭证加密

`channels.<id>.credentials` 全部 AES-GCM，密钥 derived from keychain。

### FR-9 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | sessionId 命名 |
| T2 | Gateway 启动 |
| T3 | OnEvent 分发 |
| T4 | Send 调用 |
| T5 | ReplyStream 聚合 |
| G6 | 30ms flush |
| T7 | 429 backoff |
| T8 | event id 去重 |
| T9 | webhook 重复 |
| T10 | 凭证加密 |
| T11 | 撤回事件 |

## 5. 安全与隐私

- Gateway 仅监听 `:8081` + cloudflared/ngrok 暴露公网。
- Credentials 不进日志 / 不进 SQLite 明文。
- Audit log 落本地 SQLite；不上云。
- 撤回事件记录用于支持审计。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| platform 限速 | 退避 |
| 公网 webhook 不可达 | 平台重试机制接住 |
| Gateway panic | watcher 隔离 |
| sessionId 冲突 | uuid v7 防冲突 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/im/gateway.go`（新） | Gateway |
| `src/darvin-agent/internal/im/channel.go`（新） | interface |
| `src/darvin-agent/internal/im/aggregator.go`（新） | StreamAggregator |
| `src/darvin-agent/internal/im/dedup.go`（新） | dedup |
| `src/darvin-agent/internal/im/credentials.go`（新） | credentials 加密 |
| `src/shared/darvin-api.ts` | 事件子集类型 |
| `src/renderer/services/im-channel.ts`（新） | UI 列 |

## 8. 实施顺序与依赖

1. `channel.go` + interface
2. `gateway.go` + 启动
3. `aggregator.go`
4. `dedup.go`
5. UI

> 前置：`runtime-supervision` + `darvin-api-extension`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- im-channel-abstraction` |
| V5 | dev 手工：mock platform 验证 Send/Reply |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 11 个平台（v2 / im-channel-extension）。
- 富文本 IM 编辑器（v2）。
