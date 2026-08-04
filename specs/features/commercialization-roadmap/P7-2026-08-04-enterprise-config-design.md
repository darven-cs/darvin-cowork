# Enterprise Config 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化企业部署需要：

- 强制 Provider（不允许用户切到 OpenAI）
- 强制 IM 平台
- 策略优先级（企业 > 用户）
- 签名下发策略
- UI 水印

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 企业策略优先级（设置覆盖） | priority |
| G2 | 强制 Provider（仅 use X） | force |
| G3 | 禁用 IM / 媒体 / 浏览器等能力 | disable |
| G4 | 签名下发策略 | sign |
| G5 | UI 水印 | watermark |
| G6 | 策略更新拉取（管理员下发） | pull |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不做设备管理 / MDM（v2）。
- 不做合规审计导出（v2）。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `specs/features/oauth-login/` | SSO 基础 |
| `specs/features/provider-registry/` | Provider |
| `src/main/libs/user-settings.ts` | 用户设置 |
| `src/main/libs/enterprise-config.ts` | 占位（推断） |

## 3. 用户/系统场景

### 场景 1：策略下发

**Given** admin 通过 console 推送策略
**When** darvin 拉取
**Then** 签名验证；应用；UI 灰度生效

### 场景 2：强制 Provider

**Given** 策略 `providers.allowed = ['darvin-internal']`
**When** 用户选择 OpenAI
**Then** 设置页面隐藏 OpenAI；不可用

### 场景 3：禁用 IM

**Given** 策略 `channels.all.enabled = false`
**When** 用户打开 IM 设置
**Then** 仅显示「管理员已禁用」

### 场景 4：水印

**Given** 策略 `watermark.text = 'Acme Internal'`
**When** 用户开 app
**Then** 主 canvas 含水印

## 4. 功能需求

### FR-1 优先级

```text
企业策略 > 用户设置 > 默认值
```

实现：

```go
type Settings struct {
    User  UserSettings
    Enterprise EnterpriseSettings // 仅当启用
}

func (s *Settings) Effective() EffectiveSettings
```

### FR-2 强制 Provider

```json
{
  "providers": {
    "allowed": ["darvin-internal"],
    "default": "darvin-internal"
  }
}
```

UI 仅展示白名单。

### FR-3 禁用能力

```json
{
  "capabilities": {
    "im":   { "enabled": false },
    "media":{ "enabled": false },
    "browser":{ "enabled": true, "forceSafeMode": true }
  }
}
```

### FR-4 签名下发

策略更新走 JWT signed by enterprise key：

```go
func verifyEnterprisePolicy(jwt string, publicKey string) (Policy, error)
```

公钥 embedded 在 binary（v1）；v2 走 OAuth。

### FR-5 拉取

```go
type PolicyClient interface {
    FetchPolicy(ctx context.Context) (Policy, error)
}
```

启动时拉取一次；定时刷新（4h）。

### FR-6 水印

CSS：

```html
<div class="absolute inset-0 pointer-events-none text-text-subtle/30">
  {{ policy.watermark.text }}
</div>
```

不阻塞输入。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | 策略优先级 |
| T2 | 强制 Provider |
| T3 | 禁用 IM |
| T4 | 签名校验 |
| T5 | 拉取失败回退 |
| T6 | 水印渲染 |
| T7 | dark / light 水印 |
| T8 | 多 Provider 白名单 |
| T9 | 强制 capability safe |
| T10 | JWT 过期 |
| T11 | 拉取并发 |

## 5. 安全与隐私

- 策略公钥内嵌；私钥仅 admin。
- watermark 可关闭（用户显式请求）；不做 DNS 漏水印。
- 设置变更审计日志。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 拉取失败 | 回退到上版策略 |
| JWT 过期 | 重新拉取 |
| 用户拒绝 | 在 settings 选「退出企业模式」则退出账号 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/main/libs/enterprise-config.ts`（新） | 策略加载 |
| `src/main/libs/policy-signature.ts`（新） | 签名校验 |
| `src/main/libs/user-settings.ts` | 合并 effective |
| `src/renderer/components/settings/SettingsPanelEnterprise.vue`（新） | UI |
| `src/renderer/components/canvas/Watermark.vue`（新） | 水印 |

## 8. 实施顺序与依赖

1. `enterprise-config.ts`
2. `policy-signature.ts`
3. UI 合并 + 水印

> 前置：`oauth-login`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | TS 单测 ≥ 10 条 |
| V3 | `npm run smoke -- enterprise-config` |
| V4 | dev 手工：mock 策略 |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 设备管理 / MDM（v2）。
- 合规审计导出（v2）。
