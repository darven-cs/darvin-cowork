# agentloop helpers.go 归并 设计文档

## 1. 概述

### 1.1 问题 / 动机

`src/darvin-agent/internal/agentloop/helpers.go`(58 行)是 `CLAUDE.md` F5 点名的反例文件名(`helpers.go` 是垃圾抽屉反例)。内容为 3 个符号,应归并到各自的领域文件。

### 1.2 目标

1. 删除 `helpers.go`,把 3 个符号归并到调用方所在领域文件。
2. **零行为变化**:函数体逐字移动。

### 1.3 非目标

- 不改函数体 / 签名 / error 消息。
- 不新建文件。

## 2. 现状分析

| 符号 | 定义 | 调用方 |
|---|---|---|
| `errNoHarness` | helpers.go:16,error 哨兵 | `loop.go:350`(harness 为 nil 时发 AgentErrorEvent) |
| `attachmentsToImages` | helpers.go:22,ImageRef → ImageAttachment 转换 | `loop.go:358`(组装 RunAttemptParams.Images) |
| `extractProviderName` | helpers.go:42,读 provider 名 | `factory.go:115`(`resolveHarnessFor` 选 harness)、`loop.go:360` |

## 3. 方案设计

按调用方所在文件归并(归属即消费侧):

| 符号 | 去向 | 理由 |
|---|---|---|
| `errNoHarness` | `loop.go` | 唯一使用方是 loop 的 turn 执行(harness 检查) |
| `attachmentsToImages` | `loop.go` | 唯一调用方是 loop 的 RunAttemptParams 组装 |
| `extractProviderName` | `factory.go` | `resolveHarnessFor`(factory 域)直接调用;loop.go 复用 |

`hydrate.go` 是消息历史恢复域,与三者均不相关,不并入。预估行数:`loop.go` 372 → 约 392;`factory.go` 166 → 约 183;均 <800(F1)。

helpers.go 删除后,`agent` / `harness` / `errors` import 随函数迁移:`loop.go` 需 `agent` / `harness`,`factory.go` 需 `agent`(均已在各自文件或用 `goimports` 归位)。

## 4. 实施步骤

1. `factory.go` 末尾并入 `extractProviderName`。
2. `loop.go` 末尾并入 `errNoHarness` + `attachmentsToImages`。
3. 删除 `helpers.go`。
4. `goimports -w -local darwin-cowork/ .` 归位,跑 §5 验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/agentloop/helpers.go` | 删除 |
| `internal/agentloop/factory.go` | 并入 `extractProviderName` |
| `internal/agentloop/loop.go` | 并入 `errNoHarness` + `attachmentsToImages` |

## 6. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警。
4. `golangci-lint run ./...` 输出 `0 issues`。
5. `go test ./internal/agentloop/...` 全绿。
6. `go test ./...` 全绿。
7. `bash scripts/check-agent-readability.sh` 通过。
8. 抽查 diff:确认 3 个符号函数体逐字不变。
