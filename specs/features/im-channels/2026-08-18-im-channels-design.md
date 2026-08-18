# IM 通道子系统设计文档

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 是个人桌面智能助手，但目前只能**在应用内**对话——用户必须打开应用才能和 AI 交流。目标是把 AI 接到外部 IM（QQ、微信），让用户随时随地用手机上的 IM 就能和 darvin 对话。

**调研结论**（已对 LobsterAI 的 IM 通道做完整源码调研，从 UI 到运行时）：

- LobsterAI 的 IM 能力 **~90% 依赖 OpenClaw sidecar**：QQ（`qqbot` 插件）、个人微信（`openclaw-weixin` 外部插件）、企业微信（`wecom` 插件）的收发与 agent 循环全部跑在 OpenClaw 里；LobsterAI 只是"控制器"（存配置 → 写 `openclaw.json` → 重启/监控 gateway → 会话映射）。
- darvin-cowork **没有 OpenClaw**，且架构哲学是「业务逻辑全放 Go」。照搬 sidecar 会引入第二个完整 agent 运行时，与现状冲突。
- **决定**：Go 原生实现。已确认三个通道协议都可行：
  - **QQ**：官方开放平台 Bot（`appId + clientSecret`），WS gateway（Discord 风格 opcode）+ REST 发送。协议参考 OpenClaw `extensions/qqbot/`（MIT）。
  - **企业微信 WeCom**：官方 Bot API（`botId + secret`），有 Go SDK（`go-laoji/wecom-go-sdk`）可参考。
  - **个人微信 Weixin**：腾讯 **iLink API**（固定 base `https://ilinkai.weixin.qq.com`，~8 个 HTTP endpoint，长轮询收消息，扫码登录）。MIT 参考源码 `github.com/Tencent/openclaw-weixin` 的 `src/api/api.ts` + `src/auth/login-qr.ts` 是纯 Node stdlib、协议很小，Go 移植几百行。**否决** `openwechat`（网页版协议，2017 后注册的号登不上）。

### 1.2 目标

darvin-agent (Go) 内建 IM 通道子系统：定义可扩展的通道抽象，原生实现 **QQ / 企业微信(WeCom) / 个人微信(Weixin)** 三个连接器，打通「IM 收消息 → darvin agent 回复 → IM 发回」全链路；renderer 新增「IM 通道」设置视图；配置/状态经现有三层透传模式（Go → main proxy → IPC → preload → composable）。

### 1.3 非目标

- 不内嵌 OpenClaw / 不引入第二个 agent 运行时。
- 个人微信**只做私聊**（iLink 不支持群聊）；QQ / WeCom 先做私聊 + 群 @（如协议支持），群聊复杂交互后续。
- 不做公众号 / 小程序 / 网页版微信登录。
- 不把 IM 消息历史回填到应用内会话列表（v1 的 IM 会话本身就是 darvin 的 pinned session，历史天然连续）。
- 只实现 QQ / WeCom / Weixin 三个通道；DingTalk / Feishu / Telegram 等**仅保证扩展点预留**，不在本期实现。

## 2. 用户场景

### 场景 1: 绑定 QQ 机器人
**Given** 用户在 QQ 开放平台创建了 Bot，拿到 appId + appSecret
**When** 在 darvin「IM 通道」页选择 QQ，填写 appId / appSecret，保存并启用
**Then** darvin 建立 WS 连接，状态显示「已连接」；好友私聊该 Bot 时收到回复

### 场景 2: 绑定企业微信机器人
**Given** 用户有企业微信应用凭证 botId + secret
**When** 在设置页配置并启用
**Then** 成员通过企业微信应用会话与 darvin 对话，收到回复

### 场景 3: 个人微信扫码登录
**Given** 手机装有微信
**When** 在设置页选「个人微信」，点击「扫码登录」，页面弹出二维码，用户扫码并确认
**Then** 登录成功，状态变「已连接」；对方私聊该微信号收到回复；token 持久化，重启免扫码

### 场景 4: 消息流转（核心闭环）
**Given** 某个 IM 通道已连接
**When** IM 用户向 Bot 发一条消息
**Then** darvin 把消息投递给一个稳定的 IM 会话（同一聊天对象上下文连续），agent 处理完，最终回复文本回到该聊天对象

### 场景 5: 连接状态监控
**Given** 通道已启用
**When** 掉线 / 重连 / 配置错误 / 登录失效
**Then** 设置页实时显示每个实例的连接状态（已连接 / 未连接 / 错误原因），推送事件驱动 UI 刷新

## 3. 功能需求

### FR-1: 通道抽象与注册
- 定义 `Channel` / `Instance` 接口（配置加载、启动/停止、状态、发送、入站回调）。
- 管理器 `Manager` 持有 `map[instanceID]Instance`，负责实例生命周期 + 入站分发 + 出站路由。
- 新通道按 scheduledtask 的**显式装配**模式接入（`NewHandlers(engine, store, log)` → 塞进 `HandlerOptions`，见 `internal/runtime/runtime.go:271-300`），不走 F7 `init()` 自注册；主装配只需在 runtime 加一行 build + 一个选项字段。

### FR-2: QQ 连接器
- 官方开放平台 Bot：`appId + clientSecret` → `POST /app/getAppAccessToken` 换 token（缓存 + 提前刷新）。
- WS gateway（`gorilla/websocket` 已有）：IDENTIFY / HEARTBEAT / RESUME / DISPATCH，处理 `C2C_MESSAGE_CREATE` / `GROUP_AT_MESSAGE_CREATE` / `AT_MESSAGE_CREATE` 等；断线指数退避重连，4014 等致命错误不重连。
- 出站：`POST /v2/users/{openid}/messages`（C2C）、`/v2/groups/{group_id}/messages`（群）；长文本 markdown 分块。
- 访问控制：`dmPolicy / groupPolicy`（open/allowlist/disabled）+ `allowFrom` 白名单。

### FR-3: 企业微信连接器
- 官方 Bot API：`botId + secret` 鉴权，私聊 + 群 @。
- 入站：应用消息回调（带签名校验）或轮询；v1 先做拉取/回调二选一，spec 落地时按协议确认。
- 出站：发送文本（支持 markdown / thinking 开关按协议）。

### FR-4: 个人微信连接器（iLink）
- 固定 base `https://ilinkai.weixin.qq.com`；扫码登录：`get_bot_qrcode` → 轮询 `get_qrcode_status` 拿 `bot_token` + `ilink_bot_id`。
- 收消息：`getupdates` 长轮询；发消息：`sendmessage`；媒体：`getuploadurl`；键入态：`sendtyping`。
- `bot_token` 持久化（用户数据目录），重启免扫码。

### FR-5: 配置 CRUD 与状态
- `im.list`（通道 + 实例 + 状态快照）/ `im.get` / `im.create` / `im.update` / `im.delete` / `im.set_enabled` / `im.test`（连通性）。
- 实例启用即启动连接器；停用即停止；配置变更热重载或重启实例。

### FR-6: 会话映射与 agent 集成
- IM 会话 key：`im:<channel>:<accountID>:<peerKind>:<peerID>`（对齐 OpenClaw 格式），稳定映射到一个 darvin sessionID，上下文跨消息连续。
- 复用 headless turn 机制：扩展 `sessionruntime.PromptRequest` 增加可选 `ReplySink`，turn 完成时把最终文本回调给 IM 分发器，由分发器找到对应目标并 `Send`。
- 每个 IM 聊天对象独立 darvin 会话（互不串扰）。

### FR-7: 推送事件
- `ImChanged`（实例列表/配置变更）、`ImStatusChanged`（连接状态变更）走现有 `agent.event` → EventRouter → `webContents.send` 链路。

### FR-8: renderer 设置页
- 「IM 通道」顶层视图（与 ScheduledView 平级）：左侧平台列表（QQ / 企业微信 / 个人微信），右侧平台详情（多实例卡片 + 实例配置表单）。
- 实例表单：凭据输入（QQ appId/secret、WeCom botId/secret）、访问控制、保存/启用/删除、连通性测试。
- 个人微信 / QQ 扫码登录弹窗（显示二维码、轮询状态、成功/失败/过期）。
- 状态徽标：已连接（绿）/ 已启用未连接（黄）/ 停用（灰），错误原因展示。

## 4. 实现方案

### 4.1 Go 包布局（新增 `internal/im/`）

```
internal/im/
  contract.go    # Channel / Instance / InboundMessage / Outbound / Target / Status 契约
  manager.go     # Manager：实例生命周期、入站分发、出站路由、Broadcaster 引用
  handlers.go    # JSON-RPC Handlers（agent.im.*）
  qrlogin.go     # 扫码登录通用状态机（start/poll），仅 weixin(iLink) 使用
  qq/            # QQ 连接器（token 管理、WS gateway、REST 发送）
  wecom/         # 企业微信连接器
  weixin/        # iLink 连接器
internal/agents/store/imstore.go   # IMChannelStore（GORM）+ IMSessionMappingStore
```

契约（精简版，对齐 OpenClaw `ChannelPlugin` 的 gateway/config/outbound/status 四件套）：

```go
type Instance interface {
    ID() string                       // instance id（通道内唯一）
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Status() Status                   // connected / lastError / startedAt / inbound·outbound 计数
    Send(ctx context.Context, to Target, msg Outbound) error
    SetInboundHandler(h InboundHandler)   // 由 Manager 注入
}

type InboundMessage struct {
    Channel    string // qq | wecom | weixin
    AccountID  string
    PeerKind   string // direct | group
    PeerID     string
    SenderID   string
    SenderName string
    Content    string
    Raw        json.RawMessage // 通道原文，供扩展
}

type InboundHandler func(ctx context.Context, msg InboundMessage)
```

`Manager` 职责：
- `ConfigureInstance(cfg)` → 按 channel 组装连接器实例，注册到 map；`SetEnabled` 控制 Start/Stop。
- 入站：收到 `InboundMessage` → 计算 IM 会话 key → 经 `SessionRunner`（见 4.3）提交 headless turn，`ReplySink` 闭包捕获 channel + target。
- 状态变更 / 配置变更 → `Broadcaster.Broadcast("agent.event", ...)` 推 `ImChanged` / `ImStatusChanged`。

### 4.2 各通道协议要点（Go 实现）

**QQ**（参考 `extensions/qqbot`）：
- `TokenManager`：`POST https://bots.qq.com/app/getAppAccessToken`，缓存 + singleflight + 过期前刷新。
- `GET /gateway` 拿 WS URL → `gorilla/websocket`；opcode 0/1/2/6/7/9/10/11；intents 掩码取 `GROUP_AND_C2C | DIRECT_MESSAGE`；`session_id + seq` 支持 RESUME。
- 事件 → 入站：`C2C_MESSAGE_CREATE`（openid 为 peer）、`GROUP_AT_MESSAGE_CREATE`；按 peer 串行队列 + 事件 id 去重。
- 出站：`POST /v2/users/{openid}/messages`（私聊）、`/v2/groups/{group_id}/messages`（群）；`msg_seq` 防重；长文本分块。

**WeCom**：官方 Bot API。`sendmessage` 文本/媒体；入站经应用回调（验签）或主动拉取（落地时按协议确认模式）。

**Weixin（iLink）**：8 个 endpoint 全量移植（见 1.1）。`getupdates` 长轮询循环（带退避）；扫码登录状态机复用 4.1 的 `qrlogin.go`。

### 4.3 Agent 集成（关键决策）

复刻 `SubmitForSchedule` 模式（`gateway/sessionmgr.go:216`），新增：

```go
// SessionManager 上
func (m *SessionManager) SubmitForIM(ctx context.Context, imKey, prompt string, sink ReplySink) (string, error)
// imKey = "im:<channel>:<accountID>:<peerKind>:<peerID>" → GetOrCreateEntry(imKey)
```

扩展 `sessionruntime.PromptRequest`（`loop.go:34`）增加可选字段，**非 nil 才启用**，普通对话零影响：

```go
type PromptRequest struct {
    // ... 现有字段
    ReplySink func(ctx context.Context, reply string, runID string) // 可选：turn 完成时回调最终文本
}
```

`Loop.admit` 把 sink 透传进 `promptReq`；run 的 assistant 消息完成（`done`）后若 sink 非 nil，调一次 `sink(ctx, finalText, runID)`。IM 分发器的 sink 闭包：拿 `imKey` → 反查 channel + target → `manager.Send`。**同一聊天对象的上下文连续**由稳定 sessionID 天然保证（如 scheduled task 的 pinned session 一样）。

### 4.4 JSON-RPC handlers（`agent.im.*`）

`gateway.HandlerOptions` 加 `IMHandlers *im.Handlers`（nil 时返回 internal-error，对齐 `ScheduleHandlers` 的现状）。`gateway/handlers.go` dispatch 加 case：

| method | 说明 |
|---|---|
| `agent.im.list` | 通道 + 实例 + 状态快照 |
| `agent.im.get` | 单实例 |
| `agent.im.create` | 建实例（含 channel + config） |
| `agent.im.update` | 改配置 |
| `agent.im.delete` | 删实例 + 停连接 |
| `agent.im.set_enabled` | 启用/停用 |
| `agent.im.test` | 连通性测试 |
| `agent.im.login_start` | 扫码登录开始（qq / weixin） |
| `agent.im.login_poll` | 扫码登录轮询 |

### 4.5 main 端透传（三层模式，复刻 scheduled-tasks）

- **`src/main/libs/imProxy.ts`**：`createIMProxy(client)`，每方法 `client.request('agent.im.<op>', payload)`，仿 `scheduleProxy.ts`。
- **`src/main/index.ts`**：`const imProxy = createIMProxy(client)` + ~12 个 `ipcMain.handle('im:*', ...)`。
- **`src/main/store/EventRouter.ts`**：`handle()` 加 `ImChanged` / `ImStatusChanged` 两个 case → `webContents.send(DarvinPushEvent.ImChanged / ImStatusChanged)`。
- **`src/preload/index.ts`**：暴露 `window.darvin.im.*`（invoke 透传 + `onImChanged` / `onImStatusChanged` 订阅）。

### 4.6 shared API（`src/shared/darvin-api.ts`）

- `DarvinPushEvent` 加：`ImChanged`、`ImStatusChanged`。
- `DarvinEvent` union 加成员：`{ type: 'ImChanged'; payload }`、`{ type: 'ImStatusChanged'; payload }`。
- 类型：`DarvinIMChannel`（qq/wecom/weixin 元数据）、`DarvinIMInstance`（id/name/channel/enabled/config/status）、`DarvinIMStatus`（instances 状态）、`DarvinIMLoginSession`（qr + poll）。
- `DarvinApi` 加：`imList / imGet / imCreate / imUpdate / imDelete / imSetEnabled / imTest / imLoginStart / imLoginPoll / onImChanged / onImStatusChanged`。

### 4.7 renderer

- **composable** `src/renderer/composables/useIm.ts`（singleton，仿 `useSchedules.ts`）：`instances / status` refs，`loadAll / create / update / delete / toggle / test / loginStart / loginPoll`，订阅两个推送事件。
- **视图** `src/renderer/views/ImView.vue`（仿 `ScheduledView.vue`）：左平台列表 + 右平台详情。
- **接线**：`SidebarNav.vue` 加 `{ id: 'im', labelKey: 'sidebar.nav.im', icon: '<message icon>' }`；`useViewMode.ts` ViewMode 加 `'im'`；`AppShell.vue` 加 `case 'im': return ImView`。
- **组件** `src/renderer/components/im/`：`InstanceList.vue`（多实例卡片：状态徽标、启停开关、改名、删除）、`QQForm.vue` / `WecomForm.vue` / `WeixinForm.vue`（凭据 + 访问控制 + 测试）、`QrLoginModal.vue`（二维码 + 轮询 + 过期）。
- **i18n**：`src/renderer/services/i18n.ts` 补 `im.*` key 段（zh/en 同步），侧栏 label 走 `t()`。
- **图标**：`assets/icons/` 复用或新增 message/chat 类 SVG（`stroke="currentColor"`）。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 扫码过期 / 用户取消 | 状态机回到可重扫；UI 提示「二维码已过期，重新生成」 |
| 网络断开（掉线） | QQ WS 指数退避重连；weixin 长轮询失败退避重连；status 置未连接 + lastError |
| 登录 token 失效 | 标记实例 `login_expired`，UI 引导重新扫码 / 重新填凭据；自动停止该实例 |
| 配置错误（凭据无效） | `im.test` 失败给出具体错误；启用时 Start 失败则 status=error + lastError 展示 |
| 长回复超出 IM 上限 | 按通道协议分块（QQ 5000 字符级）；v1 不分块则截断并注明 |
| 同一聊天对象并发消息 | 每 peer 串行队列（对齐 qqbot）；IM 会话 key 保证同一对象排队 |
| 多实例同通道 | 每实例独立连接 + 独立 token 缓存；实例级启停互不影响 |
| 用户删实例 | 停连接 → 删配置；已产生的 darvin session 保留不级联删 |
| Go agent 重启 | 重启后按 enabled 实例自动重连（配置持久化在 Go DB）；token 持久化免重扫 |
| 群聊 | QQ/WeCom 仅处理 @ Bot 的消息；weixin 不做群聊（协议不支持） |
| 禁言 / 限流 / 频控 | 退避 + 重试上限；持续失败自动停用并提示（对齐 schedule 的 maxAttempts 语义） |
| 跨平台（Win/Mac/Linux） | iLink 纯 HTTP 无平台依赖；QQ WS 依赖 `gorilla/websocket` 已跨平台；路径走 `user-paths` |

## 6. 涉及文件

### Go（darvin-agent）

| 文件 | 变更说明 |
|------|---------|
| `internal/im/contract.go` | 新增：Instance / InboundMessage / Outbound / Target / Status 契约 |
| `internal/im/manager.go` | 新增：实例生命周期 + 入站分发 + 出站路由 + 事件广播 |
| `internal/im/handlers.go` | 新增：`agent.im.*` JSON-RPC handlers |
| `internal/im/qrlogin.go` | 新增：扫码登录状态机 |
| `internal/im/qq/*.go` | 新增：QQ 连接器（token/WS/REST） |
| `internal/im/wecom/*.go` | 新增：企业微信连接器 |
| `internal/im/weixin/*.go` | 新增：iLink 连接器 |
| `internal/agents/store/imstore.go` | 新增：IMChannelStore / IMSessionMappingStore（GORM） |
| `internal/runtime/database.go` | 修改：`AutoMigrate` list 注册新表 + `Stores` 加 store 句柄 |
| `internal/sessionruntime/loop.go` | 修改：`PromptRequest` 加可选 `ReplySink` |
| `internal/gateway/sessionmgr.go` | 修改：加 `SubmitForIM` |
| `internal/gateway/handlers.go` | 修改：`HandlerOptions` 加 `IMHandlers` + dispatch case |
| `internal/runtime/runtime.go` | 修改：装配 Manager + Handlers，启动/停止 |
| `go.mod` | 修改：加 `gorilla/websocket`（若当前未在 agent 侧）/ WeCom SDK |

### 主进程 / 共享 / preload

| 文件 | 变更说明 |
|------|---------|
| `src/main/libs/imProxy.ts` | 新增：`agent.im.*` 透传 |
| `src/main/index.ts` | 修改：`ipcMain.handle('im:*')` + imProxy 装配 |
| `src/main/store/EventRouter.ts` | 修改：`ImChanged` / `ImStatusChanged` 转发 |
| `src/shared/darvin-api.ts` | 修改：DarvinApi 方法 + DarvinPushEvent + DarvinEvent + 类型 |
| `src/preload/index.ts` | 修改：暴露 `window.darvin.im.*` + 事件订阅 |

### renderer

| 文件 | 变更说明 |
|------|---------|
| `src/renderer/composables/useIm.ts` | 新增：singleton 状态 + CRUD + 推送订阅 |
| `src/renderer/views/ImView.vue` | 新增：平台列表 + 详情 |
| `src/renderer/components/im/InstanceList.vue` | 新增：多实例卡片 |
| `src/renderer/components/im/QQForm.vue` | 新增 |
| `src/renderer/components/im/WecomForm.vue` | 新增 |
| `src/renderer/components/im/WeixinForm.vue` | 新增 |
| `src/renderer/components/im/QrLoginModal.vue` | 新增 |
| `src/renderer/layout/AppShell.vue` | 修改：`case 'im'` |
| `src/renderer/components/sidebar/SidebarNav.vue` | 修改：nav item |
| `src/renderer/composables/useViewMode.ts` | 修改：`ViewMode` 加 `'im'` |
| `src/renderer/services/i18n.ts` | 修改：`im.*` key 段 |

## 7. 验收标准

- [ ] Go 侧：`cd src/darvin-agent && go build ./... && go vet ./...` 通过，无新增 lint 告警
- [ ] `agent.im.list` 返回 QQ / WeCom / Weixin 三个通道的实例列表（含状态快照）
- [ ] 场景 1（QQ）：填 appId/secret 启用后状态「已连接」，私聊消息收到回复（手动验证，需真实 Bot 凭据）
- [ ] 场景 2（WeCom）：配置启用后成员对话收到回复（手动验证）
- [ ] 场景 3（个人微信）：扫码登录成功、状态「已连接」、私聊收到回复；token 持久化重启免扫码（手动验证）
- [ ] 场景 4：IM 会话 key 稳定，同一聊天对象上下文连续（多轮对话）
- [ ] 场景 5：`ImStatusChanged` 推送驱动 UI 徽标实时更新；错误/登录失效有明确展示
- [ ] `npm run lint` 通过（renderer / main / preload 改动）
- [ ] `npm test` 通过（vitest 单测，覆盖新增纯函数 / wire 类型 / 事件判别）
- [ ] i18n：`im.*` key 在 dictZh / dictEn 均登记，`assertSameKeys` 通过
- [ ] 手动：`npm start` 起应用，侧栏出现「IM 通道」，切换视图无 console 报错
- [ ] 通道可扩展：新通道只需实现 `Instance` 契约并在 runtime 装配加几行（对齐 scheduledtask 显式模式），不改 `internal/im/` 主装配核心
