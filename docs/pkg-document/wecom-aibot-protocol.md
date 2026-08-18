# 企业微信 AI 智能体(Bot)通道对接协议

> 本文档整理自腾讯官方 OpenClaw 插件 `@wecom/wecom-openclaw-plugin@2026.5.7` 与其依赖的
> `@wecom/aibot-node-sdk@1.0.7`（`dist/*.d.ts` 类型定义 + `dist/index.cjs.js` 实现），
> 并按 darvin-cowork 的 `src/darvin-agent/internal/im/wecom` 落地情况标注了实现要点。
>
> 用途：darvin-cowork 的企业微信（WeCom）IM 通道对接参考。darvin 早期用的是「群机器人
> Webhook」（`cgi-bin/webhook/send?key=`，只能发、单向）；对齐 OpenClaw 官方通道后改为企业
> 微信 **AI 智能体（AI Bot）** 的 WebSocket 长连接协议，双向收发。

## 背景：两种「企业微信 bot」的区别

| 形态 | 发送 | 接收 | 凭据 | 判定 |
|------|------|------|------|------|
| 群机器人 Webhook（chatbot） | `webhook/send?key=<key>` | 无 | webhook key | ✗ 单向，仅发一个群 |
| AI 智能体 / 自建应用（aibot） | WS `aibot_send_msg` | WS `aibot_msg_callback` | `botId` + `secret` | ✓ 双向 |

darvin 对齐到 AI 智能体：**WebSocket 长连接** `wss://openws.work.weixin.qq.com` 承载收发、
心跳与文本回复，与 QQ bot（`api.sgroup.qq.com`）和微信 iLink（`ilinkai.weixin.qq.com`）
的「长连接 + 主动回发」模型一致。

## WebSocket 帧结构

收发统一为 `{ cmd, headers:{ req_id }, body?, errcode?, errmsg? }`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `cmd` | `string` | 命令；认证/心跳/回执响应可能为空 |
| `headers.req_id` | `string` | 请求 id，`<前缀>_<unixms>_<8 hex>`，用于配对回执 |
| `body` | `object` | 随命令变化的负载 |
| `errcode` / `errmsg` | `number/string` | 仅响应帧有；`0` = 成功 |

## 命令与端点

| 方向 | `cmd` | 说明 |
|------|-------|------|
| 认证 | `aibot_subscribe` | 连接建立后首帧：`body: { bot_id, secret }` |
| 心跳 | `ping` | 定时发送 `{ cmd:"ping", headers:{ req_id } }`；服务端 pong |
| 收消息 | `aibot_msg_callback` | 服务端→客户端推送（inbound） |
| 收事件 | `aibot_event_callback` | 模板卡片 / 权限变更等事件回调 |
| 主动发送 | `aibot_send_msg` | 主动向某会话推消息（outbound，不依赖回调帧） |
| 被动回复 | `aibot_respond_msg` | 沿入站 `req_id` 回消息（需持入站帧） |

darvin 落地：`wecom.Connector` 用 `aibot_subscribe`（`bot_id`+`secret`）认证；`ping` 心跳；
`aibot_msg_callback` 收文本；`aibot_send_msg` 主动回复。

## 认证（aibot_subscribe）

连接建立后立即发送：

```json
{
  "cmd": "aibot_subscribe",
  "headers": { "req_id": "aibot_subscribe_1787000000000_a1b2c3d4" },
  "body": { "bot_id": "<BotID>", "secret": "<Secret>" }
}
```

服务端回执 `{ headers:{ req_id: "aibot_subscribe_..." }, errcode: 0, errmsg: "ok" }`。
认证失败（`errcode != 0`）表示 botId/secret 配置错误，应停止重连（非瞬时抖动）。

darvin 落地：`runOnce()` 发认证帧后 `waitAuth()` 阻塞读直到具 `aibot_subscribe_` 前缀的
回执；`errcode != 0` 时 `runOnce` 返回 `fatal=true`，`wsLoop` 停止不再重连。

## 心跳（ping）

SDK 默认 30s 一次：`{ "cmd": "ping", "headers": { "req_id": "ping_<ts>_<hex>" } }`。
服务端回 pong（`errcode: 0`）。连续丢失 pong 达到阈值判定连接死亡并重连；darvin 用读超时
（`SetReadDeadline = 3 × 30s`）实现等价判定——任何帧（含 pong）都会刷新 deadline，超时则
`ReadMessage` 报错触发重连。

## 收消息（aibot_msg_callback）

`cmd: "aibot_msg_callback"`，`body` 结构（对齐 SDK `BaseMessage`）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `msgid` | `string` | 消息唯一 id（用于排重） |
| `aibotid` | `string` | 智能机器人 id |
| `chatid` | `string?` | 会话 id，**群聊返回** |
| `chattype` | `"single" \| "group"` | 单聊 / 群聊 |
| `from.userid` | `string` | 触发者 userid |
| `msgtype` | `"text" \| "image" \| ...` | 消息类型 |
| `text.content` | `string` | 文本内容（`msgtype=text` 时） |

darvin peer 映射（对齐 SDK `chatId = body.chatid || from.userid`）：

- `chattype=single`：`PeerKind=direct`，回复目标 peerID = `from.userid`
- `chattype=group`：`PeerKind=group`，peerID = `chatid`

## 主动发送（aibot_send_msg）⚠️ 本仓库踩坑点

企业微信 AI Bot 的**主动发送只支持 `markdown` / `template_card` / 媒体**，**没有纯文本
`text` 变体**（SDK `SendMsgBody` 联合类型不含 `text`）。因此文本回复必须包装成 markdown：

```json
{
  "cmd": "aibot_send_msg",
  "headers": { "req_id": "aibot_send_msg_<ts>_<hex>" },
  "body": {
    "chatid": "<用户 userid 或群 chatid>",
    "msgtype": "markdown",
    "markdown": { "content": "<回复文本>" }
  }
}
```

- `chatid`：单聊填用户的 `userid`，群聊填群的 `chatid`（即 inbound peerID）。
- 服务端以匹配 `headers.req_id` 的帧回执；`errcode != 0` 即拒投，需作为错误暴露。

darvin 落地：`wecom.Connector.Send()` 用 `aibot_send_msg` + `markdown` 主动发送，
`chatid=to.PeerID`；`req_id` 入 `pending` map，读循环按 req_id 配对回执并带回超时
（`sendAckTimeout=15s`）。

**教训**：早期 darvin wecom 用群机器人 webhook 单向发送且把 `BotID` 当 webhook key，既收不到
消息、也发不到个人；对齐 AI 智能体 WS 协议后实现双向。

## 被动回复（aibot_respond_msg）

沿入站回调的 `req_id` 回复，body 为 `StreamReplyBody`（`msgtype:"stream"` + `stream:{id, finish, content}`）。
darvin 的 `Send` 与入站帧解耦（manager 异步回发），不适用被动回复；统一走主动发送。

## 示例 curl / WS 诊断

```bash
# 用 wscat 之类的工具连上后手动发认证帧：
# {"cmd":"aibot_subscribe","headers":{"req_id":"aibot_subscribe_1_abc"},"body":{"bot_id":"<BOTID>","secret":"<SECRET>"}}
```

## 参考实现

- 腾讯官方插件：`@wecom/wecom-openclaw-plugin` / `@wecom/aibot-node-sdk`
  - `dist/src/message-sender.js`（事件回调用 `aibot_send_msg` + markdown 兜底）
  - `dist/src/message-parser.js`（inbound peer/文本解析）
  - SDK `dist/types/api.d.ts`（WsCmd / SendMsgBody / StreamReplyBody）
  - SDK `dist/types/message.d.ts`（BaseMessage / chattype / from.userid）
- darvin 落地：`src/darvin-agent/internal/im/wecom/wecom.go`
