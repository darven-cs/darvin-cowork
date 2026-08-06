# Skills & MCP 内置清理 + Loader v2 设计文档

## 1. 概述

### 1.1 问题 / 背景

`darvin-agent` 当前随二进制打包了：

- **1 个内置 MCP server** `filesystem`（`cmd/app/mcp_filesystem.go`，暴露 `list_directory` / `read_file` / `write_file` 3 个工具）；通过子命令 `darvin-agent mcp-filesystem` 走 stdio JSON-RPC 启动。
- **5 个 bundled skills**（`cmd/app/resources/skills-bundled/{api-design,code-review,docx,testing,web-search}/SKILL.md`）；通过 `//go:embed resources/skills-bundled` 嵌进二进制。

当前 `internal/skills/loader.go` 的 `UserSource` 用 `filepath.WalkDir` 平铺找 `SKILL.md`，**不区分**目录式 vs 平铺式布局，**不解析** `references/` 或 `scripts/` 同级增强，**只支持** 4 个 frontmatter 字段（`name` / `description` / `version` / `invocation`）。

参考 [`DeepSeek-Reasonix-main-v2/internal/skill/skill.go`](file:///home/darven/桌面/github-project/DeepSeek-Reasonix-main-v2/internal/skill/skill.go) 的实现，目录式 skill 发现 + 同级增强（`references/*.md` 拼 body + `scripts/*` 列表）+ 16+ 字段 frontmatter 是事实标准。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 删除内置 `filesystem` MCP server 与 5 个 bundled skills | 二进制不再打包 `mcp_filesystem.go` 与 `resources/skills-bundled/` |
| G2 | loader 区分目录式 `<name>/SKILL.md` 与平铺式 `<name>.md`（Claude 兼容） | 新单测覆盖两种布局 |
| G3 | loader 自动把 `references/*.md` 拼到 body 末尾，`scripts/*` 列表追加到 body | 新单测覆盖 |
| G4 | skills 多 source：project scope（`<workdir>/skills/`）+ global scope（`<UserConfigDir>/darvin-cowork/darvin-agent/skills/`） | 新单测 + bootstrap 日志显示两 source |
| G5 | frontmatter 扩到 16+ 字段，对齐 DeepSeek 解析面 | 单测覆盖每个字段解析 |
| G6 | 移除 `IsBuiltIn` / `IsOfficial` 字段（不再使用） | 仓库内 0 引用，grep 验证 |

### 1.3 非目标

- **不做 zip/tar.gz 解压导入**：用户在回答中明确否决；skills 仅通过"复制/链接目录到 global 路径"导入。
- **不做 marketplace / GitHub release 拉取**：本期只解决本地发现。
- **不做 UI 改动**：spec 33 的 renderer 视图不动；`SkillSummaryWire.IsBuiltIn` 字段一并下线，UI 侧自行调整。
- **不做 runner 改造**：保留现有 `SkillRunner`，即使 `runAs` / `allowed-tools` 等字段被解析但**不被消费**，等下一期接 runner。

---

## 2. 用户场景

### 场景 1：用户首次启动，没有放任何 skill

**Given** `<UserConfigDir>/darvin-cowork/darvin-agent/skills/` 不存在，`<workdir>/skills/` 也不存在
**When** darvin-agent 启动，调用 `skills.Bootstrap(...)`
**Then** 注册表为空，bootstrap 日志输出 `total=0`，agent 启动不受影响

### 场景 2：用户在全局目录放一个目录式 skill

**Given** `~/.config/darvin-cowork/darvin-agent/skills/code-review/SKILL.md` 存在（含 frontmatter `name: code-review`）
**When** 同上
**Then** 注册表含 1 个 entry，`Source=global`、`IsBuiltIn=false`；该 skill 可被 `run_skill` / `/<name>` 触发

### 场景 3：用户在全局目录放一个带 references 的 skill

**Given** `~/.config/.../skills/api-design/SKILL.md` + `~/.config/.../skills/api-design/references/rest.md` + `~/.config/.../skills/api-design/scripts/check.sh`
**When** 加载
**Then** entry 的 `Prompt` = SKILL.md 正文 + `\n\n## Reference: rest\n\n<rest.md content>` + `\n\n## Scripts\n\n- <.../scripts/check.sh>\n`

### 场景 4：project scope 与 global scope 同名冲突

**Given** `<workdir>/skills/foo/SKILL.md`（project）与 `<UserConfigDir>/.../skills/foo/SKILL.md`（global）都存在
**When** 加载
**Then** 注册表只保留 1 个 entry，`Source=project`（project 优先）

### 场景 5：用户从 Anthropic Skills 仓库拖一个 Claude 兼容的 skill

**Given** `~/.claude/skills/build-pdf/SKILL.md`（**注意**：Claude 是平铺式，不一定有目录）
**When** 加载（**条件**：当前仅当根是 Claude 兼容根且 flat 文件带 skill frontmatter marker 时认）
**Then** 项目根 `~/.config/.../skills/` 不识别 `<name>.md` 平铺式（避免把任意 `.md` 当 skill）；仍按目录式加载

> 注：Claude 兼容平铺式 `<name>.md` 是否纳入 v2 范围，**用户待确认**（见 §5 决策点 D1）。

### 场景 6：用户跑 `--show-skills` / 启动后看到列表

**Given** 上述场景 4 的目录
**When** agent.skills.changed 事件广播
**Then** renderer 收到 `Skills[]`，每个含 `source: "global" | "project"`、`path`、`prompt` 截断预览

---

## 3. 功能需求

### FR-1：删除内置 MCP `filesystem`

- 删除文件 `cmd/app/mcp_filesystem.go`（412 行）+ `cmd/app/mcp_filesystem_test.go`
- 删除 `cmd/app/main.go:56-64` 的 `mcp-filesystem` subcommand 分支
- 删除 `cmd/app/main.go:262-291` 的 `mcpRoot := ...` 创建 + `mcpRegistry.Register(... filesystem, IsBuiltIn: true ...)` 块
- 保留 `mcp.ServerSpec.IsBuiltIn` 与 `McpServerWire.IsBuiltIn` 字段（未来 plugin package 复用）

### FR-2：删除 bundled skills

- 删除目录 `cmd/app/resources/skills-bundled/`（5 个 skill 全部）
- 删除 `cmd/app/main.go:34-35` 的 `//go:embed resources/skills-bundled` 与 `var skillsBundled embed.FS`
- 删除 `cmd/app/main.go:237-241` `Bootstrap(... Bundle: skills.Bundle{...})` 中的 `Bundle` 字段
- 同步删除 `embed` import（如果只剩这一个用到）

### FR-3：清理 `IsBuiltIn` / `IsOfficial` 字段

- `internal/skills/types.go`：`SkillEntry.IsBuiltIn` / `SkillEntry.IsOfficial` 字段删除
- `internal/skills/loader.go:118-119`：不再设置这两个字段
- `internal/skills/wire.go`：`SkillSummaryWire.IsBuiltIn` / `IsOfficial` 字段删除
- 同步删除 `loader_test.go` / `wire.go` / 其它测试中引用这两字段的断言
- 保留 `internal/mcp/types.go:154` 的 `ServerSpec.IsBuiltIn`（未来用）
- 保留 `internal/gateway/handlers.go:1481` 的 `McpServerWire.IsBuiltIn`（同上）

### FR-4：loader 重写对齐 DeepSeek

- `internal/skills/loader.go` 重写 `UserSource.LoadAll` 为 `scanDir`-style：
  - 入口：`<root>/`
  - 接收 `requireFlatMarker bool`：仅在 `.claude/` 等 Claude 兼容根下为 true
  - 目录式：`<name>/SKILL.md` → 加载该 skill
  - 平铺式：`<name>.md`（仅 `requireFlatMarker=true` 时）→ 检查 frontmatter marker，存在则加载
  - 跳过：`.`-前缀、`assets` / `node_modules` / `references` / `scripts` 子目录
  - 最大深度：3（默认）/ 5（clamp）
  - symlink：os.Stat 跟随（读），scanDir 用 EvalSymlinks 去重
- 新增 `internal/skills/body_extras.go`：
  - `loadBodyWithReferences(skillPath, body string) string`：`references/*.md` 升序拼到 body，每段前缀 `## Reference: <slug>`
  - `loadBodyWithScripts(skillPath, body string) string`：`scripts/<file>` 列表追加到 body，扩展名过滤 `.sh/.py/.js/.ts/.rb/.pl/.php/.ps1`，跳过 dotfile
- `internal/skills/loader.go` 的 `loadFileSkill` 末尾改为：
  ```go
  body := loadBodyWithReferences(path, rawBody)
  body = loadBodyWithScripts(path, body)
  ```
- 保留 `BundledSource` / `loadEmbeddedSkills`（plumbing 留着，未来回滚时复用）

### FR-5：frontmatter 扩展 16 字段

`internal/skills/frontmatter.go` 增加字段解析：

| 字段 | 类型 | 默认 | 备注 |
|---|---|---|---|
| `name` | string | (必填) | 现有 |
| `description` | string | (必填) | 现有 |
| `version` | string | "" | 现有 |
| `runAs` | string | "inline" | `inline` / `subagent` |
| `context` | string | "" | `fork` → runAs=subagent |
| `agent` | string | "" | 非空 → runAs=subagent |
| `allowed-tools` | string (CSV) | nil | 解析后追加 `use_capability` |
| `model` | string | "" | Claude 别名 `sonnet`/`opus`/`haiku`/`inherit` 不解析 |
| `effort` | string | "" | 自由字符串 |
| `read-only` | bool | false | `true`/`yes`/`1`/`on` |
| `triggers` | string (CSV) | nil | |
| `negative-triggers` | string (CSV) | nil | |
| `auto-use` | string | "off" | `off` / `suggest` / `prefer` / `require` |
| `needs-fresh-data` | bool | false | |
| `cost` | string | "" | `low` / `medium` / `high` |
| `color` | string | "" | UI 标记 |
| `invocation` | string | "auto" | `auto` / `manual` |
| `requires` | string (CSV) | nil | capability ID |
| `profiles` | string (CSV) | nil | `economy` / `balanced` / `delivery` |

未知字段：忽略 + warn 日志（沿用现有 `ParseFrontmatter` 行为）。

### FR-6：`SkillEntry` 字段扩展

`internal/skills/types.go` 增加：

```go
type SkillEntry struct {
    ID, Name, Description, Version, Path, Prompt string
    Source SkillSource
    Enabled bool
    UserInvocable, DisableModelInvocation bool
    RiskLevel SecurityRiskLevel
    RiskScore int
    Findings []SecurityFinding
    LoadedAt time.Time

    // v2 新增
    RunAs            string   // "inline" | "subagent"
    AllowedTools     []string
    Model            string
    Effort           string
    ReadOnly         bool
    Color            string
    Invocation       string   // "auto" | "manual"
    Triggers         []string
    NegativeTriggers []string
    AutoUse          string   // "off" | "suggest" | "prefer" | "require"
    NeedsFreshData   bool
    Cost             string   // "low" | "medium" | "high"
    Requires         []string
    Profiles         []string
    InvalidProfiles  []string
}
```

**注意**：新增字段在本期**只解析、不消费**（runner 不变）。`SkillRunner.Execute` 当前只读 `Prompt`，新字段透传到 entry 上等下一期接。

### FR-7：bootstrap 多 source

`internal/skills/bootstrap.go` 的 `BootstrapConfig` 增加 `GlobalConfigDir string`：

```go
type BootstrapConfig struct {
    Bundle          Bundle
    ProjectSkillsDir string  // 现有 UserDataDir/skills/ 改名澄清
    GlobalConfigDir  string  // 新增
    ToolReg          *tool.Registry
}
```

`Bootstrap` 加载顺序：

1. project source（`<ProjectSkillsDir>`，`Source=project`，**最高优先级**）
2. global source（`<GlobalConfigDir>/darwin-cowork/darwin-agent/skills/`，`Source=global`）

dedup：`SkillRegistry.Load` 已经按 `merged[entry.ID] = entry` 实现，**先填的胜出**；所以遍历顺序 = 优先级顺序（project 先，global 后）。

`<GlobalConfigDir>` 计算：

```go
import "os"
func defaultGlobalConfigDir() string {
    base, err := os.UserConfigDir()
    if err != nil || base == "" {
        return ""
    }
    return filepath.Join(base, "darvin-cowork", "darvin-agent")
}
```

跨平台：

| OS | 实际路径 |
|---|---|
| Linux | `~/.config/darvin-cowork/darvin-agent/` |
| macOS | `~/Library/Application Support/darvin-cowork/darvin-agent/` |
| Windows | `%APPDATA%\darvin-cowork\darvin-agent\`（通常是 `C:\Users\<u>\AppData\Roaming\darvin-cowork\darvin-agent\`） |

主进程 `cmd/app/main.go` 注入：

```go
globalDir, _ := os.UserConfigDir()
skillsResult := skills.Bootstrap(rootCtx, log.Logger, skills.BootstrapConfig{
    ProjectSkillsDir: filepath.Join(effectiveWorkdir, "skills"),
    GlobalConfigDir:  filepath.Join(globalDir, "darvin-cowork", "darvin-agent"),
    ToolReg:          toolsReg,
})
```

`os.UserConfigDir()` 失败时 fallback 到 `effectiveWorkdir/skills/`。

---

## 4. 实现方案

### 4.1 删除（FR-1 + FR-2 + FR-3）

按文件清单执行 `rm` / `Edit`：

| 操作 | 文件 |
|---|---|
| rm | `cmd/app/mcp_filesystem.go` |
| rm | `cmd/app/mcp_filesystem_test.go` |
| rm -r | `cmd/app/resources/skills-bundled/` |
| Edit | `cmd/app/main.go`（4 处：embed 块、subcommand 分支、mcp-filesystem register 块、Bootstrap 调用） |

### 4.2 loader 重写（FR-4）

新结构（`internal/skills/loader.go`）：

```
UserSource.LoadAll(ctx)
  → for each entry in os.ReadDir(root):
      if is skill dir:    readEntryDir
      if is skill file:   readEntryFlat (only if requireFlatMarker)
      else if is subdir:  scanDir(root/<sub>, depth+1, seen)

readEntryDir(full, name)
  → os.Stat(full/SKILL.md) 必须存在
  → loadFileSkill(full/SKILL.md, source=user, builtIn=false, official=false)

readEntryFlat(full, stem)
  → loadFileSkill(full, source=user, ...) 但要求 frontmatter marker 存在

loadFileSkill(...)
  → ParseFrontmatter
  → ScanSkill(rootDir)
  → loadBodyWithReferences → loadBodyWithScripts
  → SkillEntry{...v2 fields}
```

### 4.3 frontmatter 解析（FR-5）

`internal/skills/frontmatter.go` 改为：

```go
type Frontmatter struct {
    Name             string   `yaml:"name"`
    Description      string   `yaml:"description"`
    Version          string   `yaml:"version"`
    RunAs            string   `yaml:"runAs"`
    Context          string   `yaml:"context"`
    Agent            string   `yaml:"agent"`
    AllowedTools     string   `yaml:"allowed-tools"`
    Model            string   `yaml:"model"`
    Effort           string   `yaml:"effort"`
    ReadOnly         bool     `yaml:"read-only"`
    Triggers         string   `yaml:"triggers"`
    NegativeTriggers string   `yaml:"negative-triggers"`
    AutoUse          string   `yaml:"auto-use"`
    NeedsFreshData   bool     `yaml:"needs-fresh-data"`
    Cost             string   `yaml:"cost"`
    Color            string   `yaml:"color"`
    Invocation       string   `yaml:"invocation"`
    Requires         string   `yaml:"requires"`
    Profiles         string   `yaml:"profiles"`
}
```

新增解析函数（参考 DeepSeek 命名）：

- `parseRunAs(runAs, context, agent string) string`：优先 runAs，其次 `context=fork`，最后 `agent!=`"" → subagent
- `parseCSVFrontmatter(raw string) []string`：逗号 / 方括号列表解析
- `parseBoolFrontmatter(raw string) bool`：`true`/`yes`/`1`/`on` → true
- `parseAutoUse(raw) string`：白名单
- `parseCost(raw) string`：白名单
- `parseInvocation(raw) string`：`manual` → manual，其它 → auto
- `parseProfilesFrontmatter(raw) (valid, invalid []string)`：保留未识别值给 doctor

### 4.4 bootstrap 注入（FR-7）

`internal/skills/bootstrap.go`：

```go
type BootstrapConfig struct {
    Bundle           Bundle
    ProjectSkillsDir string
    GlobalConfigDir  string
    ToolReg          *tool.Registry
}

func Bootstrap(...) *BootstrapResult {
    reg := NewSkillRegistry()
    sources := []SkillSourceLoader{}

    // project first → higher priority
    if cfg.ProjectSkillsDir != "" {
        sources = append(sources, &UserSource{
            RootDir:           cfg.ProjectSkillsDir,
            Source:            SkillSourceProject,  // 现有 SkillSource 类型加 Project const
            RequireFlatMarker: false,
        })
    }
    // global second
    if cfg.GlobalConfigDir != "" {
        sources = append(sources, &UserSource{
            RootDir:           filepath.Join(cfg.GlobalConfigDir, "skills"),
            Source:            SkillSourceGlobal,
            RequireFlatMarker: false,
        })
    }
    // bundled last (plumbing 在, 但 cfg.Bundle.FS 永远 zero value)
    if cfg.Bundle.FS != (embed.FS{}) {
        sources = append(sources, &BundledSource{...})
    }
    reg.Load(ctx, sources)
    ...
}
```

`internal/skills/types.go` 增加：

```go
const (
    SkillSourceBundled SkillSource = "bundled"  // 保留
    SkillSourceProject SkillSource = "project"  // 新
    SkillSourceGlobal  SkillSource = "global"   // 新（替代原 "user"）
    SkillSourceGitHub  SkillSource = "github"   // 保留
    SkillSourceNPM     SkillSource = "npm"      // 保留
)
```

**注意**：原 `SkillSourceUser` 被替换为 `SkillSourceProject` + `SkillSourceGlobal`（语义更清晰）。`wire.go` 与现有引用要同步更新字符串值。

### 4.5 测试更新

| 文件 | 变更 |
|---|---|
| `loader_test.go` | 重写所有 `LoadAll` 测试用 tempdir + 目录式布局；新增 `loadBodyWithReferences` / `loadBodyWithScripts` 单测 |
| `frontmatter_test.go` | 每个新字段加单测 |
| `bootstrap_test.go` | 新增：Project 优先、Global 不冲突、Project+Global 同名冲突 |
| `wire_test.go`（如有） | 删除 `IsBuiltIn` / `IsOfficial` 断言 |
| `registry_test.go` | dedup 行为测试 |
| `plugin_test.go` | `SkillTool` 行为测试 |

---

## 5. 决策点（待用户拍板）

| # | 决策 | 选项 | 建议 |
|---|---|---|---|
| D1 | 是否在 v2 支持 Claude 兼容平铺式 `<name>.md`？ | A. 不支持（**建议**：本仓库没有 `.claude/` 根，平铺式没有用） / B. 支持（FR-4 已写） | A |
| D2 | `SkillSourceUser` 字符串值改名为 `Project` + `Global`？ | A. 是（**建议**：更清晰，wire 升级一次） / B. 否（保留 `user` 含义兼容旧 wire） | A |
| D3 | `UserSource` 改名为 `DirectorySource`？ | A. 是（**建议**：不只有 user 用了）/ B. 否（保最小改动） | B |
| D4 | `Body` 增强（references/scripts）是否本期上？ | A. 上（**建议**：与 DeepSeek 对齐）/ B. 推到下一期 | A |

> 用户在回答中已隐含确认 D4=A（"extra 有 references templates 等 这个需要参考怎么做的"）；其余 D1/D2/D3 待确认。

---

## 6. 边界情况

| 场景 | 处理方式 |
|---|---|
| `<UserConfigDir>` 不可写 / 不存在 | bootstrap 日志 warn，`GlobalConfigDir=""`，只加载 project |
| SKILL.md > 256 KB | 现有 maxSkillFileSize 限制，跳过 + warn |
| SKILL.md 前置 BOM / CRLF | `ParseFrontmatter` 现有清洗逻辑处理 |
| frontmatter 解析失败 | 跳过该 skill + warn，其它继续 |
| 同名 project vs global | project 胜出（遍历顺序保证） |
| 软链循环 | `EvalSymlinks` dedup，`seen` map 防爆栈 |
| 软链指向 scope 外 | 读时跟随（DeepSeek 行为），写时由后续 `Create` / `Delete` 拒绝（FR 后续接） |
| `references/*.md` 是 binary | UTF-8 解码失败 → 跳过该文件，warn |
| `scripts/` 含 dotfile | 跳过（不列在 body 里） |
| `scripts/` 含 `.exe` / `.bat` | 当前不在白名单，不列；下期按需扩展 |
| archive 文件（`*.zip` / `*.tar.gz`）在根目录 | **不识别**（用户已否决解压导入） |

---

## 7. 涉及文件

### 删除

| 文件 | 说明 |
|---|---|
| `cmd/app/mcp_filesystem.go` | 内置 MCP server |
| `cmd/app/mcp_filesystem_test.go` | 内置 MCP 单测 |
| `cmd/app/resources/skills-bundled/` | 5 个 bundled skill |

### 修改

| 文件 | 变更说明 |
|---|---|
| `cmd/app/main.go` | 删 embed、subcommand、register、Bundle；加 `os.UserConfigDir` 注入；改名 `UserDataDir` → `ProjectSkillsDir` |
| `internal/skills/types.go` | 加 `SkillSourceProject` / `SkillSourceGlobal`；删 `SkillSourceUser` / `IsBuiltIn` / `IsOfficial`；扩 `SkillEntry` 字段 |
| `internal/skills/frontmatter.go` | 扩 `Frontmatter` struct（16 字段）+ 7 个 `parse*` 函数 |
| `internal/skills/loader.go` | 重写 `UserSource.LoadAll` 为 `scanDir`；删 `BundledSource` 调用路径（保留类型） |
| `internal/skills/body_extras.go` | **新增**：`loadBodyWithReferences` / `loadBodyWithScripts` |
| `internal/skills/bootstrap.go` | `BootstrapConfig` 加 `GlobalConfigDir`；遍历顺序 project → global |
| `internal/skills/wire.go` | 删 `IsBuiltIn` / `IsOfficial` 字段 |
| `internal/skills/loader_test.go` | 重写测试 |
| `internal/skills/frontmatter_test.go` | 扩字段单测 |
| `internal/skills/bootstrap_test.go` | 加 global source 单测 |
| `internal/skills/runner_test.go` | `ToolStub` 不需要改 |

### 不动

| 文件 | 原因 |
|---|---|
| `internal/skills/scanner.go` | 安全扫描独立，本期不动 |
| `internal/skills/registry.go` | dedup 行为已经正确 |
| `internal/skills/runner.go` | 本期新字段只解析不消费 |
| `internal/mcp/types.go` | `ServerSpec.IsBuiltIn` 字段保留 |
| `internal/gateway/handlers.go` | `McpServerWire.IsBuiltIn` 字段保留 |

---

## 8. 验收标准

- [ ] `go build ./...` 通过；`go vet ./...` 无新增 warning
- [ ] `go test ./internal/skills/...` 全绿（含新加单测）
- [ ] `go test ./cmd/app/...` 全绿（mcp-filesystem 测试已删）
- [ ] `go test ./...` 全绿
- [ ] `ls bin/` 后 `nm` 验证 `darvin-agent` 二进制不含 `darvin-filesystem` 符号、不含 5 个 bundled skill 的 frontmatter 字符串
- [ ] `ls cmd/app/resources/` 只剩 `.gitkeep`（如有）
- [ ] 手动验证场景 2：放 `~/.config/darvin-cowork/darvin-agent/skills/test/SKILL.md`，启动看日志 `loaded global=1 total=1`
- [ ] 手动验证场景 4：project + global 同名 → 日志 `project=1 global=0`（被 dedup）
- [ ] 手动验证场景 3：放 `references/` + `scripts/` → body 末尾能看到 `## Reference: ...` 与 `## Scripts` 段
- [ ] 手动验证 `grep -r "IsBuiltIn\|IsOfficial" internal/skills/` 返回 0 行
- [ ] `git grep "SkillSourceUser"` 返回 0 行（已替换）
- [ ] 跑 `go test -run TestMcpFilesystem -v ./...` 找不到测试（已删）

---

## 9. 实施步骤建议（仅顺序参考）

1. **Step 1：删内置 MCP**
   - rm `cmd/app/mcp_filesystem*.go`
   - Edit `cmd/app/main.go`（subcommand 分支 + register 块）
   - 跑 `go build ./...` 确认通过

2. **Step 2：删内置 skills**
   - rm `cmd/app/resources/skills-bundled/`
   - Edit `cmd/app/main.go`（embed + Bundle）
   - 跑 `go build ./...` 确认通过

3. **Step 3：清理 IsBuiltIn/IsOfficial**
   - Edit `types.go` / `wire.go` / `loader.go` / 引用处的测试
   - 跑 `go test ./internal/skills/...` 通过

4. **Step 4：loader 重写**
   - 新增 `body_extras.go`
   - 重写 `UserSource.LoadAll`
   - 重写 `loader_test.go`

5. **Step 5：frontmatter 16 字段**
   - 扩 `frontmatter.go`
   - 新增 `parse*` 函数
   - 扩 `types.go:SkillEntry` 字段
   - 跑 `frontmatter_test.go`

6. **Step 6：bootstrap 多 source**
   - Edit `bootstrap.go`（加 `GlobalConfigDir` + 遍历顺序）
   - Edit `cmd/app/main.go`（注入 global path）
   - 加 `bootstrap_test.go`

7. **Step 7：全量回归**
   - `go test ./...`
   - 手动验证 §2 场景 2 / 3 / 4

> 每 step 跑完测试再进下一步；不允许连续两 step 失败还不回滚。

---

## 10. 代码风格遵循（AGENTS.md §注释规范）

本节是硬约束，落地 Step 1–7 时必须遵守。规则源：`AGENTS.md` §注释规范。

### 10.1 绝对禁止出现的注释

落地过程中**不允许**写出以下注释，发现即违规：

- **阶段 / 版本 / 迭代规划类**：`// v2 实现` / `// S1 占位` / `// 后续替换此逻辑` / `// 未来会接 runner`
- **代码复述型废话**：代码已经写了 `if !enabled { return }`，不许加 `// 如果未启用就返回`
- **模型思考 / 编写过程说明**：`// 按照规范调整写法` / `// 适配项目架构修改逻辑` / `// 引用 DeepSeek 改造`
- **大范围 TODO 规划**：禁止罗列后续开发路线、架构演进内容
- **首尾铺垫 / 收尾总结**：`// 下面实现 X 逻辑` / `// 以上完成 X 封装`
- **冗余分隔线**：禁止 `// ---------` 分割代码区块

### 10.2 仅允许出现的注释场景

- **导出公共函数 / 类型 / 类**：JSDoc / godoc，**只标注**入参含义、返回值、边界约束、业务不变量；不写 `@example` 等冗余标签
- **非常规特殊逻辑**：单行意图注释，**只解释为什么这么写**，不重复代码做了什么
- **硬性架构约束校验**：例如 `// entry 字段对齐 SkillSummaryWire；删字段时同步删 wire`（删除链路提示）
- **关键边界兜底**：例如 symlink EvalSymlinks 失败时的退化逻辑一句说明

### 10.3 注释格式

- 单行 `//`（空格分隔），放在代码上方；**不写行内注释**
- Vue `<template>` 内禁止 HTML 注释（本期不涉及 Vue）
- godoc 段落用完整英文/中文短句，不写"参数说明：xxx"这种列表模板

### 10.4 标识符命名约束

- **禁止把版本号塞进 API 名字**：`ErrNotImplementedInV0` / `FixForV2` / `MockS2` 全部禁用
- 本期新增的 export，命名必须是「意图清晰 + 无版本前缀」
- `SkillSourceUser` → `SkillSourceProject` / `SkillSourceGlobal` 是字符串值变更，不是 API 名变更（合规）

### 10.5 删除残留清理

- 删 `cmd/app/mcp_filesystem.go` 时**整文件删除**，不留任何 `// removed in v2` 之类的 ghost 注释
- 删 `cmd/app/resources/skills-bundled/` 时**整目录删除**，里面 5 个 SKILL.md 一起清，不留只含注释的空文件
- 改 `cmd/app/main.go` 时，凡是删除的内置相关代码块，**顺手清掉对应的注释**（包括 doc.go 顶部的"bundled filesystem MCP subcommand"那段 7 行说明）

### 10.6 验收补充

新增到 §8 验收标准的检查项：

- [ ] `git grep -nE '// (v[0-9]|S[0-9]|TODO|FIXME|XXX|HACK) ' src/darvin-agent/` 在改动文件内**无新增命中**
- [ ] `git grep -nE '// (removed|deleted) in v2' src/darvin-agent/` **0 命中**
- [ ] `git grep -nE '\b(.*v[0-9]|.*S[0-9]|FixForV2|MockS2)\b' src/darvin-agent/internal/skills/ src/darvin-agent/cmd/app/` **无新增命名命中**
- [ ] 删 `cmd/app/mcp_filesystem.go` 后 `go vet ./cmd/app/` 无遗留引用警告

> 落地过程中如果出现"忍不住想加一段规划性注释"的冲动，回到 §10.1 自检；如不确定，**不写**。