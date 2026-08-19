# Architecture

> Three processes, one conversation. Every claim in this document is backed by a file path under `src/`.

darvin-cowork is split into three processes that talk through well-defined boundaries. The renderer is the only process that touches the user. The main process exists only to start the Go child and proxy JSON-RPC traffic. The Go agent is where every piece of business state lives.

## 1. Three processes

```mermaid
flowchart LR
  subgraph renderer["Renderer (Vue 3) — src/renderer/ + src/preload/"]
    R1["views / components / composables"]
    R2["services (i18n, markdown, artifact-renderer)"]
    R3["window.darvin (typed surface via contextBridge)"]
  end

  subgraph main["Main (Electron) — src/main/"]
    M1["ipcMain.handle (~70 channels)"]
    M2["RuntimeMgr (subprocess lifecycle) — src/main/runtime/manager.ts"]
    M3["AgentClient (WS JSON-RPC 2.0) — src/main/runtime/client.ts"]
    M4["EventRouter (agent.event → webContents.send) — src/main/store/EventRouter.ts"]
  end

  subgraph go["darvin-agent (Go) — src/darvin-agent/"]
    G1["cmd/app + internal/runtime (assembly)"]
    G2["internal/gateway (WS server + per-session handlers)"]
    G3["internal/agents + internal/sessionruntime (turn queue + executor)"]
    G4["internal/llm (anthropic / openai / gemini + streaming)"]
    G5["internal/tools (registry / permission / sandbox / mcp bridge)"]
    G6["internal/mcp + internal/skills + internal/im + internal/scheduledtask + internal/subagent + internal/memory"]
    G7["internal/database (GORM + SQLite)"]
  end

  R1 --> R3
  R3 -- "contextBridge / window.darvin" --> M1
  M1 -- "ipcMain.handle → AgentClient.request" --> M3
  M3 -- "ws://localhost:&lt;port&gt;/ws (JSON-RPC 2.0)" --> G2
  G2 -- "agent.event notifications" --> M3
  M3 -- "raw events" --> M4
  M4 -- "webContents.send" --> R1
  G6 --> G7
```

The renderer never imports `better-sqlite3`. The main process never sees LLM keys. The Go agent owns the schema, the agent loop, and every capability.

## 2. Go `runtime.Build` assembly

`runtime.Build` (`src/darvin-agent/internal/runtime/runtime.go`) is the single assembly entry. Frontend (cmd/app/main, future TUI) holds only the returned `*Runtime`.

```mermaid
flowchart TD
  S0["frontend: Build(ctx, Options{})"]
  S0 --> S1["resolveConfigPath()<br/>opts.ConfigPath ?? $DARVIN_CONFIG ?? exe-dir/cwd"]
  S1 --> S2["loadConfig + zap logger"]
  S2 --> S3["loadDatabase (database.Open → globalDB)"]
  S3 --> S4["loadProvider (anthropic / openai / gemini registry)"]
  S4 --> S5["resolveWorkspace (cfg.Agent.Workdir ?? opts.WorkspaceRoot)"]
  S5 --> S6["loadTools (NewBuiltins → sandbox + registry)"]
  S6 --> S7["memory.New(workspace)"]
  S7 --> S8["newAgentFactory (Agent + Harness + Loop plugins)"]
  S8 --> S9["bootstrapSkills (scan + load + runner)"]
  S9 --> S10["bootstrapMCP (resolveMCPPackagesDir + registry)"]
  S10 --> S11["EventLedger + SessionManager (with AgentFactory)"]
  S11 --> S12["Wire skill + mcp plugins into factory"]
  S12 --> S13["gateway.NewHandler (sessions, ledger, stores...)"]
  S13 --> S14["mcpReg.SetNotifier (handler hooks)"]
  S14 --> S15["gateway.NewServer.Start(ctx) — binds WS port"]
  S15 --> S16["scheduledtask.NewEngine + Start"]
  S16 --> S17["im.NewManager + Reload(activeWorkspaceID)"]
  S17 --> S18["im.NewHandlers (qr manager)"]
  S18 --> S19["bootstrap active session (sessions.EnsureEntry)"]
  S19 --> S20["return *Runtime (Cfg, Provider, Sessions, Handler, Server, MCP, Skills, Factory, Stores, IMManager, ScheduleEngine...)"]
```

`imBuilders()` (`runtime.go:43`) deliberately lives in `internal/runtime`, **not** in `internal/im`:

```
// imBuilders maps each channel to its live connector constructor. It lives
// here (not in internal/im) so the registry can import the connector
// subpackages without a cycle.
```

`runtime.Shutdown` (`runtime.go:120`) reverses the order independently and `errors.Join`s every error: `ScheduleEngine.Stop` → `IMManager.StopAll` → `Server.Shutdown` → `harness.DisposeAll` → `WorkspaceBootstrap.Dispose` → `Stores.Sessions.Close`.

The `Stores` struct (`runtime.go:102`) aggregates 11 SQLite-backed stores: `Sessions / Workspaces / Messages / AppState / ImportedFiles / Usages / Digests / Subagents / Agents / Schedules / IMChannels`.

`setWorkspace` closure (`runtime.go:248`) re-anchors the active workspace without rebuilding the agent: `toolsReg.SetWorkspaceRoot` → `mcpReg.SetRoots` (so filesystem-aware MCP servers see the new project) → `bootstrapSkills` against the new root → `skillPlugin.SetBootstrapResult` → `handler.Skills / SkillRunner` swap → `sessions.RefreshAllTools()`.

## 3. Main-process resolution

```mermaid
flowchart TD
  M0["prestart (npm script)"]
  M0 --> M1["npm run build:agent<br/>(scripts/build-go.js → CGO_ENABLED=0)"]
  M1 --> M2["npx electron-rebuild -w better-sqlite3"]
  M2 --> M3["electron-forge start"]
  M3 --> M4["src/main/index.ts → createWindow()"]
  M4 --> M5["RuntimeMgr.resolveAgentBinaryPath()<br/>app.isPackaged ? process.resourcesPath/bin/...<br/>else __dirname/../../../bin/..."]
  M5 --> M6["spawn(bin)"]
  S1["stdout"] --> T1
  S6["spawn(bin)"] -.stdout.->S1
  T1 --grep &quot;&lt;port&gt;…&lt;/port&gt;&quot; line--> T2
  T2 --> T3["AgentClient.connect(ws://localhost:&lt;port&gt;/ws)"]
  T3 --> T4["EventRouter.start()<br/>subscribes to AgentClient raw events"]
  T4 --> T5["AgentClient.request / prompt / abort / subscribe_events / listSessions / getMessages"]
  T5 --> M1x["ipcMain.handle('agent.*', ...)"]
  M1x --> W1["window.darvin (typed, via contextBridge)"]
```

Key files: `src/main/runtime/manager.ts` (`RuntimeMgr` + `resolveAgentBinaryPath`), `src/main/runtime/client.ts` (`AgentClient`), `src/main/store/EventRouter.ts` (pure forwarder: `agent.event` → `webContents.send`). The Electron main opens `remote-debugging-port=9222` only in dev (`!app.isPackaged`) so `electron-cdp` can drive the window without launching a second browser.

`EventRouter` is intentionally thin: it does not read active session, does not consult the store, and does not run any logic beyond `webContents.send`. On `done` it triggers `notifyIfHidden` so a user who switched away sees an OS notification with the title (looked up via `getTitle` callback, FR-10).

## 4. IPC contract layering

`src/shared/darvin-api.ts` is the single source of truth for everything that crosses the process boundary.

```mermaid
flowchart LR
  subgraph contract["src/shared/darvin-api.ts — single source of truth"]
    A["DarvinApi<br/>(~70 request/response methods)<br/>session / message / skill / MCP / workspace<br/>LLM / locale / prefs / attachment<br/>permission / artifact"]
    P["DarvinPushEvent<br/>(push notification constants)<br/>SessionsChanged / ActiveSessionChanged<br/>SessionEvent / WorkspaceChanged<br/>SkillsChanged / McpServersChanged<br/>McpConnectionChanged"]
    E["DarvinEvent (streaming union)<br/>text_delta / thinking_delta<br/>tool_start / tool_end<br/>done / error / agent_end<br/>compaction / context_usage<br/>permission_request / artifact"]
    M["DarvinMessage (message union)<br/>user / assistant / tool_use / tool_result / system"]
    H["helpers: parseDarvinEvent, assertNever,<br/>BACKEND_DEFAULT_SESSION_ID"]
  end

  subgraph renderer2["src/renderer/"]
    R["window.darvin consumer<br/>(typed shapes)"]
  end

  subgraph preload2["src/preload/index.ts"]
    Px["contextBridge.exposeInMainWorld('darvin', api)"]
  end

  subgraph main2["src/main/"]
    Mx["ipcMain.handle + AgentClient.request"]
  end

  subgraph go2["src/darvin-agent/internal/gateway/"]
    Gx["handlers_*.go + parseDarvinEvent"]
  end

  R -- "imports types" --> contract
  Px -- "imports types" --> contract
  Mx -- "imports types" --> contract
  Gx -- "imports types" --> contract
```

Rule (enforced by convention, no automated check): adding a new IPC channel means **three places** — `DarvinApi` in `darvin-api.ts` → `ipcMain.handle` in main → `window.darvin` method in preload. Wire types use `<Domain>Wire` suffix to separate internal business types from IPC protocol types. `parseDarvinEvent` and `assertNever` are the helpers every Go / TS side calls.

## 5. Session lifecycle and `SessionRuntime` container

```mermaid
flowchart TD
  G0["gateway.SessionManager (in-memory map)"]
  G0 -- "subscribe(activeID)" --> E0["SessionEntry (no SessionRuntime)<br/>subscribe-only; no agent spun up"]
  G0 -- "Submit(activeID, prompt)" --> E1["SessionEntry.SessionRuntime<br/>(lazy build on first prompt)"]

  subgraph srt["SessionRuntime (internal/sessionruntime)"]
    subgraph sub["Subagents"]
      SUB1["sub-agent 1 session"]
      SUB2["sub-agent 2 session"]
    end
    subgraph delta["DeltaHook (internal/agents/text_delta_hook)"]
      DH["text_delta accumulator / artifact detect"]
    end
    subgraph loop["Loop (internal/sessionruntime/loop)"]
      L1["Submit / Steer / Stop / Abort / Close"]
      L2["per-session turn queue (buffer 1)"]
      L3["Agent (Controller Idle/Running)"]
    end
  end

  E1 --> loop
  loop --> L1
  L1 --> L2
  L2 --> L3
  L3 --> delta
  L3 --> sub

  CL["SessionRuntime.Close"] --> SUB1
  CL --> SUB2
  CL --> DH
  CL --> loop
```

`SessionManager` (`internal/gateway/sessionmgr.go`) holds an LRU map (`idleElem` linked list, `DefaultIdleTTL = 24h`, soft cap `DefaultMaxSessions = 5000`) of `*SessionEntry`. An entry without `SessionRuntime` can be **subscribed to** but not **submitted to**. First `Prompt` builds the runtime via `AgentFactory` (`internal/sessionruntime/factory.go`).

`Controller` (`internal/agents/controller.go`) owns the per-agent `Idle / Running` state machine and the per-turn cancel function: `TryStart` transitions Idle→Running, `End` cancels + flips back to Idle, `SetCancel` binds the next turn's cancel, `Abort` fires it without changing state.

`Loop` (`internal/sessionruntime/loop.go`) owns one session's turn queue with **strict steer priority** (`internal/agents/queue.go`'s `promptCh` / `steerCh` channels, both buffer 1). `Submit / Steer / SubmitSkill / Stop / Abort / Close` are the only mutators. `PromptRequest` carries `attachments / images / provider / model` per-turn override + `ReplySink` for headless integrations (scheduled tasks, IM channels).

## 6. Single agent turn — sequence diagram

```mermaid
sequenceDiagram
  autonumber
  actor User
  participant V as Renderer (ChatView)
  participant E as Main (ipcMain.handle + AgentClient)
  participant G as Gateway (handler_prompt.go)
  participant L as Loop (sessionruntime)
  participant A as Agent (agents)
  participant LL2 as LLM (llm)
  participant TR as Tools Registry
  participant P as Permission (tools/perm)
  participant SB as Sandbox (tools/sandbox)
  participant DB as SQLite (database)
  participant EL as EventLedger (gateway)
  participant ER as EventRouter (main/store)

  User->>V: type prompt + Enter
  V->>E: window.darvin.prompt(req)
  E->>G: ws.send agent.prompt (JSON-RPC 2.0)
  G->>L: SessionManager.Submit → Loop.Submit
  L->>L: mint messageID, userMsgID (nanoid 21 chars)
  L->>A: enqueue(ModePrompt, ...)
  L->>A: Agent.Run (Controller.TryStart Idle→Running)
  A->>A: hydrate messages from MessageStore
  A->>LL2: provider.Prompt (stream)
  LL2-->>A: text_delta event (×N)
  LL2-->>A: thinking_delta event (×N)
  A->>EL: ledger.Append(text_delta) [via DeltaHook]
  EL-->>ER: gateway WS broadcast agent.event
  ER-->>V: webContents.send text_delta
  loop tool loop (zero or more rounds)
    A->>TR: invoke tool (name, args)
    TR->>P: ClassifyPermission(name, args)
    alt auto-allow (safe)
      P-->>TR: level=safe, need=false
    else need approval
      P-->>A: permission_request push
      A->>EL: ledger.Append(permission_request)
      EL-->>V: webContents.send permission_request
      V->>E: window.darvin.permissionResponse(approve|deny)
      E-->>A: resolve
    end
    TR->>SB: Sandbox.ResolveRead / ResolveWrite
    SB-->>TR: ErrPathEscapes / ok
    TR-->>A: tool_result
    A->>LL2: tool_result → next LLM round
    LL2-->>A: text_delta / tool_start / tool_end / done
  end
  A->>A: Controller.End (Running→Idle)
  A->>DB: store.MessageStore.Append (assistant message)
  A->>EL: ledger.Append(agent_end)
  EL-->>V: agent_end
```

Notes:

- `hydrate` runs **before** the first LLM round (`sessionruntime/hydrate.go`): pulls messages from `MessageStore` so a reloaded session resumes seamlessly.
- DeltaHook (`agents/text_delta_hook.go`) deduplicates `text_delta` events at the gateway boundary so the wire stays small even if the provider streams rapidly.
- `agent_end` (terminal event) carries the final assistant text + usage payload; the renderer swaps the streaming placeholder for the persisted message.
- `Ctrl+C` mid-turn or `window.darvin.abort()` flows as `Controller.Abort()` → context cancel → dispatcher returns → `Controller.End()`.

## 7. MCP bridge — tool call → MCP server → response

```mermaid
sequenceDiagram
  autonumber
  participant A as Agent (agents/dispatcher)
  participant TR as Tools Registry (tools/registry)
  participant BR as MCP Bridge (tools/mcp.go)
  participant RG as mcp.Registry (internal/mcp)
  participant TR2 as mcp.Transport (http/sse/stdio)
  participant SRV as MCP Server (external)

  A->>TR: callTool(name, args)
  TR->>BR: dispatch MCP tool
  BR->>RG: Registry.Resolve(serverName)
  alt unresolved
    RG->>TR2: launcher.Launch (fingerprint → http/sse/stdio)
    TR2->>SRV: initialize (MCP handshake)
    SRV-->>TR2: initialize result + capabilities
    TR2->>SRV: tools/list
    SRV-->>TR2: [tools]
    RG->>RG: cache tools + connection
  end
  RG->>TR2: callTool (tools/call)
  TR2->>SRV: tools/call (JSON-RPC)
  SRV-->>TR2: result
  TR2-->>BR: result
  BR-->>TR: tool_result (with redaction)
  BR-->>A: continue turn
```

- `mcp.Registry` (`internal/mcp/registry.go`) is the single owner of every server's connection + tools.
- `transport` (`internal/mcp/transport/`) speaks `http` / `sse` / `stdio` per the server's launch config.
- `resolver_fingerprint` (`internal/mcp/resolver_fingerprint.go`) determines whether a config change is the same server (no relaunch) or a new one (close + relaunch). `retryResolution` is the user-facing action that runs this fingerprint again.
- `redact.go` strips secrets from server logs.
- `notifier.go` (`OnConnectionChanged / OnResolutionChanged / OnToolsChanged`) calls `handler.OnMcp*` so the renderer refetches via push events.

## 8. Tool permission gate

```mermaid
flowchart TD
  C0["Agent dispatches tool(name, args)"]
  C0 --> C1["DangerClassifier (per-tool / MCP plugin self-classify)"]
  C1 --> C2["ClassifyPermission (tools/permission.go)"]
  C2 --> SH["shell? command regex<br/>(destructivePatterns / cautionPatterns)"]
  SH --> C3["level"]
  C3 --> L1["safe"]
  C3 --> L2["caution"]
  C3 --> L3["destructive"]
  L1 --> A1["auto-allow"]
  L2 --> A2["permission_request push"]
  L2 --> A3["renderer modal: Allow / Deny / Allow-always"]
  L3 --> A4["permission_request push"]
  A4 --> A5["renderer modal: Allow / Deny"]
  A2 --> A6{"user response"}
  A5 --> A6
  A6 -- allow --> A7["proceed"]
  A6 -- deny --> A8["abort tool + tool_result error"]
  A1 --> A7
```

Shell tool classification (`tools/permission.go`) is pattern-based. `destructivePatterns` (checked first) match: `rm -r / rm -rf`, `git push … --force / -f`, `git reset --hard`, `dd`, `mkfs.*`, `shutdown / reboot / init 0`, fork bomb `:(){`, `chmod -R / chown -R`, `find … -delete`. `cautionPatterns` match: `rm`, `git push`, `git clean`, `chmod / chown`, `kill / pkill`, `sudo`, `mv / cp`.

Non-shell tools fall through with `level=safe, need=false`; plugins that self-classify implement `DangerClassifier` and are consulted by `EvaluatePermission`. Sandbox (`tools/sandbox.go`) enforces path containment (lexical + symlink), read-byte cap (`maxHardReadBytes = 16 MiB`), and the `attach = authorize` semantics.

## 9. IM lifecycle — Create → Start → Inbound → Reply

```mermaid
sequenceDiagram
  autonumber
  actor User
  participant V as Renderer (ImView)
  participant E as Main (imProxy → AgentClient)
  participant G as Gateway (handler_im.go)
  participant M as im.Manager
  participant DB as SQLite (IMChannels store)
  participant C as Connector (qq/wecom/weixin)
  participant SR as SessionRunner (gateway.SessionManager)
  participant LOOP as Loop (headless session)
  participant PEER as IM peer (user)

  User->>V: click "添加实例" + save
  V->>E: window.darvin.imCreate({channel, name, config, enabled})
  E->>G: ws.send agent.im.create
  G->>M: Manager.Create(ctx, ch)
  M->>DB: IMChannelStore.Create
  alt enabled
    M->>M: buildInstance → Connector.Start
    C-->>M: ok / lastError
  end
  M->>G: Broadcast ImChanged (push event)

  Note over C,PEER: connector is now connected; long-poll / WS depending on channel
  PEER->>C: inbound message
  C->>M: Manager.handleInbound (via setInboundHandler)
  M->>M: authorized(peer) (access policy gate)
  M->>SR: SubmitForIM(ctx, imKey, prompt, ReplySink)
  SR->>LOOP: ensure session + Loop.Submit
  LOOP-->>SR: final assistant text via ReplySink
  SR-->>M: sink(reply, runID)
  M->>C: Connector.Send(ctx, target, outbound)
  C-->>PEER: outbound message
```

`Manager.handleInbound` (`internal/im/manager.go`) is registered as the connector's `SetInboundHandler` callback. `SubmitForIM` (`gateway.SessionManager`) creates a dedicated session per IM instance, the agent runs headless (no live WS subscriber), and the `ReplySink` writes back through `Connector.Send`. Each IM instance owns its a dedicated workspace per IM instance (in per `imManager.ensureIMWorkspace`) so the renderer UI groups sessions from the same channel under a stable, named workspace.

## 10. SQLite schema (GORM + `glebarez/sqlite`)

```mermaid
erDiagram
  SESSIONS ||--o{ MESSAGES : has
  SESSIONS ||--o{ USAGES : has
  SESSIONS ||--o{ SUBAGENTS : spawns
  SESSIONS }o--|| WORKSPACES : "bound to"
  SESSIONS ||--o{ DIGESTS : has
  SESSIONS }o--o| APP_STATE : "active_id"
  MESSAGES ||--o{ IMPORTED_FILES : "references"

  SESSIONS {
    string id PK "21-char nanoid"
    string workspace_id FK
    string title
    string system_prompt
    string model_provider
    string model_name
    int created_at
    int updated_at
  }
  MESSAGES {
    string id PK "21-char nanoid"
    string session_id FK
    string role "user|assistant|tool_use|tool_result|system"
    text content
    json tool_calls
    json tool_results
    int created_at
  }
  WORKSPACES {
    string id PK
    string name
    string root_path
  }
  APP_STATE {
    string key PK
    string value
  }
  IMPORTED_FILES {
    string id PK
    string path
    string hash
    int size
    int imported_at
  }
  MEMORY {
    string id PK
    string scope "user|project"
    text content
    int created_at
    int updated_at
  }
  MCP_SERVERS {
    string id PK
    string name
    string transport "http|sse|stdio"
    json config
    bool enabled
  }
  SKILLS_BOOTSTRAP {
    string id PK
    string name
    string source_path
    bool enabled
  }
  SCHEDULED_TASKS {
    string id PK
    string name
    string cron_expr
    string prompt
    bool enabled
  }
  IM_CHANNELS {
    string id PK
    string workspace_id FK
    string channel "qq|wecom|weixin"
    string name
    bool enabled
    json config
    string access_mode "open|allowlist|disabled"
  }
```

Schema is owned by `internal/agents/store/` (concrete SQLite types in `*_test.go` are exercised by the unit tests). `globalDB` lives in `internal/database/database.go`; main process **never** sees SQLite.

## 11. Why three processes?

- **Renderer stays simple.** It holds UI state and nothing else. No SQLite, no filesystem outside its sandbox, no secrets.
- **Main stays thin.** `~70` `ipcMain.handle` channels proxy typed JSON-RPC to Go. Adding a new IPC channel is a two-place change in `src/shared/darvin-api.ts`.
- **Go owns everything that matters.** The agent loop, tool registry, context compaction, MCP clients, skills, memory and persistence all live in one process you can attach a debugger to, run `go test` against, and grep through. The single static binary (`CGO_ENABLED=0`) makes the desktop app trivially cross-platform.

## 12. What's intentionally not in the main process

- **SQLite** — owned by Go (`globalDB` in `internal/database`). Main only proxies.
- **LLM keys** — read by Go viper from `LLM_API_KEY` env / user `config.yaml` / repo `config.yaml`. Main never sees the raw key.
- **Tool execution** — all tool calls run in the Go process so the renderer cannot bypass permissions or sandbox.
- **IM sessions** — per-channel connectors live in Go, accessed by the renderer through `window.darvin.im*` IPC only.

## 13. Cross-package rules

Enforced by `Makefile`'s `lint-agents-boundaries`:

- `internal/agents/` may NOT import capability packages (`llm / tools / skills / mcp`).
- `internal/im/` interfaces are defined in the consumer package (`contract.go`); connector implementations live in `internal/im/<channel>/`.
- `imBuilders()` lives in `internal/runtime` (not `internal/im`) to avoid a cycle with the connector subpackages.

## Next

- [Quickstart](./QUICKSTART.md) — install, configure, build, troubleshoot.
- [Guide](./GUIDE.md) — sessions, tools, todos, sub-agents, MCP, skills, memory, artifact sandbox, expert suite, scheduled tasks, IM overview, settings.
- [IM Channels](./IM.md) — QQ / WeCom / WeChat connectors, real connectivity probe, instance management.
- [Development](./DEVELOPMENT.md) — dev workflow, lint / test / smoke, Go `fmt` / `vet` / `lint`, project-specific engineering rules.