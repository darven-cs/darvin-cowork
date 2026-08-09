# mcp types.go 领域拆分 设计文档

## 1. 概述

### 1.1 问题 / 动机

`src/darvin-agent/internal/mcp/types.go`(244 行)是一个集中放置类型的 `types.go` 垃圾抽屉,直接违反 `CLAUDE.md` 的 **F5 规则**:

> **F5 文件按"类型 + 操作"组织,禁建 `types.go` / `utils.go` 垃圾抽屉** — struct / const / interface 跟它的领域逻辑放在一起;不建一个集中放类型的 `types.go`。

该文件把 6 个互不相干的业务域塞进同一个文件,读者看任一类型都得跨域翻找,且与同包其他按域命名的文件(client / launcher / registry / registry_resolve / resolver_fingerprint / persistence / notifier / sanitize)风格不一致。

附带发现的问题:

1. **生产死代码**:`StartupFailure` 结构体、`RedactCredentials` + `credentialRE` + `credentialPlaceholder` 脱敏单元在生产代码零引用,只有测试构造/调用;`LaunchResolution.Failure*` 字段生产里也从没被 set。
2. **`ConnectionStatus`(notifier.go)缺 doc 注释**:导出类型无 godoc,违反 N2"导出必须有 doc"。
3. **`mcp.ErrTransportClosed` 与 `transport.ErrTransportClosed` 重复定义**:语义重复,消息差一个空格。

### 1.2 目标

1. 按 F2 / F5 领域拆分 `types.go`,消除垃圾抽屉。
2. 删除生产死代码(经用户确认)。
3. **零行为变化**:纯 refactor,不改变任何导出 API 签名、JSON 标签、IPC / wire 字节。

### 1.3 非目标

- **不改导出 API 形状**:所有保留的类型 / 常量 / 字段定义原样搬家,只改文件归属。
- **不接线 RedactCredentials**:launcher 里 credentials 脱敏从未实现(连 `Failure*` 字段都没 set),"接线"属于 bugfix/feature,不在本 refactor 范围。
- **不动 transport 子包**:`transport.ErrTransportClosed` 保持不变;只说明与 mcp 包错误的关系。
- **不动其他包**:本 spec 仅限 `internal/mcp/`。

## 2. 现状分析

### 2.1 types.go 内容与归属(2026-08-09 全量扫描)

| 业务域 | 类型 / 常量 | 生产消费方 | 拆分去向 |
|---|---|---|---|
| JSON-RPC wire 信封 | `ProtocolVersion`、`ErrTransportClosed`/`ErrMethodNotFound`/`ErrRPCMaxRetries`/`ErrNoReconnectFactory`、`RPCError`(+`Error()`)、`Request`、`Response`、`InitializeResult`、`ServerInfo` | `client.go`(构造 Request / unmarshal Response / errors.Is) | 新文件 `wire.go` |
| MCP tool 描述 | `ToolDescriptor`、`CallToolResult`、`ToolContent` | `client.go`(ListTools/CallTool)、`registry.go`、`registry_resolve.go` | 新文件 `tool.go` |
| 服务器配置 / 传输选择 | `ServerSpec`、`TransportType` + `TransportStdio/HTTP/SSE` | `registry.go`、`launcher.go`、`registry_resolve.go`、`resolver_fingerprint.go` | 新文件 `server_spec.go` |
| Resolver / Launcher | `ResolverKind` + 4 常量、`ResolutionStatus` + 5 常量、`LaunchResolution` | `launcher.go`、`registry.go`、`registry_resolve.go`、`persistence.go`、`notifier.go`、`resolver_fingerprint.go` | 并入 `launcher.go` |
| 服务器运行时状态 | `ServerStatus` | 仅 `registry.go` | 并入 `registry.go` |
| 凭证脱敏 | `credentialRE`、`credentialPlaceholder`、`RedactCredentials` | **生产零引用**;仅 `types_json_test.go`、`registry_enhance_test.go` | **删除** |
| 启动失败快照 | `StartupFailure` | **生产零引用**;仅 `types_json_test.go` 构造 | **删除** |

### 2.2 死代码确认

| 符号 | 生产引用 | 测试引用 | 处理 |
|---|---|---|---|
| `StartupFailure` | 无 | `types_json_test.go:117 TestStartupFailure_Fields` | 删除(含测试) |
| `RedactCredentials` | 无 | `types_json_test.go` 4 处 + `registry_enhance_test.go:70 TestRedactCredentials_EveryPattern` | 删除(含测试) |
| `credentialRE` / `credentialPlaceholder` | 无(仅被 `RedactCredentials` 内部用) | — | 随 RedactCredentials 删除 |

### 2.3 其他

- **package doc 位置**:`types.go:1` 持有 `// Package mcp ...`,拆分后需迁到主文件。
- **`ErrMethodNotFound`**:生产零引用,但属导出错误哨兵组,保留、归入 `wire.go`,不当作死代码删。
- **`ErrTransportClosed` 重复**:`mcp` 包(client.go:71,236 用)与 `transport` 包(子包内部用)各自为政,当前无实际冲突;统一需改 client 语义,超出本 spec 范围,保留现状。

## 3. 方案设计

按 F2「按业务域拆分、不按语法元素」执行:不是"一个类型一个文件",而是**类型跟随它的领域逻辑文件**。

### 3.1 新建 3 个领域文件

- **`wire.go`** — JSON-RPC 信封:file-level comment + `ProtocolVersion`、4 个错误哨兵、`RPCError`(含 `Error()`)、`Request`、`Response`、`InitializeResult`、`ServerInfo`。
- **`tool.go`** — MCP 工具描述:`ToolDescriptor`、`CallToolResult`、`ToolContent`。
- **`server_spec.go`** — 服务器配置与传输选择:`ServerSpec`、`TransportType` + `TransportStdio/HTTP/SSE`。

### 3.2 并入现有领域文件

- **`launcher.go`**(477 → 约 530 行,仍 <800 软上限):并入 `ResolverKind` + 常量、`ResolutionStatus` + 常量、`LaunchResolution`。
- **`registry.go`**(309 → 约 325 行):并入 `ServerStatus`。

### 3.3 删除死代码

- 删 `StartupFailure` 结构体 + `types_json_test.go: TestStartupFailure_Fields`。
- 删 `RedactCredentials` / `credentialRE` / `credentialPlaceholder` + `types_json_test.go` 4 个 `TestRedactCredentials_*` + `registry_enhance_test.go: TestRedactCredentials_EveryPattern`。

### 3.4 补 doc

- `notifier.go` 的 `ConnectionStatus` 补 godoc(及其 4 个常量注释),对齐 N2。

### 3.5 package doc 迁移

- 拆分后 `types.go` 删除,`// Package mcp ...` 迁至 `client.go` 顶部(包的对外门面 / 主文件),原 file-level comment 合并进 package doc 之后。

## 4. 实施步骤

1. **建 `wire.go`**:搬 wire 信封 9 个符号 + file-level comment,从 `types.go` 删。
2. **建 `tool.go`**:搬 3 个 tool 描述类型。
3. **建 `server_spec.go`**:搬 `ServerSpec` + `TransportType` 常量。
4. **并入 `launcher.go`**:搬 `ResolverKind` / `ResolutionStatus` / `LaunchResolution`。
5. **并入 `registry.go`**:搬 `ServerStatus`。
6. **删死代码**:删 `StartupFailure` + 脱敏单元(生产);删 6 个测试函数(测试)。
7. **补 doc**:`ConnectionStatus` + 常量。
8. **迁 package doc**:`client.go` 顶部改挂 `// Package mcp ...`,删 `types.go`。
9. **归位与验证**:`goimports -w -local darwin-cowork/ .` 重排 import(删掉 `regexp` / `strings` 等脱敏单元独占的依赖),跑 §6 全链。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/mcp/types.go` | 删除(拆分后空壳) |
| `internal/mcp/wire.go` | 新建:wire 信封 9 符号 |
| `internal/mcp/tool.go` | 新建:tool 描述 3 类型 |
| `internal/mcp/server_spec.go` | 新建:`ServerSpec` + `TransportType` |
| `internal/mcp/launcher.go` | 并入 `ResolverKind`/`ResolutionStatus`/`LaunchResolution` |
| `internal/mcp/registry.go` | 并入 `ServerStatus` |
| `internal/mcp/client.go` | 顶部改挂 package doc |
| `internal/mcp/notifier.go` | `ConnectionStatus` + 常量补 doc |
| `internal/mcp/types_json_test.go` | 删 5 个测试(`TestStartupFailure_Fields` + 4 个 `TestRedactCredentials_*`) |
| `internal/mcp/registry_enhance_test.go` | 删 `TestRedactCredentials_EveryPattern` |

## 6. 验证计划

1. `gofmt -l .` 与 `goimports -l .` 输出为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警(新文件 F3 注释、导出符号 doc)。
4. `golangci-lint run ./...` 输出 `0 issues`(不允许新增;此前 baseline 已清零)。
5. `go test ./internal/mcp/...` 全绿(删除 6 个测试后其余全过)。
6. `go test ./...` 全绿(全仓回归)。
7. `bash scripts/check-agent-readability.sh` 通过(F1/F3/F5/C3 + baseline)。
8. `npm run build:agent` 成功产出 `bin/darvin-agent-<platform>-<arch>`(编译链完整)。
9. 抽查 diff:确认所有保留类型定义字节不变(仅换文件),无隐藏的导出 API 变更。
