# 内置工具补齐 E 组:code_index 设计文档

## 1. 概述

### 1.1 问题 / 动机

darvin-agent 当前已有 `grep` (字符串级正则) 和 `delete_symbol` (Go AST 删除),但缺一个**符号级查询**工具。LLM 想做"找一下这个项目所有 `UserService` 的定义"、"列一下 `agent.go` 里有什么符号"这类任务时,只能 grep 出 N 条假阳性(字符串 / 注释 / 引用)后自己脑补过滤;大项目遍历慢且不可靠。

参考项目 DeepSeek-Reasonix 的 `code_index` 工具 (`internal/tool/builtin/codeindex.go`) 提供三个动作:

- `outline` — 单文件大纲
- `search` — 跨文件按符号名查
- `info` — 单符号细节

darvin-agent 已有 `delete_symbol.go` 的 `go/parser` + `go/ast` 基础,可以低成本复用,且新增的"按声明查询"对 LLM 是高频刚需。

### 1.2 目标

1. 新增 `code_index` 内置工具,3 个 action:`outline` / `search` / `info`。
2. 复用 `delete_symbol.go` 的 AST 解析入口,把 `parser.ParseFile + token.NewFileSet` 抽成共享 helper,避免重复逻辑。
3. 内存索引:首次访问某路径 lazy build;`multi_edit` / `write_file` / `delete_symbol` / `edit_file` / `move_file` 写 `.go` 后增量失效。
4. 本期只支持 `.go`(Reasonix 的 tree-sitter 多语言支持留作后续)。
5. 与 `grep` 同级只读权限:`ClassifyPermission` safe,`pathEscape` 不新增分支(单文件经 `ResolveRead`,目录 walk 经 `Resolve`)。

### 1.3 非目标

- 不支持 `.ts` / `.py` / `.rs` / `.java` 等(后续可加 tree-sitter)。
- 不持久化索引到磁盘(workspace 进程重启后重扫;大项目首次冷启动时间略增,实测 < 2 s 暂可接受)。
- 不实现语义搜索 / 跨语言符号解析(LSP 不在本 spec 范围)。
- 不引入新依赖。

## 2. 现状分析

| 现有设施 | 复用方式 |
|---|---|
| `delete_symbol.go` 的 `parser.ParseFile + token.NewFileSet` | code_index 复用同一解析入口;新增 `outlineDecl` 把 AST 节点压平为 `[]SymbolRef` |
| `Sandbox.IsExcluded(abs)` | 索引扫描时跳过 `.git` / `node_modules` / `.venv` 等 |
| `Sandbox.Resolve / ResolveRead` | `outline` / `info` 时校验单文件;`search` 时校验基目录 |
| `Sandbox.SetRoot` 触发的 re-anchor | 索引整体清空(`SetRoot` 调用处加 `codeIndex.Clear()`) |
| `BuiltinConfig{Sandbox, Allowlist}` | 增加可选字段 `CodeIndex codeIndexInvalidator`,写工具落 `.go` 后回调 |
| `validateArgs` / `MarshalSchema` | 入参校验与 schema 生成 |
| `grepTool` (search.go) | 输出格式对标 `path:line:text` 风格,LLM 体验一致 |

`grep` 解决的是 "text contains pattern",`code_index` 解决的是 "declarations matching query"——前者返回字符串位置,后者返回结构化符号列表,LLM 拿到直接可用,不再需要二次解析。

参考实现对照 (DeepSeek-Reasonix `codeindex.go`):

- 全语言支持(Go 用 go/parser,其他用 tree-sitter)
- 进程级单例索引,workspace 改变时整体重建
- `search` 支持 substring / regex / exact 三种匹配模式

darvin-agent 本期仅 Go,索引结构与 Reasonix 一致但实现更简单;`search` 只做 substring(大小写不敏感)+ `kind` 过滤,后续可加 regex。

## 3. 方案设计

### 3.0 文件组织 (F2 按业务域)

| 新文件 | 域 | 内容 |
|---|---|---|
| `code_index.go` | Go 符号索引与查询 | `codeIndex` 单例 + `codeIndexTool` 3 个 action + 注册 |

`delete_symbol.go` 抽 1 个 helper(`parseGoFile`)暴露到包内供 `code_index.go` 调用,避免重复解析器逻辑。

预计 `code_index.go` 约 300 行,`delete_symbol.go` 减 5 行,均 < 800 (F1)。

### 3.1 索引数据结构

```go
type codeIndex struct {
    mu      sync.RWMutex
    byFile  map[string][]SymbolRef     // path → 该文件全部符号(供 outline)
    byName  map[string][]SymbolRef     // symbol_name(小写)→ 全部声明点(供 search)
    built   map[string]bool            // path → 是否已 parse 过
}

type SymbolRef struct {
    Path     string // workspace-relative
    Name     string // 符号名(原样大小写)
    Kind     string // func / method / type / const / var
    Receiver string // method 时为接收者类型名(供 info 用);其余为空
    Pkg      string // 包名(供 info 用)
    Line     int    // 声明起始行(1-based)
    EndLine  int
    Doc      string // doc 注释首段(供 info 用)
}
```

构建策略:

- 首次访问某 path 时若 `built[path]` 为空则 parse + 写入(`outline` / `info` 直接路径,`search` 按需 build 命中的 .go)。
- 目录 search 时 `filepath.WalkDir` 配合 `IsExcluded` 跳过排除目录;每次 parse 写完即更新索引(避免再次访问时重 parse)。
- `CodeIndex.Invalidate(path)` 把指定 path 从 `byFile` / `built` 中摘除;`byName` 中对应项懒清理(下次 search 时若发现 path 已失效则一并剔除)。
- `SetRoot` 调用 `codeIndex.Clear()` 整体清空。

### 3.2 code_index 参数

参数(schema):

- `action`(string,enum: `outline` | `search` | `info`,必填)
- `path`(string,可选):`action=outline` 时必填,workspace-relative 路径,必须 `.go`
- `query`(string,可选):`action=search` 时为符号名 substring(大小写不敏感);`action=info` 时为 `Name` 或 `RecvType.Name`(method),必填
- `kind`(string,enum: `func` | `method` | `type` | `const` | `var`,可选):过滤用,空匹配任意
- `limit`(integer,默认 50,1..500):结果封顶

### 3.3 outline

单文件大纲:

- `sb.ResolveRead(path)` → 必须 `.go` 后缀,否则报错"only .go is indexed"
- 查 `byFile[path]` → 若空,parse 后写入
- 按声明出现顺序输出:

  ```text
  src/darvin-agent/internal/agents/agent.go
    L42  type Agent struct                       // type
    L58  func New(...) *Agent                    // func
    L70  func (a *Agent) Prompt(...)             // method
    L98  func (a *Agent) Run(...)                // method
    L120 func (a *Agent) Abort(...)              // method
  ```

- 空文件 / 无顶层声明返回 `(no symbols)`。
- 受 `ResolveRead` 约束(workspace 内 / granted-reads);workspace 外路径报错"outside authorized roots"。

### 3.4 search

跨文件按符号名查:

- `query` 转小写后 substring 匹配 `byName`,`kind` 过滤。
- 命中按 `path → Line` 排序,按文件分组输出:

  ```text
  src/user/service.go
    L42  type UserService struct                 // type
    L58  func (s *UserService) Create(...)       // method
  src/admin/service.go
    L18  func (s *UserService) Admin(...)        // method
  ```

- 若 `byName` 未命中目标 prefix,转 `filepath.WalkDir` 扫描 workspace 下全部 `.go` 并 build 索引;命中即停,避免全量扫。
- `limit` 封顶,超出截断并注明 `truncated at N matches`。
- 无命中返回 `(no matches)`。
- 跳过 `IsExcluded` 的目录(`.git` / `node_modules` 等)。

权限:只读 safe,与 `grep` 一致。

### 3.5 info

单符号详情:

- `query` 形式:
  - 顶层:`Name`(函数 / 类型 / 常量 / 变量)
  - method:`RecvType.Name`(例:`*Agent.Prompt`,`Agent.Abort` 两者皆可)
- 命中 1 个返回:

  ```text
  package: internal/agents
  file:    src/darvin-agent/internal/agents/agent.go
  kind:    method
  line:    70-95
  receiver: *Agent
  doc:      Prompt enqueues a user message...
  ```

- 命中多个按 `kind` 过滤;仍歧义则报错并提示"matches N declarations; use outline or add kind"。

### 3.6 索引失效(写工具回调)+ walkTree 共享

#### 3.6.1 包级单例(不再走 BuiltinConfig)

所有写工具(`fs.go` / `delete_symbol.go`)与 `codeIndex` 在**同一 `tool` 包**内,直接用包级单例 + 同包函数回调,无需经 `BuiltinConfig` 中转:

```go
// code_index.go
package tool

var globalCodeIndex = newCodeIndex()

func invalidateCodeIndex(absPath string) {
    if strings.HasSuffix(absPath, ".go") {
        globalCodeIndex.Invalidate(absPath)
    }
}

func clearCodeIndex() { globalCodeIndex.Clear() }
```

写工具成功后调一行 `invalidateCodeIndex(abs)`;`Registry.SetWorkspaceRoot` 调一行 `clearCodeIndex()`。

失效点列表:

| 写工具 | 触发位置 |
|---|---|
| `write_file` | `fs.go` `os.WriteFile` 成功后 |
| `edit_file` | `fs.go` `os.WriteFile` 成功后 |
| `multi_edit` | `fs.go` `os.WriteFile` 成功后 |
| `delete_range` | `fs.go` `os.WriteFile` 成功后 |
| `delete_symbol` | `delete_symbol.go` `os.WriteFile` 成功后 |
| `move_file` | `fs.go` `os.Rename` / copy+remove 成功后(source + dest 两端) |

#### 3.6.2 walkTree 提升为包级

`grepTool.walkTree` (`search.go:190-212`) 当前是 `*grepTool` 的方法,内部依赖 `t.sb`。`code_index` 同样要 walk .go,需要共享 walk + exclusion 逻辑。

调整:把 `walkTree` 提升为包级函数 `walkTree(ctx context.Context, sb *Sandbox, base string, fn func(abs string) error) error`,放在 `search.go`。`grepTool` 改为调用包级版本,`codeIndexTool.searchFiles` 也调用同一版本。`isHiddenName` 同步提升为包级。

`Sandbox` / `IsExcluded` 不变。

### 3.7 权限与配置

- `ClassifyPermission` 返回 safe(只读)。
- `pathEscape` 不新增分支:单文件读经 `ResolveRead`,目录 walk 经 `Resolve` → 越界由 sandbox 返回 `ErrNeedsPermission` 由 executor 转审批门。
- 不加配置开关(索引是进程内缓存,不会泄露 workspace 外文件)。
- 输出大小受 `maxHardReadBytes`(16 MiB)间接约束(单 `.go` parse 受 sandbox 读上限保护)。

## 4. 实施步骤

1. `search.go`:把 `walkTree` 与 `isHiddenName` 提升为包级;`grepTool.walkTree` 改为调用包级版本。
2. `delete_symbol.go` 抽 `parseGoFile(src []byte) (*token.FileSet, *ast.File, error)` helper,替换内部重复 parse 逻辑。
3. 新建 `code_index.go`:
   - 包级单例 `globalCodeIndex = newCodeIndex()` + `SymbolRef` 类型。
   - `parseFileForIndex` 把 AST 节点压平为 `[]SymbolRef`。
   - `codeIndexTool` 三个 action 分支(`outline` / `search` / `info`)。
   - `init()` 注册 builtin factory。
4. `fs.go` 与 `delete_symbol.go` 的写工具成功路径末尾各加一行 `invalidateCodeIndex(abs)`(仅 `.go`)。
5. `registry.go` `SetWorkspaceRoot` 加 `clearCodeIndex()`。
6. 补 `code_index_test.go`,跑 §5 验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/tools/code_index.go` | 新增,包级单例 + 3 个 action + invalidateCodeIndex / clearCodeIndex 函数 |
| `internal/tools/delete_symbol.go` | 抽 `parseGoFile` helper,减少重复 parse 逻辑 |
| `internal/tools/search.go` | `walkTree` / `isHiddenName` 提升为包级 |
| `internal/tools/fs.go` | 6 个写工具成功路径各加一行 `invalidateCodeIndex(abs)` |
| `internal/tools/registry.go` | `SetWorkspaceRoot` 加 `clearCodeIndex()` |
| `internal/tools/code_index_test.go` | 新增,覆盖 outline / search / info + 失效 |

## 6. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警。
4. `golangci-lint run ./...` 零告警(相对 `.golangci-baseline.txt` 不新增)。
5. `go test ./...` 全绿;新工具单测覆盖:
   - `outline`:单文件结构化输出,顺序与 AST 一致;空文件 → `(no symbols)`。
   - `search`:substring 匹配 + `kind` 过滤 + `limit` 截断;跨多个文件按 path 分组。
   - `info`:method 用 `*Agent.Prompt` 定位;顶层函数用 `Prompt` 定位;歧义报错。
   - 失效:`multi_edit` 改 `.go` 后再 `outline` 看到新符号。
   - 越界:path 在 workspace 外被 `ResolveRead` 拒绝。
6. `bash scripts/check-agent-readability.sh` 不新增超密度 / 违规文件。

## 7. 后续(不在本 spec)

- tree-sitter 多语言支持(`.ts` / `.py` / `.rs` / `.java`)。
- 索引持久化到磁盘(大项目冷启动 < 200 ms)。
- 跨语言符号解析(LSP)。
- 语义搜索 / 调用图。
- `search` 加 `mode=regex|exact` 选项。
