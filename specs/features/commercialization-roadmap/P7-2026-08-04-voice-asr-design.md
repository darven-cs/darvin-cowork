# Voice ASR 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化 user 期望：语音转文字（ASR）。darvin-cowork 当前无语音能力。

实现层：

- Whisper.cpp 本地推理
- OpenAI / Azure Whisper 远端 SaaS
- 阿里 / 讯飞 / 腾讯中文 ASR SaaS

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | ASR provider 抽象 | interface |
| G2 | Whisper.cpp 本地推理 + 模型下载/校验 | local |
| G3 | SaaS ASR providers 转写 | remote |
| G4 | 30 秒中文准确率 ≥ 92%（baseline） | eval |
| G5 | 流式 chunk → 拼装 | streaming |
| G6 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做 TTS（v2）。
- 不做 speaker diarization（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/provider-registry/` | provider 接口可借鉴 |
| `src/darvin-agent/internal/asr/` | 占位 |

## 3. 用户/系统场景

### 场景 1：本地 Whisper

**Given** 配置 `provider=whisper-cpp`
**When** 30s 普通话音频
**Then** 准确率 ≥ 92%

### 场景 2：SaaS

**Given** 配置 OpenAI Whisper API
**When** 上传音频
**Then** 走 OpenAI；返回 transcription

### 场景 3：流式

**Given** 实时麦克风
**When** chunk 60ms
**Then** 增量转写，延迟 < 1s

## 4. 功能需求

### FR-1 provider 接口

```go
type ASRProvider interface {
    ID() string
    Capabilities() ASRCapability
    Transcribe(ctx context.Context, audio []byte) (*Transcript, error)
    TranscribeStream(ctx context.Context, ch <-chan AudioChunk, onDelta func(string) error) error
}
```

### FR-2 能力

| provider | language | streaming | 延迟 | 准确率 |
|---|---|---|---|---|
| whisper-cpp | 99 | no | 1.0x | 92+ |
| openai | 60 | no | 0.5x | 95+ |
| azure | 100 | yes | 0.6x | 93+ |

### FR-3 模型管理

`models.asr.whisper-cpp`：

```json
{
  "modelPath": "/userdata/models/whisper-medium-q5_1.bin",
  "sha256": "...",
  "downloaded": true
}
```

启动时校验 sha256，缺失引导下载。

### FR-4 流式

```go
type AudioChunk struct {
    Pcm       []int16
    SampleRate int
    Timestamp int64
}
```

通过回调 onDelta 拼装。

### FR-5 baseline eval

`scripts/eval-asr.ts`：

- 准备 30s 中文测试集
- 跑 Whisper.cpp 默认模型
- 比较输出 WER (Word Error Rate)

### FR-6 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | whisper-cpp 加载 |
| T2 | 模型校验 |
| T3 | 短音频转写 |
| T4 | 长音频 30s |
| T5 | 流式 chunk |
| T6 | 远端 SaaS |
| T7 | 错误归一 |
| T8 | 限流 |
| T9 | 网络断重连 |
| T10 | PII redact |
| T11 | 多 provider 切换 |

## 5. 安全与隐私

- 音频流不持久化（除非用户显式保存）。
- transcript 仅落 user workspace。
- 大型模型 binary 校验 sha256 防篡改。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 模型损坏 | 重新下载 |
| 远端 401 | 提示更新 key |
| 网络断流 | fail-fast |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/asr/whisper.go`（新） | whisper.cpp 绑定 |
| `src/darvin-agent/internal/asr/openai_asr.go`（新） | OpenAI |
| `src/darvin-agent/internal/asr/registry.go`（新） | ASR provider 选择 |
| `src/darvin-agent/internal/asr/streaming.go`（新） | 流式聚合 |
| `src/shared/darvin-api.ts` | 事件 |
| `scripts/eval-asr.ts`（新） | baseline eval |

## 8. 实施顺序与依赖

1. whisper.cpp 绑定
2. OpenAI SaaS
3. 流式
4. eval

> 前置：`provider-registry` 已确认。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 10 条 |
| V3 | `go vet ./...` |
| V4 | `npm run smoke -- voice-asr` |
| V5 | eval 30s 中文 ≥ 92% |
| V6 | dev 手工：mock 转写 |
| V7 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V8 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- TTS（v2）。
- Speaker diarization（v2）。
