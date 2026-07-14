# frontend/AGENTS.md — React 渲染层专属

## 技术栈

- React 18 + Vite 8 + TypeScript
- Tailwind CSS
- 未来：Redux Toolkit（如需全局状态）

## 命令

```bash
npm run dev              # vite dev server 5175
npm test                 # vitest run
npm run test:watch       # vitest
npm run build            # vite build，输出到 ../electron/dist-react
npm run lint             # oxlint
```

## 目录约定

```
frontend/src/
├── main.tsx             ← 入口（Vite 默认生成）
├── App.tsx              ← 顶层组件
├── pages/               ← 路由级页面
│   ├── Home.tsx
│   └── SessionDetail.tsx
├── components/          ← 跨页面复用组件
│   ├── PromptInput.tsx
│   └── MessageBubble.tsx
├── services/            ← 业务逻辑 + 外部接口封装
│   ├── cowork.ts        ← 后端 API 调用
│   └── i18n.ts          ← t() 工具
├── store/               ← Redux slices（如果引入）
├── locales/             ← i18n 字典
│   ├── zh.json
│   └── en.json
└── shared/              ← symlink 到 /shared
```

## 新增页面

1. 在 `pages/` 下建文件
2. 在 `App.tsx` 注册路由（如有 react-router）
3. 涉及后端 → 在 `services/cowork.ts` 加 API 方法
4. 涉及 IPC → 在 `shared/constants.ts` 加频道名常量

## 新增组件

- 跨多个页面复用 → `components/`
- 一次性内部用 → 与页面文件同目录

## i18n

**绝不**写硬编码字符串。必须：

```tsx
import { t } from '@/services/i18n';
return <button>{t('action.submit')}</button>;
```

新增 key 时**同时**编辑 `zh.json` 和 `en.json`，缺一个不能 commit。

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

## 不要做

- 不要把业务逻辑塞进组件，全部走 `services/`
- 不要在组件里直接 fetch，全部走 `services/`
- 不要在 `pages/` 里建子目录（直接平铺）
- 不要改 `vite.config.ts` 的 `base: './'`（保证打包后路径正确）
