# 自定义会话工作区 + 附件路径引用 + Agent FS 权限审批

> 编号 **12**。把固定 per-session workspace 改为用户可选的会话工作区；附件改为 LobsterAI 式「路径引用，不复制」；引入 LobsterAI 式权限审批门控。

## 1. 概述

### 1.1 问题 / 背景

当前 darvin 的模型：

- **固定 per-session workspace**：`workspaces/<sessionId>/`（`user-paths.ts:56`），用户无法选择。
- **附件复制进 workspace**：导入 = 复制文件到工作区 + `imported_files` 表，agent 读工作区副本。
- **硬沙箱隔离**：Go agent `fsSandbox.root = DARVIN_AGENT_WORKSPACE`，工具全部 containment 校验（`ErrPathEscapes`），越界直接拒绝。
- **无审批**：工作区内危险操作白名单命令直接执行。

用户诉求（LobsterAI 借鉴）：

1. **工作区 = 真实工作目录**：公司某文件夹 / 代码仓库，用户每会话可自定义并持久保存。
2. **文件生成在工作区**：写文档 / 写代码产物落该目录。
3. **附件读取走 LobsterAI 路径引用**：附件**不复制**，记原始绝对路径，agent 直接读原文件；「附加即授权」——用户附加的文件 agent 可读，无需审批。
4. **给 agent FS 权限但管控**：工作区 + 已附加文件 = 授权根（自由读）；危险操作 / 授权根外的访问 = 弹窗审批。

### 1.2 根因（现状映射）

| 现状 | 与目标差距 |
|---|---|
| `resolveWorkspaceRoot` 固定拼 `workspaces/<sid>` | 无法指向用户真实目录 |
| 附件复制进工作区（importFiles → workspace 拷贝） | 与 LobsterAI 路径引用相悖 |
| `ComposerContextRow` 工作目录 chip 只读 | 没有选择交互 |
| `fsSandbox` 硬隔离，越界直接拒 | 没有「授权根 + 审批放行」路径 |
| 工具 executor 无权限门 | 没有 `permission_request` / 弹窗审批 |

### 1.3 目标

- G1：每个会话可自定义工作区根目录，持久化保存，切换自动跟随。
- G2：工作区 = agent 的生成目录（写文档 / 代码编辑产物落这里）。
- G3：附件路径引用——不复制，agent 读原文件；附加即授权。
- G4：LobsterAI 式权限审批：授权根外访问、危险操作，均弹窗 allow/deny（可改入参 / 可中断）。
- G5：审批结果可记住（按会话 / 按规则持久化）。

### 1.4 非目标

- 不做完整 OS 级沙箱（bubblewrap / firejail）——沿用现有 fsSandbox containment + 审批门控。
- 不做细粒度 RBAC / 角色系统。
- 不做附件内容注入 prompt（图片 base64 除外，暂不实现）——文本附件只带路径，agent 用 read_file 读。
- 不引入跨会话共享工作区。

## 2. 用户场景

### 场景 1：自定义工作区（公司文件夹）
**Given** 用户建会话「月度报表」。
**When** 点击 ComposerContextRow 工作目录 → 选择 `~/公司/财务/2026-08`。
**Then** 该会话工作区变为此目录；写报表 / 生成图表文件都落在这里；切走再切回仍是此目录。

### 场景 2：代码仓库操作
**Given** 用户建会话「修 bug」，选工作区 `~/projects/my-app`（git 仓库）。
**When** 附加 `~/资料/需求.md`（仓库外），并发送「按需求改代码」。
**Then** agent 读 `需求.md`（附加授权，免审批）→ 在仓库内改代码（写/跑 shell）。

### 场景 3：危险操作审批
**Given** agent 在仓库内执行 `rm -rf node_modules`。
**When** 工具 executor 判定 dangerLevel=destructive。
**Then** 弹权限审批窗 → Deny → 不执行；Allow → 执行。

### 场景 4：授权根外访问审批
**Given** 工作区 `~/projects/my-app`，agent 想读 `/etc/hosts`（未附加）。
**When** 工具目标既不在工作区、也不在附加文件集。
**Then** 弹审批窗（标注越界路径）→ Allow 单次放行；Deny 拒绝。

## 3. 功能需求

### FR-1 自定义会话工作区
- 每个会话一个 `workspaceRoot`（绝对路径），用户可改。
- 持久化 `sessionId → workspaceRoot` 映射；未设置时退回 `workspaces/<sid>`。
- 切换会话时 main 按映射决定 `workspaceLoc` 并重启 Go agent（注入 `DARVIN_AGENT_WORKSPACE`）。
- UI：`ComposerContextRow` 工作目录 chip 升级为选择器（native folder dialog + 最近目录）。

### FR-2 工作区生成
- agent 的 fsSandbox.root = 自定义目录；写文档 / 代码 / 生成文件落这里。
- `listWorkspaceFiles` 展示该目录内容；可 reveal / 打开。

### FR-3 附件路径引用（LobsterAI）
- 附件不再复制进工作区：记录**原始绝对路径**（`path` + `name` + `size`），作为单条消息的暂存附件。
- 发送时 prompt 携带附件绝对路径数组。
- **附加即授权**：Go agent 把本次消息的附件路径加入「授权读集」，read_file 读这些路径**免审批**。
- 发送后附件暂存清空（one-shot，保留 Bug4 语义；无复制故无需删除文件）。

### FR-4 权限审批门控
- 新事件 `permission_request`（Go → renderer）：`{ requestId, toolName, toolInput, dangerLevel, reason }`。
- 新 RPC `agent.permission_response`（renderer → main → Go）：`{ requestId, behavior, updatedInput?, message?, interrupt? }`。
- 工具 executor 在「需审批」操作前 emit permission_request，阻塞等待（60s 超时默认 deny）。
- dangerLevel 三级（safe / caution / destructive）+ 正则判定（复用 LobsterAI 模式集）。

### FR-5 授权根 vs 审批
- **授权根（免审批）**：工作区 root（读+写）；本次消息附加的文件路径（读）。
- **需审批**：危险操作（rm -rf / 覆盖已存在文件 / 删除文件 / git force / chmod / kill / sudo）；授权根外的任何读写。

### FR-6 审批 UI + 持久规则
- `PermissionModal.vue`：展示工具名 / 入参 / 危险原因；Allow（可编辑入参 + 记住此会话）/ Deny（可带消息 + 中断）。
- 持久规则：Allow 时「记住此会话」→ 写 config.yaml per-session 规则表，后续同规则自动放行。

## 4. 实现方案

### 4.1 工作区映射持久化

main 侧映射文件 `agentDataDir()/workspace-mapping.json`：

```ts
// main/libs/workspace-map.ts（新）
type WorkspaceMap = Record<string, string>; // sessionId → rootPath
export function readWorkspaceMap(): WorkspaceMap;
export function writeWorkspaceMap(m: WorkspaceMap): void;
export function resolveWorkspaceFor(sessionId: string): WorkspaceLocation;
// 有映射 → { rootPath: mapped, workspaceId: sessionId }；无 → workspaces/<sid>
```

`followActiveWorkspace` 改用 `resolveWorkspaceFor`；新增 IPC：

```ts
// darvin-api.ts
setWorkspaceRoot(): Promise<DarvinSetWorkspaceResult>;              // dialog 选目录
setWorkspaceRootTo(path: string): Promise<DarvinSetWorkspaceResult>; // 指定路径（最近目录/测试）
getWorkspaceRoot(): Promise<{ rootPath: string; label: string }>;
```

main：dialog `properties:['openDirectory','createDirectory']` → 校验目录存在 → 写映射 → `restartGoSubprocess(newRoot)` → `broadcastWorkspaceChanged`。

### 4.2 UI：工作目录选择器 + 附件条

- `ComposerContextRow` 工作目录 chip → `FolderPicker.vue`（native dialog + 最近目录，localStorage `darvin.recent-workspaces` 最多 8 个）。
- 附件条（`ImportedFilesBar` 语义改）：显示暂存附件（原始路径 basename），移除按钮只 detach（不删原文件）。去 workspace-bytes 计（无复制）。
- `PlusMenu` 上传项：native 文件选择 → 记路径到附件暂存（不再 `runImport` 复制）。

### 4.3 附件携带 + 授权读集（Go）

```go
// gateway PromptParams 增字段（从 B1 的 importedFiles 扩展）
type PromptParams struct {
    Content       string   `json:"content"`
    SessionID     string   `json:"sessionId,omitempty"`
    RunID         string   `json:"runId,omitempty"`
    Attachments   []string `json:"attachments,omitempty"` // 绝对路径（原 importedFiles 改名/升级）
}
```

dispatcher Run：`msg.Attachments` → `a.runGrantedReadPaths = msg.Attachments`（授权读集）。

read_file 工具路径解析：

```go
// tool/fs.go（改造 sandbox resolve）
func (sb *fsSandbox) ResolveRead(path string, grantedReads []string) (string, error) {
    if rel, err := sb.ResolveWithinRoot(path); err == nil { return rel, nil }  // 工作区内
    for _, g := range grantedReads {
        if sameFile(path, g) { return path, nil }   // 附加即授权
    }
    return "", ErrNeedsPermission{Path: path}        // 越授权根 → 审批
}
```

### 4.4 权限事件 + RPC（Go）

```go
// event.go 新增
type PermissionRequestEvent struct {
    EventBase
    RequestID   string
    ToolName    string
    ToolInput   map[string]any
    DangerLevel string // safe | caution | destructive
    Reason      string
}
func (PermissionRequestEvent) EventName() string { return "permission_request" }
```

```go
// gateway handlers.go 新增 RPC
case "agent.permission_response": return handlePermissionResponse(...)
type PermissionResponseParams struct {
    RequestID    string         `json:"requestId"`
    Behavior     string         `json:"behavior"` // allow | deny
    UpdatedInput map[string]any `json:"updatedInput,omitempty"`
    Message      string         `json:"message,omitempty"`
}
```

agent 侧 pending-permission 表：

```go
type pendingPermission struct { ch chan permission.Result; timeout *time.Timer }
func (a *Agent) RequestPermission(req PermissionRequest) (PermissionResult, error) {
    id := a.idGen()
    ch := make(chan PermissionResult, 1)
    a.pendingPerms[id] = &pendingPermission{ch, time.AfterFunc(60*time.Second, func(){ ch <- deny("timeout") })}
    a.Emit(event.PermissionRequestEvent{...})
    select { case r := <-ch: return r, nil; case <-a.ctx.Done(): return deny("cancelled"), ctx.Err() }
}
```

`Deps` 加 `RequestPermission(req) (PermissionResult, error)`；executor 工具调用处接入。

### 4.5 危险判定 + 越授权根判定

`internal/agent/tool/permission.go`（新）：

```go
func ClassifyPermission(toolName string, args map[string]any) (level, reason string, need bool)
func ClassifyPathEscape(toolName string, args map[string]any, root string, granted []string) (reason string, escaped bool)
```

规则（复用 LobsterAI 模式集）：
- **destructive**：`rm -rf/--recursive`、git push --force、git reset --hard、dd、mkfs、覆盖已存在文件、删除文件。
- **caution**：rm、git push、chmod/chown、kill/pkill、git clean、sudo、mv/cp 覆盖。
- **safe**：其余白名单命令。
- **越授权根**：任何工具路径参数解析 → `ErrNeedsPermission` → 审批（dangerLevel=caution，reason 标注路径）。

### 4.6 executor 接入

`executor.go` `runToolsParallel` / `executeOneTool`：

```go
if reason, escaped := permission.ClassifyPathEscape(c.Name, c.Arguments, d.WorkspaceRoot(), d.GrantedReadPaths()); escaped {
    res, err := d.RequestPermission(PermissionRequest{ToolName: c.Name, ToolInput: c.Arguments, DangerLevel: "caution", Reason: reason})
    if err != nil || res.Behavior == "deny" { return tool.Result{IsError: true, Content: "用户拒绝访问：" + res.Message} }
}
level, reason, need := permission.ClassifyPermission(c.Name, c.Arguments)
if need {
    res, err := d.RequestPermission(...)
    if err != nil || res.Behavior == "deny" { return tool.Result{IsError: true, Content: "用户拒绝：" + res.Message} }
    if res.UpdatedInput != nil { c.Arguments = res.UpdatedInput }
}
```

### 4.7 renderer 权限弹窗

- `PermissionModal.vue`：监听 `window.darvin.onEvent` 的 `permission_request`。
- Allow：可编辑入参 JSON + 「记住此会话」→ `window.darvin.respondPermission({ requestId, behavior:'allow', updatedInput })`。
- Deny：可填消息 + 「中断运行」→ `respondPermission({ requestId, behavior:'deny', message, interrupt })`。
- 记住规则：写 Go `agent.set_permission_rules`（per-session 规则表），executor 查询命中自动放行。

### 4.8 共享类型

```ts
// darvin-api.ts
export type DarvinPermissionBehavior = 'allow' | 'deny';
export interface DarvinPermissionRequest { requestId: string; toolName: string; toolInput: unknown; dangerLevel: 'safe'|'caution'|'destructive'; reason: string; }
export interface DarvinPermissionResponse { requestId: string; behavior: DarvinPermissionBehavior; updatedInput?: unknown; message?: string; interrupt?: boolean; }
export interface DarvinAttachmentRef { path: string; name: string; size: number; }
// DarvinPromptRequest.attachments?: DarvinAttachmentRef[]（替换 importedFiles）
// DarvinEvent 增 permission_request 成员
// DarvinApi 增 respondPermission(r): Promise<void>；setWorkspaceRoot* / getWorkspaceRoot
```

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 审批 60s 超时 | 默认 deny + reason「审批超时」 |
| 工作区目录被删/不可访问 | main 启动校验，不存在退回 `workspaces/<sid>` 并提示 |
| 并行工具多个申请 | 独立 requestId，弹窗排队（一次一个） |
| 审批中断 run | executor 返回 IsError，agent 继续或停止 |
| 附加文件本身被删/移动 | read_file 报错，agent 感知；附件条保留路径但标注失效 |
| 用户把工作区设为 home | 允许但提示「授权根变大，危险操作仍需审批」 |
| 最近目录失效 | 列表点击前校验存在性，失效项灰显 |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/main/libs/user-paths.ts` | `resolveWorkspaceRoot` 支持映射 |
| `src/main/libs/workspace-map.ts`（新） | 映射读写 + `resolveWorkspaceFor` |
| `src/main/index.ts` | workspace IPC + `followActiveWorkspace` 用映射；附件不复制（`import_files` 语义改） |
| `src/main/libs/importFiles.ts` | 改为路径引用（不再复制/写表）或废弃，由附件条替代 |
| `src/shared/darvin-api.ts` | permission 类型 + `respondPermission` + `attachments` + workspace IPC |
| `src/preload/index.ts` | 新 IPC 转发 |
| `src/renderer/components/chat/ComposerContextRow.vue` | 工作目录 chip → 选择器 |
| `src/renderer/components/chat/FolderPicker.vue`（新） | 目录选择浮层 |
| `src/renderer/components/chat/PermissionModal.vue`（新） | 权限审批弹窗 |
| `src/renderer/composables/usePermissions.ts`（新） | 监听 permission_request + 响应 |
| `src/renderer/composables/useImportedFiles.ts` | 语义改附件暂存（path ref，无复制） |
| `src/renderer/components/chat/ImportedFilesBar.vue` | 附件条（路径 + 移除，无容量计） |
| `src/renderer/composables/useChatActions.ts` / `ChatPane.vue` | 发送带 `attachments` |
| `src/renderer/services/i18n.ts` | 新增 key |
| `src/darvin-agent/internal/agent/event/event.go` | `PermissionRequestEvent` |
| `src/darvin-agent/internal/agent/agent.go` | `RequestPermission` / `ResolvePermission` / granted reads |
| `src/darvin-agent/internal/agent/executor/executor.go` | 工具执行前权限门 |
| `src/darvin-agent/internal/agent/tool/permission.go`（新） | 危险判定 + 越授权根 |
| `src/darvin-agent/internal/agent/tool/fs.go`（改 sandbox） | `ResolveRead` 授权根放行 |
| `src/darvin-agent/internal/gateway/handlers.go` | `agent.permission_response` RPC + eventledger case |

## 7. 验收标准

- [x] 场景 1：会话 A 选自定义工作区，切走再切回仍是该目录（映射持久化 + FolderPicker 已实测弹层；native 目录对话框无法自动化，映射读写路径代码级验证）
- [x] 场景 2：附加仓库外文件，agent 免审批读取（gateway 实测：attachments 携带后 read_file 直接命中，无 permission_request）
- [x] 场景 3：危险操作弹审批窗，Deny 拒绝 / Allow 执行（renderer 端到端实测：rm -rf 弹「危险」窗，Allow 后执行、Deny 携带消息回传 agent）
- [x] 场景 4：授权根外未附加访问弹审批窗，Allow 单次放行 / Deny 拒绝（gateway 实测：读 /etc/hosts → permission_request(caution) → allow 后读到内容）
- [x] 附件不复制进工作区（renderer 附件条只记路径；import_files 不再被调用）
- [x] 审批超时默认 deny（60s timer 代码级）；记住规则后同操作自动放行（renderer 实测：勾选记住 → 二次同命令不再弹窗）
- [x] 通过 lint + `npm run test`（147 例）+ Go `go build` / `go vet` / 全部单测
- [x] live 验证：4 场景逐一实测，console 0 error（playwright/CDP 驱动重启后的应用）

### 落地补充（实现期决议）

- **放行后工具真能跑**：路径越界审批通过后，仅把「该路径」加入沙箱一次性授权集（`fsSandbox.approvePath`，随 run 生命周期清除），而非放开整个授权根——`ResolveRead` / `Resolve` 对已授权路径放行。attach 的授权读集（`grantedReads`）与弹窗单次放行（`approvedPaths`）分离：附件仅可读，弹窗放行可读可写。
- **`EvaluatePermission` 合并判定**：pathEscape（越授权根）与 `ClassifyPermission`（危险命令）在 `tool.permission.go` 统一为 `Registry.EvaluatePermission` → 返回 `PermissionEval{Authorized, Need, Level, Reason, EscapedPath}`。executor 门在 `executeOneTool`，先查 `HasPermissionRule`（记住规则）再 `RequestPermission`。
- **write_file / edit_file 不弹覆盖审批**：工作区是 agent 的生成目录，写/改已存在文件是常态（改代码场景）；「覆盖已存在文件」的 caution 落在 shell 的 `cp`/`mv`/`rm` 模式里，不对 write/edit 逐次弹窗，避免改代码被审批打断。
- **shell 白名单扩充**：工作区可以是真实代码仓库，故默认白名单加入 `git` / `node` / `npm` / `npx` / `pnpm` / `yarn` / `python` / `python3` / `pip` / `pip3` / `go` / `make` / `tar` / `unzip` / `touch`；危险子命令由权限门拦截。
- **「记住此会话」规则为 Agent 内存态**（`(toolName, level, reason)` 三元组，命中自动放行），未落 config.yaml——重启 Go 子进程后清空。全量持久化留待后续。
- **附件语义替换 importedFiles**：`DarvinPromptRequest.attachments`（绝对路径）取代原 `importedFiles`（相对路径）；Go 链 `queue.Message.Attachments` / `acp.PromptRequest.Attachments` / `PromptParams.Attachments` 线程化。原 `import_files` / `list_imported_files` / `remove_imported_file` / ImportedFileStore 保留但 renderer 不再调用（无复制）。
- **删除会话不清自定义工作区**：`delete_session` 只删默认 `workspaces/<sid>` 目录 + 清映射；用户自选的真实目录绝不 rm。
- **reload 渲染沿用 spec B1**：授权/审批是运行时行为，不落库；工具结果截断（64KB）只影响 reload 展示。

## 8. 参考

### darvin-cowork
- `src/main/libs/user-paths.ts` / `src/main/runtime/manager.ts` / `cmd/app/main.go` — workspace root 链路
- `src/darvin-agent/internal/agent/tool/sandbox.go` / `shell.go` — fsSandbox / 白名单
- `src/renderer/components/chat/ComposerContextRow.vue` / `useImportedFiles.ts`
- `specs/bugfixes/tool-render-persist-import/...` — 现附件复制模型（本 spec 取代其附件部分）

### LobsterAI（借鉴）
> 参考项目根目录：`~/桌面/github-project/LobsterAI`

- `src/renderer/components/cowork/FolderSelectorPopover.tsx` — 工作目录选择
- `src/renderer/components/cowork/CoworkPermissionModal.tsx` — 审批弹窗（dangerLevel + allow/deny + 改入参）
- `src/renderer/types/cowork.ts:268-295` — `CoworkPermissionRequest` / `CoworkPermissionResult`
- `src/renderer/services/coworkPromptPayload.ts` — 附件路径引用 + `file:` 注入
- `src/renderer/services/cowork.ts:1427` — `respondToPermission`
