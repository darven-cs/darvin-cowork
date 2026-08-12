# MCP SSE 传输完整打通设计文档

## 1. 概述

### 1.1 问题 / 背景

当前 MCP server 支持 stdio（github / context7）与 http（tavily，Streamable HTTP）两种传输，均可用；SSE 全链路不可用，卡在两个独立环节：

1. **页面**：`McpServerFormModal.vue:200` 把 `sse` 选项 `<option disabled>` 置灰，用户无法通过表单添加 SSE 类型 server。
2. **Go 侧 `SSETransport`**（`transport/sse.go`）对接不了标准 MCP-over-SSE（2024-11-05 规范）：
   - `Send` 里 `resp.StatusCode != http.StatusOK` 只认 200。标准 SSE 服务器对 POST 请求只回 **202 Accepted + 空 body**（响应经 GET 流推送），第一步 `initialize` 就返回 `mcp sse: status 202 Accepted`，握手失败。探针测试已实证。
   - 更深层：Send/Recv 同步配对模型承载不了 SSE 的异步响应。标准 SSE 的响应在 **GET 流上以 `message` 事件**推送，POST 只做确认；当前 `Recv` 返回 POST body（空），GET 流上的响应丢给 nil 的 `notificationHandler`。**即使修了 202 也连不通。**

主进程（`mcpStore.ts` / `index.ts`）对 transportType 透传不拦截，共享类型 `DarvinMcpTransportType` 已含 `'sse'`，Go registry（`registry_resolve.go:169`）已会构建 `SSETransport`——这三个环节无需改动。

### 1.2 目标

- 页面可添加 / 编辑 / 删除 SSE 类型 MCP server（表单放开 + `canSave` / save 分支补全）。
- 重写 `SSETransport` 为真实 MCP-over-SSE，完成 连接 → 握手 → 列工具 → 调用工具 → 消费通知 / 答复服务端请求 全流程。
- 参考 DeepSeek-Reasonix `internal/plugin/transport_sse.go` 的语义（GET 流 + `endpoint` 事件 + POST 2xx + 按 id 关联），**但复用 darvin-cowork 现有 stdio transport 的 pending-channel 架构**，不引入新抽象。

### 1.3 非目标

- 不实现 Streamable HTTP 的「POST 202 → 异步 SSE 流」增强（那是 HTTPTransport 的能力边界，本次 SSE 目标为经典 MCP-over-SSE 2024-11-05）。
- 不实现 SSE 的 OAuth 动态授权（参考项目另有 `oauth.go`；本轮认证走静态 headers，与 http 表单一致）。
- 不改 `diagnose.go` 的 auth 诊断（SSE 暂按非 HTTP 处理，维持现状）。
- 不做 SSE session 失效（404）后的自动重握手。

## 2. 用户场景

### 场景 1: 添加 SSE server
**Given** 用户在 MCP 页面点「添加 server」，transport 下拉可选中 `sse`
**When** 选择 sse，填 URL（如 `http://localhost:3001/sse`）+ 可选 headers，点保存
**Then** 卡片出现该 server，状态「连接中」→「已连接」，工具列表展示，**无**「不支持优化」误导报错

### 场景 2: 调用 SSE server 的工具
**Given** SSE server 已连接
**When** agent 调用其某个工具
**Then** 请求经 POST 送达，响应经 GET 流 `message` 事件按 id 关联返回，工具正常执行

### 场景 3: server 推送通知
**Given** SSE server 已连接
**When** server 在 GET 流推送 `notifications/tools/list_changed`
**Then** registry 收到并刷新工具列表（复用现有 `consumeNotifications`）

## 3. 功能需求

### FR-1: 表单放开 SSE
- `McpServerFormModal.vue` 移除 `sse` 选项的 `disabled`。
- `canSave`：`sse` 校验 `url` 非空（同 http）。
- `save` 分支：新增 `sse` 分支，构造 `transportType: 'sse' + url + headers`（create / patch 两态，同 http 逻辑）。
- 模板：url + headers 输入区块条件由 `form.transportType === 'http'` 扩为 `'http' || 'sse'`。

### FR-2: `SSETransport` 支持真实 MCP-over-SSE
- POST 接受**任意 2xx**（202 Accepted 是标准回执），不再以非 200 判死。
- GET 长连接流解析 SSE 帧，处理 `endpoint` / `message` 两类事件。
- `endpoint` 事件：解析 POST 目标 URL；相对 URL 基于 GET base URL `ResolveReference`；跨源 endpoint 拒绝。
- `message` 事件：JSON-RPC 载荷带 `id` 且 pending 中有该 id → `dispatchResponse`；带 `id` 且无 pending（服务端请求，如 ping / roots/list）→ 进 `Inbound()`；无 `id`（通知）→ 进 `Inbound()`。
- 实现 `Inbound() <-chan Frame` + `SendRaw(body)`，使 Client 现有 `inboundLoop` / `replyToRequest` / `sendReply` 自动生效，答复服务端请求。
- 捕获 `Mcp-Session-Id`（GET 响应头），随 POST 回显。
- GET 流断开：alive 保持，退避重连（沿用现 `streamLoop` 思路）。

### FR-3: registry 接线
- `connectServer` 对 `TransportSSE` 构建重写后的 `SSETransport`，透传 `Logger`（对齐 `buildStdioTransport`）。

## 4. 实现方案

### 4.1 `SSETransport` 重写（对齐 stdio 架构）

**现状 stdio 的成熟模型（直接复用思路）**：`Send` 提取 JSON-RPC id → 注册 `pending[id]` → 写帧 → **阻塞等响应**（reader goroutine 按 id 路由回 channel）→ 存 `lastFrame`；`Recv` 返回 `lastFrame`。SSE 只把「写 stdin / 读 stdout」换成「POST 到 endpoint / 读 GET 流」。

```go
type SSETransport struct {
	URL     string
	Headers map[string]string
	Logger  *zap.Logger

	client  *http.Client
	alive   atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc

	mu         sync.Mutex
	nextID     int64
	pending    map[int64]chan Frame
	lastFrame  Frame
	endpoint   *url.URL   // POST 目标（endpoint 事件后更新，默认 = URL）
	sessionID  string
	inbound    chan Frame // 服务端请求 / 通知
}

func (s *SSETransport) Connect(ctx context.Context) error // 起 GET 流 goroutine，初始化 inbound
func (s *SSETransport) streamLoop()                        // GET URL，解析 SSE，路由事件
func (s *SSETransport) handleEvent(name, data string)      // endpoint / message 分发
func (s *SSETransport) dispatchResponse(id int64, f Frame) bool // pending 命中返回 true
func (s *SSETransport) pushInbound(f Frame)                // 不阻塞，满则丢弃（仿 stdio）
func (s *SSETransport) Send(ctx, body) error               // 注册 pending → POST(2xx) → 等 channel
func (s *SSETransport) Recv(ctx) (Frame, error)            // 返回 lastFrame
func (s *SSETransport) SendRaw(body) error                 // POST 应答（答复服务端请求）
func (s *SSETransport) Inbound() <-chan Frame              // 供 Client inboundLoop 消费
func (s *SSETransport) Close() error                       // cancel + 关闭
func (s *SSETransport) Alive() bool
```

要点：

- `Send` 在注册 pending 后 POST，POST 返回 2xx 即视为送达，随后**阻塞等待** GET 流上对应 id 的 `message` 事件（带 ctx 超时）。POST 失败时清理 pending。
- **`post` 先等 `endpoint` 事件**：`endpointReady` 通道 + `sync.Once`，`setEndpoint` 首次成功时 close。`endpoint` 事件是 SSE 流的第一个帧，若在它被解析前就 POST 到 GET URL，会被只接受 GET 的服务器（如 MCP SDK 的 `/sse`）回 404。实现中对 SDK 服务器实证过该竞态，已加等待。
- `streamLoop` 用 `Accept: text/event-stream` GET，SSE 帧解析复刻现 `readSSEStream`（event / data 字段、空行 flush、注释行跳过）。
- Client 侧**零改动**：`client.go` 的 `inboundLoop` 以 `transport.(inboundReader)` 判别，SSETransport 实现 `Inbound()` 后自动启用通知 / 服务端请求处理；`handleInbound` → `replyToRequest` → `sendReply` → `SendRaw` 链路现成。

### 4.2 表单放开（`McpServerFormModal.vue`）

```html
<option value="sse">sse</option>
```
```ts
if (form.value.transportType === 'sse') return form.value.url.trim().length > 0;
```
save 分支补 `sse`（url + headers 同 http）；模板 url+headers 区块条件扩为 `'http' || 'sse'`。i18n `mcp.transport.sse` 已存在，无需加 key。

### 4.3 测试

新增 `transport/sse_test.go`，用 `httptest` 模拟标准 MCP-over-SSE server（GET 流先发 `endpoint` 事件；POST 回 202；POST 后把对应 `message` 事件写入 GET 流）：

1. `Connect` + `Send(initialize)` 接受 202 不报错。
2. `Recv` 拿到 GET 流上按 id 关联的响应体。
3. 无 id 的 `message` 事件进 `Inbound()`（通知路径）。
4. 带 id 且无 pending 的 `message`（如 `ping`）进 `Inbound()`，`SendRaw` 回包成功。
5. 相对 URL 的 `endpoint` 事件基于 GET base 解析。

`launcher_test.go` 已含 `TransportSSE → StatusReady` 用例（上轮 HTTP 修复时新增 `TestPickResolver_NonStdio_ResolvesReady`），无需新增。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| GET 流断开 | alive 保留，1s 退避重连（沿用现 streamLoop） |
| `endpoint` 事件跨源 | 拒绝并报错（仿参考项目 `sameHTTPOrigin`） |
| `endpoint` 事件缺失 | `post` 一直等 `endpointReady`（随 ctx / POST 超时上抛）；SSE 服务器必须发 endpoint 事件才合规 |
| `endpoint` 为相对 URL | 基于 GET base URL `ResolveReference` |
| POST 返回非 2xx | 返回明确错误，清理 pending |
| Send 超时 / ctx 取消 | 清理 pending，返回 ctx.Err() |
| inbound 通道满 | 丢弃帧（仿 stdio `pushInbound`，不让 reader 阻塞） |
| 服务端请求洪泛 | 不设额外限流，依赖 inbound 满即丢 + Client 串行（本期够用） |
| 会话过期（404） | 本期不做自动重握手，报错上抛（非目标） |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/mcp/transport/sse.go` | 重写：pending-channel 模型 + endpoint/message 事件 + 2xx + Inbound/SendRaw |
| `src/darvin-agent/internal/mcp/transport/sse_test.go` | 新增：httptest 模拟标准 MCP-over-SSE 全流程测试 |
| `src/darvin-agent/internal/mcp/registry_resolve.go` | `connectServer` 对 SSE 透传 `Logger`（小改） |
| `src/renderer/components/mcp/McpServerFormModal.vue` | 放开 SSE 选项 + `canSave` / `save` / 模板补 sse |

## 7. 验收标准

- [ ] 场景 1：页面能添加 SSE server，卡片显示「已连接」、无「不支持优化」报错
- [ ] 场景 2：SSE server 的工具可被 agent 调用成功
- [ ] 场景 3：SSE server 推送 `tools/list_changed` 后工具列表刷新
- [ ] `cd src/darvin-agent && go build ./... && go vet ./...` 零错误
- [ ] `cd src/darvin-agent && go test ./internal/mcp/...` 全绿（含新增 `sse_test`）
- [ ] `npm run build:agent` 重建二进制成功
- [ ] 手动：`npm start` 后用页面添加一个真实 MCP-over-SSE server（如 `mcp` npm 生态 / 本地起一个 SSE 参考 server）验证连接与调用
- [ ] `npm run lint` 通过（renderer 改动）
