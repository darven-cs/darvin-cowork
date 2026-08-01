# Tool Runtime Hardening 设计文档

> Tier 1：把 Go agent 工具层的「基础安全与正确性」拉到 OpenClaw 现有水平，并把「主进程 + 渲染进程」的文件导入与工作区绑定一并落地。覆盖 realpath 沙箱、路径排除清单、maxBytes 强制 + TOCTOU 防护、参数 schema 补全、用户文件导入 + 工作区强制。
>
> 本版本是 v2，相对初稿做了以下结构修正（详见 §8 review 修订记录）：
>
> 1. `imported_files` 落到 Go 端 GORM 模型（`src/main/store/SessionStore.ts` 不存在；session/message 数据所有权已归 Go）
> 2. `injectSystemNote` 走新增 JSON-RPC `agent.save_message`，main 端不直接写库
> 3. `openRootFileLimited` 补 `offset` 参数，保留 `read_file` 跳读语义
> 4. §7.3 smoke 全部按 `<workspaceRoot>` 重写
> 5. 删 `Pattern: "^[a-z0-9_-]+$"`（与 `Enum` 冗余）
> 6. workspace 路径 helper 并入现有 `src/main/libs/user-paths.ts`
> 7. `darvin:delete_session` 同步 `fs.rm(workspace root)`
> 8. `SumBytes` + `Insert` 同事务防 workspace TOCTOU

## 1. 概述

### 1.1 问题 / 背景

`src/darvin-agent/internal/agent/tool/` 是 LLM 与本地文件系统 / shell 的唯一接口面。当前实现存在 5 类基础缺陷，与 OpenClaw 等成熟工具层相比明显落后：

1. **`fsSandbox.Resolve` 用字符串 `filepath.Rel` 判包容**，可被 symlink 逃逸。
   - `internal/agent/tool/sandbox.go:47-53`：`rel := filepath.Rel(s.root, abs)` 之后再判 `rel == ".."` 前缀——这只防「词法上逃逸」，不防「真实路径逃逸」。
   - macOS 上 `/var` → `/private/var` 的隐式 symlink、`/tmp` → `/private/tmp`，以及用户在工作目录里放的 `link → /etc/passwd` 这种显式 symlink，都能绕过当前沙箱。
   - `sandbox_test.go:23-34` 的 `TestSandboxRejectEscape` 只覆盖词法逃逸（`../etc/passwd`、`/etc/hosts`），是「看似绿但实际有洞」的假绿。
2. **没有路径排除清单**，LLM 误传 `.git/` / `node_modules/` / `.venv/` / `__pycache__/` 时会被静默读取或写入，污染仓库、拖慢遍历、暴露 `.env` 类敏感文件。
3. **`readFileTool` 的 `maxReadBytes` 强制靠循环累加截断**，且 `os.Open` → `sb.Resolve` → `f.Seek` / `f.Read` 三步不在同一个原子点：
   - `fs.go:45-49` `sb.Resolve` 拿到 abs 后，到 `:49 os.Open(abs)` 之间，存在 TOCTOU 窗口：路径被换成指向 `/etc/passwd` 的 symlink 后 `Open` 仍能读。
   - shell 工具（`shell.go:94` `exec.CommandContext`）对 stdout / stderr **没有 maxBytes 上限**，恶意/误用 `find /` 会把内存打爆。
   - **`offset` 参数在 v2 重构时必须保留**（初稿的 `openRootFileLimited` 签名漏掉了 `offset`，会让跳读退化为从头读）。
4. **参数 schema 校验只支持 type / required**，不支持 `enum` / `format` / `additionalProperties: false`：
   - `params.go:17-42` 注释明确写「intentionally tiny」。
   - LLM 经常瞎传 `command: "rm -rf /"`（含 shell 元字符）、`mode: "DELETED_PERMANENTLY"`（应当走 enum），当前只能靠 shell allowlist 兜底。
5. **没有「用户文件导入 → 工作区绑定」链路**：主进程 / 渲染层缺文件导入 UI，Go agent 的工作区根 (`fsSandbox.root`) 没有受控来源，agent 唯一能碰的「文件来源」只能是 prompt 里粘的 inline content 或用户手动 ls 到的工作目录——没有「用户从任意位置挑文件 → 强制拷进受控 workspace → agent 在 workspace 内读写」的端到端流程。
   - 当前 session / message 数据所有权归 Go（`src/main/index.ts:6-7` 注释；`src/darvin-agent/internal/agent/store/` 持有 GORM 模型），main 端无 SQLite、所有读写经 JSON-RPC。本 spec 的 `imported_files` 表按同模式落地在 Go 端，不开 main 端 DB。
   - 用户视角下要「给 agent 看一份本地 markdown 让它改」目前只能让用户自己把文件 `cp` 到工作目录，没有 UI 入口；而且一旦 Go agent 的 sandbox root 配置错了，agent 就能直接读 `~/.ssh/id_rsa` 这种敏感文件，无 UI 兜底。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | symlink 攻击面封闭 | `TestSandboxSymlinkEscape`（macOS CI 跑）+ Linux `TestSandboxSymlinkEscape` 全绿 |
| G2 | 默认路径排除清单覆盖常见噪音/敏感目录 | `.git` / `node_modules` / `.venv` / `__pycache__` / `.env` / `.env.*` / `target` / `dist` / `build` 默认 deny |
| G3 | 受信任根内 read 走 single-step API + maxBytes + offset 语义 | `openRootFile(p, label)` / `openRootFileLimited(p, label, offset, maxBytes)`；shell stdout / stderr 强制 maxBytes（默认 1 MiB） |
| G4 | 参数 schema 支持 enum / format / additionalProperties | `validateArgs` 升级；至少 5 个 built-in tool 改用新约束 |
| G5 | 主进程 / 渲染层有「文件导入 + 工作区强制」闭环；agent 的所有 fs 操作仅落在受控 workspace | Renderer 入口（paperclip → 系统对话框）→ Main 拷进 `{userData}/workspaces/{sid}/` → 写入 Go 端 `imported_files` 表 → Go agent `fsSandbox.root` 同一路径 → FR-1/FR-2 沙箱兜底；原始文件路径**不**留底，agent 永远见不到 |

### 1.3 非目标

- **不做 OS-level syscall 过滤**（seccomp / sandbox-exec / AppContainer）：留作 Tier 2+ 议题，本层只到应用层 hardlink / symlink 兜底。
- **不做 macOS / Windows 平台差异化**：所有路径处理走 `os.Lstat` + `filepath.EvalSymlinks`，跨平台行为一致；Win 上 `\\?\` 前缀不处理（工作目录在用户工程里，没外部 UNC 路径需求）。
- **不做工具 plugin 化**：见 `tool-architecture-rework/`（Tier 2）专项。
- **不替换 `validateArgs` 为 `gojsonschema` 完整实现**：先把 enum / format / additionalProperties 加进现有轻量校验器；后续若需要 `$ref` / `anyOf` 再评估切换。
- **不做 binary / PDF / 图片读取**：FR-5 v0 仅支持文本文件（`TEXT_FILE_EXTS` whitelist）；PDF / 图片 / 视频等走 vision / PDF parser 留作 v1。
- **不做 workspace 跨 session 共享 / 跨 device 同步**：v0 single-workspace-per-session；workspace reconcile + sync 已在 `tool-architecture-rework/`（Tier 2）FR-8 落 spec。
- **不做 workspace 内容 GC**：500 MiB 上限触发后只拒新 import，不主动 LRU 清理旧文件（LLM 主动 `remove_imported_file` 是清理通道）。session 删除时清掉对应 workspace 目录（v2 增项，避免孤儿文件累积）。

## 2. 用户场景

### 场景 1: macOS 工作目录里藏 symlink 攻击

**Given** 用户把 darvin-cowork 工作目录设为 `~/proj`，目录内放 `secrets → /etc/passwd`（攻击者诱导 / 用户笔误）。
**When** LLM 调 `read_file { path: "secrets" }`。
**Then** 当前实现直接打开 `/etc/passwd` 并返回内容（漏洞）；spec 落地后沙箱识别 symlink 目标越界，返回 `Result{IsError: true, Content: "path escapes sandbox via symlink: ..."}`。

### 场景 2: LLM 想扫 `.git/` 历史

**Given** 用户工程是个 git 仓库，`.git/objects/` 内有大量二进制 pack 文件。
**When** LLM 调 `list_dir { path: ".git", max_depth: 5 }`。
**Then** 当前实现遍历整个 `.git/`（数千个 entries + 巨大 pack），返回给 LLM 上 MB 数据；spec 落地后默认 deny `.git/`，工具返回 `Result{IsError: true, Content: "path excluded by workspace filter: .git"}`，LLM 看到错误重试。

### 场景 3: 大文件读取时间窗口里被换 symlink

**Given** `~/proj/data.txt` 当前是普通文件，`sb.Resolve` 已通过。
**When** 在 `Open` 之前那一瞬间，攻击者把 `data.txt` 换成 `→ /etc/shadow` 的 symlink。
**Then** 当前实现直接打开 symlink，读到 shadow；spec 落地后 `openRootFile` 一步完成 `EvalSymlinks(abs)` → `Open`，Open 拿到的 fd 已经是真实路径（不会再被换），并与已 `EvalSymlinks` 的 sandbox root 重新做 containment check，不通过就拒。

### 场景 4: LLM 调 shell `command: "rm"`

**Given** `defaultShellAllowlist` 含 `rm`，LLM 提交 `command: "rm -rf /"`。
**When** `validateArgs` 走完当前校验。
**Then** 当前 `params.go:checkType` 只查 type == string，无法拒；spec 落地后 `command` 字段声明 `enum: ["ls", "cat", ...]`，`"rm -rf /"` 不在 enum 里，`validateArgs` 立刻返回 `unknown enum value: rm -rf /`。
（v2 注：`Pattern: "^[a-z0-9_-]+$"` 与 `Enum` 冗余且会卡未来加 `2to3` / `g++` 这种命令，**已删**。）

### 场景 5: shell 输出淹没内存

**Given** LLM 调 `shell { command: "find", args: ["/"], max_output_bytes: 65536 }`，命中大量文件。
**When** 进程跑完。
**Then** 当前 `shell.go:96-99` 用 `bytes.Buffer` 无上限累加 stdout / stderr，OOM 风险；spec 落地后 `cmd.Stdout` / `cmd.Stderr` 走限流 `LimitWriter`，超过 `max_output_bytes` 后丢弃 + 在末尾追加 `[stdout truncated at N bytes]`。

### 场景 6: 用户从任意位置挑文件让 agent 改

**Given** 用户在 darvin-cowork 主界面看一份本地 `~/Documents/spec.md` 想让 agent 改。
**When** 用户点 composer 上的 paperclip → 系统 open dialog 选中 `~/Documents/spec.md`。
**Then** 当前实现没有入口；spec 落地后：
1. main 弹 `dialog.showOpenDialog`（带 text-file filter）；
2. 用户选完后 main 把 `spec.md` 拷到 `{userData}/workspaces/{activeSessionId}/spec.md`，sha256 入库 Go 端 `imported_files` 表；
3. composer 上方出现 chip：`spec.md  (4.2 KiB) [×]`；
4. main 经 JSON-RPC `agent.save_message` 注入一条 system note（`role: 'system'`，`meta.tag: 'workspace_event'`）：`[系统] 用户导入了文件：spec.md (4.2 KiB, sha256: ...). 你可以用 read_file 配合相对路径 "spec.md" 访问。`；
5. LLM 调 `read_file { path: "spec.md" }` → 走 workspace sandbox，**永远看不到**原始 `~/Documents/spec.md` 路径。

### 场景 7: LLM 想越权读 `/etc/passwd`

**Given** 用户已导入文件，LLM 试图绕过。
**When** LLM 调 `read_file { path: "/etc/passwd" }` 或 `read_file { path: "../../../etc/passwd" }`。
**Then** Go agent 端 `fsSandbox.Resolve`（FR-1）立刻返 `ErrPathEscapes` 或 `ErrPathEscapesViaSymlink`；main 端**不**接受任何外部路径写入——LLM 唯一能看到的「路径空间」就是 workspace 根（连 import 进来后的实际路径也以相对 workspace 形式表达）。

### 场景 8: 用户导入超出 workspace 容量上限的文件

**Given** 用户选了一个 200 MiB 的 `big.log`。
**When** 走 import 流程。
**Then** main 端 `validateImportSize` 拒（`maxImportBytes = 50 MiB`），返 `{ error: "file_too_large", size: 209715200, max: 52428800 }`；composer 弹 toast，文件**不**写入 workspace，agent 看不到。

### 场景 9: 用户删 session，workspace 目录不残留

**Given** 用户已用某 session 导入 100 MiB 文件。
**When** 用户删该 session（`darvin:delete_session`）。
**Then** v1 行为：`<userData>/workspaces/<sid>/` 残留，500 MiB 上限逐步被吃满；spec v2 落地后 main 端 `delete_session` handler 同步 `fs.rm(root, { recursive: true })`，释放 100 MiB。

### 场景 10: 两个 renderer tab 同时 import，绕过 workspace 上限

**Given** workspace 已用 480 MiB；用户 A 在 tab1、用户 B 在 tab2 同时点 ImportButton。
**When** 两个 `darvin:import_files` 调用并发到达 main。
**Then** v1 行为：两个 handler 都读 `getWorkspaceBytes` 得 480 MiB，都判 ≤500 MiB，写完后 530 MiB 超限；spec v2 落地后 Go 端 `ImportedFileStore.Insert` 与 `SumBytes` 在同一 SQLite 事务内，第二个 Insert 失败返 `workspace_full`，UI 弹 toast。

## 3. 功能需求

### FR-1: realpath 沙箱

**新增 API（`sandbox.go`）**：

```go
// Resolve 重写为：先 EvalSymlinks（路径若不存在则降级为 Clean + 父目录 EvalSymlinks），
// 再与 root 的 EvalSymlinks 结果做 filepath.Rel containment check。
// 路径分量命中排除清单时返 ErrPathExcluded，优先于 containment check。
func (s *fsSandbox) Resolve(p string) (string, error)
```

- `newFsSandbox` 在构造时即 `realpath(s.root)` 存为 `realRoot`；后续所有 Resolve 比较都用 `realRoot`。
- 路径分量逐段 `os.Lstat`，任何一段是 symlink 时取 `EvalSymlinks` 后与 `realRoot` 做 `Rel` 检查；超界返 `ErrPathEscapesViaSymlink`。
- 路径不存在场景：把「不存在的最终叶子」的父目录全部 `EvalSymlinks`，父目录必须在 `realRoot` 内。
- 错误信息格式：`"path %q escapes sandbox via symlink (resolves to %q outside root %q)"`，便于上层和日志排查。

**新增 sentinel**：

```go
var (
    ErrPathEscapes            = errors.New("sandbox: path escapes sandbox root")
    ErrPathEscapesViaSymlink  = errors.New("sandbox: path escapes sandbox root via symlink")
    ErrPathExcluded           = errors.New("sandbox: path excluded by workspace filter")
    ErrReadTooLarge           = errors.New("sandbox: read exceeds hard limit")
)
```

### FR-2: 路径排除清单

**配置层**（新增 `sandbox.go` 内常量 + 可覆盖）：

```go
// DefaultPathExclusions 返回保守的内置相对路径分量列表；
// 任一 fs 工具命中即拒，不论是否在 sandbox root 内。
func DefaultPathExclusions() []string
```

默认清单（按目录名匹配，相对 workdir 的任何一段路径分量命中即拒；匹配走 component-level 分段比对，case-insensitive）：

| 名称 | 理由 |
|------|------|
| `.git` | git 内部对象，不应让 LLM 扫到 |
| `node_modules` | npm 依赖，通常几 GB |
| `.venv` / `venv` / `__pycache__` | Python 缓存与虚拟环境 |
| `.env` / `.env.*` | 用户级凭据，**默认 deny** |
| `target` / `dist` / `build` / `.next` / `.turbo` | 各语言构建产物 |
| `.DS_Store` / `Thumbs.db` | OS 噪音 |

匹配规则（`sandbox.go` 内 `isExcluded(abs string) (string, bool)`）：

1. 对 abs 路径在 sandbox root 内的所有路径分量（toLower 后）做包含检查——**component-level**，不是子串匹配。`proj/sub/.git/foo` 命中 `.git` 即拒。
2. glob 形式仅支持 `**`（匹配任意层级分隔）与字面匹配；不引入 `doublestar` 等三方库。
3. 匹配命中 → 返 `ErrPathExcluded`，错误信息含具体命中的 pattern。

**API 调整**：

```go
func newFsSandbox(workdir string, exclusions []string) (*fsSandbox, error)
func (s *fsSandbox) Resolve(p string) (string, error) // 内部先 isExcluded 再 Resolve
```

`NewBuiltins(workdir, allowlist)` 签名不变；exclusions 走默认 `DefaultPathExclusions()`。`config.yaml` 自定义本期不开（开了会让用户写出 `.*` 把保护废掉，引入负向价值）。

### FR-3: 受信任根内 read + maxBytes 强制 + offset 保留 + TOCTOU 防护

**抽 helper（新增 `sandbox.go`）**：

```go
// openRootFile 打开 root 内的文件，做 single-step realpath + containment check。
// 返回 *os.File 真实已打开 fd（即使外部换 symlink 也指原 inode），调用方 Close()。
func (s *fsSandbox) openRootFile(p string, label string) (*os.File, string, error)

// openRootFileLimited 在 openRootFile 基础上，强制 offset + maxBytes：
//   - offset > 0 时 Seek(offset) 再 LimitReader
//   - maxBytes 是「从 offset 起最多读的字节数」（不是文件总大小）
//   - truncation 时末尾追加 `[truncated at offset N, limit M bytes]`
// 返回：fd、真实已读字节数、是否触发 limit、error。
func (s *fsSandbox) openRootFileLimited(p string, label string, offset, maxBytes int64) (*os.File, int64, bool, error)
```

**关键不变量**：

1. `openRootFile` 内必须用 `filepath.EvalSymlinks(abs)` 拿到 `realAbs`，与 `s.realRoot` 做 `filepath.Rel` 重新 containment check，**之后**才 `os.Open(realAbs)`。Open 拿到 fd 后，即使外部把文件换成 symlink，fd 仍指向原 inode。
2. `openRootFileLimited` 的 maxBytes 默认 `maxReadBytes = 1 << 20`（沿用 `fs.go:17`），可被调用方覆盖；上限 `maxHardReadBytes = 16 << 20`（16 MiB），超过返 `ErrReadTooLarge`。
3. `offset` 必须生效（v2 修订：原初稿漏掉 `offset`，会让 `read_file { offset: 500 }` 退化为从头读——保留调用方原始语义是这次重构的 hard requirement）。
4. `os.Open` 不引入 `O_NOFOLLOW` / `O_SYMLINK`（跨平台不一致），靠「先 EvalSymlinks 后 Open」逻辑等价保证。

**`readFileTool` 改造**（`fs.go:40-99`）：

- `os.Open` + `Seek` + 循环 Read 整段重写为：调 `openRootFileLimited(path, "read_file", offset, limit)`，offset 与 limit 全部由 helper 内部处理。
- 移除 `min` helper（Go 1.21+ builtin 即可，原代码自带重复实现）。
- 把 helper 拿到的 bytes 转 string（按 UTF-8 截断到最近的合法 rune 边界，避免半个汉字）。
- truncated 时 content 末尾追加 `\n[truncated at offset N, limit M bytes]`。

**shell 工具 stdout / stderr 限流**（`shell.go:94-107`）：

- 新增常量 `maxShellBytes int64 = 1 << 20`（1 MiB，默认）、`maxHardShellBytes int64 = 16 << 20`（16 MiB，硬上限）。
- 替换 `bytes.Buffer` 为 `&limitWriter{cap: maxShellBytes}`（新增 `internal/agent/tool/limitwriter.go`，超过 cap 后丢弃 + 计数）。
- shell 工具的 Parameters 加可选 `max_output_bytes: { Type: "integer", Minimum: 0, Maximum: 16777216 }`，覆盖默认。
- 输出末尾在 truncation 时按 stream 区分追加 `[stdout truncated at N bytes]` / `[stderr truncated at N bytes]`。

### FR-4: 参数 schema 校验补全

**`llm.ParameterSchema` 扩展**（`internal/agent/llm/types.go`）：

```go
type ParameterProperty struct {
    Type        string
    Description string
    Enum        []string            // 新增
    Format      string              // 新增：uri / path / email / date-time 等 hint，不强校验
    Minimum     *float64            // 新增：仅对 number/integer 生效
    Maximum     *float64            // 新增：仅对 number/integer 生效
    MinLength   *int                // 新增：仅对 string 生效
    MaxLength   *int                // 新增：仅对 string 生效
    Pattern     string              // 新增：仅对 string 生效，构造时 MustCompile 一次缓存
    Items       *ParameterProperty  // 新增：仅对 array 生效，递归校验元素
}

type ParameterSchema struct {
    Type                 string
    Properties           map[string]ParameterProperty
    Required             []string
    AdditionalProperties *bool                       // 新增：false 严格模式 → 拒未声明字段
    // 暂不支持: $ref, anyOf, allOf, oneOf, definitions
}
```

**`validateArgs` 升级**（`params.go`）：

- 在每个 property 上额外校验：enum（值不在列表 → 拒）、format（仅做格式 hint 字符串透传，不做强校验；保留供 future 与外部 schema 转换用）、numeric range、string length / pattern、array items 递归。
- **行为不变约束**（v2 修订）：现有 `params.go:37-40` 对「未声明字段」是硬拒（`return fmt.Errorf(...)`），v0 保持这个语义。新增 `AdditionalProperties: ptrBool(false)` 在严格模式下行为相同；未声明 `AdditionalProperties` 的旧 schema 也保持硬拒（**不是** warn-but-not-reject）。
- 兼容旧工具：所有未声明 `AdditionalProperties` 的旧 schema 行为不变。

**built-in 工具应用新约束**：

| 工具 | 字段 | 约束 |
|------|------|------|
| `shell` | `command` | `Enum: DefaultShellAllowlist()` |
| `shell` | `cwd` | `Format: "path"`，`MaxLength: 4096` |
| `shell` | `timeout_ms` | `Minimum: 0`，`Maximum: float64(maxShellTimeout / time.Millisecond)` |
| `shell` | `max_output_bytes` | `Minimum: 0`，`Maximum: float64(maxHardShellBytes)` |
| `read_file` | `limit` | `Minimum: 0`，`Maximum: float64(maxHardReadBytes)` |
| `read_file` | `offset` | `Minimum: 0` |
| `write_file` | `content` | `MaxLength: int(maxHardWriteBytes)`（`maxHardWriteBytes = 32 << 20`） |
| `edit_file` | `old_text` / `new_text` | `MaxLength: int(maxHardWriteBytes)`（对称，避免 LLM 一次性塞超大字符串） |
| `list_dir` | `max_depth` | `Minimum: 0`，`Maximum: 20` |
| 全部 built-in root schema | — | `AdditionalProperties: ptrBool(false)` |

### FR-5: 主进程 + 渲染层 — 用户文件导入 + 工作区强制

> v2 关键变更：`imported_files` 表落 Go 端（main 端无 DB），`injectSystemNote` 走 JSON-RPC，session 删除时清 workspace。

#### FR-5.1 工作区根（managed workspace root）

**所有权**：main 进程独占路径解析 + 文件拷贝；workspace 状态由 Go 端 GORM 表维护；Go agent 只读 env 拿到绝对路径。

**helper 落地位置**：并入现有 `src/main/libs/user-paths.ts`，不开新文件（AGENTS.md 强调「优先使用现有模式与本地 helper」）。

```ts
// src/main/libs/user-paths.ts 增项
import { app } from 'electron';
import path from 'node:path';

export interface WorkspaceLocation {
  /** 绝对路径；macOS 例: ~/Library/Application Support/darvin-cowork/workspaces/{sid}/ */
  rootPath: string;
  /** workspace 自身的 id（v0 = sessionId，一一对应） */
  workspaceId: string;
}

/** v0 单 workspace per session；workspaceId = sessionId */
export function resolveWorkspaceRoot(sessionId: string): WorkspaceLocation {
  const root = path.join(userDataDir(), 'workspaces', sessionId);
  return { rootPath: root, workspaceId: sessionId };
}

export async function ensureWorkspaceRoot(loc: WorkspaceLocation): Promise<void> {
  await fs.promises.mkdir(loc.rootPath, { recursive: true });
}

/** 单 workspace 总容量上限（软上限，触发后 import 拒；不主动 GC） */
export const MAX_WORKSPACE_BYTES = 500 * 1024 * 1024; // 500 MiB

/** 单文件导入上限 */
export const MAX_IMPORT_BYTES = 50 * 1024 * 1024;     // 50 MiB
```

**传给 Go agent**（`src/main/runtime/manager.ts` 改造）：

```ts
spawn(bin, [], {
  env: {
    ...process.env,
    DARVIN_AGENT_WORKSPACE: loc.rootPath,  // ← Go agent 的 fsSandbox.root
  },
});
```

`cmd/app/main.go` 启动期读 env：

```go
workdir := os.Getenv("DARVIN_AGENT_WORKSPACE")
if workdir == "" {
    wd, _ := os.Getwd()
    workdir = wd  // dev 期兜底
}
log.Info("workspace resolved",
    zap.String("env", os.Getenv("DARVIN_AGENT_WORKSPACE")),
    zap.String("effective", workdir))
sb, err := newFsSandbox(workdir, nil)
```

**不变量**：

1. main 进程 `whenReady` → `client.bootstrapActiveSession()`（走 JSON-RPC）→ `resolveWorkspaceRoot(sid)` → `ensureWorkspaceRoot(loc)` → `mgr.start(loc.rootPath)` 四步**严格按序**；先建好 workspace 目录再起 Go 子进程。
2. renderer **任何** UI 路径都不允许直接 `fs.*` 或调 native dialog；只走 `window.darvin.importFiles()` → IPC → main 弹 dialog → main 拷文件。
3. Go agent 的 `fsSandbox.root` 在启动期就是 env 注入的绝对路径；FR-1 沙箱按 realRoot 校验，imported 文件天然在 root 内。
4. **env vs main 计算不一致时**：Go 端启动 log 一行 `env=X effective=Y`；main 端优先使用自己 `resolveWorkspaceRoot(sid)` 算出的路径，env 仅作 dev 期兜底。

#### FR-5.2 GORM 模型（Go 端）

**模型**（`src/darvin-agent/internal/agent/store/models.go` 增项）：

```go
type ImportedFile struct {
    ID            string    `gorm:"primaryKey"`
    SessionID     string    `gorm:"index;not null"`
    OriginalName  string    `gorm:"not null"`          // 仅 basename
    RelativePath  string    `gorm:"uniqueIndex;not null"`
    Size          int64     `gorm:"not null"`
    MimeType      *string
    Sha256        string    `gorm:"not null"`
    ImportedAt    time.Time `gorm:"autoCreateTime"`
}
func (ImportedFile) TableName() string { return "imported_files" }
```

**AutoMigrate 注册**（`src/darvin-agent/cmd/app/main.go:96-101`）：在 `&store.AppState{}` 后追加 `&store.ImportedFile{}`。

**仓库层**（`src/darvin-agent/internal/agent/store/imported_file_store.go` 新增）：

```go
type ImportedFileStore struct{ db *gorm.DB }

func NewImportedFileStore(db *gorm.DB) *ImportedFileStore

// Insert 含 workspace 容量上限检查（事务内）；超限返 ErrWorkspaceFull。
// concurrency: 与 SumBytes 共享 *gorm.DB，事务级 isolation。
func (s *ImportedFileStore) Insert(ctx context.Context, sessionID string, size int64, ...) (ImportedFile, error)

func (s *ImportedFileStore) Delete(ctx context.Context, sessionID, relPath string) error
func (s *ImportedFileStore) List(ctx context.Context, sessionID string) ([]ImportedFile, error)
func (s *ImportedFileStore) SumBytes(ctx context.Context, sessionID string) (int64, error)
func (s *ImportedFileStore) DeleteBySession(ctx context.Context, sessionID string) error  // session 删时调
```

**事务约束**（防 #11 review：workspace TOCTOU）：

```go
err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    var sum int64
    if err := tx.Model(&ImportedFile{}).
        Where("session_id = ?", sessionID).
        Select("COALESCE(SUM(size), 0)").Scan(&sum).Error; err != nil { return err }
    if sum + size > MaxWorkspaceBytes { return ErrWorkspaceFull }
    return tx.Create(&record).Error
})
```

#### FR-5.3 JSON-RPC methods（Go 端 handler）

新增 methods（`src/darvin-agent/internal/gateway/handlers.go` + `jsonrpc.go` 注册）：

| method | params | returns | 说明 |
|--------|--------|---------|------|
| `agent.save_message` | `{ sessionId, content, meta?: { tag? } }` | `{ id }` | 见下方 |
| `agent.import_files` | `{ sourcePaths: string[] }` | `{ imported, skipped }` | 见下方 |
| `agent.list_imported_files` | `{ sessionId }` | `{ files, workspaceBytes }` | |
| `agent.remove_imported_file` | `{ sessionId, relPath }` | `{ removed }` | |
| `agent.get_workspace_info` | `{ sessionId }` | `{ workspaceBytes }` | 不返回 `workspaceRoot`（main 端持有） |
| `agent.delete_session_workspace` | `{ sessionId }` | `{ deleted }` | 删表行 + main 端删 fs（实际删 fs 由 main 触发；Go 端只清表） |

**`agent.save_message`**：在指定 session 下插入一条 `Message` 行。Role 由 `meta.tag` 派生：`tag === 'workspace_event'` → `role: 'system'`，否则 caller 显式传 `role`（默认 `user`）。`meta` 字段不入库。`Message.ID` 由 Go 侧 `uuid.NewString()` 派生，`Timestamp` 由 Go 端 `time.Now().UnixMilli()` 派生。

实现：handler 取 `MessageStore.Save(ctx, &store.MessageRecord{...})`。

```go
func (h *Handlers) handleSaveMessage(ctx context.Context, params json.RawMessage) (any, *rpcerr.Error) {
    var req struct {
        SessionID string  `json:"sessionId"`
        Content   string  `json:"content"`
        Role      string  `json:"role"`
        Meta      *struct{ Tag string `json:"tag"` } `json:"meta"`
    }
    if err := json.Unmarshal(params, &req); err != nil { return nil, rpcErrBadRequest(err) }
    role := req.Role
    if req.Meta != nil && req.Meta.Tag == "workspace_event" { role = "system" }
    if role == "" { role = "user" }
    id := uuid.NewString()
    if err := h.msgStore.Save(ctx, &store.MessageRecord{
        ID: id, SessionID: req.SessionID, Role: role,
        Content: req.Content, Timestamp: time.Now().UnixMilli(),
    }); err != nil { return nil, rpcErrInternal(err) }
    return map[string]any{"id": id}, nil
}
```

**`agent.import_files`**：纯 Go 端实现，做 dedupe / capacity / sha256 检查后写表 + 落 `WorkspaceChanged` 推事件（由 Go 端 EventLedger 发 `DarvinPushEvent.WorkspaceChanged` 到 main，main 再 broadcast 到 renderer）。

> **架构决策说明**：v1 设想在 main 端 `importFiles.ts` 做 dialog → copy → DB。v2 把「写 DB」完全收回 Go 端，原因：① 跨 renderer tab 的并发 TOCTOU 必须由 SQLite 事务兜底；② main 端无 DB，所有写本来就走 JSON-RPC；③ Go 端的 `ImportedFileStore.Insert` 已在事务内，main 端不需要重复实现容量检查。

流程：

1. handler 接收 `sourcePaths: string[]`；
2. 对每条：lstat → `isFile()` 检查 → `size > MAX_IMPORT_BYTES` 检查 → sha256 → 调用 `Insert`（事务内自检容量 + dedupe by sha256 + basename）；
3. 返回 `{ imported: ImportedFile[], skipped: { reason, message }[] }`。

文件拷贝仍由 main 端 `dialog.showOpenDialog` + `importFilesDialog` 触发——dialog 必须在 main 端（renderer 拿不到 native dialog）。Go 端 handler 只接 `sourcePaths`，拷文件由 main 在调 handler 之前完成。

> 这一刀切得不太干净——dialog 拿到的路径在 main 端，拷文件得在 main 端做，但写 DB 又在 Go 端。**落地时**的具体时序：
>
> 1. main `dialog.showOpenDialog` 拿 `sourcePaths`；
> 2. main 对每条 `fs.copyFile(src, workspaceRoot/<relPath>)`（streaming pipeline）；
> 3. main 算 sha256（流式 hash）；
> 4. main 调 `agent.import_files { sourcePaths: [<workspaceRoot>/<relPath>], workspaceRelPaths: [...] }`，handler 写入 `imported_files` 表；
> 5. Go 端 `WorkspaceChanged` 事件 → main 端 EventRouter 透推 renderer。

> 不变量：handler 收到的路径必须在 workspace root 内（realpath containment check，handler 入口拦截）——防 main 端被注入 `sourcePath` 指向 workspace 外。

#### FR-5.4 IPC 契约（main 端）

`src/shared/darvin-api.ts` 加类型：

```ts
export interface DarvinImportedFile {
  id: string;
  originalName: string;          // basename
  relativePath: string;
  size: number;
  mimeType: string | null;
  sha256: string;
  importedAt: number;
}

export type DarvinImportErrorCode =
  | 'too_large'
  | 'unsupported_type'
  | 'workspace_full'
  | 'source_unreadable'
  | 'copy_failed'
  | 'name_conflict';

export interface DarvinImportFilesResponse {
  imported: DarvinImportedFile[];
  skipped: Array<{ sourcePath: string; reason: DarvinImportErrorCode; message: string }>;
}

export interface DarvinListImportedFilesResponse {
  files: DarvinImportedFile[];
  workspaceBytes: number;
}

export interface DarvinRemoveImportedFileResponse {
  removed: boolean;
}

export interface DarvinWorkspaceInfoResponse {
  workspaceBytes: number;
}

export const DarvinPushEvent = {
  SessionsChanged: 'darvin:push:sessions-changed',
  ActiveSessionChanged: 'darvin:push:active-session-changed',
  SessionEvent: 'darvin:push:session-event',
  WorkspaceChanged: 'darvin:push:workspace-changed',  // 新增
} as const;
```

**不变量**：`workspaceRoot` 绝对路径**不**经 IPC 暴露给 renderer（v1 设想透传，v2 收回：避免 renderer 持长生命周期绝对路径；renderer 只看 `workspaceBytes` 数字与 chip 元数据）。

#### FR-5.5 main 端 IPC handlers（`src/main/index.ts`）

```ts
ipcMain.handle('darvin:import_files', async (): Promise<DarvinImportFilesResponse> => {
  if (!cache.activeSessionId) throw new Error('no active session');
  const loc = resolveWorkspaceRoot(cache.activeSessionId);
  await ensureWorkspaceRoot(loc);

  const result = await dialog.showOpenDialog(getMainWindow()!, {
    title: t('import.dialog.title'),
    properties: ['openFile', 'multiSelections'],
    filters: [{ name: 'Text files', extensions: TEXT_FILE_EXTS }],
  });
  if (result.canceled || result.filePaths.length === 0) return { imported: [], skipped: [] };

  return await runImport(loc, result.filePaths, cache.activeSessionId, client);
});

// 内部：src/main/libs/importFiles.ts（v2 改名：原来叫 importFiles.ts 内容也调过）
async function runImport(loc, sourcePaths, sessionId, client): Promise<DarvinImportFilesResponse> {
  const imported: DarvinImportedFile[] = [];
  const skipped: Array<{ sourcePath: string; reason: DarvinImportErrorCode; message: string }> = [];

  for (const src of sourcePaths) {
    try {
      const lst = await fs.promises.lstat(src);
      if (!lst.isFile() || lst.isSymbolicLink()) {
        skipped.push({ sourcePath: src, reason: 'unsupported_type', message: 'not a regular file (symlink rejected)' });
        continue;
      }
      if (lst.size > MAX_IMPORT_BYTES) {
        skipped.push({ sourcePath: src, reason: 'too_large', message: `file size ${lst.size} > ${MAX_IMPORT_BYTES}` });
        continue;
      }
      const ext = path.extname(src).toLowerCase().slice(1);
      if (!TEXT_FILE_EXTS.includes(ext)) {
        skipped.push({ sourcePath: src, reason: 'unsupported_type', message: `extension ${ext} not in whitelist` });
        continue;
      }

      const base = path.basename(src);
      const sha = await sha256OfFile(src);
      const targetRel = await resolveTargetRelative(base, sha, /* 通过 client.list_imported_files 查 */);
      const targetAbs = path.join(loc.rootPath, targetRel);
      await fs.promises.mkdir(path.dirname(targetAbs), { recursive: true });
      await copyFileStreaming(src, targetAbs);

      const resp = await client.request<{ inserted: DarvinImportedFile }>(
        'agent.import_files',
        { sessionId, sourcePaths: [targetAbs], workspaceRelPaths: [targetRel], shas: [sha], sizes: [lst.size], originalNames: [base] },
      );
      imported.push(resp.inserted);
    } catch (e) {
      const code = e.code === 'ENOENT' || e.code === 'EACCES' ? 'source_unreadable' : 'copy_failed';
      skipped.push({ sourcePath: src, reason: code, message: e.message });
    }
  }

  if (imported.length > 0) {
    // 注意顺序：先 save_message（同步等 Go ack），再触发 push
    await client.request('agent.save_message', {
      sessionId,
      content: formatImportNote(imported),
      meta: { tag: 'workspace_event' },
    });
    // WorkspaceChanged push 由 Go 端在 import_files 完成后自动发，主端 EventRouter 透传
  }
  return { imported, skipped };
}
```

**name conflict**（v2 同步落地）：`resolveTargetRelative(base, sha, existing)` 由 main 端跑（不查 Go DB）：
- 同 basename + 相同 sha256 → 静默 dedupe，不写入；
- 同 basename + 不同 sha256 → `"foo (2).md"` / `"foo (3).md"` 后缀递增。

`existing` 通过 `client.request('agent.list_imported_files', { sessionId })` 一次拿全（不频繁，import 时拉一次）。

#### FR-5.6 其它 IPC handlers

```ts
ipcMain.handle('darvin:list_imported_files', async () => {
  if (!cache.activeSessionId) return { files: [], workspaceBytes: 0 };
  return client.request('agent.list_imported_files', { sessionId: cache.activeSessionId });
});

ipcMain.handle('darvin:remove_imported_file', async (_e, relPath: string) => {
  if (!cache.activeSessionId) throw new Error('no active session');
  const loc = resolveWorkspaceRoot(cache.activeSessionId);
  // 防 path traversal
  const realAbs = await fs.promises.realpath(path.join(loc.rootPath, relPath));
  const realRoot = await fs.promises.realpath(loc.rootPath);
  if (!realAbs.startsWith(realRoot + path.sep) && realAbs !== realRoot) {
    throw new Error('path escapes workspace');
  }
  await fs.promises.unlink(realAbs);
  const r = await client.request<DarvinRemoveImportedFileResponse>(
    'agent.remove_imported_file', { sessionId: cache.activeSessionId, relPath },
  );
  await client.request('agent.save_message', {
    sessionId: cache.activeSessionId,
    content: `[系统] 文件已从工作区移除：${relPath}`,
    meta: { tag: 'workspace_event' },
  });
  return r;
});

ipcMain.handle('darvin:get_workspace_info', async () => {
  if (!cache.activeSessionId) return { workspaceBytes: 0 };
  return client.request('agent.get_workspace_info', { sessionId: cache.activeSessionId });
});

ipcMain.handle('darvin:reveal_workspace', async () => {
  if (!cache.activeSessionId) return;
  const loc = resolveWorkspaceRoot(cache.activeSessionId);
  await ensureWorkspaceRoot(loc);
  shell.showItemInFolder(loc.rootPath);
});
```

**`darvin:delete_session` 增项**（v2）：删 session 时同步清 workspace。

```ts
ipcMain.handle('darvin:delete_session', async (_e, sessionId: string) => {
  // ... 原有 abort + delete_session RPC ...
  const loc = resolveWorkspaceRoot(sessionId);
  try {
    await fs.promises.rm(loc.rootPath, { recursive: true, force: true });
  } catch (e) {
    console.warn(`[main] workspace cleanup failed for ${sessionId}: ${(e as Error).message}`);
  }
  // ... 原有 refresh + broadcast ...
});
```

#### FR-5.7 preload bridge（`src/preload/index.ts`）

```ts
contextBridge.exposeInMainWorld('darvin', {
  importFiles: () => ipcRenderer.invoke('darvin:import_files'),
  listImportedFiles: () => ipcRenderer.invoke('darvin:list_imported_files'),
  removeImportedFile: (relPath: string) => ipcRenderer.invoke('darvin:remove_imported_file', relPath),
  getWorkspaceInfo: () => ipcRenderer.invoke('darvin:get_workspace_info'),
  revealWorkspace: () => ipcRenderer.invoke('darvin:reveal_workspace'),
  onWorkspaceChanged: (cb: (info: { sessionId: string; files: DarvinImportedFile[] }) => void) => {
    const handler = (_e: unknown, info: any) => cb(info);
    ipcRenderer.on(DarvinPushEvent.WorkspaceChanged, handler);
    return () => ipcRenderer.removeListener(DarvinPushEvent.WorkspaceChanged, handler);
  },
});
```

#### FR-5.8 渲染层 UI

新增组件 / composable：

```
src/renderer/components/chat/
├── ImportButton.vue              # composer 旁的 paperclip 图标按钮
└── ImportedFilesBar.vue          # composer 上方的 chip strip
src/renderer/composables/
└── useImportedFiles.ts           # 单一来源：files ref + importFiles/remove + onWorkspaceChanged 订阅
```

**`ImportButton.vue`**（最小骨架）：

```vue
<script setup lang="ts">
const { importFiles, busy } = useImportedFiles();
const onClick = async () => {
  const res = await importFiles();
  if (res.skipped.length) showToast(formatSkipped(res.skipped));
};
</script>
<template>
  <button :disabled="busy" @click="onClick" :aria-label="t('composer.import')">
    <Icon name="paperclip" />
  </button>
</template>
```

**`ImportedFilesBar.vue`**：

```vue
<script setup lang="ts">
const { files, remove, workspaceBytes } = useImportedFiles();
const fmtSize = (b: number) => formatBytes(b);
</script>
<template>
  <div v-if="files.length > 0" class="flex flex-wrap gap-1.5">
    <div v-for="f in files" :key="f.id" class="chip">
      <Icon name="file-text" />
      <span class="filename">{{ f.originalName }}</span>
      <span class="size">{{ fmtSize(f.size) }}</span>
      <button @click="remove(f.relativePath)" :aria-label="t('imported.remove')">
        <Icon name="x" />
      </button>
    </div>
    <div class="workspace-meter">
      <span>{{ t('workspace.used', { bytes: fmtSize(workspaceBytes), max: fmtSize(MAX_WORKSPACE_BYTES) }) }}</span>
    </div>
  </div>
</template>
```

**`Composer.vue` 改造**：在 `<textarea>` 左侧加 `<ImportButton />`；`<ImportedFilesBar />` 挂在 `<textarea>` 上方。

#### FR-5.9 图标（新增）

- 已有：`paperclip.svg` / `x.svg`（已在 `src/renderer/assets/icons/`）
- 新增：`src/renderer/assets/icons/file-text.svg`（按 AGENTS.md 图标规范：viewBox="0 0 34 34" + stroke="currentColor" + stroke-width="2.4" + round caps）

#### FR-5.10 i18n 新 key（`src/renderer/services/i18n.ts`）

```ts
'composer.import': '导入文件',
'imported.remove': '从工作区移除',
'imported.empty': '工作区还没有导入的文件',
'workspace.used': '工作区已用 {bytes} / {max}',
'workspace.reveal': '在文件管理器中打开',
'import.dialog.title': '选择要导入的文件',
'import.toast.imported': '已导入 {count} 个文件',
'import.toast.partial': '导入 {ok} 个，跳过 {skip} 个',
'import.error.too_large': '文件过大（{size}），单文件上限 {max}',
'import.error.unsupported_type': '文件类型不支持：{ext}',
'import.error.workspace_full': '工作区空间不足',
'import.error.source_unreadable': '无法读取源文件',
'import.error.copy_failed': '复制失败：{message}',
```

en 字典同步补齐，`assertSameKeys` 不通过即 dev 红屏。

#### FR-5.11 安全边界

1. **原始路径不落盘**：main 端 copy 完成后，源 `filePath` 立刻出栈；日志只记 `basename` 不记完整路径；DB 只存 `original_name`（basename）。
2. **renderer 不持 workspace 绝对路径**：v2 收紧——`workspaceRoot` 不经 IPC 暴露，renderer 只能拿到 `workspaceBytes` 数字 + chip 元数据。`revealWorkspace` 由 main 端调 `shell.showItemInFolder`。
3. **agent 永远见不到 workspace 外路径**：Go agent 的 `fsSandbox.root` = workspace 根；任何 LLM 调 `read_file` / `write_file` / `list_dir` / `shell.cwd` 都过 FR-1 沙箱。
4. **import 不引入 symlink**：源是 symlink 时 `lstat.isSymbolicLink()` 真 → 拒（`unsupported_type`）。
5. **remove 防 path traversal**：handler 收到 `relPath` 后用 `realpath` 拼绝对路径再与 `workspaceRoot.realpath` 比 `startsWith`；越界抛错。
6. **system note role 隔离**：落库 `role: 'system'`；renderer 按 role 渲染（中性灰色样式）；ctxengine ingest 时按 role 路由到 system 区，与 user message 物理隔离。content 以 `[系统]` / `[system]` 前缀开头保留 UI 兜底。
7. **workspace 总量 cap 原子化**：500 MiB 上限，事务内 `SumBytes + Insert`，并发绕过不可能。
8. **session 删除清 workspace**：`fs.rm(root, { recursive: true, force: true })`，避免孤儿累积。
9. **text-only v0**：binary 文件（PDF / 图片 / 编译产物）目前 Go agent `read_file` 走 UTF-8 读会乱码；v0 通过 `TEXT_FILE_EXTS` 直接拒 binary。

#### FR-5.12 启动时序（`src/main/index.ts`）

```ts
app.whenReady().then(async () => {
  installAppMenu();

  try {
    // 1) bootstrap active session via Go (RPC)
    const active = await client.request<{ sessionId: string | null }>('agent.get_active_session', {});
    if (!active.sessionId) throw new Error('no active session after bootstrap');

    // 2) workspace root
    const loc = resolveWorkspaceRoot(active.sessionId);
    await ensureWorkspaceRoot(loc);

    // 3) Go 子进程（env 已含 DARVIN_AGENT_WORKSPACE）
    const resolved = await mgr.start();
    await client.connect(resolved.port);
    await subscribeAllSessions();
    eventRouter.start();
  } catch (e) {
    console.error(`[runtime] ${(e as Error).message}`);
  }

  createWindow();
});
```

`cmd/app/main.go` 启动期读 env（同 FR-5.1）。

## 4. 实现方案

### 4.1 文件布局

新增 / 修改文件（Go + main + preload + shared + renderer 五层）：

```
src/darvin-agent/
├── cmd/app/main.go                              # 改：读 DARVIN_AGENT_WORKSPACE env；AutoMigrate 加 ImportedFile
├── internal/agent/tool/
│   ├── sandbox.go                               # 改：Resolve 重写 + openRootFile / openRootFileLimited (含 offset)
│   ├── sandbox_test.go                          # 改：补 symlink + 排除清单 + offset 用例
│   ├── exclusions.go                            # 新增：compileExclusions + component-level match
│   ├── exclusions_test.go                       # 新增
│   ├── limitwriter.go                           # 新增：limitWriter + truncation 计数
│   ├── limitwriter_test.go                      # 新增
│   ├── params.go                                # 改：validateArgs 升级
│   ├── params_test.go                           # 改：补新约束用例
│   ├── fs.go                                    # 改：readFileTool 走 openRootFileLimited (含 offset)
│   ├── fs_test.go                               # 改：offset + truncation 用例
│   ├── shell.go                                 # 改：Enum + max_output_bytes + limitWriter
│   ├── shell_test.go                            # 改：truncation / enum 拒
│   ├── builtins.go                              # 不变（透传 exclusions）
│   └── registry.go                              # 不变
├── internal/agent/llm/types.go                  # 改：ParameterSchema 加字段
├── internal/agent/store/
│   ├── models.go                                # 改：增 ImportedFile struct
│   ├── imported_file_store.go                   # 新增：ImportedFileStore（含事务化 Insert）
│   └── imported_file_store_test.go              # 新增
└── internal/gateway/
    ├── handlers.go                              # 改：增 handleSaveMessage / handleImportFiles / handleListImportedFiles / handleRemoveImportedFile / handleGetWorkspaceInfo
    ├── jsonrpc.go                               # 改：method 注册表增 5 条
    └── handlers_test.go                         # 改：补 workspace + save_message 用例

src/main/
├── libs/
│   ├── user-paths.ts                            # 改：增 resolveWorkspaceRoot / ensureWorkspaceRoot / MAX_WORKSPACE_BYTES
│   ├── user-paths_test.ts                       # 新增（vitest）
│   ├── importFiles.ts                           # 新增：dialog → copy → RPC
│   └── importFiles_test.ts                      # 新增
├── index.ts                                     # 改：whenReady 4 步 + 5 新 IPC handler + delete_session 同步清 workspace
└── runtime/manager.ts                           # 改：spawn env 注入 DARVIN_AGENT_WORKSPACE

src/preload/
└── index.ts                                     # 改：桥接 5 新 method + onWorkspaceChanged

src/shared/
└── darvin-api.ts                                # 改：DarvinImportedFile / 各 Response 类型 + WorkspaceChanged push + DarvinApi 5 新 method

src/renderer/
├── services/i18n.ts                             # 改：新 key（zh + en 一致）
├── composables/useImportedFiles.ts              # 新增
├── components/chat/ImportButton.vue             # 新增
├── components/chat/ImportedFilesBar.vue         # 新增
├── components/chat/Composer.vue                 # 改：嵌入
└── assets/icons/file-text.svg                   # 新增（paperclip.svg / x.svg 已存在）
```

新增依赖（`package.json`）：无（v0 不引新依赖；streaming copy 走 `node:fs/promises` 的 `createReadStream` + `pipeline`，uuidv4 已有）。

**前置条件（必须先满足才能落地，详见 §8）**：
- `package.json` 加 `test` 脚本（vitest）；当前仓库 `AGENTS.md` 标注「尚未配置单元测试运行器」。
- 跑 `npm install -D vitest @vitest/coverage-v8` + 在 `package.json` 注册 `"test": "vitest run"`。

### 4.2 sandbox.go 关键代码骨架

```go
type fsSandbox struct {
    root        string                  // 词法 root（构造时传入）
    realRoot    string                  // EvalSymlinks(root)
    exclusions  []compiledExclusion
}

func newFsSandbox(workdir string, exclusions []string) (*fsSandbox, error) {
    if workdir == "" { workdir, _ = os.Getwd() }
    abs, err := filepath.Abs(workdir)
    if err != nil { return nil, fmt.Errorf("sandbox: abs %q: %w", workdir, err) }
    abs = filepath.Clean(abs)
    real, err := filepath.EvalSymlinks(abs)
    if err != nil { real = abs }   // root 不存在时降级
    excl, err := compileExclusions(exclusions)
    if err != nil { return nil, fmt.Errorf("sandbox: compile exclusions: %w", err) }
    return &fsSandbox{root: abs, realRoot: real, exclusions: excl}, nil
}

func (s *fsSandbox) Resolve(p string) (string, error) {
    var abs string
    if filepath.IsAbs(p) {
        abs = filepath.Clean(p)
    } else {
        abs = filepath.Clean(filepath.Join(s.root, p))
    }
    // 1) 排除清单先查（component-level，case-insensitive）
    if pattern, ok := matchExclusion(s.exclusions, abs); ok {
        return "", fmt.Errorf("%w: %s matches pattern %q", ErrPathExcluded, abs, pattern)
    }
    // 2) EvalSymlinks：路径不存在时降级为「父目录 EvalSymlinks + 叶子字面」
    real, err := evalPathReal(abs)
    if err != nil { return "", fmt.Errorf("sandbox: resolve %q: %w", p, err) }
    // 3) 重新 containment
    rel, err := filepath.Rel(s.realRoot, real)
    if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
        return "", fmt.Errorf("%w: %q resolves to %q outside root %q",
            ErrPathEscapesViaSymlink, p, real, s.realRoot)
    }
    return abs, nil
}
```

### 4.3 openRootFile / openRootFileLimited 骨架

```go
func (s *fsSandbox) openRootFile(p string, label string) (*os.File, string, error) {
    abs, err := s.Resolve(p)
    if err != nil { return nil, "", err }
    real, err := evalPathReal(abs)
    if err != nil { return nil, "", fmt.Errorf("%s: resolve %q: %w", label, p, err) }
    f, err := os.Open(real)
    if err != nil { return nil, "", fmt.Errorf("%s: open %q: %w", label, real, err) }
    return f, real, nil
}

func (s *fsSandbox) openRootFileLimited(p string, label string, offset, maxBytes int64) (*os.File, int64, bool, error) {
    if maxBytes <= 0 || maxBytes > maxHardReadBytes {
        return nil, 0, false, fmt.Errorf("%w: maxBytes=%d", ErrReadTooLarge, maxBytes)
    }
    if offset < 0 {
        return nil, 0, false, fmt.Errorf("sandbox: negative offset %d", offset)
    }
    f, real, err := s.openRootFile(p, label)
    if err != nil { return nil, 0, false, err }
    if offset > 0 {
        if _, err := f.Seek(offset, io.SeekStart); err != nil {
            f.Close()
            return nil, 0, false, fmt.Errorf("%s: seek offset %d: %w", label, offset, err)
        }
    }
    lr := io.LimitReader(f, maxBytes+1)
    data, err := io.ReadAll(lr)
    if err != nil { f.Close(); return nil, 0, false, fmt.Errorf("%s: read %q: %w", label, real, err) }
    truncated := int64(len(data)) > maxBytes
    if truncated { data = data[:maxBytes] }
    return f, int64(len(data)), truncated, nil
}
```

### 4.4 ImportedFileStore 骨架

```go
const MaxWorkspaceBytes int64 = 500 * 1024 * 1024

type ImportedFileStore struct{ db *gorm.DB }

func NewImportedFileStore(db *gorm.DB) *ImportedFileStore { return &ImportedFileStore{db: db} }

var ErrWorkspaceFull = errors.New("store: workspace would exceed limit")
var ErrDuplicate = errors.New("store: file with same sha256 already imported")

func (s *ImportedFileStore) Insert(ctx context.Context, rec ImportedFile) (ImportedFile, error) {
    var inserted ImportedFile
    err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // dedupe by sha256 + session_id
        var existing ImportedFile
        if err := tx.Where("session_id = ? AND sha256 = ?", rec.SessionID, rec.Sha256).
            First(&existing).Error; err == nil {
            return ErrDuplicate
        }
        // capacity check
        var sum int64
        if err := tx.Model(&ImportedFile{}).
            Where("session_id = ?", rec.SessionID).
            Select("COALESCE(SUM(size), 0)").Scan(&sum).Error; err != nil {
            return err
        }
        if sum + rec.Size > MaxWorkspaceBytes {
            return ErrWorkspaceFull
        }
        return tx.Create(&rec).Error
    })
    if err != nil { return ImportedFile{}, err }
    return rec, nil
}

func (s *ImportedFileStore) Delete(ctx context.Context, sessionID, relPath string) error {
    return s.db.WithContext(ctx).
        Where("session_id = ? AND relative_path = ?", sessionID, relPath).
        Delete(&ImportedFile{}).Error
}

func (s *ImportedFileStore) List(ctx context.Context, sessionID string) ([]ImportedFile, error) {
    var rows []ImportedFile
    err := s.db.WithContext(ctx).
        Where("session_id = ?", sessionID).
        Order("imported_at DESC").Find(&rows).Error
    return rows, err
}

func (s *ImportedFileStore) SumBytes(ctx context.Context, sessionID string) (int64, error) {
    var sum int64
    err := s.db.WithContext(ctx).Model(&ImportedFile{}).
        Where("session_id = ?", sessionID).
        Select("COALESCE(SUM(size), 0)").Scan(&sum).Error
    return sum, err
}

func (s *ImportedFileStore) DeleteBySession(ctx context.Context, sessionID string) error {
    return s.db.WithContext(ctx).
        Where("session_id = ?", sessionID).
        Delete(&ImportedFile{}).Error
}
```

### 4.5 llm.ParameterSchema 字段扩展注意点

- `Minimum` / `Maximum` 用 `*float64`：JSON Schema numeric 同时支持 int / float，pointer 区分「未声明」与「声明 0」。
- `MinLength` / `MaxLength` 用 `*int`：`MinLength: 0` 是合法约束，不能用 `int` 默认值区分。
- `Pattern`：regexp 编译在 `ParameterProperty` 构造时一次性 `MustCompile`（避免每次 validate 重编译）。
- `Items`：仅 1 层 array-of-T 不嵌套（不写 allOf/anyOf）；若 Items 不为空但 Type != array，构造时返错。
- 兼容性：`ParameterSchema` 加字段是**非破坏性**变更（所有字段新加，存量 json 序列化 / 反序列化兼容）。

### 4.6 shell 工具改造细节

```go
func (t *shellTool) Parameters() llm.ParameterSchema {
    return llm.ParameterSchema{
        Type: "object",
        Properties: map[string]llm.ParameterProperty{
            "command": {
                Type:        "string",
                Enum:        DefaultShellAllowlist(),
                Description: "Command name; must be in allowlist.",
            },
            "args": {Type: "array", Items: &llm.ParameterProperty{Type: "string"}},
            "cwd":  {Type: "string", Format: "path", MaxLength: ptrInt(4096)},
            "timeout_ms": {Type: "integer",
                Minimum: ptrFloat64(0),
                Maximum: ptrFloat64(float64(maxShellTimeout / time.Millisecond))},
            "max_output_bytes": {Type: "integer",
                Minimum: ptrFloat64(0),
                Maximum: ptrFloat64(float64(maxHardShellBytes))},
        },
        Required:             []string{"command", "args"},
        AdditionalProperties: ptrBool(false),
    }
}
```

`shell.go` execute 内 `bytes.Buffer` 替换：

```go
cap := int64(maxShellBytes)
if v, ok := args["max_output_bytes"].(float64); ok && v > 0 {
    cap = int64(v)
    if cap > maxHardShellBytes { cap = maxHardShellBytes }
}
out := &limitWriter{cap: cap}
errOut := &limitWriter{cap: cap}
c.Stdout, c.Stderr = out, errOut
err := c.Run()

content := out.String()
if errOut.Len() > 0 {
    if content != "" { content += "\n" }
    content += "[stderr]\n" + errOut.String()
}
if out.Truncated() {
    content += fmt.Sprintf("\n[stdout truncated at %d bytes]", cap)
}
if errOut.Truncated() {
    content += fmt.Sprintf("\n[stderr truncated at %d bytes]", cap)
}
```

### 4.7 兼容性与开关

- **不存在老工具 schema 兼容问题**：`AdditionalProperties` 默认 nil → 走旧行为（仍硬拒 unknown args，与现有 `params.go:39` 一致——v2 明确这条不是 warn-but-not-reject）；所有 built-in 工具主动声明 `AdditionalProperties: ptrBool(false)` 走严格模式。
- **`config.yaml` 暂不开放 exclusions 自定义**：v0 走 hard-coded `DefaultPathExclusions()`。
- **`maxHardReadBytes` / `maxHardWriteBytes` / `maxHardShellBytes` 不进 config**：硬上限是安全屏障。
- **测试默认在 Linux runner 跑**：macOS CI 不强依赖；symlink 行为 Linux / macOS 一致，但 `EvalSymlinks(/var)` 在 macOS 上展开为 `/private/var`——这正是要测的攻击面，CI 在 macOS runner 上要单跑 `TestSandboxMacOSVarSymlink`（条件编译 `//go:build darwin`）。

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 路径不存在但父目录存在 | `evalPathReal` 降级：父目录 EvalSymlinks + 叶子字面拼接 |
| 路径完全不存在（中间某层也不存在） | 同上降级逻辑；最终报 ENOENT 由调用方处理 |
| root 本身不存在（`newFsSandbox` 时） | `EvalSymlinks` 失败 → fallback 到 abs；不阻断构造 |
| root 是 symlink（如 `~/proj → ~/proj-real`） | 构造时 realRoot 解析为 `proj-real` |
| symlink 链 `a → b → c`（c 在 root 外） | 链上每段 `EvalSymlinks` 都跑 |
| 用户工作目录里有合法 symlink（如 monorepo vendor） | 当前 v0 行为：拒。后续 v1 加 `symlink_within_workspace: true` config 开关 |
| LLM 传 `path: ""` / `path: "."` | `Resolve` 返 root；`openRootFile` Open 失败（root 是目录）→ 工具返 `is a directory` |
| `command: "ls"` 但 `args: ["-la", "--color=always"]` | enum 校验只查 `command`；args 数组元素走 `Items: { Type: "string" }` |
| `command` 包含 NUL 字节或换行 | enum 拒（不在 allowlist 里就是拒） |
| `cwd: "/etc"` | 词法级 escape → `ErrPathEscapes` |
| shell 输出超 cap 但 exit 0 | 仍返 success，content 含 `[stdout truncated at N bytes]` |
| `write_file` content 超 `maxHardWriteBytes` | `validateArgs` 直接拒 `content too large` |
| macOS CI `TestSandboxMacOSVarSymlink` | 仅 darwin build；非 darwin `t.Skip` |
| `EvalSymlinks` 对循环 symlink 报错 | 捕获 `EINVAL` 类错误，返 `ErrPathEscapesViaSymlink` |
| `pattern` 是用户错误 regex | `compileExclusions` 阶段返错 |
| 用户选了一个目录而非文件 | `lstat.isFile()` 假 → `unsupported_type` |
| 用户选的源文件是 symlink（如 `~/doc.md → /etc/shadow`） | `lstat.isSymbolicLink()` 真 → 拒 |
| 用户选的文件超 50 MiB | `too_large` skipped |
| workspace 已用 480 MiB + 选 30 MiB | `workspace_full` skipped；事务兜底并发场景 |
| 同 basename 不同 sha256 | 自动 `foo (2).md`；DB 两行 |
| 同 basename 同 sha256 | dedupe，DB 不增行 |
| 用户手动在 workspace 根外新建文件 | 不在 imported_files 表里；Go agent 仍能 list_dir 看到 |
| 用户选 100 个文件 | 串行处理；UI 期间 disable ImportButton；progress 反馈 v0 TODO |
| 用户取消 dialog | 返 `{ imported: [], skipped: [] }`；不报错 |
| `remove_imported_file` 传绝对路径 | `realpath.startsWith(realRoot + sep)` 拦截 |
| `remove_imported_file` 传 `../etc/passwd` | 同上，越界拒 |
| 导入时源文件被另一进程删 | `source_unreadable` skipped；batch 继续 |
| 导入时 workspace 目录被外部 rm | `ensureWorkspaceRoot` 重建 |
| **session 删时 workspace 残留** | v2 修订：`fs.rm(root, { recursive: true, force: true })` |
| **两个 renderer tab 同时 import** | v2 修订：Go 端 `Insert` 事务内自检容量，并发绕过不可能 |
| 启动时 main 创建 workspace 之前 Go agent 已启动 | 禁止：`whenReady` 顺序保证 |
| 启动时 `DARVIN_AGENT_WORKSPACE` env 已被外部注入 | main 端优先；env 仅 dev 兜底；Go 端 log `env=X effective=Y` |
| workspace 根落在网络盘 / UNC | v0 不支持（main 端 `app.getPath('userData')` 必是本地盘），启动时发现 UNC 抛 startup error |
| renderer 收到 push 后与本地 state 短暂不一致 | `useImportedFiles` 以 push 为权威 |
| 用户导入 `.env` 文件 | whitelist 不含 `.env` → `unsupported_type`；UI toast「为安全不允许导入敏感凭据」 |
| 用户导入 `node_modules/foo/bar.js` | whitelist 通过；FR-2 排除清单在 Go 端兜底 |
| binary 文件（PDF / 图片 / .png） | whitelist 不含 → skipped |
| shell 工具 `cat` 一个超大文件 | 走 FR-3 `max_output_bytes` 兜底 |
| Go agent 启动后用户调 `read_file { path: "" }` 或 `path: "."` | workspace root；`info.IsDir()` 返「is a directory」 |
| 用户在 renderer 直接调 `fs.*` | 不可能（contextBridge 隔离） |
| **read_file 跳读** | v2 修订：`openRootFileLimited` 接受 `offset`；不传时从 0 读；传 `offset: 500, limit: 100` 读 offset 500-600 字节 |
| **`agent.save_message` role 派生** | v2：`meta.tag: 'workspace_event'` → `role: 'system'`；其它 caller 显式传 role |
| **`WorkspaceChanged` push 顺序** | v2：`agent.save_message` 完成后由 Go 端 EventLedger 推；renderer 端 useImportedFiles 收到 push 后才更新 chip |

## 6. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `internal/agent/tool/sandbox.go` | 改：`Resolve` 重写；新增 `openRootFile` / `openRootFileLimited(p, label, offset, maxBytes)` / `DefaultPathExclusions`；新增 sentinel `ErrPathEscapesViaSymlink` / `ErrPathExcluded` / `ErrReadTooLarge` |
| `internal/agent/tool/sandbox_test.go` | 改：补 symlink + 排除清单 + offset 用例 |
| `internal/agent/tool/exclusions.go` | 新增：`compileExclusions` + `matchExclusion`（component-level，case-insensitive） |
| `internal/agent/tool/exclusions_test.go` | 新增 |
| `internal/agent/tool/limitwriter.go` | 新增：`limitWriter{cap, written, truncated}` |
| `internal/agent/tool/limitwriter_test.go` | 新增 |
| `internal/agent/tool/params.go` | 改：`validateArgs` 升级（enum / format / range / length / pattern / items / `AdditionalProperties`）；保留旧硬拒 unknown args 行为 |
| `internal/agent/tool/params_test.go` | 改：补新约束用例 |
| `internal/agent/tool/fs.go` | 改：`readFileTool` 走 `openRootFileLimited(p, label, offset, limit)`；移除独立 `f.Seek(offset)` 调用与本地 `min` helper |
| `internal/agent/tool/fs_test.go` | 改：offset + truncation / 大 content 拒 / symlink 拒 |
| `internal/agent/tool/shell.go` | 改：`Parameters` 加 `Enum` / `cwd MaxLength` / `timeout_ms range` / `max_output_bytes`；`cmd.Stdout/Stderr` 换 `limitWriter` |
| `internal/agent/tool/shell_test.go` | 改：truncation / 超 `max_output_bytes` / `command` enum 拒 |
| `internal/agent/llm/types.go` | 改：`ParameterSchema` / `ParameterProperty` 加上述字段 |
| `internal/agent/store/models.go` | 改：增 `ImportedFile` struct + `TableName` |
| `internal/agent/store/imported_file_store.go` | 新增：`ImportedFileStore` + 事务化 `Insert` |
| `internal/agent/store/imported_file_store_test.go` | 新增 |
| `internal/gateway/handlers.go` | 改：增 `handleSaveMessage` / `handleImportFiles` / `handleListImportedFiles` / `handleRemoveImportedFile` / `handleGetWorkspaceInfo` |
| `internal/gateway/jsonrpc.go` | 改：method 注册表增 5 条 |
| `internal/gateway/handlers_test.go` | 改：补 workspace + save_message 用例 |
| `cmd/app/main.go` | 改：读 `DARVIN_AGENT_WORKSPACE` env；AutoMigrate 加 `&store.ImportedFile{}`；log `workspace resolved` |
| `src/main/libs/user-paths.ts` | 改：增 `resolveWorkspaceRoot` / `ensureWorkspaceRoot` / `MAX_WORKSPACE_BYTES` / `MAX_IMPORT_BYTES` |
| `src/main/libs/user-paths_test.ts` | 新增 |
| `src/main/libs/importFiles.ts` | 新增：dialog → stream copy → RPC |
| `src/main/libs/importFiles_test.ts` | 新增 |
| `src/main/index.ts` | 改：`whenReady` 4 步 + 5 新 IPC handler + `delete_session` 同步清 workspace |
| `src/main/runtime/manager.ts` | 改：spawn env 注入 `DARVIN_AGENT_WORKSPACE` |
| `src/preload/index.ts` | 改：桥接 5 新 method + `onWorkspaceChanged` |
| `src/shared/darvin-api.ts` | 改：`DarvinImportedFile` / 各 Response + `WorkspaceChanged` push + `DarvinApi` 5 新 method |
| `src/renderer/services/i18n.ts` | 改：增 8 个 key（zh + en 一致） |
| `src/renderer/composables/useImportedFiles.ts` | 新增 |
| `src/renderer/components/chat/ImportButton.vue` | 新增 |
| `src/renderer/components/chat/ImportedFilesBar.vue` | 新增 |
| `src/renderer/components/chat/Composer.vue` | 改：嵌入 |
| `src/renderer/assets/icons/file-text.svg` | 新增 |

## 7. 验收标准

### 7.1 Go 端单测（`go test ./...`）

- [ ] `TestSandboxResolveSymlinkEscape`：root 内放 `link → /etc/passwd`，`Resolve("link")` 必须返 `ErrPathEscapesViaSymlink`。
- [ ] `TestSandboxResolveNestedSymlink`：root 内 `a/b/c` 是 3 段 symlink 链，链尾指向 root 外；任一 Resolve 都必须拒。
- [ ] `TestSandboxResolveNonExistentPath`：root 内不存在的 `subdir/new.txt` Resolve 不报错（返 abs），`openRootFile` 报 ENOENT。
- [ ] `TestSandboxRootIsSymlink`：构造时 root 本身就是 symlink，`Resolve("foo")` 仍按 realRoot 校验。
- [ ] `TestExclusionDefault`：默认清单覆盖 `.git` / `node_modules` / `.env` / `__pycache__` / `target` / `dist` / `.DS_Store`。
- [ ] `TestExclusionComponentLevel`：`proj/sub/.git/foo` 命中 `.git` 即拒。
- [ ] `TestExclusionCaseInsensitive`：`node_modules` 与 `NODE_MODULES` 等价拒。
- [ ] `TestOpenRootFileTOCTOU`：mock `swapSymlink` 在 Resolve 与 Open 之间换 symlink，`openRootFile` 仍拿到原 inode 的内容（或报错）。
- [ ] `TestOpenRootFileLimited`：构造大文件 ≥ `maxReadBytes+1`，验证 truncation + 末尾提示。
- [ ] `TestOpenRootFileLimitedWithOffset`（v2 增项）：`openRootFileLimited(p, "", 100, 50)` 读到 offset 100 起 50 字节。
- [ ] `TestValidateArgsEnum` / `TestValidateArgsRange` / `TestValidateArgsPattern` / `TestValidateArgsMaxLength` / `TestValidateArgsAdditionalPropertiesFalse`：每条新约束至少 1 用例。
- [ ] `TestValidateArgsUnknownArgsHardReject`（v2 增项）：未声明字段仍硬拒（与现有 `params.go:39` 一致）。
- [ ] `TestReadFileOffsetBackwardCompat`：端到端 `read_file { offset: 500, limit: 100 }` 与 `f.ReadAt` 一致。
- [ ] `TestReadFileMaxBytes`：构造超 1 MiB 文件，验证 truncation。
- [ ] `TestWriteFileContentTooLarge`：content 超 `maxHardWriteBytes`，`validateArgs` 拒。
- [ ] `TestShellOutputTruncation`：输出超 `maxShellBytes`，末尾 truncation 提示。
- [ ] `TestShellEnumRejectCommand`：`command: "rm -rf /"` 拒。
- [ ] `TestShellMaxOutputBytes`：`max_output_bytes: 1024` 生效。
- [ ] `TestSandboxMacOSVarSymlink`（仅 darwin）：`/var/folders/...` → `/private/var/folders/...` 透明。
- [ ] `TestImportedFileStoreInsertConcurrency`（v2 增项）：两个 goroutine 并发 Insert，超 `MaxWorkspaceBytes` 的那个返 `ErrWorkspaceFull`，DB 行不超限。
- [ ] `TestImportedFileStoreInsertDedupe`（v2 增项）：同 sha256 二次 Insert 返 `ErrDuplicate`，DB 行数不变。
- [ ] `TestImportedFileStoreDeleteBySession`（v2 增项）：清表，cascade 由 main 端 fs.rm 处理。

### 7.2 Go 端兼容与回归

- [ ] `TestSandboxRejectEscape`（旧 case，词法 escape）仍绿。
- [ ] `TestValidateArgsBasic`（旧 case）仍绿。
- [ ] 旧 schema 不声明 `AdditionalProperties` 走旧硬拒路径，行为不变。
- [ ] `go vet ./...` / `gofmt -l .` 干净。

### 7.3 Go 端集成（`internal/gateway`）

- [ ] `TestHandleSaveMessageWorkspaceEvent`：`meta.tag: 'workspace_event'` → `role: 'system'`。
- [ ] `TestHandleSaveMessageDefaultRole`：无 meta → `role: 'user'`。
- [ ] `TestHandleImportFilesWorkspaceFull`：`Insert` 返 `ErrWorkspaceFull` → handler 转 `{ reason: 'workspace_full' }`。
- [ ] `TestHandleImportFilesPathTraversal`：`sourcePaths` 指向 workspace 外 → handler 拒（realpath containment check）。

### 7.4 main 端单测（vitest）

**前置**：必须先 `npm install -D vitest`，`package.json` 注册 `"test": "vitest run"`（当前仓库没有 `test` 脚本——见 §8）。

- [ ] `user-paths.test.ts::TestResolveWorkspaceRoot`：不同 sessionId 产生不同 root；root 在 `app.getPath('userData')` 下；`ensureWorkspaceRoot` 后是目录。
- [ ] `importFiles.test.ts::TestImportTextFile`：mock dialog 返 `.md` → 拷到 workspace → 调 `agent.import_files` → 收到 `inserted`。
- [ ] `TestImportSkippedTooLarge`：60 MiB → `too_large`。
- [ ] `TestImportSkippedUnsupportedExt`：`.pdf` / `.env` → `unsupported_type`。
- [ ] `TestImportSkippedSymlink`：源是 symlink → `unsupported_type`。
- [ ] `TestImportDedupeSameHash`：同 basename 同 sha256 二次 → 不调 RPC（dedupe）；DB 行不变。
- [ ] `TestImportConflictSameName`：同 basename 不同 sha256 → `foo (2).md`。
- [ ] `TestRemoveImportedFilePathTraversal`：`../etc/passwd` → handler 抛 `path escapes workspace`。
- [ ] `TestRemoveImportedFileSuccess`：正常路径 → unlink + RPC + save_message（`role: 'system'`）。
- [ ] `TestDeleteSessionCleansWorkspace`（v2 增项）：`darvin:delete_session` 后 `fs.rm` 触发，workspace root 不存在。
- [ ] `TestWorkspaceBootstrapOrder`：`mgr.start` 在 `ensureWorkspaceRoot` 之前调用 → 抛「workspace not ready」。

### 7.5 手工 smoke（v2 全部按 `<workspaceRoot>` 重写）

**前置**：`npm run build:agent && npm start`，新建 session → 主界面就绪。

- [ ] **workspace 路径**。DevTools console：`await window.darvin.getWorkspaceInfo()` → `{ workspaceBytes: 0 }`（**不**含 `workspaceRoot`，v2 修订）。`fs.lstat(<userData>/workspaces/<sid>)` 看到目录。
- [ ] **realRoot 透明性**。`<workspaceRoot>/leak → /etc/passwd`，`read_file { path: "leak" }` → `path "leak" escapes sandbox via symlink (resolves to "/etc/passwd" outside root "<workspaceRoot>")`。macOS `userData` 是 `/Library/Application Support/darvin-cowork`（非 symlink），不会触发 `/var → /private/var` 误报。
- [ ] **排除清单**。`mkdir <workspaceRoot>/.git && echo a > .git/HEAD`，`list_dir { path: ".git" }` → `path "<...>/.git" matches pattern ".git"`。同样 `node_modules` / `.env`。
- [ ] **大文件 + offset**。`write_file { path: "big.txt", content: <3MB> }` → `read_file { path: "big.txt", offset: 1000000, limit: 1048576 }` → 末尾 `[truncated at offset 1000000, limit 1048576 bytes]`，读到的内容与 `tail -c +1000001 | head -c 1048576` 一致。
- [ ] **maxShellBytes**。`shell { command: "find", args: ["<workspaceRoot>"], max_output_bytes: 65536 }` → `[stdout truncated at 65536 bytes]`。
- [ ] **shell enum 拒**。`shell { command: "rm -rf /" }` → `command "rm -rf /" must be one of [ls cat ...]`。
- [ ] **file import 端到端**。点 paperclip → 选 `~/Documents/spec.md` → 拷进 workspace → chip `spec.md (4.2 KiB)` → prompt「读 spec.md 改标题」→ agent 调 read_file/write_file → terminal `cat <workspaceRoot>/spec.md` 看到修改。
- [ ] **不让 agent 越权**。prompt「读 `/Users/<u>/.ssh/id_rsa`」→ agent 工具返 `ErrPathEscapes`；`lsof -p $(pgrep darvin-agent)` 看不到该路径 open。
- [ ] **删 chip**。点 × → 文件从 workspace 删 → LLM 后续 `read_file` 返 ENOENT → UI chip 消失 → `sessionEvent` 收到 system note（`role: 'system'`，灰色样式）。
- [ ] **session 删清 workspace**（v2 增项）。删当前 session → `<userData>/workspaces/<sid>/` 不再存在。
- [ ] **导入 `.env`**。dialog 选 `.env` → toast「为安全不允许导入敏感凭据」；workspace 不增。
- [ ] **导入 50 / 100 MiB**。50 MiB 成功；100 MiB → toast「文件过大」。

### 7.6 e2e（Playwright，manual）

- [ ] `e2e/file-import.spec.ts::TestImportFileHappyPath`：UI 点 ImportButton → mock dialog → chip → LLM 读 → chip 仍在。
- [ ] `TestImportBinaryFileRejected`：`.png` → toast「文件类型不支持」。
- [ ] `TestImportTooLargeFileRejected`：60 MiB → toast。
- [ ] `TestRemoveImportedFile`：× → 文件删 → LLM 读 ENOENT → chip 消失。
- [ ] `TestWorkspaceInfo`：onWorkspaceChanged push 触发，payload 含 files。
- [ ] **回归**：`e2e/happy-path.spec.ts` / `e2e/sessions.spec.ts` 不破坏。

## 8. 落地前置条件（必须先满足）

1. **vitest 落地**。AGENTS.md §测试 标注「当前仓库尚未配置单元测试运行器——`package.json` 没有 `test` 脚本」。本 spec FR-5 / FR-4 / FR-3 的单测全部依赖 vitest。**前置任务**：
   - `npm install -D vitest @vitest/coverage-v8`
   - `package.json` 加 `"test": "vitest run"`
   - vitest config（`vitest.config.ts`）排除 `out/` / `.vite/build/` / `node_modules/`
   - 首次跑 `npm test` 现有测试都通过（占位）

2. **Go module 版本**。`openRootFileLimited` 用 `*float64` / `*int` 指针语义依赖 Go 1.21+；`min` builtin 同。检查 `src/darvin-agent/go.mod` 的 `go` directive ≥ 1.21，必要时 bump。

3. **图标 `file-text.svg`**。`paperclip.svg` / `x.svg` 已存在；`file-text.svg` 按 AGENTS.md 图标规范新建（viewBox="0 0 34 34" + stroke="currentColor" + stroke-width="2.4" + round caps）。

4. **GORM AutoMigrate 顺序**。`&store.ImportedFile{}` 加在 `&store.AppState{}` 之后；既有 `database.AutoMigrate` 调用一次过即可。

5. **JSON-RPC method 注册表扩展**。`internal/gateway/jsonrpc.go` 现有 method 注册方式（如 `methods := map[string]Method{...}`）必须支持动态添加；检查后落地。

## 9. review 修订记录（v1 → v2）

| # | 原 v1 问题 | v2 处理 |
|---|-----------|---------|
| 1 | `src/main/store/SessionStore.ts` 不存在（误描述） | `imported_files` 表落到 Go 端 GORM 模型（`internal/agent/store/models.go`）+ `ImportedFileStore` + AutoMigrate 注册 |
| 2 | `injectSystemNote` 走不存在的 `store.saveMessage` | 新增 JSON-RPC `agent.save_message`；role 派生（`meta.tag: 'workspace_event'` → `role: 'system'`）；main 端零 DB 依赖 |
| 3 | `openRootFileLimited` 丢 `offset`（回归 bug） | helper 签名补 `offset int64`；truncation 文案改 `[truncated at offset N, limit M bytes]` |
| 4 | §7.3 smoke 用 `/tmp`（与 FR-5 工作目录冲突） | smoke 全部按 `<workspaceRoot>` 重写；新增 v2 增项（`TestDeleteSessionCleansWorkspace` / workspace info 不透传 path） |
| 5 | `Pattern: "^[a-z0-9_-]+$"` + `Enum` 冗余且过严 | 删 Pattern，Enum 单独约束 |
| 6 | `src/main/libs/workspace.ts` 与现有 `user-paths.ts` 风格冲突 | workspace helper 并入 `user-paths.ts`，不开新文件 |
| 7 | session 删除时 workspace 残留（孤儿文件累积） | `darvin:delete_session` 同步 `fs.rm(root, { recursive: true, force: true })` |
| 8 | `SumBytes` + `Insert` 非原子，并发绕过 | `ImportedFileStore.Insert` 用 `db.Transaction(...)`：先 `SumBytes` 判容量、再 `Create`；并发安全 |
| 9 | 缺 `test` 脚本但 spec 大量依赖单测 | §8 列出 vitest 落地为前置条件 |
| 10 | 缺 `file-text.svg` | §4.1 / §6 列图标新增 |
| 11 | `workspaceRoot` 经 IPC 透传给 renderer | v2 收紧：不透传；renderer 只能看 `workspaceBytes` 数字 |
| 12 | Go 端启动期无 workspace 来源可观测性 | FR-5.12 / §4.1 加 `log.Info("workspace resolved", env, effective)` |
| 13 | `edit_file` `old_text` / `new_text` 无 size 上限 | FR-4 增项：`MaxLength: int(maxHardWriteBytes)` |
| 14 | `validateArgs` 旧行为被误描述为 warn-but-not-reject | FR-4 明确「保留硬拒 unknown args」 |

## 10. 范围外（不做）

- 不引入 `gojsonschema` / `doublestar` 等三方库。
- 不开放 `config.yaml` 自定义 `path_exclusions` / `max_bytes` / `max_output_bytes`。
- 不做 `O_NOFOLLOW` / `O_SYMLINK` 等 OS-level flag（跨平台不一致，靠 `EvalSymlinks`-first 等价保证）。
- 不做 macOS sandbox-exec / Linux seccomp / Windows AppContainer（Tier 2+）。
- 不动 `internal/agent/agent.go` / `internal/agent/dispatcher.go`（无业务逻辑变更）。
- 不改 `internal/acp/*`（已有 spec 覆盖）。