import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  root: 'src/renderer',
  base: './', // 生产环境相对路径，避免 file:// 加载失败
  build: {
    outDir: '.vite/renderer',
  },
  plugins: [vue(), tailwindcss()],
});
