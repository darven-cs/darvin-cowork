# Agent 包实施路线图

> **范围**：本仓库的 Go 部分，含两个核心包：
> - `internal/agent/` — Agent 运行时（P0-P7），最终作为 Electron spawn 的子进程二进制运行
> - `internal/gateway/` — 网关服务（P8），独立 HTTP/WS 服务，多用户 / 鉴权 / 限流都在这里
>
> Electron 主进程 / 渲染层（仓库其他部分）负责 UI 和窗口；Go 部分负责运行时 + 网关。
>
> **执行顺序**：P0 → P1 → … → P7 把 agent 运行时打通；**P8 网关最后做**，等 agent 能力稳定。

---

## P0 — Electron 子进程接入（无 IPC = 不能用）

这是把当前二进制变成"Electron 能 spawn 并驱动的运行时"的最少集合。其他 P1-P4 再重要，Electron 也调不动。

- [ ] **IPC 协议选型** — stdio JSON-RPC / ndjson / 自定义帧；定 request/response/event 三通道的 schema
- [ ] **IPC server 在 cmd/app 启动** — listen stdin，把请求 dispatch 到 Agent.Prompt/Steer/FollowUp/Abort/Run
- [ ] **Event → stdout push** — agent.EventBus 流式输出（TextDelta/ToolStart/End/TurnStart/End/AgentEnd/AgentError）
- [ ] **优雅关闭** — SIGTERM/SIGINT 触发 Agent.Abort → flush pending events → close stdio
- [ ] **错误传递** — ProviderError + AgentErrorEvent 序列化成 IPC error 响应
- [ ] **最小 Electron 接入验证** — 一个 ts/electron 脚本 spawn 二进制、发送 prompt、收到流式事件、能 abort

## P1 — 会话持久化（重启不丢上下文）

不持久化，用户关掉应用再打开就失忆 — 桌面应用最基础体验。

- [ ] **SQLite SessionStore** — gorm 落 messages 表（schema 按 agent-loop spec §4.5）
- [ ] **Agent.Run 启动 Load** — `store.Load(sessionID)` 恢复 Session
- [ ] **Run 结束 Save** — dispatcher defer save
- [ ] **Session 列表 IPC** — Electron 能拉历史会话列表 + 切换
- [ ] **cmd/app 暴露 `--session-id`** — Electron 启动时传入持久化 id

## P2 — Memory 跨会话记忆（agent 真正"记得"用户）

Ingest 现在是空壳。Fact 数据模型都还不全。

- [ ] **Fact 数据模型补全** — `Fact{id, content, metadata{source, sessionId, confidence, tier, tags, createdAt, updatedAt}}`
- [ ] **Ingest fact 提取** — Ingest/IngestBatch 调一次 LLM 抽 facts → 写 short-term store
- [ ] **三层存储** — short（当前 session 摘要） / medium（每日笔记 md） / long（SQLite facts 表）
- [ ] **Daily notes 落盘** — `workspace/daily/YYYY-MM-DD.md`
- [ ] **Memory recall in Assemble** — 检索相关 facts 注入 `AssembleParams.AvailableFacts`
- [ ] **MemoryEvent 接入 event bus** — `memory.recall.recorded / memory.promotion.applied`
- [ ] **Light dreaming (cron)** — `Maintain()` 触发，定时整理 daily → medium

## P3 — Skills 系统（agent 能主动用 skill）

`SkillSummary` 占位类型定义了，但 0 loader、0 XML 注入。

- [ ] **Skill 加载器** — 扫描 `workspace/skills/*.md` 解析 YAML frontmatter
- [ ] **SkillEntry 注册表** — `SkillRegistry` 类型
- [ ] **`<available_skills>` XML 注入** — `formatSkillsForPrompt` + 接到 `composeSystemAddition`
- [ ] **SkillInvocationPolicy / SkillExposure** — `userInvocable / disableModelInvocation / mode / trigger`
- [ ] **Content hash 缓存** — `promptVersion` + 文件变更检测
- [ ] **Skill command dispatch** — `/skill-name args` 解析 + 路由

## P4 — MCP 集成（接外部工具生态）

零代码。`MCPServerInfo` 占位类型有，无 connector。

- [ ] **MCP transport (stdio + http)** — JSON-RPC 客户端
- [ ] **McpServerConfig YAML 块** — `config.yaml` 加 `mcp.servers.*` 段
- [ ] **McpManager** — 连接池 + 生命周期 + health check
- [ ] **MCP tool descriptors 注入** — `AvailableTools` 拼本地 + MCP，统一去重
- [ ] **Tool filter (glob)** — `include / exclude` 通配符匹配
- [ ] **Auth (api_key / oauth)** — 起码 api_key 走 header

## P5 — SubAgent 多 Agent 编排

`PrepareSubagentSpawn / OnSubagentEnded` 现在直接 `ErrSubAgentUnsupported`。

- [ ] **PrepareSubagentSpawn 真实实现** — 切上下文 + 配 instructions
- [ ] **OnSubagentEnded 实现** — 合并子 session 结果回父 + 触发 `SubagentEndReason`
- [ ] **EndReason 完整** — `completed / aborted / error / orphaned / timeout`
- [ ] **TTL + parentContextRef** — 子 session 生命周期管理

## P6 — LLM 多 provider（不止 Anthropic）

目前只 anthropic。Electron 用户要选模型就要多个。

- [ ] **OpenAI Completions provider** — `chat.completions` API
- [ ] **OpenAI Responses provider** — 新版 API + tool call ID 规范化（`fc_xxx|rs_xxx` → ≤64 字符）
- [ ] **Gemini provider** — `function_declarations` + thinkingConfig
- [ ] **Provider registry failover** — 主 provider 失败自动切换备用

## P7 — 完善打磨

- [ ] **Cache token 跟踪** — `Usage` 加 `cacheRead / cacheWrite / cacheWrite1h`
- [ ] **Cost 计算** — `Usage.cost` + `model.cost` 表 + `calculateCost`
- [ ] **Model registry** — `Model{id, contextWindow, maxTokens, cost, reasoning}`
- [ ] **Thinking 流式事件** — `thinking_start / delta / end`（Claude extended thinking 不再丢）
- [ ] **CompactionEvent 触发** — executor 在 Compact 成功后 emit
- [ ] **Tool executionMode** — `sequential / parallel` 控制
- [ ] **Agent.Reset / Continue** — 补公共 API
- [ ] **Image/multimodal** — `ContentBlock` 联合类型
- [ ] **Rate limit / Retry-After** — HTTPClient 读 header
- [ ] **stderr 日志** — 子进程日志走 stderr 不污染 stdout IPC 通道

## P8 — 网关 `internal/gateway/`（最后做，先完成 agent 运行时）

**位置**：`internal/gateway/`。与 agent 运行时**同 Go module、不同 package**，结构按原 `开发计划.md` M3 那一套。

**为什么放最后**：P0-P7 全在做 **agent 运行时本身**（Electron spawn 出来的那个 Go 子进程该有什么能力）。网关是**外面一层**的事 — 鉴权、会话管理、限流、代理、多用户隔离都属于"谁来调 agent 进程、怎么调、按什么策略调"。agent 能力没稳定之前，网关做的任何决策都可能在改。

**调用关系**：
```
客户端 (Electron / 浏览器 / CLI)
    ↓ HTTP / WS
internal/gateway/  ← P8（本桶）
    ↓ 进程内调用 or stdio IPC
internal/agent/    ← P0-P7（已稳定）
    ↓ HTTPS
Anthropic API
```

- [ ] **server.go** — HTTP/WebSocket 主服务（接收外部请求，转发给 agent）
- [ ] **router.go** — 路由定义（`/prompt /abort /subscribe /history /sessions` 等）
- [ ] **auth.go** — API Key 鉴权 + 用户身份（决定谁能调、调哪个 session）
- [ ] **ratelimit.go** — 限流（按用户/按 IP，防止单用户耗光 agent 进程）
- [ ] **proxy.go** — 上游代理（如果将来 agent 跑在远端机器）
- [ ] **middleware.go** — 日志 / 追踪 / CORS / 压缩中间件
- [ ] **会话注册表** — 网关侧的 `user → session_id → agent_pid` 映射（区别于 agent 内部的 `*session.Session`）
- [ ] **跨进程事件聚合** — 多个 agent 子进程的 event 流按 session 路由回客户端
- [ ] **桌面版简化路径** — 单用户本地场景下，Electron 主进程直接 spawn agent 子进程绕过网关（决定要不要双模式）

---

## 验证策略

每完成一个 P 桶跑：

- `go test -race ./...` 全绿
- `gofmt -l .` 干净
- `go vet ./...` 干净
- **子进程 smoke test**：一个独立 ts/electron 脚本 spawn 二进制 → 发 prompt → 收流式 events → abort → 重启后 Load 同一个 session 验证持久化

## 不在本包范围

- Electron 主进程 / 渲染层 / 窗口管理（仓库其他部分）
- 云端同步 / 多端协同
- 插件市场 / skill 共享生态

> 网关（鉴权 / 多用户 / 配额 / 计费 / 跨进程路由）**已纳入本路线图 P8**，但属于 `internal/gateway/` 独立 package，最后做。