# skills types.go 领域拆分 设计文档

## 1. 概述

### 1.1 问题 / 动机

`src/darvin-agent/internal/skills/types.go`(79 行)是一个集中放置类型的 `types.go` 垃圾抽屉,违反 `CLAUDE.md` **F5 规则**(禁建 `types.go`;struct / const / interface 跟它的领域逻辑放在一起)。同包其他文件(scanner / loader / registry / runner / bootstrap / frontmatter / plugin / wire)均按域命名,唯独 `types.go` 例外。

该文件把两类模型集中堆放:

| 类型 | 域 | 实际消费方 |
|---|---|---|
| `SkillSource` + 5 常量 | skill 来源 | `loader.go`(`UserSource.Source`、`SkillSourceBundled/Global/Project`)、`registry.go`(`ListBySource`)、`bootstrap.go` |
| `SecurityRiskLevel` + 5 常量、`SecurityFinding`、`SecurityReport` | 目录安全扫描 | `scanner.go`(`ScanSkill` / `riskScoreToLevel` 生产它们)、`wire.go`(`RiskFindingWire(f)` 转换) |
| `SkillEntry` | 核心实体(30 字段) | 全包:loader 构造、registry 管理、runner 执行、wire 投影 |
| `SkillSourceLoader` 接口 | 来源加载能力 | `bootstrap.go`(组装)、`registry.go`(`Load` 签名);实现 `BundledSource` / `UserSource` 在 `loader.go` |

附带问题:**6 个导出类型全部缺 godoc**(`SkillSource` / `SecurityRiskLevel` / `SecurityFinding` / `SecurityReport` / `SkillEntry` / `SkillSourceLoader`),违反 N2"导出必须有 doc"。

### 1.2 目标

1. 按 F2 / F5 领域拆分 `types.go`,消除垃圾抽屉。
2. 给 6 个导出类型补 godoc,对齐 N2。
3. **零行为变化**:纯 refactor,类型定义、常量值、导出 API 签名全部原样,只改文件归属。

### 1.3 非目标

- **不改任何行为 / 字段 / 值**:`SkillEntry` 30 字段、5 个 `SkillSource` 值、5 个 `SecurityRiskLevel` 值逐字保留。
- **不引入新文件**:skills 包类型两簇,直接并入两个现有领域文件,不需要新文件。
- **不动其他包**:仅限 `internal/skills/`。

## 2. 现状分析

- `types.go`:79 行,`import (context, time)`。仅 `SkillSourceLoader` 用 `context`,`SkillEntry` 用 `time`。
- `scanner.go`:266 行,已自带 `Severity` 类型 + `severity*` 常量(安全扫描域),与 `SecurityReport` / `SecurityFinding` / `SecurityRiskLevel` 高度内聚。
- `loader.go`:284 行,`BundledSource` / `UserSource` 实现 `LoadAll`,`loadFileSkill` 构造 `SkillEntry`,`SkillSource` 标记来源 —— 是"skill 来源加载"域。
- package doc(`// Package skills ...`)已在 `registry.go:1`,不受影响。

## 3. 方案设计

按 F2「按业务域拆分」:类型并入各自的领域文件,不新建文件。

| 类型 | 去向 | 理由 |
|---|---|---|
| `SkillSource` + 常量 | `loader.go` | 来源加载域:loader 生产 source 标记(`UserSource.Source`) |
| `SkillEntry` | `loader.go` | 核心实体,加载产物;`loadFileSkill` 逐字段构造 |
| `SkillSourceLoader` 接口 | `loader.go` | 来源加载能力;与实现 `BundledSource`/`UserSource` 同文件,域自洽(同包内调用方 bootstrap/registry 不构成跨包,F6 不适用) |
| `SecurityRiskLevel` + 常量、`SecurityFinding`、`SecurityReport` | `scanner.go` | 安全扫描域:scanner 生产这些类型,已有同域 `Severity` |

预估行数:`loader.go` 284 → 约 335;`scanner.go` 266 → 约 290;均 <800(F1)。

**补 godoc(N2)**:6 个导出类型各加 1 行 doc 注释(简短说明用途)。

**文件头注释**:`loader.go` / `scanner.go` 现有 file-level comment 仍准确,不变。

## 4. 实施步骤

1. `loader.go` 末尾并入 `SkillSource` + 常量、`SkillEntry`、`SkillSourceLoader`,各补 doc。
2. `scanner.go` 末尾并入 `SecurityRiskLevel` + 常量、`SecurityFinding`、`SecurityReport`,各补 doc。
3. 删除 `types.go`。
4. `goimports -w -local darwin-cowork/ .` 归位(loader.go 需 `context` / `time`,scanner.go 无需新 import),跑 §6 全链验证。

## 5. 涉及文件

| 文件 | 变更 |
|---|---|
| `internal/skills/types.go` | 删除 |
| `internal/skills/loader.go` | 并入 `SkillSource` + `SkillEntry` + `SkillSourceLoader`(各补 doc) |
| `internal/skills/scanner.go` | 并入安全 3 类型 + 常量(各补 doc) |

## 6. 验证计划

1. `gofmt -l .` 与 `goimports -l .` 输出为空。
2. `go vet ./...` 零警告。
3. `staticcheck -checks 'ST10*' ./...` 零告警(新补 doc 对齐 ST1020-1023)。
4. `golangci-lint run ./...` 输出 `0 issues`(不允许新增)。
5. `go test ./internal/skills/...` 全绿。
6. `go test ./...` 全绿(全仓回归)。
7. `bash scripts/check-agent-readability.sh` 通过(F1/F3/F5/C3 + baseline)。
8. `npm run build:agent` 编译成功。
9. 抽查 diff:确认 `SkillEntry` 30 字段、各常量值逐字不变。
