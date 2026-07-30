import { builtinModules } from 'node:module';
import { defineConfig } from 'vite';

// 主进程跑在 Node/Electron 里，node 内置模块必须 external。默认 vite 会把
// 它们替换成浏览器 shim（`__vite-browser-external`），命名导入直接构建失败，
// 默认导入更糟——静默产出一个调用即抛的代理对象。
const nodeBuiltins = [
  ...builtinModules,
  ...builtinModules.map((m) => `node:${m}`),
];

export default defineConfig({
  build: {
    outDir: '.vite/build/main',
    target: 'node20',
    lib: {
      entry: 'src/main/index.ts',
      formats: ['cjs'],
      fileName: () => 'index.js',
    },
    rollupOptions: {
      // ws 也保持 external：它对 bufferutil / utf-8-validate 做可选 require，
      // 打进 bundle 会让 rollup 解析失败。作为 dependencies 随 asar 分发。
      // better-sqlite3 也 external：它通过 `bindings()` 动态 require 编译产物
      // `.node` 二进制，rollup commonjs 插件不会分析这种 dynamic require，
      // 必须 external 让运行时直接走 node_modules 解析。
      external: ['electron', 'electron-squirrel-startup', 'ws', 'better-sqlite3', ...nodeBuiltins],
    },
  },
});
