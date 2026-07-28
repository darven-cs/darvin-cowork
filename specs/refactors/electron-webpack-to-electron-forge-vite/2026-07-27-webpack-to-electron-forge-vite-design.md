# Webpack → electron-forge + vite 迁移设计文档

## 1. 概述

### 1.1 问题 / 背景
- 当前项目使用 `electron-forge + webpack` 构建，打包速度慢
- TypeScript 版本老旧 (~4.5.4)，`baseUrl` 已 deprecated
- 需要迁移到更现代的 `vite` 构建，保留 electron-forge 的打包分发能力
- 架构上预留了 Golang Agent 运行时（`docs/系统架构.md`），需要在仓库里建立对应的构建与分发链路

### 1.2 目标
- 替换构建工具：webpack → vite (`@electron-forge/plugin-vite`)
- 目录重组：适配 vite 标准结构 (`src/main`, `src/preload`, `src/renderer`)
- TypeScript 升级到 ^5.7.3；tsconfig 拆分为 node / web 两份以适应 main vs renderer 的 module 差异
- 保留 electron-forge 打包分发生态（makers / fuses）
- 修复 `baseUrl` deprecated 警告（彻底移除，不靠 `ignoreDeprecations` 掩盖）
- 新增 `src/darvin-agent/` Go 源码 + `bin/` 编译产物落点 + `scripts/build-go.js` 构建脚本
- 新增主进程对 Go agent 的子进程管理（`src/main/runtime/manager.ts` + `client.ts`）
- 删除 `AutoUnpackNativesPlugin`（当前无 native 模块依赖）

### 1.3 非目标
- 不改业务逻辑
- 不迁移到其他前端框架（Vue/React）
- 不使用 electron-vite（保持 electron-forge 主框架）
- 不实现 Go agent 内部业务逻辑（只搭目录、构建脚本、桥接框架）
- 不引入 native 模块（sqlite/keytar 等）及其 asar 解包处理（未来要加则单独评审）

---

## 2. 目录结构变更

### 当前结构
```
src/
├── index.ts        # main process
├── preload.ts      # preload
├── renderer.ts     # renderer entry
├── index.html      # renderer html
└── index.css       # renderer styles
```

### 目标结构
```
src/
├── main/                       # main process (原 index.ts, 需重写)
│   ├── index.ts
│   └── runtime/                # Go agent 子进程桥接层（新增）
│       ├── manager.ts
│       └── client.ts
├── preload/                    # preload (原 preload.ts)
│   └── index.ts
├── renderer/                   # renderer (原 renderer.ts + index.html + index.css)
│   ├── index.ts
│   ├── index.html              # 需新增 <script type="module" src="./index.ts">
│   └── index.css
└── darvin-agent/               # Golang Agent 源码 (新增)
    ├── go.mod
    ├── main.go
    └── ...
bin/                            # Go 编译产物落点 (新增, 仅保留 .gitkeep)
scripts/
└── build-go.js                 # Go 跨平台编译脚本 (新增)
vite.main.config.ts             # 新增
vite.preload.config.ts          # 新增
vite.renderer.config.ts         # 新增
tsconfig.json                   # 改为公共基础配置
tsconfig.node.json              # 新增, main/preload 用
tsconfig.web.json               # 新增, renderer 用
```

---

## 3. 涉及文件变更

| 类别 | 文件 | 动作 |
|------|------|------|
| **删除** | `webpack.main.config.ts` | 删除 |
| | `webpack.renderer.config.ts` | 删除 |
| | `webpack.plugins.ts` | 删除 |
| | `webpack.rules.ts` | 删除 |
| **重写** | `forge.config.ts` | 改用 VitePlugin，移除 WebpackPlugin 与 AutoUnpackNativesPlugin |
| **重写** | `tsconfig.json` | 改为公共基础配置（移出 module/include），删除 baseUrl |
| | `tsconfig.node.json` | 新增，main/preload 用，module: CommonJS |
| | `tsconfig.web.json` | 新增，renderer 用，module: ESNext, lib: DOM |
| | `vite.main.config.ts` | 新增 |
| | `vite.preload.config.ts` | 新增 |
| | `vite.renderer.config.ts` | 新增 |
| **修改** | `package.json` | 切依赖（见 4.1）、改 scripts（见 4.2）、改 `"main"` 字段 |
| | `src/main/index.ts` | 重写，把 `MAIN_WINDOW_WEBPACK_ENTRY` 等 magic 常量换成 VITE 版本（见 4.8） |
| | `src/preload.ts` → `src/preload/index.ts` | 移动 |
| | `src/renderer.ts` → `src/renderer/index.ts` | 移动 |
| | `src/index.html` → `src/renderer/index.html` | 移动 + 新增 `<script type="module" src="./index.ts">`（见 4.9） |
| | `src/index.css` → `src/renderer/index.css` | 移动 |
| **新增** | `src/main/runtime/manager.ts` | Go agent 子进程管理器 |
| | `src/main/runtime/client.ts` | 与 Go agent 的 IPC 客户端 |
| | `src/darvin-agent/` | Go 源码（go.mod / main.go / ...） |
| | `bin/.gitkeep` | Go 编译产物占位 |
| | `scripts/build-go.js` | 跨平台 Go 编译脚本（见 4.7） |
| **保留** | `@electron-forge/maker-squirrel` / `-zip` / `-deb` / `-rpm` | 不变 |
| **保留** | `@electron-forge/plugin-fuses` | 不变（已在原配置中） |

---

## 4. 实现方案

### 4.1 依赖变更

**删除 devDependencies:**
- `@electron-forge/plugin-webpack`
- `@vercel/webpack-asset-relocator-loader`
- `@electron-forge/plugin-auto-unpack-natives`（当前无 native 模块依赖，无需解包）
- `css-loader`
- `fork-ts-checker-webpack-plugin`
- `node-loader`
- `style-loader`
- `ts-loader`

**新增 devDependencies:**
- `@electron-forge/plugin-vite` (^7.4.0)
- `vite` (^5.4.0)

**升级 devDependencies:**
- `typescript`: ~4.5.4 → ^5.7.3
- `@typescript-eslint/eslint-plugin`: ^5.62.0 → ^7.18.0（兼容 TS 5）
- `@typescript-eslint/parser`: ^5.62.0 → ^7.18.0

### 4.2 package.json 变更

**`main` 字段**（指向 vite 产物的入口）：
```json
"main": ".vite/build/main/index.js"
```

**scripts**：
```json
{
  "scripts": {
    "start": "electron-forge start",
    "build:agent": "node scripts/build-go.js",
    "premake": "npm run build:agent",
    "make": "electron-forge make",
    "package": "electron-forge package",
    "publish": "electron-forge publish",
    "lint": "eslint --ext .ts,.tsx ."
  }
}
```

> 注：`premake` 在 `make` 前自动触发（npm lifecycle），保证 `npm run make` 时二进制已就绪。`start` / `package` 不依赖 Go binary，主进程对 Go agent 调用失败时需降级（见 4.7）。

### 4.3 forge.config.ts 重写

```ts
import type { ForgeConfig } from '@electron-forge/shared-types';
import { MakerSquirrel } from '@electron-forge/maker-squirrel';
import { MakerZIP } from '@electron-forge/maker-zip';
import { MakerDeb } from '@electron-forge/maker-deb';
import { MakerRpm } from '@electron-forge/maker-rpm';
import { VitePlugin } from '@electron-forge/plugin-vite';
import { FusesPlugin } from '@electron-forge/plugin-fuses';
import { FuseV1Options, FuseVersion } from '@electron/fuses';

const config: ForgeConfig = {
  packagerConfig: {
    asar: true,
    // Go agent 二进制随包分发，置于 resources/bin/，不进 asar（spawn 需要可执行权限）
    extraResources: [
      {
        from: 'bin',
        to: 'bin',
        filter: (filePath) => {
          // 仅打包当前平台的二进制，避免 dev 机器把 darwin+linux 全打进去
          const { platform, arch } = process;
          const suffix =
            platform === 'win32' ? '.exe' : '';
          const name = `darvin-agent-${platform}-${arch}${suffix}`;
          return filePath.endsWith(name) || filePath.endsWith('.gitkeep');
        },
      },
    ],
  },
  rebuildConfig: {},
  makers: [
    new MakerSquirrel({}),
    new MakerZIP({}, ['darwin']),
    new MakerRpm({}),
    new MakerDeb({}),
  ],
  plugins: [
    new VitePlugin({
      build: [
        {
          entry: 'src/main/index.ts',
          config: 'vite.main.config.ts',
        },
        {
          entry: 'src/preload/index.ts',
          config: 'vite.preload.config.ts',
        },
      ],
      renderer: [
        {
          name: 'main_window',
          config: 'vite.renderer.config.ts',
        },
      ],
    }),
    new FusesPlugin({
      version: FuseVersion.V1,
      [FuseV1Options.RunAsNode]: false,
      [FuseV1Options.EnableCookieEncryption]: true,
      [FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
      [FuseV1Options.EnableNodeCliInspectArguments]: false,
      [FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
      [FuseV1Options.OnlyLoadAppFromAsar]: true,
    }),
  ],
};

export default config;
```

> 注意：`extraResources` 写在 `packagerConfig` 内，不影响 `rebuildConfig`。`AutoUnpackNativesPlugin` 已移除（当前无 native 模块依赖）。

### 4.4 vite.*.config.ts

`outDir` 路径与 `@electron-forge/plugin-vite` 默认值保持一致，产物体现在 `.vite/build/main/`、`.vite/build/preload/`、`.vite/renderer/`，与 `package.json` 的 `"main"` 字段对齐。

**vite.main.config.ts:**
```ts
import { defineConfig } from 'vite';

export default defineConfig({
  build: {
    outDir: '.vite/build/main',
    lib: {
      entry: 'src/main/index.ts',
      formats: ['cjs'],
      fileName: () => 'index.js',
    },
    rollupOptions: {
      external: ['electron', 'electron-squirrel-startup'],
    },
  },
});
```

**vite.preload.config.ts:**
```ts
import { defineConfig } from 'vite';

export default defineConfig({
  build: {
    outDir: '.vite/build/preload',
    lib: {
      entry: 'src/preload/index.ts',
      formats: ['cjs'],
      fileName: () => 'index.js',
    },
    rollupOptions: {
      external: ['electron'],
    },
  },
});
```

**vite.renderer.config.ts:**
```ts
import { defineConfig } from 'vite';

export default defineConfig({
  root: 'src/renderer',
  base: './',  // 生产环境相对路径，避免 file:// 加载失败
  build: {
    outDir: '.vite/renderer',
  },
});
```

### 4.5 tsconfig 拆分（解决 main/preload 与 renderer 的 module 冲突）

main/preload 在 Electron 主进程跑 CJS，renderer 在浏览器跑 ESM，单 tsconfig 无法两边都准。拆为三份。

**tsconfig.json（公共基础，仅放共享配置）:**
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "esModuleInterop": true,
    "skipLibCheck": true,
    "resolveJsonModule": true,
    "sourceMap": true,
    "strict": true,
    "noImplicitAny": true,
    "forceConsistentCasingInFileNames": true,
    "isolatedModules": true
  }
}
```

**tsconfig.node.json（main / preload）:**
```json
{
  "extends": "./tsconfig.json",
  "compilerOptions": {
    "module": "CommonJS",
    "moduleResolution": "node",
    "types": ["node"]
  },
  "include": ["src/main/**/*", "src/preload/**/*"]
}
```

**tsconfig.web.json（renderer）:**
```json
{
  "extends": "./tsconfig.json",
  "compilerOptions": {
    "module": "ESNext",
    "moduleResolution": "bundler",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "types": []
  },
  "include": ["src/renderer/**/*"]
}
```

> 弃用 `baseUrl` / `paths: { "*": ["node_modules/*"] }`（原本就是默认行为，TS 5 警告其 deprecated）。不引入 `ignoreDeprecations`，把问题彻底根除。IDE 与 CI 都指向 `tsconfig.node.json`，renderer 走 `tsconfig.web.json`。

### 4.6 src 目录重组

```
src/
├── main/
│   ├── index.ts         # 移动自 src/index.ts (内容重写, 见 4.8)
│   └── runtime/         # Go agent 桥接层 (新增)
│       ├── manager.ts
│       └── client.ts
├── preload/
│   └── index.ts         # 移动自 src/preload.ts
├── renderer/
│   ├── index.ts         # 移动自 src/renderer.ts
│   ├── index.html       # 移动自 src/index.html (内容改造, 见 4.9)
│   └── index.css        # 移动自 src/index.css
└── darvin-agent/        # Go 源码 (新增)
    ├── go.mod
    └── main.go
```

### 4.7 darvin-agent 集成

**布局**：
- Go 源码放 `src/darvin-agent/`（与 electron TS 同仓库，便于 CI 一处管理）
- 编译产物落 `bin/`（项目根，已在 `.gitignore`；仅 `bin/.gitkeep` 入库）

**go.mod 示例**：
```
module github.com/darven-cowork/agent

go 1.22
```

**scripts/build-go.js**：跨平台 `go build`，输出命名规则 `darvin-agent-${platform}-${arch}${suffix}`：
```js
const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const platform = process.platform; // 'darwin' | 'linux' | 'win32'
const arch = process.arch;          // 'arm64' | 'x64'
const exeSuffix = platform === 'win32' ? '.exe' : '';
const outName = `darvin-agent-${platform}-${arch}${exeSuffix}`;
const outPath = path.join(__dirname, '..', 'bin', outName);

const agentDir = path.join(__dirname, '..', 'src', 'darvin-agent');
const env = { ...process.env, CGO_ENABLED: '0', GOOS: platform, GOARCH: arch };

fs.mkdirSync(path.dirname(outPath), { recursive: true });
execSync(`go build -ldflags="-s -w" -o "${outPath}" .`, { cwd: agentDir, env, stdio: 'inherit' });
console.log(`✓ built ${outPath}`);
```

**分发集成**（已在 `forge.config.ts` `extraResources` 里）：
- `extraResources: bin/ → resources/bin/`，spawn 必须拿到可执行权限，**不能进 asar**
- `filter` 只打当前平台二进制，dev 机器不会塞全平台

**main 进程启动位置解析**（`src/main/runtime/manager.ts` 草案）：
```ts
import { app } from 'electron';
import path from 'node:path';

export function resolveAgentBinaryPath(): string {
  const { platform, arch } = process;
  const exeSuffix = platform === 'win32' ? '.exe' : '';
  const name = `darvin-agent-${platform}-${arch}${exeSuffix}`;
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'bin', name);
  }
  return path.join(__dirname, '..', '..', '..', 'bin', name); // dev: <repo>/bin/
}
```

> dev 模式 `__dirname` 在 `.vite/build/main/`，相对路径回溯到仓库根后取 `bin/`。
> `start` / `package` 不强制要求 binary 存在；若未编译，runtime manager 应降级并打 warning，不阻塞 Electron 启动。

### 4.8 main 进程改造

原 `src/index.ts:5-6` 用的是 webpack 插件注入的 magic 常量（`MAIN_WINDOW_WEBPACK_ENTRY` / `MAIN_WINDOW_PRELOAD_WEBPACK_ENTRY`）。Vite 插件注入的是 `MAIN_WINDOW_VITE_DEV_SERVER_URL`（dev）与 `MAIN_WINDOW_RENDERER_FILE`（prod, 不带 `.html` 后缀）。

新 `src/main/index.ts` 关键片段：
```ts
import { app, BrowserWindow } from 'electron';
import path from 'node:path';
import { resolveAgentBinaryPath } from './runtime/manager';

// 由 @electron-forge/plugin-vite 在编译期注入，无需手写 types
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string; // 不带后缀, 如 'main_window'

if (require('electron-squirrel-startup')) app.quit();

const createWindow = (): void => {
  const mainWindow = new BrowserWindow({
    height: 600,
    width: 800,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'), // vite preload 产物
    },
  });

  if (process.env.NODE_ENV !== 'production') {
    mainWindow.loadURL(MAIN_WINDOW_VITE_DEV_SERVER_URL!);
  } else {
    mainWindow.loadFile(
      path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}/index.html`),
    );
  }
};
// ... windows-all-closed / activate 不变
```

> `@electron-forge/plugin-vite` 会在编译期把 `MAIN_WINDOW_VITE_DEV_SERVER_URL` / `MAIN_WINDOW_VITE_NAME` 注入到 main 进程的 d.ts，无需手写 types。`resolveAgentBinaryPath` 暂未调用，桥接代码落定后接入 `app.whenReady()`。

### 4.9 HTML 改造

原 `src/index.html` 只有骨架，没有 `<script>` —— webpack 通过 `entryPoints` 自动注入。Vite 不会自动注入，必须显式声明。

`src/renderer/index.html`：
```html
<!doctype html>
<html>
  <head>
    <meta charset="UTF-8" />
    <title>Darvin Cowork</title>
  </head>
  <body>
    <h1>💖 Hello World!</h1>
    <p>Welcome to your Electron application.</p>
    <script type="module" src="./index.ts"></script>
  </body>
</html>
```

> 不写 `type="module"` 在 vite dev 模式下浏览器会按 ESM 解析失败。vite.renderer.config 设了 `root: 'src/renderer'`，`./index.ts` 是相对 root 的解析路径。

---

## 5. 验收标准

构建与启动
- [ ] `npm run start` 冷启动 ≤ 3s（webpack 时代基线 ~10s）
- [ ] main / preload / renderer 三进程在 DevTools 中均无 error / warning（除 vite 已知的 hmr 提示外）
- [ ] `npm run package` 产物双击启动正常，preload 路径解析无 ENOENT

TS / 配置
- [ ] `npx tsc -p tsconfig.node.json --noEmit` 零错误
- [ ] `npx tsc -p tsconfig.web.json --noEmit` 零错误
- [ ] 无 `baseUrl` deprecated 警告（彻底移除，不依赖 `ignoreDeprecations`）
- [ ] `package.json` `"main"` 字段指向 `.vite/build/main/index.js`

Go 集成
- [ ] `npm run build:agent` 在当前平台产出 `bin/darvin-agent-${platform}-${arch}${suffix}`
- [ ] `npm run make` 前自动触发 `build:agent`（`premake` 钩子）
- [ ] 安装包 `resources/bin/` 下存在本平台二进制（其他平台被 `extraResources` filter 排除）

清理
- [ ] 删除 `webpack.*.ts` 四件套
- [ ] 旧 `.webpack/` 缓存目录不存在（一次性 `rm -rf .webpack`）

---

## 6. 风险点

| 风险 | 缓解 |
|------|------|
| `vite.renderer.config.ts` 的 `root: 'src/renderer'` 下，HTML 引用的 `./index.ts` 解析是否生效 | 已在 4.9 显式声明 `<script type="module" src="./index.ts">`；DevTools 看不到加载失败即可 |
| `electron-squirrel-startup` 包名/行为变化 | 已加入 main external 列表；如未来移除需同步外部 |
| Go 二进制跨平台构建需要本机具备各平台 Go toolchain | CI（GitHub Actions matrix）补齐，本机 darwin+linux 可用，windows 需 CI 兜底 |
| 生产包 asar 模式下 spawn Go 二进制失败（asar 内文件不可执行） | 已用 `extraResources` 替代 asar 内打包，详见 4.7 |
| main 进程 `path.join(__dirname, '../preload/index.js')` 相对路径在 dev / pack 后结构可能错位 | vite 插件统一产物布局为 `.vite/build/{main,preload}/`，相对关系稳定 |
| `--ignore-deprecation` 类字段在新 TS 版本移除 | 已改用彻底移除 `baseUrl`，不依赖 `ignoreDeprecations` |
| `@typescript-eslint/*` ^5 与 TS ^5.7 规则不兼容 | 已升级到 ^7.18 |
| 未来引入 native 模块时缺少 AutoUnpackNativesPlugin | 当前无 native 依赖；若后续加入 sqlite/keytar 等，需重新引入或手工处理 asar 解包 |
| `process.env.NODE_ENV` 在 vite 中表现与 webpack 不同（dev 默认 `development`，prod 默认 `production`） | 4.8 用 `process.env.NODE_ENV !== 'production'` 显式判断，不依赖环境变量具体值 |
| darvin-agent Go 进程崩溃导致 Electron 启动失败 | 4.7 的 manager 草案要求 binary 缺失时降级警告，不阻塞 |

---

## 7. 回滚方案

**事前准备**（迁移动手前必做）
1. 在 clean baseline 上打 tag：`git tag forge-vite-migration-start -m "pre-vite baseline"`
2. 旧文件改名留底（不要直接删）：
   - `forge.config.ts` → `forge.config.ts.webpack.bak`
   - `tsconfig.json` → `tsconfig.json.webpack.bak`
   - `webpack.*.ts` 移入 `.bak/` 目录

   等 vite 路线稳定跑通一周后再删除这些 `.bak` 产物。

**短期回滚**（迁移当天发现致命阻塞）
- `git revert -m 1 <merge-commit>` 回退 PR（推荐，不丢新提交）
- 或 `git reset --hard forge-vite-migration-start`（仅本地分支，慎用，会丢未推送提交）
- 旧 `package-lock.json` 已保留，对比可还原依赖快照
