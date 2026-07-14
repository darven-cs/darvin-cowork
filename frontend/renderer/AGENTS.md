# frontend/renderer/AGENTS.md — React 渲染层专属

## 技术栈

- React 19 + Vite 8 + TypeScript
- Tailwind CSS
- Zustand（v0.1 第 2-3 周引入全局状态）

## 命令

所有命令在根 `package.json`，**不在** renderer/ 单独跑：

```bash
npm test           # vitest run（config: frontend/renderer/vitest.config.ts）
npm run test:watch # vitest watch
npm run lint       # oxlint 全量
npm run dev        # electron-vite dev frontend（renderer 走 Vite HMR）
npm run build      # electron-vite build frontend（产物在 frontend/out/renderer/）
```

> 构建由 electron-vite 统一处理（配置在 `frontend/electron.vite.config.ts` 的 `renderer` 段），renderer 段不再单独建 `vite.config.ts`。

## 目录约定

```
frontend/renderer/src/
├── main.tsx             ← 入口（Vite 默认生成）
├── App.tsx              ← 顶层组件
├── pages/               ← 路由级页面
├── components/          ← 跨页面复用组件
├── services/            ← 业务逻辑 + 外部接口封装
│   └── i18n.ts          ← t() 工具
├── api/                 ← 后端 HTTP 调用
│   ├── hello.ts
│   ├── hello.test.ts
│   └── request.ts
├── store/               ← Zustand 入口（v0.1 第 2-3 周填）
├── utils/               ← 工具函数
├── types/
│   └── global.d.ts      ← window.api 类型声明（import type { DarvinApi })
├── locales/             ← i18n 字典
│   ├── zh.json
│   └── en.json
├── assets/              ← 静态资源（图片 / 字体）
└── shared/              ← symlink → /shared
```

## 新增页面

1. 在 `frontend/renderer/src/pages/` 下建文件
2. 在 `App.tsx` 注册路由（如有 react-router）
3. 涉及后端 → 在 `frontend/renderer/src/api/` 或 `services/` 加方法
4. 涉及 IPC → 走四步流程（见 `frontend/main/AGENTS.md`）

## 新增组件

- 跨多个页面复用 → `components/`
- 一次性内部用 → 与页面文件同目录

## i18n

**绝不**写硬编码字符串。必须：

```tsx
import { t } from '@/services/i18n';
return <button>{t('action.submit')}</button>;
```

新增 key 时**同时**编辑 `frontend/renderer/src/locales/zh.json` 和 `en.json`，缺一个不能 commit。

## 测试

- 工具型组件（纯 props in / JSX out）→ 直接写 Vitest
- 涉及 hook / fetch → 用 `vi.mock()` mock 外部依赖
- 文件命名：`*.test.tsx` 与源文件同目录
- jsdom 默认环境已经够用，不要引 @testing-library 除非真要

## 样式

- 优先 Tailwind utility class
- 复杂重复样式抽到 `components/ui/` 或本地 className
- 不用内联 style 除非真的只能用一次
- 颜色 / 间距用 Tailwind token，不要硬编码 hex

## 跨进程 API

渲染层不能直接 `import 'electron'`，只能用 preload 暴露的 `window.api`：

```ts
// 正确
const result = await window.api.sendMessage('hello');

// 错误（contextIsolation 会拦）
import { ipcRenderer } from 'electron'; // ❌
```

`window.api` 的类型由 `types/global.d.ts` 通过 `import type { DarvinApi }` 注入。

## 不要做

- 不要把业务逻辑塞进组件，全部走 `services/`
- 不要在组件里直接 fetch，全部走 `api/` 或 `services/`
- 不要在 `pages/` 里建子目录（直接平铺）
- 不要直接 `import 'electron'`（contextIsolation 会拦，要走 `window.api`）
- 不要在 renderer 下建 `vite.config.ts`（由 `frontend/electron.vite.config.ts` 统一管）
