# OAuth Login 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

商业化要求 darvin-cowork 有账号体系 + OAuth 登录（Google / GitHub / 企业 SSO 占位）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | Authorization Code + PKCE | flow |
| G2 | `state` 防 CSRF + 一次性 | state |
| G3 | token 加密（AES-GCM）+ keychain 派生 | encrypt |
| G4 | refresh token 自动刷新 | refresh |
| G5 | 单点登录（SSO）企业域占位 | SSO |
| G6 | logout + 撤销本地凭据 | logout |
| G7 | ≥ 10 测试场景 | tests |

### 1.3 非目标

- 不实接商业身份厂商（仅设计）。
- 不实现密码登录。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `src/main/libs/user-settings.ts` | 设置持久化 |
| `src/main/runtime/manager.ts` | 进程托管 |

## 3. 用户/系统场景

### 场景 1：Google OAuth

**Given** 用户选 Google
**When** 浏览器跳 authorize endpoint
**Then** callback → exchange code → refresh_token 加密落 keychain

### 场景 2：token 刷新

**Given** access_token 临近过期
**When** runtime 检测
**Then** refresh 后重试

### 场景 3：SSO

**Given** 企业配置 SAML IdP
**When** 登录
**Then** 跳转企业 IdP；返回 id_token

### 场景 4：登出

**Given** 用户点 logout
**When** runtime 收到事件
**Then** 清除本地 token；UI 回到未登录态

## 4. 功能需求

### FR-1 PKCE 流

```
client → authorization_server/authorize?client_id=...&code_challenge=...
       ← callback?code=...&state=...
client → /token  {grant_type=authorization_code, code, code_verifier}
```

### FR-2 state 防 CSRF

```go
state := sha256(random + session_id)
session.Store("oauth.state", state)
```

回调时校验一次性消费。

### FR-3 token 加密

```go
type Vault interface {
    Encrypt(plain []byte) ([]byte, error)
    Decrypt(cipher []byte) ([]byte, error)
}

type keychainVault struct{} // macOS Keychain / Linux Secret Service / Win DPAPI
```

### FR-4 refresh

```go
type Token struct {
    Access  string
    Refresh string
    Exp     time.Time
}

func (t *Token) Refresh(ctx context.Context) error
```

提前 5 分钟续期；并发 race 用 mutex。

### FR-5 SSO 占位

```go
type SSOProvider struct {
    Type    string // 'saml' / 'oidc'
    Issuer  string
    ClientID string
}

func LoginSSO(ctx context.Context) error
```

实现 OIDC 即可；SAML 留 v2。

### FR-6 logout

```go
func Logout() error
```

撤销本地 + IdP revoke-endpoint 调一次（失败可容忍）。

### FR-7 ≥ 10 测试场景

| # | 场景 |
|---|---|
| T1 | PKCE 生成 |
| T2 | state 生成 |
| T3 | callback 验证 |
| T4 | state 一次性 |
| T5 | token 加密落盘 |
| T6 | token 解密读 |
| T7 | refresh 续期 |
| T8 | refresh race |
| T9 | SSO OIDC |
| T10 | logout 撤销 |
| T11 | IdP revoke 失败容忍 |

## 5. 安全与隐私

- access_token / refresh_token 永不出日志。
- code_verifier 随机 64 字节 base64url。
- 旁路 CSRF：state 一次性 + `SameSite=Lax` 浏览器侧。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| IdP 不可达 | 提示重试 |
| refresh 失败 | 重新登录 |
| keychain 拒绝 | 失败提示 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `src/main/libs/oauth-flow.ts`（新） | PKCE |
| `src/main/libs/keychain.ts`（新） | 凭证 vault |
| `src/main/libs/sso.ts`（新） | SSO 占位 |
| `src/shared/darvin-api.ts` | `auth.*` 事件 |
| `src/renderer/components/settings/SettingsPanelAccount.vue`（新） | UI |

## 8. 实施顺序与依赖

1. `keychain.ts`
2. `oauth-flow.ts`
3. `sso.ts`
4. UI

> 前置：基础 user_settings 表。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | TS 单测 ≥ 10 条 |
| V3 | `npm run smoke -- oauth-login` |
| V4 | dev 手工：mock OAuth flow |
| V5 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V6 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 真实 OAuth 厂商实接（v2）。
- SAML（v2）。
