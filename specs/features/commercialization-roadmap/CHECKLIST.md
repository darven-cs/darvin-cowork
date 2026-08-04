# 商业化路线图 Spec 落地 CHECKLIST

> 唯一事实源（Single Source of Truth）。任何 spec 的「编写 / 确认 / 实现 / 验收」四类事件变更均必须同步此表，不允许仅在 commit 或聊天中记录。
> 状态枚举：`待编写 / 待确认 / 已确认 / 实现中 / 待验收 / 已完成 / 阻塞`。
> 双向链接规约：每份 spec 顶部链接回本文件；本表每行对应唯一的 spec path。

## 1. 总索引（按阶段）

### P1 — Foundation Hardening（W1–W12，M3）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 1 | [`./P1-2026-08-04-runtime-supervision-design.md`](./P1-2026-08-04-runtime-supervision-design.md) | main-go-decompose 占位 / RuntimeManager 现状 | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；RuntimeManager watchdog + 端口探活 + 最多 3 次指数退避 |
| 2 | [`./P1-2026-08-04-db-consistency-fixes-design.md`](./P1-2026-08-04-db-consistency-fixes-design.md) | merge-databases v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；双库单写者仲裁 + drift 检测 + resync 原子性 |
| 3 | [`specs/refactors/per-session-acp-agent/2026-07-31-per-session-acp-agent-design.md`](../../refactors/per-session-acp-agent/2026-07-31-per-session-acp-agent-design.md) | — | 已存在 | 已确认（沿用） | 未开始 | 未开始 | — | 沿用既有 spec |
| 4 | [`specs/features/i18n-enhancement/2026-08-01-i18n-enhancement-design.md`](../i18n-enhancement/2026-08-01-i18n-enhancement-design.md) | — | 已存在 | 已确认（沿用） | 未开始 | 未开始 | — | 沿用既有 spec |
| 5 | [`specs/refactors/merge-databases/2026-08-01-merge-databases-design.md`](../../refactors/merge-databases/2026-08-01-merge-databases-design.md) | — | 已存在 | 已确认（沿用） | 未开始 | 未开始 | — | 沿用 v1；P8 出商业化迭代版 |

### P2 — Multi-Provider、Compaction、Failover（W13–W26，M6）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 6 | [`./P2-2026-08-04-provider-registry-design.md`](./P2-2026-08-04-provider-registry-design.md) | darvin-api-extension | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Factory/Register/Get/List + 能力声明 + 并发安全 |
| 7 | [`./P2-2026-08-04-provider-openai-design.md`](./P2-2026-08-04-provider-openai-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Chat Completions + tool use + SSE |
| 8 | [`./P2-2026-08-04-provider-mistral-design.md`](./P2-2026-08-04-provider-mistral-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Mistral Chat Completions 兼容层 |
| 9 | [`./P2-2026-08-04-provider-openai-responses-design.md`](./P2-2026-08-04-provider-openai-responses-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Responses API + stateful conversation_id |
| 10 | [`./P2-2026-08-04-provider-azure-design.md`](./P2-2026-08-04-provider-azure-design.md) | provider-openai | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Azure OpenAI 部署级路由 + extra_headers |
| 11 | [`./P2-2026-08-04-provider-gemini-design.md`](./P2-2026-08-04-provider-gemini-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Gemini generateContent + SSE |
| 12 | [`./P2-2026-08-04-provider-vertex-design.md`](./P2-2026-08-04-provider-vertex-design.md) | provider-gemini | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Vertex AI 鉴权 + project/location |
| 13 | [`./P2-2026-08-04-provider-bedrock-design.md`](./P2-2026-08-04-provider-bedrock-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；AWS SigV4 + Converse API 归一 |
| 14 | [`./P2-2026-08-04-failover-and-circuit-breaker-design.md`](./P2-2026-08-04-failover-and-circuit-breaker-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；CLOSED/OPEN/HALF_OPEN 状态机 |
| 15 | [`./P2-2026-08-04-cost-and-usage-tracking-design.md`](./P2-2026-08-04-cost-and-usage-tracking-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；token / model / provider / session 四维 |
| 16 | [`./P2-2026-08-04-agent-context-engine-commercialization-design.md`](./P2-2026-08-04-agent-context-engine-commercialization-design.md) | 已有 v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 迭代；补齐 provider-aware context budget |
| 17 | [`./P2-2026-08-04-context-compaction-ui-commercialization-design.md`](./P2-2026-08-04-context-compaction-ui-commercialization-design.md) | 已有 v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 迭代；费用预估 + provider 切换前 compaction |

### P3 — Memory + Dreaming（W27–W39，M9）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 18 | [`./P3-2026-08-04-memory-core-design.md`](./P3-2026-08-04-memory-core-design.md) | — | 待规范化 | 待确认 | 未开始 | 未开始 | — | 由 `2026-MM-DD-…` 重命名 |
| 19 | [`./P3-2026-08-04-memory-extract-cleanup-design.md`](./P3-2026-08-04-memory-extract-cleanup-design.md) | memory-core | 待规范化 | 待确认 | 未开始 | 未开始 | — | 由 `2026-MM-DD-…` 重命名 |
| 20 | [`./P3-2026-08-04-memory-bootstrap-agents-design.md`](./P3-2026-08-04-memory-bootstrap-agents-design.md) | memory-core | 待规范化 | 待确认 | 未开始 | 未开始 | — | 由 `2026-MM-DD-…` 重命名 |
| 21 | [`./P3-2026-08-04-memory-renderer-design.md`](./P3-2026-08-04-memory-renderer-design.md) | memory-core | 待规范化 | 待确认 | 未开始 | 未开始 | — | 由 `2026-MM-DD-…` 重命名 |
| 22 | [`./P3-2026-08-04-memory-dreaming-design.md`](./P3-2026-08-04-memory-dreaming-design.md) | memory-core / context-compaction | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Light/Deep/REM + DREAMS.md |

### P4 — Subagent、Artifact 沙箱、Plan、Goal、Fork（W40–W52，M12）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 23 | [`./P4-2026-08-04-subagent-design.md`](./P4-2026-08-04-subagent-design.md) | per-session-acp-agent | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；3 种 spawn/task/scope |
| 24 | [`./P4-2026-08-04-artifact-sandbox-iframe-design.md`](./P4-2026-08-04-artifact-sandbox-iframe-design.md) | artifact-panel v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；CSP + postMessage 校验 + XSS corpus |
| 25 | [`./P4-2026-08-04-plan-mode-goal-mode-design.md`](./P4-2026-08-04-plan-mode-goal-mode-design.md) | ContextEngine | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；只读模式 + 确认后执行 |
| 26 | [`./P4-2026-08-04-session-fork-design.md`](./P4-2026-08-04-session-fork-design.md) | session-management | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；session DAG + 附件复制 + 可选 worktree |
| 27 | [`./P4-2026-08-04-artifact-panel-commercialization-design.md`](./P4-2026-08-04-artifact-panel-commercialization-design.md) | artifact-panel v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 迭代；新增 share & persist |
| 28 | [`./P4-2026-08-04-artifact-panel-ux-commercialization-design.md`](./P4-2026-08-04-artifact-panel-ux-commercialization-design.md) | artifact-panel-ux v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 迭代；新增键盘流与 token budget |

### P5 — Scheduled Tasks、Heartbeat、系统能力（W53–W65）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 29 | [`./P5-2026-08-04-scheduled-tasks-and-cron-design.md`](./P5-2026-08-04-scheduled-tasks-and-cron-design.md) | scheduler | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；cron + timezone/DST + missed-task queue |
| 30 | [`./P5-2026-08-04-heartbeat-and-cost-control-design.md`](./P5-2026-08-04-heartbeat-and-cost-control-design.md) | ContextEngine / cost-tracking | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；5min heartbeat + token/费用预算 |
| 31 | [`./P5-2026-08-04-anti-sleep-and-shortcuts-design.md`](./P5-2026-08-04-anti-sleep-and-shortcuts-design.md) | RuntimeManager | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；防休眠句柄 + 全局快捷键 |
| 32 | [`./P5-2026-08-04-sqlite-backup-restore-design.md`](./P5-2026-08-04-sqlite-backup-restore-design.md) | merge-databases v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；一致性备份 + 恢复演练 |
| 33 | [`./P5-2026-08-04-usage-analytics-design.md`](./P5-2026-08-04-usage-analytics-design.md) | cost-and-usage-tracking | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；去敏聚合 + 报表 |

### P6 — IM Gateway 国内三家（W66–W78）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 34 | [`./P6-2026-08-04-im-channel-abstraction-design.md`](./P6-2026-08-04-im-channel-abstraction-design.md) | darvin-api-extension | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；独立 Gateway + OnEvent/Send/ReplyStream |
| 35 | [`./P6-2026-08-04-im-channel-feishu-design.md`](./P6-2026-08-04-im-channel-feishu-design.md) | im-channel-abstraction | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；飞书回调验签 + 卡片流式更新 |
| 36 | [`./P6-2026-08-04-im-channel-dingtalk-design.md`](./P6-2026-08-04-im-channel-dingtalk-design.md) | im-channel-abstraction | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；钉钉 Stream + 加签 |
| 37 | [`./P6-2026-08-04-im-channel-wecom-design.md`](./P6-2026-08-04-im-channel-wecom-design.md) | im-channel-abstraction | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；企业微信回调 + 加密 |
| 38 | [`./P6-2026-08-04-im-channel-extension-design.md`](./P6-2026-08-04-im-channel-extension-design.md) | im-channel-abstraction | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；扩展点 + Slack/Telegram/Discord 等 v2 范围 |

### P7 — Browser、Voice、Media、商业化（W79–W91，M18）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 39 | [`./P7-2026-08-04-web-browser-tool-design.md`](./P7-2026-08-04-web-browser-tool-design.md) | artifact-sandbox-iframe | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Playwright/Chromium 会话隔离 |
| 40 | [`./P7-2026-08-04-voice-asr-design.md`](./P7-2026-08-04-voice-asr-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；Whisper.cpp/SaaS ASR 抽象 |
| 41 | [`./P7-2026-08-04-media-generation-design.md`](./P7-2026-08-04-media-generation-design.md) | provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；5 家 provider 归一化 |
| 42 | [`./P7-2026-08-04-billing-v1-design.md`](./P7-2026-08-04-billing-v1-design.md) | oauth-login | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；账本 + 幂等计量 |
| 43 | [`./P7-2026-08-04-oauth-login-design.md`](./P7-2026-08-04-oauth-login-design.md) | credential-vault | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；PKCE + state + token 加密刷新 |
| 44 | [`./P7-2026-08-04-enterprise-config-design.md`](./P7-2026-08-04-enterprise-config-design.md) | oauth-login / provider-registry | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；策略优先级 + 强制 Provider + 水印 |

### P8 — 成熟产品收尾（W92–W104，M24）

| # | spec | 依赖 | 文档状态 | 用户确认 | 代码实现 | 验收 | 完成日期 | 备注 |
|---|---|---|---|---|---|---|---|---|
| 45 | [`./P8-2026-08-04-single-sqlite-wal-commercialization-design.md`](./P8-2026-08-04-single-sqlite-wal-commercialization-design.md) | merge-databases v1 | 待编写 | 待确认 | 未开始 | 未开始 | — | 迭代；单 SQLite + WAL + 校验回滚 |
| 46 | [`./P8-2026-08-04-observability-and-monitoring-design.md`](./P8-2026-08-04-observability-and-monitoring-design.md) | runtime-supervision | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；指标 / 日志 / 告警 / crash-free |
| 47 | [`./P8-2026-08-04-sla-and-disaster-recovery-design.md`](./P8-2026-08-04-sla-and-disaster-recovery-design.md) | sqlite-backup-restore | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；RPO/RTO 演练 |
| 48 | [`./P8-2026-08-04-ts-test-coverage-design.md`](./P8-2026-08-04-ts-test-coverage-design.md) | 现有 vitest | 待编写 | 待确认 | 未开始 | 未开始 | — | 新增；覆盖率基线 + 按模块门禁 |

## 2. 路线图总览与里程碑

| 阶段 | 周次 | 里程碑 | 退出条件 |
|---|---|---|---|
| P1 | W1–W12 | M3 | `smoke:recovery` 通过；24h soak 无崩溃；双库仲裁 100% |
| P2 | W13–W26 | M6 | 7 厂商 spec 全部 ≥10 场景；Failover 状态机验证通过；费用可查询 |
| P3 | W27–W39 | M9 | 5 份 spec 全部确认；Dreaming 触发边界明确 |
| P4 | W40–W52 | M12 | 3 种 subagent 模式可演示；iframe XSS corpus 通过；Fork 重放 |
| P5 | W53–W65 | — | cron + DST 通过；missed-task 重跑幂等；24h 备份验证 |
| P6 | W66–W78 | — | 3 家 IM mock 全过；撤回幂等 |
| P7 | W79–W91 | M18 | Playwright 隔离；ASR 中准确率评测；OAuth PKCE |
| P8 | W92–W104 | M24 | 单 SQLite + WAL；RPO ≤ 5 分钟；RTO ≤ 30 分钟；TS 覆盖基线 |

## 3. 状态变更流程

| 事件 | 触发方 | CHECKLIST 更新字段 |
|---|---|---|
| spec 编写完成 | AI agent | 文档状态 → `待确认`；完成日期 = spec 文件日期 |
| 用户确认通过 | 用户 | 用户确认 → `已确认` |
| 代码实现启动 | AI agent | 代码实现 → `实现中` |
| 代码实现完成 | AI agent | 代码实现 → `待验收` |
| 验收通过 | 用户/AI | 验收 → `已完成`；完成日期 = 验收日期 |
| 阻塞 | 任意方 | 任一字段 → `阻塞`；备注写明阻塞原因 |

## 4. 历史占位追踪（仅记录，不算完成）

| 项 | 状态 | 行动 |
|---|---|---|
| `specs/features/memory-subsystem/2026-MM-DD-*.md` | 占位 | 重命名为 `2026-08-04-*`；同步 README 链接 |
| `specs/refactors/main-go-decompose/`（目录空） | 占位 | 保留为 P1 子目录占位；本次不补 spec |

## 5. 反向链接要求

每份新增 / 迭代 spec 的顶部必须包含以下三段（否则视为未完成）：

```markdown
> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：<状态枚举值>
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。
```

## 6. 不在 CHECKLIST 范围

- `specs/features/agent-acp-loop` / `agent-loop` / `agent-llm-encapsulation` 等早期 spec：仅沿用，不进 CHECKLIST。
- 已存在的 bugfixes / refactors 文档：仅在 P1 / P8 中以迭代版形式登记。
- `specs/features/agent-output-ux-research` 等调研类文档：不属于落地 spec。
