import { defineConfig, externalizeDepsPlugin } from 'electron-vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'node:path';

export default defineConfig({
  main: {
    build: {
      lib: { entry: resolve(__dirname, 'main/index.ts') },
    },
    plugins: [externalizeDepsPlugin()],
  },
  preload: {
    build: {
      lib: { entry: resolve(__dirname, 'preload/index.ts') },
    },
    plugins: [externalizeDepsPlugin()],
  },
  renderer: {
    base: './',
    build: {
      rollupOptions: {
        input: resolve(__dirname, 'renderer/index.html'),
      },
    },
    plugins: [react()],
    root: resolve(__dirname, 'renderer'),
    server: {
      proxy: {
        '/api': {
          changeOrigin: true,
          target: 'http://127.0.0.1:8080',
        },
      },
    },
  },
});
