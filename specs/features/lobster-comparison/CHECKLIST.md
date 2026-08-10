# Lobster 对位 CHECKLIST

> 与 `~/桌面/github-project/LobsterAI`（约 28 skills + 11 IM 平台 + 6 preset agent + 20 LLM provider）的横向对位清单。
> 完成一项把 `- [ ]` 改成 `- [x] [commit-hash]`（commit 短 hash，前 7 位）。
>
> 维护规则见 `README.md` § 7。

---

## 1. 横向对位打分

| 维度 | LobsterAI | darvin-agent | 缺口性质 |
|---|---|---|---|
| **内置工具数** | ~28 skills + 多类工具 | 6 个 built-in (fs/search/shell/web_fetch/code_index/notebook_edit) | 工具面窄 |
| **IM 平台** | 11 个 (微信/钉钉/飞书/企微/QQ/TG/Discord/邮件...) | 0 | 大重构 (Tier 4) |
| **Preset agent (角色)** | 6 个 (股票/内容/课程/总结/健康/宠物) | 1 个默认主 Agent | 场景入口窄 (Tier 2) |
| **Office 三件套** | docx / xlsx / pptx / pdf 4 个 skill | 0 | UI 占位但无后端 (Tier 1) |
| **Email / Calendar** | IMAP/SMTP skill + macOS/Outlook 集成 | 0 | 全无 (Tier 2) |
| **LLM providers** | 20 个 (含全中国主流) | 1 (Anthropic);OpenAI/Custom 配了不连 | 半完成 (Tier 2) |
| **Scheduling** | cron / at / every + agentTurn 触发 | 0 | UI 占位 (Tier 3) |
| **Sub-agent** | subagent message/run store | 0 | UI 占位 (Tier 3) |
| **Memory FTS / RAG** | MEMORY.md 文件级 | stub (UI 有设置无后端) | 半完成 (Tier 3) |
| **Web search** | web-search skill | 仅 web_fetch (无搜索) | 缺 (Tier 1) |
| **Media gen** | image / video / seedance / seedream | 0 | 缺 (Tier 3) |
| **Computer Use** | Windows 桌面控制 MCP | 0 | 缺 (Tier 3) |

---

## 2. darvin 现状工具面（基线）

| 工具 | 状态 | 文件 |
|---|---|---|
| `read_file` / `write_file` / `edit_file` / `list_dir` / `move_file` / `multi_edit` / `delete_range` | ✅ | `internal/tools/fs.go` |
| `grep` / `glob` | ✅ | `internal/tools/search.go` |
| `shell` (30-item allowlist) | ✅ | `internal/tools/shell.go` |
| `web_fetch` (HTTPS GET, SSRF-guarded) | ✅ | `internal/tools/web_fetch.go` |
| `code_index` (Go AST outline/search/info) | ✅ | `internal/tools/code_index.go` |
| `delete_symbol` (Go AST delete) | ✅ | `internal/tools/delete_symbol.go` |
| `notebook_edit` (Jupyter .ipynb) | ✅ | `internal/tools/notebook_edit.go` |
| `mcp__<server>__<tool>` (MCP bridge) | ✅ | `internal/tools/mcp.go` |
| `bash_output` / `kill_shell` / `wait` / `list_jobs` (后台任务) | 🔄 spec 已写，**未实现** | `specs/features/builtin-tools-c-bg-jobs/` |
| `todo_write` / `complete_step` (结构化任务跟踪) | ✅ | `internal/tools/todo.go` |
| `compress` (按需上下文压缩) | ❌ spec 未写 | — |
| `use_capability` (MCP proxy 抽象) | 🔄 spec 已写，**未实现** | `specs/features/use-capability-mcp-proxy/` |

---

## 3. 缺口清单（按 Tier 排）

### Tier 1 · 必修（UI 已经在假装能做 / 用户立刻能感知到）

- [ ] **Office 三件套工具**（pptx / xlsx / docx）
  - **现状**：`quick.slide` / `home.example.data` / `home.example.doc` 三个 quick 按钮 + `home.greeting` 引诱用户点;点了 agent 只能回"做不到"。**最伤体验**。
  - **建议**：`pptx` (走 python-pptx via shell) / `xlsx` (openpyxl) / `docx` (pandoc + python-docx);或纯 shell-based 走 `weasyprint` / `xlsxwriter`。LobsterAI 是 SKILLs;darvin 可以走 skill + 内置工具两种形态。
  - **工作量**：~400-600 行 shell 工具 + 几个 skill wrapper;UI 已有入口,无新增。
  - **优先级**：🔴 最高

- [ ] **Web search 工具**
  - **现状**：只有 `web_fetch` 拿已知 URL;LLM 想"搜一下 XX"只能瞎编。
  - **建议**：一个 `web_search` 工具,接一个免费源(DuckDuckGo HTML / Brave Search API / SerpAPI);`web_fetch` 留作"已知 URL 抓取"。
  - **工作量**：~150 行 + 1 个 source 适配;UI 不需要改。
  - **优先级**：🔴 最高

- [x] 49efed0 (2026-08-10) **Todo + Goal 工具**
  - **现状**：`plus.todo.*` `plus.goal.*` 两条 UI 都是占位("能力尚未接入")。
  - **建议**：走 D spec;~300-400 行。**后端已落地**：`todo_write`（stateless 清单，args 即状态）+ `complete_step`（证据强制签收），见 commit `49efed0`；`update_goal` 明确不做（缺 host goal FSM，见 D spec §7）；**前端 TodoPanel 渲染为后续项**。
  - **优先级**：🔴 最高

---

### Tier 2 · 重要（影响"办公"场景主路径）

- [ ] **Email 工具 (IMAP/SMTP)**
  - **现状**：LobsterAI 有 `imap-smtp-email` skill;darvin 完全无。
  - **建议**：两个工具 `email_list` / `email_send`;`imap` + `smtp` 标准库足够;OAuth 留作后续。
  - **工作量**：~400 行 + 凭证存储;UI 新增邮件视图(可选)。
  - **优先级**：🟠 高

- [ ] **Calendar 工具**
  - **现状**：完全无。
  - **建议**：`calendar_list` / `calendar_create` / `calendar_update` / `calendar_delete`;走 CalDAV 标准协议(跨平台,macOS Calendar / iCloud / Google Calendar 都支持)。
  - **工作量**：~500 行。
  - **优先级**：🟠 高

- [ ] **LLM provider 扩展（OpenAI / DeepSeek / Qwen 等）**
  - **现状**：UI 让你配 OpenAI key / Custom endpoint,保存了 runtime 走 Anthropic-only。**这是 bug 不是 feature**。
  - **建议**：`internal/llm/` 已经有 `protocol.ModelProvider` 抽象 + `Registry`,加 OpenAI / DeepSeek / Qwen 适配各 ~300-500 行。LobsterAI 20 个 provider 不必对齐,先做 3 个(OpenAI + DeepSeek + Qwen)覆盖主要用户。
  - **工作量**：~900-1500 行 (3 个适配)。
  - **优先级**：🟠 高（性价比最高：bug 修复 + 场景解锁）

- [ ] **Preset agent 体系**
  - **现状**：单 `主 Agent` 走 IDENTITY/SOUL/USER;无法切换场景。
  - **建议**：把 IDENTITY/SOUL 文件做成可切换的 preset 列表(类似 LobsterAI 6 个 preset),agent switch 走 UI sidebar;每个 preset 是一个目录(`<workspace>/.claude/presets/<name>/IDENTITY.md` + `SOUL.md`)。
  - **工作量**：~200-300 行 renderer + preset JSON 文件;后端无大改。
  - **优先级**：🟠 高

---

### Tier 3 · 加分（场景化扩展 / 需大重构）

- [ ] **Scheduling / 定时任务**
  - **现状**：UI sidebar 入口 + settings 入口都在,都指向 placeholder;`specs/features/builtin-tools-c-bg-jobs/` 跟这个相关但只是前台 scheduler。
  - **建议**：`internal/cron` 包 + `internal/agents/scheduler`;3 个工具 `cron_create` / `cron_list` / `cron_delete`;payload 支持 `agentTurn`(定时给 agent 发消息) + `systemEvent`(纯通知)。
  - **工作量**：~500-800 行 + 持久化。
  - **优先级**：🟡 中

- [x] a204c72 (2026-08-10) **Sub-agent**
  - **现状**：UI 占位 (`artifact.special.subagents` = "子代理能力尚未接入")。
  - **建议**：已抽到独立子 spec → `specs/features/subagent/2026-08-09-subagent-design.md`（5 工具 + Manager + SQLite 持久化 + 并发限流 + stale 恢复）；**已实现落地**：5 内置工具 + Subagents artifact 面板 UI + 4 IPC 通道 + i18n，见 commit `a204c72`。
  - **工作量**：~2700 行（Go 后端，不含 UI）。
  - **优先级**：🟡 中

- [ ] **Memory FTS / RAG**
  - **现状**：`memory.Search()` 是 stub,UI 有 `embedding_provider` 设置无后端。
  - **建议**：`Manager.Search` 接 FTS5 + (可选)embedding;**~400 行**。
  - **优先级**：🟡 中

- [ ] **Media generation (image / video)**
  - **现状**：0。
  - **建议**：`image_gen` / `video_gen` 工具;接用户自己的 API key(Replicate / DashScope / 自部署)。
  - **工作量**：~300 行 + source 适配。
  - **优先级**：🟡 中

- [ ] **Continuity capsule（跨 session 上下文）**
  - **现状**：0。
  - **建议**：`internal/memory/capsule.go`;**~200 行**;跟 FTS 一起做更顺。
  - **优先级**：🟢 低（跟 FTS 一起）

- [ ] **Computer Use (Windows 桌面控制)**
  - **现状**：0。
  - **建议**：仅 Windows + 用户装 driver;~800 行 + 平台条件编译。
  - **优先级**：🟢 低（产品定位：darvin 当前不是"操作桌面"风格）

---

### Tier 4 · 大型重构（产品定位层）

- [ ] **IM 平台接入（11 个）**
  - **现状**：0;darvin 走 Electron 客户端单入口。
  - **建议**：**如果定位是"个人桌面助手",不需要;如果定位是"中国版 Coze Desktop",这是核心**。需要的话一平台 ~500-1500 行,11 平台 ~10K-15K 行,加 IPC schema 重新设计。
  - **优先级**：⚪ 取决产品方向

- [ ] **Skill marketplace / 第三方 skill 仓**
  - **现状**：LobsterAI 有 `openclaw-extensions/`;darvin skills 走 `<workspace>/.claude/skills/` 加载 + GitHub URL install,无 marketplace。
  - **建议**：走 GitHub + 自建 index 即可,不必做平台。
  - **工作量**：~300 行。
  - **优先级**：🟢 低

---

## 4. 完成进度

- Tier 1: 1/3 完成（Todo + Goal 后端，`49efed0`；前端面板后续）
- Tier 2: 0/4 完成
- Tier 3: 1/6 完成（Sub-agent，`a204c72`）
- Tier 4: 0/2 完成
- 总体: 2/15 项

最近一次更新: 2026-08-10（Todo + Goal 后端落地，`49efed0`）

---

## 5. 维护规则

1. **状态变更**：每完成一项,把 `- [ ]` 改成 `- [x] [commit-hash] [YYYY-MM-DD] [简短描述]`,并同步 §4 完成进度。
2. **范围调整**：新加/删除 Tier 1-4 任意项时,在 commit message 里说明原因(产品方向变化 / 已不再需要 / 拆分成独立 spec)。
3. **新对比对象**：每季度（建议）做一次对位,新引入参考项目时另开 spec 目录,不在本 spec 累积。
4. **子 spec 抽离**：任何 Tier 3/4 项启动实现时,从本 CHECKLIST 抽成独立子 spec 目录(避免本文件越长越乱);子 spec 的链接写回本 CHECKLIST 对应项。
