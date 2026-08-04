# Media Generation 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化 agent 需要生成 / 编辑图片 / 视频 / 音频。路线图明确要求「5 家 provider 归一化」：

- OpenAI gpt-image-1 / DALL·E 3
- Stability AI
- Midjourney（v6 / v7 API）
- Runway
- Pika

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 5 家 provider 注册 + 归一化接口 | registry |
| G2 | 异步任务模式（create → poll → fetch） | task |
| G3 | 与 cost-and-usage-tracking 联动 | cost |
| G4 | 与 artifact-panel 联动输出 | artifact |
| G5 | 安全过滤（NSFW / PII） | filter |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做本地扩散模型推理（不实接 ComfyUI）。
- 不做视频编辑（独立 spec）。
- 不做实时多人协同生成（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-registry/` | provider 接口 |
| `specs/features/artifact-panel/` | 输出回流 |
| `specs/features/cost-and-usage-tracking/` | 费用 |

## 3. 用户/系统场景

### 场景 1：图像生成

**Given** user 描述 prompt
**When** agent 调 `media.image.generate(prompt)`
**Then** 选 provider（OpenAI / Stability / Midjourney）；异步任务；产物落 artifact

### 场景 2：异步 poll

**Given** 任务提交
**When** provider 处于 pending
**Then** 每 5s poll；进度事件

### 场景 3：成本分摊

**Given** image 生成
**When** 完成
**Then** 写 usage_events；report 计入

## 4. 功能需求

### FR-1 provider 接口

```go
type ImageProvider interface {
    ID() string
    Submit(ctx context.Context, req *ImageRequest) (*ImageTask, error)
    Poll(ctx context.Context, taskID string) (*ImageStatus, error)
    Download(ctx context.Context, url string) ([]byte, error)
}

type ImageRequest struct {
    Prompt    string
    NegativePrompt string
    Size      string  // 1024x1024
    Style     string
    N         int
    ReferenceImages []string
}
```

### FR-2 5 家 provider

| provider | 端点 |
|---|---|
| OpenAI | `/v1/images/generations` |
| Stability | `/v2beta/stable-image/generate` |
| Midjourney | third-party relay (Discord/ImagineAPI) |
| Runway | `/v1/tasks` (image-to-image) |
| Pika | `/v1/generate` |

### FR-3 异步任务

```go
type ImageTask struct {
    ID        string
    Status    string  // pending / running / done / failed
    Provider  string
    SubmittedAt time.Time
    Error     string
}
```

runtime 轮询 `Provider.Poll` 直到 status=done / failed。

### FR-4 artifact 输出

```ts
const artifact: Artifact = {
  type: 'image',
  url: 'darvin://workspace/darvin-artifacts/xxx.png',
  mime: 'image/png',
  width: 1024,
  height: 1024,
}
```

引用走 `artifact-panel` 的 `cross-session reference`。

### FR-5 安全过滤

```go
type SafetyFilter struct {
    BlockNSFW  bool
    BlockPII   bool
}

func (s *SafetyFilter) PreCheck(req *ImageRequest) error
func (s *SafetyFilter) PostCheck(content []byte) error
```

### FR-6 计费

归入 `cost-and-usage-tracking`：

```sql
INSERT INTO usage_events(... source='media', provider='openai', ...)
```

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 5 家注册 |
| T2 | 提交任务 |
| T3 | Poll 状态 |
| T4 | 异步取消 |
| T5 | Midjourney relay |
| T6 | Stability 鉴权 |
| T7 | 错误归一 |
| T8 | NSFW 拒 |
| T9 | PII 拒 |
| T10 | artifact 输出 |
| T11 | 成本归集 |

## 5. 安全与隐私

- 提示词不进任何持久化日志。
- 生成内容：用户明确同意后才写入 workspace。
- provider 凭证 AES-GCM。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| Provider 401 | 提示用户 |
| Provider 限流 | backoff |
| NSFW 拒 | UI 提示 |
| Provider 不可达 | 切下一个 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/media/image.go`（新） | interface |
| `src/darvin-agent/internal/media/openai.go`（新） | OpenAI |
| `src/darvin-agent/internal/media/stability.go`（新） | Stability |
| `src/darvin-agent/internal/media/midjourney.go`（新） | Midjourney relay |
| `src/darvin-agent/internal/media/runway.go`（新） | Runway |
| `src/darvin-agent/internal/media/pika.go`（新） | Pika |
| `src/darvin-agent/internal/media/safety.go`（新） | filter |
| `src/shared/darvin-api.ts` | 事件 |

## 8. 实施顺序与依赖

1. `image.go` interface
2. OpenAI / Stability
3. Midjourney relay / Runway / Pika
4. safety + 计费 + artifact

> 前置：`provider-registry` + `cost-and-usage-tracking` + `artifact-panel`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- media-generation` |
| V5 | dev mock 全 5 家 |
| V6 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V7 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 视频 / 音频生成（独立 spec / v2）。
- 实时多人协同（v2）。
