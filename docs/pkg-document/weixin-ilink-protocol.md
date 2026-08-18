# 微信(iLink)通道对接协议

> 本文档整理自腾讯官方微信 OpenClaw 插件 `@tencent-weixin/openclaw-weixin`（v2.4.6）的
> `README.zh_CN.md`「后端 API 协议」章节与 `src/api/*.ts` / `src/messaging/send.ts` 源码，
> 并按 darvin-cowork 的 `src/darvin-agent/internal/im/weixin` 落地情况标注了实现要点。
>
> 用途：darvin-cowork 的个人微信（WeChat/Weixin）IM 通道对接参考。接收走 `getupdates` 长轮询，
> 发送走 `sendmessage`。协议本身是腾讯 iLink 网关（`https://ilinkai.weixin.qq.com`）的公开 HTTP JSON 接口。

## 通用请求头

所有接口均为 `POST`，请求/响应均为 JSON。

| Header | 说明 |
|--------|------|
| `Content-Type` | `application/json` |
| `AuthorizationType` | 固定值 `ilink_bot_token` |
| `Authorization` | `Bearer <token>`（扫码登录后获取） |
| `X-WECHAT-UIN` | 随机 uint32 的 base64 编码（**每请求随机即可**，官方即如此） |

darvin 落地：`weixin.Connector.post()` 构造这些 header；`X-WECHAT-UIN` 用
`wechatUIN()` 每请求生成一个 `base64(decimal-uint32)`。

## 接口列表

| 接口 | 路径 | 说明 |
|------|------|------|
| getUpdates | `getupdates` | 长轮询获取新消息 |
| sendMessage | `sendmessage` | 发送消息（文本/图片/视频/文件） |
| getUploadUrl | `getuploadurl` | 获取 CDN 上传预签名 URL |
| getConfig | `getconfig` | 获取账号配置（typing ticket 等） |
| sendTyping | `sendtyping` | 发送/取消输入状态指示 |

darvin 落地：`weixin.go` 已用 `getupdates` + `sendmessage`；`getuploadurl` / `getconfig` /
`sendtyping` 尚未接入。

## getUpdates（收消息）

长轮询接口。服务端有新消息或超时后返回。

**请求体：**

```json
{
  "get_updates_buf": ""
}
```

- `get_updates_buf`：上次响应返回的同步游标，首次请求传空字符串。

**响应体：**

```json
{
  "ret": 0,
  "msgs": [...],
  "get_updates_buf": "<新游标>",
  "longpolling_timeout_ms": 35000
}
```

- `ret`：返回码，`0` = 成功。
- `errcode` / `errmsg`：错误码 / 描述（如 `errcode: -14` = 会话超时，需重新扫码）。
- `msgs`：`WeixinMessage[]`（结构见下）。
- `get_updates_buf`：新游标，下次长轮询回传。

darvin 落地：`pollLoop()` 把游标存 `Connector.cursor` 回传；`-14` 清空游标并**不停止连接器**
（继续长轮询重新建立会话）。文本从 `item_list[]` 的 `text_item.text` 提取（`Text()`）。

## sendMessage（发消息）⚠️ 本仓库踩坑点

**请求体：**

```json
{
  "msg": {
    "from_user_id": "",
    "to_user_id": "<目标用户 ID>",
    "client_id": "<每次新生成的 client_id>",
    "message_type": 2,
    "message_state": 2,
    "context_token": "<入站缓存回传>",
    "item_list": [
      { "type": 1, "text_item": { "text": "你好" } }
    ]
  },
  "base_info": { "channel_version": "1.0.3" }
}
```

**关键字段（缺失会导致消息被 iLink 受理[拿到 message_id/ret:0]但对方微信窗口收不到）：**

- `message_type: 2`（BOT）——必须，缺了 iLink 不知道这是 bot 消息。
- `message_state: 2`（FINISH）——必须，否则消息停留在未完成态不对外展示。
- `client_id`：**每次发送新生成**一个 `prefix:<unixms>-<8hex>`（如 `openclaw-weixin:1787000000000-a1b2c3d4`），
  **不要**回传入站的 `client_id`。
- 文本 item 用 `text_item: { text }` 嵌套，不是 `{ text }` 平铺。
- `context_token`：从对应入站消息缓存，按 `to_user_id` 回显。

darvin 落地：`weixin.Connector.Send()` 已按上述格式构造；`context_token` 存
`Connector.ctxToken[peerID]`（收到消息时以 `from_user_id` 为 key 缓存）。`client_id` 用
`newClientID()` 每次生成。

**教训**：早期 darvin 实现缺 `message_type`/`message_state` 且 `item_list` 用 `{type,text}` 平铺，
结果是 iLink 返回 `ret:0` + `message_id`，但对方微信看不到回复；对齐腾讯插件 wire 格式后修复。

## 消息结构

### WeixinMessage

| 字段 | 类型 | 说明 |
|------|------|------|
| `seq` | `number?` | 消息序列号 |
| `message_id` | `number?` | 消息唯一 ID |
| `from_user_id` | `string?` | 发送者 ID |
| `to_user_id` | `string?` | 接收者 ID |
| `create_time_ms` | `number?` | 创建时间戳（ms） |
| `session_id` | `string?` | 会话 ID |
| `message_type` | `number?` | `1` = USER, `2` = BOT |
| `message_state` | `number?` | `0` = NEW, `1` = GENERATING, `2` = FINISH |
| `item_list` | `MessageItem[]?` | 消息内容列表 |
| `context_token` | `string?` | 会话上下文令牌，回复时需回传 |

### MessageItem

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | `number` | `1` TEXT, `2` IMAGE, `3` VOICE, `4` FILE, `5` VIDEO |
| `text_item` | `{ text: string }?` | 文本内容 |
| `image_item` | `ImageItem?` | 图片（含 CDN 引用和 AES 密钥） |
| `voice_item` | `VoiceItem?` | 语音（SILK 编码） |
| `file_item` | `FileItem?` | 文件附件 |
| `video_item` | `VideoItem?` | 视频 |
| `ref_msg` | `RefMessage?` | 引用消息 |

### CDN 媒体引用（CDNMedia）

所有媒体类型（图片/语音/文件/视频）通过 CDN 传输，使用 AES-128-ECB 加密：

| 字段 | 类型 | 说明 |
|------|------|------|
| `encrypt_query_param` | `string?` | CDN 下载/上传的加密参数 |
| `aes_key` | `string?` | base64 编码的 AES-128 密钥 |

## CDN 上传流程（媒体发送前置）

1. 计算文件明文大小、MD5，以及 AES-128-ECB 加密后的密文大小。
2. 图片/视频还需缩略图的明文与密文参数。
3. 调用 `getUploadUrl` 获取 `upload_param`（和 `thumb_upload_param`）。
4. 用 AES-128-ECB 加密文件内容，PUT 上传到 CDN URL。
5. 缩略图同理加密上传。
6. 用返回的 `encrypt_query_param` 构造 `CDNMedia`，放入 `MessageItem` 发送。

## 参考实现

- 腾讯官方插件源码：
  - 协议类型 `src/api/types.ts`
  - API 调用 `src/api/api.ts`
  - 文本发送 `src/messaging/send.ts`
  - 入站处理 `src/messaging/inbound.ts` / `process-message.ts`
- darvin 落地：`src/darvin-agent/internal/im/weixin/weixin.go`
