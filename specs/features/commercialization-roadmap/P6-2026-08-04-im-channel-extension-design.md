# IM Channel: Extension 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

P6 验收仅三家：飞书 / 钉钉 / 企业微信。路图原提案提及 11 个平台，本 spec 把剩余 8 个（Slack / Teams / Telegram / Discord / WhatsApp / LINE / Messenger / iMessage）+ 微信公众号 + WebChat **全部**明示为 v2 范围，并固化扩展点（plugin hook）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 明确列出 v2 候选平台（10 个）与各自阻碍项 | document |
| G2 | 注册 / 退订 / 配置 UI 入口 | extension |
| G3 | 扩展点 channel 接口契约不变 | contract |
| G4 | ≥ 5 单元测试场景 | tests |

### 1.3 非目标

- 不实现 10 个平台的协议。
- 不为单一平台在 P6 投入资源。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/im-channel-abstraction/` | 抽象层（含 Channel interface） |
| `specs/features/im-channel-feishu/` + `dingtalk` + `wecom` | P6 三家 |

## 3. v2 候选平台与阻碍

| 平台 | 进入阻碍 |
|---|---|
| Slack | OAuth + socket mode；需要多租户 routing；token rotation；中文支持弱 |
| Teams | MS Graph SDK；企业租户注册；webhook 加密 |
| Telegram | bot token；公网 bot API；TLS-only；中文支持好 |
| Discord | bot token；gateway 协议；权限管理复杂 |
| WhatsApp | 商业 API 限制；模板消息 + 24h session；BSP 接入 |
| LINE | Messaging API；webhook 验签；中日用户多 |
| Messenger | Meta 商家平台；24h 限制；i18n |
| iMessage | Apple 不开放第三方接入 |
| 微信公众号 | 客服消息接口；主动消息受限；appsecret 加密 |
| WebChat | 自定义协议；arvin 自部署场景预留 |

每个平台需独立 spec 落地，且必须含：

- 鉴权与凭证加密
- 回调验签 + 重放
- 消息类型映射
- 限流与重试
- 撤回 / 编辑幂等

## 4. 扩展点

### FR-1 channel registry

```go
type ChannelDescriptor struct {
    ID          string
    DisplayName string
    Provider    string
    Factory     func(conf ChannelConfig) (Channel, error)
}

var defaultDescriptors = []ChannelDescriptor{
    {ID: "feishu", ...},
    {ID: "dingtalk", ...},
    {ID: "wecom", ...},
    // v2 占位（不实接）
}
```

### FR-2 配置 schema

```json
{
  "channels": {
    "<id>": {
      "enabled": false,
      "credentials": "***redacted***",
      "config": { ... },
      "rateLimit": { "burst": 5, "interval": "1s" }
    }
  }
}
```

未实现的 channel 写入 settings.json 时 fail-fast 验证。

### FR-3 UI

`SettingsPanelChannels.vue`：仅显示 v1 已实现 + v2 占位「即将推出」。

### FR-4 ≥ 5 测试场景

| # | 场景 |
|---|---|
| T1 | 注册 v2 channel descriptor |
| T2 | 写入 enabled=false 通过 |
| T3 | 写入 enabled=true 失败（未实接） |
| T4 | 显示在 UI「即将推出」 |
| T5 | credentials 加密 |

## 5. 安全与隐私

- v2 占位不进 production 路径；避免误用。

## 6. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/darvin-agent/internal/im/extension.go`（新） | 注册与元数据 |
| `src/renderer/components/settings/SettingsPanelChannels.vue`（新） | UI |

## 7. 实施顺序与依赖

1. `extension.go`
2. UI

> 前置：`im-channel-abstraction` + 三家已实现。

## 8. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | Go/TS 单测 ≥ 5 条 |
| V3 | `npm run smoke -- im-channel-extension` |
| V4 | dev 手工：v2 channel 显示「即将推出」 |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 9. v2 实施准入

每个 v2 平台必须：

- 提交对应 spec 至 `specs/features/im-channel-<platform>/`
- CHECKLIST.md 增列 + 状态 `待确认`
- 实施期与当前 P6 三家保持 Channel interface 一致
- 完成平台测试 ≥ 10 条

## 10. 不在范围

- 任何 v2 平台实现（本文仅固化扩展点）。
- 跨平台消息搜索引擎（v2）。
