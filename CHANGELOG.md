# Changelog

All notable changes to darvin-cowork are documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Real one-shot connectivity probe for IM channels.** Each connector (QQ / WeCom / WeChat) implements a `Prober` interface that performs an actual round-trip — QQ exchanges an app access token, WeCom dials and authenticates against the AI Bot gateway, WeChat runs a short `getupdates` with `timeout: 3` — and returns a structured `[]Check` report. `imTest` no longer returns a fake-ok.
- **IM management UI:** test-report modal with verdict (pass / warn / fail) and per-check rows; manual "enable on pass" button (no silent auto-enable); `lastError` red banner on instance cards; secret show / hide + clear button; inline rename (Enter / blur saves, empty falls back to channel); delete confirmation modal naming the instance; unsaved-changes badge with a discard toast on tab switch / cancel.

### Changed
- `internal/im/handlers.go` converges all `imTest` failure paths into the single `TestResult` shape (`Checks` carries every judgement); unknown channel and bad config fold into fail checks instead of JSON-RPC errors.
- QQ `Probe` uses a fresh connector with no cache, so the cached-token shortcut never fires and the test always hits the real token endpoint.

## [1.0.0] — 2026-08-18

First tagged open-source release.

### Added
- **Three-process architecture**: Vue 3 renderer, Electron shell, Go `darvin-agent` runtime over WebSocket JSON-RPC 2.0. The renderer never touches the database.
- **IM-channel subsystem** with three connectors:
  - QQ official bot (Discord-style WS gateway + app access token + REST sends).
  - WeCom (enterprise WeChat) AI Bot WS channel with `aibot_subscribe` auth and `aibot_msg_callback` for inbound.
  - Personal WeChat iLink HTTP channel with QR login + long-poll `getupdates`.
- **Scheduled tasks**: cron-style engine that runs brand-new sessions in the background and posts results back as `ScheduleFired` notifications.
- **Workspace-first sessions**: per-workspace file isolation; sessions stay scoped to the workspace they were created in.
- **Agent persistence**: per-agent config + identity persisted in Go SQLite; editing UI in the renderer.
- **Multi-provider LLM support**: Anthropic / OpenAI / Gemini providers, streaming protocol, model registry.
- **Inline thinking-block normalization** to `thinking_delta` events.
- **MCP end-to-end**: `http` / `sse` / `stdio` transports, fingerprint resolution, per-server enable, runtime security hardening, server detail modal in the renderer.
- **Sub-agent delegation**: 5 tools + artifact-panel UI; concurrent sub-agents share the parent's session-wide concurrency budget.
- **Todo tooling**: `todo_write` (two-level checklist) + `complete_step` (evidence-backed sign-off) + host-tracked active-task list injection into the agent context.
- **Renderer features**: home chat with running glow, TodoPanel artifact tab, sandboxed artifact renderers (Code / Document / Html / Image / LocalService / Markdown / Mermaid / Svg / Text / Video), light / dark theme + 3 accent colors, zh / en i18n (~800 keys).
- **Tool set**: `read_file` / `write_file` / `edit_file` / `multi_edit` / `move_file` / `list_dir` / `glob` / `grep` / `notebook_edit`, sandboxed `shell` with command whitelist, `web_fetch`, `search` / `code_index` / `delete_symbol`.
- **Build & package**: Electron Forge makers for `squirrel` (Windows) / `zip` (macOS) / `deb` (Linux) / `rpm` (Linux); `CGO_ENABLED=0` Go binary packed per-platform via `extraResources.filter`.
- **Quality gates**: ESLint (`@typescript-eslint` + `import` + Vue plugin), Vitest unit tests, Go `fmt` / `vet` / `staticcheck` / `golangci-lint`, headless smoke (`scripts/smoke.sh`), `electron-cdp` skill for E2E driving of the running window.

### Fixed
- QQ sends now carry a fresh `msg_seq` for idempotency / ordering.
- WeChat replies route to the correct peer WeChat window.
- IM channel messages surface reliably in the chat UI.

## [0.x] — internal prototype

Pre-open-source work. Versions and dates were not formally tracked; the codebase reorganized around the v1.0.0 feature set. Notable prior work included the merge-databases refactor (Go owns SQLite; renderer is fully cache-driven), the spec-driven design docs under `specs/`, and the initial Vue 3 + Tailwind v4 + Vite renderer shell.