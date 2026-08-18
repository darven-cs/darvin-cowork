<a href="./IM.md">English</a>
&nbsp;·&nbsp;
<strong>简体中文</strong>
&nbsp;·&nbsp;
<a href="../README.zh-CN.md">README</a>

# IM 通道

> QQ / 企业微信 / 个人微信连接器，真实一次性连通性探测、结构化检查报告、实例管理、个人微信扫码登录。

IM 子系统在 `src/darvin-agent/internal/im/`。它是一个可插拔的传输层，把外部 IM bot 桥接到 darvin 会话：三个通道任意一个的入站消息都被归一化并派发到绑定的 session，agent 跑一次 headless turn，回执通过同通道回给原 peer。

## 架构

```
┌─────────────────────────────────────────────────────────────────────┐
│   Renderer（ImView / ImInstanceCard.vue）                          │
│   - 每通道 tab（qq / wecom / weixin）                              │
│   - 新建 / 编辑 / 删除 / 重命名实例                                │
│   - secret 明文/隐藏 + 清空、删除二次确认                          │
│   - 内联重命名、未保存 badge、丢弃 toast                            │
│   - 一次性测试弹窗（verdict + 逐条 check + 启用提示）              │
│   - lastError 红条                                                 │
└─────────────────────────────────────────────────────────────────────┘
                       │ window.darvin.im*  (Electron IPC 上的 JSON-RPC)
                       ▼
┌─────────────────────────────────────────────────────────────────────┐
│   主进程（imProxy → AgentClient）                                  │
└─────────────────────────────────────────────────────────────────────┘
                       │  ws://localhost:<port>/ws  (agent.im.*)
                       ▼
┌─────────────────────────────────────────────────────────────────────┐
│   darvin-agent                                                      │
│   internal/im/                                                      │
│   - manager.go     生命周期 / 按通道 builder / 实例映射            │
│   - contract.go    Instance 接口 + Status + Prober + Check          │
│   - handlers.go    im.* 方法的 JSON-RPC handler                     │
│   - base.go        共享的 status / inbound plumbing                │
│   - qq/            QQ 官方机器人                                   │
│   - wecom/         企业微信 AI Bot WS 通道                         │
│   - weixin/        个人微信 iLink HTTP 通道（+ 扫码登录）          │
│   - qrlogin.go     共享 QR 状态机                                  │
└─────────────────────────────────────────────────────────────────────┘
```

## 通道

### QQ —— 官方机器人

- **传输**：对 `https://api.sgroup.qq.com` 的 Discord 风格 WebSocket gateway；REST 发消息。
- **鉴权**：按需刷 app access token（向 `https://bots.qq.com/app/getAppAccessToken`）。提前 1 分钟内过期即视为热 key，避免在过期临界点抖动。
- **发**：`POST /v2/users/{peerID}/messages`（C2C）、`POST /v2/groups/{peerID}/messages`（群）。每次发送带新 `msg_seq` 做幂等 / 排序。
- **收**：WS gateway pump；op `0`（Dispatch）解码成 `InboundMessage`。心跳保活；指数退避重连。fatal 码（如 4014）停止循环而不是重试。
- **探测**：真实跑 `ensureToken` 向 QQ token 端点换 token——**永不读缓存 token**。缓存路径不会触发，因为 `handleTest` 用候选 config 新建一个 Connector，cache 状态为空。

### 企业微信 —— AI Bot

- **传输**：WebSocket 到 `wss://openws.work.weixin.qq.com`。
- **鉴权**：`aibot_subscribe` 带 `bot_id` + `secret`。等 `aibot_subscribe` ack；非零 `errcode` 即 fatal。
- **发**：同 socket 上 `aibot_send_msg`。主动发送支持 markdown / template_card / media；文本走 markdown（与官方 `@wecom/aibot-node-sdk` 一致）。
- **收**：`aibot_msg_callback` 帧作为入站消息，派发给绑定的 session。
- **探测**：独立一次性 dial + `aibot_subscribe` + `waitAuth` + `Close` 会话。probe 自带 `probeTimeout`（`20s`），`waitAuth` 自己的 read deadline 是 `10s`；`waitAuth` 不响应 ctx，外层 ctx 是双保险。

### 个人微信 —— iLink

- **传输**：HTTP，对固定 iLink base `https://ilinkai.weixin.qq.com`。
- **登录**：`GET /ilink/bot/get_bot_qrcode?bot_type=3` 返回 QR 图 + 扫码 key；`GET /ilink/bot/get_qrcode_status?qrcode=…` 长轮询状态（`waiting / scanned / confirmed / expired`）。`confirmed` 后 renderer 用返回的 `bot_token` + `ilink_bot_id` 自动保存实例。
- **发**：`POST /ilink/bot/sendmessage`，按 openclaw-weixin 插件的 envelope（`base_info.channel_version`、`text_item.text`、每次发送新 `client_id`）。
- **收**：长轮询 `POST /ilink/bot/getupdates`，`timeout: 50`。iLink `ret == -14` 即会话超时——连接器清掉本地游标 + 缓存的 `context_token`，重连。
- **探测**：独立的 probe-only `getupdates`，`timeout: 3`——**不**动游标、**不**缓存 `context_token`、**不**派发入站。`BotToken` 必填；缺 token 时直接 `login_ok: fail` check，不发请求。

## Prober —— 一次性连通性探测

旧版 `imTest` handler 只调 `build()` 然后返回 `{ok: true}`——假 ok。新的 `Prober` 接口是消费侧契约，给 renderer 一个结构化检查报告。

`internal/im/contract.go`：

```go
// Prober performs a one-shot connectivity check for the candidate config
// without persisting or staying connected. Connectors able to probe implement
// it; others fall back to a build-only pass.
type Prober interface {
    Probe(ctx context.Context) ([]Check, error)
}

// Check is one granular connectivity check inside a probe report.
type Check struct {
    Code   string `json:"code"`
    Title  string `json:"title"`
    Level  string `json:"level"` // pass | warn | fail
    Detail string `json:"detail,omitempty"`
}
```

行为：

- 各连接器实现 `Probe(ctx) ([]Check, error)`。
- `internal/im/handlers.go` 里的 handler 用候选 config 新建实例后跑 `Probe`，再 `defer inst.Stop`。实例不会进入 manager 的实例映射。
- 错误合并进 `Checks`（`level:"fail"`）。空报告视为未决议（非 pass）。
- 所有错误收敛成单个 `TestResult` shape——`Checks` 承担所有判断，`Error` 是兜底；`imTest` 不再为「未知 channel」「config 损坏」返回 JSON-RPC error——这两类并入 `{code:"channel" / "config_valid", fail}`。

各连接器发出的稳定 `code` 值（renderer i18n 把 code 映射到用户可见 title）：

| Code | Title | 使用方 |
|---|---|---|
| `auth_ok` | App access token（QQ）/ Gateway auth（企业微信）/ Get updates（个人微信） | 三个连接器都用 |
| `login_ok` | Bot token（个人微信）/ Session（个人微信遇 `-14`） | 个人微信 |
| `config_valid` | Config | handler（build 失败） |
| `channel` | Connector | handler（未知 channel） |
| `build_ok` | Build | handler（连接器没实现 `Prober` 的兜底） |
| `probe` | Probe | handler（probe 报错且 checks 为空） |

## 实例管理

`manager.go` 持有活的实例集：每个 IM 通道 × 每个已注册 config 一个实例。manager：

- 通过通道专属 builder（注册于 `runtime.go: im.ChannelQQ → qq.NewConnector` 等）从持久化 config 构造连接器。
- 把入站 handler 接到 session runner（`SubmitForIM(ctx, imKey, prompt, sink)`）。
- 出站回执走 `inst.Send(ctx, target, outbound)`。
- 配置更新时热重载（`Update(ctx, id, patch)` 重建并重启）。
- 通过 WebSocket Broadcaster 推通知（`ImChanged` / `ImStatusChanged`），renderer 重新拉取。

每个实例回报一个 `Status`：

```go
type Status struct {
    Channel    string `json:"channel"`
    InstanceID string `json:"instanceId"`
    Enabled    bool   `json:"enabled"`
    State      string `json:"state"` // connected | disconnected | error | login_expired | stopped
    LastError  string `json:"lastError,omitempty"`
    StartedAt  int64  `json:"startedAt,omitempty"` // unix ms
    SentCount  int64  `json:"sentCount"`
    RecvCount  int64  `json:"recvCount"`
}
```

renderer 把 `lastError` 直接展示在卡片红条上——连接失败不必再点一次测试也能看到根因。

## RPC 表面

renderer 可调的 `agent.im.*` 方法（`src/shared/darvin-api.ts`）：

| 方法 | 用途 |
|---|---|
| `imList` | 列出全部实例 + 状态快照 |
| `imGet` | 拿单个实例 |
| `imCreate` | 新建实例；持久化 config，`enabled=true` 时启动 |
| `imUpdate` | patch 更新；运行时热重载 |
| `imDelete` | 停 + 删 |
| `imSetEnabled` | 切换 enabled（启动或停止） |
| `imTest` | 对候选 config 跑 `Prober` |
| `imLoginStart` | 开始扫码登录（个人微信） |
| `imLoginPoll` | 轮询扫码登录 |

推送事件：

- `ImChanged`——配置变更（创建 / 更新 / 删除 / enabled 切换）。
- `ImStatusChanged`——连接状态变迁。

## Renderer UI

`src/renderer/components/im/ImInstanceCard.vue`（每通道列表在 `src/renderer/views/ImView.vue`）：

- 每个实例一张卡片，含状态 pill + lastError 红条。
- 编辑面板：名称 / 凭据 / 访问控制字段；secret 输入带眼睛切换 + ✕ 清空。
- 删除按钮先弹确认 modal，列出实例名再销毁。
- 内联重命名：点名称进编辑态，回车或失焦落库；空值回退到 channel 名。
- 编辑面板任一字段改动即出现「未保存」badge；切 tab 或关闭编辑时若 dirty 弹丢弃 toast。

测试报告弹窗：

- verdict：绿（全 `pass`）、黄（含 `warn`）、红（含 `fail`）。
- 逐条 check 行：level 图标 + title + detail（如有）。
- 对当前**停用**实例 pass 时，弹一个**手动「启用」按钮**（不静默自启）。
- fail 时弹窗提示「保存后重试」。

## 设计要点

- **Prober 是可选的**。没实现 `Probe` 的连接器仍能响应 `imTest`，返回单条 `build_ok: pass` check。接口 opt-in 设计——跳过零成本，实现了收益大。
- **probe pass 不静默自启**。LobsterAI 在 `auth_check` 通过后自动开 enabled；darvin-cowork 在弹窗里给一个手动确认按钮，避免误启用。
- **每 IM 实例一个 workspace**。Manager 保证每个 IM 实例一个独立 workspace，UI 把同一通道的所有会话归到一个稳定、带名字的 workspace 下。
- **probe 超时分层**。每个连接器给自己的 probe 选合理上限（`Probe` ctx 超时）。当底层 transport 不响应 ctx（如 WeCom `waitAuth` 用 WS read deadline）时，probe ctx 仍是双保险。
- **`-14` 处理按通道**。QQ 没有，企业微信没有，个人微信有——probe 把它展示成 `login_ok: fail` check，让用户知道要重新扫码，而不是「保存后再试」。

## 未来通道

加一个新 IM 通道（如飞书、钉钉）按设计是三步：

1. 创建 `internal/im/<channel>/<channel>.go`，写 `Connector` 类型并（可选）实现 `Probe(ctx) ([]Check, error)`。
2. 在 `src/darvin-agent/internal/runtime/runtime.go` 注册 builder（`im.ChannelFoo → foo.NewConnector`）。
3. 把 channel 常量加进 `internal/im/contract.go`，在 renderer 侧加凭据 UI。

Renderer 侧通过 `imList` 的 payload 自动识别新通道。