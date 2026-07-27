import { defineConfig } from 'vite';

export default defineConfig({
  root: 'src/renderer',
  base: './', // 生产环境相对路径，避免 file:// 加载失败
  build: {
    outDir: '.vite/renderer',
  },
});
