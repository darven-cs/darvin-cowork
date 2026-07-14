# backend/AGENTS.md — Go 后端专属

## 技术栈

- Go 1.26+
- stdlib `net/http`（不引入 gin / echo / fiber）
- 后续可能：`modernc.org/sqlite`（纯 Go，无 CGO）

## 命令

```bash
go run ./cmd                    # 启动（监听 127.0.0.1:8080）
go build ./...                  # 全量编译
go test ./...                   # 全量测试
go vet ./...                    # 静态检查
```

## 目录约定

```
backend/
├── cmd/main.go                 ← 入口，组装路由 + 启动 server
├── internal/
│   ├── <domain>/               ← 业务包（每个 domain 一个）
│   │   ├── handler.go          ← HTTP handler
│   │   ├── service.go          ← 业务逻辑
│   │   ├── store.go            ← 持久化（如果需要）
│   │   └── types.go            ← 该 domain 的 struct / interface
│   └── middleware/             ← 跨 domain 中间件
└── go.mod
```

## 新增 handler

1. 在 `internal/<domain>/` 下建文件
2. handler 函数签名：`func (h *Handler) MethodName(w http.ResponseWriter, r *http.Request)`
3. 在 `cmd/main.go` 注册路由
4. 加对应 `handler_test.go`

## 错误处理

```go
// 业务错误
type Error struct {
    Code    string
    Message string
    Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// 在 handler 里
if err != nil {
    http.Error(w, err.Error(), http.StatusBadRequest)
    return
}
```

## 日志

- 用 stdlib `log` 包，prefix 写模块名：`log.Printf("[cowork] ...")`
- 启动 / 关闭用 `log.Println`
- 错误用 `log.Printf` + 错误对象

## 测试

- 单元测试：`handler_test.go` / `service_test.go`
- 用 stdlib `testing` + `httptest`
- 不引入 testify（除非真有必要）
- 表驱动测试优先

## 不要做

- 不要引入 gin / echo / fiber（保持 stdlib）
- 不要用 `init()` 做副作用
- 不要 panic 在主路径
- 不要把 context 漏传
- 不要忽略错误（`_, _ = ...`）
- 不要把时间相关逻辑用 `time.Sleep`，用 channel / ticker
