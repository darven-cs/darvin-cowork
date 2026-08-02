# Sub-spec 32 — Skills IPC & Bootstrap（Go ↔ Main）

> **父 spec**：[`2026-08-02-skills-and-mcp-modules-design.md`](./2026-08-02-skills-and-mcp-modules-design.md)
>
> **本 spec 范围**：Go 端 + Main 端 skill manager 的 IPC 通信 + bootstrap 流程。**不包含** UI（spec 33）。
>
> 创建日期：2026-08-02
> 状态：⏳ 待启动
> 前置：[spec 31 skills-loader-and-registry](./2026-08-02-skills-loader-and-registry.md)

---

## 1. 概述

### 1.1 问题 / 背景

spec 31 落地后，Go 端有 `SkillRegistry` + `SkillRunner`，但 renderer / main 都看不到。本 spec 把 IPC 层立起来：main 端通过 `agent.skills.*` JSON-RPC 跟 Go agent 通信；bundled 5 个 skill 打包到 darvin-agent 二进制 + 用户目录扫描。

### 1.2 目标

| # | 目标 | 度量 |
|---|------|------|
| G1 | main 端 `skillsManager` 启动时通过 WS RPC `agent.skills.bootstrap` 推初始 5 + N user skill 列表给 Go | App 启动 ≤ 2s 内 Go 收到 bootstrap |
| G2 | 用户改 enabled → main → Go `agent.skills.set_enabled` → Go 更新 registry + emit `agent.skills.changed` → main push renderer | live 验证：UI toggle → ≤ 500ms 看到状态变化 |
| G3 | Go 端 skill 变化（v0 不主动发生；预留接口）→ notification `agent.skills.changed` → main → renderer | RPC handler stub 落地 |
| G4 | main 端 `skillsManager` 监听 bundled / user 目录 fs event（chokidar），Go agent 端 reload | live：手动 `mkdir userData/SKILLs/foo` → Go 1s 内收到 changed |
| G5 | bundled skill 列表从 darvin-agent 二进制读（`//go:embed`），用户目录从 `userData/SKILLs/` 读 | 代码 review：main 端不依赖任何 npm 包 |

### 1.3 非目标

- 不做 install / uninstall / upgrade（spec 33）
- 不做 marketplace 拉取
- 不做安全扫描 UI（spec 33）
- 不做 renderer composables / views（spec 33）

---

## 2. 用户场景

### 场景 1：App 启动时 skills bootstrap

**Given** 用户首次启动 darvin-cowork，无任何 user skill
**When** main 端 `skillsManager.bootstrap()` 执行
**Then**：
1. main 端读 bundled skill 列表（hardcoded 5 个 + 描述；实际 SKILL.md 内容由 Go 端解析）
2. main 端 `agent.skills.bootstrap` 调 Go 端，传入初始 enabled 状态（默认全 enabled）
3. Go 端写入 registry，emit `agent.skills.bootstrap-ack`
4. main 端监听 `agent.skills.changed` notification，缓存最新 skills 列表

### 场景 2：用户在 UI 禁用 skill

**Given** `web-search` enabled，UI toggle 显示「开」
**When** 用户点 toggle → 关
**Then**：
1. renderer 调 `window.darvin.skills.setEnabled({ skillId: 'web-search', enabled: false })`
2. main IPC handler `skills:set_enabled` → `skillsManager.setEnabled()` → 写 SQLite
3. main `agent.skills.set_enabled` → Go 端 `SkillRegistry.SetEnabled('web-search', false)` → emit `agent.skills.changed`
4. main 收到 notification → push `onSkillsChanged` 给 renderer
5. renderer 看到 skill 卡片 toggle 已变关

### 场景 3：用户手动加一个 user skill

**Given** App 在运行；用户执行 `mkdir ~/userData/SKILLs/foo && echo '...' > ~/userData/SKILLs/foo/SKILL.md`
**When** 文件系统事件触发（≤ 1s）
**Then**：
1. main `skillsManager` fs watcher 检测新目录
2. 触发 `agent.skills.bootstrap`（增量推送单条）
3. Go 端 reload，emit `agent.skills.changed`
4. main → renderer 推送新 skill 列表

### 场景 4：bundled skill 误删

**Given** 用户手动 `rm -rf darvin-agent/resources/skills-bundled/code-review`（不合理但容错）
**When** App 重启
**Then** Go 端 embed 仍含 code-review（因为是编译期嵌入，不受文件系统影响）

---

## 3. 功能需求

### FR-1: 共享类型（`src/shared/darvin-api.ts`）

```typescript
export interface DarvinSkillSummary {
  id: string;
  name: string;
  description: string;
  version?: string;
  enabled: boolean;
  isOfficial: boolean;
  isBuiltIn: boolean;
  path: string;             // main 端路径（renderer 不展示）
  source: 'bundled' | 'user' | 'github' | 'npm';
  updatedAt: number;
  /** 安全扫描等级 */
  riskLevel?: 'safe' | 'low' | 'medium' | 'high' | 'critical';
  /** risk findings（high/critical 才回填） */
  riskFindings?: Array<{
    dimension: string;
    severity: 'info' | 'warning' | 'danger' | 'critical';
    message: string;
    file: string;
    line: number;
  }>;
}

export interface DarvinApi {
  // skills
  listSkills(): Promise<{ skills: DarvinSkillSummary[] }>;
  setSkillEnabled(req: { skillId: string; enabled: boolean }): Promise<{ ok: boolean }>;
  onSkillsChanged(handler: (skills: DarvinSkillSummary[]) => void): () => void;
}
```

### FR-2: Main 端 `skillsManager`

```typescript
// src/main/libs/skillManager.ts
export class SkillManager {
  private skills: Map<string, DarvinSkillSummary> = new Map();
  private watcher?: chokidar.FSWatcher;
  private sqliteStore: SqliteStore;

  constructor(sqliteStore: SqliteStore) { /* ... */ }

  async bootstrap(): Promise<void> {
    // 1. 启动 fs watcher（user 目录）
    this.watcher = chokidar.watch(path.join(getSkillsRoot(), '**/SKILL.md'), {
      ignored: /(^|[\/\\])\../,
      persistent: true,
      ignoreInitial: false,
    });
    this.watcher.on('add', () => this.reloadFromDisk());
    this.watcher.on('change', () => this.reloadFromDisk());
    this.watcher.on('unlink', () => this.reloadFromDisk());

    // 2. 第一次 reload：扫磁盘 + 从 SQLite 读 enabled 状态
    await this.reloadFromDisk();

    // 3. 推给 Go agent
    await this.agent.skills.bootstrap({ skills: Array.from(this.skills.values()) });

    // 4. 订阅 agent.skills.changed
    this.agent.on('skills.changed', (skills) => {
      this.skills = new Map(skills.map(s => [s.id, s]));
      this.broadcast();
    });
  }

  async list(): Promise<DarvinSkillSummary[]> {
    return Array.from(this.skills.values());
  }

  async setEnabled(skillId: string, enabled: boolean): Promise<void> {
    // 1. 写 SQLite
    this.sqliteStore.run(
      'INSERT OR REPLACE INTO skill_state (skill_id, enabled, updated_at) VALUES (?, ?, ?)',
      [skillId, enabled ? 1 : 0, Date.now()]
    );
    // 2. 调 Go
    await this.agent.skills.setEnabled({ skillId, enabled });
    // 3. 更新本地缓存
    const skill = this.skills.get(skillId);
    if (skill) { skill.enabled = enabled; this.broadcast(); }
  }

  private async reloadFromDisk(): Promise<void> {
    // 1. 扫 userData/SKILLs/*/SKILL.md
    // 2. 解析 frontmatter（轻量解析，不做安全扫描——扫描由 Go 端做）
    // 3. 从 SQLite 读 enabled 状态覆盖
    // 4. 通过 agent.skills.bootstrap 增量推
  }

  private broadcast(): void {
    this.eventBus.emit('skills:changed', Array.from(this.skills.values()));
  }

  async shutdown(): Promise<void> {
    await this.watcher?.close();
  }
}
```

### FR-3: Main 端 SQLite 表

```sql
-- 合并后 db schema（spec merge-databases）
CREATE TABLE IF NOT EXISTS skill_state (
  skill_id     TEXT PRIMARY KEY,
  enabled      INTEGER NOT NULL DEFAULT 1,
  updated_at   INTEGER NOT NULL
);
```

### FR-4: IPC Channels（`src/main/index.ts`）

```typescript
ipcMain.handle('skills:list', async () => {
  return { skills: await skillsManager.list() };
});

ipcMain.handle('skills:set_enabled', async (_event, req: { skillId: string; enabled: boolean }) => {
  await skillsManager.setEnabled(req.skillId, req.enabled);
  return { ok: true };
});

// 推 renderer 用
mainWindow.webContents.send('skills:changed', skills);  // 由 skillsManager.broadcast() 触发
```

### FR-5: Preload 暴露（`src/preload/index.ts`）

```typescript
contextBridge.exposeInMainWorld('darvin', {
  // ... 已有 API
  skills: {
    list: () => ipcRenderer.invoke('skills:list'),
    setEnabled: (req: { skillId: string; enabled: boolean }) => ipcRenderer.invoke('skills:set_enabled', req),
    onChanged: (handler: (skills: DarvinSkillSummary[]) => void) => {
      const wrapped = (_event: IpcRendererEvent, skills: DarvinSkillSummary[]) => handler(skills);
      ipcRenderer.on('skills:changed', wrapped);
      return () => ipcRenderer.off('skills:changed', wrapped);
    },
  },
});
```

### FR-6: Agent Client 扩展（`src/main/runtime/client.ts`）

```typescript
export class AgentClient {
  // ... 已有方法

  skills = {
    list: () => this.request<{ skills: any[] }>('agent.skills.list'),
    setEnabled: (req: { skillId: string; enabled: boolean }) =>
      this.request<{ ok: boolean }>('agent.skills.set_enabled', req),
    bootstrap: (req: { skills: any[] }) =>
      this.request<{ ok: boolean }>('agent.skills.bootstrap', req),
    onChanged: (handler: (skills: any[]) => void) => {
      this.on('skills.changed', handler);
    },
  };
}
```

### FR-7: Go 端 IPC handler

```go
// src/darvin-agent/internal/gateway/handlers.go 增量
type Handlers struct {
    // ... 已有字段
    Skills *skills.SkillRegistry
}

// handler:
h.handle("agent.skills.list", func(params json.RawMessage) (any, error) {
    list := h.Skills.Snapshot()
    return map[string]any{"skills": convertToDarvinSkills(list)}, nil
})

h.handle("agent.skills.set_enabled", func(params json.RawMessage) (any, error) {
    var req struct {
        SkillID string `json:"skillId"`
        Enabled bool   `json:"enabled"`
    }
    if err := json.Unmarshal(params, &req); err != nil {
        return nil, fmt.Errorf("invalid params: %w", err)
    }
    if err := h.Skills.SetEnabled(req.SkillID, req.Enabled); err != nil {
        return nil, err
    }
    // 通知所有订阅者
    h.broadcastSkillsChanged()
    return map[string]any{"ok": true}, nil
})

h.handle("agent.skills.bootstrap", func(params json.RawMessage) (any, error) {
    var req struct {
        Skills []darvin.SkillSummary `json:"skills"`
    }
    if err := json.Unmarshal(params, &req); err != nil {
        return nil, err
    }
    // 用 main 端的 enabled 状态覆盖 Go 端默认（main 端是 source of truth）
    for _, s := range req.Skills {
        _ = h.Skills.SetEnabled(s.ID, s.Enabled)
    }
    return map[string]any{"ok": true}, nil
})
```

### FR-8: bundled 5 个 skill 的内容（仅示例）

```markdown
---
name: code-review
description: 对代码做静态审查并给出修改建议
version: 0.1.0
invocation:
  userInvocable: true
---

# Code Review Skill

## 工具

- read_file
- search
- shell（仅 grep / find / wc）

## 工作流

1. 读用户指定的文件
2. 用 grep 找反模式（TODO / FIXME / XXX / hardcoded credentials）
3. 检查命名 / 注释 / 测试覆盖
4. 生成 markdown 报告
```

其余 4 个 skill 类似；v0 不要求实际可用，**仅占位让 UI 有内容**。

---

## 4. 实现方案

### 4.1 文件清单

```
src/main/
├── libs/
│   ├── skillManager.ts          🆕 ~250 行
│   └── skillManager.test.ts     🆕 ~80 行
├── index.ts                     +ipc handler 注册
└── libs/user-paths.ts           +getSkillsRoot()

src/preload/
└── index.ts                     +window.darvin.skills.*

src/shared/
└── darvin-api.ts                +DarvinSkillSummary + DarvinApi.skills.*

src/main/runtime/
└── client.ts                    +skills: {list, setEnabled, bootstrap, onChanged}

src/darvin-agent/internal/gateway/
└── handlers.go                  +3 个 handler
```

### 4.2 关键代码片段

#### 4.2.1 frontmatter 解析（main 端用，不做安全扫描）

```typescript
// src/main/libs/skillManager.ts
import yaml from 'js-yaml';  // darvin-cowork 已用（同 darvin-api-extension 的 markdown 处理）

interface ParsedFrontmatter {
  name: string;
  description: string;
  version?: string;
  invocation?: {
    userInvocable?: boolean;
    disableModelInvocation?: boolean;
  };
}

function parseFrontmatter(raw: string): { fm: ParsedFrontmatter; body: string } | null {
  const match = raw.match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/);
  if (!match) return null;
  const fm = yaml.load(match[1]) as ParsedFrontmatter;
  const body = match[2];
  if (!fm?.name || !fm?.description) return null;
  return { fm, body };
}
```

#### 4.2.2 fs watcher

```typescript
import chokidar from 'chokidar';

private async startWatcher(): Promise<void> {
  const skillsRoot = path.join(getUserDataPath(), 'SKILLs');
  await fs.ensureDir(skillsRoot);

  this.watcher = chokidar.watch(path.join(skillsRoot, '**/SKILL.md'), {
    ignored: /(^|[\/\\])\../,
    persistent: true,
    ignoreInitial: true,
    awaitWriteFinish: { stabilityThreshold: 500, pollInterval: 100 },
  });

  this.watcher.on('add', (filePath) => this.reloadOne(filePath));
  this.watcher.on('change', (filePath) => this.reloadOne(filePath));
  this.watcher.on('unlink', (filePath) => this.removeOne(filePath));
}
```

#### 4.2.3 bundled skill 列表（main 端 hardcoded）

```typescript
// src/main/libs/skillManager.ts
const BUNDLED_SKILLS: Array<{ id: string; name: string; description: string; version: string }> = [
  { id: 'code-review', name: 'Code Review', description: '对代码做静态审查并给出修改建议', version: '0.1.0' },
  { id: 'api-design',  name: 'API Design',  description: '检查 REST API 设计规范（命名 / 状态码 / 错误处理）', version: '0.1.0' },
  { id: 'testing',     name: 'Testing',     description: '给出单元测试覆盖建议', version: '0.1.0' },
  { id: 'web-search',  name: 'Web Search',  description: '联网搜索最新信息', version: '0.1.0' },
  { id: 'docx',        name: 'Word Document', description: '创建 / 修改 Word 文档', version: '0.1.0' },
];
```

bundled skill 的 SKILL.md 实际内容由 Go 端 embed 解析，main 端只存元数据。

### 4.3 关键决策与理由

#### 4.3.1 enabled 状态由 main 端 SQLite 持久化

**理由**：renderer 不直接写 SQLite；UI 操作经 main 端 IPC → SQLite → Go 端。**main 端是 source of truth**。

#### 4.3.2 frontmatter 解析在 main 端做（不调 Go）

**理由**：frontmatter 解析是纯字符串处理，不需要 Go agent 参与；保持 main 端逻辑简单。安全扫描由 Go 端做（spec 31 scanner）。

#### 4.3.3 fs watcher 用 chokidar 而非 Node fs.watch

**理由**：macOS fs.watch 不可靠；chokidar 跨平台稳定。darvin-cowork 已有 chokidar 依赖（用于 workspace 文件监听）。

#### 4.3.4 不做 install / uninstall RPC

**理由**：v0 安装流程在 spec 33 落地（renderer → main → 解压 + 扫描 + 写目录 → fs watcher 自动触发 reload）。

### 4.4 测试策略

| 测试文件 | 覆盖 |
|----------|------|
| `skillManager.test.ts` | `parseFrontmatter` 正常 / 缺 name / 缺 description / 无 frontmatter / unknown field；`setEnabled` 写 SQLite；`bootstrap` 调 agent.skills.bootstrap |

---

## 5. 边界情况

| 场景 | 处理方式 |
|------|---------|
| userData/SKILLs 目录不存在 | 创建空目录；reload 返回空 |
| SKILL.md 文件非 UTF-8 | 跳过 + warn |
| SKILL.md frontmatter 解析失败 | 跳过该 skill，warn 日志，其它 skill 正常 |
| chokidar 在 macOS FSEvents 失效 | 加 30s 兜底 reload 定时器 |
| 用户装一个 id 跟 bundled 同名 | user 覆盖 bundled，UI 提示「已覆盖 bundled skill X」 |
| Go agent 启动失败 | main 端缓存 enabled 状态；下次启动重试 |
| SQLite write 失败 | retry 3 次；最终 fail 抛错 |
| bundled skill 在 main 端 hardcoded 列表缺失 | Go 端启动时检测；warn 日志「bundled skill X not in main manifest」 |

---

## 6. 涉及文件

| 文件 | 变更 |
|------|------|
| `src/main/libs/skillManager.ts` | 🆕 |
| `src/main/libs/skillManager.test.ts` | 🆕 |
| `src/main/libs/user-paths.ts` | +`getSkillsRoot()` |
| `src/main/index.ts` | +`ipcMain.handle('skills:list')` + `'skills:set_enabled'` + bootstrap 流程 |
| `src/main/runtime/client.ts` | +`skills: { list, setEnabled, bootstrap, onChanged }` |
| `src/preload/index.ts` | +`window.darvin.skills.*` |
| `src/shared/darvin-api.ts` | +`DarvinSkillSummary` + `DarvinApi.skills.*` |
| `src/darvin-agent/internal/gateway/handlers.go` | +3 个 handler（list / set_enabled / bootstrap） |
| `src/darvin-agent/internal/skills/types.go` | +`Darvin` 转换函数 |

---

## 7. 验收标准

**通用**：
- [ ] `npm run lint` + `npm run test` 通过
- [ ] `cd src/darvin-agent && go build ./...` 编译通过
- [ ] `go vet ./...` 无警告

**FR-1 类型**：
- [ ] `DarvinSkillSummary` 11 个字段齐全
- [ ] `DarvinApi.skills.list` / `setEnabled` / `onChanged` 类型定义正确

**FR-2 manager**：
- [ ] `bootstrap()` 启动后 fs watcher 在 2s 内就绪
- [ ] `setEnabled` 写 SQLite + 调 Go 端 + broadcast 三步顺序执行
- [ ] SQLite write 失败 retry 3 次

**FR-3 SQLite**：
- [ ] `skill_state` 表创建成功
- [ ] enable / disable 状态重启 App 后恢复

**FR-4 IPC**：
- [ ] `skills:list` 返回当前缓存
- [ ] `skills:set_enabled` 调 manager

**FR-5 preload**：
- [ ] `window.darvin.skills.list()` 可在 renderer console 调

**FR-6 AgentClient**：
- [ ] `client.skills.list()` 走 JSON-RPC 协议

**FR-7 Go handler**：
- [ ] `agent.skills.list` 返回 Go 端 registry 快照
- [ ] `agent.skills.set_enabled` 改内存 + emit changed
- [ ] `agent.skills.bootstrap` 接受 main 端初始 enabled 状态覆盖

**FR-8 bundled 5**：
- [ ] main 端 `BUNDLED_SKILLS` 列表 5 项
- [ ] Go 端 embed 5 个 SKILL.md 文件

**集成手测**：

```bash
# 启动 App
npm start

# renderer console:
await window.darvin.skills.list()
# 期望：{ skills: [{id:'code-review', enabled:true, ...}, ...] }

await window.darvin.skills.setEnabled({ skillId: 'web-search', enabled: false })
# 期望：{ ok: true }
await window.darvin.skills.list()
# 期望：web-search.enabled === false

# 创建 user skill：
mkdir -p ~/userData/SKILLs/foo
cat > ~/userData/SKILLs/foo/SKILL.md <<EOF
---
name: foo
description: 一个测试 skill
version: 0.0.1
---
body
EOF
# 1s 后 renderer list 应包含 foo
```

---

## 8. 与其他 spec 的关系

**前置依赖**：spec 31

**下游依赖**：
- spec 33（renderer view）消费本 spec 的 `window.darvin.skills.*`

**并行**：spec 34 / 35 / 36（MCP）不依赖本 spec

---

## 9. 状态变更日志

- 2026-08-02 · 完成 spec 设计；待用户确认后启动实现