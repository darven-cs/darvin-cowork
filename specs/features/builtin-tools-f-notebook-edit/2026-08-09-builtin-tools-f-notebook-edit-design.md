# 内置工具补齐 F 组:notebook_edit 设计文档

## 1. 概述

### 1.1 问题 / 动机

Jupyter notebook(`.ipynb`)是数据科学标配。darvin-agent 当前对 `.ipynb` 的支持形同虚设:

- `read_file` 能读(拿到 JSON 文本)。
- `write_file` 能写(但 LLM 要手动构造 `cells` 数组,极易丢 `outputs` / `execution_count` / `cell.id`)。
- `edit_file` 能跑(但 `old_text` / `new_text` 字符串匹配在 JSON 上极脆弱,改一行 cell 经常误命中多处,改完 JSON 格式可能坏掉)。

LLM 完全无法可靠地编辑 notebook。参考项目 DeepSeek-Reasonix 的 `notebook_edit` 工具(`internal/tool/builtin/notebookedit.go`)提供结构化的 cell 级编辑——本 spec 对齐 Reasonix 落地 darvin-agent。

### 1.2 目标

1. 新增 `notebook_edit` 内置工具,3 个 operation:`insert` / `replace` / `delete`。
2. 只支持 nbformat 4(当前 Jupyter 唯一主流格式),其它格式报错并提示导出。
3. 不引入新依赖,纯 `encoding/json` + `crypto/rand`。
4. `replace` 时保留 `outputs` / `execution_count` / `metadata` / `id`,避免丢执行结果。
5. `insert` 时自动生成 cell id(`crypto/rand` 16 字节 hex 截前 8 字符)。
6. 与现有 fs 写工具同等权限:走 `pathEscape` 写分支,workspace 内 safe 越界走审批。

### 1.3 非目标

- 不实现 cell 执行 / kernel 通信(本工具只编辑 JSON,不运行)。
- 不支持 nbformat 3 等历史格式(报错并提示导出为 v4)。
- 不实现图片附件 inline 渲染 / base64 处理。
- 不做 notebook diff / merge(后续)。
- 不做 cell 拖拽排序(顺序通过 `position=above|below` 控制)。

## 2. 现状分析

Jupyter notebook 标准结构(nbformat 4):

```json
{
  "cells": [
    {
      "cell_type": "code",
      "id": "abc123",
      "metadata": {},
      "source": ["print('hello')\n"],
      "outputs": [{"output_type": "stream", "name": "stdout", "text": ["hello\n"]}],
      "execution_count": 1
    },
    {
      "cell_type": "markdown",
      "id": "def456",
      "metadata": {},
      "source": ["# Title\n", "Some text"]
    }
  ],
  "metadata": {
    "kernelspec": {"name": "python3", "display_name": "Python 3"},
    "language_info": {"name": "python"}
  },
  "nbformat": 4,
  "nbformat_minor": 5
}
```

关键约束:

- `cells` 数组,每项必须有 `id`(nbformat 4.5+);缺 id 时工具在 insert 时生成。
- `source` 是字符串数组(每行一项,最后一项可不含 `\n`),不要拼成单字符串(否则 Jupyter 会重新拆行,引入 diff 噪声)。
- code cell 的 `outputs` / `execution_count` 是用户运行结果,`replace` 必须保留。
- 顶层 `nbformat` / `nbformat_minor` / `metadata.kernelspec` 必须原样保留(否则下次打开 kernel 错)。

参考实现对照(DeepSeek-Reasonix `notebookedit.go`):

- 3 种 operation:`insert` / `replace` / `delete`。
- `replace` 按 cell_id 定位,保留 outputs。
- `insert` 支持 above / below 相对位置。
- nbformat 校验,非 4 报错。

darvin-agent 本期实现对齐 Reasonix,精简 CLI 注释。

## 3. 方案设计

### 3.0 文件组织(F2 按业务域)

| 新文件 | 域 | 内容 |
|---|---|---|
| `notebook_edit.go` | Jupyter notebook cell 编辑 | `notebookEditTool` + JSON helper + 注册 |

预计 220 行,< 800(F1)。纯 stdlib,无新依赖。

### 3.1 参数

参数(schema):

- `path`(string,必填,必须 `.ipynb` 后缀)
- `operation`(string,enum:`insert` | `replace` | `delete`,必填)
- `cell_id`(string,`replace` / `delete` 时必填;`insert` 时作为锚点,必填)
- `cell_type`(string,enum:`code` | `markdown`,`insert` 时必填)
- `source`(string,`insert` / `replace` 时必填):cell 源代码(按 `\n` 拆字符串数组)
- `position`(string,enum:`above` | `below`,`insert` 时默认 `below`):相对 `cell_id` 的位置

### 3.2 内部类型

```go
type notebook struct {
    NbFormat       int            `json:"nbformat"`
    NbFormatMinor  int            `json:"nbformat_minor"`
    Metadata       map[string]any `json:"metadata"`
    Cells          []cell         `json:"cells"`
}

type cell struct {
    ID             string         `json:"id"`
    CellType       string         `json:"cell_type"`
    Metadata       map[string]any `json:"metadata"`
    Source         []string       `json:"source"`
    Outputs        []any          `json:"outputs,omitempty"`
    ExecutionCount *int           `json:"execution_count,omitempty"`
}
```

`Metadata` / `Outputs` 用 `map[string]any` / `[]any` 容错,避免对 kernelspec 结构做强约束。

### 3.3 insert

操作流程:

1. `sb.Resolve(path)` → 必须 `.ipynb` 后缀,否则 `notebook_edit only supports .ipynb files`。
2. `os.ReadFile` → `json.Unmarshal` 到 `notebook{}`。
3. 校验 `nbformat == 4 && nbformat_minor >= 5`,否则 `notebook format v<N>.<M> not supported (cell.id requires 4.5+); export to v4.5+ first`。
4. unmarshal 后归一化:所有 cell 的 `Metadata == nil` → `map[string]any{}`;code cell 的 `Outputs == nil` → `[]any{}`;顶层 `Metadata == nil` → `map[string]any{}`。避免写回时把 `{}` 写成 `null`。
5. 找到 `cell_id` 锚点位置(遍历 `cells` 比对 `id`;找不到报 `cell "abc123" not found`)。
6. 构造新 cell:
   - `id`:`generateCellID()`(见 §3.6)。
   - `source`:`splitSource(sourceArg)`(见 §3.6)。
   - `cell_type=code` 时 `Outputs=[]`、`ExecutionCount=nil`(首次插入还没跑过)。
   - `cell_type=markdown` 时 `Outputs` / `ExecutionCount` 字段不输出(`omitempty` 自动省)。
   - `Metadata=map[string]any{}`。
7. 插入到锚点 `position` 位置:
   - `above` → 锚点 index 之前。
   - `below` → 锚点 index + 1。
8. `json.MarshalIndent` 写回(`indent=1`,符合 Jupyter 默认格式),受 `maxHardWriteBytes`(32 MiB)约束。

### 3.4 replace

操作流程:

1-4 同 insert。
5. 按 `cell_id` 找到 cell index(找不到报错)。
6. 替换 `source` 字段:
   - `cell_type` 必须匹配(不一致报错:`cell abc123 is type code; cannot replace as markdown (delete first)`)。
   - `Outputs` / `ExecutionCount` 原样保留。
   - `Metadata` 保留。
   - `ID` 保留。
7. `json.MarshalIndent` 写回。

### 3.5 delete

操作流程:

1-4 同 insert。
5. 按 `cell_id` 找到 cell index(找不到报错)。
6. `cells = append(cells[:i], cells[i+1:]...)`。
7. `json.MarshalIndent` 写回。

### 3.4bis 数据归一化(unmarshal 后必做)

写回前对 `notebook` 做一次就地归一化:

```go
if nb.Metadata == nil { nb.Metadata = map[string]any{} }
for i := range nb.Cells {
    if nb.Cells[i].Metadata == nil {
        nb.Cells[i].Metadata = map[string]any{}
    }
    if nb.Cells[i].CellType == "code" && nb.Cells[i].Outputs == nil {
        nb.Cells[i].Outputs = []any{}
    }
}
```

目的:确保写回的 JSON 中 `metadata` / `outputs` 是 `{}` / `[]` 而不是 `null`(Jupyter 解析两种都接受,但 `null` 在部分版本触发警告,且与现有 notebook diff 工具不友好)。

### 3.6 helper

```go
// splitSource splits a user-provided source string into the nbformat
// []string convention: each line is a separate entry; the final entry
// keeps its trailing newline if present.
func splitSource(s string) []string {
    if s == "" {
        return []string{""}
    }
    lines := strings.Split(s, "\n")
    // strings.Split drops the trailing empty string if s ends with "\n";
    // nbformat requires that last entry to be empty (representing the "\n"
    // before EOF). Restore it.
    if strings.HasSuffix(s, "\n") {
        lines = append(lines, "")
    }
    return lines
}

// generateCellID returns 8 hex chars (32 bits of entropy) — short enough
// to read in a notebook UI, unique enough for in-file use.
func generateCellID() string {
    var b [4]byte
    _, _ = rand.Read(b[:])
    return hex.EncodeToString(b[:])
}
```

### 3.7 错误处理

| 场景 | 报错文案 |
|---|---|
| 文件不存在 | `read: no such file or directory` |
| 非 `.ipynb` 后缀 | `notebook_edit only supports .ipynb files` |
| JSON 解析失败 | `parse: <err>`(含 json 错误位置) |
| `nbformat != 4` 或 `nbformat_minor < 5` | `notebook format v<N>.<M> not supported (cell.id requires 4.5+); export to v4.5+ first` |
| `cell_id` 找不到 | `cell "abc123" not found` |
| `insert` 时缺 `cell_id` | `cell_id required as anchor for insert` |
| `replace` 时 `cell_type` 不一致 | `cell abc123 is type code; cannot replace as markdown (delete first)` |
| 写失败 | `write: <err>` |
| 输出超 32 MiB | `notebook exceeds 32 MiB write cap` |

### 3.8 权限

- 走 `pathEscape` 写分支(新增 `notebook_edit` 到 case 列表),workspace 内 safe,越界走审批。
- 单文件大小受 `maxHardWriteBytes`(32 MiB)约束。
- 不加新配置开关。

### 3.9 输出格式

成功返回一行概要 + 结构化字段(LLM 可据此确认操作落地):

```text
notebook_edit: insert code cell "x7f2a9c1b" in analysis.ipynb
  position:   below "abc123" (new idx 3)
  source:     2 line(s)
  outputs:    cleared (fresh cell)

notebook_edit: replace cell "abc123" in analysis.ipynb
  kind:       code (outputs/execution_count preserved)
  source:     was 1 line, now 2 line(s)

notebook_edit: delete cell "abc123" in analysis.ipynb
  cells:      12 → 11
```

## 4. 实施步骤

1. `notebook_edit.go`:
   - `notebook` / `cell` 内部 JSON 结构。
   - `splitSource` / `generateCellID` helper。
   - `notebookEditTool.Execute` 三分支(insert / replace / delete)。
   - `init()` 注册 builtin factory。
2. `permission.go` `pathEscape` 写分支(`case "write_file", "edit_file", ...`)追加 `notebook_edit`。
3. 补 `notebook_edit_test.go`,跑 §5 验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/tools/notebook_edit.go` | 新增,纯 JSON 操作 |
| `internal/tools/permission.go` | `pathEscape` 写分支追加 1 项 |
| `internal/tools/notebook_edit_test.go` | 新增,覆盖 insert / replace / delete + 错误路径 |

## 6. 验证计划

1. `gofmt -l .` / `goimports -l .` 为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警。
4. `golangci-lint run ./...` 零告警(相对 baseline 不新增)。
5. `go test ./...` 全绿;新工具单测覆盖:
   - `insert`:`code` + `markdown` 各 1 次,验证 source 拆行、id 自动生成、`outputs` 为空、`position=above|below`。
   - `replace`:code cell 改 source 后 `outputs` / `execution_count` 仍保留;`cell_type` 不一致报错。
   - `delete`:删中间 cell 后前后 cell id 顺序不变。
   - 错误:非 `.ipynb` 后缀、`nbformat=3`、`cell_id` 找不到、`cell_type` 不一致、空 cell_id。
   - 沙箱:workspace 外路径被 `Resolve` 拒绝。
   - round-trip:插入若干 cell 后 `json.Unmarshal` 能完整还原结构。
6. `bash scripts/check-agent-readability.sh` 不新增超密度 / 违规文件。

## 7. 后续(不在本 spec)

- nbformat 3 向后兼容(legacy kernels)。
- notebook diff / merge(cell 级 patch)。
- 多 cell 批量操作(`multi_notebook_edit`)。
- cell metadata 编辑(tags / collapse / slideshow 等)。
- 与 kernel 通信支持执行 cell(走 MCP python-kernel server)。
