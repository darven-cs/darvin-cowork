# darvin-cowork Memory Subsystem — Spec Index

完整记忆子系统落地方案。共 5 个文件，按依赖顺序阅读。

## 文件清单

| 文件 | 覆盖范围 | CHECKLIST 章节 |
|---|---|---|
| [CHECKLIST.md](./CHECKLIST.md) | 全局 checklist + 风险 + spec 拆分 | A-M 全部 |
| [2026-08-04-memory-core-design.md](../commercialization-roadmap/P3-2026-08-04-memory-core-design.md) | 存储 + 文件格式 + FTS5 + bootstrap + config + tools + IPC | A, B, C1-C4, D, F, I1-I8 |
| [2026-08-04-memory-extract-cleanup-design.md](../commercialization-roadmap/P3-2026-08-04-memory-extract-cleanup-design.md) | auto-extract pipeline + near-dup 合并 + 启动清扫 + pre-compaction 防护 | E, G |
| [2026-08-04-memory-bootstrap-agents-design.md](../commercialization-roadmap/P3-2026-08-04-memory-bootstrap-agents-design.md) | SQLite 迁移 + workspace 迁移 + AGENTS.md 用户区 + FTS trigram 升级 + per-agent 钩子 | C5-C6, I9, J, K |
| [2026-08-04-memory-renderer-design.md](../commercialization-roadmap/P3-2026-08-04-memory-renderer-design.md) | Settings 3 tab UI + 13 字段统一持久化 + i18n | H |

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](../commercialization-roadmap/CHECKLIST.md)
> 当前状态：沿用既有内容；文件名为本次从 `2026-MM-DD-*` 规范化为 `2026-08-04-*` 的新文件名，不重写正文。
> 商业化路线图引用参见上方「路线图追踪」一行。

## 实现顺序（建议）

```
Step 1: memory-core（最大块）
        └── file.go + db.go + bootstrap.go + manager.go + tools + IPC handlers
        └── 设置 / TS 类型 / IPC channel
        └── 编译门禁：go build + npm run lint

Step 2: memory-extract-cleanup
        └── filter.go + similarity.go + extract.go
        └── dispatcher.go + ctxengine/history.go + event/filter.go
        └── AfterTurn 闭包注入 AutoExtract

Step 3: memory-bootstrap-agents
        └── migrate.go + workspace_migrate.go + bootstrap_migrate.go + index.go
        └── main.go 启动期 5 类 migration wiring
        └── memory_index_meta_v1 表 + trigram rebuild

Step 4: memory-renderer
        └── SettingsPanelMemory.vue 重写（3 tab）
        └── useMemory + useMemorySettings composable
        └── i18n.ts 增 ~30 key

Step 5: dreaming（独立 spec，未在此 index）
        └── 占位 tab 已实现；具体 spec 等 v2
```

每个 step 完成后端到端 smoke 一次（参考各 spec 验收标准），再进下一个 step。

## 与 LobsterAI 的差距对照

参考 LobsterAI 实现（`~/桌面/github-project/LobsterAI/src/main/libs/openclawMemoryFile.ts`, `openclawConfigSync.ts`, `coworkStore.ts`）：

| 能力 | LobsterAI | darvin-cowork v1 | 备注 |
|---|---|---|---|
| MEMORY.md block parser | ✅ | ✅ spec core | 全功能移植 |
| FTS5 trigram | ✅ | ✅ spec core | 关键：不能用 unicode61 |
| SQLite user_memories + sources | ✅ | ✅ spec core | 全功能移植 |
| Near-dup 合并 (0.82 dice) | ✅ | ✅ spec extract | 全功能移植 |
| 启动期 cleanup | ✅ | ✅ spec extract | 全功能移植 |
| Pre-compaction flush 防护 | ✅ | ✅ spec extract | 全功能移植 |
| AGENTS.md / TOOLS.md / BOOTSTRAP.md 迁移 | ✅ | ✅ spec bootstrap | 全功能移植 |
| FTS index migration (tokenizer upgrade) | ✅ | ✅ spec bootstrap | 全功能移植 |
| Per-agent bootstrap workspace | ✅ | 🟡 spec bootstrap v1 hook only | 仅 agentId 参数扩展；非 main agent v1 不支持 |
| Embedding provider 实接 (OpenAI / Gemini / ...) | 🟡 schema + UI only | 🟡 schema + UI only | v1 不实接（与 LobsterAI 一致） |
| LLM judge auto-extract | ❌ | ❌ | 字段保留，默认 false |
| Dreaming 后台整合 | ✅ | ❌ 独立 spec | 待 v2 |
| `memory/YYYY-MM-DD.md` daily notes | ✅ | ❌ | 待 v2 |
| `MEMORY.md` → SQLite 反向迁移 | ❌ | ❌ | 不需要 |
| OS-level 强制目录隔离 | ❌ | ❌ | system prompt 引导即可 |

## 关键设计决策（再次强调）

1. **FTS5 tokenizer = trigram**（不是 unicode61）— CJK 必须用 trigram
2. **FTS5 virtual table 不支持 UPSERT** — 写 entry 必须先 DELETE 再 INSERT
3. **MEMORY.md 多行 block** — `serializeEntryLines` 把续行缩进 2 空格
4. **path.Base === name** — bootstrap filename 严格白名单
5. **IDENTITY.md 默认内容跟随 locale** — zh / en 两份
6. **近重复合并 0.82 dice** — 不是 exact fingerprint dedup
7. **Capacity 触顶 → stale**（不是 deleted）— FTS 删除但 row 保留
8. **systemPromptOverride 字段已废弃** — 走 OpenClaw / Go assembler 的 sections 路径
9. **pre-compaction flush 内部 user prompt 不被 auto-extract** — 用 marker 检测
10. **per-agent workspace v1 hook only** — agentId 参数扩展但仅 main 生效

## 风险（沿用 LobsterAI 已踩过的坑）

- FTS5 trigram 才是 CJK 可用的 tokenizer
- FTS5 virtual table 不支持 `ON CONFLICT DO UPDATE`
- AGENTS.md 用户区必须按 managed marker 拆分
- 目标 memory/ 目录非空会挡住迁移（必须"目标空才复制"）
- IDENTITY.md 默认内容跟随 locale
- path traversal 防护：bootstrap filename 必须严格白名单 + path.Base 检查
- MEMORY.md code fence / HTML comment 不能当 entry
- systemPromptOverride 已废弃（OpenClaw v2026.6.1 移除）

## 进度追踪

每个 spec 文件最末有「涉及文件」「验收标准」「边界 / 非目标」三段；实现时按 spec 走，每完成一个 step 在 commit message 里注明对应 spec 文件名。