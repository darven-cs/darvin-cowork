# TS Test Coverage 设计文档

> 路线图追踪：[`specs/features/commercialization-roadmap/CHECKLIST.md`](./CHECKLIST.md)
> 当前状态：待确认
> 规则约束：本文档及后续实现必须严格遵循仓库根 `CLAUDE.md`、`AGENTS.md` 与目标子目录下更具体的 `AGENTS.md` / `AGENTS.override.md`；若文档与这些规则冲突，以更具体、更新的仓库规则为准。

## 1. 概述

### 1.1 背景

darvin-cowork 已有 `vitest.config.ts` 与 `npm run test`，但覆盖率基线没规范。商业化要求按模块递增门禁。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | 覆盖率基线分模块定义 | baseline |
| G2 | `npm run test --coverage` 跑完输出 4 项指标 | reports |
| G3 | 增量门禁：新增模块 ≥ 80%，老模块 ≥ 60%（迁移期） | gate |
| G4 | CI 中挡提 PR | ci gate |
| G5 | ≥ 5 测试场景 | tests |

### 1.3 非目标

- 不引入新一轮覆盖率工具（沿用 `@vitest/coverage-v8`）。
- 不要求 100% 覆盖。

## 2. 现状锚点

| 路径 | 现状 |
|---|---|
| `vitest.config.ts` | `environment: 'node'`, `include: ['src/**/*.test.ts']` |
| `package.json` | `test: vitest run` |
| `devDependencies` | `vitest` + `@vitest/coverage-v8` |

## 3. 用户/系统场景

### 场景 1：跑覆盖率

**Given** dev 跑 npm run test:coverage
**When** 完成
**Then** 输出 statements / branches / functions / lines 4 项

### 场景 2：CI 失败

**Given** PR 修改 src/main/libs/user-paths.ts 但未加测试
**When** CI 跑
**Then** 因覆盖率下降挡提

### 场景 3：新模块门禁

**Given** 用户新增 src/renderer/foo/bar.ts
**When** 提 PR
**Then** bar.ts 覆盖率 < 80% 拦截

## 4. 功能需求

### FR-1 配置文件

`vitest.config.ts`：

```ts
coverage: {
  provider: 'v8',
  include: ['src/**/*.ts'],
  exclude: ['src/**/*.test.ts', 'src/**/*.d.ts', 'src/renderer/**/index.html'],
  thresholds: {
    perModule: true,
    rules: {
      'src/main/libs/**': { lines: 80, branches: 70 },
      'src/renderer/services/i18n.ts': { lines: 90, branches: 80 },
      'src/renderer/composables/**': { lines: 70 },
    },
  },
}
```

### FR-2 模块基线

| 模块 | 当前 | 目标 |
|---|---|---|
| `src/main/libs/**` | ~70% | ≥ 80% |
| `src/main/runtime/**` | ~30% | ≥ 60%（迁移期）|
| `src/renderer/components/**` | ~20% | ≥ 50%（迁移期） |
| `src/renderer/services/i18n.ts` | 100% | 100% |
| `src/renderer/composables/**` | ~40% | ≥ 70% |

### FR-3 增量门禁

`coverage.diff.ts`：

```ts
type Diff = {
  path: string
  before: number
  after: number
  diff: number
}

export function checkCoverageDiff(baseline: Coverage, current: Coverage): FailReason[]
```

### FR-4 CI gate

`.github/workflows/test.yml` 增加：

```yaml
- name: Coverage gate
  run: npm run test:coverage -- --reporter=json
  if: always()
- run: node scripts/check-coverage-gate.mjs
```

### FR-5 报告导出

`coverage/index.html` 直接浏览；JSON 输出 `coverage/coverage-summary.json`。

### FR-6 ≥ 5 测试场景

| # | 场景 |
|---|---|
| T1 | 跑 baseline |
| T2 | 跑新模块 |
| T3 | 下降拦截 |
| T4 | 提升通过 |
| T5 | rule 加载 |

## 5. 安全与隐私

- coverage 报告不进 git。

## 6. 故障与边界

| 场景 | 处理 |
|---|---|
| 测试失败 | 第一阶段不挡 |
| 文件未覆盖 | 提示 |

## 7. 涉及文件

| 文件 | 变更说明（不在本次交付） |
|---|---|
| `vitest.config.ts` | 增 thresholds |
| `scripts/check-coverage-gate.mjs`（新） | CI gate |
| `.github/workflows/test.yml` | 流程 |

## 8. 实施顺序与依赖

1. `vitest.config.ts` 配置
2. CI gate 脚本
3. 文档

## 9. 验收标准

| # | 标准 |
|---|---|
| V1 | `npm run lint` 通过 |
| V2 | `npm run test:coverage` 跑通 |
| V3 | CI 挡提示例 case |
| V4 | 不主动 commit、不 broad refactor、不写 `Co-Authored-By` |
| V5 | 验收后同步 `CHECKLIST.md` |

## 10. 不在范围

- 覆盖率 100% 强制。
- 商业工具接入（v2）。
