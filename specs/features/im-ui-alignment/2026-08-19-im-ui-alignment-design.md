# IM UI 对齐 LobsterAI 设计文档

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 的 IM 通道子系统（`src/darvin-agent/internal/im/`）已落地 QQ / 企业微信 WeCom / 个人微信 Weixin 三个连接器，renderer 侧有 `ImView.vue` + `ImInstanceCard.vue` 做实例管理。对照 LobsterAI（`/home/darven/桌面/github-project/LobsterAI`）的 IM 管理 UI（`src/renderer/components/im/IMSettings.tsx` + 各 `*InstanceSettings.tsx`），darvin 当前缺几项用户可感知的诊断 / 编辑能力：

- **连接测试是假的**：`handleTest`（`handlers.go:294`）只 `build()` 一下 connector（不拨号、不换 token）就返回 `{ok:true}`，UI 只弹一个 ok/ko toast。用户填错 secret 只会得到「ok 但实际连不上」的误导。
- `lastError` 有数据（`DarvinIMStatus.lastError`）但卡片从不展示，连接失败只能靠 toast 一闪而过，看不出根因。
- secret 输入框没有显示/隐藏切换，也没有一键清空。
- 删除实例无二次确认；实例不能重命名。
- 编辑面板改动后无「未保存」提示，用户改了不点保存就切走会静默丢改动。

两边都是「实例管理 + 连接诊断」面（非聊天室 UI；入站→agent→回复由 Go 后端自动处理，renderer 不参与）。所以对齐方向是**管理/诊断体验**，不新增聊天视图。

### 1.2 目标

让 darvin 的 IM 管理 UI 具备与 LobsterAI 同级的诊断与编辑体验：测试真正探测连通性并给出结构化逐条检查报告；卡片直接展示最近错误；secret 可明文查看 / 一键清空；删除有二次确认、可重命名；有「未保存改动」提醒。

### 1.3 非目标

- 不把测试跑成 WS 常驻链接 / 不做多跳握手（qq 换 token 成功即视为连通；wecom 只拨号 + 认证一次即断）。
- 不做「测试通过自动静默启用」（LobsterAI 会 auth_check 通过即自动开）；darvin 改为「测试通过弹窗里手动确认启用」，避免误启用。
- 不新增聊天 / 会话历史视图。
- 不做 save-reminder 的 beforeunload / 离开拦截（Electron SPA 无刷新语义，代价高收益低），只做温和提醒。
- 不新增第三个通道（Feishu / DingTalk 等）——本期只把现有三通道的诊断与编辑体验做齐。

## 2. 用户场景

### 场景 1: 诊断连接失败根因
**Given** 用户已在 QQ 平台创建 Bot，填了 appId / appSecret
**When** 点击「测试连接」，secret 实际填错
**Then** 弹出测试报告窗：verdict 显示「失败」，逐条列出检查项（如 `auth_ok` 标红），detail 给出原因；不再误报成功

### 场景 2: 卡片上看连接错误
**Given** 某实例状态为 error（如 weixin token 失效）
**When** 打开「IM 通道」页
**Then** 实例卡片内直接显示最近错误文本（红条），无需点测试

### 场景 3: secret 明文查看
**Given** 正在编辑 qq / wecom 实例的 secret
**When** 点击眼睛图标
**Then** 明文显示，可核对；再点收起；旁边有 ✕ 可一键清空

### 场景 4: 删除 / 重命名
**Given** 已有一个实例
**When** 点「删除」
**Then** 弹出确认框（含实例名），确认后删除；点名称/编辑可重命名，回车或失焦保存

### 场景 5: 未保存改动提醒
**Given** 正在编辑某实例且字段有改动
**When** 改动后未点保存
**Then** 卡片标题旁显示「未保存」badge；切走或关闭编辑时 toast 提示改动已放弃

## 3. 功能需求

### FR-1: 结构化连接测试（Go + renderer）
- `im.Instance` 契约新增**可选探针接口** `Prober`，`Probe(ctx) ([]Check, error)`（不持久化、不常驻连通）。
- 逐一连接器实现真探测：
  - **qq**：`Probe` 调 `ensureToken()` 真向 `bots.qq.com/app/getAppAccessToken` 换 token；成功 `{code:"auth_ok", level:"pass"}`，失败 fail。
  - **wecom**：短超时 `Dial(wsURL)` + 发 `aibot_subscribe` 等回执（复用 `waitAuth`），完毕后 `Close`；`errcode!=0` → fail。
  - **weixin**：以 `cfg.BotToken` 走一次 `getupdates` 探测（长轮询窗口压到 ~3s 避免阻塞）；`ret==-14` / HTTP 错误 → fail。

> **实现要点（评审补充）**：weixin `Probe` **不能直接复用现成 `fetchUpdates`**（`weixin.go` 的 `fetchUpdates` 写死 `timeout:50`、会改写 `cursor` / 缓存 `ctxToken` / `dispatchCallback` 派发入站，且不经 `Start` 时空 token 拦截不触发）。需抽一个接受 `timeout` 参数、不落游标、不派发的 probe 专用 getupdates 请求（内联或在 `fetchUpdates` 上提参重构）；无 `BotToken` 时直接 `{login_ok:fail, detail:"missing token"}` 不发请求（见 5.边界情况）。
- `TestResult` 扩展 `Checks []Check` 字段；`handleTest` 对实现 `Prober` 的连接器跑 `Probe`，否则回退 `{build_ok, pass}`。
- `Check` 字段：`code` / `title` / `level(pass|warn|fail)` / `detail?`。

### FR-2: 测试报告弹窗（renderer）
- 点「测试连接」打开弹窗：顶部 verdict（通过绿 / 部分警告黄 / 失败红，按 checks 中 fail/warn 判定），逐条检查行（level 点 + detail），有 fail 时提示「保存后重试」。
- 若实例已存在、当前 `enabled==false`、checks 全 pass：弹窗内提供「测试通过，是否启用？」按钮（**手动确认**，非静默自开）。

> **实例定位路径（评审补充）**：`imTest` 入参 `TestParams` 只有 `{channel, config}`，**不含 instanceId**（`handlers.go` 与 `useIm.test` 均如此）。FR-2 的「取已存在且已停用实例」判断**不改 Go 契约**，在 renderer 侧完成：弹窗持有当前 `instances` 列表（`ImInstanceCard` 已有），按 `channel + config` 与编辑中候选配置比对定位目标实例；点「启用」直接调用现有 `imSetEnabled(id, true)`（`handleSetEnabled` 已支持），不再扩展 `TestParams`。

### FR-3: 卡片展示 lastError（renderer）
- 卡片内当 `inst.status.state==='error' || lastError` 非空时，标题行下方渲染红底错误条。

### FR-4: secret 显示/隐藏 + 清空（renderer）
- qq `appSecret` 与 wecom `secret` 输入框（编辑态与新增态）加眼睛切换（`eye`↔`eye-off`，新增到 `assets/icons/`）与一键清空 ✕。

### FR-5: 删除二次确认 + 重命名（renderer）
- 删除先弹确认框（含实例名/通道），不用系统 `confirm()`。
- 名称可内联编辑（铅笔/点名称进 input，回车/失焦保存，空值回退 `channel`）。

### FR-6: save-reminder（renderer）
- 编辑面板任一字段改动 → `dirty[id]=true`，卡片标题旁显示「未保存」badge；保存成功清除。
- 切走/关编辑时若 dirty 则 toast 提示改动已放弃。

## 4. 实现方案

### 4.1 Go：Prober 接口（`internal/im/contract.go`）

```go
// Prober performs a one-shot connectivity check for the candidate config
// without persisting or staying connected. Connectors that can probe
// implement it; others fall back to a build-only pass.
type Prober interface {
    Probe(ctx context.Context) ([]Check, error)
}

// Check is one granular connectivity check inside a probe report.
type Check struct {
    Code   string `json:"code"`
    Title  string `json:"title"`
    Level  string `json:"level"` // pass | warn | fail
    Detail string `json:"detail,omitempty"`
}
```

- 接口放消费侧契约包（`im`），实现在被调方（各连接器）——满足 F6。
- `Probe` 需短超时 + 不阻塞长轮询；错误合并进 `Checks`（`level:"fail"`），`err` 不单独当失败依据（避免双层报错）。
- 检查项标题 `title` 由 renderer 端 i18n 按 `code` 映射（`Code→title` 稳定键放 renderer），Go 端 `title` 字段可由各连接器填英文兜底（与现有 zap 日志同为技术输出，不走 i18n）。

### 4.2 Go：handleTest 改造（`internal/im/handlers.go`）

```go
type TestResult struct {
    OK     bool    `json:"ok"`
    Error  string  `json:"error,omitempty"`
    Checks []Check `json:"checks,omitempty"`
}
```

`handleTest` 装配 `inst` 后：`defer inst.Stop`；若 `inst` 实现 `Prober` 则跑 `Probe`，`ok := err==nil && all checks pass`；否则回退 `{OK:true, Checks:[{build_ok,pass}]}`。装配失败返回 `{OK:false, Error:..., Checks:[{config_valid, fail}]}`。

> **错误语义收敛（评审补充）**：把「未知 channel」（现 `handlers.go:304` 走 JSON-RPC error）与「config 解码失败」也并入 `Checks`（如 `{code:"channel", fail}` / `{code:"config_valid", fail}`），让 `imTest` 的返回路径收敛成单一 `TestResult` 结构（Checks 承载全部判断，`Error` 仅兜底），renderer 弹窗只需渲染 checks 列表；不再并存 Checks / Error / JSON-RPC error 三层错误语义。

### 4.3 Shared 类型（`src/shared/darvin-api.ts`）

```ts
export interface DarvinIMCheck {
  code: string;
  title: string;
  level: 'pass' | 'warn' | 'fail';
  detail?: string;
}
// imTest 返回扩展
imTest(req): Promise<{ ok: boolean; error?: string; checks?: DarvinIMCheck[] }>
```

`useIm.test()` 透传 `checks`。

### 4.4 Renderer（`ImInstanceCard.vue` / `ImView.vue` / i18n）

- 测试弹窗：本地 modal 状态（`add:` 弹层与仓库现有 Sweep 弹层风格一致）；verdict 颜色映射 + 逐条检查行。
- lastError 红条：卡片内 `v-if="inst.status.lastError"`。
- secret 眼睛切换 + ✕ 清空：本地 `editing`/`adding` 值即可；清空不等于保存，落库仍走 `update()`。
- 删除确认：本地 `confirmRef` 记录待删 id；重命名：`editingName` state + 复用 `update({name})`。
- save-reminder：`dirty: Record<string, boolean>`；切换平台 tab / 关编辑 / `onUnmounted` 时按 dirty 弹 toast。
- 新增图标：`src/renderer/assets/icons/eye.svg` + `eye-off.svg`（`stroke="currentColor"`，viewBox 34）。
- i18n 新增 key（zh/en 严格对齐，`assertSameKeys`）。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 测试期间连接器拨号卡住 | `Probe` 用短超时（wecom `probeTimeout≈10s`，weixin 长轮询压到 ~3s），超时 → fail check；注意 wecom `waitAuth` 用 WS read deadline 而非 ctx 控制，probe 里用独立 `context.WithTimeout` 包裹探测会话，避免依赖 ctx 取消 |
| weixin 无 token 时测试 | 直接 `{login_ok:fail, detail:"missing token"}`，不发起请求 |
| qq token 已缓存未过期 | **不会命中**：`handleTest` 每次用候选 config 新建 Connector（`token` 恒空），`Probe` 必然真向 `/getAppAccessToken` 换 token，此分支不触发，无需特判 |
| 实例未启用时测试 | 测试独立于 enabled，只探测候选 config；可正常出报告 |
| 用户改 secret 但未保存就点测试 | 测试用当前 `editing/adding` 值构造的候选 config（现状行为），不反映已落库值——UI 给出「保存后再测」提示 |
| 删除实例正在运行 | 删除确认框即防误触；仍用现有 `imDelete` 流程（后端负责 Stop） |
| 跨平台（Win/Mac/Linux） | 纯前端 + HTTP/WS 探测，无平台分支 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/darvin-agent/internal/im/contract.go` | 新增 `Check` / `Prober` |
| `src/darvin-agent/internal/im/handlers.go` | `TestResult` 加 `Checks`；`handleTest` 跑 `Probe` |
| `src/darvin-agent/internal/im/qq/qq.go` | 实现 `Probe`（换 token 探测） |
| `src/darvin-agent/internal/im/wecom/wecom.go` | 实现 `Probe`（拨号 + auth） |
| `src/darvin-agent/internal/im/weixin/weixin.go` | 实现 `Probe`（压短 getupdates） |
| `src/shared/darvin-api.ts` | `DarvinIMCheck` + `imTest` 返回扩展 |
| `src/renderer/composables/useIm.ts` | `test()` 透传 `checks` |
| `src/renderer/views/ImView.vue` | 测试弹窗挂载（如需） |
| `src/renderer/components/im/ImInstanceCard.vue` | 弹窗 / lastError / secret 切换 / 删除确认 / 重命名 / save-reminder |
| `src/renderer/assets/icons/eye.svg`、`eye-off.svg` | 新增图标 |
| `src/renderer/services/i18n.ts` | 新增 key（zh/en 对齐） |

## 7. 验收标准

- [ ] 场景 1：错误 secret 测试 → 弹窗显示 `auth_ok` fail 检查 + detail，verdict 红
- [ ] 场景 2：error 实例卡片直接显示 lastError 红条
- [ ] 场景 3：secret 眼睛切换明文 / 收起，✕ 一键清空有效
- [ ] 场景 4：删除有二次确认；重命名回车/失焦落库、空值回退 channel
- [ ] 场景 5：有未保存改动显示 badge，切走 toast 提示
- [ ] `imTest` 返回 `checks`，Go 三连接器 `Probe` 均生效；无自定义 `Probe` 的连接器回退 build_ok
- [ ] `cd src/darvin-agent && go build ./... && go vet ./... && go test ./...` 通过
- [ ] `npm run lint` 通过；zh/en 字典 `assertSameKeys` 通过
- [ ] `npm start` 手工验证上述交互；无生成产物（`.vite/` / `out/` / `bin/`）、user-visible 字符串全走 `t()`
