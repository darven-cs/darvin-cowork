# Architecture

> Three processes, one conversation. Vue 3 renderer, Electron shell, Go agent runtime.

## Overview

darvin-cowork is split into three processes that talk to each other through well-defined boundaries. The renderer is the only process that talks to the user. The main process exists only to start the Go child and proxy JSON-RPC traffic. The Go agent is where every piece of business state lives.

```
┌────────────────────────┐    Electron IPC (ipcMain.handle / ipcRenderer.invoke)
│   Renderer (Vue 3)     │ ◄───────────────────────────────────────────────────┐
│   src/renderer/        │                                                     │
│   src/preload/         │ ─── contextBridge exposes window.darvin (typed) ──► │
└────────────────────────┘                                                     │
                                                                                ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│   Main (Electron)                                                             │
│   src/main/                                                                   │
│   - BrowserWindow + DevTools + remote-debugging-port=9222 (dev only)          │
│   - RuntimeMgr: spawns darvin-agent-<platform>-<arch>, reads <port> from     │
│     stdout (5s timeout)                                                       │
│   - AgentClient: WebSocket JSON-RPC 2.0 client                                │
│   - EventRouter: agent.event → webContents.send (pure forwarder)             │
│   - ~70 ipcMain.handle channels (session / message / skill / MCP / workspace │
│     / LLM / prefs / locale / runtime status / attachment / permission / ...)  │
└───────────────────────────────────────────────────────────────────────────────┘
                                                                                │
                                                                                ▼  WebSocket
                                                                       ws://localhost:<port>/ws
                                                                                │
┌───────────────────────────────────────────────────────────────────────────────┐
│   darvin-agent (Go)                                                           │
│   src/darvin-agent/                                                           │
│   - Gateway: WS JSON-RPC server, per-session handler dispatch                 │
│   - SessionRuntime: per-session agent loop, lifecycle container               │
│   - Agents: dispatcher / executor / agent loop / ctx engine / permissions     │
│   - LLM: anthropic / openai / gemini providers, streaming protocol           │
│   - Tools: built-in tools (fs / shell / search / web_fetch / code_index /     │
│     sandbox / todo / subagent / notebook_edit / mcp bridge)                   │
│   - MCP: client (http / sse / stdio) + launcher + registry                    │
│   - Skills: scanner / loader / frontmatter / registry / runner                │
│   - IM: QQ / WeCom / WeChat connectors with Prober (one-shot connectivity)   │
│   - ScheduledTask / SubAgent / Memory / Todos                                │
│   - Persistence: GORM + SQLite (sessions.db, MCP, skills, scheduled tasks,    │
│     IM channels, memory)                                                      │
└───────────────────────────────────────────────────────────────────────────────┘
```

## Why three processes?

- **Renderer stays simple.** It holds UI state and nothing else. No SQLite, no filesystem outside its sandbox, no secrets.
- **Main stays thin.** ~70 `ipcMain.handle` channels proxy typed JSON-RPC to Go. Adding a new IPC channel is a two-place change in `src/shared/darvin-api.ts`.
- **Go owns everything that matters.** The agent loop, tool registry, context compaction, MCP clients, skills, memory and persistence all live in one process you can attach a debugger to, run `go test` against, and grep through. The single static binary (`CGO_ENABLED=0`) makes the desktop app trivially cross-platform.

## Go runtime layout

`src/darvin-agent/` is a Go module (`backend`) with a single assembly entry (`cmd/app/main.go` → `internal/runtime`) and 15+ `internal/` packages.

| Package | Responsibility |
|---|---|
| `cmd/app` | 15-line entry: `os.Exit(runApp(os.Args[1:]))`; `runApp` is a var, test-replaceable |
| `internal/runtime` | `Build(ctx, Options) (*Runtime, error)` loads config + DB + LLM provider, assembles the agent factory, bootstraps skills / MCP, starts the gateway, bootstraps the active session; `Run(args)` wires SIGINT / SIGTERM; `Shutdown(ctx)` closes server / harness / SQLite |
| `internal/gateway` | WebSocket server, JSON-RPC framing, handler dispatch, per-session manager, per-session event ledger |
| `internal/sessionruntime` | Per-session agent runtime. `AgentFactory` assembles Agent + Harness + Loop + DeltaHook + Subagents; `Loop` owns the per-session serialized turn queue; `SessionRuntime` is the lifecycle container (Close chains Subagents → DeltaHook → Loop) |
| `internal/agents` | `Agent.Prompt / Run / Abort / Subscribe`; dispatcher enqueue + `runMsgID`; subpackages `queue / session / store / executor / perm / ctxengine / msgid / protocol / runtime / usage` |
| `internal/llm` | Streaming protocol + `anthropic` / `openai` / `gemini` providers + model registry; events and errors in separate files |
| `internal/tools` | Built-in tools + permission registry + MCP bridge; exclusions file whitelist |
| `internal/skills` | Scanner / loader / frontmatter / registry / plugin / runner / wire; install goes through `skillInstall` (main side) + `wire` (Go side) |
| `internal/mcp` | Client / launcher / registry / transport (http + sse + stdio) / resolver fingerprint / persistence |
| `internal/scheduledtask` | Cron-style task engine with per-task run history |
| `internal/subagent` | Sub-agent orchestration (delegate / parallel / abort / list / read-result) |
| `internal/memory` | Lightweight memory manager |
| `internal/database` | GORM + `glebarez/sqlite` single `globalDB`; `internal/agents/store/` owns session / message / app_state / imported_file / memory |
| `internal/config` | viper configuration loading |
| `internal/logger` | zap + lumberjack log rotation |
| `internal/harness` | Frontend / CLI / embedded runner and capability registration |
| `internal/jsonschema` | Schema normalization and validation |

Cross-package rules:

- `agents/` may NOT import capability packages (`llm / tools / skills / mcp`); enforced by `Makefile`'s `lint-agents-boundaries` target.
- `internal/im/` interfaces are defined in the consumer package (`contract.go`); connector implementations live in `internal/im/<channel>/`.

## IPC contract — single source of truth

Every IPC channel, push event, streaming event and message type lives in one file:

- `src/shared/darvin-api.ts`
  - `DarvinApi` — the request/response interface (~70 methods; session / message / skill / MCP / workspace / LLM / locale / prefs / attachment / permission / artifact)
  - `DarvinPushEvent` — push event constants (`SessionsChanged / ActiveSessionChanged / SessionEvent / WorkspaceChanged / SkillsChanged / McpServersChanged / McpConnectionChanged`)
  - `DarvinEvent` — discriminated union (`text_delta / thinking_delta / tool_start / tool_end / done / error / agent_end / compaction / context_usage / permission_request / artifact`)
  - `DarvinMessage` — discriminated union (`user / assistant / tool_use / tool_result / system`)
  - Helpers: `parseDarvinEvent`, `assertNever`
  - Per-channel record shapes (`DarvinIMInstance`, `DarvinIMStatus`, etc.)

Conventions:

- One source. Main / preload / renderer all import from `darvin-api.ts`. No `any` in components.
- Add a new IPC channel in three places: `DarvinApi` in `darvin-api.ts` → `ipcMain.handle` in main → `window.darvin` method in preload.
- Wire types use `<Domain>Wire` suffix to separate internal business types from IPC protocol types.

## Main process

`src/main/index.ts` (~67 KB) is the only Electron-side file that holds business knowledge:

- Handles `electron-squirrel-startup` short-circuit on Windows.
- `createWindow()` builds the `BrowserWindow`; preload points to the Vite output.
- Dev mode: `loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL)`, DevTools open by default, `remote-debugging-port=9222` + `remote-allow-origins=*` (so `electron-cdp` can drive the window for E2E).
- ~70 `ipcMain.handle` channels (see the full list in `src/shared/darvin-api.ts`).
- `RuntimeMgr` (subprocess lifecycle) + `AgentClient` (WS JSON-RPC client) + `EventRouter` (pure forwarder).
- `window-all-closed` quits on non-macOS; `activate` rebuilds the window if needed.
- After the merge-databases refactor, main no longer holds SQLite; if Go is offline the renderer sees an in-memory cache of the last-known view.

`src/main/runtime/manager.ts`:

- `resolveAgentBinaryPath()` — picks `process.resourcesPath/bin/...` (packaged) or the dev path (`__dirname` three levels up). Missing binary prints a warning and does not throw.
- `start(workspaceRoot?)` — `spawn(bin)`, reads `<port>…</port>` from stdout (5 s timeout), SIGTERM + 4 s grace period on stop.
- Exposes `pid() / port() / isResolved() / resolveAgentConfigPath()` (dev only).

`src/main/runtime/client.ts`:

- `class AgentClient` — WebSocket JSON-RPC 2.0 client over `ws://localhost:{port}/ws`.
- Full method surface: `connect / disconnect / request / prompt / abort / invokeSkill / subscribeEvents / listSessions / getMessages` + namespaced `skills.{list,setEnabled,bootstrap,onChanged}` / `mcp.{list,register,update,unregister,setEnabled,test,retryResolution,bootstrap,onConnectionChanged,onResolutionChanged}` / `tools.list`.
- Helpers: `parseDarvinEvent`, `BACKEND_DEFAULT_SESSION_ID`.

## Preload

`src/preload/index.ts` exposes a typed surface via `contextBridge.exposeInMainWorld('darvin', api)`. Renderer code never imports `electron`; everything comes through `window.darvin`.

## Renderer

Vue 3 + Vite (`root: 'src/renderer'`, `base: './'` for production relative paths). Specifics:

- **Vue 3 SFC + `<script setup lang="ts">`** + Composition API only. No mixins, no class-based components, no Options API.
- **Tailwind CSS v4** via `@tailwindcss/vite`. Design tokens live in `src/renderer/styles/theme.css` `@theme` block — components use utility classes (`bg-surface` / `text-text-muted` / `rounded-md`); no `<style>` blocks, no magic values.
- **Icon system** — ~70 SVG icons in `src/renderer/assets/icons/` auto-globbed through `import.meta.glob`. `Icon` component takes `name` + `:size`. All `stroke="currentColor"`.
- **i18n** — flat `dictZh` / `dictEn` with `assertSameKeys` enforcing key parity. Renderer-only; main process stays English.
- **Artifact renderers** — `src/renderer/services/artifact-renderer/` renders AI artifacts inside `sandbox` iframes keyed by type (Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video). AI output **never** enters the main page DOM.

## IM subsystem (summary)

`src/darvin-agent/internal/im/`:

- `qq/` — QQ official bot, app access token + Discord-style WS gateway.
- `wecom/` — Enterprise WeChat (WeCom) AI Bot, WS to `wss://openws.work.weixin.qq.com`, `aibot_subscribe` for auth.
- `weixin/` — Personal WeChat iLink bot, HTTP gateway, QR login + long-poll `getupdates`.
- `manager.go` — unified lifecycle, maps inbound messages to a bound darvin session, routes outbound replies back to the originating peer.
- `Prober` — one-shot `Probe(ctx) ([]Check, error)` implemented per connector; the `imTest` RPC returns a structured check report rather than a fake-ok.

See [`docs/IM.md`](./IM.md) for the full IM subsystem design.

## Build, package, cross-platform

- `scripts/build-go.js` — outputs `<repo>/bin/darvin-agent-<platform>-<arch><.exe?>` with `CGO_ENABLED=0`.
- `npm run build:agent` — compile Go only.
- `npm run package` — unpacked Electron app (auto-runs `build:agent`).
- `npm run make` — installers: `squirrel` (Windows) / `zip` (macOS) / `deb` (Linux) / `rpm` (Linux).
- `forge.config.ts` — `extraResources.filter` keeps **only** the current-platform Go binary, so dev machines with cross-platform binaries in `bin/` don't bloat the installer.

## What's intentionally not in the main process

- SQLite — owned by Go (`globalDB` in `internal/database`). Main only proxies to Go.
- LLM keys — read by Go viper from `LLM_API_KEY` env / user `config.yaml` / repo `config.yaml`. Main never sees the raw key.
- Tool execution — all tool calls run in the Go process so the renderer cannot bypass permissions or sandbox.
- IM sessions — per-channel connectors live in Go, accessed by the renderer through `window.darvin.im*` IPC only.