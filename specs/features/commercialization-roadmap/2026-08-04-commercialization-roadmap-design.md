# darvin-cowork 商业化路线图设计文档

> 路线图追踪：[`CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

darvin-cowork 已经具备 Electron + Vue3 + Go agent 的最小可运行架构与一批基础 spec（会话 / ContextEngine / Artifact Panel / Memory Subsystem / i18n / 数据库合并）。本路线图承载后续 18-24 个月内把该原型推进到可商业化版本的全部主题，把之前散落的 brainstorm 转写为可评审、可分级、可追踪的中文设计文档。

本次只落 spec，**不**改业务代码、构建配置、依赖或测试代码；不提交 Git commit。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 把 8 个阶段（P1–P8）和 104 周（W1–W104）映射到具体 spec 文件 | `CHECKLIST.md` 中每行一项可定位 |
| G2 | 把全部里程碑（M3 / M6 / M9 / M12 / M18 / M24）的退出条件写明 | 每阶段末尾的「退出条件」段对齐里程碑 |
| G3 | 用单 SQLite + WAL 替换现有 fts / meta / sessions 多库方案 | `single-sqlite-wal-commercialization` 给出迁移与回滚 |
| G4 | 多 Provider（OpenAI / Mistral / OpenAI Responses / Azure / Gemini / Vertex / Bedrock）可注册可热切换 | 每个 Provider spec 至少 10 个 wire/单元测试场景 |
| G5 | Failover + 熔断 + 退避保证 session 不丢失 | `failover-and-circuit-breaker` 给出状态机 |
| G6 | 记忆子系统（V1）+ Dreaming 异步整合 + Subagent + Plan/Fork | Memory core/extract/bootstrap/renderer+dreaming 五份 spec 全套 |
| G7 | IM Gateway：飞书 / 钉钉 / 企业微信三家通过抽象层接入 | `im-channel-abstraction` + 3 平台 spec + extension spec |
| G8 | 商业化：账单 / OAuth / 企业策略 / 计费 / 用量分析 | `billing-v1` + `oauth-login` + `enterprise-config` + `usage-analytics` |
| G9 | 观测 / SLA / 灾备 / TS 测试覆盖率收尾 | `observability-and-monitoring` + `sla-and-disaster-recovery` + `ts-test-coverage` |

### 1.3 非目标

- 不实接任何真实云账号 / 商户号 / OAuth 第三方（仅 spec 约束，不写真实凭据）。
- 不修改 `src/` 任何业务源码、构建脚本或依赖（仅 spec 文档变更）。
- 不引入新前端组件库 / 状态管理库 / 第三方 CDN。
- 不主动 commit / 不 broad refactor / 不写 `Co-Authored-By`。

## 2. 阶段与里程碑

### 2.1 P1 — Foundation Hardening（W1–W12，M3）

**范围**：Runtime supervision / 数据库一致性 / Go runtime 业务下沉。

| 子主题 | spec | 状态 |
|---|---|---|
| Runtime watchdog | `./P1-2026-08-04-runtime-supervision-design.md` | 新增 |
| 双库一致性 | `./P1-2026-08-04-db-consistency-fixes-design.md` | 新增 |
| main-go decompose 占位 | `specs/refactors/main-go-decompose/`（沿用既有目录，待补） | 沿用 |
| per-session ACP agent | `specs/refactors/per-session-acp-agent/2026-07-31-per-session-acp-agent-design.md` | 沿用 |
| i18n enhancement | `specs/features/i18n-enhancement/2026-08-01-i18n-enhancement-design.md` | 沿用 |
| merge databases v1 | `specs/refactors/merge-databases/2026-08-01-merge-databases-design.md` | 沿用 |

**退出条件（M3）**：`smoke:recovery` 通过 / 24h soak 无崩溃 / 双库仲裁 100% 一致。

### 2.2 P2 — Multi-Provider、Compaction、Failover（W13–W26，M6）

**范围**：Provider Registry + 7 厂商 + Failover + 费用统计 + context 商业化迭代。

| 子主题 | spec |
|---|---|
| Provider Registry | `./P2-2026-08-04-provider-registry-design.md` |
| OpenAI Chat Completions | `./P2-2026-08-04-provider-openai-design.md` |
| Mistral | `./P2-2026-08-04-provider-mistral-design.md` |
| OpenAI Responses | `./P2-2026-08-04-provider-openai-responses-design.md` |
| Azure OpenAI | `./P2-2026-08-04-provider-azure-design.md` |
| Gemini | `./P2-2026-08-04-provider-gemini-design.md` |
| Vertex AI | `./P2-2026-08-04-provider-vertex-design.md` |
| Bedrock | `./P2-2026-08-04-provider-bedrock-design.md` |
| Failover + 熔断 | `./P2-2026-08-04-failover-and-circuit-breaker-design.md` |
| 费用 & 用量 | `./P2-2026-08-04-cost-and-usage-tracking-design.md` |
| Context Engine 商业化迭代 | `./P2-2026-08-04-agent-context-engine-commercialization-design.md` |
| Compaction UI 商业化迭代 | `./P2-2026-08-04-context-compaction-ui-commercialization-design.md` |

**退出条件（M6）**：7 厂商 spec 全部含 10+ 场景 / Failover 状态机验证通过 / 费用数据可查询。

### 2.3 P3 — Memory + Dreaming（W27–W39，M9）

**范围**：把 2026-MM-DD 占位规范为真实日期；新增 Dreaming。

| 子主题 | spec |
|---|---|
| memory-core | `./P3-2026-08-04-memory-core-design.md` |
| memory-extract-cleanup | `./P3-2026-08-04-memory-extract-cleanup-design.md` |
| memory-bootstrap-agents | `./P3-2026-08-04-memory-bootstrap-agents-design.md` |
| memory-renderer | `./P3-2026-08-04-memory-renderer-design.md` |
| memory-dreaming | `./P3-2026-08-04-memory-dreaming-design.md` |

**退出条件（M9）**：5 份 spec 全部确认 / Dreaming 触发点（Light/Deep/REM）边界明确。

### 2.4 P4 — Subagent、Artifact 沙箱、Plan、Goal、Fork（W40–W52，M12）

| 子主题 | spec |
|---|---|
| Subagent | `./P4-2026-08-04-subagent-design.md` |
| Artifact sandbox iframe | `./P4-2026-08-04-artifact-sandbox-iframe-design.md` |
| Plan Mode / Goal Mode | `./P4-2026-08-04-plan-mode-goal-mode-design.md` |
| Session Fork | `./P4-2026-08-04-session-fork-design.md` |
| Artifact Panel 商业化迭代 | `./P4-2026-08-04-artifact-panel-commercialization-design.md` |
| Artifact Panel UX 商业化迭代 | `./P4-2026-08-04-artifact-panel-ux-commercialization-design.md` |

**退出条件（M12）**：3 种 subagent 模式可演示 / iframe sandbox 通过 XSS corpus / Fork 后历史可重放。

### 2.5 P5 — Scheduled Tasks、Heartbeat、系统能力（W53–W65）

| 子主题 | spec |
|---|---|
| Scheduled Tasks | `./P5-2026-08-04-scheduled-tasks-and-cron-design.md` |
| Heartbeat & Cost Control | `./P5-2026-08-04-heartbeat-and-cost-control-design.md` |
| 防休眠 + 快捷键 | `./P5-2026-08-04-anti-sleep-and-shortcuts-design.md` |
| SQLite 备份恢复 | `./P5-2026-08-04-sqlite-backup-restore-design.md` |
| 用量分析 | `./P5-2026-08-04-usage-analytics-design.md` |

**退出条件**：cron 表达式支持时区 DST / missed-task 重跑幂等 / 24h 留存备份验证。

### 2.6 P6 — IM Gateway 国内三家（W66–W78）

| 子主题 | spec |
|---|---|
| IM Channel 抽象 | `./P6-2026-08-04-im-channel-abstraction-design.md` |
| 飞书 | `./P6-2026-08-04-im-channel-feishu-design.md` |
| 钉钉 | `./P6-2026-08-04-im-channel-dingtalk-design.md` |
| 企业微信 | `./P6-2026-08-04-im-channel-wecom-design.md` |
| Channel Extension | `./P6-2026-08-04-im-channel-extension-design.md` |

**退出条件（M18 前）**：3 平台 mock 验证全过 / 撤回与重复回调幂等。

> 路线图原叙述提及「11 个 IM 平台」，但 P6 阶段验收仅飞书 / 钉钉 / 企业微信三家；其作他市（Slack / Teams / Telegram / Discord / WhatsApp / LINE / Messenger / iMessage / 微信公众号 / WebChat）均预留在 `im-channel-extension` spec 中显式列为 v2 范围，本路线图不再为单项单列 spec。

### 2.7 P7 — Browser、Voice、Media、商业化（W79–W91，M18）

| 子主题 | spec |
|---|---|
| Web Browser Tool | `./P7-2026-08-04-web-browser-tool-design.md` |
| Voice ASR | `./P7-2026-08-04-voice-asr-design.md` |
| Media Generation | `./P7-2026-08-04-media-generation-design.md` |
| Billing v1 | `./P7-2026-08-04-billing-v1-design.md` |
| OAuth Login | `./P7-2026-08-04-oauth-login-design.md` |
| Enterprise Config | `./P7-2026-08-04-enterprise-config-design.md` |

**退出条件（M18）**：Playwright 会话隔离 / ASR 中准确率评测 / 5 家媒体归一 / OAuth PKCE 流 / 企业策略优先级。

### 2.8 P8 — 成熟产品收尾（W92–W104，M24）

| 子主题 | spec |
|---|---|
| Single SQLite WAL 商业化迭代 | `./P8-2026-08-04-single-sqlite-wal-commercialization-design.md` |
| 观测与监控 | `./P8-2026-08-04-observability-and-monitoring-design.md` |
| SLA & 灾备 | `./P8-2026-08-04-sla-and-disaster-recovery-design.md` |
| TS 测试覆盖率 | `./P8-2026-08-04-ts-test-coverage-design.md` |

**退出条件（M24）**：单 SQLite + WAL / RPO ≤ 5 分钟 / RTO ≤ 30 分钟 / TS 覆盖率基线达标。

## 3. 交叉依赖

```
P1 ─► P2 ─► P3 ─► P4 ─► P5 ─► P6 ─► P7 ─► P8
 │    │     │     │     │     │     │     │
 │    └─► 工具层共享 Provider Registry / Failover（Provider-Failover-Cost 三角）
 │    │
 │    └─► Memory Subsystem ◄─► Dreaming ◄─► Context Compaction UI
 │
 └─► Runtime supervision ◄─► 双库一致性 ◄─► 单 SQLite WAL（P8）
                              │
                              └─► SQLite 备份恢复（P5）
```

**跨阶段约束**：

- P1 退出前，P2 不可开始 Provider 实接（仅契约）。
- P2 完成 Failover 后，P4 Subagent / P5 Heartbeat 才允许使用 circuit breaker。
- Memory core / extract / bootstrap / renderer 全部未确认，Dreaming 不可开始。
- 单 SQLite WAL（P8）的迁移只能从 P1 双库仲裁已就绪的版本起步。
- IM Gateway（P6）依赖 Provider Registry 已确认（P2），但允许先行抽象 + 平台 mock。

## 4. 跨 spec 约束（统一规则）

| 维度 | 规则 |
|---|---|
| IPC 协议 | WebSocket + JSON-RPC 2.0，方法名与事件名集中在 `src/shared/darvin-api.ts`，Go 侧常量化在 `src/darvin-agent/internal/darvinapi/`（最终路径以执行时为准） |
| 业务归属 | 业务逻辑一律在 Go runtime；主进程仅负责 Electron 生命周期 |
| 凭证加密 | 任何 API Key / IM Token / OAuth Refresh Token 一律走 Go runtime 内部 `crypto/aes` + 系统 keychain；不写明文到 SQLite |
| 数据库迁移 | 所有 schema 变更走 `src/darvin-agent/internal/store/migrate.go` 版本化迁移；不允许在生产路径上 IF NOT EXISTS / ALTER TABLE 试错 |
| i18n | renderer 一律走 `t('xxx', params)`；hardcoded 中文字符串视为漏译 |
| 测试门槛 | 单元测试 ≥ 80% 覆盖目标模块；integration 由 `playwright-cli` 手动驱动；Go 侧 `go test` + `go vet` |
| 范围外业务变更 | 不主动 commit；不 broad refactor；不写 `Co-Authored-By` |

## 5. 现有 spec 复用矩阵

| 主题 | 现有 spec | 复用方式 |
|---|---|---|
| Per-Session ACP | `refactors/per-session-acp-agent/2026-07-31` | 沿用 |
| i18n Enhancement | `features/i18n-enhancement/2026-08-01` | 沿用，作为所有新 spec 的 i18n 基础 |
| Merge Databases v1 | `refactors/merge-databases/2026-08-01` | 沿用 v1，P8 出商业化迭代版 |
| Agent Context Engine | `features/agent-context-engine/2026-07-29` | P2 出商业化迭代版 |
| Context Compaction UI | `features/context-compaction-ui/2026-08-01` | P2 出商业化迭代版 |
| Memory Subsystem | `features/memory-subsystem/2026-MM-DD-*` + `README.md` + `CHECKLIST.md` | 规范化日期为 `2026-08-04-*`；同步 README 链接；追加 Dreaming |
| Artifact Panel | `features/artifact-panel/2026-08-01` | P4 出商业化迭代版 |
| Artifact Panel UX | `features/artifact-panel-ux/2026-08-02` | P4 出商业化迭代版 |
| Main-go Decompose | `refactors/main-go-decompose/`（目录空） | 留作 P1 子目录占位 |

## 6. 决策与矛盾纠正

| # | 矛盾点 | 决策 |
|---|---|---|
| D1 | 「P6 国内三家」vs「11 个 IM 平台」 | P6 验收仅三家；其余平台在 `im-channel-extension` 中预留 v2 范围 |
| D2 | Memory subspec 日期 `2026-MM-DD` 占位 | 本次统一规范为 `2026-08-04`，不重写内容；同步更新 `specs/features/memory-subsystem/README.md` 链接 |
| D3 | `main-go-decompose` 目录空 | 保留为占位目录；本次不补 spec，避免抢跑 P1 实际业务拆分 |
| D4 | Provider 7 厂商在 P2 同时落地 | 顺序：Registry → OpenAI Chat → Mistral → OpenAI Responses → Azure → Gemini → Vertex → Bedrock；Provider 间互不阻塞 |
| D5 | 「沿用既有 spec」vs「覆盖」 | 既有 spec 不覆盖；新增文件命名为 `*-commercialization-design.md` |

## 7. 涉及文件

本次仅创建以下文档：

```
specs/features/commercialization-roadmap/
├── 2026-08-04-commercialization-roadmap-design.md  ← 本文档
└── CHECKLIST.md                                   ← 唯一事实源

（其余阶段新增 spec 见 CHECKLIST.md 索引）
```

## 8. 实施顺序与依赖

1. **本次工作**：仅写 spec，不动业务代码。
2. **CHECKLIST.md 是唯一追踪索引**：每完成 spec 编写 / 用户确认 / 代码实现 / 验收 4 类事件，必须同步更新对应行的状态、日期与验收证据。
3. **状态枚举**：固定 `待编写 / 待确认 / 已确认 / 实现中 / 待验收 / 已完成 / 阻塞`。
4. **依赖链**：见第 3 节；P1 必须先全部 `已确认`，P2 才允许启动代码实现。
5. **纪律**：不主动 commit、不 broad refactor、不写 `Co-Authored-By`；所有业务规则源自 `CLAUDE.md` / `AGENTS.md`。

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `CHECKLIST.md` 中所有 spec 行均存在，无重复主题目录 |
| V2 | 文件名全部符合 `YYYY-MM-DD-<topic>-design.md` 模式（除占位目录） |
| V3 | 每份 spec 顶部含三条固定治理块；含「概述」与「验收标准」 |
| V4 | Memory 5 份 spec 文件名已规范为 `2026-08-04-*`；README 链接同步 |
| V5 | 任何 spec 中的「实现路径」均以相对路径指向真实存在的 src / docs 路径或标注「未来实现」 |
| V6 | `git diff --check` 在所有新增 Markdown 文件上无空白错误 |
| V7 | grep 全文不残留 `2026-MM-DD`（memory 文档目录除外，且 README 已更新） |
| V8 | 所有相对链接双向可达：`CHECKLIST.md` → spec 文件、spec 文件 → `CHECKLIST.md` |
| V9 | 不运行 lint / test / build（仅改文档） |

## 10. 验证步骤

本次纯文档变更，验证步骤如下（不涉及业务代码）：

1. `Glob` 输出所有 `specs/features/commercialization-roadmap/**/*.md` 与本次新增 spec，确认 0 漏。
2. 解析 `CHECKLIST.md` 中每行 path 字段，`Glob` 确认全部存在。
3. `grep` 全文 `2026-MM-DD` —— 应仅在 `memory-subsystem/README.md` 与 `CHECKLIST.md` 中以历史占位注释形式存在。
4. `grep` `已实现` / `已完成` / `done` —— 确认未把未来能力写成既成事实。
5. `git diff --check` 跑一次，输出 0 错误。
6. 抽样 5 份 spec 检查「概述」「验收标准」「涉及文件」「非目标」四章。

## 11. 不在本次范围

- 实际编写任何 `src/` / `darvin-agent/` 代码。
- 修改 `package.json`、`forge.config.ts`、`vite.*.config.ts`。
- 升级依赖 / 改 ESLint / 改 vitest 配置。
- 提交 Git commit。
