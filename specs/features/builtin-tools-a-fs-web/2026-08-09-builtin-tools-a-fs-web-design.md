# 内置工具补齐 A 组(grep/glob/move_file/multi_edit/delete_range/delete_symbol)+ web_fetch 设计文档

## 1. 概述

### 1.1 问题 / 动机

`src/darvin-agent/internal/tools/` 当前只有 5 个内置工具(`read_file` / `write_file` / `edit_file` / `list_dir` / `shell`)。对比参考项目 DeepSeek-Reasonix 的 23 个内置工具,当前缺口共 18 个。其中 6 个纯搜索 / 文件操作工具加 `web_fetch` 不依赖 host 架构,可独立落地:

- `grep`(递归正则搜索)
- `glob`(模式匹配找文件)
- `move_file`(移动 / 重命名)
- `multi_edit`(单文件原子多编辑)
- `delete_range`(删除行区间)
- `delete_symbol`(删除 Go 符号,AST)
- `web_fetch`(HTTP 抓取转文本)

### 1.2 目标

1. 新增 7 个内置工具,全部复用现有 `BuiltinConfig` / `RegisterBuiltinFactory` / `Sandbox` / `validateArgs` / `MarshalSchema` 机制,零架构改动。
2. 读写工具纳入现有沙箱与权限路径(`pathEscape`),不绕过 `EvaluatePermission` 门。
3. 每个工具配单元测试,保持 `go test ./...` 全绿与 `golangci-lint` 零告警。

### 1.3 非目标

- 不改 `Tool` / `Result` / `Registry` / `Sandbox` 契约。
- 不引入 ripgrep 外部二进制依赖(`grep` 用原生 Go 遍历)。
- 不新增前后端 IPC / 渲染层改动(工具经现有 dispatcher 自动暴露)。
- 后台任务(C 组)、todo/goal(D 组)不在本 spec 范围。

## 2. 现状分析

| 现有设施 | 说明 |
|---|---|
| `BuiltinConfig{Sandbox, Allowlist}` | registry.go:239,`NewBuiltins` 逐个构造;新增工具工厂从这里拿沙箱 |
| `RegisterBuiltinFactory` + `init()` | registry.go:255,`NewBuiltins` 经 `RegisteredBuiltinFactories()` 自动装配,新增工具只需注册即上线 |
| `Sandbox.Resolve / ResolveRead` | sandbox.go,写 / 读路径的沙箱与授权根判定 |
| `validateArgs` | params.go,arg 校验(unknown arg 硬拒绝、enum/范围/长度) |
| `ClassifyPermission / pathEscape` | permission.go,`shell` 走命令级危险判定;`read_file/write_file/edit_file/list_dir` 走路径越界判定 |

`grep` / `glob` 为只读工具,对标 `read_file`(允许读 workspace + granted-reads);其余 4 个写工具对标 `write_file` / `edit_file`(workspace 内是授权生成区,越界才要审批)。`web_fetch` 是全新的网络出站能力,需单独定策略。

参考实现对照(DeepSeek-Reasonix):
- `grep`(432 行):原生 `filepath.WalkDir` + `bufio.Scanner` + RE2 `regexp`,支持 `engine=rg` 可选回退;默认 200 条封顶,`timeout_seconds` 默认 30 max 300。
- `glob`(用 doublestar/v4):`**` 递归匹配,无 `**` 时走 `filepath.Glob`,超时返回部分结果。
- `move_file` / `multi_edit` / `delete_range` / `delete_symbol`:`delete_symbol` 用 `go/parser` + `go/ast` 只支持 Go,其他语言提示走 `delete_range`。
- `web_fetch`(585 行):`http.Client` + SSRF 防护(私网 IP 拦截)+ `golang.org/x/net/html` 转文本,HTML 剥脚本 / 样式 / 标签。

## 3. 方案设计

### 3.0 文件组织(F2 按业务域)

| 新文件 | 域 | 工具 |
|---|---|---|
| `search.go` | 文本 / 文件搜索 | `grep` + `glob`(共享 walk 与超时 helper) |
| `fs.go`(扩展) | 文件读写操作 | 并入 `move_file` / `multi_edit` / `delete_range` |
| `delete_symbol.go` | Go AST 符号删除 | `delete_symbol` |
| `web_fetch.go` | 网络抓取 | `web_fetch` |

`fs.go` 现有 300 行,并入 3 个工具后约 480 行,仍 <800(F1)。

### 3.1 grep

参数(schema):
- `pattern`(string,必填):RE2 正则。
- `path`(string,默认 workspace 根):目标文件或目录。
- `timeout_seconds`(integer,默认 30,max 300):超时返回部分结果。
- `max_matches`(integer,默认 200,1..1000):结果封顶。

实现:
- 单文件:经 `sb.ResolveRead`(读授权根,含 granted-reads);`bufio.Scanner` 逐行匹配,输出 `path:line:text`。
- 目录:`sb.Resolve` 基目录后 `filepath.WalkDir`;跳过沙箱排除目录(`.git` / `node_modules` / `dist` / `target` 等)与 `.` 开头的隐藏目录;逐行匹配;walk 每步检查 `ctx.Err()` 以便超时 / 取消及时退出。
- 单文件读取做字节上限(默认 1 MiB,超过跳过并注明),避免把大二进制拖进结果。
- 输出封顶 `max_matches` 条,超出注明截断。空结果显示 `(no matches)`。

权限:只读,`ClassifyPermission` 返回 safe。`pathEscape` 不新增分支(目录读经 `Resolve`,单文件经 `ResolveRead`,越界时工具自身返回 `ErrNeedsPermission` 文案)。

### 3.2 glob

参数(schema):
- `pattern`(string,必填):glob 模式,支持 `*` `?` `[]` 与 `**`。
- `path`(string,默认 workspace 根):搜索基目录。
- `timeout_seconds`(integer,默认 30,max 300):超时返回已收集的部分结果。

实现:
- 新增依赖 `github.com/bmatcuk/doublestar/v4`(纯 Go、无传递依赖),语义对齐参考实现;备选方案是手写 `**` 匹配(约 40 行、易错),不采用。
- `pattern` 转 OS 路径分隔符后,`doublestar.Glob` 从基目录递归匹配;结果做相对路径展示 + 排序去重。
- walk 受 `ctx` 取消约束;跳过排除目录。命中为空输出 `(no matches)`。

权限:只读 safe,同 `grep`。

### 3.3 move_file

参数(schema):
- `source_path`(string,必填)。
- `destination_path`(string,必填,目标不得已存在)。

实现:
- 两端都经 `sb.Resolve`(workspace 内);`os.MkdirAll(filepath.Dir(dest))` 后 `os.Rename`;跨设备 `rename` 失败时回退 copy+remove 并校验。
- 目标已存在则报错(不静默覆盖),语义对齐参考实现。

权限:写操作。在 `permission.go` 的 `pathEscape` 写入分支(`case "write_file", "edit_file", "list_dir"`)追加 `move_file`,workspace 内 safe,越界走审批。

### 3.4 multi_edit

参数(schema):
- `path`(string,必填)。
- `edits`(array,必填,1..100 项):每项 `{old_text, new_text, replace_all?}`。

实现:
- 重构 `editFileTool` 的替换核心为共享 helper:`applyEdits(src []byte, edits []editSpec) (out []byte, applied int, err error)`——内存内逐项应用,每项 old_text 必须命中(否则整体失败),最终一次性 `os.WriteFile`。
- `edit_file` 改为调用该 helper(行为不变),`multi_edit` 复用。
- 总 payload 受 `maxHardWriteBytes`(32 MiB)约束。

权限:写操作,`pathEscape` 写入分支追加 `multi_edit`。

### 3.5 delete_range

参数(schema):
- `path`(string,必填)。
- `start_text`(string,必填):区间首行锚点,必须精确匹配且唯一。
- `end_text`(string,必填):区间末行锚点,必须精确匹配且唯一,且位于 start_text 之后。

实现(文本锚点,对齐参考实现;行号方案易因文件行移动错位,不用):
- 读取文件,按行保留行尾分隔符切分;`start_text` / `end_text` 各自须与某一行整行精确匹配,且各仅命中一行;`start` 行号 ≤ `end` 行号,否则报错。
- 删除 `[start_line, end_line]` 闭区间,其余行原样拼接写回。
- 成功返回 **unified diff**(纯 stdlib 实现的最小行级 diff:删除行标 `-`,上下文行标 ` `;只删不改,无需通用 diff 算法),调用方可据此核对落盘结果。
- 锚点未命中 / 命中多行 / 区间倒置 / 空文件均报错;文件大小受读 / 写上限约束。

权限:写操作,`pathEscape` 写入分支追加 `delete_range`。

### 3.6 delete_symbol

参数(schema):
- `path`(string,必填,须为 `.go` 文件)。
- `name`(string,必填):符号名。
- `kind`(string,枚举 func/type/const/var/method,可选):缩小查找范围。

实现:
- 仅支持 Go:经 `sb.Resolve` 后 `os.ReadFile`,`go/parser` + `go/ast` 解析。
- 查找目标:`*ast.FuncDecl`(按 `Name`;method 按 `Recv` + `Name`)、`*ast.TypeSpec`、`*ast.GenDecl`(const/var 的 `ValueSpec` 命中),取声明字节区间,并向前扩展到所属 doc comment 组,字节级 splice 后写回。
- 命中 0 个报 `symbol "x" not found`;多个同名符号按 kind 过滤或报歧义。非 Go 文件报错并提示用 `delete_range`。
- 不跑 gofmt,保持字节 splice 不重排格式。

权限:写操作,`pathEscape` 写入分支追加 `delete_symbol`。

### 3.7 web_fetch

参数(schema):
- `url`(string,必填,http/https)。

实现(参数面对齐参考实现——只有 `url`;超时由 executor 的 per-tool timeout 统一管,响应体上限为内部常量不暴露):
- `http.NewRequestWithContext` + `http.Client`,设 `User-Agent` 与 `Accept` 头;读 `LimitReader` 封顶(内部常量,默认 1 MiB)。
- **SSRF 防护**(安全硬约束,不提供开关):URL 先解析 host → `net.LookupIP` → 拒绝私网 / loopback / link-local / 保留地址(IPv4 与 IPv6),再发请求;重定向同样复核目标 IP。
- 响应 Content-Type 为 HTML(或内容像 HTML)时,用 stdlib 实现的最小提取器:剥 `<script>` / `<style>` / 标签、`html.UnescapeString` 还原实体、折叠空白,输出可读文本;JSON / 纯文本 / markdown 原样返回。不引入 `golang.org/x/net/html`(避免新增依赖;提取质量不足时留作后续升级项)。
- 非 2xx 状态码返回错误并附状态行。

权限:网络出站,分类为 safe(只读 GET、无 workspace 副作用),但其存在本身受配置开关控制(见 §3.8)。不加 `pathEscape`。

### 3.8 配置

`internal/config/config.go` 的 `AgentConfig` 新增:

```go
WebFetchEnabled bool `mapstructure:"web_fetch_enabled"` // 默认 true
```

`internal/runtime/gateway.go` 的 `loadTools` 读取该开关:关闭时不注册 `web_fetch` 工厂(其余 6 个工具无条件注册)。域名白名单留作后续(§6)。

## 4. 实施步骤

1. `search.go`:实现 `grepTool` + `globTool`,各带 `init()` 注册。
2. `fs.go`:抽 `applyEdits` helper,`edit_file` 改为调用;新增 `moveFileTool` / `multiEditTool` / `deleteRangeTool` + 注册。
3. `delete_symbol.go`:实现 `deleteSymbolTool` + 注册。
4. `web_fetch.go`:实现 `webFetchTool` + SSRF 防护 + HTML 提取器 + 注册。
5. `permission.go`:`pathEscape` 写入分支追加 `move_file` / `multi_edit` / `delete_range` / `delete_symbol`。
6. `config.go` 加 `WebFetchEnabled`;`gateway.go` 读开关条件注册 `web_fetch`。
7. `go.mod` 加 `github.com/bmatcuk/doublestar/v4`。
8. 补 `*_test.go`,跑 §5 验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/tools/search.go` | 新增,`grep` + `glob` |
| `internal/tools/fs.go` | 抽 `applyEdits`;新增 `move_file` / `multi_edit` / `delete_range` |
| `internal/tools/delete_symbol.go` | 新增,`delete_symbol` |
| `internal/tools/web_fetch.go` | 新增,`web_fetch` + SSRF + HTML 提取 |
| `internal/tools/permission.go` | `pathEscape` 写入分支追加 4 个写工具 |
| `internal/config/config.go` | 新增 `WebFetchEnabled` |
| `internal/runtime/gateway.go` | 按开关条件注册 `web_fetch` |
| `go.mod` / `go.sum` | 新增 `doublestar/v4` |
| 各 `*_test.go` | 新增 7 组单元测试 |

## 6. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警。
4. `golangci-lint run ./...` 零告警(相对 `.golangci-baseline.txt` 不新增)。
5. `go test ./...` 全绿;新工具单测覆盖:grep 命中/空/超时部分结果、glob `**` 递归、applyEdits 原子失败、delete_range 锚点命中/唯一性/区间倒置/多行命中报错与 unified diff 输出、delete_symbol 函数/类型/方法/doc 注释、web_fetch SSRF 私网拦截与 HTML 提取。
6. `bash scripts/check-agent-readability.sh` 不新增超密度 / 违规文件。

## 7. 后续(不在本 spec)

- `grep` 可选 `engine=rg` 加速。
- `web_fetch` 域名白名单配置、`x/net/html` 高质量提取、响应缓存。
- C 组(后台任务)、D 组(todo/goal)工具另立 spec。
