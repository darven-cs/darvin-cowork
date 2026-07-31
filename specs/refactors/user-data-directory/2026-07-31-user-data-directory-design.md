# 用户数据目录统一设计文档

## 1. 概述

### 1.1 问题 / 动机

当前 darvin-cowork 有三份用户级持久化文件，散落在**三个不同的目录**，其中一份甚至写进了源码树：

```
~/.config/Darvin-Cowork/darvin-cowork.sqlite     28K   ← Electron 主进程的 session/message 库
~/.config/darvin-cowork/config.yaml              336B  ← 用户级配置（LLM key / locale）
<仓库根>/sessions.db                              68K   ← Go agent 的库（相对 cwd 生成）
<仓库根>/bin/sessions.db                          68K   ← 直接跑二进制时留下的第二份残留
```

三个根因：

1. **大小写目录不一致** — `src/main/index.ts:41` 调 `app.setName('Darvin-Cowork')`，把 Electron 的
   `app.getPath('userData')` 顶到了 `~/.config/Darvin-Cowork/`（大写连字符）；而 Go 侧
   `os.UserConfigDir()` 永远拼小写的 `darvin-cowork`。`src/main/libs/user-settings.ts:29-36`
   已经踩过这个坑并特意改用 `app.getPath('appData')` 绕开，但 `SessionStore` 的路径没跟着改。

2. **Go 侧 DSN 是 cwd 相对路径** — `src/darvin-agent/config.yaml:6` 写死 `sessions_dsn: ./sessions.db`。
   子进程由 `src/main/runtime/manager.ts:108` 的 `spawn(bin, [], { ... })` 拉起，未传 `cwd`，
   于是继承 Electron main 的 cwd（开发态 = 仓库根），数据库就落在了源码目录里。
   `.gitignore:97` 的 `*.db` 兜住了没进版本库，但这是掩盖而不是解决。

3. **没有单一的路径解析出口** — 目录名 `'darvin-cowork'` 目前在 `user-settings.ts:36` 硬编码一份、
   Go 的 `config.go:118` 硬编码一份，第三处（数据库）干脆走了另一套。任何新增的持久化文件都会
   再次面临"该拼哪个目录"的问题。

实际影响：卸载/重装不干净、备份用户数据要跑三个地方、开发时 `git clean` 会误删数据库、
打包后 cwd 是不可写目录时 Go 侧会直接启动失败。

### 1.2 目标

把**所有用户级持久化文件收敛到各平台标准的单一目录**，Electron 与 Go 两侧对同一路径达成一致：

| 平台 | 目录 | 依据 |
|------|------|------|
| Windows | `C:\Users\<user>\AppData\Roaming\darvin-cowork\` | Known Folders `%APPDATA%`（Roaming，重装不删） |
| macOS | `~/Library/Application Support/darvin-cowork/` | macOS 沙盒规范 |
| Linux | `$XDG_CONFIG_HOME/darvin-cowork/`（默认 `~/.config/darvin-cowork/`） | XDG Base Directory |

目录内容：

```
<userConfigDir>/darvin-cowork/
├── config.yaml            # 用户级配置（已就位，不动）
├── darvin-cowork.sqlite   # Electron 主进程库（+ -wal / -shm）
└── sessions.db            # Go agent 库
```

关键点：Electron 的 `app.getPath('appData')` 与 Go 的 `os.UserConfigDir()` 在三个平台上
返回的**恰好是同一个目录**，所以两侧各自拼 `+ '/darvin-cowork'` 天然对齐，不需要跨进程传路径
（但仍会通过环境变量注入，见 §3.3 的取舍）。

### 1.3 非目标

- **不动 `app.setName('Darvin-Cowork')`**。改完后 `app.getPath('userData')`（`~/.config/Darvin-Cowork/`）
  只剩 Chromium 自己的 Cache / Cookies / GPUCache，那是 Electron 强绑的运行时缓存，与业务数据
  本就该分开，不在本次范围。
- **不做日志路径迁移**。`config.yaml:13` 的 `log.filename: ./logs/app.log` 同样是 cwd 相对路径，
  但当前 `log.output: stderr`，该字段未被使用，没有实际产出文件。留待后续（真要落盘应走
  `~/.cache` / `%LOCALAPPDATA%` / `~/Library/Caches` 这一层，与本次的 config 层不同）。
- **不做自动数据迁移**（见 §3.5 的决策与理由）。
- 不改数据库 schema、不改 SessionStore / GORM 的任何读写逻辑。

---

## 2. 现状分析

### 2.1 Electron 侧调用链

```
src/main/index.ts:52
  new SessionStore(defaultSessionStorePath(app.getPath('userData')))
                                            └─ ~/.config/Darvin-Cowork   ← 问题在这里
src/main/store/SessionStore.ts:301
  defaultSessionStorePath(dir) => path.join(dir, 'darvin-cowork.sqlite')
src/main/store/SessionStore.ts:112
  fs.mkdirSync(path.dirname(dbPath), { recursive: true })   ← 已有建目录，但没设 mode
```

对照组（已经正确的那条）：

```
src/main/libs/user-settings.ts:29-36
  userSettingsPath() => path.join(app.getPath('appData'), 'darvin-cowork', 'config.yaml')
                                  └─ ~/.config              └─ 小写，与 Go 对齐
```

### 2.2 Go 侧调用链

```
src/darvin-agent/config.yaml:6        sessions_dsn: ./sessions.db
src/darvin-agent/internal/config/config.go:60ish   Database.SessionsDSN mapstructure
src/darvin-agent/cmd/app/main.go:78-85
  dbCfg := &database.Config{ SessionsDSN: cfg.Database.SessionsDSN }
  database.Init(dbCfg)
src/darvin-agent/internal/database/sqlite.go:28
  gorm.Open(sqlite.Open(cfg.SessionsDSN), ...)   ← 直接把字符串喂给驱动，不做任何解析
```

`internal/database/sqlite.go:15` 的注释已经明说了 "relative to the Go agent's cwd"——
即当前行为是**有意识的**，只是这个意识过时了。

已有的正确参照：`internal/config/config.go:113` 的 `UserConfigPath()` 就是我们要复用的解析逻辑。

### 2.3 Electron → Go 的现有注入模式

`src/main/runtime/manager.ts:101-106` 已经有一套成熟的环境变量注入：

```ts
const env: NodeJS.ProcessEnv = { ...process.env, DARVIN_DEV: app.isPackaged ? '0' : '1' };
const cfg = resolveAgentConfigPath();
if (cfg) env.DARVIN_CONFIG = cfg;
```

Go 侧 `cmd/app/main.go:34-45` 的 `configPath()` 消费 `DARVIN_CONFIG`。本次可以照抄这个模式。

---

## 3. 方案设计

### 3.1 新增 Electron 侧共享路径模块

新建 `src/main/libs/user-paths.ts`，成为 Electron 侧**唯一**的用户目录出口：

```ts
/**
 * 用户级数据目录：与 Go 侧 config.UserConfigPath() 落在同一个目录。
 *
 * app.getPath('appData') 与 Go 的 os.UserConfigDir() 在三平台返回同一层：
 *   Windows %APPDATA% / macOS ~/Library/Application Support / Linux ~/.config
 * 不能用 getPath('userData') —— app.setName 会把它偏到 Darvin-Cowork/（大写）。
 */
export function userDataDir(): string {
  return path.join(app.getPath('appData'), 'darvin-cowork');
}

/** 用户级 yaml 配置的绝对路径。 */
export function userSettingsPath(): string {
  return path.join(userDataDir(), 'config.yaml');
}

/** 主进程 session / message 库的绝对路径。 */
export function sessionStorePath(): string {
  return path.join(userDataDir(), 'darvin-cowork.sqlite');
}

/** Go agent 库的绝对路径，通过 DARVIN_SESSIONS_DSN 注入子进程。 */
export function agentSessionsDsnPath(): string {
  return path.join(userDataDir(), 'sessions.db');
}
```

`user-settings.ts` 删掉本地的 `userSettingsPath()`，改 import 上面这个——目录名字符串
从此在 Electron 侧只存在一份。

### 3.2 Electron 侧接线

- `src/main/index.ts:52` → `new SessionStore(sessionStorePath())`
- `src/main/store/SessionStore.ts` 的 `defaultSessionStorePath()` **删除**（唯一调用点已改，
  留着就是第二个真相来源）。同时给 `mkdirSync` 补 `mode: 0o700`，与 config.yaml 的 `0o600` 对齐。

### 3.3 Go 侧 DSN 解析

在 `internal/config/config.go` 新增：

```go
// ResolveSessionsDSN 把 database.sessions_dsn 解析成绝对路径：
//   - 空值   → <UserConfigDir>/darvin-cowork/sessions.db（默认，与 config.yaml 同目录）
//   - 绝对路径 → 原样返回（Electron 通过 DARVIN_SESSIONS_DSN 注入的就是这种）
//   - 相对路径 → 相对 cwd 展开（保留给测试与显式覆盖）
// 返回前不建目录，由 caller 决定。
func ResolveSessionsDSN(dsn string) (string, error)
```

三分支语义的取舍：**空值走用户目录**而不是"相对路径也重定向到用户目录"——后者会让
`sessions_dsn: ./x.db` 这种写法产生反直觉的结果（字面写着相对 cwd，实际落在别处），
也会让单测没法用 `t.TempDir()`。

`config.yaml` 改为：

```yaml
database:
  # 空值 = 落在用户级目录（与 config.yaml 同目录）：
  #   Windows %APPDATA%\darvin-cowork\sessions.db
  #   macOS   ~/Library/Application Support/darvin-cowork/sessions.db
  #   Linux   ~/.config/darvin-cowork/sessions.db
  # 填绝对路径可覆盖；填相对路径则相对进程 cwd。
  sessions_dsn: ""
```

`cmd/app/main.go:78-85` 改为先 resolve 再建目录：

```go
dsn, err := config.ResolveSessionsDSN(cfg.Database.SessionsDSN)
if err != nil { /* log + exit 1 */ }
if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil { /* log + exit 1 */ }
dbCfg := &database.Config{SessionsDSN: dsn}
```

`internal/database/sqlite.go:14-15` 的 Config 注释同步更新（"relative to cwd" 已不再成立，
改为"绝对路径，由 caller 解析完毕"）。

环境变量覆盖走 viper 的既有机制：`config.Load` 里补一条
`viper.BindEnv("database.sessions_dsn", "DARVIN_SESSIONS_DSN")`，与现有的
`BindEnv("llm.api_key", "LLM_API_KEY")`（config.go:138）同款。

### 3.4 Electron 注入 DSN（防御性，非必需）

`manager.ts` 的 env 组装里加一行：

```ts
env.DARVIN_SESSIONS_DSN = agentSessionsDsnPath();
```

严格说 §3.3 的空值默认已经能让 Go 自己算出同一个路径，这里注入是**冗余的**。保留它的理由：
一旦将来 Electron 侧决定支持"数据目录自定义"，这是唯一的注入口；且它让
"Electron 决定数据放哪、Go 服从"这个所有权关系在代码里显式可见，而不是靠两边算法巧合一致。
代价是一行代码，接受。

### 3.5 数据迁移决策

**方案：不做自动迁移，提供手动迁移命令。**

现存数据是开发期测试数据（Electron 库 28K / Go 库 68K），项目处于原型阶段（CLAUDE.md：
"这是一个 Electron + Vue3 + Go 桌面应用原型"），尚无真实用户。自动迁移代码一旦写进
启动路径就是永久负担（要处理"新旧都存在"、"迁移中崩溃"、"wal 文件一致性"等分支），
收益与成本不成比例。

spec 在 §6 给出一次性手动迁移命令；若用户认为需要自动迁移，在确认环节调整本节即可。

---

## 4. 实施步骤

按依赖顺序，每步可独立验证：

1. **新建 `src/main/libs/user-paths.ts`**，`user-settings.ts` 改为复用（此时行为完全不变，
   因为 `userSettingsPath` 的实现是原样搬过去的）。跑 `npm run lint`。
2. **Electron 库改址**：`index.ts` 用 `sessionStorePath()`，删 `defaultSessionStorePath`，
   `SessionStore` 建目录补 `0o700`。
3. **Go 侧 `ResolveSessionsDSN` + 单测**：先加函数和测试，`go test ./internal/config/...` 通过。
4. **Go 侧接线**：`main.go` 调用 resolve + MkdirAll，`config.yaml` 置空并补注释，
   `sqlite.go` 更新 Config 注释，`config.go` 补 BindEnv。
5. **Electron 注入 `DARVIN_SESSIONS_DSN`**（manager.ts）。
6. **清理残留**：提示用户手动删 `<仓库根>/sessions.db` 与 `bin/sessions.db`（不由本次改动
   自动删除——删文件是破坏性操作）。

---

## 5. 涉及文件

| 文件 | 变更说明 |
|------|---------|
| `src/main/libs/user-paths.ts` | **新增**。`userDataDir` / `userSettingsPath` / `sessionStorePath` / `agentSessionsDsnPath` 四个导出 |
| `src/main/libs/user-settings.ts` | 删本地 `userSettingsPath()`，改 import `user-paths`；文件头 JSDoc 里的路径说明保留 |
| `src/main/index.ts` | L52 改为 `new SessionStore(sessionStorePath())`；补 import |
| `src/main/store/SessionStore.ts` | 删 `defaultSessionStorePath()` 及其 JSDoc；L112 `mkdirSync` 补 `mode: 0o700` |
| `src/main/runtime/manager.ts` | env 组装处注入 `DARVIN_SESSIONS_DSN` |
| `src/darvin-agent/internal/config/config.go` | 新增 `ResolveSessionsDSN()`；`Load` 里补 `BindEnv("database.sessions_dsn", "DARVIN_SESSIONS_DSN")` |
| `src/darvin-agent/internal/config/config_test.go` | 新增 `ResolveSessionsDSN` 的三分支测试；L152 现有 fixture 的 `sessions_dsn: ./sessions.db` 保持不变（验证相对路径分支仍生效） |
| `src/darvin-agent/cmd/app/main.go` | L78-85 改为 resolve + `os.MkdirAll(dir, 0o700)` 后再 `database.Init` |
| `src/darvin-agent/internal/database/sqlite.go` | L14-15 Config 注释更新（不再是 cwd 相对） |
| `src/darvin-agent/config.yaml` | `sessions_dsn` 置空 + 三平台路径说明注释 |

不涉及：任何 renderer / preload / shared 文件；数据库 schema；GORM 与 better-sqlite3 的读写逻辑。

---

## 6. 边界情况

| 场景 | 处理方式 |
|------|---------|
| 首次启动，目录不存在 | 两侧都 `MkdirAll`（Electron `0o700` / Go `0o700`），幂等 |
| 目录不可写（只读家目录 / 权限异常） | Electron：`SessionStore` 构造抛错 → main 启动失败，符合现状语义。Go：`MkdirAll` 失败 → 记 zap error + `os.Exit(1)`，Electron 的 `manager.start()` 读不到端口行，5s 后超时 reject，走 `index.ts` 的 catch，窗口照常开、badge 显示 offline |
| `os.UserConfigDir()` 失败（无 HOME 的最小化 Linux） | `ResolveSessionsDSN` 返回 error，main 记 error 退出。与 `config.go:154-157` 对 `UserConfigPath` 失败的处理不同（那里是非致命降级），因为数据库无路径可用时无法继续 |
| Windows 大小写不敏感 | `%APPDATA%\darvin-cowork` 与已有的任何大小写变体指向同一目录，不会产生第二份 |
| macOS 目录名含空格（`Application Support`） | Node 与 Go 的 `path.join` / `filepath.Join` 都正确处理；注入的环境变量值不经 shell，无需转义 |
| 用户手工在 `config.yaml` 填了绝对 `sessions_dsn` | 原样使用，`DARVIN_SESSIONS_DSN` 环境变量优先级更高（viper BindEnv 语义），Electron 注入会覆盖它 |
| 旧数据仍在旧位置 | 不自动迁移。新位置从空库开始，AutoMigrate / SCHEMA 各自建表 |
| WAL / SHM 副文件 | `darvin-cowork.sqlite-wal` / `-shm` 由 SQLite 在主库同目录自动管理，无需额外处理；手动迁移时需一并复制 |
| `go run` 独立跑（无 Electron） | `sessions_dsn` 为空 → 走 `UserConfigDir` 默认，与 Electron 拉起时是同一个库。这是有意的：本地调试和正式运行看到同一份数据 |

### 手动迁移命令（一次性，用户自行决定是否执行）

```bash
# Linux
DEST=~/.config/darvin-cowork
cp -v ~/.config/Darvin-Cowork/darvin-cowork.sqlite*  "$DEST"/ 2>/dev/null
cp -v <仓库根>/sessions.db                            "$DEST"/ 2>/dev/null
```

macOS 把 `~/.config` 换成 `~/Library/Application Support`；Windows 换成 `%APPDATA%`。
执行前先退出应用，避免 WAL 处于未 checkpoint 状态。

---

## 7. 验证计划

### 自动化

- [ ] `npm run lint` 通过（package.json 实际只有 `lint`，无 `check`）
- [ ] `cd src/darvin-agent && go build ./... && go vet ./...` 通过
- [ ] `cd src/darvin-agent && go test ./...` 全绿
- [ ] 新增的 `ResolveSessionsDSN` 三分支单测覆盖：空值 → `UserConfigDir` 下、
      绝对路径 → 原样、相对路径 → 相对 cwd

### 手动（必须，涉及 Electron 主进程 + 子进程 spawn）

- [ ] 删除/改名旧的三处数据文件后 `npm start`，确认
      `~/.config/darvin-cowork/` 下同时出现 `config.yaml`、`darvin-cowork.sqlite`、`sessions.db`
- [ ] 确认仓库根与 `bin/` **不再**生成 `sessions.db`
- [ ] 确认 `~/.config/Darvin-Cowork/` 下不再新增业务数据（只剩 Chromium 缓存）
- [ ] 发一条 prompt 走完整链路，重启应用后 sidebar 能读回该会话（证明两个库都写到了新位置且可读）
- [ ] 设置面板改 API key 触发 `restartGoSubprocess()`，确认子进程重启后仍用同一个 `sessions.db`
- [ ] `npm run smoke` 通过

### Given/When/Then

**场景 1：全新安装**
- **Given** 用户机器上不存在 `<userConfigDir>/darvin-cowork/`
- **When** 首次启动应用并发送一条消息
- **Then** 该目录被创建（权限 700），内含 `config.yaml`、`darvin-cowork.sqlite`、`sessions.db`；
  源码目录与 `bin/` 无任何 `.db` 文件

**场景 2：开发者在仓库根跑应用**
- **Given** 开发者的 cwd 是仓库根
- **When** `npm start`
- **Then** Go 子进程的库落在用户目录而非 cwd；`git status` 保持干净
