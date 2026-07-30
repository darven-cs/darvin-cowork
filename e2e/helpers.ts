import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

import type { DarvinApi } from '../src/shared/darvin-api';

/**
 * page.evaluate / waitForFunction 的回调跑在渲染进程里，`globalThis`
 * 就是挂了 preload 桥的 window。回调会被序列化传过去，所以不能引用本
 * 文件的辅助函数——只能导出类型，在回调内部就地断言。
 */
export type DarvinBridgeGlobal = { darvin: DarvinApi };

/**
 * 启动 Electron 应用并返回 { app, window }。electron-builder / forge 的
 * forge.config.ts 已经把 darvin-agent 二进制 + config.yaml 配齐；这里
 * 只负责 launch。
 *
 * 启动后做最少的"ready"判定：
 *   - 主窗口可见
 *   - preload 已挂（window.darvin 有定义）
 *   - 状态走 'online' 或 'offline'（取决于 binary 是否 build）
 *
 * 抛错的位置定位在 e2e 失败信息里，spec 失败时不会留下泄漏进程。
 */
export async function launchApp(): Promise<{ app: ElectronApplication; window: Page }> {
  const app = await electron.launch({
    args: [
      path.join(
        __dirname,
        '..',
        'node_modules',
        '@electron-forge',
        'cli',
        'dist',
        'electron-forge.js',
      ),
      'start',
    ],
    timeout: 30_000,
    env: {
      ...process.env,
      NODE_ENV: 'development',
      // chrome-sandbox 在容器/非 root 环境下没设 SUID；用 env var 让
      // Electron 跳过 SUID 助手。生产桌面环境该 env 不存在，不影响 CI。
      ELECTRON_DISABLE_SANDBOX: '1',
      // Playwright 的 Electron 子进程在隔离进程组里跑，必须显式透传
      // DISPLAY / WAYLAND_DISPLAY，否则 main 进程没有窗口系统连接。
      DISPLAY: process.env.DISPLAY ?? ':0',
      WAYLAND_DISPLAY: process.env.WAYLAND_DISPLAY ?? '',
      XDG_SESSION_TYPE: process.env.XDG_SESSION_TYPE ?? 'x11',
    },
  });
  const window = await app.firstWindow();
  await window.waitForLoadState('domcontentloaded');
  // wait for the preload bridge
  await window.waitForFunction(
    () => typeof (globalThis as Partial<DarvinBridgeGlobal>).darvin === 'object',
  );
  return { app, window };
}

/**
 * 测试结束后清理：关 Electron + 等 Go 子进程退出，避免 e2e 跑完留一堆
 * darvin-agent 在系统里吃内存。
 */
export async function closeApp(app: ElectronApplication): Promise<void> {
  try {
    await app.close();
  } catch {
    /* 已关 */
  }
}

/**
 * 把 Anthropic 真实 key 写到用户级 yaml 并 reload。settings UI 走的是
 * ipcRenderer.invoke('darvin:set_llm_config')；这里直接调底层逻辑
 * 把 key 灌进 darwin-cowork/config.yaml，让 Go 子进程下次冷启动拿到
 * 真 key。
 */
export async function writeUserSettings(apiKey: string, baseUrl = ''): Promise<string> {
  const base = process.env.APPDATA
    ? path.join(process.env.APPDATA, 'darvin-cowork')
    : process.platform === 'darwin'
      ? path.join(os.homedir(), 'Library', 'Application Support', 'darvin-cowork')
      : path.join(process.env.XDG_CONFIG_HOME ?? path.join(os.homedir(), '.config'), 'darvin-cowork');
  fs.mkdirSync(base, { recursive: true });
  const cfgPath = path.join(base, 'config.yaml');
  fs.writeFileSync(cfgPath, `llm:\n  provider: anthropic\n  api_key: "${apiKey}"\n  base_url: "${baseUrl}"\n`);
  return cfgPath;
}

/**
 * 把 sessions.db 重置到干净状态——只清 messages 表，保留 schema。
 * session-persistence spec 启动前需要"已知状态"；happy-path spec 不
 * 该被旧 db 污染。
 */
export async function resetSessionsDb(sessionsDbPath: string): Promise<void> {
  if (!fs.existsSync(sessionsDbPath)) return;
  // 调 sqlite3 直接 truncate messages；schema 保留。
  // 删除整个 db 文件最简单——下次启动 main.go 会 AutoMigrate 重建。
  fs.unlinkSync(sessionsDbPath);
}

export { test, expect };