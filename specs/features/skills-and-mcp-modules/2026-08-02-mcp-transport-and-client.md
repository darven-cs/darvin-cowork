# Sub-spec 34 — MCP Transport & Client（Go 端）

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：Go 端 stdio + http transport 实现 + JSON-RPC 2.0 client。**不包含** launcher / registry / connection pool（spec 35）、main 端 store / IPC（spec 36）、renderer UI（spec 37）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 26 tool-architecture-rework](./../tool-architecture-rework/2026-08-01-tool-architecture-rework-design.md)（plugin loader + session-aware Registry）

---

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 的 Go agent 完全没有 MCP 接入能力。本 spec 在 Go 侧实现 MCP transport（stdio + http）+ JSON-RPC 2.0 client，作为后续 registry / launcher 的基础。

**关键决策**：不引入 `@modelcontextprotocol/sdk`（npm 包，darvin-cowork 不希望 Node 依赖面扩大）。Go 标准库 `net/http` / `bufio` / `encoding/json` 已够用。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | `Transport` interface 抽象（stdio + http 都实现） | 单元测试 mock Transport 跑通 client 流程 |
| G2 | stdio transport：spawn 子进程 + 双向 pipe + Content-Length frame | 单元测试用 fake stdin/stdout 验证 frame 解析 |
| G3 | http transport：POST + optional SSE 接收 | 单元测试用 httptest server 验证 |
| G4 | JSON-RPC 2.0 client：`initialize` / `tools/list` / `tools/call` | 单测覆盖 request/response + error code 解析 |
| G5 | 断开自动重连：指数退避最多 3 次 | 单测模拟断开验证 |

### 1.3 非目标

- 不做 SSE transport（v1 暂不实现；SSE 走 http 流式但不解析 SSE 事件，回退到 polling）
- 不做 OAuth / auth（v1）
- 不做 launcher / registry / connection pool（spec 35）
- 不做 main 端 store（spec 36）

---

## 2. 用户场景

### 场景 1：stdio transport 连接成功

**Given** 配置：command=`npx`，args=`-y @modelcontextprotocol/server-filesystem /tmp`
**When** `client.Connect()` 调用
**Then**：
1. spawn 子进程：`npx -y @modelcontextprotocol/server-filesystem /tmp`
2. 子进程 stdin 写入 `Content-Length: N\r\n\r\n{...initialize request}`
3. 子进程 stdout 读 frame：`Content-Length: N\r\n\r\n{...initialize response}`
4. 解析 JSON-RPC 响应，提取 `protocolVersion` + `capabilities`
5. 调 `tools/list` 拿 tool 列表
6. 连接就绪

### 场景 2：stdio transport 断开

**Given** 已连接的 stdio 子进程，OS 主动 kill（SIGKILL）
**When** client 调 `tools/call`
**Then**：
1. write 失败 → 检测到 EOF
2. 触发重连（指数退避 1s / 2s / 4s，最多 3 次）
3. 重连失败 3 次 → 返回 error，标记 transport dead
4. 调用方收到 `ErrTransportClosed`

### 场景 3：http transport 连接成功

**Given** 配置：url=`http://localhost:3001/mcp`
**When** `client.Connect()`
**Then**：
1. POST `http://localhost:3001/mcp` + body=initialize request
2. 解析 response（标准 HTTP JSON）
3. 调 `tools/list`
4. 连接就绪

### 场景 4：http transport 调用工具

**Given** http transport 已连接，tools 列表有 `read_file`
**When** 调 `client.CallTool("read_file", { path: "/tmp/foo.txt" })`
**Then** POST `tools/call` request → 200 OK → 解析 response → 返回 `CallToolResult`

### 场景 5：JSON-RPC error

**Given** 客户端调 `tools/call` 但 server 返回 JSON-RPC error code=-32601（method not found）
**When** 接收响应
**Then** 返回 `*RPCError{Code: -32601, Message: "method not found"}`

---

## 3. 功能需求

### FR-1: Transport Interface

```go
// internal/mcp/transport/transport.go
package transport

import (
    "context"
    "io"
)

type Frame struct {
    Body []byte  // 完整 JSON-RPC message
}

type Transport interface {
    // Connect 建立 transport
    Connect(ctx context.Context) error

    // Send 写入一帧
    Send(ctx context.Context, body []byte) error

    // Recv 阻塞读取一帧；EOF 时返回 io.EOF
    Recv(ctx context.Context) (Frame, error)

    // Close 关闭 transport
    Close() error

    // Alive 检查 transport 是否存活
    Alive() bool
}
```

### FR-2: stdio Transport

```go
// internal/mcp/transport/stdio.go
package transport

type StdioTransport struct {
    Command string
    Args    []string
    Env     map[string]string

    cmd     *exec.Cmd
    stdin   io.WriteCloser
    stdout  io.ReadCloser
    stderr  io.ReadCloser
    alive   atomic.Bool
    mu      sync.Mutex
}

func (s *StdioTransport) Connect(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    cmd := exec.CommandContext(ctx, s.Command, s.Args...)
    cmd.Env = os.Environ()
    for k, v := range s.Env {
        cmd.Env = append(cmd.Env, k+"="+v)
    }

    stdin, err := cmd.StdinPipe()
    if err != nil { return err }
    stdout, err := cmd.StdoutPipe()
    if err != nil { return err }
    stderr, err := cmd.StderrPipe()
    if err != nil { return err }

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("spawn: %w", err)
    }

    s.cmd = cmd
    s.stdin = stdin
    s.stdout = stdout
    s.alive.Store(true)

    // stderr 单独 goroutine 读，写到 zap logger
    go func() {
        scanner := bufio.NewScanner(stderr)
        for scanner.Scan() {
            log.Debug("[mcp-stdio-stderr]", "line", scanner.Text())
        }
    }()

    return nil
}

func (s *StdioTransport) Send(ctx context.Context, body []byte) error {
    if !s.alive.Load() { return ErrTransportClosed }
    header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
    full := append([]byte(header), body...)
    _, err := io.WriteString(s.stdin, string(full))
    return err
}

func (s *StdioTransport) Recv(ctx context.Context) (Frame, error) {
    if !s.alive.Load() { return Frame{}, ErrTransportClosed }

    reader := bufio.NewReader(s.stdout)
    // 读 headers
    var contentLength int
    for {
        line, err := reader.ReadString('\n')
        if err != nil { return Frame{}, err }
        line = strings.TrimRight(line, "\r\n")
        if line == "" { break }  // headers 结束
        if strings.HasPrefix(line, "Content-Length: ") {
            n, err := strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
            if err != nil { return Frame{}, fmt.Errorf("invalid Content-Length: %w", err) }
            contentLength = n
        }
    }
    if contentLength == 0 {
        return Frame{}, errors.New("missing Content-Length header")
    }
    body := make([]byte, contentLength)
    if _, err := io.ReadFull(reader, body); err != nil {
        return Frame{}, err
    }
    return Frame{Body: body}, nil
}

func (s *StdioTransport) Close() error {
    if !s.alive.CompareAndSwap(true, false) {
        return nil
    }
    _ = s.stdin.Close()
    _ = s.stdout.Close()
    if s.cmd != nil && s.cmd.Process != nil {
        _ = s.cmd.Process.Signal(syscall.SIGTERM)
        // 等 5s 再 SIGKILL
        done := make(chan error, 1)
        go func() { done <- s.cmd.Wait() }()
        select {
        case <-done:
        case <-time.After(5 * time.Second):
            _ = s.cmd.Process.Kill()
        }
    }
    return nil
}

func (s *StdioTransport) Alive() bool {
    return s.alive.Load()
}
```

### FR-3: http Transport

```go
// internal/mcp/transport/http.go
package transport

type HTTPTransport struct {
    URL     string
    Headers map[string]string

    client  *http.Client
    sessionID string
    alive   atomic.Bool
    mu      sync.Mutex
}

func (h *HTTPTransport) Connect(ctx context.Context) error {
    h.client = &http.Client{Timeout: 30 * time.Second}
    h.alive.Store(true)
    return nil
}

func (h *HTTPTransport) Send(ctx context.Context, body []byte) error {
    if !h.alive.Load() { return ErrTransportClosed }
    req, err := http.NewRequestWithContext(ctx, "POST", h.URL, bytes.NewReader(body))
    if err != nil { return err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json, text/event-stream")
    for k, v := range h.Headers {
        req.Header.Set(k, v)
    }
    if h.sessionID != "" {
        req.Header.Set("Mcp-Session-Id", h.sessionID)
    }
    resp, err := h.client.Do(req)
    if err != nil { return fmt.Errorf("http: %w", err) }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
    }

    // 解析 response body（v0 不解析 SSE，按 JSON 处理）
    body, err := io.ReadAll(resp.Body)
    if err != nil { return err }
    h.lastResponse = body

    // 如果是 initialize 响应，提取 session id
    if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
        h.sessionID = sid
    }
    return nil
}

func (h *HTTPTransport) Recv(ctx context.Context) (Frame, error) {
    // http transport 的 Send 是同步的；Recv 直接返回上次 Send 的 response
    if h.lastResponse == nil { return Frame{}, ErrTransportClosed }
    return Frame{Body: h.lastResponse}, nil
}

func (h *HTTPTransport) Close() error {
    h.alive.Store(false)
    return nil
}

func (h *HTTPTransport) Alive() bool {
    return h.alive.Load()
}
```

### FR-4: JSON-RPC 2.0 Client

```go
// internal/mcp/client.go
package mcp

import (
    "context"
    "encoding/json"
    "errors"
    "sync"
    "sync/atomic"

    "darvin-cowork/internal/mcp/transport"
)

var (
    ErrTransportClosed = errors.New("transport closed")
    ErrMethodNotFound  = errors.New("method not found")
    ErrTimeout         = errors.New("rpc timeout")
)

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
    return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

type Request struct {
    JSONRPC string `json:"jsonrpc"`
    ID      int64  `json:"id"`
    Method  string `json:"method"`
    Params  any    `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      int64           `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}

type Client struct {
    transport transport.Transport
    nextID    atomic.Int64
    mu        sync.Mutex   // 序列化 Send/Recv（一来一回）
}

func NewClient(t transport.Transport) *Client {
    return &Client{transport: t}
}

func (c *Client) Connect(ctx context.Context) error {
    return c.transport.Connect(ctx)
}

func (c *Client) Close() error {
    return c.transport.Close()
}

// Call 发送 JSON-RPC 请求并等待响应
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if !c.transport.Alive() {
        return nil, ErrTransportClosed
    }

    id := c.nextID.Add(1)
    req := Request{
        JSONRPC: "2.0",
        ID:      id,
        Method:  method,
        Params:  params,
    }
    body, err := json.Marshal(req)
    if err != nil { return nil, err }

    if err := c.transport.Send(ctx, body); err != nil {
        return nil, fmt.Errorf("send: %w", err)
    }

    frame, err := c.transport.Recv(ctx)
    if err != nil {
        return nil, fmt.Errorf("recv: %w", err)
    }

    var resp Response
    if err := json.Unmarshal(frame.Body, &resp); err != nil {
        return nil, fmt.Errorf("unmarshal: %w", err)
    }
    if resp.Error != nil {
        return nil, resp.Error
    }
    return resp.Result, nil
}

// Initialize 调 initialize 方法
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
    params := map[string]any{
        "protocolVersion": "2024-11-05",
        "capabilities": map[string]any{
            "roots": map[string]any{},
        },
        "clientInfo": map[string]any{
            "name":    "darvin-cowork",
            "version": "0.1.0",
        },
    }
    raw, err := c.Call(ctx, "initialize", params)
    if err != nil { return nil, err }
    var result InitializeResult
    if err := json.Unmarshal(raw, &result); err != nil { return nil, err }
    return &result, nil
}

// ListTools 调 tools/list
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
    raw, err := c.Call(ctx, "tools/list", nil)
    if err != nil { return nil, err }
    var result struct {
        Tools []ToolDescriptor `json:"tools"`
    }
    if err := json.Unmarshal(raw, &result); err != nil { return nil, err }
    return result.Tools, nil
}

// CallTool 调 tools/call
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
    params := map[string]any{
        "name":      name,
        "arguments": args,
    }
    raw, err := c.Call(ctx, "tools/call", params)
    if err != nil { return nil, err }
    var result CallToolResult
    if err := json.Unmarshal(raw, &result); err != nil { return nil, err }
    return &result, nil
}

type InitializeResult struct {
    ProtocolVersion string         `json:"protocolVersion"`
    Capabilities    map[string]any `json:"capabilities"`
    ServerInfo      struct {
        Name    string `json:"name"`
        Version string `json:"version"`
    } `json:"serverInfo"`
}

type ToolDescriptor struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

type CallToolResult struct {
    Content []ToolContent `json:"content"`
    IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
    Type string `json:"type"`
    Text string `json:"text"`
    // Data, MimeType 可选（image / audio），v0 不实现
}
```

### FR-5: 重连机制

```go
// internal/mcp/client.go
func (c *Client) CallWithRetry(ctx context.Context, method string, params any, maxRetries int) (json.RawMessage, error) {
    backoff := 1 * time.Second
    var lastErr error
    for i := 0; i <= maxRetries; i++ {
        raw, err := c.Call(ctx, method, params)
        if err == nil { return raw, nil }
        lastErr = err

        if errors.Is(err, ErrTransportClosed) || isConnectionError(err) {
            // 尝试重连
            if err := c.reconnect(ctx); err != nil {
                lastErr = err
            } else {
                continue
            }
        } else {
            // RPC error / 参数错等，不重试
            return nil, err
        }
        time.Sleep(backoff)
        backoff *= 2
    }
    return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (c *Client) reconnect(ctx context.Context) error {
    _ = c.transport.Close()
    // 重新构造 transport（调用方负责提供 factory）
    if c.reconnectFactory != nil {
        c.transport = c.reconnectFactory()
        return c.transport.Connect(ctx)
    }
    return errors.New("no reconnect factory")
}

func isConnectionError(err error) bool {
    return errors.Is(err, ErrTransportClosed) ||
        errors.Is(err, io.EOF) ||
        (err != nil && strings.Contains(err.Error(), "connection refused"))
}
```

### FR-6: cmd 接入（v0 不接 main，仅占位）

```go
// src/darvin-agent/cmd/app/main.go 增量（占位，spec 36 落地）
import "darvin-cowork/internal/mcp"

// 暂时在 main 中只 import，证明编译通过；不真正创建 client
var _ = mcp.NewClient
```

---

## 4. 实现方案

### 4.1 文件清单

```
src/darvin-agent/internal/mcp/
├── transport/
│   ├── transport.go              🆕 Transport interface + Frame + ErrTransportClosed
│   ├── stdio.go                  🆕 StdioTransport
│   ├── http.go                   🆕 HTTPTransport
│   ├── stdio_test.go             🆕 ~150 行
│   └── http_test.go              🆕 ~120 行
├── client.go                     🆕 JSON-RPC Client
├── types.go                      🆕 Request/Response/RPCError/InitializeResult/ToolDescriptor/CallToolResult/ToolContent
├── client_test.go                🆕 ~200 行
└── reconnect_test.go             🆕 ~80 行
```

### 4.2 关键代码片段（见 FR-2 / FR-3 / FR-4）

### 4.3 关键决策与理由

#### 4.3.1 stdio frame 用 Content-Length（LSP / JSON-RPC 标准）

**理由**：所有 MCP server 默认走 LSP 风格 `Content-Length: N\r\n\r\n<body>` 帧格式；MCP 官方协议规范。

#### 4.3.2 不解析 SSE，回退到同步 JSON response

**理由**：SSE 解析需要额外状态机；v0 简化——http transport 的 server 用同步 JSON response。SSE 流式留 v1。

#### 4.3.3 序列化 Send/Recv（互斥锁）

**理由**：JSON-RPC 2.0 over stdio 是同步一问一答；并发调 `Call` 必须串行化。http transport 因为是同步 POST，天然串行。

#### 4.3.4 重连仅在 connection error 时触发

**理由**：RPC error（method not found 等）是 server 端逻辑错，重试无意义；只对 transport 断开的 connection error 重试。

### 4.4 测试策略

| 测试 | 覆盖 |
|------|------|
| `stdio_test.go` | Connect 成功 / Send + Recv frame 正确 / 子进程崩溃检测 / Close 优雅退出 |
| `http_test.go` | httptest server mock；200 OK 解析；500 错误处理 |
| `client_test.go` | Initialize 流程 / ListTools 解析 / CallTool 调用 / RPC error 返回 |
| `reconnect_test.go` | 模拟断开 → 重连 → 重试 3 次失败 |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 子进程立即崩溃 | `cmd.Start()` 返回 nil 但 `cmd.Wait()` 立即结束；stdin pipe 写时检测到 SIGPIPE → `transport.Alive()=false` |
| Content-Length 缺失 / 格式错 | 返回 error + close transport |
| HTTP 5xx | 返回 error；不重试（5xx 通常是 server bug） |
| HTTP timeout（30s） | 返回 error；不重试（避免阻塞过久） |
| 子进程 stderr 输出到 zaps log | 单独 goroutine 读取；用于调试 |
| Transport 重连时 transport 实例重建 | 调用方传 factory function；不直接在 client 内部重建（避免破坏生命周期） |
| JSON-RPC id 冲突 | `nextID atomic.Int64` 单调递增，不可能冲突 |
| response id 不匹配 request id | 返回 error；理论上不应发生 |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/mcp/transport/transport.go` | 🆕 |
| `src/darvin-agent/internal/mcp/transport/stdio.go` | 🆕 |
| `src/darvin-agent/internal/mcp/transport/http.go` | 🆕 |
| `src/darvin-agent/internal/mcp/transport/stdio_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/transport/http_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/client.go` | 🆕 |
| `src/darvin-agent/internal/mcp/types.go` | 🆕 |
| `src/darvin-agent/internal/mcp/client_test.go` | 🆕 |
| `src/darvin-agent/internal/mcp/reconnect_test.go` | 🆕 |
| `src/darvin-agent/cmd/app/main.go` | +import mcp（占位） |

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] 不引入第三方依赖

**FR-1 Transport interface**：
- [ ] Transport interface 4 方法（Connect/Send/Recv/Close）+ Alive
- [ ] Frame 是 body bytes 包装
- [ ] ErrTransportClosed 定义

**FR-2 stdio**：
- [ ] 子进程 spawn 成功
- [ ] Send 写 Content-Length frame
- [ ] Recv 解析 Content-Length + body
- [ ] Close 发 SIGTERM，5s 后 SIGKILL

**FR-3 http**：
- [ ] POST 携带 Content-Type + Accept + 自定义 headers
- [ ] 200 OK 解析
- [ ] 非 200 返回 error
- [ ] `Mcp-Session-Id` header 处理（v0 占位）

**FR-4 Client**：
- [ ] Initialize 协议版本 "2024-11-05"
- [ ] ListTools 解析 tool 列表
- [ ] CallTool 返回 CallToolResult
- [ ] RPCError 包装

**FR-5 重连**：
- [ ] connection error 触发重试
- [ ] RPC error 不重试
- [ ] 指数退避 1s / 2s / 4s
- [ ] 最多 3 次重试

**集成手测**：

```bash
cd src/darvin-agent
cat > /tmp/mcp_check.go <<'EOF'
package main
import (
    "context"
    "fmt"
    "darvin-cowork/internal/mcp"
    "darvin-cowork/internal/mcp/transport"
)
func main() {
    t := &transport.StdioTransport{
        Command: "npx",
        Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
    }
    c := mcp.NewClient(t)
    if err := c.Connect(context.Background()); err != nil {
        fmt.Println("connect err:", err); return
    }
    defer c.Close()
    info, err := c.Initialize(context.Background())
    if err != nil { fmt.Println("init err:", err); return }
    fmt.Println("server info:", info.ServerInfo)
    tools, err := c.ListTools(context.Background())
    if err != nil { fmt.Println("list err:", err); return }
    for _, tool := range tools {
        fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
    }
}
EOF
go run /tmp/mcp_check.go
# 期望输出：filesystem server info + 4 tools
```

---

## 8. 与其他 spec 的关系

**前置**：spec 26 tool-architecture-rework（plugin loader 落地后才能注册 mcp 工具）

**下游依赖**：
- spec 35（mcp-registry-and-launcher）消费本 spec 的 Client + Transport
- spec 36（mcp-main-store-and-ipc）调本 spec 的 Client
- spec 38（tool-registry-merge-and-routing）注册 mcp 工具

**并行**：spec 31 / 32 / 33（skills）不依赖本 spec

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现