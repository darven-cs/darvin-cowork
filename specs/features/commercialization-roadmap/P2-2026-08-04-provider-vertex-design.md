# Provider: Vertex AI 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

Vertex AI 是 Google Cloud 的企业级 Gemini 托管平台：

- 端点：`https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:generateContent`
- 鉴权：OAuth 2.0 Bearer Token
- 通常使用 Application Default Credentials (ADC) 或 service account JSON
- 协议体与 Gemini 几乎一致

darvin-cowork 必须让用户在企业部署下走 Vertex，而开发 SaaS 走 Gemini 公网。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | region / project 配置 | config |
| G2 | OAuth token 注入 + 自动刷新 | auth provider |
| G3 | 复用 Gemini mapping（DRY） | 共享 |
| G4 | extra_headers 透传 | config |
| G5 | ADC 模式（GOOGLE_APPLICATION_CREDENTIALS 环境变量） | runtime |
| G6 | ≥ 10 测试场景 | |

### 1.3 非目标

- 不支持 Vertex Model Garden 上其他非 Gemini 模型。
- 不实现 Workbench / Pipelines / Vertex Agent Builder。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-gemini/...` | 协议基础（DRY 复用） |
| `src/darvin-agent/internal/provider/gemini/` | mapping |

## 3. 用户/系统场景

### 场景 1：project / region 路由

**Given** 配置 `project=myproj`、`region=us-central1`
**When** generateContent 调用
**Then** URL = `https://us-central1-aiplatform.googleapis.com/v1/projects/myproj/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent`

### 场景 2：ADC 鉴权

**Given** `GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json`
**When** runtime 启动
**Then** 自动 refresh access token；401 时刷新一次再重试

### 场景 3：service account JSON

**Given** settings.json 含 `serviceAccountJSON` 路径
**When** runtime 启动
**Then** 装载凭据 + 缓存 access token

### 场景 4：403

**Given** 鉴权失败
**When** 调用
**Then** 归一为 `ErrProviderUnauthorized`

## 4. 功能需求

### FR-1 配置

```go
type VertexConfig struct {
    Project             string
    Region              string // 默认 us-central1
    AuthMode            string // "adc" | "service_account" | "oauth_token"
    ServiceAccountPath  string // 仅 authMode=service_account
    OAuthToken          string // 可选手工 token（v2 推翻）
}
```

### FR-2 URL 模板

`{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:{action}`

`action` ∈ `generateContent` / `streamGenerateContent`。

### FR-3 Auth

```go
type AuthClient interface {
    AccessToken(ctx context.Context) (string, error)
    Refresh(ctx context.Context) error
}
```

三种实现：

- `ADCAuthClient` 通过 metadata server
- `ServiceAccountAuthClient` 通过 JWT exchange
- `StaticTokenClient`（仅 dev）

### FR-4 复用 Gemini mapping

```go
type VertexProvider struct {
    gemini *GeminiProvider
    auth   AuthClient
    conf   VertexConfig
}
```

请求 / 响应经 `gemini.Mapping`；URL 模板覆盖；auth header 改为 `Authorization: Bearer <token>`。

### FR-5 错误归一化

与 Gemini 一致；额外：

| HTTP | 处理 |
|---|---|
| 401 | `ErrProviderUnauthorized` |
| 403 | `ErrProviderForbidden`（含 `IAM` / `quota` 等） |
| 404 | `ErrProviderModelNotFound`（含 region / publisher miss） |

### FR-6 测试场景 ≥ 10

| # | 场景 |
|---|---|
| T1 | project/region URL 模板 |
| T2 | ADC 模式 token |
| T3 | ServiceAccount JSON 装载 |
| T4 | StaticToken dev 模式 |
| T5 | 401 |
| T6 | 403 含 quota |
| T7 | token 缓存命中 |
| T8 | token refresh retry |
| T9 | stream |
| T10 | tool_use |
| T11 | usage 含 cached |

## 5. 安全与隐私

- Service Account JSON 不进任何日志；`***redacted***`。
- token 在内存持有，进程结束即丢弃。
- 用户上传的 inline_data 走 darvin 的 multi-tenant 文件权限策略。
- 跨 region 路由需要用户在 settings 显式确认。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| token expired | Refresh + retry 一次 |
| SA JSON 损坏 | fail-fast，提示用户 |
| region 不支持 | fail-fast 提示 |
| 403 quota | Failover |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/provider/vertex/vertex.go` | 主实现 |
| `src/darvin-agent/internal/provider/vertex/auth.go` | ADC / SA / Static |
| `url.go` | URL 模板 |
| `errors.go` | 错误归一（重用 gemini/errors） |

## 8. 实施顺序与依赖

1. URL 模板 + ADC client
2. ServiceAccount client
3. StaticToken
4. 主串联（复用 gemini mapping）

> 前置：`specs/features/provider-gemini/` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- provider-vertex` |
| V5 | dev 手工验证（mock） |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- Model Garden（v2）。
- Agent Builder / Workbench（v2）。
