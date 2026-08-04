# Provider: Azure OpenAI 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

Azure OpenAI 把 OpenAI 的 Chat/Responses 端点换成部署级 URL：

`https://{resource}.openai.azure.com/openai/deployments/{deployment}/chat/completions?api-version=2024-08-01-preview`

并增加 `api-key` header（独立于 Bearer）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 鉴权：可选 Bearer 或 api-key header | config |
| G2 | 部署级路由 | URL 模板 |
| G3 | api-version 显式配置 | config |
| G4 | Responses / Chat 两种端点 | spec 子选择 |
| G5 | extra_headers：用户可加 `Ocp-Apim-Subscription-Key` 等 | 透传 |
| G6 | 错误归一化（含 429 DeploymentNotFound） | errors.go |
| G7 | ≥ 10 测试场景 | |

### 1.3 非目标

- 不实现 Azure 容器推理 / Phi-3 等非 OpenAI 系列模型。
- 不接入 Azure AD（仅 api-key + AAD token 由用户侧准备）。
- 不实现 Azure Speech TTS / STT。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-openai/` | Chat 协议基础 |
| `specs/features/provider-openai-responses/` | Responses 协议基础 |

## 3. 用户/系统场景

### 场景 1：部署级路由

**Given** 配置 `resource=myres`、`deployment=gpt4o`、`apiVersion=2024-08-01-preview`
**When** Chat 请求
**Then** URL = `https://myres.openai.azure.com/openai/deployments/gpt4o/chat/completions?api-version=2024-08-01-preview`

### 场景 2：api-version 缺省

**Given** settings 未填 apiVersion
**When** Provider 启动
**Then** 走默认 `2024-08-01-preview` 并 warning 日志

### 场景 3：401

**Given** 错误 api-key
**When** 调用
**Then** 归一为 `ErrProviderUnauthorized`

## 4. 功能需求

### FR-1 配置字段

```go
type AzureConfig struct {
    Resource    string
    Deployment  string
    APIVersion  string // 默认 2024-08-01-preview
    AuthMode    string // "apikey" | "bearer" | "aad"
    APIKey      string
    EndpointKind string // "chat" | "responses"
}
```

### FR-2 URL 模板

```
{base}/openai/deployments/{deployment}/{chat|responses}?api-version={apiVersion}
```

`base` 默认 `https://{resource}.openai.azure.com`；可 override。

### FR-3 鉴权

| 模式 | header |
|---|---|
| apikey | `api-key: <key>` |
| bearer | `Authorization: Bearer <token>` |
| aad | `Authorization: Bearer <aad-token>`（runtime 仅透传） |

### FR-4 错误归一化

| HTTP | 处理 |
|---|---|
| 400 | `ErrProviderBadRequest`（deployment not found / role assignment 等） |
| 401 | `ErrProviderUnauthorized` |
| 403 | `ErrProviderForbidden`（access denied, content filter） |
| 404 | `ErrProviderDeploymentNotFound` |
| 429 | `ErrProviderRateLimited` + Retry-After |
| 5xx | `ErrProviderServer` |

### FR-5 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | 部署级 URL 拼接 |
| T2 | apikey 鉴权 |
| T3 | bearer 鉴权 |
| T4 | aad 鉴权 |
| T5 | api-version 默认值 |
| T6 | Chat / Responses 切换 |
| T7 | 401 |
| T8 | 404 DeploymentNotFound |
| T9 | 429 + Retry-After |
| T10 | extra_headers 透传 |
| T11 | SSE 流 |

## 5. 安全与隐私

- api-key 不进日志。
- 使用 `api-version` 必填最少字段，避免 race condition。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| api-version 不支持 | 提示用户升级 |
| deployment 不存在 | fail-fast |
| 限流 | Failover |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/azure/azure.go` | 主实现 |
| `url.go` | URL 模板 |
| `errors.go` | 错误归一 |
| `mapping.go` | 字段 |

## 8. 实施顺序与依赖

1. url.go + mapping.go
2. errors.go
3. 主串联

> 前置：`specs/features/provider-openai/` 与 `openai-responses/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- provider-azure` |
| V5 | dev 手工验证 deployment 路由 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Azure AI Foundry 多服务（v2）。
- Azure Speech / Vision 集成（独立 spec）。
