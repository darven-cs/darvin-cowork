# Memory Subsystem — Implementation Checklist

Reference: LobsterAI `openclawMemoryFile.ts` + `openclawConfigSync.ts` + `coworkStore.ts` memory section + 4 specs (dreaming / workspace-decoupling / bootstrap-cwd / task-cwd-system-prompt).

## A. Storage & File Format (LobsterAI parity)

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| A1 | `state/workspace-main/` 作为唯一 memory 目录 | `openclawMemoryFile.ts:54` `getMainAgentWorkspacePath` | darvin 已有 `DARVIN_AGENT_WORKSPACE` env，落到 `state/workspace-main/` 子目录 |
| A2 | `MEMORY.md` block-aware parser（top-level bullet + indented children + verbatim） | `openclawMemoryFile.ts:131-232` | 上一版实现是简化版（只识别 top-bullet），需要重写为支持 indented children / code fence / HTML comment |
| A3 | `serializeEntryLines` 把用户文本拆成 first-line + 2-space indented 续行 | `openclawMemoryFile.ts:266-278` | 上版没有，导致多行 block 被拆成多个 bullet |
| A4 | `parseMemoryMd` dedup（first occurrence wins） | `openclawMemoryFile.ts:243-254` | 上版的 fingerprint 一样，但没有 dedup |
| A5 | Fingerprint：lowercase + `[^\p{L}\p{N}\s]` → space + `\s+` → ' ' + trim | `openclawMemoryFile.ts:74-80` | 上版用的是 ASCII only；CJK 漏 token。改用 Unicode property |
| A6 | `serializeMemoryMd` 完整序列化（header + 双换行 + blocks） | `openclawMemoryFile.ts:283-287` | 上版没单独抽出这个函数 |
| A7 | Add / Update / Delete / Search / ReadRaw / WriteRaw 全部 6 个 API | `openclawMemoryFile.ts:342-468` | 上版有，但缺 `serializeEntryLines` 集成 |
| A8 | `ensureBackup` 在第一次 write 之前备份为 `.bak`，永不覆盖 | `openclawMemoryFile.ts:312-323` | 上版只在 main file 已存在时备份，新版要无论先后都保一次 |
| A9 | `migrateSqliteToMemoryMd` 从旧 SQLite user_memories 一次迁移到 MEMORY.md | `openclawMemoryFile.ts:537-562` + `main.ts:7400-7417` | 上版没做 |
| A10 | `collectExistingFingerprints` + `appendMemoryTexts` 支持 workspace 切换时合并 dedup | `openclawMemoryFile.ts:479-518` | 上版没有 |
| A11 | `syncMemoryFileOnWorkspaceChange` 旧 workspace → 新 workspace 的 merge-dedup | `openclawMemoryFile.ts:643-689` | 上版没有 |

## B. SQLite + FTS5

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| B1 | `user_memories` 表（id / text / fingerprint / confidence / is_explicit / status / created_at / updated_at / last_used_at） | `coworkStore.ts:536-545` | 上版字段一致但缺 `fingerprint` 列 |
| B2 | `user_memory_sources` 表（provenance：memory_id / session_id / message_id / role / is_active / created_at） | `coworkStore.ts:547-555` | 上版有 |
| B3 | `memory_fts` virtual table (FTS5) — **trigram tokenizer**（CJK 必须） | `openclawConfigSync.ts:1880-1883` 显式注释 `unicode61 cannot tokenize CJK` | 上版用 unicode61 是错的，**必须改成 trigram** |
| B4 | `memory_index_meta_v1` 表存 schema version + tokenizer + provider + model | `openclawMemoryIndexMigration.ts:159-194` | 上版只有 `memories.meta.json` 文件版本，需要补 DB meta |
| B5 | FTS only-memory index migration（旧 tokenizer → trigram 的 lazy rebuild） | `openclawMemoryIndexMigration.ts:208-269` | 上版没做，需要新加 |
| B6 | FTS 一致性写入：`Save` row + delete-then-insert FTS（no UPSERT on FTS5 virtual table） | 上版的修复点 | **上版在写 entry 时用过 `INSERT ... ON CONFLICT(memory_id) DO UPDATE` — FTS5 不支持，必须先 DELETE 再 INSERT** |
| B7 | `getUserMemoryStats` SQL aggregate by status × is_explicit | `coworkStore.ts:2533-2564` | 上版 Stats 只按 section，没有按 status / is_explicit |

## C. Bootstrap Files

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| C1 | 白名单 `{IDENTITY.md, USER.md, SOUL.md}` + path-traversal 防护 | `openclawMemoryFile.ts:580-586` | 上版一致 |
| C2 | `readBootstrap / writeBootstrap` 原子写 | `openclawMemoryFile.ts:603-616` | 上版一致 |
| C3 | `ensureDefaultIdentity` 只在空 / 缺时写默认 IDENTITY.md | `openclawMemoryFile.ts:622-630` | 上版一致 |
| C4 | **默认 IDENTITY.md 跟随 locale**（zh / en） | `openclawMemoryFile.ts:568-578` | 上版 hardcode 英文，要按 `app.getLocale()` 切 |
| C5 | `AGENTS.md` 用户内容迁移（managed marker 之前的部分） | `openclawWorkspaceMigration.ts:141-188` | 上版没做 |
| C6 | `TOOLS.md` / `BOOTSTRAP.md` 也按 bootstrap 同样的"空目标才复制"策略 | `openclawWorkspaceMigration.ts:23,265-273` | 上版没做 |

## D. Settings / Config

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| D1 | `MemoryConfig` 完整字段：enabled / implicitUpdateEnabled / llmJudgeEnabled / guardLevel / userMemoriesMaxItems / embeddingEnabled / provider / model / vectorWeight / localModelPath / remoteBaseUrl / remoteApiKey | `coworkStore.ts:586-599` | 上版只覆盖 enabled + max_items + embedding 三字段；guardLevel / llmJudge / 4 个 embedding 字段缺 |
| D2 | `clampMemoryUserMemoriesMaxItems(value)` clamp `[1, 60]`，默认 12 | `coworkStore.ts:118-124` | 上版实现基本一致 |
| D3 | `normalizeMemoryGuardLevel` strict/standard/relaxed | `coworkStore.ts:103-106` | 上版没做 |
| D4 | `setAppPreferences` 拆成 `memory.*` patch + 真正持久化到 yaml | `main.ts:1016-1047` 现有 `setAppPreferences` | 上版 setAppPreferences 写的是 `enabled / embeddingProvider / apiKey`，忽略其他字段。要让 Settings UI 把全部字段写入 |
| D5 | 设置页右上角"未在 darvin-agent 接入"hint 删掉（接入已完成） | `SettingsPanelMemory.vue:49-51` | 文案改成"重启后生效" |

## E. Auto-Extract / Cleanup Pipeline

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| E1 | `isQuestionLikeMemoryText`（中英文 question prefix / suffix / inline + 标点） | `coworkStore.ts:90-101` | 上版只识别 prefix，没有 suffix / inline / 标点 |
| E2 | `MEMORY_PROCEDURAL_TEXT_RE` 7+ 命令白名单 | `coworkStore.ts:67` | 上版只 7 个命令；需要扩充 shell / git / curl / wget / `--flag` / `.sh` / `.bat` / `.ps1` / `$VAR` / `&&` / `/tmp/` |
| E3 | `MEMORY_ASSISTANT_STYLE_TEXT_RE` skill-meta / `使用 xxx 技能` | `coworkStore.ts:68` | 上版没做 |
| E4 | `shouldAutoDeleteMemoryText` 启动期清理 | `coworkStore.ts:354-362` | 上版只在 `AutoExtract` 时跳过，没启动期清扫 |
| E5 | `autoDeleteNonPersonalMemories()` 启动期调用一次 | `main.ts:1759-1762` | 上版没做（需要在 `getCoworkStore()` 第一次调用时跑） |
| E6 | AutoExtract 用 filter：拒 question / procedural / skill-meta / 短噪 | 上版简单版 | 需要用 E1-E3 的 isQuestionLikeMemoryText + MEMORY_PROCEDURAL_TEXT_RE |
| E7 | **near-duplicate 合并**（0.82 bigram-dice 阈值）+ preferred-text 选择 | `coworkStore.ts:271-331` + `createOrReviveUserMemory` | 上版只有 exact-fingerprint dedup，**没有 near-dup 合并** |
| E8 | Capacity 触顶时把最老条标 `stale`（不是 deleted） | `extract.go` `enforceCapacity` | 上版有；LobsterAI 也是 stale — 一致 |
| E9 | `MEMORY.md` raw view 写入时回写 SQLite（FTS 同步） | `manager.go WriteRaw` | 上版有；保留 |
| E10 | `LastUsedAt` 在 search / get 时更新 | `coworkStore.ts` 隐含 | 上版没有 LastUsedAt 写回 |

## F. Tool Integration (model-side)

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| F1 | `memory_search` 走 FTS5 MATCH（不是 LIKE） | 上版的 Search | 上版有，保留 |
| F2 | `memory_get(id)` 单条查询 | 上版有 | 保留 |
| F3 | `memory_write(text, section?, is_explicit=true)` | 上版有 | 保留 |
| F4 | **system prompt 政策**：write-before-confirm（marker 关键词命中） | `openclawConfigSync.ts:362-381` `MANAGED_MEMORY_POLICY_PROMPT` | 上版的 policy 没写 marker 触发词，模型不会主动 write |
| F5 | `memory/YYYY-MM-DD.md` daily notes 暂不实现（v1 不接 daily log） | — | 在 spec 里写为"v2 再做"，避免 scope creep |

## G. Pre-compaction Memory Flush Hook

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| G1 | AfterTurn 已经存在，但需要把 `protocol.Message[]` 真正喂给 AutoExtract | `ctxengine.after_turn.go` | 上版是空 stub |
| G2 | `AutoExtract` 走 `Last N=20 user message` + filter | `extract.go` 已经写 | 上版实现 OK 但 filter 太松（用 E1-E3 替换） |
| G3 | `isPreCompactionMemoryFlushPromptText` marker 检测 | `openclawHistory.ts:268-274` | 上版没有 |
| G4 | ctxengine SystemSection 注入 policy | `openclawConfigSync.ts:362-381` | 上版的 priority=50 + 内容基本一致；需要补 marker 触发词 |
| G5 | ShouldSuppressHeartbeatText 跳过 NO_REPLY / pre-compaction flush | `openclawHistory.ts:276-284` | 上版没做。需要在 renderer / dispatcher 端过滤 |

## H. Renderer UI

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| H1 | Settings → Memory tab 化（3 个 tab: entries / embedding / dreaming） | `Settings.tsx:4899-5135` | 上版只有平铺；需要拆 Tab |
| H2 | **Dreaming tab**（enabled / frequency cron / timezone / dream diary 只读展示） | `DreamingSettingsSection.tsx` + `specs/features/dreaming/...md` | 上版没做。**这是个独立大功能，单独 spec** |
| H3 | Raw view modal（textarea + save / reload） | `Settings.tsx:5060-5112` | 上版直接 textarea，没有 modal 包装 |
| H4 | Entries 列表按 section 分组、长文本 line-clamp-3 + expand、删除确认 | `Settings.tsx:4967-5058` | 上版没有 line-clamp |
| H5 | **Embedding section 完整**（provider 6 个 / model / vectorWeight / remote base url + api key / local model path） | `EmbeddingSettingsSection.tsx` + `openclawConfigSync.ts:1861-1878` | 上版只暴露 provider + apiKey，缺 4 个字段 |
| H6 | settings.memory.* 增 ~30 个 i18n key（entries title / empty / add / delete / raw / reload / save / embedding provider / dreaming title / frequency / etc.） | `i18n.ts` 当前 13 个 key | 上版只覆盖 13 个；要补齐到 ~30 |
| H7 | settings.ts 文件改名为 settings-memory.ts，列入 `settings-sections.ts` | — | 上版已经在 `settings-sections.ts` 里 |

## I. IPC + Preload

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| I1 | `cowork:memory:listEntries(query, status, includeDeleted, limit, offset)` 返回 entries | `main.ts:7383-7429` | 上版用 `memory.list_entries`，参数 shape 不同 |
| I2 | `cowork:memory:createEntry({text, confidence?, isExplicit?, source?})` | `main.ts:7430-7460` | 上版用 `memory.create_entry`，参数 shape 不同 |
| I3 | `cowork:memory:updateEntry({id, text?, confidence?, status?, isExplicit?})` | `main.ts:7460-7485` | 上版用 `memory.update_entry` |
| I4 | `cowork:memory:deleteEntry(id)` | `main.ts:7487-7505` | 上版用 `memory.delete_entry` |
| I5 | `cowork:memory:stats()` | `main.ts:7500-7510` | 上版用 `memory.get_stats`，字段不一致 |
| I6 | `cowork:memory:readRaw()` / `cowork:memory:writeRaw(content)` | 上版对齐 | 一致 |
| I7 | `cowork:memory:reindex()` | 上版对齐 | 一致 |
| I8 | `cowork:bootstrap:read(filename)` / `cowork:bootstrap:write(filename, content)` | 上版对齐 | 一致 |
| I9 | `cowork:bootstrap:agentId` 选项（per-agent workspace，非主 agent 用自己的 IDENTITY/SOUL/USER） | `main.ts:7606-7642` `resolveExistingAgentWorkspacePath` | **上版完全没做** — 只有 main workspace |
| I10 | `cowork:memory:rebuildIndex`（force） | `main.ts` 没有；LobsterAI 通过 `openclaw` 子进程跑 `openclaw.mjs memory index --force` | 上版没做 |

## J. Migration / Bootstrap

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| J1 | 启动期一次性 `migrateSqliteToMemoryMd`（lazy，kv flag） | `main.ts:7400-7417` | 上版没做 |
| J2 | 启动期一次性 `migrateMainAgentWorkspace`（含 AGENTS.md 用户区 / TOOLS.md / BOOTSTRAP.md） | `openclawWorkspaceMigration.ts:194-283` | 上版没做 |
| J3 | FTS index migration（FTS-only + tokenizer mismatch → rebuild） | `openclawMemoryIndexMigration.ts:208-409` | 上版没做 |
| J4 | `migratePerArtifact` 状态（per-file 标记，不是单个全局 flag） | `openclawWorkspaceMigration.ts:225-282` 仍单 key 但已注释要 per-artifact | 上版没做 |
| J5 | `MEMORY.md` 在 migrate 失败时不重写 meta（重试下次启动） | `openclawMemoryIndexMigration.ts:382-407` | 上版没做 |

## K. Workspace / cwd 分离（v1 可只占位）

| # | 任务 | LobsterAI 来源 | 备注 |
|---|---|---|---|
| K1 | 在 `Memory` policy section 里写明"长记忆在 agent workspace，task cwd 是项目目录" | `openclawConfigSync.ts:362-381` 隐含 | 上版 policy 模糊 |
| K2 | `state/workspace-main/` 路径常量（避开 `DARVIN_AGENT_WORKSPACE` env 漂移） | `openclawMemoryFile.ts:54-56` | 上版用 `memory.StateDir` 常量，但要保证不与用户 task cwd 重叠 |
| K3 | 旧版本用户的 `MEMORY.md` 从 `DARVIN_AGENT_WORKSPACE/MEMORY.md` 一次迁移到 `state/workspace-main/MEMORY.md` | 推断 | **不在 v1 scope**，写到 "Out of scope" |

## L. Out of Scope（明确不做）

- **Embedding 向量检索**：schema 字段保留，UI 加 tab，但代码不实接（与 LobsterAI 现状一致 — 字段保留）
- **LLM Judge auto-extract**：`LLMJudgeEnabled` 字段保留，默认 false
- **Dreaming 后台压缩 / DREAMS.md 解析**：v1 单独 spec（H2）
- **多 agent workspace 路由**：v1 单 main agent，per-agent IDENTITY/SOUL/USER 用 `agentId` 参数但只针对 main
- **跨 workspace 记忆同步**：各 workspace 自治
- **`memory/YYYY-MM-DD.md` daily notes**：v2
- **HEARTBEAT.md / BOOTSTRAP.md 实际语义**：v2
- **Embedding provider 实接 OpenAI / Gemini / Voyage / Mistral / Ollama**：v2

## M. 测试覆盖

| # | 任务 | 备注 |
|---|---|---|
| M1 | `parseMemoryMd` round-trip + dedup + fingerprint 稳定性 | 上版有，扩充多行 block |
| M2 | `serializeEntryLines` 多行 split 边界 | 新 |
| M3 | `MEMORY.md` workspace 切换合并 dedup | 新 |
| M4 | `shouldAutoDeleteMemoryText` 5 类 case | 新 |
| M5 | `isQuestionLikeMemoryText` 中文 / 英文 question | 新 |
| M6 | `MEMORY_PROCEDURAL_TEXT_RE` 正负样本 | 新 |
| M7 | FTS5 trigram 命中 CJK / 英文 | 新 |
| M8 | `autoDeleteNonPersonalMemories` 启动清扫 | 新 |
| M9 | `createOrReviveUserMemory` near-dup 合并 | 新 |
| M10 | `migrateSqliteToMemoryMd` 一次迁移 + 幂等 | 新 |
| M11 | `migrateMainAgentWorkspace` 5 类 file 迁移 | 新 |
| M12 | `migrateMemoryIndex` FTS5 trigram rebuild | 新 |
| M13 | gateway `memory.*` handler 10 个（与上版一致） | 上版有 |
| M14 | `SettingsPanelMemory.vue` 3 tab 切换 | 新 |

## N. Spec 拆分建议

变更较大，建议按以下 4 个 spec 拆分：

1. **`2026-MM-DD-memory-core-design.md`**：A1-A11, B1-B7, C1-C4, D1-D5, F1-F4 — 核心存储 + 文件 + 工具 + 配置
2. **`2026-MM-DD-memory-extract-cleanup-design.md`**：E1-E10 — 提取 / 过滤 / 近重复合并 / 启动清扫
3. **`2026-MM-DD-memory-bootstrap-agents-design.md`**：C5-C6, I9, J1-J5, K1-K3 — bootstrap 迁移 + per-agent workspace + 启动迁移
4. **`2026-MM-DD-memory-renderer-design.md`**：H1-H7 — Settings tab 化 + Embedding / Dreaming 独立 section + i18n
5. **`2026-MM-DD-memory-dreaming-design.md`**（可选第四份）：Dreaming 独立功能（参考 LobsterAI dreaming spec 8 大节）

各 spec 互不阻塞，可以并行实现 + 单独 review。

## O. 关键风险（沿用 LobsterAI 已踩过的）

1. **FTS5 tokenizer**：必须 `trigram`，不能 `unicode61`（CJK 漏 token）— LobsterAI 显式注释过这个坑
2. **FTS5 virtual table 不支持 UPSERT**：必须先 DELETE 再 INSERT
3. **systemPromptOverride 字段已废弃**：不能用 `agents.defaults.systemPromptOverride`，要走 OpenClaw 自带的 system-prompt 路径
4. **AGENTS.md 用户区迁移**：managed marker 之前的部分才算 user content
5. **目标 memory/ 目录非空会挡住迁移**：必须用 "目标空就复制" 而不是 "目标存在就跳过"
6. **IDENTITY.md 默认内容跟随 locale**：zh / en 两份
7. **path traversal 防护**：bootstrap filename 必须严格白名单 + `path.Base === name` 检查
8. **MEMORY.md 多行 block**：serializeEntryLines 把续行缩进 2 空格，否则 parseMemoryMd 会拆成多个 bullet
9. **MEMORY.md code fence / HTML comment**：verbatim 段保留时不能把它当 entry