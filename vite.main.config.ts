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
